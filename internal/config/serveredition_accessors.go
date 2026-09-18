//go:build server

package config

import (
	"bytes"
	"encoding/json"
)

// Build-tagged accessors for the server-edition block (Spec 107 T052). The
// edition-neutral packages (internal/server, internal/management) read the
// block only through these, so the personal build — where the block is an
// opaque carrier — never interprets it.

// ServerEditionEnabled reports whether the server-edition block is present and
// enabled.
func ServerEditionEnabled(cfg *Config) bool {
	return cfg != nil && cfg.ServerEdition != nil && cfg.ServerEdition.Enabled
}

// EffectiveRequireMCPAuth is the value the /mcp auth middleware enforces:
// under an enabled server-edition block it is always true (FR-029), otherwise
// the configured require_mcp_auth.
func EffectiveRequireMCPAuth(cfg *Config) bool {
	if cfg == nil {
		return false
	}
	return cfg.RequireMCPAuth || ServerEditionEnabled(cfg)
}

// RequireMCPAuthOverridden reports the case the boot notice and the doctor
// finding name: an explicit require_mcp_auth: false forced to true by an
// enabled server-edition block.
func RequireMCPAuthOverridden(cfg *Config) bool {
	return cfg != nil && !cfg.RequireMCPAuth && ServerEditionEnabled(cfg)
}

// IdPProviderFamily returns the configured oauth.provider family ("google",
// "github", "microsoft", "oidc") or "" when no block/oauth is configured.
func IdPProviderFamily(cfg *Config) string {
	if cfg == nil || cfg.ServerEdition == nil || cfg.ServerEdition.OAuth == nil {
		return ""
	}
	return cfg.ServerEdition.OAuth.Provider
}

// PublicURL returns server_edition.public_url ("" when unset).
func PublicURL(cfg *Config) string {
	if cfg == nil || cfg.ServerEdition == nil {
		return ""
	}
	return cfg.ServerEdition.PublicURL
}

// ServerEditionRestartProjection returns the restart-pinned subset of the
// block for DetectConfigChanges (Spec 107 FR-039 part 2): every key setup.go
// binds at construction — enabled, oauth.*, public_url, session_cookie_secure,
// the TTLs and credential_encryption_key. admin_emails is deliberately absent
// (live through ServerEditionConfigProvider) and so is the deprecated no-op
// store_idp_tokens (contracts/config-keys.md: never reported). The value is
// meant for jsonEqual: a nil block projects to the zero value, so an absent
// block and `{}` compare equal, and omitempty collapses nil-vs-[] the way the
// PATCH round-trip does.
func ServerEditionRestartProjection(cfg *Config) any {
	var p serverEditionRestartProjection
	if cfg == nil || cfg.ServerEdition == nil {
		return p
	}
	se := cfg.ServerEdition
	p.Enabled = se.Enabled
	p.OAuth = se.OAuth
	p.SessionTTL = se.SessionTTL
	p.BearerTokenTTL = se.BearerTokenTTL
	p.CredentialEncryptionKey = se.CredentialEncryptionKey
	p.PublicURL = se.PublicURL
	p.SessionCookieSecure = se.SessionCookieSecure
	return p
}

type serverEditionRestartProjection struct {
	Enabled                 bool                      `json:"enabled"`
	OAuth                   *ServerEditionOAuthConfig `json:"oauth,omitempty"`
	SessionTTL              Duration                  `json:"session_ttl,omitempty"`
	BearerTokenTTL          Duration                  `json:"bearer_token_ttl,omitempty"`
	CredentialEncryptionKey string                    `json:"credential_encryption_key,omitempty"`
	PublicURL               string                    `json:"public_url,omitempty"`
	SessionCookieSecure     string                    `json:"session_cookie_secure,omitempty"`
}

// ServerEditionAdminEmails returns server_edition.admin_emails (nil when the
// block is absent) — the one live key of the block, compared with
// slices.Equal by DetectConfigChanges.
func ServerEditionAdminEmails(cfg *Config) []string {
	if cfg == nil || cfg.ServerEdition == nil {
		return nil
	}
	return cfg.ServerEdition.AdminEmails
}

// ServerEditionRestartReason names the first restart-pinned key group that
// differs between the two blocks, in the words of contracts/config-keys.md.
// It carries key names only — never a value, so no secret can reach a log
// line or an API result through it. "" when nothing restart-pinned differs.
func ServerEditionRestartReason(oldCfg, newCfg *Config) string {
	o, n := serverEditionOrZero(oldCfg), serverEditionOrZero(newCfg)
	switch {
	case o.Enabled != n.Enabled:
		return "server_edition.enabled requires a restart"
	case o.PublicURL != n.PublicURL:
		return "server_edition.public_url is used at login handler construction"
	case !oauthBlocksEqual(o.OAuth, n.OAuth):
		return "server_edition.oauth.* is bound at login handler construction"
	case o.SessionCookieSecure != n.SessionCookieSecure:
		return "server_edition.session_cookie_secure is bound at session store construction"
	case o.SessionTTL != n.SessionTTL:
		return "server_edition.session_ttl is bound at session store construction"
	case o.BearerTokenTTL != n.BearerTokenTTL:
		return "server_edition.bearer_token_ttl is bound at session store construction"
	case o.CredentialEncryptionKey != n.CredentialEncryptionKey:
		return "server_edition.credential_encryption_key is bound at credential store construction"
	}
	return ""
}

// oauthBlocksEqual compares two oauth blocks by their JSON form so the PATCH
// round-trip's nil-vs-[] on allowed_domains/scopes does not name oauth.* as
// the reason for an unrelated TTL edit.
func oauthBlocksEqual(a, b *ServerEditionOAuthConfig) bool {
	if (a == nil) != (b == nil) {
		return false
	}
	if a == nil {
		return true
	}
	ab, errA := json.Marshal(a)
	bb, errB := json.Marshal(b)
	return errA == nil && errB == nil && bytes.Equal(ab, bb)
}

func serverEditionOrZero(cfg *Config) ServerEditionConfig {
	if cfg == nil || cfg.ServerEdition == nil {
		return ServerEditionConfig{}
	}
	return *cfg.ServerEdition
}

// MergeServerEditionRestartGated returns the ServerEdition block the running
// process can actually adopt for one config apply (Spec 107 FR-039 part 2;
// cross-review round 1, chunk 3 P1): the exact restart-pinned subset
// ServerEditionRestartProjection/ServerEditionRestartReason name — enabled,
// oauth.*, public_url, session_cookie_secure, session_ttl, bearer_token_ttl,
// credential_encryption_key — pinned to `live`, so the process can never
// silently adopt a change DetectConfigChanges reports as restart-required
// (before this, internal/runtime's pinRestartGated did not pin ServerEdition
// at all, so e.g. disabling server_edition.enabled took effect immediately —
// restoring anonymous /mcp access — while the API still reported "requires
// restart"). admin_emails (and the deprecated store_idp_tokens no-op) pass
// through from `desired`, which is what makes admin_emails hot (#1169,
// setup.go's ServerEditionConfigProvider).
func MergeServerEditionRestartGated(live, desired *ServerEditionConfig) *ServerEditionConfig {
	if live == nil && desired == nil {
		return nil
	}
	out := &ServerEditionConfig{}
	if desired != nil {
		*out = *desired
	}
	if live != nil {
		out.Enabled = live.Enabled
		out.OAuth = live.OAuth
		out.SessionTTL = live.SessionTTL
		out.BearerTokenTTL = live.BearerTokenTTL
		out.CredentialEncryptionKey = live.CredentialEncryptionKey
		out.PublicURL = live.PublicURL
		out.SessionCookieSecure = live.SessionCookieSecure
	} else {
		out.Enabled = false
		out.OAuth = nil
		out.SessionTTL = 0
		out.BearerTokenTTL = 0
		out.CredentialEncryptionKey = ""
		out.PublicURL = ""
		out.SessionCookieSecure = ""
	}
	return out
}
