package server

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/toonenc"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/truncate"
)

// StructuredContent invariance tests (spec 084 T030, FR-010b): output-schema
// validation evaluates forwarded.StructuredContent, which the TOON seam never
// touches (it rewrites TextContent only) — so validation outcomes are
// identical with the feature on and off.

// tabularEnvelopeSchema accepts {"rows": [...]} — matched by the fixture's
// structured content.
const tabularEnvelopeSchema = `{"type":"object","properties":{"rows":{"type":"array"}},"required":["rows"]}`

// structuredTabularResult builds a result whose text block is the JSON
// rendering of its structured content (the common upstream pattern).
func structuredTabularResult(t *testing.T) (*mcp.CallToolResult, map[string]interface{}) {
	t.Helper()
	var rows []interface{}
	require.NoError(t, json.Unmarshal([]byte(tabularJSON(50)), &rows))
	structured := map[string]interface{}{"rows": rows}
	text, err := json.Marshal(structured)
	require.NoError(t, err)
	return &mcp.CallToolResult{
		Content:           []mcp.Content{mcp.TextContent{Type: "text", Text: string(text)}},
		StructuredContent: structured,
	}, structured
}

// TestToonStructuredContent_UnaffectedByEncoding: after the seam encodes the
// text block and forwardContentResult builds the forwarded result, the
// forwarded StructuredContent is the ORIGINAL value — and schema validation
// reaches the same verdict it reaches with the feature off.
func TestToonStructuredContent_UnaffectedByEncoding(t *testing.T) {
	for _, mode := range []string{"adaptive", "always"} {
		t.Run(mode, func(t *testing.T) {
			p := newToonProxy(mode)
			p.setStaticTruncator(truncate.NewTruncator(0))
			result, structured := structuredTabularResult(t)

			_, decisions := p.encodeToonBlocks("srv", "tool", contracts.ContentTrustTrusted, nil, result)
			require.Len(t, decisions, 1)
			require.Equal(t, toonenc.OutcomeEncoded, decisions[0].Outcome,
				"fixture text must encode (envelope over a uniform array)")

			forwarded, _, _ := forwardContentResult(result, p.currentTruncator(), nil, nil, "srv:tool", nil)
			require.NotNil(t, forwarded)

			// The text block is TOON…
			encodedText := forwarded.Content[0].(mcp.TextContent).Text
			assert.True(t, strings.HasPrefix(encodedText, toonenc.Marker+"\n"))

			// …but StructuredContent is the untouched original value.
			assert.Equal(t, structured, forwarded.StructuredContent,
				"StructuredContent must be the original structured result (FR-010b)")

			// And validation of the forwarded result matches the off-mode verdict.
			d := evaluateOutputValidation(newTestValidator(), "srv:tool", tabularEnvelopeSchema, true, true, forwarded)
			assert.Empty(t, d.decision, "conforming structured content must still pass after encoding")
		})
	}
}

// TestToonStructuredContent_ViolationStillDetected: a schema violation in the
// structured content is detected identically whether or not the text block was
// TOON-encoded — encoding can neither mask nor cause a violation.
func TestToonStructuredContent_ViolationStillDetected(t *testing.T) {
	// Schema requires rows to be an object — the fixture's array violates it.
	const violatedSchema = `{"type":"object","properties":{"rows":{"type":"object"}},"required":["rows"]}`

	verdicts := map[string]ovDecision{}
	for _, mode := range []string{"off", "always"} {
		p := newToonProxy(mode)
		p.setStaticTruncator(truncate.NewTruncator(0))
		result, _ := structuredTabularResult(t)

		p.encodeToonBlocks("srv", "tool", contracts.ContentTrustTrusted, nil, result)
		forwarded, _, _ := forwardContentResult(result, p.currentTruncator(), nil, nil, "srv:tool", nil)
		verdicts[mode] = evaluateOutputValidation(newTestValidator(), "srv:tool", violatedSchema, true, true, forwarded)
	}

	require.Equal(t, "blocked", verdicts["off"].decision, "fixture must violate the schema with the feature off")
	assert.Equal(t, verdicts["off"], verdicts["always"],
		"validation verdict must be identical with the feature on and off (FR-010b)")
}
