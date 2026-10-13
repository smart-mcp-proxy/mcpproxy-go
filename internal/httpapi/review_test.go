package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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

// TestSSE_ReviewChangedOutOfScopeAndEmptyServerAreDroppedForScopedSubscriber
// pins T075 / FR-006. review.changed is an identity-bearing notification, so
// a scoped subscriber must not receive either an out-of-scope server name or
// a malformed frame with no server identity. The admin frames are the positive
// control: this proves the runtime did publish both events.
func TestSSE_ReviewChangedOutOfScopeAndEmptyServerAreDroppedForScopedSubscriber(t *testing.T) {
	ctrl := &scopeController{cfg: scopeFixtureConfig(false), servers: scopeFixtureServers(), withManagement: true}
	srv, token := scopedAgentServer(t, ctrl, []string{"alpha"})

	ts := httptest.NewServer(srv)
	defer ts.Close()

	adminBody, adminClose := sseSubscribe(t, ts.URL, scopeAdminAPIKey)
	defer adminClose()
	agentBody, agentClose := sseSubscribe(t, ts.URL, token)
	defer agentClose()

	require.Eventually(t, func() bool { return ctrl.subscriberCount() == 2 }, 5*time.Second, 20*time.Millisecond,
		"precondition: both SSE connections must be subscribed to the event bus")

	ctrl.publishToAll(runtime.Event{
		Type:      runtime.EventTypeReviewChanged,
		Payload:   map[string]any{"server": "beta"},
		Timestamp: time.Now(),
	})
	ctrl.publishToAll(runtime.Event{
		Type:      runtime.EventTypeReviewChanged,
		Payload:   map[string]any{"server": ""},
		Timestamp: time.Now(),
	})
	// A visible identity-bearing event is the sentinel that makes the scoped
	// read decisive: it must skip both review frames and then receive this one.
	ctrl.publishToAll(runtime.Event{
		Type:      runtime.EventTypeOAuthTokenRefreshed,
		Payload:   map[string]any{"server_name": "alpha"},
		Timestamp: time.Now(),
	})

	adminFirst := sseReadRuntimeEvent(t, adminBody)
	adminSecond := sseReadRuntimeEvent(t, adminBody)
	require.Equal(t, string(runtime.EventTypeReviewChanged), adminFirst.event)
	require.Equal(t, string(runtime.EventTypeReviewChanged), adminSecond.event)
	var firstEnvelope, secondEnvelope struct {
		Payload map[string]any `json:"payload"`
	}
	require.NoError(t, json.Unmarshal([]byte(adminFirst.data), &firstEnvelope))
	require.NoError(t, json.Unmarshal([]byte(adminSecond.data), &secondEnvelope))
	require.Equal(t, "beta", firstEnvelope.Payload["server"])
	require.Equal(t, "", secondEnvelope.Payload["server"])

	agentFirst := sseReadRuntimeEvent(t, agentBody)
	require.Equal(t, string(runtime.EventTypeOAuthTokenRefreshed), agentFirst.event,
		"scoped subscriber must skip both out-of-scope and empty-server review frames")
}
