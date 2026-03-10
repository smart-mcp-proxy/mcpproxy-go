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
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/teams/multiuser"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/teams/users"
)

// mockActivityProvider implements multiuser.ActivityStorageProvider for tests.
type mockActivityProvider struct {
	records []*storage.ActivityRecord
}

func (m *mockActivityProvider) ListActivities(filter storage.ActivityFilter) ([]*storage.ActivityRecord, int, error) {
	var matched []*storage.ActivityRecord
	for _, r := range m.records {
		matched = append(matched, r)
	}
	total := len(matched)

	// Apply pagination.
	start := filter.Offset
	if start > len(matched) {
		start = len(matched)
	}
	end := start + filter.Limit
	if end > len(matched) {
		end = len(matched)
	}

	return matched[start:end], total, nil
}

func (m *mockActivityProvider) GetActivity(id string) (*storage.ActivityRecord, error) {
	for _, r := range m.records {
		if r.ID == id {
			return r, nil
		}
	}
	return nil, nil
}

// activityTestSetup creates UserActivityHandlers with a mock activity provider.
func activityTestSetup(t *testing.T, records []*storage.ActivityRecord, sharedServers []*config.ServerConfig) (*UserActivityHandlers, *users.UserStore) {
	t.Helper()

	tmpFile := filepath.Join(t.TempDir(), "test.db")
	db, err := bbolt.Open(tmpFile, 0600, &bbolt.Options{Timeout: 1 * time.Second})
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	store := users.NewUserStore(db)
	require.NoError(t, store.EnsureBuckets())

	provider := &mockActivityProvider{records: records}
	activityFilter := multiuser.NewActivityFilter(provider)
	logger := zap.NewNop().Sugar()

	handlers := NewUserActivityHandlers(activityFilter, store, sharedServers, logger)
	return handlers, store
}

// activityTestRouter creates a chi router with user activity handlers and auth context.
func activityTestRouter(handlers *UserActivityHandlers, authCtx *auth.AuthContext) *chi.Mux {
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

// --- Activity tests ---

func TestUserActivity_ReturnsUserRecords(t *testing.T) {
	records := []*storage.ActivityRecord{
		{
			ID:         "rec1",
			Type:       storage.ActivityTypeToolCall,
			ServerName: "github",
			ToolName:   "list_repos",
			Status:     "success",
			Timestamp:  time.Now().UTC(),
			UserID:     testUserID,
			UserEmail:  "test@example.com",
		},
		{
			ID:         "rec2",
			Type:       storage.ActivityTypeToolCall,
			ServerName: "gitlab",
			ToolName:   "create_issue",
			Status:     "success",
			Timestamp:  time.Now().UTC(),
			UserID:     testUserID,
			UserEmail:  "test@example.com",
		},
	}

	handlers, _ := activityTestSetup(t, records, nil)
	router := activityTestRouter(handlers, defaultAuthContext())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/user/activity", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp ActivityListResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, 2, resp.Total)
}

func TestUserActivity_WithPagination(t *testing.T) {
	records := make([]*storage.ActivityRecord, 5)
	for i := 0; i < 5; i++ {
		records[i] = &storage.ActivityRecord{
			ID:        "rec" + string(rune('A'+i)),
			Type:      storage.ActivityTypeToolCall,
			Status:    "success",
			Timestamp: time.Now().UTC(),
			UserID:    testUserID,
		}
	}

	handlers, _ := activityTestSetup(t, records, nil)
	router := activityTestRouter(handlers, defaultAuthContext())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/user/activity?limit=2&offset=0", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp ActivityListResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	// Total should be the full count of matching records.
	assert.Equal(t, 5, resp.Total)
}

func TestUserActivity_Unauthenticated(t *testing.T) {
	handlers, _ := activityTestSetup(t, nil, nil)
	router := activityTestRouter(handlers, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/user/activity", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// --- Diagnostics tests ---

func TestUserDiagnostics_ShowsServers(t *testing.T) {
	shared := defaultSharedServers()
	handlers, store := activityTestSetup(t, nil, shared)
	router := activityTestRouter(handlers, defaultAuthContext())

	// Create a personal server for the user.
	sc := &config.ServerConfig{
		Name:     "my-personal-server",
		URL:      "http://localhost:9999",
		Protocol: "http",
		Enabled:  true,
		Created:  time.Now().UTC(),
	}
	require.NoError(t, store.CreateUserServer(testUserID, sc))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/user/diagnostics", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp DiagnosticsResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	// Should have 2 shared + 1 personal = 3 servers.
	assert.Len(t, resp.Servers, 3)

	// Verify shared servers come first.
	assert.Equal(t, "shared-github", resp.Servers[0].Name)
	assert.Equal(t, "shared", resp.Servers[0].Ownership)
	assert.Equal(t, "shared-gitlab", resp.Servers[1].Name)
	assert.Equal(t, "shared", resp.Servers[1].Ownership)

	// Verify personal server.
	assert.Equal(t, "my-personal-server", resp.Servers[2].Name)
	assert.Equal(t, "personal", resp.Servers[2].Ownership)
	assert.True(t, resp.Servers[2].Enabled)
}

func TestUserDiagnostics_NoServers(t *testing.T) {
	handlers, _ := activityTestSetup(t, nil, nil)
	router := activityTestRouter(handlers, defaultAuthContext())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/user/diagnostics", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp DiagnosticsResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Empty(t, resp.Servers)
}

func TestUserDiagnostics_Unauthenticated(t *testing.T) {
	handlers, _ := activityTestSetup(t, nil, defaultSharedServers())
	router := activityTestRouter(handlers, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/user/diagnostics", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}
