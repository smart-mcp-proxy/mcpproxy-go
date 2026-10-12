package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOAuthCallbackBaseURLValidation(t *testing.T) {
	for _, valid := range []string{"", "https://proxy.example", "https://proxy.example:8447/", "https://proxy.tailnet.ts.net"} {
		require.NoError(t, (&OAuthConfig{CallbackBaseURL: valid}).Validate())
	}
	for _, invalid := range []string{"http://proxy.example", "//proxy.example", "https://", "https://user:secret@proxy.example", "https://proxy.example/path", "https://proxy.example?key=x", "https://proxy.example#anchor", "https://proxy.example#", " https://proxy.example"} {
		t.Run(invalid, func(t *testing.T) {
			require.Error(t, (&OAuthConfig{CallbackBaseURL: invalid}).Validate())
			cfg := &Config{Servers: []*ServerConfig{{Name: "linear", OAuth: &OAuthConfig{CallbackBaseURL: invalid}}}}
			require.NotEmpty(t, cfg.oauthRedirectURIErrors())
		})
	}
	actual, err := OAuthRemoteCallbackURL("https://proxy.example:8447/", "server/name")
	require.NoError(t, err)
	require.Equal(t, "https://proxy.example:8447/oauth/callback/server%2Fname", actual)
}

func TestOAuthCallbackBaseURLSurvivesMergeAndTriggersReconnect(t *testing.T) {
	base := &ServerConfig{Name: "linear", Protocol: "http", URL: "https://mcp.linear.app/mcp", OAuth: &OAuthConfig{Scopes: []string{"read"}}}
	patch := &ServerConfig{OAuth: &OAuthConfig{CallbackBaseURL: "https://proxy.example"}}
	merged, _, err := MergeServerConfig(base, patch, DefaultMergeOptions())
	require.NoError(t, err)
	require.Equal(t, "https://proxy.example", merged.OAuth.CallbackBaseURL)
	require.True(t, OAuthConfigChanged(base.OAuth, merged.OAuth))
	require.False(t, OAuthConfigChanged(merged.OAuth, copyOAuthConfig(merged.OAuth)))
}
