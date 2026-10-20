package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
)

const (
	// EnvAPIKey is the environment variable that carries the API key itself.
	EnvAPIKey = "MCPPROXY_API_KEY"
	// EnvAPIKeyFile names a file that holds the API key: the *_FILE convention
	// of Docker Compose and Swarm secrets (/run/secrets/<name>), Kubernetes
	// secret volumes and systemd LoadCredential=. Unlike EnvAPIKey the key is
	// not in the process environment, so it does not show up in
	// `docker inspect`, /proc/<pid>/environ or a crash dump.
	EnvAPIKeyFile = "MCPPROXY_API_KEY_FILE"
)

// EnvAPIKeyResult is the API key a process was handed through its
// environment, however it was handed over.
type EnvAPIKeyResult struct {
	// Set is true when the environment carried a key variable: MCPPROXY_API_KEY
	// is set, or MCPPROXY_API_KEY_FILE names a readable, non-empty file. An
	// empty MCPPROXY_API_KEY is Set with an empty Key, which callers treat as
	// "no key supplied" (a key is generated), the same as an unset variable.
	Set bool
	// Key is the value. It is never empty when FromFile is true.
	Key string
	// FromFile is true when Key was read from MCPPROXY_API_KEY_FILE.
	FromFile bool
	// Path is the file Key was read from, when FromFile.
	Path string
	// Shadowed is true when both variables carry a key and the file won.
	Shadowed bool
}

// ResolveEnvAPIKey is the one place the environment's API key is read. Every
// reader (the loader, EnsureAPIKey, the CLI client, `status`, the tray and the
// core it launches) goes through it so the two variables mean the same thing
// everywhere.
//
// MCPPROXY_API_KEY_FILE wins over MCPPROXY_API_KEY when both are set (an
// explicit file is the more deliberate choice and the one that keeps the key
// out of the environment); the result reports Shadowed so a caller can warn.
// An empty MCPPROXY_API_KEY_FILE is treated as unset, as Compose files that
// interpolate an optional variable produce one.
//
// A file that is named but cannot be used (missing, unreadable, not absolute,
// too large, empty after removing its trailing newline) is an error. The caller
// must stop: falling back to the other variable, or worse to a generated key,
// would start the proxy under a credential the operator did not choose.
func ResolveEnvAPIKey() (EnvAPIKeyResult, error) {
	envKey, envSet := os.LookupEnv(EnvAPIKey)

	if path := os.Getenv(EnvAPIKeyFile); path != "" {
		key, err := secret.ReadFile(path)
		if err != nil {
			return EnvAPIKeyResult{}, fmt.Errorf("%s: %w", EnvAPIKeyFile, err)
		}
		if strings.TrimSpace(key) == "" {
			return EnvAPIKeyResult{}, fmt.Errorf("%s: secret file %q is empty", EnvAPIKeyFile, path)
		}
		return EnvAPIKeyResult{
			Set:      true,
			Key:      key,
			FromFile: true,
			Path:     path,
			Shadowed: envSet && envKey != "",
		}, nil
	}

	return EnvAPIKeyResult{Set: envSet, Key: envKey}, nil
}
