//go:build !server

package config

// validateServerEditionConfig is a no-op in the personal edition: the
// server_edition block is an opaque JSON carrier there (Spec 107 FR-040).
func validateServerEditionConfig(_ *Config) []ValidationError {
	return nil
}
