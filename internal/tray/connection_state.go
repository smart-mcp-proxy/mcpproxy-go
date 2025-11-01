//go:build !nogui && !headless && !linux

package tray

// ConnectionState represents the current connectivity status between the tray and the core runtime.
type ConnectionState string

const (
	ConnectionStateInitializing      ConnectionState = "initializing"
	ConnectionStateStartingCore      ConnectionState = "starting_core"
	ConnectionStateConnecting        ConnectionState = "connecting"
	ConnectionStateConnected         ConnectionState = "connected"
	ConnectionStateReconnecting      ConnectionState = "reconnecting"
	ConnectionStateDisconnected      ConnectionState = "disconnected"
	ConnectionStateAuthError         ConnectionState = "auth_error"
	ConnectionStateErrorPortConflict ConnectionState = "error_port_conflict" // ADD: Specific error states
	ConnectionStateErrorDBLocked     ConnectionState = "error_db_locked"
	ConnectionStateErrorDocker       ConnectionState = "error_docker"
	ConnectionStateErrorConfig       ConnectionState = "error_config"
	ConnectionStateErrorGeneral      ConnectionState = "error_general"
	ConnectionStateFailed            ConnectionState = "failed"
)
