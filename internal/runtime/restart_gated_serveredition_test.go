//go:build server

package runtime

import (
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPinRestartGatedCoversServerEdition is the regression test for
// cross-review round 1, chunk 3 P1: pinRestartGated did not pin ServerEdition
// at all, so a config apply that only reported "requires restart" for
// server_edition.enabled (and the rest of the restart-pinned subset — oauth.*,
// public_url, session_cookie_secure, session_ttl, bearer_token_ttl,
// credential_encryption_key — Spec 107 FR-039 part 2) actually adopted the new
// value into the running config immediately. The most severe instance:
// disabling server_edition.enabled restored anonymous /mcp access at once
// (config.EffectiveRequireMCPAuth reads it live) while the operator was told a
// restart was still needed.
//
// Mirrors TestPinRestartGatedCoversEveryRestartGatedField's technique (driven
// off the detector itself) for the one field the edition-neutral test above
// cannot reach — ServerEditionConfig only exists with real fields under the
// server build.
func TestPinRestartGatedCoversServerEdition(t *testing.T) {
	base := func() *config.Config {
		c := config.DefaultConfig()
		c.ServerEdition = &config.ServerEditionConfig{
			Enabled:     true,
			AdminEmails: []string{"admin@example.com"},
			OAuth:       &config.ServerEditionOAuthConfig{Provider: "google", ClientID: "cid", ClientSecret: "secret"},
		}
		return c
	}

	mutations := map[string]func(*config.Config){
		"enabled": func(c *config.Config) { c.ServerEdition.Enabled = false },
		"public_url": func(c *config.Config) {
			c.ServerEdition.PublicURL = "https://mcpproxy.example.com"
		},
		"oauth_provider": func(c *config.Config) { c.ServerEdition.OAuth.Provider = "oidc" },
	}

	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			live := base()
			desired := base() // independent structs — no shared pointers
			mutate(desired)

			require.True(t, DetectConfigChanges(live, desired).RequiresRestart,
				"test premise: server_edition.%s must be restart-gated", name)

			pinned := pinRestartGated(live, desired)
			result := DetectConfigChanges(live, pinned)
			assert.False(t, result.RequiresRestart,
				"pinRestartGated missed server_edition.%s: it would be adopted in memory (e.g. flipping EffectiveRequireMCPAuth) while the API reports it pending", name)
			assert.Equal(t, live.ServerEdition.Enabled, pinned.ServerEdition.Enabled,
				"enabled must come from live, never from desired, until restart")
		})
	}
}

// TestPinRestartGatedKeepsServerEditionAdminEmailsHot proves the flip side:
// admin_emails is deliberately NOT pinned (it is read live through
// ServerEditionConfigProvider, #1169), so a write that only touches
// admin_emails must not be forced to live's stale list.
func TestPinRestartGatedKeepsServerEditionAdminEmailsHot(t *testing.T) {
	live := &config.Config{ServerEdition: &config.ServerEditionConfig{
		Enabled:     true,
		AdminEmails: []string{"old@example.com"},
		OAuth:       &config.ServerEditionOAuthConfig{Provider: "google", ClientID: "cid", ClientSecret: "secret"},
	}}
	desired := &config.Config{ServerEdition: &config.ServerEditionConfig{
		Enabled:     true,
		AdminEmails: []string{"new@example.com"},
		OAuth:       &config.ServerEditionOAuthConfig{Provider: "google", ClientID: "cid", ClientSecret: "secret"},
	}}

	pinned := pinRestartGated(live, desired)
	require.NotNil(t, pinned.ServerEdition)
	assert.Equal(t, []string{"new@example.com"}, pinned.ServerEdition.AdminEmails,
		"admin_emails must survive pinning: it is the one hot field of the block (#1169)")
}
