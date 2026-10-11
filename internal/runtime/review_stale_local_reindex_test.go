package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// A local reindex that selected approved definition V1 must not apply it after
// a newer capture V2 marked the tool changed: checkToolApprovals would read V1
// as a genuine revert, restore approved status and clear the rug-pull hold.
func TestLocalReindexPaths_DoNotUndoNewerCaptureHold(t *testing.T) {
	paths := map[string]func(rt *Runtime){
		"approval_reindex": func(rt *Runtime) { rt.reindexServerToolsAfterApprovalChange("srv") },
		"sweep_fallback":   func(rt *Runtime) { rt.reapplyLastGoodSnapshot(context.Background(), "srv") },
	}
	for name, run := range paths {
		t.Run(name, func(t *testing.T) {
			rt := setupQuarantineRuntime(t, nil, []*config.ServerConfig{{Name: "srv", Enabled: true}})
			ctx := context.Background()
			_, err := rt.commitDiscoveredTools(ctx, "srv", []*config.ToolMetadata{descTool("srv", "v1")})
			require.NoError(t, err)
			rec, err := rt.storageManager.GetToolApproval("srv", "t")
			require.NoError(t, err)
			rec.Status = storage.ToolApprovalStatusApproved
			rec.ApprovedHash = rec.CurrentHash
			require.NoError(t, rt.storageManager.SaveToolApproval(rec))

			v2Done := make(chan error, 1)
			rt.localReindexAfterSnapshot = func() {
				// Snapshot V1 is selected. Start a V2 discovery and give it
				// time to finish if nothing serializes it behind this path.
				go func() {
					_, err := rt.commitDiscoveredTools(ctx, "srv", []*config.ToolMetadata{descTool("srv", "v2")})
					v2Done <- err
				}()
				select {
				case <-v2Done:
					v2Done <- nil
				case <-time.After(300 * time.Millisecond):
				}
			}
			run(rt)
			rt.localReindexAfterSnapshot = nil
			require.NoError(t, <-v2Done)

			got, err := rt.storageManager.GetToolApproval("srv", "t")
			require.NoError(t, err)
			require.Equal(t, storage.ToolApprovalStatusChanged, got.Status, "stale V1 must not clear the rug-pull hold")
			require.Equal(t, "v2", got.CurrentDescription)
			review := reviewOf(t, rt, "srv")
			require.Equal(t, "v2", review.Tools[0].Description)
			require.Equal(t, "changed", review.Tools[0].ApprovalStatus)
			require.Equal(t, 1, review.Server.LiveToolCount)
			indexed, err := rt.indexManager.GetToolsByServer("srv")
			require.NoError(t, err)
			require.Empty(t, indexed, "changed tool must not be served from the index")
		})
	}
}
