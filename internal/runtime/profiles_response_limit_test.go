package runtime

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Issue #2027: the profile's tool_response_limit is part of its REST view (a
// copy, never the config's own pointer) and of the profile change diff.
func TestProfileView_ToolResponseLimit(t *testing.T) {
	zero := 0
	cfg := &config.Config{Servers: []*config.ServerConfig{{Name: "a"}}}
	p := &config.ProfileConfig{Name: "script", Servers: []string{"a"}, ToolResponseLimit: &zero}

	v := ProfileViewFromConfig(cfg, p)
	require.NotNil(t, v.ToolResponseLimit)
	require.Equal(t, 0, *v.ToolResponseLimit)
	require.NotSame(t, p.ToolResponseLimit, v.ToolResponseLimit)
	require.True(t, v.IsLegacy, "the limit is not a policy field")

	require.Nil(t, ProfileViewFromConfig(cfg, &config.ProfileConfig{Name: "plain", Servers: []string{"a"}}).ToolResponseLimit)
}

func TestDiffProfileFields_ToolResponseLimit(t *testing.T) {
	zero, some := 0, 9000
	plain := config.ProfileConfig{Name: "p"}
	unlimited := config.ProfileConfig{Name: "p", ToolResponseLimit: &zero}
	bounded := config.ProfileConfig{Name: "p", ToolResponseLimit: &some}

	require.Equal(t, map[string]interface{}{"tool_response_limit": map[string]interface{}{"from": nil, "to": 0}}, diffProfileFields(plain, unlimited))
	require.Equal(t, map[string]interface{}{"tool_response_limit": map[string]interface{}{"from": 0, "to": 9000}}, diffProfileFields(unlimited, bounded))
	require.Empty(t, diffProfileFields(unlimited, config.ProfileConfig{Name: "p", ToolResponseLimit: &zero}))
}
