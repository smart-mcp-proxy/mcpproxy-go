package server

import (
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// TestBuildPatchConfig_ExposeSkills: the upstream_servers patch/update tool maps
// expose_skills into the ServerConfig patch as a tri-state *bool.
func TestBuildPatchConfig_ExposeSkills(t *testing.T) {
	proxy, _ := createTestProxyWithRuntime(t, nil)
	existing := &config.ServerConfig{Name: "srv", Protocol: "stdio", Enabled: true}

	t.Run("explicit false sets the pointer", func(t *testing.T) {
		req := mcp.CallToolRequest{Params: mcp.CallToolParams{
			Arguments: map[string]interface{}{"operation": "patch", "name": "srv", "expose_skills": false},
		}}
		patch, _, err := proxy.buildPatchConfigFromRequest(req, existing)
		require.NoError(t, err)
		require.NotNil(t, patch.ExposeSkills)
		require.False(t, *patch.ExposeSkills)
	})
	t.Run("omitted key leaves nil", func(t *testing.T) {
		req := mcp.CallToolRequest{Params: mcp.CallToolParams{
			Arguments: map[string]interface{}{"operation": "patch", "name": "srv", "enabled": true},
		}}
		patch, _, err := proxy.buildPatchConfigFromRequest(req, existing)
		require.NoError(t, err)
		require.Nil(t, patch.ExposeSkills)
	})
	t.Run("non-boolean is rejected", func(t *testing.T) {
		req := mcp.CallToolRequest{Params: mcp.CallToolParams{
			Arguments: map[string]interface{}{"operation": "patch", "name": "srv", "expose_skills": "yes"},
		}}
		_, _, err := proxy.buildPatchConfigFromRequest(req, existing)
		require.Error(t, err)
		require.Contains(t, err.Error(), "expose_skills")
	})
}
