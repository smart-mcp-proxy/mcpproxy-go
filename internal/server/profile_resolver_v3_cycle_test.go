package server

import (
	"context"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
)

func cycleProfiles() []config.ProfileConfig {
	toB, toAC := []string{"b"}, []string{"a", "c"}
	return []config.ProfileConfig{
		{Name: "a", Servers: []string{"github"}, SwitchableTo: &toB},
		{Name: "b", Servers: []string{"notion"}, SwitchableTo: &toAC},
		{Name: "c", Servers: []string{"filesystem"}},
	}
}

func setProfileCall(t *testing.T, proxy *MCPProxyServer, ctx context.Context, slug string) *mcp.CallToolResult {
	t.Helper()
	res, err := proxy.handleSetProfile(ctx, mcp.CallToolRequest{Params: mcp.CallToolParams{
		Name: "set_profile", Arguments: map[string]interface{}{"profile": slug},
	}})
	require.NoError(t, err)
	return res
}

// TestResolveProfileV3_SwitchableCycleCannotEscalate pins T029's cycle row:
// A.switchable_to=[B] and B.switchable_to=[A,C]; a switchable binding on A may
// select B; while on B it may go back to A (its own base) but NOT to C, which
// only B's list names — admission is evaluated against the BASE (A), never
// against the profile currently selected.
func TestResolveProfileV3_SwitchableCycleCannotEscalate(t *testing.T) {
	proxy, _ := newProfilesV3FixtureWithConfig(t, func(cfg *config.Config) { cfg.Profiles = cycleProfiles() })
	sid := "sess-cycle"
	ctx := sessionCtx(clientCtx("laptop", "a", auth.ProfileModeSwitchable), sid)

	require.False(t, setProfileCall(t, proxy, ctx, "b").IsError, "b is in a's switchable_to")
	require.Equal(t, "b", proxy.sessionStore.GetActiveProfile(sid))

	idx := proxy.profileIndexFor(proxy.currentConfig())
	res := proxy.ResolveProfileV3(ctx, idx)
	assert.Equal(t, "b", res.Name)
	assert.Equal(t, string(profile.SourceSession), res.Source)
	assert.Equal(t, "a", res.Base, "the base stays the binding")

	require.False(t, setProfileCall(t, proxy, ctx, "a").IsError, "the caller's own base is always admitted")
	proxy.sessionStore.SetActiveProfile(sid, "b")

	refused := setProfileCall(t, proxy, ctx, "c")
	require.True(t, refused.IsError, "c is only in b's switchable_to, not in the base a's")
	assert.Equal(t, "b", proxy.sessionStore.GetActiveProfile(sid), "a refused selection changes nothing")
}
