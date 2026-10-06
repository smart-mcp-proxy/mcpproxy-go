package server

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime/stateview"
)

func guardTestConfig(requireAuth bool, anonymous string, profiles ...config.ProfileConfig) *config.Config {
	cfg := config.DefaultConfig()
	cfg.Servers = []*config.ServerConfig{{Name: "a", Enabled: true}, {Name: "b", Enabled: true}}
	cfg.RequireMCPAuth = requireAuth
	cfg.AnonymousProfile = anonymous
	cfg.Profiles = profiles
	return cfg
}

func guardTestToken(clientID, mode, pin string) auth.AgentToken {
	return auth.AgentToken{
		Name: auth.ClientTokenName(clientID), Kind: auth.KindClient, ClientID: clientID,
		ProfileMode: mode, ProfilePin: pin,
		ExpiresAt: time.Now().Add(time.Hour), CreatedAt: time.Now(),
	}
}

func guardTestProxy(t *testing.T) *MCPProxyServer {
	t.Helper()
	proxy, _ := createTestProxyWithRuntimeCfg(t, []*config.ServerConfig{{Name: "a", Enabled: true}}, nil)
	return proxy
}

func clientIDs(refs []runtime.BindingRef) []string {
	out := []string{}
	for _, r := range refs {
		out = append(out, r.ClientID)
	}
	return out
}

// TestBindingGuardDelta_EnforcementMatrixDeltaCases pins data-model §7 /
// plan D2: Delta = bypassable(candidate) minus already-bypassable(current),
// keyed by client id.
func TestBindingGuardDelta_EnforcementMatrixDeltaCases(t *testing.T) {
	proxy := guardTestProxy(t)
	P := config.ProfileConfig{Name: "P", Servers: []string{"a"}}
	Q := config.ProfileConfig{Name: "Q", Servers: []string{"a", "b"}}
	switchS := []string{"S"}
	tokens := func(mode, pin string) []auth.AgentToken {
		return []auth.AgentToken{guardTestToken("cursor", mode, pin)}
	}

	tests := []struct {
		name      string
		current   runtime.GuardState
		candidate runtime.GuardState
		want      []string
	}{
		{
			name:      "auth off, unconfined anonymous, new named binding is refused",
			current:   runtime.GuardState{Config: guardTestConfig(false, "", P)},
			candidate: runtime.GuardState{Config: guardTestConfig(false, "", P), Tokens: tokens(auth.ProfileModeLocked, "P")},
			want:      []string{"cursor"},
		},
		{
			name:      "auth on in candidate is empty",
			current:   runtime.GuardState{Config: guardTestConfig(false, "", P)},
			candidate: runtime.GuardState{Config: guardTestConfig(true, "", P), Tokens: tokens(auth.ProfileModeLocked, "P")},
		},
		{
			name:      "already bypassable and staying bypassable is not refused",
			current:   runtime.GuardState{Config: guardTestConfig(false, "", P), Tokens: tokens(auth.ProfileModeLocked, "P")},
			candidate: runtime.GuardState{Config: guardTestConfig(false, "", P), Tokens: tokens(auth.ProfileModeLocked, "P")},
		},
		{
			name:      "turning auth off under a binding is refused",
			current:   runtime.GuardState{Config: guardTestConfig(true, "", P), Tokens: tokens(auth.ProfileModeLocked, "P")},
			candidate: runtime.GuardState{Config: guardTestConfig(false, "", P), Tokens: tokens(auth.ProfileModeLocked, "P")},
			want:      []string{"cursor"},
		},
		{
			name:      "clearing anonymous_profile is refused",
			current:   runtime.GuardState{Config: guardTestConfig(false, "P", P), Tokens: tokens(auth.ProfileModeLocked, "P")},
			candidate: runtime.GuardState{Config: guardTestConfig(false, "", P), Tokens: tokens(auth.ProfileModeLocked, "P")},
			want:      []string{"cursor"},
		},
		{
			name:      "widening anonymous_profile is refused",
			current:   runtime.GuardState{Config: guardTestConfig(false, "P", P, Q), Tokens: tokens(auth.ProfileModeLocked, "P")},
			candidate: runtime.GuardState{Config: guardTestConfig(false, "Q", P, Q), Tokens: tokens(auth.ProfileModeLocked, "P")},
			want:      []string{"cursor"},
		},
		{
			name:      "moving a binding onto a narrower profile is refused",
			current:   runtime.GuardState{Config: guardTestConfig(false, "Q", P, Q), Tokens: tokens(auth.ProfileModeLocked, "Q")},
			candidate: runtime.GuardState{Config: guardTestConfig(false, "Q", P, Q), Tokens: tokens(auth.ProfileModeLocked, "P")},
			want:      []string{"cursor"},
		},
		{
			name:      "pure rename of an unrelated profile is empty",
			current:   runtime.GuardState{Config: guardTestConfig(false, "P", P, config.ProfileConfig{Name: "X", Servers: []string{"b"}}), Tokens: tokens(auth.ProfileModeLocked, "P")},
			candidate: runtime.GuardState{Config: guardTestConfig(false, "P", P, config.ProfileConfig{Name: "Y", Servers: []string{"b"}}), Tokens: tokens(auth.ProfileModeLocked, "P")},
		},
		{
			// Anonymous Q equals what the switchable binding on P can reach
			// through S (P{a}, S{a,b}); anything that shrinks that reach makes Q
			// wider than the binding.
			name: "narrowing a member of switchable_to is refused",
			current: runtime.GuardState{
				Config: guardTestConfig(false, "Q", config.ProfileConfig{Name: "P", Servers: []string{"a"}, SwitchableTo: &switchS},
					config.ProfileConfig{Name: "S", Servers: []string{"a", "b"}}, Q),
				Tokens: tokens(auth.ProfileModeSwitchable, "P"),
			},
			candidate: runtime.GuardState{
				Config: guardTestConfig(false, "Q", config.ProfileConfig{Name: "P", Servers: []string{"a"}, SwitchableTo: &switchS},
					config.ProfileConfig{Name: "S", Servers: []string{"a"}}, Q),
				Tokens: tokens(auth.ProfileModeSwitchable, "P"),
			},
			want: []string{"cursor"},
		},
		{
			name: "removing S from switchable_to is refused",
			current: runtime.GuardState{
				Config: guardTestConfig(false, "Q", config.ProfileConfig{Name: "P", Servers: []string{"a"}, SwitchableTo: &switchS},
					config.ProfileConfig{Name: "S", Servers: []string{"a", "b"}}, Q),
				Tokens: tokens(auth.ProfileModeSwitchable, "P"),
			},
			candidate: runtime.GuardState{
				Config: guardTestConfig(false, "Q", config.ProfileConfig{Name: "P", Servers: []string{"a"}},
					config.ProfileConfig{Name: "S", Servers: []string{"a", "b"}}, Q),
				Tokens: tokens(auth.ProfileModeSwitchable, "P"),
			},
			want: []string{"cursor"},
		},
		{
			name: "deleting S is refused",
			current: runtime.GuardState{
				Config: guardTestConfig(false, "Q", config.ProfileConfig{Name: "P", Servers: []string{"a"}, SwitchableTo: &switchS},
					config.ProfileConfig{Name: "S", Servers: []string{"a", "b"}}, Q),
				Tokens: tokens(auth.ProfileModeSwitchable, "P"),
			},
			candidate: runtime.GuardState{
				Config: guardTestConfig(false, "Q", config.ProfileConfig{Name: "P", Servers: []string{"a"}, SwitchableTo: &switchS}, Q),
				Tokens: tokens(auth.ProfileModeSwitchable, "P"),
			},
			want: []string{"cursor"},
		},
		{
			name:      "an expired or revoked credential is not a binding",
			current:   runtime.GuardState{Config: guardTestConfig(false, "", P)},
			candidate: runtime.GuardState{Config: guardTestConfig(false, "", P), Tokens: []auth.AgentToken{revokedGuardToken()}},
		},
		{
			name:      "an empty pin is never a named binding",
			current:   runtime.GuardState{Config: guardTestConfig(false, "", P)},
			candidate: runtime.GuardState{Config: guardTestConfig(false, "", P), Tokens: tokens(auth.ProfileModeSwitchable, "")},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := proxy.BindingGuardDelta(tt.current, tt.candidate)
			require.ElementsMatch(t, tt.want, clientIDs(got))
		})
	}
}

func revokedGuardToken() auth.AgentToken {
	tok := guardTestToken("cursor", auth.ProfileModeLocked, "P")
	tok.Revoked = true
	return tok
}

// TestBindingGuardDelta_ClassifyChangeOnAnonymousReachableProfile: a
// tools.classify change on the anonymous profile that admits a tool the bound
// profile does not is refused.
func TestBindingGuardDelta_ClassifyChangeOnAnonymousReachableProfile(t *testing.T) {
	proxy := guardTestProxy(t)
	proxy.mainServer.runtime.Supervisor().StateView().UpdateServer("a", func(status *stateview.ServerStatus) {
		status.ToolsDiscovered = true
		status.Tools = []stateview.ToolInfo{{Name: "mystery"}} // unannotated
	})
	bound := config.ProfileConfig{Name: "P", Servers: []string{"a"}, Unannotated: config.ProfileUnannotatedDeny}
	anonBefore := config.ProfileConfig{Name: "Q", Servers: []string{"a"}, Unannotated: config.ProfileUnannotatedDeny}
	anonAfter := config.ProfileConfig{Name: "Q", Servers: []string{"a"}, Unannotated: config.ProfileUnannotatedDeny,
		Tools: &config.ProfileToolRules{Classify: map[string]string{"a:mystery": "read"}}}
	tokens := []auth.AgentToken{guardTestToken("cursor", auth.ProfileModeLocked, "P")}
	// anonymous Q starts equal to P (both deny unannotated) -> not bypassable.
	cur := runtime.GuardState{Config: guardTestConfig(false, "Q", bound, anonBefore), Tokens: tokens}
	require.Empty(t, proxy.BindingGuardDelta(cur, cur))
	cand := runtime.GuardState{Config: guardTestConfig(false, "Q", bound, anonAfter), Tokens: tokens}
	require.Equal(t, []string{"cursor"}, clientIDs(proxy.BindingGuardDelta(cur, cand)))
}

func TestBindingGuardFixes_TargetOnlyWhenProfileQualifies(t *testing.T) {
	proxy := guardTestProxy(t)
	P := config.ProfileConfig{Name: "P", Servers: []string{"a"}}
	tokens := []auth.AgentToken{guardTestToken("cursor", auth.ProfileModeLocked, "P")}
	cand := runtime.GuardState{Config: guardTestConfig(false, "", P), Tokens: tokens}
	delta := proxy.BindingGuardDelta(runtime.GuardState{Config: cand.Config}, cand)
	require.Equal(t, []runtime.GuardFix{
		{Kind: profile.GuardFixRequireMCPAuth},
		{Kind: profile.GuardFixSetAnonymousProfile, Target: "P"},
	}, proxy.BindingGuardFixes(cand, delta))

	// P's switchable_to reaches wider R: a locked binding cannot escape to R,
	// so anonymous=P (which reaches R) is still wider -> no target.
	R := config.ProfileConfig{Name: "R", Servers: []string{"a", "b"}}
	sw := []string{"R"}
	P2 := config.ProfileConfig{Name: "P", Servers: []string{"a"}, SwitchableTo: &sw}
	cand2 := runtime.GuardState{Config: guardTestConfig(false, "", P2, R), Tokens: tokens}
	delta2 := proxy.BindingGuardDelta(runtime.GuardState{Config: cand2.Config}, cand2)
	require.Equal(t, []runtime.GuardFix{
		{Kind: profile.GuardFixRequireMCPAuth},
		{Kind: profile.GuardFixSetAnonymousProfile},
	}, proxy.BindingGuardFixes(cand2, delta2))
}

// TestBindingGuardActive_ToolSetReevaluation: a binding that is not
// bypassable becomes bypassable when a newly published snapshot adds a tool
// the anonymous profile admits and the bound profile does not, with no config
// change (FR-008a "tool-set re-evaluation").
func TestBindingGuardActive_ToolSetReevaluation(t *testing.T) {
	proxy, idx := bindingGuardTestProxy(t, "Q", []config.ProfileConfig{
		{Name: "P", Servers: []string{"a"}, Tools: &config.ProfileToolRules{Deny: []string{"a:danger"}}},
		{Name: "Q", Servers: []string{"a"}},
	}, auth.ProfileModeLocked, "P")
	require.False(t, proxy.bindingGuardActive(idx), "no tools yet: Q admits nothing P does not")
	require.Empty(t, proxy.BindingGuardActiveBindings())

	proxy.mainServer.runtime.Supervisor().StateView().UpdateServer("a", func(status *stateview.ServerStatus) {
		status.ToolsDiscovered = true
		status.Tools = []stateview.ToolInfo{{Name: "danger", Annotations: &config.ToolAnnotations{ReadOnlyHint: boolPtr(true)}}}
	})
	require.True(t, proxy.bindingGuardActive(idx), "the runtime guard trips on the publish with no config change")
	active := proxy.BindingGuardActiveBindings()
	require.Len(t, active, 1)
	require.Equal(t, "cursor", active[0].ClientID)
	require.Equal(t, "P", active[0].Profile)
}
