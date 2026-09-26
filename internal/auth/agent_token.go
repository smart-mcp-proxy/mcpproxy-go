package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// errMalformedCredential is the exact FR-021 refusal text returned by
// ValidateTokenInvariants for every violation (contracts/refusals.md:
// `401 {"error":"Agent token invalid: malformed credential record"}`).
var errMalformedCredential = errors.New("malformed credential record")

// Token prefix used for all agent tokens.
const TokenPrefixStr = "mcp_agt_"

// ClientTokenPrefixStr is the secret prefix for a per-client credential
// (Spec 108-c, FR-021). Same length and HMAC-SHA256 scheme as a regular
// agent token, so the kind is carried by the secret itself: a pre-108
// binary, which only recognises TokenPrefixStr, never treats a client
// credential as a wildcard agent token (rollback safety, research D27).
const ClientTokenPrefixStr = "mcp_cli_"

// Kind values for AgentToken.Kind (data-model.md §3). The empty string is
// the legacy default and is always equivalent to KindAgent.
const (
	KindAgent  = "agent"
	KindClient = "client"
)

// ProfileMode values for a client credential's AgentToken.ProfileMode
// (data-model.md §3). Applies to Kind=client only; empty/not-applicable for
// every regular token.
const (
	ProfileModeLocked     = "locked"
	ProfileModeSwitchable = "switchable"
)

// clientIDPattern is the FR-021 client_id format: lower-case letters,
// digits, '-' or '_', starting with a letter or digit, at most 56 characters
// — so "client-"+client_id always satisfies the token-name rule
// ([A-Za-z0-9][A-Za-z0-9_-]{0,63}) and is a single safe path segment.
var clientIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,55}$`)

// ValidClientID reports whether id matches the FR-021 client_id pattern.
func ValidClientID(id string) bool {
	return clientIDPattern.MatchString(id)
}

// ClientTokenName is the FR-021 token name for a client credential.
func ClientTokenName(clientID string) string {
	return "client-" + clientID
}

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

	// Kind, ClientID and ProfileMode are the Spec 108-c client-credential
	// fields (data-model.md §3). Kind is ""/"agent" for a regular token,
	// "client" for a per-client credential; any other value is invalid
	// (fail closed at authentication, FR-021). ClientID and ProfileMode are
	// set iff Kind=client.
	Kind        string `json:"kind,omitempty"`
	ClientID    string `json:"client_id,omitempty"`
	ProfileMode string `json:"profile_mode,omitempty"`

	// PendingHash/PendingPrefix/RotationStartedAt track an in-progress
	// staged rotation (FR-021a, Kind=client only): both the old secret's
	// hash (the record's own TokenHash) and PendingHash authenticate while
	// a rotation is staged, until finalize promotes PendingHash and clears
	// these fields.
	PendingHash       string     `json:"pending_hash,omitempty"`
	PendingPrefix     string     `json:"pending_prefix,omitempty"`
	RotationStartedAt *time.Time `json:"rotation_started_at,omitempty"`

	// ConnectedAt is when connect/add minted this credential.
	ConnectedAt *time.Time `json:"connected_at,omitempty"`

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
		TokenKind:      normalizeKind(t.Kind),
		ClientID:       t.ClientID,
		ProfileMode:    t.ProfileMode,
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

// GenerateClientToken creates a new client credential secret with the
// mcp_cli_ prefix followed by 64 hex characters (32 random bytes) — same
// length and shape as GenerateToken, so hashing and prefix extraction are
// unchanged; only the prefix differs (FR-021).
func GenerateClientToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("failed to generate random bytes: %w", err)
	}
	return ClientTokenPrefixStr + hex.EncodeToString(b), nil
}

// normalizeKind returns the canonical Kind spelling: "" is treated exactly
// like KindAgent everywhere invariants and authentication reason about it.
func normalizeKind(kind string) string {
	if kind == "" {
		return KindAgent
	}
	return kind
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

// ValidateAnyTokenFormat checks a raw secret against BOTH recognised
// prefixes (mcp_agt_ and mcp_cli_, FR-021/FR-023) and reports which kind the
// secret CLAIMS to be, so the caller (storage.ValidateAgentToken) can check
// that claim against the stored record's own Kind (ValidateTokenInvariants)
// rather than trusting the record alone. ok is false for any other shape,
// including a correctly-shaped but foreign prefix.
func ValidateAnyTokenFormat(token string) (kind string, ok bool) {
	if len(token) != 72 {
		return "", false
	}
	prefix := token[:8]
	if _, err := hex.DecodeString(token[8:]); err != nil {
		return "", false
	}
	switch prefix {
	case TokenPrefixStr:
		return KindAgent, true
	case ClientTokenPrefixStr:
		return KindClient, true
	default:
		return "", false
	}
}

// ValidateTokenInvariants is THE fail-closed check for FR-021: every
// AgentToken record must satisfy these invariants before it may authenticate
// a request, checked IDENTICALLY at mint time (self-consistent: claimedKind
// == normalizeKind(t.Kind)) and on every authentication
// (storage.ValidateAgentToken, claimedKind derived from the presented
// secret's prefix via ValidateAnyTokenFormat). Any violation returns the
// exact FR-021 refusal text "malformed credential record" — never a
// downgrade to a regular wildcard agent token.
func ValidateTokenInvariants(t *AgentToken, claimedKind string) error {
	if t == nil {
		return errMalformedCredential
	}
	kind := normalizeKind(t.Kind)
	if kind != KindAgent && kind != KindClient {
		return errMalformedCredential
	}
	// The secret actually presented must match the record it resolved to —
	// a mcp_cli_ secret whose record is not kind=client (and vice versa) is
	// exactly the "malformed" case the spec calls out (never a downgrade).
	if claimedKind != "" && kind != claimedKind {
		return errMalformedCredential
	}
	if kind == KindClient {
		if t.ClientID == "" || !ValidClientID(t.ClientID) {
			return errMalformedCredential
		}
		if t.Name != ClientTokenName(t.ClientID) {
			return errMalformedCredential
		}
		if len(t.AllowedServers) != 1 || t.AllowedServers[0] != "*" {
			return errMalformedCredential
		}
		if !hasAllPermissions(t.Permissions) {
			return errMalformedCredential
		}
		if t.ProfileMode != ProfileModeLocked && t.ProfileMode != ProfileModeSwitchable {
			return errMalformedCredential
		}
		if t.ProfileMode == ProfileModeLocked && t.ProfilePin == "" {
			return errMalformedCredential
		}
		return nil
	}
	// kind == KindAgent (legacy pinned or unpinned token): none of the
	// client-only fields may be set. A legacy unpinned token (no mode, no
	// pin) and a legacy pinned token (no mode, a pin) are both VALID and
	// keep their exact existing semantics.
	if t.ClientID != "" || t.ProfileMode != "" || t.PendingHash != "" {
		return errMalformedCredential
	}
	return nil
}

// hasAllPermissions reports whether perms is exactly the three permission
// tiers (order-independent, no duplicates) — the invariant a client
// credential's Permissions must satisfy (FR-021: "scope comes from the
// profile only").
func hasAllPermissions(perms []string) bool {
	if len(perms) != 3 {
		return false
	}
	seen := map[string]bool{}
	for _, p := range perms {
		if !validPermissions[p] || seen[p] {
			return false
		}
		seen[p] = true
	}
	return len(seen) == 3
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
