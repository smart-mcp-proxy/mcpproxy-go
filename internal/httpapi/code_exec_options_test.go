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

// TestCodeExecHandler_ForwardsExplicitZeroOptions pins that presence, not
// value, decides what is dispatched. A zero is a value the caller chose:
// max_tool_calls 0 means "no limit of my own" and must reach the tool, which
// applies its own default resolution to it. Inferring "unset" from the zero
// dropped it, so a configured limit kept applying to a request that opted out.
func TestCodeExecHandler_ForwardsExplicitZeroOptions(t *testing.T) {
	recorder, args := execCodeRequest(t, map[string]interface{}{
		"code": "({ result: 1 })",
		"options": map[string]interface{}{
			"max_tool_calls": 0,
		},
	})

	require.Equal(t, http.StatusOK, recorder.Code)
	require.NotNil(t, args)

	options, ok := args["options"].(map[string]interface{})
	require.True(t, ok)
	require.Contains(t, options, "max_tool_calls", "an explicitly sent option must be forwarded")
	assert.Equal(t, 0, options["max_tool_calls"])
	assert.NotContains(t, options, "timeout_ms", "an omitted option must still be absent")
}

// TestCodeExecHandler_ForwardsExplicitEmptyAllowedServers pins the nil-vs-empty
// distinction on the list option. An explicit empty array is forwarded as an
// empty array, which the tool reads exactly as it reads one over MCP — the two
// surfaces must not disagree about what `"allowed_servers": []` means.
func TestCodeExecHandler_ForwardsExplicitEmptyAllowedServers(t *testing.T) {
	recorder, args := execCodeRequest(t, map[string]interface{}{
		"code": "({ result: 1 })",
		"options": map[string]interface{}{
			"allowed_servers": []string{},
		},
	})

	require.Equal(t, http.StatusOK, recorder.Code)
	require.NotNil(t, args)

	options, ok := args["options"].(map[string]interface{})
	require.True(t, ok)
	require.Contains(t, options, "allowed_servers")
	assert.Equal(t, []string{}, options["allowed_servers"])
}

// TestCodeExecHandler_NullOptionsAreUnset pins that a JSON null is absence, not
// a value: it must dispatch nothing rather than an empty restriction.
func TestCodeExecHandler_NullOptionsAreUnset(t *testing.T) {
	recorder, args := execCodeRequest(t, map[string]interface{}{
		"code": "({ result: 1 })",
		"options": map[string]interface{}{
			"timeout_ms":      nil,
			"max_tool_calls":  nil,
			"allowed_servers": nil,
		},
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
		{
			// The tool rejects a present timeout_ms of 0 (valid range is
			// 1-600000). Inferring "unset" from the zero value made this
			// endpoint accept it and silently substitute the configured
			// budget instead.
			name:    "explicit zero timeout_ms",
			options: map[string]interface{}{"timeout_ms": 0},
			message: "timeout_ms must be between 1 and 600000 milliseconds",
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
