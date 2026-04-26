package server

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// TestRetrieveTools_SessionRisk_DefaultNoWarning verifies the issue #406 fix:
// when no opt-in is set, the response's session_risk field MUST contain the
// structured risk fields but MUST NOT contain the verbose `warning` prose.
//
// This is end-to-end through the handler with a real runtime + supervisor wired
// up; the runtime's stateview is initially empty, so the risk level is "low"
// and there is no trifecta — but the test still confirms the field shape and
// the absence of the prose warning, which is the contract callers depend on.
func TestRetrieveTools_SessionRisk_DefaultNoWarning(t *testing.T) {
	if testing.Short() {
		t.Skip("integration — needs runtime")
	}
	proxy, _, _ := buildMCPProxyWithActivation(t)
	require.False(t, proxy.config.ToolResponseSessionRiskWarning,
		"default config must keep the prose warning OFF (issue #406)")

	req := mcp.CallToolRequest{}
	req.Params.Name = "retrieve_tools"
	req.Params.Arguments = map[string]interface{}{"query": "anything"}

	result, err := proxy.handleRetrieveToolsWithMode(context.Background(), req, config.RoutingModeRetrieveTools)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.IsError)

	responseText := result.Content[0].(mcp.TextContent).Text
	var response map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(responseText), &response))

	sessionRisk, ok := response["session_risk"].(map[string]interface{})
	require.True(t, ok, "session_risk must be present and be an object")

	// Structured fields are always present.
	assert.Contains(t, sessionRisk, "level")
	assert.Contains(t, sessionRisk, "has_open_world_tools")
	assert.Contains(t, sessionRisk, "has_destructive_tools")
	assert.Contains(t, sessionRisk, "has_write_tools")
	assert.Contains(t, sessionRisk, "lethal_trifecta")

	// Prose warning is OFF by default — issue #406 fix.
	_, hasWarning := sessionRisk["warning"]
	assert.False(t, hasWarning, "default response must NOT include prose warning")
}

// TestRetrieveTools_SessionRisk_PerCallOptInDoesNotBreakWhenLowRisk verifies
// that opting in via the per-call argument is accepted by the handler without
// errors, even when the warning would not fire (low risk → no Warning string).
func TestRetrieveTools_SessionRisk_PerCallOptInDoesNotBreakWhenLowRisk(t *testing.T) {
	if testing.Short() {
		t.Skip("integration — needs runtime")
	}
	proxy, _, _ := buildMCPProxyWithActivation(t)

	req := mcp.CallToolRequest{}
	req.Params.Name = "retrieve_tools"
	req.Params.Arguments = map[string]interface{}{
		"query":                        "anything",
		"include_session_risk_warning": true,
	}

	result, err := proxy.handleRetrieveToolsWithMode(context.Background(), req, config.RoutingModeRetrieveTools)
	require.NoError(t, err)
	require.False(t, result.IsError)

	responseText := result.Content[0].(mcp.TextContent).Text
	var response map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(responseText), &response))

	sessionRisk, ok := response["session_risk"].(map[string]interface{})
	require.True(t, ok)

	// No upstreams connected → low risk → analyzeSessionRisk returns no Warning,
	// so even with opt-in there is nothing to render.
	_, hasWarning := sessionRisk["warning"]
	assert.False(t, hasWarning, "low-risk session has no Warning to render even when opted in")
}
