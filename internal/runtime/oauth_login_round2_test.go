package runtime

import (
	"context"
	"errors"
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

// --- 2: Approve finalisation cannot interleave with a cancel ---------------------------------

func TestApprove_FinalisationIsAtomicAgainstASwitchOff(t *testing.T) {
	fx := newOAuthFx(t)
	reg := fx.register()
	id := fx.authorize(reg, "cursor")
	cancelDone := make(chan struct{})
	var once sync.Once
	fx.svc.testHookApproveFinalising = func() {
		once.Do(func() {
			go func() { fx.svc.CancelAll("race"); close(cancelDone) }()
			select {
			case <-cancelDone:
				t.Error("a cancel completed while Approve was finalising")
			case <-time.After(100 * time.Millisecond): // it is blocked on lifecycleMu
			}
		})
	}
	redirect, err := fx.svc.Approve(fx.ctx(), id, ApprovalChoice{Profile: oauthStrp("ro")})
	require.NoError(t, err, "the cancel came after finalisation")
	code := oauthCodeOf(t, redirect)
	<-cancelDone
	require.Len(t, fx.changeRows("approve"), 1)
	_, terr := fx.svc.Exchange(fx.tokenInput(reg, code))
	requireInvalidGrant(t, terr) // the code was cancelled: fine and expected
	fx.nothingStored()
}

// --- 3: key I/O never blocks the switch-off ------------------------------------------------------

func TestSwitchOffDoesNotWaitOnLazyKeyInitialisation(t *testing.T) {
	fx := newOAuthFx(t)
	reg := fx.register()
	release := make(chan struct{})
	entered := make(chan struct{}, 1)
	svc := NewOAuthLoginService(OAuthLoginDeps{
		Store: fx.sm, Config: func() *config.Config { return fx.cfg }, Now: fx.clock.Now,
		HMACKey: func() ([]byte, error) {
			select {
			case entered <- struct{}{}:
			default:
			}
			<-release
			return oauthTestKey, nil
		},
	})
	go func() { _, _ = svc.BeginAuthorization(fx.authInput(reg, oauthTestOrigin+"/mcp/oauth/cursor")) }()
	<-entered // key initialisation is now stalled

	done := make(chan struct{})
	go func() {
		svc.CancelAll("switch_off")
		unlock := svc.beginDisable("switch_off")
		unlock()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the switch-off waited on key I/O")
	}
	close(release)
}

// --- 4: connect rollback never restores a binding the guard refuses ---------------------------------

func rollbackFixture(t *testing.T) (*oauthFx, connect.CredentialMinter, *connect.IssuedCredential) {
	fx := newOAuthFx(t)
	fx.loginNew("cursor", "ro") // browser-only client bound to ro (locked)
	m := fx.clients.ConnectMinter()
	rw := "rw"
	issued, err := m.Issue("cursor", connect.CredentialIntent{Profile: &rw})
	require.NoError(t, err)
	b, _ := fx.sm.GetOAuthClientBinding("cursor")
	require.Equal(t, "rw", b.ProfilePin, "the connect moved both copies")
	return fx, m, issued
}

func TestConnectRollback_RestoresThePriorBindingWhenTheGuardAllowsIt(t *testing.T) {
	fx, m, issued := rollbackFixture(t)
	require.NoError(t, m.Abort("cursor", connect.CredentialIntent{}, issued))
	b, _ := fx.sm.GetOAuthClientBinding("cursor")
	assert.Equal(t, "ro", b.ProfilePin, "as before: the aborted connect is undone")
	tok, _ := fx.sm.GetAgentTokenByName("client-cursor")
	assert.True(t, tok.Revoked)
}

func TestConnectRollback_SkipsTheRestoreWhenTheGuardNowRefusesIt(t *testing.T) {
	fx, m, issued := rollbackFixture(t)
	fx.guard = StrictOfflineBindingGuard{}
	fx.cfg.RequireMCPAuth = false // the config changed since the connect

	require.NoError(t, m.Abort("cursor", connect.CredentialIntent{}, issued))
	tok, _ := fx.sm.GetAgentTokenByName("client-cursor")
	assert.True(t, tok.Revoked, "the API key is still revoked")
	b, _ := fx.sm.GetOAuthClientBinding("cursor")
	assert.Equal(t, "rw", b.ProfilePin, "the logins keep the binding the guard already accepts")
	assert.False(t, fx.grants()[0].Revoked)
	rows := fx.changeRows("forget")
	require.Len(t, rows, 1)
	diff, _ := rows[0]["diff"].(map[string]interface{})
	assert.Equal(t, "guard", diff["restore_skipped"])
}

func TestConnectRollback_APIKeyOnlyClientIsUnchanged(t *testing.T) {
	fx := newOAuthFx(t)
	m := fx.clients.ConnectMinter()
	rw := "rw"
	issued, err := m.Issue("cursor", connect.CredentialIntent{Profile: &rw})
	require.NoError(t, err)
	fx.guard = StrictOfflineBindingGuard{}
	fx.cfg.RequireMCPAuth = false
	require.NoError(t, m.Abort("cursor", connect.CredentialIntent{}, issued))
	assert.Empty(t, fx.changeRows("forget"), "no row, exactly as before")
}

// --- 5: a CredentialsService without a clients service -----------------------------------------------

func newClientlessCreds(fx *oauthFx) *CredentialsService {
	return NewCredentialsService(CredentialsServiceDeps{
		Tokens: fx.sm, HMACKey: func() ([]byte, error) { return oauthTestKey, nil },
		Config: func() *config.Config { return fx.cfg }, Activity: func(*storage.ActivityRecord) error { return nil },
		Now: fx.clock.Now,
	})
}

func TestRevokeByTokenName_WithoutAClientsService(t *testing.T) {
	fx := newOAuthFx(t)
	fx.mintClient("codex", "ro", auth.ProfileModeLocked)
	cs := newClientlessCreds(fx)

	r, err := cs.Revoke(context.Background(), fx.actor(), CredentialRef{Token: "client-codex"}, true)
	require.NoError(t, err, "no panic, the original token-revoke path")
	assert.True(t, r.Changed)
	r, err = cs.Revoke(context.Background(), fx.actor(), CredentialRef{Token: "client-codex"}, true)
	require.NoError(t, err)
	assert.False(t, r.Changed, "already revoked")
}

func TestRevokeByTokenName_WithoutAClientsServiceButWithLogins(t *testing.T) {
	fx := newOAuthFx(t)
	fx.mintClient("codex", "ro", auth.ProfileModeLocked)
	reg := fx.register()
	id := fx.authorize(reg, "codex")
	rev, _ := fx.sm.ClientBindingRevision("codex")
	redirect, err := fx.svc.Approve(fx.ctx(), id, ApprovalChoice{BindingRevision: rev})
	require.NoError(t, err)
	_, terr := fx.svc.Exchange(fx.tokenInput(reg, oauthCodeOf(t, redirect)))
	require.Nil(t, terr)

	cs := newClientlessCreds(fx)
	_, err = cs.Revoke(context.Background(), fx.actor(), CredentialRef{Token: "client-codex"}, true)
	var ce *CredentialError
	require.True(t, errors.As(err, &ce), "a controlled error, not a panic")
	tok, _ := fx.sm.GetAgentTokenByName("client-codex")
	assert.False(t, tok.Revoked, "nothing half-done")
	assert.False(t, fx.grants()[0].Revoked)
}

var _ = mcplogin.AccessTokenPrefix
