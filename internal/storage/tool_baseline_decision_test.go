package storage

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestToolBaselineDecision_MarkReadAndCleanup(t *testing.T) {
	m, cleanup := setupTestStorageForToolApproval(t)
	defer cleanup()

	decided, err := m.ToolBaselineDecided("a")
	require.NoError(t, err)
	require.False(t, decided, "no marker before any decision")

	require.NoError(t, m.MarkToolBaselineDecided("a"))
	require.NoError(t, m.MarkToolBaselineDecided("a"), "idempotent")
	require.NoError(t, m.MarkToolBaselineDecided("b"))
	require.NoError(t, m.MarkToolBaselineDecided("c"))
	for _, s := range []string{"a", "b", "c"} {
		decided, err = m.ToolBaselineDecided(s)
		require.NoError(t, err)
		require.True(t, decided, s)
	}

	// Removed with the server's approval records.
	require.NoError(t, m.DeleteServerToolApprovals("a"))
	decided, err = m.ToolBaselineDecided("a")
	require.NoError(t, err)
	require.False(t, decided)

	// Orphan GC drops markers of servers no longer configured.
	_, err = m.PruneOrphanToolApprovals([]string{"b"})
	require.NoError(t, err)
	decided, _ = m.ToolBaselineDecided("b")
	require.True(t, decided, "configured server keeps its marker")
	decided, _ = m.ToolBaselineDecided("c")
	require.False(t, decided, "orphan marker is pruned")
}
