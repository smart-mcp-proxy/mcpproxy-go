package server

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// retrieveToolsResponse decodes the JSON the handler returns.
type retrieveToolsResponse struct {
	Tools       []map[string]interface{}    `json:"tools"`
	Total       int                         `json:"total"`
	Disabled    []contracts.LockedToolEntry `json:"disabled"`
	Remediation map[string]string           `json:"remediation"`
}

func decodeRetrieve(t *testing.T, result *mcp.CallToolResult) retrieveToolsResponse {
	t.Helper()
	require.False(t, result.IsError, "handler returned an error result")
	text := result.Content[0].(mcp.TextContent).Text
	var resp retrieveToolsResponse
	require.NoError(t, json.Unmarshal([]byte(text), &resp))
	return resp
}

// seedDiscoveryFixture creates one callable, one config-denied, one
// user-disabled and one pending tool — all matching the same query.
func seedDiscoveryFixture(t *testing.T, proxy *MCPProxyServer) {
	t.Helper()
	require.NoError(t, proxy.storage.SaveUpstreamServer(&config.ServerConfig{
		Name: "github", Enabled: true,
		DisabledTools: []string{"delete_repo"}, // config-denied
	}))
	require.NoError(t, proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "github", ToolName: "list_issues",
		Status: storage.ToolApprovalStatusApproved, Disabled: true, // user-disabled
	}))
	require.NoError(t, proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "github", ToolName: "new_tool",
		Status: storage.ToolApprovalStatusPending, // pending_approval
	}))
	for _, name := range []string{"get_repo", "delete_repo", "list_issues", "new_tool"} {
		require.NoError(t, proxy.index.IndexTool(&config.ToolMetadata{
			Name:        "github:" + name,
			ServerName:  "github",
			Description: "repo issue helper " + name,
			ParamsJSON:  `{"type":"object"}`,
		}))
	}
}

func TestDisabledDiscovery_DefaultPathUnchanged(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	seedDiscoveryFixture(t, proxy)

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{"query": "repo issue helper", "limit": float64(10)}
	result, err := proxy.handleRetrieveTools(context.Background(), req)
	require.NoError(t, err)

	resp := decodeRetrieve(t, result)
	// The byte-for-byte-unchanged guarantee (FR-002/SC-001): the opt-in keys
	// MUST be absent on the default path.
	assert.Nil(t, resp.Disabled, "default path must not include disabled[]")
	assert.Nil(t, resp.Remediation, "default path must not include remediation")
	names := map[string]bool{}
	for _, tl := range resp.Tools {
		names[tl["name"].(string)] = true
	}
	assert.True(t, names["github:get_repo"], "callable tool present")
	// user-disabled tool is dropped by the existing filter (works without runtime).
	assert.False(t, names["github:list_issues"], "user-disabled tool excluded as before")
}

func TestDisabledDiscovery_OptInReturnsStatusesAndRemediation(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	seedDiscoveryFixture(t, proxy)

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"query": "repo issue helper", "limit": float64(10), "include_disabled": true,
	}
	result, err := proxy.handleRetrieveTools(context.Background(), req)
	require.NoError(t, err)
	resp := decodeRetrieve(t, result)

	byName := map[string]contracts.DisabledToolStatus{}
	for _, d := range resp.Disabled {
		byName[d.Name] = d.Status
	}
	// What retrieve_tools' discovery loop surfaces is exactly what
	// isToolCallable rejects. In this unit harness (no wired runtime) that is
	// the user-disabled tool. config-denied needs a runtime (covered by the
	// runtime classifier unit test + live-MCP quickstart §3/§4); pending tools
	// stay callable so they surface as normal results, not in disabled[]
	// (pending is reached via the US3 upstream_servers counts instead).
	assert.Equal(t, contracts.DisabledStatusByUser, byName["github:list_issues"])

	// remediation keyed ONLY by statuses actually present (a subset of the 5).
	assert.Contains(t, resp.Remediation, contracts.DisabledStatusByUser)
	assert.NotContains(t, resp.Remediation, contracts.DisabledStatusServerDisabled)
	assert.NotContains(t, resp.Remediation, contracts.DisabledStatusByConfig)
	assert.Len(t, resp.Remediation, len(uniqueStatuses(resp.Disabled)))

	// callable tool still present and ranked before the disabled list.
	require.NotEmpty(t, resp.Tools)
}

func TestDisabledDiscovery_CapAtTen(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	require.NoError(t, proxy.storage.SaveUpstreamServer(&config.ServerConfig{Name: "s", Enabled: true}))
	for i := 0; i < 25; i++ {
		name := "tool_" + string(rune('a'+i))
		require.NoError(t, proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
			ServerName: "s", ToolName: name,
			Status: storage.ToolApprovalStatusApproved, Disabled: true,
		}))
		require.NoError(t, proxy.index.IndexTool(&config.ToolMetadata{
			Name: "s:" + name, ServerName: "s", Description: "widget helper thing",
			ParamsJSON: `{"type":"object"}`,
		}))
	}
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"query": "widget helper thing", "limit": float64(100), "include_disabled": true,
	}
	result, err := proxy.handleRetrieveTools(context.Background(), req)
	require.NoError(t, err)
	resp := decodeRetrieve(t, result)
	assert.LessOrEqual(t, len(resp.Disabled), 10, "disabled[] must be capped at min(limit,10)")
}

func uniqueStatuses(entries []contracts.LockedToolEntry) map[contracts.DisabledToolStatus]bool {
	m := map[contracts.DisabledToolStatus]bool{}
	for _, e := range entries {
		m[e.Status] = true
	}
	return m
}

// T009: the in-memory include_disabled counter increments only when the flag
// is set, and is never persisted (process-lifetime only).
func TestDisabledDiscovery_UsageCounter(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	seedDiscoveryFixture(t, proxy)
	require.Equal(t, int64(0), proxy.IncludeDisabledCalls())

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{"query": "repo issue helper"}
	_, err := proxy.handleRetrieveTools(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, int64(0), proxy.IncludeDisabledCalls(), "flag absent → no increment")

	req.Params.Arguments = map[string]interface{}{"query": "repo issue helper", "include_disabled": true}
	_, err = proxy.handleRetrieveTools(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, int64(1), proxy.IncludeDisabledCalls(), "flag set → +1")
}
