package index

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Spec 108 (Profiles v3) T016a/T022: SearchToolsAdmitted is SearchToolsScoped's
// hit-level counterpart — the predicate sees the hit's canonical (server,
// tool) identity, not merely its server, and returns a three-way Admission so
// the caller can separate "invisible" (RejectScope, never counted) from
// "hidden by profile" (RejectPolicy, counted) over one exhaustive pre-limit
// scan (data-model.md §2, FR-011).

// admittedSeamQuery mirrors scopedSeamQuery's shape: one entitled, low-
// frequency hit vs a pile of hidden, high-frequency ones, so an unlimited
// scan is required to find the entitled hit at all.
const admittedSeamQuery = "widget"

func buildAdmittedSeamCorpus(t *testing.T, hiddenCount int) *BleveIndex {
	t.Helper()

	idx, err := NewBleveIndex(t.TempDir(), zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { _ = idx.Close() })

	entitled := &config.ToolMetadata{
		Name:        "a:status_reader",
		ServerName:  "a",
		Description: "widget status check",
		ParamsJSON:  "{}",
		Hash:        "admitted-entitled",
	}
	require.NoError(t, idx.IndexTool(entitled))

	// One tool that is IN SCOPE (server "a") but must be excluded by POLICY —
	// distinct from the hidden "b" server population below, so a test can
	// tell RejectPolicy from RejectScope.
	policyExcluded := &config.ToolMetadata{
		Name:        "a:delete_everything",
		ServerName:  "a",
		Description: "widget delete_everything",
		ParamsJSON:  "{}",
		Hash:        "admitted-policy-excluded",
	}
	require.NoError(t, idx.IndexTool(policyExcluded))

	hidden := make([]*config.ToolMetadata, 0, hiddenCount)
	for i := 0; i < hiddenCount; i++ {
		hidden = append(hidden, &config.ToolMetadata{
			Name:        fmt.Sprintf("b:tool_%d", i),
			ServerName:  "b",
			Description: "widget widget widget widget widget widget widget widget widget widget",
			ParamsJSON:  "{}",
			Hash:        fmt.Sprintf("admitted-hidden-%d", i),
		})
	}
	require.NoError(t, idx.BatchIndex(hidden))

	return idx
}

// serverOnlyAdmit builds an admit func that only ever inspects Hit.Server —
// the SearchToolsAdmitted equivalent of SearchToolsScoped's inScope closure —
// so the two methods can be compared for equality (T016a "equals
// SearchToolsScoped when the predicate only checks the server").
func serverOnlyAdmit(inScope func(server string) bool) func(Hit) Admission {
	return func(h Hit) Admission {
		if inScope(h.Server) {
			return Admit
		}
		return RejectScope
	}
}

func TestSearchToolsAdmitted_FiltersBeforeLimitLikeScoped(t *testing.T) {
	idx := buildAdmittedSeamCorpus(t, 50)

	// Control: the unscoped window really is a "b" hit.
	unscoped, err := idx.SearchTools(admittedSeamQuery, 1)
	require.NoError(t, err)
	require.Len(t, unscoped, 1)
	require.Equal(t, "b", unscoped[0].Tool.ServerName, "fixture: hidden server must outrank the entitled one")

	inScopeA := func(server string) bool { return server == "a" }

	t.Run("equals SearchToolsScoped when the predicate only checks the server", func(t *testing.T) {
		scoped, err := idx.SearchToolsScoped(admittedSeamQuery, 10, inScopeA)
		require.NoError(t, err)

		admitted, hiddenByPolicy, err := idx.SearchToolsAdmitted(admittedSeamQuery, 10, serverOnlyAdmit(inScopeA))
		require.NoError(t, err)
		require.Equal(t, 0, hiddenByPolicy, "a predicate that never returns RejectPolicy must never count anything")

		require.Len(t, admitted, len(scoped))
		for i := range scoped {
			assert.Equal(t, scoped[i].Tool.Name, admitted[i].Tool.Name)
			assert.Equal(t, scoped[i].Score, admitted[i].Score)
		}
	})

	t.Run("limit applies to admitted hits only: a policy-rejected top hit never shortens the page", func(t *testing.T) {
		// admit: server "a" only, AND exclude "delete_everything" by policy.
		admit := func(h Hit) Admission {
			if h.Server != "a" {
				return RejectScope
			}
			if h.Tool == "delete_everything" {
				return RejectPolicy
			}
			return Admit
		}
		results, hiddenByPolicy, err := idx.SearchToolsAdmitted(admittedSeamQuery, 1, admit)
		require.NoError(t, err)
		require.Len(t, results, 1, "the admitted hit must fill the page despite a higher/rejected candidate ahead of it")
		assert.Equal(t, "a:status_reader", results[0].Tool.Name)
		assert.Equal(t, 1, hiddenByPolicy, "the excluded in-scope tool must be counted")
	})

	t.Run("hiddenByPolicy still counts a RejectPolicy hit ranked BELOW an already-full page (zcode review round 1)", func(t *testing.T) {
		// Two admitted "a" tools plus one policy-excluded "a" tool that
		// ranks LAST among the three (single mention vs the others'
		// heavier repetition) — with limit=2 the page fills on the first
		// two admitted hits, and the excluded one is scanned afterward.
		idx2, err := NewBleveIndex(t.TempDir(), zap.NewNop())
		require.NoError(t, err)
		t.Cleanup(func() { _ = idx2.Close() })
		require.NoError(t, idx2.IndexTool(&config.ToolMetadata{
			Name: "a:keep_one", ServerName: "a",
			Description: "gadget gadget gadget gadget gadget gadget gadget gadget", ParamsJSON: "{}",
		}))
		require.NoError(t, idx2.IndexTool(&config.ToolMetadata{
			Name: "a:keep_two", ServerName: "a",
			Description: "gadget gadget gadget gadget gadget gadget", ParamsJSON: "{}",
		}))
		require.NoError(t, idx2.IndexTool(&config.ToolMetadata{
			Name: "a:excluded_one", ServerName: "a",
			Description: "gadget", ParamsJSON: "{}",
		}))

		admit := func(h Hit) Admission {
			if h.Server != "a" {
				return RejectScope
			}
			if h.Tool == "excluded_one" {
				return RejectPolicy
			}
			return Admit
		}
		results, hiddenByPolicy, err := idx2.SearchToolsAdmitted("gadget", 2, admit)
		require.NoError(t, err)
		require.Len(t, results, 2, "the page must fill with the two admitted hits")
		assert.Equal(t, 1, hiddenByPolicy,
			"the excluded hit ranked below the already-full page must still be counted (FR-011: counted over the full match set)")
	})

	t.Run("RejectScope is never counted in hiddenByPolicy", func(t *testing.T) {
		_, hiddenByPolicy, err := idx.SearchToolsAdmitted(admittedSeamQuery, 10, serverOnlyAdmit(inScopeA))
		require.NoError(t, err)
		assert.Equal(t, 0, hiddenByPolicy, "the 50 out-of-scope 'b' hits must never inflate hiddenByPolicy")
	})

	t.Run("predicate receives the canonical (server, tool) identity, never a bare doc id", func(t *testing.T) {
		var seen []Hit
		admit := func(h Hit) Admission {
			seen = append(seen, h)
			return RejectScope
		}
		_, _, err := idx.SearchToolsAdmitted(admittedSeamQuery, 10, admit)
		require.NoError(t, err)
		require.NotEmpty(t, seen)
		for _, h := range seen {
			assert.NotEmpty(t, h.Server)
			assert.NotEmpty(t, h.Tool)
			assert.NotContains(t, h.Tool, ":", "Tool must be the RAW name, never the canonical server:tool id")
		}
	})

	t.Run("pages exhaustively: the entitled hit is found beyond the first page", func(t *testing.T) {
		bigIdx := buildAdmittedSeamCorpus(t, scopedSearchMinPage+25)
		admitOnlyStatusReader := func(h Hit) Admission {
			if h.Server != "a" {
				return RejectScope
			}
			if h.Tool != "status_reader" {
				return RejectPolicy
			}
			return Admit
		}
		results, hiddenByPolicy, err := bigIdx.SearchToolsAdmitted(admittedSeamQuery, 1, admitOnlyStatusReader)
		require.NoError(t, err)
		require.Len(t, results, 1)
		assert.Equal(t, "a:status_reader", results[0].Tool.Name)
		assert.Equal(t, 1, hiddenByPolicy, "the other in-scope 'a' tool must be counted, out-of-scope 'b' hits must not")
	})
}

func TestSearchToolsAdmitted_EmptyQueryOrNilPredicate(t *testing.T) {
	idx := buildAdmittedSeamCorpus(t, 1)

	_, _, err := idx.SearchToolsAdmitted("", 10, serverOnlyAdmit(func(string) bool { return true }))
	require.Error(t, err)

	results, hidden, err := idx.SearchToolsAdmitted(admittedSeamQuery, 10, nil)
	require.NoError(t, err)
	assert.Empty(t, results)
	assert.Equal(t, 0, hidden)
}

// TestManager_SearchToolsAdmitted pins the Manager-level delegation (T022).
func TestManager_SearchToolsAdmitted(t *testing.T) {
	m, err := NewManager(t.TempDir(), zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { _ = m.Close() })

	require.NoError(t, m.IndexTool(&config.ToolMetadata{
		Name: "a:status_reader", ServerName: "a", Description: "widget status check", ParamsJSON: "{}",
	}))
	require.NoError(t, m.IndexTool(&config.ToolMetadata{
		Name: "b:other", ServerName: "b", Description: "widget widget widget widget widget", ParamsJSON: "{}",
	}))

	results, hidden, err := m.SearchToolsAdmitted(admittedSeamQuery, 10, serverOnlyAdmit(func(s string) bool { return s == "a" }))
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "a:status_reader", results[0].Tool.Name)
	assert.Equal(t, 0, hidden)
}
