package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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

// MaxTokens is the maximum number of stored agent tokens allowed deployment-wide.
const MaxTokens = 100

// MaxTokensPerOwner caps how many stored agent tokens a single owner may hold
// in the server edition. Without an owner cap, any authenticated tenant can
// consume the entire deployment-wide pool and prevent every other tenant,
// including an administrator, from minting a token (issue #1177).
//
// Ownerless personal-edition tokens are exempt so their established MaxTokens
// limit remains unchanged. Twenty-five preserves room for at least four fully
// provisioned owners inside the existing deployment cap.
const MaxTokensPerOwner = 25

// AgentToken represents a stored agent token record.
type AgentToken struct {
	Name           string     `json:"name"`
	TokenHash      string     `json:"token_hash"`
	TokenPrefix    string     `json:"token_prefix"` // first 12 chars of the raw token
	AllowedServers []string   `json:"allowed_servers"`
	Permissions    []string   `json:"permissions"`
	ExpiresAt      time.Time  `json:"expires_at"`
	CreatedAt      time.Time  `json:"created_at"`
	LastUsedAt     *time.Time `json:"last_used_at,omitempty"`
	Revoked        bool       `json:"revoked"`
	UserID         string     `json:"user_id,omitempty"`     // Owner user ID (server edition)
	ProfilePin     string     `json:"profile_pin,omitempty"` // Profile this token is pinned to (Profiles v2 T3)

	// OwnerEmail, OwnerProvider and OwnerRole are the owner's identity as of
	// THIS authentication (Spec 107 FR-004/FR-013, data-model.md §3). They are
	// NEVER persisted (`json:"-"` keeps them out of the BBolt record, which is
	// json.Marshal'ed): storage.ValidateAgentToken stamps them on the token
	// value it returns from the single owner resolution, and AuthContext()
	// copies them into the request context. A token read back from the store
	// by any other path carries them empty. Role is derived live from
	// admin_emails, so it can change between two authentications of the same
	// token — which is exactly why it must not be stored.
	OwnerEmail    string `json:"-"`
	OwnerProvider string `json:"-"`
	OwnerRole     string `json:"-"`
}

// Token expiry rule shared by core POST /api/v1/tokens and the server
// edition's POST /api/v1/user/tokens (Spec 107 FR-011).
const (
	// DefaultTokenExpiry is the expiry applied when expires_in is omitted.
	DefaultTokenExpiry = 30 * 24 * time.Hour
	// MaxTokenExpiry caps every owned or operator token at 365 days.
	MaxTokenExpiry = 365 * 24 * time.Hour
)

// ParseTokenExpiry is THE expiry rule for an agent token (Spec 107 FR-011,
// contracts/entitlement-predicate.md §5): positive, at most 365 days, fixed
// error text. Accepted formats: "30d" (days), "720h" (hours), or any Go
// duration string; "" means the 30-day default. Both minting doors call it,
// so neither can drift to an unbounded lifetime again (the server-edition
// door used to parse with a bare time.ParseDuration and no cap).
func ParseTokenExpiry(expiresIn string, now time.Time) (time.Time, error) {
	if expiresIn == "" {
		return now.Add(DefaultTokenExpiry), nil
	}

	var d time.Duration

	// Handle "Nd" format (days)
	if strings.HasSuffix(expiresIn, "d") {
		daysStr := strings.TrimSuffix(expiresIn, "d")
		days, err := strconv.Atoi(daysStr)
		if err != nil || days <= 0 {
			return time.Time{}, fmt.Errorf("invalid expiry duration: %q", expiresIn)
		}
		d = time.Duration(days) * 24 * time.Hour
	} else {
		// Try standard Go duration
		var err error
		d, err = time.ParseDuration(expiresIn)
		if err != nil {
			return time.Time{}, fmt.Errorf("invalid expiry duration: %q", expiresIn)
		}
		if d <= 0 {
			return time.Time{}, fmt.Errorf("expiry duration must be positive")
		}
	}

	if d > MaxTokenExpiry {
		return time.Time{}, fmt.Errorf("expiry duration cannot exceed 365 days")
	}

	return now.Add(d), nil
}

// AuthContext builds the request AuthContext for a validated agent token. It
// is the single constructor for the agent tier so no auth path can silently
// drop a field: the REST path used to omit ProfilePin, which meant a
// profile-pinned token evaluated (and, with Spec 098, preflighted) against the
// unpinned server set. Returns nil for a nil token.
//
// Spec 107 (T076): it also copies the non-persisted OwnerEmail/OwnerProvider/
// OwnerRole that storage stamped on the validated token value into
// Email/Provider/Role, so an owned token's request carries its owner's live
// identity without a second store read. CredentialKind is always agent_token.
func (t *AgentToken) AuthContext() *AuthContext {
	if t == nil {
		return nil
	}
	return &AuthContext{
		Type:           AuthTypeAgent,
		AgentName:      t.Name,
		TokenPrefix:    t.TokenPrefix,
		AllowedServers: t.AllowedServers,
		Permissions:    t.Permissions,
		ProfilePin:     t.ProfilePin,
		Email:          t.OwnerEmail,
		Provider:       t.OwnerProvider,
		Role:           t.OwnerRole,
		CredentialKind: CredentialKindAgentToken,
		// UserID carries the owning tenant (server edition). Without it an
		// agent-token request had no tenant identity at all, so its activity
		// could not be attributed or scoped. It does NOT confer the user tier:
		// Type stays AuthTypeAgent, so IsUser()/IsAdmin() remain false, and
		// every per-user surface must gate on IsUser() rather than on a
		// non-empty UserID.
		UserID: t.UserID,
	}
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
