package oauth

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Spec 123 T033 (FR-024): GET /api/v1/config serves RedactedConfig. A literal
// oauth_token_encryption_key must be masked there, and the raw-JSON editor's
// GET -> /config/apply round trip of the masked document must leave the stored
// key unchanged (the write twin reverts the mask instead of persisting it).

const spec123LiteralKey = "q83vEjRWeJCrze8SNFZ4kKvN7xI0VniQq83vEjRWeJA="

func redactedWire(t *testing.T, cfg *config.Config) map[string]interface{} {
	t.Helper()
	masked := RedactedConfig(cfg)
	require.NotNil(t, masked)
	raw, err := json.Marshal(masked)
	require.NoError(t, err)
	var wire map[string]interface{}
	require.NoError(t, json.Unmarshal(raw, &wire))
	return wire
}

func TestRedactedConfig_MasksLiteralOAuthTokenKey(t *testing.T) {
	stored := config.DefaultConfig()
	stored.OAuthTokenEncryptionKey = spec123LiteralKey

	wire := redactedWire(t, stored)
	got, _ := wire["oauth_token_encryption_key"].(string)
	require.NotEmpty(t, got, "the field is present, masked")
	assert.NotEqual(t, spec123LiteralKey, got)
	raw, _ := json.Marshal(wire)
	assert.NotContains(t, string(raw), spec123LiteralKey)
	assert.NotContains(t, string(raw), spec123LiteralKey[4:40], "no substantial fragment of the key")
	assert.Equal(t, spec123LiteralKey, stored.OAuthTokenEncryptionKey, "the source is not mutated")

	resolved, err := UnmaskLiveConfigDocument(wire, stored)
	require.NoError(t, err)
	assert.Equal(t, spec123LiteralKey, resolved.OAuthTokenEncryptionKey,
		"the mask must be reverted, never persisted over the key")
}

func TestRedactedConfig_OAuthTokenKeyReferenceRoundTripsVerbatim(t *testing.T) {
	const ref = "${file:/run/credentials/mcpproxy.service/oauth-key}"
	stored := config.DefaultConfig()
	stored.OAuthTokenEncryptionKey = ref

	wire := redactedWire(t, stored)
	resolved, err := UnmaskLiveConfigDocument(wire, stored)
	require.NoError(t, err)
	assert.Equal(t, ref, resolved.OAuthTokenEncryptionKey)
}
