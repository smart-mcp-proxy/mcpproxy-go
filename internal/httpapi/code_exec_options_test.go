package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/httpapi"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// execCodeRequest runs one request through the code exec handler and returns
// the recorder plus the arguments the handler dispatched (nil when it never
// reached the tool).
func execCodeRequest(t *testing.T, body map[string]interface{}) (*httptest.ResponseRecorder, map[string]interface{}) {
	t.Helper()

	bodyBytes, err := json.Marshal(body)
	require.NoError(t, err)

	var dispatched map[string]interface{}
	mockCtrl := &mockController{
		callToolFunc: func(_ context.Context, _ string, args map[string]interface{}) (interface{}, error) {
			dispatched = args
			resultJSON, err := json.Marshal(map[string]interface{}{"ok": true, "value": nil})
			require.NoError(t, err)
			return []interface{}{map[string]interface{}{"type": "text", "text": string(resultJSON)}}, nil
		},
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/code/exec", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	httpapi.NewCodeExecHandler(mockCtrl, zap.NewNop().Sugar()).ServeHTTP(recorder, req)

	return recorder, dispatched
}

// TestCodeExecHandler_ForwardsRestrictions proves the execution restrictions a
// REST caller sends reach the code_execution tool. They used to be built into
// the arguments in a shape the tool's parser rejected, so a caller that scoped
// its script to one server got an unrestricted run.
func TestCodeExecHandler_ForwardsRestrictions(t *testing.T) {
	recorder, args := execCodeRequest(t, map[string]interface{}{
		"code": "({ result: 1 })",
		"options": map[string]interface{}{
			"timeout_ms":      300000,
			"max_tool_calls":  1,
			"allowed_servers": []string{"github"},
		},
	})

	require.Equal(t, http.StatusOK, recorder.Code)
	require.NotNil(t, args)

	options, ok := args["options"].(map[string]interface{})
	require.True(t, ok, "options must be dispatched as an object")
	assert.Equal(t, 300000, options["timeout_ms"])
	assert.Equal(t, 1, options["max_tool_calls"])
	assert.Equal(t, []string{"github"}, options["allowed_servers"])
}

// TestCodeExecHandler_OmitsUnsetOptions pins the default semantics: an option
// the caller left out must not be dispatched at all, so the tool resolves it
// from config rather than from this endpoint's request-level fallback.
func TestCodeExecHandler_OmitsUnsetOptions(t *testing.T) {
	recorder, args := execCodeRequest(t, map[string]interface{}{
		"code": "({ result: 1 })",
	})

	require.Equal(t, http.StatusOK, recorder.Code)
	require.NotNil(t, args)

	options, ok := args["options"].(map[string]interface{})
	require.True(t, ok)
	assert.NotContains(t, options, "timeout_ms")
	assert.NotContains(t, options, "max_tool_calls")
	assert.NotContains(t, options, "allowed_servers")
}

// TestCodeExecHandler_RejectsOutOfRangeOptions covers the bounds the
// code_execution tool enforces. Left to the tool they come back as a plain-text
// tool error the response parser cannot read, so the endpoint checks them
// itself and answers 400.
func TestCodeExecHandler_RejectsOutOfRangeOptions(t *testing.T) {
	tests := []struct {
		name    string
		options map[string]interface{}
		message string
	}{
		{
			name:    "timeout_ms above the maximum",
			options: map[string]interface{}{"timeout_ms": 600001},
			message: "timeout_ms must be between 1 and 600000 milliseconds",
		},
		{
			name:    "negative timeout_ms",
			options: map[string]interface{}{"timeout_ms": -1},
			message: "timeout_ms must be between 1 and 600000 milliseconds",
		},
		{
			name:    "negative max_tool_calls",
			options: map[string]interface{}{"max_tool_calls": -1},
			message: "max_tool_calls cannot be negative",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			recorder, args := execCodeRequest(t, map[string]interface{}{
				"code":    "({ result: 1 })",
				"options": tc.options,
			})

			assert.Equal(t, http.StatusBadRequest, recorder.Code)
			assert.Nil(t, args, "an invalid request must not reach the tool")

			var response map[string]interface{}
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
			errorMap, ok := response["error"].(map[string]interface{})
			require.True(t, ok)
			assert.Equal(t, "INVALID_OPTIONS", errorMap["code"])
			assert.Equal(t, tc.message, errorMap["message"])
		})
	}
}
