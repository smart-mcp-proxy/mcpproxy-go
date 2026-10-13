package server

import (
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/toonenc"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/truncate"
)

// Encode-then-truncate ordering tests (spec 084 T028, FR-008, US3-AC2).
//
// The seam encodes BEFORE forwardContentResult truncates, so the size limit
// applies to the final rendered payload: a truncated TOON payload keeps the
// marker + decode hint at its head and carries the standard truncation notice
// at its tail. When the truncator's actual retained-prefix budget cannot hold
// marker + one data row, the block passes through and truncation behaves
// exactly as today — in EVERY mode (FR-009 precedence).

// runSeamThenForward mirrors handleCallToolVariant's ordering:
// encodeToonBlocks → forwardContentResult (mcp.go ~2124-2129).
func runSeamThenForward(p *MCPProxyServer, result *mcp.CallToolResult) (decisions []toonenc.Decision, finalText string, wasTruncated bool) {
	_, decisions = p.encodeToonBlocks("srv", "tool", contracts.ContentTrustTrusted, nil, result)
	forwarded, response, wasTruncated := forwardContentResult(result, p.currentTruncator(), nil, nil, "srv:tool", nil)
	_ = forwarded
	return decisions, response, wasTruncated
}

// TestToonEncodeThenTruncate_MarkerAndNoticeSurvive: an oversized tabular
// result is encoded first, then truncated — the final payload starts with the
// intact marker + hint and ends with the standard simple-truncation notice
// (encoded TOON is not valid JSON, so the truncator's simpleTruncate path
// runs, exactly what SimpleTruncateBudget models).
func TestToonEncodeThenTruncate_MarkerAndNoticeSurvive(t *testing.T) {
	const limit = 2000
	original := tabularJSON(400) // ~18KB; TOON emission comfortably exceeds the limit

	for _, mode := range []string{"adaptive", "always"} {
		t.Run(mode, func(t *testing.T) {
			p := newToonProxy(mode)
			p.setStaticTruncator(truncate.NewTruncator(limit))
			result := toonTextResult(original)

			decisions, finalText, wasTruncated := runSeamThenForward(p, result)

			require.Len(t, decisions, 1)
			require.Equal(t, toonenc.OutcomeEncoded, decisions[0].Outcome,
				"fixture must encode before truncation")
			assert.True(t, wasTruncated, "oversized encoded payload must be truncated")

			// Marker + hint intact at the head — never truncated away (FR-008).
			assert.True(t, strings.HasPrefix(finalText, toonenc.Marker+"\n"),
				"marker must survive truncation at the head of the payload")

			// Standard truncation notice intact at the tail.
			assert.True(t, strings.HasSuffix(finalText, "... [truncated by mcpproxy, cache not available]"),
				"standard truncation notice must be present and intact, got tail %q",
				finalText[len(finalText)-80:])

			// The final rendered payload honours the limit.
			assert.LessOrEqual(t, len(finalText), limit,
				"truncation must apply to the final rendered payload")
		})
	}
}

// TestToonEncodeThenTruncate_BudgetBoundary (finding 2): the too-small guard
// keys off the truncator's ACTUAL retained-prefix budget
// (SimpleTruncateBudget = limit - min(200, limit/2)), not the raw limit. At
// budget = len(Marker)+1+MinToonRowBytes-1 the block passes through in every
// mode; one byte more and encoding proceeds.
func TestToonEncodeThenTruncate_BudgetBoundary(t *testing.T) {
	original := tabularJSON(100)
	guard := len(toonenc.Marker) + 1 + toonenc.MinToonRowBytes

	// For limits >= 200 the retained budget is limit-200 (messageSpace is the
	// fixed 200), so these limits pin the budget exactly one below / exactly
	// at the guard.
	limitBelow := guard - 1 + 200
	limitAt := guard + 200
	require.GreaterOrEqual(t, limitBelow, 200, "test premise: messageSpace must be the fixed 200")
	require.Equal(t, guard-1, truncate.NewTruncator(limitBelow).SimpleTruncateBudget())
	require.Equal(t, guard, truncate.NewTruncator(limitAt).SimpleTruncateBudget())

	for _, mode := range []string{"adaptive", "always"} {
		t.Run(mode+"/just below guard passes through", func(t *testing.T) {
			p := newToonProxy(mode)
			p.setStaticTruncator(truncate.NewTruncator(limitBelow))
			result := toonTextResult(original)

			decisions, finalText, _ := runSeamThenForward(p, result)

			require.Len(t, decisions, 1)
			assert.Equal(t, toonenc.OutcomePassthroughBelowThreshold, decisions[0].Outcome,
				"a budget below marker+one-row must force passthrough in %s mode", mode)
			assert.NotContains(t, finalText, toonenc.Marker,
				"passthrough must carry no marker")
			// Truncation then behaves exactly as today, on the original JSON.
			offP := newToonProxy("off")
			offP.setStaticTruncator(truncate.NewTruncator(limitBelow))
			offResult := toonTextResult(original)
			_, offText, offTruncated := runSeamThenForward(offP, offResult)
			assert.True(t, offTruncated)
			// The JSON cache-path banner embeds a timestamped cache key, so
			// compare the retained prefix (identical) rather than full bytes.
			assert.Equal(t, offText[:100], finalText[:100],
				"passthrough truncation must behave exactly as the off path")
		})

		t.Run(mode+"/at guard encodes", func(t *testing.T) {
			p := newToonProxy(mode)
			p.setStaticTruncator(truncate.NewTruncator(limitAt))
			result := toonTextResult(original)

			decisions, finalText, _ := runSeamThenForward(p, result)

			require.Len(t, decisions, 1)
			assert.Equal(t, toonenc.OutcomeEncoded, decisions[0].Outcome,
				"a budget at marker+one-row must allow encoding in %s mode", mode)
			assert.True(t, strings.HasPrefix(finalText, toonenc.Marker+"\n"))
		})
	}
}

// TestToonEncodeThenTruncate_UnlimitedNoTruncation: with truncation disabled
// (limit 0) the guard never fires and the full encoded emission is forwarded
// untouched.
func TestToonEncodeThenTruncate_UnlimitedNoTruncation(t *testing.T) {
	p := newToonProxy("adaptive")
	p.setStaticTruncator(truncate.NewTruncator(0))
	result := toonTextResult(tabularJSON(100))

	decisions, finalText, wasTruncated := runSeamThenForward(p, result)

	require.Len(t, decisions, 1)
	assert.Equal(t, toonenc.OutcomeEncoded, decisions[0].Outcome)
	assert.False(t, wasTruncated)
	assert.True(t, strings.HasPrefix(finalText, toonenc.Marker+"\n"))
	assert.Equal(t, decisions[0].EncodedEmissionBytes, len(finalText))
}

// TestToonEncodeThenTruncate_NilTruncator: a nil truncator (some unit paths)
// means unlimited — the seam passes retainedBudget 0 and encoding proceeds.
func TestToonEncodeThenTruncate_NilTruncator(t *testing.T) {
	p := newToonProxy("adaptive")
	p.setStaticTruncator(nil)
	result := toonTextResult(tabularJSON(100))

	_, decisions := p.encodeToonBlocks("srv", "tool", contracts.ContentTrustTrusted, nil, result)
	require.Len(t, decisions, 1)
	assert.Equal(t, toonenc.OutcomeEncoded, decisions[0].Outcome)
	assert.True(t, strings.HasPrefix(result.Content[0].(mcp.TextContent).Text, toonenc.Marker+"\n"))
}
