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
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

func TestDirectToolCallabilityBlock_ServerDisabled(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	require.NoError(t, proxy.storage.SaveUpstreamServer(&config.ServerConfig{Name: "github", Enabled: false}))

	result := proxy.directToolCallabilityBlock(context.Background(), "github", "list_repos", map[string]interface{}{})
	require.NotNil(t, result)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Content[0].(mcp.TextContent).Text, "Tool is disabled")
}

func TestDirectToolCallabilityBlock_ServerQuarantined(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	require.NoError(t, proxy.storage.SaveUpstreamServer(&config.ServerConfig{Name: "github", Enabled: true, Quarantined: true}))

	result := proxy.directToolCallabilityBlock(context.Background(), "github", "list_repos", map[string]interface{}{"q": "x"})
	require.NotNil(t, result)
	assert.False(t, result.IsError)

	var response map[string]interface{}
	text := result.Content[0].(mcp.TextContent).Text
	require.NoError(t, json.Unmarshal([]byte(text), &response))
	assert.Equal(t, "QUARANTINED_SERVER_BLOCKED", response["status"])
	assert.Equal(t, "github", response["serverName"])
	assert.Equal(t, "list_repos", response["toolName"])

	// The remediation must point at operations the agent can actually execute:
	// list_quarantined/inspect_quarantined live on quarantine_security, and
	// upstream_servers rejects them with "Unknown operation".
	instructions, _ := response["instructions"].(string)
	assert.Contains(t, instructions, "quarantine_security")
	assert.NotContains(t, instructions, "'upstream_servers' tool with operation 'list_quarantined'")
}

func TestDirectToolCallabilityBlock_ConfigDeniedTool(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	require.NoError(t, proxy.storage.SaveUpstreamServer(&config.ServerConfig{
		Name:          "github",
		Enabled:       true,
		DisabledTools: []string{"delete_repo"},
	}))

	result := proxy.directToolCallabilityBlock(context.Background(), "github", "delete_repo", map[string]interface{}{})
	require.NotNil(t, result)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Content[0].(mcp.TextContent).Text, "NOT user-overridable")
}

func TestDirectToolCallabilityBlock_DisabledTool(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	require.NoError(t, proxy.storage.SaveUpstreamServer(&config.ServerConfig{
		Name:          "github",
		Enabled:       true,
		DisabledTools: []string{"config_disabled"},
	}))
	require.NoError(t, proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "github",
		ToolName:   "delete_repo",
		Status:     storage.ToolApprovalStatusApproved,
		Disabled:   true,
	}))

	result := proxy.directToolCallabilityBlock(context.Background(), "github", "delete_repo", map[string]interface{}{})
	require.NotNil(t, result)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Content[0].(mcp.TextContent).Text, "Tool is disabled")
}

func TestDirectToolCallabilityBlock_PendingApproval(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	require.NoError(t, proxy.storage.SaveUpstreamServer(&config.ServerConfig{Name: "github", Enabled: true}))
	require.NoError(t, proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName:         "github",
		ToolName:           "new_tool",
		Status:             storage.ToolApprovalStatusPending,
		CurrentDescription: "new capability",
	}))

	result := proxy.directToolCallabilityBlock(context.Background(), "github", "new_tool", map[string]interface{}{})
	require.NotNil(t, result)
	assert.False(t, result.IsError)

	var response map[string]interface{}
	text := result.Content[0].(mcp.TextContent).Text
	require.NoError(t, json.Unmarshal([]byte(text), &response))
	assert.Equal(t, "TOOL_QUARANTINED", response["status"])
	assert.Equal(t, "github", response["server_name"])
	assert.Equal(t, "new_tool", response["tool_name"])
	assert.Equal(t, "new_unapproved_tool", response["reason"])
	assert.Contains(t, response["message"], "has not been approved")
	assert.Equal(t, "new capability", response["current_description"])
	assert.Contains(t, response["action"], "/api/v1/servers/github/tools/approve")
}

func TestDirectToolCallabilityBlock_ChangedApproval(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	require.NoError(t, proxy.storage.SaveUpstreamServer(&config.ServerConfig{Name: "github", Enabled: true}))
	require.NoError(t, proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName:          "github",
		ToolName:            "mutated_tool",
		Status:              storage.ToolApprovalStatusChanged,
		PreviousDescription: "old",
		CurrentDescription:  "new",
	}))

	result := proxy.directToolCallabilityBlock(context.Background(), "github", "mutated_tool", map[string]interface{}{})
	require.NotNil(t, result)
	assert.False(t, result.IsError)

	var response map[string]interface{}
	text := result.Content[0].(mcp.TextContent).Text
	require.NoError(t, json.Unmarshal([]byte(text), &response))
	assert.Equal(t, "TOOL_QUARANTINED", response["status"])
	assert.Equal(t, "github", response["server_name"])
	assert.Equal(t, "mutated_tool", response["tool_name"])
	assert.Equal(t, "tool_description_changed", response["reason"])
	assert.Contains(t, response["message"], "description has changed")
	assert.Equal(t, "old", response["previous_description"])
	assert.Equal(t, "new", response["current_description"])
	assert.Contains(t, response["action"], "/api/v1/servers/github/tools/approve")
}

func TestDirectToolCallabilityBlock_ApprovedToolAllowed(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	require.NoError(t, proxy.storage.SaveUpstreamServer(&config.ServerConfig{Name: "github", Enabled: true}))
	require.NoError(t, proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "github",
		ToolName:   "list_repos",
		Status:     storage.ToolApprovalStatusApproved,
	}))

	result := proxy.directToolCallabilityBlock(context.Background(), "github", "list_repos", map[string]interface{}{})
	assert.Nil(t, result)
}

func TestFilterDirectToolsForAgentCallability_AgentOnly(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	require.NoError(t, proxy.storage.SaveUpstreamServer(&config.ServerConfig{
		Name:          "github",
		Enabled:       true,
		DisabledTools: []string{"config_disabled"},
	}))
	require.NoError(t, proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "github",
		ToolName:   "allowed",
		Status:     storage.ToolApprovalStatusApproved,
	}))
	require.NoError(t, proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "github",
		ToolName:   "disabled",
		Status:     storage.ToolApprovalStatusApproved,
		Disabled:   true,
	}))
	require.NoError(t, proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "github",
		ToolName:   "pending",
		Status:     storage.ToolApprovalStatusPending,
	}))

	tools := []mcp.Tool{
		{Name: FormatDirectToolName("github", "allowed")},
		{Name: FormatDirectToolName("github", "disabled")},
		{Name: FormatDirectToolName("github", "pending")},
		{Name: FormatDirectToolName("github", "config_disabled")},
	}

	agentCtx := auth.WithAuthContext(context.Background(), &auth.AuthContext{
		Type:           auth.AuthTypeAgent,
		AgentName:      "agent",
		AllowedServers: []string{"github"},
		Permissions:    []string{auth.PermRead},
	})

	filtered := proxy.filterDirectToolsForAgentCallability(agentCtx, tools)
	assert.Equal(t, []string{FormatDirectToolName("github", "allowed")}, directCallabilityToolNamesForTest(filtered))

	assert.Equal(t, tools, proxy.filterDirectToolsForAgentCallability(context.Background(), tools))
}

func directCallabilityToolNamesForTest(tools []mcp.Tool) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name)
	}
	return names
}
