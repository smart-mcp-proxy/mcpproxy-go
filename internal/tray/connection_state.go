//go:build !nogui && !headless && !linux

package tray

// ConnectionState represents the current connectivity status between the tray and the core runtime.
type ConnectionState string

const (
	ConnectionStateInitializing ConnectionState = "initializing"
	ConnectionStateStartingCore ConnectionState = "starting_core"
	ConnectionStateConnecting   ConnectionState = "connecting"
	ConnectionStateConnected    ConnectionState = "connected"
	ConnectionStateReconnecting ConnectionState = "reconnecting"
	ConnectionStateDisconnected ConnectionState = "disconnected"
)
