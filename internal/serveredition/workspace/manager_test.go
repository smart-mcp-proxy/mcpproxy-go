//go:build server

package workspace

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestManager_GetOrCreateWorkspace(t *testing.T) {
	store := setupTestStore(t)
	logger := testLogger(t)
	userID := "mgr-user-1"

	seedServers(t, store, userID, makeServer("srv1"))

	mgr := NewManager(store, 10*time.Minute, logger)
	defer mgr.Stop()

	// First access creates the workspace
	ws1, err := mgr.GetOrCreateWorkspace(userID)
	require.NoError(t, err)
	require.NotNil(t, ws1)
	assert.Equal(t, userID, ws1.UserID)
	assert.Equal(t, []string{"srv1"}, ws1.ServerNames())
	assert.Equal(t, 1, mgr.ActiveWorkspaceCount())

	// Second access returns the same workspace
	ws2, err := mgr.GetOrCreateWorkspace(userID)
	require.NoError(t, err)
	assert.Same(t, ws1, ws2)
	assert.Equal(t, 1, mgr.ActiveWorkspaceCount())
}

func TestManager_GetOrCreateWorkspace_MultipleUsers(t *testing.T) {
	store := setupTestStore(t)
	logger := testLogger(t)

	seedServers(t, store, "alice", makeServer("alice-srv"))
	seedServers(t, store, "bob", makeServer("bob-srv"))

	mgr := NewManager(store, 10*time.Minute, logger)
	defer mgr.Stop()

	wsAlice, err := mgr.GetOrCreateWorkspace("alice")
	require.NoError(t, err)

	wsBob, err := mgr.GetOrCreateWorkspace("bob")
	require.NoError(t, err)

	assert.NotSame(t, wsAlice, wsBob)
	assert.Equal(t, 2, mgr.ActiveWorkspaceCount())
	assert.Equal(t, []string{"alice-srv"}, wsAlice.ServerNames())
	assert.Equal(t, []string{"bob-srv"}, wsBob.ServerNames())
}

func TestManager_GetWorkspace_NotExists(t *testing.T) {
	store := setupTestStore(t)
	logger := testLogger(t)

	mgr := NewManager(store, 10*time.Minute, logger)
	defer mgr.Stop()

	ws, exists := mgr.GetWorkspace("nonexistent")
	assert.False(t, exists)
	assert.Nil(t, ws)
}

func TestManager_GetWorkspace_Exists(t *testing.T) {
	store := setupTestStore(t)
	logger := testLogger(t)

	mgr := NewManager(store, 10*time.Minute, logger)
	defer mgr.Stop()

	created, err := mgr.GetOrCreateWorkspace("user-x")
	require.NoError(t, err)

	got, exists := mgr.GetWorkspace("user-x")
	assert.True(t, exists)
	assert.Same(t, created, got)
}

func TestManager_RemoveWorkspace(t *testing.T) {
	store := setupTestStore(t)
	logger := testLogger(t)

	seedServers(t, store, "rm-user", makeServer("srv"))

	mgr := NewManager(store, 10*time.Minute, logger)
	defer mgr.Stop()

	_, err := mgr.GetOrCreateWorkspace("rm-user")
	require.NoError(t, err)
	assert.Equal(t, 1, mgr.ActiveWorkspaceCount())

	mgr.RemoveWorkspace("rm-user")
	assert.Equal(t, 0, mgr.ActiveWorkspaceCount())

	_, exists := mgr.GetWorkspace("rm-user")
	assert.False(t, exists)
}

func TestManager_RemoveWorkspace_NotExists(t *testing.T) {
	store := setupTestStore(t)
	logger := testLogger(t)

	mgr := NewManager(store, 10*time.Minute, logger)
	defer mgr.Stop()

	// Should not panic
	mgr.RemoveWorkspace("ghost")
	assert.Equal(t, 0, mgr.ActiveWorkspaceCount())
}

func TestManager_ActiveWorkspaceCount(t *testing.T) {
	store := setupTestStore(t)
	logger := testLogger(t)

	mgr := NewManager(store, 10*time.Minute, logger)
	defer mgr.Stop()

	assert.Equal(t, 0, mgr.ActiveWorkspaceCount())

	_, err := mgr.GetOrCreateWorkspace("u1")
	require.NoError(t, err)
	assert.Equal(t, 1, mgr.ActiveWorkspaceCount())

	_, err = mgr.GetOrCreateWorkspace("u2")
	require.NoError(t, err)
	assert.Equal(t, 2, mgr.ActiveWorkspaceCount())

	_, err = mgr.GetOrCreateWorkspace("u3")
	require.NoError(t, err)
	assert.Equal(t, 3, mgr.ActiveWorkspaceCount())

	mgr.RemoveWorkspace("u2")
	assert.Equal(t, 2, mgr.ActiveWorkspaceCount())
}

func TestManager_CleanupIdle(t *testing.T) {
	store := setupTestStore(t)
	logger := testLogger(t)

	// Use a very short idle timeout for testing
	idleTimeout := 50 * time.Millisecond
	mgr := NewManager(store, idleTimeout, logger)
	defer mgr.Stop()

	// Create two workspaces
	ws1, err := mgr.GetOrCreateWorkspace("idle-user")
	require.NoError(t, err)
	require.NotNil(t, ws1)

	ws2, err := mgr.GetOrCreateWorkspace("active-user")
	require.NoError(t, err)
	require.NotNil(t, ws2)

	assert.Equal(t, 2, mgr.ActiveWorkspaceCount())

	// Wait for idle timeout to pass
	time.Sleep(70 * time.Millisecond)

	// Touch the active user to keep it alive
	ws2.Touch()

	// Run cleanup directly
	mgr.cleanupIdle()

	// idle-user should be removed, active-user should remain
	assert.Equal(t, 1, mgr.ActiveWorkspaceCount())

	_, exists := mgr.GetWorkspace("idle-user")
	assert.False(t, exists)

	_, exists = mgr.GetWorkspace("active-user")
	assert.True(t, exists)
}

func TestManager_CleanupIdle_AllIdle(t *testing.T) {
	store := setupTestStore(t)
	logger := testLogger(t)

	idleTimeout := 50 * time.Millisecond
	mgr := NewManager(store, idleTimeout, logger)
	defer mgr.Stop()

	_, err := mgr.GetOrCreateWorkspace("u1")
	require.NoError(t, err)
	_, err = mgr.GetOrCreateWorkspace("u2")
	require.NoError(t, err)

	time.Sleep(70 * time.Millisecond)
	mgr.cleanupIdle()

	assert.Equal(t, 0, mgr.ActiveWorkspaceCount())
}

func TestManager_CleanupIdle_NoneIdle(t *testing.T) {
	store := setupTestStore(t)
	logger := testLogger(t)

	// Long timeout so nothing is idle
	mgr := NewManager(store, 1*time.Hour, logger)
	defer mgr.Stop()

	_, err := mgr.GetOrCreateWorkspace("u1")
	require.NoError(t, err)

	mgr.cleanupIdle()

	assert.Equal(t, 1, mgr.ActiveWorkspaceCount())
}

func TestManager_Stop(t *testing.T) {
	store := setupTestStore(t)
	logger := testLogger(t)

	seedServers(t, store, "s1", makeServer("a"))
	seedServers(t, store, "s2", makeServer("b"))

	mgr := NewManager(store, 10*time.Minute, logger)

	ws1, err := mgr.GetOrCreateWorkspace("s1")
	require.NoError(t, err)
	ws2, err := mgr.GetOrCreateWorkspace("s2")
	require.NoError(t, err)

	// Both workspaces have servers loaded
	assert.Len(t, ws1.GetServers(), 1)
	assert.Len(t, ws2.GetServers(), 1)

	mgr.Stop()

	// After stop, all workspaces should be shut down (servers cleared)
	assert.Equal(t, 0, mgr.ActiveWorkspaceCount())
	assert.Empty(t, ws1.GetServers())
	assert.Empty(t, ws2.GetServers())
}

func TestManager_Stop_WithCleanupRunning(t *testing.T) {
	store := setupTestStore(t)
	logger := testLogger(t)

	mgr := NewManager(store, 1*time.Minute, logger)
	mgr.StartCleanup()

	_, err := mgr.GetOrCreateWorkspace("u1")
	require.NoError(t, err)

	// Stop should cleanly shut down cleanup goroutine and workspaces
	mgr.Stop()
	assert.Equal(t, 0, mgr.ActiveWorkspaceCount())
}
