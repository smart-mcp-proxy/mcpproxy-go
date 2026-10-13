package auth

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGenerateClientToken_Format pins the mcp_cli_ prefix generator (T027):
// same length and hex shape as a regular agent token, distinct prefix.
func TestGenerateClientToken_Format(t *testing.T) {
	token, err := GenerateClientToken()
	require.NoError(t, err)
	assert.Len(t, token, 72)
	assert.True(t, strings.HasPrefix(token, ClientTokenPrefixStr))
	assert.True(t, ValidateAnyTokenFormatOK(token))
}

func ValidateAnyTokenFormatOK(token string) bool {
	_, ok := ValidateAnyTokenFormat(token)
	return ok
}

func TestValidateAnyTokenFormat(t *testing.T) {
	agentTok, err := GenerateToken()
	require.NoError(t, err)
	clientTok, err := GenerateClientToken()
	require.NoError(t, err)

	kind, ok := ValidateAnyTokenFormat(agentTok)
	require.True(t, ok)
	assert.Equal(t, KindAgent, kind)

	kind, ok = ValidateAnyTokenFormat(clientTok)
	require.True(t, ok)
	assert.Equal(t, KindClient, kind)

	_, ok = ValidateAnyTokenFormat("mcp_xyz_" + strings.Repeat("a", 64))
	assert.False(t, ok, "an unrecognised prefix must not validate")

	_, ok = ValidateAnyTokenFormat("short")
	assert.False(t, ok)
}

// TestAgentToken_KindClientRoundTrip pins Kind/ClientID/ProfileMode
// round-tripping through AuthContext (T027, data-model.md §3).
func TestAgentToken_KindClientRoundTrip(t *testing.T) {
	tok := &AgentToken{
		Name:           "client-cursor",
		Kind:           KindClient,
		ClientID:       "cursor",
		ProfileMode:    ProfileModeLocked,
		ProfilePin:     "work-readonly",
		AllowedServers: []string{"*"},
		Permissions:    []string{PermRead, PermWrite, PermDestructive},
	}
	ac := tok.AuthContext()
	require.NotNil(t, ac)
	assert.Equal(t, KindClient, ac.TokenKind)
	assert.Equal(t, "cursor", ac.ClientID)
	assert.Equal(t, ProfileModeLocked, ac.ProfileMode)
	assert.True(t, ac.IsClientCredential())
}

// TestAgentToken_LegacyTokensRoundTripUnaffected pins that a legacy unpinned
// token (no mode, no pin) and a legacy pinned token (no mode, a pin) are
// both valid and keep their exact existing semantics (T027).
func TestAgentToken_LegacyTokensRoundTripUnaffected(t *testing.T) {
	unpinned := &AgentToken{Name: "agent-1", AllowedServers: []string{"*"}, Permissions: []string{PermRead}}
	pinned := &AgentToken{Name: "agent-2", ProfilePin: "research", AllowedServers: []string{"*"}, Permissions: []string{PermRead}}

	for _, tok := range []*AgentToken{unpinned, pinned} {
		require.NoError(t, ValidateTokenInvariants(tok, KindAgent))
		ac := tok.AuthContext()
		assert.Equal(t, KindAgent, ac.TokenKind)
		assert.Empty(t, ac.ClientID)
		assert.Empty(t, ac.ProfileMode)
		assert.False(t, ac.IsClientCredential())
	}
}

// TestValidateTokenInvariants is the T027a fail-closed malformed-record
// table (the pure, storage-independent half; the bbolt round-trip half is
// internal/server/credential_invariants_test.go). Every row must be refused
// with the exact FR-021 text.
func TestValidateTokenInvariants(t *testing.T) {
	now := time.Now()
	valid := func() *AgentToken {
		return &AgentToken{
			Name:           "client-cursor",
			Kind:           KindClient,
			ClientID:       "cursor",
			ProfileMode:    ProfileModeLocked,
			ProfilePin:     "work-readonly",
			AllowedServers: []string{"*"},
			Permissions:    []string{PermRead, PermWrite, PermDestructive},
			CreatedAt:      now,
			ExpiresAt:      now.Add(24 * time.Hour),
		}
	}

	cases := []struct {
		name        string
		claimedKind string
		mutate      func(*AgentToken)
	}{
		{"missing kind on a client secret", KindClient, func(tok *AgentToken) { tok.Kind = "" }},
		{"kind=client without client_id", KindClient, func(tok *AgentToken) { tok.ClientID = "" }},
		{"client_id fails the pattern", KindClient, func(tok *AgentToken) { tok.ClientID = "Bad_ID!" }},
		{"name does not match client-<id>", KindClient, func(tok *AgentToken) { tok.Name = "client-other" }},
		{"locked with empty pin", KindClient, func(tok *AgentToken) { tok.ProfilePin = "" }},
		{"unknown profile_mode", KindClient, func(tok *AgentToken) { tok.ProfileMode = "bogus" }},
		{"empty profile_mode on a client record", KindClient, func(tok *AgentToken) { tok.ProfileMode = "" }},
		{"unknown kind value", KindClient, func(tok *AgentToken) { tok.Kind = "superadmin" }},
		{"allowed_servers not wildcard-only", KindClient, func(tok *AgentToken) { tok.AllowedServers = []string{"github"} }},
		{"missing a permission tier", KindClient, func(tok *AgentToken) { tok.Permissions = []string{PermRead} }},
		{"mcp_agt_ secret whose record is kind=client", KindAgent, func(tok *AgentToken) {}},
		{"zero-value expiry (F3)", KindClient, func(tok *AgentToken) { tok.ExpiresAt = time.Time{} }},
		{"expiry already past at CreatedAt (F3)", KindClient, func(tok *AgentToken) { tok.ExpiresAt = tok.CreatedAt.Add(-time.Hour) }},
		{"expiry exceeds the 365-day cap (F3)", KindClient, func(tok *AgentToken) {
			tok.ExpiresAt = tok.CreatedAt.Add(10 * 365 * 24 * time.Hour)
		}},
		{"client_id set on a kind=agent record", KindAgent, func(tok *AgentToken) {
			tok.Kind = KindAgent
			tok.ClientID = "cursor"
			tok.Name = "agent-1"
		}},
		{"profile_mode set on a kind=agent record", KindAgent, func(tok *AgentToken) {
			tok.Kind = KindAgent
			tok.ClientID = ""
			tok.Name = "agent-1"
			// ProfileMode stays set from valid() — invalid on a regular token.
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tok := valid()
			tc.mutate(tok)
			err := ValidateTokenInvariants(tok, tc.claimedKind)
			require.Error(t, err)
			assert.Equal(t, "malformed credential record", err.Error())
		})
	}

	// Positive cases: both legacy shapes and a switchable client (no pin
	// required) must validate cleanly.
	require.NoError(t, ValidateTokenInvariants(valid(), KindClient))

	switchable := valid()
	switchable.ProfileMode = ProfileModeSwitchable
	switchable.ProfilePin = ""
	require.NoError(t, ValidateTokenInvariants(switchable, KindClient))

	legacyUnpinned := &AgentToken{Name: "agent-1", AllowedServers: []string{"*"}, Permissions: []string{PermRead}, CreatedAt: now}
	require.NoError(t, ValidateTokenInvariants(legacyUnpinned, KindAgent))
}
