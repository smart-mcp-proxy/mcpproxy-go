package server

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every truncation banner mcpproxy emits points the caller at read_cache. That
// banner reaches REST/CLI/Web-UI/tray callers too (POST /api/v1/tools/call →
// CallToolDirect), so read_cache has to be routable there — otherwise the
// advertised follow-up answers "unknown tool: read_cache" with an HTTP 500 and
// the full payload is unreachable outside the MCP surface.

func TestCallToolDirect_ReadCacheIsRoutable(t *testing.T) {
	proxy := createTestMCPProxyServer(t)

	request := mcp.CallToolRequest{}
	request.Params.Name = "read_cache"
	request.Params.Arguments = map[string]interface{}{
		"key": "no-such-key", "offset": float64(0), "limit": float64(50),
	}

	_, err := proxy.CallToolDirect(context.Background(), request)
	require.Error(t, err, "an unresolvable key is still a read_cache answer, not a routing miss")
	assert.NotContains(t, err.Error(), "unknown tool",
		"read_cache must be routed by CallToolDirect, not fall through to the default branch")
	assert.Contains(t, err.Error(), "cache key not found")
}

func TestCallToolDirect_ReadCachePagesStoredRecords(t *testing.T) {
	proxy := createTestMCPProxyServer(t)

	const key = "cache-key-direct"
	content := `{"tools":[{"name":"github:get_repo"},{"name":"github:list_issues"}]}`
	require.NoError(t, proxy.cacheManager.Store(key, "retrieve_tools", map[string]interface{}{"query": "manage"}, content, "tools", 2))

	request := mcp.CallToolRequest{}
	request.Params.Name = "read_cache"
	request.Params.Arguments = map[string]interface{}{
		"key": key, "offset": float64(0), "limit": float64(50),
	}

	result, err := proxy.CallToolDirect(context.Background(), request)
	require.NoError(t, err)

	blocks, ok := result.([]mcp.Content)
	require.True(t, ok, "CallToolDirect returns the raw content blocks")
	require.NotEmpty(t, blocks)
	text, ok := blocks[0].(mcp.TextContent)
	require.True(t, ok)

	var page struct {
		Records []map[string]interface{} `json:"records"`
	}
	require.NoError(t, json.Unmarshal([]byte(text.Text), &page))
	require.Len(t, page.Records, 2)
	assert.Equal(t, "github:get_repo", page.Records[0]["name"])
}
