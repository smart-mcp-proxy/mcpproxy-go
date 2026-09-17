package index

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Spec 107 PR-C, T070a (behaviour-red).
//
// entitled-server filtering for the ranked-window search door
// (GET /api/v1/index/search, and its planned MCP twin) does not live in this
// package today — SearchTools has no notion of "which server may the caller
// see". The REST handler (internal/httpapi/server.go:handleSearchTools)
// currently calls SearchTools(query, limit) — which the loop below shows
// already applies the global top-`limit` cut across the WHOLE corpus,
// oblivious to any caller — and THEN filters the (already cut) result set by
// caller scope. When a server the caller cannot see ranks above one they can,
// its hits occupy the limited slots before the caller-aware filter ever runs,
// so the caller loses a result they were entitled to see (contracts/
// entitlement-predicate.md, spec.md FR-039 part 3 / FR-041).
//
// This file pins that seam directly against the real bleve engine, one layer
// below the HTTP door exercised by internal/httpapi/index_search_scoped_test.go:
// it proves that SearchTools(query, limit) alone — the only tool available to
// the door today — cannot answer "the top hit(s) among servers this caller
// may see" once a hidden server outranks an entitled one, even though the
// entitled hit is fully present in the index and reachable by an exhaustive,
// unlimited scan. T075a closes this by filtering before the ranked cut
// (scoped, paginated search) rather than after it.
//
// Every assertion below is against the CURRENT, unscoped SearchTools API: a
// future scoped implementation changes call shape, not this function's
// existing contract, so these are legitimate target assertions on today's
// symbol, not requests for a symbol that does not exist yet.

const scopedSeamQuery = "gizmo"

// buildScopedSeamCorpus indexes one tool on server "a" (the entitled server)
// whose description mentions the query term once, plus `hiddenCount` tools on
// server "b" (the hidden server) whose descriptions repeat the query term
// heavily. BM25 rewards term frequency: repeating the term keeps every "b"
// tool's score well above "a"'s single-occurrence tool regardless of corpus
// size (a plain prefix- or DF-based signal was tried first and, as corpus
// size grows, its score collapses relative to "a"'s from query-norm/IDF
// dilution — measured empirically; TF repetition is the stable choice here),
// so this reliably reproduces the exact "hidden high-ranker" shape
// contracts/entitlement-predicate.md and tasks.md T070a describe, at any
// scale.
func buildScopedSeamCorpus(t *testing.T, hiddenCount int) *BleveIndex {
	t.Helper()

	idx, err := NewBleveIndex(t.TempDir(), zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { _ = idx.Close() })

	entitled := &config.ToolMetadata{
		Name:        "a:status_reader",
		ServerName:  "a",
		Description: "gizmo status check",
		ParamsJSON:  "{}",
		Hash:        "seam-entitled",
	}
	require.NoError(t, idx.IndexTool(entitled))

	hidden := make([]*config.ToolMetadata, 0, hiddenCount)
	for i := 0; i < hiddenCount; i++ {
		hidden = append(hidden, &config.ToolMetadata{
			Name:        fmt.Sprintf("b:tool_%d", i),
			ServerName:  "b",
			Description: "gizmo gizmo gizmo gizmo gizmo gizmo gizmo gizmo gizmo gizmo",
			ParamsJSON:  "{}",
			Hash:        fmt.Sprintf("seam-hidden-%d", i),
		})
	}
	require.NoError(t, idx.BatchIndex(hidden))

	return idx
}

// TestBleveIndex_SearchTools_HiddenHighRankerDisplacesEntitledHit is the
// small-scale version of the T070a displacement case: one hidden server ("b")
// with a single tool that outranks the entitled server's ("a") only matching
// tool. SearchTools(query, 1) is the exact call shape the REST door makes
// today (bleve.go:272-275 sets searchReq.Size = limit before any caller-scope
// filtering exists), so requesting the top-1 hit for a caller entitled only
// to "a" should surface "a"'s tool — it does not, because SearchTools cannot
// take entitlement into account at all.
func TestBleveIndex_SearchTools_HiddenHighRankerDisplacesEntitledHit(t *testing.T) {
	idx := buildScopedSeamCorpus(t, 1)

	results, err := idx.SearchTools(scopedSeamQuery, 1)
	require.NoError(t, err)
	require.Len(t, results, 1, "the global top-1 cut must return exactly one hit")

	// Desired end state (T075a): the caller entitled only to server "a" must
	// see their own tool at the top of a size-1 window, never the hidden
	// server's. This fails today: SearchTools has no entitlement input, so
	// the hidden "b" tool (prefix-boosted to outrank "a") wins the cut.
	assert.Equal(t, "a", results[0].Tool.ServerName,
		"SearchTools(query, 1) must surface the entitled server's hit, not a hidden higher-ranked one — "+
			"today it cannot, because the global top-K cut runs before any caller-scope filter exists")
}

// TestBleveIndex_SearchTools_HiddenPrefixLongerThanOnePage is the same
// displacement shape at the scale tasks.md T070a calls out explicitly: 300
// hidden "b" tools all outrank the single entitled "a" tool, so even a
// generously large single-page request (well above bleve's default
// searchPageSize windows used elsewhere in this package, e.g.
// GetToolsByServer) is entirely consumed by hidden hits before any
// entitlement filter could apply. A caller entitled to "a" and asking for the
// single best result they can see must get "a"'s tool — proving this
// requires filtering the corpus BEFORE the ranked cut, i.e. the exhaustive,
// paginated scan-then-filter-then-cut shape T075a is expected to add
// (mirroring the existing GetToolsByServer pagination pattern in this file),
// not a one-shot SearchTools(query, limit) call.
func TestBleveIndex_SearchTools_HiddenPrefixLongerThanOnePage(t *testing.T) {
	const hiddenCount = 300
	idx := buildScopedSeamCorpus(t, hiddenCount)

	// Sanity: the entitled tool is genuinely present and discoverable by an
	// exhaustive scan — this is what makes the assertion below a real defect
	// rather than a fixture mistake. Without this, "a" not appearing in a
	// size-1 result could just mean it was never indexed.
	all, err := idx.SearchTools(scopedSeamQuery, hiddenCount+1)
	require.NoError(t, err)
	require.Len(t, all, hiddenCount+1, "an unlimited scan must surface every matching document, entitled and hidden alike")
	foundEntitledSomewhere := false
	for _, r := range all {
		if r.Tool.ServerName == "a" {
			foundEntitledSomewhere = true
			break
		}
	}
	require.True(t, foundEntitledSomewhere, "the entitled tool must exist in the corpus for this test to mean anything")

	results, err := idx.SearchTools(scopedSeamQuery, 1)
	require.NoError(t, err)
	require.Len(t, results, 1)

	// Desired end state (T075a, tasks.md T070a "hidden prefix longer than one
	// page"): filtering to the entitled set before the ranked cut always
	// finds "a"'s tool here, however many hidden documents outrank it. Today
	// it does not — the 300 hidden tools alone fill the size-1 window.
	assert.Equal(t, "a", results[0].Tool.ServerName,
		"a caller entitled only to server \"a\" must get \"a\"'s tool even when 300 hidden-server tools outrank it — "+
			"a single-page SearchTools(query, limit) call cannot guarantee this without filtering first")
}
