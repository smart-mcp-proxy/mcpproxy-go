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
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
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

func (m *shedController) GetCurrentConfig() *config.Config {
	return &config.Config{APIKey: m.apiKey}
}

func (m *shedController) CallTool(_ context.Context, _ string, _ map[string]interface{}) (interface{}, error) {
	return nil, m.err
}

func postToolCall(t *testing.T, srv *Server, apiKey string) *httptest.ResponseRecorder {
	return postToolCallNamed(t, srv, apiKey, "call_tool_read")
}

func postToolCallNamed(t *testing.T, srv *Server, apiKey, toolName string) *httptest.ResponseRecorder {
	t.Helper()
	bodyJSON, err := json.Marshal(map[string]interface{}{"tool_name": toolName, "arguments": map[string]interface{}{}})
	require.NoError(t, err)
	body := strings.NewReader(string(bodyJSON))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tools/call", body)
	req.Header.Set("X-API-Key", apiKey)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	return w
}

func TestHandleCallTool_ProfileHiddenCodeExecutionMatchesUnknownTool(t *testing.T) {
	apiKey := "test-profile-code-exec-api-key"
	profileCtrl := &shedController{apiKey: apiKey, err: fmt.Errorf("tool call failed: %w", profile.ErrCodeExecutionBlocked)}
	unknownCtrl := &shedController{apiKey: apiKey, err: fmt.Errorf("tool call failed: unknown tool: no_such_tool")}
	profileResponse := postToolCallNamed(t, NewServer(profileCtrl, zap.NewNop().Sugar(), nil), apiKey, "code_execution")
	unknownResponse := postToolCallNamed(t, NewServer(unknownCtrl, zap.NewNop().Sugar(), nil), apiKey, "no_such_tool")

	require.Equal(t, http.StatusInternalServerError, profileResponse.Code)
	require.Equal(t, unknownResponse.Code, profileResponse.Code)
	var profileBody, unknownBody map[string]interface{}
	require.NoError(t, json.Unmarshal(profileResponse.Body.Bytes(), &profileBody))
	require.NoError(t, json.Unmarshal(unknownResponse.Body.Bytes(), &unknownBody))
	require.Equal(t, strings.ReplaceAll(unknownBody["error"].(string), "no_such_tool", "code_execution"), profileBody["error"])
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

func TestHandleCallTool_ProfileBlockedReturns403(t *testing.T) {
	apiKey := "test-profile-api-key"
	message := "blocked by profile: github:create_issue is a write tool; this profile allows read tools only"
	ctrl := &shedController{
		apiKey: apiKey,
		err:    &profile.ToolBlockedError{Reason: profile.BlockReasonTier, Message: message},
	}
	srv := NewServer(ctrl, zap.NewNop().Sugar(), nil)

	w := postToolCall(t, srv, apiKey)

	require.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, w.Body.String(), message)
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

func TestReplayToolCall_ProfileGateUses403AndNonDisclosing404(t *testing.T) {
	apiKey := "test-replay-profile-api-key"
	message := "blocked by profile: github:create_issue is denied by a profile rule"
	cases := []struct {
		name string
		err  error
		want int
		body string
	}{
		{name: "policy denial", err: &profile.ToolBlockedError{Reason: profile.BlockReasonRule, Message: message}, want: http.StatusForbidden, body: message},
		{name: "server outside profile", err: profile.ErrToolOutsideProfile, want: http.StatusNotFound, body: "Tool call not found"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := &replayShedController{shedController{apiKey: apiKey, err: tc.err}}
			srv := NewServer(ctrl, zap.NewNop().Sugar(), nil)
			req := httptest.NewRequest(http.MethodPost, "/api/v1/tool-calls/hidden-call/replay", strings.NewReader(`{}`))
			req.Header.Set("X-API-Key", apiKey)
			w := httptest.NewRecorder()
			srv.ServeHTTP(w, req)
			require.Equal(t, tc.want, w.Code)
			assert.Contains(t, w.Body.String(), tc.body)
		})
	}
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
