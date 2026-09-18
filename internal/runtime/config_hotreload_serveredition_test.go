//go:build server

package runtime

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Spec 107 FR-039 part 2 (T055/T056): DetectConfigChanges reports the
// `server_edition` block. Every key that setup.go binds at construction (the
// login handler, the session store, the credential store) is restart-pinned
// and reported as the single field `server_edition` with a reason;
// `admin_emails` is read live through ServerEditionConfigProvider and is
// reported as `server_edition.admin_emails` without a restart.
//
// Single-key edits only: the detector keeps its early-return shape for
// listen/routing_mode/data_dir/api_key/tls/timeouts, so a mixed edit that
// touches one of those reports THAT key alone (see the last subtest) — the
// restart re-reads everything, which is why the existing keys do the same.

func serverEditionBase() *config.Config {
	return &config.Config{
		Listen: "127.0.0.1:8080", DataDir: "/d", TLS: &config.TLSConfig{},
		ServerEdition: &config.ServerEditionConfig{
			Enabled:     true,
			AdminEmails: []string{"admin@example.com"},
			OAuth: &config.ServerEditionOAuthConfig{
				Provider:            "oidc",
				ClientID:            "client-one",
				ClientSecret:        "secret-one",
				AllowedDomains:      []string{"example.com"},
				IssuerURL:           "https://idp.example.com",
				Scopes:              []string{"openid", "profile", "email"},
				GroupsClaim:         "groups",
				EmailVerifiedPolicy: config.EmailVerifiedPolicyRefuseFalse,
				DisplayName:         "Example SSO",
			},
			SessionTTL:              config.Duration(24 * time.Hour),
			BearerTokenTTL:          config.Duration(24 * time.Hour),
			CredentialEncryptionKey: "0123456789abcdef0123456789abcdef",
			PublicURL:               "https://mcp.example.com",
			SessionCookieSecure:     config.SessionCookieSecureAuto,
		},
	}
}

func TestDetectConfigChanges_ServerEditionRestartPinnedKeys(t *testing.T) {
	// One case per restart-pinned key of contracts/config-keys.md. `reason`
	// is the substring the RestartReason must carry so the operator can tell
	// WHICH part of the block pinned the restart.
	cases := []struct {
		name   string
		mutate func(se *config.ServerEditionConfig)
		reason string
	}{
		{"enabled", func(se *config.ServerEditionConfig) { se.Enabled = false }, "server_edition.enabled requires a restart"},
		{"oauth.provider", func(se *config.ServerEditionConfig) { se.OAuth.Provider = "google" }, "server_edition.oauth.* is bound at login handler construction"},
		{"oauth.client_id", func(se *config.ServerEditionConfig) { se.OAuth.ClientID = "client-two" }, "server_edition.oauth.*"},
		{"oauth.client_secret", func(se *config.ServerEditionConfig) { se.OAuth.ClientSecret = "secret-two" }, "server_edition.oauth.*"},
		{"oauth.tenant_id", func(se *config.ServerEditionConfig) { se.OAuth.TenantID = "tenant" }, "server_edition.oauth.*"},
		{"oauth.allowed_domains", func(se *config.ServerEditionConfig) { se.OAuth.AllowedDomains = []string{"other.example"} }, "server_edition.oauth.*"},
		{"oauth.issuer_url", func(se *config.ServerEditionConfig) { se.OAuth.IssuerURL = "https://idp2.example.com" }, "server_edition.oauth.*"},
		{"oauth.allow_insecure_issuer", func(se *config.ServerEditionConfig) { se.OAuth.AllowInsecureIssuer = true }, "server_edition.oauth.*"},
		{"oauth.scopes", func(se *config.ServerEditionConfig) { se.OAuth.Scopes = []string{"openid", "email"} }, "server_edition.oauth.*"},
		{"oauth.groups_claim", func(se *config.ServerEditionConfig) { se.OAuth.GroupsClaim = "roles" }, "server_edition.oauth.*"},
		{"oauth.email_verified_policy", func(se *config.ServerEditionConfig) { se.OAuth.EmailVerifiedPolicy = config.EmailVerifiedPolicyIgnore }, "server_edition.oauth.*"},
		{"oauth.display_name", func(se *config.ServerEditionConfig) { se.OAuth.DisplayName = "Other SSO" }, "server_edition.oauth.*"},
		{"oauth removed", func(se *config.ServerEditionConfig) { se.OAuth = nil }, "server_edition.oauth.*"},
		{"public_url", func(se *config.ServerEditionConfig) { se.PublicURL = "https://mcp2.example.com" }, "server_edition.public_url is used at login handler construction"},
		{"session_cookie_secure", func(se *config.ServerEditionConfig) { se.SessionCookieSecure = config.SessionCookieSecureTrue }, "server_edition.session_cookie_secure"},
		{"session_ttl", func(se *config.ServerEditionConfig) { se.SessionTTL = config.Duration(time.Hour) }, "server_edition.session_ttl"},
		{"bearer_token_ttl", func(se *config.ServerEditionConfig) { se.BearerTokenTTL = config.Duration(time.Hour) }, "server_edition.bearer_token_ttl"},
		// Bound at setup (setup.go NewBBoltAESStore(ResolveMasterKey(...))).
		{"credential_encryption_key", func(se *config.ServerEditionConfig) { se.CredentialEncryptionKey = "fedcba9876543210fedcba9876543210" }, "server_edition.credential_encryption_key"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			oldCfg, newCfg := serverEditionBase(), serverEditionBase()
			tc.mutate(newCfg.ServerEdition)

			result := DetectConfigChanges(oldCfg, newCfg)
			require.True(t, result.Success)
			assert.Equal(t, []string{"server_edition"}, result.ChangedFields)
			assert.True(t, result.RequiresRestart, "%s is bound at setup and must be restart-pinned", tc.name)
			assert.False(t, result.AppliedImmediately)
			assert.Contains(t, result.RestartReason, tc.reason)
			// Never the secret itself, whichever key moved.
			assert.NotContains(t, result.RestartReason, "secret-two")
			assert.NotContains(t, result.RestartReason, "fedcba9876543210")
		})
	}

	// Adding or removing the whole block moves admin_emails too, so both
	// fields are reported: the restart-pinned group and the live key.
	t.Run("block added", func(t *testing.T) {
		oldCfg := serverEditionBase()
		oldCfg.ServerEdition = nil
		result := DetectConfigChanges(oldCfg, serverEditionBase())
		assert.ElementsMatch(t, []string{"server_edition", "server_edition.admin_emails"}, result.ChangedFields)
		assert.True(t, result.RequiresRestart)
		assert.Contains(t, result.RestartReason, "server_edition.enabled requires a restart")
	})

	t.Run("block removed", func(t *testing.T) {
		newCfg := serverEditionBase()
		newCfg.ServerEdition = nil
		result := DetectConfigChanges(serverEditionBase(), newCfg)
		assert.ElementsMatch(t, []string{"server_edition", "server_edition.admin_emails"}, result.ChangedFields)
		assert.True(t, result.RequiresRestart)
	})
}

func TestDetectConfigChanges_ServerEditionAdminEmailsLive(t *testing.T) {
	t.Run("admin_emails edit is live", func(t *testing.T) {
		newCfg := serverEditionBase()
		newCfg.ServerEdition.AdminEmails = []string{"admin@example.com", "second@example.com"}

		result := DetectConfigChanges(serverEditionBase(), newCfg)
		require.True(t, result.Success)
		assert.Equal(t, []string{"server_edition.admin_emails"}, result.ChangedFields)
		assert.False(t, result.RequiresRestart, "admin_emails is read live through ServerEditionConfigProvider")
		assert.True(t, result.AppliedImmediately)
		assert.Empty(t, result.RestartReason)
	})

	t.Run("nil vs empty admin_emails not reported", func(t *testing.T) {
		oldCfg, newCfg := serverEditionBase(), serverEditionBase()
		oldCfg.ServerEdition.AdminEmails = nil
		newCfg.ServerEdition.AdminEmails = []string{}
		result := DetectConfigChanges(oldCfg, newCfg)
		assert.NotContains(t, result.ChangedFields, "server_edition.admin_emails")
		assert.NotContains(t, result.ChangedFields, "server_edition")
	})
}

func TestDetectConfigChanges_ServerEditionNoFalsePositives(t *testing.T) {
	t.Run("unchanged block not reported", func(t *testing.T) {
		result := DetectConfigChanges(serverEditionBase(), serverEditionBase())
		assert.NotContains(t, result.ChangedFields, "server_edition")
		assert.NotContains(t, result.ChangedFields, "server_edition.admin_emails")
		assert.False(t, result.RequiresRestart)
	})

	t.Run("PATCH round-trip not reported", func(t *testing.T) {
		// PATCH /api/v1/config marshals the live config and decodes the merged
		// document; omitempty collapses empty slices, which is why the clause
		// compares JSON rather than reflect.DeepEqual.
		oldCfg := serverEditionBase()
		oldCfg.ServerEdition.OAuth.AllowedDomains = []string{}
		oldCfg.ServerEdition.OAuth.Scopes = []string{}
		raw, err := json.Marshal(oldCfg)
		require.NoError(t, err)
		var newCfg config.Config
		require.NoError(t, json.Unmarshal(raw, &newCfg))

		result := DetectConfigChanges(oldCfg, &newCfg)
		assert.NotContains(t, result.ChangedFields, "server_edition")
		assert.NotContains(t, result.ChangedFields, "server_edition.admin_emails")
		assert.False(t, result.RequiresRestart)
	})

	t.Run("absent block equals empty block", func(t *testing.T) {
		oldCfg, newCfg := serverEditionBase(), serverEditionBase()
		oldCfg.ServerEdition = nil
		newCfg.ServerEdition = &config.ServerEditionConfig{}
		result := DetectConfigChanges(oldCfg, newCfg)
		assert.Empty(t, result.ChangedFields)
		assert.False(t, result.RequiresRestart)
	})

	t.Run("deprecated store_idp_tokens is never reported", func(t *testing.T) {
		// contracts/config-keys.md: retained decoder, no-op, "—" for
		// live/restart. A toggle must not pin a restart.
		newCfg := serverEditionBase()
		newCfg.ServerEdition.StoreIDPTokens = true
		result := DetectConfigChanges(serverEditionBase(), newCfg)
		assert.NotContains(t, result.ChangedFields, "server_edition")
		assert.False(t, result.RequiresRestart)
	})

	t.Run("mixed edit with an early-return key reports that key alone", func(t *testing.T) {
		// Documents the existing early-return shape: listen (and
		// routing_mode/data_dir/api_key/tls/timeouts) return immediately with
		// only that field; the restart re-reads the whole file, block included.
		newCfg := serverEditionBase()
		newCfg.Listen = "127.0.0.1:9090"
		newCfg.ServerEdition.OAuth.ClientID = "client-two"
		result := DetectConfigChanges(serverEditionBase(), newCfg)
		assert.Equal(t, []string{"listen"}, result.ChangedFields)
		assert.True(t, result.RequiresRestart)
	})

	t.Run("restart-pinned key beside a live key reports both", func(t *testing.T) {
		newCfg := serverEditionBase()
		newCfg.ServerEdition.AdminEmails = []string{"other@example.com"}
		newCfg.ServerEdition.PublicURL = "https://mcp2.example.com"
		result := DetectConfigChanges(serverEditionBase(), newCfg)
		assert.ElementsMatch(t, []string{"server_edition", "server_edition.admin_emails"}, result.ChangedFields)
		assert.True(t, result.RequiresRestart)
	})
}
