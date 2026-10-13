package upstream

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/commitview"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"
)

// Spec 116 PR2b (#1607) review r9: the in-process OAuth completion callback
// is bound like the completion record. An old instance's login that finishes
// (code exchanged, completion notification parked) after a removal and a
// same-name re-add with a different binding must not RetryConnection the
// replacement: no disconnect, no state transition, no network request. A
// completion of the current binding still reconnects (unchanged behaviour).
func TestOAuthCompletionCallbackIsBindingChecked(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	// The completion record is process-global: a fresh name per run.
	name := fmt.Sprintf("r9-completion-retry-%d", time.Now().UnixNano())
	cfg := func() *config.ServerConfig {
		return &config.ServerConfig{Name: name, URL: srv.URL + "/mcp", Protocol: "streamable-http", Enabled: true}
	}
	m := newGenTestManager(t)
	tm := oauth.GetTokenStoreManager()
	m.installOAuthCompletionHandlers(tm)
	t.Cleanup(func() {
		tm.SetOAuthCompletionBoundCallback(nil)
		tm.SetCompletionBindingResolver(nil)
	})

	old := commitview.CredentialBinding{Incarnation: 5, OAuthEpoch: 5}
	ch := present(name, 5, cfg())
	ch.Binding = old
	m.AdvanceGenerations([]GenerationChange{ch})
	require.NoError(t, m.AddServerConfigAt(name, cfg(), 5))

	// Removal and a same-name re-add with a different binding while the old
	// instance's completion notification is parked.
	m.AdvanceGenerations([]GenerationChange{{Name: name, Gen: 6}})
	repl := commitview.CredentialBinding{Incarnation: 7, OAuthEpoch: 7}
	ch = present(name, 7, cfg())
	ch.Binding = repl
	m.AdvanceGenerations([]GenerationChange{ch})
	require.NoError(t, m.AddServerConfigAt(name, cfg(), 7))
	c := registered(t, m, name)
	require.Equal(t, repl, c.CredentialBinding())
	stateBefore := c.GetState()

	// Release: the old instance's completion lands.
	tm.MarkOAuthCompletedBound(name, old)
	time.Sleep(300 * time.Millisecond)
	assert.Equal(t, int32(0), hits.Load(), "no network request against the replacement")
	assert.Equal(t, stateBefore, c.GetState(), "no state transition of the replacement")
	assert.False(t, tm.HasRecentOAuthCompletionBound(name, commitview.CredentialBinding{}),
		"a superseded completion records nothing")

	// The replacement's own completion still reconnects it.
	tm.MarkOAuthCompletedBound(name, repl)
	require.Eventually(t, func() bool { return hits.Load() > 0 }, 10*time.Second, 20*time.Millisecond,
		"a completion with the current binding triggers a reconnect")
	_ = c.Disconnect()
}
