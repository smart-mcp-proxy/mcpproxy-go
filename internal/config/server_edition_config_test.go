//go:build server

package config

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServerEditionDefaultServerEditionConfig(t *testing.T) {
	cfg := DefaultServerEditionConfig()

	assert.False(t, cfg.Enabled, "teams should be disabled by default")
	assert.Empty(t, cfg.AdminEmails, "admin emails should be empty by default")
	assert.Nil(t, cfg.OAuth, "OAuth config should be nil by default")
	assert.Equal(t, Duration(24*time.Hour), cfg.SessionTTL, "session TTL should default to 24h")
	assert.Equal(t, Duration(24*time.Hour), cfg.BearerTokenTTL, "bearer token TTL should default to 24h")
	assert.Equal(t, Duration(30*time.Minute), cfg.WorkspaceIdleTimeout, "workspace idle timeout should default to 30m")
	assert.Equal(t, 20, cfg.MaxUserServers, "max user servers should default to 20")
}

func TestServerEditionIsAdminEmail(t *testing.T) {
	cfg := &ServerEditionConfig{
		AdminEmails: []string{"admin@example.com", "Boss@Corp.io"},
	}

	tests := []struct {
		name  string
		email string
		want  bool
	}{
		{name: "exact match", email: "admin@example.com", want: true},
		{name: "case insensitive match", email: "ADMIN@EXAMPLE.COM", want: true},
		{name: "mixed case match", email: "Admin@Example.Com", want: true},
		{name: "second admin exact", email: "Boss@Corp.io", want: true},
		{name: "second admin lowercase", email: "boss@corp.io", want: true},
		{name: "not an admin", email: "user@example.com", want: false},
		{name: "empty email", email: "", want: false},
		{name: "partial match is not enough", email: "admin@example", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cfg.IsAdminEmail(tt.email)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestServerEditionIsAdminEmail_EmptyList(t *testing.T) {
	cfg := &ServerEditionConfig{AdminEmails: nil}
	assert.False(t, cfg.IsAdminEmail("anyone@example.com"))

	cfg2 := &ServerEditionConfig{AdminEmails: []string{}}
	assert.False(t, cfg2.IsAdminEmail("anyone@example.com"))
}

func TestServerEditionValidate_DisabledSkipsValidation(t *testing.T) {
	cfg := &ServerEditionConfig{Enabled: false}
	err := cfg.Validate()
	assert.NoError(t, err, "disabled teams config should pass validation")
}

func TestServerEditionValidate_MissingAdminEmails(t *testing.T) {
	cfg := &ServerEditionConfig{
		Enabled:     true,
		AdminEmails: nil,
	}
	err := cfg.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "admin_emails")
}

func TestServerEditionValidate_MissingOAuth(t *testing.T) {
	cfg := &ServerEditionConfig{
		Enabled:     true,
		AdminEmails: []string{"admin@example.com"},
		OAuth:       nil,
	}
	err := cfg.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "oauth configuration is required")
}

func TestServerEditionValidate_InvalidProvider(t *testing.T) {
	cfg := &ServerEditionConfig{
		Enabled:     true,
		AdminEmails: []string{"admin@example.com"},
		OAuth: &ServerEditionOAuthConfig{
			Provider:     "facebook",
			ClientID:     "id",
			ClientSecret: "secret",
		},
	}
	err := cfg.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "provider must be one of")
	assert.Contains(t, err.Error(), "facebook")
}

func TestServerEditionValidate_MissingClientID(t *testing.T) {
	cfg := &ServerEditionConfig{
		Enabled:     true,
		AdminEmails: []string{"admin@example.com"},
		OAuth: &ServerEditionOAuthConfig{
			Provider:     "google",
			ClientID:     "",
			ClientSecret: "secret",
		},
	}
	err := cfg.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "client_id is required")
}

func TestServerEditionValidate_MissingClientSecret(t *testing.T) {
	cfg := &ServerEditionConfig{
		Enabled:     true,
		AdminEmails: []string{"admin@example.com"},
		OAuth: &ServerEditionOAuthConfig{
			Provider:     "google",
			ClientID:     "my-client-id",
			ClientSecret: "",
		},
	}
	err := cfg.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "client_secret is required")
}

func TestServerEditionValidate_ValidGoogleConfig(t *testing.T) {
	cfg := &ServerEditionConfig{
		Enabled:     true,
		AdminEmails: []string{"admin@example.com"},
		OAuth: &ServerEditionOAuthConfig{
			Provider:     "google",
			ClientID:     "my-client-id.apps.googleusercontent.com",
			ClientSecret: "GOCSPX-secret",
		},
		SessionTTL:           Duration(8 * time.Hour),
		BearerTokenTTL:       Duration(1 * time.Hour),
		WorkspaceIdleTimeout: Duration(15 * time.Minute),
		MaxUserServers:       10,
	}
	err := cfg.Validate()
	assert.NoError(t, err)
}

func TestServerEditionValidate_ValidGitHubConfig(t *testing.T) {
	cfg := &ServerEditionConfig{
		Enabled:     true,
		AdminEmails: []string{"admin@example.com"},
		OAuth: &ServerEditionOAuthConfig{
			Provider:     "github",
			ClientID:     "Iv1.abc123",
			ClientSecret: "secret123",
		},
	}
	err := cfg.Validate()
	assert.NoError(t, err)
}

func TestServerEditionValidate_MicrosoftDefaultsTenantID(t *testing.T) {
	cfg := &ServerEditionConfig{
		Enabled:     true,
		AdminEmails: []string{"admin@example.com"},
		OAuth: &ServerEditionOAuthConfig{
			Provider:     "microsoft",
			ClientID:     "my-client-id",
			ClientSecret: "my-client-secret",
			TenantID:     "", // empty should default to "common"
		},
	}
	err := cfg.Validate()
	assert.NoError(t, err)
	assert.Equal(t, "common", cfg.OAuth.TenantID, "Microsoft tenant ID should default to 'common'")
}

func TestServerEditionValidate_MicrosoftExplicitTenantID(t *testing.T) {
	cfg := &ServerEditionConfig{
		Enabled:     true,
		AdminEmails: []string{"admin@example.com"},
		OAuth: &ServerEditionOAuthConfig{
			Provider:     "microsoft",
			ClientID:     "my-client-id",
			ClientSecret: "my-client-secret",
			TenantID:     "my-tenant-id",
		},
	}
	err := cfg.Validate()
	assert.NoError(t, err)
	assert.Equal(t, "my-tenant-id", cfg.OAuth.TenantID, "explicit tenant ID should be preserved")
}

func TestServerEditionValidate_DefaultsAppliedForZeroValues(t *testing.T) {
	cfg := &ServerEditionConfig{
		Enabled:     true,
		AdminEmails: []string{"admin@example.com"},
		OAuth: &ServerEditionOAuthConfig{
			Provider:     "google",
			ClientID:     "id",
			ClientSecret: "secret",
		},
		// All duration/limit fields left at zero
	}
	err := cfg.Validate()
	assert.NoError(t, err)
	assert.Equal(t, Duration(24*time.Hour), cfg.SessionTTL, "zero SessionTTL should default to 24h")
	assert.Equal(t, Duration(24*time.Hour), cfg.BearerTokenTTL, "zero BearerTokenTTL should default to 24h")
	assert.Equal(t, Duration(30*time.Minute), cfg.WorkspaceIdleTimeout, "zero WorkspaceIdleTimeout should default to 30m")
	assert.Equal(t, 20, cfg.MaxUserServers, "zero MaxUserServers should default to 20")
}

func TestServerEditionValidate_AllProviders(t *testing.T) {
	providers := []string{"google", "github", "microsoft"}
	for _, provider := range providers {
		t.Run(provider, func(t *testing.T) {
			cfg := &ServerEditionConfig{
				Enabled:     true,
				AdminEmails: []string{"admin@example.com"},
				OAuth: &ServerEditionOAuthConfig{
					Provider:     provider,
					ClientID:     "id",
					ClientSecret: "secret",
				},
			}
			err := cfg.Validate()
			assert.NoError(t, err, "provider %s should be valid", provider)
		})
	}
}

func TestServerEditionConfig_JSONRoundTrip(t *testing.T) {
	original := &ServerEditionConfig{
		Enabled:     true,
		AdminEmails: []string{"admin@example.com", "boss@corp.io"},
		OAuth: &ServerEditionOAuthConfig{
			Provider:       "google",
			ClientID:       "my-client-id.apps.googleusercontent.com",
			ClientSecret:   "GOCSPX-secret",
			AllowedDomains: []string{"example.com", "corp.io"},
		},
		SessionTTL:           Duration(8 * time.Hour),
		BearerTokenTTL:       Duration(1 * time.Hour),
		WorkspaceIdleTimeout: Duration(15 * time.Minute),
		MaxUserServers:       50,
	}

	data, err := json.Marshal(original)
	require.NoError(t, err)

	var restored ServerEditionConfig
	err = json.Unmarshal(data, &restored)
	require.NoError(t, err)

	assert.Equal(t, original.Enabled, restored.Enabled)
	assert.Equal(t, original.AdminEmails, restored.AdminEmails)
	require.NotNil(t, restored.OAuth)
	assert.Equal(t, original.OAuth.Provider, restored.OAuth.Provider)
	assert.Equal(t, original.OAuth.ClientID, restored.OAuth.ClientID)
	assert.Equal(t, original.OAuth.ClientSecret, restored.OAuth.ClientSecret)
	assert.Equal(t, original.OAuth.AllowedDomains, restored.OAuth.AllowedDomains)
	assert.Equal(t, original.SessionTTL, restored.SessionTTL)
	assert.Equal(t, original.BearerTokenTTL, restored.BearerTokenTTL)
	assert.Equal(t, original.WorkspaceIdleTimeout, restored.WorkspaceIdleTimeout)
	assert.Equal(t, original.MaxUserServers, restored.MaxUserServers)
}

func TestServerEditionConfig_JSONRoundTrip_MicrosoftWithTenant(t *testing.T) {
	original := &ServerEditionConfig{
		Enabled:     true,
		AdminEmails: []string{"admin@contoso.com"},
		OAuth: &ServerEditionOAuthConfig{
			Provider:       "microsoft",
			ClientID:       "my-client-id",
			ClientSecret:   "my-secret",
			TenantID:       "contoso.onmicrosoft.com",
			AllowedDomains: []string{"contoso.com"},
		},
	}

	data, err := json.Marshal(original)
	require.NoError(t, err)

	var restored ServerEditionConfig
	err = json.Unmarshal(data, &restored)
	require.NoError(t, err)

	assert.Equal(t, "microsoft", restored.OAuth.Provider)
	assert.Equal(t, "contoso.onmicrosoft.com", restored.OAuth.TenantID)
}

func TestServerEditionConfig_JSONRoundTrip_MinimalDisabled(t *testing.T) {
	original := &ServerEditionConfig{Enabled: false}

	data, err := json.Marshal(original)
	require.NoError(t, err)

	var restored ServerEditionConfig
	err = json.Unmarshal(data, &restored)
	require.NoError(t, err)

	assert.False(t, restored.Enabled)
	assert.Nil(t, restored.OAuth)
	assert.Empty(t, restored.AdminEmails)
}

func TestServerEditionConfig_EmbeddedInConfig(t *testing.T) {
	cfg := &Config{
		Listen: "127.0.0.1:8080",
		ServerEdition: &ServerEditionConfig{
			Enabled:     true,
			AdminEmails: []string{"admin@example.com"},
			OAuth: &ServerEditionOAuthConfig{
				Provider:     "google",
				ClientID:     "id",
				ClientSecret: "secret",
			},
			SessionTTL:     Duration(12 * time.Hour),
			MaxUserServers: 30,
		},
	}

	data, err := json.Marshal(cfg)
	require.NoError(t, err)

	var restored Config
	err = json.Unmarshal(data, &restored)
	require.NoError(t, err)

	require.NotNil(t, restored.ServerEdition)
	assert.True(t, restored.ServerEdition.Enabled)
	assert.Equal(t, []string{"admin@example.com"}, restored.ServerEdition.AdminEmails)
	assert.Equal(t, "google", restored.ServerEdition.OAuth.Provider)
	assert.Equal(t, Duration(12*time.Hour), restored.ServerEdition.SessionTTL)
	assert.Equal(t, 30, restored.ServerEdition.MaxUserServers)
}

// TestServerEditionConfig_LegacyTeamsKeyAlias verifies the backward-compat
// alias (MCP-1085): a config that still uses the old top-level "teams" key is
// normalized on read into ServerEdition so existing deployments keep working.
func TestServerEditionConfig_LegacyTeamsKeyAlias(t *testing.T) {
	jsonStr := `{
		"listen": "0.0.0.0:8080",
		"teams": {
			"enabled": true,
			"admin_emails": ["admin@example.com"],
			"oauth": {
				"provider": "github",
				"client_id": "Iv1.abc",
				"client_secret": "ghp_secret"
			},
			"max_user_servers": 5
		}
	}`

	var cfg Config
	err := json.Unmarshal([]byte(jsonStr), &cfg)
	require.NoError(t, err)

	require.NotNil(t, cfg.ServerEdition, "legacy teams key should populate ServerEdition")
	assert.True(t, cfg.ServerEdition.Enabled)
	assert.Equal(t, []string{"admin@example.com"}, cfg.ServerEdition.AdminEmails)
	require.NotNil(t, cfg.ServerEdition.OAuth)
	assert.Equal(t, "github", cfg.ServerEdition.OAuth.Provider)
	assert.Equal(t, 5, cfg.ServerEdition.MaxUserServers)
}

// TestServerEditionConfig_NewKeyWinsOverLegacy verifies that if both the new
// "server_edition" key and the legacy "teams" key are present, the new key is
// authoritative and the legacy one is ignored.
func TestServerEditionConfig_NewKeyWinsOverLegacy(t *testing.T) {
	jsonStr := `{
		"teams": { "enabled": false, "max_user_servers": 1 },
		"server_edition": { "enabled": true, "max_user_servers": 9 }
	}`

	var cfg Config
	err := json.Unmarshal([]byte(jsonStr), &cfg)
	require.NoError(t, err)

	require.NotNil(t, cfg.ServerEdition)
	assert.True(t, cfg.ServerEdition.Enabled, "server_edition key must take precedence over teams")
	assert.Equal(t, 9, cfg.ServerEdition.MaxUserServers)
}

// TestServerEditionConfig_WritesNewKey verifies SaveConfig/Marshal always emits
// the canonical "server_edition" key, never the legacy "teams" key.
func TestServerEditionConfig_WritesNewKey(t *testing.T) {
	cfg := &Config{
		ServerEdition: &ServerEditionConfig{Enabled: true, MaxUserServers: 7},
	}
	data, err := json.Marshal(cfg)
	require.NoError(t, err)

	var raw map[string]interface{}
	require.NoError(t, json.Unmarshal(data, &raw))
	_, hasNew := raw["server_edition"]
	assert.True(t, hasNew, "config must be written with the server_edition key")
	_, hasLegacy := raw["teams"]
	assert.False(t, hasLegacy, "config must never be written with the legacy teams key")
}

func TestServerEditionConfig_OmittedFromConfig(t *testing.T) {
	cfg := &Config{
		Listen: "127.0.0.1:8080",
		// ServerEdition is nil
	}

	data, err := json.Marshal(cfg)
	require.NoError(t, err)

	// Verify "server_edition" key is not present in JSON output
	var raw map[string]interface{}
	err = json.Unmarshal(data, &raw)
	require.NoError(t, err)
	_, hasServerEdition := raw["server_edition"]
	assert.False(t, hasServerEdition, "nil ServerEdition should be omitted from JSON")
	// And the legacy "teams" key must never be emitted either.
	_, hasLegacy := raw["teams"]
	assert.False(t, hasLegacy, "legacy teams key must never be written")
}

func TestServerEditionConfig_UnmarshalFromJSON(t *testing.T) {
	jsonStr := `{
		"listen": "0.0.0.0:8080",
		"server_edition": {
			"enabled": true,
			"admin_emails": ["admin@example.com"],
			"oauth": {
				"provider": "github",
				"client_id": "Iv1.abc",
				"client_secret": "ghp_secret"
			},
			"session_ttl": "4h",
			"bearer_token_ttl": "30m",
			"workspace_idle_timeout": "10m",
			"max_user_servers": 5
		}
	}`

	var cfg Config
	err := json.Unmarshal([]byte(jsonStr), &cfg)
	require.NoError(t, err)

	require.NotNil(t, cfg.ServerEdition)
	assert.True(t, cfg.ServerEdition.Enabled)
	assert.Equal(t, "github", cfg.ServerEdition.OAuth.Provider)
	assert.Equal(t, "Iv1.abc", cfg.ServerEdition.OAuth.ClientID)
	assert.Equal(t, Duration(4*time.Hour), cfg.ServerEdition.SessionTTL)
	assert.Equal(t, Duration(30*time.Minute), cfg.ServerEdition.BearerTokenTTL)
	assert.Equal(t, Duration(10*time.Minute), cfg.ServerEdition.WorkspaceIdleTimeout)
	assert.Equal(t, 5, cfg.ServerEdition.MaxUserServers)
}
