//go:build teams

package workspace

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/teams/users"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.etcd.io/bbolt"
	"go.uber.org/zap"
)

func setupTestStore(t *testing.T) *users.UserStore {
	t.Helper()
	tmpFile := filepath.Join(t.TempDir(), "test.db")
	db, err := bbolt.Open(tmpFile, 0600, &bbolt.Options{Timeout: 1 * time.Second})
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	store := users.NewUserStore(db)
	require.NoError(t, store.EnsureBuckets())
	return store
}

func testLogger(t *testing.T) *zap.SugaredLogger {
	t.Helper()
	logger, _ := zap.NewDevelopment()
	t.Cleanup(func() { _ = logger.Sync() })
	return logger.Sugar()
}

func seedServers(t *testing.T, store *users.UserStore, userID string, servers ...*config.ServerConfig) {
	t.Helper()
	for _, s := range servers {
		require.NoError(t, store.CreateUserServer(userID, s))
	}
}

func makeServer(name string) *config.ServerConfig {
	return &config.ServerConfig{
		Name:    name,
		URL:     "https://" + name + ".example.com/mcp",
		Enabled: true,
	}
}

func TestUserWorkspace_LoadServers(t *testing.T) {
	store := setupTestStore(t)
	userID := "user-1"

	seedServers(t, store, userID, makeServer("github"), makeServer("gitlab"))

	ws := NewUserWorkspace(userID, testLogger(t))
	err := ws.LoadServers(store)
	require.NoError(t, err)

	servers := ws.GetServers()
	assert.Len(t, servers, 2)

	names := ws.ServerNames()
	assert.Equal(t, []string{"github", "gitlab"}, names)
}

func TestUserWorkspace_LoadServers_Empty(t *testing.T) {
	store := setupTestStore(t)

	ws := NewUserWorkspace("user-no-servers", testLogger(t))
	err := ws.LoadServers(store)
	require.NoError(t, err)

	assert.Empty(t, ws.GetServers())
	assert.Empty(t, ws.ServerNames())
}

func TestUserWorkspace_AddServer(t *testing.T) {
	store := setupTestStore(t)
	userID := "user-2"

	ws := NewUserWorkspace(userID, testLogger(t))
	require.NoError(t, ws.LoadServers(store))

	server := makeServer("new-server")
	err := ws.AddServer(store, server)
	require.NoError(t, err)

	// Verify in workspace
	got, ok := ws.GetServer("new-server")
	require.True(t, ok)
	assert.Equal(t, "new-server", got.Name)

	// Verify persisted to store
	persisted, err := store.GetUserServer(userID, "new-server")
	require.NoError(t, err)
	require.NotNil(t, persisted)
	assert.Equal(t, "new-server", persisted.Name)
}

func TestUserWorkspace_AddServer_Duplicate(t *testing.T) {
	store := setupTestStore(t)
	userID := "user-3"

	ws := NewUserWorkspace(userID, testLogger(t))
	require.NoError(t, ws.LoadServers(store))
	require.NoError(t, ws.AddServer(store, makeServer("dup")))

	err := ws.AddServer(store, makeServer("dup"))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")
}

func TestUserWorkspace_AddServer_EmptyName(t *testing.T) {
	store := setupTestStore(t)

	ws := NewUserWorkspace("user-x", testLogger(t))
	require.NoError(t, ws.LoadServers(store))

	err := ws.AddServer(store, &config.ServerConfig{Name: ""})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "server name is required")
}

func TestUserWorkspace_RemoveServer(t *testing.T) {
	store := setupTestStore(t)
	userID := "user-4"
	seedServers(t, store, userID, makeServer("to-remove"), makeServer("keep"))

	ws := NewUserWorkspace(userID, testLogger(t))
	require.NoError(t, ws.LoadServers(store))

	err := ws.RemoveServer(store, "to-remove")
	require.NoError(t, err)

	// Verify removed from workspace
	_, ok := ws.GetServer("to-remove")
	assert.False(t, ok)
	assert.Equal(t, []string{"keep"}, ws.ServerNames())

	// Verify removed from store
	persisted, err := store.GetUserServer(userID, "to-remove")
	require.NoError(t, err)
	assert.Nil(t, persisted)
}

func TestUserWorkspace_RemoveServer_NotFound(t *testing.T) {
	store := setupTestStore(t)

	ws := NewUserWorkspace("user-5", testLogger(t))
	require.NoError(t, ws.LoadServers(store))

	err := ws.RemoveServer(store, "nonexistent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestUserWorkspace_UpdateServer(t *testing.T) {
	store := setupTestStore(t)
	userID := "user-6"
	seedServers(t, store, userID, makeServer("updatable"))

	ws := NewUserWorkspace(userID, testLogger(t))
	require.NoError(t, ws.LoadServers(store))

	updated := &config.ServerConfig{
		Name:    "updatable",
		URL:     "https://new-url.example.com/mcp",
		Enabled: false,
	}
	err := ws.UpdateServer(store, updated)
	require.NoError(t, err)

	// Verify updated in workspace
	got, ok := ws.GetServer("updatable")
	require.True(t, ok)
	assert.Equal(t, "https://new-url.example.com/mcp", got.URL)
	assert.False(t, got.Enabled)

	// Verify persisted
	persisted, err := store.GetUserServer(userID, "updatable")
	require.NoError(t, err)
	require.NotNil(t, persisted)
	assert.Equal(t, "https://new-url.example.com/mcp", persisted.URL)
}

func TestUserWorkspace_UpdateServer_NotFound(t *testing.T) {
	store := setupTestStore(t)

	ws := NewUserWorkspace("user-7", testLogger(t))
	require.NoError(t, ws.LoadServers(store))

	err := ws.UpdateServer(store, makeServer("missing"))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestUserWorkspace_GetServer(t *testing.T) {
	store := setupTestStore(t)
	userID := "user-8"
	seedServers(t, store, userID, makeServer("alpha"), makeServer("beta"))

	ws := NewUserWorkspace(userID, testLogger(t))
	require.NoError(t, ws.LoadServers(store))

	got, ok := ws.GetServer("alpha")
	require.True(t, ok)
	assert.Equal(t, "alpha", got.Name)

	got, ok = ws.GetServer("beta")
	require.True(t, ok)
	assert.Equal(t, "beta", got.Name)

	_, ok = ws.GetServer("gamma")
	assert.False(t, ok)
}

func TestUserWorkspace_Touch(t *testing.T) {
	ws := NewUserWorkspace("user-9", testLogger(t))
	initial := ws.LastAccess()

	// Small sleep to ensure time difference
	time.Sleep(10 * time.Millisecond)
	ws.Touch()

	assert.True(t, ws.LastAccess().After(initial))
}

func TestUserWorkspace_ServerNames(t *testing.T) {
	store := setupTestStore(t)
	userID := "user-10"
	seedServers(t, store, userID, makeServer("zulu"), makeServer("alpha"), makeServer("mike"))

	ws := NewUserWorkspace(userID, testLogger(t))
	require.NoError(t, ws.LoadServers(store))

	// ServerNames should be sorted alphabetically
	names := ws.ServerNames()
	assert.Equal(t, []string{"alpha", "mike", "zulu"}, names)
}

func TestUserWorkspace_Shutdown(t *testing.T) {
	store := setupTestStore(t)
	userID := "user-11"
	seedServers(t, store, userID, makeServer("srv1"), makeServer("srv2"))

	ws := NewUserWorkspace(userID, testLogger(t))
	require.NoError(t, ws.LoadServers(store))
	assert.Len(t, ws.GetServers(), 2)

	ws.Shutdown()
	assert.Empty(t, ws.GetServers())
}
