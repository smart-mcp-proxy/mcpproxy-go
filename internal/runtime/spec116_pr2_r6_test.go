package runtime

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime/stateview"
)

// Spec 116 PR2 review round 6 runtime tests.

// The REST tool listings (per-server and global) read the committed
// incarnation and then the tools from Runtime.GetServerTools, which serves the
// supervisor's StateView. A view that lags a remove + same-name re-add still
// holds the removed server's definitions; serving them would label A's
// descriptions and schemas with B's incarnation, and an approval carrying that
// stamp would pass for definitions the operator never reviewed. The view's
// tools are served only when its incarnation is the committed one. The control
// read (before the removal) proves the assertion is not vacuous.
func TestServerTools_R6_LaggingViewNotServedUnderReplacementIncarnation(t *testing.T) {
	cfgA := explicitServer(&config.ServerConfig{Name: "acme", URL: "http://127.0.0.1:9/a", Protocol: "http", Enabled: true})
	rt, _ := newSpec116Runtime(t, nil, cfgA)
	incA, ok := rt.ServerIncarnation("acme")
	require.True(t, ok)

	// A's tools, as reconcile + discovery published them for incarnation A.
	rt.supervisor.StateView().UpdateServer("acme", func(status *stateview.ServerStatus) {
		status.Connected = true
		status.State = "ready"
		status.Incarnation = incA
		status.Tools = []stateview.ToolInfo{{Name: "a_tool", Description: "A's definition",
			InputSchema: map[string]interface{}{"type": "object"}}}
		status.ToolCount = 1
		status.ToolsDiscovered = true
	})
	tools, err := rt.GetServerTools("acme")
	require.NoError(t, err)
	require.Len(t, tools, 1, "control: the view's tools are served under their own incarnation")
	assert.Equal(t, "a_tool", tools[0]["name"])

	// Remove and re-add B with different tool definitions while the
	// supervisor's reconciliation is parked (it is not started here), so the
	// StateView still holds A's tools under A's incarnation.
	removeAndReAdd(t, rt, explicitServer(&config.ServerConfig{
		Name: "acme", URL: "http://127.0.0.1:9/b", Protocol: "http", Enabled: true}))
	incB, ok := rt.ServerIncarnation("acme")
	require.True(t, ok)
	require.NotEqual(t, incA, incB)
	status, _ := rt.supervisor.StateView().GetServer("acme")
	require.NotNil(t, status)
	require.Len(t, status.Tools, 1, "the view lags the commit")

	tools, err = rt.GetServerTools("acme")
	require.NoError(t, err)
	for _, tool := range tools {
		assert.NotEqual(t, "a_tool", tool["name"], "A's definition must never be served while B is the committed server")
	}
}
