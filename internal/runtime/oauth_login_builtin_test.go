package runtime

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A built-in client with no API key and no login yet (#1971: VS Code reports
// "visual studio code") is offered on the generic address and matched by its
// known names, so the page suggests it instead of "New client".
func TestOAuthDescribe_BuiltinClientWithoutRecordsIsOfferedAndSuggested(t *testing.T) {
	fx := newOAuthFx(t)

	v := fx.suggestFor("visual studio code", "")
	require.NotNil(t, v.SuggestedClient)
	assert.Equal(t, "vscode", v.SuggestedClient.ID)
	assert.Equal(t, "known_name", v.SuggestedClient.Matched)

	var found *ExistingClientView
	for i := range v.ExistingClients {
		if v.ExistingClients[i].ID == "vscode" {
			found = &v.ExistingClients[i]
		}
	}
	require.NotNil(t, found, "the picker must list built-in clients without records")
	assert.True(t, found.Builtin)
	assert.Nil(t, found.Binding)

	// Unsupported registry entries are not offered.
	for _, c := range v.ExistingClients {
		if c.Builtin {
			assert.NotNil(t, fx.svc.builtinClient(c.ID), c.ID)
		}
	}
}

// Choosing that built-in client attaches the login to it; the profile is asked
// like for a new client, and nothing is stored before the exchange.
func TestOAuthApprove_BuiltinClientWithoutRecords(t *testing.T) {
	fx := newOAuthFx(t)
	reg := fx.register()
	id := fx.authorize(reg, "")
	_, err := fx.svc.Approve(fx.ctx(), id, ApprovalChoice{Client: ClientChoice{ID: "vscode"}})
	require.Error(t, err, "a profile must be chosen when profiles exist")

	redirect, err := fx.svc.Approve(fx.ctx(), id, ApprovalChoice{Client: ClientChoice{ID: "vscode"}, Profile: oauthStrp("ro")})
	require.NoError(t, err)
	fx.nothingStored() // approval stores nothing: the exchange is the commit
	_, terr := fx.svc.Exchange(fx.tokenInput(reg, oauthCodeOf(t, redirect)))
	require.Nil(t, terr)
	g := fx.grants()
	require.Len(t, g, 1)
	assert.Equal(t, "vscode", g[0].Client)
	assert.True(t, fx.svc.hasClientRecords("vscode"))
}

// A registry id the admin could not bind (unknown) stays refused, and the
// "new client" form still cannot claim a built-in id.
func TestOAuthApprove_UnknownIDStillRefused(t *testing.T) {
	fx := newOAuthFx(t)
	reg := fx.register()
	id := fx.authorize(reg, "")
	_, err := fx.svc.Approve(fx.ctx(), id, ApprovalChoice{Client: ClientChoice{ID: "no-such-client"}, Profile: oauthStrp("ro")})
	assert.ErrorIs(t, err, ErrOAuthClientUnknown)
	_, err = fx.svc.Approve(fx.ctx(), id, ApprovalChoice{Client: ClientChoice{New: &NewClientChoice{ID: "vscode"}}, Profile: oauthStrp("ro")})
	assert.ErrorIs(t, err, ErrOAuthReservedIdentity)
}

// A recorded client wins over a built-in without records when both match the
// reported name, so adding built-in candidates never silences a suggestion that
// was made before.
func TestOAuthDescribe_RecordedClientBeatsBuiltinInSuggestion(t *testing.T) {
	fx := newOAuthFx(t)
	fx.addCustom("my-vscode", "VS Code")

	s := fx.suggestFor("Visual Studio Code", "").SuggestedClient
	require.NotNil(t, s) // control: only the built-in matches this spelling
	assert.Equal(t, "vscode", s.ID)

	s = fx.suggestFor("VS Code", "").SuggestedClient
	require.NotNil(t, s)
	assert.Equal(t, "my-vscode", s.ID)
}

// A binding revision from a page that saw the client WITH a binding must not
// turn into a new-client approval after the client's records are gone.
func TestOAuthApprove_StaleRevisionForVanishedBuiltinIsRefused(t *testing.T) {
	fx := newOAuthFx(t)
	reg := fx.register()
	id := fx.authorize(reg, "")
	_, err := fx.svc.Approve(fx.ctx(), id, ApprovalChoice{
		Client: ClientChoice{ID: "vscode"}, Profile: oauthStrp(""), BindingRevision: "some-revision"})
	assert.ErrorIs(t, err, ErrOAuthBindingChanged)
}
