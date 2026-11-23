package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/mcplogin"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// commitOAuthOnlyClient stores a client that has only a browser login.
func commitOAuthOnlyClient(t *testing.T, sm *storage.Manager, regID, grantID, client, pin, mode string) {
	t.Helper()
	now := time.Now().UTC()
	require.NoError(t, sm.CreateOAuthRegistration(storage.OAuthRegistration{
		ClientID: regID, ReportedName: "App", RedirectURIs: []string{"http://127.0.0.1:9/cb"},
		TokenEndpointAuthMethod: "none", GrantTypes: []string{"authorization_code", "refresh_token"}, CreatedAt: now,
	}))
	_, err := sm.CommitOAuthLogin(storage.CommitLoginInput{
		Grant: storage.OAuthGrant{
			GrantID: grantID, RegistrationID: regID, Client: client, RedirectURI: "http://127.0.0.1:9/cb",
			Resource: "http://127.0.0.1:8080/mcp/oauth/" + client, Issuer: "http://127.0.0.1:8080",
			RefreshHash: "rh-" + grantID, AccessHash: "ah-" + grantID,
			AccessExpiresAt: now.Add(time.Hour), RefreshExpiresAt: now.Add(24 * time.Hour),
			ApprovedAt: now, DisplayPrefix: "mcp_oat_" + grantID[:4],
		},
		Client: client, NewClient: true, Pin: pin, Mode: mode, ExpectedBindingRevision: storage.OAuthBindingAbsent, Now: now,
	})
	require.NoError(t, err)
}

// --- views ------------------------------------------------------------------------------------------

// While browser login was never used a view is EXACTLY what it was: no new
// field is set, so the JSON carries no new key.
func TestClientViews_UnchangedWhenNeverUsed(t *testing.T) {
	fx := newOAuthFx(t)
	fx.mintClient("codex", "ro", auth.ProfileModeLocked)

	list, err := fx.clients.List()
	require.NoError(t, err)
	require.Len(t, list, 1)
	got, err := fx.clients.Get("codex")
	require.NoError(t, err)
	require.NotNil(t, got)
	for _, v := range []ClientCredentialView{list[0], *got} {
		assert.Nil(t, v.Credentials)
		assert.Nil(t, v.BrowserLoginDisabled)
		raw, err := json.Marshal(v)
		require.NoError(t, err)
		assert.NotContains(t, string(raw), "credentials")
		assert.NotContains(t, string(raw), "browser_login_disabled")
	}

	cl, err := fx.creds.List(CredentialFilter{})
	require.NoError(t, err)
	require.Len(t, cl, 1)
	assert.Equal(t, CredentialKindClient, cl[0].Kind)
	raw, _ := json.Marshal(cl[0])
	assert.NotContains(t, string(raw), "reported_name")

	ghost, err := fx.clients.Get("ghost")
	require.NoError(t, err)
	assert.Nil(t, ghost)
	used, _ := fx.sm.OAuthLoginEverUsed()
	assert.False(t, used)
}

func TestClientViews_ListCredentialsOnceUsed(t *testing.T) {
	fx := newOAuthFx(t)
	fx.mintClient("codex", "ro", auth.ProfileModeLocked)
	fx.loginNew("cursor", "rw")
	gid := fx.grants()[0].GrantID
	require.NoError(t, fx.sm.TouchOAuthGrant(gid, fx.clock.Now(), "totally-other-app"))

	list, err := fx.clients.List()
	require.NoError(t, err)
	require.Len(t, list, 2)
	byID := map[string]ClientCredentialView{}
	for _, v := range list {
		byID[v.ID] = v
	}
	// An API-key-only client carries its api_key entry and the flag, no logins.
	codex := byID["codex"]
	require.Len(t, codex.Credentials, 1)
	assert.Equal(t, "api_key", codex.Credentials[0].Kind)
	assert.Equal(t, "active", codex.Credentials[0].State)
	require.NotNil(t, codex.BrowserLoginDisabled)
	assert.False(t, *codex.BrowserLoginDisabled)

	// The OAuth-only client is a row of the hub.
	cur := byID["cursor"]
	assert.Equal(t, "rw", cur.Profile)
	assert.Equal(t, auth.ProfileModeLocked, cur.Mode)
	assert.Equal(t, profile.CredentialStateNone, cur.CredentialState, "it has no API key")
	assert.Equal(t, "client-cursor", cur.TokenName)
	require.Len(t, cur.Credentials, 1)
	e := cur.Credentials[0]
	assert.Equal(t, "browser_login", e.Kind)
	assert.Equal(t, gid, e.GrantID)
	assert.Equal(t, "active", e.State)
	assert.Equal(t, "cursor ide", e.ReportedName)
	assert.Equal(t, "127.0.0.1", e.RedirectHost)
	assert.Equal(t, "totally-other-app", e.ObservedName)
	require.NotNil(t, e.NameMismatch)
	assert.True(t, *e.NameMismatch, "advisory only")
	assert.NotNil(t, e.ApprovedAt)

	// The matching name clears the warning.
	require.NoError(t, fx.sm.TouchOAuthGrant(gid, fx.clock.Now(), "cursor"))
	got, err := fx.clients.Get("cursor")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.False(t, *got.Credentials[0].NameMismatch)
	assert.False(t, *got.BrowserLoginDisabled)

	// The switch off after use: logins stay listed, flagged.
	fx.cfg.MCPOAuthEnabled = false
	got, err = fx.clients.Get("cursor")
	require.NoError(t, err)
	assert.True(t, *got.BrowserLoginDisabled)
	codex2, _ := fx.clients.Get("codex")
	assert.True(t, *codex2.BrowserLoginDisabled, "browser_login_disabled reflects the install-wide switch, not whether the client has logins")

	// A lapsed login says so.
	fx.clock.Advance(OAuthRefreshTokenTTL + time.Hour)
	got, _ = fx.clients.Get("cursor")
	assert.Equal(t, "lapsed", got.Credentials[0].State)
}

func TestCredentialsService_ListsAndGetsBrowserLogins(t *testing.T) {
	fx := newOAuthFx(t)
	fx.mintClient("codex", "ro", auth.ProfileModeLocked)
	fx.loginNew("cursor", "rw")
	gid := fx.grants()[0].GrantID

	all, err := fx.creds.List(CredentialFilter{})
	require.NoError(t, err)
	kinds := map[string]CredentialView{}
	for _, v := range all {
		kinds[v.Kind+"/"+v.ID] = v
	}
	login := kinds["browser_login/"+gid]
	assert.Equal(t, "cursor", login.Client)
	assert.Equal(t, "active", login.State)
	assert.Equal(t, "rw", login.Profile)
	assert.Contains(t, kinds, "client/codex")
	assert.Equal(t, "active", kinds["client/cursor"].State, "an OAuth-only client is a client")

	only, err := fx.creds.List(CredentialFilter{Kind: "browser_login"})
	require.NoError(t, err)
	require.Len(t, only, 1)
	none, err := fx.creds.List(CredentialFilter{ExcludeClients: true})
	require.NoError(t, err)
	assert.Empty(t, none, "the server edition has no client credentials and no logins")

	v, err := fx.creds.Get(CredentialRef{Client: "cursor"})
	require.NoError(t, err)
	assert.Equal(t, "rw", v.Profile)
	_, err = fx.creds.Get(CredentialRef{Client: "ghost"})
	require.Error(t, err)
}

// --- binding for a client that has only logins ---------------------------------------------------------

func TestSetBinding_OAuthOnlyClient(t *testing.T) {
	fx := newOAuthFx(t)
	fx.loginNew("cursor", "ro")

	v, err := fx.clients.SetBinding(fx.ctx(), fx.actor(), "cursor", "rw", nil)
	require.NoError(t, err)
	assert.Equal(t, "rw", v.Profile)
	pin, mode, found, err := fx.sm.CurrentClientBinding("cursor")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "rw", pin)
	assert.Equal(t, auth.ProfileModeLocked, mode)
	rows := fx.changeRows("assign")
	require.Len(t, rows, 1)
	assert.Equal(t, "cursor", rows[0]["client_id"])
	assert.Equal(t, "rw", rows[0]["profile"])
	assert.Equal(t, "ro", rows[0]["previous_profile"])
	assert.NotEmpty(t, fx.eventsOf(EventTypeClientBindingChanged))

	sw := auth.ProfileModeSwitchable
	_, err = fx.clients.SetBinding(fx.ctx(), fx.actor(), "cursor", "rw", &sw)
	require.NoError(t, err)
	assert.Len(t, fx.changeRows("unlock"), 1)

	_, err = fx.clients.SetBinding(fx.ctx(), fx.actor(), "cursor", "ghost", nil)
	var ve *ValidationError
	require.True(t, errors.As(err, &ve))
	// A client with nothing at all is still no_client_credential.
	_, err = fx.clients.SetBinding(fx.ctx(), fx.actor(), "ghost", "rw", nil)
	var nc *NoClientCredentialError
	require.True(t, errors.As(err, &nc))
}

func TestSetBinding_OAuthOnlyClientIsJudgedByTheGuard(t *testing.T) {
	fx := newOAuthFx(t)
	fx.loginNew("cursor", "ro")
	fx.guard = StrictOfflineBindingGuard{}
	fx.cfg.RequireMCPAuth = false

	_, err := fx.clients.SetBinding(fx.ctx(), fx.actor(), "cursor", "rw", nil)
	var ge *BindingGuardError
	require.ErrorAs(t, err, &ge, "the live login is an active named binding for the guard")
	pin, _, _, _ := fx.sm.CurrentClientBinding("cursor")
	assert.Equal(t, "ro", pin, "a refused change writes nothing")
	assert.Empty(t, fx.changeRows("assign"))
}

func TestBulkAssign_MovesOAuthOnlyClients(t *testing.T) {
	fx := newOAuthFx(t)
	fx.loginNew("cursor", "ro")
	fx.mintClient("codex", "ro", auth.ProfileModeLocked)

	moved, skipped, err := fx.clients.BulkAssign(fx.ctx(), fx.actor(), "ro", "rw", nil)
	require.NoError(t, err)
	assert.Empty(t, skipped)
	assert.Equal(t, []string{"codex", "cursor"}, moved)
	pin, _, _, _ := fx.sm.CurrentClientBinding("cursor")
	assert.Equal(t, "rw", pin)
}

func TestMintingAnAPIKeyForAnOAuthOnlyClientKeepsOneBinding(t *testing.T) {
	fx := newOAuthFx(t)
	reg := fx.register()
	code := fx.approvedCode(reg, "", ApprovalChoice{Client: ClientChoice{New: &NewClientChoice{ID: "acme"}}, Profile: oauthStrp("ro")})
	_, terr := fx.svc.Exchange(fx.tokenInput(reg, code))
	require.Nil(t, terr)

	_, secret, err := fx.clients.Add(fx.ctx(), fx.actor(), AddRequest{ID: "acme", Profile: "rw"})
	require.NoError(t, err)
	assert.NotEmpty(t, secret)
	pin, _, _, err := fx.sm.CurrentClientBinding("acme")
	require.NoError(t, err)
	assert.Equal(t, "rw", pin)
	b, err := fx.sm.GetOAuthClientBinding("acme")
	require.NoError(t, err)
	assert.Equal(t, "rw", b.ProfilePin, "both copies carry the binding the caller asked for")
}

// --- forget, revoke ---------------------------------------------------------------------------------------

func TestForgetClient_DeletesTheLoginsAndTheBinding(t *testing.T) {
	fx := newOAuthFx(t)
	fx.loginNew("cursor", "ro")
	_, err := fx.clients.Forget(fx.ctx(), fx.actor(), "cursor", false)
	require.NoError(t, err)
	assert.Empty(t, fx.grants())
	b, _ := fx.sm.GetOAuthClientBinding("cursor")
	assert.Nil(t, b)
	got, _ := fx.clients.Get("cursor")
	assert.Nil(t, got)
	assert.Len(t, fx.changeRows("forget"), 1)
	// Forgetting something that has nothing is still the old refusal.
	_, err = fx.clients.Forget(fx.ctx(), fx.actor(), "cursor", false)
	var nc *NoClientCredentialError
	require.True(t, errors.As(err, &nc))
}

func TestRevokeClient_RevokesTheKeyAndEveryLoginAndKeepsThemForDisplay(t *testing.T) {
	fx := newOAuthFx(t)
	fx.mintClient("codex", "ro", auth.ProfileModeLocked)
	reg := fx.register()
	id := fx.authorize(reg, "codex")
	rev, _ := fx.sm.ClientBindingRevision("codex")
	redirect, err := fx.svc.Approve(fx.ctx(), id, ApprovalChoice{BindingRevision: rev})
	require.NoError(t, err)
	res, terr := fx.svc.Exchange(fx.tokenInput(reg, oauthCodeOf(t, redirect)))
	require.Nil(t, terr)

	r, err := fx.creds.Revoke(fx.ctx(), fx.actor(), CredentialRef{Client: "codex"}, false)
	require.NoError(t, err)
	assert.True(t, r.Changed)
	tok, _ := fx.sm.GetAgentTokenByName("client-codex")
	assert.True(t, tok.Revoked)
	gs := fx.grants()
	require.Len(t, gs, 1)
	assert.True(t, gs[0].Revoked)
	assert.Equal(t, storage.OAuthRevokeClientRevoked, gs[0].RevokeReason)
	_, _, aerr := fx.svc.Authenticate(res.AccessToken, oauthTestOrigin, "/mcp/oauth/codex", fx.clock.Now())
	assert.ErrorIs(t, aerr, ErrOAuthUnauthorized, "a live session's next request is refused")
	assert.Len(t, fx.changeRows("revoke"), 1)

	// An API key that is already revoked with a live login still revokes the login.
	fx.mintClient("zed", "ro", auth.ProfileModeLocked)
	regZ := fx.register()
	idZ := fx.authorize(regZ, "zed")
	revZ, _ := fx.sm.ClientBindingRevision("zed")
	redirectZ, err := fx.svc.Approve(fx.ctx(), idZ, ApprovalChoice{BindingRevision: revZ})
	require.NoError(t, err)
	_, terr = fx.svc.Exchange(fx.tokenInput(regZ, oauthCodeOf(t, redirectZ)))
	require.Nil(t, terr)
	_, err = fx.clients.RevokeAPIKey(fx.ctx(), fx.actor(), "zed")
	require.NoError(t, err)
	r, err = fx.creds.Revoke(fx.ctx(), fx.actor(), CredentialRef{Client: "zed"}, false)
	require.NoError(t, err)
	assert.True(t, r.Changed, "the revoked key does not hide a live login")
	for _, g := range fx.grants() {
		assert.True(t, g.Revoked, g.Client)
	}
}

func TestRevokeClient_OAuthOnlyClient(t *testing.T) {
	fx := newOAuthFx(t)
	fx.loginNew("cursor", "ro")
	r, err := fx.creds.Revoke(fx.ctx(), fx.actor(), CredentialRef{Client: "cursor"}, false)
	require.NoError(t, err)
	assert.True(t, r.Changed)
	assert.Equal(t, CredentialStateRevoked, r.View.State)
	require.Len(t, fx.grants(), 1)
	assert.True(t, fx.grants()[0].Revoked)
	b, _ := fx.sm.GetOAuthClientBinding("cursor")
	assert.NotNil(t, b, "kept for display")
	evs := fx.eventsOf(EventTypeCredentialsChanged)
	assert.Equal(t, "revoke", evs[len(evs)-1].Payload["change"])
}

func TestRevokeAPIKey_LeavesTheLoginsAndTheBinding(t *testing.T) {
	fx := newOAuthFx(t)
	fx.mintClient("codex", "ro", auth.ProfileModeLocked)
	reg := fx.register()
	id := fx.authorize(reg, "codex")
	rev, _ := fx.sm.ClientBindingRevision("codex")
	redirect, err := fx.svc.Approve(fx.ctx(), id, ApprovalChoice{BindingRevision: rev})
	require.NoError(t, err)
	res, terr := fx.svc.Exchange(fx.tokenInput(reg, oauthCodeOf(t, redirect)))
	require.Nil(t, terr)

	v, err := fx.clients.RevokeAPIKey(fx.ctx(), fx.actor(), "codex")
	require.NoError(t, err)
	assert.Equal(t, profile.CredentialStateRevoked, v.CredentialState)
	_, _, aerr := fx.svc.Authenticate(res.AccessToken, oauthTestOrigin, "/mcp/oauth/codex", fx.clock.Now())
	assert.NoError(t, aerr, "the login keeps working")
	pin, _, _, _ := fx.sm.CurrentClientBinding("codex")
	assert.Equal(t, "ro", pin)
	assert.False(t, fx.grants()[0].Revoked)

	_, err = fx.clients.RevokeAPIKey(fx.ctx(), fx.actor(), "codex")
	var nc *NoClientCredentialError
	require.True(t, errors.As(err, &nc), "no API key left")
	_, err = fx.clients.RevokeAPIKey(fx.ctx(), fx.actor(), "ghost")
	require.True(t, errors.As(err, &nc))
}

func TestCredentialsRevoke_OneGrant(t *testing.T) {
	fx := newOAuthFx(t)
	_, res := fx.loginNew("cursor", "ro")
	gid := fx.grants()[0].GrantID
	r, err := fx.creds.Revoke(fx.ctx(), fx.actor(), CredentialRef{Client: "cursor", Grant: gid}, false)
	require.NoError(t, err)
	assert.True(t, r.Changed)
	assert.Equal(t, "browser_login", r.View.Kind)
	assert.Equal(t, "revoked", r.View.State)
	_, _, aerr := fx.svc.Authenticate(res.AccessToken, oauthTestOrigin, "/mcp/oauth/cursor", fx.clock.Now())
	assert.ErrorIs(t, aerr, ErrOAuthUnauthorized)

	_, err = fx.creds.Revoke(fx.ctx(), fx.actor(), CredentialRef{Client: "cursor", Grant: "nope"}, false)
	var ce *CredentialError
	require.True(t, errors.As(err, &ce))
	assert.Equal(t, 404, ce.Status)
	_, err = fx.creds.Revoke(fx.ctx(), fx.actor(), CredentialRef{Token: "x", Grant: gid}, false)
	require.Error(t, err)
}

// --- profiles and the funnel ---------------------------------------------------------------------------------

func TestProfilesService_OAuthOnlyClientIsAUserOfItsProfile(t *testing.T) {
	h := newProfilesHarness(t)
	ctx := context.Background()
	commitOAuthOnlyClient(t, h.rt.StorageManager(), "reg1", "grantAAAAAAAAAAAAAAAAAA", "acme", "ro", auth.ProfileModeLocked)

	u, err := h.svc.UsedBy("ro")
	require.NoError(t, err)
	require.Len(t, u.Clients, 1)
	assert.Equal(t, "acme", u.Clients[0].ID)

	// Delete is refused while it points at the profile.
	_, err = h.svc.Delete(ctx, h.actor(), "ro", "", false)
	var inUse *ProfileInUseError
	require.True(t, errors.As(err, &inUse))
	require.Len(t, inUse.UsedBy.Clients, 1)

	// A revoked login no longer counts.
	_, err = h.rt.StorageManager().RevokeOAuthGrant("grantAAAAAAAAAAAAAAAAAA", storage.OAuthRevokeAdmin, time.Now())
	require.NoError(t, err)
	u, err = h.svc.UsedBy("ro")
	require.NoError(t, err)
	assert.Empty(t, u.Clients)
}

func TestProfilesService_ReassignAndRenameMoveTheOAuthOnlyClient(t *testing.T) {
	h := newProfilesHarness(t)
	ctx := context.Background()
	sm := h.rt.StorageManager()
	commitOAuthOnlyClient(t, sm, "reg1", "grantAAAAAAAAAAAAAAAAAA", "acme", "ro", auth.ProfileModeLocked)

	_, err := h.svc.Rename(ctx, h.actor(), "ro", "ro2")
	require.NoError(t, err)
	pin, _, _, _ := sm.CurrentClientBinding("acme")
	assert.Equal(t, "ro2", pin, "rename moves its pin")

	_, err = h.svc.Delete(ctx, h.actor(), "ro2", "full", false)
	require.NoError(t, err)
	pin, _, _, _ = sm.CurrentClientBinding("acme")
	assert.Equal(t, "full", pin, "reassign_to moves it")
}

func TestMutateConfig_FailedApplyRestoresAnOAuthOnlyClient(t *testing.T) {
	rt := newFunnelRuntime(t)
	sm := rt.StorageManager()
	commitOAuthOnlyClient(t, sm, "reg1", "grantAAAAAAAAAAAAAAAAAA", "acme", "ro", auth.ProfileModeLocked)

	blocker := filepath.Join(t.TempDir(), "blocker")
	require.NoError(t, os.WriteFile(blocker, []byte("x"), 0o600))
	rt.configSvc.UpdatePath(filepath.Join(blocker, "mcp_config.json"))

	_, _, err := rt.MutateConfig(context.Background(), funnelActor(), func(d *config.Config) (ChangeHint, error) {
		d.Profiles[0].Name = "ro2"
		return ChangeHint{Kind: profile.ChangeRename, Profile: "ro2", PreviousProfile: "ro"}, nil
	}, TokenRewrite{From: "ro", To: "ro2"})
	require.Error(t, err)
	pin, _, _, _ := sm.CurrentClientBinding("acme")
	assert.Equal(t, "ro", pin, "the failed commit restores the browser-login client")
	b, _ := sm.GetOAuthClientBinding("acme")
	assert.Equal(t, "ro", b.ProfilePin)
}

// The funnel judges the whole candidate, browser logins included: a config
// that turns auth off while a live login holds a named binding is refused.
func TestMutateConfig_GuardSeesAnOAuthOnlyBinding(t *testing.T) {
	rt := newFunnelRuntime(t)
	sm := rt.StorageManager()
	commitOAuthOnlyClient(t, sm, "reg1", "grantAAAAAAAAAAAAAAAAAA", "acme", "ro", auth.ProfileModeLocked)
	rt.SetBindingGuard(ConservativeBindingGuard{})

	_, _, err := rt.MutateConfig(context.Background(), funnelActor(), func(d *config.Config) (ChangeHint, error) {
		d.RequireMCPAuth = false
		return ChangeHint{}, nil
	}, TokenRewrite{})
	var ge *BindingGuardError
	require.ErrorAs(t, err, &ge)
	require.NotEmpty(t, ge.Bindings)
	assert.Equal(t, "acme", ge.Bindings[0].ClientID)

	// Once the login is revoked nothing is bound.
	_, err = sm.RevokeOAuthGrant("grantAAAAAAAAAAAAAAAAAA", storage.OAuthRevokeAdmin, time.Now())
	require.NoError(t, err)
	_, _, err = rt.MutateConfig(context.Background(), funnelActor(), func(d *config.Config) (ChangeHint, error) {
		d.RequireMCPAuth = false
		return ChangeHint{}, nil
	}, TokenRewrite{})
	require.NoError(t, err)
}

// --- the switch ---------------------------------------------------------------------------------------------------

// Applying a config in which the switch goes off cancels every pending and
// approved request BEFORE the new config is visible, on the commit path both
// the API update and the file reload end in, with no event delivered.
func TestCommit_TurningTheSwitchOffCancelsSignInRequests(t *testing.T) {
	rt := newFunnelRuntime(t)
	_, err := rt.Commit("test_on", func(tx *CommitTx) error {
		return tx.MutateGlobal(func(c *config.Config) error { c.MCPOAuthEnabled = true; return nil })
	})
	require.NoError(t, err)
	svc := rt.OAuthLoginService()
	require.True(t, svc.Enabled())

	reg, rerr, err := svc.Register(mcpRegistration())
	require.NoError(t, err)
	require.Nil(t, rerr)
	regID := reg["client_id"].(string)
	id, aerr := svc.BeginAuthorization(AuthorizeInput{
		Origin: oauthTestOrigin, ClientID: regID, RedirectURI: oauthTestRedirect, State: "s",
		CodeChallenge: oauthTestChallenge(), Method: "S256", Resource: oauthTestOrigin + "/mcp/oauth/cursor", ResponseType: "code",
	})
	require.Nil(t, aerr)
	redirect, err := svc.Approve(context.Background(), id, ApprovalChoice{Profile: oauthStrp("ro")})
	require.NoError(t, err)
	code := oauthCodeOf(t, redirect)
	pendingID, aerr := svc.BeginAuthorization(AuthorizeInput{
		Origin: oauthTestOrigin, ClientID: regID, RedirectURI: oauthTestRedirect, State: "s",
		CodeChallenge: oauthTestChallenge(), Method: "S256", Resource: oauthTestOrigin + "/mcp/oauth", ResponseType: "code",
	})
	require.Nil(t, aerr)

	_, err = rt.Commit("test_off", func(tx *CommitTx) error {
		return tx.MutateGlobal(func(c *config.Config) error { c.MCPOAuthEnabled = false; return nil })
	})
	require.NoError(t, err)
	_, err = rt.Commit("test_on_again", func(tx *CommitTx) error {
		return tx.MutateGlobal(func(c *config.Config) error { c.MCPOAuthEnabled = true; return nil })
	})
	require.NoError(t, err)

	_, terr := svc.Exchange(TokenInput{Origin: oauthTestOrigin, Code: code, RedirectURI: oauthTestRedirect, ClientID: regID, CodeVerifier: oauthTestVerifier})
	requireInvalidGrant(t, terr)
	_, err = svc.Approve(context.Background(), pendingID, ApprovalChoice{Client: ClientChoice{ID: "x"}})
	assert.ErrorIs(t, err, ErrOAuthRequestNotFound)
	used, _ := rt.StorageManager().OAuthLoginEverUsed()
	assert.False(t, used)
}

func mcpRegistration() mcplogin.RegistrationRequest {
	return mcplogin.RegistrationRequest{RedirectURIs: []string{oauthTestRedirect}, ClientName: "Cursor"}
}

// --- startup ------------------------------------------------------------------------------------------------------

// RepairOAuthClientBindings runs once at startup; on a database that never
// used browser login it opens no write transaction (the runtime is built in
// every funnel test: a never-used database stays free of the OAuth buckets).
func TestStartup_RepairIsANoOpWithoutBrowserLogin(t *testing.T) {
	rt := newFunnelRuntime(t)
	rt.repairOAuthBindings() // a second run changes nothing and does not panic
	b, err := rt.StorageManager().ListOAuthClientBindings()
	require.NoError(t, err)
	assert.Empty(t, b)
	used, _ := rt.StorageManager().OAuthLoginEverUsed()
	assert.False(t, used)
}
