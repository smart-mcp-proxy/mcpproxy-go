package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/httpapi"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/jsruntime"
)

// TestApplyCodeExecutionOptionsValueShapes pins the option shapes the parser
// must accept. Arguments reach handleCodeExecution either JSON-decoded (over
// the MCP transport: float64 and []interface{}) or as Go-typed values from an
// in-process caller that builds the arguments map directly — the REST handler
// behind POST /api/v1/code/exec is one. Accepting only the JSON shapes made
// every Go-typed restriction vanish silently.
func TestApplyCodeExecutionOptionsValueShapes(t *testing.T) {
	tests := []struct {
		name    string
		options map[string]interface{}
		want    jsruntime.ExecutionOptions
		errMsg  string
	}{
		{
			name: "JSON-decoded values",
			options: map[string]interface{}{
				"timeout_ms":      float64(300000),
				"max_tool_calls":  float64(1),
				"allowed_servers": []interface{}{"github", "slack"},
			},
			want: jsruntime.ExecutionOptions{
				TimeoutMs:      300000,
				MaxToolCalls:   1,
				AllowedServers: []string{"github", "slack"},
			},
		},
		{
			name: "Go-typed values from an in-process caller",
			options: map[string]interface{}{
				"timeout_ms":      300000,
				"max_tool_calls":  1,
				"allowed_servers": []string{"github", "slack"},
			},
			want: jsruntime.ExecutionOptions{
				TimeoutMs:      300000,
				MaxToolCalls:   1,
				AllowedServers: []string{"github", "slack"},
			},
		},
		{
			name: "json.Number values from a UseNumber decoder",
			options: map[string]interface{}{
				"timeout_ms":     json.Number("300000"),
				"max_tool_calls": json.Number("1"),
			},
			want: jsruntime.ExecutionOptions{TimeoutMs: 300000, MaxToolCalls: 1},
		},
		{
			name:    "absent options leave the zero values for config defaults",
			options: map[string]interface{}{},
			want:    jsruntime.ExecutionOptions{},
		},
		{
			name:    "timeout_ms above the maximum is rejected",
			options: map[string]interface{}{"timeout_ms": 600001},
			errMsg:  "timeout_ms must be between 1 and 600000 milliseconds",
		},
		{
			name:    "negative max_tool_calls is rejected",
			options: map[string]interface{}{"max_tool_calls": -1},
			errMsg:  "max_tool_calls cannot be negative",
		},
		{
			name:    "allowed_servers with a non-string element is rejected",
			options: map[string]interface{}{"allowed_servers": []interface{}{"github", 42}},
			errMsg:  "allowed_servers must be an array of strings",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var got jsruntime.ExecutionOptions
			errMsg := applyCodeExecutionOptions(tc.options, &got)

			if tc.errMsg != "" {
				assert.Equal(t, tc.errMsg, errMsg)
				return
			}
			require.Empty(t, errMsg)
			assert.Equal(t, tc.want.TimeoutMs, got.TimeoutMs, "timeout_ms")
			assert.Equal(t, tc.want.MaxToolCalls, got.MaxToolCalls, "max_tool_calls")
			assert.Equal(t, tc.want.AllowedServers, got.AllowedServers, "allowed_servers")
		})
	}
}

// codeExecArgsCapture records the arguments the REST handler dispatches to the
// code_execution tool, standing in for the controller's CallTool.
type codeExecArgsCapture struct {
	args map[string]interface{}
}

func (c *codeExecArgsCapture) CallTool(_ context.Context, _ string, args map[string]interface{}) (interface{}, error) {
	c.args = args
	resultJSON, err := json.Marshal(map[string]interface{}{"ok": true, "value": nil})
	if err != nil {
		return nil, err
	}
	return []interface{}{map[string]interface{}{"type": "text", "text": string(resultJSON)}}, nil
}

// TestRESTCodeExecOptionsReachTheParser joins the two halves of the REST code
// execution path: the httpapi handler builds the code_execution arguments, and
// handleCodeExecution parses them. The restrictions a caller sends over REST
// must survive that handoff — when they did not, a request that limited itself
// to one tool call on one server actually ran unrestricted.
func TestRESTCodeExecOptionsReachTheParser(t *testing.T) {
	body, err := json.Marshal(map[string]interface{}{
		"code":  "({ result: 1 })",
		"input": map[string]interface{}{},
		"options": map[string]interface{}{
			"timeout_ms":      300000,
			"max_tool_calls":  1,
			"allowed_servers": []string{"github"},
		},
	})
	require.NoError(t, err)

	capture := &codeExecArgsCapture{}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/code/exec", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	httpapi.NewCodeExecHandler(capture, zap.NewNop().Sugar()).ServeHTTP(recorder, req)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.NotNil(t, capture.args, "handler did not dispatch to code_execution")

	optionsObj, ok := capture.args["options"].(map[string]interface{})
	require.True(t, ok, "code_execution arguments carry no options object")

	var options jsruntime.ExecutionOptions
	require.Empty(t, applyCodeExecutionOptions(optionsObj, &options))

	assert.Equal(t, 300000, options.TimeoutMs, "timeout_ms was dropped between REST and the tool")
	assert.Equal(t, 1, options.MaxToolCalls, "max_tool_calls was dropped between REST and the tool")
	assert.Equal(t, []string{"github"}, options.AllowedServers, "allowed_servers was dropped between REST and the tool")
}

// TestRESTCodeExecOmittedOptionsKeepConfigDefaults guards the other direction:
// options the caller did not send must stay unset so handleCodeExecution
// resolves them from config (code_execution_timeout_ms / max_tool_calls) and
// leaves allowed_servers meaning "no restriction".
func TestRESTCodeExecOmittedOptionsKeepConfigDefaults(t *testing.T) {
	body, err := json.Marshal(map[string]interface{}{
		"code":  "({ result: 1 })",
		"input": map[string]interface{}{},
	})
	require.NoError(t, err)

	capture := &codeExecArgsCapture{}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/code/exec", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	httpapi.NewCodeExecHandler(capture, zap.NewNop().Sugar()).ServeHTTP(recorder, req)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.NotNil(t, capture.args)

	optionsObj, ok := capture.args["options"].(map[string]interface{})
	require.True(t, ok)

	var options jsruntime.ExecutionOptions
	require.Empty(t, applyCodeExecutionOptions(optionsObj, &options))

	assert.Zero(t, options.TimeoutMs, "an unsent timeout_ms must fall through to the configured default")
	assert.Zero(t, options.MaxToolCalls, "an unsent max_tool_calls must fall through to the configured default")
	assert.Empty(t, options.AllowedServers, "an unsent allowed_servers must not restrict anything")
}
