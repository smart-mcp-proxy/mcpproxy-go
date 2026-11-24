package runtime

import (
	"context"
	"sync"
	"sync/atomic"
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

// --- the exchange is fenced against config publication (real runtime) -----------------------------

func enabledFunnelRuntime(t *testing.T) *Runtime {
	t.Helper()
	rt := newFunnelRuntime(t)
	_, err := rt.Commit("test_on", func(tx *CommitTx) error {
		return tx.MutateGlobal(func(c *config.Config) error { c.MCPOAuthEnabled = true; return nil })
	})
	require.NoError(t, err)
	return rt
}

func rtActor() context.Context {
	return ContextWithActor(context.Background(), Actor{Kind: "api_key", Name: "admin", Surface: "api"})
}

// rtApprovedNewClient registers, authorizes at the generic address and approves a
// NEW client bound to profile pin; it returns the registration and the code.
func rtApprovedNewClient(t *testing.T, rt *Runtime, client, pin string) (reg, code string) {
	t.Helper()
	svc := rt.OAuthLoginService()
	resp, rerr, err := svc.Register(mcplogin.RegistrationRequest{RedirectURIs: []string{oauthTestRedirect}, ClientName: "App"})
	require.NoError(t, err)
	require.Nil(t, rerr)
	reg = resp["client_id"].(string)
	id, aerr := svc.BeginAuthorization(AuthorizeInput{
		Origin: oauthTestOrigin, ClientID: reg, RedirectURI: oauthTestRedirect, State: "s",
		CodeChallenge: oauthTestChallenge(), Method: "S256", Resource: oauthTestOrigin + "/mcp/oauth", ResponseType: "code",
	})
	require.Nil(t, aerr)
	redirect, err := svc.Approve(rtActor(), id, ApprovalChoice{Client: ClientChoice{New: &NewClientChoice{ID: client}}, Profile: oauthStrp(pin)})
	require.NoError(t, err)
	return reg, oauthCodeOf(t, redirect)
}

func rtToken(reg, code string) TokenInput {
	return TokenInput{Origin: oauthTestOrigin, Code: code, RedirectURI: oauthTestRedirect, ClientID: reg, CodeVerifier: oauthTestVerifier}
}

func deleteProfileRO(c *config.Config) error {
	kept := c.Profiles[:0:0]
	for _, p := range c.Profiles {
		if p.Name != "ro" {
			kept = append(kept, p)
		}
	}
	c.Profiles = kept
	return nil
}

// A config publication attempted DURING the exchange (the file-reload entry point
// and the API funnel) waits for it, then succeeds over the stored login.
func TestExchange_ConfigPublicationWaitsForTheExchange(t *testing.T) {
	rt := enabledFunnelRuntime(t)
	reg, code := rtApprovedNewClient(t, rt, "acme", "ro")
	svc := rt.OAuthLoginService()

	reloadDone := make(chan error, 1)
	funnelDone := make(chan error, 1)
	var reloadFinished, funnelFinished atomic.Bool
	var once sync.Once
	svc.testHookBeforeCommit = func() {
		once.Do(func() {
			go func() {
				_, err := rt.Commit(commitReasonFileReload, func(tx *CommitTx) error { return tx.MutateGlobal(deleteProfileRO) })
				reloadFinished.Store(true)
				reloadDone <- err
			}()
			go func() {
				_, _, err := rt.MutateConfig(context.Background(), funnelActor(), func(d *config.Config) (ChangeHint, error) {
					d.AnonymousProfile = ""
					return ChangeHint{}, nil
				}, TokenRewrite{})
				funnelFinished.Store(true)
				funnelDone <- err
			}()
			time.Sleep(150 * time.Millisecond)
			if reloadFinished.Load() {
				t.Error("a config publication completed while the exchange held the fence")
			}
			if funnelFinished.Load() {
				t.Error("a funnel write completed while the exchange held the fence")
			}
		})
	}

	res, terr := svc.Exchange(rtToken(reg, code))
	require.Nil(t, terr, "the exchange saw the config as it was when it started")
	require.NotEmpty(t, res.AccessToken)
	require.NoError(t, <-reloadDone)
	require.NoError(t, <-funnelDone)

	gs, err := rt.StorageManager().ListOAuthGrants()
	require.NoError(t, err)
	require.Len(t, gs, 1)
	assert.False(t, gs[0].Revoked, "the login is handled by the ordinary config path afterwards")
	pin, _, found, _ := rt.StorageManager().CurrentClientBinding("acme")
	require.True(t, found)
	assert.Equal(t, "ro", pin, "a now-dangling pin is deny-all, never wider")
}

// A reload that completed just before: the exchange sees it and refuses.
func TestExchange_ReloadCompletedBeforeIsSeenAndRefused(t *testing.T) {
	rt := enabledFunnelRuntime(t)
	reg, code := rtApprovedNewClient(t, rt, "acme", "ro")
	_, err := rt.Commit(commitReasonFileReload, func(tx *CommitTx) error { return tx.MutateGlobal(deleteProfileRO) })
	require.NoError(t, err)

	_, terr := rt.OAuthLoginService().Exchange(rtToken(reg, code))
	requireInvalidGrant(t, terr)
	gs, _ := rt.StorageManager().ListOAuthGrants()
	assert.Empty(t, gs)
	reasons := []interface{}{}
	for _, m := range profileChanges(t, rt) {
		if m["change"] == "deny" {
			reasons = append(reasons, m["reason"])
		}
	}
	assert.Equal(t, []interface{}{"profile_gone"}, reasons)
}

// The replacement scenario: the reload that turned auth off came earlier, the
// binding was already bypassable. The pre-commit guard decides, once: the
// replacement succeeds and the client is never left with no live login.
func TestExchange_ReplacementOfAnAlreadyBypassableBindingSucceeds(t *testing.T) {
	fx := newOAuthFx(t)
	fx.guard = ConservativeBindingGuard{}
	reg, _ := fx.loginNew("cursor", "ro") // auth on at the time
	old := fx.grants()[0]
	fx.cfg.RequireMCPAuth = false // a reload turned auth off earlier

	id := fx.authorize(reg, "cursor")
	rev, _ := fx.sm.ClientBindingRevision("cursor")
	redirect, err := fx.svc.Approve(fx.ctx(), id, ApprovalChoice{BindingRevision: rev, ReplaceGrant: old.GrantID})
	require.NoError(t, err, "no new bypassable binding: the guard has nothing to add")
	res, terr := fx.svc.Exchange(fx.tokenInput(reg, oauthCodeOf(t, redirect)))
	require.Nil(t, terr)

	live := 0
	for _, g := range fx.grants() {
		if !g.Revoked {
			live++
		}
	}
	assert.Equal(t, 1, live, "exactly the replacement is live")
	_, _, aerr := fx.svc.Authenticate(res.AccessToken, oauthTestOrigin, "/mcp/oauth/cursor", fx.clock.Now())
	assert.NoError(t, aerr)
}

// --- the lock-order hammer, with config commits ------------------------------------------------------------

func TestExchangeAgainstConfigCommitsRevokeAndReloadDoesNotDeadlockOrRace(t *testing.T) {
	rt := enabledFunnelRuntime(t)
	const n = 8
	type pending struct{ reg, code string }
	var ps []pending
	for i := 0; i < n; i++ {
		reg, code := rtApprovedNewClient(t, rt, "app"+string(rune('a'+i)), "ro")
		ps = append(ps, pending{reg, code})
	}
	svc := rt.OAuthLoginService()

	done := make(chan struct{})
	go func() {
		defer close(done)
		var wg sync.WaitGroup
		for _, p := range ps {
			p := p
			wg.Add(1)
			go func() { defer wg.Done(); _, _ = svc.Exchange(rtToken(p.reg, p.code)) }()
		}
		for i := 0; i < 4; i++ {
			wg.Add(4)
			go func() { // the API commit path
				defer wg.Done()
				_, _ = rt.Commit("test", func(tx *CommitTx) error {
					return tx.MutateGlobal(func(c *config.Config) error { c.ToolsLimit++; return nil })
				})
			}()
			go func() { // the file-reload entry point
				defer wg.Done()
				_ = rt.ReloadConfiguration()
			}()
			go func() { // the funnel
				defer wg.Done()
				_, _, _ = rt.MutateConfig(context.Background(), funnelActor(), func(d *config.Config) (ChangeHint, error) {
					return ChangeHint{}, nil
				}, TokenRewrite{})
			}()
			go func() {
				defer wg.Done()
				_, _ = rt.CredentialsService().Revoke(rtActor(), funnelActor(), CredentialRef{Client: "appa"}, false)
				svc.CancelForClient("appb")
			}()
		}
		wg.Wait()
	}()
	select {
	case <-done:
	case <-time.After(120 * time.Second):
		t.Fatal("deadlock between the exchange and config commits")
	}
}

// --- connect rollback when the guard inspection itself fails ---------------------------------------------

type armedFailStore struct {
	*storage.Manager
	armed *atomic.Bool
}

func (a armedFailStore) ListOAuthGrants() ([]storage.OAuthGrant, error) {
	if a.armed.Load() {
		return nil, assert.AnError
	}
	return a.Manager.ListOAuthGrants()
}

func failingRollbackFixture(t *testing.T) (*oauthFx, *ClientsService, *atomic.Bool) {
	fx := newOAuthFx(t)
	fx.loginNew("cursor", "ro")
	armed := &atomic.Bool{}
	cs := NewClientsService(ClientsServiceDeps{
		Store: armedFailStore{fx.sm, armed}, HMACKey: func() ([]byte, error) { return oauthTestKey, nil },
		Config: func() *config.Config { return fx.cfg }, Guard: func() BindingGuard { return stubGuard{} },
		Activity: func(r *storage.ActivityRecord) error {
			fx.mu.Lock()
			defer fx.mu.Unlock()
			fx.rows = append(fx.rows, r)
			return nil
		},
		Now: fx.clock.Now, Mu: fx.clients.mu,
	})
	return fx, cs, armed
}

func TestConnectAbort_InspectionFailureStillRevokesTheFreshKey(t *testing.T) {
	fx, cs, armed := failingRollbackFixture(t)
	m := cs.ConnectMinter()
	rw := "rw"
	issued, err := m.Issue("cursor", connect.CredentialIntent{Profile: &rw})
	require.NoError(t, err)
	armed.Store(true)

	require.NoError(t, m.Abort("cursor", connect.CredentialIntent{}, issued))
	tok, _ := fx.sm.GetAgentTokenByName("client-cursor")
	assert.True(t, tok.Revoked, "the aborted connect's key is revoked whatever the inspection said")
	b, _ := fx.sm.GetOAuthClientBinding("cursor")
	assert.Equal(t, "rw", b.ProfilePin, "the prior binding is not restored")
	rows := fx.changeRows("forget")
	require.Len(t, rows, 1)
	diff, _ := rows[0]["diff"].(map[string]interface{})
	assert.Equal(t, "inspect_error", diff["restore_skipped"])
}

func TestConnectUndo_InspectionFailureStillRevokesTheFreshKey(t *testing.T) {
	fx, cs, armed := failingRollbackFixture(t)
	m := cs.ConnectMinter()
	rw := "rw"
	_, err := m.Issue("cursor", connect.CredentialIntent{Profile: &rw})
	require.NoError(t, err)
	armed.Store(true)

	name, err := m.ForgetUnheld("cursor", "", connect.CredentialIntent{})
	require.NoError(t, err)
	assert.Equal(t, "client-cursor", name)
	tok, _ := fx.sm.GetAgentTokenByName("client-cursor")
	assert.True(t, tok.Revoked)
	b, _ := fx.sm.GetOAuthClientBinding("cursor")
	assert.Equal(t, "rw", b.ProfilePin)
	rows := fx.changeRows("forget")
	require.Len(t, rows, 1)
	diff, _ := rows[0]["diff"].(map[string]interface{})
	assert.Equal(t, "inspect_error", diff["restore_skipped"])
}

var _ = auth.KindClient
