//go:build server

package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
)

// Spec 107 PR-C, T070a (behaviour-red).
//
// GET /api/v1/index/search (handleSearchTools, server.go:3793) is the REST
// twin of #1166's MCP retrieve_tools scoping bug. Today it:
//
//  1. calls controller.SearchTools(query, limit) — which, in production,
//     wraps bleve.go's SearchTools and sets searchReq.Size = limit
//     (bleve.go:272-275) BEFORE anything about the caller is known, so the
//     result is the top-`limit` hits across the WHOLE index;
//  2. THEN filters that already-cut slice down to servers the caller may see
//     (server.go:3820-3826, auth.IsScopedCaller + canSeeServer).
//
// Step 2 running after step 1 means a server the caller cannot see, if it
// ranks above one they can, occupies a result slot the entitled hit needed —
// the entitled hit is silently dropped, and (per fixture-oracle two-fixture
// convention, memory/project_entitlement_test_oracle.md) the caller's
// response differs depending on whether that hidden, higher-ranked server
// happens to exist at all, which the tenant caller must never be able to
// detect (FR-039 part 3, FR-041, Spec 105 "Ranking under scope").
//
// T075a (a later task in this phase) fixes this by filtering BEFORE the
// ranked cut. This file proves the defect and pins the target contract:
// every subtest below computes the desired response with a per-fixture
// derivation — exhaustive search of the corpus, filtered to the entitled
// set, THEN cut to `limit` — and asserts the door's actual response against
// it. Every scoped assertion here fails on HEAD.
//
// The fixture controller below stands in for the real SearchTools/bleve
// pipeline: it returns exactly the shape production returns today (the
// global top-`limit` slice of a pre-ranked corpus, entitlement-blind), so
// exercising handleSearchTools through it reproduces the real defect without
// depending on bleve's own scoring internals (those are covered directly,
// at the index layer, by internal/index/search_scoped_test.go).

// fixtureSearchHit is one pre-ranked corpus entry. Corpora in this file are
// always built already sorted by score descending, matching bleve's own
// deterministic tie-break (bleve.go SortBy "-_score", "_id").
type fixtureSearchHit struct {
	name       string
	serverName string
	score      float64
}

// fixtureSearchController reproduces the CURRENT production seam: SearchTools
// returns the global top-`limit` of a fixed, pre-ranked corpus, with no
// awareness of any caller. Every call is counted so a test can assert the
// index was (or, for the fail-closed case, was NOT) consulted at all.
type fixtureSearchController struct {
	*MockServerController
	corpus []fixtureSearchHit
	calls  int
}

func (c *fixtureSearchController) SearchTools(_ string, limit int) ([]map[string]interface{}, error) {
	c.calls++
	n := limit
	if n > len(c.corpus) {
		n = len(c.corpus)
	}
	out := make([]map[string]interface{}, 0, n)
	for _, h := range c.corpus[:n] {
		out = append(out, map[string]interface{}{
			"tool": map[string]interface{}{
				"name":        h.name,
				"server_name": h.serverName,
				"description": "fixture tool",
			},
			"score": h.score,
		})
	}
	return out, nil
}

// entitledOracle is the per-fixture derivation tasks.md T070a specifies:
// filter the (already score-sorted) corpus down to servers `allowed` admits,
// then cut to `limit`. This is test-only logic — the target contract for a
// future production implementation, not a production symbol.
func entitledOracle(corpus []fixtureSearchHit, allowed map[string]bool, limit int) []fixtureSearchHit {
	filtered := make([]fixtureSearchHit, 0, len(corpus))
	for _, h := range corpus {
		if allowed[h.serverName] {
			filtered = append(filtered, h)
		}
	}
	if limit < len(filtered) {
		filtered = filtered[:limit]
	}
	return filtered
}

// doScopedSearch drives GET /api/v1/index/search through the real handler
// with the given caller context and returns the decoded response.
func doScopedSearch(t *testing.T, controller ServerController, ctx context.Context, limit int) contracts.SearchToolsResponse {
	t.Helper()

	server := NewServer(controller, zaptest.NewLogger(t).Sugar(), nil)
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/index/search?q=widget&limit=%d", limit), http.NoBody)
	if ctx != nil {
		req = req.WithContext(ctx)
	}
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	var response contracts.APIResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	require.True(t, response.Success)

	payload, err := json.Marshal(response.Data)
	require.NoError(t, err)
	var typed contracts.SearchToolsResponse
	require.NoError(t, json.Unmarshal(payload, &typed))
	return typed
}

func agentCtx(allowed ...string) context.Context {
	return auth.WithAuthContext(context.Background(), &auth.AuthContext{
		Type:           auth.AuthTypeAgent,
		AllowedServers: allowed,
	})
}

func requireOracleResponse(t *testing.T, got contracts.SearchToolsResponse, want []fixtureSearchHit) {
	t.Helper()
	require.Len(t, got.Results, len(want), "result count must equal the entitled-oracle derivation")
	require.Equal(t, len(want), got.Total, "total must equal the entitled-oracle derivation's count")
	for i, h := range want {
		assert.Equal(t, h.serverName, got.Results[i].Tool.ServerName, "result[%d] server_name", i)
		assert.Equal(t, h.score, got.Results[i].Score, "result[%d] score must equal the unfiltered search's score (the scoped path never adds a scoring clause)", i)
	}
}

// TestIndexSearch_HiddenHighRankerDisplacesEntitledHit is T070a's core
// displacement case: fixture A's hidden "b" tool outranks fixture B's-and-A's
// shared "a" tool. A caller entitled only to "a" must get an IDENTICAL
// response (a's tool, total 1) from both fixtures — whether or not "b"
// exists must be undetectable. Today it is not: A's hidden top-1 slot goes to
// "b", which is then filtered out, leaving A empty while B still returns "a".
func TestIndexSearch_HiddenHighRankerDisplacesEntitledHit(t *testing.T) {
	allowed := map[string]bool{"a": true}

	corpusA := []fixtureSearchHit{
		{name: "b:widget_reader", serverName: "b", score: 10.0}, // hidden, outranks a
		{name: "a:widget_reader", serverName: "a", score: 5.0},  // entitled
	}
	corpusB := []fixtureSearchHit{
		{name: "a:widget_reader", serverName: "a", score: 5.0}, // b never existed
	}

	ctlA := &fixtureSearchController{MockServerController: &MockServerController{}, corpus: corpusA}
	ctlB := &fixtureSearchController{MockServerController: &MockServerController{}, corpus: corpusB}

	gotA := doScopedSearch(t, ctlA, agentCtx("a"), 1)
	gotB := doScopedSearch(t, ctlB, agentCtx("a"), 1)

	wantA := entitledOracle(corpusA, allowed, 1)
	wantB := entitledOracle(corpusB, allowed, 1)

	requireOracleResponse(t, gotA, wantA)
	requireOracleResponse(t, gotB, wantB)

	// Cross-fixture: since entitled matches (1) <= limit (1) in both
	// fixtures, membership must be identical regardless of whether the
	// hidden server exists (FR-039 part 3 / FR-041 non-disclosure).
	bytesA, err := json.Marshal(gotA)
	require.NoError(t, err)
	bytesB, err := json.Marshal(gotB)
	require.NoError(t, err)
	assert.JSONEq(t, string(bytesB), string(bytesA),
		"a caller entitled only to \"a\" must get a byte-identical response whether or not the hidden, higher-ranked \"b\" server exists")
}

// TestIndexSearch_LimitLargerThanEntitledPopulation covers the "limit larger
// than the entitled population" case: two entitled "a" tools, limit 5, but 10
// hidden "b" tools all outrank both. Because 10 hidden hits exceed the
// window, today's global top-5 cut is entirely hidden hits, and the entitled
// caller sees zero results even though their own 2 tools fit comfortably
// under the limit — the correct answer, per contracts/entitlement-
// predicate.md, is every entitled hit and none hidden, total = entitled
// count (2 <= limit, so cross-fixture membership equality holds and is
// asserted).
func TestIndexSearch_LimitLargerThanEntitledPopulation(t *testing.T) {
	allowed := map[string]bool{"a": true}

	var corpusA []fixtureSearchHit
	for i := 0; i < 10; i++ {
		corpusA = append(corpusA, fixtureSearchHit{name: fmt.Sprintf("b:hidden_%d", i), serverName: "b", score: 100 - float64(i)})
	}
	corpusA = append(corpusA,
		fixtureSearchHit{name: "a:widget_one", serverName: "a", score: 50},
		fixtureSearchHit{name: "a:widget_two", serverName: "a", score: 49},
	)
	corpusB := []fixtureSearchHit{
		{name: "a:widget_one", serverName: "a", score: 50},
		{name: "a:widget_two", serverName: "a", score: 49},
	}

	ctlA := &fixtureSearchController{MockServerController: &MockServerController{}, corpus: corpusA}
	ctlB := &fixtureSearchController{MockServerController: &MockServerController{}, corpus: corpusB}

	gotA := doScopedSearch(t, ctlA, agentCtx("a"), 5)
	gotB := doScopedSearch(t, ctlB, agentCtx("a"), 5)

	wantA := entitledOracle(corpusA, allowed, 5)
	wantB := entitledOracle(corpusB, allowed, 5)
	require.Len(t, wantA, 2, "precondition: entitled matches (2) must be <= limit (5) for this case")
	require.Len(t, wantB, 2, "precondition: entitled matches (2) must be <= limit (5) for this case")

	requireOracleResponse(t, gotA, wantA)
	requireOracleResponse(t, gotB, wantB)
}

// TestIndexSearch_MoreEntitledMatchesThanLimit covers "more entitled matches
// than limit": three entitled "a" tools, limit 2, plus hidden "b" tools that
// outrank all of them in fixture A only. Each fixture's response must equal
// the top-2 of ITS OWN exhaustive filtered search (per-fixture derivation),
// total 2 on both — but membership is NOT compared across fixtures here:
// with more entitled matches than the limit, which entitled hits survive the
// cut is allowed to depend on corpus-dependent scores (Spec 105 "Ranking
// under scope"), only displacement by the HIDDEN population is forbidden.
func TestIndexSearch_MoreEntitledMatchesThanLimit(t *testing.T) {
	allowed := map[string]bool{"a": true}

	entitledTools := []fixtureSearchHit{
		{name: "a:widget_one", serverName: "a", score: 30},
		{name: "a:widget_two", serverName: "a", score: 20},
		{name: "a:widget_three", serverName: "a", score: 10},
	}

	var corpusA []fixtureSearchHit
	for i := 0; i < 5; i++ {
		corpusA = append(corpusA, fixtureSearchHit{name: fmt.Sprintf("b:hidden_%d", i), serverName: "b", score: 100 - float64(i)})
	}
	corpusA = append(corpusA, entitledTools...)

	corpusB := append([]fixtureSearchHit{}, entitledTools...)

	ctlA := &fixtureSearchController{MockServerController: &MockServerController{}, corpus: corpusA}
	ctlB := &fixtureSearchController{MockServerController: &MockServerController{}, corpus: corpusB}

	gotA := doScopedSearch(t, ctlA, agentCtx("a"), 2)
	gotB := doScopedSearch(t, ctlB, agentCtx("a"), 2)

	wantA := entitledOracle(corpusA, allowed, 2)
	wantB := entitledOracle(corpusB, allowed, 2)
	require.Len(t, wantA, 2)
	require.Len(t, wantB, 2)

	// Per-fixture only — no cross-fixture comparison in this case.
	requireOracleResponse(t, gotA, wantA)
	requireOracleResponse(t, gotB, wantB)
}

// TestIndexSearch_HiddenPrefixLongerThanOnePage covers the "hidden prefix
// longer than one page" case: 300 hidden "b" tools all outrank the single
// entitled "a" hit for the query — far more than any single search page —
// and limit=1 must still return "a"'s tool with total 1, byte-identical to a
// fixture where "b" never existed. Paging to find the entitled hit is
// exhaustive with no cap (contracts/entitlement-predicate.md, tasks.md
// T070a).
func TestIndexSearch_HiddenPrefixLongerThanOnePage(t *testing.T) {
	allowed := map[string]bool{"a": true}

	var corpusA []fixtureSearchHit
	for i := 0; i < 300; i++ {
		corpusA = append(corpusA, fixtureSearchHit{name: fmt.Sprintf("b:hidden_%d", i), serverName: "b", score: 1000 - float64(i)})
	}
	corpusA = append(corpusA, fixtureSearchHit{name: "a:widget_reader", serverName: "a", score: 5})
	corpusB := []fixtureSearchHit{
		{name: "a:widget_reader", serverName: "a", score: 5},
	}

	ctlA := &fixtureSearchController{MockServerController: &MockServerController{}, corpus: corpusA}
	ctlB := &fixtureSearchController{MockServerController: &MockServerController{}, corpus: corpusB}

	gotA := doScopedSearch(t, ctlA, agentCtx("a"), 1)
	gotB := doScopedSearch(t, ctlB, agentCtx("a"), 1)

	wantA := entitledOracle(corpusA, allowed, 1)
	wantB := entitledOracle(corpusB, allowed, 1)
	require.Len(t, wantA, 1)
	require.Len(t, wantB, 1)

	requireOracleResponse(t, gotA, wantA)
	requireOracleResponse(t, gotB, wantB)

	bytesA, err := json.Marshal(gotA)
	require.NoError(t, err)
	bytesB, err := json.Marshal(gotB)
	require.NoError(t, err)
	assert.JSONEq(t, string(bytesB), string(bytesA),
		"the entitled caller's response must be byte-identical whether or not 300 higher-ranked hidden tools exist")
}

// TestIndexSearch_EmptyEntitlementFailsClosedWithoutIndexSearch: an agent
// token with an empty (non-nil) AllowedServers list — deny-all, per the
// security invariants in the dispatch prompt and Spec 106 FR-004 — must get
// results: [], total: 0 WITHOUT ever consulting the index: there is nothing
// this caller could possibly be entitled to, so calling SearchTools at all
// is unnecessary index I/O. Today the handler always calls
// controller.SearchTools regardless of the caller's entitlement.
func TestIndexSearch_EmptyEntitlementFailsClosedWithoutIndexSearch(t *testing.T) {
	corpus := []fixtureSearchHit{
		{name: "a:widget_reader", serverName: "a", score: 5},
	}
	ctl := &fixtureSearchController{MockServerController: &MockServerController{}, corpus: corpus}

	got := doScopedSearch(t, ctl, agentCtx(), 10) // AllowedServers: [] (empty, non-nil)

	assert.Empty(t, got.Results, "empty entitlement must return no results")
	assert.Equal(t, 0, got.Total)
	assert.Equal(t, 0, ctl.calls, "empty entitlement must fail closed before any index search — SearchTools must not be called")
}

// TestIndexSearch_AdminCallerUnaffected pins SC-006: an administrator (or any
// caller with no AuthContext at all, e.g. the personal-edition API-key path)
// must see exactly what SearchTools(query, limit) returns, untouched — the
// scoped filter must never engage for them. This already holds on HEAD; it
// is included so a future scoped implementation cannot regress it silently.
func TestIndexSearch_AdminCallerUnaffected(t *testing.T) {
	corpus := []fixtureSearchHit{
		{name: "b:hidden_tool", serverName: "b", score: 10},
		{name: "a:widget_reader", serverName: "a", score: 5},
	}
	ctl := &fixtureSearchController{MockServerController: &MockServerController{}, corpus: corpus}

	adminCtx := auth.WithAuthContext(context.Background(), auth.AdminContext())
	got := doScopedSearch(t, ctl, adminCtx, 2)

	require.Len(t, got.Results, 2)
	assert.Equal(t, "b", got.Results[0].Tool.ServerName)
	assert.Equal(t, "a", got.Results[1].Tool.ServerName)
	assert.Equal(t, 2, got.Total)
}
