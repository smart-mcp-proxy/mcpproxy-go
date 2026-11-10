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
			ConfigFile:     cfgPath,
			DataDir:        cfg.DataDir,
			DataSecretsDir: filepath.Join(cfg.DataDir, "secrets"),
			AdminKeyPaths:  adminKeySourcePaths(cfg),
		}
	}

	return secret.NewResolverWithOptions(secret.ResolverOptions{
		Scope:      scope,
		FilePolicy: policy,
	})
}

func adminKeySourcePaths(cfg *Config) []string {
	var paths []string
	if path := os.Getenv(EnvAPIKeyFile); path != "" {
		paths = append(paths, path)
	}
	if cfg != nil && secret.IsSecretRef(cfg.APIKey) {
		if ref, err := secret.ParseSecretRef(cfg.APIKey); err == nil && ref.Type == secret.SecretTypeFile {
			paths = append(paths, ref.Name)
		}
	}
	return paths
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
