//go:build server

package config

import (
	"os"
	"strings"
)

// EnvPublicURL is the environment alias of server_edition.public_url — the
// one nested `server_edition.*` key with an env alias (Spec 107 FR-025).
const EnvPublicURL = "MCPPROXY_PUBLIC_URL"

// applyServerEditionEnvOverrides applies MCPPROXY_PUBLIC_URL over the file
// value. An unset or blank variable leaves the file value alone. The override
// runs before Validate in LoadFromFile, so an invalid value is refused with the
// same message as a file value. A document without a server_edition block
// gains one only to carry the override (disabled, so no rule applies).
func applyServerEditionEnvOverrides(cfg *Config) {
	// Trimmed before the blank check (cross-review round 1, chunk 3 P3): a
	// whitespace-only value (e.g. an env file with a stray space) used to
	// pass the `!= ""` gate, overwrite a valid file value with a non-empty
	// but unparsable string, and fail startup validation — contradicting
	// this function's own "blank leaves the file value alone" contract.
	value := strings.TrimSpace(os.Getenv(EnvPublicURL))
	if value == "" || cfg == nil {
		return
	}
	if cfg.ServerEdition == nil {
		cfg.ServerEdition = &ServerEditionConfig{}
	}
	cfg.ServerEdition.PublicURL = value
}
