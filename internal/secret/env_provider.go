package secret

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"
)

const (
	SecretTypeEnv = "env"
)

// EnvProvider resolves secrets from environment variables
type EnvProvider struct {
	guardMCPProxy bool
}

// NewEnvProvider creates a new environment variable provider
func NewEnvProvider(guardMCPProxy bool) *EnvProvider {
	return &EnvProvider{guardMCPProxy: guardMCPProxy}
}

// CanResolve returns true if this provider can handle the given secret type
func (p *EnvProvider) CanResolve(secretType string) bool {
	return secretType == SecretTypeEnv
}

// Resolve retrieves the secret value from environment variables
func (p *EnvProvider) Resolve(_ context.Context, ref Ref) (string, error) {
	if !p.CanResolve(ref.Type) {
		return "", fmt.Errorf("env provider cannot resolve secret type: %s", ref.Type)
	}

	if p.guardMCPProxy {
		if ReservedMCPProxyEnvName(ref.Name, runtime.GOOS) {
			return "", ErrReservedEnvVar
		}
	}

	value := os.Getenv(ref.Name)
	if value == "" {
		return "", fmt.Errorf("environment variable %s not found or empty", ref.Name)
	}

	return value, nil
}

// ReservedMCPProxyEnvName reports whether name is in the reserved MCPPROXY_ namespace.
func ReservedMCPProxyEnvName(name, goos string) bool {
	const prefix = "MCPPROXY_"
	if len(name) < len(prefix) {
		return false
	}
	if goos == "windows" {
		return strings.EqualFold(name[:len(prefix)], prefix)
	}
	return strings.HasPrefix(name, prefix)
}

// Store is not supported for environment variables
func (p *EnvProvider) Store(_ context.Context, _ Ref, _ string) error {
	return fmt.Errorf("env provider does not support storing secrets")
}

// Delete is not supported for environment variables
func (p *EnvProvider) Delete(_ context.Context, _ Ref) error {
	return fmt.Errorf("env provider does not support deleting secrets")
}

// List returns all environment variables that look like secrets
func (p *EnvProvider) List(_ context.Context) ([]Ref, error) {
	var refs []Ref

	for _, env := range os.Environ() {
		pair := strings.SplitN(env, "=", 2)
		if len(pair) != 2 {
			continue
		}

		name := pair[0]
		value := pair[1]

		if isLikelySecretEnvVar(name, value) {
			refs = append(refs, Ref{
				Type:     "env",
				Name:     name,
				Original: fmt.Sprintf("${env:%s}", name),
			})
		}
	}

	return refs, nil
}

// IsAvailable always returns true as environment variables are always available
func (p *EnvProvider) IsAvailable() bool {
	return true
}

// isLikelySecretEnvVar returns true if the environment variable looks like it contains a secret
func isLikelySecretEnvVar(name, value string) bool {
	if value == "" {
		return false
	}
	isSecret, confidence := DetectPotentialSecret(value, name)
	return isSecret || confidence >= 0.3
}
