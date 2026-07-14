package server

import (
	"encoding/json"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/telemetry"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/toonenc"
)

// toonEncodeBlock is the per-block encode seam. A package variable (not a
// direct call) so tests can inject a Decision — in particular the
// passthrough-error outcome, which is unreachable through the real encoder
// with JSON-parseable input (toon-go accepts every value parsed JSON yields).
var toonEncodeBlock = toonenc.EncodeBlock

// encodeToonBlocks is the spec-084 server seam: it applies adaptive/always
// TOON encoding to the TextContent blocks of a sanitised call_tool_* result,
// in place, and returns what the activity pipeline needs:
//
//   - detectionText: a best-effort reconstruction of the pre-encoding text the
//     feature-off path would hand the sensitive-data detector (FR-007b) —
//     all content blocks rendered with forwardContentResult's rules (text
//     verbatim + image/audio/unknown placeholders), each text block truncated
//     with the same truncator budget/tool/args, then spotlight-wrapped when
//     contentTrust is untrusted. The contract is detection FINDING parity,
//     not byte parity (the truncation banner's timestamped cache key differs
//     but carries no upstream data — data-model §7).
//   - decisions: one toonenc.Decision per text block, BlockIndex set to the
//     text-block index (FR-010).
//
// Resolution and behavior:
//   - The mode is resolved fresh on every call (per-server toon_output >
//     global > off, config.ResolveToonOutput) so a hot-reloaded config edit
//     applies to the next call with no restart (FR-001) — the same pattern
//     applyOutputSanitisation uses.
//   - off (or unparseable mode, or a nil/error result) returns ("", nil) and
//     touches nothing: the response stays byte-identical to pre-feature
//     behavior and runAsyncDetection falls back to scanning today's response
//     string (FR-002, issue 2).
//   - The caller MUST invoke this AFTER applyOutputSanitisation (the encoder
//     input is the sanitised result, FR-007a) and BEFORE forwardContentResult
//     (truncation applies to the final rendered payload, FR-008).
//   - A passthrough-error outcome (genuine encoder failure) is logged and
//     counted here (FR-006); EncodeBlock itself is pure. Non-JSON blocks are
//     ordinary passthrough-not-tabular traffic and are never logged.
func (p *MCPProxyServer) encodeToonBlocks(serverName, toolName, contentTrust string, args map[string]interface{}, result *mcp.CallToolResult) (string, []toonenc.Decision) {
	cfg := p.liveToonConfig()
	if cfg == nil {
		return "", nil
	}
	mode, ok := toonenc.ParseMode(cfg.ResolveToonOutput(findServerConfig(cfg, serverName)))
	if !ok || mode == toonenc.ModeOff {
		return "", nil
	}
	if result == nil || result.IsError {
		// Error results are never encoded (mirrors applyOutputSanitisation);
		// empty detectionText keeps the detector on today's response.
		return "", nil
	}

	pct := cfg.ToonMinSavingsPct
	if pct < 1 || pct > 90 {
		pct = 15 // validated range is 1-90; 0/unset resolves to the default
	}
	retainedBudget := 0
	if p.truncator != nil {
		retainedBudget = p.truncator.SimpleTruncateBudget()
	}

	// Spotlight framing must match spotlightForwarded's decision EXACTLY so
	// the detector sees the same delimiters the off path produces (finding
	// parity). That helper reads the boot-time p.config.OutputSanitisation,
	// so the reconstruction reads the same source — not the live snapshot.
	sanCfg := cfg.OutputSanitisation
	if p.config != nil {
		sanCfg = p.config.OutputSanitisation
	}
	if sanCfg == nil {
		sanCfg = config.DefaultOutputSanitisationConfig()
	}
	spotlight := contentTrust == contracts.ContentTrustUntrusted && sanCfg.IsSpotlightEnabled()

	// forwardContentResult derives truncation cache keys from the qualified
	// tool name; use the same identity for budget parity (issue 5).
	qualifiedTool := serverName + ":" + toolName

	var detParts []string
	var decisions []toonenc.Decision
	textIdx := 0
	for i, c := range result.Content {
		switch tc := c.(type) {
		case mcp.TextContent:
			original := tc.Text

			// 1. Detection rendering from the PRE-encoding text: truncate with
			// the same truncator (no cache writes — caching stays owned by the
			// real forwardContentResult), then spotlight, matching the order
			// of the off path (forward → spotlight).
			det := original
			if p.truncator != nil && p.truncator.ShouldTruncate(det) {
				det = p.truncator.Truncate(det, qualifiedTool, args).TruncatedContent
			}
			if spotlight {
				det = security.SpotlightUntrusted(det, serverName, toolName)
			}
			detParts = append(detParts, det)

			// 2. Per-block encode decision (FR-003/FR-009).
			out, d := toonEncodeBlock(original, mode, pct, retainedBudget)
			d.BlockIndex = textIdx
			decisions = append(decisions, d)
			if d.Outcome == toonenc.OutcomePassthroughError {
				// FR-006: the only outcome that is logged and counted.
				if p.logger != nil {
					p.logger.Warn("TOON encoding failed; block passed through unchanged",
						zap.String("server", serverName),
						zap.String("tool", toolName),
						zap.Int("block_index", textIdx))
				}
				telemetry.RecordErrorOn(p.telemetryRegistry(), telemetry.ErrCatToonEncodeFallback)
			}
			if out != original {
				tc.Text = out
				result.Content[i] = tc
			}
			textIdx++
		case mcp.ImageContent:
			detParts = append(detParts, fmt.Sprintf("[image:%s len=%d]", tc.MIMEType, len(tc.Data)))
		case mcp.AudioContent:
			detParts = append(detParts, fmt.Sprintf("[audio:%s len=%d]", tc.MIMEType, len(tc.Data)))
		default:
			// Unknown content (e.g. EmbeddedResource): best-effort JSON, the
			// same rule forwardContentResult applies.
			if b, err := json.Marshal(c); err == nil {
				detParts = append(detParts, string(b))
			}
		}
	}
	return joinTextParts(detParts), decisions
}

// liveToonConfig returns the CURRENT hot-reloaded configuration. p.config is
// the boot-time pointer; /api/v1/config/apply (and the file watcher) swap the
// Runtime's config snapshot without touching it (the stale-config-snapshot
// gotcha), so the seam must resolve through the runtime for FR-001's "applies
// to the next tool call without restart" to hold. Unit tests without a wired
// mainServer fall back to p.config, which those tests swap directly.
func (p *MCPProxyServer) liveToonConfig() *config.Config {
	if p.mainServer != nil && p.mainServer.runtime != nil {
		if cfg := p.mainServer.runtime.Config(); cfg != nil {
			return cfg
		}
	}
	return p.config
}

// findServerConfig returns the live per-server config for name, or nil. Reads
// the hot-reloaded cfg.Servers slice so a per-server toon_output edit applies
// on the next call.
func findServerConfig(cfg *config.Config, name string) *config.ServerConfig {
	for _, sc := range cfg.Servers {
		if sc != nil && sc.Name == name {
			return sc
		}
	}
	return nil
}

// toonOutputMetadata converts per-block decisions into the FR-010 activity
// metadata shape (data-model §6). Returns nil when the feature did not run
// (no decisions), so off-mode records carry no toon_output key (SC-002).
func toonOutputMetadata(decisions []toonenc.Decision) map[string]interface{} {
	if len(decisions) == 0 {
		return nil
	}
	blocks := make([]interface{}, 0, len(decisions))
	for _, d := range decisions {
		classification := "not-tabular"
		if d.Classification.Tabular {
			classification = "tabular"
		}
		b := map[string]interface{}{
			"index":          d.BlockIndex,
			"outcome":        string(d.Outcome),
			"classification": classification,
			"threshold_pct":  d.ThresholdPct,
		}
		if d.Outcome == toonenc.OutcomeEncoded {
			b["bytes_before"] = d.PassthroughEmissionBytes
			b["bytes_after"] = d.EncodedEmissionBytes
		}
		blocks = append(blocks, b)
	}
	return map[string]interface{}{
		"mode":   string(decisions[0].Mode),
		"blocks": blocks,
	}
}
