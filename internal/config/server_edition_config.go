//go:build server

package config

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
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

	// PublicURL is the absolute origin (scheme://host[:port], no path) the
	// deployment is reached at (Spec 107 FR-025). When set it is the sole
	// source of the OAuth callback URL and the connect-flow base URL; Host and
	// X-Forwarded-* are then ignored. Env alias: MCPPROXY_PUBLIC_URL.
	PublicURL string `json:"public_url,omitempty" mapstructure:"public-url"`
	// SessionCookieSecure is the Secure-attribute policy of the session cookie
	// (Spec 107 FR-026): "auto" (default — https public_url, in-process TLS or
	// a trusted X-Forwarded-Proto: https), "true" or "false".
	SessionCookieSecure string `json:"session_cookie_secure,omitempty" mapstructure:"session-cookie-secure"`

	// Access is the IdP-group → server grant map (Spec 107 FR-007, US1). Absent
	// = today's Shared-only semantics; present = the map is ACTIVE and a tenant
	// sees a shared server only through a group grant (or default_servers).
	// Read live through ServerEditionConfigProvider on every entitlement
	// decision, so it is hot-reloadable (FR-039 part 3) and never restart-pinned.
	Access *ServerEditionAccessConfig `json:"access,omitempty" mapstructure:"access"`
}

// ServerEditionAccessConfig is `server_edition.access` (Spec 107 FR-007,
// contracts/config-keys.md): the group map that turns "onboarding is adding
// someone to a group" into server entitlement.
//
//	group_servers   group value (compared exactly, case-sensitive) → admin-config
//	                server names, or "*" = every shared server. Non-empty only
//	                with oauth.provider "oidc", the one provider that yields groups.
//	default_servers the grant for a user whose stored groups match no key;
//	                absent, null and [] all mean "no default grant".
//
// With the block present there is no silent allow-all: "*" is the only way to
// grant everything, and a user in no mapped group with no default grant is
// entitled to no shared server at all (deny-all, FR-006).
type ServerEditionAccessConfig struct {
	GroupServers   map[string][]string `json:"group_servers,omitempty" mapstructure:"group-servers"`
	DefaultServers []string            `json:"default_servers,omitempty" mapstructure:"default-servers"`
}

// Validation message fixed by contracts/config-keys.md for the oidc-only rule.
const msgAccessGroupServersOIDCOnly = `server_edition.access.group_servers requires oauth.provider "oidc" (legacy providers yield no groups)`

// AccessWildcard is the group-map entry that expands to every shared server.
const AccessWildcard = "*"

// Clone returns a deep copy of the access block (nil-safe).
func (a *ServerEditionAccessConfig) Clone() *ServerEditionAccessConfig {
	if a == nil {
		return nil
	}
	out := &ServerEditionAccessConfig{}
	if a.GroupServers != nil {
		out.GroupServers = make(map[string][]string, len(a.GroupServers))
		for g, names := range a.GroupServers {
			out.GroupServers[g] = append([]string(nil), names...)
		}
	}
	if a.DefaultServers != nil {
		out.DefaultServers = append([]string(nil), a.DefaultServers...)
	}
	return out
}

// GrantFor returns the group grant of a user with the given stored groups
// (spec Definitions "Group grant"): the union of group_servers[g] for every g
// in groups, plus default_servers when NO stored group matches any key. A
// group value with no map entry contributes nothing; nil groups (a pre-upgrade
// record) match no key. Values are compared exactly, case-sensitive. The
// result may contain "*" (AccessWildcard), which the caller expands to the
// shared set. It never allocates a grant for a nil receiver — the block being
// absent is the caller's decision (today's Shared-only semantics), not an
// empty grant.
func (a *ServerEditionAccessConfig) GrantFor(groups []string) []string {
	if a == nil {
		return nil
	}
	seen := make(map[string]struct{})
	out := make([]string, 0)
	add := func(names []string) {
		for _, n := range names {
			if _, dup := seen[n]; dup || n == "" {
				continue
			}
			seen[n] = struct{}{}
			out = append(out, n)
		}
	}
	matched := false
	for _, g := range groups {
		names, ok := a.GroupServers[g]
		if !ok {
			continue
		}
		matched = true
		add(names)
	}
	if !matched {
		add(a.DefaultServers)
	}
	return out
}

// validate holds the shape rules of the access block (FR-007): a non-empty
// group_servers needs the oidc provider, and every entry must be a valid
// server name or "*". Unknown-but-valid names are NOT refused here — they are
// the doctor finding of serverEditionDoctorFindings (warn, never fail), so an
// operator can write the map before the server it names exists.
func (a *ServerEditionAccessConfig) validate(provider string) error {
	if a == nil {
		return nil
	}
	if len(a.GroupServers) > 0 && provider != "oidc" {
		return fmt.Errorf("%s", msgAccessGroupServersOIDCOnly)
	}
	for group, names := range a.GroupServers {
		if group == "" {
			return fmt.Errorf("server_edition.access.group_servers has an empty group key")
		}
		for _, name := range names {
			if err := validateAccessServerName(name); err != nil {
				return fmt.Errorf("server_edition.access.group_servers[%q]: %w", group, err)
			}
		}
	}
	for _, name := range a.DefaultServers {
		if err := validateAccessServerName(name); err != nil {
			return fmt.Errorf("server_edition.access.default_servers: %w", err)
		}
	}
	return nil
}

// validateAccessServerName admits "*" and any name Config.ValidateDetailed
// would admit for a server (non-empty, no ':' routing separator).
func validateAccessServerName(name string) error {
	if name == AccessWildcard {
		return nil
	}
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("invalid server name %q (must not be empty)", name)
	}
	if strings.Contains(name, ":") {
		return fmt.Errorf("invalid server name %q (must not contain ':', the server:tool routing separator)", name)
	}
	return nil
}

// UnknownAccessServerNames returns every access-map entry (group_servers and
// default_servers, "*" excluded) that names no server in servers — the input
// of the doctor finding. Deterministic order: group keys sorted, then
// default_servers, duplicates dropped.
func (a *ServerEditionAccessConfig) UnknownAccessServerNames(servers []*ServerConfig) []string {
	if a == nil {
		return nil
	}
	known := make(map[string]struct{}, len(servers))
	for _, sc := range servers {
		if sc != nil {
			known[sc.Name] = struct{}{}
		}
	}
	seen := make(map[string]struct{})
	var out []string
	consider := func(name string) {
		if name == AccessWildcard {
			return
		}
		if _, ok := known[name]; ok {
			return
		}
		if _, dup := seen[name]; dup {
			return
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	groups := make([]string, 0, len(a.GroupServers))
	for g := range a.GroupServers {
		groups = append(groups, g)
	}
	sort.Strings(groups)
	for _, g := range groups {
		for _, name := range a.GroupServers[g] {
			consider(name)
		}
	}
	for _, name := range a.DefaultServers {
		consider(name)
	}
	return out
}

// Session cookie Secure policies (Spec 107 FR-026).
const (
	SessionCookieSecureAuto  = "auto"
	SessionCookieSecureTrue  = "true"
	SessionCookieSecureFalse = "false"
)

// Validation messages fixed by contracts/config-keys.md (FR-039: boot, PATCH
// and /config/apply say the same thing).
const (
	msgPublicURLShape            = "server_edition.public_url must be an absolute origin (scheme://host[:port]) with no path"
	msgSessionCookieSecureFalse  = "server_edition.session_cookie_secure=false cannot be combined with an https public_url or tls.enabled"
	msgSessionCookieSecurePolicy = "server_edition.session_cookie_secure must be one of: auto, true, false"
)

// ValidatePublicURL checks the public_url shape: absolute http(s) origin,
// host present, no userinfo, path, query or fragment. Empty is valid (unset).
func ValidatePublicURL(raw string) error {
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%s (got: %q)", msgPublicURLShape, raw)
	}
	// u.Host == "" alone does not catch "https://:443": Go's url.Parse leaves
	// a non-empty Host (":443") with an EMPTY Hostname() when only a port is
	// given, and that value went on to build a malformed OAuth callback /
	// connect-flow URL (cross-review round 1, chunk 3 P2).
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Hostname() == "" || u.User != nil ||
		u.Path != "" || u.RawPath != "" || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" ||
		strings.Contains(raw, "?") || strings.Contains(raw, "#") {
		return fmt.Errorf("%s (got: %q)", msgPublicURLShape, raw)
	}
	return nil
}

// resolveOAuthSecretRef resolves a `${env:...}` / `${keyring:...}` reference
// (or returns a literal value unchanged) WITHOUT mutating the caller's
// config — deliberately, unlike an earlier design (cross-review round 6,
// chunk 3 P2): resolving `server_edition.oauth.client_id`/`client_secret` in
// place at Load time meant the resolved plaintext secret lived in the same
// Config object that GetDesiredConfig/ApplyConfig round-trip through
// SaveConfig on every PATCH /api/v1/config or /config/apply — even one
// editing an unrelated field — permanently overwriting the operator's
// `${env:...}` reference in mcp_config.json with the resolved secret and
// defeating docs/configuration/config-file.md's documented purpose of
// keeping it out of the file. Every doc page for this block —
// docs/configuration/config-file.md, docs/getting-started/installation.md,
// docs/development/server-edition-multiuser-auth.md,
// scripts/dev-server-edition.sh — tells the operator to write
// `${env:OIDC_CLIENT_SECRET}`; ServerEditionConfig.Validate() calls this to
// enforce the "required" check against the RESOLVED value (so a missing env
// var is refused by "client_secret is required", never silently accepted as
// the non-empty placeholder text — cross-review rounds 1 and 2), and
// auth.NewOAuthHandler calls the identical `secret.Resolver` on its own
// private, never-persisted config clone to get the actual value for the
// token endpoint. An empty input resolves to "" with no error (client_id and
// client_secret share the same "is required" check for the empty case).
func resolveOAuthSecretRef(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	return secret.NewResolver().ExpandSecretRefs(context.Background(), raw)
}

// PublicURLIsHTTPS reports whether the configured public_url uses https.
func (c *ServerEditionConfig) PublicURLIsHTTPS() bool {
	return c != nil && strings.HasPrefix(strings.ToLower(c.PublicURL), "https://")
}

// EffectiveSessionCookieSecure returns the policy with "" read as "auto".
func (c *ServerEditionConfig) EffectiveSessionCookieSecure() string {
	if c == nil || c.SessionCookieSecure == "" {
		return SessionCookieSecureAuto
	}
	return c.SessionCookieSecure
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
	if c.SessionCookieSecure == "" {
		c.SessionCookieSecure = SessionCookieSecureAuto
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
	// Resolved (never mutated — c.OAuth.ClientID/ClientSecret keep the
	// operator's literal text, `${env:...}` reference included, for the
	// "required" check below and for every other reader, including
	// SaveConfig's persistence path (cross-review round 6, chunk 3 P2: an
	// earlier design resolved the reference into ClientID/ClientSecret
	// in place at Load time, so ANY later PATCH /api/v1/config or
	// /config/apply — even one editing an unrelated field — round-tripped
	// that already-resolved value back through SaveConfig and permanently
	// overwrote the operator's `${env:...}` reference in mcp_config.json
	// with the plaintext secret, defeating docs/configuration/config-file.md's
	// documented purpose of keeping the secret out of the file). The actual
	// runtime resolution now happens once, at the one place that needs the
	// live secret for the token endpoint: auth.NewOAuthHandler, on its own
	// private, never-persisted config clone.
	if _, err := resolveOAuthSecretRef(c.OAuth.ClientID); err != nil || c.OAuth.ClientID == "" {
		return fmt.Errorf("server_edition.oauth.client_id is required")
	}
	if _, err := resolveOAuthSecretRef(c.OAuth.ClientSecret); err != nil || c.OAuth.ClientSecret == "" {
		return fmt.Errorf("server_edition.oauth.client_secret is required")
	}
	if err := c.OAuth.validateOIDC(); err != nil {
		return err
	}
	if err := c.Access.validate(c.OAuth.Provider); err != nil {
		return err
	}
	if err := ValidatePublicURL(c.PublicURL); err != nil {
		return err
	}
	switch c.SessionCookieSecure {
	case "", SessionCookieSecureAuto, SessionCookieSecureTrue, SessionCookieSecureFalse:
	default:
		return fmt.Errorf("%s (got: %q)", msgSessionCookieSecurePolicy, c.SessionCookieSecure)
	}
	if c.SessionCookieSecure == SessionCookieSecureFalse && c.PublicURLIsHTTPS() {
		return fmt.Errorf("%s", msgSessionCookieSecureFalse)
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
	out.Access = c.Access.Clone()
	return &out
}

// applyOIDCDefaults fills the `oidc` defaults: scopes (with "openid" appended
// when the operator's list lacks it), groups_claim and email_verified_policy.
func (o *ServerEditionOAuthConfig) applyOIDCDefaults() {
	if len(o.Scopes) == 0 {
		o.Scopes = defaultOIDCScopes()
	} else if !containsExact(o.Scopes, "openid") {
		// Exact-case match only: OAuth/OIDC scope values are case-sensitive
		// (RFC 6749 §3.3), so an operator-configured "OpenID"/"OPENID" is a
		// different scope value to a compliant IdP and must not be treated
		// as satisfying the FR-020 requirement — the literal "openid" scope
		// is always appended, even if a differently-cased lookalike is
		// already present (cross-review round 6, chunk 1 P2).
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
//
// display_name is provider-neutral (FR-020/FR-030: the login-button label for
// ANY provider, falling back to the provider family name), so its length
// check runs unconditionally. email_verified_policy is an `oidc`-only concern
// — the struct doc above says so, and only oidcIdentity (oauth_handler.go)
// ever reads it, legacyIdentity never does — so its check must run only for
// `oidc`; checking it unconditionally rejected an otherwise-valid legacy
// (google/github/microsoft) config that carried a leftover or mistyped value
// in that field, instead of ignoring it as documented (cross-review round 1,
// chunk 3 P2).
func (o *ServerEditionOAuthConfig) validateOIDC() error {
	if len(o.DisplayName) > maxOAuthDisplayNameLen {
		return fmt.Errorf("server_edition.oauth.display_name must be at most %d characters", maxOAuthDisplayNameLen)
	}
	if o.Provider != "oidc" {
		return nil
	}
	switch o.EmailVerifiedPolicy {
	case "", EmailVerifiedPolicyRefuseFalse, EmailVerifiedPolicyRequireTrue, EmailVerifiedPolicyIgnore:
	default:
		return fmt.Errorf("server_edition.oauth.email_verified_policy must be one of: refuse_false, require_true, ignore")
	}
	if o.IssuerURL == "" {
		return fmt.Errorf("server_edition.oauth.issuer_url is required when provider is oidc")
	}
	if !IsAllowedOIDCEndpoint(o.IssuerURL, o.AllowInsecureIssuer) {
		return fmt.Errorf("server_edition.oauth.issuer_url must use https (http is allowed only for a loopback host with allow_insecure_issuer: true)")
	}
	// OpenID Connect Discovery 1.0 §2: the Issuer Identifier "MUST NOT
	// contain query or fragment components". IsAllowedOIDCEndpoint admits
	// them (it also gates the discovered endpoints, which the spec does not
	// restrict this way), so an issuer_url carrying either passed validation
	// and reached fetchDiscovery, which builds the discovery request by
	// string-appending "/.well-known/openid-configuration" to issuer_url
	// (oidc_provider.go) — for
	// "https://idp.example/issuer?tenant=x" that produces
	// ".../issuer?tenant=x/.well-known/openid-configuration", a request whose
	// well-known suffix lands inside the query string instead of the path,
	// so every login for that (accepted-at-boot) config failed discovery
	// (cross-review round 6, chunk 3 P2).
	if u, err := url.Parse(o.IssuerURL); err == nil && (u.RawQuery != "" || u.Fragment != "" || u.RawFragment != "") {
		return fmt.Errorf("server_edition.oauth.issuer_url must not contain a query or fragment component (got: %q)", o.IssuerURL)
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

func containsExact(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
