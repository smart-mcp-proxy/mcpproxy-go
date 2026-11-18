package types

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

// GH #1929: the connectivity watcher may only reconnect a server whose CURRENT
// failure is a network failure (DNS, dial, timeout). The flag must follow the
// last recorded failure and never outlive it.
func TestStateManager_NetworkFailureFollowsTheLastFailure(t *testing.T) {
	sm := NewStateManager()
	netErr := errors.New("dial tcp: lookup mcp.example: no such host")

	sm.SetNetworkError(netErr)
	info := sm.GetConnectionInfo()
	assert.Equal(t, StateError, info.State)
	assert.True(t, info.NetworkFailure)
	assert.Equal(t, 1, info.RetryCount, "a network failure advances the backoff like any other")
	assert.Equal(t, netErr, info.LastError)

	sm.SetError(errors.New("unexpected status 500"))
	assert.False(t, sm.GetConnectionInfo().NetworkFailure, "a later non-network failure replaces it")
	assert.Equal(t, 2, sm.GetConnectionInfo().RetryCount)

	cases := map[string]func(){
		"oauth error":      func() { sm.SetOAuthError(errors.New("invalid_grant")) },
		"terminal error":   func() { sm.SetTerminalError(errors.New("no such file"), "MCPX_X") },
		"pending auth":     func() { sm.SetPendingAuth(errors.New("sign in required")) },
		"reset":            func() { sm.Reset() },
		"reset for retry":  func() { sm.ResetForReconnect() },
		"transition ready": func() { sm.TransitionTo(StateReady) },
	}
	for name, clear := range cases {
		sm.Reset()
		sm.SetNetworkError(netErr)
		assert.True(t, sm.GetConnectionInfo().NetworkFailure, name)
		clear()
		assert.False(t, sm.GetConnectionInfo().NetworkFailure, "%s must clear the network flag", name)
	}
}

func TestStateManager_NetworkFailureKeepsTheGiveUpRule(t *testing.T) {
	sm := NewStateManager()
	for i := 0; i < MaxConnectionRetries; i++ {
		sm.SetNetworkError(errors.New("i/o timeout"))
	}
	info := sm.GetConnectionInfo()
	assert.True(t, info.GaveUp, "network failures still count toward give-up; recovery is the watcher's job")
	assert.True(t, info.NetworkFailure)
	assert.False(t, sm.ShouldRetry())
}
