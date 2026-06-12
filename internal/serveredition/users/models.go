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
	Provider          string    `json:"provider"`            // google, github, microsoft
	ProviderSubjectID string    `json:"provider_subject_id"` // Provider's unique subject ID
	CreatedAt         time.Time `json:"created_at"`
	LastLoginAt       time.Time `json:"last_login_at"`
	Disabled          bool      `json:"disabled"`
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
	validProviders := map[string]bool{"google": true, "github": true, "microsoft": true}
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
