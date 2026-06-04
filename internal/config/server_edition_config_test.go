//go:build server

package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTeamsDefaultServerEditionConfig(t *testing.T) {
	cfg := DefaultServerEditionConfig()

	assert.False(t, cfg.Enabled, "teams should be disabled by default")
	assert.Empty(t, cfg.AdminEmails, "admin emails should be empty by default")
	assert.Nil(t, cfg.OAuth, "OAuth config should be nil by default")
	assert.Equal(t, Duration(24*time.Hour), cfg.SessionTTL, "session TTL should default to 24h")
	assert.Equal(t, Duration(24*time.Hour), cfg.BearerTokenTTL, "bearer token TTL should default to 24h")
	assert.Equal(t, Duration(30*time.Minute), cfg.WorkspaceIdleTimeout, "workspace idle timeout should default to 30m")
	assert.Equal(t, 20, cfg.MaxUserServers, "max user servers should default to 20")
}

func TestTeamsIsAdminEmail(t *testing.T) {
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

func TestTeamsIsAdminEmail_EmptyList(t *testing.T) {
	cfg := &ServerEditionConfig{AdminEmails: nil}
	assert.False(t, cfg.IsAdminEmail("anyone@example.com"))

	cfg2 := &ServerEditionConfig{AdminEmails: []string{}}
	assert.False(t, cfg2.IsAdminEmail("anyone@example.com"))
}

func TestTeamsValidate_DisabledSkipsValidation(t *testing.T) {
	cfg := &ServerEditionConfig{Enabled: false}
	err := cfg.Validate()
	assert.NoError(t, err, "disabled teams config should pass validation")
}

func TestTeamsValidate_MissingAdminEmails(t *testing.T) {
	cfg := &ServerEditionConfig{
		Enabled:     true,
		AdminEmails: nil,
	}
	err := cfg.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "admin_emails")
}

func TestTeamsValidate_MissingOAuth(t *testing.T) {
	cfg := &ServerEditionConfig{
		Enabled:     true,
		AdminEmails: []string{"admin@example.com"},
		OAuth:       nil,
	}
	err := cfg.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "oauth configuration is required")
}

func TestTeamsValidate_InvalidProvider(t *testing.T) {
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

func TestTeamsValidate_MissingClientID(t *testing.T) {
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

func TestTeamsValidate_MissingClientSecret(t *testing.T) {
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

func TestTeamsValidate_ValidGoogleConfig(t *testing.T) {
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

func TestTeamsValidate_ValidGitHubConfig(t *testing.T) {
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

func TestTeamsValidate_MicrosoftDefaultsTenantID(t *testing.T) {
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

func TestTeamsValidate_MicrosoftExplicitTenantID(t *testing.T) {
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

func TestTeamsValidate_DefaultsAppliedForZeroValues(t *testing.T) {
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

func TestTeamsValidate_AllProviders(t *testing.T) {
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

// writeServerEditionConfigFile writes a config JSON to a temp file and returns
// its path. Each test gets an isolated data_dir so LoadFromFile does not touch
// the real ~/.mcpproxy.
func writeServerEditionConfigFile(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp_config.json")
	content := `{
		"listen": "127.0.0.1:8080",
		"data_dir": "` + dir + `",
		` + body + `
	}`
	require.NoError(t, os.WriteFile(path, []byte(content), 0600))
	return path
}

// TestLoadFromFile_ServerEditionKey verifies the new canonical "server_edition"
// key loads onto Config.ServerEdition (MCP-1086).
func TestLoadFromFile_ServerEditionKey(t *testing.T) {
	path := writeServerEditionConfigFile(t, `"server_edition": {
		"enabled": true,
		"admin_emails": ["new@example.com"],
		"oauth": {"provider": "google", "client_id": "id", "client_secret": "secret"}
	}`)

	cfg, err := LoadFromFile(path)
	require.NoError(t, err)
	require.NotNil(t, cfg.ServerEdition)
	assert.True(t, cfg.ServerEdition.Enabled)
	assert.Equal(t, []string{"new@example.com"}, cfg.ServerEdition.AdminEmails)
	assert.Equal(t, "google", cfg.ServerEdition.OAuth.Provider)
}

// TestLoadFromFile_LegacyTeamsKey verifies a config that still uses the legacy
// "teams" key is normalized onto Config.ServerEdition on load (back-compat,
// MCP-1086).
func TestLoadFromFile_LegacyTeamsKey(t *testing.T) {
	path := writeServerEditionConfigFile(t, `"teams": {
		"enabled": true,
		"admin_emails": ["legacy@example.com"],
		"oauth": {"provider": "github", "client_id": "Iv1.abc", "client_secret": "ghp_x"}
	}`)

	cfg, err := LoadFromFile(path)
	require.NoError(t, err)
	require.NotNil(t, cfg.ServerEdition, "legacy 'teams' key must load onto ServerEdition")
	assert.True(t, cfg.ServerEdition.Enabled)
	assert.Equal(t, []string{"legacy@example.com"}, cfg.ServerEdition.AdminEmails)
	assert.Equal(t, "github", cfg.ServerEdition.OAuth.Provider)
}

// TestLoadFromFile_BothKeysNewWins verifies that when both keys are present the
// new "server_edition" key takes precedence over the legacy "teams" key.
func TestLoadFromFile_BothKeysNewWins(t *testing.T) {
	path := writeServerEditionConfigFile(t, `"server_edition": {
		"enabled": true,
		"admin_emails": ["new@example.com"],
		"oauth": {"provider": "google", "client_id": "id", "client_secret": "secret"}
	},
	"teams": {
		"enabled": true,
		"admin_emails": ["legacy@example.com"],
		"oauth": {"provider": "github", "client_id": "Iv1.abc", "client_secret": "ghp_x"}
	}`)

	cfg, err := LoadFromFile(path)
	require.NoError(t, err)
	require.NotNil(t, cfg.ServerEdition)
	assert.Equal(t, []string{"new@example.com"}, cfg.ServerEdition.AdminEmails,
		"new server_edition key must win over legacy teams key")
	assert.Equal(t, "google", cfg.ServerEdition.OAuth.Provider)
}
