package upstream

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
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

// Spec 116 PR2b (#1607) review r10: the binding check and the selection of
// the retry target are one registry critical section. A removal and a
// same-name re-add with a different binding that land after the check
// passed (the hook pauses there) must not let the old instance's
// completion disconnect or reconnect the replacement: the retry acts on
// the instance it checked, which is unregistered and never connects. The
// replacement's own completion still reconnects it.
func TestOAuthCompletionRetryTargetIsTheCheckedInstance(t *testing.T) {
	mkSrv := func(hits *atomic.Int32) *httptest.Server {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			hits.Add(1)
			http.Error(w, "nope", http.StatusInternalServerError)
		}))
		t.Cleanup(s.Close)
		return s
	}
	var oldHits, newHits atomic.Int32
	oldSrv, newSrv := mkSrv(&oldHits), mkSrv(&newHits)

	name := fmt.Sprintf("r10-completion-retry-%d", time.Now().UnixNano())
	cfg := func(url string) *config.ServerConfig {
		return &config.ServerConfig{Name: name, URL: url + "/mcp", Protocol: "streamable-http", Enabled: true}
	}
	m := newGenTestManager(t)
	tm := oauth.GetTokenStoreManager()
	m.installOAuthCompletionHandlers(tm)
	t.Cleanup(func() {
		tm.SetOAuthCompletionBoundCallback(nil)
		tm.SetCompletionBindingResolver(nil)
	})

	old := commitview.CredentialBinding{Incarnation: 5, OAuthEpoch: 5}
	ch := present(name, 5, cfg(oldSrv.URL))
	ch.Binding = old
	m.AdvanceGenerations([]GenerationChange{ch})
	require.NoError(t, m.AddServerConfigAt(name, cfg(oldSrv.URL), 5))
	oldClient := registered(t, m, name)

	repl := commitview.CredentialBinding{Incarnation: 7, OAuthEpoch: 7}
	var once sync.Once
	swapped := make(chan struct{})
	hook := func(n string) {
		if n != name {
			return
		}
		once.Do(func() {
			// Removal and same-name re-add with a different binding, after
			// the old completion's binding check passed.
			m.AdvanceGenerations([]GenerationChange{{Name: name, Gen: 6}})
			ch := present(name, 7, cfg(newSrv.URL))
			ch.Binding = repl
			m.AdvanceGenerations([]GenerationChange{ch})
			if err := m.AddServerConfigAt(name, cfg(newSrv.URL), 7); err != nil {
				t.Errorf("re-add: %v", err)
			}
			close(swapped)
		})
	}
	afterOAuthCompletionBindingCheckHook.Store(&hook)
	t.Cleanup(func() { afterOAuthCompletionBindingCheckHook.Store(nil) })

	tm.MarkOAuthCompletedBound(name, old)
	select {
	case <-swapped:
	case <-time.After(10 * time.Second):
		t.Fatal("the completion never reached the post-check pause")
	}
	c := registered(t, m, name)
	require.NotSame(t, oldClient, c)
	require.Equal(t, repl, c.CredentialBinding())
	stateBefore := c.GetState()

	time.Sleep(300 * time.Millisecond)
	assert.Equal(t, int32(0), newHits.Load(), "no network request against the replacement")
	assert.Equal(t, stateBefore, c.GetState(), "no state transition of the replacement")
	assert.Equal(t, int32(0), oldHits.Load(), "the unregistered old instance never reconnects")

	// The replacement's own completion still reconnects it.
	tm.MarkOAuthCompletedBound(name, repl)
	require.Eventually(t, func() bool { return newHits.Load() > 0 }, 10*time.Second, 20*time.Millisecond,
		"a completion with the current binding triggers a reconnect")
	_ = c.Disconnect()
}
