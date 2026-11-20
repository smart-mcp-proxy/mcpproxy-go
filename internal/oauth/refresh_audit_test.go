package oauth

import (
	"context"
	"fmt"
	"github.com/mark3labs/mcp-go/client"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"sync"
	"testing"
	"time"

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
	assert.NotContains(t, got.Code, "server_error", "only a catalogue code crosses the boundary, never error text")
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

// Review round 2: a refresh that fails before any token request is sent (no
// client id, no token endpoint) is not a failed refresh request.
func TestRefreshAudit_FailureBeforeTheRequestIsNotAnAttempt(t *testing.T) {
	for name, err := range map[string]error{
		"no client credentials": ErrNoClientCredentials,
		"metadata discovery":    MarkRefreshNotSent(fmt.Errorf("failed to get server metadata for srv: %w", context.DeadlineExceeded)),
		"wrapped":               fmt.Errorf("refresh: %w", MarkRefreshNotSent(fmt.Errorf("token endpoint not found"))),
	} {
		t.Run(name, func(t *testing.T) {
			f := newCoordFixture(t, "cid")
			f.failErr = err
			c := newTestCoordinator()
			sink := &auditSink{}
			c.SetAuditObserver(sink.observe)

			_, _, doErr := c.Do(context.Background(), f.req(RefreshTriggerProactive, "rt-0"))
			require.Error(t, doErr)
			assert.Zero(t, sink.count())
		})
	}
	marked := MarkRefreshNotSent(context.DeadlineExceeded)
	assert.Equal(t, context.DeadlineExceeded.Error(), marked.Error(), "the mark changes no text")
	assert.ErrorIs(t, marked, context.DeadlineExceeded, "and no chain")
	assert.NoError(t, MarkRefreshNotSent(nil))
}

// Review round 6: the audit time is the time the refresh returned, not the
// time after the storage reads that follow it.
func TestRefreshAudit_TimeIsTakenWhenTheRefreshReturns(t *testing.T) {
	f := newCoordFixture(t, "cid")
	f.failErr = transientRefreshErr()
	c := newTestCoordinator()
	var mu sync.Mutex
	tick := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	var afterRefresh time.Time
	c.now = func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		tick = tick.Add(time.Minute)
		if f.calls.Load() > 0 && afterRefresh.IsZero() {
			afterRefresh = tick // the first reading after the refresh ran
		}
		return tick
	}
	sink := &auditSink{}
	c.SetAuditObserver(sink.observe)

	_, _, err := c.Do(context.Background(), f.req(RefreshTriggerProactive, "rt-0"))
	require.Error(t, err)
	require.Equal(t, 1, sink.count())
	assert.Equal(t, afterRefresh, sink.got[0].At)
}

// Review round 11: a refresh function that sends two token requests (the
// replay of a lost response) has each failed request recorded: the first
// through the flight's context, the last by the flight itself.
func TestRefreshAudit_ReplayedAttemptsAreEachRecorded(t *testing.T) {
	firstAt := time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)
	cases := map[string]struct {
		first, second error
		want          int
	}{
		"first fails, replay succeeds":    {transientRefreshErr(), nil, 1},
		"both fail":                       {transientRefreshErr(), transientRefreshErr(), 2},
		"first was never sent":            {MarkRefreshNotSent(context.DeadlineExceeded), transientRefreshErr(), 1},
		"first refused by a retired gate": {dialgate.ErrRetired, nil, 0},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := newCoordFixture(t, "cid")
			c := newTestCoordinator()
			sink := &auditSink{}
			c.SetAuditObserver(sink.observe)

			req := f.req(RefreshTriggerReactive, "rt-0")
			inner := req.Refresh
			req.Refresh = func(ctx context.Context, rec *storage.OAuthTokenRecord) (*client.Token, error) {
				ReportRefreshAttemptFailure(ctx, tc.first, firstAt)
				f.mu.Lock()
				f.failErr = tc.second
				f.mu.Unlock()
				return inner(ctx, rec)
			}
			_, _, err := c.Do(context.Background(), req)
			if tc.second == nil {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			require.Equal(t, tc.want, sink.count())
			if tc.want > 0 && !RefreshWasNotSent(tc.first) {
				assert.Equal(t, firstAt, sink.got[0].At, "the first request keeps its own time")
				assert.Equal(t, RefreshTriggerReactive, sink.got[0].Trigger)
			}
		})
	}
	// Outside a flight the report is a no-op.
	ReportRefreshAttemptFailure(context.Background(), transientRefreshErr(), firstAt)
}
