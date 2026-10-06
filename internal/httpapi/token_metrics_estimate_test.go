package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
)

// Spec 109-k / audit finding F-Token: GET /api/v1/stats/tokens must carry
// contracts.ServerTokenMetrics.Estimated through unchanged, so the Web UI,
// macOS and CLI can render an "estimate" label before any real retrieve_tools
// call has happened, and drop it once real usage exists.

func doTokenStatsRequest(t *testing.T, srv *Server) (*httptest.ResponseRecorder, *contracts.ServerTokenMetrics) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/stats/tokens", nil)
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		return w, nil
	}
	var resp struct {
		Success bool                         `json:"success"`
		Data    contracts.ServerTokenMetrics `json:"data"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	return w, &resp.Data
}

func TestTokenStats_EstimatedTrueBeforeRealRetrieve(t *testing.T) {
	ctrl := &mockUsageController{
		apiKey: "test-key",
		tokens: &contracts.ServerTokenMetrics{
			AverageQueryResultSize: 1500, // non-zero synthetic estimate
			Estimated:              true,
		},
	}
	srv := NewServer(ctrl, zap.NewNop().Sugar(), nil)

	w, data := doTokenStatsRequest(t, srv)
	require.Equal(t, http.StatusOK, w.Code)
	require.NotNil(t, data)
	assert.True(t, data.Estimated)
	assert.NotZero(t, data.AverageQueryResultSize)
}

func TestTokenStats_EstimatedFalseAfterRealCalls(t *testing.T) {
	ctrl := &mockUsageController{
		apiKey: "test-key",
		tokens: &contracts.ServerTokenMetrics{
			AverageQueryResultSize: 900, // derived from real usage
			Estimated:              false,
		},
	}
	srv := NewServer(ctrl, zap.NewNop().Sugar(), nil)

	w, data := doTokenStatsRequest(t, srv)
	require.Equal(t, http.StatusOK, w.Code)
	require.NotNil(t, data)
	assert.False(t, data.Estimated)
	assert.NotZero(t, data.AverageQueryResultSize)
}
