//go:build !server

package config

// applyServerEditionEnvOverrides is a no-op in the personal edition: the
// server_edition block is an opaque carrier there (Spec 107 FR-040) and
// MCPPROXY_PUBLIC_URL is never interpreted.
func applyServerEditionEnvOverrides(_ *Config) {}
