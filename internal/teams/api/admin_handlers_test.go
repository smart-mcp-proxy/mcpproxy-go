//go:build teams

package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.etcd.io/bbolt"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	teamsauth "github.com/smart-mcp-proxy/mcpproxy-go/internal/teams/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/teams/multiuser"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/teams/users"
)

const testAdminUserID = "01HTEST000000000000000ADMN"

// adminTestSetup creates AdminHandlers backed by a temporary BBolt database.
func adminTestSetup(t *testing.T, records []*storage.ActivityRecord) (*AdminHandlers, *users.UserStore) {
	t.Helper()

	tmpFile := filepath.Join(t.TempDir(), "test.db")
	db, err := bbolt.Open(tmpFile, 0600, &bbolt.Options{Timeout: 1 * time.Second})
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	store := users.NewUserStore(db)
	require.NoError(t, store.EnsureBuckets())

	provider := &mockActivityProvider{records: records}
	activityFilter := multiuser.NewActivityFilter(provider)
	sessionManager := teamsauth.NewSessionManager(store, 24*time.Hour, false)
	logger := zap.NewNop().Sugar()

	handlers := NewAdminHandlers(store, activityFilter, sessionManager, []string{"admin@example.com"}, nil, nil, "", nil, logger)
	return handlers, store
}

// adminTestRouter creates a chi router with admin handlers and auth context.
func adminTestRouter(handlers *AdminHandlers, authCtx *auth.AuthContext) *chi.Mux {
	r := chi.NewRouter()
	if authCtx != nil {
		r.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ctx := auth.WithAuthContext(r.Context(), authCtx)
				next.ServeHTTP(w, r.WithContext(ctx))
			})
		})
	}
	r.Route("/api/v1", func(r chi.Router) {
		handlers.RegisterRoutes(r)
	})
	return r
}

// adminAuthContext returns an admin auth context for testing.
func adminAuthContext() *auth.AuthContext {
	return auth.AdminUserContext(testAdminUserID, "admin@example.com", "Admin User", "google")
}

// createTestUser creates a user in the store for testing.
func createTestUser(t *testing.T, store *users.UserStore, id, email, displayName, provider string) *users.User {
	t.Helper()
	user := &users.User{
		ID:                id,
		Email:             email,
		DisplayName:       displayName,
		Provider:          provider,
		ProviderSubjectID: "sub-" + id,
		CreatedAt:         time.Now().UTC(),
		LastLoginAt:       time.Now().UTC(),
	}
	require.NoError(t, store.CreateUser(user))
	return user
}

// --- List users ---

func TestAdminListUsers(t *testing.T) {
	handlers, store := adminTestSetup(t, nil)
	router := adminTestRouter(handlers, adminAuthContext())

	// Create some users.
	createTestUser(t, store, "user-001", "alice@example.com", "Alice", "google")
	createTestUser(t, store, "user-002", "bob@example.com", "Bob", "github")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/users", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var profiles []*UserProfileResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &profiles))
	assert.Len(t, profiles, 2)

	// Verify profiles contain expected fields.
	emails := map[string]bool{}
	for _, p := range profiles {
		emails[p.Email] = true
		assert.NotEmpty(t, p.ID)
		assert.NotEmpty(t, p.Provider)
		assert.NotEmpty(t, p.LastLoginAt)
		assert.False(t, p.Disabled)
	}
	assert.True(t, emails["alice@example.com"])
	assert.True(t, emails["bob@example.com"])
}

func TestAdminListUsers_Empty(t *testing.T) {
	handlers, _ := adminTestSetup(t, nil)
	router := adminTestRouter(handlers, adminAuthContext())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/users", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var profiles []*UserProfileResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &profiles))
	assert.Empty(t, profiles)
}

// --- Disable/Enable user ---

func TestAdminDisableUser(t *testing.T) {
	handlers, store := adminTestSetup(t, nil)
	router := adminTestRouter(handlers, adminAuthContext())

	user := createTestUser(t, store, "user-disable", "disable@example.com", "DisableMe", "google")

	// Create a session for the user.
	session := users.NewSession(user.ID, 24*time.Hour)
	require.NoError(t, store.CreateSession(session))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/users/user-disable/disable", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	// Verify user is disabled.
	updated, err := store.GetUser("user-disable")
	require.NoError(t, err)
	require.NotNil(t, updated)
	assert.True(t, updated.Disabled)

	// Verify sessions were revoked.
	s, err := store.GetSession(session.ID)
	require.NoError(t, err)
	assert.Nil(t, s, "session should be deleted after disable")
}

func TestAdminEnableUser(t *testing.T) {
	handlers, store := adminTestSetup(t, nil)
	router := adminTestRouter(handlers, adminAuthContext())

	user := createTestUser(t, store, "user-enable", "enable@example.com", "EnableMe", "google")
	user.Disabled = true
	require.NoError(t, store.UpdateUser(user))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/users/user-enable/enable", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	// Verify user is enabled.
	updated, err := store.GetUser("user-enable")
	require.NoError(t, err)
	require.NotNil(t, updated)
	assert.False(t, updated.Disabled)
}

func TestAdminDisableUser_NotFound(t *testing.T) {
	handlers, _ := adminTestSetup(t, nil)
	router := adminTestRouter(handlers, adminAuthContext())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/users/nonexistent/disable", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// --- Admin activity ---

func TestAdminActivity_AllUsers(t *testing.T) {
	records := []*storage.ActivityRecord{
		{
			ID:        "act-1",
			Type:      storage.ActivityTypeToolCall,
			Status:    "success",
			Timestamp: time.Now().UTC(),
			UserID:    "user-a",
		},
		{
			ID:        "act-2",
			Type:      storage.ActivityTypeToolCall,
			Status:    "success",
			Timestamp: time.Now().UTC(),
			UserID:    "user-b",
		},
	}

	handlers, _ := adminTestSetup(t, records)
	router := adminTestRouter(handlers, adminAuthContext())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/activity", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp ActivityListResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, 2, resp.Total)
}

func TestAdminActivity_FilterByUser(t *testing.T) {
	records := []*storage.ActivityRecord{
		{
			ID:        "act-1",
			Type:      storage.ActivityTypeToolCall,
			Status:    "success",
			Timestamp: time.Now().UTC(),
			UserID:    "user-a",
		},
		{
			ID:        "act-2",
			Type:      storage.ActivityTypeToolCall,
			Status:    "success",
			Timestamp: time.Now().UTC(),
			UserID:    "user-b",
		},
	}

	handlers, _ := adminTestSetup(t, records)
	router := adminTestRouter(handlers, adminAuthContext())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/activity?user_id=user-a", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp ActivityListResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, 1, resp.Total)
}

// --- Sessions ---

func TestAdminListSessions(t *testing.T) {
	handlers, store := adminTestSetup(t, nil)
	router := adminTestRouter(handlers, adminAuthContext())

	// Create a user and session.
	user := createTestUser(t, store, "session-user", "session@example.com", "SessionUser", "google")
	session := users.NewSession(user.ID, 24*time.Hour)
	session.UserAgent = "TestBrowser/1.0"
	session.IPAddress = "127.0.0.1"
	require.NoError(t, store.CreateSession(session))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/sessions", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var sessions []*SessionResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &sessions))
	assert.Len(t, sessions, 1)
	assert.Equal(t, user.ID, sessions[0].UserID)
	assert.Equal(t, "session@example.com", sessions[0].UserEmail)
	assert.Equal(t, "TestBrowser/1.0", sessions[0].UserAgent)
	assert.False(t, sessions[0].Expired)
}

// --- Non-admin access ---

func TestAdminEndpoints_NonAdmin_Forbidden(t *testing.T) {
	handlers, _ := adminTestSetup(t, nil)
	userCtx := auth.UserContext(testUserID, "user@example.com", "Regular User", "google")
	router := adminTestRouter(handlers, userCtx)

	endpoints := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/v1/admin/users"},
		{http.MethodPost, "/api/v1/admin/users/some-id/disable"},
		{http.MethodPost, "/api/v1/admin/users/some-id/enable"},
		{http.MethodGet, "/api/v1/admin/activity"},
		{http.MethodGet, "/api/v1/admin/sessions"},
	}

	for _, ep := range endpoints {
		t.Run(ep.method+" "+ep.path, func(t *testing.T) {
			req := httptest.NewRequest(ep.method, ep.path, nil)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			assert.Equal(t, http.StatusForbidden, w.Code)
		})
	}
}

func TestAdminEndpoints_Unauthenticated(t *testing.T) {
	handlers, _ := adminTestSetup(t, nil)
	router := adminTestRouter(handlers, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/users", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}
