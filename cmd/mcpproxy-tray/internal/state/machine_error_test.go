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

// TestConfigErrorPersistence tests that config errors persist without auto-transitioning
func TestConfigErrorPersistence(t *testing.T) {
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

	// Verify that the state persists without transitioning
	timeout := time.After(5 * time.Second)
	select {
	case transition := <-transitionsCh:
		// If we get any transition, it should NOT be to failed state
		if transition.From == StateCoreErrorConfig && transition.To == StateFailed {
			t.Fatal("Config error should not auto-transition to failed state")
		}
		t.Logf("Got transition from %v to %v (not to failed - good)", transition.From, transition.To)
	case <-timeout:
		// Timeout is expected - config error should persist
		t.Log("Config error persisted without auto-transition (expected behavior)")
	case <-ctx.Done():
		t.Fatal("Context cancelled before test completed")
	}

	// Verify final state is still StateCoreErrorConfig
	finalState := m.GetCurrentState()
	if finalState != StateCoreErrorConfig {
		t.Errorf("Expected state to remain %v, got %v", StateCoreErrorConfig, finalState)
	}

	// Verify that shutdown event is the only valid transition
	newState := m.determineNewState(StateCoreErrorConfig, EventShutdown)
	if newState != StateShuttingDown {
		t.Errorf("Config error should transition to shutdown on EventShutdown, got %v", newState)
	}

	// Verify that other events don't cause transitions
	newState = m.determineNewState(StateCoreErrorConfig, EventRetry)
	if newState != StateCoreErrorConfig {
		t.Errorf("Config error should persist on EventRetry, got %v", newState)
	}
}

// TestStateInfoNoTimeout verifies that config error state has NO timeout configured
func TestStateInfoNoTimeout(t *testing.T) {
	info := GetInfo(StateCoreErrorConfig)

	// Config errors should NOT have a timeout - they should persist
	if info.Timeout != nil {
		t.Error("Config error state should NOT have a timeout configured - errors should persist")
	}

	if info.CanRetry {
		t.Error("Config error state should not be retryable")
	}

	if !info.IsError {
		t.Error("Config error state should be marked as an error")
	}
}

// TestAllErrorStatesPersist verifies that all error states persist without auto-retry
func TestAllErrorStatesPersist(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	defer logger.Sync()

	errorStates := []State{
		StateCoreErrorPortConflict,
		StateCoreErrorDBLocked,
		StateCoreErrorConfig,
		StateCoreErrorGeneral,
	}

	for _, errorState := range errorStates {
		t.Run(string(errorState), func(t *testing.T) {
			m := NewMachine(logger.Sugar())
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			// Start the machine
			m.Start()

			// Subscribe to transitions
			transitionsCh := m.Subscribe()

			// Set initial state to error state
			m.mu.Lock()
			m.currentState = errorState
			m.mu.Unlock()

			// Trigger state entry handler
			m.handleStateEntry(errorState)

			// Verify that the state persists without transitioning
			timeout := time.After(5 * time.Second)
			select {
			case transition := <-transitionsCh:
				// We should NOT get any auto-transitions
				t.Errorf("Error state %v should not auto-transition, got transition to %v", errorState, transition.To)
			case <-timeout:
				// Timeout is expected - error should persist
				t.Logf("Error state %v persisted without auto-transition (expected)", errorState)
			case <-ctx.Done():
				t.Fatal("Context cancelled before test completed")
			}

			// Verify final state is still the error state
			finalState := m.GetCurrentState()
			if finalState != errorState {
				t.Errorf("Expected state to remain %v, got %v", errorState, finalState)
			}

			// Verify that shutdown is the only valid transition
			newState := m.determineNewState(errorState, EventShutdown)
			if newState != StateShuttingDown {
				t.Errorf("Error state %v should transition to shutdown on EventShutdown, got %v", errorState, newState)
			}

			// Verify that retry events don't cause transitions
			newState = m.determineNewState(errorState, EventRetry)
			if newState != errorState {
				t.Errorf("Error state %v should persist on EventRetry, got %v", errorState, newState)
			}
		})
	}
}

// TestErrorStateInfoConfiguration verifies all error states are configured correctly
func TestErrorStateInfoConfiguration(t *testing.T) {
	errorStates := []State{
		StateCoreErrorPortConflict,
		StateCoreErrorDBLocked,
		StateCoreErrorConfig,
		StateCoreErrorGeneral,
	}

	for _, errorState := range errorStates {
		t.Run(string(errorState), func(t *testing.T) {
			info := GetInfo(errorState)

			// All error states should NOT have timeouts
			if info.Timeout != nil {
				t.Errorf("Error state %v should NOT have a timeout - errors should persist", errorState)
			}

			// All error states should NOT be retryable
			if info.CanRetry {
				t.Errorf("Error state %v should not be retryable", errorState)
			}

			// All should be marked as errors
			if !info.IsError {
				t.Errorf("Error state %v should be marked as an error", errorState)
			}

			// All should have helpful user messages
			if info.UserMessage == "" || info.UserMessage == string(errorState) {
				t.Errorf("Error state %v should have a helpful user message, got: %q", errorState, info.UserMessage)
			}
		})
	}
}
