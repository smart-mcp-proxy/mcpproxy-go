//go:build teams

package multiuser

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/teams/users"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/teams/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.etcd.io/bbolt"
	"go.uber.org/zap"
)

// --- test helpers ---

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

func makeSharedServers(names ...string) []*config.ServerConfig {
	servers := make([]*config.ServerConfig, len(names))
	for i, name := range names {
		servers[i] = makeServer(name)
	}
	return servers
}

func setupRouter(t *testing.T, sharedNames []string, userServers map[string][]string) (*Router, *workspace.Manager) {
	t.Helper()
	store := setupTestStore(t)
	logger := testLogger(t)

	for userID, serverNames := range userServers {
		for _, name := range serverNames {
			seedServers(t, store, userID, makeServer(name))
		}
	}

	wm := workspace.NewManager(store, 10*time.Minute, logger)
	t.Cleanup(func() { wm.Stop() })

	shared := makeSharedServers(sharedNames...)
	router := NewRouter(shared, wm, logger)
	return router, wm
}

func adminCtx(userID string) context.Context {
	ac := auth.AdminUserContext(userID, userID+"@example.com", "Admin User", "google")
	return auth.WithAuthContext(context.Background(), ac)
}

func userCtx(userID string) context.Context {
	ac := auth.UserContext(userID, userID+"@example.com", "Regular User", "google")
	return auth.WithAuthContext(context.Background(), ac)
}

func noAuthCtx() context.Context {
	return context.Background()
}

// --- Router tests ---

func TestRouter_GetUserServers_Admin(t *testing.T) {
	router, _ := setupRouter(t,
		[]string{"github", "gitlab", "slack"},
		nil,
	)

	ctx := adminCtx("admin-1")
	servers, err := router.GetUserServers(ctx)
	require.NoError(t, err)

	// Admin gets all shared servers.
	assert.Len(t, servers, 3)
	for _, s := range servers {
		assert.Equal(t, OwnershipShared, s.Ownership)
	}

	names := serverInfoNames(servers)
	assert.Contains(t, names, "github")
	assert.Contains(t, names, "gitlab")
	assert.Contains(t, names, "slack")
}

func TestRouter_GetUserServers_AdminWithPersonal(t *testing.T) {
	router, _ := setupRouter(t,
		[]string{"github", "gitlab"},
		map[string][]string{
			"admin-1": {"my-private-server"},
		},
	)

	ctx := adminCtx("admin-1")
	servers, err := router.GetUserServers(ctx)
	require.NoError(t, err)

	// Admin gets shared + their own personal servers.
	assert.Len(t, servers, 3)

	names := serverInfoNames(servers)
	assert.Contains(t, names, "github")
	assert.Contains(t, names, "gitlab")
	assert.Contains(t, names, "my-private-server")

	// Verify ownership metadata.
	for _, s := range servers {
		if s.Config.Name == "my-private-server" {
			assert.Equal(t, OwnershipPersonal, s.Ownership)
		} else {
			assert.Equal(t, OwnershipShared, s.Ownership)
		}
	}
}

func TestRouter_GetUserServers_User(t *testing.T) {
	router, _ := setupRouter(t,
		[]string{"github", "gitlab"},
		map[string][]string{
			"alice": {"alice-dev", "alice-staging"},
		},
	)

	ctx := userCtx("alice")
	servers, err := router.GetUserServers(ctx)
	require.NoError(t, err)

	// User gets shared + their personal servers.
	assert.Len(t, servers, 4)

	names := serverInfoNames(servers)
	assert.Contains(t, names, "github")
	assert.Contains(t, names, "gitlab")
	assert.Contains(t, names, "alice-dev")
	assert.Contains(t, names, "alice-staging")
}

func TestRouter_GetUserServers_UserDoesNotSeeOtherUserServers(t *testing.T) {
	router, _ := setupRouter(t,
		[]string{"github"},
		map[string][]string{
			"alice": {"alice-server"},
			"bob":   {"bob-server"},
		},
	)

	ctx := userCtx("alice")
	servers, err := router.GetUserServers(ctx)
	require.NoError(t, err)

	names := serverInfoNames(servers)
	assert.Contains(t, names, "github")
	assert.Contains(t, names, "alice-server")
	assert.NotContains(t, names, "bob-server")
}

func TestRouter_GetUserServers_NoAuth(t *testing.T) {
	router, _ := setupRouter(t, []string{"github"}, nil)

	ctx := noAuthCtx()
	_, err := router.GetUserServers(ctx)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no authentication context")
}

func TestRouter_GetUserServers_ApiKeyAdmin(t *testing.T) {
	router, _ := setupRouter(t, []string{"github", "gitlab"}, nil)

	// API key admin (non-OAuth) gets shared servers only (no UserID, no workspace).
	ac := auth.AdminContext()
	ctx := auth.WithAuthContext(context.Background(), ac)

	servers, err := router.GetUserServers(ctx)
	require.NoError(t, err)
	assert.Len(t, servers, 2)
	for _, s := range servers {
		assert.Equal(t, OwnershipShared, s.Ownership)
	}
}

func TestRouter_GetServerForUser_Shared(t *testing.T) {
	router, _ := setupRouter(t,
		[]string{"github", "gitlab"},
		nil,
	)

	// Both admin and regular user can access shared servers.
	for _, ctx := range []context.Context{adminCtx("admin-1"), userCtx("alice")} {
		info, err := router.GetServerForUser(ctx, "github")
		require.NoError(t, err)
		assert.Equal(t, "github", info.Config.Name)
		assert.Equal(t, OwnershipShared, info.Ownership)
	}
}

func TestRouter_GetServerForUser_Personal(t *testing.T) {
	router, _ := setupRouter(t,
		[]string{"github"},
		map[string][]string{
			"alice": {"alice-dev"},
		},
	)

	ctx := userCtx("alice")
	info, err := router.GetServerForUser(ctx, "alice-dev")
	require.NoError(t, err)
	assert.Equal(t, "alice-dev", info.Config.Name)
	assert.Equal(t, OwnershipPersonal, info.Ownership)
}

func TestRouter_GetServerForUser_OtherUser(t *testing.T) {
	router, _ := setupRouter(t,
		[]string{"github"},
		map[string][]string{
			"alice": {"alice-dev"},
			"bob":   {"bob-dev"},
		},
	)

	// Alice cannot access Bob's personal server.
	ctx := userCtx("alice")
	_, err := router.GetServerForUser(ctx, "bob-dev")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found or not accessible")
}

func TestRouter_GetServerForUser_NotFound(t *testing.T) {
	router, _ := setupRouter(t, []string{"github"}, nil)

	ctx := userCtx("alice")
	_, err := router.GetServerForUser(ctx, "nonexistent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found or not accessible")
}

func TestRouter_GetServerForUser_NoAuth(t *testing.T) {
	router, _ := setupRouter(t, []string{"github"}, nil)

	_, err := router.GetServerForUser(noAuthCtx(), "github")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no authentication context")
}

func TestRouter_IsServerAccessible(t *testing.T) {
	router, _ := setupRouter(t,
		[]string{"github"},
		map[string][]string{
			"alice": {"alice-dev"},
			"bob":   {"bob-dev"},
		},
	)

	ctx := userCtx("alice")

	assert.True(t, router.IsServerAccessible(ctx, "github"))
	assert.True(t, router.IsServerAccessible(ctx, "alice-dev"))
	assert.False(t, router.IsServerAccessible(ctx, "bob-dev"))
	assert.False(t, router.IsServerAccessible(ctx, "nonexistent"))
	assert.False(t, router.IsServerAccessible(noAuthCtx(), "github"))
}

func TestRouter_UpdateSharedServers(t *testing.T) {
	router, _ := setupRouter(t, []string{"github"}, nil)

	assert.Equal(t, []string{"github"}, router.GetSharedServerNames())

	// Update to a new list.
	router.UpdateSharedServers(makeSharedServers("gitlab", "slack"))

	names := router.GetSharedServerNames()
	assert.Equal(t, []string{"gitlab", "slack"}, names)

	// Old shared server is no longer accessible.
	ctx := userCtx("alice")
	assert.False(t, router.IsServerAccessible(ctx, "github"))
	assert.True(t, router.IsServerAccessible(ctx, "gitlab"))
	assert.True(t, router.IsServerAccessible(ctx, "slack"))
}

func TestRouter_UpdateSharedServers_Empty(t *testing.T) {
	router, _ := setupRouter(t, []string{"github"}, nil)

	router.UpdateSharedServers(nil)
	assert.Empty(t, router.GetSharedServerNames())
}

func TestRouter_GetSharedServerNames_Sorted(t *testing.T) {
	router, _ := setupRouter(t, []string{"zulu", "alpha", "mike"}, nil)

	names := router.GetSharedServerNames()
	assert.Equal(t, []string{"alpha", "mike", "zulu"}, names)
}

func TestRouter_PersonalServerShadowingShared(t *testing.T) {
	// If a user has a personal server with the same name as a shared server,
	// the shared server takes precedence and the personal one is skipped.
	router, _ := setupRouter(t,
		[]string{"github"},
		map[string][]string{
			"alice": {"github", "alice-dev"},
		},
	)

	ctx := userCtx("alice")
	servers, err := router.GetUserServers(ctx)
	require.NoError(t, err)

	// Should have shared "github" + personal "alice-dev", not duplicate "github".
	names := serverInfoNames(servers)
	assert.Len(t, servers, 2)
	assert.Contains(t, names, "github")
	assert.Contains(t, names, "alice-dev")

	// The "github" should be the shared one.
	for _, s := range servers {
		if s.Config.Name == "github" {
			assert.Equal(t, OwnershipShared, s.Ownership)
		}
	}
}

// --- ToolFilter tests ---

func TestToolFilter_FilterToolsByUser(t *testing.T) {
	router, _ := setupRouter(t,
		[]string{"github", "gitlab"},
		map[string][]string{
			"alice": {"alice-dev"},
			"bob":   {"bob-dev"},
		},
	)

	filter := NewToolFilter(router, testLogger(t))

	allTools := []ToolInfo{
		{ToolName: "create_issue", ServerName: "github"},
		{ToolName: "list_repos", ServerName: "github"},
		{ToolName: "create_mr", ServerName: "gitlab"},
		{ToolName: "deploy", ServerName: "alice-dev"},
		{ToolName: "build", ServerName: "bob-dev"},
		{ToolName: "run", ServerName: "unknown-server"},
	}

	ctx := userCtx("alice")
	filtered := filter.FilterToolsByUser(ctx, allTools)

	// Alice should see tools from github, gitlab, and alice-dev (not bob-dev or unknown).
	assert.Len(t, filtered, 4)

	toolNames := make([]string, len(filtered))
	for i, ti := range filtered {
		toolNames[i] = ti.ToolName
	}
	assert.Contains(t, toolNames, "create_issue")
	assert.Contains(t, toolNames, "list_repos")
	assert.Contains(t, toolNames, "create_mr")
	assert.Contains(t, toolNames, "deploy")
	assert.NotContains(t, toolNames, "build")
	assert.NotContains(t, toolNames, "run")
}

func TestToolFilter_FilterToolsByUser_PreservesOwnership(t *testing.T) {
	router, _ := setupRouter(t,
		[]string{"github"},
		map[string][]string{
			"alice": {"alice-dev"},
		},
	)

	filter := NewToolFilter(router, testLogger(t))

	allTools := []ToolInfo{
		{ToolName: "create_issue", ServerName: "github"},
		{ToolName: "deploy", ServerName: "alice-dev"},
	}

	ctx := userCtx("alice")
	filtered := filter.FilterToolsByUser(ctx, allTools)

	require.Len(t, filtered, 2)
	for _, ti := range filtered {
		switch ti.ServerName {
		case "github":
			assert.Equal(t, OwnershipShared, ti.Ownership)
		case "alice-dev":
			assert.Equal(t, OwnershipPersonal, ti.Ownership)
		}
	}
}

func TestToolFilter_FilterToolsByUser_NoAuth(t *testing.T) {
	router, _ := setupRouter(t, []string{"github"}, nil)
	filter := NewToolFilter(router, testLogger(t))

	allTools := []ToolInfo{
		{ToolName: "create_issue", ServerName: "github"},
	}

	filtered := filter.FilterToolsByUser(noAuthCtx(), allTools)
	assert.Nil(t, filtered)
}

func TestToolFilter_FilterToolsByUser_EmptyInput(t *testing.T) {
	router, _ := setupRouter(t, []string{"github"}, nil)
	filter := NewToolFilter(router, testLogger(t))

	ctx := userCtx("alice")
	filtered := filter.FilterToolsByUser(ctx, nil)
	assert.Nil(t, filtered)

	filtered = filter.FilterToolsByUser(ctx, []ToolInfo{})
	assert.Nil(t, filtered)
}

func TestToolFilter_GetAccessibleServerNames(t *testing.T) {
	router, _ := setupRouter(t,
		[]string{"github", "gitlab"},
		map[string][]string{
			"alice": {"alice-dev", "alice-staging"},
		},
	)

	filter := NewToolFilter(router, testLogger(t))

	ctx := userCtx("alice")
	names, err := filter.GetAccessibleServerNames(ctx)
	require.NoError(t, err)

	// Should be sorted.
	assert.Equal(t, []string{"alice-dev", "alice-staging", "github", "gitlab"}, names)
}

func TestToolFilter_GetAccessibleServerNames_NoAuth(t *testing.T) {
	router, _ := setupRouter(t, []string{"github"}, nil)
	filter := NewToolFilter(router, testLogger(t))

	_, err := filter.GetAccessibleServerNames(noAuthCtx())
	assert.Error(t, err)
}

func TestToolFilter_IsToolAccessible(t *testing.T) {
	router, _ := setupRouter(t,
		[]string{"github"},
		map[string][]string{
			"alice": {"alice-dev"},
			"bob":   {"bob-dev"},
		},
	)

	filter := NewToolFilter(router, testLogger(t))
	ctx := userCtx("alice")

	assert.True(t, filter.IsToolAccessible(ctx, "github"))
	assert.True(t, filter.IsToolAccessible(ctx, "alice-dev"))
	assert.False(t, filter.IsToolAccessible(ctx, "bob-dev"))
	assert.False(t, filter.IsToolAccessible(ctx, "nonexistent"))
}

// --- helper ---

func serverInfoNames(servers []ServerInfo) []string {
	names := make([]string, len(servers))
	for i, s := range servers {
		names[i] = s.Config.Name
	}
	return names
}
