package server

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security"
)

// osDecision is the pure outcome of the output-sanitisation decision core
// (Spec 054 Track B). It declares WHICH actions apply to a response; the
// caller (applyOutputSanitisation) performs the actual mutation and logging.
//
//   - block:     replace the whole payload with a remediation error (FR-B7).
//   - redact:    mask detected secret spans (FR-B3).
//   - strip:     neutralise control sequences on untrusted text (FR-B4).
//   - spotlight: wrap untrusted text in source-identifying delimiters (FR-B1).
type osDecision struct {
	block     bool
	redact    bool
	strip     bool
	spotlight bool
	reason    string // populated for the block path
}

// evaluateOutputSanitisation is the pure decision core. It performs no I/O and
// no scanning so it is fully unit-testable. `criticalDetected` is supplied by
// the caller (only meaningful for block mode). A nil config is treated as the
// safe default (spotlight untrusted, no mutation) per FR-B6/FR-X1.
func evaluateOutputSanitisation(cfg *config.OutputSanitisationConfig, trust string, criticalDetected bool) osDecision {
	if cfg == nil {
		cfg = config.DefaultOutputSanitisationConfig()
	}
	untrusted := trust == contracts.ContentTrustUntrusted

	if cfg.IsBlock() {
		if criticalDetected {
			return osDecision{block: true, reason: "critical sensitive data detected in tool output"}
		}
		// Block mode but nothing critical: still spotlight untrusted output.
		return osDecision{spotlight: untrusted && cfg.IsSpotlightEnabled()}
	}

	return osDecision{
		redact:    cfg.IsRedact(),
		strip:     untrusted && cfg.IsStripEnabled(),
		spotlight: untrusted && cfg.IsSpotlightEnabled(),
	}
}

// applyOutputSanitisation enforces the MUTATING, cacheable part of Spec 054
// Track B — block, redact, and control-sequence stripping — against the RAW
// upstream result BEFORE forwardContentResult truncates and caches it. Doing it
// here (rather than on the post-truncation result) means the read_cache store
// never holds an unredacted secret, and a blocked response is never cached at
// all: a non-nil return tells the caller to short-circuit before forwarding.
//
// It mutates the result's TextContent blocks in place (redact -> strip); the
// lossless spotlight wrapper is applied separately by spotlightForwarded after
// truncation, since it is a presentation frame that must not be cached.
// Non-text blocks (image/audio/embedded) are never touched (FR-B5).
//
// `result` is the upstream value (interface{}) handed to forwardContentResult;
// when it is not a *mcp.CallToolResult (the legacy JSON-wrap path) sanitisation
// is a no-op. The fast path returns immediately for the common opt-out case.
func (p *MCPProxyServer) applyOutputSanitisation(ctx context.Context, serverName, toolName, contentTrust string, result interface{}) *mcp.CallToolResult {
	cfg := p.config.OutputSanitisation
	if cfg == nil {
		cfg = config.DefaultOutputSanitisationConfig()
	}
	untrusted := contentTrust == contracts.ContentTrustUntrusted
	stripActive := untrusted && cfg.IsStripEnabled()
	// Fast path: spotlight is handled post-forward, so only block/redact/strip
	// are relevant here. Nothing else mutates the cacheable payload.
	if !cfg.IsBlock() && !cfg.IsRedact() && !stripActive {
		return nil
	}
	ctr, ok := result.(*mcp.CallToolResult)
	if !ok || ctr == nil || ctr.IsError {
		return nil
	}

	// Block mode evaluates BEFORE any mutation/caching so no critical bytes are
	// ever forwarded or persisted to the cache (research D4).
	criticalDetected := false
	if cfg.IsBlock() && p.sanitisationDetector != nil {
		criticalDetected = hasCriticalDetection(p.sanitisationDetector, concatTextBlocks(ctr))
	}

	d := evaluateOutputSanitisation(cfg, contentTrust, criticalDetected)
	if !d.block && !d.redact && !d.strip {
		return nil
	}

	sessionID := ""
	if sess := mcpserver.ClientSessionFromContext(ctx); sess != nil {
		sessionID = sess.SessionID()
	}

	if d.block {
		p.emitActivityPolicyDecision(serverName, toolName, sessionID, "blocked", d.reason)
		return mcp.NewToolResultError("tool output blocked by sanitisation policy: " + d.reason)
	}

	// Mutate text blocks in place. Order: redact -> strip (D4).
	redactedCount := 0
	redactedCats := map[string]struct{}{}
	strippedClasses := map[string]struct{}{}
	stripSet := cfg.EnabledStripClasses()

	for i, c := range ctr.Content {
		tc, ok := c.(mcp.TextContent)
		if !ok {
			continue // FR-B5: non-text blocks untouched
		}
		txt := tc.Text

		if d.redact && p.sanitisationDetector != nil {
			redacted, dets := p.sanitisationDetector.Redact(txt)
			if len(dets) > 0 {
				txt = redacted
				redactedCount += len(dets)
				for _, det := range dets {
					redactedCats[det.Category] = struct{}{}
				}
			}
		}

		if d.strip {
			stripped, classes := security.StripControlSequences(txt, stripSet)
			txt = stripped
			for _, cl := range classes {
				strippedClasses[cl] = struct{}{}
			}
		}

		tc.Text = txt
		ctr.Content[i] = tc
	}

	if redactedCount > 0 || len(strippedClasses) > 0 {
		action, reason := summariseSanitisation(redactedCount, redactedCats, strippedClasses)
		p.emitActivityPolicyDecision(serverName, toolName, sessionID, action, reason)
	}

	return nil
}

// spotlightForwarded wraps untrusted text blocks of the (already truncated)
// forwarded result in source-identifying delimiters (FR-B1/B2). It is lossless
// and a presentation frame, so it runs AFTER forwardContentResult and is never
// cached. Trusted output and the opt-out default leave the result untouched.
func (p *MCPProxyServer) spotlightForwarded(serverName, toolName, contentTrust string, forwarded *mcp.CallToolResult) {
	cfg := p.config.OutputSanitisation
	if cfg == nil {
		cfg = config.DefaultOutputSanitisationConfig()
	}
	if contentTrust != contracts.ContentTrustUntrusted || !cfg.IsSpotlightEnabled() {
		return
	}
	if forwarded == nil || forwarded.IsError {
		return
	}
	wrapped := false
	for i, c := range forwarded.Content {
		tc, ok := c.(mcp.TextContent)
		if !ok {
			continue // FR-B5
		}
		tc.Text = security.SpotlightUntrusted(tc.Text, serverName, toolName)
		forwarded.Content[i] = tc
		wrapped = true
	}
	if wrapped {
		p.logger.Debug("Spotlighted untrusted tool output",
			zap.String("server", serverName), zap.String("tool", toolName))
	}
}

// concatTextBlocks joins the text of all TextContent blocks for scanning.
func concatTextBlocks(r *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range r.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			if b.Len() > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

// hasCriticalDetection reports whether the detector finds any critical-severity
// secret in content. Used to gate the block action (FR-B7).
func hasCriticalDetection(d *security.Detector, content string) bool {
	if content == "" {
		return false
	}
	res := d.Scan("", content)
	if res == nil || !res.Detected {
		return false
	}
	for _, det := range res.Detections {
		if det.Severity == string(security.SeverityCritical) {
			return true
		}
	}
	return false
}

// summariseSanitisation builds the policy_decision action label + human reason.
func summariseSanitisation(redactedCount int, cats, classes map[string]struct{}) (action, reason string) {
	parts := []string{}
	if redactedCount > 0 {
		action = "redact"
	} else {
		action = "strip"
	}
	if redactedCount > 0 {
		parts = append(parts, fmt.Sprintf("redacted %d secret(s) [%s]", redactedCount, joinSorted(cats)))
	}
	if len(classes) > 0 {
		parts = append(parts, fmt.Sprintf("stripped control sequences [%s]", joinSorted(classes)))
	}
	return action, strings.Join(parts, "; ")
}

func joinSorted(m map[string]struct{}) string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}
