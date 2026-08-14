package server

import (
	"context"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
)

// A padded 'name' must resolve to the same target as the canonical id: the
// server segment is parsed from the trimmed id, so gates and errors key on
// "github", never " github". Interior padding around the ':' separator counts
// too — the call path shares splitServerTool with describe_tool, so
// "github: get_repo" resolves exactly like "github:get_repo" instead of
// missing every exact-name gate and being dispatched upstream padded.
func TestCallToolVariant_TrimsWhitespaceInToolName(t *testing.T) {
	cases := []struct {
		name string
		id   string
	}{
		{"canonical", "github:get_repo"},
		{"outer padding", " github:get_repo "},
		{"padding after separator", "github: get_repo"},
		{"padding before separator", "github :get_repo"},
		{"padding on both segments", "  github : get_repo  "},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			proxy := createTestMCPProxyServer(t)
			seedEntryBuilderFixture(t, proxy)

			request := mcp.CallToolRequest{}
			request.Params.Arguments = map[string]interface{}{"name": tc.id}

			result, err := proxy.handleCallToolVariant(context.Background(), request, contracts.ToolVariantRead)
			require.NoError(t, err)
			require.NotNil(t, result)
			require.True(t, result.IsError, "no upstream client is connected in this fixture")

			text := result.Content[0].(mcp.TextContent).Text
			assert.NotContains(t, text, "Invalid tool name format")
			assert.Contains(t, text, "No client found for server: github.",
				"the padded id must resolve to server 'github', not a padded variant")
		})
	}
}

// The same normalization has to hold at the gates, not just in the final error
// string: an agent-scoped session that allows "github" must not see a padded
// id rejected as an out-of-scope server.
func TestCallToolVariant_InteriorPaddingPassesAgentScope(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	seedEntryBuilderFixture(t, proxy)

	agentCtx := &auth.AuthContext{
		Type:           auth.AuthTypeAgent,
		AgentName:      "test-bot",
		AllowedServers: []string{"github"},
		Permissions:    []string{auth.PermRead},
	}
	ctx := auth.WithAuthContext(context.Background(), agentCtx)

	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]interface{}{"name": "github : get_repo"}

	result, err := proxy.handleCallToolVariant(ctx, request, contracts.ToolVariantRead)
	require.NoError(t, err)
	require.True(t, result.IsError, "no upstream client is connected in this fixture")

	text := result.Content[0].(mcp.TextContent).Text
	assert.NotContains(t, text, "not in scope",
		"'github ' must normalize to 'github' before the agent-scope gate")
	assert.Contains(t, text, "No client found for server: github.")
}
