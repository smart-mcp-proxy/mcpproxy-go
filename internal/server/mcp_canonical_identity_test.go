package server

import (
	"context"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// TestCanonicalIdentity_RetrieveToolsRoundTrip is the #871 keystone: discovery
// stores a BARE tool name (ServerName:"github", Name:"create_issue"), yet
// retrieve_tools must expose the canonical "server:tool" id in BOTH modes, and
// that exact id must round-trip straight back into describe_tool.
func TestCanonicalIdentity_RetrieveToolsRoundTrip(t *testing.T) {
	proxy := createTestMCPProxyServer(t)

	require.NoError(t, proxy.storage.SaveUpstreamServer(&config.ServerConfig{
		Name: "github", Enabled: true,
	}))
	// BARE name — exactly how the live discovery path indexes tools.
	require.NoError(t, proxy.index.IndexTool(&config.ToolMetadata{
		Name:        "create_issue",
		ServerName:  "github",
		Description: "Create a GitHub issue with a title and body",
		ParamsJSON:  `{"type":"object","properties":{"title":{"type":"string"}}}`,
		Hash:        "hash-create-issue",
	}))

	const canonical = "github:create_issue"

	retrieve := func(detail string) map[string]interface{} {
		req := mcp.CallToolRequest{}
		req.Params.Arguments = map[string]interface{}{
			"query": "create issue", "limit": float64(20), "detail": detail,
		}
		result, err := proxy.handleRetrieveTools(context.Background(), req)
		require.NoError(t, err)
		resp := decodeRetrieve(t, result)
		require.NotEmpty(t, resp.Tools, "retrieve_tools (%s) returned no tools", detail)
		return resp.Tools[0]
	}

	t.Run("full mode name is canonical, not bare", func(t *testing.T) {
		assert.Equal(t, canonical, retrieve(config.ToolResponseModeFull)["name"])
	})

	t.Run("compact mode id equals full mode name", func(t *testing.T) {
		full := retrieve(config.ToolResponseModeFull)["name"]
		compact := retrieve(config.ToolResponseModeCompact)["id"]
		assert.Equal(t, canonical, compact)
		assert.Equal(t, full, compact, "compact id must equal full name (FR-007 identity)")
	})

	t.Run("describe_tool accepts the exact id retrieve_tools returned", func(t *testing.T) {
		id := retrieve(config.ToolResponseModeCompact)["id"].(string)
		resp := callDescribe(t, proxy, context.Background(), []interface{}{id})
		require.Empty(t, resp.Errors, "describe_tool rejected the canonical id: %v", resp.Errors)
		require.Len(t, resp.Definitions, 1)
		assert.Equal(t, canonical, resp.Definitions[0]["name"])
	})
}
