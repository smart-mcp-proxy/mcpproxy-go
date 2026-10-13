package server

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
)

// TestBindingBypassable_FR008aMatrixExtraRows adds the T033a enforcement-matrix
// rows the reachability matrix in profile_binding_guard_test.go does not have:
// wider only by an allow rule, and the empty-pin cases that can never be
// bypassed (an "All servers" binding has nothing to escape from).
func TestBindingBypassable_FR008aMatrixExtraRows(t *testing.T) {
	tools := []bindingGuardTool{
		{server: "a", tool: "read_tool", tier: profile.TierRead},
		{server: "a", tool: "write_tool", tier: profile.TierWrite},
	}
	tests := []struct {
		name      string
		anonymous string
		profiles  []config.ProfileConfig
		mode, pin string
		want      bool
	}{
		{
			// allow overrides the tier cap, so an allow rule on the anonymous
			// profile alone admits a write tool the read-capped binding refuses.
			name: "wider only by an allow rule that overrides the cap", anonymous: "Q", pin: "P", mode: auth.ProfileModeLocked, want: true,
			profiles: []config.ProfileConfig{
				{Name: "P", Servers: []string{"a"}, MaxTier: config.ProfileTierRead},
				{Name: "Q", Servers: []string{"a"}, MaxTier: config.ProfileTierRead, Tools: &config.ProfileToolRules{Allow: []string{"a:write_tool"}}},
			},
		},
		{
			name: "equal allow rules are not wider", anonymous: "Q", pin: "P", mode: auth.ProfileModeLocked,
			profiles: []config.ProfileConfig{
				{Name: "P", Servers: []string{"a"}, MaxTier: config.ProfileTierRead, Tools: &config.ProfileToolRules{Allow: []string{"a:write_tool"}}},
				{Name: "Q", Servers: []string{"a"}, MaxTier: config.ProfileTierRead, Tools: &config.ProfileToolRules{Allow: []string{"a:write_tool"}}},
			},
		},
		{
			name: "the binding's allow rule makes it wider than anonymous, not narrower", anonymous: "Q", pin: "P", mode: auth.ProfileModeLocked,
			profiles: []config.ProfileConfig{
				{Name: "P", Servers: []string{"a"}, MaxTier: config.ProfileTierRead, Tools: &config.ProfileToolRules{Allow: []string{"a:write_tool"}}},
				{Name: "Q", Servers: []string{"a"}, MaxTier: config.ProfileTierRead},
			},
		},
		{
			name: "an empty pin is never a named binding (switchable All servers)", anonymous: "", pin: "", mode: auth.ProfileModeSwitchable,
			profiles: []config.ProfileConfig{{Name: "P", Servers: []string{"a"}}},
		},
		{
			name: "an empty pin with anonymous confined is never bypassable either", anonymous: "P", pin: "", mode: auth.ProfileModeSwitchable,
			profiles: []config.ProfileConfig{{Name: "P", Servers: []string{"a"}}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.DefaultConfig()
			cfg.Servers = []*config.ServerConfig{{Name: "a", Enabled: true}}
			cfg.AnonymousProfile = tt.anonymous
			cfg.Profiles = tt.profiles
			idx := newProfileIndex(cfg)
			binding := &auth.AgentToken{Kind: auth.KindClient, ProfileMode: tt.mode, ProfilePin: tt.pin}
			require.Equal(t, tt.want, bindingBypassable(idx, cfg, binding, tools))
		})
	}
}
