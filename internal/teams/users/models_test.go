//go:build teams

package users

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewUser_GeneratesULID(t *testing.T) {
	u := NewUser("test@example.com", "Test User", "google", "sub-123")
	assert.NotEmpty(t, u.ID)

	// Verify it's a valid ULID by parsing it.
	_, err := ulid.Parse(u.ID)
	require.NoError(t, err, "ID should be a valid ULID")

	// Two users should get different IDs.
	u2 := NewUser("other@example.com", "Other", "github", "sub-456")
	assert.NotEqual(t, u.ID, u2.ID)
}

func TestNewUser_NormalizesEmail(t *testing.T) {
	u := NewUser("  Alice@Example.COM  ", "Alice", "google", "sub-1")
	assert.Equal(t, "alice@example.com", u.Email)
}

func TestNewUser_SetsTimestamps(t *testing.T) {
	before := time.Now().UTC().Add(-time.Second)
	u := NewUser("test@example.com", "Test", "google", "sub-1")
	after := time.Now().UTC().Add(time.Second)

	assert.False(t, u.CreatedAt.IsZero(), "CreatedAt should be set")
	assert.False(t, u.LastLoginAt.IsZero(), "LastLoginAt should be set")
	assert.True(t, u.CreatedAt.After(before), "CreatedAt should be after test start")
	assert.True(t, u.CreatedAt.Before(after), "CreatedAt should be before test end")
	assert.Equal(t, u.CreatedAt, u.LastLoginAt, "CreatedAt and LastLoginAt should be equal for new user")
}

func TestUser_Validate_Valid(t *testing.T) {
	u := NewUser("test@example.com", "Test User", "google", "sub-123")
	err := u.Validate()
	assert.NoError(t, err)
}

func TestUser_Validate_MissingEmail(t *testing.T) {
	u := NewUser("", "Test User", "google", "sub-123")
	err := u.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "email")
}

func TestUser_Validate_InvalidProvider(t *testing.T) {
	u := NewUser("test@example.com", "Test User", "facebook", "sub-123")
	err := u.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid provider")
}

func TestUser_Validate_MissingProviderSubjectID(t *testing.T) {
	u := NewUser("test@example.com", "Test User", "google", "")
	err := u.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "provider subject ID")
}

func TestUser_JSON_RoundTrip(t *testing.T) {
	original := NewUser("test@example.com", "Test User", "github", "sub-789")
	original.Disabled = true

	data, err := json.Marshal(original)
	require.NoError(t, err)

	var restored User
	err = json.Unmarshal(data, &restored)
	require.NoError(t, err)

	assert.Equal(t, original.ID, restored.ID)
	assert.Equal(t, original.Email, restored.Email)
	assert.Equal(t, original.DisplayName, restored.DisplayName)
	assert.Equal(t, original.Provider, restored.Provider)
	assert.Equal(t, original.ProviderSubjectID, restored.ProviderSubjectID)
	assert.True(t, original.CreatedAt.Equal(restored.CreatedAt), "CreatedAt should survive round-trip")
	assert.True(t, original.LastLoginAt.Equal(restored.LastLoginAt), "LastLoginAt should survive round-trip")
	assert.Equal(t, original.Disabled, restored.Disabled)
}

func TestNewSession_GeneratesUUID(t *testing.T) {
	s := NewSession("user-1", time.Hour)
	assert.NotEmpty(t, s.ID)

	// UUID format: 8-4-4-4-12 hex chars.
	assert.Regexp(t, `^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`, s.ID)

	// Two sessions should get different IDs.
	s2 := NewSession("user-1", time.Hour)
	assert.NotEqual(t, s.ID, s2.ID)
}

func TestNewSession_SetsExpiry(t *testing.T) {
	ttl := 2 * time.Hour
	before := time.Now().UTC()
	s := NewSession("user-1", ttl)
	after := time.Now().UTC()

	// ExpiresAt should be approximately now + ttl.
	expectedMin := before.Add(ttl)
	expectedMax := after.Add(ttl)

	assert.False(t, s.ExpiresAt.Before(expectedMin), "ExpiresAt should be >= now + ttl")
	assert.False(t, s.ExpiresAt.After(expectedMax), "ExpiresAt should be <= now + ttl")
}

func TestSession_IsExpired(t *testing.T) {
	// Session that expired in the past.
	expired := &Session{
		ID:        "sess-1",
		UserID:    "user-1",
		ExpiresAt: time.Now().UTC().Add(-time.Hour),
	}
	assert.True(t, expired.IsExpired())

	// Session that expires in the future.
	active := &Session{
		ID:        "sess-2",
		UserID:    "user-1",
		ExpiresAt: time.Now().UTC().Add(time.Hour),
	}
	assert.False(t, active.IsExpired())
}

func TestSession_Validate_Valid(t *testing.T) {
	s := NewSession("user-1", time.Hour)
	err := s.Validate()
	assert.NoError(t, err)
}

func TestSession_Validate_MissingUserID(t *testing.T) {
	s := NewSession("", time.Hour)
	err := s.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "user ID")
}

func TestSession_Validate_MissingExpiry(t *testing.T) {
	s := &Session{
		ID:     "sess-1",
		UserID: "user-1",
		// ExpiresAt is zero value.
	}
	err := s.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expiry")
}

func TestSession_JSON_RoundTrip(t *testing.T) {
	original := NewSession("user-abc", 30*time.Minute)
	original.BearerToken = "jwt-token-here"
	original.UserAgent = "Mozilla/5.0"
	original.IPAddress = "192.168.1.100"

	data, err := json.Marshal(original)
	require.NoError(t, err)

	var restored Session
	err = json.Unmarshal(data, &restored)
	require.NoError(t, err)

	assert.Equal(t, original.ID, restored.ID)
	assert.Equal(t, original.UserID, restored.UserID)
	assert.Equal(t, original.BearerToken, restored.BearerToken)
	assert.True(t, original.CreatedAt.Equal(restored.CreatedAt), "CreatedAt should survive round-trip")
	assert.True(t, original.ExpiresAt.Equal(restored.ExpiresAt), "ExpiresAt should survive round-trip")
	assert.Equal(t, original.UserAgent, restored.UserAgent)
	assert.Equal(t, original.IPAddress, restored.IPAddress)
}
