package runtime

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

func oauthB(client, pin, mode string, live bool) OAuthBinding {
	return OAuthBinding{ClientID: client, Pin: pin, Mode: mode, Live: live}
}

func authOff() *config.Config { return &config.Config{} }

// With no OAuth bindings the effective token list IS the token list.
func TestGuardState_EffectiveTokensWithoutOAuthIsIdentical(t *testing.T) {
	toks := []auth.AgentToken{strictTok("cursor", "ro", auth.ProfileModeLocked)}
	st := GuardState{Config: authOff(), Tokens: toks}
	got := st.EffectiveTokens(time.Now())
	require.Equal(t, toks, got)
	if len(got) > 0 {
		require.Same(t, &toks[0], &got[0], "no copy when there is nothing to add")
	}
}

func TestBindingGuard_OAuthOnlyClientIsAnActiveNamedBinding(t *testing.T) {
	cur := GuardState{Config: authOff()}
	live := GuardState{Config: authOff(), OAuthBindings: []OAuthBinding{oauthB("cursor", "ro", auth.ProfileModeLocked, true)}}

	for name, g := range map[string]BindingGuard{"conservative": ConservativeBindingGuard{}, "strict": StrictOfflineBindingGuard{}} {
		t.Run(name, func(t *testing.T) {
			// A first approval: the candidate adds the entry with Live: true.
			delta := g.BindingGuardDelta(cur, live)
			require.Len(t, delta, 1)
			require.Equal(t, BindingRef{ClientID: "cursor", TokenName: "client-cursor", Profile: "ro", Mode: auth.ProfileModeLocked}, delta[0])

			// Lapsed or revoked: not a binding.
			lapsed := GuardState{Config: authOff(), OAuthBindings: []OAuthBinding{oauthB("cursor", "ro", auth.ProfileModeLocked, false)}}
			require.Empty(t, g.BindingGuardDelta(cur, lapsed))
			// All servers (no pin) is not a named binding.
			open := GuardState{Config: authOff(), OAuthBindings: []OAuthBinding{oauthB("cursor", "", auth.ProfileModeSwitchable, true)}}
			require.Empty(t, g.BindingGuardDelta(cur, open))
			// require_mcp_auth on never refuses.
			authOn := GuardState{Config: &config.Config{RequireMCPAuth: true}, OAuthBindings: live.OAuthBindings}
			require.Empty(t, g.BindingGuardDelta(cur, authOn))
			// An unchanged live binding is not NEW.
			require.Empty(t, g.BindingGuardDelta(live, live))
		})
	}
}

// A client with both credentials counts once, and the API-key record decides
// the binding (it is the truth for the request path too).
func TestBindingGuard_ClientWithBothCredentialsCountsOnce(t *testing.T) {
	g := ConservativeBindingGuard{}
	api := strictTok("cursor", "ro", auth.ProfileModeLocked)
	cur := GuardState{Config: authOff()}
	both := GuardState{
		Config: authOff(), Tokens: []auth.AgentToken{api},
		OAuthBindings: []OAuthBinding{oauthB("cursor", "ro", auth.ProfileModeLocked, true)},
	}
	delta := g.BindingGuardDelta(cur, both)
	require.Len(t, delta, 1, "one client, one ref")

	// The record wins over a stale entry.
	stale := GuardState{
		Config: authOff(), Tokens: []auth.AgentToken{api},
		OAuthBindings: []OAuthBinding{oauthB("cursor", "other", auth.ProfileModeSwitchable, true)},
	}
	delta = g.BindingGuardDelta(cur, stale)
	require.Len(t, delta, 1)
	require.Equal(t, "ro", delta[0].Profile)

	// A revoked API record leaves the live login binding in force.
	revoked := api
	revoked.Revoked = true
	login := GuardState{
		Config: authOff(), Tokens: []auth.AgentToken{revoked},
		OAuthBindings: []OAuthBinding{oauthB("cursor", "ro", auth.ProfileModeLocked, true)},
	}
	require.Len(t, g.BindingGuardDelta(cur, login), 1)
}

func TestBindingGuard_NoOAuthBindingsKeepsTodaysResult(t *testing.T) {
	cur := GuardState{Config: authOff(), Tokens: []auth.AgentToken{strictTok("cursor", "ro", auth.ProfileModeLocked)}}
	cand := GuardState{Config: authOff(), Tokens: []auth.AgentToken{strictTok("cursor", "ro", auth.ProfileModeLocked), strictTok("codex", "ro", auth.ProfileModeLocked)}}
	for _, g := range []BindingGuard{ConservativeBindingGuard{}, StrictOfflineBindingGuard{}} {
		require.Len(t, g.BindingGuardDelta(cur, cand), 1)
	}
}

type fakeOAuthSource struct {
	bindings map[string]storage.OAuthClientBinding
	grants   []storage.OAuthGrant
	err      error
}

func (f fakeOAuthSource) ListOAuthClientBindings() (map[string]storage.OAuthClientBinding, error) {
	return f.bindings, f.err
}
func (f fakeOAuthSource) ListOAuthGrants() ([]storage.OAuthGrant, error) { return f.grants, f.err }

func TestLoadOAuthBindings_LiveMeansAGrantNotRevokedAndNotLapsed(t *testing.T) {
	now := time.Now()
	src := fakeOAuthSource{
		bindings: map[string]storage.OAuthClientBinding{
			"live": {ProfilePin: "p", ProfileMode: auth.ProfileModeLocked},
			"dead": {ProfilePin: "p", ProfileMode: auth.ProfileModeLocked},
			"lap":  {ProfilePin: "p", ProfileMode: auth.ProfileModeLocked},
			"none": {ProfilePin: "p", ProfileMode: auth.ProfileModeLocked},
		},
		grants: []storage.OAuthGrant{
			{GrantID: "g1", Client: "live", RefreshExpiresAt: now.Add(time.Hour)},
			{GrantID: "g2", Client: "dead", RefreshExpiresAt: now.Add(time.Hour), Revoked: true},
			{GrantID: "g3", Client: "lap", RefreshExpiresAt: now.Add(-time.Hour)},
			{GrantID: "g4", Client: "dead", RefreshExpiresAt: now.Add(time.Hour), Revoked: true},
			{GrantID: "g5", Client: "live", RefreshExpiresAt: now.Add(-time.Hour)},
		},
	}
	got, err := LoadOAuthBindings(src, now)
	require.NoError(t, err)
	require.Equal(t, []OAuthBinding{
		{ClientID: "dead", Pin: "p", Mode: auth.ProfileModeLocked},
		{ClientID: "lap", Pin: "p", Mode: auth.ProfileModeLocked},
		{ClientID: "live", Pin: "p", Mode: auth.ProfileModeLocked, Live: true},
		{ClientID: "none", Pin: "p", Mode: auth.ProfileModeLocked},
	}, got)
}

// A storage read error refuses the change instead of guessing (fail closed),
// and a store that has no browser-login storage contributes nothing.
func TestLoadOAuthBindings_ErrorIsReturnedAndNonStoreIsNil(t *testing.T) {
	_, err := LoadOAuthBindings(fakeOAuthSource{err: errors.New("boom")}, time.Now())
	require.Error(t, err)
	got, err := LoadOAuthBindings(struct{}{}, time.Now())
	require.NoError(t, err)
	require.Nil(t, got)
	got, err = LoadOAuthBindings(nil, time.Now())
	require.NoError(t, err)
	require.Nil(t, got)
}

// On a database that never used browser login the read is empty and writes
// nothing (no bucket is created by the read path).
func TestLoadOAuthBindings_NeverUsedDatabaseIsEmpty(t *testing.T) {
	store, cleanup := setupTestStorage(t)
	defer cleanup()
	got, err := LoadOAuthBindings(store, time.Now())
	require.NoError(t, err)
	require.Empty(t, got)
}
