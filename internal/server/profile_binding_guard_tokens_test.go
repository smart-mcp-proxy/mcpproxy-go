package server

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

// Spec 115 T012: a guard-bound token pinned to P gets the SAME verdict as a
// locked client bound to P in every guard path; a REST-style token (profile_pin
// but no guard_bound) changes no verdict (A13).

func guardToken(name, pin string, guardBound bool) auth.AgentToken {
	return auth.AgentToken{Name: name, ProfilePin: pin, GuardBound: guardBound, ExpiresAt: time.Now().Add(time.Hour),
		AllowedServers: []string{"*"}, Permissions: []string{"read", "write", "destructive"}}
}

func guardClient(id, pin string) auth.AgentToken {
	return auth.AgentToken{Name: auth.ClientTokenName(id), Kind: auth.KindClient, ClientID: id, ProfilePin: pin,
		ProfileMode: auth.ProfileModeLocked, ExpiresAt: time.Now().Add(time.Hour)}
}

func TestBindingGuard_TokenVerdictEqualsLockedClient(t *testing.T) {
	profiles := []config.ProfileConfig{
		{Name: "P", Servers: []string{"a"}, MaxTier: "read"},
		{Name: "Wide", Servers: []string{"a", "b"}},
		{Name: "Narrow", Servers: []string{"a"}, MaxTier: "read"},
	}
	proxy, _ := createTestProxyWithRuntimeCfg(t, []*config.ServerConfig{{Name: "a", Enabled: true}, {Name: "b", Enabled: true}}, func(cfg *config.Config) {
		cfg.Profiles = profiles
	})
	for _, auth0 := range []bool{true, false} {
		for _, anon := range []string{"", "Narrow", "Wide"} {
			cfg := &config.Config{RequireMCPAuth: auth0, AnonymousProfile: anon, Profiles: profiles}
			on := &config.Config{RequireMCPAuth: true, Profiles: profiles}
			tok := guardToken("t1", "P", true)
			cli := guardClient("c1", "P")
			tokDelta := proxy.BindingGuardDelta(runtime.GuardState{Config: on}, runtime.GuardState{Config: cfg, Tokens: []auth.AgentToken{tok}})
			cliDelta := proxy.BindingGuardDelta(runtime.GuardState{Config: on}, runtime.GuardState{Config: cfg, Tokens: []auth.AgentToken{cli}})
			assert.Equal(t, len(cliDelta) > 0, len(tokDelta) > 0, "auth=%v anon=%q", auth0, anon)
			if len(tokDelta) > 0 {
				assert.Equal(t, "t1", tokDelta[0].TokenName)
				assert.Equal(t, "", tokDelta[0].ClientID)
				assert.Equal(t, auth.ProfileModeLocked, tokDelta[0].Mode)
				assert.Equal(t, len(proxy.BindingGuardFixes(runtime.GuardState{Config: cfg, Tokens: []auth.AgentToken{tok}}, tokDelta)),
					len(proxy.BindingGuardFixes(runtime.GuardState{Config: cfg, Tokens: []auth.AgentToken{cli}}, cliDelta)))
			}
			// Conservative guard parity.
			cons := runtime.ConservativeBindingGuard{}
			assert.Equal(t,
				len(cons.BindingGuardDelta(runtime.GuardState{Config: on}, runtime.GuardState{Config: cfg, Tokens: []auth.AgentToken{cli}})) > 0,
				len(cons.BindingGuardDelta(runtime.GuardState{Config: on}, runtime.GuardState{Config: cfg, Tokens: []auth.AgentToken{tok}})) > 0)

			// A REST-style pinned token without guard_bound changes nothing.
			rest := guardToken("rest", "P", false)
			assert.Empty(t, proxy.BindingGuardDelta(runtime.GuardState{Config: on}, runtime.GuardState{Config: cfg, Tokens: []auth.AgentToken{rest}}))
			assert.Empty(t, cons.BindingGuardDelta(runtime.GuardState{Config: on}, runtime.GuardState{Config: cfg, Tokens: []auth.AgentToken{rest}}))
		}
	}

	// A token and a client in the same state do not collide in `already`.
	off := &config.Config{RequireMCPAuth: false, Profiles: profiles}
	cur := runtime.GuardState{Config: off, Tokens: []auth.AgentToken{guardClient("c1", "P"), guardToken("t0", "P", true)}}
	cand := runtime.GuardState{Config: off, Tokens: []auth.AgentToken{guardClient("c1", "P"), guardToken("t0", "P", true), guardToken("t1", "P", true)}}
	delta := proxy.BindingGuardDelta(cur, cand)
	require.Len(t, delta, 1, "already-bypassable bindings (keyed by token name, not the empty client id) do not hide the new token")
	assert.Equal(t, "t1", delta[0].TokenName)
}

// The per-request anonymous guard (which also covers file-watcher hot reloads)
// counts a stored guard-bound token exactly like a client binding.
func TestBindingGuardActive_CountsGuardBoundToken(t *testing.T) {
	profiles := []config.ProfileConfig{{Name: "P", Servers: []string{"a"}}}
	proxy, rt := createTestProxyWithRuntimeCfg(t, []*config.ServerConfig{{Name: "a", Enabled: true}}, func(cfg *config.Config) {
		cfg.RequireMCPAuth = false
		cfg.Profiles = profiles
	})
	idx := proxy.profileIndexFor(proxy.currentConfig())
	assert.False(t, proxy.bindingGuardActive(idx), "no bindings yet")

	raw, err := auth.GenerateToken()
	require.NoError(t, err)
	rest := guardToken("rest", "P", false)
	require.NoError(t, rt.StorageManager().CreateAgentToken(rest, raw, []byte("k")))
	assert.False(t, proxy.bindingGuardActive(idx), "a REST pinned token is not a standing binding (A13)")

	raw2, err := auth.GenerateToken()
	require.NoError(t, err)
	require.NoError(t, rt.StorageManager().CreateAgentToken(guardToken("mcp", "P", true), raw2, []byte("k")))
	assert.True(t, proxy.bindingGuardActive(idx), "a guard-bound token denies anonymous access while it is bypassable")
	assert.True(t, proxy.bindingGuardActive(nil), "publication gap: any live guarded binding denies")
	got := proxy.ResolveProfileV3(context.Background(), idx)
	assert.True(t, got.BindingGuarded)
	assert.Equal(t, string(profile.SourceAnonymous), got.Source)

	active := proxy.BindingGuardActiveBindings()
	require.Len(t, active, 1)
	assert.Equal(t, "mcp", active[0].TokenName)

	_, _, err = rt.StorageManager().RevokeAgentTokenReport("", "mcp")
	require.NoError(t, err)
	assert.False(t, proxy.bindingGuardActive(idx), "a revoked token releases the guard")
}

// Spec 115 T013b (FR-012b): a dangling pin stays guarded. Fixes never point
// anonymous at the missing profile.
func TestBindingGuard_DanglingPinStaysGuarded(t *testing.T) {
	profiles := []config.ProfileConfig{{Name: "Q", Servers: []string{"a"}}}
	proxy, rt := createTestProxyWithRuntimeCfg(t, []*config.ServerConfig{{Name: "a", Enabled: true}}, func(cfg *config.Config) {
		cfg.RequireMCPAuth = false
		cfg.AnonymousProfile = "Q"
		cfg.Profiles = profiles
	})
	idx := proxy.profileIndexFor(proxy.currentConfig())
	for _, rec := range []auth.AgentToken{guardToken("dangling-tok", "gone", true), guardClient("dangling-cli", "gone")} {
		on := &config.Config{RequireMCPAuth: true, Profiles: profiles}
		off := &config.Config{RequireMCPAuth: false, AnonymousProfile: "Q", Profiles: profiles}
		delta := proxy.BindingGuardDelta(runtime.GuardState{Config: on, Tokens: []auth.AgentToken{rec}}, runtime.GuardState{Config: off, Tokens: []auth.AgentToken{rec}})
		require.Len(t, delta, 1, rec.Name)
		fixes := proxy.BindingGuardFixes(runtime.GuardState{Config: off, Tokens: []auth.AgentToken{rec}}, delta)
		for _, f := range fixes {
			assert.NotEqual(t, profile.GuardFixSetAnonymousProfile, f.Kind, "no anonymous_profile fix for a dangling target")
		}
		both := &config.Config{RequireMCPAuth: false, AnonymousProfile: "also-gone", Profiles: profiles}
		assert.Empty(t, proxy.BindingGuardDelta(runtime.GuardState{Config: on}, runtime.GuardState{Config: both, Tokens: []auth.AgentToken{rec}}),
			"deny-all both ways is not bypassable")
	}
	raw, err := auth.GenerateToken()
	require.NoError(t, err)
	require.NoError(t, rt.StorageManager().CreateAgentToken(guardToken("dangling-tok", "gone", true), raw, []byte("k")))
	assert.True(t, proxy.bindingGuardActive(idx), "a hot-reloaded auth-off config with a dangling guarded binding denies anonymous")
}
