package server

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// oauthOnlyGuardProxy builds a proxy whose only binding is a browser login:
// client "cursor", no API key, a live grant, bound to profile P (locked).
func oauthOnlyGuardProxy(t *testing.T, anonymousProfile string) (*MCPProxyServer, *profileIndex) {
	t.Helper()
	proxy, rt := createTestProxyWithRuntimeCfg(t, []*config.ServerConfig{{Name: "a", Enabled: true}}, func(cfg *config.Config) {
		cfg.RequireMCPAuth = false
		cfg.AnonymousProfile = anonymousProfile
		cfg.Profiles = []config.ProfileConfig{{Name: "P", Servers: []string{"a"}}}
	})
	sm := rt.StorageManager()
	now := time.Now()
	require.NoError(t, sm.CreateOAuthRegistration(storage.OAuthRegistration{
		ClientID: "reg1", ReportedName: "Cursor IDE", RedirectURIs: []string{"http://127.0.0.1:9/cb"},
		TokenEndpointAuthMethod: "none", GrantTypes: []string{"authorization_code", "refresh_token"}, CreatedAt: now,
	}))
	_, err := sm.CommitOAuthLogin(storage.CommitLoginInput{
		Grant: storage.OAuthGrant{
			GrantID: "grantAAAAAAAAAAAAAAAAAA", RegistrationID: "reg1", Client: "cursor",
			RedirectURI: "http://127.0.0.1:9/cb", Resource: "http://127.0.0.1:8080/mcp/oauth/cursor", Issuer: "http://127.0.0.1:8080",
			RefreshHash: "rh", AccessHash: "ah", AccessExpiresAt: now.Add(time.Hour), RefreshExpiresAt: now.Add(24 * time.Hour),
			ApprovedAt: now, DisplayPrefix: "mcp_oat_abcd",
		},
		Client: "cursor", NewClient: true, Pin: "P", Mode: auth.ProfileModeLocked,
		ExpectedBindingRevision: storage.OAuthBindingAbsent, Now: now,
	})
	require.NoError(t, err)
	return proxy, proxy.profileIndexFor(proxy.currentConfig())
}

// Spec 132 R5: an OAuth-only client with a named pin and a live grant is an
// active binding, so an anonymous caller that could escape it is denied.
func TestBindingGuard_OAuthOnlyClientGuardsAnonymous(t *testing.T) {
	proxy, idx := oauthOnlyGuardProxy(t, "")
	got := proxy.ResolveProfileV3(context.Background(), idx)
	require.Equal(t, string(profile.SourceAnonymous), got.Source)
	require.True(t, got.BindingGuarded, "a live browser login bound to P is a binding anonymous could escape")

	active := proxy.BindingGuardActiveBindings()
	require.Len(t, active, 1)
	require.Equal(t, "cursor", active[0].ClientID)
	require.Equal(t, "P", active[0].Profile)

	// Revoking the login ends the binding.
	_, err := proxy.storage.RevokeOAuthGrant("grantAAAAAAAAAAAAAAAAAA", storage.OAuthRevokeAdmin, time.Now())
	require.NoError(t, err)
	got = proxy.ResolveProfileV3(context.Background(), idx)
	require.False(t, got.BindingGuarded, "a revoked login binds nothing")
	require.Empty(t, proxy.BindingGuardActiveBindings())
}

func TestBindingGuard_OAuthEntryIsEvaluatedThroughItsCandidate(t *testing.T) {
	proxy, _ := bindingGuardTestProxy(t, "", []config.ProfileConfig{{Name: "P", Servers: []string{"a"}}}, auth.ProfileModeLocked, "P")
	cfg := proxy.currentConfig()
	cur := runtime.GuardState{Config: cfg}
	cand := runtime.GuardState{Config: cfg, OAuthBindings: []runtime.OAuthBinding{
		{ClientID: "cursor2", Pin: "P", Mode: auth.ProfileModeLocked, Live: true},
	}}
	delta := proxy.BindingGuardDelta(cur, cand)
	require.Len(t, delta, 1, "a first approval is judged through its candidate entry before anything is stored")
	require.Equal(t, "cursor2", delta[0].ClientID)
	fixes := proxy.BindingGuardFixes(cand, delta)
	require.NotEmpty(t, fixes)
	require.Equal(t, profile.GuardFixRequireMCPAuth, fixes[0].Kind)

	// Not live: no binding. Unchanged: nothing new.
	cand.OAuthBindings[0].Live = false
	require.Empty(t, proxy.BindingGuardDelta(cur, cand))
	cand.OAuthBindings[0].Live = true
	require.Empty(t, proxy.BindingGuardDelta(cand, cand))
}
