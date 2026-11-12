package httpapi

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/telemetry"
)

type countingFeedbackSubmitter struct{ calls atomic.Int32 }

func (c *countingFeedbackSubmitter) SubmitFeedback(_ context.Context, _ *telemetry.FeedbackRequest) (*telemetry.FeedbackResponse, error) {
	c.calls.Add(1)
	return &telemetry.FeedbackResponse{}, nil
}

// POST /api/v1/feedback submits under the install identity and spends the
// shared rate limit, so a scoped agent token must be refused.
func TestFeedback_AdminOnly(t *testing.T) {
	body := []byte(`{"category":"bug","message":"something is broken in the proxy"}`)

	t.Run("agent token is refused and nothing is submitted", func(t *testing.T) {
		ctrl := &adminConfigController{ServerController: &MockServerController{}, apiKey: "admin-secret"}
		srv, agentToken := agentTokenServer(t, ctrl)
		sub := &countingFeedbackSubmitter{}
		srv.SetFeedbackSubmitter(sub)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/feedback", bytes.NewReader(body))
		req.Header.Set("X-API-Key", agentToken)
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)

		require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
		require.Zero(t, sub.calls.Load())
	})

	t.Run("admin API key still succeeds", func(t *testing.T) {
		ctrl := &adminConfigController{ServerController: &MockServerController{}, apiKey: "admin-secret"}
		srv, _ := agentTokenServer(t, ctrl)
		sub := &countingFeedbackSubmitter{}
		srv.SetFeedbackSubmitter(sub)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/feedback", bytes.NewReader(body))
		req.Header.Set("X-API-Key", "admin-secret")
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)

		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		require.Equal(t, int32(1), sub.calls.Load())
	})
}

type tierRefusingReplayController struct {
	*MockServerController
	apiKey string
}

func (c *tierRefusingReplayController) GetCurrentConfig() *config.Config {
	return &config.Config{APIKey: c.apiKey}
}

func (c *tierRefusingReplayController) GetToolCallByID(_ string) (*contracts.ToolCallRecord, error) {
	return &contracts.ToolCallRecord{ID: "c1", ServerName: "test-server", ToolName: "delete_repo"}, nil
}

func (c *tierRefusingReplayController) ReplayToolCall(_ context.Context, _ string, _ map[string]interface{}) (*contracts.ToolCallRecord, error) {
	return nil, &profile.ToolBlockedError{Reason: "token_permission", Message: "Permission denied: token does not have 'destructive' permission"}
}

// The replay handler maps the permission-tier refusal to 403.
func TestReplayToolCall_TokenPermissionRefusalIs403(t *testing.T) {
	ctrl := &tierRefusingReplayController{MockServerController: &MockServerController{}, apiKey: "admin-secret"}
	srv, agentToken := agentTokenServer(t, ctrl)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/tool-calls/c1/replay", bytes.NewReader([]byte(`{"arguments":{"x":1}}`)))
	req.Header.Set("X-API-Key", agentToken)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
}
