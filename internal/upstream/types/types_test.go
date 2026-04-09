package types

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestConnectionState_String tests the string representation of connection states
func TestConnectionState_String(t *testing.T) {
	tests := []struct {
		state    ConnectionState
		expected string
	}{
		{StateDisconnected, "Disconnected"},
		{StateConnecting, "Connecting"},
		{StatePendingAuth, "Pending Auth"},
		{StateAuthenticating, "Authenticating"},
		{StateDiscovering, "Discovering"},
		{StateReady, "Ready"},
		{StateError, "Error"},
		{ConnectionState(999), "Unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			got := tt.state.String()
			assert.Equal(t, tt.expected, got)
		})
	}
}

// TestStateManager_TransitionTo_PendingAuth tests transitioning to PendingAuth state
func TestStateManager_TransitionTo_PendingAuth(t *testing.T) {
	sm := NewStateManager()

	// Should start in Disconnected state
	assert.Equal(t, StateDisconnected, sm.GetState())

	// Transition to Connecting
	sm.TransitionTo(StateConnecting)
	assert.Equal(t, StateConnecting, sm.GetState())

	// Transition to PendingAuth
	sm.TransitionTo(StatePendingAuth)
	assert.Equal(t, StatePendingAuth, sm.GetState())

	// Can transition back to Connecting (for retry)
	sm.TransitionTo(StateConnecting)
	assert.Equal(t, StateConnecting, sm.GetState())
}

// TestStateManager_PendingAuth_WithCallback tests state change callbacks for PendingAuth
func TestStateManager_PendingAuth_WithCallback(t *testing.T) {
	sm := NewStateManager()

	var callbackInvoked bool
	var oldState, newState ConnectionState

	sm.SetStateChangeCallback(func(old, new ConnectionState, info *ConnectionInfo) {
		callbackInvoked = true
		oldState = old
		newState = new
		assert.Equal(t, StatePendingAuth, info.State)
	})

	sm.TransitionTo(StatePendingAuth)

	assert.True(t, callbackInvoked, "Callback should be invoked")
	assert.Equal(t, StateDisconnected, oldState)
	assert.Equal(t, StatePendingAuth, newState)
}

// TestStateManager_GetConnectionInfo_PendingAuth tests getting connection info for PendingAuth state
func TestStateManager_GetConnectionInfo_PendingAuth(t *testing.T) {
	sm := NewStateManager()
	sm.TransitionTo(StatePendingAuth)

	info := sm.GetConnectionInfo()
	assert.Equal(t, StatePendingAuth, info.State)
	assert.Equal(t, "Pending Auth", info.State.String())
}

// Ensure OAuth retries are blocked after an explicit logout
func TestStateManager_ShouldRetryOAuth_LoggedOut(t *testing.T) {
	sm := NewStateManager()

	// Simulate an OAuth error
	sm.SetOAuthError(errors.New("oauth failed"))
	sm.lastOAuthAttempt = time.Now().Add(-6 * time.Minute)
	sm.oauthRetryCount = 1

	assert.True(t, sm.ShouldRetryOAuth())

	// Mark user logged out and ensure retries are suppressed
	sm.SetUserLoggedOut(true)
	assert.False(t, sm.ShouldRetryOAuth())
}

func TestResetForReconnect_PreservesRetryCount(t *testing.T) {
	sm := NewStateManager()

	// Simulate several failed connection attempts
	for i := 0; i < 5; i++ {
		sm.SetError(errors.New("connection failed"))
	}

	info := sm.GetConnectionInfo()
	assert.Equal(t, 5, info.RetryCount)
	assert.Equal(t, StateError, info.State)

	// ResetForReconnect should keep retryCount but transition to Disconnected
	sm.ResetForReconnect()

	info = sm.GetConnectionInfo()
	assert.Equal(t, StateDisconnected, info.State)
	assert.Equal(t, 5, info.RetryCount, "retryCount must be preserved across reconnect")
	assert.Nil(t, info.LastError, "lastError should be cleared")
}

func TestReset_ClearsRetryCount(t *testing.T) {
	sm := NewStateManager()

	for i := 0; i < 5; i++ {
		sm.SetError(errors.New("connection failed"))
	}

	sm.Reset()

	info := sm.GetConnectionInfo()
	assert.Equal(t, StateDisconnected, info.State)
	assert.Equal(t, 0, info.RetryCount, "Reset should zero retryCount for manual reconnect")
}

func TestShouldRetry_MaxRetries(t *testing.T) {
	sm := NewStateManager()

	// Fill up to MaxConnectionRetries
	for i := 0; i < MaxConnectionRetries; i++ {
		sm.SetError(errors.New("connection failed"))
	}

	// At exactly MaxConnectionRetries, should stop
	assert.False(t, sm.ShouldRetry(), "should not retry after max retries")

	info := sm.GetConnectionInfo()
	assert.True(t, info.GaveUp, "GaveUp should be true when at max retries")
}

func TestShouldRetry_BelowMaxRetries(t *testing.T) {
	sm := NewStateManager()

	// Set a few errors, well below max
	for i := 0; i < 3; i++ {
		sm.SetError(errors.New("connection failed"))
	}
	// Backoff requires waiting, so set lastRetryTime in the past
	sm.mu.Lock()
	sm.lastRetryTime = time.Now().Add(-10 * time.Minute)
	sm.mu.Unlock()

	assert.True(t, sm.ShouldRetry(), "should retry when below max and backoff elapsed")
}

func TestShouldRetry_ResetAfterGaveUp(t *testing.T) {
	sm := NewStateManager()

	// Exhaust retries
	for i := 0; i < MaxConnectionRetries; i++ {
		sm.SetError(errors.New("connection failed"))
	}
	assert.False(t, sm.ShouldRetry())

	// Manual Reset should allow retrying again
	sm.Reset()
	sm.SetError(errors.New("fresh attempt"))
	sm.mu.Lock()
	sm.lastRetryTime = time.Now().Add(-10 * time.Minute)
	sm.mu.Unlock()

	assert.True(t, sm.ShouldRetry(), "should retry after manual Reset clears gave-up state")
}
