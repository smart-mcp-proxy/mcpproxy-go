package storage

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func setupTestStorageForEmptySlices(t *testing.T) *Manager {
	t.Helper()

	tmpDir, err := os.MkdirTemp("", "empty_slices_test_*")
	require.NoError(t, err)

	manager, err := NewManager(tmpDir, zap.NewNop().Sugar())
	require.NoError(t, err)

	t.Cleanup(func() {
		manager.Close()
		os.RemoveAll(tmpDir)
	})
	return manager
}

// Issue #953 follow-up: MCP responses built from these slices must serialize
// as [] — never null — for strict clients that iterate the arrays.

func TestListQuarantinedUpstreamServers_EmptyReturnsNonNil(t *testing.T) {
	manager := setupTestStorageForEmptySlices(t)

	servers, err := manager.ListQuarantinedUpstreamServers()
	require.NoError(t, err)
	require.NotNil(t, servers, "quarantined-server list must be non-nil so it serializes as [], not null")
	require.Empty(t, servers)
}

func TestGetToolStats_NoStatsReturnsNonNil(t *testing.T) {
	manager := setupTestStorageForEmptySlices(t)

	stats, err := manager.GetToolStats(10)
	require.NoError(t, err)
	require.NotNil(t, stats, "tool stats must be non-nil so usage_summary.top_tools serializes as [], not null")
	require.Empty(t, stats)
}
