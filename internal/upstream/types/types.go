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
// MaxConnectionRetries is the maximum number of consecutive connection failures
// before giving up automatic reconnection. The server can still be reconnected
// manually or via reconnect-on-use.
const MaxConnectionRetries = 20

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
	GaveUp           bool            `json:"gave_up"` // True when max retries exceeded
	// RetryAfter is the instant before which the upstream asked us not to come
	// back, captured from a `Retry-After` header on a rate-limited response
	// (#1040). Zero when the upstream gave no hint. It acts as a floor under the
	// retry ladder — the effective delay is max(backoff, Retry-After).
	RetryAfter time.Time `json:"retry_after,omitempty"`

	// Terminal marks a failure the diagnostics classifier proved deterministic:
	// the same attempt will fail identically until a human edits the config, the
	// image, or the machine. Automatic reconnection stops once the confirmation
	// budget (PermanentFailureAttempts) is spent — retrying a guaranteed failure
	// burns a container spawn, a package install and a log flood per attempt for
	// no chance of success (GH #1145: 55 identical attempts over 19 hours).
	//
	// It is CLEARED by every recovery path — a successful connection, an
	// explicit Reset (manual reconnect / disconnect), and client recreation on a
	// config change — so a user who fixes the problem always gets the server
	// back. See StateManager.SetTerminalError.
	Terminal bool `json:"terminal,omitempty"`
	// TerminalCode is the stable MCPX_* diagnostics code that proved the failure
	// permanent. Carried as a plain string so this package stays a leaf (no
	// import of internal/diagnostics). Empty unless Terminal is set.
	TerminalCode string `json:"terminal_code,omitempty"`
}

// PermanentFailureAttempts is how many classified-permanent connection failures
// are required before automatic reconnection stops.
//
// Two, not one: the second attempt is a confirmation. A classifier false
// positive then costs one wasted retry instead of stranding a server that would
// have recovered — while still cutting the GH #1145 log from 55 attempts to 2.
const PermanentFailureAttempts = 2

// GaveUpProbeBackoff returns how long to wait between post-give-up probes.
//
// The flat GaveUpProbeInterval self-heals a long outage, but for a failure that
// will never fix itself it is simply a 30-minute-forever loop — 35 of the 55
// attempts in GH #1145. Codes we can PROVE permanent are parked outright (see
// ConnectionInfo.Terminal); this decay covers the tail we cannot prove, without
// ever claiming permanence: 30m, 1h, 2h, 4h, then a 6h ceiling. The counter is
// free — SetError keeps bumping RetryCount past MaxConnectionRetries.
//
// The first probe is deliberately unchanged at GaveUpProbeInterval, so the
// #1013 self-healing guarantee holds for every outage that recovers promptly.
func GaveUpProbeBackoff(retryCount int) time.Duration {
	exponent := retryCount - MaxConnectionRetries
	if exponent < 0 {
		exponent = 0
	}
	const maxExponent = 4 // 30m << 4 == 8h, clamped to the 6h ceiling below
	if exponent > maxExponent {
		exponent = maxExponent
	}
	d := GaveUpProbeInterval << uint(exponent)
	if d > MaxGaveUpProbeInterval {
		return MaxGaveUpProbeInterval
	}
	return d
}

// RetryBackoffDuration returns the exponential backoff to wait after the given
// number of consecutive connection failures: 1s, 2s, 4s, ... capped at 5 minutes.
func RetryBackoffDuration(retryCount int) time.Duration {
	// Ensure retry count is valid and within safe range to avoid overflow
	exponent := retryCount - 1
	if exponent < 0 {
		exponent = 0
	}
	if exponent > 30 { // Cap at 30 to prevent overflow in 64-bit systems
		exponent = 30
	}
	backoffDuration := time.Duration(1<<uint(exponent)) * time.Second //nolint:gosec // exponent is bounds-checked above
	maxBackoff := 5 * time.Minute
	if backoffDuration > maxBackoff {
		backoffDuration = maxBackoff
	}
	return backoffDuration
}

// OAuthRetryBackoffDuration returns the (much longer) backoff to wait after the
// given number of consecutive OAuth failures: 5min, 15min, 1h, 4h, then 24h.
// An OAuth failure cannot be resolved by redialing quickly — it needs a token
// refresh or a human completing a login — so its ladder is deliberately coarse.
func OAuthRetryBackoffDuration(oauthRetryCount int) time.Duration {
	switch {
	case oauthRetryCount <= 1:
		return 5 * time.Minute
	case oauthRetryCount <= 2:
		return 15 * time.Minute
	case oauthRetryCount <= 3:
		return 1 * time.Hour
	case oauthRetryCount <= 4:
		return 4 * time.Hour
	default:
		return 24 * time.Hour // Max backoff for OAuth: 24 hours
	}
}

// GaveUpProbeInterval is how often a given-up server is still probed by the
// periodic reconciliation. After MaxConnectionRetries consecutive failures the
// client stops its own retry ladder, but "never again" would leave an upstream
// silently dead after any outage longer than the ladder (laptop sleep, VPN drop,
// overnight maintenance) until a human notices and reconnects by hand — a failed
// upstream is invisible to the operator (#1013). One probe every 30 minutes keeps
// that self-healing at ~48 requests/day instead of ~2880.
const GaveUpProbeInterval = 30 * time.Minute

// MaxGaveUpProbeInterval is the ceiling of the escalating post-give-up probe
// (GaveUpProbeBackoff). Bounded at 6 hours so an unattended machine still
// recovers within a working day after a very long outage.
const MaxGaveUpProbeInterval = 6 * time.Hour

// ShouldAutoReconnect reports whether an automatic (supervisor-driven) reconnect
// attempt is appropriate given the connection's failure history. It returns false
// while a backoff window from the last failure has not elapsed and for servers
// parked in PendingAuth — redialing cannot succeed until the user completes the
// OAuth login, and each attempt costs real requests against the upstream. Manual
// reconnects, login flows, and reconnect-on-use are not subject to this policy.
func (ci *ConnectionInfo) ShouldAutoReconnect(now time.Time) bool {
	if ci == nil {
		return true
	}
	// A `Retry-After` from a rate-limited upstream is a floor under every other
	// rung of the ladder: the vendor told us in so many words when to come back,
	// and dialing earlier only burns quota. It applies whatever the state,
	// because a 429 can arrive on a tool call as easily as on connect (#1040).
	if !ci.RetryAfter.IsZero() && now.Before(ci.RetryAfter) {
		return false
	}
	// A confirmed-permanent failure is parked for good (GH #1145). This sits
	// ABOVE the state switch on purpose: ResetForReconnect leaves a mid-attempt
	// client in StateDisconnected, which the default arm answers "true" — so a
	// gate inside the StateError arm would leak on every teardown.
	if ci.Terminal && ci.RetryCount >= PermanentFailureAttempts {
		return false
	}
	switch ci.State {
	case StatePendingAuth:
		return false
	case StateError:
		// OAuth-classified failures are paced by SetOAuthError's coarse ladder,
		// which bumps OAuthRetryCount and NOT RetryCount. Without this branch such
		// a server reads as RetryCount==0 ("no failures yet") and is re-dialed on
		// every 30s reconcile tick forever — exactly the storm this gate exists to
		// stop, for the error class (#1013) that triggered it.
		if ci.IsOAuthError && !ci.oauthBackoffElapsed(now) {
			return false
		}
		if ci.GaveUp || ci.RetryCount >= MaxConnectionRetries {
			return now.Sub(ci.LastRetryTime) >= GaveUpProbeBackoff(ci.RetryCount)
		}
		if ci.RetryCount == 0 {
			return true
		}
		return now.Sub(ci.LastRetryTime) >= RetryBackoffDuration(ci.RetryCount)
	default:
		return true
	}
}

// oauthBackoffElapsed reports whether the OAuth ladder allows another attempt.
func (ci *ConnectionInfo) oauthBackoffElapsed(now time.Time) bool {
	if ci.OAuthRetryCount == 0 {
		return true
	}
	return now.Sub(ci.LastOAuthAttempt) >= OAuthRetryBackoffDuration(ci.OAuthRetryCount)
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
	// retryAfter is the upstream-supplied park deadline from a rate-limited
	// response (#1040), set by SetRetryAfter and cleared on a successful
	// connection or an explicit Reset.
	retryAfter time.Time
	// terminal / terminalCode record a failure the diagnostics classifier proved
	// deterministic (GH #1145). Set only by SetTerminalError; cleared by
	// SetError, ClearTerminal, Reset and a successful connection.
	terminal     bool
	terminalCode string

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
		RetryAfter:       sm.retryAfter,
		Terminal:         sm.terminal,
		TerminalCode:     sm.terminalCode,
		GaveUp:           sm.retryCount >= MaxConnectionRetries,
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
		sm.retryAfter = time.Time{}
		// The problem is demonstrably fixed — un-park.
		sm.terminal = false
		sm.terminalCode = ""
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
		RetryAfter:       sm.retryAfter,
		Terminal:         sm.terminal,
		TerminalCode:     sm.terminalCode,
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
	// An unclassified (or differently classified) failure is NOT evidence of
	// permanence, so it revokes any earlier park rather than inheriting it. This
	// is the safe default for the health-probe and call-path callers of
	// SetError, none of which observe the connect attempt that proved the fault.
	sm.terminal = false
	sm.terminalCode = ""

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
		RetryAfter:       sm.retryAfter,
		Terminal:         sm.terminal,
		TerminalCode:     sm.terminalCode,
	}

	callback := sm.onStateChange

	// Call the callback outside the lock to avoid deadlocks
	if callback != nil {
		go callback(oldState, StateError, &info)
	}
}

// SetTerminalError records a connection failure that the diagnostics classifier
// proved deterministic and unrecoverable — a missing binary, an image without
// the interpreter, an unparseable config. Retrying it cannot succeed until a
// human changes something, so after PermanentFailureAttempts confirmations both
// retry gates stop dialing and the server is surfaced as terminally failed.
//
// It is otherwise exactly SetError: the state, error, counter and timestamp all
// advance identically, so every existing consumer keeps working. `code` is the
// stable MCPX_* string that justified the park, carried through to the API so
// the failure is visible with its reason rather than silently swallowed.
//
// Call this ONLY from the connect path, and only for a code that
// diagnostics.IsPermanent accepts. Plain SetError deliberately clears the park.
func (sm *StateManager) SetTerminalError(err error, code string) {
	sm.mu.Lock()

	oldState := sm.currentState
	sm.currentState = StateError
	sm.lastError = err
	sm.retryCount++
	sm.lastRetryTime = time.Now()
	sm.terminal = true
	sm.terminalCode = code

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
		RetryAfter:       sm.retryAfter,
		GaveUp:           sm.retryCount >= MaxConnectionRetries,
		Terminal:         sm.terminal,
		TerminalCode:     sm.terminalCode,
	}

	callback := sm.onStateChange
	sm.mu.Unlock()

	// Dispatched in a goroutine, matching SetError — the callback chain reaches
	// notification handlers that must not run under the state lock.
	if callback != nil {
		go callback(oldState, StateError, &info)
	}
}

// ClearTerminal un-parks a terminally failed connection without otherwise
// touching the retry ladder. It exists for the "the user changed something"
// paths that keep the existing client instead of recreating it.
func (sm *StateManager) ClearTerminal() {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.terminal = false
	sm.terminalCode = ""
}

// IsTerminal reports whether the connection is parked on a classifier-proven
// permanent failure, and the code that proved it.
func (sm *StateManager) IsTerminal() (bool, string) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.terminal, sm.terminalCode
}

// SetPendingAuth parks the connection in StatePendingAuth with the deferred-OAuth
// error attached. It exists because the obvious spelling — TransitionTo(StatePendingAuth)
// followed by SetError(err) — silently undoes itself: SetError forces StateError
// unconditionally, so the PendingAuth state survived microseconds and every consumer
// (health, notifications, the supervisor's reconnect gate) saw a plain error and
// kept redialing a server that cannot connect until a human logs in (#1013).
//
// RetryCount is deliberately NOT bumped: a parked server is not retrying, so there
// is no ladder to advance and nothing to "give up" on. The wake paths are explicit
// user action (login, manual reconnect, reconnect-on-use) and the persisted-token
// scan in Manager.scanForNewTokens.
func (sm *StateManager) SetPendingAuth(err error) {
	sm.mu.Lock()

	oldState := sm.currentState
	sm.currentState = StatePendingAuth
	sm.lastError = err
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
		RetryAfter:       sm.retryAfter,
		Terminal:         sm.terminal,
		TerminalCode:     sm.terminalCode,
		GaveUp:           sm.retryCount >= MaxConnectionRetries,
	}

	callback := sm.onStateChange
	sm.mu.Unlock()

	// Dispatched synchronously, like TransitionTo (which delivered this very
	// state before) and unlike SetError: a detached goroutine can be scheduled
	// after a subsequent Connecting/Ready callback, which would surface a stale
	// "sign in required" prompt right after a successful login. The registered
	// consumers only log and hand off to notification handlers that are
	// themselves goroutine-dispatched, and they never take the managed client's
	// mutex — the same reason TransitionTo can already run under it.
	if callback != nil {
		callback(oldState, StatePendingAuth, &info)
	}
}

// SetRetryAfter records an upstream-supplied park deadline captured from a
// `Retry-After` header on a rate-limited (429 / 503) response (#1040).
//
// It only ever EXTENDS the window: a later, shorter hint must not shorten a wait
// the upstream already asked for, and an already-elapsed deadline is dropped
// rather than stored. Call it BEFORE SetError/SetOAuthError so the resulting
// ConnectionInfo the state-change callback publishes already carries the window.
func (sm *StateManager) SetRetryAfter(deadline time.Time) {
	if deadline.IsZero() || !deadline.After(time.Now()) {
		return
	}
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if deadline.After(sm.retryAfter) {
		sm.retryAfter = deadline
	}
}

// RetryAfter returns the current park deadline, or the zero time when the
// upstream has given no rate-limit hint.
func (sm *StateManager) RetryAfter() time.Time {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.retryAfter
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

	// Honour an upstream-supplied Retry-After window (#1040) — the same floor
	// ConnectionInfo.ShouldAutoReconnect applies for the supervisor.
	if !sm.retryAfter.IsZero() && time.Now().Before(sm.retryAfter) {
		return false
	}

	if sm.currentState != StateError {
		return false
	}

	// A classifier-proven permanent failure stops once its confirmation attempt
	// is spent — the same policy ConnectionInfo.ShouldAutoReconnect applies for
	// the supervisor (GH #1145).
	if sm.terminal && sm.retryCount >= PermanentFailureAttempts {
		return false
	}

	// Stop retrying after max consecutive failures
	if sm.retryCount >= MaxConnectionRetries {
		return false
	}

	if sm.retryCount == 0 {
		return true
	}

	return time.Since(sm.lastRetryTime) >= RetryBackoffDuration(sm.retryCount)
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
		StateConnecting:     {StateAuthenticating, StateDiscovering, StateReady, StateError, StateDisconnected, StatePendingAuth}, // Allow direct to Ready for OAuth flows
		StateAuthenticating: {StateConnecting, StateDiscovering, StateReady, StateError, StateDisconnected, StatePendingAuth},
		StateDiscovering:    {StateReady, StateError, StateDisconnected},
		StateReady:          {StateError, StateDisconnected},
		StateError:          {StateConnecting, StateDisconnected},
		// A parked server leaves PendingAuth when the user logs in / reconnects
		// (StateConnecting), is disabled (StateDisconnected), or a later attempt
		// fails for a non-auth reason (StateError).
		StatePendingAuth: {StateConnecting, StateDisconnected, StateError},
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
	sm.retryAfter = time.Time{}
	// Reset is the explicit "start over" path — manual reconnect (ForceReconnect)
	// and Disconnect both route through it. It MUST un-park a terminal server, or
	// a user who fixed the config would find the Restart button inert (GH #1145).
	sm.terminal = false
	sm.terminalCode = ""

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
		RetryAfter:       sm.retryAfter,
		Terminal:         sm.terminal,
		TerminalCode:     sm.terminalCode,
	}

	callback := sm.onStateChange

	// Call the callback outside the lock to avoid deadlocks
	if callback != nil {
		go callback(oldState, StateDisconnected, &info)
	}
}

// ResetForReconnect transitions to Disconnected state for a reconnection attempt
// while PRESERVING retryCount and lastRetryTime so exponential backoff is not defeated.
// Use this instead of Reset() when retrying a failed connection.
func (sm *StateManager) ResetForReconnect() {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	oldState := sm.currentState
	sm.currentState = StateDisconnected
	sm.lastError = nil
	// Preserve: retryCount, lastRetryTime — these drive exponential backoff
	sm.serverName = ""
	sm.serverVersion = ""

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
		RetryAfter:       sm.retryAfter,
		Terminal:         sm.terminal,
		TerminalCode:     sm.terminalCode,
	}

	callback := sm.onStateChange

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
		RetryAfter:       sm.retryAfter,
		Terminal:         sm.terminal,
		TerminalCode:     sm.terminalCode,
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

	// A rate-limit window outranks the OAuth ladder too (#1040): an upstream
	// that answered 429 will answer 429 to the token exchange as well, and the
	// OAuth ladder can be shorter than the window the upstream asked for.
	if !sm.retryAfter.IsZero() && time.Now().Before(sm.retryAfter) {
		return false
	}

	if !sm.isOAuthError || sm.currentState != StateError {
		return false
	}

	if sm.oauthRetryCount == 0 {
		return true
	}

	// OAuth has much longer backoff intervals: 5min, 15min, 1h, 4h, 24h
	return time.Since(sm.lastOAuthAttempt) >= OAuthRetryBackoffDuration(sm.oauthRetryCount)
}

// IsOAuthError returns true if the last error was OAuth-related
func (sm *StateManager) IsOAuthError() bool {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.isOAuthError
}
