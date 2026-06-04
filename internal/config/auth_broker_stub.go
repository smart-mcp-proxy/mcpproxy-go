//go:build !server

package config

// AuthBrokerConfig is a stub for the personal edition. Per-upstream token
// brokering is a server-edition feature (spec 074); the personal edition keeps
// the field on ServerConfig so configs round-trip, but carries no behavior and
// performs no validation — personal-edition behavior is unaffected.
type AuthBrokerConfig struct{}

// validateServerAuthBroker is a no-op in the personal edition.
func validateServerAuthBroker(_ *ServerConfig, _ string) []ValidationError {
	return nil
}
