package runtime

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 116 PR2 review round 3 runtime tests.

// A local reindex (the approval-driven reindex and the sweep's last-good
// fallback) that copied the server's last-good snapshot before a remove and
// same-name re-add must not apply that copy to the replacement: the removed
// server's definitions would recreate approval records (auto-approved for a
// trusted replacement with no baseline) and index entries the replacement
// never reported.
func TestRemoval_R3_LocalReindexDropsSnapshotSelectedBeforeReAdd(t *testing.T) {
	paths := map[string]func(rt *Runtime){
		"approval_reindex": func(rt *Runtime) { rt.reindexServerToolsAfterApprovalChange("acme") },
		"sweep_fallback":   func(rt *Runtime) { rt.reapplyLastGoodSnapshot(context.Background(), "acme") },
	}
	for name, run := range paths {
		t.Run(name, func(t *testing.T) {
			live := func() *config.ServerConfig {
				return explicitServer(&config.ServerConfig{Name: "acme", Command: "true", Protocol: "stdio", Enabled: true})
			}
			rt, _ := newSpec116Runtime(t, nil, live())
			ctx := context.Background()
			_, err := rt.commitDiscoveredTools(ctx, "acme", []*config.ToolMetadata{descTool("acme", "old")}, 0)
			require.NoError(t, err)
			_, err = rt.storageManager.GetToolApproval("acme", "t")
			require.NoError(t, err, "the old server has a record before the removal")

			reAdded := false
			rt.localReindexAfterSnapshot = func() {
				// The old snapshot is selected; the server is removed and
				// re-added before the copy is applied.
				removeAndReAdd(t, rt, live())
				reAdded = true
			}
			run(rt)
			rt.localReindexAfterSnapshot = nil
			require.True(t, reAdded, "the path selected a snapshot")

			_, err = rt.storageManager.GetToolApproval("acme", "t")
			assert.ErrorIs(t, err, storage.ErrToolApprovalNotFound,
				"the removed server's definitions must not recreate an approval record for the replacement")
			indexed, err := rt.indexManager.GetToolsByServer("acme")
			require.NoError(t, err)
			assert.Empty(t, indexed, "the removed server's definitions must not be indexed for the replacement")
		})
	}
}
