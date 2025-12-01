package types

import (
	"testing"

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
