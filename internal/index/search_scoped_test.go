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
// The scoped assertions below drive SearchToolsScoped (T075a) — the
// filter-before-the-cut door — while the unscoped SearchTools(query, limit)
// calls stay as the CONTROL that shows the hidden server really does outrank
// the entitled one (so a green scoped result is not a fixture accident). An
// unscoped call has no entitlement input and can never be asked to surface
// the entitled hit; asserting that on SearchTools would demand a change to
// the unscoped contract SC-006 keeps byte-identical.

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

	// Control: the unscoped global top-1 cut IS the hidden server's tool —
	// the displacement shape is real.
	control, err := idx.SearchTools(scopedSeamQuery, 1)
	require.NoError(t, err)
	require.Len(t, control, 1, "the global top-1 cut must return exactly one hit")
	require.Equal(t, "b", control[0].Tool.ServerName, "fixture: the hidden server must outrank the entitled one")

	// Scoped (T075a): the caller entitled only to server "a" must see their
	// own tool at the top of a size-1 window, never the hidden server's.
	onlyA := func(server string) bool { return server == "a" }
	results, err := idx.SearchToolsScoped(scopedSeamQuery, 1, onlyA)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "a", results[0].Tool.ServerName,
		"SearchToolsScoped(query, 1, onlyA) must surface the entitled server's hit, not a hidden higher-ranked one")

	// The kept hit carries the UNFILTERED search's score: the scoped path
	// never adds a scoring clause.
	all, err := idx.SearchTools(scopedSeamQuery, 10)
	require.NoError(t, err)
	for _, r := range all {
		if r.Tool.ServerName == "a" {
			assert.Equal(t, r.Score, results[0].Score, "scoped score must equal the unscoped search's score for the same hit")
		}
	}

	// A predicate that admits nothing short-circuits to an empty, non-nil
	// result (fail closed, Spec 106 FR-004).
	none, err := idx.SearchToolsScoped(scopedSeamQuery, 1, func(string) bool { return false })
	require.NoError(t, err)
	require.NotNil(t, none)
	assert.Empty(t, none)
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

	// Control: the unscoped size-1 window is entirely a hidden hit.
	control, err := idx.SearchTools(scopedSeamQuery, 1)
	require.NoError(t, err)
	require.Len(t, control, 1)
	require.Equal(t, "b", control[0].Tool.ServerName, "fixture: 300 hidden tools must outrank the entitled one")

	// T075a, tasks.md T070a "hidden prefix longer than one page": filtering
	// to the entitled set before the ranked cut always finds "a"'s tool here,
	// however many hidden documents outrank it — the 300 hidden tools exceed
	// the 256-hit page, so this proves paging is exhaustive with no cap.
	onlyA := func(server string) bool { return server == "a" }
	results, err := idx.SearchToolsScoped(scopedSeamQuery, 1, onlyA)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "a", results[0].Tool.ServerName,
		"a caller entitled only to server \"a\" must get \"a\"'s tool even when 300 hidden-server tools outrank it")
}

// TestBleveIndex_SearchToolsScoped_AppliesUnderscoreSegmentEnhancement is a
// Spec 105 PR C review finding: SearchToolsScoped originally ran the plain
// boolean query only, silently skipping the underscore-segment enhancement
// SearchTools applies for identifier-style queries (bleve.go
// underscoreSegmentQuery / augmentedToolSearchQuery) — so a scoped caller
// searching for a real tool name like "work_upload_attachment" (segments out
// of order) could get NO result at all where an equal-limit unscoped
// SearchTools call finds it. Both entitled and wildcard-scoped callers must
// now find it too, at the SAME score SearchTools(query, limit) reports.
func TestBleveIndex_SearchToolsScoped_AppliesUnderscoreSegmentEnhancement(t *testing.T) {
	idx, err := NewBleveIndex(t.TempDir(), zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { _ = idx.Close() })

	require.NoError(t, idx.BatchIndex([]*config.ToolMetadata{
		{
			Name:        "a:work_start_task_attachment_upload",
			ServerName:  "a",
			Description: "Create a signed request for a local file.",
			ParamsJSON:  `{"type":"object","properties":{}}`,
			Hash:        "target",
		},
		{
			Name:        "b:network_upload_attachment",
			ServerName:  "b",
			Description: "Create a signed request for a remote file.",
			ParamsJSON:  `{"type":"object","properties":{}}`,
			Hash:        "substring-decoy",
		},
	}))

	const query = "work_upload_attachment"
	const limit = 10

	unscoped, err := idx.SearchTools(query, limit)
	require.NoError(t, err)
	require.NotEmpty(t, unscoped, "fixture: the unscoped segment-enhanced query must find the target")
	require.Equal(t, "a:work_start_task_attachment_upload", unscoped[0].Tool.Name)

	onlyA := func(server string) bool { return server == "a" }
	scoped, err := idx.SearchToolsScoped(query, limit, onlyA)
	require.NoError(t, err)
	require.NotEmpty(t, scoped, "a scoped caller must find the same segment-matched hit an equal-limit unscoped call finds")
	assert.Equal(t, "a:work_start_task_attachment_upload", scoped[0].Tool.Name)
	assert.Equal(t, unscoped[0].Score, scoped[0].Score, "the scoped hit must carry the identical (segment-boosted) score")

	wildcard := func(string) bool { return true }
	scopedWildcard, err := idx.SearchToolsScoped(query, limit, wildcard)
	require.NoError(t, err)
	require.NotEmpty(t, scopedWildcard)
	assert.Equal(t, unscoped[0].Score, scopedWildcard[0].Score)
}
