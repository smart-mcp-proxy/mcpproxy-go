//go:build teams

package workspace

import (
	"testing"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIntegration_UserAddsServerAndDiscoverTools(t *testing.T) {
	store := setupTestStore(t)
	logger := testLogger(t)
	userID := "integ-user-1"

	// Create workspace for user
	mgr := NewManager(store, 10*time.Minute, logger)
	defer mgr.Stop()

	ws, err := mgr.GetOrCreateWorkspace(userID)
	require.NoError(t, err)
	require.NotNil(t, ws)

	// Initially empty
	assert.Empty(t, ws.GetServers())
	assert.Empty(t, ws.ServerNames())

	// Add a server config to the workspace
	server := &config.ServerConfig{
		Name:     "github-dev",
		URL:      "https://github-dev.example.com/mcp",
		Protocol: "http",
		Enabled:  true,
	}
	err = ws.AddServer(store, server)
	require.NoError(t, err)

	// Verify server appears in workspace.GetServers()
	servers := ws.GetServers()
	require.Len(t, servers, 1)
	assert.Equal(t, "github-dev", servers[0].Name)
	assert.Equal(t, "https://github-dev.example.com/mcp", servers[0].URL)
	assert.True(t, servers[0].Enabled)

	// Verify server name appears in workspace.ServerNames()
	names := ws.ServerNames()
	assert.Equal(t, []string{"github-dev"}, names)

	// Add a second server
	server2 := &config.ServerConfig{
		Name:     "gitlab-staging",
		URL:      "https://gitlab-staging.example.com/mcp",
		Protocol: "http",
		Enabled:  true,
	}
	err = ws.AddServer(store, server2)
	require.NoError(t, err)

	// Verify both servers present
	assert.Len(t, ws.GetServers(), 2)
	assert.Equal(t, []string{"github-dev", "gitlab-staging"}, ws.ServerNames())

	// Update server - verify changes persisted
	updatedServer := &config.ServerConfig{
		Name:     "github-dev",
		URL:      "https://github-dev-v2.example.com/mcp",
		Protocol: "http",
		Enabled:  false,
	}
	err = ws.UpdateServer(store, updatedServer)
	require.NoError(t, err)

	got, ok := ws.GetServer("github-dev")
	require.True(t, ok)
	assert.Equal(t, "https://github-dev-v2.example.com/mcp", got.URL)
	assert.False(t, got.Enabled)

	// Verify persisted to store
	persisted, err := store.GetUserServer(userID, "github-dev")
	require.NoError(t, err)
	require.NotNil(t, persisted)
	assert.Equal(t, "https://github-dev-v2.example.com/mcp", persisted.URL)
	assert.False(t, persisted.Enabled)

	// Remove server - verify no longer listed
	err = ws.RemoveServer(store, "github-dev")
	require.NoError(t, err)

	_, ok = ws.GetServer("github-dev")
	assert.False(t, ok)

	assert.Equal(t, []string{"gitlab-staging"}, ws.ServerNames())

	// Verify removed from store
	removed, err := store.GetUserServer(userID, "github-dev")
	require.NoError(t, err)
	assert.Nil(t, removed)
}

func TestIntegration_WorkspaceLifecycle(t *testing.T) {
	store := setupTestStore(t)
	logger := testLogger(t)

	// Use a very short idle timeout for test
	idleTimeout := 50 * time.Millisecond
	mgr := NewManager(store, idleTimeout, logger)
	defer mgr.Stop()

	// Create workspace for user A
	userID := "lifecycle-user"
	ws, err := mgr.GetOrCreateWorkspace(userID)
	require.NoError(t, err)
	require.NotNil(t, ws)

	// Add servers
	require.NoError(t, ws.AddServer(store, makeServer("srv-1")))
	require.NoError(t, ws.AddServer(store, makeServer("srv-2")))
	assert.Len(t, ws.GetServers(), 2)

	// Touch workspace (activity)
	ws.Touch()

	// Verify active workspace count
	assert.Equal(t, 1, mgr.ActiveWorkspaceCount())

	// Let it idle (wait past the short timeout)
	time.Sleep(70 * time.Millisecond)

	// Run cleanup
	mgr.cleanupIdle()

	// Verify workspace removed from manager
	assert.Equal(t, 0, mgr.ActiveWorkspaceCount())
	_, exists := mgr.GetWorkspace(userID)
	assert.False(t, exists)

	// Re-create workspace loads from store (servers still persisted)
	ws2, err := mgr.GetOrCreateWorkspace(userID)
	require.NoError(t, err)
	assert.Len(t, ws2.GetServers(), 2)
	assert.Equal(t, []string{"srv-1", "srv-2"}, ws2.ServerNames())
}

func TestIntegration_MultipleUsersIndependentWorkspaces(t *testing.T) {
	store := setupTestStore(t)
	logger := testLogger(t)

	mgr := NewManager(store, 10*time.Minute, logger)
	defer mgr.Stop()

	// Create workspaces for user A and user B
	wsA, err := mgr.GetOrCreateWorkspace("userA")
	require.NoError(t, err)

	wsB, err := mgr.GetOrCreateWorkspace("userB")
	require.NoError(t, err)

	// Add different servers to each
	require.NoError(t, wsA.AddServer(store, makeServer("server-a1")))
	require.NoError(t, wsA.AddServer(store, makeServer("server-a2")))
	require.NoError(t, wsA.AddServer(store, makeServer("server-a3")))

	require.NoError(t, wsB.AddServer(store, makeServer("server-b1")))
	require.NoError(t, wsB.AddServer(store, makeServer("server-b2")))

	// Verify each workspace has only its own servers
	assert.Equal(t, []string{"server-a1", "server-a2", "server-a3"}, wsA.ServerNames())
	assert.Equal(t, []string{"server-b1", "server-b2"}, wsB.ServerNames())

	// User A should not see B's servers
	_, okA := wsA.GetServer("server-b1")
	assert.False(t, okA, "user A should not see user B's servers")

	// User B should not see A's servers
	_, okB := wsB.GetServer("server-a1")
	assert.False(t, okB, "user B should not see user A's servers")

	assert.Equal(t, 2, mgr.ActiveWorkspaceCount())

	// Remove workspace for A
	mgr.RemoveWorkspace("userA")

	// Verify B's workspace still active
	assert.Equal(t, 1, mgr.ActiveWorkspaceCount())
	wsBAgain, exists := mgr.GetWorkspace("userB")
	assert.True(t, exists)
	assert.Equal(t, []string{"server-b1", "server-b2"}, wsBAgain.ServerNames())

	// Verify A's workspace is gone
	_, exists = mgr.GetWorkspace("userA")
	assert.False(t, exists)

	// Re-creating A's workspace restores from store
	wsANew, err := mgr.GetOrCreateWorkspace("userA")
	require.NoError(t, err)
	assert.Equal(t, []string{"server-a1", "server-a2", "server-a3"}, wsANew.ServerNames())
}
