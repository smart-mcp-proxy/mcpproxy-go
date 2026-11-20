package runtime

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Issue #1698: regenerating an agent token minted a fresh secret with no
// record at all. It now goes through the credentials service and writes one
// `profile_change` row with change "rotate", the actor, and the old and new
// token PREFIXES (never a secret), the shape client rotation already has.

func issueRESTToken(t *testing.T, h *credHarness, name string) *IssuedCredentialResult {
	t.Helper()
	res, err := h.cs.IssueToken(context.Background(), h.actor(), IssueTokenRequest{
		Name: name, AllowedServers: []string{"*"}, Permissions: []string{"read"}, Expiry: ExpiryTokenDefault,
	})
	require.NoError(t, err)
	return res
}

func TestCredentialsService_Regenerate_WritesRotateRecord(t *testing.T) {
	h := newCredHarness(t)
	issued := issueRESTToken(t, h, "ci-bot")
	require.Len(t, h.changes(), 1, "the issue row")

	actor := Actor{Kind: "api_key", Surface: profile.SurfaceCLI}
	res, err := h.cs.Regenerate(context.Background(), actor, "ci-bot")
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Equal(t, "ci-bot", res.View.TokenName)
	assert.True(t, auth.ValidateTokenFormat(res.Secret))
	assert.NotEqual(t, issued.Secret, res.Secret)

	ch := h.changes()
	require.Len(t, ch, 2)
	rot := ch[1]
	assert.Equal(t, "rotate", rot["change"])
	assert.Equal(t, "ci-bot", rot["token_name"])
	assert.Equal(t, "api_key", rot["actor_kind"])
	assert.Equal(t, "cli", rot["surface"])
	diff, _ := rot["diff"].(map[string]interface{})
	require.NotNil(t, diff)
	assert.Equal(t, issued.View.TokenPrefix, diff["old_token_prefix"])
	assert.Equal(t, auth.TokenPrefix(res.Secret), diff["new_token_prefix"])
	assert.NotContains(t, fmt.Sprint(rot), res.Secret, "the record never carries the secret")
	assert.NotContains(t, fmt.Sprint(rot), issued.Secret)

	// The store really rotated: the old secret is dead, the new one works.
	assert.False(t, h.authenticates(issued.Secret))
	assert.True(t, h.authenticates(res.Secret))
}

func TestCredentialsService_Regenerate_UnknownTokenWritesNothing(t *testing.T) {
	h := newCredHarness(t)
	_, err := h.cs.Regenerate(context.Background(), h.actor(), "ghost")
	require.Error(t, err)
	assert.Equal(t, profile.CredentialErrorCodeIdentityNotFound, codeOf(err))
	assert.Empty(t, h.changes())
}

func TestCredentialsService_Regenerate_RevokedTokenRefused(t *testing.T) {
	h := newCredHarness(t)
	issueRESTToken(t, h, "old-bot")
	_, err := h.cs.Revoke(context.Background(), h.actor(), CredentialRef{Token: "old-bot"}, false)
	require.NoError(t, err)
	before := len(h.changes())

	_, err = h.cs.Regenerate(context.Background(), h.actor(), "old-bot")
	require.Error(t, err)
	assert.True(t, errors.Is(err, storage.ErrAgentTokenRevoked), "the store's sentinel survives so the REST door keeps its 409: %v", err)
	assert.Len(t, h.changes(), before, "a refused rotation writes no record")
}

func TestCredentialsService_Regenerate_ClientCredentialRefused(t *testing.T) {
	h := newCredHarness(t)
	h.mint("w1", "ro", nil)
	before := len(h.changes())

	_, err := h.cs.Regenerate(context.Background(), h.actor(), "client-w1")
	require.Error(t, err)
	assert.True(t, errors.Is(err, storage.ErrClientCredentialRegenerateRefused), "%v", err)
	assert.Len(t, h.changes(), before)
}
