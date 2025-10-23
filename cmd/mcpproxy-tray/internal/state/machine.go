//go:build darwin || windows

package state

import (
	"context"
	"sync"
	"time"

	"go.uber.org/zap"
)

// Transition represents a state change with metadata
type Transition struct {
	From      State
	To        State
	Event     Event
	Timestamp time.Time
	Data      map[string]interface{}
	Error     error
}

// Machine manages state transitions for the tray application
type Machine struct {
	mu           sync.RWMutex
	currentState State
	logger       *zap.SugaredLogger

	// Channels for communication
	eventCh       chan Event
	transitionCh  chan Transition
	shutdownCh    chan struct{}
	subscribers   []chan Transition
	subscribersMu sync.RWMutex

	// Retry management
	retryCount map[State]int
	maxRetries map[State]int
	retryDelay map[State]time.Duration
	lastError  error

	// Timeout management
	timeoutTimer *time.Timer
	timeoutMu    sync.Mutex

	// Context for cancellation
	ctx    context.Context
	cancel context.CancelFunc
}

// NewMachine creates a new state machine
func NewMachine(logger *zap.SugaredLogger) *Machine {
	ctx, cancel := context.WithCancel(context.Background())

	// Configure retry settings
	maxRetries := map[State]int{
		StateLaunchingCore:         3,
		StateWaitingForCore:        2,
		StateConnectingAPI:         5,
		StateReconnecting:          10,
		StateCoreErrorPortConflict: 2,
		StateCoreErrorDBLocked:     3,
		StateCoreErrorGeneral:      2,
	}

	retryDelay := map[State]time.Duration{
		StateLaunchingCore:         2 * time.Second,
		StateWaitingForCore:        5 * time.Second,
		StateConnectingAPI:         3 * time.Second,
		StateReconnecting:          5 * time.Second,
		StateCoreErrorPortConflict: 10 * time.Second, // Longer delay for port conflicts
		StateCoreErrorDBLocked:     5 * time.Second,
		StateCoreErrorGeneral:      3 * time.Second,
	}

	return &Machine{
		currentState: StateInitializing,
		logger:       logger,
		eventCh:      make(chan Event, 10),
		transitionCh: make(chan Transition, 100),
		shutdownCh:   make(chan struct{}),
		subscribers:  make([]chan Transition, 0),
		retryCount:   make(map[State]int),
		maxRetries:   maxRetries,
		retryDelay:   retryDelay,
		ctx:          ctx,
		cancel:       cancel,
	}
}

// Start starts the state machine
func (m *Machine) Start() {
	m.logger.Info("State machine starting", "initial_state", m.currentState)
	go m.run()
	// Note: Initial event is now sent by the caller to allow proper SKIP_CORE handling
}

// SendEvent sends an event to the state machine
func (m *Machine) SendEvent(event Event) {
	select {
	case m.eventCh <- event:
		m.logger.Debug("Event sent", "event", event, "current_state", m.GetCurrentState())
	case <-m.ctx.Done():
		m.logger.Debug("Event dropped due to shutdown", "event", event)
	default:
		m.logger.Warn("Event channel full, dropping event", "event", event)
	}
}

// GetCurrentState returns the current state
func (m *Machine) GetCurrentState() State {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.currentState
}

// GetLastError returns the last error that occurred
func (m *Machine) GetLastError() error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.lastError
}

// Subscribe returns a channel for receiving state transitions
func (m *Machine) Subscribe() <-chan Transition {
	m.subscribersMu.Lock()
	defer m.subscribersMu.Unlock()

	ch := make(chan Transition, 10)
	m.subscribers = append(m.subscribers, ch)
	return ch
}

// Shutdown gracefully shuts down the state machine
func (m *Machine) Shutdown() {
	m.logger.Info("State machine shutting down")
	m.SendEvent(EventShutdown)

	// Wait a bit for graceful shutdown
	select {
	case <-m.shutdownCh:
		m.logger.Info("State machine shut down gracefully")
	case <-time.After(5 * time.Second):
		m.logger.Warn("State machine shutdown timeout, forcing")
	}

	m.cancel()
	m.closeTimeoutTimer()
}

// run is the main state machine loop
func (m *Machine) run() {
	defer close(m.shutdownCh)

	for {
		select {
		case event := <-m.eventCh:
			m.handleEvent(event)
		case <-m.ctx.Done():
			m.logger.Info("State machine context cancelled")
			return
		}

		// Check if we're in a terminal state
		if m.GetCurrentState() == StateShuttingDown {
			m.logger.Info("State machine reached terminal state")
			return
		}
	}
}

// handleEvent processes an event and potentially triggers a state transition
func (m *Machine) handleEvent(event Event) {
	m.mu.Lock()
	currentState := m.currentState
	m.mu.Unlock()

	m.logger.Debug("Handling event", "event", event, "current_state", currentState)

	newState := m.determineNewState(currentState, event)

	if newState != currentState {
		m.transition(currentState, newState, event, nil)
	}
}

// determineNewState determines the new state based on current state and event
func (m *Machine) determineNewState(currentState State, event Event) State {
	switch currentState {
	case StateInitializing:
		switch event {
		case EventStart:
			return StateLaunchingCore
		case EventSkipCore:
			return StateConnectingAPI
		case EventShutdown:
			return StateShuttingDown
		}

	case StateLaunchingCore:
		switch event {
		case EventCoreStarted:
			return StateWaitingForCore
		case EventPortConflict:
			return StateCoreErrorPortConflict
		case EventDBLocked:
			return StateCoreErrorDBLocked
		case EventConfigError:
			return StateCoreErrorConfig
		case EventGeneralError, EventTimeout:
			return StateCoreErrorGeneral
		case EventShutdown:
			return StateShuttingDown
		}

	case StateWaitingForCore:
		switch event {
		case EventCoreReady:
			return StateConnectingAPI

		// ADD: Handle error events that can occur while waiting for core
		case EventPortConflict:
			return StateCoreErrorPortConflict
		case EventDBLocked:
			return StateCoreErrorDBLocked
		case EventConfigError:
			return StateCoreErrorConfig
		case EventGeneralError:
			return StateCoreErrorGeneral

		case EventTimeout, EventCoreExited:
			return StateCoreErrorGeneral
		case EventRetry:
			return StateLaunchingCore
		case EventShutdown:
			return StateShuttingDown
		}

	case StateConnectingAPI:
		switch event {
		case EventAPIConnected:
			return StateConnected

		// ADD: Handle core crashes during API connection
		case EventCoreExited:
			return StateCoreErrorGeneral
		case EventPortConflict:
			return StateCoreErrorPortConflict
		case EventDBLocked:
			return StateCoreErrorDBLocked
		case EventConfigError:
			return StateCoreErrorConfig

		case EventConnectionLost, EventTimeout:
			return StateReconnecting
		case EventShutdown:
			return StateShuttingDown
		}

	case StateConnected:
		switch event {
		case EventConnectionLost:
			return StateReconnecting
		case EventShutdown:
			return StateShuttingDown
		}

	case StateReconnecting:
		switch event {
		case EventAPIConnected:
			return StateConnected
		case EventCoreExited:
			return StateLaunchingCore
		case EventRetry:
			// Check retry count
			if m.shouldRetry(StateReconnecting) {
				return StateConnectingAPI
			}
			return StateFailed
		case EventShutdown:
			return StateShuttingDown
		}

	case StateCoreErrorPortConflict, StateCoreErrorDBLocked, StateCoreErrorGeneral:
		switch event {
		case EventShutdown:
			return StateShuttingDown
		// Error states persist - require user to fix issue manually
		// Stay in current error state for all other events
		}

	case StateCoreErrorConfig:
		switch event {
		case EventShutdown:
			return StateShuttingDown
		// Config errors persist - require user to fix config manually
		// Stay in StateCoreErrorConfig for all other events
		}

	case StateFailed:
		if event == EventShutdown {
			return StateShuttingDown
		}

	case StateShuttingDown:
		// Terminal state - no transitions
		return StateShuttingDown
	}

	// No valid transition found
	m.logger.Debug("No valid transition found", "current_state", currentState, "event", event)
	return currentState
}

// transition performs a state transition
func (m *Machine) transition(from, to State, event Event, data map[string]interface{}) {
	if !CanTransition(from, to) {
		m.logger.Error("Invalid state transition", "from", from, "to", to, "event", event)
		return
	}

	m.mu.Lock()
	m.currentState = to
	m.mu.Unlock()

	// Create transition record
	transition := Transition{
		From:      from,
		To:        to,
		Event:     event,
		Timestamp: time.Now(),
		Data:      data,
		Error:     m.lastError,
	}

	m.logger.Info("State transition",
		"from", from,
		"to", to,
		"event", event,
		"retry_count", m.retryCount[from])

	// Reset retry count on successful progress
	if !GetInfo(to).IsError {
		m.retryCount[from] = 0
	}

	// Handle state-specific actions
	m.handleStateEntry(to)

	// Notify subscribers
	m.notifySubscribers(&transition)

	// Send to transition channel
	select {
	case m.transitionCh <- transition:
	default:
		// Channel full, drop oldest
		select {
		case <-m.transitionCh:
			m.transitionCh <- transition
		default:
		}
	}
}

// handleStateEntry performs actions when entering a new state
func (m *Machine) handleStateEntry(state State) {
	stateInfo := GetInfo(state)

	// Set up timeout if the state has one
	if stateInfo.Timeout != nil {
		m.setStateTimeout(*stateInfo.Timeout)
	} else {
		m.clearStateTimeout()
	}

	// All error states now persist until user fixes the issue or shuts down
	// No auto-retry or auto-transition to failed state for any error states
}

// shouldRetry checks if we should retry for the given state
func (m *Machine) shouldRetry(state State) bool {
	maxRetries, exists := m.maxRetries[state]
	if !exists {
		return false
	}

	currentCount := m.retryCount[state]
	if currentCount >= maxRetries {
		m.logger.Warn("Max retries exceeded", "state", state, "count", currentCount, "max", maxRetries)
		return false
	}

	m.retryCount[state]++
	return true
}

// setStateTimeout sets a timeout for the current state
func (m *Machine) setStateTimeout(duration time.Duration) {
	m.timeoutMu.Lock()
	defer m.timeoutMu.Unlock()

	m.clearTimeoutTimerUnsafe()

	m.timeoutTimer = time.AfterFunc(duration, func() {
		m.logger.Warn("State timeout", "state", m.GetCurrentState(), "duration", duration)
		m.SendEvent(EventTimeout)
	})
}

// clearStateTimeout clears the current state timeout
func (m *Machine) clearStateTimeout() {
	m.timeoutMu.Lock()
	defer m.timeoutMu.Unlock()
	m.clearTimeoutTimerUnsafe()
}

// closeTimeoutTimer closes the timeout timer (for shutdown)
func (m *Machine) closeTimeoutTimer() {
	m.timeoutMu.Lock()
	defer m.timeoutMu.Unlock()
	m.clearTimeoutTimerUnsafe()
}

// clearTimeoutTimerUnsafe clears the timeout timer without locking
func (m *Machine) clearTimeoutTimerUnsafe() {
	if m.timeoutTimer != nil {
		m.timeoutTimer.Stop()
		m.timeoutTimer = nil
	}
}

// notifySubscribers sends transition notifications to all subscribers
func (m *Machine) notifySubscribers(transition *Transition) {
	m.subscribersMu.RLock()
	defer m.subscribersMu.RUnlock()

	for _, subscriber := range m.subscribers {
		select {
		case subscriber <- *transition:
		default:
			// Subscriber channel full, skip
			m.logger.Debug("Subscriber channel full, dropping transition notification")
		}
	}
}

// SetError sets an error on the state machine
func (m *Machine) SetError(err error) {
	m.mu.Lock()
	m.lastError = err
	m.mu.Unlock()
}
