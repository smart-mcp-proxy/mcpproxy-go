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
	return nil
}
