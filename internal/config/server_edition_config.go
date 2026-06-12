//go:build server

package config

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// ServerEditionConfig holds configuration for the server edition multi-user features.
type ServerEditionConfig struct {
	Enabled              bool                      `json:"enabled" mapstructure:"enabled"`
	AdminEmails          []string                  `json:"admin_emails" mapstructure:"admin-emails"`
	OAuth                *ServerEditionOAuthConfig `json:"oauth,omitempty" mapstructure:"oauth"`
	SessionTTL           Duration                  `json:"session_ttl,omitempty" mapstructure:"session-ttl"`
	BearerTokenTTL       Duration                  `json:"bearer_token_ttl,omitempty" mapstructure:"bearer-token-ttl"`
	WorkspaceIdleTimeout Duration                  `json:"workspace_idle_timeout,omitempty" mapstructure:"workspace-idle-timeout"`
	MaxUserServers       int                       `json:"max_user_servers,omitempty" mapstructure:"max-user-servers"`

	// CredentialEncryptionKey encrypts per-user upstream credentials at rest
	// (spec 074). When empty, it falls back to the MCPPROXY_CRED_KEY env var.
	CredentialEncryptionKey string `json:"credential_encryption_key,omitempty" mapstructure:"credential-encryption-key"`
	// StoreIDPTokens controls whether caller IdP subject tokens are persisted.
	// Privacy-preserving default: false (FR-006).
	StoreIDPTokens bool `json:"store_idp_tokens" mapstructure:"store-idp-tokens"`
}

// ServerEditionOAuthConfig holds OAuth identity provider configuration for the server edition.
type ServerEditionOAuthConfig struct {
	Provider       string   `json:"provider" mapstructure:"provider"` // "google", "github", "microsoft"
	ClientID       string   `json:"client_id" mapstructure:"client-id"`
	ClientSecret   string   `json:"client_secret" mapstructure:"client-secret"`
	TenantID       string   `json:"tenant_id,omitempty" mapstructure:"tenant-id"` // Microsoft only
	AllowedDomains []string `json:"allowed_domains,omitempty" mapstructure:"allowed-domains"`
}

// DefaultServerEditionConfig returns a ServerEditionConfig with sensible defaults.
func DefaultServerEditionConfig() *ServerEditionConfig {
	return &ServerEditionConfig{
		Enabled:              false,
		SessionTTL:           Duration(24 * time.Hour),
		BearerTokenTTL:       Duration(24 * time.Hour),
		WorkspaceIdleTimeout: Duration(30 * time.Minute),
		MaxUserServers:       20,
	}
}

// IsAdminEmail checks if the given email is in the admin list (case-insensitive).
func (c *ServerEditionConfig) IsAdminEmail(email string) bool {
	for _, admin := range c.AdminEmails {
		if strings.EqualFold(admin, email) {
			return true
		}
	}
	return false
}

// Validate checks that the ServerEditionConfig is valid for operation.
func (c *ServerEditionConfig) Validate() error {
	if !c.Enabled {
		return nil // disabled, no validation needed
	}
	// Spec 074: fall back to MCPPROXY_CRED_KEY when no explicit key is set.
	// An explicit config value always wins over the environment.
	if c.CredentialEncryptionKey == "" {
		c.CredentialEncryptionKey = os.Getenv("MCPPROXY_CRED_KEY")
	}
	if len(c.AdminEmails) == 0 {
		return fmt.Errorf("server_edition.admin_emails must contain at least one admin email")
	}
	if c.OAuth == nil {
		return fmt.Errorf("server_edition.oauth configuration is required when server_edition is enabled")
	}
	validProviders := map[string]bool{"google": true, "github": true, "microsoft": true}
	if !validProviders[c.OAuth.Provider] {
		return fmt.Errorf("server_edition.oauth.provider must be one of: google, github, microsoft (got: %s)", c.OAuth.Provider)
	}
	if c.OAuth.ClientID == "" {
		return fmt.Errorf("server_edition.oauth.client_id is required")
	}
	if c.OAuth.ClientSecret == "" {
		return fmt.Errorf("server_edition.oauth.client_secret is required")
	}
	if c.OAuth.Provider == "microsoft" && c.OAuth.TenantID == "" {
		// Default to "common" for multi-tenant
		c.OAuth.TenantID = "common"
	}
	if c.SessionTTL.Duration() <= 0 {
		c.SessionTTL = Duration(24 * time.Hour)
	}
	if c.BearerTokenTTL.Duration() <= 0 {
		c.BearerTokenTTL = Duration(24 * time.Hour)
	}
	if c.WorkspaceIdleTimeout.Duration() <= 0 {
		c.WorkspaceIdleTimeout = Duration(30 * time.Minute)
	}
	if c.MaxUserServers <= 0 {
		c.MaxUserServers = 20
	}
	return nil
}
