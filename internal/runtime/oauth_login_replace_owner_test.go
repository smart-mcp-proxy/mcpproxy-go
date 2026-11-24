package runtime

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
)

// A login of this registration that belongs to ANOTHER client is never
// replaced by approving a different client: the choice is refused and the
// request stays pending.
func TestOAuthApprove_ReplaceGrantOfAnotherClientIsRefused(t *testing.T) {
	fx := newOAuthFx(t)
	reg, _ := fx.loginNew("cursor", "ro")
	cursorGrant := fx.grants()[0]
	fx.mintClient("codex", "rw", auth.ProfileModeSwitchable)

	id := fx.authorize(reg, "")
	v, err := fx.svc.Describe(fx.ctx(), id)
	require.NoError(t, err)
	require.NotNil(t, v.PreviousLogin)
	assert.Equal(t, "cursor", v.PreviousLogin.ClientID, "the view says which client owns the previous login")

	rev, _ := fx.sm.ClientBindingRevision("codex")
	body := ApprovalChoice{Client: ClientChoice{ID: "codex"}, BindingRevision: rev, ReplaceGrant: cursorGrant.GrantID}
	_, err = fx.svc.Approve(fx.ctx(), id, body)
	var ve *ValidationError
	require.ErrorAs(t, err, &ve)
	assert.Equal(t, "replace_grant", ve.Field)
	_, err = fx.svc.Preview(fx.ctx(), id, body)
	assert.ErrorAs(t, err, &ve)
	assert.Len(t, fx.svc.ListPending(), 1, "refused, still pending")
	for _, g := range fx.grants() {
		assert.False(t, g.Revoked)
	}

	// The owner may replace it.
	cursorRev, _ := fx.sm.ClientBindingRevision("cursor")
	_, err = fx.svc.Approve(fx.ctx(), id, ApprovalChoice{Client: ClientChoice{ID: "cursor"}, BindingRevision: cursorRev, ReplaceGrant: cursorGrant.GrantID})
	assert.NoError(t, err)
}
