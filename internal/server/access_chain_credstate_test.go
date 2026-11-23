package server

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
)

// A never-expiring token (zero ExpiresAt) authenticates, so the explainer must
// not report it expired (SC-009: callable must match the real call outcome).
func TestCredentialStateOf_NeverExpiringTokenIsNotExpired(t *testing.T) {
	now := time.Now()
	forever := &auth.AgentToken{Name: "forever"}
	assert.NotEqual(t, profile.CredentialStateExpired, credentialStateOf(forever, now))
	assert.NotEqual(t, profile.CredentialStateRevoked, credentialStateOf(forever, now))

	past := &auth.AgentToken{Name: "old", ExpiresAt: now.Add(-time.Hour)}
	assert.Equal(t, profile.CredentialStateExpired, credentialStateOf(past, now))

	revoked := &auth.AgentToken{Name: "r", Revoked: true}
	assert.Equal(t, profile.CredentialStateRevoked, credentialStateOf(revoked, now))
}
