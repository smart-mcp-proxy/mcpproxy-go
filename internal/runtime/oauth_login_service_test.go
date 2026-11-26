package runtime

import (
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/mcplogin"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
)

// --- BeginAuthorization ---------------------------------------------------------

func TestBeginAuthorization_OrderOfChecks(t *testing.T) {
	fx := newOAuthFx(t)
	reg := fx.register()
	fx.mintClient("codex", "ro", auth.ProfileModeLocked)

	in := func(mut func(*AuthorizeInput)) *AuthorizeError {
		a := fx.authInput(reg, oauthTestOrigin+"/mcp/oauth/codex")
		mut(&a)
		_, err := fx.svc.BeginAuthorization(a)
		return err
	}
	// 1. unknown client or unregistered redirect: no redirect, whatever else is wrong.
	e := in(func(a *AuthorizeInput) { a.ClientID = "nope"; a.ResponseType = "token" })
	require.NotNil(t, e)
	assert.Equal(t, AuthorizeNoRedirect, e.Kind)
	e = in(func(a *AuthorizeInput) { a.RedirectURI = "http://127.0.0.1:5555/other" })
	require.NotNil(t, e)
	assert.Equal(t, AuthorizeNoRedirect, e.Kind)
	// 2. a per-client resource of a client that does not exist: 404, no redirect, before the rest.
	e = in(func(a *AuthorizeInput) { a.Resource = oauthTestOrigin + "/mcp/oauth/ghost"; a.ResponseType = "token" })
	require.NotNil(t, e)
	assert.Equal(t, AuthorizeNotFound, e.Kind)
	// A registry id without a record exists.
	e = in(func(a *AuthorizeInput) { a.Resource = oauthTestOrigin + "/mcp/oauth/cursor" })
	assert.Nil(t, e)
	// 3. redirectable errors.
	e = in(func(a *AuthorizeInput) { a.ResponseType = "token" })
	require.NotNil(t, e)
	assert.Equal(t, AuthorizeRedirect, e.Kind)
	assert.Equal(t, "unsupported_response_type", e.Code)
	e = in(func(a *AuthorizeInput) { a.Method = "plain" })
	assert.Equal(t, "invalid_request", e.Code)
	e = in(func(a *AuthorizeInput) { a.CodeChallenge = "short" })
	assert.Equal(t, "invalid_request", e.Code)
	e = in(func(a *AuthorizeInput) { a.Resource = oauthTestOrigin + "/mcp" })
	assert.Equal(t, "invalid_target", e.Code)
	e = in(func(a *AuthorizeInput) { a.Resource = "http://localhost:8080/mcp/oauth/codex" })
	assert.Equal(t, "invalid_target", e.Code, "another origin is another resource")

	u, err := e.RedirectURL()
	require.NoError(t, err)
	pu, _ := url.Parse(u)
	assert.Equal(t, "invalid_target", pu.Query().Get("error"))
	assert.Equal(t, "st-1", pu.Query().Get("state"))
}

func TestBeginAuthorization_PublishesPendingAndMarksAuthorized(t *testing.T) {
	fx := newOAuthFx(t)
	reg := fx.register()
	id := fx.authorize(reg, "cursor")
	ev := fx.eventsOf(EventTypeOAuthAuthorizationPending)
	require.Len(t, ev, 1)
	assert.Equal(t, map[string]any{"request_id": id, "client_id": "cursor", "client_label": "cursor ide"}, ev[0].Payload)
	got, err := fx.sm.GetOAuthRegistration(reg)
	require.NoError(t, err)
	assert.Equal(t, "authorized", got.State)
	pend := fx.svc.ListPending()
	require.Len(t, pend, 1)
	assert.Equal(t, id, pend[0].ID)
	assert.Equal(t, "cursor ide", pend[0].ClientLabel)

	// The generic address names no client in the event.
	id2 := fx.authorize(reg, "")
	ev = fx.eventsOf(EventTypeOAuthAuthorizationPending)
	require.Len(t, ev, 2)
	assert.Equal(t, id2, ev[1].Payload["request_id"])
	assert.Equal(t, "", ev[1].Payload["client_id"])
}

// --- Describe -----------------------------------------------------------------------

func TestDescribe_FixedClientOnPerClientAddress(t *testing.T) {
	fx := newOAuthFx(t)
	fx.mintClient("codex", "ro", auth.ProfileModeLocked)
	reg := fx.register()
	id := fx.authorize(reg, "codex")

	v, err := fx.svc.Describe(fx.ctx(), id)
	require.NoError(t, err)
	assert.Equal(t, "client", v.AddressKind)
	require.NotNil(t, v.Client)
	assert.Equal(t, "codex", v.Client.ID)
	assert.True(t, v.Client.Exists)
	assert.True(t, v.Client.HasAPIKey)
	require.NotNil(t, v.Client.Binding)
	assert.Equal(t, "ro", v.Client.Binding.Profile)
	rev, err := fx.sm.ClientBindingRevision("codex")
	require.NoError(t, err)
	assert.Equal(t, rev, v.Client.Binding.Revision)
	assert.False(t, v.ProfileRequired, "the client already has a binding")
	assert.Empty(t, v.ExistingClients)
	assert.Nil(t, v.PreviousLogin)
	assert.Nil(t, v.Guard, "auth is on: nothing to refuse")
	assert.Equal(t, "cursor ide", v.ReportedName, "shown, unverified")
	assert.Len(t, v.Profiles, 2)
}

func TestDescribe_GenericAddressListsClientsNothingPreselected(t *testing.T) {
	fx := newOAuthFx(t)
	fx.mintClient("codex", "ro", auth.ProfileModeLocked)
	fx.mintClient("zed", "rw", auth.ProfileModeSwitchable)
	reg := fx.register() // reports itself as "Cursor IDE"
	id := fx.authorize(reg, "")

	v, err := fx.svc.Describe(fx.ctx(), id)
	require.NoError(t, err)
	assert.Equal(t, "generic", v.AddressKind)
	assert.Nil(t, v.Client, "the generic address fixes nothing")
	// Clients with records come first; supported built-ins without records
	// follow (#1971) and are marked.
	require.Greater(t, len(v.ExistingClients), 2)
	assert.Equal(t, "codex", v.ExistingClients[0].ID)
	assert.Equal(t, "zed", v.ExistingClients[1].ID)
	assert.False(t, v.ExistingClients[0].Builtin)
	assert.False(t, v.ExistingClients[1].Builtin)
	for _, c := range v.ExistingClients[2:] {
		assert.True(t, c.Builtin, c.ID)
		assert.Nil(t, c.Binding, c.ID)
	}
	assert.True(t, v.ProfileRequired)
	require.NotNil(t, v.SuggestedNewClient)
	assert.Equal(t, "cursor-ide", v.SuggestedNewClient.ID)

	// The reported name never selects: an empty choice stays incomplete even
	// though the app calls itself after an existing client.
	pv, err := fx.svc.Preview(fx.ctx(), id, ApprovalChoice{})
	require.NoError(t, err)
	assert.False(t, pv.Complete)
	assert.Nil(t, pv.Access)
}

func TestDescribe_SuggestedIDIsUniqueAndNeverARegistryID(t *testing.T) {
	fx := newOAuthFx(t)
	resp, _, err := fx.svc.Register(mcplogin.RegistrationRequest{RedirectURIs: []string{oauthTestRedirect}, ClientName: "Cursor"})
	require.NoError(t, err)
	id := fx.authorize(resp["client_id"].(string), "")
	v, err := fx.svc.Describe(fx.ctx(), id)
	require.NoError(t, err)
	assert.Equal(t, "cursor-2", v.SuggestedNewClient.ID, "cursor is a supported client's id")
	assert.Equal(t, "cursor", v.SuggestedNewClient.DisplayName)
}

func TestDescribe_ProfileRequiredOnlyWhenProfilesExistAndNoBinding(t *testing.T) {
	fx := newOAuthFx(t)
	reg := fx.register()
	id := fx.authorize(reg, "cursor") // a registry client with no record yet
	v, err := fx.svc.Describe(fx.ctx(), id)
	require.NoError(t, err)
	require.NotNil(t, v.Client)
	assert.False(t, v.Client.Exists)
	assert.Nil(t, v.Client.Binding)
	assert.True(t, v.ProfileRequired)

	fx.cfg.Profiles = nil
	v, err = fx.svc.Describe(fx.ctx(), id)
	require.NoError(t, err)
	assert.False(t, v.ProfileRequired, "no profiles are configured: All servers")
}

func TestDescribe_PreviousLoginOnlyForTheSameRegistration(t *testing.T) {
	fx := newOAuthFx(t)
	reg, _ := fx.loginNew("cursor", "ro")
	grants := fx.grants()
	require.Len(t, grants, 1)

	id := fx.authorize(reg, "cursor")
	v, err := fx.svc.Describe(fx.ctx(), id)
	require.NoError(t, err)
	require.NotNil(t, v.PreviousLogin)
	assert.Equal(t, grants[0].GrantID, v.PreviousLogin.GrantID)
	assert.Equal(t, 1, v.Client.Logins)

	// Another registration of the same client: signed in before? No: this
	// registration never was.
	other := fx.register()
	id2 := fx.authorize(other, "cursor")
	v2, err := fx.svc.Describe(fx.ctx(), id2)
	require.NoError(t, err)
	assert.Nil(t, v2.PreviousLogin)

	// A revoked login is not a previous login.
	_, err = fx.svc.RevokeGrant(fx.ctx(), "cursor", grants[0].GrantID)
	require.NoError(t, err)
	v, err = fx.svc.Describe(fx.ctx(), id)
	require.NoError(t, err)
	assert.Nil(t, v.PreviousLogin)
}

func TestDescribe_GuardVerdictForAFixedClientThatHasABinding(t *testing.T) {
	fx := newOAuthFx(t)
	fx.guard = ConservativeBindingGuard{}
	reg, _ := fx.loginNew("cursor", "ro") // auth on: allowed
	gs := fx.grants()
	_, err := fx.svc.RevokeGrant(fx.ctx(), "cursor", gs[0].GrantID)
	require.NoError(t, err)
	fx.cfg.RequireMCPAuth = false

	id := fx.authorize(reg, "cursor")
	v, err := fx.svc.Describe(fx.ctx(), id)
	require.NoError(t, err)
	require.NotNil(t, v.Guard, "a new live login on a named binding with auth off is refused")
	assert.Equal(t, profile.ErrorCodeBindingBypassable, v.Guard.Code)
	require.NotEmpty(t, v.Guard.Fixes)
	assert.Equal(t, profile.GuardFixRequireMCPAuth, v.Guard.Fixes[0].Kind)
}

func TestDescribe_DecidedOrUnknownRequestIsNotFound(t *testing.T) {
	fx := newOAuthFx(t)
	reg := fx.register()
	id := fx.authorize(reg, "cursor")
	_, err := fx.svc.Describe(fx.ctx(), "nope")
	assert.ErrorIs(t, err, ErrOAuthRequestNotFound)
	_, err = fx.svc.Deny(fx.ctx(), id)
	require.NoError(t, err)
	_, err = fx.svc.Describe(fx.ctx(), id)
	assert.ErrorIs(t, err, ErrOAuthRequestNotFound, "decided: one uniform answer")
}

// --- Preview ----------------------------------------------------------------------------

func TestPreview_CompleteVerdictAndWritesNothing(t *testing.T) {
	fx := newOAuthFx(t)
	reg := fx.register()
	id := fx.authorize(reg, "cursor")

	// Profiles exist and none is chosen: incomplete, no verdict.
	pv, err := fx.svc.Preview(fx.ctx(), id, ApprovalChoice{})
	require.NoError(t, err)
	assert.False(t, pv.Complete)
	assert.Nil(t, pv.Guard)

	pv, err = fx.svc.Preview(fx.ctx(), id, ApprovalChoice{Profile: oauthStrp("ro")})
	require.NoError(t, err)
	assert.True(t, pv.Complete)
	require.NotNil(t, pv.Access)
	assert.Equal(t, PreviewAccess{Client: "cursor", Profile: "ro", Mode: auth.ProfileModeLocked, SameAccessAsExistingClient: false}, *pv.Access)
	assert.Nil(t, pv.Guard)

	// auth off: the guard's verdict is part of the answer, not an error.
	fx.guard = ConservativeBindingGuard{}
	fx.cfg.RequireMCPAuth = false
	pv, err = fx.svc.Preview(fx.ctx(), id, ApprovalChoice{Profile: oauthStrp("ro")})
	require.NoError(t, err)
	require.NotNil(t, pv.Guard)
	assert.Equal(t, profile.ErrorCodeBindingBypassable, pv.Guard.Code)

	// Invalid choices are the same errors approve answers.
	_, err = fx.svc.Preview(fx.ctx(), id, ApprovalChoice{Profile: oauthStrp("ghost")})
	assert.ErrorIs(t, err, ErrOAuthProfileNotFound)

	fx.nothingStored()
	toks, lerr := fx.sm.ListAgentTokens()
	require.NoError(t, lerr)
	assert.Empty(t, toks, "a preview mints and writes nothing")
	assert.Empty(t, fx.changeRows("approve"))
	_, ok := fx.reqs.Get(id)
	assert.True(t, ok)
	r, _ := fx.reqs.Get(id)
	assert.Equal(t, mcplogin.StatusPending, r.Status)
}

func TestPreview_ExistingClientIsTheSameAccess(t *testing.T) {
	fx := newOAuthFx(t)
	fx.mintClient("codex", "ro", auth.ProfileModeLocked)
	reg := fx.register()
	id := fx.authorize(reg, "")
	rev, err := fx.sm.ClientBindingRevision("codex")
	require.NoError(t, err)
	pv, err := fx.svc.Preview(fx.ctx(), id, ApprovalChoice{Client: ClientChoice{ID: "codex"}, BindingRevision: rev})
	require.NoError(t, err)
	require.True(t, pv.Complete)
	assert.True(t, pv.Access.SameAccessAsExistingClient)
	assert.Equal(t, "ro", pv.Access.Profile)
}

// --- Approve and Deny ----------------------------------------------------------------------

func TestApprove_StoresNothingAndRecordsTheDecision(t *testing.T) {
	fx := newOAuthFx(t)
	reg := fx.register()
	id := fx.authorize(reg, "cursor")

	redirect, err := fx.svc.Approve(fx.ctx(), id, ApprovalChoice{Profile: oauthStrp("ro")})
	require.NoError(t, err)
	u, err := url.Parse(redirect)
	require.NoError(t, err)
	assert.NotEmpty(t, u.Query().Get("code"))
	assert.Equal(t, "st-1", u.Query().Get("state"))
	assert.Equal(t, "127.0.0.1:5555", u.Host)

	fx.nothingStored()
	toks, lerr := fx.sm.ListAgentTokens()
	require.NoError(t, lerr)
	assert.Empty(t, toks)
	r, ok := fx.reqs.Get(id)
	require.True(t, ok)
	assert.Equal(t, mcplogin.StatusApproved, r.Status)
	assert.Equal(t, "cursor", r.Decision.ClientID)
	assert.True(t, r.Decision.NewClient)
	assert.Equal(t, "ro", r.Decision.Profile)
	assert.Equal(t, auth.ProfileModeLocked, r.Decision.Mode)
	assert.Equal(t, "admin", r.Decision.ActorName)

	rows := fx.changeRows("approve")
	require.Len(t, rows, 1)
	assert.Equal(t, id, rows[0]["request_id"])
	assert.Equal(t, "api_key", rows[0]["actor_kind"])
	assert.Equal(t, "admin", rows[0]["actor_name"])
	assert.Equal(t, "cursor", rows[0]["client_id"])
	decided := fx.eventsOf(EventTypeOAuthAuthorizationDecided)
	require.Len(t, decided, 1)
	assert.Equal(t, "approved", decided[0].Payload["status"])
}

func TestApprove_NoStateInTheRedirectWhenTheClientSentNone(t *testing.T) {
	fx := newOAuthFx(t)
	reg := fx.register()
	in := fx.authInput(reg, oauthTestOrigin+"/mcp/oauth/cursor")
	in.State = ""
	id, aerr := fx.svc.BeginAuthorization(in)
	require.Nil(t, aerr)
	redirect, err := fx.svc.Approve(fx.ctx(), id, ApprovalChoice{Profile: oauthStrp("ro")})
	require.NoError(t, err)
	u, _ := url.Parse(redirect)
	assert.False(t, u.Query().Has("state"))
}

func TestDeny_RedirectsWithAccessDeniedAndWritesARow(t *testing.T) {
	fx := newOAuthFx(t)
	reg := fx.register()
	id := fx.authorize(reg, "cursor")
	redirect, err := fx.svc.Deny(fx.ctx(), id)
	require.NoError(t, err)
	u, _ := url.Parse(redirect)
	assert.Equal(t, "access_denied", u.Query().Get("error"))
	assert.Equal(t, "st-1", u.Query().Get("state"))
	assert.Empty(t, u.Query().Get("code"))
	rows := fx.changeRows("deny")
	require.Len(t, rows, 1)
	assert.Equal(t, id, rows[0]["request_id"])
	assert.Equal(t, "admin", rows[0]["actor_name"])
	fx.nothingStored()
	_, err = fx.svc.Deny(fx.ctx(), id)
	assert.ErrorIs(t, err, ErrOAuthAlreadyDecided)
}

func TestApprove_Refusals(t *testing.T) {
	fx := newOAuthFx(t)
	fx.mintClient("codex", "ro", auth.ProfileModeLocked)
	fx.mintClient("acme", "ro", auth.ProfileModeLocked)
	reg := fx.register()

	t.Run("already decided", func(t *testing.T) {
		id := fx.authorize(reg, "cursor")
		_, err := fx.svc.Approve(fx.ctx(), id, ApprovalChoice{Profile: oauthStrp("ro")})
		require.NoError(t, err)
		_, err = fx.svc.Approve(fx.ctx(), id, ApprovalChoice{Profile: oauthStrp("ro")})
		assert.ErrorIs(t, err, ErrOAuthAlreadyDecided)
		code, status, ok := OAuthErrorStatus(err)
		assert.True(t, ok)
		assert.Equal(t, "already_decided", code)
		assert.Equal(t, 409, status)
	})
	t.Run("client_fixed", func(t *testing.T) {
		id := fx.authorize(reg, "cursor")
		_, err := fx.svc.Approve(fx.ctx(), id, ApprovalChoice{Client: ClientChoice{ID: "codex"}, Profile: oauthStrp("ro")})
		assert.ErrorIs(t, err, ErrOAuthClientFixed)
		_, err = fx.svc.Approve(fx.ctx(), id, ApprovalChoice{Client: ClientChoice{New: &NewClientChoice{ID: "x"}}, Profile: oauthStrp("ro")})
		assert.ErrorIs(t, err, ErrOAuthClientFixed)
	})
	t.Run("profile_required", func(t *testing.T) {
		id := fx.authorize(reg, "cursor")
		_, err := fx.svc.Approve(fx.ctx(), id, ApprovalChoice{})
		assert.ErrorIs(t, err, ErrOAuthProfileRequired)
	})
	t.Run("client_required on the generic address", func(t *testing.T) {
		id := fx.authorize(reg, "")
		_, err := fx.svc.Approve(fx.ctx(), id, ApprovalChoice{Profile: oauthStrp("ro")})
		assert.ErrorIs(t, err, ErrOAuthClientRequired)
	})
	t.Run("profile_not_found", func(t *testing.T) {
		id := fx.authorize(reg, "cursor")
		_, err := fx.svc.Approve(fx.ctx(), id, ApprovalChoice{Profile: oauthStrp("ghost")})
		assert.ErrorIs(t, err, ErrOAuthProfileNotFound)
	})
	t.Run("identity_exists", func(t *testing.T) {
		id := fx.authorize(reg, "")
		_, err := fx.svc.Approve(fx.ctx(), id, ApprovalChoice{Client: ClientChoice{New: &NewClientChoice{ID: "acme"}}, Profile: oauthStrp("ro")})
		assert.ErrorIs(t, err, ErrOAuthIdentityExists)
	})
	t.Run("reserved_identity", func(t *testing.T) {
		id := fx.authorize(reg, "")
		_, err := fx.svc.Approve(fx.ctx(), id, ApprovalChoice{Client: ClientChoice{New: &NewClientChoice{ID: "cursor"}}, Profile: oauthStrp("ro")})
		assert.ErrorIs(t, err, ErrOAuthReservedIdentity)
		_, err = fx.svc.Approve(fx.ctx(), id, ApprovalChoice{Client: ClientChoice{New: &NewClientChoice{ID: "Bad Id"}}, Profile: oauthStrp("ro")})
		var ve *ValidationError
		assert.ErrorAs(t, err, &ve)
	})
	t.Run("binding_changed on a stale or missing revision", func(t *testing.T) {
		id := fx.authorize(reg, "codex")
		_, err := fx.svc.Approve(fx.ctx(), id, ApprovalChoice{BindingRevision: "stale"})
		assert.ErrorIs(t, err, ErrOAuthBindingChanged)
		_, err = fx.svc.Approve(fx.ctx(), id, ApprovalChoice{})
		assert.ErrorIs(t, err, ErrOAuthBindingChanged, "an existing client needs the revision the page displayed")
		rev, _ := fx.sm.ClientBindingRevision("codex")
		_, err = fx.svc.Approve(fx.ctx(), id, ApprovalChoice{BindingRevision: rev, Profile: oauthStrp("rw")})
		assert.NoError(t, err, "the profile of an existing client is ignored")
	})
	fx.nothingStored()
}

func TestApprove_GuardRefusalCarriesTheFixesAndLeavesTheRequestPending(t *testing.T) {
	fx := newOAuthFx(t)
	fx.guard = ConservativeBindingGuard{}
	fx.cfg.RequireMCPAuth = false
	reg := fx.register()
	id := fx.authorize(reg, "cursor")
	_, err := fx.svc.Approve(fx.ctx(), id, ApprovalChoice{Profile: oauthStrp("ro")})
	var ge *BindingGuardError
	require.ErrorAs(t, err, &ge, "a first approval is judged through its candidate entry")
	require.NotEmpty(t, ge.Fixes)
	assert.Equal(t, profile.GuardFixRequireMCPAuth, ge.Fixes[0].Kind)
	code, status, _ := OAuthErrorStatus(err)
	assert.Equal(t, profile.ErrorCodeBindingBypassable, code)
	assert.Equal(t, 409, status)
	r, _ := fx.reqs.Get(id)
	assert.Equal(t, mcplogin.StatusPending, r.Status)

	// All servers (no pin) is not a named binding.
	fx.cfg.Profiles = nil
	_, err = fx.svc.Approve(fx.ctx(), id, ApprovalChoice{})
	assert.NoError(t, err)
}

func TestApprove_NewCustomClientOnTheGenericAddress(t *testing.T) {
	fx := newOAuthFx(t)
	reg := fx.register()
	id := fx.authorize(reg, "")
	redirect, err := fx.svc.Approve(fx.ctx(), id, ApprovalChoice{
		Client: ClientChoice{New: &NewClientChoice{ID: "acme-agent", DisplayName: "Acme Agent"}}, Profile: oauthStrp("rw"), Mode: auth.ProfileModeSwitchable,
	})
	require.NoError(t, err)
	r, _ := fx.reqs.Get(id)
	assert.Equal(t, "acme-agent", r.Decision.ClientID)
	assert.Equal(t, "Acme Agent", r.Decision.DisplayName)
	assert.Equal(t, auth.ProfileModeSwitchable, r.Decision.Mode)
	assert.NotEmpty(t, oauthCodeOf(t, redirect))
}
