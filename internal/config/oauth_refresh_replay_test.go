package config

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func replayBool(v bool) *bool { return &v }

func TestRefreshReplayConfigContract(t *testing.T) {
	for _, tc := range []struct {
		name, endpoint string
		enabled        *bool
		allowed, valid bool
	}{
		{name: "default", valid: true},
		{name: "off", enabled: replayBool(false), valid: true},
		{name: "no_pin", enabled: replayBool(true)},
		{name: "remote_http", endpoint: "http://example.com/token", enabled: replayBool(true)},
		{name: "query", endpoint: "https://example.com/token?secret=synthetic", enabled: replayBool(true)},
		{name: "https", endpoint: "https://example.com/token", enabled: replayBool(true), allowed: true, valid: true},
		{name: "loopback", endpoint: "http://127.0.0.1:1234/token", enabled: replayBool(true), allowed: true, valid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := &OAuthConfig{RefreshReplayEnabled: tc.enabled, TokenEndpoint: tc.endpoint}
			require.Equal(t, tc.allowed, o.RefreshReplayAllowed())
			require.Equal(t, tc.valid, o.ValidateRefreshReplay() == nil)
			if err := o.ValidateRefreshReplay(); err != nil {
				require.NotContains(t, err.Error(), "synthetic")
			}
			cfg := DefaultConfig()
			cfg.Servers = []*ServerConfig{{Name: "fixture", URL: "https://example.com/mcp", OAuth: o}}
			found := false
			for _, e := range cfg.ValidateDetailed() {
				if e.Field == "mcpServers[0].oauth.refresh_replay_enabled" {
					found = true
				}
			}
			require.Equal(t, !tc.valid, found)
		})
	}
	require.False(t, (*OAuthConfig)(nil).RefreshReplayAllowed())
}

func TestRefreshReplayConfigRoundTripAndCopy(t *testing.T) {
	for _, value := range []bool{false, true} {
		in := &OAuthConfig{RefreshReplayEnabled: replayBool(value), TokenEndpoint: "https://example.com/token"}
		raw, err := json.Marshal(in)
		require.NoError(t, err)
		require.Contains(t, string(raw), "refresh_replay_enabled")
		var out OAuthConfig
		require.NoError(t, json.Unmarshal(raw, &out))
		require.Equal(t, in, &out)
		cp := copyOAuthConfig(in)
		require.Equal(t, in, cp)
		*cp.RefreshReplayEnabled = !value
		require.Equal(t, value, *in.RefreshReplayEnabled)
	}
	empty, err := json.Marshal(&OAuthConfig{})
	require.NoError(t, err)
	require.NotContains(t, string(empty), "refresh_replay_enabled")
}

func TestRefreshReplayConfigMergeCanDisable(t *testing.T) {
	base := &OAuthConfig{RefreshReplayEnabled: replayBool(true), TokenEndpoint: "https://example.com/token"}
	kept := MergeOAuthConfig(base, &OAuthConfig{ClientID: "client"}, false)
	require.True(t, *kept.RefreshReplayEnabled)
	patch := &OAuthConfig{RefreshReplayEnabled: replayBool(false)}
	changed := MergeOAuthConfig(base, patch, false)
	require.False(t, *changed.RefreshReplayEnabled)
	*changed.RefreshReplayEnabled = true
	require.False(t, *patch.RefreshReplayEnabled)
	require.True(t, *base.RefreshReplayEnabled)
}

func TestRefreshReplayConfigPolicyChangePreservesGrantIdentity(t *testing.T) {
	off := &ServerConfig{Name: "fixture", OAuth: &OAuthConfig{TokenEndpoint: "https://example.com/token"}}
	on := CopyServerConfig(off)
	on.OAuth.RefreshReplayEnabled = replayBool(true)
	require.False(t, OAuthConfigChanged(off.OAuth, on.OAuth))
	require.False(t, ConnectionEquivalent(off, on))
}

func TestRefreshReplayPartialPatchInheritsEndpoint(t *testing.T) {
	base := &OAuthConfig{TokenEndpoint: "https://example.com/token"}
	patch := &OAuthConfig{RefreshReplayEnabled: replayBool(true)}
	require.NoError(t, patch.Validate())
	merged := MergeOAuthConfig(base, patch, false)
	require.NoError(t, merged.ValidateRefreshReplay())
	require.True(t, merged.RefreshReplayAllowed())
}
