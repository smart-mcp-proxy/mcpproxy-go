package runtime

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/mcplogin"
)

// The name sent at initialize goes through the same normalisation as the name
// sent at registration: an app that registers and initializes as the same
// Cf-containing name is not flagged, and no Cf character is stored or listed.
func TestOAuthTouch_ObservedNameLosesFormatCharacters(t *testing.T) {
	zwsp := string(rune(0x200b))
	rlo := string(rune(0x202e))
	name := "Ac" + zwsp + "me"

	fx := newOAuthFx(t)
	fx.mintClient("acme", "ro", auth.ProfileModeLocked)
	resp, rerr, err := fx.svc.Register(mcplogin.RegistrationRequest{RedirectURIs: []string{oauthTestRedirect}, ClientName: name})
	require.NoError(t, err)
	require.Nil(t, rerr)
	reg := resp["client_id"].(string)
	id := fx.authorize(reg, "acme")
	rev, _ := fx.sm.ClientBindingRevision("acme")
	redirect, err := fx.svc.Approve(fx.ctx(), id, ApprovalChoice{BindingRevision: rev})
	require.NoError(t, err)
	_, terr := fx.svc.Exchange(fx.tokenInput(reg, oauthCodeOf(t, redirect)))
	require.Nil(t, terr)
	grant := fx.grants()[0]

	fx.svc.Touch(grant.GrantID, name)
	require.Eventually(t, func() bool { return fx.grants()[0].ObservedClientName != "" }, 2*time.Second, 10*time.Millisecond)
	observed := fx.grants()[0].ObservedClientName
	assert.Equal(t, "acme", observed, "no format character is stored")
	assert.False(t, OAuthNameMismatch(observed, "acme", "acme"), "the same identity is not flagged")

	// Already-stored or direct input is normalised too, and a bidi override
	// cannot make an unchanged name look different.
	assert.False(t, OAuthNameMismatch(name, "acme", "acme"))
	assert.False(t, OAuthNameMismatch("ac"+rlo+"me", "acme", "acme"))
	assert.True(t, OAuthNameMismatch("other"+zwsp, "acme", "acme"))
}
