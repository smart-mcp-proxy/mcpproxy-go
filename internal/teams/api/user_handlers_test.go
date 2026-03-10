//go:build server

package api

import (
	"bytes"
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
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/teams/users"
)

const testUserID = "01HTEST000000000000000USER"

// testSetup creates a UserHandlers instance backed by a temporary BBolt database
// with optional shared servers for testing.
func testSetup(t *testing.T, sharedServers []*config.ServerConfig) (*UserHandlers, *users.UserStore) {
	t.Helper()

	tmpFile := filepath.Join(t.TempDir(), "test.db")
	db, err := bbolt.Open(tmpFile, 0600, &bbolt.Options{Timeout: 1 * time.Second})
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	store := users.NewUserStore(db)
	require.NoError(t, store.EnsureBuckets())

	logger := zap.NewNop().Sugar()
	handlers := NewUserHandlers(store, sharedServers, nil, nil, logger)

	return handlers, store
}

// testRouter creates a chi router with the user handlers registered and
// wraps all requests with the given auth context.
func testRouter(handlers *UserHandlers, authCtx *auth.AuthContext) *chi.Mux {
	r := chi.NewRouter()
	// Inject auth context into every request for testing.
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := auth.WithAuthContext(r.Context(), authCtx)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	})
	r.Route("/api/v1", func(r chi.Router) {
		handlers.RegisterRoutes(r)
	})
	return r
}

// defaultSharedServers returns a set of shared servers for testing.
func defaultSharedServers() []*config.ServerConfig {
	return []*config.ServerConfig{
		{
			Name:     "shared-github",
			URL:      "https://api.github.com/mcp",
			Protocol: "http",
			Enabled:  true,
			Shared:   true,
			Created:  time.Now().UTC(),
		},
		{
			Name:     "shared-gitlab",
			URL:      "https://gitlab.com/mcp",
			Protocol: "http",
			Enabled:  true,
			Shared:   true,
			Created:  time.Now().UTC(),
		},
	}
}

// defaultAuthContext returns a user auth context for testing.
func defaultAuthContext() *auth.AuthContext {
	return auth.UserContext(testUserID, "test@example.com", "Test User", "google")
}

// --- List servers ---

func TestListUserServers_Empty(t *testing.T) {
	shared := defaultSharedServers()
	handlers, _ := testSetup(t, shared)
	router := testRouter(handlers, defaultAuthContext())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/user/servers", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp ServerListResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	assert.Empty(t, resp.Personal)
	assert.Len(t, resp.Shared, 2)
	assert.Equal(t, "shared", resp.Shared[0].Ownership)
	assert.Equal(t, "shared-github", resp.Shared[0].Name)
}

func TestListUserServers_WithPersonal(t *testing.T) {
	shared := defaultSharedServers()
	handlers, store := testSetup(t, shared)
	router := testRouter(handlers, defaultAuthContext())

	// Create a personal server.
	sc := &config.ServerConfig{
		Name:     "my-server",
		URL:      "http://localhost:9999",
		Protocol: "http",
		Enabled:  true,
		Created:  time.Now().UTC(),
	}
	require.NoError(t, store.CreateUserServer(testUserID, sc))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/user/servers", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp ServerListResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	assert.Len(t, resp.Personal, 1)
	assert.Equal(t, "personal", resp.Personal[0].Ownership)
	assert.Equal(t, "my-server", resp.Personal[0].Name)
	assert.Len(t, resp.Shared, 2)
}

// --- Create server ---

func TestCreateServer_Success(t *testing.T) {
	handlers, _ := testSetup(t, defaultSharedServers())
	router := testRouter(handlers, defaultAuthContext())

	body, _ := json.Marshal(CreateServerRequest{
		Name:     "new-server",
		URL:      "http://localhost:5555",
		Protocol: "http",
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/user/servers", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)

	var resp ServerResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "new-server", resp.Name)
	assert.Equal(t, "personal", resp.Ownership)
	assert.Equal(t, "http://localhost:5555", resp.URL)
	assert.True(t, resp.Enabled)
}

func TestCreateServer_DuplicateName(t *testing.T) {
	handlers, store := testSetup(t, defaultSharedServers())
	router := testRouter(handlers, defaultAuthContext())

	// Pre-create a personal server.
	sc := &config.ServerConfig{
		Name:     "existing-server",
		URL:      "http://localhost:1111",
		Protocol: "http",
		Enabled:  true,
		Created:  time.Now().UTC(),
	}
	require.NoError(t, store.CreateUserServer(testUserID, sc))

	body, _ := json.Marshal(CreateServerRequest{
		Name:     "existing-server",
		URL:      "http://localhost:2222",
		Protocol: "http",
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/user/servers", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusConflict, w.Code)
}

func TestCreateServer_ConflictsWithShared(t *testing.T) {
	handlers, _ := testSetup(t, defaultSharedServers())
	router := testRouter(handlers, defaultAuthContext())

	body, _ := json.Marshal(CreateServerRequest{
		Name:     "shared-github",
		URL:      "http://localhost:3333",
		Protocol: "http",
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/user/servers", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusConflict, w.Code)

	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Contains(t, resp["message"], "conflicts with a shared server")
}

func TestCreateServer_MissingName(t *testing.T) {
	handlers, _ := testSetup(t, defaultSharedServers())
	router := testRouter(handlers, defaultAuthContext())

	body, _ := json.Marshal(CreateServerRequest{
		URL:      "http://localhost:4444",
		Protocol: "http",
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/user/servers", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// --- Get server ---

func TestGetServer_Personal(t *testing.T) {
	handlers, store := testSetup(t, defaultSharedServers())
	router := testRouter(handlers, defaultAuthContext())

	sc := &config.ServerConfig{
		Name:     "my-personal",
		URL:      "http://localhost:7777",
		Protocol: "http",
		Enabled:  true,
		Created:  time.Now().UTC(),
	}
	require.NoError(t, store.CreateUserServer(testUserID, sc))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/user/servers/my-personal", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp ServerResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "my-personal", resp.Name)
	assert.Equal(t, "personal", resp.Ownership)
}

func TestGetServer_Shared(t *testing.T) {
	handlers, _ := testSetup(t, defaultSharedServers())
	router := testRouter(handlers, defaultAuthContext())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/user/servers/shared-github", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp ServerResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "shared-github", resp.Name)
	assert.Equal(t, "shared", resp.Ownership)
}

func TestGetServer_NotFound(t *testing.T) {
	handlers, _ := testSetup(t, defaultSharedServers())
	router := testRouter(handlers, defaultAuthContext())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/user/servers/nonexistent", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// --- Update server ---

func TestUpdateServer_Personal(t *testing.T) {
	handlers, store := testSetup(t, defaultSharedServers())
	router := testRouter(handlers, defaultAuthContext())

	sc := &config.ServerConfig{
		Name:     "update-me",
		URL:      "http://localhost:8888",
		Protocol: "http",
		Enabled:  true,
		Created:  time.Now().UTC(),
	}
	require.NoError(t, store.CreateUserServer(testUserID, sc))

	body, _ := json.Marshal(UpdateServerRequest{
		URL:      "http://localhost:9999",
		Protocol: "sse",
	})

	req := httptest.NewRequest(http.MethodPut, "/api/v1/user/servers/update-me", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp ServerResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "update-me", resp.Name)
	assert.Equal(t, "http://localhost:9999", resp.URL)
	assert.Equal(t, "sse", resp.Protocol)
	assert.Equal(t, "personal", resp.Ownership)
}

func TestUpdateServer_Shared(t *testing.T) {
	handlers, _ := testSetup(t, defaultSharedServers())
	router := testRouter(handlers, defaultAuthContext())

	body, _ := json.Marshal(UpdateServerRequest{
		URL: "http://evil.com",
	})

	req := httptest.NewRequest(http.MethodPut, "/api/v1/user/servers/shared-github", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestUpdateServer_NotFound(t *testing.T) {
	handlers, _ := testSetup(t, defaultSharedServers())
	router := testRouter(handlers, defaultAuthContext())

	body, _ := json.Marshal(UpdateServerRequest{
		URL: "http://localhost:1234",
	})

	req := httptest.NewRequest(http.MethodPut, "/api/v1/user/servers/nonexistent", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// --- Delete server ---

func TestDeleteServer_Personal(t *testing.T) {
	handlers, store := testSetup(t, defaultSharedServers())
	router := testRouter(handlers, defaultAuthContext())

	sc := &config.ServerConfig{
		Name:     "delete-me",
		URL:      "http://localhost:6666",
		Protocol: "http",
		Enabled:  true,
		Created:  time.Now().UTC(),
	}
	require.NoError(t, store.CreateUserServer(testUserID, sc))

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/user/servers/delete-me", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	// Verify it's gone.
	got, err := store.GetUserServer(testUserID, "delete-me")
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestDeleteServer_Shared(t *testing.T) {
	handlers, _ := testSetup(t, defaultSharedServers())
	router := testRouter(handlers, defaultAuthContext())

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/user/servers/shared-github", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestDeleteServer_NotFound(t *testing.T) {
	handlers, _ := testSetup(t, defaultSharedServers())
	router := testRouter(handlers, defaultAuthContext())

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/user/servers/nonexistent", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// --- Enable server ---

func TestEnableServer(t *testing.T) {
	handlers, store := testSetup(t, defaultSharedServers())
	router := testRouter(handlers, defaultAuthContext())

	sc := &config.ServerConfig{
		Name:     "toggle-me",
		URL:      "http://localhost:5050",
		Protocol: "http",
		Enabled:  true,
		Created:  time.Now().UTC(),
	}
	require.NoError(t, store.CreateUserServer(testUserID, sc))

	// Disable the server.
	body, _ := json.Marshal(EnableServerRequest{Enabled: false})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/user/servers/toggle-me/enable", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp ServerResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.False(t, resp.Enabled)
	assert.Equal(t, "personal", resp.Ownership)

	// Verify in store.
	got, err := store.GetUserServer(testUserID, "toggle-me")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.False(t, got.Enabled)

	// Re-enable the server.
	body, _ = json.Marshal(EnableServerRequest{Enabled: true})
	req = httptest.NewRequest(http.MethodPost, "/api/v1/user/servers/toggle-me/enable", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.True(t, resp.Enabled)
}

func TestEnableServer_SharedPreference(t *testing.T) {
	handlers, _ := testSetup(t, defaultSharedServers())
	router := testRouter(handlers, defaultAuthContext())

	// Users can set a per-user preference on shared servers
	body, _ := json.Marshal(EnableServerRequest{Enabled: false})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/user/servers/shared-github/enable", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	// Verify the response includes the user preference
	var resp ServerResponse
	err := json.NewDecoder(w.Body).Decode(&resp)
	assert.NoError(t, err)
	assert.Equal(t, "shared", resp.Ownership)
	assert.NotNil(t, resp.UserEnabled)
	assert.Equal(t, false, *resp.UserEnabled)
}

func TestEnableServer_NotFound(t *testing.T) {
	handlers, _ := testSetup(t, defaultSharedServers())
	router := testRouter(handlers, defaultAuthContext())

	body, _ := json.Marshal(EnableServerRequest{Enabled: true})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/user/servers/nonexistent/enable", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// --- Authentication ---

func TestListServers_Unauthenticated(t *testing.T) {
	handlers, _ := testSetup(t, defaultSharedServers())

	// Create router WITHOUT auth context middleware.
	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) {
		handlers.RegisterRoutes(r)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/user/servers", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}
