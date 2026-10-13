package oauth

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	transport "github.com/mark3labs/mcp-go/client/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/commitview"
)

// blockingResolverRuntime answers CurrentCredentialBinding with the binding
// current at the call, then parks until released: the window in which a
// commit replaces the server between the outcome's binding check and its
// schedule selection.
type blockingResolverRuntime struct {
	mockRuntime
	mu      sync.Mutex
	cur     commitview.CredentialBinding
	block   bool
	parked  chan struct{}
	release chan struct{}
}

func (r *blockingResolverRuntime) CurrentCredentialBinding(string) (commitview.CredentialBinding, bool) {
	r.mu.Lock()
	b, block := r.cur, r.block
	r.block = false
	r.mu.Unlock()
	if block {
		close(r.parked)
		<-r.release
	}
	return b, true
}

// Sol r1 (#1621): a reactive terminal outcome of the old binding's flight,
// validated against the resolver just before a commit replaced the server,
// must not fail the replacement's schedule even when the replacement's token
// has the same absolute expiry.
func TestRefreshManager_StaleBindingOutcomeKeepsReplacementSchedule(t *testing.T) {
	oldB := commitview.CredentialBinding{Incarnation: 3, OAuthEpoch: 3}
	newB := commitview.CredentialBinding{Incarnation: 7, OAuthEpoch: 7}
	expiry := time.Now().Add(time.Hour).Truncate(time.Second)

	m := NewRefreshManager(newMockTokenStore(), nil, &RefreshManagerConfig{Threshold: 0.1}, zaptest.NewLogger(t))
	rt := &blockingResolverRuntime{cur: oldB, block: true, parked: make(chan struct{}), release: make(chan struct{})}
	em := &mockEventEmitter{}
	m.SetRuntime(rt)
	m.SetEventEmitter(em)
	require.NoError(t, m.Start(context.Background()))
	t.Cleanup(m.Stop)
	m.OnTokenSavedFor("srv", expiry, oldB)

	done := make(chan struct{})
	go func() {
		defer close(done)
		m.onRefreshOutcome(RefreshOutcome{
			ServerName: "srv", Binding: oldB, Trigger: RefreshTriggerReactive, Class: RefreshClassInvalidGrant,
			Err: fmt.Errorf("x: %w", transport.OAuthError{ErrorCode: "invalid_grant"}), ExpiresAt: expiry,
		})
	}()
	<-rt.parked

	// The commit replaces the server; the replacement saves a token with the
	// same absolute expiry.
	rt.mu.Lock()
	rt.cur = newB
	rt.mu.Unlock()
	m.OnTokenSavedFor("srv", expiry, newB)
	m.mu.RLock()
	repl := m.schedules["srv"]
	m.mu.RUnlock()

	close(rt.release)
	<-done

	m.mu.RLock()
	defer m.mu.RUnlock()
	assert.Same(t, repl, m.schedules["srv"])
	assert.Equal(t, RefreshStateScheduled, repl.RefreshState, "the replacement's schedule is not failed")
	require.NotNil(t, repl.Timer)
	assert.True(t, repl.Timer.Stop(), "the replacement's timer is still armed")
	assert.Equal(t, 0, em.GetFailedEvents(), "no false sign-in-required event")
}
