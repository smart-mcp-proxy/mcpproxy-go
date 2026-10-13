package runtime

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// The prune snapshots pending records for absent tools, then deletes them. An
// operator decision saved in between must survive the delete.
func TestPruneAbsentUndecidedApprovals_KeepsDecisionSavedAfterSnapshot(t *testing.T) {
	mutations := map[string]func(t *testing.T, rt *Runtime){
		"block": func(t *testing.T, rt *Runtime) {
			n, err := rt.BlockTools("srv", []string{"gone"}, "operator")
			require.NoError(t, err)
			require.Equal(t, 1, n)
		},
		"disable": func(t *testing.T, rt *Runtime) {
			require.NoError(t, rt.SetToolEnabled("srv", "gone", false, "operator"))
		},
		"approve": func(t *testing.T, rt *Runtime) {
			require.NoError(t, rt.ApproveTools("srv", []string{"gone"}, "operator"))
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			rt := newTestRuntime(t)
			require.NoError(t, rt.storageManager.SaveToolApproval(&storage.ToolApprovalRecord{
				ServerName: "srv", ToolName: "gone", Status: storage.ToolApprovalStatusPending,
				CurrentHash: "h", CurrentDescription: "d",
			}))
			rt.pruneAfterSnapshot = func(tool string) {
				if tool == "gone" {
					mutate(t, rt)
				}
			}
			rt.pruneAbsentUndecidedApprovals("srv", []*config.ToolMetadata{})

			rec, err := rt.storageManager.GetToolApproval("srv", "gone")
			require.NoError(t, err, "the operator decision must survive the prune")
			switch name {
			case "approve":
				require.Equal(t, storage.ToolApprovalStatusApproved, rec.Status)
			default:
				require.True(t, rec.Disabled)
			}
		})
	}
}

func TestPruneAbsentUndecidedApprovals_StillDropsUndecidedAbsent(t *testing.T) {
	rt := newTestRuntime(t)
	require.NoError(t, rt.storageManager.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "srv", ToolName: "gone", Status: storage.ToolApprovalStatusPending, CurrentHash: "h",
	}))
	rt.pruneAbsentUndecidedApprovals("srv", []*config.ToolMetadata{})
	_, err := rt.storageManager.GetToolApproval("srv", "gone")
	require.Error(t, err)
}
