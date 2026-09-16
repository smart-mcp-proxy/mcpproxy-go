//go:build server

package config

import (
	"fmt"
	"net"
	"net/url"
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
//
// Spec 107 FR-020 adds the generic `oidc` provider (OpenID Connect Discovery +
// a verified ID token) beside the three legacy providers. The six keys below
// `AllowedDomains` are `oidc` concerns (contracts/config-keys.md); the legacy
// providers ignore them.
type ServerEditionOAuthConfig struct {
	Provider       string   `json:"provider" mapstructure:"provider"` // "google", "github", "microsoft", "oidc"
	ClientID       string   `json:"client_id" mapstructure:"client-id"`
	ClientSecret   string   `json:"client_secret" mapstructure:"client-secret"`
	TenantID       string   `json:"tenant_id,omitempty" mapstructure:"tenant-id"` // Microsoft only
	AllowedDomains []string `json:"allowed_domains,omitempty" mapstructure:"allowed-domains"`

	// IssuerURL is the OpenID Provider issuer (required for `oidc`). Discovery
	// reads `<issuer_url>/.well-known/openid-configuration` lazily on the first
	// login, and the ID token's `iss` must equal it byte for byte. It must be
	// https, or http only for a loopback host with AllowInsecureIssuer.
	IssuerURL string `json:"issuer_url,omitempty" mapstructure:"issuer-url"`
	// AllowInsecureIssuer admits a plain-http issuer (and plain-http discovered
	// endpoints) when — and only when — the host is loopback. A development
	// toggle for the in-process fake IdP; non-loopback http is refused
	// regardless.
	AllowInsecureIssuer bool `json:"allow_insecure_issuer,omitempty" mapstructure:"allow-insecure-issuer"`
	// Scopes requested on the authorization request (`oidc`). Default
	// ["openid","profile","email"]; ApplyDefaults appends "openid" if missing.
	Scopes []string `json:"scopes,omitempty" mapstructure:"scopes"`
	// GroupsClaim names the ID-token / userinfo claim carrying the user's
	// groups (`oidc`). Default "groups".
	GroupsClaim string `json:"groups_claim,omitempty" mapstructure:"groups-claim"`
	// EmailVerifiedPolicy decides what an `email_verified` claim of false or
	// absent does to an `oidc` login: refuse_false (default: refuse only an
	// explicit false), require_true (refuse false and absent) or ignore.
	EmailVerifiedPolicy string `json:"email_verified_policy,omitempty" mapstructure:"email-verified-policy"`
	// DisplayName is the login-button label (<= 64 chars); falls back to the
	// provider family name when empty.
	DisplayName string `json:"display_name,omitempty" mapstructure:"display-name"`
}

// Email-verified policies (server_edition.oauth.email_verified_policy).
const (
	EmailVerifiedPolicyRefuseFalse = "refuse_false"
	EmailVerifiedPolicyRequireTrue = "require_true"
	EmailVerifiedPolicyIgnore      = "ignore"
)

// OIDC defaults applied by ApplyDefaults for provider "oidc".
const (
	defaultOIDCGroupsClaim = "groups"
	maxOAuthDisplayNameLen = 64
)

// defaultOIDCScopes is the scope set requested when `scopes` is unset.
func defaultOIDCScopes() []string { return []string{"openid", "profile", "email"} }

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
	if c.OAuth != nil && c.OAuth.Provider == "oidc" {
		c.OAuth.applyOIDCDefaults()
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
	validProviders := map[string]bool{"google": true, "github": true, "microsoft": true, "oidc": true}
	if !validProviders[c.OAuth.Provider] {
		return fmt.Errorf("server_edition.oauth.provider must be one of: google, github, microsoft, oidc (got: %s)", c.OAuth.Provider)
	}
	if c.OAuth.ClientID == "" {
		return fmt.Errorf("server_edition.oauth.client_id is required")
	}
	if c.OAuth.ClientSecret == "" {
		return fmt.Errorf("server_edition.oauth.client_secret is required")
	}
	if err := c.OAuth.validateOIDC(); err != nil {
		return err
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
		if c.OAuth.Scopes != nil {
			oauth.Scopes = append([]string(nil), c.OAuth.Scopes...)
		}
		out.OAuth = &oauth
	}
	return &out
}

// applyOIDCDefaults fills the `oidc` defaults: scopes (with "openid" appended
// when the operator's list lacks it), groups_claim and email_verified_policy.
func (o *ServerEditionOAuthConfig) applyOIDCDefaults() {
	if len(o.Scopes) == 0 {
		o.Scopes = defaultOIDCScopes()
	} else if !containsFold(o.Scopes, "openid") {
		o.Scopes = append(append([]string(nil), o.Scopes...), "openid")
	}
	if o.GroupsClaim == "" {
		o.GroupsClaim = defaultOIDCGroupsClaim
	}
	if o.EmailVerifiedPolicy == "" {
		o.EmailVerifiedPolicy = EmailVerifiedPolicyRefuseFalse
	}
}

// validateOIDC holds the `oidc`-specific and provider-neutral rules of the new
// keys (FR-020). Non-mutating: unset defaulted keys are admitted (FR-039).
func (o *ServerEditionOAuthConfig) validateOIDC() error {
	if len(o.DisplayName) > maxOAuthDisplayNameLen {
		return fmt.Errorf("server_edition.oauth.display_name must be at most %d characters", maxOAuthDisplayNameLen)
	}
	switch o.EmailVerifiedPolicy {
	case "", EmailVerifiedPolicyRefuseFalse, EmailVerifiedPolicyRequireTrue, EmailVerifiedPolicyIgnore:
	default:
		return fmt.Errorf("server_edition.oauth.email_verified_policy must be one of: refuse_false, require_true, ignore")
	}
	if o.Provider != "oidc" {
		return nil
	}
	if o.IssuerURL == "" {
		return fmt.Errorf("server_edition.oauth.issuer_url is required when provider is oidc")
	}
	if !IsAllowedOIDCEndpoint(o.IssuerURL, o.AllowInsecureIssuer) {
		return fmt.Errorf("server_edition.oauth.issuer_url must use https (http is allowed only for a loopback host with allow_insecure_issuer: true)")
	}
	return nil
}

// IsAllowedOIDCEndpoint reports whether raw is an absolute https URL, or an
// absolute http URL whose host is loopback while allowInsecure is set. The
// same rule gates the configured issuer and every discovered endpoint
// (FR-020): non-loopback http is never admitted, flag or no flag.
func IsAllowedOIDCEndpoint(raw string, allowInsecure bool) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Hostname() == "" {
		return false
	}
	switch u.Scheme {
	case "https":
		return true
	case "http":
		return allowInsecure && isLoopbackHost(u.Hostname())
	default:
		return false
	}
}

// isLoopbackHost reports whether host is "localhost" or a loopback IP literal.
func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func containsFold(list []string, want string) bool {
	for _, s := range list {
		if strings.EqualFold(s, want) {
			return true
		}
	}
	return false
}
