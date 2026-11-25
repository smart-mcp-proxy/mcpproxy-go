package runtime

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
)

// A built-in client with no API key and no login yet (#1971: VS Code reports
// "visual studio code") is offered on the generic address and matched by its
// known names, so the page suggests it instead of "New client".
func TestOAuthDescribe_BuiltinClientWithoutRecordsIsOfferedAndSuggested(t *testing.T) {
	fx := newOAuthFx(t)

	v := fx.suggestFor("visual studio code", "")
	// VS Code registers as "Visual Studio Code" on both harnesses, so the name
	// is ambiguous between vscode (Local harness) and copilot (the default
	// harness); with no other evidence the default harness is suggested.
	require.NotNil(t, v.SuggestedClient)
	assert.Equal(t, "copilot", v.SuggestedClient.ID)
	assert.Equal(t, "known_name", v.SuggestedClient.Matched)
	assert.True(t, hasExisting(v, "vscode"), "both stay choosable")
	assert.True(t, hasExisting(v, "copilot"))

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
	require.NotNil(t, s) // control: only built-ins match this spelling; the default harness wins
	assert.Equal(t, "copilot", s.ID)

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

func hasExisting(v *AuthorizationView, id string) bool {
	for _, c := range v.ExistingClients {
		if c.ID == id {
			return true
		}
	}
	return false
}

// seen installs a presence reading for the suggestion tie-break.
func (fx *oauthFx) seen(at map[string]time.Time) {
	fx.t.Helper()
	m := map[string]time.Time{}
	for name, when := range at {
		m[sanitizeClientName(name)] = when
	}
	fx.svc.presence = func() map[string]time.Time { return m }
}

// The registration name does not tell the two VS Code harnesses apart, so the
// tie is broken by records, then by which harness this installation saw
// recently (clientInfo.name), then by the default harness (#1971).
func TestOAuthDescribe_VSCodeRegistrationTieBreak(t *testing.T) {
	const name = "Visual Studio Code"
	now := time.Now()

	t.Run("records win over presence and default", func(t *testing.T) {
		fx := newOAuthFx(t)
		fx.mintClient("vscode", "ro", auth.ProfileModeLocked)
		fx.seen(map[string]time.Time{"vscode-agent-host": now})
		s := fx.suggestFor(name, "").SuggestedClient
		require.NotNil(t, s)
		assert.Equal(t, "vscode", s.ID)
	})
	t.Run("both recorded: presence decides", func(t *testing.T) {
		fx := newOAuthFx(t)
		fx.mintClient("vscode", "ro", auth.ProfileModeLocked)
		fx.mintClient("copilot", "ro", auth.ProfileModeLocked)
		fx.seen(map[string]time.Time{name: now})
		s := fx.suggestFor(name, "").SuggestedClient
		require.NotNil(t, s)
		assert.Equal(t, "vscode", s.ID)
	})
	t.Run("local harness seen recently", func(t *testing.T) {
		fx := newOAuthFx(t)
		fx.seen(map[string]time.Time{name: now.Add(-time.Hour)})
		s := fx.suggestFor(name, "").SuggestedClient
		require.NotNil(t, s)
		assert.Equal(t, "vscode", s.ID)
	})
	t.Run("copilot harness seen recently", func(t *testing.T) {
		fx := newOAuthFx(t)
		fx.seen(map[string]time.Time{"vscode-agent-host": now.Add(-time.Hour)})
		s := fx.suggestFor(name, "").SuggestedClient
		require.NotNil(t, s)
		assert.Equal(t, "copilot", s.ID)
	})
	t.Run("both seen: the newer one", func(t *testing.T) {
		fx := newOAuthFx(t)
		fx.seen(map[string]time.Time{name: now.Add(-2 * time.Hour), "vscode-agent-host": now.Add(-time.Hour)})
		assert.Equal(t, "copilot", fx.suggestFor(name, "").SuggestedClient.ID)
		fx.seen(map[string]time.Time{name: now.Add(-time.Hour), "vscode-agent-host": now.Add(-2 * time.Hour)})
		assert.Equal(t, "vscode", fx.suggestFor(name, "").SuggestedClient.ID)
	})
	t.Run("old presence is ignored", func(t *testing.T) {
		fx := newOAuthFx(t)
		fx.seen(map[string]time.Time{name: now.Add(-90 * 24 * time.Hour)})
		assert.Equal(t, "copilot", fx.suggestFor(name, "").SuggestedClient.ID)
	})
	t.Run("equal sightings fall back to the default harness", func(t *testing.T) {
		fx := newOAuthFx(t)
		fx.seen(map[string]time.Time{name: now.Add(-time.Hour), "vscode-agent-host": now.Add(-time.Hour)})
		assert.Equal(t, "copilot", fx.suggestFor(name, "").SuggestedClient.ID)
	})
	t.Run("a mixed tier (display name and known name) stays ambiguous", func(t *testing.T) {
		fx := newOAuthFx(t)
		fx.mintClient("vscode", "ro", auth.ProfileModeLocked)
		fx.addCustom("acme", "Visual Studio Code")
		assert.Nil(t, fx.suggestFor(name, "").SuggestedClient)
	})
	t.Run("an unrelated ambiguity stays ambiguous", func(t *testing.T) {
		fx := newOAuthFx(t)
		fx.addCustom("acme-a", "Shared Name")
		fx.addCustom("acme-b", "Shared Name")
		assert.Nil(t, fx.suggestFor("Shared Name", "").SuggestedClient)
	})
}
