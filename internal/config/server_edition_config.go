//go:build server

package config

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// ServerEditionConfig holds configuration for the server edition multi-user features.
//
// Spec 107 FR-032 removed the never-enforced `max_user_servers` and
// `workspace_idle_timeout` knobs. A config file that still carries them loads
// (the server-build normaliser drops them from the raw document and records a
// LoadDiagnostic per key); the write doors refuse them (ValidateRemovedKeys).
type ServerEditionConfig struct {
	Enabled        bool                      `json:"enabled" mapstructure:"enabled"`
	AdminEmails    []string                  `json:"admin_emails" mapstructure:"admin-emails"`
	OAuth          *ServerEditionOAuthConfig `json:"oauth,omitempty" mapstructure:"oauth"`
	SessionTTL     Duration                  `json:"session_ttl,omitempty" mapstructure:"session-ttl"`
	BearerTokenTTL Duration                  `json:"bearer_token_ttl,omitempty" mapstructure:"bearer-token-ttl"`

	// CredentialEncryptionKey encrypts per-user upstream credentials at rest
	// (spec 074). When empty, ApplyDefaults falls back to the MCPPROXY_CRED_KEY
	// env var.
	CredentialEncryptionKey string `json:"credential_encryption_key,omitempty" mapstructure:"credential-encryption-key"`
	// StoreIDPTokens is a deprecated no-op retained so pre-107 configs keep
	// loading (Spec 107 FR-033). IdP tokens are no longer persisted at login;
	// `true` records one deprecation LoadDiagnostic at load time.
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

// defaultServerEditionTTL is the default for session_ttl and bearer_token_ttl.
const defaultServerEditionTTL = Duration(24 * time.Hour)

// DefaultServerEditionConfig returns a ServerEditionConfig with sensible defaults.
func DefaultServerEditionConfig() *ServerEditionConfig {
	return &ServerEditionConfig{
		Enabled:        false,
		SessionTTL:     defaultServerEditionTTL,
		BearerTokenTTL: defaultServerEditionTTL,
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

// ApplyDefaults fills the derived values a running server edition needs: the
// TTLs, the Microsoft multi-tenant "common" tenant, and the MCPPROXY_CRED_KEY
// fallback for credential_encryption_key (an explicit config value always wins
// over the environment). It is the boot-time companion of Validate (Spec 107
// FR-039): setup calls ApplyDefaults then Validate on a Clone of the live
// block — never on the runtime's own pointer, which is the PATCH merge base
// and the next write-back — while the write doors call only Validate, so
// nothing derived is ever persisted into the config file.
func (c *ServerEditionConfig) ApplyDefaults() {
	if c == nil {
		return
	}
	if c.CredentialEncryptionKey == "" {
		c.CredentialEncryptionKey = os.Getenv("MCPPROXY_CRED_KEY")
	}
	if c.OAuth != nil && c.OAuth.Provider == "microsoft" && c.OAuth.TenantID == "" {
		c.OAuth.TenantID = "common"
	}
	if c.SessionTTL.Duration() <= 0 {
		c.SessionTTL = defaultServerEditionTTL
	}
	if c.BearerTokenTTL.Duration() <= 0 {
		c.BearerTokenTTL = defaultServerEditionTTL
	}
}

// Validate checks that the ServerEditionConfig is valid for operation. It is
// non-mutating: unset TTLs, an unset Microsoft tenant and an unset encryption
// key are defaulted by ApplyDefaults, never refused here, so the same rules
// apply at boot, on PATCH /api/v1/config and on /config/apply (FR-039).
func (c *ServerEditionConfig) Validate() error {
	if c == nil || !c.Enabled {
		return nil // disabled, no validation needed
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
	if c.SessionTTL.Duration() < 0 {
		return fmt.Errorf("server_edition.session_ttl must be positive")
	}
	if c.BearerTokenTTL.Duration() < 0 {
		return fmt.Errorf("server_edition.bearer_token_ttl must be positive")
	}
	return nil
}

// Clone returns a deep copy of the block (nil-safe).
func (c *ServerEditionConfig) Clone() *ServerEditionConfig {
	if c == nil {
		return nil
	}
	out := *c
	if c.AdminEmails != nil {
		out.AdminEmails = append([]string(nil), c.AdminEmails...)
	}
	if c.OAuth != nil {
		oauth := *c.OAuth
		if c.OAuth.AllowedDomains != nil {
			oauth.AllowedDomains = append([]string(nil), c.OAuth.AllowedDomains...)
		}
		out.OAuth = &oauth
	}
	return &out
}
