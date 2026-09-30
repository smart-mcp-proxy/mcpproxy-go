package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// scopedSessionController applies storage.SessionFilter the way the manager
// does (filter, then count, then truncate), so total is the filtered count.
type scopedSessionController struct {
	mockSessionController
}

func (m *scopedSessionController) GetRecentSessions(f storage.SessionFilter) ([]*contracts.MCPSession, int, error) {
	m.callCount++
	m.gotFilter = f
	match := func(want, have string) bool {
		switch want {
		case "":
			return true
		case storage.ScopeFilterUnattributed:
			return have == ""
		default:
			return have == want
		}
	}
	var out []*contracts.MCPSession
	for _, s := range m.sessions {
		if f.Status != "" && s.Status != f.Status {
			continue
		}
		if match(f.Profile, s.Profile) && match(f.ClientID, s.ClientID) && match(f.TokenName, s.TokenName) {
			out = append(out, s)
		}
	}
	total := len(out)
	if f.Limit > 0 && len(out) > f.Limit {
		out = out[:f.Limit]
	}
	return out, total, nil
}

func scopeTestSessions() []*contracts.MCPSession {
	start := time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)
	return []*contracts.MCPSession{
		{ID: "s-cursor", ClientName: "Cursor", Status: "active", StartTime: start, ClientID: "cursor", TokenName: "client-cursor", Profile: "work-readonly", ProfileSource: "pin"},
		{ID: "s-zed", ClientName: "Zed", Status: "active", StartTime: start, ClientID: "zed", TokenName: "client-zed", Profile: "work-full", ProfileSource: "binding"},
		{ID: "s-api", ClientName: "curl", Status: "closed", StartTime: start},
	}
}

func TestSessionsScope_RowsCarryAttributionAndFiltersReachStorage(t *testing.T) {
	newCtrl := func() *scopedSessionController {
		return &scopedSessionController{mockSessionController{apiKey: "test-key", sessions: scopeTestSessions()}}
	}
	fetch := func(path string) (*scopedSessionController, *httptest.ResponseRecorder, contracts.GetSessionsResponse) {
		ctrl := newCtrl()
		srv := NewServer(ctrl, zap.NewNop().Sugar(), nil)
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("X-API-Key", "test-key")
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)
		var data contracts.GetSessionsResponse
		if w.Code == http.StatusOK {
			data = decodeSessionsResponse(t, w)
		}
		return ctrl, w, data
	}

	ctrl, w, data := fetch("/api/v1/sessions?client=cursor")
	require.Equal(t, http.StatusOK, w.Code)
	require.Len(t, data.Sessions, 1)
	s := data.Sessions[0]
	assert.Equal(t, "s-cursor", s.ID)
	assert.Equal(t, "cursor", s.ClientID)
	assert.Equal(t, "client-cursor", s.TokenName)
	assert.Equal(t, "work-readonly", s.Profile)
	assert.Equal(t, "pin", s.ProfileSource)
	assert.Equal(t, "cursor", ctrl.gotFilter.ClientID, "the filter is pushed down to storage")
	assert.Equal(t, 1, data.Total, "total is the filtered count")

	_, _, data = fetch("/api/v1/sessions?profile=-")
	require.Len(t, data.Sessions, 1)
	assert.Equal(t, "s-api", data.Sessions[0].ID)

	ctrl, _, data = fetch("/api/v1/sessions?token=client-zed")
	require.Len(t, data.Sessions, 1)
	assert.Equal(t, "s-zed", data.Sessions[0].ID)
	assert.Equal(t, "client-zed", ctrl.gotFilter.TokenName)

	ctrl, _, data = fetch("/api/v1/sessions?agent=client-zed")
	require.Len(t, data.Sessions, 1, "agent is an alias of token")
	assert.Equal(t, "client-zed", ctrl.gotFilter.TokenName)

	ctrl, w, _ = fetch("/api/v1/sessions?token=a&agent=b")
	require.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "token and agent must name the same token (agent is an alias of token)")
	assert.Zero(t, ctrl.callCount, "a rejected filter never reaches storage")

	ctrl, w, _ = fetch("/api/v1/sessions?client_name=Cursor")
	require.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "client_name is not supported on this endpoint; filter by client")
	assert.Zero(t, ctrl.callCount, "the check runs before any storage read")
}
