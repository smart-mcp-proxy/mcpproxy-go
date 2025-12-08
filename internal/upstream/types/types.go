package types

import (
	"fmt"
	"sync"
	"time"
)

// ConnectionState represents the state of an upstream connection
type ConnectionState int

const (
	// StateDisconnected indicates the upstream is not connected
	StateDisconnected ConnectionState = iota
	// StateConnecting indicates the upstream is attempting to connect
	StateConnecting
	// StatePendingAuth indicates the upstream requires OAuth authentication but is deferred (e.g., waiting for user action)
	StatePendingAuth
	// StateAuthenticating indicates the upstream is performing OAuth authentication
	StateAuthenticating
	// StateDiscovering indicates the upstream is discovering available tools
	StateDiscovering
	// StateReady indicates the upstream is connected and ready for requests
	StateReady
	// StateError indicates the upstream encountered an error
	StateError
)

// String returns the string representation of the connection state
func (s ConnectionState) String() string {
	switch s {
	case StateDisconnected:
		return "Disconnected"
	case StateConnecting:
		return "Connecting"
	case StatePendingAuth:
		return "Pending Auth"
	case StateAuthenticating:
		return "Authenticating"
	case StateDiscovering:
		return "Discovering"
	case StateReady:
		return "Ready"
	case StateError:
		return "Error"
	default:
		return "Unknown"
	}
}

// ConnectionInfo holds information about the current connection state
type ConnectionInfo struct {
	State            ConnectionState `json:"state"`
	LastError        error           `json:"last_error,omitempty"`
	RetryCount       int             `json:"retry_count"`
	LastRetryTime    time.Time       `json:"last_retry_time,omitempty"`
	ServerName       string          `json:"server_name,omitempty"`
	ServerVersion    string          `json:"server_version,omitempty"`
	LastOAuthAttempt time.Time       `json:"last_oauth_attempt,omitempty"`
	OAuthRetryCount  int             `json:"oauth_retry_count"`
	IsOAuthError     bool            `json:"is_oauth_error"`
}

// StateManager manages the state transitions for an upstream connection
type StateManager struct {
	mu               sync.RWMutex
	currentState     ConnectionState
	lastError        error
	retryCount       int
	lastRetryTime    time.Time
	serverName       string
	serverVersion    string
	lastOAuthAttempt time.Time
	oauthRetryCount  int
	isOAuthError     bool
	userLoggedOut    bool // When true, prevents auto-reconnection until user explicitly logs in

	// Callbacks for state transitions
	onStateChange func(oldState, newState ConnectionState, info *ConnectionInfo)
}

// NewStateManager creates a new state manager
func NewStateManager() *StateManager {
	return &StateManager{
		currentState: StateDisconnected,
	}
}

// SetStateChangeCallback sets a callback function that will be called on state changes
func (sm *StateManager) SetStateChangeCallback(callback func(oldState, newState ConnectionState, info *ConnectionInfo)) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.onStateChange = callback
}

// GetStateChangeCallback returns the current state change callback
func (sm *StateManager) GetStateChangeCallback() func(oldState, newState ConnectionState, info *ConnectionInfo) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.onStateChange
}

// GetState returns the current connection state
func (sm *StateManager) GetState() ConnectionState {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.currentState
}

// GetConnectionInfo returns detailed connection information
func (sm *StateManager) GetConnectionInfo() ConnectionInfo {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	return ConnectionInfo{
		State:            sm.currentState,
		LastError:        sm.lastError,
		RetryCount:       sm.retryCount,
		LastRetryTime:    sm.lastRetryTime,
		ServerName:       sm.serverName,
		ServerVersion:    sm.serverVersion,
		LastOAuthAttempt: sm.lastOAuthAttempt,
		OAuthRetryCount:  sm.oauthRetryCount,
		IsOAuthError:     sm.isOAuthError,
	}
}

// TransitionTo transitions to a new state
func (sm *StateManager) TransitionTo(newState ConnectionState) {
	sm.mu.Lock()
	oldState := sm.currentState

	// Validate transition
	if err := sm.ValidateTransition(oldState, newState); err != nil {
		// For now, log the validation error but allow the transition
		// In the future, we might want to be stricter
		fmt.Printf("Invalid state transition: %v (from %s to %s)\n", err, oldState.String(), newState.String())
	}

	sm.currentState = newState

	// Clear error on successful transitions
	if newState == StateReady {
		sm.lastError = nil
		sm.retryCount = 0
		sm.isOAuthError = false
		sm.oauthRetryCount = 0
		sm.userLoggedOut = false // Clear logout flag on successful connection
	}

	info := ConnectionInfo{
		State:            sm.currentState,
		LastError:        sm.lastError,
		RetryCount:       sm.retryCount,
		LastRetryTime:    sm.lastRetryTime,
		ServerName:       sm.serverName,
		ServerVersion:    sm.serverVersion,
		LastOAuthAttempt: sm.lastOAuthAttempt,
		OAuthRetryCount:  sm.oauthRetryCount,
		IsOAuthError:     sm.isOAuthError,
	}

	callback := sm.onStateChange
	sm.mu.Unlock()

	// Call the callback outside the lock to avoid deadlocks
	if callback != nil {
		callback(oldState, newState, &info)
	}
}

// SetError sets an error and transitions to error state
func (sm *StateManager) SetError(err error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	oldState := sm.currentState
	sm.currentState = StateError
	sm.lastError = err
	sm.retryCount++
	sm.lastRetryTime = time.Now()

	info := ConnectionInfo{
		State:            sm.currentState,
		LastError:        sm.lastError,
		RetryCount:       sm.retryCount,
		LastRetryTime:    sm.lastRetryTime,
		ServerName:       sm.serverName,
		ServerVersion:    sm.serverVersion,
		LastOAuthAttempt: sm.lastOAuthAttempt,
		OAuthRetryCount:  sm.oauthRetryCount,
		IsOAuthError:     sm.isOAuthError,
	}

	callback := sm.onStateChange

	// Call the callback outside the lock to avoid deadlocks
	if callback != nil {
		go callback(oldState, StateError, &info)
	}
}

// SetServerInfo sets the server information
func (sm *StateManager) SetServerInfo(name, version string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.serverName = name
	sm.serverVersion = version
}

// SetUserLoggedOut marks that the user has explicitly logged out
// This prevents automatic reconnection until cleared (e.g., by explicit login)
func (sm *StateManager) SetUserLoggedOut(loggedOut bool) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.userLoggedOut = loggedOut
}

// IsUserLoggedOut returns true if the user has explicitly logged out
func (sm *StateManager) IsUserLoggedOut() bool {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.userLoggedOut
}

// ShouldRetry returns true if the connection should be retried based on exponential backoff
func (sm *StateManager) ShouldRetry() bool {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	// Don't auto-reconnect if user explicitly logged out
	if sm.userLoggedOut {
		return false
	}

	if sm.currentState != StateError {
		return false
	}

	if sm.retryCount == 0 {
		return true
	}

	// Calculate exponential backoff
	// Ensure retry count is valid and within safe range to avoid overflow
	retryCount := sm.retryCount - 1
	if retryCount < 0 {
		retryCount = 0
	}
	if retryCount > 30 { // Cap at 30 to prevent overflow in 64-bit systems
		retryCount = 30
	}
	backoffDuration := time.Duration(1<<uint(retryCount)) * time.Second //nolint:gosec // retryCount is bounds-checked above
	maxBackoff := 5 * time.Minute
	if backoffDuration > maxBackoff {
		backoffDuration = maxBackoff
	}

	return time.Since(sm.lastRetryTime) >= backoffDuration
}

// IsState checks if the current state matches the given state
func (sm *StateManager) IsState(state ConnectionState) bool {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.currentState == state
}

// IsReady returns true if the connection is ready for requests
func (sm *StateManager) IsReady() bool {
	return sm.IsState(StateReady)
}

// IsConnecting returns true if the connection is in progress
func (sm *StateManager) IsConnecting() bool {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.currentState == StateConnecting || sm.currentState == StateAuthenticating || sm.currentState == StateDiscovering
}

// ValidateTransition validates if a state transition is allowed
func (sm *StateManager) ValidateTransition(from, to ConnectionState) error {
	// Define valid transitions
	validTransitions := map[ConnectionState][]ConnectionState{
		StateDisconnected:   {StateConnecting},
		StateConnecting:     {StateAuthenticating, StateDiscovering, StateReady, StateError, StateDisconnected}, // Allow direct to Ready for OAuth flows
		StateAuthenticating: {StateConnecting, StateDiscovering, StateReady, StateError, StateDisconnected},
		StateDiscovering:    {StateReady, StateError, StateDisconnected},
		StateReady:          {StateError, StateDisconnected},
		StateError:          {StateConnecting, StateDisconnected},
	}

	allowed, exists := validTransitions[from]
	if !exists {
		return fmt.Errorf("invalid source state: %s", from)
	}

	for _, validTo := range allowed {
		if validTo == to {
			return nil
		}
	}

	return fmt.Errorf("invalid transition from %s to %s", from, to)
}

// Reset resets the state manager to disconnected state
func (sm *StateManager) Reset() {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	oldState := sm.currentState
	sm.currentState = StateDisconnected
	sm.lastError = nil
	sm.retryCount = 0
	sm.lastRetryTime = time.Time{}
	sm.serverName = ""
	sm.serverVersion = ""
	sm.lastOAuthAttempt = time.Time{}
	sm.oauthRetryCount = 0
	sm.isOAuthError = false

	info := ConnectionInfo{
		State:            sm.currentState,
		LastError:        sm.lastError,
		RetryCount:       sm.retryCount,
		LastRetryTime:    sm.lastRetryTime,
		ServerName:       sm.serverName,
		ServerVersion:    sm.serverVersion,
		LastOAuthAttempt: sm.lastOAuthAttempt,
		OAuthRetryCount:  sm.oauthRetryCount,
		IsOAuthError:     sm.isOAuthError,
	}

	callback := sm.onStateChange

	// Call the callback outside the lock to avoid deadlocks
	if callback != nil {
		go callback(oldState, StateDisconnected, &info)
	}
}

// SetOAuthError sets an OAuth-specific error with longer backoff periods
func (sm *StateManager) SetOAuthError(err error) {
	sm.mu.Lock()

	oldState := sm.currentState
	sm.currentState = StateError
	sm.lastError = err
	sm.isOAuthError = true
	sm.oauthRetryCount++
	sm.lastOAuthAttempt = time.Now()
	sm.lastRetryTime = time.Now()

	info := ConnectionInfo{
		State:            sm.currentState,
		LastError:        sm.lastError,
		RetryCount:       sm.retryCount,
		LastRetryTime:    sm.lastRetryTime,
		ServerName:       sm.serverName,
		ServerVersion:    sm.serverVersion,
		LastOAuthAttempt: sm.lastOAuthAttempt,
		OAuthRetryCount:  sm.oauthRetryCount,
		IsOAuthError:     sm.isOAuthError,
	}

	callback := sm.onStateChange
	sm.mu.Unlock()

	// Call the callback outside the lock to avoid deadlocks
	if callback != nil {
		callback(oldState, StateError, &info)
	}
}

// ShouldRetryOAuth returns true if OAuth should be retried with much longer backoff intervals
func (sm *StateManager) ShouldRetryOAuth() bool {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	// Don't auto-retry OAuth flows if the user explicitly logged out
	if sm.userLoggedOut {
		return false
	}

	if !sm.isOAuthError || sm.currentState != StateError {
		return false
	}

	if sm.oauthRetryCount == 0 {
		return true
	}

	// OAuth has much longer backoff intervals: 5min, 15min, 1h, 4h, 24h
	var backoffDuration time.Duration
	switch {
	case sm.oauthRetryCount <= 1:
		backoffDuration = 5 * time.Minute
	case sm.oauthRetryCount <= 2:
		backoffDuration = 15 * time.Minute
	case sm.oauthRetryCount <= 3:
		backoffDuration = 1 * time.Hour
	case sm.oauthRetryCount <= 4:
		backoffDuration = 4 * time.Hour
	default:
		backoffDuration = 24 * time.Hour // Max backoff for OAuth: 24 hours
	}

	return time.Since(sm.lastOAuthAttempt) >= backoffDuration
}

// IsOAuthError returns true if the last error was OAuth-related
func (sm *StateManager) IsOAuthError() bool {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.isOAuthError
}
