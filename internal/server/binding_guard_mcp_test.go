package server

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
)

// TestBindingGuardRuntime_EndToEnd is T033a's runtime-guard half (FR-008a (2)):
// a config hand-edited into the bypassable state (auth off, a locked named
// binding, no anonymous_profile) leaves an anonymous MCP caller with no tools,
// refuses every call before any upstream I/O, refuses set_profile("x") while
// admitting set_profile(""), keeps the locked client's own credential working,
// and reports anonymous_denied_by_binding_guard naming the binding and both
// fixes. Setting anonymous_profile equal to the binding lifts the guard.
func TestBindingGuardRuntime_EndToEnd(t *testing.T) {
	proxy, rt := createTestProxyWithRuntimeCfg(t, nil, func(cfg *config.Config) {
		cfg.RequireMCPAuth = false
		cfg.AnonymousProfile = ""
		cfg.Servers = []*config.ServerConfig{{Name: "github", Enabled: true}}
		cfg.Profiles = []config.ProfileConfig{{Name: "work-readonly", Servers: []string{"github"}, MaxTier: config.ProfileTierRead}}
	})
	up := startCountingUpstream(t, proxy, rt, "github", readSpec("list_issues"))
	rt.SetBindingGuard(proxy)

	_, err := rt.StorageManager().MintClientCredential("cursor", "mcp_cli_runtime_guard_e2e", []byte("runtime-guard-key"),
		auth.ProfileModeLocked, "work-readonly", time.Now().Add(time.Hour))
	require.NoError(t, err)

	callRead := func(ctx context.Context) (isError bool) {
		res, err := proxy.handleCallToolVariant(ctx, auditCallToolRequest("github:list_issues", map[string]interface{}{}), contracts.ToolVariantRead)
		require.NoError(t, err)
		return res.IsError
	}

	// anonymous: deny-all, refused before any upstream call
	idx := proxy.profileIndexFor(proxy.currentConfig())
	res := proxy.ResolveProfileV3(anonCtx(), idx)
	require.True(t, res.BindingGuarded)
	require.True(t, res.Scope.DeniesAll())
	require.True(t, callRead(anonCtx()), "an anonymous call is refused while the guard is active")
	require.Equal(t, int64(0), up.count.Load(), "with zero upstream calls")

	sid := "guarded-anon"
	ctx := sessionCtx(anonCtx(), sid)
	require.True(t, setProfileCall(t, proxy, ctx, "work-readonly").IsError, "set_profile(x) is refused for a guarded anonymous caller")
	require.False(t, setProfileCall(t, proxy, ctx, "").IsError, "set_profile(\"\") is always admitted")

	// the locked client's own credential still works
	cursor := clientCtx("cursor", "work-readonly", auth.ProfileModeLocked)
	require.False(t, callRead(cursor), "the client's own credential is unaffected by the anonymous guard")
	require.Equal(t, int64(1), up.count.Load())

	// the warning names the binding and both fixes
	var found bool
	for _, w := range rt.ClientsService().Warnings(nil) {
		if w.Code != profile.WarningAnonymousDeniedByBindingGuard {
			continue
		}
		found = true
		require.Len(t, w.Bindings, 1)
		require.Equal(t, "cursor", w.Bindings[0].ClientID)
		require.Equal(t, "work-readonly", w.Bindings[0].Profile)
		require.Len(t, w.Fixes, 2)
		require.Equal(t, "require_mcp_auth", w.Fixes[0].Kind)
		require.Equal(t, "set_anonymous_profile", w.Fixes[1].Kind)
		require.Equal(t, "work-readonly", w.Fixes[1].Target)
	}
	require.True(t, found, "anonymous_denied_by_binding_guard must be reported")

	// a fix lifts the guard: anonymous_profile equal to the binding
	proxy.currentConfig().AnonymousProfile = "work-readonly"
	idx = proxy.profileIndexFor(proxy.currentConfig())
	res = proxy.ResolveProfileV3(anonCtx(), idx)
	require.False(t, res.BindingGuarded)
	require.Equal(t, "work-readonly", res.Name)
	for _, w := range rt.ClientsService().Warnings(nil) {
		require.NotEqual(t, profile.WarningAnonymousDeniedByBindingGuard, w.Code)
	}
}
