package config

import (
	"os"
	"path/filepath"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
)

// NewResolverForConfig is an alias for BuildSecretResolver.
func NewResolverForConfig(cfg *Config, configPath string, scope secret.ResolveScope) *secret.Resolver {
	return BuildSecretResolver(cfg, configPath, scope)
}

// BuildSecretResolver constructs a secret resolver for the loaded configuration.
func BuildSecretResolver(cfg *Config, configPath string, scope secret.ResolveScope) *secret.Resolver {
	wildcard, roots, disabled := EffectiveSecretFileDirRoots(cfg, true)

	policy := &secret.FilePolicy{Scope: scope}
	if disabled {
		policy.Disabled = true
	} else if wildcard {
		policy.Wildcard = true
	} else {
		policy.AllowedRoots = append([]string(nil), roots...)
	}

	if scope == secret.ScopeUpstream {
		cfgPath := ResolveConfigPath(cfg, configPath)
		policy.Deny = secret.DenyPaths{
			ConfigFile:     resolveExistingPath(cfgPath),
			DataDir:        resolveExistingPath(cfg.DataDir),
			DataSecretsDir: resolveExistingPath(filepath.Join(cfg.DataDir, "secrets")),
		}
		policy.DenyIdentities = buildDenyIdentities(cfg, configPath)
	}

	return secret.NewResolverWithOptions(secret.ResolverOptions{
		Scope:      scope,
		FilePolicy: policy,
	})
}

func buildDenyIdentities(cfg *Config, configPath string) []secret.DenyIdentity {
	var ids []secret.DenyIdentity
	add := func(path string) {
		if id, ok := secret.StatDenyIdentity(path); ok {
			ids = append(ids, id)
		}
	}

	cfgPath := ResolveConfigPath(cfg, configPath)
	add(cfgPath)

	if path := os.Getenv(EnvAPIKeyFile); path != "" {
		add(path)
	}
	if cfg != nil && secret.IsSecretRef(cfg.APIKey) {
		if ref, err := secret.ParseSecretRef(cfg.APIKey); err == nil && ref.Type == secret.SecretTypeFile {
			add(ref.Name)
		}
	}

	if cfg != nil && cfg.DataDir != "" {
		add(filepath.Join(cfg.DataDir, "config.db"))
		add(filepath.Join(cfg.DataDir, ConfigFileName))
		add(filepath.Join(cfg.DataDir, "mcpproxy.sock"))
	}

	return ids
}

// BuildSecretResolverForDataDirExpand builds a proxy-scoped resolver used only
// while resolving data_dir: allow-list entries are independent of data_dir, so
// a ${file:} data_dir cannot include its own derived secrets directory.
func BuildSecretResolverForDataDirExpand(cfg *Config) *secret.Resolver {
	wildcard, roots, disabled := EffectiveSecretFileDirRoots(cfg, false)
	policy := &secret.FilePolicy{Scope: secret.ScopeProxy}
	if disabled {
		policy.Disabled = true
	} else if wildcard {
		policy.Wildcard = true
	} else {
		policy.AllowedRoots = append([]string(nil), roots...)
	}
	return secret.NewResolverWithOptions(secret.ResolverOptions{
		Scope:      secret.ScopeProxy,
		FilePolicy: policy,
	})
}

func resolveExistingPath(path string) string {
	if path == "" {
		return ""
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return filepath.Clean(path)
	}
	return resolved
}
