package oauth

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/dialgate"
)

// Spec 124 D5: the audit observer reports every refresh that was sent and
// failed, once, and nothing for a return that sent no request.

type auditSink struct {
	mu  sync.Mutex
	got []RefreshAttemptFailure
}

func (s *auditSink) observe(f RefreshAttemptFailure) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.got = append(s.got, f)
}

func (s *auditSink) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.got)
}

func transientRefreshErr() error {
	return fmt.Errorf("refresh token request failed: %w", transport.OAuthError{ErrorCode: "server_error"})
}

// Ten waiters on one failed flight are one failed refresh.
func TestRefreshAudit_OneReportPerFailedFlight(t *testing.T) {
	f := newCoordFixture(t, "cid")
	f.block = make(chan struct{})
	f.failErr = transientRefreshErr()
	c := newTestCoordinator()
	sink := &auditSink{}
	c.SetAuditObserver(sink.observe)

	const n = 10
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, _ = c.Do(context.Background(), f.req(RefreshTriggerReactive, "rt-0"))
		}()
	}
	<-f.started
	waitForWaiters(t, c, f.key, n)
	close(f.block)
	wg.Wait()

	require.Equal(t, 1, sink.count(), "waiters report nothing; the flight reports once")
	got := sink.got[0]
	assert.Equal(t, "srv", got.ServerName)
	assert.Equal(t, RefreshClassServerError, got.Class)
	assert.Equal(t, RefreshTriggerReactive, got.Trigger, "a transient reactive failure is reported, which the completion hook never sees")
	assert.False(t, got.At.IsZero())
}

// A cooldown that answers from memory sends nothing and reports nothing; a
// proactive failure is reported like a reactive one.
func TestRefreshAudit_CachedOutcomeIsNotAnAttempt(t *testing.T) {
	f := newCoordFixture(t, "cid")
	f.failErr = transientRefreshErr()
	c := newTestCoordinator()
	sink := &auditSink{}
	c.SetAuditObserver(sink.observe)

	_, _, err := c.Do(context.Background(), f.req(RefreshTriggerProactive, "rt-0"))
	require.Error(t, err)
	require.Equal(t, 1, sink.count())
	assert.Equal(t, RefreshTriggerProactive, sink.got[0].Trigger)

	calls := f.calls.Load()
	_, _, err = c.Do(context.Background(), f.req(RefreshTriggerReactive, "rt-0"))
	require.Error(t, err)
	require.Equal(t, calls, f.calls.Load(), "the cooldown answered without a request")
	assert.Equal(t, 1, sink.count(), "no request, no report")
}

// A successful refresh reports nothing.
func TestRefreshAudit_SuccessIsSilent(t *testing.T) {
	f := newCoordFixture(t, "cid")
	c := newTestCoordinator()
	sink := &auditSink{}
	c.SetAuditObserver(sink.observe)

	_, _, err := c.Do(context.Background(), f.req(RefreshTriggerProactive, "rt-0"))
	require.NoError(t, err)
	assert.Zero(t, sink.count())
}

// A retired instance's gate refuses before any token request is sent: that is
// not a failed refresh.
func TestRefreshAudit_RetiredInstanceIsNotAnAttempt(t *testing.T) {
	f := newCoordFixture(t, "cid")
	f.failErr = dialgate.ErrRetired
	c := newTestCoordinator()
	sink := &auditSink{}
	c.SetAuditObserver(sink.observe)

	_, _, err := c.Do(context.Background(), f.req(RefreshTriggerProactive, "rt-0"))
	require.Error(t, err)
	assert.Zero(t, sink.count())
}
