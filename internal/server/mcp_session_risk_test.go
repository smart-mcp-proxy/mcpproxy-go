package server

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime/stateview"
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

// sessionRiskOf extracts the session_risk block from a retrieve_tools
// response as a plain map, for the T071 (FR-005 G3) scope assertions below.
func sessionRiskOf(t *testing.T, result *mcp.CallToolResult) map[string]interface{} {
	t.Helper()
	require.False(t, result.IsError)
	responseText := result.Content[0].(mcp.TextContent).Text
	var response map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(responseText), &response))
	sessionRisk, ok := response["session_risk"].(map[string]interface{})
	require.True(t, ok, "session_risk must be present and be an object")
	return sessionRisk
}

// TestRetrieveTools_SessionRisk_ScopedToAuthorizedServers is T071 (Spec 105
// FR-005 G3): a scoped caller's session_risk MUST be computed over its own
// authorized servers' tool annotations only. Server "b"'s tool carries nil
// annotations — the MCP-spec most-permissive default that alone trips every
// risk flag — so an a-only view that leaked "b" in would read "high" instead
// of "low". Deleting "b" entirely must not change the a-only view (proving
// "b" never contributed), while an administrator keeps seeing the full,
// unrestricted "high" verdict throughout (not a named SC-005 exception here —
// session_risk has none).
func TestRetrieveTools_SessionRisk_ScopedToAuthorizedServers(t *testing.T) {
	if testing.Short() {
		t.Skip("integration — needs runtime")
	}
	proxy, rt, _ := buildMCPProxyWithActivation(t)

	rt.Supervisor().StateView().UpdateServer("a", func(s *stateview.ServerStatus) {
		s.Name = "a"
		s.Connected = true
		s.ToolsDiscovered = true
		s.Tools = []stateview.ToolInfo{{
			Name: "read_tool",
			Annotations: &config.ToolAnnotations{
				ReadOnlyHint: boolPtr(true), DestructiveHint: boolPtr(false), OpenWorldHint: boolPtr(false),
			},
		}}
	})
	rt.Supervisor().StateView().UpdateServer("b", func(s *stateview.ServerStatus) {
		s.Name = "b"
		s.Connected = true
		s.ToolsDiscovered = true
		s.Tools = []stateview.ToolInfo{{Name: "danger_tool"}} // nil annotations
	})
	for _, srv := range []string{"a", "b"} {
		require.NoError(t, proxy.storage.SaveUpstreamServer(&config.ServerConfig{Name: srv, Enabled: true}))
	}

	newReq := func() mcp.CallToolRequest {
		r := mcp.CallToolRequest{}
		r.Params.Arguments = map[string]interface{}{"query": "anything"}
		return r
	}

	// Baseline: an administrator sees the full-fleet "high" / lethal-trifecta
	// verdict "b" alone contributes.
	adminResult, err := proxy.handleRetrieveToolsWithMode(adminCtx(), newReq(), config.RoutingModeRetrieveTools)
	require.NoError(t, err)
	adminRisk := sessionRiskOf(t, adminResult)
	assert.Equal(t, "high", adminRisk["level"])
	assert.Equal(t, true, adminRisk["lethal_trifecta"])

	scopedCtx := agentCtx([]string{"a"}, []string{auth.PermRead}, "")
	scopedResult, err := proxy.handleRetrieveToolsWithMode(scopedCtx, newReq(), config.RoutingModeRetrieveTools)
	require.NoError(t, err)
	scopedRisk := sessionRiskOf(t, scopedResult)
	assert.Equal(t, "low", scopedRisk["level"], "an a-only caller must never see risk contributed by hidden server b")
	assert.Equal(t, false, scopedRisk["lethal_trifecta"])

	// Deleting "b" entirely must not change the a-only view — proof that "b"
	// never leaked into it in the first place.
	rt.Supervisor().StateView().RemoveServer("b")
	scopedAfterDelete, err := proxy.handleRetrieveToolsWithMode(scopedCtx, newReq(), config.RoutingModeRetrieveTools)
	require.NoError(t, err)
	assert.Equal(t, scopedRisk, sessionRiskOf(t, scopedAfterDelete), "a-only session_risk must be deep-equal before and after b is removed")
}
