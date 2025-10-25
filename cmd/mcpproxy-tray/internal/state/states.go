//go:build darwin || windows

package state

import (
	"time"
)

// State represents the current state of the tray application
type State string

const (
	// StateInitializing represents the initial startup state
	StateInitializing State = "initializing"

	// StateLaunchingCore represents launching the core subprocess
	StateLaunchingCore State = "launching_core"

	// StateWaitingForCore represents waiting for core to become ready
	StateWaitingForCore State = "waiting_for_core"

	// StateConnectingAPI represents establishing API/SSE connection
	StateConnectingAPI State = "connecting_api"

	// StateConnected represents fully connected and operational
	StateConnected State = "connected"

	// StateReconnecting represents attempting to reconnect after disconnection
	StateReconnecting State = "reconnecting"

	// StateCoreErrorPortConflict represents core failed due to port conflict
	StateCoreErrorPortConflict State = "core_error_port_conflict"

	// StateCoreErrorDBLocked represents core failed due to database lock
	StateCoreErrorDBLocked State = "core_error_db_locked"

	// StateCoreErrorConfig represents core failed due to configuration error
	StateCoreErrorConfig State = "core_error_config"

	// StateCoreErrorPermission represents core failed due to permission error
	StateCoreErrorPermission State = "core_error_permission"

	// StateCoreErrorGeneral represents core failed with general error
	StateCoreErrorGeneral State = "core_error_general"

	// StateShuttingDown represents clean shutdown in progress
	StateShuttingDown State = "shutting_down"

	// StateFailed represents unrecoverable failure
	StateFailed State = "failed"
)

// Event represents events that can trigger state transitions
type Event string

const (
	// EventStart triggers initial startup
	EventStart Event = "start"

	// EventCoreStarted indicates core subprocess started successfully
	EventCoreStarted Event = "core_started"

	// EventCoreReady indicates core is ready to serve requests
	EventCoreReady Event = "core_ready"

	// EventAPIConnected indicates successful API/SSE connection
	EventAPIConnected Event = "api_connected"

	// EventConnectionLost indicates lost connection to core
	EventConnectionLost Event = "connection_lost"

	// EventCoreExited indicates core subprocess exited
	EventCoreExited Event = "core_exited"

	// EventPortConflict indicates core failed due to port conflict
	EventPortConflict Event = "port_conflict"

	// EventDBLocked indicates core failed due to database lock
	EventDBLocked Event = "db_locked"

	// EventConfigError indicates core failed due to configuration error
	EventConfigError Event = "config_error"

	// EventPermissionError indicates core failed due to permission error
	EventPermissionError Event = "permission_error"

	// EventGeneralError indicates core failed with general error
	EventGeneralError Event = "general_error"

	// EventRetry triggers retry attempt
	EventRetry Event = "retry"

	// EventShutdown triggers shutdown
	EventShutdown Event = "shutdown"

	// EventTimeout indicates a timeout occurred
	EventTimeout Event = "timeout"

	// EventSkipCore indicates core launch should be skipped
	EventSkipCore Event = "skip_core"
)

// Info provides metadata about each state
type Info struct {
	Name        State
	Description string
	IsError     bool
	CanRetry    bool
	UserMessage string
	Timeout     *time.Duration
}

// GetInfo returns metadata for a given state
func GetInfo(state State) Info {
	timeout90s := 90 * time.Second  // Must exceed health monitor's readinessTimeout (60s)
	timeout5s := 5 * time.Second
	timeout10s := 10 * time.Second

	stateInfoMap := map[State]Info{
		StateInitializing: {
			Name:        StateInitializing,
			Description: "Initializing tray application",
			UserMessage: "Starting up...",
			Timeout:     &timeout5s,
		},
		StateLaunchingCore: {
			Name:        StateLaunchingCore,
			Description: "Launching mcpproxy core process",
			UserMessage: "Starting mcpproxy core...",
			Timeout:     &timeout10s,
		},
		StateWaitingForCore: {
			Name:        StateWaitingForCore,
			Description: "Waiting for core to become ready",
			UserMessage: "Core starting up...",
			Timeout:     &timeout90s,  // Increased to 90s to allow Docker isolation startup (health timeout is 60s)
		},
		StateConnectingAPI: {
			Name:        StateConnectingAPI,
			Description: "Establishing API connection",
			UserMessage: "Connecting to core...",
			Timeout:     &timeout10s,
		},
		StateConnected: {
			Name:        StateConnected,
			Description: "Fully connected and operational",
			UserMessage: "Connected and ready",
		},
		StateReconnecting: {
			Name:        StateReconnecting,
			Description: "Attempting to reconnect",
			UserMessage: "Reconnecting...",
			CanRetry:    true,
		},
		StateCoreErrorPortConflict: {
			Name:        StateCoreErrorPortConflict,
			Description: "Core failed due to port conflict",
			UserMessage: "Port already in use - kill other instance or change port",
			IsError:     true,
			CanRetry:    false,
			// No timeout - port conflicts persist until user fixes manually
		},
		StateCoreErrorDBLocked: {
			Name:        StateCoreErrorDBLocked,
			Description: "Core failed due to database lock",
			UserMessage: "Database locked - kill other mcpproxy instance",
			IsError:     true,
			CanRetry:    false,
			// No timeout - DB locks persist until user fixes manually
		},
		StateCoreErrorConfig: {
			Name:        StateCoreErrorConfig,
			Description: "Core failed due to configuration error",
			UserMessage: "Configuration error - check config file",
			IsError:     true,
			CanRetry:    false,
			// No timeout - config errors persist until user fixes the config
		},
		StateCoreErrorPermission: {
			Name:        StateCoreErrorPermission,
			Description: "Core failed due to permission error",
			UserMessage: "Permission error - data directory must have 0700 permissions (chmod 0700 ~/.mcpproxy)",
			IsError:     true,
			CanRetry:    false,
			// No timeout - permission errors persist until user fixes permissions
		},
		StateCoreErrorGeneral: {
			Name:        StateCoreErrorGeneral,
			Description: "Core failed with general error",
			UserMessage: "Core startup failed - check logs",
			IsError:     true,
			CanRetry:    false,
			// No timeout - general errors persist until user investigates
		},
		StateShuttingDown: {
			Name:        StateShuttingDown,
			Description: "Shutting down gracefully",
			UserMessage: "Shutting down...",
		},
		StateFailed: {
			Name:        StateFailed,
			Description: "Unrecoverable failure",
			UserMessage: "Failed to start",
			IsError:     true,
		},
	}

	if info, exists := stateInfoMap[state]; exists {
		return info
	}

	// Default for unknown states
	return Info{
		Name:        state,
		Description: string(state),
		UserMessage: string(state),
	}
}

// CanTransition checks if a transition from one state to another is valid
func CanTransition(from, to State) bool {
	validTransitions := map[State][]State{
		StateInitializing: {
			StateLaunchingCore,
			StateConnectingAPI, // Skip core launch
			StateShuttingDown,
		},
		StateLaunchingCore: {
			StateWaitingForCore,
			StateCoreErrorPortConflict,
			StateCoreErrorDBLocked,
			StateCoreErrorConfig,
			StateCoreErrorGeneral,
			StateShuttingDown,
		},
		StateWaitingForCore: {
			StateConnectingAPI,
			StateCoreErrorPortConflict, // ADD: Handle port conflict
			StateCoreErrorDBLocked,     // ADD: Handle DB lock
			StateCoreErrorConfig,       // ADD: Handle config error
			StateCoreErrorGeneral,
			StateLaunchingCore, // Retry
			StateShuttingDown,
		},
		StateConnectingAPI: {
			StateConnected,
			StateReconnecting,
			StateCoreErrorPortConflict, // ADD: Handle port conflict during connection
			StateCoreErrorDBLocked,     // ADD: Handle DB lock during connection
			StateCoreErrorConfig,       // ADD: Handle config error during connection
			StateCoreErrorGeneral,
			StateShuttingDown,
		},
		StateConnected: {
			StateReconnecting,
			StateShuttingDown,
		},
		StateReconnecting: {
			StateConnected,
			StateLaunchingCore, // Core died, restart
			StateFailed,
			StateShuttingDown,
		},
		StateCoreErrorPortConflict: {
			// Error persists - only shutdown allowed
			StateShuttingDown,
		},
		StateCoreErrorDBLocked: {
			// Error persists - only shutdown allowed
			StateShuttingDown,
		},
		StateCoreErrorConfig: {
			// Error persists - only shutdown allowed
			StateShuttingDown,
		},
		StateCoreErrorGeneral: {
			// Error persists - only shutdown allowed
			StateShuttingDown,
		},
		StateFailed: {
			StateShuttingDown,
		},
		StateShuttingDown: {
			// Terminal state - no transitions out
		},
	}

	if allowedStates, exists := validTransitions[from]; exists {
		for _, allowedState := range allowedStates {
			if allowedState == to {
				return true
			}
		}
	}

	return false
}
