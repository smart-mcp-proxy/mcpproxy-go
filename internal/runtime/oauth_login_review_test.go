package runtime

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/connect"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/mcplogin"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// --- A1: cancellation before any idempotent early return -----------------------------------

func TestRevokeClient_AlreadyRevokedKeyStillCancelsApprovedRequests(t *testing.T) {
	fx := newOAuthFx(t)
	fx.mintClient("codex", "ro", auth.ProfileModeLocked)
	_, err := fx.clients.RevokeAPIKey(fx.ctx(), fx.actor(), "codex")
	require.NoError(t, err)
	reg := fx.register()
	id := fx.authorize(reg, "codex")
	rev, _ := fx.sm.ClientBindingRevision("codex")
	redirect, err := fx.svc.Approve(fx.ctx(), id, ApprovalChoice{BindingRevision: rev})
	require.NoError(t, err)

	r, err := fx.creds.Revoke(fx.ctx(), fx.actor(), CredentialRef{Client: "codex"}, false)
	require.NoError(t, err)
	assert.False(t, r.Changed, "nothing live: the revoke itself is a no-op")
	_, terr := fx.svc.Exchange(fx.tokenInput(reg, oauthCodeOf(t, redirect)))
	requireInvalidGrant(t, terr)
	assert.Empty(t, fx.grants())
}

func TestForgetClient_WithNothingStoredStillCancelsApprovedRequests(t *testing.T) {
	fx := newOAuthFx(t)
	reg := fx.register()
	code := fx.approvedCode(reg, "cursor", ApprovalChoice{Profile: oauthStrp("ro")})
	pending := fx.authorize(reg, "cursor")

	_, err := fx.clients.Forget(fx.ctx(), fx.actor(), "cursor", false)
	var nc *NoClientCredentialError
	require.True(t, errors.As(err, &nc), "the returned error is unchanged")
	_, terr := fx.svc.Exchange(fx.tokenInput(reg, code))
	requireInvalidGrant(t, terr)
	_, err = fx.svc.Approve(fx.ctx(), pending, ApprovalChoice{Profile: oauthStrp("ro")})
	assert.ErrorIs(t, err, ErrOAuthRequestNotFound)
	fx.nothingStored()
}

// --- A2: hasLiveGrant fails closed --------------------------------------------------------------

type failingGrantsStore struct{ *storage.Manager }

func (failingGrantsStore) ListOAuthGrants() ([]storage.OAuthGrant, error) {
	return nil, errors.New("disk on fire")
}

func TestRevokeClient_UnreadableGrantsRefuseInsteadOfSkipping(t *testing.T) {
	fx := newOAuthFx(t)
	fx.mintClient("codex", "ro", auth.ProfileModeLocked)
	reg := fx.register()
	id := fx.authorize(reg, "codex")
	rev, _ := fx.sm.ClientBindingRevision("codex")
	redirect, err := fx.svc.Approve(fx.ctx(), id, ApprovalChoice{BindingRevision: rev})
	require.NoError(t, err)
	_, terr := fx.svc.Exchange(fx.tokenInput(reg, oauthCodeOf(t, redirect)))
	require.Nil(t, terr)
	_, err = fx.clients.RevokeAPIKey(fx.ctx(), fx.actor(), "codex")
	require.NoError(t, err)

	broken := failingGrantsStore{fx.sm}
	cs := NewClientsService(ClientsServiceDeps{
		Store: broken, HMACKey: func() ([]byte, error) { return oauthTestKey, nil },
		Config: func() *config.Config { return fx.cfg }, Guard: func() BindingGuard { return stubGuard{} },
		Now: fx.clock.Now, Mu: fx.clients.mu,
	})
	_, err = cs.CredentialsServiceOver(broken).Revoke(fx.ctx(), fx.actor(), CredentialRef{Client: "codex"}, false)
	require.Error(t, err, "an unreadable grant list must not look like 'nothing live'")
	assert.False(t, fx.grants()[0].Revoked)
}

// --- A3: the legacy token-name door cascades -------------------------------------------------

func TestRevokeByTokenName_ClientWithLoginsCascades(t *testing.T) {
	fx := newOAuthFx(t)
	fx.mintClient("codex", "ro", auth.ProfileModeLocked)
	reg := fx.register()
	id := fx.authorize(reg, "codex")
	rev, _ := fx.sm.ClientBindingRevision("codex")
	redirect, err := fx.svc.Approve(fx.ctx(), id, ApprovalChoice{BindingRevision: rev})
	require.NoError(t, err)
	code := oauthCodeOf(t, redirect)
	_, terr := fx.svc.Exchange(fx.tokenInput(reg, code))
	require.Nil(t, terr)
	pending := fx.approvedCode(fx.register(), "codex", ApprovalChoice{BindingRevision: rev})

	_, err = fx.creds.Revoke(fx.ctx(), fx.actor(), CredentialRef{Token: "client-codex"}, true)
	require.NoError(t, err)
	tok, _ := fx.sm.GetAgentTokenByName("client-codex")
	assert.True(t, tok.Revoked)
	for _, g := range fx.grants() {
		assert.True(t, g.Revoked, "the door cut a client off completely before, and still does")
	}
	_, terr = fx.svc.Exchange(fx.tokenInput(reg, pending))
	requireInvalidGrant(t, terr)
}

func TestRevokeByTokenName_ClientWithoutLoginsIsExactlyTheOldDoor(t *testing.T) {
	fx := newOAuthFx(t)
	fx.mintClient("codex", "ro", auth.ProfileModeLocked)
	r, err := fx.creds.Revoke(fx.ctx(), fx.actor(), CredentialRef{Token: "client-codex"}, true)
	require.NoError(t, err)
	assert.True(t, r.Changed)
	assert.False(t, r.ClientConfigUntouched, "the token door never said this")
	rows := fx.changeRows("revoke")
	require.Len(t, rows, 1)
	diff, _ := rows[0]["diff"].(map[string]interface{})
	assert.Equal(t, true, diff["changed"])
	assert.NotContains(t, diff, "client_config_untouched", "the client-revoke diff is not used")
	evs := fx.eventsOf(EventTypeCredentialsChanged)
	require.Len(t, evs, 1)
	assert.Equal(t, CredentialKindClient, evs[0].Payload["kind"])
}

// --- A4: the switch-off boundary -----------------------------------------------------------------

func TestBeginAuthorization_RequestAdmittedBeforeSwitchOffIsCancelledByIt(t *testing.T) {
	fx := newOAuthFx(t)
	reg := fx.register()
	done := make(chan struct{})
	var once sync.Once
	fx.svc.testHookBeginAfterEnabled = func() {
		once.Do(func() {
			go func() {
				release := fx.svc.beginDisable("test") // blocks until Begin leaves the lifecycle section
				fx.cfg.MCPOAuthEnabled = false
				release()
				close(done)
			}()
			time.Sleep(50 * time.Millisecond) // let it queue on the lifecycle lock
		})
	}
	id, aerr := fx.svc.BeginAuthorization(fx.authInput(reg, oauthTestOrigin+"/mcp/oauth/cursor"))
	require.Nil(t, aerr, "admitted: the enabled check and the create share one boundary")
	<-done
	_, err := fx.svc.Approve(fx.ctx(), id, ApprovalChoice{Profile: oauthStrp("ro")})
	assert.ErrorIs(t, err, ErrOAuthRequestNotFound, "the switch-off cancelled it")

	// During the off window nothing is admitted, and after off -> on nothing from
	// the window is approvable.
	_, aerr = fx.svc.BeginAuthorization(fx.authInput(reg, oauthTestOrigin+"/mcp/oauth/cursor"))
	require.NotNil(t, aerr)
	fx.cfg.MCPOAuthEnabled = true
	_, err = fx.svc.Approve(fx.ctx(), id, ApprovalChoice{Profile: oauthStrp("ro")})
	assert.ErrorIs(t, err, ErrOAuthRequestNotFound)
	assert.Empty(t, fx.svc.ListPending())
}

func TestApprove_LosingToACancelWritesNoRowAndNoRedirect(t *testing.T) {
	fx := newOAuthFx(t)
	reg := fx.register()
	id := fx.authorize(reg, "cursor")
	fx.svc.testHookApproveAfterStore = func() { fx.svc.CancelAll("race") }
	redirect, err := fx.svc.Approve(fx.ctx(), id, ApprovalChoice{Profile: oauthStrp("ro")})
	assert.ErrorIs(t, err, ErrOAuthRequestNotFound)
	assert.Empty(t, redirect)
	assert.Empty(t, fx.changeRows("approve"))
	for _, e := range fx.eventsOf(EventTypeOAuthAuthorizationDecided) {
		assert.NotEqual(t, "approved", e.Payload["status"])
	}
}

// --- B5: wrong-origin replay -----------------------------------------------------------------------

func TestExchange_ReplayOnAnotherOriginDoesNotRevokeTheLogin(t *testing.T) {
	fx := newOAuthFx(t)
	reg := fx.register()
	code := fx.approvedCode(reg, "cursor", ApprovalChoice{Profile: oauthStrp("ro")})
	_, terr := fx.svc.Exchange(fx.tokenInput(reg, code))
	require.Nil(t, terr)

	in := fx.tokenInput(reg, code)
	in.Origin = "http://localhost:8080"
	_, terr = fx.svc.Exchange(in)
	requireInvalidGrant(t, terr)
	gs := fx.grants()
	require.Len(t, gs, 1)
	assert.False(t, gs[0].Revoked, "a foreign-origin replay revokes nothing")
	assert.Empty(t, fx.changeRows("replay_revoke"))
	// The same-origin replay still does.
	_, terr = fx.svc.Exchange(fx.tokenInput(reg, code))
	requireInvalidGrant(t, terr)
	assert.True(t, fx.grants()[0].Revoked)
}

// --- B6: a dead grant never answers invalid_target ---------------------------------------------------

func TestRefresh_DeadGrantAnswersInvalidGrantEvenWithAWrongResource(t *testing.T) {
	fx := newOAuthFx(t)
	reg, first := fx.loginNew("cursor", "ro")
	gid := fx.grants()[0].GrantID
	wrong := func(tok string) *TokenError {
		_, terr := fx.svc.Refresh(TokenInput{Origin: oauthTestOrigin, RefreshToken: tok, ClientID: reg, Resource: oauthTestOrigin + "/mcp/oauth/other"})
		return terr
	}
	live := wrong(first.RefreshToken)
	require.NotNil(t, live)
	assert.Equal(t, TokenErrInvalidTarget, live.Code, "a live grant: the resource check applies")

	key := oauthTestKey
	unknownTok, _, err := mcplogin.NewRefreshToken(key, strings.Repeat("A", mcplogin.GrantIDLen))
	require.NoError(t, err)
	unknown := wrong(unknownTok)

	_, err = fx.sm.RevokeOAuthGrant(gid, storage.OAuthRevokeAdmin, fx.clock.Now())
	require.NoError(t, err)
	revoked := wrong(first.RefreshToken)

	fx2 := newOAuthFx(t)
	reg2, first2 := fx2.loginNew("cursor", "ro")
	fx2.clock.Advance(OAuthRefreshTokenTTL + time.Hour)
	_, expired := fx2.svc.Refresh(TokenInput{Origin: oauthTestOrigin, RefreshToken: first2.RefreshToken, ClientID: reg2, Resource: oauthTestOrigin + "/mcp/oauth/other"})

	for name, e := range map[string]*TokenError{"unknown": unknown, "revoked": revoked, "expired": expired} {
		require.NotNil(t, e, name)
		assert.Equal(t, errInvalidGrant, e, name+": the one uniform answer")
	}
}

// --- B7: one resource identity --------------------------------------------------------------------------

func TestResource_SlashFormIsCanonicalisedEndToEnd(t *testing.T) {
	fx := newOAuthFx(t)
	reg := fx.register()
	id, aerr := fx.svc.BeginAuthorization(fx.authInput(reg, oauthTestOrigin+"/mcp/oauth/cursor/"))
	require.Nil(t, aerr)
	redirect, err := fx.svc.Approve(fx.ctx(), id, ApprovalChoice{Profile: oauthStrp("ro")})
	require.NoError(t, err)
	in := fx.tokenInput(reg, oauthCodeOf(t, redirect))
	in.Resource = oauthTestOrigin + "/mcp/oauth/cursor/" // the form the client sent
	res, terr := fx.svc.Exchange(in)
	require.Nil(t, terr)
	assert.Equal(t, oauthTestOrigin+"/mcp/oauth/cursor", fx.grants()[0].Resource, "stored canonical")

	now := fx.clock.Now()
	for _, p := range []string{"/mcp/oauth/cursor", "/mcp/oauth/cursor/"} {
		_, _, aerr := fx.svc.Authenticate(res.AccessToken, oauthTestOrigin, p, now)
		assert.NoError(t, aerr, p)
	}
	_, terr = fx.svc.Refresh(TokenInput{Origin: oauthTestOrigin, RefreshToken: res.RefreshToken, ClientID: reg, Resource: oauthTestOrigin + "/mcp/oauth/cursor/"})
	require.Nil(t, terr, "the slash form the client sent still refreshes")
}

func TestResource_RefreshAcceptsEitherFormAndGenericAddress(t *testing.T) {
	fx := newOAuthFx(t)
	reg, res := fx.loginNew("cursor", "ro")
	r1, terr := fx.svc.Refresh(TokenInput{Origin: oauthTestOrigin, RefreshToken: res.RefreshToken, ClientID: reg, Resource: oauthTestOrigin + "/mcp/oauth/cursor/"})
	require.Nil(t, terr)
	_, terr = fx.svc.Refresh(TokenInput{Origin: oauthTestOrigin, RefreshToken: r1.RefreshToken, ClientID: reg, Resource: oauthTestOrigin + "/mcp/oauth/cursor"})
	require.Nil(t, terr)
	_, terr = fx.svc.Refresh(TokenInput{Origin: oauthTestOrigin, RefreshToken: "x", ClientID: reg, Resource: oauthTestOrigin + "/mcp/oauth/codex/"})
	require.NotNil(t, terr)

	// the generic address, with and without the slash
	reg2 := fx.register()
	id, aerr := fx.svc.BeginAuthorization(fx.authInput(reg2, oauthTestOrigin+"/mcp/oauth/"))
	require.Nil(t, aerr)
	redirect, err := fx.svc.Approve(fx.ctx(), id, ApprovalChoice{Client: ClientChoice{New: &NewClientChoice{ID: "acme"}}, Profile: oauthStrp("ro")})
	require.NoError(t, err)
	res2, terr := fx.svc.Exchange(fx.tokenInput(reg2, oauthCodeOf(t, redirect)))
	require.Nil(t, terr)
	_, _, aerr2 := fx.svc.Authenticate(res2.AccessToken, oauthTestOrigin, "/mcp/oauth", fx.clock.Now())
	assert.NoError(t, aerr2)
	_, _, aerr2 = fx.svc.Authenticate(res2.AccessToken, oauthTestOrigin, "/mcp/oauth/", fx.clock.Now())
	assert.NoError(t, aerr2)
}

// --- B8: the profile must still exist ---------------------------------------------------------------------

func TestExchange_NewClientProfileGoneAtExchange(t *testing.T) {
	fx := newOAuthFx(t)
	reg := fx.register()
	code := fx.approvedCode(reg, "cursor", ApprovalChoice{Profile: oauthStrp("ro")})
	fx.cfg.Profiles = fx.cfg.Profiles[1:] // "ro" deleted
	_, terr := fx.svc.Exchange(fx.tokenInput(reg, code))
	requireInvalidGrant(t, terr)
	fx.nothingStored()
	rows := fx.changeRows("deny")
	require.Len(t, rows, 1)
	assert.Equal(t, "profile_gone", rows[0]["reason"])
}

// --- B9: Register is gated --------------------------------------------------------------------------------

func TestRegister_DisabledRefusesAndWritesNothing(t *testing.T) {
	fx := newOAuthFx(t)
	fx.cfg.MCPOAuthEnabled = false
	_, rerr, err := fx.svc.Register(mcplogin.RegistrationRequest{RedirectURIs: []string{oauthTestRedirect}, ClientName: "x"})
	assert.ErrorIs(t, err, ErrOAuthFeatureDisabled)
	assert.Nil(t, rerr)
	regs, lerr := fx.sm.ListOAuthRegistrations()
	require.NoError(t, lerr)
	assert.Empty(t, regs)
}

// --- C10: connect on a browser-only client ----------------------------------------------------------------

func TestConnect_BrowserOnlyClientKeepsItsBindingWhenNoProfileIsGiven(t *testing.T) {
	fx := newOAuthFx(t)
	fx.loginNew("cursor", "ro")
	m := fx.clients.ConnectMinter()

	pin, mode, err := m.PreviewBinding("cursor", connect.CredentialIntent{})
	require.NoError(t, err)
	assert.Equal(t, "ro", pin)
	assert.Equal(t, auth.ProfileModeLocked, mode)

	issued, err := m.Issue("cursor", connect.CredentialIntent{})
	require.NoError(t, err)
	assert.Equal(t, "ro", issued.Profile)
	assert.Equal(t, auth.ProfileModeLocked, issued.Mode)
	tok, _ := fx.sm.GetAgentTokenByName("client-cursor")
	assert.Equal(t, "ro", tok.ProfilePin)
	b, _ := fx.sm.GetOAuthClientBinding("cursor")
	assert.Equal(t, "ro", b.ProfilePin)
	assert.False(t, fx.grants()[0].Revoked, "logins unchanged")
	require.NoError(t, m.Abort("cursor", connect.CredentialIntent{}, issued))

	// An explicit profile still wins.
	rw := "rw"
	pin, _, err = m.PreviewBinding("cursor", connect.CredentialIntent{Profile: &rw})
	require.NoError(t, err)
	assert.Equal(t, "rw", pin)
}

// --- C11: create_client must not rebind an OAuth-only client -----------------------------------------------

func TestIssueClientMCP_RefusesAClientThatHasBrowserLogins(t *testing.T) {
	fx := newOAuthFx(t)
	reg := fx.register()
	code := fx.approvedCode(reg, "", ApprovalChoice{Client: ClientChoice{New: &NewClientChoice{ID: "acme"}}, Profile: oauthStrp("ro")})
	_, terr := fx.svc.Exchange(fx.tokenInput(reg, code))
	require.Nil(t, terr)

	_, err := fx.creds.IssueClient(fx.ctx(), fx.actor(), IssueClientRequest{
		ID: "acme", Profile: "rw", ProfilePresent: true, ExpiresIn: "7d", RequireProfile: true, RefuseExistingRecord: true,
	})
	var ce *CredentialError
	require.True(t, errors.As(err, &ce))
	assert.Equal(t, "identity_exists", ce.Code())
	pin, _, _, _ := fx.sm.CurrentClientBinding("acme")
	assert.Equal(t, "ro", pin, "nothing was rebound")
}

// --- C12: list filters apply after the unfiltered record check ------------------------------------------------

func TestCredentialsList_ActiveFilterDoesNotFabricateAnOAuthOnlyRow(t *testing.T) {
	fx := newOAuthFx(t)
	fx.mintClient("codex", "ro", auth.ProfileModeLocked)
	reg := fx.register()
	id := fx.authorize(reg, "codex")
	rev, _ := fx.sm.ClientBindingRevision("codex")
	redirect, err := fx.svc.Approve(fx.ctx(), id, ApprovalChoice{BindingRevision: rev})
	require.NoError(t, err)
	_, terr := fx.svc.Exchange(fx.tokenInput(reg, oauthCodeOf(t, redirect)))
	require.Nil(t, terr)
	_, err = fx.clients.RevokeAPIKey(fx.ctx(), fx.actor(), "codex")
	require.NoError(t, err)

	got, err := fx.creds.List(CredentialFilter{State: "active", Kind: "client"})
	require.NoError(t, err)
	assert.Empty(t, got, "the client has an API-key record (revoked): no OAuth-only stand-in under an active filter")
	got, err = fx.creds.List(CredentialFilter{State: "active"})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "browser_login", got[0].Kind)
}

// --- C13: the explicit actor reaches the single-grant revoke -----------------------------------------------------

func TestCredentialsRevoke_OneGrantUsesTheSuppliedActor(t *testing.T) {
	fx := newOAuthFx(t)
	fx.loginNew("cursor", "ro")
	gid := fx.grants()[0].GrantID
	other := Actor{Kind: "agent_token", Name: "ops-bot", Surface: "mcp"}
	_, err := fx.creds.Revoke(fx.ctx(), other, CredentialRef{Client: "cursor", Grant: gid}, false)
	require.NoError(t, err)
	rows := fx.changeRows("revoke")
	require.Len(t, rows, 1)
	assert.Equal(t, "ops-bot", rows[0]["actor_name"])
	assert.Equal(t, "agent_token", rows[0]["actor_kind"])
	assert.Equal(t, "mcp", rows[0]["surface"])
}

// --- C14: profile users take the pin from the API-key record ---------------------------------------------------------

// A binary that knew nothing of browser logins moved only the API-key record:
// the record is the truth (storage.CurrentClientBinding), the entry is stale.
func TestProfilesService_UsedByTakesTheAPIKeyRecordPinOverADivergentEntry(t *testing.T) {
	h := newProfilesHarness(t)
	commitOAuthOnlyClient(t, h.rt.StorageManager(), "reg1", "grantAAAAAAAAAAAAAAAAAA", "acme", "ro", auth.ProfileModeLocked)
	tokens := []auth.AgentToken{{
		Name: auth.ClientTokenName("acme"), Kind: auth.KindClient, ClientID: "acme",
		ProfilePin: "full", ProfileMode: auth.ProfileModeSwitchable, Revoked: true,
	}}
	collect := func(pin string) *UsedBy {
		u := &UsedBy{}
		require.NoError(t, h.svc.addOAuthUsers(tokens, func(p string) *UsedBy {
			if p == pin {
				return u
			}
			return nil
		}))
		return u
	}
	assert.Empty(t, collect("ro").Clients, "the stale entry pin does not decide")
	full := collect("full")
	require.Len(t, full.Clients, 1)
	assert.Equal(t, "acme", full.Clients[0].ID)
	assert.Equal(t, auth.ProfileModeSwitchable, full.Clients[0].Mode)
}

// --- C15: issued-credential prefixes -------------------------------------------------------------------------------------

func TestSecretShaped_RecognisesBrowserLoginTokens(t *testing.T) {
	c := NewCredentialScreen(nil)
	assert.True(t, c.SecretShaped("here: mcp_oat_abcdef"))
	assert.True(t, c.SecretShaped("MCP_ORT_whatever"))
	assert.False(t, c.SecretShaped("an ordinary sentence"))
}
