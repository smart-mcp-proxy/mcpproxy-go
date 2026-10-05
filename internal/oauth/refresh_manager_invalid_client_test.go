package oauth

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	transport "github.com/mark3labs/mcp-go/client/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

type labelRecorder struct {
	mu     sync.Mutex
	labels []string
}

func (r *labelRecorder) RecordOAuthRefresh(_, result string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.labels = append(r.labels, result)
}
func (r *labelRecorder) RecordOAuthRefreshDuration(string, string, time.Duration) {}
func (r *labelRecorder) last() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.labels) == 0 {
		return ""
	}
	return r.labels[len(r.labels)-1]
}

func startedManager(t *testing.T, store RefreshTokenStore, runtimeErr error) (*RefreshManager, *mockRuntime, *mockEventEmitter, *labelRecorder) {
	t.Helper()
	m := NewRefreshManager(store, nil, &RefreshManagerConfig{Threshold: 0.1}, zaptest.NewLogger(t))
	rt := &mockRuntime{refreshErr: runtimeErr}
	em := &mockEventEmitter{}
	rec := &labelRecorder{}
	m.SetRuntime(rt)
	m.SetEventEmitter(em)
	m.SetMetricsRecorder(rec)
	require.NoError(t, m.Start(context.Background()))
	t.Cleanup(m.Stop)
	m.OnTokenSaved("srv", time.Now().Add(time.Hour))
	return m, rt, em, rec
}

// Spec 113 FR-008/FR-009, SC-002: a rejected client is terminal at once.
func TestRefreshManager_InvalidClientIsTerminal(t *testing.T) {
	for _, tc := range []struct {
		name    string
		err     error
		message string
	}{
		{"dcr cleared", &RefreshFailure{Class: RefreshClassInvalidClient, Message: "client registration rejected by the authorization server; sign in again to re-register", Err: errors.New("x")}, "sign in again to re-register"},
		{"static", &RefreshFailure{Class: RefreshClassInvalidClient, Message: "fix oauth.client_id / oauth.client_secret", Err: errors.New("x")}, "oauth.client_id"},
		{"raw mcp-go invalid_client", fmt.Errorf("OAuth refresh failed for srv: %w", fmt.Errorf("refresh token request failed: %w", transport.OAuthError{ErrorCode: "invalid_client"})), "invalid_client"},
		{"unauthorized_client", fmt.Errorf("x: %w", transport.OAuthError{ErrorCode: "unauthorized_client"}), "unauthorized_client"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, rt, em, rec := startedManager(t, newMockTokenStore(), tc.err)
			m.executeRefresh("srv")

			st := m.GetRefreshState("srv")
			require.NotNil(t, st)
			assert.Equal(t, RefreshStateFailed, st.State)
			assert.Contains(t, st.LastError, tc.message)
			assert.Equal(t, 1, em.GetFailedEvents())
			assert.Equal(t, int32(1), rt.refreshCounter.Load(), "zero automatic retries")
			if tc.name != "unauthorized_client" {
				assert.Equal(t, "failed_invalid_client", rec.last())
			}
		})
	}
}

// FR-008/FR-013: 5xx/429 keep the existing backoff, with their own label.
func TestRefreshManager_ServerErrorBacksOff(t *testing.T) {
	m, _, em, rec := startedManager(t, newMockTokenStore(), errors.New("refresh token request failed with status 503: <html>"))
	m.executeRefresh("srv")
	st := m.GetRefreshState("srv")
	require.NotNil(t, st)
	assert.Equal(t, RefreshStateRetrying, st.State)
	assert.Equal(t, 0, em.GetFailedEvents())
	assert.Equal(t, "failed_server_error", rec.last())
	sched := m.GetSchedule("srv")
	m.mu.RLock()
	assert.Equal(t, RetryBackoffBase, sched.RetryBackoff)
	m.mu.RUnlock()
}

func TestRefreshManager_BackoffSequenceUnchanged(t *testing.T) {
	m := NewRefreshManager(nil, nil, nil, zaptest.NewLogger(t))
	want := []time.Duration{10 * time.Second, 20 * time.Second, 40 * time.Second, 80 * time.Second, 160 * time.Second, 300 * time.Second, 300 * time.Second}
	for i, w := range want {
		assert.Equal(t, w, m.calculateBackoff(i))
		assert.Equal(t, w, refreshBackoff(i), "coordinator cooldown mirrors the RefreshManager backoff")
	}
	assert.Equal(t, 50, DefaultMaxRetries)
}

// A terminal outcome of a reactive flight reaches the RefreshManager through
// the coordinator completion hook, once.
func TestRefreshManager_ReactiveTerminalOutcomeViaHook(t *testing.T) {
	invalidGrant := fmt.Errorf("x: %w", transport.OAuthError{ErrorCode: "invalid_grant"})
	m, _, em, _ := startedManager(t, newMockTokenStore(), invalidGrant)

	m.onRefreshOutcome(RefreshOutcome{ServerName: "srv", Trigger: RefreshTriggerReactive, Class: RefreshClassInvalidGrant, Err: invalidGrant})
	st := m.GetRefreshState("srv")
	require.NotNil(t, st)
	assert.Equal(t, RefreshStateFailed, st.State)
	assert.Equal(t, 1, em.GetFailedEvents())

	// A proactive attempt that hits the same latched error does not report twice.
	m.handleRefreshFailure("srv", invalidGrant)
	assert.Equal(t, 1, em.GetFailedEvents())

	// Proactive outcomes are handled by executeRefresh itself, not the hook.
	m.OnTokenSaved("srv", time.Now().Add(time.Hour))
	m.onRefreshOutcome(RefreshOutcome{ServerName: "srv", Trigger: RefreshTriggerProactive, Class: RefreshClassInvalidGrant, Err: invalidGrant})
	assert.Equal(t, RefreshStateScheduled, m.GetRefreshState("srv").State)
}

// Spec 113 FR-010: records are keyed by GenerateServerKey(name, url), not by
// display name; the refreshed event used to never fire.
func TestRefreshManager_ReadsTokenByServerKey(t *testing.T) {
	store := newMockTokenStore()
	expires := time.Now().Add(time.Hour).Truncate(time.Second)
	store.AddToken(&storage.OAuthTokenRecord{
		ServerName: GenerateServerKey("srv", "https://srv.example.com/mcp"), DisplayName: "srv",
		AccessToken: "at", RefreshToken: "rt", ExpiresAt: expires, Updated: time.Now(),
	})
	m, _, em, _ := startedManager(t, store, nil)
	m.executeRefresh("srv")

	em.mu.Lock()
	defer em.mu.Unlock()
	require.Len(t, em.refreshedEvents, 1)
	assert.Equal(t, "srv", em.refreshedEvents[0].serverName)
	assert.True(t, em.refreshedEvents[0].expiresAt.Equal(expires))
}

// Review round 2 (FR-006a): a reactive terminal outcome for the old token
// must not fail the schedule a login built for its new token, even when the
// login replaced the schedule after the flight's last generation check.
func TestRefreshManager_StaleReactiveOutcomeKeepsLoginSchedule(t *testing.T) {
	m, _, em, _ := startedManager(t, newMockTokenStore(), nil)
	oldExpiry := time.Now().Add(-time.Minute)
	m.OnTokenSaved("srv", time.Now().Add(time.Hour)) // the login's schedule

	m.onRefreshOutcome(RefreshOutcome{
		ServerName: "srv", Trigger: RefreshTriggerReactive, Class: RefreshClassInvalidGrant,
		Err: fmt.Errorf("x: %w", transport.OAuthError{ErrorCode: "invalid_grant"}), ExpiresAt: oldExpiry,
	})
	st := m.GetRefreshState("srv")
	require.NotNil(t, st)
	assert.NotEqual(t, RefreshStateFailed, st.State)
	assert.Equal(t, 0, em.GetFailedEvents())

	// The outcome for the token the schedule was built for still applies.
	m.mu.Lock()
	cur := m.schedules["srv"].ExpiresAt
	m.mu.Unlock()
	m.onRefreshOutcome(RefreshOutcome{
		ServerName: "srv", Trigger: RefreshTriggerReactive, Class: RefreshClassInvalidGrant,
		Err: fmt.Errorf("x: %w", transport.OAuthError{ErrorCode: "invalid_grant"}), ExpiresAt: cur,
	})
	assert.Equal(t, RefreshStateFailed, m.GetRefreshState("srv").State)
	assert.Equal(t, 1, em.GetFailedEvents())
}

// Review round 5: a transient retry for a replaced schedule does not touch
// the login's schedule.
func TestRefreshManager_StaleRetryKeepsLoginTimer(t *testing.T) {
	m, _, _, _ := startedManager(t, newMockTokenStore(), nil)
	m.mu.Lock()
	old := m.schedules["srv"]
	m.mu.Unlock()
	m.OnTokenSaved("srv", time.Now().Add(2*time.Hour))
	m.mu.Lock()
	login := m.schedules["srv"]
	planned := login.ScheduledRefresh
	m.mu.Unlock()
	require.NotSame(t, old, login)

	m.rescheduleAfterDelayFor("srv", 5*time.Minute, old)
	m.mu.Lock()
	defer m.mu.Unlock()
	assert.Equal(t, planned, m.schedules["srv"].ScheduledRefresh)
}
