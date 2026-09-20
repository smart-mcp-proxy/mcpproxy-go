package index

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Spec 105 PR C, T069, research.md D6.
//
// SearchToolsScoped (Spec 107 T075a) already proves it surfaces the entitled
// hit a plain Search(query, limit) would displace (search_scoped_test.go).
// D6 asks for a stronger, independent guarantee: SearchScoped's ordered
// (id, score) result must be PROVABLY EQUAL to the FR-005 reference
// derivation — an unchanged, unfiltered Search over the whole selected
// corpus (Size = document count), filtered to the authorized ids, then cut
// to the requested limit — not merely "returns a plausible entitled hit".
// Anything else (a capped oversize scan, a boosted-query approximation) could
// drift from the reference derivation without any test noticing. This file
// is that proof, at both a small hand-built scale and the 527-tool
// LiveMCPBench snapshot the fleet-scale specs already use.
//
// Multi-word queries only (see queries below): the underscore-segment
// enhancement SearchTools applies for identifier-style queries
// (bleve.go:underscoreSegmentQuery) is adaptive on the FIRST unfiltered
// window's own hits — a decision that has no well-defined generalisation to
// an arbitrary scoped/exhaustive comparison, and SearchToolsScoped
// deliberately does not attempt to replicate it (buildToolSearchQuery is the
// one query both paths run — see its doc comment in bleve.go). That is a
// pre-existing property of the unscoped ranking heuristic, orthogonal to the
// FR-005 G1 scope-before-cut gap this PR closes, so this equality proof is
// stated for the query shapes FR-005's own fixture uses: single- and
// multi-word text queries.

// exhaustiveScopedDerivation is the FR-005 reference algorithm, spelled out
// directly against the unscoped Search API so it cannot share a code path
// with the SearchToolsScoped implementation it is verifying: search the
// WHOLE selected corpus (Size = every document), filter to the ids inScope
// admits, keep the ranked order Search already returned, then cut to limit.
func exhaustiveScopedDerivation(t *testing.T, m *Manager, query string, limit int, inScope func(string) bool) []*config.SearchResult {
	t.Helper()

	docCount, err := m.GetDocumentCount()
	require.NoError(t, err)

	all, err := m.SearchTools(query, int(docCount))
	require.NoError(t, err)

	filtered := make([]*config.SearchResult, 0, limit)
	for _, r := range all {
		if !inScope(r.Tool.ServerName) {
			continue
		}
		filtered = append(filtered, r)
		if len(filtered) >= limit {
			break
		}
	}
	return filtered
}

// idScorePairs projects a result slice onto the ordered (id, score) pairs D6
// asks the two derivations to agree on.
type idScorePair struct {
	ID    string
	Score float64
}

func idScorePairs(results []*config.SearchResult) []idScorePair {
	out := make([]idScorePair, 0, len(results))
	for _, r := range results {
		out = append(out, idScorePair{ID: r.Tool.Name, Score: r.Score})
	}
	return out
}

// assertScopedMatchesExhaustive runs both derivations for one (query, limit,
// inScope) case and asserts their ordered (id, score) lists are identical.
func assertScopedMatchesExhaustive(t *testing.T, m *Manager, query string, limit int, inScope func(string) bool) {
	t.Helper()

	scoped, err := m.SearchToolsScoped(query, limit, inScope)
	require.NoError(t, err)

	want := exhaustiveScopedDerivation(t, m, query, limit, inScope)

	require.Equal(t, idScorePairs(want), idScorePairs(scoped),
		"SearchScoped(%q, %d) must be ordered-(id,score)-identical to the exhaustive Search+filter+cut derivation (D6)", query, limit)
}

// TestSearchScoped_ScoreIdenticalToExhaustiveFilter_SmallFixture is the
// hand-built, easy-to-audit scale: one entitled server with a single
// low-frequency hit and a hidden server whose tools repeat the query terms
// heavily (the same shape search_scoped_test.go's buildScopedSeamCorpus
// uses), queried with both a single- and a multi-word query.
func TestSearchScoped_ScoreIdenticalToExhaustiveFilter_SmallFixture(t *testing.T) {
	m, err := NewManager(t.TempDir(), zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { _ = m.Close() })

	require.NoError(t, m.BatchIndexTools([]*config.ToolMetadata{
		{Name: "a:rotate_keys", ServerName: "a", Description: "rotate keys for a service account", ParamsJSON: "{}", Hash: "h1"},
		{Name: "b:one", ServerName: "b", Description: "rotate keys rotate keys rotate keys rotate keys", ParamsJSON: "{}", Hash: "h2"},
		{Name: "b:two", ServerName: "b", Description: "rotate keys rotate keys rotate keys", ParamsJSON: "{}", Hash: "h3"},
		{Name: "c:other", ServerName: "c", Description: "an unrelated weather forecast tool", ParamsJSON: "{}", Hash: "h4"},
	}))

	onlyA := func(server string) bool { return server == "a" }
	allServers := func(string) bool { return true }

	for _, tc := range []struct {
		name  string
		query string
		limit int
		scope func(string) bool
	}{
		{"single word, narrow scope, limit 1", "keys", 1, onlyA},
		{"multi word, narrow scope, limit 1", "rotate keys", 1, onlyA},
		{"multi word, narrow scope, limit larger than hits", "rotate keys", 10, onlyA},
		{"multi word, wildcard scope", "rotate keys", 10, allServers},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertScopedMatchesExhaustive(t, m, tc.query, tc.limit, tc.scope)
		})
	}
}

// TestSearchScoped_ScoreIdenticalToExhaustiveFilter_527ToolSnapshot is the
// fleet-scale proof: the same LiveMCPBench 527-tool corpus the Spec 102/083
// token-budget tests use, scoped to a small, real-server subset, across
// several query shapes.
func TestSearchScoped_ScoreIdenticalToExhaustiveFilter_527ToolSnapshot(t *testing.T) {
	tools, servers := loadLiveMCPBenchSnapshotForScoping(t)
	require.GreaterOrEqual(t, len(servers), 4, "fixture: the snapshot must name at least a handful of distinct servers")

	m, err := NewManager(t.TempDir(), zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { _ = m.Close() })
	require.NoError(t, m.BatchIndexTools(tools))

	// A small, deterministic authorized subset: every 5th distinct server
	// name, so the scope is a real minority of the fleet rather than
	// everything or nothing.
	authorized := make(map[string]bool, len(servers)/5+1)
	for i, s := range servers {
		if i%5 == 0 {
			authorized[s] = true
		}
	}
	require.NotEmpty(t, authorized)
	inScope := func(server string) bool { return authorized[server] }

	for _, tc := range []struct {
		name  string
		query string
		limit int
	}{
		{"single word, limit 1", "search", 1},
		{"single word, limit 5", "search", 5},
		{"multi word, limit 1", "search web results", 1},
		{"multi word, limit 20", "get current data", 20},
		{"multi word, large limit", "list all available", 100},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertScopedMatchesExhaustive(t, m, tc.query, tc.limit, inScope)
		})
	}
}

// loadLiveMCPBenchSnapshotForScoping reads the 527-tool LiveMCPBench snapshot
// used by the Spec 102/083 fleet-scale tests (internal/server's copy carries
// its own loader; this package needs its own because tests never share
// unexported helpers across packages). Returns the indexable tools plus the
// distinct, order-stable list of server names present.
func loadLiveMCPBenchSnapshotForScoping(t *testing.T) ([]*config.ToolMetadata, []string) {
	t.Helper()

	const path = "../../specs/083-discovery-profiler/datasets/livemcptool_snapshot/tools.json"
	raw, err := os.ReadFile(path)
	require.NoError(t, err, "the LiveMCPBench snapshot must be present")

	var corpus struct {
		ToolCount int `json:"tool_count"`
		Tools     []struct {
			Server      string          `json:"server"`
			Tool        string          `json:"tool"`
			Description string          `json:"description"`
			InputSchema json.RawMessage `json:"inputSchema"`
		} `json:"tools"`
	}
	require.NoError(t, json.Unmarshal(raw, &corpus))
	require.Len(t, corpus.Tools, 527, "the fleet-scale fixture is stated against the 527-tool snapshot")

	tools := make([]*config.ToolMetadata, 0, len(corpus.Tools))
	seen := make(map[string]bool)
	var servers []string
	for _, tool := range corpus.Tools {
		params := string(tool.InputSchema)
		if params == "" || params == "null" {
			params = `{"type":"object"}`
		}
		tools = append(tools, &config.ToolMetadata{
			ServerName:  tool.Server,
			Name:        tool.Server + ":" + tool.Tool,
			Description: tool.Description,
			ParamsJSON:  params,
			Hash:        tool.Server + ":" + tool.Tool,
		})
		if !seen[tool.Server] {
			seen[tool.Server] = true
			servers = append(servers, tool.Server)
		}
	}
	return tools, servers
}
