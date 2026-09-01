package types

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestShouldAutoReconnect_TerminalNeverRetries is the core regression for GH
// #1145: a server whose failure the classifier proved deterministic must stop
// being re-dialed by the supervisor, at every point on the ladder.
//
// On the unfixed tree the -25h case returns true — GaveUpProbeInterval keeps
// probing a guaranteed failure every 30 minutes, forever (35 of the 55 attempts
// in the reported log).
func TestShouldAutoReconnect_TerminalNeverRetries(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name string
		info *ConnectionInfo
		want bool
	}{
		{
			"terminal, backoff window elapsed",
			&ConnectionInfo{State: StateError, Terminal: true, TerminalCode: "MCPX_DOCKER_EXEC_NOT_FOUND",
				RetryCount: PermanentFailureAttempts, LastRetryTime: now.Add(-time.Hour)},
			false,
		},
		{
			"terminal, a whole day later",
			&ConnectionInfo{State: StateError, Terminal: true, TerminalCode: "MCPX_DOCKER_EXEC_NOT_FOUND",
				RetryCount: PermanentFailureAttempts, LastRetryTime: now.Add(-25 * time.Hour)},
			false,
		},
		{
			// ResetForReconnect parks a mid-attempt client in Disconnected,
			// which the state switch's default arm answers "true". The terminal
			// gate must sit ABOVE that switch or the park leaks.
			"terminal, parked in Disconnected",
			&ConnectionInfo{State: StateDisconnected, Terminal: true, TerminalCode: "MCPX_STDIO_SPAWN_ENOENT",
				RetryCount: PermanentFailureAttempts, LastRetryTime: now.Add(-25 * time.Hour)},
			false,
		},
		{
			// One confirmation attempt: we never park on a single failure, so a
			// classifier false positive costs a retry rather than the server.
			"terminal but confirmation attempt still owed",
			&ConnectionInfo{State: StateError, Terminal: true, TerminalCode: "MCPX_STDIO_SPAWN_ENOENT",
				RetryCount: 1, LastRetryTime: now.Add(-time.Hour)},
			true,
		},
		{
			"not terminal, gave-up probe still self-heals",
			&ConnectionInfo{State: StateError, GaveUp: true, LastRetryTime: now.Add(-25 * time.Hour)},
			true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.info.ShouldAutoReconnect(now))
		})
	}
}

// TestShouldRetry_TerminalStopsAtConfirmation pins the in-client half of the
// same policy and the size of the confirmation budget.
func TestShouldRetry_TerminalStopsAtConfirmation(t *testing.T) {
	require.Equal(t, 2, PermanentFailureAttempts, "the confirmation budget is one extra attempt")

	sm := NewStateManager()
	sm.TransitionTo(StateConnecting)
	sm.SetTerminalError(errors.New(`exec: "uvx": executable file not found in $PATH`), "MCPX_DOCKER_EXEC_NOT_FOUND")

	sm.mu.Lock()
	sm.lastRetryTime = time.Now().Add(-time.Hour)
	sm.mu.Unlock()
	assert.True(t, sm.ShouldRetry(), "the first permanent failure must still get one confirmation attempt")

	sm.SetTerminalError(errors.New(`exec: "uvx": executable file not found in $PATH`), "MCPX_DOCKER_EXEC_NOT_FOUND")
	sm.mu.Lock()
	sm.lastRetryTime = time.Now().Add(-time.Hour)
	sm.mu.Unlock()
	assert.False(t, sm.ShouldRetry(), "a confirmed permanent failure must stop retrying")

	info := sm.GetConnectionInfo()
	assert.True(t, info.Terminal)
	assert.Equal(t, "MCPX_DOCKER_EXEC_NOT_FOUND", info.TerminalCode)
}

// TestSetError_ClearsTerminal: an unclassified follow-up failure must not
// inherit permanence from an earlier classified one. Plain SetError is used by
// the health-probe and call paths, none of which prove anything about config.
func TestSetError_ClearsTerminal(t *testing.T) {
	sm := NewStateManager()
	sm.TransitionTo(StateConnecting)
	sm.SetTerminalError(errors.New("missing toolchain"), "MCPX_DOCKER_EXEC_NOT_FOUND")
	require.True(t, sm.GetConnectionInfo().Terminal)

	sm.SetError(errors.New("connection reset by peer"))

	info := sm.GetConnectionInfo()
	assert.False(t, info.Terminal, "plain SetError must clear the terminal park")
	assert.Empty(t, info.TerminalCode)
}

// TestTerminalClearedByRecoveryPaths covers the recovery contract from GH
// #1145: a user who fixes the problem must see the server come back. Reset() is
// the manual-reconnect / Disconnect path; StateReady is the success path;
// ClearTerminal is the explicit un-park.
func TestTerminalClearedByRecoveryPaths(t *testing.T) {
	t.Run("Reset", func(t *testing.T) {
		sm := NewStateManager()
		sm.TransitionTo(StateConnecting)
		sm.SetTerminalError(errors.New("boom"), "MCPX_STDIO_SPAWN_ENOENT")
		sm.Reset()
		info := sm.GetConnectionInfo()
		assert.False(t, info.Terminal)
		assert.Empty(t, info.TerminalCode)
	})

	t.Run("StateReady", func(t *testing.T) {
		sm := NewStateManager()
		sm.TransitionTo(StateConnecting)
		sm.SetTerminalError(errors.New("boom"), "MCPX_STDIO_SPAWN_ENOENT")
		sm.TransitionTo(StateConnecting)
		sm.TransitionTo(StateReady)
		info := sm.GetConnectionInfo()
		assert.False(t, info.Terminal)
		assert.Empty(t, info.TerminalCode)
	})

	t.Run("ClearTerminal", func(t *testing.T) {
		sm := NewStateManager()
		sm.TransitionTo(StateConnecting)
		sm.SetTerminalError(errors.New("boom"), "MCPX_STDIO_SPAWN_ENOENT")
		sm.ClearTerminal()
		sm.mu.Lock()
		sm.lastRetryTime = time.Now().Add(-time.Hour)
		sm.mu.Unlock()
		assert.False(t, sm.GetConnectionInfo().Terminal)
		assert.True(t, sm.ShouldRetry(), "an un-parked server retries again")
	})

	t.Run("ResetForReconnect preserves", func(t *testing.T) {
		sm := NewStateManager()
		sm.TransitionTo(StateConnecting)
		sm.SetTerminalError(errors.New("boom"), "MCPX_STDIO_SPAWN_ENOENT")
		sm.SetTerminalError(errors.New("boom"), "MCPX_STDIO_SPAWN_ENOENT")
		sm.ResetForReconnect()
		info := sm.GetConnectionInfo()
		assert.True(t, info.Terminal, "a mid-attempt teardown must not launder the park")
		assert.False(t, info.ShouldAutoReconnect(time.Now()))
	})
}

// TestGaveUpProbeBackoff covers the unclassified tail: a failure we could NOT
// prove permanent still must not probe at a flat 30 minutes forever. The first
// probe is unchanged, so every previously-pinned case still holds.
func TestGaveUpProbeBackoff(t *testing.T) {
	tests := []struct {
		retryCount int
		expected   time.Duration
	}{
		{0, GaveUpProbeInterval},
		{MaxConnectionRetries, GaveUpProbeInterval},
		{MaxConnectionRetries + 1, 1 * time.Hour},
		{MaxConnectionRetries + 2, 2 * time.Hour},
		{MaxConnectionRetries + 3, 4 * time.Hour},
		{MaxConnectionRetries + 4, 6 * time.Hour},
		{MaxConnectionRetries + 50, 6 * time.Hour},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.expected, GaveUpProbeBackoff(tt.retryCount), "retryCount=%d", tt.retryCount)
	}
}

// TestShouldAutoReconnect_GaveUpProbeEscalates proves the escalation is live in
// the gate, not just in the helper.
func TestShouldAutoReconnect_GaveUpProbeEscalates(t *testing.T) {
	now := time.Now()

	// 22 failures in: the probe window is 2h, so 45 minutes is too soon.
	within := &ConnectionInfo{State: StateError, GaveUp: true, RetryCount: MaxConnectionRetries + 2, LastRetryTime: now.Add(-45 * time.Minute)}
	assert.False(t, within.ShouldAutoReconnect(now))

	elapsed := &ConnectionInfo{State: StateError, GaveUp: true, RetryCount: MaxConnectionRetries + 2, LastRetryTime: now.Add(-3 * time.Hour)}
	assert.True(t, elapsed.ShouldAutoReconnect(now))
}
