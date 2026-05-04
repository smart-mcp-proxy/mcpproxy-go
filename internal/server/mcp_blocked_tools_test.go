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

func TestIsToolCallable_DisabledTool(t *testing.T) {
	proxy := createTestMCPProxyServer(t)

	require.NoError(t, proxy.storage.SaveUpstreamServer(&config.ServerConfig{
		Name:     "context7",
		URL:      "https://mcp.context7.com/mcp",
		Protocol: "http",
		Enabled:  true,
	}))

	require.NoError(t, proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "context7",
		ToolName:   "resolve-library-id",
		Status:     storage.ToolApprovalStatusApproved,
		Disabled:   true,
	}))

	assert.False(t, proxy.isToolCallable("context7", "resolve-library-id"))
}

func TestRetrieveTools_ExcludesDisabledToolsPreRanking(t *testing.T) {
	proxy := createTestMCPProxyServer(t)

	require.NoError(t, proxy.storage.SaveUpstreamServer(&config.ServerConfig{Name: "context7", Enabled: true}))
	require.NoError(t, proxy.storage.SaveUpstreamServer(&config.ServerConfig{Name: "github", Enabled: true}))

	require.NoError(t, proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "context7",
		ToolName:   "resolve-library-id",
		Status:     storage.ToolApprovalStatusApproved,
		Disabled:   true,
	}))

	require.NoError(t, proxy.index.IndexTool(&config.ToolMetadata{
		Name:        "context7:resolve-library-id",
		ServerName:  "context7",
		Description: "Resolve library IDs",
		ParamsJSON:  "{\"type\":\"object\"}",
	}))
	require.NoError(t, proxy.index.IndexTool(&config.ToolMetadata{
		Name:        "github:get_file_contents",
		ServerName:  "github",
		Description: "Get file contents",
		ParamsJSON:  "{\"type\":\"object\"}",
	}))

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"query": "context7 resolve library id",
		"limit": float64(10),
	}

	result, err := proxy.handleRetrieveTools(context.Background(), req)
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.NotEmpty(t, result.Content)

	text := result.Content[0].(mcp.TextContent).Text
	var payload map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(text), &payload))

	toolsValue, exists := payload["tools"]
	if !exists || toolsValue == nil {
		return
	}

	toolsRaw, ok := toolsValue.([]interface{})
	require.True(t, ok)

	for _, item := range toolsRaw {
		tool := item.(map[string]interface{})
		name := tool["name"].(string)
		assert.NotEqual(t, "context7:resolve-library-id", name)
	}
}

func TestCallBlockedTool_ReturnsToolBlocked(t *testing.T) {
	proxy := createTestMCPProxyServer(t)

	require.NoError(t, proxy.storage.SaveUpstreamServer(&config.ServerConfig{Name: "context7", Enabled: true}))
	require.NoError(t, proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "context7",
		ToolName:   "query-docs",
		Status:     storage.ToolApprovalStatusApproved,
		Disabled:   true,
	}))

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"name": "context7:query-docs",
	}

	result, err := proxy.handleCallToolVariant(context.Background(), req, contracts.ToolVariantRead)
	require.NoError(t, err)
	require.True(t, result.IsError)

	text := result.Content[0].(mcp.TextContent).Text
	assert.Contains(t, text, "TOOL_BLOCKED")
	assert.Contains(t, text, "Tool is disabled and not callable.")
}

func TestReenableTool_VisibleAndCallable(t *testing.T) {
	proxy := createTestMCPProxyServer(t)

	require.NoError(t, proxy.storage.SaveUpstreamServer(&config.ServerConfig{Name: "context7", Enabled: true}))
	require.NoError(t, proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "context7",
		ToolName:   "resolve-library-id",
		Status:     storage.ToolApprovalStatusApproved,
		Disabled:   true,
	}))

	assert.False(t, proxy.isToolCallable("context7", "resolve-library-id"))

	require.NoError(t, proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "context7",
		ToolName:   "resolve-library-id",
		Status:     storage.ToolApprovalStatusApproved,
		Disabled:   false,
	}))

	assert.True(t, proxy.isToolCallable("context7", "resolve-library-id"))
}
