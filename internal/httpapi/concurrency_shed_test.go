package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/reqcontext"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/limiter"
)

// shedController answers every tool call with the typed limiter rejection,
// wrapped the way the real dispatch chain wraps it (Server.CallTool adds
// "tool call failed: %w") so the handler's errors.As has to look through a
// wrapper, as it does in production.
type shedController struct {
	baseController
	apiKey string
	err    error
}

func (m *shedController) GetCurrentConfig() any {
	return &config.Config{APIKey: m.apiKey}
}

func (m *shedController) CallTool(_ context.Context, _ string, _ map[string]interface{}) (interface{}, error) {
	return nil, m.err
}

func postToolCall(t *testing.T, srv *Server, apiKey string) *httptest.ResponseRecorder {
	t.Helper()
	body := strings.NewReader(`{"tool_name":"call_tool_read","arguments":{"name":"db:query"}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tools/call", body)
	req.Header.Set("X-API-Key", apiKey)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	return w
}

// TestHandleCallTool_ShedReturns429WithRetryAfter is the FR-011 contract: the
// REST surface answers a concurrency shed with 429 and a Retry-After hint
// derived from the shedding scope's effective queue_timeout — not the blanket
// 500 a flattened string error would have produced.
func TestHandleCallTool_ShedReturns429WithRetryAfter(t *testing.T) {
	t.Setenv("CI", "")
	apiKey := "test-shed-api-key"

	cases := []struct {
		name        string
		limitErr    *limiter.LimitError
		wantRetry   string
		wantInBody  string
		notInBody   string
		wantCodeMsg string
	}{
		{
			name: "server scope queue full",
			limitErr: &limiter.LimitError{
				Scope: limiter.ScopeServer, Reason: limiter.ReasonQueueFull,
				Server: "analytics-db", Limit: 2, RetryAfter: 30 * time.Second,
			},
			wantRetry:  "30",
			wantInBody: "analytics-db",
		},
		{
			name: "global scope queue timeout rounds up",
			limitErr: &limiter.LimitError{
				Scope: limiter.ScopeGlobal, Reason: limiter.ReasonQueueTimeout,
				Limit: 20, RetryAfter: 1500 * time.Millisecond,
			},
			wantRetry:  "2",
			wantInBody: "proxy-wide",
			notInBody:  "analytics-db",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := &shedController{
				apiKey: apiKey,
				err:    fmt.Errorf("tool call failed: %w", tc.limitErr),
			}
			srv := NewServer(ctrl, zap.NewNop().Sugar(), nil)

			w := postToolCall(t, srv, apiKey)

			require.Equal(t, http.StatusTooManyRequests, w.Code)
			assert.Equal(t, tc.wantRetry, w.Header().Get("Retry-After"))

			var body map[string]interface{}
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
			msg, _ := body["error"].(string)
			assert.Contains(t, msg, tc.wantInBody)
			assert.Contains(t, msg, limiter.RetryAdvice)
			if tc.notInBody != "" {
				assert.NotContains(t, msg, tc.notInBody)
			}
		})
	}
}

// TestHandleCallTool_ServerUnavailableIsNot429 keeps FR-009 separate from
// FR-011: a server that went away mid-queue is not backpressure.
func TestHandleCallTool_ServerUnavailableIsNot429(t *testing.T) {
	t.Setenv("CI", "")
	apiKey := "test-shed-api-key"

	ctrl := &shedController{
		apiKey: apiKey,
		err: fmt.Errorf("tool call failed: %w", &limiter.LimitError{
			Scope: limiter.ScopeServer, Reason: limiter.ReasonServerUnavailable, Server: "db",
		}),
	}
	srv := NewServer(ctrl, zap.NewNop().Sugar(), nil)

	w := postToolCall(t, srv, apiKey)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Empty(t, w.Header().Get("Retry-After"))
}

// TestHandleReplayToolCall_ShedReturns429 extends FR-011 to the replay endpoint.
// Replay used to flatten the rejection into the new record's Error field and
// return no error at all, so a shed answered 200 with success:true — a client
// could not tell a replay that never ran from one that did.
func TestHandleReplayToolCall_ShedReturns429(t *testing.T) {
	t.Setenv("CI", "")
	apiKey := "test-shed-api-key"

	ctrl := &replayShedController{shedController{
		apiKey: apiKey,
		err: fmt.Errorf("tool call failed: %w", &limiter.LimitError{
			Scope: limiter.ScopeServer, Reason: limiter.ReasonQueueTimeout,
			Server: "analytics-db", Limit: 2, RetryAfter: 30 * time.Second,
		}),
	}}
	srv := NewServer(ctrl, zap.NewNop().Sugar(), nil)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/tool-calls/call-1/replay", strings.NewReader(`{}`))
	req.Header.Set("X-API-Key", apiKey)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	require.Equal(t, http.StatusTooManyRequests, w.Code)
	assert.Equal(t, "30", w.Header().Get("Retry-After"))

	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, false, body["success"])
	msg, _ := body["error"].(string)
	assert.Contains(t, msg, "analytics-db")
	assert.Contains(t, msg, limiter.RetryAdvice)
}

type replayShedController struct {
	shedController
}

func (m *replayShedController) ReplayToolCall(_ context.Context, _ string, _ map[string]interface{}) (*contracts.ToolCallRecord, error) {
	return nil, m.err
}

// TestRetryAfterSeconds covers the delta-seconds conversion, including the
// "never say retry now" rounding rule.
func TestRetryAfterSeconds(t *testing.T) {
	assert.Equal(t, 1, retryAfterSeconds(0))
	assert.Equal(t, 1, retryAfterSeconds(-5*time.Second))
	assert.Equal(t, 1, retryAfterSeconds(10*time.Millisecond))
	assert.Equal(t, 2, retryAfterSeconds(1100*time.Millisecond))
	assert.Equal(t, 30, retryAfterSeconds(30*time.Second))
}

// TestToolCallRequestSource is the P3 origin-attribution fix. POST
// /api/v1/tools/call is shared by the CLI, the Web UI and the tray, and it used
// to stamp every one of them as CLI — overwriting the REST source the
// middleware had set, so a Web-UI tool call (and any shed of one) was logged
// against the wrong origin.
func TestToolCallRequestSource(t *testing.T) {
	cases := []struct {
		header string
		want   reqcontext.RequestSource
	}{
		{"cli/v0.52.0", reqcontext.SourceCLI},
		{"CLI/dev", reqcontext.SourceCLI},
		{"webui/web", reqcontext.SourceRESTAPI},
		{"tray/v0.52.0", reqcontext.SourceRESTAPI},
		{"", reqcontext.SourceRESTAPI},
		{"clipper/1.0", reqcontext.SourceRESTAPI},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/tools/call", strings.NewReader("{}"))
		if tc.header != "" {
			req.Header.Set(XMCPProxyClientHeader, tc.header)
		}
		assert.Equal(t, tc.want, toolCallRequestSource(req), "header %q", tc.header)
	}
}
