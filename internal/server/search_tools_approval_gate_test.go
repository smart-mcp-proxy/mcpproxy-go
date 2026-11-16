package server

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// TestSearchTools_WithholdsApprovalLockedHits covers GET /api/v1/index/search
// for a tool whose approval is pending or changed while its document still
// sits in the index (discovery removes it on the next pass). The stored
// description and schema are what the review exists to hold back, so the hit
// must not be returned — by its canonical "server:tool" id, which the index
// resolves to that one document, or by any word that matches it.
func TestSearchTools_WithholdsApprovalLockedHits(t *testing.T) {
	srv := newConsistencyServer(t)

	// See TestSearchTools_AnnotationsSurviveToTier: seeding before the
	// background config sync finishes races it.
	require.Eventually(t, func() bool {
		return srv.runtime.CurrentPhase() == runtime.PhaseReady
	}, 10*time.Second, 10*time.Millisecond, "fixture: runtime never reached PhaseReady")

	store := srv.runtime.StorageManager()
	require.NoError(t, store.SaveUpstreamServer(&config.ServerConfig{Name: "orchid", Enabled: true}))
	require.NoError(t, store.SaveUpstreamServer(&config.ServerConfig{Name: "cedar", Enabled: true, TrustMode: string(config.TrustModeAuto)}))
	for _, tool := range []struct{ server, name, status string }{
		{"orchid", "read_item", storage.ToolApprovalStatusApproved},
		{"orchid", "pending_item", storage.ToolApprovalStatusPending},
		{"orchid", "changed_item", storage.ToolApprovalStatusChanged},
		// The server opts out of tool-level review, so the record does not lock.
		{"cedar", "pending_item", storage.ToolApprovalStatusPending},
	} {
		require.NoError(t, srv.runtime.IndexManager().IndexTool(&config.ToolMetadata{
			Name:        tool.server + ":" + tool.name,
			ServerName:  tool.server,
			Description: "gadget lookup",
			ParamsJSON:  "{}",
		}))
		require.NoError(t, store.SaveToolApproval(&storage.ToolApprovalRecord{ServerName: tool.server, ToolName: tool.name, Status: tool.status}))
	}

	everyServer := func(string) bool { return true }
	searches := map[string]func(query string) ([]map[string]interface{}, error){
		"unscoped": func(query string) ([]map[string]interface{}, error) { return srv.SearchTools(query, 10) },
		"scoped": func(query string) ([]map[string]interface{}, error) {
			return srv.SearchToolsScoped(query, 10, everyServer)
		},
	}
	for name, search := range searches {
		t.Run(name, func(t *testing.T) {
			for _, tc := range []struct {
				query string
				want  []string
			}{
				{"gadget", []string{"orchid:read_item", "cedar:pending_item"}},
				{"orchid:read_item", []string{"orchid:read_item"}},
				{"orchid:pending_item", nil},
				{"orchid:changed_item", nil},
				{"cedar:pending_item", []string{"cedar:pending_item"}},
			} {
				rows, err := search(tc.query)
				require.NoError(t, err)
				require.ElementsMatch(t, tc.want, searchRowNames(t, rows), "query %q", tc.query)
			}
		})
	}
}

func searchRowNames(t *testing.T, rows []map[string]interface{}) []string {
	t.Helper()
	names := make([]string, 0, len(rows))
	for _, row := range rows {
		tool, ok := row["tool"].(map[string]interface{})
		require.True(t, ok, "search result must have a nested tool map")
		name, ok := tool["name"].(string)
		require.True(t, ok)
		names = append(names, name)
	}
	return names
}
