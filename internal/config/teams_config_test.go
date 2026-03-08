//go:build teams

package config

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTeamsDefaultTeamsConfig(t *testing.T) {
	cfg := DefaultTeamsConfig()

	assert.False(t, cfg.Enabled, "teams should be disabled by default")
	assert.Empty(t, cfg.AdminEmails, "admin emails should be empty by default")
	assert.Nil(t, cfg.OAuth, "OAuth config should be nil by default")
	assert.Equal(t, Duration(24*time.Hour), cfg.SessionTTL, "session TTL should default to 24h")
	assert.Equal(t, Duration(24*time.Hour), cfg.BearerTokenTTL, "bearer token TTL should default to 24h")
	assert.Equal(t, Duration(30*time.Minute), cfg.WorkspaceIdleTimeout, "workspace idle timeout should default to 30m")
	assert.Equal(t, 20, cfg.MaxUserServers, "max user servers should default to 20")
}

func TestTeamsIsAdminEmail(t *testing.T) {
	cfg := &TeamsConfig{
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
	cfg := &TeamsConfig{AdminEmails: nil}
	assert.False(t, cfg.IsAdminEmail("anyone@example.com"))

	cfg2 := &TeamsConfig{AdminEmails: []string{}}
	assert.False(t, cfg2.IsAdminEmail("anyone@example.com"))
}

func TestTeamsValidate_DisabledSkipsValidation(t *testing.T) {
	cfg := &TeamsConfig{Enabled: false}
	err := cfg.Validate()
	assert.NoError(t, err, "disabled teams config should pass validation")
}

func TestTeamsValidate_MissingAdminEmails(t *testing.T) {
	cfg := &TeamsConfig{
		Enabled:     true,
		AdminEmails: nil,
	}
	err := cfg.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "admin_emails")
}

func TestTeamsValidate_MissingOAuth(t *testing.T) {
	cfg := &TeamsConfig{
		Enabled:     true,
		AdminEmails: []string{"admin@example.com"},
		OAuth:       nil,
	}
	err := cfg.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "oauth configuration is required")
}

func TestTeamsValidate_InvalidProvider(t *testing.T) {
	cfg := &TeamsConfig{
		Enabled:     true,
		AdminEmails: []string{"admin@example.com"},
		OAuth: &TeamsOAuthConfig{
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
	cfg := &TeamsConfig{
		Enabled:     true,
		AdminEmails: []string{"admin@example.com"},
		OAuth: &TeamsOAuthConfig{
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
	cfg := &TeamsConfig{
		Enabled:     true,
		AdminEmails: []string{"admin@example.com"},
		OAuth: &TeamsOAuthConfig{
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
	cfg := &TeamsConfig{
		Enabled:     true,
		AdminEmails: []string{"admin@example.com"},
		OAuth: &TeamsOAuthConfig{
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
	cfg := &TeamsConfig{
		Enabled:     true,
		AdminEmails: []string{"admin@example.com"},
		OAuth: &TeamsOAuthConfig{
			Provider:     "github",
			ClientID:     "Iv1.abc123",
			ClientSecret: "secret123",
		},
	}
	err := cfg.Validate()
	assert.NoError(t, err)
}

func TestTeamsValidate_MicrosoftDefaultsTenantID(t *testing.T) {
	cfg := &TeamsConfig{
		Enabled:     true,
		AdminEmails: []string{"admin@example.com"},
		OAuth: &TeamsOAuthConfig{
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
	cfg := &TeamsConfig{
		Enabled:     true,
		AdminEmails: []string{"admin@example.com"},
		OAuth: &TeamsOAuthConfig{
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
	cfg := &TeamsConfig{
		Enabled:     true,
		AdminEmails: []string{"admin@example.com"},
		OAuth: &TeamsOAuthConfig{
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
			cfg := &TeamsConfig{
				Enabled:     true,
				AdminEmails: []string{"admin@example.com"},
				OAuth: &TeamsOAuthConfig{
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

func TestTeamsConfig_JSONRoundTrip(t *testing.T) {
	original := &TeamsConfig{
		Enabled:     true,
		AdminEmails: []string{"admin@example.com", "boss@corp.io"},
		OAuth: &TeamsOAuthConfig{
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

	var restored TeamsConfig
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

func TestTeamsConfig_JSONRoundTrip_MicrosoftWithTenant(t *testing.T) {
	original := &TeamsConfig{
		Enabled:     true,
		AdminEmails: []string{"admin@contoso.com"},
		OAuth: &TeamsOAuthConfig{
			Provider:       "microsoft",
			ClientID:       "my-client-id",
			ClientSecret:   "my-secret",
			TenantID:       "contoso.onmicrosoft.com",
			AllowedDomains: []string{"contoso.com"},
		},
	}

	data, err := json.Marshal(original)
	require.NoError(t, err)

	var restored TeamsConfig
	err = json.Unmarshal(data, &restored)
	require.NoError(t, err)

	assert.Equal(t, "microsoft", restored.OAuth.Provider)
	assert.Equal(t, "contoso.onmicrosoft.com", restored.OAuth.TenantID)
}

func TestTeamsConfig_JSONRoundTrip_MinimalDisabled(t *testing.T) {
	original := &TeamsConfig{Enabled: false}

	data, err := json.Marshal(original)
	require.NoError(t, err)

	var restored TeamsConfig
	err = json.Unmarshal(data, &restored)
	require.NoError(t, err)

	assert.False(t, restored.Enabled)
	assert.Nil(t, restored.OAuth)
	assert.Empty(t, restored.AdminEmails)
}

func TestTeamsConfig_EmbeddedInConfig(t *testing.T) {
	cfg := &Config{
		Listen: "127.0.0.1:8080",
		Teams: &TeamsConfig{
			Enabled:     true,
			AdminEmails: []string{"admin@example.com"},
			OAuth: &TeamsOAuthConfig{
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

	require.NotNil(t, restored.Teams)
	assert.True(t, restored.Teams.Enabled)
	assert.Equal(t, []string{"admin@example.com"}, restored.Teams.AdminEmails)
	assert.Equal(t, "google", restored.Teams.OAuth.Provider)
	assert.Equal(t, Duration(12*time.Hour), restored.Teams.SessionTTL)
	assert.Equal(t, 30, restored.Teams.MaxUserServers)
}

func TestTeamsConfig_OmittedFromConfig(t *testing.T) {
	cfg := &Config{
		Listen: "127.0.0.1:8080",
		// Teams is nil
	}

	data, err := json.Marshal(cfg)
	require.NoError(t, err)

	// Verify "teams" key is not present in JSON output
	var raw map[string]interface{}
	err = json.Unmarshal(data, &raw)
	require.NoError(t, err)
	_, hasTeams := raw["teams"]
	assert.False(t, hasTeams, "nil Teams should be omitted from JSON")
}

func TestTeamsConfig_UnmarshalFromJSON(t *testing.T) {
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
			"session_ttl": "4h",
			"bearer_token_ttl": "30m",
			"workspace_idle_timeout": "10m",
			"max_user_servers": 5
		}
	}`

	var cfg Config
	err := json.Unmarshal([]byte(jsonStr), &cfg)
	require.NoError(t, err)

	require.NotNil(t, cfg.Teams)
	assert.True(t, cfg.Teams.Enabled)
	assert.Equal(t, "github", cfg.Teams.OAuth.Provider)
	assert.Equal(t, "Iv1.abc", cfg.Teams.OAuth.ClientID)
	assert.Equal(t, Duration(4*time.Hour), cfg.Teams.SessionTTL)
	assert.Equal(t, Duration(30*time.Minute), cfg.Teams.BearerTokenTTL)
	assert.Equal(t, Duration(10*time.Minute), cfg.Teams.WorkspaceIdleTimeout)
	assert.Equal(t, 5, cfg.Teams.MaxUserServers)
}
