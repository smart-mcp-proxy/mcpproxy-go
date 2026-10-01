package runtime

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

func strictTok(client, pin, mode string) auth.AgentToken {
	return auth.AgentToken{
		Name: auth.ClientTokenName(client), Kind: auth.KindClient, ClientID: client,
		ProfilePin: pin, ProfileMode: mode, ExpiresAt: time.Now().Add(time.Hour),
	}
}

// The offline CLI cannot evaluate reach, so its guard exempts only an UNCHANGED
// named binding: a re-point of an already-bound client is refused, while the
// conservative guard (whose re-point allowance other callers rely on) lets it
// through.
func TestStrictOfflineBindingGuard_RepointIsRefused(t *testing.T) {
	authOff := GuardState{Config: &config.Config{}}
	authOn := GuardState{Config: &config.Config{RequireMCPAuth: true}}
	withTokens := func(st GuardState, toks ...auth.AgentToken) GuardState {
		st.Tokens = toks
		return st
	}
	cur := withTokens(authOff, strictTok("cursor", "ro", auth.ProfileModeLocked))
	g := StrictOfflineBindingGuard{}

	cases := []struct {
		name string
		cand GuardState
		want int
	}{
		{"re-point to another profile", withTokens(authOff, strictTok("cursor", "full", auth.ProfileModeLocked)), 1},
		{"mode change", withTokens(authOff, strictTok("cursor", "ro", auth.ProfileModeSwitchable)), 1},
		{"unchanged binding", withTokens(authOff, strictTok("cursor", "ro", auth.ProfileModeLocked)), 0},
		{"fresh client", withTokens(authOff, strictTok("cursor", "ro", auth.ProfileModeLocked), strictTok("codex", "ro", auth.ProfileModeLocked)), 1},
		{"unbound client is not bypassable", withTokens(authOff, strictTok("cursor", "", auth.ProfileModeSwitchable)), 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			delta := g.BindingGuardDelta(cur, tc.cand)
			require.Len(t, delta, tc.want)
		})
	}

	t.Run("auth on never refuses", func(t *testing.T) {
		curOn := withTokens(authOn, strictTok("cursor", "ro", auth.ProfileModeLocked))
		require.Empty(t, g.BindingGuardDelta(curOn, withTokens(authOn, strictTok("cursor", "full", auth.ProfileModeLocked))))
	})

	t.Run("fixes and active bindings delegate to the conservative guard", func(t *testing.T) {
		fixes := g.BindingGuardFixes(authOff, nil)
		require.Equal(t, ConservativeBindingGuard{}.BindingGuardFixes(authOff, nil), fixes)
		require.Nil(t, g.BindingGuardActiveBindings())
	})

	t.Run("the conservative guard still lets the re-point through", func(t *testing.T) {
		cand := withTokens(authOff, strictTok("cursor", "full", auth.ProfileModeLocked))
		require.Empty(t, ConservativeBindingGuard{}.BindingGuardDelta(cur, cand),
			"documented semantics: an already-bound client is not newly bypassable")
	})
}
