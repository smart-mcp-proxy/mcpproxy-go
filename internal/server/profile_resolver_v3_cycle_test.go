package server

import (
	"context"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/connect"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
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

// TestBindingReassignment_KeepsPinAndReportsBindingAfterMove pins the
// remaining T029 rows against the REAL clients service and token store:
// a locked credential stays source=pin after a reassignment with mode
// omitted, and the moved binding clears the stored selection so the next
// resolution reports `binding` for a switchable one.
func TestBindingReassignment_KeepsPinAndReportsBindingAfterMove(t *testing.T) {
	srv, _ := newNotifyServer(t)
	proxy := srv.mcpProxy
	svc := srv.runtime.ClientsService()
	ctx := context.Background()
	admin := runtime.Actor{Kind: "api_key", Surface: profile.SurfaceAPI}

	// locked credential
	mint := func(id, prof, mode string) {
		p, m := prof, mode
		mn := svc.ConnectMinter()
		intent := connect.CredentialIntent{Profile: &p, Mode: &m, ActorKind: "api_key", Surface: "api"}
		issued, err := mn.Issue(id, intent)
		require.NoError(t, err)
		require.NoError(t, mn.Commit(id, intent, issued))
	}
	mint("cursor", "ro", auth.ProfileModeLocked)
	mint("windsurf", "ro", auth.ProfileModeSwitchable)

	authCtxFor := func(id string) context.Context {
		tok, err := srv.runtime.StorageManager().GetAgentTokenByName("client-" + id)
		require.NoError(t, err)
		require.NotNil(t, tok)
		return auth.WithAuthContext(context.Background(), tok.AuthContext())
	}

	_, err := svc.SetBinding(ctx, admin, "cursor", "full", nil)
	require.NoError(t, err)
	idx := proxy.profileIndexFor(proxy.currentConfig())
	res := proxy.ResolveProfileV3(authCtxFor("cursor"), idx)
	assert.Equal(t, "full", res.Name)
	assert.Equal(t, string(profile.SourcePin), res.Source, "a locked credential stays source=pin after the move, mode omitted")

	// switchable: a stored selection is cleared by the move (the notifier is
	// wired into the real service) and the next resolution reports the binding.
	sid := "sess-move"
	proxy.sessionStore.SetSession(sid, "windsurf", "1", false, false, nil)
	proxy.sessionStore.SetSessionIdentity(sid, "client-windsurf", "windsurf")
	proxy.sessionStore.SetActiveProfile(sid, "full")
	_, err = svc.SetBinding(ctx, admin, "windsurf", "full", nil)
	require.NoError(t, err)
	assert.Empty(t, proxy.sessionStore.GetActiveProfile(sid), "the move cleared the registered session's stored selection")

	res = proxy.ResolveProfileV3(sessionCtx(authCtxFor("windsurf"), sid), idx)
	assert.Equal(t, "full", res.Name)
	assert.Equal(t, string(profile.SourceBinding), res.Source)
}
