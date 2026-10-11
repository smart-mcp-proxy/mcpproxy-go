package auth

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Spec 115 T004: the lifecycle fields are additive, omitempty, and never
// change ValidateTokenInvariants for an otherwise valid record.
func TestAgentTokenLifecycleFields_RoundTripOmitEmpty(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	tok := AgentToken{
		Name: "research-task-42", ProfilePin: "daily-research", CreatedAt: now, ExpiresAt: now.Add(30 * time.Minute),
		AllowedServers: []string{"*"}, Permissions: []string{PermRead, PermWrite, PermDestructive},
		RevokedAt: &now, Revoked: true,
		Issuer:     &CredentialIssuer{ActorKind: "api_key", Surface: "mcp"},
		Purpose:    "digest the library",
		GuardBound: true,
	}
	raw, err := json.Marshal(tok)
	require.NoError(t, err)
	for _, key := range []string{`"revoked_at"`, `"issuer"`, `"purpose"`, `"guard_bound":true`, `"actor_kind":"api_key"`, `"surface":"mcp"`} {
		assert.Contains(t, string(raw), key)
	}
	var back AgentToken
	require.NoError(t, json.Unmarshal(raw, &back))
	assert.Equal(t, tok.Purpose, back.Purpose)
	assert.Equal(t, *tok.Issuer, *back.Issuer)
	assert.True(t, back.GuardBound)
	require.NotNil(t, back.RevokedAt)
	assert.True(t, back.RevokedAt.Equal(now))

	// Absent fields are omitted entirely.
	plain, err := json.Marshal(AgentToken{Name: "x"})
	require.NoError(t, err)
	for _, key := range []string{"revoked_at", "issuer", "purpose", "guard_bound"} {
		assert.NotContains(t, string(plain), key)
	}
}

func TestAgentTokenLifecycleFields_LegacyRecordDecodesUnchanged(t *testing.T) {
	legacy := `{"name":"ci","token_hash":"abc","token_prefix":"mcp_agt_1234","allowed_servers":["*"],"permissions":["read"],"expires_at":"2027-01-01T00:00:00Z","created_at":"2026-01-01T00:00:00Z","revoked":true}`
	var tok AgentToken
	require.NoError(t, json.Unmarshal([]byte(legacy), &tok))
	assert.Nil(t, tok.RevokedAt)
	assert.Nil(t, tok.Issuer)
	assert.Empty(t, tok.Purpose)
	assert.False(t, tok.GuardBound)
	assert.NoError(t, ValidateTokenInvariants(&tok, KindAgent))
}

func TestAgentTokenLifecycleFields_InvariantsIgnoreDisplayFields(t *testing.T) {
	now := time.Now().UTC()
	client := AgentToken{
		Name: ClientTokenName("w1"), Kind: KindClient, ClientID: "w1", ProfileMode: ProfileModeLocked, ProfilePin: "p",
		AllowedServers: []string{"*"}, Permissions: []string{PermRead, PermWrite, PermDestructive},
		CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}
	require.NoError(t, ValidateTokenInvariants(&client, KindClient))
	client.Issuer = &CredentialIssuer{ActorKind: "api_key", Surface: "mcp"}
	client.Purpose = strings.Repeat("x", MaxCredentialPurpose)
	client.RevokedAt = &now
	assert.NoError(t, ValidateTokenInvariants(&client, KindClient), "issuer/purpose/revoked_at never make a valid record invalid")

	// GuardBound: agent tokens only, and only with a pin.
	client.GuardBound = true
	assert.Error(t, ValidateTokenInvariants(&client, KindClient))
	agent := AgentToken{Name: "t", GuardBound: true}
	assert.Error(t, ValidateTokenInvariants(&agent, KindAgent), "a guard-bound token without a pin is malformed")
	agent.ProfilePin = "p"
	assert.NoError(t, ValidateTokenInvariants(&agent, KindAgent))
	assert.Equal(t, 500, MaxCredentialPurpose)
}

func TestAgentTokenIsLease(t *testing.T) {
	now := time.Now().UTC()
	at := func(d time.Duration) *AgentToken { return &AgentToken{CreatedAt: now, ExpiresAt: now.Add(d)} }
	assert.True(t, at(24*time.Hour).IsLease(), "exactly 24h is a lease")
	assert.False(t, at(24*time.Hour+time.Second).IsLease())
	assert.True(t, at(30*time.Minute).IsLease())
	assert.False(t, (&AgentToken{CreatedAt: now}).IsLease(), "no expiry is never a lease")
}

// Spec 115 T009a / A16: the day count is bounded before it is multiplied, so
// no input can overflow time.Duration and mint an already-expired token.
func TestParseTokenExpiry_DayCountOverflow(t *testing.T) {
	now := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	for _, in := range []string{"106752d", "106751d", "366d", "9223372036854775807d", "9999999999999d", "9999999999d"} {
		_, err := ParseTokenExpiry(in, now)
		require.Error(t, err, in)
		assert.Equal(t, "expiry duration cannot exceed 365 days", err.Error(), in)
	}
	for _, in := range []string{"-1d", "0d"} {
		_, err := ParseTokenExpiry(in, now)
		require.Error(t, err, in)
	}
	for in, want := range map[string]time.Duration{"365d": 365 * 24 * time.Hour, "7d": 7 * 24 * time.Hour, "90s": 90 * time.Second, "8760h": 8760 * time.Hour} {
		got, err := ParseTokenExpiry(in, now)
		require.NoError(t, err, in)
		assert.Equal(t, now.Add(want), got, in)
	}
	_, err := ParseTokenExpiry("8761h", now)
	require.Error(t, err)
	_, err = ParseTokenExpiry("0s", now)
	require.Error(t, err)
	_, err = ParseTokenExpiry("-1h", now)
	require.Error(t, err)
}
