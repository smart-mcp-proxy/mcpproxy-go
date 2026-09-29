package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

func TestReviewRoutesReturnQueueAndServerShapes(t *testing.T) {
	controller := &reviewControllerMock{
		mockActivityController: mockActivityController{apiKey: "admin-key"},
		queue: &runtime.ReviewQueue{
			Count:   1,
			Servers: []runtime.ReviewQueueRow{{Server: "alpha", Kind: "tool_review", Pending: 1}},
		},
	}
	srv := NewServer(controller, zap.NewNop().Sugar(), nil)

	queueReq := httptest.NewRequest(http.MethodGet, "/api/v1/review", nil)
	queueReq.Header.Set("X-API-Key", "admin-key")
	queueRec := httptest.NewRecorder()
	srv.ServeHTTP(queueRec, queueReq)
	require.Equal(t, http.StatusOK, queueRec.Code)
	var queueResponse struct {
		Success bool                `json:"success"`
		Data    runtime.ReviewQueue `json:"data"`
	}
	require.NoError(t, json.Unmarshal(queueRec.Body.Bytes(), &queueResponse))
	require.True(t, queueResponse.Success)
	require.Equal(t, 1, queueResponse.Data.Count)
	require.Equal(t, "alpha", queueResponse.Data.Servers[0].Server)

	detailReq := httptest.NewRequest(http.MethodGet, "/api/v1/servers/alpha/review", nil)
	detailReq.Header.Set("X-API-Key", "admin-key")
	detailRec := httptest.NewRecorder()
	srv.ServeHTTP(detailRec, detailReq)
	require.Equal(t, http.StatusOK, detailRec.Code)
	var detailResponse struct {
		Success bool `json:"success"`
		Data    struct {
			Server struct {
				Name string `json:"name"`
			} `json:"server"`
			Tools []any `json:"tools"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(detailRec.Body.Bytes(), &detailResponse))
	require.True(t, detailResponse.Success)
	require.Equal(t, "alpha", detailResponse.Data.Server.Name)
	require.NotNil(t, detailResponse.Data.Tools)
}
