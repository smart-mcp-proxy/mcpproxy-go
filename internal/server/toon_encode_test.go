package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/telemetry"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/toonenc"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/truncate"
)

// tabularJSON builds a uniform flat array large enough that TOON wins by well
// over the default 15% threshold.
func tabularJSON(rows int) string {
	items := make([]map[string]interface{}, 0, rows)
	for i := 0; i < rows; i++ {
		items = append(items, map[string]interface{}{
			"id":     i,
			"name":   fmt.Sprintf("row-%d", i),
			"active": i%2 == 0,
		})
	}
	b, _ := json.Marshal(items)
	return string(b)
}

func newToonProxy(mode string, servers ...*config.ServerConfig) *MCPProxyServer {
	cfg := config.DefaultConfig()
	cfg.ToonOutput = mode
	cfg.Servers = servers
	return &MCPProxyServer{config: cfg, logger: zap.NewNop()}
}

func toonTextResult(texts ...string) *mcp.CallToolResult {
	content := make([]mcp.Content, 0, len(texts))
	for _, s := range texts {
		content = append(content, mcp.TextContent{Type: "text", Text: s})
	}
	return &mcp.CallToolResult{Content: content}
}

// TestEncodeToonBlocks_OffIsInert (FR-002, issue 2): with the feature off the
// seam returns ("", nil) — empty detection text so the detector falls back to
// today's response — and every block stays byte-identical.
func TestEncodeToonBlocks_OffIsInert(t *testing.T) {
	for _, mode := range []string{"", "off"} {
		t.Run("mode="+mode, func(t *testing.T) {
			p := newToonProxy(mode)
			original := tabularJSON(50)
			result := toonTextResult(original)

			detText, decisions := p.encodeToonBlocks("srv", "tool", contracts.ContentTrustTrusted, nil, result)

			if detText != "" {
				t.Errorf("off mode must return empty detection text, got %d bytes", len(detText))
			}
			if decisions != nil {
				t.Errorf("off mode must return nil decisions, got %v", decisions)
			}
			if got := result.Content[0].(mcp.TextContent).Text; got != original {
				t.Error("off mode must leave the block byte-identical")
			}
		})
	}
}

// TestEncodeToonBlocks_AdaptiveEncodesTabular (US1-AC1): a uniform tabular
// block is rewritten to Marker + "\n" + TOON body, smaller than the
// passthrough by at least the threshold, and the decision carries the sizes.
func TestEncodeToonBlocks_AdaptiveEncodesTabular(t *testing.T) {
	p := newToonProxy("adaptive")
	original := tabularJSON(50)
	result := toonTextResult(original)

	detText, decisions := p.encodeToonBlocks("srv", "tool", contracts.ContentTrustTrusted, nil, result)

	got := result.Content[0].(mcp.TextContent).Text
	if !strings.HasPrefix(got, toonenc.Marker+"\n") {
		t.Fatalf("encoded block must start with the marker, got %q", got[:80])
	}
	if len(got) > len(original)*(100-15)/100 {
		t.Errorf("encoded emission %d bytes does not beat passthrough %d by the threshold", len(got), len(original))
	}
	if len(decisions) != 1 {
		t.Fatalf("want 1 decision, got %d", len(decisions))
	}
	d := decisions[0]
	if d.Outcome != toonenc.OutcomeEncoded || d.BlockIndex != 0 {
		t.Errorf("unexpected decision: %+v", d)
	}
	if d.PassthroughEmissionBytes != len(original) || d.EncodedEmissionBytes != len(got) {
		t.Errorf("decision sizes wrong: %+v (want before=%d after=%d)", d, len(original), len(got))
	}
	// Detection text is the PRE-encoding rendering (FR-007b).
	if detText != original {
		t.Errorf("detection text must be the pre-encoding block text")
	}
}

// TestEncodeToonBlocks_AdaptiveNonTabularPassthrough (US1-AC2): a nested
// object passes through byte-identical with a passthrough-not-tabular
// decision — same bytes as off mode.
func TestEncodeToonBlocks_AdaptiveNonTabularPassthrough(t *testing.T) {
	p := newToonProxy("adaptive")
	original := `{"outer":{"inner":{"deep":[1,2,3]}},"other":"x"}`
	result := toonTextResult(original)

	_, decisions := p.encodeToonBlocks("srv", "tool", contracts.ContentTrustTrusted, nil, result)

	if got := result.Content[0].(mcp.TextContent).Text; got != original {
		t.Errorf("non-tabular block must be byte-identical, got %q", got)
	}
	if len(decisions) != 1 || decisions[0].Outcome != toonenc.OutcomePassthroughNotTabular {
		t.Errorf("want passthrough-not-tabular, got %+v", decisions)
	}
}

// TestEncodeToonBlocks_PerServerOverride (FR-001, US2-AC2): a per-server
// toon_output overrides the global in both directions.
func TestEncodeToonBlocks_PerServerOverride(t *testing.T) {
	original := tabularJSON(50)

	t.Run("global adaptive, server off → passthrough", func(t *testing.T) {
		p := newToonProxy("adaptive", &config.ServerConfig{Name: "quiet", ToonOutput: "off"})
		result := toonTextResult(original)
		detText, decisions := p.encodeToonBlocks("quiet", "tool", contracts.ContentTrustTrusted, nil, result)
		if detText != "" || decisions != nil {
			t.Error("per-server off must behave exactly like off")
		}
		if got := result.Content[0].(mcp.TextContent).Text; got != original {
			t.Error("per-server off must leave the block byte-identical")
		}
	})

	t.Run("global off, server adaptive → encodes", func(t *testing.T) {
		p := newToonProxy("off", &config.ServerConfig{Name: "loud", ToonOutput: "adaptive"})
		result := toonTextResult(original)
		_, decisions := p.encodeToonBlocks("loud", "tool", contracts.ContentTrustTrusted, nil, result)
		if len(decisions) != 1 || decisions[0].Outcome != toonenc.OutcomeEncoded {
			t.Errorf("per-server adaptive must encode, got %+v", decisions)
		}
	})

	t.Run("other servers keep the global", func(t *testing.T) {
		p := newToonProxy("adaptive", &config.ServerConfig{Name: "quiet", ToonOutput: "off"})
		result := toonTextResult(original)
		_, decisions := p.encodeToonBlocks("other", "tool", contracts.ContentTrustTrusted, nil, result)
		if len(decisions) != 1 || decisions[0].Outcome != toonenc.OutcomeEncoded {
			t.Errorf("global adaptive must still encode for other servers, got %+v", decisions)
		}
	})
}

// TestEncodeToonBlocks_AlwaysEncodesNested (FR-009): always mode encodes any
// JSON value, marker included; non-JSON still passes through unmarked.
func TestEncodeToonBlocks_AlwaysEncodesNested(t *testing.T) {
	p := newToonProxy("always")
	result := toonTextResult(`{"outer":{"inner":[1,2,3]}}`, "plain text, not JSON")

	_, decisions := p.encodeToonBlocks("srv", "tool", contracts.ContentTrustTrusted, nil, result)

	if got := result.Content[0].(mcp.TextContent).Text; !strings.HasPrefix(got, toonenc.Marker+"\n") {
		t.Errorf("always mode must encode nested JSON, got %q", got)
	}
	if got := result.Content[1].(mcp.TextContent).Text; got != "plain text, not JSON" {
		t.Errorf("non-JSON must pass through unmarked, got %q", got)
	}
	if len(decisions) != 2 ||
		decisions[0].Outcome != toonenc.OutcomeEncoded ||
		decisions[1].Outcome != toonenc.OutcomePassthroughNotTabular {
		t.Errorf("unexpected decisions: %+v", decisions)
	}
	if decisions[0].BlockIndex != 0 || decisions[1].BlockIndex != 1 {
		t.Errorf("block indices wrong: %+v", decisions)
	}
}

// TestEncodeToonBlocks_DetectionTextAllBlocks (FR-007b, finding 6): the
// detection rendering spans ALL content blocks with forwardContentResult's
// placeholder rules, so a secret next to an image block is still scanned.
func TestEncodeToonBlocks_DetectionTextAllBlocks(t *testing.T) {
	p := newToonProxy("adaptive")
	result := &mcp.CallToolResult{Content: []mcp.Content{
		mcp.TextContent{Type: "text", Text: "secret AKIA1234567890ABCDEF here"},
		mcp.ImageContent{Type: "image", MIMEType: "image/png", Data: "abcd"},
		mcp.TextContent{Type: "text", Text: tabularJSON(50)},
	}}

	detText, decisions := p.encodeToonBlocks("srv", "tool", contracts.ContentTrustTrusted, nil, result)

	parts := strings.Split(detText, "\n[image:image/png len=4]\n")
	if len(parts) != 2 {
		t.Fatalf("detection text must contain the image placeholder between the text blocks, got %q", detText)
	}
	if parts[0] != "secret AKIA1234567890ABCDEF here" {
		t.Errorf("first block must be scanned verbatim, got %q", parts[0])
	}
	if parts[1] != tabularJSON(50) {
		t.Error("second text block must appear pre-encoding in the detection text")
	}
	// Decisions are keyed by TEXT-block index — the image block does not count.
	if len(decisions) != 2 || decisions[0].BlockIndex != 0 || decisions[1].BlockIndex != 1 {
		t.Errorf("decision indices must skip non-text blocks: %+v", decisions)
	}
	// And the agent-facing second block was actually encoded.
	if got := result.Content[2].(mcp.TextContent).Text; !strings.HasPrefix(got, toonenc.Marker+"\n") {
		t.Error("tabular block next to an image must still encode")
	}
}

// TestEncodeToonBlocks_DetectionTextSpotlight (round-3): when contentTrust is
// untrusted and spotlight is enabled, the detection text carries the same
// delimiter framing the off path produces (security.SpotlightUntrusted).
func TestEncodeToonBlocks_DetectionTextSpotlight(t *testing.T) {
	p := newToonProxy("adaptive")
	sanCfg := config.DefaultOutputSanitisationConfig()
	sanCfg.SpotlightUntrusted = true
	p.config.OutputSanitisation = sanCfg

	original := `{"nested":{"deep":true}}` // passthrough — content unchanged
	result := toonTextResult(original)

	detText, _ := p.encodeToonBlocks("srv", "tool", contracts.ContentTrustUntrusted, nil, result)

	want := security.SpotlightUntrusted(original, "srv", "tool")
	if detText != want {
		t.Errorf("detection text must be spotlight-wrapped:\n got %q\nwant %q", detText, want)
	}

	// Trusted content is never wrapped.
	result2 := toonTextResult(original)
	detText2, _ := p.encodeToonBlocks("srv", "tool", contracts.ContentTrustTrusted, nil, result2)
	if detText2 != original {
		t.Errorf("trusted detection text must be unwrapped, got %q", detText2)
	}
}

// TestEncodeToonBlocks_DetectionTextTruncated (issue 5): an over-limit block
// is truncated in the detection text with the same truncator budget, so a
// secret past the limit is dropped exactly as the off path would drop it.
func TestEncodeToonBlocks_DetectionTextTruncated(t *testing.T) {
	p := newToonProxy("adaptive")
	tr := truncate.NewTruncator(500)
	p.setStaticTruncator(tr)

	// Non-JSON so the truncator's deterministic simpleTruncate path runs both
	// here and in the seam (JSON-analyzable content mints a timestamped cache
	// key, which is legitimate but not byte-comparable in a test).
	original := strings.Repeat("x", 2000)
	result := toonTextResult(original)

	detText, _ := p.encodeToonBlocks("srv", "tool", contracts.ContentTrustTrusted, nil, result)

	want := tr.Truncate(original, "srv:tool", nil).TruncatedContent
	if detText != want {
		t.Errorf("detection text must be truncated with the same budget:\n got %d bytes\nwant %d bytes", len(detText), len(want))
	}
	// The agent-facing block itself is untouched here (non-JSON passthrough);
	// truncation of the response is forwardContentResult's job, after the seam.
	if got := result.Content[0].(mcp.TextContent).Text; got != original {
		t.Error("the seam must not truncate the agent-facing block")
	}
}

// TestEncodeToonBlocks_ErrorLoggedAndCounted (T-ERR, FR-006): a genuine
// encoder failure (injected via the toonEncodeBlock seam — unreachable through
// the real encoder on JSON input) passes the block through unchanged, emits
// exactly one zap.Warn with server/tool/block fields, and increments the
// toon_encode_fallback telemetry counter.
func TestEncodeToonBlocks_ErrorLoggedAndCounted(t *testing.T) {
	core, logs := observer.New(zap.WarnLevel)
	reg := telemetry.NewCounterRegistry()
	p := newToonProxy("adaptive")
	p.logger = zap.New(core)
	p.telemetryRegOverride = reg

	orig := toonEncodeBlock
	toonEncodeBlock = func(text string, mode toonenc.Mode, pct, budget int) (string, toonenc.Decision) {
		return text, toonenc.Decision{
			Mode:                     mode,
			Outcome:                  toonenc.OutcomePassthroughError,
			PassthroughEmissionBytes: len(text),
			ThresholdPct:             pct,
		}
	}
	defer func() { toonEncodeBlock = orig }()

	original := tabularJSON(50)
	result := toonTextResult(original)
	_, decisions := p.encodeToonBlocks("srv", "tool", contracts.ContentTrustTrusted, nil, result)

	if got := result.Content[0].(mcp.TextContent).Text; got != original {
		t.Error("passthrough-error must never lose data — block must be byte-identical")
	}
	if len(decisions) != 1 || decisions[0].Outcome != toonenc.OutcomePassthroughError {
		t.Fatalf("want passthrough-error decision, got %+v", decisions)
	}

	entries := logs.All()
	if len(entries) != 1 {
		t.Fatalf("want exactly 1 warn, got %d", len(entries))
	}
	fields := entries[0].ContextMap()
	if fields["server"] != "srv" || fields["tool"] != "tool" || fields["block_index"] != int64(0) {
		t.Errorf("warn fields wrong: %v", fields)
	}
	if got := reg.Snapshot().ErrorCategoryCounts[string(telemetry.ErrCatToonEncodeFallback)]; got != 1 {
		t.Errorf("toon_encode_fallback counter = %d, want 1", got)
	}
}

// TestEncodeToonBlocks_NonJSONNotLogged (issue 3): a parse failure / non-JSON
// block is ordinary passthrough-not-tabular traffic — no warn, no counter.
func TestEncodeToonBlocks_NonJSONNotLogged(t *testing.T) {
	core, logs := observer.New(zap.WarnLevel)
	reg := telemetry.NewCounterRegistry()
	p := newToonProxy("adaptive")
	p.logger = zap.New(core)
	p.telemetryRegOverride = reg

	result := toonTextResult("just some plain text output")
	_, decisions := p.encodeToonBlocks("srv", "tool", contracts.ContentTrustTrusted, nil, result)

	if len(decisions) != 1 || decisions[0].Outcome != toonenc.OutcomePassthroughNotTabular {
		t.Fatalf("want passthrough-not-tabular, got %+v", decisions)
	}
	if logs.Len() != 0 {
		t.Errorf("non-JSON must not be logged, got %d entries", logs.Len())
	}
	if got := reg.Snapshot().ErrorCategoryCounts[string(telemetry.ErrCatToonEncodeFallback)]; got != 0 {
		t.Errorf("non-JSON must not be counted, counter = %d", got)
	}
}

// TestEncodeToonBlocks_HotReloadApplies (FR-001, US2-AC3): the seam reads the
// config fresh on every call, so an atomically swapped config (what the
// hot-reload path does) changes behavior on the next call with no restart.
func TestEncodeToonBlocks_HotReloadApplies(t *testing.T) {
	p := newToonProxy("off")
	original := tabularJSON(50)

	result := toonTextResult(original)
	if detText, decisions := p.encodeToonBlocks("srv", "tool", contracts.ContentTrustTrusted, nil, result); detText != "" || decisions != nil {
		t.Fatal("precondition: off must be inert")
	}

	// Simulate the hot-reload swap.
	newCfg := config.DefaultConfig()
	newCfg.ToonOutput = "adaptive"
	p.config = newCfg

	result2 := toonTextResult(original)
	_, decisions := p.encodeToonBlocks("srv", "tool", contracts.ContentTrustTrusted, nil, result2)
	if len(decisions) != 1 || decisions[0].Outcome != toonenc.OutcomeEncoded {
		t.Errorf("after reload to adaptive the next call must encode, got %+v", decisions)
	}

	// And back off again.
	offCfg := config.DefaultConfig()
	p.config = offCfg
	result3 := toonTextResult(original)
	if detText, decisions := p.encodeToonBlocks("srv", "tool", contracts.ContentTrustTrusted, nil, result3); detText != "" || decisions != nil {
		t.Error("after reverting to off the next call must be inert")
	}
}

// TestEncodeToonBlocks_SanitisationBeforeEncoding (FR-007a, T026): the seam
// runs on the sanitised result — a redacted secret is absent from the encoded
// TOON body. This mirrors the real ordering in handleCallToolVariant
// (applyOutputSanitisation → encodeToonBlocks → forwardContentResult).
func TestEncodeToonBlocks_SanitisationBeforeEncoding(t *testing.T) {
	sanCfg := config.DefaultOutputSanitisationConfig()
	sanCfg.ResponseAction = "redact"

	p := newToonProxy("adaptive")
	p.config.OutputSanitisation = sanCfg
	p.sanitisationDetector = security.NewDetector(config.DefaultSensitiveDataDetectionConfig())

	// A tabular payload with a detectable AWS key in one row.
	items := make([]map[string]interface{}, 0, 20)
	for i := 0; i < 20; i++ {
		items = append(items, map[string]interface{}{"id": i, "token": "value", "note": fmt.Sprintf("row %d padding padding", i)})
	}
	items[3]["token"] = "AKIA1234567890ABCDEF"
	raw, _ := json.Marshal(items)
	result := toonTextResult(string(raw))

	// Real pipeline order: sanitise first…
	if block := p.applyOutputSanitisation(context.Background(), "srv", "tool", contracts.ContentTrustTrusted, result); block != nil {
		t.Fatal("redact mode must not block")
	}
	// …then encode.
	detText, decisions := p.encodeToonBlocks("srv", "tool", contracts.ContentTrustTrusted, nil, result)

	encoded := result.Content[0].(mcp.TextContent).Text
	if strings.Contains(encoded, "AKIA1234567890ABCDEF") {
		t.Error("redacted secret leaked into the encoded body — sanitisation must run before encoding")
	}
	if strings.Contains(detText, "AKIA1234567890ABCDEF") {
		t.Error("redacted secret leaked into the detection text")
	}
	if len(decisions) != 1 || decisions[0].Outcome != toonenc.OutcomeEncoded {
		t.Fatalf("fixture must still encode after redaction, got %+v", decisions)
	}
}

// TestToonOutputMetadata (FR-010): the activity metadata shape — mode +
// per-block entries, byte sizes only on encoded outcomes, nil when the
// feature did not run.
func TestToonOutputMetadata(t *testing.T) {
	if toonOutputMetadata(nil) != nil {
		t.Error("no decisions must yield nil metadata (off-mode records carry no toon_output key)")
	}

	md := toonOutputMetadata([]toonenc.Decision{
		{
			BlockIndex: 0, Mode: toonenc.ModeAdaptive,
			Classification:           toonenc.Classification{Tabular: true, Rows: 50, Cols: 3},
			PassthroughEmissionBytes: 8123, EncodedEmissionBytes: 5140,
			ThresholdPct: 15, Outcome: toonenc.OutcomeEncoded,
		},
		{
			BlockIndex: 1, Mode: toonenc.ModeAdaptive,
			Classification:           toonenc.Classification{Reason: toonenc.ReasonNestedValues},
			PassthroughEmissionBytes: 100, ThresholdPct: 15,
			Outcome: toonenc.OutcomePassthroughNotTabular,
		},
	})

	if md["mode"] != "adaptive" {
		t.Errorf("mode = %v", md["mode"])
	}
	blocks := md["blocks"].([]interface{})
	if len(blocks) != 2 {
		t.Fatalf("want 2 blocks, got %d", len(blocks))
	}
	b0 := blocks[0].(map[string]interface{})
	if b0["index"] != 0 || b0["outcome"] != "encoded" || b0["classification"] != "tabular" ||
		b0["bytes_before"] != 8123 || b0["bytes_after"] != 5140 || b0["threshold_pct"] != 15 {
		t.Errorf("block 0 wrong: %v", b0)
	}
	b1 := blocks[1].(map[string]interface{})
	if b1["outcome"] != "passthrough-not-tabular" || b1["classification"] != "not-tabular" {
		t.Errorf("block 1 wrong: %v", b1)
	}
	if _, has := b1["bytes_after"]; has {
		t.Error("byte sizes must be present only on encoded outcomes")
	}
}

// TestCallToolVariantDescriptionsEchoToonMarker (T037, FR-005): the
// call_tool_read|write|destructive tool descriptions echo the TOON marker
// contract so agents learn it in-session (spec Assumptions,
// contracts/marker-format.md "Cross-surface documentation"). The description
// embeds toonenc.Marker verbatim — the constant stays the single source of
// truth; the description is documentation of it, not a second copy to drift.
func TestCallToolVariantDescriptionsEchoToonMarker(t *testing.T) {
	for _, variant := range []string{
		contracts.ToolVariantRead,
		contracts.ToolVariantWrite,
		contracts.ToolVariantDestructive,
	} {
		t.Run(variant, func(t *testing.T) {
			tool := buildCallToolVariantTool(variant)
			if !strings.Contains(tool.Description, toonenc.Marker) {
				t.Errorf("%s description must echo the TOON marker contract (toonenc.Marker); got: %q", variant, tool.Description)
			}
		})
	}
}

// TestToonEncodedAny guards the token-metrics recount trigger (Codex R2):
// OutputTokens must be recounted from the final response iff at least one
// block was actually re-encoded.
func TestToonEncodedAny(t *testing.T) {
	if toonEncodedAny(nil) {
		t.Fatal("nil decisions must not report encoded")
	}
	if toonEncodedAny([]toonenc.Decision{
		{Outcome: toonenc.OutcomePassthroughNotTabular},
		{Outcome: toonenc.OutcomePassthroughBelowThreshold},
	}) {
		t.Fatal("pure passthrough decisions must not report encoded")
	}
	if !toonEncodedAny([]toonenc.Decision{
		{Outcome: toonenc.OutcomePassthroughNotTabular},
		{Outcome: toonenc.OutcomeEncoded},
	}) {
		t.Fatal("any encoded block must report encoded")
	}
}
