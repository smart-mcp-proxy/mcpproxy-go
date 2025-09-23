package secret

import (
	"context"
	"fmt"
	"os"
	"strings"
)

const (
	SecretTypeEnv = "env"
)

// EnvProvider resolves secrets from environment variables
type EnvProvider struct{}

// NewEnvProvider creates a new environment variable provider
func NewEnvProvider() *EnvProvider {
	return &EnvProvider{}
}

// CanResolve returns true if this provider can handle the given secret type
func (p *EnvProvider) CanResolve(secretType string) bool {
	return secretType == SecretTypeEnv
}

// Resolve retrieves the secret value from environment variables
func (p *EnvProvider) Resolve(ctx context.Context, ref SecretRef) (string, error) {
	if !p.CanResolve(ref.Type) {
		return "", fmt.Errorf("env provider cannot resolve secret type: %s", ref.Type)
	}

	value := os.Getenv(ref.Name)
	if value == "" {
		return "", fmt.Errorf("environment variable %s not found or empty", ref.Name)
	}

	return value, nil
}

// Store is not supported for environment variables
func (p *EnvProvider) Store(ctx context.Context, ref SecretRef, value string) error {
	return fmt.Errorf("env provider does not support storing secrets")
}

// Delete is not supported for environment variables
func (p *EnvProvider) Delete(ctx context.Context, ref SecretRef) error {
	return fmt.Errorf("env provider does not support deleting secrets")
}

// List returns all environment variables that look like secrets
func (p *EnvProvider) List(ctx context.Context) ([]SecretRef, error) {
	var refs []SecretRef

	for _, env := range os.Environ() {
		pair := strings.SplitN(env, "=", 2)
		if len(pair) != 2 {
			continue
		}

		name := pair[0]
		value := pair[1]

		// Only include variables that look like secrets
		if isLikelySecretEnvVar(name, value) {
			refs = append(refs, SecretRef{
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

	// Check if the variable name suggests it's a secret
	isSecret, confidence := DetectPotentialSecret(value, name)

	// Lower threshold for env vars since they're commonly used for secrets
	return isSecret || confidence >= 0.3
}
