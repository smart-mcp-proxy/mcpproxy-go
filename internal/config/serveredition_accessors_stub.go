//go:build !server

package config

// Personal-build stubs of the server-edition accessors (Spec 107 T052): the
// block is an opaque carrier here (FR-040), so nothing in it is interpreted.

// ServerEditionEnabled is always false on the personal build.
func ServerEditionEnabled(_ *Config) bool { return false }

// EffectiveRequireMCPAuth is the configured value on the personal build.
func EffectiveRequireMCPAuth(cfg *Config) bool {
	return cfg != nil && cfg.RequireMCPAuth
}

// RequireMCPAuthOverridden is always false on the personal build.
func RequireMCPAuthOverridden(_ *Config) bool { return false }

// IdPProviderFamily is always "" on the personal build.
func IdPProviderFamily(_ *Config) string { return "" }

// PublicURL is always "" on the personal build.
func PublicURL(_ *Config) string { return "" }
