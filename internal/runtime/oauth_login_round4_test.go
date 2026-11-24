package runtime

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

func switchBrowserLogin(rt *Runtime, on bool) error {
	_, err := rt.Commit("test_switch", func(tx *CommitTx) error {
		return tx.MutateGlobal(func(c *config.Config) error { c.MCPOAuthEnabled = on; return nil })
	})
	return err
}

func rtLogin(t *testing.T, rt *Runtime, client string) (reg string, res TokenResult) {
	t.Helper()
	reg, code := rtApprovedNewClient(t, rt, client, "ro")
	res, terr := rt.OAuthLoginService().Exchange(rtToken(reg, code))
	require.Nil(t, terr)
	return reg, res
}

// A switch-off commit started during a refresh waits for the rotation.
func TestRefresh_SwitchOffCommitWaitsForTheRotation(t *testing.T) {
	rt := enabledFunnelRuntime(t)
	reg, res := rtLogin(t, rt, "acme")
	svc := rt.OAuthLoginService()

	var finished atomic.Bool
	done := make(chan error, 1)
	var once sync.Once
	svc.testHookInRotate = func() {
		once.Do(func() {
			go func() {
				err := switchBrowserLogin(rt, false)
				finished.Store(true)
				done <- err
			}()
			time.Sleep(150 * time.Millisecond)
			if finished.Load() {
				t.Error("the switch-off completed while the refresh held the fence")
			}
		})
	}
	_, terr := svc.Refresh(TokenInput{Origin: oauthTestOrigin, RefreshToken: res.RefreshToken, ClientID: reg})
	require.Nil(t, terr, "the refresh started before the switch-off")
	require.NoError(t, <-done)
	assert.False(t, svc.Enabled())
}

// A refresh that passed the early check but meets the switch-off at the fence is
// refused uniformly and the stored grant is byte-for-byte unchanged.
func TestRefresh_AfterSwitchOffLeavesTheGrantUntouched(t *testing.T) {
	fx := newOAuthFx(t)
	reg, res := fx.loginNew("cursor", "ro")
	before, _ := fx.sm.GetOAuthGrant(fx.grants()[0].GrantID)
	fx.svc.testHookBeforeRotateLock = func() { fx.cfg.MCPOAuthEnabled = false }

	fx.clock.Advance(time.Hour)
	_, terr := fx.svc.Refresh(TokenInput{Origin: oauthTestOrigin, RefreshToken: res.RefreshToken, ClientID: reg})
	requireInvalidGrant(t, terr)
	after, _ := fx.sm.GetOAuthGrant(before.GrantID)
	assert.Equal(t, before.RefreshHash, after.RefreshHash, "secrets not rotated")
	assert.Equal(t, before.AccessHash, after.AccessHash)
	assert.True(t, before.RefreshExpiresAt.Equal(after.RefreshExpiresAt), "expiry not extended")
	assert.False(t, after.Revoked)
}

// Concurrent refreshes, exchanges, switch-offs and other config commits finish.
func TestRefreshExchangeAndSwitchOffHammerDoesNotDeadlockOrRace(t *testing.T) {
	rt := enabledFunnelRuntime(t)
	svc := rt.OAuthLoginService()
	type login struct{ reg, refresh string }
	var logins []login
	for _, c := range []string{"appa", "appb", "appc", "appd"} {
		reg, res := rtLogin(t, rt, c)
		logins = append(logins, login{reg, res.RefreshToken})
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		var wg sync.WaitGroup
		for _, l := range logins {
			l := l
			for i := 0; i < 3; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					_, _ = svc.Refresh(TokenInput{Origin: oauthTestOrigin, RefreshToken: l.refresh, ClientID: l.reg})
				}()
			}
		}
		for i := 0; i < 4; i++ {
			wg.Add(3)
			go func() { defer wg.Done(); _ = switchBrowserLogin(rt, false); _ = switchBrowserLogin(rt, true) }()
			go func() { defer wg.Done(); _ = rt.ReloadConfiguration() }()
			go func() {
				defer wg.Done()
				_, _, _ = rt.MutateConfig(context.Background(), funnelActor(), func(d *config.Config) (ChangeHint, error) {
					return ChangeHint{}, nil
				}, TokenRewrite{})
			}()
		}
		wg.Wait()
	}()
	select {
	case <-done:
	case <-time.After(120 * time.Second):
		t.Fatal("deadlock between refresh and config commits")
	}
}
