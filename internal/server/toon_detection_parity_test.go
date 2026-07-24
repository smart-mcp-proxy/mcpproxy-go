package server

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/toonenc"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/truncate"
)

// Detection FINDING-parity tests (spec 084 T027, FR-007b, SC-004, US3-AC1).
//
// The tested guarantee is *finding-set* parity, NOT input-byte parity
// (data-model.md §7): the sensitive-data detector must produce the same
// findings whether it scans the feature-off pipeline's response text or the
// seam's reconstructed detection_text. Raw bytes legitimately differ by the
// truncation banner's timestamped cache key, which carries no upstream data.

// parityDetector builds the same detector runAsyncDetection uses.
func parityDetector() *security.Detector {
	cfg := config.DefaultSensitiveDataDetectionConfig()
	cfg.Enabled = true
	cfg.ScanResponses = true
	return security.NewDetector(cfg)
}

// findingSet scans text as a tool response and returns the detection set in a
// canonical order: {type, category, severity, location, is_likely_example}
// per finding — the identity FR-007b requires to be equal on and off.
func findingSet(det *security.Detector, text string) []security.Detection {
	res := det.Scan("", text)
	out := append([]security.Detection(nil), res.Detections...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Type != out[j].Type {
			return out[i].Type < out[j].Type
		}
		return out[i].Location < out[j].Location
	})
	return out
}

// offPathScanText reproduces the exact scan input the feature-off pipeline
// hands the detector: forwardContentResult (per-block truncation + non-text
// placeholders) → spotlightForwarded → forwardedText — the same sequence
// handleCallToolVariant runs at mcp.go ~2129-2141 when the seam returns
// ("", nil). The result is mutated the same way the real pipeline mutates it.
func offPathScanText(p *MCPProxyServer, serverName, toolName, contentTrust string, args map[string]interface{}, result *mcp.CallToolResult) string {
	forwarded, response, _ := forwardContentResult(result, p.currentTruncator(), nil, nil, serverName+":"+toolName, args)
	p.spotlightForwarded(serverName, toolName, contentTrust, forwarded)
	return forwardedText(forwarded, response)
}

// secretTable renders a uniform tabular JSON array with the secret embedded in
// the note field of row secretRow. Rows are padded so the table is comfortably
// tabular-classified and TOON-encodable above the default threshold.
func secretTable(rows, secretRow int, secret string) string {
	items := make([]map[string]interface{}, 0, rows)
	for i := 0; i < rows; i++ {
		note := fmt.Sprintf("routine log line %d with ordinary padding text", i)
		if i == secretRow {
			note = "credential leaked here: " + secret
		}
		items = append(items, map[string]interface{}{
			"id":     i,
			"name":   fmt.Sprintf("user-%d", i),
			"email":  fmt.Sprintf("user-%d@example.com", i),
			"active": i%2 == 0,
			"note":   note,
		})
	}
	b, _ := json.Marshal(items)
	return string(b)
}

// assertFindingParity runs one fixture through both pipelines under the given
// mode and asserts the finding sets are equal. Returns the decisions so the
// caller can also assert the agent-facing response actually diverged (i.e. the
// parity check is non-vacuous).
func assertFindingParity(t *testing.T, p *MCPProxyServer, mode, serverName, toolName, contentTrust string, args map[string]interface{}, mkResult func() *mcp.CallToolResult, wantDetected bool) []toonenc.Decision {
	t.Helper()
	det := parityDetector()

	// OFF pipeline: feature-off scan input.
	offCfg := p.config
	p.config = cloneConfigWithToonMode(offCfg, "off")
	offText := offPathScanText(p, serverName, toolName, contentTrust, args, mkResult())
	p.config = offCfg

	// ON pipeline: the seam's reconstructed detection_text.
	p.config = cloneConfigWithToonMode(offCfg, mode)
	detText, decisions := p.encodeToonBlocks(serverName, toolName, contentTrust, args, mkResult())
	p.config = offCfg

	require.NotEmpty(t, detText, "mode %s must supply a detection_text", mode)

	offFindings := findingSet(det, offText)
	onFindings := findingSet(det, detText)
	assert.Equal(t, offFindings, onFindings,
		"mode %s: detection finding sets must be identical with the feature on and off", mode)

	if wantDetected {
		require.NotEmpty(t, offFindings, "fixture secret must be detectable on the off path (otherwise parity is vacuous)")
		require.NotEmpty(t, onFindings, "fixture secret must be caught identically on the TOON path")
	} else {
		assert.Empty(t, offFindings, "fixture must be clean on the off path")
		assert.Empty(t, onFindings, "fixture must be clean on the TOON path")
	}
	return decisions
}

// cloneConfigWithToonMode returns a shallow copy of cfg with ToonOutput set —
// so a test can flip modes without mutating the shared fixture config.
func cloneConfigWithToonMode(cfg *config.Config, mode string) *config.Config {
	cp := *cfg
	cp.ToonOutput = mode
	return &cp
}

// TestToonDetectionParity_SecurityCorpus (SC-004): for every detectable secret
// class in the sensitive-data corpus embedded in a within-limit tabular
// payload, the finding sets are identical in adaptive and always mode — while
// the agent-facing block IS TOON-encoded (the response diverged, the findings
// did not).
func TestToonDetectionParity_SecurityCorpus(t *testing.T) {
	corpus := []struct {
		name   string
		secret string
	}{
		{"aws_access_key", "AKIA1234567890ABCDEF"},
		{"github_pat", "ghp_1234567890abcdefghijABCDEFGHIJ123456"},
		{"postgres_connection", "postgresql://user:password123@localhost:5432/mydb"},
		{"credit_card", "Card: 4111111111111111"},
		{"jwt_token", "Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U"},
	}

	for _, tc := range corpus {
		for _, mode := range []string{"adaptive", "always"} {
			t.Run(tc.name+"/"+mode, func(t *testing.T) {
				p := newToonProxy("off")
				payload := secretTable(30, 3, tc.secret)
				mkResult := func() *mcp.CallToolResult { return toonTextResult(payload) }

				decisions := assertFindingParity(t, p, mode, "srv", "tool", contracts.ContentTrustTrusted, nil, mkResult, true)

				// Non-vacuous: the agent-facing block was actually encoded.
				require.Len(t, decisions, 1)
				assert.Equal(t, toonenc.OutcomeEncoded, decisions[0].Outcome,
					"corpus fixture must encode so the parity check exercises a diverged response")
			})
		}
	}
}

// TestToonDetectionParity_MixedContent (finding 6): a secret in a text block
// NEXT TO an image block is scanned identically — the detection_text is an
// all-blocks rendering with the same placeholder rules the off path uses, not
// a text-only walk.
func TestToonDetectionParity_MixedContent(t *testing.T) {
	const secret = "AKIA1234567890ABCDEF"
	p := newToonProxy("off")
	mkResult := func() *mcp.CallToolResult {
		return &mcp.CallToolResult{Content: []mcp.Content{
			mcp.TextContent{Type: "text", Text: "log line with " + secret},
			mcp.ImageContent{Type: "image", MIMEType: "image/png", Data: "aGVsbG8="},
			mcp.TextContent{Type: "text", Text: secretTable(30, -1, "")},
		}}
	}

	for _, mode := range []string{"adaptive", "always"} {
		t.Run(mode, func(t *testing.T) {
			decisions := assertFindingParity(t, p, mode, "srv", "tool", contracts.ContentTrustTrusted, nil, mkResult, true)
			require.Len(t, decisions, 2)
			assert.Equal(t, toonenc.OutcomeEncoded, decisions[1].Outcome,
				"the tabular block beside the image must encode")
		})
	}
}

// TestToonDetectionParity_OverLimit (issue 5): an over-limit result is
// truncated in the detection_text with the SAME truncator budget the off path
// uses, so parity holds past the limit in both directions: a secret that
// survives truncation is found on both paths; a secret past the retained
// budget is dropped on both paths. The raw texts differ (timestamped cache-key
// banner) — the finding sets must not.
func TestToonDetectionParity_OverLimit(t *testing.T) {
	const secret = "AKIA1234567890ABCDEF"

	t.Run("secret survives truncation", func(t *testing.T) {
		p := newToonProxy("off")
		p.setStaticTruncator(truncate.NewTruncator(1500))
		payload := secretTable(40, 0, secret) // ~5KB, secret in the first row
		require.Greater(t, len(payload), 1500, "fixture must exceed the limit")
		mkResult := func() *mcp.CallToolResult { return toonTextResult(payload) }

		for _, mode := range []string{"adaptive", "always"} {
			assertFindingParity(t, p, mode, "srv", "tool", contracts.ContentTrustTrusted, nil, mkResult, true)
		}
	})

	t.Run("secret truncated away on both paths", func(t *testing.T) {
		p := newToonProxy("off")
		p.setStaticTruncator(truncate.NewTruncator(1500))
		payload := secretTable(40, 39, secret) // secret in the last row, past the budget
		require.Greater(t, len(payload), 1500, "fixture must exceed the limit")
		mkResult := func() *mcp.CallToolResult { return toonTextResult(payload) }

		for _, mode := range []string{"adaptive", "always"} {
			assertFindingParity(t, p, mode, "srv", "tool", contracts.ContentTrustTrusted, nil, mkResult, false)
		}
	})
}

// TestToonDetectionParity_UntrustedSpotlight (round-3): with spotlight enabled
// and untrusted content, the seam's detection_text carries the same delimiter
// framing spotlightForwarded produces on the off path — the finding set is
// unchanged by the framing.
func TestToonDetectionParity_UntrustedSpotlight(t *testing.T) {
	const secret = "AKIA1234567890ABCDEF"
	p := newToonProxy("off")
	sanCfg := config.DefaultOutputSanitisationConfig()
	sanCfg.SpotlightUntrusted = true
	p.config.OutputSanitisation = sanCfg

	payload := secretTable(30, 3, secret)
	mkResult := func() *mcp.CallToolResult { return toonTextResult(payload) }

	for _, mode := range []string{"adaptive", "always"} {
		t.Run(mode, func(t *testing.T) {
			assertFindingParity(t, p, mode, "srv", "tool", contracts.ContentTrustUntrusted, nil, mkResult, true)
		})
	}
}

// TestToonDetectionParity_OffLeavesDetectionTextEmpty (issue 2): with the
// feature off the seam supplies NO detection_text — the detector keeps
// scanning today's response string, byte-for-byte unchanged.
func TestToonDetectionParity_OffLeavesDetectionTextEmpty(t *testing.T) {
	p := newToonProxy("off")
	payload := secretTable(30, 3, "AKIA1234567890ABCDEF")
	result := toonTextResult(payload)

	detText, decisions := p.encodeToonBlocks("srv", "tool", contracts.ContentTrustTrusted, nil, result)
	assert.Empty(t, detText, "off mode must leave detection_text empty (detector falls back to response)")
	assert.Nil(t, decisions)
	assert.Equal(t, payload, result.Content[0].(mcp.TextContent).Text, "off mode must not touch the block")
}

// TestToonDetectionParity_EncodedResponseDiffersFromScanInput sanity-checks
// the premise of the whole exercise: in adaptive mode the agent-facing bytes
// are TOON while the scan input is the pre-encoding text — i.e. without
// detection_text the detector would be scanning different bytes.
func TestToonDetectionParity_EncodedResponseDiffersFromScanInput(t *testing.T) {
	p := newToonProxy("adaptive")
	payload := secretTable(30, 3, "AKIA1234567890ABCDEF")
	result := toonTextResult(payload)

	detText, decisions := p.encodeToonBlocks("srv", "tool", contracts.ContentTrustTrusted, nil, result)
	require.Len(t, decisions, 1)
	require.Equal(t, toonenc.OutcomeEncoded, decisions[0].Outcome)

	encoded := result.Content[0].(mcp.TextContent).Text
	assert.True(t, strings.HasPrefix(encoded, toonenc.Marker+"\n"))
	assert.NotEqual(t, encoded, detText, "scan input must be the pre-encoding text, not the TOON emission")
	assert.Equal(t, payload, detText)
}
