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
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/dialgate"
)

// GH #1722: the background callback waiter runs behind the registered
// instance's gate. When that instance is retired (the server was restarted by
// a scan, replaced, removed or quarantined) the waiter must stop waiting and
// invalidate its state, so the callback that arrives later tells the person
// the login was invalidated instead of showing a success page for a code
// nobody can exchange any more.
func TestWaitForOAuthCallbackAsync_RetiredInstanceInvalidatesTheLogin(t *testing.T) {
	serverName := "wait-retired-server"
	manager := oauth.GetGlobalCallbackManager()
	cs, err := manager.StartCallbackServer(serverName, 0)
	require.NoError(t, err)
	t.Cleanup(func() { _ = manager.StopCallbackServer(serverName) })

	ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()

	c := newOAuthWaitTestClient(serverName)
	gate := dialgate.New()
	c.SetGate(gate)

	const state = "state-retired-mid-login"
	cs.RegisterState(state)

	done := make(chan struct{})
	go func() {
		defer close(done)
		c.waitForOAuthCallbackAsync(ctx, nil, "verifier", state, "corr-retired")
	}()
	require.Eventually(t, func() bool { return cs.HasState(state) }, 2*time.Second, 5*time.Millisecond)

	gate.Retire()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the callback waiter kept waiting after its instance was retired")
	}
	assert.False(t, cs.HasState(state), "a retired flow must not keep its callback waiter")

	// The browser lands on the callback after the retirement: not a success.
	url := fmt.Sprintf("http://%s:%d%s?code=late-code&state=%s", cs.BindHost, cs.Port, cs.Path, state)
	resp, err := http.Get(url) //nolint:gosec // loopback test listener
	require.NoError(t, err)
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Contains(t, string(body), "Authorization Failed")
	assert.NotContains(t, string(body), "Authorization Successful")
	assert.Contains(t, string(body), "retired", "the page says why the login was invalidated")
}
