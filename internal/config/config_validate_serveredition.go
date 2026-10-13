//go:build server

package config

// validateServerEditionConfig is the *Config-level bridge that makes
// Config.Validate() and ValidateDetailed() reach the non-mutating
// ServerEditionConfig.Validate (Spec 107 FR-039): boot, PATCH /api/v1/config
// and /config/apply refuse a broken block identically instead of persisting it
// for the next restart. A nil or disabled block has no rule.
func validateServerEditionConfig(cfg *Config) []ValidationError {
	if cfg == nil || cfg.ServerEdition == nil {
		return nil
	}
	if err := cfg.ServerEdition.Validate(); err != nil {
		return []ValidationError{{Field: "server_edition", Message: err.Error()}}
	}
	// The tls.enabled half of the session_cookie_secure=false refusal (Spec
	// 107 FR-026) needs the top-level TLS block the nested Validate cannot
	// see; the https-public_url half lives in ServerEditionConfig.Validate.
	if cfg.ServerEdition.Enabled && cfg.ServerEdition.SessionCookieSecure == SessionCookieSecureFalse &&
		cfg.TLS != nil && cfg.TLS.Enabled {
		return []ValidationError{{Field: "server_edition.session_cookie_secure", Message: msgSessionCookieSecureFalse}}
	}
	return nil
}
