//go:build server

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
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	teamsauth "github.com/smart-mcp-proxy/mcpproxy-go/internal/teams/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/teams/users"
)

// authTestSetup creates AuthEndpoints backed by a temporary BBolt database.
func authTestSetup(t *testing.T) (*AuthEndpoints, *users.UserStore) {
	t.Helper()

	tmpFile := filepath.Join(t.TempDir(), "test.db")
	db, err := bbolt.Open(tmpFile, 0600, &bbolt.Options{Timeout: 1 * time.Second})
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	store := users.NewUserStore(db)
	require.NoError(t, store.EnsureBuckets())

	sessionManager := teamsauth.NewSessionManager(store, 24*time.Hour, false)
	teamsConfig := config.DefaultTeamsConfig()
	hmacKey := []byte("test-hmac-key-32-bytes-long!!!!!!")
	logger := zap.NewNop().Sugar()

	endpoints := NewAuthEndpoints(store, sessionManager, teamsConfig, hmacKey, logger)
	return endpoints, store
}

// authTestRouter creates a chi router with auth endpoints and auth context.
func authTestRouter(endpoints *AuthEndpoints, authCtx *auth.AuthContext) *chi.Mux {
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
		endpoints.RegisterRoutes(r)
	})
	return r
}

// --- GET /auth/me ---

func TestAuthMe_ReturnsProfile(t *testing.T) {
	endpoints, store := authTestSetup(t)

	// Create the user in the store.
	user := &users.User{
		ID:                testUserID,
		Email:             "test@example.com",
		DisplayName:       "Test User",
		Provider:          "google",
		ProviderSubjectID: "sub-test",
		CreatedAt:         time.Now().UTC(),
		LastLoginAt:       time.Now().UTC(),
	}
	require.NoError(t, store.CreateUser(user))

	userCtx := auth.UserContext(testUserID, "test@example.com", "Test User", "google")
	router := authTestRouter(endpoints, userCtx)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp MeResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, testUserID, resp.ID)
	assert.Equal(t, "test@example.com", resp.Email)
	assert.Equal(t, "Test User", resp.DisplayName)
	assert.Equal(t, "user", resp.Role)
	assert.Equal(t, "google", resp.Provider)
}

func TestAuthMe_AdminRole(t *testing.T) {
	endpoints, store := authTestSetup(t)

	user := &users.User{
		ID:                testAdminUserID,
		Email:             "admin@example.com",
		DisplayName:       "Admin User",
		Provider:          "google",
		ProviderSubjectID: "sub-admin",
		CreatedAt:         time.Now().UTC(),
		LastLoginAt:       time.Now().UTC(),
	}
	require.NoError(t, store.CreateUser(user))

	adminCtx := auth.AdminUserContext(testAdminUserID, "admin@example.com", "Admin User", "google")
	router := authTestRouter(endpoints, adminCtx)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp MeResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "admin", resp.Role)
}

func TestAuthMe_Unauthenticated(t *testing.T) {
	endpoints, _ := authTestSetup(t)
	router := authTestRouter(endpoints, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestAuthMe_UserNotInStore(t *testing.T) {
	endpoints, _ := authTestSetup(t)

	// Auth context with a user ID that doesn't exist in the store.
	userCtx := auth.UserContext("nonexistent-user", "ghost@example.com", "Ghost", "google")
	router := authTestRouter(endpoints, userCtx)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// --- POST /auth/token ---

func TestAuthToken_GeneratesJWT(t *testing.T) {
	endpoints, store := authTestSetup(t)

	user := &users.User{
		ID:                testUserID,
		Email:             "test@example.com",
		DisplayName:       "Test User",
		Provider:          "google",
		ProviderSubjectID: "sub-test",
		CreatedAt:         time.Now().UTC(),
		LastLoginAt:       time.Now().UTC(),
	}
	require.NoError(t, store.CreateUser(user))

	userCtx := auth.UserContext(testUserID, "test@example.com", "Test User", "google")
	router := authTestRouter(endpoints, userCtx)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/token", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp TokenResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.NotEmpty(t, resp.Token)
	assert.NotEmpty(t, resp.ExpiresAt)

	// Verify the token is a valid JWT by parsing it.
	claims, err := teamsauth.ValidateBearerToken(resp.Token, []byte("test-hmac-key-32-bytes-long!!!!!!"))
	require.NoError(t, err)
	assert.Equal(t, testUserID, claims.Subject)
	assert.Equal(t, "test@example.com", claims.Email)
	assert.Equal(t, "user", claims.Role)
	assert.Equal(t, "google", claims.Provider)

	// Verify expiry is in the future.
	expiresAt, err := time.Parse(time.RFC3339, resp.ExpiresAt)
	require.NoError(t, err)
	assert.True(t, expiresAt.After(time.Now()))
}

func TestAuthToken_Unauthenticated(t *testing.T) {
	endpoints, _ := authTestSetup(t)
	router := authTestRouter(endpoints, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/token", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestAuthToken_UserNotInStore(t *testing.T) {
	endpoints, _ := authTestSetup(t)

	userCtx := auth.UserContext("nonexistent-user", "ghost@example.com", "Ghost", "google")
	router := authTestRouter(endpoints, userCtx)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/token", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}
