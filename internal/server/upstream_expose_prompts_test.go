package server

import (
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// TestBuildPatchConfig_ExposePrompts verifies F9: the upstream_servers
// patch/update tool maps expose_prompts into the ServerConfig patch as a
// tri-state *bool — present false sets it, an omitted key leaves it nil.
func TestBuildPatchConfig_ExposePrompts(t *testing.T) {
	proxy, _ := createTestProxyWithRuntime(t, nil)
	existing := &config.ServerConfig{Name: "srv", Protocol: "stdio", Enabled: true}

	t.Run("explicit false sets the pointer", func(t *testing.T) {
		req := mcp.CallToolRequest{Params: mcp.CallToolParams{
			Arguments: map[string]interface{}{"operation": "patch", "name": "srv", "expose_prompts": false},
		}}
		patch, _, err := proxy.buildPatchConfigFromRequest(req, existing)
		require.NoError(t, err)
		require.NotNil(t, patch.ExposePrompts)
		require.False(t, *patch.ExposePrompts)
	})
	t.Run("omitted key leaves nil", func(t *testing.T) {
		req := mcp.CallToolRequest{Params: mcp.CallToolParams{
			Arguments: map[string]interface{}{"operation": "patch", "name": "srv", "enabled": true},
		}}
		patch, _, err := proxy.buildPatchConfigFromRequest(req, existing)
		require.NoError(t, err)
		require.Nil(t, patch.ExposePrompts)
	})
}
