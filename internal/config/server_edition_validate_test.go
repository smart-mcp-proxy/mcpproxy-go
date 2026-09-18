//go:build server

package config

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Spec 107 FR-039 part 1 (T011 / T019): Config.Validate() and
// ValidateDetailed() must reach a NON-mutating ServerEditionConfig.Validate
// under the server build, so PATCH /api/v1/config and /config/apply refuse a
// broken block at write time instead of persisting it for the next restart
// (critic G4: today the only caller is setup.go:96 and it mutates).
//
// Defaults (TTLs, Microsoft TenantID) and the MCPPROXY_CRED_KEY fallback move
// to a separate ApplyDefaults() step that setup runs before Validate().

// minimalServerEditionConfig returns a Config that passes Config.Validate()
// on its own, carrying the given server_edition block.
func minimalServerEditionConfig(se *ServerEditionConfig) *Config {
	return &Config{
		Listen:            "127.0.0.1:8080",
		ToolsLimit:        15,
		ToolResponseLimit: 1000,
		CallToolTimeout:   Duration(time.Minute),
		Servers:           []*ServerConfig{},
		ServerEdition:     se,
	}
}

func bogusProviderBlock() *ServerEditionConfig {
	return &ServerEditionConfig{
		Enabled:     true,
		AdminEmails: []string{"admin@example.com"},
		OAuth: &ServerEditionOAuthConfig{
			Provider:     "bogus",
			ClientID:     "cid",
			ClientSecret: "csec",
		},
	}
}

// TestConfigValidate_ReachesServerEditionValidate: an enabled block with
// provider "bogus" fails at Config.Validate. Today it passes because
// Config.Validate never looks at c.ServerEdition (behaviour-red).
func TestConfigValidate_ReachesServerEditionValidate(t *testing.T) {
	cfg := minimalServerEditionConfig(bogusProviderBlock())

	err := cfg.Validate()
	require.Error(t, err, "Config.Validate must refuse an enabled server_edition block with provider=bogus")
	assert.Contains(t, err.Error(), "server_edition.oauth.provider",
		"the refusal must name the offending key so PATCH/apply callers see the same message as boot")
}

// TestConfigValidateDetailed_ReachesServerEditionValidate: the same block is
// reported by ValidateDetailed() (the write-surface path used by PATCH and
// /config/apply), not only by the boot-compatible Validate().
func TestConfigValidateDetailed_ReachesServerEditionValidate(t *testing.T) {
	cfg := minimalServerEditionConfig(bogusProviderBlock())

	errs := cfg.ValidateDetailed()
	require.NotEmpty(t, errs, "ValidateDetailed must report the invalid server_edition.oauth.provider")

	found := false
	for _, ve := range errs {
		if strings.Contains(ve.Error(), "server_edition.oauth.provider") {
			found = true
			break
		}
	}
	assert.True(t, found, "expected a ValidationError naming server_edition.oauth.provider, got %v", errs)
}

// A nil block and a disabled block keep passing: the bridge must not refuse
// the common (personal-like) case.
func TestConfigValidate_ServerEditionNilOrDisabledPasses(t *testing.T) {
	require.NoError(t, minimalServerEditionConfig(nil).Validate(), "nil server_edition block must validate")

	disabled := bogusProviderBlock()
	disabled.Enabled = false
	require.NoError(t, minimalServerEditionConfig(disabled).Validate(),
		"a disabled block is not validated (no rule applies when server_edition.enabled=false)")
	assert.Empty(t, minimalServerEditionConfig(disabled).ValidateDetailed())
}

// TestServerEditionConfig_ValidateIsNonMutating: Validate() leaves TenantID,
// the TTLs and CredentialEncryptionKey exactly as it found them. Today it
// fills all three (server_edition_config.go: CRED_KEY fallback, "common"
// tenant, 24h TTLs), which is the bug that lets a write door persist derived
// values (behaviour-red).
func TestServerEditionConfig_ValidateIsNonMutating(t *testing.T) {
	t.Setenv("MCPPROXY_CRED_KEY", "from-env-key")

	cfg := &ServerEditionConfig{
		Enabled:     true,
		AdminEmails: []string{"admin@example.com"},
		OAuth: &ServerEditionOAuthConfig{
			Provider:     "microsoft",
			ClientID:     "cid",
			ClientSecret: "csec",
			// TenantID deliberately empty: today Validate writes "common".
		},
		// TTLs deliberately zero (unset): today Validate writes 24h.
		// CredentialEncryptionKey deliberately empty: today Validate copies
		// MCPPROXY_CRED_KEY into it.
	}

	require.NoError(t, cfg.Validate(), "an otherwise valid block must validate; unset values are defaulted by ApplyDefaults, not refused")

	assert.Equal(t, "", cfg.OAuth.TenantID, "Validate must not fill TenantID")
	assert.Equal(t, Duration(0), cfg.SessionTTL, "Validate must not fill SessionTTL")
	assert.Equal(t, Duration(0), cfg.BearerTokenTTL, "Validate must not fill BearerTokenTTL")
	assert.Equal(t, "", cfg.CredentialEncryptionKey, "Validate must not apply the MCPPROXY_CRED_KEY fallback")
}

// The same guarantee through the Config-level bridge: a valid block passes
// Config.Validate()/ValidateDetailed() untouched. This is the path a PATCH
// takes before persisting, so nothing derived may leak into the saved file.
func TestConfigValidate_DoesNotMutateServerEditionBlock(t *testing.T) {
	t.Setenv("MCPPROXY_CRED_KEY", "from-env-key")

	se := &ServerEditionConfig{
		Enabled:     true,
		AdminEmails: []string{"admin@example.com"},
		OAuth: &ServerEditionOAuthConfig{
			Provider:     "microsoft",
			ClientID:     "cid",
			ClientSecret: "csec",
		},
	}
	cfg := minimalServerEditionConfig(se)

	require.NoError(t, cfg.Validate())
	assert.Empty(t, cfg.ValidateDetailed())

	assert.Equal(t, "", se.OAuth.TenantID)
	assert.Equal(t, Duration(0), se.SessionTTL)
	assert.Equal(t, Duration(0), se.BearerTokenTTL)
	assert.Equal(t, "", se.CredentialEncryptionKey)
}

// TestServerEditionConfig_ApplyDefaultsFills: ApplyDefaults() is where the
// TTLs, the Microsoft "common" tenant and the MCPPROXY_CRED_KEY fallback now
// land (compile-red until T019 adds ApplyDefaults).
func TestServerEditionConfig_ApplyDefaultsFills(t *testing.T) {
	t.Setenv("MCPPROXY_CRED_KEY", "from-env-key")

	cfg := &ServerEditionConfig{
		Enabled:     true,
		AdminEmails: []string{"admin@example.com"},
		OAuth: &ServerEditionOAuthConfig{
			Provider:     "microsoft",
			ClientID:     "cid",
			ClientSecret: "csec",
		},
	}

	cfg.ApplyDefaults()

	assert.Equal(t, "common", cfg.OAuth.TenantID, "ApplyDefaults fills the Microsoft multi-tenant default")
	assert.Equal(t, Duration(24*time.Hour), cfg.SessionTTL, "ApplyDefaults fills session_ttl")
	assert.Equal(t, Duration(24*time.Hour), cfg.BearerTokenTTL, "ApplyDefaults fills bearer_token_ttl")
	assert.Equal(t, "from-env-key", cfg.CredentialEncryptionKey, "ApplyDefaults applies the MCPPROXY_CRED_KEY fallback")

	// The filled block validates, still without mutation.
	require.NoError(t, cfg.Validate())
	assert.Equal(t, "common", cfg.OAuth.TenantID)
	assert.Equal(t, Duration(24*time.Hour), cfg.SessionTTL)
	assert.Equal(t, "from-env-key", cfg.CredentialEncryptionKey)
}

// Explicit values survive ApplyDefaults: config wins over env for the key,
// and set TTLs/tenant are left alone.
func TestServerEditionConfig_ApplyDefaultsKeepsExplicitValues(t *testing.T) {
	t.Setenv("MCPPROXY_CRED_KEY", "from-env-key")

	cfg := &ServerEditionConfig{
		Enabled:                 true,
		AdminEmails:             []string{"admin@example.com"},
		SessionTTL:              Duration(2 * time.Hour),
		BearerTokenTTL:          Duration(3 * time.Hour),
		CredentialEncryptionKey: "from-config",
		OAuth: &ServerEditionOAuthConfig{
			Provider:     "microsoft",
			ClientID:     "cid",
			ClientSecret: "csec",
			TenantID:     "contoso.onmicrosoft.com",
		},
	}

	cfg.ApplyDefaults()

	assert.Equal(t, "contoso.onmicrosoft.com", cfg.OAuth.TenantID)
	assert.Equal(t, Duration(2*time.Hour), cfg.SessionTTL)
	assert.Equal(t, Duration(3*time.Hour), cfg.BearerTokenTTL)
	assert.Equal(t, "from-config", cfg.CredentialEncryptionKey, "explicit config key wins over MCPPROXY_CRED_KEY")
}
