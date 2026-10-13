package core

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"
)

// Issue #975: the background callback waiter used to give up after a hardcoded
// 120 seconds even though the manager hands it a 30-minute context, so a
// sign-in that needed 2FA or an org approval found nobody waiting when the
// callback finally arrived.

func newOAuthWaitTestClient(name string) *Client {
	return &Client{
		config: &config.ServerConfig{Name: name, URL: "https://example.test/mcp"},
		logger: zap.NewNop(),
	}
}

func TestOAuthCallbackWaitContext_HonoursCallerDeadline(t *testing.T) {
	callerDeadline := time.Now().Add(30 * time.Minute)
	parent, cancelParent := context.WithDeadline(context.Background(), callerDeadline)
	defer cancelParent()

	ctx, cancel := oauthCallbackWaitContext(parent)
	defer cancel()

	deadline, ok := ctx.Deadline()
	require.True(t, ok)
	assert.WithinDuration(t, callerDeadline, deadline, time.Second,
		"the wait must follow the caller's deadline, not a hardcoded 120s")
	assert.Greater(t, time.Until(deadline), 2*time.Minute,
		"a 30-minute sign-in window must survive past the old 120s cutoff")
}

func TestOAuthCallbackWaitContext_FallsBackWhenCallerHasNoDeadline(t *testing.T) {
	ctx, cancel := oauthCallbackWaitContext(context.Background())
	defer cancel()

	deadline, ok := ctx.Deadline()
	require.True(t, ok, "a deadline-less caller must still get a bounded wait")
	assert.WithinDuration(t, time.Now().Add(defaultOAuthCallbackWait), deadline, time.Minute)
}

// TestWaitForOAuthCallbackAsync_ExpiryIsReported verifies (d): when the sign-in
// window closes without a callback, the failure reaches the operator through
// the OAuth failure hook rather than being logged and forgotten.
func TestWaitForOAuthCallbackAsync_ExpiryIsReported(t *testing.T) {
	serverName := "wait-expiry-server"
	manager := oauth.GetGlobalCallbackManager()
	_, err := manager.StartCallbackServer(serverName, 0)
	require.NoError(t, err)
	t.Cleanup(func() { _ = manager.StopCallbackServer(serverName) })

	failures := make(chan error, 4)
	tm := oauth.GetTokenStoreManager()
	tm.SetOAuthFailureCallback(func(server string, failure error) {
		if server == serverName {
			failures <- failure
		}
	})
	t.Cleanup(func() { tm.SetOAuthFailureCallback(nil) })

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	c := newOAuthWaitTestClient(serverName)

	done := make(chan struct{})
	start := time.Now()
	go func() {
		defer close(done)
		c.waitForOAuthCallbackAsync(ctx, nil, "verifier", "state-nobody-answers", "corr-expiry")
	}()

	select {
	case <-done:
		assert.Less(t, time.Since(start), 5*time.Second,
			"the waiter must follow the caller's deadline")
	case <-time.After(10 * time.Second):
		t.Fatal("waitForOAuthCallbackAsync ignored the caller's deadline")
	}

	select {
	case failure := <-failures:
		require.Error(t, failure)
		assert.Contains(t, failure.Error(), serverName)
	case <-time.After(2 * time.Second):
		t.Fatal("an expired sign-in window was not surfaced through the failure hook")
	}
}

// TestHandleOAuthAuthorization_StandsDownForManualFlow verifies (e): a
// background reconnect does not start a competing OAuth flow while the user is
// still completing a manual sign-in.
func TestHandleOAuthAuthorization_StandsDownForManualFlow(t *testing.T) {
	serverName := "stand-down-server"
	release := oauth.BeginManualFlow(serverName, time.Minute)
	defer release()

	c := newOAuthWaitTestClient(serverName)

	err := c.handleOAuthAuthorization(context.Background(), nil, nil, nil)
	require.Error(t, err, "the automatic flow must stand down while a manual login is in flight")
	assert.Contains(t, err.Error(), "already in progress")

	// A manual flow (marked on the context) is never suppressed by this guard.
	release()
	release2 := oauth.BeginManualFlow(serverName, time.Minute)
	defer release2()
	manualCtx := context.WithValue(context.Background(), manualOAuthKey, true)
	assert.True(t, c.isManualOAuthFlow(manualCtx),
		"a manual flow must be recognised so it is not suppressed by its own claim")
}
