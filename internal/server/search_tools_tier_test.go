package server

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

// TestSearchTools_AnnotationsSurviveToTier is a regression test for a Spec
// 109 PR-a review finding (round 1, medium): GET /index/search (the Web UI
// Tools search box and the macOS tools search) computed `tier` from whatever
// searchResultsToMaps put in the "tool" map, but that map never carried an
// "annotations" key — so every search hit's tier came back "unannotated" no
// matter what the tool's real MCP behavior hints were, even though the SAME
// tool's tier on GET /servers/{id}/tools (fed from live upstream state, not
// the search index) was correct. A destructive tool would show a red
// Destructive badge in the normal Tools list and a gray Unannotated badge
// when found via search — the exact "silently not destructive" outcome the
// tier feature exists to prevent.
func TestSearchTools_AnnotationsSurviveToTier(t *testing.T) {
	srv := newConsistencyServer(t)

	// newConsistencyServer's cfg.Servers is empty, so NewServer's async
	// LoadConfiguredServers (StartBackgroundInitialization) is about to
	// re-sync storage to match it. Seeding the upstream server config before
	// that sync completes races it: if the sync's own save lands after this
	// fixture's, it silently reverts/removes the record — quarantinedServerFilter
	// then finds no server and withholds the tool, making the search return
	// zero hits (flake reproduced under -race full-package runs). Same
	// pattern as mcp_group_scope_test.go / server_logs_missing_file_test.go.
	require.Eventually(t, func() bool {
		return srv.runtime.CurrentPhase() == runtime.PhaseReady
	}, 10*time.Second, 10*time.Millisecond, "fixture: runtime never reached PhaseReady")

	require.NoError(t, srv.runtime.StorageManager().SaveUpstreamServer(&config.ServerConfig{
		Name:    "danger",
		Enabled: true,
		// Quarantined defaults to false; the search path withholds a
		// quarantined server's tools entirely, so this must be non-quarantined
		// for the hit to reach searchResultsToMaps at all.
	}))

	destructive := true
	require.NoError(t, srv.runtime.IndexManager().IndexTool(&config.ToolMetadata{
		Name:        "danger:delete_everything",
		ServerName:  "danger",
		Description: "irreversibly deletes everything, no confirmation",
		Annotations: &config.ToolAnnotations{
			DestructiveHint: &destructive,
		},
	}))

	maps, err := srv.SearchTools("delete_everything", 10)
	require.NoError(t, err)
	require.Len(t, maps, 1)

	toolMap, ok := maps[0]["tool"].(map[string]interface{})
	require.True(t, ok, "search result must have a nested tool map")
	assert.NotNil(t, toolMap["annotations"], "the tool map must carry annotations so AnnotationTier can compute a real tier")

	typed := contracts.ConvertGenericSearchResultsToTyped(maps)
	require.Len(t, typed, 1)
	assert.Equal(t, contracts.TierDestructive, typed[0].Tool.Tier,
		"a destructiveHint:true tool found via search must report tier=destructive, not unannotated")
}
