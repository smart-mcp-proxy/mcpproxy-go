//go:build server

// Package broker provides per-user, per-upstream credential storage for the
// server edition's upstream token-brokering feature (spec 074).
//
// Credentials are persisted encrypted-at-rest using authenticated encryption
// (AES-256-GCM with a unique nonce per record). The storage backend is hidden
// behind the CredentialStore interface so that alternative backends (external
// secret managers such as HashiCorp Vault or AWS Secrets Manager) can be added
// later without changing callers (FR-023).
package broker

import (
	"errors"
	"os"
	"time"
)

// MasterKeyEnvVar is the environment variable that supplies the base64-encoded
// 32-byte AES-256 master key. It takes precedence over the configuration value.
const MasterKeyEnvVar = "MCPPROXY_CRED_KEY" //nolint:gosec // env var name, not a credential

// Sentinel errors returned by CredentialStore implementations.
var (
	// ErrStoreDisabled is returned by every operation when the store has no
	// encryption key configured. The broker degrades gracefully: the rest of
	// the gateway is unaffected (FR-022).
	ErrStoreDisabled = errors.New("credential store disabled: no encryption key configured")

	// ErrNotFound is returned when no credential exists for the given key, or
	// when an existing record cannot be decrypted (e.g. the master key was
	// rotated). Undecryptable records are treated as absent rather than fatal.
	ErrNotFound = errors.New("credential not found")
)

// UpstreamCredential is the value model persisted for each user/upstream pair
// and for each identity-provider subject token. It is serialized and then
// AES-256-GCM encrypted before being written to the backend (FR-020).
type UpstreamCredential struct {
	// Type identifies the credential flavour, e.g. "oauth2", "api_key",
	// "idp_subject_token".
	Type string `json:"type"`
	// AccessToken is the bearer/access credential presented to the upstream.
	AccessToken string `json:"access_token"`
	// RefreshToken, when present, is used to mint a fresh access token.
	RefreshToken string `json:"refresh_token,omitempty"`
	// ExpiresAt is the access-token expiry. A zero value means never-expiring
	// (matches Go's oauth2 convention).
	ExpiresAt time.Time `json:"expires_at,omitempty"`
	// Scopes granted to the access token.
	Scopes []string `json:"scopes,omitempty"`
	// TokenType, e.g. "Bearer".
	TokenType string `json:"token_type,omitempty"`
	// Audience is the intended audience of the token (RFC 8693).
	Audience string `json:"audience,omitempty"`
	// ObtainedVia records how the credential was acquired, e.g.
	// "token_exchange", "connect_flow", "static".
	ObtainedVia string `json:"obtained_via,omitempty"`
	// UpdatedAt is the last write time for this record.
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}

// IsExpired reports whether the credential's access token has expired. A zero
// ExpiresAt is treated as never-expiring.
func (c *UpstreamCredential) IsExpired() bool {
	if c.ExpiresAt.IsZero() {
		return false
	}
	return time.Now().After(c.ExpiresAt)
}

// IsValid reports whether the credential has a non-expired access token.
func (c *UpstreamCredential) IsValid() bool {
	return !c.IsExpired()
}

// ExpiresWithin reports whether the credential expires within the given
// duration from now. A never-expiring credential always returns false.
func (c *UpstreamCredential) ExpiresWithin(d time.Duration) bool {
	if c.ExpiresAt.IsZero() {
		return false
	}
	return time.Now().Add(d).After(c.ExpiresAt)
}

// CredentialEntry pairs a stored upstream credential with the serverKey it was
// stored under. Returned by CredentialStore.List.
type CredentialEntry struct {
	ServerKey  string
	Credential *UpstreamCredential
}

// CredentialStore is the abstraction over credential persistence. Callers
// depend only on this interface so that the BBolt+AES backend can be swapped
// for an external secret manager later (FR-023).
//
// serverKey identifies an upstream and is the existing SHA256(name+url) scheme
// from internal/oauth (see oauth.GenerateServerKey). An empty serverKey selects
// the identity-provider subject-token record, which is keyed by userID alone.
//
// All implementations isolate records per user (FR-021): no user can read or
// enumerate another user's credentials.
type CredentialStore interface {
	// Enabled reports whether the store has a usable encryption key. When
	// false, all other methods return ErrStoreDisabled.
	Enabled() bool

	// Get returns the credential for (userID, serverKey). An empty serverKey
	// selects the user's idp subject token. Returns ErrNotFound if absent or
	// undecryptable.
	Get(userID, serverKey string) (*UpstreamCredential, error)

	// Put stores (or overwrites) the credential for (userID, serverKey). An
	// empty serverKey stores the user's idp subject token.
	Put(userID, serverKey string, cred *UpstreamCredential) error

	// Delete removes the credential for (userID, serverKey). Deleting a
	// non-existent record is a no-op.
	Delete(userID, serverKey string) error

	// List returns all upstream credentials for the user. The idp subject
	// token (serverKey == "") is NOT included.
	List(userID string) ([]CredentialEntry, error)
}

// Compile-time assertion that the BBolt+AES backend satisfies the interface.
var _ CredentialStore = (*BBoltAESStore)(nil)

// ResolveMasterKey returns the base64-encoded master key, preferring the
// MCPPROXY_CRED_KEY environment variable over the supplied configuration value
// (e.g. server_edition.credential_encryption_key). Returns "" when neither is set, in
// which case the store is disabled.
func ResolveMasterKey(configKey string) string {
	if v := os.Getenv(MasterKeyEnvVar); v != "" {
		return v
	}
	return configKey
}
