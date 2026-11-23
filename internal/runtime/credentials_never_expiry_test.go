package runtime

import (
	"context"
	"testing"
	"time"

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

// Client credentials keep their capped expiry: "never" is not a duration, and
// is refused over REST and MCP alike, with nothing stored.
func TestCredentialsService_IssueClient_NeverIsRefused(t *testing.T) {
	h := newCredHarness(t)
	for name, req := range map[string]IssueClientRequest{
		"mcp":  mcpClientReq("cli-mcp", "ro", auth.TokenExpiryNever),
		"rest": {ID: "cli-rest", ExpiresIn: auth.TokenExpiryNever, Expiry: ExpiryClientCap},
	} {
		_, err := h.cs.IssueClient(context.Background(), mcpActor(), req)
		require.Error(t, err, name)
	}
	all, err := h.sm.ListAgentTokens()
	require.NoError(t, err)
	assert.Empty(t, all, "no credential was stored")
}

// "never" is accepted only as the whole value.
func TestCredentialsService_IssueToken_NeverIsExact(t *testing.T) {
	h := newCredHarness(t)
	h.cfg.RequireMCPAuth = false
	for _, v := range []string{"Never", "never ", "forever"} {
		_, err := h.cs.IssueToken(context.Background(), funnelActor(), IssueTokenRequest{
			Name: "n1", Permissions: []string{"read"}, ExpiresIn: v, Expiry: ExpiryTokenDefault,
		})
		require.Error(t, err, v)
	}
}

// A never-expiring token's view and state: active far in the future, not a lease.
func TestCredentialsService_NeverTokenState(t *testing.T) {
	h := newCredHarness(t)
	h.cfg.RequireMCPAuth = false
	res, err := h.cs.IssueToken(context.Background(), funnelActor(), IssueTokenRequest{
		Name: "forever2", Permissions: []string{"read"}, ExpiresIn: auth.TokenExpiryNever, Expiry: ExpiryTokenDefault,
	})
	require.NoError(t, err)
	assert.False(t, res.View.Lease)
	tok, err := h.sm.GetAgentTokenByName("forever2")
	require.NoError(t, err)
	assert.Equal(t, CredentialStateActive, CredentialStateOf(tok, time.Now().Add(100*365*24*time.Hour)))
}

// A zero ExpiresAt means "never expires" only for a regular agent token. A
// client credential must carry an expiry, so a zero one reads as expired.
func TestCredentialStateOf_ZeroExpiryDependsOnKind(t *testing.T) {
	now := time.Now()
	assert.Equal(t, CredentialStateActive, CredentialStateOf(&auth.AgentToken{Kind: auth.KindAgent}, now))
	assert.Equal(t, CredentialStateActive, CredentialStateOf(&auth.AgentToken{}, now))
	assert.Equal(t, CredentialStateExpired, CredentialStateOf(&auth.AgentToken{Kind: auth.KindClient}, now))
}
