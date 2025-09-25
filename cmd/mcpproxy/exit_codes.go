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

// exitCodeDescription returns a human-readable description of the exit code
func exitCodeDescription(code int) string {
	switch code {
	case ExitCodeSuccess:
		return "Success"
	case ExitCodeGeneralError:
		return "General error"
	case ExitCodePortConflict:
		return "Port conflict - address already in use"
	case ExitCodeDBLocked:
		return "Database locked by another process"
	case ExitCodeConfigError:
		return "Configuration error"
	case ExitCodePermissionError:
		return "Permission denied"
	default:
		return "Unknown error"
	}
}
