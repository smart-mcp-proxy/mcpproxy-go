//go:build !server

package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// clientPresenceController records the session page requested by the Clients
// handlers. Its explicit cap is the scalability boundary: the persistent
// session store retains a fixed recent page, and neither the list nor detail
// route may turn a client expansion into an unbounded history query.
type clientPresenceController struct {
	baseController
	limits []int
}

func (c *clientPresenceController) GetCurrentConfig() *config.Config {
	return &config.Config{APIKey: "clients-admin-key"}
}

func (c *clientPresenceController) GetConfig() (*config.Config, error) {
	return &config.Config{APIKey: "clients-admin-key", RoutingMode: config.RoutingModeRetrieveTools}, nil
}

func (c *clientPresenceController) GetRecentSessions(limit int, _ string) ([]*contracts.MCPSession, int, error) {
	c.limits = append(c.limits, limit)
	return []*contracts.MCPSession{{
		ID: "S1", ClientName: "claude-code", Status: "active",
		StartTime:    time.Date(2026, 9, 25, 6, 0, 0, 0, time.UTC),
		LastActivity: time.Date(2026, 9, 25, 6, 10, 0, 0, time.UTC),
	}}, 1, nil
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
