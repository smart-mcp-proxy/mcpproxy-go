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
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/telemetry"
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
// `requestID` is the dispatch's correlation id; the block and redaction records
// this writes belong to that call and would otherwise be unattributable.
//
// `result` is the upstream value (interface{}) handed to forwardContentResult;
// when it is not a *mcp.CallToolResult (the legacy JSON-wrap path) sanitisation
// is a no-op. The fast path returns immediately for the common opt-out case.
func (p *MCPProxyServer) applyOutputSanitisation(ctx context.Context, serverName, toolName, requestID, contentTrust string, result interface{}) *mcp.CallToolResult {
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
		p.emitActivityPolicyDecision(serverName, toolName, sessionID, requestID, "blocked", d.reason, telemetry.BlockReasonOutputSanitisation)
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
		txt, n := p.sanitiseTextValue(tc.Text, d, stripSet, redactedCats, strippedClasses)
		redactedCount += n
		tc.Text = txt
		ctr.Content[i] = tc
	}

	if redactedCount > 0 || len(strippedClasses) > 0 {
		action, reason := summariseSanitisation(redactedCount, redactedCats, strippedClasses)
		// Redact/strip are not blocks — the availability counter ignores them —
		// but the key is still declared so the funnel has no unclassified sites.
		p.emitActivityPolicyDecision(serverName, toolName, sessionID, requestID, action, reason, telemetry.BlockReasonOutputSanitisation)
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

// sanitiseTextValue applies the redact->strip mutation (the same order the tool
// path uses, D4) to a single text value, using the shared detector + strip
// config. It accumulates redaction categories / stripped classes into the
// caller-owned maps and returns the mutated text plus the redaction count for
// this value. Pure w.r.t. I/O — the caller owns activity emission. This is the
// per-block body shared by applyOutputSanitisation (CallToolResult) and
// applyPromptResultSanitisation (GetPromptResult, Finding F2).
func (p *MCPProxyServer) sanitiseTextValue(
	txt string, d osDecision, stripSet map[string]bool,
	redactedCats, strippedClasses map[string]struct{},
) (string, int) {
	redactedCount := 0
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
	return txt, redactedCount
}

// concatPromptText joins the text of a prompt result's messages for critical-
// secret scanning (block mode). Walks TextContent and the text of any embedded
// TextResourceContents; binary/image/audio blocks contribute nothing.
func concatPromptText(r *mcp.GetPromptResult) string {
	var b strings.Builder
	appendTxt := func(s string) {
		if s == "" {
			return
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(s)
	}
	for i := range r.Messages {
		switch c := r.Messages[i].Content.(type) {
		case mcp.TextContent:
			appendTxt(c.Text)
		case mcp.EmbeddedResource:
			if tr, ok := c.Resource.(mcp.TextResourceContents); ok {
				appendTxt(tr.Text)
			}
		}
	}
	return b.String()
}

// applyPromptResultSanitisation is the GetPromptResult analogue of
// applyOutputSanitisation (Finding F2). Prompt content originates from an
// upstream server and has no openWorldHint distinction, so it is ALWAYS treated
// as untrusted. It reuses the same decision core (evaluateOutputSanitisation),
// the same detector (p.sanitisationDetector) and the same strip/spotlight
// helpers the tool path uses. Because prompts have no truncation/cache stage,
// spotlight is applied inline here rather than in a separate post-forward pass.
//
// Returns (result, blocked). On block (critical secret + block mode) it emits a
// policy decision and returns (nil, true); the caller substitutes an error.
func (p *MCPProxyServer) applyPromptResultSanitisation(
	ctx context.Context, serverName, promptName, requestID string, result *mcp.GetPromptResult,
) (*mcp.GetPromptResult, bool) {
	if result == nil {
		return result, false
	}
	cfg := p.config.OutputSanitisation
	if cfg == nil {
		cfg = config.DefaultOutputSanitisationConfig()
	}
	const contentTrust = contracts.ContentTrustUntrusted
	// Fast opt-out: prompt content is untrusted, so strip AND spotlight are both
	// live inputs here (unlike the tool fast path, which defers spotlight).
	if !cfg.IsBlock() && !cfg.IsRedact() && !cfg.IsStripEnabled() && !cfg.IsSpotlightEnabled() {
		return result, false
	}

	criticalDetected := false
	if cfg.IsBlock() && p.sanitisationDetector != nil {
		criticalDetected = hasCriticalDetection(p.sanitisationDetector, concatPromptText(result))
	}
	d := evaluateOutputSanitisation(cfg, contentTrust, criticalDetected)

	sessionID := ""
	if sess := mcpserver.ClientSessionFromContext(ctx); sess != nil {
		sessionID = sess.SessionID()
	}

	if d.block {
		p.emitActivityPolicyDecision(serverName, promptName, sessionID, requestID,
			"blocked", d.reason, telemetry.BlockReasonOutputSanitisation)
		return nil, true
	}
	if !d.redact && !d.strip && !d.spotlight {
		return result, false
	}

	redactedCount := 0
	redactedCats := map[string]struct{}{}
	strippedClasses := map[string]struct{}{}
	stripSet := cfg.EnabledStripClasses()

	applyOne := func(txt string) string {
		txt, n := p.sanitiseTextValue(txt, d, stripSet, redactedCats, strippedClasses)
		redactedCount += n
		if d.spotlight {
			txt = security.SpotlightUntrusted(txt, serverName, promptName)
		}
		return txt
	}

	for i := range result.Messages {
		switch c := result.Messages[i].Content.(type) {
		case mcp.TextContent:
			c.Text = applyOne(c.Text)
			result.Messages[i].Content = c
		case mcp.EmbeddedResource:
			if tr, ok := c.Resource.(mcp.TextResourceContents); ok {
				tr.Text = applyOne(tr.Text)
				c.Resource = tr
				result.Messages[i].Content = c
			}
			// BlobResourceContents (binary) untouched — FR-B5 parity.
		default:
			// ImageContent / AudioContent untouched — FR-B5 parity.
		}
	}

	if redactedCount > 0 || len(strippedClasses) > 0 {
		action, reason := summariseSanitisation(redactedCount, redactedCats, strippedClasses)
		p.emitActivityPolicyDecision(serverName, promptName, sessionID, requestID,
			action, reason, telemetry.BlockReasonOutputSanitisation)
	}
	return result, false
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
