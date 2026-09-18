//go:build server

package users

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/oklog/ulid/v2"
)

// User represents an authenticated team member.
type User struct {
	ID                string    `json:"id"`                  // ULID
	Email             string    `json:"email"`               // From OAuth provider
	DisplayName       string    `json:"display_name"`        // From OAuth provider
	Provider          string    `json:"provider"`            // google, github, microsoft, oidc
	ProviderSubjectID string    `json:"provider_subject_id"` // Provider's unique subject ID
	CreatedAt         time.Time `json:"created_at"`
	LastLoginAt       time.Time `json:"last_login_at"`
	Disabled          bool      `json:"disabled"`

	// Groups holds the groups claim of the last successful `oidc` login,
	// replaced wholesale on every login (Spec 107 FR-008). Legacy providers
	// store []. An upgraded record decodes as nil until its next login.
	Groups []string `json:"groups"`
	// GroupsUpdatedAt is stamped by the login write that set Groups; zero on
	// an upgraded record that has not logged in since.
	GroupsUpdatedAt time.Time `json:"groups_updated_at,omitzero"`
	// SubjectRebindArmedAt is the administrator-opened rebind window (FR-023):
	// set by a real disabled→enabled transition, cleared by disable and by the
	// first successful login while set (which then accepts a new subject).
	// Nil = closed; absent on every upgraded record.
	SubjectRebindArmedAt *time.Time `json:"subject_rebind_armed_at,omitempty"`
}

// NewUser creates a new User with a generated ULID.
func NewUser(email, displayName, provider, providerSubjectID string) *User {
	now := time.Now().UTC()
	return &User{
		ID:                ulid.Make().String(),
		Email:             strings.ToLower(strings.TrimSpace(email)),
		DisplayName:       displayName,
		Provider:          provider,
		ProviderSubjectID: providerSubjectID,
		CreatedAt:         now,
		LastLoginAt:       now,
	}
}

// Validate checks the user has required fields.
func (u *User) Validate() error {
	if u.ID == "" {
		return fmt.Errorf("user ID is required")
	}
	if u.Email == "" {
		return fmt.Errorf("user email is required")
	}
	validProviders := map[string]bool{"google": true, "github": true, "microsoft": true, "oidc": true}
	if !validProviders[u.Provider] {
		return fmt.Errorf("invalid provider: %s", u.Provider)
	}
	if u.ProviderSubjectID == "" {
		return fmt.Errorf("provider subject ID is required")
	}
	return nil
}

// Session represents an active authenticated session.
type Session struct {
	ID          string    `json:"id"`           // UUID
	UserID      string    `json:"user_id"`      // Reference to User.ID
	BearerToken string    `json:"bearer_token"` // JWT for MCP/API access
	CreatedAt   time.Time `json:"created_at"`
	ExpiresAt   time.Time `json:"expires_at"`
	UserAgent   string    `json:"user_agent,omitempty"`
	IPAddress   string    `json:"ip_address,omitempty"`
	// CookieSecure records whether the session cookie was set with the Secure
	// attribute (Spec 107 FR-026) so logout clears it with the same attribute.
	CookieSecure bool `json:"cookie_secure,omitempty"`
}

// NewSession creates a new Session with a generated UUID.
func NewSession(userID string, ttl time.Duration) *Session {
	now := time.Now().UTC()
	return &Session{
		ID:        uuid.New().String(),
		UserID:    userID,
		CreatedAt: now,
		ExpiresAt: now.Add(ttl),
	}
}

// IsExpired checks if the session has expired.
func (s *Session) IsExpired() bool {
	return time.Now().UTC().After(s.ExpiresAt)
}

// Validate checks the session has required fields.
func (s *Session) Validate() error {
	if s.ID == "" {
		return fmt.Errorf("session ID is required")
	}
	if s.UserID == "" {
		return fmt.Errorf("session user ID is required")
	}
	if s.ExpiresAt.IsZero() {
		return fmt.Errorf("session expiry is required")
	}
	return nil
}

// isLoginSession reports whether this really is a user login session, rather
// than a foreign record that merely unmarshalled without error.
//
// JSON unmarshalling accepts any shape and leaves unknown fields zero, so a
// record belonging to someone else looks like a session with no user and a zero
// expiry. Treating that as an expired session is how the sweep used to delete
// data that was never its own.
func (s *Session) isLoginSession() bool {
	return s.UserID != "" && !s.ExpiresAt.IsZero()
}

// LoginClaims carries the VERIFIED identity of one successful login attempt
// into UpdateUserLogin (Spec 107 FR-008/FR-023): only values that passed the
// provider's checks may be placed here. Groups is the captured groups claim
// (nil or empty → stored as []); GroupsKnown reports whether the login
// captured groups at all — false leaves the stored list untouched.
type LoginClaims struct {
	Email       string
	Provider    string
	Subject     string
	Name        string
	AvatarURL   string
	Groups      []string
	GroupsKnown bool
}

// LoginOutcome is what UpdateUserLogin decided inside its transaction.
type LoginOutcome struct {
	// User is the record as written (never nil on a nil error).
	User *User
	// Created reports a first login: the record did not exist.
	Created bool
	// Rebound reports that (provider, subject) changed on an existing record —
	// a configured-provider change, or a different subject inside an armed
	// rebind window. The auth_event line carries `provider_rebound`.
	Rebound bool
	// RebindConsumed reports that SubjectRebindArmedAt was set and has been
	// cleared by this write.
	RebindConsumed bool
}
