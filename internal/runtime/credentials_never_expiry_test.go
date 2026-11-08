package runtime

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
)

// An admin minting an agent token over REST/CLI can pass expires_in "never":
// the token is stored with a zero ExpiresAt, which kind=agent tokens already
// read as "never expires", and it authenticates.
func TestCredentialsService_IssueToken_NeverExpires(t *testing.T) {
	h := newCredHarness(t)
	h.cfg.RequireMCPAuth = false
	res, err := h.cs.IssueToken(context.Background(), funnelActor(), IssueTokenRequest{
		Name: "forever", AllowedServers: []string{"a"}, Permissions: []string{"read"},
		ExpiresIn: auth.TokenExpiryNever, Expiry: ExpiryTokenDefault,
	})
	require.NoError(t, err)
	if res.View.ExpiresAt != nil {
		assert.True(t, res.View.ExpiresAt.IsZero(), "a never-expiring token reports no expiry, got %v", *res.View.ExpiresAt)
	}

	tok, err := h.sm.GetAgentTokenByName("forever")
	require.NoError(t, err)
	require.NotNil(t, tok)
	assert.True(t, tok.ExpiresAt.IsZero(), "stored with a zero ExpiresAt")
	assert.False(t, tok.IsExpired())
}

// The MCP create_token door still requires a real duration: an agent cannot
// mint itself a credential that never expires.
func TestCredentialsService_IssueToken_NeverIsRefusedOverMCP(t *testing.T) {
	h := newCredHarness(t)
	_, err := h.cs.IssueToken(context.Background(), mcpActor(), mcpTokenReq("forever", "ro", auth.TokenExpiryNever))
	require.Error(t, err)
	tok, _ := h.sm.GetAgentTokenByName("forever")
	assert.Nil(t, tok, "no token was stored")
}

// A duration is still capped at 365 days; "never" is the only way past it.
func TestCredentialsService_IssueToken_DurationCapUnchanged(t *testing.T) {
	h := newCredHarness(t)
	h.cfg.RequireMCPAuth = false
	_, err := h.cs.IssueToken(context.Background(), funnelActor(), IssueTokenRequest{
		Name: "too-long", Permissions: []string{"read"}, ExpiresIn: "400d", Expiry: ExpiryTokenDefault,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "365 days")
}
