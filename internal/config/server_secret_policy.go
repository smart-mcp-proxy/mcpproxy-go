package config

import (
	"context"
	"errors"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
)

// ServerSecretPolicyErrors reports upstream secret-policy failures suitable for
// write-time rejection. Messages use only the fixed public denial strings.
func ServerSecretPolicyErrors(cfg *Config, configPath string, sc *ServerConfig) []ValidationError {
	if sc == nil {
		return nil
	}
	r := BuildSecretResolver(cfg, ConfigFilePathForPolicyFrom(cfg, configPath), secret.ScopeUpstream)
	cp := CopyServerConfig(sc)
	errs := r.ExpandStructSecretsCollectErrors(context.Background(), cp)
	if len(errs) == 0 {
		return nil
	}
	var out []ValidationError
	for _, e := range errs {
		if !secret.IsPolicyDenial(e.Err) {
			continue
		}
		out = append(out, ValidationError{
			Field:   e.FieldPath,
			Message: secret.PublicPolicyMessage(e.Err),
		})
	}
	return out
}

// ConfigFilePathForPolicyFrom returns the config path used for policy deny lists.
func ConfigFilePathForPolicyFrom(cfg *Config, configPath string) string {
	if configPath != "" {
		return configPath
	}
	if cfg != nil {
		return cfg.ConfigFilePathForPolicy()
	}
	return ""
}

// ErrServerSecretPolicy is returned when a server entry violates secret policy.
var ErrServerSecretPolicy = errors.New("server secret reference refused by policy")
