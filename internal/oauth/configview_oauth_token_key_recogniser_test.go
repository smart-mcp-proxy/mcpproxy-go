package oauth

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Spec 123: the publish door uses the SAME recogniser as config validation, so
// every value validation accepts as a reference (spaced forms included) is
// published as written, and anything else (a reference mixed with literal
// text) stays masked.
func TestOAuthTokenKeyMaskingMatchesValidationRecogniser(t *testing.T) {
	refs := []string{
		"${ file:/run/credentials/mcpproxy.service/oauth-key}",
		"${ env:MCPPROXY_OAUTH_KEY_SOURCE }",
		"${ keyring:mcpproxy-oauth-key }",
	}
	for _, v := range refs {
		t.Run("reference "+v, func(t *testing.T) {
			require.True(t, config.IsOAuthTokenKeyReference(v), "validation accepts it")
			stored := config.DefaultConfig()
			stored.OAuthTokenEncryptionKey = v
			published := RedactedConfig(stored)
			require.NotNil(t, published)
			assert.Equal(t, v, published.OAuthTokenEncryptionKey)
		})
	}
	mixed := []string{
		"${env:KEY}" + spec123LiteralKey,
		spec123LiteralKey + "${ file:/run/key }",
		"prefix-${keyring:name}",
		// An unclosed nested reference is one regexp match, not a reference.
		"${env:${env:KEY}",
		"${keyring:${file:/abs/key}",
	}
	for _, v := range mixed {
		t.Run("mixed "+v, func(t *testing.T) {
			require.False(t, config.IsOAuthTokenKeyReference(v))
			stored := config.DefaultConfig()
			stored.OAuthTokenEncryptionKey = v
			published := RedactedConfig(stored)
			require.NotNil(t, published)
			assert.NotEqual(t, v, published.OAuthTokenEncryptionKey)
		})
	}
}
