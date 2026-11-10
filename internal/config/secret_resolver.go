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
		policy.Deny = secret.DenyPaths{
			ConfigFile:     resolveExistingPath(ResolveConfigPath(cfg, configPath)),
			DataDir:        resolveExistingPath(cfg.DataDir),
			DataSecretsDir: resolveExistingPath(filepath.Join(cfg.DataDir, "secrets")),
			AdminKeyFiles:  adminKeySourcePaths(cfg, configPath),
		}
	}

	return secret.NewResolverWithOptions(secret.ResolverOptions{
		Scope:      scope,
		FilePolicy: policy,
	})
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

func adminKeySourcePaths(cfg *Config, configPath string) []string {
	var paths []string
	if path := os.Getenv(EnvAPIKeyFile); path != "" {
		paths = append(paths, resolveExistingPath(path))
	}
	if cfg != nil && secret.IsSecretRef(cfg.APIKey) {
		if ref, err := secret.ParseSecretRef(cfg.APIKey); err == nil && ref.Type == secret.SecretTypeFile {
			paths = append(paths, resolveExistingPath(ref.Name))
		}
	}
	_ = configPath
	return paths
}
