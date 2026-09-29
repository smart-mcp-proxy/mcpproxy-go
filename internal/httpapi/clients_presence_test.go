//go:build !server

package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/connect"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	proxyRuntime "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// clientPresenceController records the session page requested by the Clients
// handlers. Its explicit cap is the scalability boundary: the persistent
// session store retains a fixed recent page, and neither the list nor detail
// route may turn a client expansion into an unbounded history query.
type clientPresenceController struct {
	baseController
	limits   []int
	state    *storage.OnboardingState
	sessions []*contracts.MCPSession
	usage    *proxyRuntime.UsageAggregate
}

func (c *clientPresenceController) UsageSnapshot() *proxyRuntime.UsageAggregate {
	return c.usage
}

func (c *clientPresenceController) GetOnboardingState() (*storage.OnboardingState, error) {
	if c.state == nil {
		return &storage.OnboardingState{}, nil
	}
	copy := *c.state
	return &copy, nil
}

func (c *clientPresenceController) GetCurrentConfig() *config.Config {
	return &config.Config{APIKey: "clients-admin-key"}
}

func (c *clientPresenceController) GetConfig() (*config.Config, error) {
	return &config.Config{APIKey: "clients-admin-key", RoutingMode: config.RoutingModeRetrieveTools}, nil
}

func (c *clientPresenceController) GetRecentSessions(limit int, _ string) ([]*contracts.MCPSession, int, error) {
	c.limits = append(c.limits, limit)
	if c.sessions != nil {
		if len(c.sessions) > limit {
			return c.sessions[:limit], len(c.sessions), nil
		}
		return c.sessions, len(c.sessions), nil
	}
	return []*contracts.MCPSession{{
		ID: "S1", ClientName: "claude-code", Status: "active",
		StartTime:    time.Date(2026, 9, 25, 6, 0, 0, 0, time.UTC),
		LastActivity: time.Date(2026, 9, 25, 6, 10, 0, 0, time.UTC),
	}}, 1, nil
}

func TestClientsPresence_ReconnectFiltersPreviousGenerationSessions(t *testing.T) {
	connectedAt := time.Date(2026, 9, 29, 11, 0, 0, 0, time.UTC)
	ctrl := &clientPresenceController{
		state: &storage.OnboardingState{ClientConnectedAt: map[string]time.Time{"cursor": connectedAt}},
		sessions: []*contracts.MCPSession{
			{ID: "old", ClientName: "cursor", Status: "active", StartTime: connectedAt.Add(-time.Hour), LastActivity: connectedAt.Add(-time.Minute)},
			{ID: "current", ClientName: "cursor", Status: "active", StartTime: connectedAt.Add(time.Minute), LastActivity: connectedAt.Add(2 * time.Minute)},
		},
	}
	srv := NewServer(ctrl, zap.NewNop().Sugar(), nil)
	list := httptest.NewRequest(http.MethodGet, "/api/v1/clients", nil)
	list.Header.Set("X-API-Key", "clients-admin-key")
	listRec := httptest.NewRecorder()
	srv.ServeHTTP(listRec, list)
	require.Equal(t, http.StatusOK, listRec.Code, listRec.Body.String())
	var cursor map[string]any
	for _, raw := range successData(t, listRec)["clients"].([]any) {
		row := raw.(map[string]any)
		if row["id"] == "cursor" {
			cursor = row
			break
		}
	}
	require.NotNil(t, cursor)
	require.Equal(t, float64(1), cursor["active_sessions"])

	detail := httptest.NewRequest(http.MethodGet, "/api/v1/clients/cursor", nil)
	detail.Header.Set("X-API-Key", "clients-admin-key")
	detailRec := httptest.NewRecorder()
	srv.ServeHTTP(detailRec, detail)
	require.Equal(t, http.StatusOK, detailRec.Code, detailRec.Body.String())
	detailSessions := successData(t, detailRec)["sessions"].([]any)
	require.Len(t, detailSessions, 1)
	require.Equal(t, "current", detailSessions[0].(map[string]any)["id"])
}

func TestClientsPresence_ListIsLightweightAndDetailUsesBoundedSessionPage(t *testing.T) {
	ctrl := &clientPresenceController{}
	srv := NewServer(ctrl, zap.NewNop().Sugar(), nil)

	list := httptest.NewRequest(http.MethodGet, "/api/v1/clients", nil)
	list.Header.Set("X-API-Key", "clients-admin-key")
	listRec := httptest.NewRecorder()
	srv.ServeHTTP(listRec, list)
	require.Equal(t, http.StatusOK, listRec.Code, listRec.Body.String())
	listData := successData(t, listRec)
	clients := listData["clients"].([]any)
	require.NotEmpty(t, clients)
	for _, raw := range clients {
		row := raw.(map[string]any)
		_, hasSessions := row["sessions"]
		require.False(t, hasSessions, "list row must not materialize a sessions array")
		require.Contains(t, row, "active_sessions")
	}
	require.Equal(t, []int{100}, ctrl.limits, "list reads only the bounded recent-session page")
	require.NotNil(t, listData["routing"], "Clients response embeds the GET /routing payload")

	detail := httptest.NewRequest(http.MethodGet, "/api/v1/clients/claude-code", nil)
	detail.Header.Set("X-API-Key", "clients-admin-key")
	detailRec := httptest.NewRecorder()
	srv.ServeHTTP(detailRec, detail)
	require.Equal(t, http.StatusOK, detailRec.Code, detailRec.Body.String())
	detailData := successData(t, detailRec)
	require.Len(t, detailData["sessions"].([]any), 1)
	require.Equal(t, []int{100, 100}, ctrl.limits, "detail uses the same bounded session page")
}

func TestClientsPresence_RejectsUnsupportedScopeBeforeReadingSessions(t *testing.T) {
	ctrl := &clientPresenceController{}
	srv := NewServer(ctrl, zap.NewNop().Sugar(), nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/clients?client=cursor", nil)
	req.Header.Set("X-API-Key", "clients-admin-key")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	require.Empty(t, ctrl.limits, "scope rejection must precede session access")
}

func TestClientsPresence_AdministratorOnly(t *testing.T) {
	readers := []struct {
		name       string
		key        string
		wantStatus int
	}{
		{name: "missing-key", wantStatus: http.StatusUnauthorized},
		{name: "invalid-key", key: "not-admin", wantStatus: http.StatusUnauthorized},
		{name: "agent-token", wantStatus: http.StatusForbidden},
	}
	for _, path := range []string{"/api/v1/clients", "/api/v1/clients/cursor"} {
		t.Run(path, func(t *testing.T) {
			for _, reader := range readers {
				t.Run(reader.name, func(t *testing.T) {
					ctrl := &clientPresenceController{}
					srv, token := agentTokenServer(t, ctrl)
					key := reader.key
					if reader.name == "agent-token" {
						key = token
					}
					req := httptest.NewRequest(http.MethodGet, path, nil)
					if key != "" {
						req.Header.Set("X-API-Key", key)
					}
					rec := httptest.NewRecorder()
					srv.ServeHTTP(rec, req)
					require.Equal(t, reader.wantStatus, rec.Code, rec.Body.String())
				})
			}

			t.Run("administrator-allowed", func(t *testing.T) {
				ctrl := &clientPresenceController{}
				srv := NewServer(ctrl, zap.NewNop().Sugar(), nil)
				req := httptest.NewRequest(http.MethodGet, path, nil)
				req.Header.Set("X-API-Key", "clients-admin-key")
				rec := httptest.NewRecorder()
				srv.ServeHTTP(rec, req)
				require.NotEqual(t, http.StatusUnauthorized, rec.Code, rec.Body.String())
				require.NotEqual(t, http.StatusForbidden, rec.Code, rec.Body.String())
			})
		})
	}
}

func TestClientsPresence_DetailResolvesHandConfiguredConnection(t *testing.T) {
	home := t.TempDir()
	cfgPath := connect.ConfigPath("cursor", home)
	require.NoError(t, os.MkdirAll(filepath.Dir(cfgPath), 0o755))
	require.NoError(t, os.WriteFile(cfgPath, []byte(`{"mcpServers":{"mcpproxy":{"url":"http://127.0.0.1:8080/mcp"}}}`), 0o600))

	ctrl := &clientPresenceController{}
	srv := NewServer(ctrl, zap.NewNop().Sugar(), nil)
	srv.SetConnectService(connect.NewServiceWithHome("127.0.0.1:8080", "", home))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/clients/cursor", nil)
	req.Header.Set("X-API-Key", "clients-admin-key")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	data := successData(t, rec)
	require.Equal(t, true, data["connected"])
	require.Equal(t, "connected_never_seen", data["state"])
	require.NotEqual(t, true, data["connection_unverified"])
}

func TestClientsPresence_UsesClientCallAggregateOnWire(t *testing.T) {
	bucket := time.Now().UTC().Truncate(time.Hour).Unix()
	ctrl := &clientPresenceController{usage: &proxyRuntime.UsageAggregate{
		ClientCalls: map[string]map[int64]int64{"claude-code": {bucket: 7}},
	}}
	srv := NewServer(ctrl, zap.NewNop().Sugar(), nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/clients", nil)
	req.Header.Set("X-API-Key", "clients-admin-key")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	for _, raw := range successData(t, rec)["clients"].([]any) {
		row := raw.(map[string]any)
		if row["id"] == "claude-code" {
			require.Equal(t, float64(7), row["calls_24h"])
			return
		}
	}
	t.Fatal("Claude Code presence row is missing")
}

func successData(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var envelope struct {
		Success bool           `json:"success"`
		Data    map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope), rec.Body.String())
	require.True(t, envelope.Success)
	return envelope.Data
}
