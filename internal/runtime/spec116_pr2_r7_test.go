package runtime

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime/stateview"
)

// Spec 116 PR2 review round 7 runtime tests.

// Between a removal and a same-name re-add the committed snapshot has no such
// server, so there is no incarnation to bind a decision to. A StateView that
// lags the removal still holds the removed server's row and tools; serving them
// would hand the Web UI unbound rows (incarnation 0, so no
// expected_incarnation), and a decision on them would later bind to whichever
// same-name server exists when the operator clicks. Neither the per-server
// read nor the server list may serve an absent server.
func TestServerTools_R7_RemovedServerNotServedFromLaggingView(t *testing.T) {
	cfgA := explicitServer(&config.ServerConfig{Name: "acme", URL: "http://127.0.0.1:9/a", Protocol: "http", Enabled: true})
	rt, _ := newSpec116Runtime(t, nil, cfgA)
	incA, ok := rt.ServerIncarnation("acme")
	require.True(t, ok)
	rt.supervisor.StateView().UpdateServer("acme", func(status *stateview.ServerStatus) {
		status.Connected = true
		status.State = "ready"
		status.Incarnation = incA
		status.Tools = []stateview.ToolInfo{{Name: "a_tool", Description: "A's definition"}}
		status.ToolCount = 1
		status.ToolsDiscovered = true
	})

	// Control: A is listed with its tool.
	tools, err := rt.GetServerTools("acme")
	require.NoError(t, err)
	require.Len(t, tools, 1)
	servers, err := rt.GetAllServers()
	require.NoError(t, err)
	require.True(t, hasServerRow(servers, "acme"), "control: A is listed")

	// Removal only; the supervisor (not started) leaves A in the view.
	_, err = rt.Commit("remove_server", func(tx *CommitTx) error { return tx.RemoveServer("acme") })
	require.NoError(t, err)
	_, ok = rt.ServerIncarnation("acme")
	require.False(t, ok)
	status, _ := rt.supervisor.StateView().GetServer("acme")
	require.NotNil(t, status, "the view lags the removal")
	require.Len(t, status.Tools, 1)

	tools, err = rt.GetServerTools("acme")
	require.Error(t, err, "an absent server's tools are not served")
	assert.Contains(t, err.Error(), "not found")
	assert.Empty(t, tools)
	servers, err = rt.GetAllServers()
	require.NoError(t, err)
	assert.False(t, hasServerRow(servers, "acme"), "an absent server is not listed")

	// Re-add B: B is listed and A's definitions are never served for it.
	_, err = rt.Commit("create_server", func(tx *CommitTx) error {
		return tx.CreateServer(explicitServer(&config.ServerConfig{Name: "acme", URL: "http://127.0.0.1:9/b", Protocol: "http", Enabled: true}))
	})
	require.NoError(t, err)
	tools, err = rt.GetServerTools("acme")
	require.NoError(t, err)
	for _, tool := range tools {
		assert.NotEqual(t, "a_tool", tool["name"])
	}
}

func hasServerRow(rows []map[string]interface{}, name string) bool {
	for _, row := range rows {
		if n, _ := row["name"].(string); n == name {
			return true
		}
	}
	return false
}
