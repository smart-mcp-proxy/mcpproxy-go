//go:build server

package config

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServerEditionConfig_StoreIDPTokensDefaultsFalse(t *testing.T) {
	// Default config: privacy-preserving default (FR-006).
	cfg := DefaultServerEditionConfig()
	assert.False(t, cfg.StoreIDPTokens)

	// Absent from JSON => false.
	var parsed ServerEditionConfig
	require.NoError(t, json.Unmarshal([]byte(`{"enabled":false}`), &parsed))
	assert.False(t, parsed.StoreIDPTokens)
}

func TestServerEditionConfig_StoreIDPTokensParsed(t *testing.T) {
	var parsed ServerEditionConfig
	require.NoError(t, json.Unmarshal([]byte(`{"store_idp_tokens":true}`), &parsed))
	assert.True(t, parsed.StoreIDPTokens)
}

func TestServerEditionConfig_CredentialEncryptionKeyParsed(t *testing.T) {
	var parsed ServerEditionConfig
	require.NoError(t, json.Unmarshal([]byte(`{"credential_encryption_key":"abc123"}`), &parsed))
	assert.Equal(t, "abc123", parsed.CredentialEncryptionKey)
}

func TestServerEditionConfig_CredentialEncryptionKeyEnvFallback(t *testing.T) {
	t.Setenv("MCPPROXY_CRED_KEY", "from-env-key")
	cfg := &ServerEditionConfig{
		Enabled:     true,
		AdminEmails: []string{"admin@example.com"},
		OAuth: &ServerEditionOAuthConfig{
			Provider:     "google",
			ClientID:     "cid",
			ClientSecret: "csec",
		},
	}
	require.NoError(t, cfg.Validate())
	assert.Equal(t, "from-env-key", cfg.CredentialEncryptionKey, "env MCPPROXY_CRED_KEY should fill an empty key")
}

func TestServerEditionConfig_CredentialEncryptionKeyConfigWins(t *testing.T) {
	t.Setenv("MCPPROXY_CRED_KEY", "from-env-key")
	cfg := &ServerEditionConfig{
		Enabled:                 true,
		AdminEmails:             []string{"admin@example.com"},
		CredentialEncryptionKey: "from-config",
		OAuth: &ServerEditionOAuthConfig{
			Provider:     "google",
			ClientID:     "cid",
			ClientSecret: "csec",
		},
	}
	require.NoError(t, cfg.Validate())
	assert.Equal(t, "from-config", cfg.CredentialEncryptionKey, "explicit config key should win over env")
}
