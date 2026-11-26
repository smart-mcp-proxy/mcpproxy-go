package core

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"
)

// Spec 135 plan D1 ("one waiter per flow"): the waiter registered when the
// authorization URL is built is the one the background consumer reads. A
// callback that lands before the consumer goroutine is scheduled is not lost:
// before, the goroutine registered the state a second time, found the waiter
// already consumed and parked on a fresh channel until the window closed.
func TestCallback_ArrivesBeforeAsyncConsumer(t *testing.T) {
	serverName := "callback-before-consumer"
	manager := oauth.GetGlobalCallbackManager()
	cs, err := manager.StartCallbackServer(serverName, 0)
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

	const state = "state-fast-browser"
	callbackCh := cs.RegisterState(state) // what getAuthorizationURLQuick does

	// The browser comes back before the consumer goroutine runs.
	url := fmt.Sprintf("http://%s:%d%s?error=access_denied&state=%s", cs.BindHost, cs.Port, cs.Path, state)
	resp, err := http.Get(url) //nolint:gosec // loopback test listener
	require.NoError(t, err)
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	require.False(t, cs.HasState(state), "delivery consumed the waiter")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c := newOAuthWaitTestClient(serverName)
	done := make(chan struct{})
	go func() {
		defer close(done)
		c.waitForOAuthCallbackAsync(ctx, nil, "verifier", state, callbackCh, "corr-fast")
	}()

	select {
	case failure := <-failures:
		assert.Contains(t, failure.Error(), "access_denied",
			"the consumer must read the callback delivered before it started, not wait for the window to close")
	case <-time.After(3 * time.Second):
		t.Fatal("the callback delivered before the consumer started was lost")
	}
	<-done
}
