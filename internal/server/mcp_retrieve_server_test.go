package server

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime/stateview"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

func retrieveServerText(t *testing.T, p *MCPProxyServer, ctx context.Context, args map[string]any) string {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	result, err := p.handleRetrieveToolsWithMode(ctx, req, config.RoutingModeRetrieveTools)
	require.NoError(t, err)
	require.False(t, result.IsError, "%v", result.Content)
	return resultText(t, result)
}

func TestRetrieveToolsWithoutServerGolden(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	seedScopeSearchFixture(t, proxy)
	args := map[string]any{"query": "rotate keys", "limit": float64(1), "debug": true, "include_stats": true}
	// Captured with the pre-selector handler, including its ranking and debug fields.
	want, err := os.ReadFile("testdata/retrieve_tools_without_server.golden.json")
	require.NoError(t, err)
	assert.Equal(t, strings.TrimRight(string(want), "\r\n"), retrieveServerText(t, proxy, adminCtx(), args))
}

func TestRetrieveToolsServerBeforeLimit(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	seedScopeSearchFixture(t, proxy)
	require.NoError(t, proxy.index.IndexTool(&config.ToolMetadata{
		Name: "b:rotate_keys", ServerName: "b", Description: "rotate keys", ParamsJSON: "{}",
	}))
	args := map[string]any{"query": "rotate keys", "limit": float64(1), "debug": true}

	unfiltered := callRetrieveScoped(t, proxy, adminCtx(), args)
	require.Len(t, unfiltered.Tools, 1)
	require.Equal(t, "b", unfiltered.Tools[0]["server"], "fixture must displace the a hit without the selector")

	args["server"] = "a"
	selected := callRetrieveScoped(t, proxy, adminCtx(), args)
	assert.Equal(t, []string{"a:rotate_keys"}, toolNamesOf(selected))
	assert.Equal(t, 1, selected.Debug.TotalIndexedTools)

	args["query"] = "rotate_keys"
	args["server"] = "b"
	args["limit"] = float64(10)
	selected = callRetrieveScoped(t, proxy, adminCtx(), args)
	assert.Contains(t, toolNamesOf(selected), "b:rotate_keys")
	assert.NotContains(t, toolNamesOf(selected), "a:rotate_keys")
	assert.Equal(t, 3, selected.Debug.TotalIndexedTools)

	// An orphaned document cannot make an unconfigured upstream observable.
	require.NoError(t, proxy.index.IndexTool(&config.ToolMetadata{
		Name: "stale:rotate_keys", ServerName: "stale", Description: "rotate keys", ParamsJSON: "{}",
	}))
	args["include_disabled"] = true
	args["server"] = "unknown"
	empty := retrieveServerText(t, proxy, adminCtx(), args)
	args["server"] = "stale"
	assert.Equal(t, empty, retrieveServerText(t, proxy, adminCtx(), args))
}

func TestRetrieveToolsServerRespectsTokenAndPin(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	seedScopeSearchFixture(t, proxy)
	require.NoError(t, proxy.storage.SaveUpstreamServer(&config.ServerConfig{Name: "empty", Enabled: true}))
	for _, server := range []string{"a", "b", "empty", "outside"} {
		proxy.config.Servers = append(proxy.config.Servers, &config.ServerConfig{Name: server, Enabled: true})
	}
	proxy.config.Profiles = []config.ProfileConfig{{
		Name: "pinned", Servers: []string{"a", "b", "empty"},
		Tools: &config.ProfileToolRules{Deny: []string{"a:denied"}},
	}}
	require.NoError(t, proxy.storage.SaveUpstreamServer(&config.ServerConfig{Name: "outside", Enabled: true}))
	require.NoError(t, proxy.index.IndexTool(&config.ToolMetadata{
		Name: "outside:rotate_keys", ServerName: "outside", Description: "rotate keys", ParamsJSON: "{}",
	}))
	require.NoError(t, proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "outside", ToolName: "rotate_keys", Status: storage.ToolApprovalStatusPending,
	}))

	// Seed the pinned index independently so the policy branch is exercised
	// even though these fixtures have no runtime index publisher.
	pinnedIndex, err := proxy.index.ForProfile("pinned")
	require.NoError(t, err)
	for _, tool := range []*config.ToolMetadata{
		{Name: "a:rotate_keys", ServerName: "a", Description: "rotate keys", ParamsJSON: "{}"},
		{Name: "a:denied", ServerName: "a", Description: "rotate keys rotate keys rotate keys", ParamsJSON: "{}"},
		{Name: "b:rotate_keys", ServerName: "b", Description: "rotate keys", ParamsJSON: "{}"},
		{Name: "outside:rotate_keys", ServerName: "outside", Description: "rotate keys", ParamsJSON: "{}"},
	} {
		require.NoError(t, pinnedIndex.IndexTool(tool))
	}

	ctx := agentCtx([]string{"a", "empty", "outside"}, []string{auth.PermRead}, "pinned")
	args := map[string]any{
		"query": "rotate keys", "limit": float64(1), "debug": true, "include_stats": true, "include_disabled": true,
	}
	args["server"] = "a"
	selected := retrieveServerText(t, proxy, ctx, args)
	assert.Contains(t, selected, `"name":"a:rotate_keys"`)
	assert.Contains(t, selected, `"hidden_by_profile":1`)
	assert.NotContains(t, selected, "a:denied")

	args["server"] = "unknown"
	empty := retrieveServerText(t, proxy, ctx, args)
	assert.Contains(t, empty, `"tools":[]`)
	assert.Contains(t, empty, `"total_indexed_tools":0`)
	for _, server := range []string{"b", "outside", "empty", "A", "a ", "*", ""} {
		args["server"] = server
		assert.Equal(t, empty, retrieveServerText(t, proxy, ctx, args), "selector %q", server)
	}

	args["query"] = "denied"
	args["server"] = "unknown"
	empty = retrieveServerText(t, proxy, ctx, args)
	args["server"] = "a"
	assert.Equal(t, empty, retrieveServerText(t, proxy, ctx, args), "policy-only hits cannot disclose the selected upstream")
}

func TestRetrieveToolsServerFiltersAuxiliaryOutput(t *testing.T) {
	proxy, rt, _ := buildMCPProxyWithActivation(t)
	for _, server := range []string{"a", "b", "held"} {
		require.NoError(t, proxy.storage.SaveUpstreamServer(&config.ServerConfig{
			Name: server, Enabled: true, Quarantined: server == "held",
		}))
		tool := stateview.ToolInfo{Name: "read_widget", Description: "widget sentinel"}
		if server == "a" {
			tool.Annotations = &config.ToolAnnotations{
				ReadOnlyHint: boolPtr(true), DestructiveHint: boolPtr(false), OpenWorldHint: boolPtr(false),
			}
		}
		rt.Supervisor().StateView().UpdateServer(server, func(s *stateview.ServerStatus) {
			s.Connected = true
			s.ToolsDiscovered = true
			s.Tools = []stateview.ToolInfo{tool}
		})
		if server != "held" {
			require.NoError(t, proxy.index.IndexTool(&config.ToolMetadata{
				Name: server + ":" + tool.Name, ServerName: server, Description: tool.Description, ParamsJSON: "{}",
			}))
			require.NoError(t, proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
				ServerName: server, ToolName: tool.Name, Status: storage.ToolApprovalStatusApproved,
			}))
			require.NoError(t, proxy.storage.IncrementToolUsage(server+":"+tool.Name))
		}
	}
	require.NoError(t, proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "a", ToolName: "pending_widget", Status: storage.ToolApprovalStatusPending,
	}))

	args := map[string]any{
		"query": "widget", "server": "a", "include_disabled": true, "include_stats": true, "debug": true,
		"read_only_only": true, "exclude_destructive": true, "exclude_open_world": true,
	}
	text := retrieveServerText(t, proxy, adminCtx(), args)
	var result retrieveToolsResponse
	require.NoError(t, json.Unmarshal([]byte(text), &result))
	require.Len(t, result.Tools, 1)
	assert.Equal(t, "a:read_widget", result.Tools[0]["name"])
	assert.Equal(t, []contracts.LockedToolEntry{{
		Name: "a:pending_widget", Server: "a", Status: contracts.DisabledStatusPendingApproval,
	}}, result.Disabled)
	assert.Contains(t, text, `"total_indexed_tools":1`)
	assert.Contains(t, text, `"tool_name":"a:read_widget"`)
	assert.Contains(t, text, `"level":"low"`)
	assert.NotContains(t, text, "b:read_widget")
	assert.NotContains(t, text, "held:read_widget")
	assert.NotContains(t, text, "filter_diagnostics")

	args["server"] = "b"
	text = retrieveServerText(t, proxy, adminCtx(), args)
	assert.Contains(t, text, `"tools":[]`)
	assert.Contains(t, text, "filter_diagnostics")
	assert.NotContains(t, text, "pending_widget")

	args["server"] = "held"
	args["limit"] = float64(-1) // The same default window applies to locked results.
	text = retrieveServerText(t, proxy, adminCtx(), args)
	require.NoError(t, json.Unmarshal([]byte(text), &result))
	assert.Equal(t, []contracts.LockedToolEntry{{
		Name: "held:read_widget", Server: "held", Status: contracts.DisabledStatusServerQuarantined,
	}}, result.Disabled)
	assert.NotContains(t, text, "widget sentinel")
	assert.NotContains(t, text, "inputSchema")

	delete(args, "include_disabled")
	text = retrieveServerText(t, proxy, adminCtx(), args)
	assert.Contains(t, text, "1 relevant tool(s) exist but are locked")
	assert.NotContains(t, text, "held:read_widget")

	args["query"] = "xyzzy"
	args["server"] = "unknown"
	empty := retrieveServerText(t, proxy, adminCtx(), args)
	for _, server := range []string{"a", "b", "held"} {
		args["server"] = server
		assert.Equal(t, empty, retrieveServerText(t, proxy, adminCtx(), args))
	}
}

func TestRetrieveToolsServerRejectsNonString(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"query": "widget", "server": []any{"a"}}
	result, err := proxy.handleRetrieveTools(adminCtx(), req)
	require.NoError(t, err)
	require.True(t, result.IsError)
	assert.Contains(t, resultText(t, result), `argument "server" is not a string`)
}
