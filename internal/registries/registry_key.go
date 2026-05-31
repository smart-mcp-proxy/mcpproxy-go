package registries

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// ErrRegistryKeyMissing is returned when a registry declares RequiresKey but no
// API key is configured for it. Calling surfaces should treat this as
// "registry unavailable" and continue rather than failing the whole search
// (FR-008 / SC-006).
var ErrRegistryKeyMissing = errors.New("registry requires an API key that is not configured")

// RegistryKeyEnvVar returns the environment variable a key-requiring registry
// reads its API key from: MCPPROXY_REGISTRY_<ID>_API_KEY, with the ID
// upper-cased and any non-alphanumeric character replaced by an underscore.
// e.g. "azure-mcp-demo" -> "MCPPROXY_REGISTRY_AZURE_MCP_DEMO_API_KEY".
func RegistryKeyEnvVar(id string) string {
	var b strings.Builder
	b.WriteString("MCPPROXY_REGISTRY_")
	for _, r := range strings.ToUpper(id) {
		switch {
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	b.WriteString("_API_KEY")
	return b.String()
}

// registryAPIKey resolves the configured API key for a registry, or "" when
// none is set.
func registryAPIKey(reg *RegistryEntry) string {
	return os.Getenv(RegistryKeyEnvVar(reg.ID))
}

// checkRegistryKey enforces FR-008: when a registry requires a key and none is
// configured, it returns a wrapped ErrRegistryKeyMissing naming the env var to
// set. Returns nil when the registry needs no key or one is present.
func checkRegistryKey(reg *RegistryEntry) error {
	if !reg.RequiresKey {
		return nil
	}
	if registryAPIKey(reg) == "" {
		return fmt.Errorf("%w: set %s for registry %q", ErrRegistryKeyMissing, RegistryKeyEnvVar(reg.ID), reg.ID)
	}
	return nil
}
