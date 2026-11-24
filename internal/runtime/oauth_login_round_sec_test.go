package runtime

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/mcplogin"
)

func TestOAuthDescribe_FormatCharactersNeverReachThePage(t *testing.T) {
	fx := newOAuthFx(t)
	fx.mintClient("cursor", "ro", auth.ProfileModeLocked)

	// Bidi override inside the suffix: the control is gone; the suffix strip still
	// yields "cursor", so the real cursor is suggested (advice only, and the page
	// shows the stripped name between its own quotes).
	v := fx.suggestFor("cursor ("+string(rune(0x202e))+"x)", "")
	assert.NotContains(t, v.ReportedName, string(rune(0x202e)))
	assert.Equal(t, "cursor (x)", v.ReportedName)
	require.NotNil(t, v.SuggestedClient)
	assert.Equal(t, "cursor", v.SuggestedClient.ID)
	assert.NotContains(t, v.SuggestedNewClient.DisplayName, string(rune(0x202e)))

	// A zero-width character inside the name is removed at registration, so the
	// name IS "cursor": it suggests cursor (the suggestion is only advice), and a
	// new client with that id is still refused as reserved_identity.
	v = fx.suggestFor("cur"+string(rune(0x200b))+"sor", "")
	assert.Equal(t, "cursor", v.ReportedName)
	require.NotNil(t, v.SuggestedClient)
	assert.Equal(t, "cursor", v.SuggestedClient.ID)
	assert.NotContains(t, v.SuggestedNewClient.ID, string(rune(0x200b)))
	_, err := fx.svc.Approve(fx.ctx(), v.ID, ApprovalChoice{Client: ClientChoice{New: &NewClientChoice{ID: "cursor"}}, Profile: oauthStrp("ro")})
	assert.ErrorIs(t, err, ErrOAuthReservedIdentity)
}

func TestOAuthApprove_NewClientDisplayNameLosesFormatCharacters(t *testing.T) {
	fx := newOAuthFx(t)
	v := fx.suggestFor("Acme", "")
	req, _, err := fx.svc.pendingRequest(v.ID)
	require.NoError(t, err)
	rc, err := fx.svc.resolveChoice(req, ApprovalChoice{
		Client:  ClientChoice{New: &NewClientChoice{ID: "acme-x", DisplayName: "Ac" + string(rune(0x202e)) + "me" + string(rune(0x200b)) + " " + string(rune(0x2066)) + "Agent" + string(rune(0x2069))}},
		Profile: oauthStrp("ro"),
	})
	require.NoError(t, err)
	assert.Equal(t, "Acme Agent", rc.display)
}

// With logins of one registration under two clients, every client's own newest
// live login is offered, not only the newest overall.
func TestOAuthDescribe_PreviousLoginsPerClient(t *testing.T) {
	fx := newOAuthFx(t)
	fx.mintClient("cursor", "ro", auth.ProfileModeLocked)
	fx.mintClient("codex", "rw", auth.ProfileModeSwitchable)
	reg := fx.register()
	login := func(client string) {
		t.Helper()
		fx.clock.Advance(1_000_000_000)
		id := fx.authorize(reg, client)
		rev, _ := fx.sm.ClientBindingRevision(client)
		redirect, err := fx.svc.Approve(fx.ctx(), id, ApprovalChoice{BindingRevision: rev})
		require.NoError(t, err)
		_, terr := fx.svc.Exchange(fx.tokenInput(reg, oauthCodeOf(t, redirect)))
		require.Nil(t, terr)
	}
	login("cursor") // older
	login("codex")  // newer

	id := fx.authorize(reg, "")
	v, err := fx.svc.Describe(fx.ctx(), id)
	require.NoError(t, err)
	require.NotNil(t, v.PreviousLogin)
	assert.Equal(t, "codex", v.PreviousLogin.ClientID, "previous_login stays the newest overall")
	byClient := map[string]string{}
	for _, p := range v.PreviousLogins {
		byClient[p.ClientID] = p.GrantID
	}
	assert.Len(t, byClient, 2)
	assert.NotEmpty(t, byClient["cursor"])
	assert.NotEmpty(t, byClient["codex"])
	assert.NotEqual(t, byClient["cursor"], byClient["codex"])

	// A per-client address lists only that client's login.
	id = fx.authorize(reg, "cursor")
	v, err = fx.svc.Describe(fx.ctx(), id)
	require.NoError(t, err)
	require.Len(t, v.PreviousLogins, 1)
	assert.Equal(t, "cursor", v.PreviousLogins[0].ClientID)
	_ = mcplogin.ResourcePath
}
