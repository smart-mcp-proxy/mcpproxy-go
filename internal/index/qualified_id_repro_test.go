package index

import (
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/stretchr/testify/require"
)

func TestQualifiedIDSearch(t *testing.T) {
	idx := newRawNameIndex(t)
	for _, tool := range []*config.ToolMetadata{
		{ServerName: "orchid-hub", Name: "ledger_get_entry", Description: "Retrieve an entry."},
		{ServerName: "cedar", Name: "ledger_get_entry", Description: "Retrieve an entry."},
		{ServerName: "orchid-hub", Name: "ns:erase", RawName: "ns:erase", Description: "Erase namespace."},
		{ServerName: "maple", Name: "other", Description: "orchid-hub:ledger_get_entry"},
	} {
		require.NoError(t, idx.IndexTool(tool))
	}
	for _, limit := range []int{1, 100} {
		hits, err := idx.SearchTools("orchid-hub:ledger_get_entry", limit)
		require.NoError(t, err)
		require.NotEmpty(t, hits)
		require.Equal(t, "orchid-hub:ledger_get_entry", hits[0].Tool.Name)
		count := 0
		for _, h := range hits {
			if h.Tool.Name == "orchid-hub:ledger_get_entry" {
				count++
			}
		}
		require.Equal(t, 1, count)
		for _, other := range hits[1:] {
			require.Greater(t, hits[0].Score, other.Score)
		}
	}
	hits, err := idx.SearchTools("orchid-hub:ns:erase", 1)
	require.NoError(t, err)
	require.Len(t, hits, 1)
	require.Equal(t, "ns:erase", hits[0].Tool.RawName)
	hits, err = idx.SearchTools("ledger_get_entry", 100)
	require.NoError(t, err)
	require.Len(t, hits, 2)
	hits, err = idx.SearchTools("cedar:missing", 100)
	require.NoError(t, err)
	require.Empty(t, hits)

	t.Run("scopes and policy apply to exact candidate", func(t *testing.T) {
		q := "orchid-hub:ledger_get_entry"
		scoped, err := idx.SearchToolsScoped(q, 1, func(s string) bool { return s == "maple" })
		require.NoError(t, err)
		require.Len(t, scoped, 1)
		require.Equal(t, "maple:other", scoped[0].Tool.Name)
		admitted, hidden, err := idx.SearchToolsAdmitted(q, 1, func(h Hit) Admission {
			if h.Server == "orchid-hub" {
				return RejectPolicy
			}
			return Admit
		})
		require.NoError(t, err)
		require.Len(t, admitted, 1)
		require.Equal(t, "maple:other", admitted[0].Tool.Name)
		require.Equal(t, 1, hidden)
		_, hidden, err = idx.SearchToolsAdmitted(q, 1, func(h Hit) Admission {
			if h.Server == "orchid-hub" {
				return RejectScope
			}
			return Admit
		})
		require.NoError(t, err)
		require.Zero(t, hidden)
	})
	t.Run("existing lexical scores preserved", func(t *testing.T) {
		q := "orchid-hub:ledger_get_entry"
		lexical, err := idx.augmentedToolSearchQuery(q, 100)
		require.NoError(t, err)
		before, err := idx.index.Search(newToolSearchRequest(lexical, 0, 100))
		require.NoError(t, err)
		after, err := idx.SearchTools(q, 100)
		require.NoError(t, err)
		scores := map[string]float64{}
		for _, h := range before.Hits {
			scores[h.ID] = h.Score
		}
		for _, h := range after {
			if score, ok := scores[h.Tool.Name]; ok {
				require.InDelta(t, score, h.Score, 1e-12, h.Tool.Name)
			}
		}
		// A legacy document may already have its canonical ID in the
		// searchable field. Its ranking score must not fall below the original lexical score.
		control := newRawNameIndex(t)
		require.NoError(t, control.IndexTool(&config.ToolMetadata{ServerName: "orchid-hub", Name: q}))
		lexical, err = control.augmentedToolSearchQuery(q, 100)
		require.NoError(t, err)
		before, err = control.index.Search(newToolSearchRequest(lexical, 0, 100))
		require.NoError(t, err)
		require.Len(t, before.Hits, 1)
		after, err = control.SearchTools(q, 100)
		require.NoError(t, err)
		require.Len(t, after, 1)
		require.GreaterOrEqual(t, after[0].Score, before.Hits[0].Score)
	})
}
