//go:build darwin

package state

import (
	"context"
	"testing"
	"time"

	"go.uber.org/zap"
)

// TestErrorEventHandling tests that error events are properly handled in StateWaitingForCore
func TestErrorEventHandling(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	defer logger.Sync()

	tests := []struct {
		name          string
		initialState  State
		event         Event
		expectedState State
	}{
		{
			name:          "Port conflict during waiting for core",
			initialState:  StateWaitingForCore,
			event:         EventPortConflict,
			expectedState: StateCoreErrorPortConflict,
		},
		{
			name:          "DB locked during waiting for core",
			initialState:  StateWaitingForCore,
			event:         EventDBLocked,
			expectedState: StateCoreErrorDBLocked,
		},
		{
			name:          "Config error during waiting for core",
			initialState:  StateWaitingForCore,
			event:         EventConfigError,
			expectedState: StateCoreErrorConfig,
		},
		{
			name:          "General error during waiting for core",
			initialState:  StateWaitingForCore,
			event:         EventGeneralError,
			expectedState: StateCoreErrorGeneral,
		},
		{
			name:          "Port conflict during API connection",
			initialState:  StateConnectingAPI,
			event:         EventPortConflict,
			expectedState: StateCoreErrorPortConflict,
		},
		{
			name:          "DB locked during API connection",
			initialState:  StateConnectingAPI,
			event:         EventDBLocked,
			expectedState: StateCoreErrorDBLocked,
		},
		{
			name:          "Config error during API connection",
			initialState:  StateConnectingAPI,
			event:         EventConfigError,
			expectedState: StateCoreErrorConfig,
		},
		{
			name:          "Core exited during API connection",
			initialState:  StateConnectingAPI,
			event:         EventCoreExited,
			expectedState: StateCoreErrorGeneral,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewMachine(logger.Sugar())
			m.currentState = tt.initialState

			newState := m.determineNewState(tt.initialState, tt.event)

			if newState != tt.expectedState {
				t.Errorf("Expected state %v, got %v", tt.expectedState, newState)
			}

			// Verify the transition is allowed
			if !CanTransition(tt.initialState, newState) {
				t.Errorf("Transition from %v to %v should be allowed", tt.initialState, newState)
			}
		})
	}
}

// TestConfigErrorAutoTransition tests that config errors automatically transition to failed state
func TestConfigErrorAutoTransition(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	defer logger.Sync()

	m := NewMachine(logger.Sugar())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start the machine
	m.Start()

	// Subscribe to transitions
	transitionsCh := m.Subscribe()

	// Set initial state to StateCoreErrorConfig
	m.mu.Lock()
	m.currentState = StateCoreErrorConfig
	m.mu.Unlock()

	// Trigger state entry handler
	m.handleStateEntry(StateCoreErrorConfig)

	// Wait for auto-transition (should happen within 3 seconds + some margin)
	timeout := time.After(5 * time.Second)
	transitionFound := false

	for !transitionFound {
		select {
		case transition := <-transitionsCh:
			if transition.From == StateCoreErrorConfig && transition.To == StateFailed {
				transitionFound = true
				t.Logf("Config error auto-transitioned to failed state after timeout")
			}
		case <-timeout:
			t.Fatal("Config error did not auto-transition to failed state within expected time")
		case <-ctx.Done():
			t.Fatal("Context cancelled before auto-transition")
		}
	}

	// Verify final state
	finalState := m.GetCurrentState()
	if finalState != StateFailed {
		t.Errorf("Expected final state %v, got %v", StateFailed, finalState)
	}
}

// TestStateInfoTimeout verifies that config error state has a timeout configured
func TestStateInfoTimeout(t *testing.T) {
	info := GetInfo(StateCoreErrorConfig)

	if info.Timeout == nil {
		t.Error("Config error state should have a timeout configured")
	}

	expectedTimeout := 3 * time.Second
	if *info.Timeout != expectedTimeout {
		t.Errorf("Expected timeout %v, got %v", expectedTimeout, *info.Timeout)
	}

	if info.CanRetry {
		t.Error("Config error state should not be retryable")
	}

	if !info.IsError {
		t.Error("Config error state should be marked as an error")
	}
}
