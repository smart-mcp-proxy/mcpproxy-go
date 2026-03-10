//go:build teams

package users

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.etcd.io/bbolt"
)

func setupTestStore(t *testing.T) *UserStore {
	t.Helper()
	tmpFile := filepath.Join(t.TempDir(), "test.db")
	db, err := bbolt.Open(tmpFile, 0600, &bbolt.Options{Timeout: 1 * time.Second})
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	store := NewUserStore(db)
	require.NoError(t, store.EnsureBuckets())
	return store
}

// --- User CRUD tests ---

func TestUserStore_CreateAndGetUser(t *testing.T) {
	store := setupTestStore(t)

	user := NewUser("alice@example.com", "Alice", "google", "google-sub-1")
	err := store.CreateUser(user)
	require.NoError(t, err)

	got, err := store.GetUser(user.ID)
	require.NoError(t, err)
	require.NotNil(t, got)

	assert.Equal(t, user.ID, got.ID)
	assert.Equal(t, "alice@example.com", got.Email)
	assert.Equal(t, "Alice", got.DisplayName)
	assert.Equal(t, "google", got.Provider)
	assert.Equal(t, "google-sub-1", got.ProviderSubjectID)
}

func TestUserStore_GetUser_NotFound(t *testing.T) {
	store := setupTestStore(t)

	got, err := store.GetUser("nonexistent")
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestUserStore_GetUserByEmail(t *testing.T) {
	store := setupTestStore(t)

	user := NewUser("bob@example.com", "Bob", "github", "gh-sub-1")
	require.NoError(t, store.CreateUser(user))

	got, err := store.GetUserByEmail("bob@example.com")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, user.ID, got.ID)
	assert.Equal(t, "Bob", got.DisplayName)
}

func TestUserStore_GetUserByEmail_CaseInsensitive(t *testing.T) {
	store := setupTestStore(t)

	user := NewUser("carol@example.com", "Carol", "microsoft", "ms-sub-1")
	require.NoError(t, store.CreateUser(user))

	// Lookup with different casing
	tests := []string{
		"CAROL@EXAMPLE.COM",
		"Carol@Example.Com",
		"carol@example.com",
		"  Carol@Example.Com  ",
	}
	for _, email := range tests {
		got, err := store.GetUserByEmail(email)
		require.NoError(t, err, "email=%q", email)
		require.NotNil(t, got, "email=%q should find the user", email)
		assert.Equal(t, user.ID, got.ID, "email=%q", email)
	}
}

func TestUserStore_GetUserByEmail_NotFound(t *testing.T) {
	store := setupTestStore(t)

	got, err := store.GetUserByEmail("nobody@example.com")
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestUserStore_UpdateUser(t *testing.T) {
	store := setupTestStore(t)

	user := NewUser("dave@example.com", "Dave", "google", "g-sub-2")
	require.NoError(t, store.CreateUser(user))

	// Update display name and disabled status
	user.DisplayName = "David"
	user.Disabled = true
	err := store.UpdateUser(user)
	require.NoError(t, err)

	got, err := store.GetUser(user.ID)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "David", got.DisplayName)
	assert.True(t, got.Disabled)
}

func TestUserStore_UpdateUser_NotFound(t *testing.T) {
	store := setupTestStore(t)

	user := NewUser("ghost@example.com", "Ghost", "google", "g-sub-ghost")
	err := store.UpdateUser(user)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestUserStore_UpdateUser_EmailChange(t *testing.T) {
	store := setupTestStore(t)

	user := NewUser("old@example.com", "User", "google", "g-sub-email")
	require.NoError(t, store.CreateUser(user))

	// Change email
	user.Email = "new@example.com"
	require.NoError(t, store.UpdateUser(user))

	// Old email should not find user
	got, err := store.GetUserByEmail("old@example.com")
	require.NoError(t, err)
	assert.Nil(t, got)

	// New email should find user
	got, err = store.GetUserByEmail("new@example.com")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, user.ID, got.ID)
}

func TestUserStore_ListUsers(t *testing.T) {
	store := setupTestStore(t)

	u1 := NewUser("a@example.com", "A", "google", "sub-a")
	u2 := NewUser("b@example.com", "B", "github", "sub-b")
	u3 := NewUser("c@example.com", "C", "microsoft", "sub-c")

	require.NoError(t, store.CreateUser(u1))
	require.NoError(t, store.CreateUser(u2))
	require.NoError(t, store.CreateUser(u3))

	users, err := store.ListUsers()
	require.NoError(t, err)
	assert.Len(t, users, 3)

	ids := make(map[string]bool)
	for _, u := range users {
		ids[u.ID] = true
	}
	assert.True(t, ids[u1.ID])
	assert.True(t, ids[u2.ID])
	assert.True(t, ids[u3.ID])
}

func TestUserStore_ListUsers_Empty(t *testing.T) {
	store := setupTestStore(t)

	users, err := store.ListUsers()
	require.NoError(t, err)
	assert.Empty(t, users)
}

func TestUserStore_DeleteUser_RemovesEmailIndex(t *testing.T) {
	store := setupTestStore(t)

	user := NewUser("delete@example.com", "Delete Me", "google", "sub-del")
	require.NoError(t, store.CreateUser(user))

	// Verify user exists
	got, err := store.GetUser(user.ID)
	require.NoError(t, err)
	require.NotNil(t, got)

	// Delete user
	err = store.DeleteUser(user.ID)
	require.NoError(t, err)

	// User should be gone
	got, err = store.GetUser(user.ID)
	require.NoError(t, err)
	assert.Nil(t, got)

	// Email index should also be gone
	got, err = store.GetUserByEmail("delete@example.com")
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestUserStore_DeleteUser_NotFound(t *testing.T) {
	store := setupTestStore(t)

	err := store.DeleteUser("nonexistent")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestUserStore_CreateUser_DuplicateEmail_Error(t *testing.T) {
	store := setupTestStore(t)

	u1 := NewUser("dupe@example.com", "First", "google", "sub-first")
	require.NoError(t, store.CreateUser(u1))

	u2 := NewUser("dupe@example.com", "Second", "github", "sub-second")
	err := store.CreateUser(u2)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")
}

func TestUserStore_CreateUser_DuplicateEmail_CaseInsensitive(t *testing.T) {
	store := setupTestStore(t)

	u1 := NewUser("same@example.com", "First", "google", "sub-1")
	require.NoError(t, store.CreateUser(u1))

	u2 := NewUser("SAME@EXAMPLE.COM", "Second", "github", "sub-2")
	err := store.CreateUser(u2)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")
}

// --- Session CRUD tests ---

func TestSessionStore_CreateAndGetSession(t *testing.T) {
	store := setupTestStore(t)

	session := NewSession("user-1", time.Hour)
	session.BearerToken = "jwt-token-abc"
	session.UserAgent = "TestAgent/1.0"
	session.IPAddress = "127.0.0.1"

	err := store.CreateSession(session)
	require.NoError(t, err)

	got, err := store.GetSession(session.ID)
	require.NoError(t, err)
	require.NotNil(t, got)

	assert.Equal(t, session.ID, got.ID)
	assert.Equal(t, "user-1", got.UserID)
	assert.Equal(t, "jwt-token-abc", got.BearerToken)
	assert.Equal(t, "TestAgent/1.0", got.UserAgent)
	assert.Equal(t, "127.0.0.1", got.IPAddress)
}

func TestSessionStore_GetSession_NotFound(t *testing.T) {
	store := setupTestStore(t)

	got, err := store.GetSession("nonexistent")
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestSessionStore_GetSession_Expired_ReturnsNil(t *testing.T) {
	store := setupTestStore(t)

	// Create a session that is already expired
	session := &Session{
		ID:        "expired-session",
		UserID:    "user-1",
		CreatedAt: time.Now().UTC().Add(-2 * time.Hour),
		ExpiresAt: time.Now().UTC().Add(-1 * time.Hour),
	}
	require.NoError(t, store.CreateSession(session))

	got, err := store.GetSession(session.ID)
	require.NoError(t, err)
	assert.Nil(t, got, "expired session should return nil")
}

func TestSessionStore_DeleteSession(t *testing.T) {
	store := setupTestStore(t)

	session := NewSession("user-1", time.Hour)
	require.NoError(t, store.CreateSession(session))

	// Verify it exists
	got, err := store.GetSession(session.ID)
	require.NoError(t, err)
	require.NotNil(t, got)

	// Delete it
	err = store.DeleteSession(session.ID)
	require.NoError(t, err)

	// Should be gone
	got, err = store.GetSession(session.ID)
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestSessionStore_DeleteUserSessions(t *testing.T) {
	store := setupTestStore(t)

	// Create sessions for two users
	s1 := NewSession("user-A", time.Hour)
	s2 := NewSession("user-A", time.Hour)
	s3 := NewSession("user-B", time.Hour)

	require.NoError(t, store.CreateSession(s1))
	require.NoError(t, store.CreateSession(s2))
	require.NoError(t, store.CreateSession(s3))

	// Delete all sessions for user-A
	err := store.DeleteUserSessions("user-A")
	require.NoError(t, err)

	// user-A sessions should be gone
	got, err := store.GetSession(s1.ID)
	require.NoError(t, err)
	assert.Nil(t, got)

	got, err = store.GetSession(s2.ID)
	require.NoError(t, err)
	assert.Nil(t, got)

	// user-B session should still exist
	got, err = store.GetSession(s3.ID)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "user-B", got.UserID)
}

func TestSessionStore_ListSessions(t *testing.T) {
	store := setupTestStore(t)

	s1 := NewSession("user-1", time.Hour)
	s2 := NewSession("user-2", time.Hour)
	require.NoError(t, store.CreateSession(s1))
	require.NoError(t, store.CreateSession(s2))

	sessions, err := store.ListSessions()
	require.NoError(t, err)
	assert.Len(t, sessions, 2)
}

func TestSessionStore_CleanupExpiredSessions(t *testing.T) {
	store := setupTestStore(t)

	// Create one active and two expired sessions
	active := NewSession("user-1", time.Hour)
	require.NoError(t, store.CreateSession(active))

	expired1 := &Session{
		ID:        "expired-1",
		UserID:    "user-1",
		CreatedAt: time.Now().UTC().Add(-3 * time.Hour),
		ExpiresAt: time.Now().UTC().Add(-2 * time.Hour),
	}
	require.NoError(t, store.CreateSession(expired1))

	expired2 := &Session{
		ID:        "expired-2",
		UserID:    "user-2",
		CreatedAt: time.Now().UTC().Add(-5 * time.Hour),
		ExpiresAt: time.Now().UTC().Add(-4 * time.Hour),
	}
	require.NoError(t, store.CreateSession(expired2))

	// Cleanup
	count, err := store.CleanupExpiredSessions()
	require.NoError(t, err)
	assert.Equal(t, 2, count)

	// Only active session should remain
	sessions, err := store.ListSessions()
	require.NoError(t, err)
	assert.Len(t, sessions, 1)
	assert.Equal(t, active.ID, sessions[0].ID)
}

func TestSessionStore_CleanupExpiredSessions_NoneExpired(t *testing.T) {
	store := setupTestStore(t)

	s := NewSession("user-1", time.Hour)
	require.NoError(t, store.CreateSession(s))

	count, err := store.CleanupExpiredSessions()
	require.NoError(t, err)
	assert.Equal(t, 0, count)
}

// --- Server store tests ---

func TestServerStore_CreateAndListUserServers(t *testing.T) {
	store := setupTestStore(t)

	server1 := &config.ServerConfig{
		Name:     "github-server",
		URL:      "https://api.github.com/mcp",
		Protocol: "http",
		Enabled:  true,
	}
	server2 := &config.ServerConfig{
		Name:     "gitlab-server",
		URL:      "https://gitlab.com/mcp",
		Protocol: "http",
		Enabled:  true,
	}

	userID := "user-server-1"

	require.NoError(t, store.CreateUserServer(userID, server1))
	require.NoError(t, store.CreateUserServer(userID, server2))

	servers, err := store.ListUserServers(userID)
	require.NoError(t, err)
	assert.Len(t, servers, 2)

	names := make(map[string]bool)
	for _, s := range servers {
		names[s.Name] = true
	}
	assert.True(t, names["github-server"])
	assert.True(t, names["gitlab-server"])
}

func TestServerStore_GetUserServer(t *testing.T) {
	store := setupTestStore(t)

	server := &config.ServerConfig{
		Name:     "my-server",
		URL:      "https://example.com/mcp",
		Protocol: "http",
		Enabled:  true,
		Headers:  map[string]string{"Authorization": "Bearer token"},
	}

	userID := "user-get-server"
	require.NoError(t, store.CreateUserServer(userID, server))

	got, err := store.GetUserServer(userID, "my-server")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "my-server", got.Name)
	assert.Equal(t, "https://example.com/mcp", got.URL)
	assert.Equal(t, "Bearer token", got.Headers["Authorization"])
}

func TestServerStore_GetUserServer_NotFound(t *testing.T) {
	store := setupTestStore(t)

	got, err := store.GetUserServer("user-x", "nonexistent")
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestServerStore_UpdateUserServer(t *testing.T) {
	store := setupTestStore(t)

	userID := "user-update-srv"
	server := &config.ServerConfig{
		Name:     "update-me",
		URL:      "https://old.com/mcp",
		Protocol: "http",
		Enabled:  true,
	}
	require.NoError(t, store.CreateUserServer(userID, server))

	// Update the server
	server.URL = "https://new.com/mcp"
	server.Enabled = false
	require.NoError(t, store.UpdateUserServer(userID, server))

	got, err := store.GetUserServer(userID, "update-me")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "https://new.com/mcp", got.URL)
	assert.False(t, got.Enabled)
}

func TestServerStore_UpdateUserServer_NotFound(t *testing.T) {
	store := setupTestStore(t)

	server := &config.ServerConfig{Name: "ghost-server", URL: "https://ghost.com"}
	err := store.UpdateUserServer("user-ghost", server)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no servers found")
}

func TestServerStore_DeleteUserServer(t *testing.T) {
	store := setupTestStore(t)

	userID := "user-del-srv"
	server := &config.ServerConfig{
		Name:     "to-delete",
		URL:      "https://example.com/mcp",
		Protocol: "http",
		Enabled:  true,
	}
	require.NoError(t, store.CreateUserServer(userID, server))

	// Delete the server
	err := store.DeleteUserServer(userID, "to-delete")
	require.NoError(t, err)

	// Should be gone
	got, err := store.GetUserServer(userID, "to-delete")
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestServerStore_CreateUserServer_DuplicateName(t *testing.T) {
	store := setupTestStore(t)

	userID := "user-dup-srv"
	server := &config.ServerConfig{Name: "dupe", URL: "https://example.com"}
	require.NoError(t, store.CreateUserServer(userID, server))

	err := store.CreateUserServer(userID, server)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")
}

func TestServerStore_UserServerExists(t *testing.T) {
	store := setupTestStore(t)

	userID := "user-exists"
	server := &config.ServerConfig{Name: "exists-srv", URL: "https://example.com"}
	require.NoError(t, store.CreateUserServer(userID, server))

	exists, err := store.UserServerExists(userID, "exists-srv")
	require.NoError(t, err)
	assert.True(t, exists)

	exists, err = store.UserServerExists(userID, "not-exists")
	require.NoError(t, err)
	assert.False(t, exists)
}

func TestServerStore_UserIsolation(t *testing.T) {
	store := setupTestStore(t)

	userA := "user-A-isolation"
	userB := "user-B-isolation"

	serverA := &config.ServerConfig{
		Name:     "server-alpha",
		URL:      "https://alpha.com/mcp",
		Protocol: "http",
		Enabled:  true,
	}
	serverB := &config.ServerConfig{
		Name:     "server-beta",
		URL:      "https://beta.com/mcp",
		Protocol: "http",
		Enabled:  true,
	}

	// Create servers for different users
	require.NoError(t, store.CreateUserServer(userA, serverA))
	require.NoError(t, store.CreateUserServer(userB, serverB))

	// User A should only see their server
	serversA, err := store.ListUserServers(userA)
	require.NoError(t, err)
	assert.Len(t, serversA, 1)
	assert.Equal(t, "server-alpha", serversA[0].Name)

	// User B should only see their server
	serversB, err := store.ListUserServers(userB)
	require.NoError(t, err)
	assert.Len(t, serversB, 1)
	assert.Equal(t, "server-beta", serversB[0].Name)

	// User A should not be able to get User B's server
	got, err := store.GetUserServer(userA, "server-beta")
	require.NoError(t, err)
	assert.Nil(t, got)

	// User B should not be able to get User A's server
	got, err = store.GetUserServer(userB, "server-alpha")
	require.NoError(t, err)
	assert.Nil(t, got)

	// Both users can have a server with the same name
	sharedName := &config.ServerConfig{Name: "shared-name", URL: "https://a.com"}
	require.NoError(t, store.CreateUserServer(userA, sharedName))

	sharedName2 := &config.ServerConfig{Name: "shared-name", URL: "https://b.com"}
	require.NoError(t, store.CreateUserServer(userB, sharedName2))

	gotA, err := store.GetUserServer(userA, "shared-name")
	require.NoError(t, err)
	require.NotNil(t, gotA)
	assert.Equal(t, "https://a.com", gotA.URL)

	gotB, err := store.GetUserServer(userB, "shared-name")
	require.NoError(t, err)
	require.NotNil(t, gotB)
	assert.Equal(t, "https://b.com", gotB.URL)
}

func TestServerStore_ListUserServers_Empty(t *testing.T) {
	store := setupTestStore(t)

	servers, err := store.ListUserServers("user-with-no-servers")
	require.NoError(t, err)
	assert.Empty(t, servers)
}

func TestUserStore_DeleteUser_CleansUpServerBucket(t *testing.T) {
	store := setupTestStore(t)

	// Create user with servers
	user := NewUser("cleanup@example.com", "Cleanup", "google", "sub-cleanup")
	require.NoError(t, store.CreateUser(user))

	server := &config.ServerConfig{Name: "user-server", URL: "https://example.com"}
	require.NoError(t, store.CreateUserServer(user.ID, server))

	// Verify server exists
	exists, err := store.UserServerExists(user.ID, "user-server")
	require.NoError(t, err)
	assert.True(t, exists)

	// Delete user (should also delete server bucket)
	require.NoError(t, store.DeleteUser(user.ID))

	// Server bucket should be gone
	servers, err := store.ListUserServers(user.ID)
	require.NoError(t, err)
	assert.Empty(t, servers)
}
