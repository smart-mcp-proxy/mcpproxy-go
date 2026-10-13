package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/cliclient"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/httpapi"
)

// TestCodeExecHandler_CLIDaemonModeRoundTrip drives the real CLI client against
// the real handler with the flag values `mcpproxy code exec` ships by default:
// --timeout=120000, --max-tool-calls=0 (unlimited) and an empty
// --allowed-servers. The CLI always sends all three, so the option-presence
// rules on the server side have to keep accepting that request and preserve
// each flag's meaning.
func TestCodeExecHandler_CLIDaemonModeRoundTrip(t *testing.T) {
	var dispatched map[string]interface{}
	ctrl := &mockController{
		callToolFunc: func(_ context.Context, _ string, args map[string]interface{}) (interface{}, error) {
			dispatched = args
			resultJSON, err := json.Marshal(map[string]interface{}{"ok": true, "value": "done"})
			require.NoError(t, err)
			return []interface{}{map[string]interface{}{"type": "text", "text": string(resultJSON)}}, nil
		},
	}

	mux := http.NewServeMux()
	mux.Handle("/api/v1/code/exec", httpapi.NewCodeExecHandler(ctrl, zap.NewNop().Sugar()))
	server := httptest.NewServer(mux)
	defer server.Close()

	// The CLI's own defaults, as validateOptions() lets them through.
	const cliTimeoutMS = 120000
	const cliMaxToolCalls = 0
	cliAllowedServers := []string{}

	result, err := cliclient.NewClient(server.URL, nil).CodeExec(
		context.Background(),
		"({ result: 1 })",
		map[string]interface{}{},
		cliTimeoutMS,
		cliMaxToolCalls,
		cliAllowedServers,
	)
	require.NoError(t, err)
	require.True(t, result.OK, "the CLI's default flags were rejected: %+v", result.Error)

	require.NotNil(t, dispatched, "the request never reached the tool")
	options, ok := dispatched["options"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, cliTimeoutMS, options["timeout_ms"])
	assert.Equal(t, cliMaxToolCalls, options["max_tool_calls"],
		"--max-tool-calls=0 means unlimited and must reach the tool as sent")
	assert.Equal(t, cliAllowedServers, options["allowed_servers"])
}

// TestCodeExecHandler_CLIFlagsReachTheTool asserts the dispatched options carry
// exactly what the CLI asked for.
func TestCodeExecHandler_CLIFlagsReachTheTool(t *testing.T) {
	var dispatched map[string]interface{}
	ctrl := &mockController{
		callToolFunc: func(_ context.Context, _ string, args map[string]interface{}) (interface{}, error) {
			dispatched = args
			resultJSON, err := json.Marshal(map[string]interface{}{"ok": true, "value": "done"})
			require.NoError(t, err)
			return []interface{}{map[string]interface{}{"type": "text", "text": string(resultJSON)}}, nil
		},
	}

	mux := http.NewServeMux()
	mux.Handle("/api/v1/code/exec", httpapi.NewCodeExecHandler(ctrl, zap.NewNop().Sugar()))
	server := httptest.NewServer(mux)
	defer server.Close()

	result, err := cliclient.NewClient(server.URL, nil).CodeExec(
		context.Background(),
		"({ result: 1 })",
		map[string]interface{}{},
		60000,
		5,
		[]string{"github"},
	)
	require.NoError(t, err)
	require.True(t, result.OK, "CLI round trip failed: %+v", result.Error)

	require.NotNil(t, dispatched)
	options, ok := dispatched["options"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, 60000, options["timeout_ms"])
	assert.Equal(t, 5, options["max_tool_calls"])
	assert.Equal(t, []string{"github"}, options["allowed_servers"])
}
