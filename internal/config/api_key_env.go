package config

import (
	"context"
	"errors"
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
}

// ErrAPIKeyBothSet is returned when MCPPROXY_API_KEY and MCPPROXY_API_KEY_FILE
// are both set to a non-empty value. The text is fixed: it names the two
// variables and nothing else, so it can never carry a key or file contents.
var ErrAPIKeyBothSet = errors.New(EnvAPIKey + " and " + EnvAPIKeyFile + " are both set; set only one of them")

// checkEnvAPIKeyConflict returns ErrAPIKeyBothSet when both variables are
// non-empty. It reads no file.
func checkEnvAPIKeyConflict() error {
	if os.Getenv(EnvAPIKey) != "" && os.Getenv(EnvAPIKeyFile) != "" {
		return ErrAPIKeyBothSet
	}
	return nil
}

// ResolveEnvAPIKey is the one place the environment's API key is read. Every
// reader (the loader, EnsureAPIKey, the CLI client, `status`, the tray and the
// core it launches) goes through it so the two variables mean the same thing
// everywhere.
//
// Setting both MCPPROXY_API_KEY and MCPPROXY_API_KEY_FILE to a non-empty value
// is an error (ErrAPIKeyBothSet), whatever the file holds and whether or not it
// exists: there is one rule, and no variable silently shadows the other. The
// check looks at the variables only and does not open the file. An empty
// variable counts as unset, as Compose files that interpolate an optional
// variable produce one. (An empty MCPPROXY_API_KEY alone still disables
// authentication, as before.)
//
// A file that is named but cannot be used (missing, unreadable, not absolute,
// too large, empty after removing its trailing newline) is an error. The caller
// must stop: falling back to the other variable, or worse to a generated key,
// would start the proxy under a credential the operator did not choose.
func ResolveEnvAPIKey() (EnvAPIKeyResult, error) {
	envKey, envSet := os.LookupEnv(EnvAPIKey)
	path := os.Getenv(EnvAPIKeyFile)

	if err := checkEnvAPIKeyConflict(); err != nil {
		return EnvAPIKeyResult{}, err
	}

	if path != "" {
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
		}, nil
	}

	return EnvAPIKeyResult{Set: envSet, Key: envKey}, nil
}

// resolveAPIKeyRef replaces an api_key that is a secret reference with the
// value it points at, as a process-only override: the effective config runs
// with the secret, while every save writes the reference (never the secret)
// back, whatever else the save is about (see process_overrides.go).
//
// The reference is resolved here, once per load, and a reference that cannot be
// resolved fails the load. Generating a key instead would start the proxy
// under a credential the operator did not choose and save it over the
// reference.
//
// A key from the environment (MCPPROXY_API_KEY_FILE or MCPPROXY_API_KEY)
// overrides the config file's key, so with one present the reference is left
// as written, unread, for EnsureAPIKey's override to replace.
func resolveAPIKeyRef(c *Config) error {
	if !secret.IsSecretRef(c.APIKey) {
		return nil
	}
	envKey, err := ResolveEnvAPIKey()
	if err != nil {
		return err
	}
	if envKey.Set && envKey.Key != "" {
		return nil
	}
	resolved, err := secret.NewResolver().ExpandSecretRefs(context.Background(), c.APIKey)
	if err != nil {
		// ExpandSecretRefs prefixes its error with the reference text; the
		// operator already knows which field this is, so report the cause only.
		if cause := errors.Unwrap(err); cause != nil {
			err = cause
		}
		return fmt.Errorf("api_key: could not resolve the reference: %w", err)
	}
	if resolved == "" {
		return fmt.Errorf("api_key: the reference resolved to an empty value")
	}
	OverrideForProcess(c, FieldAPIKey, OverrideSourceEnv, resolved)
	return nil
}
