package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Token prefix used for all agent tokens.
const TokenPrefixStr = "mcp_agt_"

// Permission constants define the allowed permission tiers.
const (
	PermRead        = "read"
	PermWrite       = "write"
	PermDestructive = "destructive"
)

// validPermissions is the set of all valid permission values.
var validPermissions = map[string]bool{
	PermRead:        true,
	PermWrite:       true,
	PermDestructive: true,
}

// MaxTokens is the maximum number of agent tokens allowed.
const MaxTokens = 100

// AgentToken represents a stored agent token record.
type AgentToken struct {
	Name           string    `json:"name"`
	TokenHash      string    `json:"token_hash"`
	TokenPrefix    string    `json:"token_prefix"` // first 12 chars of the raw token
	AllowedServers []string  `json:"allowed_servers"`
	Permissions    []string  `json:"permissions"`
	ExpiresAt      time.Time `json:"expires_at"`
	CreatedAt      time.Time `json:"created_at"`
	LastUsedAt     *time.Time `json:"last_used_at,omitempty"`
	Revoked        bool      `json:"revoked"`
	UserID         string    `json:"user_id,omitempty"` // Owner user ID (teams edition)
}

// IsExpired returns true if the token has passed its expiry time.
func (t *AgentToken) IsExpired() bool {
	if t.ExpiresAt.IsZero() {
		return false
	}
	return time.Now().After(t.ExpiresAt)
}

// IsRevoked returns true if the token has been revoked.
func (t *AgentToken) IsRevoked() bool {
	return t.Revoked
}

// GenerateToken creates a new agent token with the mcp_agt_ prefix
// followed by 64 hex characters (32 random bytes). Total length: 72 chars.
func GenerateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("failed to generate random bytes: %w", err)
	}
	return TokenPrefixStr + hex.EncodeToString(b), nil
}

// HashToken computes HMAC-SHA256 of the token using the given key
// and returns the hex-encoded digest.
func HashToken(token string, key []byte) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(token))
	return hex.EncodeToString(mac.Sum(nil))
}

// ValidateTokenFormat checks that a token has the correct format:
// mcp_agt_ prefix followed by exactly 64 hex characters (72 chars total).
func ValidateTokenFormat(token string) bool {
	if len(token) != 72 {
		return false
	}
	if token[:8] != TokenPrefixStr {
		return false
	}
	// Validate remaining 64 chars are hex
	_, err := hex.DecodeString(token[8:])
	return err == nil
}

// TokenPrefix returns the first 12 characters of the token for display purposes.
func TokenPrefix(token string) string {
	if len(token) < 12 {
		return token
	}
	return token[:12]
}

// hmacKeyFile is the filename for the persisted HMAC key.
const hmacKeyFile = ".token_key"

// GetOrCreateHMACKey reads the HMAC key from <dataDir>/.token_key.
// If the file does not exist, it generates a 32-byte random key,
// writes it with 0600 permissions, and returns it.
func GetOrCreateHMACKey(dataDir string) ([]byte, error) {
	keyPath := filepath.Join(dataDir, hmacKeyFile)

	// Try to read existing key
	data, err := os.ReadFile(keyPath)
	if err == nil && len(data) == 32 {
		return data, nil
	}

	// Generate new key
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("failed to generate HMAC key: %w", err)
	}

	// Ensure directory exists
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create data directory: %w", err)
	}

	// Write key file with restrictive permissions
	if err := os.WriteFile(keyPath, key, 0600); err != nil {
		return nil, fmt.Errorf("failed to write HMAC key file: %w", err)
	}

	return key, nil
}

// ValidatePermissions checks that the given permissions list is valid.
// It must contain "read" and only contain valid permission values.
func ValidatePermissions(perms []string) error {
	if len(perms) == 0 {
		return fmt.Errorf("permissions list cannot be empty")
	}

	hasRead := false
	for _, p := range perms {
		if !validPermissions[p] {
			return fmt.Errorf("invalid permission: %q (valid: read, write, destructive)", p)
		}
		if p == PermRead {
			hasRead = true
		}
	}

	if !hasRead {
		return fmt.Errorf("permissions must include %q", PermRead)
	}

	return nil
}
