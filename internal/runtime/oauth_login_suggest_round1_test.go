package runtime

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
)

func (fx *oauthFx) addCustom(id, display string) {
	fx.t.Helper()
	_, _, err := fx.clients.Add(fx.ctx(), fx.actor(), AddRequest{ID: id, DisplayName: display, Profile: "ro"})
	require.NoError(fx.t, err)
}

func TestOAuthDescribe_SuggestedClient_DisplayName(t *testing.T) {
	fx := newOAuthFx(t)
	fx.addCustom("acme", "Acme Agent")
	for _, name := range []string{"Acme Agent", "  acme agent  "} {
		s := fx.suggestFor(name, "").SuggestedClient
		require.NotNil(t, s, name)
		assert.Equal(t, "acme", s.ID)
		assert.Equal(t, "display_name", s.Matched)
	}
}

func TestOAuthDescribe_SuggestedClient_DisplayNameBeatsKnownName(t *testing.T) {
	fx := newOAuthFx(t)
	fx.mintClient("claude-desktop", "ro", auth.ProfileModeLocked)
	// Registry display name "Claude Desktop"; "Claude" and "claude-ai" are known names.
	s := fx.suggestFor("Claude Desktop", "").SuggestedClient
	require.NotNil(t, s)
	assert.Equal(t, "display_name", s.Matched)
	s = fx.suggestFor("Claude", "").SuggestedClient
	require.NotNil(t, s)
	assert.Equal(t, "known_name", s.Matched)
}

func TestOAuthDescribe_SuggestedClient_UnicodeFolding(t *testing.T) {
	fx := newOAuthFx(t)
	fx.addCustom("sig-a", "Σ")
	s := fx.suggestFor("σ", "").SuggestedClient
	require.NotNil(t, s)
	assert.Equal(t, "sig-a", s.ID)
	fx.addCustom("sig-b", "ς")
	assert.Nil(t, fx.suggestFor("σ", "").SuggestedClient, "Σ and ς fold to the same letter as σ: two clients")
}

func TestOAuthDescribe_SuggestedClient_Suffix(t *testing.T) {
	fx := newOAuthFx(t)
	fx.mintClient("codex", "ro", auth.ProfileModeLocked)
	s := fx.suggestFor("Codex (my (dev) server)", "").SuggestedClient
	require.NotNil(t, s, "one balanced trailing group is stripped, nested parentheses included")
	assert.Equal(t, "codex", s.ID)

	fx.addCustom("acme", "Acme (prod)")
	s = fx.suggestFor("Acme (prod)", "").SuggestedClient
	require.NotNil(t, s, "whole-name match")
	assert.Equal(t, "acme", s.ID)

	// Whole name matches one client, the stripped name another: no suggestion.
	fx.addCustom("other", "Codex (x)")
	assert.Nil(t, fx.suggestFor("Codex (x)", "").SuggestedClient)
}

func TestSuggestExistingClient_StripTrailingGroup(t *testing.T) {
	cases := map[string]struct {
		in, base string
		ok       bool
	}{
		"plain":        {"a (b)", "a", true},
		"nested":       {"a (b (c) d)", "a", true},
		"unbalanced":   {"a (b", "", false},
		"only group":   {"(b)", "", false},
		"no group":     {"abc", "", false},
		"not trailing": {"a (b) c", "", false},
		"close first":  {"a )b(", "", false},
	}
	for name, c := range cases {
		base, ok := stripTrailingGroup(c.in)
		assert.Equal(t, c.ok, ok, name)
		assert.Equal(t, c.base, base, name)
	}
}
