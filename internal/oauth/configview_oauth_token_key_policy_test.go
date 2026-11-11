package oauth

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Spec 123 FR-024 (masking policy): in every config output a LITERAL
// oauth_token_encryption_key is always masked, and a REFERENCE (${file:},
// ${env:}, ${keyring:}) is shown exactly as written, because it is
// operator-authored and not secret. reveal_secret_headers changes neither.
func TestOAuthTokenKeyMaskingPolicy(t *testing.T) {
	cases := []struct {
		name      string
		value     string
		reference bool
	}{
		{"literal", spec123LiteralKey, false},
		{"file reference", "${file:/run/credentials/mcpproxy.service/oauth-key}", true},
		{"env reference", "${env:MCPPROXY_OAUTH_KEY_SOURCE}", true},
		{"keyring reference", "${keyring:mcpproxy-oauth-key}", true},
	}
	for _, tc := range cases {
		for _, reveal := range []bool{false, true} {
			name := tc.name + "/reveal=off"
			if reveal {
				name = tc.name + "/reveal=on"
			}
			t.Run(name, func(t *testing.T) {
				stored := config.DefaultConfig()
				stored.OAuthTokenEncryptionKey = tc.value

				var published *config.Config
				if reveal {
					published = WithMaskedOAuthTokenKey(stored)
				} else {
					published = RedactedConfig(stored)
				}
				require.NotNil(t, published)
				if tc.reference {
					assert.Equal(t, tc.value, published.OAuthTokenEncryptionKey)
				} else {
					assert.NotEmpty(t, published.OAuthTokenEncryptionKey)
					assert.NotEqual(t, tc.value, published.OAuthTokenEncryptionKey)
					assert.NotContains(t, published.OAuthTokenEncryptionKey, tc.value[4:40])
				}
				assert.Equal(t, tc.value, stored.OAuthTokenEncryptionKey, "the source is not mutated")

				// Write path: echoing what the read door published restores the
				// stored value (never persists the mask).
				wire := redactedWire(t, stored)
				if reveal {
					wire["oauth_token_encryption_key"] = published.OAuthTokenEncryptionKey
				}
				resolved, err := UnmaskLiveConfigDocument(wire, stored)
				require.NoError(t, err)
				assert.Equal(t, tc.value, resolved.OAuthTokenEncryptionKey)
			})
		}
	}
}
