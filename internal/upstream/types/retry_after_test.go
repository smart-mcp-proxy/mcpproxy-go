package types

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestConnectionInfo_ShouldAutoReconnect_RetryAfter pins the 429 park window
// (#1040): an upstream-supplied Retry-After is a FLOOR under the retry ladder,
// so the effective delay is max(backoff, Retry-After).
func TestConnectionInfo_ShouldAutoReconnect_RetryAfter(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name     string
		info     *ConnectionInfo
		expected bool
	}{
		{
			name:     "retry-after window still open beats an elapsed backoff",
			info:     &ConnectionInfo{State: StateError, RetryCount: 1, LastRetryTime: now.Add(-time.Hour), RetryAfter: now.Add(30 * time.Minute)},
			expected: false,
		},
		{
			name:     "retry-after window elapsed falls back to the ladder",
			info:     &ConnectionInfo{State: StateError, RetryCount: 1, LastRetryTime: now.Add(-time.Hour), RetryAfter: now.Add(-time.Second)},
			expected: true,
		},
		{
			name:     "no retry-after leaves the ladder untouched",
			info:     &ConnectionInfo{State: StateError, RetryCount: 1, LastRetryTime: now.Add(-time.Hour)},
			expected: true,
		},
		{
			name:     "backoff still open even though retry-after elapsed",
			info:     &ConnectionInfo{State: StateError, RetryCount: 5, LastRetryTime: now, RetryAfter: now.Add(-time.Minute)},
			expected: false,
		},
		{
			name:     "a disconnected server is parked too while the window is open",
			info:     &ConnectionInfo{State: StateDisconnected, RetryAfter: now.Add(10 * time.Minute)},
			expected: false,
		},
		{
			name:     "a give-up probe is still held back by the window",
			info:     &ConnectionInfo{State: StateError, GaveUp: true, LastRetryTime: now.Add(-time.Hour), RetryAfter: now.Add(15 * time.Minute)},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.info.ShouldAutoReconnect(now))
		})
	}
}

// TestStateManager_SetRetryAfter covers the state-manager side of the 429 park:
// the hint reaches ConnectionInfo and both reconnect gates, extends rather than
// shortens, and is dropped once the server actually connects.
func TestStateManager_SetRetryAfter(t *testing.T) {
	sm := NewStateManager()
	sm.TransitionTo(StateConnecting)

	deadline := time.Now().Add(time.Hour)
	sm.SetRetryAfter(deadline)
	sm.SetError(errors.New("request failed with status 429: rate limited"))

	info := sm.GetConnectionInfo()
	assert.WithinDuration(t, deadline, info.RetryAfter, time.Second, "the hint must reach ConnectionInfo")
	assert.False(t, info.ShouldAutoReconnect(time.Now()), "the supervisor gate must park the server")
	assert.False(t, sm.ShouldRetry(), "ConnectAll/health must not redial inside the window either")

	// A shorter hint must not shorten an outstanding window.
	sm.SetRetryAfter(time.Now().Add(time.Second))
	assert.WithinDuration(t, deadline, sm.GetConnectionInfo().RetryAfter, time.Second)

	// A successful connection clears it — a stale window must not hold back a
	// later, unrelated reconnect.
	sm.TransitionTo(StateConnecting)
	sm.TransitionTo(StateReady)
	assert.True(t, sm.GetConnectionInfo().RetryAfter.IsZero(), "Ready must clear the park window")

	// Reset (manual reconnect) clears it as well.
	sm.SetRetryAfter(time.Now().Add(time.Hour))
	sm.Reset()
	assert.True(t, sm.GetConnectionInfo().RetryAfter.IsZero(), "Reset must clear the park window")
}

// TestStateManager_SetRetryAfter_GatesTheOAuthLadderToo: the OAuth ladder is a
// separate reconnect path (the monitoring loop consults ShouldRetryOAuth), and
// its rungs can be shorter than the window a rate-limited upstream asked for.
func TestStateManager_SetRetryAfter_GatesTheOAuthLadderToo(t *testing.T) {
	// An OAuth failure whose ladder rung (5 min for the first retry) has long
	// since elapsed — built directly so the test does not have to wait it out.
	sm := &StateManager{
		currentState:     StateError,
		lastError:        errors.New("request failed with status 429: rate limited during token exchange"),
		isOAuthError:     true,
		oauthRetryCount:  1,
		lastOAuthAttempt: time.Now().Add(-time.Hour),
	}
	require.True(t, sm.ShouldRetryOAuth(), "precondition: the OAuth ladder has elapsed and would retry")

	sm.SetRetryAfter(time.Now().Add(time.Hour))
	assert.False(t, sm.ShouldRetryOAuth(), "the rate-limit window outranks the OAuth ladder")
}

// TestStateManager_SetRetryAfter_IgnoresPastDeadlines guards against a
// misparsed/expired hint permanently poisoning the gate.
func TestStateManager_SetRetryAfter_IgnoresPastDeadlines(t *testing.T) {
	sm := NewStateManager()
	sm.SetRetryAfter(time.Time{})
	assert.True(t, sm.GetConnectionInfo().RetryAfter.IsZero())

	sm.SetRetryAfter(time.Now().Add(-time.Hour))
	assert.True(t, sm.GetConnectionInfo().RetryAfter.IsZero(), "an already-elapsed hint is not worth storing")
}
