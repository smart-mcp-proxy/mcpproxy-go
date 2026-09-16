//go:build server

package config

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
