package storage

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mkApproval(server, tool string) *ToolApprovalRecord {
	return &ToolApprovalRecord{
		ServerName: server,
		ToolName:   tool,
		Status:     ToolApprovalStatusApproved,
		ApprovedAt: time.Now().UTC(),
	}
}

func TestPruneOrphanToolApprovals(t *testing.T) {
	manager, cleanup := setupTestStorageForToolApproval(t)
	defer cleanup()

	require.NoError(t, manager.SaveToolApproval(mkApproval("github", "create_issue")))
	require.NoError(t, manager.SaveToolApproval(mkApproval("github", "list_repos")))
	require.NoError(t, manager.SaveToolApproval(mkApproval("disabled-but-configured", "x")))
	require.NoError(t, manager.SaveToolApproval(mkApproval("old-server", "do_thing")))
	require.NoError(t, manager.SaveToolApproval(mkApproval("removed", "y")))

	// "disabled-but-configured" stays in config (just disabled) → must be kept.
	removed, err := manager.PruneOrphanToolApprovals([]string{"github", "disabled-but-configured"})
	require.NoError(t, err)
	assert.Equal(t, 2, removed, "only old-server + removed are orphans")

	// configured (incl. disabled) survive
	for _, tc := range []struct{ s, tool string }{{"github", "create_issue"}, {"github", "list_repos"}, {"disabled-but-configured", "x"}} {
		rec, err := manager.GetToolApproval(tc.s, tc.tool)
		require.NoError(t, err)
		assert.NotNilf(t, rec, "%s:%s should be kept", tc.s, tc.tool)
	}
	// orphans gone (GetToolApproval returns a not-found error for missing records)
	for _, tc := range []struct{ s, tool string }{{"old-server", "do_thing"}, {"removed", "y"}} {
		_, err := manager.GetToolApproval(tc.s, tc.tool)
		assert.Errorf(t, err, "%s:%s should be pruned (not found)", tc.s, tc.tool)
	}
}

func TestPruneOrphanToolApprovals_EmptyConfigPrunesAll(t *testing.T) {
	manager, cleanup := setupTestStorageForToolApproval(t)
	defer cleanup()
	require.NoError(t, manager.SaveToolApproval(mkApproval("a", "t1")))
	require.NoError(t, manager.SaveToolApproval(mkApproval("b", "t2")))

	removed, err := manager.PruneOrphanToolApprovals(nil)
	require.NoError(t, err)
	assert.Equal(t, 2, removed)
}

func TestPruneOrphanToolApprovals_NoOrphansNoChange(t *testing.T) {
	manager, cleanup := setupTestStorageForToolApproval(t)
	defer cleanup()
	require.NoError(t, manager.SaveToolApproval(mkApproval("a", "t1")))

	removed, err := manager.PruneOrphanToolApprovals([]string{"a", "b", "c"})
	require.NoError(t, err)
	assert.Equal(t, 0, removed)
}
