package main

// Exit codes for mcpproxy to enable specific error handling by the tray launcher

const (
	// ExitCodeSuccess indicates normal program termination
	ExitCodeSuccess = 0

	// ExitCodeGeneralError indicates a generic error (default)
	ExitCodeGeneralError = 1

	// ExitCodePortConflict indicates the listen port is already in use
	ExitCodePortConflict = 2

	// ExitCodeDBLocked indicates the database is locked by another process
	ExitCodeDBLocked = 3

	// ExitCodeConfigError indicates configuration validation failed
	ExitCodeConfigError = 4

	// ExitCodePermissionError indicates insufficient permissions (file access, port binding)
	ExitCodePermissionError = 5
)
