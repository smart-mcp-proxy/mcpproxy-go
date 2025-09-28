package secret

import (
	"context"
)

// Ref represents a reference to a secret
type Ref struct {
	Type     string `json:"type"`     // env, keyring, op, age
	Name     string `json:"name"`     // environment variable name, keyring alias, etc.
	Original string `json:"original"` // original reference string
}

// Provider interface for secret resolution
type Provider interface {
	// CanResolve returns true if this provider can handle the given secret type
	CanResolve(secretType string) bool

	// Resolve retrieves the actual secret value
	Resolve(ctx context.Context, ref Ref) (string, error)

	// Store saves a secret (if supported by the provider)
	Store(ctx context.Context, ref Ref, value string) error

	// Delete removes a secret (if supported by the provider)
	Delete(ctx context.Context, ref Ref) error

	// List returns all secret references handled by this provider
	List(ctx context.Context) ([]Ref, error)

	// IsAvailable checks if the provider is available on the current system
	IsAvailable() bool
}

// Resolver manages secret resolution using multiple providers
type Resolver struct {
	providers map[string]Provider
}

// ResolveResult contains the result of secret resolution
type ResolveResult struct {
	Ref      Ref
	Value    string
	Error    error
	Resolved bool
}

// MigrationCandidate represents a potential secret that could be migrated
type MigrationCandidate struct {
	Field      string  `json:"field"`      // Field path in config
	Value      string  `json:"value"`      // Current plaintext value (masked in responses)
	Suggested  string  `json:"suggested"`  // Suggested Ref
	Confidence float64 `json:"confidence"` // Confidence this is a secret (0-1)
}

// MigrationAnalysis contains analysis of potential secrets to migrate
type MigrationAnalysis struct {
	Candidates []MigrationCandidate `json:"candidates"`
	TotalFound int                  `json:"total_found"`
}

// EnvVarStatus represents the status of an environment variable reference
type EnvVarStatus struct {
	Ref   Ref  `json:"secret_ref"`
	IsSet bool `json:"is_set"`
}

// ConfigSecretsResponse contains secrets and environment variables referenced in config
type ConfigSecretsResponse struct {
	Secrets         []Ref          `json:"secrets"`
	EnvironmentVars []EnvVarStatus `json:"environment_vars"`
	TotalSecrets    int            `json:"total_secrets"`
	TotalEnvVars    int            `json:"total_env_vars"`
}
