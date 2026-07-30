package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
)

// mockSessionController records the status argument the handler pushes down, so
// the tests can prove the filter reaches storage rather than being applied (or
// dropped) in the handler.
type mockSessionController struct {
	baseController
	apiKey    string
	sessions  []*contracts.MCPSession
	gotLimit  int
	gotStatus string
	callCount int
}

func (m *mockSessionController) GetCurrentConfig() any {
	return &config.Config{APIKey: m.apiKey}
}

func (m *mockSessionController) GetRecentSessions(limit int, status string) ([]*contracts.MCPSession, int, error) {
	m.callCount++
	m.gotLimit = limit
	m.gotStatus = status

	var out []*contracts.MCPSession
	for _, s := range m.sessions {
		if status == "" || s.Status == status {
			out = append(out, s)
		}
	}
	return out, len(out), nil
}

func testSessions() []*contracts.MCPSession {
	start := time.Date(2026, 7, 29, 10, 0, 0, 0, time.UTC)
	return []*contracts.MCPSession{
		{ID: "sess-active", ClientName: "claude-code", Status: "active", StartTime: start, LastActivity: start.Add(time.Hour)},
		{ID: "sess-closed", ClientName: "cursor", Status: "closed", StartTime: start.Add(time.Minute), LastActivity: start.Add(2 * time.Minute)},
	}
}

func decodeSessionsResponse(t *testing.T, w *httptest.ResponseRecorder) contracts.GetSessionsResponse {
	t.Helper()
	var resp struct {
		Success bool                          `json:"success"`
		Data    contracts.GetSessionsResponse `json:"data"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	require.True(t, resp.Success)
	return resp.Data
}

func TestGetSessions_StatusFilter(t *testing.T) {
	logger := zap.NewNop().Sugar()

	t.Run("status=active is pushed down and narrows the result", func(t *testing.T) {
		ctrl := &mockSessionController{apiKey: "test-key", sessions: testSessions()}
		srv := NewServer(ctrl, logger, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions?status=active&limit=25", nil)
		req.Header.Set("X-API-Key", "test-key")
		w := httptest.NewRecorder()

		srv.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "active", ctrl.gotStatus, "the status filter must reach the controller, not be applied in the handler")
		assert.Equal(t, 25, ctrl.gotLimit)

		data := decodeSessionsResponse(t, w)
		require.Len(t, data.Sessions, 1)
		assert.Equal(t, "sess-active", data.Sessions[0].ID)
		assert.Equal(t, 1, data.Total)
		assert.Equal(t, 25, data.Limit)
	})

	t.Run("no status parameter means no filter", func(t *testing.T) {
		ctrl := &mockSessionController{apiKey: "test-key", sessions: testSessions()}
		srv := NewServer(ctrl, logger, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions", nil)
		req.Header.Set("X-API-Key", "test-key")
		w := httptest.NewRecorder()

		srv.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "", ctrl.gotStatus)
		assert.Equal(t, 10, ctrl.gotLimit, "default limit for sessions is 10")

		data := decodeSessionsResponse(t, w)
		assert.Len(t, data.Sessions, 2)
	})

	t.Run("status=closed selects closed sessions", func(t *testing.T) {
		ctrl := &mockSessionController{apiKey: "test-key", sessions: testSessions()}
		srv := NewServer(ctrl, logger, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions?status=closed", nil)
		req.Header.Set("X-API-Key", "test-key")
		w := httptest.NewRecorder()

		srv.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Code)
		data := decodeSessionsResponse(t, w)
		require.Len(t, data.Sessions, 1)
		assert.Equal(t, "sess-closed", data.Sessions[0].ID)
	})

	t.Run("unknown status is rejected and never reaches the controller", func(t *testing.T) {
		ctrl := &mockSessionController{apiKey: "test-key", sessions: testSessions()}
		srv := NewServer(ctrl, logger, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions?status=bogus", nil)
		req.Header.Set("X-API-Key", "test-key")
		w := httptest.NewRecorder()

		srv.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Equal(t, 0, ctrl.callCount, "a rejected filter must not fall through to an unfiltered query")

		var errResp contracts.ErrorResponse
		require.NoError(t, json.NewDecoder(w.Body).Decode(&errResp))
		assert.Contains(t, errResp.Error, "Invalid status")
	})
}
