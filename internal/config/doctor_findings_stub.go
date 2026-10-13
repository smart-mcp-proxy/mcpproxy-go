//go:build !server

package config

// serverEditionDoctorFindings has nothing to report on the personal build:
// the server_edition block is an opaque carrier there (Spec 107 FR-040).
func serverEditionDoctorFindings(_ *Config) []string { return nil }
