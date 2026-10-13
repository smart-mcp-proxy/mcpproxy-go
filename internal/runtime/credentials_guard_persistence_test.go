package runtime

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Spec 115 T013a (FR-012a): an MCP-issued token stays a standing binding. A
// config write through the API funnel that would let a caller escape it by
// omitting the credential is refused, naming the token, until it is revoked.
// A REST/CLI token with a pin never participates (A13).
func TestCredentialsGuard_TokenIsStandingBinding(t *testing.T) {
	rt := newFunnelRuntime(t)
	rt.SetBindingGuard(ConservativeBindingGuard{})
	ctx := context.Background()
	cs := rt.CredentialsService()
	require.NotNil(t, cs)

	// A REST token with a pin first: it must not block anything (A13).
	_, err := cs.IssueToken(ctx, funnelActor(), IssueTokenRequest{Name: "rest-pinned", ProfilePin: "ro", Expiry: ExpiryTokenDefault})
	require.NoError(t, err)
	authOff := func(d *config.Config) (ChangeHint, error) { d.RequireMCPAuth = false; return ChangeHint{}, nil }
	_, _, err = rt.MutateConfig(ctx, funnelActor(), authOff, TokenRewrite{})
	require.NoError(t, err, "a REST token is not a standing binding")
	_, _, err = rt.MutateConfig(ctx, funnelActor(), func(d *config.Config) (ChangeHint, error) { d.RequireMCPAuth = true; return ChangeHint{}, nil }, TokenRewrite{})
	require.NoError(t, err)

	_, err = cs.IssueToken(ctx, mcpActor(), mcpTokenReq("confined-1", "ro", "1h"))
	require.NoError(t, err)

	_, _, err = rt.MutateConfig(ctx, funnelActor(), authOff, TokenRewrite{})
	var ge *BindingGuardError
	require.ErrorAs(t, err, &ge)
	require.Len(t, ge.Bindings, 1)
	assert.Equal(t, "confined-1", ge.Bindings[0].TokenName)
	assert.Equal(t, "", ge.Bindings[0].ClientID)
	assert.Equal(t, "locked", ge.Bindings[0].Mode)
	cur, err := rt.GetDesiredConfig()
	require.NoError(t, err)
	assert.True(t, config.EffectiveRequireMCPAuth(cur), "the refused write changed nothing")

	next, err := rt.GetDesiredConfig()
	require.NoError(t, err)
	next.RequireMCPAuth = false
	_, err = rt.GuardedApplyConfig(next, rt.funnelConfigPath())
	require.ErrorAs(t, err, &ge, "GuardedApplyConfig refuses the same way")

	// Revoked: the binding stops counting.
	_, err = cs.Revoke(ctx, mcpActor(), CredentialRef{Token: "confined-1"}, false)
	require.NoError(t, err)
	_, _, err = rt.MutateConfig(ctx, funnelActor(), authOff, TokenRewrite{})
	require.NoError(t, err)
}

// T013a issuance half: under auth off the MCP path refuses to issue a token
// the worker could escape (FR-012); REST still issues (A9).
func TestCredentialsGuard_IssueRefusedWhenBypassable(t *testing.T) {
	rt := newFunnelRuntime(t)
	rt.SetBindingGuard(ConservativeBindingGuard{})
	ctx := context.Background()
	_, _, err := rt.MutateConfig(ctx, funnelActor(), func(d *config.Config) (ChangeHint, error) { d.RequireMCPAuth = false; return ChangeHint{}, nil }, TokenRewrite{})
	require.NoError(t, err)
	cs := rt.CredentialsService()
	_, err = cs.IssueToken(ctx, mcpActor(), mcpTokenReq("w", "ro", "1h"))
	var ge *BindingGuardError
	require.ErrorAs(t, err, &ge)
	_, err = cs.IssueClient(ctx, mcpActor(), mcpClientReq("w", "ro", "1h"))
	require.ErrorAs(t, err, &ge)
	toks, err := rt.StorageManager().ListAgentTokens()
	require.NoError(t, err)
	assert.Empty(t, toks, "nothing minted")
	_, err = cs.IssueToken(ctx, funnelActor(), IssueTokenRequest{Name: "rest", ProfilePin: "ro", Expiry: ExpiryTokenDefault})
	assert.NoError(t, err, "REST keeps issuing without the guard (A9)")
}
