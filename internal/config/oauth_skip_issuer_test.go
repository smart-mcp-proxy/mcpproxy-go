package config

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Spec 135 FR-015 (contracts/config-skip-issuer-validation.md): the opt-out
// is request policy. Toggling it, or adding an oauth block that holds only
// it, is not an OAuth identity change, so it never clears a grant or moves
// the OAuth epoch. Any credential field still is.
func TestFR015_SkipIssuerValidationIsNotIdentity(t *testing.T) {
	on, off := true, false
	assert.False(t, OAuthConfigChanged(nil, &OAuthConfig{SkipIssuerValidation: &on}), "nil -> skip-only block")
	assert.False(t, OAuthConfigChanged(&OAuthConfig{SkipIssuerValidation: &on}, nil), "skip-only block -> nil")
	assert.False(t, OAuthConfigChanged(&OAuthConfig{SkipIssuerValidation: &on}, &OAuthConfig{SkipIssuerValidation: &off}))
	assert.False(t, OAuthConfigChanged(&OAuthConfig{ClientID: "a"}, &OAuthConfig{ClientID: "a", SkipIssuerValidation: &on}))
	assert.False(t, OAuthConfigChanged(nil, &OAuthConfig{SkipIssuerValidation: &on, RefreshReplayEnabled: &on}))

	assert.True(t, OAuthConfigChanged(nil, &OAuthConfig{SkipIssuerValidation: &on, ClientID: "a"}), "a credential field is identity")
	assert.True(t, OAuthConfigChanged(nil, &OAuthConfig{Scopes: []string{"read"}}), "a non-policy block is still a change")
}

func TestSkipIssuerValidation_JSONAndMerge(t *testing.T) {
	var sc ServerConfig
	require.NoError(t, json.Unmarshal([]byte(`{"name":"x","oauth":{"skip_issuer_validation":true}}`), &sc))
	require.NotNil(t, sc.OAuth.SkipIssuerValidation)
	assert.True(t, *sc.OAuth.SkipIssuerValidation)

	on, off := true, false
	base := &OAuthConfig{ClientID: "a", SkipIssuerValidation: &on}
	// Omitted in a patch: unchanged.
	merged := MergeOAuthConfig(base, &OAuthConfig{ClientID: "a"}, false)
	require.NotNil(t, merged.SkipIssuerValidation)
	assert.True(t, *merged.SkipIssuerValidation)
	// Explicit false: set.
	merged = MergeOAuthConfig(base, &OAuthConfig{ClientID: "a", SkipIssuerValidation: &off}, false)
	require.NotNil(t, merged.SkipIssuerValidation)
	assert.False(t, *merged.SkipIssuerValidation)
	// The copy does not alias the source.
	cp := copyOAuthConfig(base)
	*cp.SkipIssuerValidation = false
	assert.True(t, *base.SkipIssuerValidation)
}
