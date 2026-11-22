package runtime

import (
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/mcplogin"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// approvedCode runs authorize + approve for a fixed client and returns the code.
func (fx *oauthFx) approvedCode(reg, client string, choice ApprovalChoice) string {
	fx.t.Helper()
	id := fx.authorize(reg, client)
	redirect, err := fx.svc.Approve(fx.ctx(), id, choice)
	require.NoError(fx.t, err)
	return oauthCodeOf(fx.t, redirect)
}

func requireInvalidGrant(t *testing.T, terr *TokenError) {
	t.Helper()
	require.NotNil(t, terr)
	assert.Equal(t, TokenErrInvalidGrant, terr.Code)
}

// --- the exchange is the commit ---------------------------------------------------------

func TestExchange_StoresTheLoginAndWritesTheIssueRow(t *testing.T) {
	fx := newOAuthFx(t)
	reg := fx.register()
	code := fx.approvedCode(reg, "cursor", ApprovalChoice{Profile: oauthStrp("ro")})
	fx.nothingStored()

	res, terr := fx.svc.Exchange(fx.tokenInput(reg, code))
	require.Nil(t, terr)
	assert.True(t, strings.HasPrefix(res.AccessToken, mcplogin.AccessTokenPrefix))
	assert.True(t, strings.HasPrefix(res.RefreshToken, mcplogin.RefreshTokenPrefix))
	assert.Equal(t, "Bearer", res.TokenType)
	assert.Equal(t, 3600, res.ExpiresIn)

	gs := fx.grants()
	require.Len(t, gs, 1)
	g := gs[0]
	assert.Equal(t, "cursor", g.Client)
	assert.Equal(t, reg, g.RegistrationID)
	assert.Equal(t, oauthTestOrigin+"/mcp/oauth/cursor", g.Resource)
	assert.Equal(t, oauthTestOrigin, g.Issuer)
	assert.Equal(t, oauthTestRedirect, g.RedirectURI)
	assert.Equal(t, "admin", g.ApprovedBy.Name)
	assert.NotContains(t, g.AccessHash, res.AccessToken, "only hashes are stored")
	pin, mode, found, err := fx.sm.CurrentClientBinding("cursor")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "ro", pin)
	assert.Equal(t, auth.ProfileModeLocked, mode)
	used, _ := fx.sm.OAuthLoginEverUsed()
	assert.True(t, used)

	rows := fx.changeRows("issue")
	require.Len(t, rows, 1)
	assert.Equal(t, g.GrantID, rows[0]["grant_id"])
	assert.Equal(t, "client", rows[0]["trigger"])
	assert.Equal(t, "admin", rows[0]["actor_name"], "the approving administrator, not the client")
	assert.Equal(t, map[string]interface{}{"kind": "api_key", "name": "admin"}, rows[0]["approved_by"])
	ev := fx.eventsOf(EventTypeCredentialsChanged)
	require.NotEmpty(t, ev)
	last := ev[len(ev)-1].Payload
	assert.Equal(t, "browser_login", last["kind"])
	assert.Equal(t, "issue", last["change"])
	assert.Equal(t, g.GrantID, last["id"])

	// The access token authenticates exactly where it was approved.
	ac, grant, aerr := fx.svc.Authenticate(res.AccessToken, oauthTestOrigin, "/mcp/oauth/cursor", fx.clock.Now())
	require.NoError(t, aerr)
	assert.Equal(t, g.GrantID, grant.GrantID)
	assert.Equal(t, auth.CredentialKindOAuth, ac.CredentialKind)
	assert.Equal(t, g.GrantID, ac.GrantID)
	assert.Equal(t, g.GrantID, ac.TokenGeneration)
	assert.Equal(t, "cursor", ac.ClientID)
	assert.Equal(t, "client-cursor", ac.AgentName)
	assert.Equal(t, "ro", ac.ProfilePin)
	assert.Equal(t, auth.ProfileModeLocked, ac.ProfileMode)
	assert.True(t, ac.IsClientCredential())
}

func TestExchange_ExistingAPIKeyClientGetsItsFirstLoginFromItsCurrentBinding(t *testing.T) {
	fx := newOAuthFx(t)
	fx.mintClient("codex", "ro", auth.ProfileModeLocked)
	reg := fx.register()
	id := fx.authorize(reg, "codex")
	rev, _ := fx.sm.ClientBindingRevision("codex")
	redirect, err := fx.svc.Approve(fx.ctx(), id, ApprovalChoice{BindingRevision: rev})
	require.NoError(t, err)
	_, terr := fx.svc.Exchange(fx.tokenInput(reg, oauthCodeOf(t, redirect)))
	require.Nil(t, terr)
	b, err := fx.sm.GetOAuthClientBinding("codex")
	require.NoError(t, err)
	require.NotNil(t, b)
	assert.Equal(t, "ro", b.ProfilePin)
}

func TestExchange_FirstApprovalIsJudgedByTheGuardAtExchange(t *testing.T) {
	fx := newOAuthFx(t)
	fx.guard = ConservativeBindingGuard{}
	reg := fx.register()
	code := fx.approvedCode(reg, "cursor", ApprovalChoice{Profile: oauthStrp("ro")}) // auth on: allowed
	fx.cfg.RequireMCPAuth = false                                                    // ...then auth goes off

	_, terr := fx.svc.Exchange(fx.tokenInput(reg, code))
	requireInvalidGrant(t, terr)
	fx.nothingStored()
	rows := fx.changeRows("deny")
	require.Len(t, rows, 1)
	assert.Equal(t, "guard", rows[0]["reason"])
	assert.Equal(t, "client", rows[0]["trigger"])
	assert.Equal(t, "admin", rows[0]["actor_name"], "the approving administrator")
}

func TestExchange_RefusalsAreInvalidGrantAndBurnTheCode(t *testing.T) {
	cases := map[string]func(in *TokenInput){
		"wrong verifier":     func(in *TokenInput) { in.CodeVerifier = strings.Repeat("w", 43) },
		"wrong client":       func(in *TokenInput) { in.ClientID = "someoneelse" },
		"wrong redirect uri": func(in *TokenInput) { in.RedirectURI = "http://127.0.0.1:5556/cb" },
	}
	for name, mut := range cases {
		t.Run(name, func(t *testing.T) {
			fx := newOAuthFx(t)
			reg := fx.register()
			code := fx.approvedCode(reg, "cursor", ApprovalChoice{Profile: oauthStrp("ro")})
			in := fx.tokenInput(reg, code)
			mut(&in)
			_, terr := fx.svc.Exchange(in)
			requireInvalidGrant(t, terr)
			fx.nothingStored()
			// Single use: even the right request now fails.
			_, terr = fx.svc.Exchange(fx.tokenInput(reg, code))
			requireInvalidGrant(t, terr)
			fx.nothingStored()
		})
	}
}

func TestExchange_ResourceMustEqualTheApprovedOne(t *testing.T) {
	fx := newOAuthFx(t)
	reg := fx.register()
	code := fx.approvedCode(reg, "cursor", ApprovalChoice{Profile: oauthStrp("ro")})
	in := fx.tokenInput(reg, code)
	in.Resource = oauthTestOrigin + "/mcp/oauth/codex"
	_, terr := fx.svc.Exchange(in)
	require.NotNil(t, terr)
	assert.Equal(t, TokenErrInvalidTarget, terr.Code)
	fx.nothingStored()

	// The same resource, and an absent one, are fine.
	code = fx.approvedCode(reg, "cursor", ApprovalChoice{Profile: oauthStrp("ro")})
	in = fx.tokenInput(reg, code)
	in.Resource = oauthTestOrigin + "/mcp/oauth/cursor"
	_, terr = fx.svc.Exchange(in)
	require.Nil(t, terr)
}

func TestExchange_OriginMismatchIsRefusedFirstAndConsumesNothing(t *testing.T) {
	fx := newOAuthFx(t)
	reg := fx.register()
	code := fx.approvedCode(reg, "cursor", ApprovalChoice{Profile: oauthStrp("ro")})
	in := fx.tokenInput(reg, code)
	in.Origin = "http://localhost:8080"
	_, terr := fx.svc.Exchange(in)
	requireInvalidGrant(t, terr)
	fx.nothingStored()
	_, terr = fx.svc.Exchange(fx.tokenInput(reg, code))
	require.Nil(t, terr, "the code is still usable on the right origin")
}

func TestExchange_CodeExpiryAndReuse(t *testing.T) {
	fx := newOAuthFx(t)
	reg := fx.register()

	code := fx.approvedCode(reg, "cursor", ApprovalChoice{Profile: oauthStrp("ro")})
	fx.clock.Advance(mcplogin.CodeLifetime)
	_, terr := fx.svc.Exchange(fx.tokenInput(reg, code))
	requireInvalidGrant(t, terr)
	fx.nothingStored()

	// Reuse inside the tombstone window revokes the login it produced.
	code = fx.approvedCode(reg, "cursor", ApprovalChoice{Profile: oauthStrp("ro")})
	res, terr := fx.svc.Exchange(fx.tokenInput(reg, code))
	require.Nil(t, terr)
	_, terr = fx.svc.Exchange(fx.tokenInput(reg, code))
	requireInvalidGrant(t, terr)
	gs := fx.grants()
	require.Len(t, gs, 1)
	assert.True(t, gs[0].Revoked)
	assert.Equal(t, storage.OAuthRevokeReplay, gs[0].RevokeReason)
	rows := fx.changeRows("replay_revoke")
	require.Len(t, rows, 1)
	assert.Equal(t, gs[0].GrantID, rows[0]["grant_id"])
	assert.Equal(t, "client", rows[0]["trigger"])
	assert.Equal(t, "admin", rows[0]["actor_name"])
	_, _, aerr := fx.svc.Authenticate(res.AccessToken, oauthTestOrigin, "/mcp/oauth/cursor", fx.clock.Now())
	assert.ErrorIs(t, aerr, ErrOAuthUnauthorized, "a revoked login stops authenticating")

	// After the window the code is simply unknown: nothing more is revoked.
	fx.clock.Advance(mcplogin.TombstoneLifetime + time.Second)
	_, terr = fx.svc.Exchange(fx.tokenInput(reg, code))
	requireInvalidGrant(t, terr)
	assert.Len(t, fx.changeRows("replay_revoke"), 1)
}

func TestExchange_ReuseOfACodeWhoseCommitFailedRevokesNothing(t *testing.T) {
	fx := newOAuthFx(t)
	fx.mintClient("codex", "ro", auth.ProfileModeLocked)
	reg := fx.register()
	id := fx.authorize(reg, "codex")
	rev, _ := fx.sm.ClientBindingRevision("codex")
	redirect, err := fx.svc.Approve(fx.ctx(), id, ApprovalChoice{BindingRevision: rev})
	require.NoError(t, err)
	code := oauthCodeOf(t, redirect)
	_, _, err = fx.clients.SetBindingForTest(fx, "rw")
	require.NoError(t, err)

	_, terr := fx.svc.Exchange(fx.tokenInput(reg, code))
	requireInvalidGrant(t, terr)
	_, terr = fx.svc.Exchange(fx.tokenInput(reg, code))
	requireInvalidGrant(t, terr)
	assert.Empty(t, fx.changeRows("replay_revoke"), "an empty grant id means there is nothing to revoke")
}

// SetBindingForTest changes codex's binding through the real clients service.
func (s *ClientsService) SetBindingForTest(fx *oauthFx, pin string) (*ClientCredentialView, struct{}, error) {
	v, err := s.SetBinding(fx.ctx(), fx.actor(), "codex", pin, nil)
	return v, struct{}{}, err
}

func TestExchange_BindingChangedAfterApprovalStoresNothing(t *testing.T) {
	fx := newOAuthFx(t)
	fx.mintClient("codex", "ro", auth.ProfileModeLocked)
	reg := fx.register()
	id := fx.authorize(reg, "codex")
	rev, _ := fx.sm.ClientBindingRevision("codex")
	redirect, err := fx.svc.Approve(fx.ctx(), id, ApprovalChoice{BindingRevision: rev})
	require.NoError(t, err)
	_, _, err = fx.clients.SetBindingForTest(fx, "rw")
	require.NoError(t, err)

	_, terr := fx.svc.Exchange(fx.tokenInput(reg, oauthCodeOf(t, redirect)))
	requireInvalidGrant(t, terr)
	assert.Empty(t, fx.grants())
	rows := fx.changeRows("deny")
	require.Len(t, rows, 1)
	assert.Equal(t, "binding_changed", rows[0]["reason"])
	assert.Equal(t, "client", rows[0]["trigger"])
	assert.Equal(t, "admin", rows[0]["actor_name"])
}

func TestExchange_ReplaceGrantRevokesTheOldLogin(t *testing.T) {
	fx := newOAuthFx(t)
	reg, first := fx.loginNew("cursor", "ro")
	old := fx.grants()[0]

	id := fx.authorize(reg, "cursor")
	rev, _ := fx.sm.ClientBindingRevision("cursor")
	redirect, err := fx.svc.Approve(fx.ctx(), id, ApprovalChoice{BindingRevision: rev, ReplaceGrant: old.GrantID})
	require.NoError(t, err)
	second, terr := fx.svc.Exchange(fx.tokenInput(reg, oauthCodeOf(t, redirect)))
	require.Nil(t, terr)

	_, _, aerr := fx.svc.Authenticate(first.AccessToken, oauthTestOrigin, "/mcp/oauth/cursor", fx.clock.Now())
	assert.ErrorIs(t, aerr, ErrOAuthUnauthorized)
	_, _, aerr = fx.svc.Authenticate(second.AccessToken, oauthTestOrigin, "/mcp/oauth/cursor", fx.clock.Now())
	assert.NoError(t, aerr)
	for _, g := range fx.grants() {
		if g.GrantID == old.GrantID {
			assert.True(t, g.Revoked)
			assert.Equal(t, storage.OAuthRevokeReplaced, g.RevokeReason)
		}
	}

	// A replace_grant of someone else's login is ignored, never revoked.
	other := fx.register()
	id = fx.authorize(other, "cursor")
	rev, _ = fx.sm.ClientBindingRevision("cursor")
	live := ""
	for _, g := range fx.grants() {
		if !g.Revoked {
			live = g.GrantID
		}
	}
	redirect, err = fx.svc.Approve(fx.ctx(), id, ApprovalChoice{BindingRevision: rev, ReplaceGrant: live})
	require.NoError(t, err)
	_, terr = fx.svc.Exchange(fx.tokenInput(other, oauthCodeOf(t, redirect)))
	require.Nil(t, terr)
	_, _, aerr = fx.svc.Authenticate(second.AccessToken, oauthTestOrigin, "/mcp/oauth/cursor", fx.clock.Now())
	assert.NoError(t, aerr, "another registration's login is not replaced")
}

// --- cancellation after approval ------------------------------------------------------------

func TestExchange_AfterApprovalRevokeClientFails(t *testing.T) {
	fx := newOAuthFx(t)
	fx.mintClient("codex", "ro", auth.ProfileModeLocked)
	reg := fx.register()
	id := fx.authorize(reg, "codex")
	rev, _ := fx.sm.ClientBindingRevision("codex")
	redirect, err := fx.svc.Approve(fx.ctx(), id, ApprovalChoice{BindingRevision: rev})
	require.NoError(t, err)

	_, err = fx.creds.Revoke(fx.ctx(), fx.actor(), CredentialRef{Client: "codex"}, false)
	require.NoError(t, err)
	_, terr := fx.svc.Exchange(fx.tokenInput(reg, oauthCodeOf(t, redirect)))
	requireInvalidGrant(t, terr)
	assert.Empty(t, fx.grants(), "a revoke changes neither pin nor mode: only the cancellation catches it")
}

func TestExchange_AfterApprovalForgetAndRecreateFails(t *testing.T) {
	fx := newOAuthFx(t)
	fx.mintClient("codex", "ro", auth.ProfileModeLocked)
	reg := fx.register()
	id := fx.authorize(reg, "codex")
	rev, _ := fx.sm.ClientBindingRevision("codex")
	redirect, err := fx.svc.Approve(fx.ctx(), id, ApprovalChoice{BindingRevision: rev})
	require.NoError(t, err)

	_, err = fx.clients.Forget(fx.ctx(), fx.actor(), "codex", false)
	require.NoError(t, err)
	fx.mintClient("codex", "ro", auth.ProfileModeLocked) // recreated with the very same binding
	_, terr := fx.svc.Exchange(fx.tokenInput(reg, oauthCodeOf(t, redirect)))
	requireInvalidGrant(t, terr)
	assert.Empty(t, fx.grants())
}

func TestExchange_AfterApprovalFeatureOffThenOnFailsEvenWithoutAnEvent(t *testing.T) {
	fx := newOAuthFx(t)
	reg := fx.register()
	code := fx.approvedCode(reg, "cursor", ApprovalChoice{Profile: oauthStrp("ro")})
	pendingID := fx.authorize(reg, "cursor")

	// The synchronous step of applying a config in which the switch goes off.
	assert.Equal(t, 2, fx.svc.CancelAll("feature_disabled"))
	fx.cfg.MCPOAuthEnabled = false
	_, terr := fx.svc.Exchange(fx.tokenInput(reg, code))
	requireInvalidGrant(t, terr)
	fx.cfg.MCPOAuthEnabled = true // on again: the code is still dead
	_, terr = fx.svc.Exchange(fx.tokenInput(reg, code))
	requireInvalidGrant(t, terr)
	fx.nothingStored()
	_, err := fx.svc.Approve(fx.ctx(), pendingID, ApprovalChoice{Profile: oauthStrp("ro")})
	assert.ErrorIs(t, err, ErrOAuthRequestNotFound, "a cancelled pending request cannot be approved")
	assert.NotEmpty(t, fx.eventsOf(EventTypeOAuthAuthorizationDecided))
}

func TestExchange_TwoApprovedNewClientRequestsWithTheSameID(t *testing.T) {
	fx := newOAuthFx(t)
	choice := ApprovalChoice{Client: ClientChoice{New: &NewClientChoice{ID: "acme"}}, Profile: oauthStrp("ro")}
	regA, regB := fx.register(), fx.register()
	codeA := fx.approvedCode(regA, "", choice)
	codeB := fx.approvedCode(regB, "", choice)

	_, terr := fx.svc.Exchange(fx.tokenInput(regA, codeA))
	require.Nil(t, terr)
	_, terr = fx.svc.Exchange(fx.tokenInput(regB, codeB))
	requireInvalidGrant(t, terr)
	gs := fx.grants()
	require.Len(t, gs, 1, "the second is not turned into an existing-client login")
	assert.Equal(t, regA, gs[0].RegistrationID)
	rows := fx.changeRows("deny")
	require.Len(t, rows, 1)
	assert.Equal(t, "client_exists", rows[0]["reason"])
}

func TestExchange_RegistrationEvictedAfterApproval(t *testing.T) {
	fx := newOAuthFx(t)
	reg := fx.register()
	code := fx.approvedCode(reg, "cursor", ApprovalChoice{Profile: oauthStrp("ro")})
	n, err := fx.sm.SweepOAuthRegistrations(fx.clock.Now().Add(48 * time.Hour))
	require.NoError(t, err)
	require.Equal(t, 1, n)

	_, terr := fx.svc.Exchange(fx.tokenInput(reg, code))
	requireInvalidGrant(t, terr)
	fx.nothingStored()
	_, err = fx.sm.GetOAuthRegistration(reg)
	assert.ErrorIs(t, err, storage.ErrOAuthRegistrationGone, "it is never recreated from the code")
}

func TestExchange_FeatureOffRefusesEverything(t *testing.T) {
	fx := newOAuthFx(t)
	reg := fx.register()
	code := fx.approvedCode(reg, "cursor", ApprovalChoice{Profile: oauthStrp("ro")})
	fx.cfg.MCPOAuthEnabled = false
	_, terr := fx.svc.Exchange(fx.tokenInput(reg, code))
	requireInvalidGrant(t, terr)
	_, terr = fx.svc.Refresh(TokenInput{Origin: oauthTestOrigin, RefreshToken: "x", ClientID: reg})
	requireInvalidGrant(t, terr)
	_, _, aerr := fx.svc.Authenticate("mcp_oat_x", oauthTestOrigin, "/mcp/oauth", fx.clock.Now())
	assert.ErrorIs(t, aerr, ErrOAuthUnauthorized)
}

// --- authentication ------------------------------------------------------------------------------

func TestAuthenticate_AudienceBinding(t *testing.T) {
	fx := newOAuthFx(t)
	_, res := fx.loginNew("cursor", "ro")
	now := fx.clock.Now()

	_, _, err := fx.svc.Authenticate(res.AccessToken, oauthTestOrigin, "/mcp/oauth/cursor", now)
	assert.NoError(t, err)
	_, _, err = fx.svc.Authenticate(res.AccessToken, oauthTestOrigin, "/mcp/oauth/cursor/", now)
	assert.NoError(t, err, "a trailing slash names the same resource path")
	for name, c := range map[string]struct{ origin, path string }{
		"another client's address": {oauthTestOrigin, "/mcp/oauth/codex"},
		"the generic address":      {oauthTestOrigin, "/mcp/oauth"},
		"another origin":           {"http://localhost:8080", "/mcp/oauth/cursor"},
		"the plain MCP endpoint":   {oauthTestOrigin, "/mcp"},
	} {
		_, _, err := fx.svc.Authenticate(res.AccessToken, c.origin, c.path, now)
		assert.ErrorIs(t, err, ErrOAuthUnauthorized, name)
	}
	_, _, err = fx.svc.Authenticate("mcp_oat_notatoken", oauthTestOrigin, "/mcp/oauth/cursor", now)
	assert.ErrorIs(t, err, ErrOAuthUnauthorized)
	_, _, err = fx.svc.Authenticate("mcp_cli_something", oauthTestOrigin, "/mcp/oauth/cursor", now)
	assert.ErrorIs(t, err, ErrOAuthUnauthorized)
	_, _, err = fx.svc.Authenticate(res.AccessToken, oauthTestOrigin, "/mcp/oauth/cursor", now.Add(time.Hour))
	assert.ErrorIs(t, err, ErrOAuthUnauthorized, "the access token lives one hour")
}

func TestAuthenticate_GenericResourceGrantOnlyOnTheGenericAddress(t *testing.T) {
	fx := newOAuthFx(t)
	reg := fx.register()
	code := fx.approvedCode(reg, "", ApprovalChoice{Client: ClientChoice{New: &NewClientChoice{ID: "acme"}}, Profile: oauthStrp("ro")})
	res, terr := fx.svc.Exchange(fx.tokenInput(reg, code))
	require.Nil(t, terr)
	_, _, err := fx.svc.Authenticate(res.AccessToken, oauthTestOrigin, "/mcp/oauth", fx.clock.Now())
	assert.NoError(t, err)
	_, _, err = fx.svc.Authenticate(res.AccessToken, oauthTestOrigin, "/mcp/oauth/acme", fx.clock.Now())
	assert.ErrorIs(t, err, ErrOAuthUnauthorized)
}

func TestAuthenticate_BindingIsReadThroughTheAPIKeyRecord(t *testing.T) {
	fx := newOAuthFx(t)
	fx.mintClient("codex", "ro", auth.ProfileModeLocked)
	reg := fx.register()
	id := fx.authorize(reg, "codex")
	rev, _ := fx.sm.ClientBindingRevision("codex")
	redirect, err := fx.svc.Approve(fx.ctx(), id, ApprovalChoice{BindingRevision: rev})
	require.NoError(t, err)
	res, terr := fx.svc.Exchange(fx.tokenInput(reg, oauthCodeOf(t, redirect)))
	require.Nil(t, terr)

	ac, _, aerr := fx.svc.Authenticate(res.AccessToken, oauthTestOrigin, "/mcp/oauth/codex", fx.clock.Now())
	require.NoError(t, aerr)
	assert.Equal(t, "ro", ac.ProfilePin)
	// An older binary moved the API-key record only; the entry is stale.
	_, _, uerr := fx.sm.UpdateClientCredentialBinding("codex", "rw", auth.ProfileModeLocked)
	require.NoError(t, uerr)
	ac, _, aerr = fx.svc.Authenticate(res.AccessToken, oauthTestOrigin, "/mcp/oauth/codex", fx.clock.Now())
	require.NoError(t, aerr)
	assert.Equal(t, "rw", ac.ProfilePin)
}

// --- refresh ------------------------------------------------------------------------------------------

func (fx *oauthFx) refresh(reg, token string) (TokenResult, *TokenError) {
	return fx.svc.Refresh(TokenInput{Origin: oauthTestOrigin, RefreshToken: token, ClientID: reg})
}

func TestRefresh_RotatesAndNeverExtendsAnExpiredLogin(t *testing.T) {
	fx := newOAuthFx(t)
	reg, first := fx.loginNew("cursor", "ro")
	gid := fx.grants()[0].GrantID

	fx.clock.Advance(10 * 24 * time.Hour)
	second, terr := fx.refresh(reg, first.RefreshToken)
	require.Nil(t, terr)
	assert.NotEqual(t, first.RefreshToken, second.RefreshToken)
	assert.NotEqual(t, first.AccessToken, second.AccessToken)
	g, _ := fx.sm.GetOAuthGrant(gid)
	assert.True(t, g.RefreshExpiresAt.Equal(fx.clock.Now().Add(OAuthRefreshTokenTTL)), "30 days from the last rotation")
	_, _, aerr := fx.svc.Authenticate(first.AccessToken, oauthTestOrigin, "/mcp/oauth/cursor", fx.clock.Now())
	assert.ErrorIs(t, aerr, ErrOAuthUnauthorized, "the previous access token is replaced")
	_, _, aerr = fx.svc.Authenticate(second.AccessToken, oauthTestOrigin, "/mcp/oauth/cursor", fx.clock.Now())
	assert.NoError(t, aerr)

	// Expiry: 31 days of silence. Refused, not extended, nothing written.
	before, _ := fx.sm.GetOAuthGrant(gid)
	fx.clock.Advance(31 * 24 * time.Hour)
	_, terr = fx.refresh(reg, second.RefreshToken)
	requireInvalidGrant(t, terr)
	after, _ := fx.sm.GetOAuthGrant(gid)
	assert.Equal(t, before.RefreshExpiresAt, after.RefreshExpiresAt)
	assert.False(t, after.Revoked)
}

func TestRefresh_AuthenticReplayRevokesAndForgeryDoesNot(t *testing.T) {
	fx := newOAuthFx(t)
	reg, first := fx.loginNew("cursor", "ro")
	gid := fx.grants()[0].GrantID
	second, terr := fx.refresh(reg, first.RefreshToken)
	require.Nil(t, terr)

	// A made-up token for a real grant id: bad MAC, nothing written.
	forged := first.RefreshToken[:len(first.RefreshToken)-3] + "AAA"
	_, terr = fx.refresh(reg, forged)
	requireInvalidGrant(t, terr)
	g, _ := fx.sm.GetOAuthGrant(gid)
	assert.False(t, g.Revoked, "knowing a grant id is not enough to disturb a grant")
	_, terr = fx.refresh(reg, "mcp_ort_"+gid+"_"+strings.Repeat("A", 43)+"_"+strings.Repeat("B", 22))
	requireInvalidGrant(t, terr)
	g, _ = fx.sm.GetOAuthGrant(gid)
	assert.False(t, g.Revoked)

	// The authentic, already-spent token comes back: the login is revoked.
	_, terr = fx.refresh(reg, first.RefreshToken)
	requireInvalidGrant(t, terr)
	g, _ = fx.sm.GetOAuthGrant(gid)
	assert.True(t, g.Revoked)
	assert.Equal(t, storage.OAuthRevokeReplay, g.RevokeReason)
	rows := fx.changeRows("replay_revoke")
	require.Len(t, rows, 1)
	assert.Equal(t, gid, rows[0]["grant_id"])
	assert.Equal(t, "client", rows[0]["trigger"])
	evs := fx.eventsOf(EventTypeCredentialsChanged)
	assert.Equal(t, "replay_revoke", evs[len(evs)-1].Payload["change"])
	// The current token is dead too now.
	_, terr = fx.refresh(reg, second.RefreshToken)
	requireInvalidGrant(t, terr)
}

func TestRefresh_ClientOriginAndResourceChecks(t *testing.T) {
	fx := newOAuthFx(t)
	reg, first := fx.loginNew("cursor", "ro")
	gid := fx.grants()[0].GrantID

	_, terr := fx.refresh("someoneelse", first.RefreshToken)
	requireInvalidGrant(t, terr)
	_, terr = fx.svc.Refresh(TokenInput{Origin: "http://localhost:8080", RefreshToken: first.RefreshToken, ClientID: reg})
	requireInvalidGrant(t, terr)
	_, terr = fx.svc.Refresh(TokenInput{Origin: oauthTestOrigin, RefreshToken: first.RefreshToken, ClientID: reg, Resource: oauthTestOrigin + "/mcp/oauth"})
	require.NotNil(t, terr)
	assert.Equal(t, TokenErrInvalidTarget, terr.Code)
	g, _ := fx.sm.GetOAuthGrant(gid)
	assert.False(t, g.Revoked, "none of these wrote anything")

	_, terr = fx.svc.Refresh(TokenInput{Origin: oauthTestOrigin, RefreshToken: first.RefreshToken, ClientID: reg, Resource: oauthTestOrigin + "/mcp/oauth/cursor"})
	require.Nil(t, terr, "the same resource is accepted")
}

// --- admin revoke ------------------------------------------------------------------------------------------

func TestRevokeGrant_NamesTheRequestActorAndStopsTheToken(t *testing.T) {
	fx := newOAuthFx(t)
	_, res := fx.loginNew("cursor", "ro")
	gid := fx.grants()[0].GrantID
	n := &countingNotifier{}
	fx.svc.SetNotifier(n)

	changed, err := fx.svc.RevokeGrant(fx.ctx(), "cursor", gid)
	require.NoError(t, err)
	assert.True(t, changed)
	rows := fx.changeRows("revoke")
	require.Len(t, rows, 1)
	assert.Equal(t, gid, rows[0]["grant_id"])
	assert.Equal(t, "admin", rows[0]["actor_name"])
	assert.NotContains(t, rows[0], "trigger", "an administrator's action, not the client's")
	assert.Equal(t, 1, int(n.calls.Load()))
	evs := fx.eventsOf(EventTypeCredentialsChanged)
	assert.Equal(t, "revoke", evs[len(evs)-1].Payload["change"])
	_, _, aerr := fx.svc.Authenticate(res.AccessToken, oauthTestOrigin, "/mcp/oauth/cursor", fx.clock.Now())
	assert.ErrorIs(t, aerr, ErrOAuthUnauthorized, "the next request is refused")

	changed, err = fx.svc.RevokeGrant(fx.ctx(), "cursor", gid)
	require.NoError(t, err)
	assert.False(t, changed, "already revoked")
	_, err = fx.svc.RevokeGrant(fx.ctx(), "codex", gid)
	assert.ErrorIs(t, err, ErrOAuthGrantNotFound)
	_, err = fx.svc.RevokeGrant(fx.ctx(), "cursor", "nope")
	assert.ErrorIs(t, err, ErrOAuthGrantNotFound)
}

type countingNotifier struct{ calls atomic.Int32 }

func (c *countingNotifier) NotifyBindingChanged(string) { c.calls.Add(1) }

// --- the lock order --------------------------------------------------------------------------------------------

// Exchange takes bindingWriteMu before the request store, and revoke/forget take
// it before CancelForClient: one order, so hammering both finishes. Run under
// -race.
func TestExchangeAgainstRevokeClientDoesNotDeadlockOrRace(t *testing.T) {
	fx := newOAuthFx(t)
	fx.mintClient("codex", "ro", auth.ProfileModeLocked)
	const n = 12
	type pending struct{ reg, code string }
	var ps []pending
	for i := 0; i < n; i++ {
		reg := fx.register()
		id := fx.authorize(reg, "codex")
		rev, _ := fx.sm.ClientBindingRevision("codex")
		redirect, err := fx.svc.Approve(fx.ctx(), id, ApprovalChoice{BindingRevision: rev})
		require.NoError(t, err)
		ps = append(ps, pending{reg, oauthCodeOf(t, redirect)})
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		var wg sync.WaitGroup
		for _, p := range ps {
			p := p
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, _ = fx.svc.Exchange(fx.tokenInput(p.reg, p.code))
			}()
		}
		for i := 0; i < 4; i++ {
			wg.Add(2)
			go func() {
				defer wg.Done()
				_, _ = fx.creds.Revoke(fx.ctx(), fx.actor(), CredentialRef{Client: "codex"}, false)
			}()
			go func() {
				defer wg.Done()
				fx.svc.CancelForClient("codex")
				fx.svc.CancelAll("race")
			}()
		}
		wg.Wait()
	}()
	select {
	case <-done:
	case <-time.After(60 * time.Second):
		t.Fatal("deadlock: Exchange and revoke/cancel never finished")
	}
	// Whatever won, no login is left live for a revoked client.
	for _, g := range fx.grants() {
		assert.True(t, g.Revoked, "a login that slipped in before the revoke is revoked by it")
	}
}

var _ = profile.ChangeApprove
