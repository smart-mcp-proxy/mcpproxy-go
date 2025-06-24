package logs

import (
	"os"
	"path/filepath"
	"runtime"
)

// GetLogDir returns the standard log directory for the current OS
func GetLogDir() (string, error) {
	switch runtime.GOOS {
	case "windows":
		return getWindowsLogDir()
	case "darwin":
		return getMacOSLogDir()
	case "linux":
		return getLinuxLogDir()
	default:
		// Fallback to home directory for unsupported OS
		return getDefaultLogDir()
	}
}

// getWindowsLogDir returns Windows standard log directory
// Uses %LOCALAPPDATA%\mcpproxy\logs
func getWindowsLogDir() (string, error) {
	localAppData := os.Getenv("LOCALAPPDATA")
	if localAppData == "" {
		// Fallback to %USERPROFILE%\AppData\Local
		userProfile := os.Getenv("USERPROFILE")
		if userProfile == "" {
			return getDefaultLogDir()
		}
		localAppData = filepath.Join(userProfile, "AppData", "Local")
	}
	return filepath.Join(localAppData, "mcpproxy", "logs"), nil
}

// getMacOSLogDir returns macOS standard log directory
// Uses ~/Library/Logs/mcpproxy
func getMacOSLogDir() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return getDefaultLogDir()
	}
	return filepath.Join(homeDir, "Library", "Logs", "mcpproxy"), nil
}

// getLinuxLogDir returns Linux standard log directory
// Uses ~/.local/var/log/mcpproxy or /var/log/mcpproxy if running as root
func getLinuxLogDir() (string, error) {
	// Check if running as root
	if os.Getuid() == 0 {
		return "/var/log/mcpproxy", nil
	}

	// For regular users, use XDG Base Directory Specification
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return getDefaultLogDir()
	}

	// Use XDG_STATE_HOME if set, otherwise use ~/.local/state
	stateDir := os.Getenv("XDG_STATE_HOME")
	if stateDir == "" {
		stateDir = filepath.Join(homeDir, ".local", "state")
	}

	return filepath.Join(stateDir, "mcpproxy", "logs"), nil
}

// getDefaultLogDir returns a fallback log directory
func getDefaultLogDir() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		// Last resort fallback to temp directory
		return filepath.Join(os.TempDir(), "mcpproxy", "logs"), nil
	}
	return filepath.Join(homeDir, ".mcpproxy", "logs"), nil
}

// EnsureLogDir creates the log directory if it doesn't exist
func EnsureLogDir(logDir string) error {
	return os.MkdirAll(logDir, 0755)
}

// GetLogFilePath returns the full path for a log file in the standard log directory
func GetLogFilePath(filename string) (string, error) {
	logDir, err := GetLogDir()
	if err != nil {
		return "", err
	}

	if err := EnsureLogDir(logDir); err != nil {
		return "", err
	}

	return filepath.Join(logDir, filename), nil
}

// LogDirInfo returns information about the log directory for different OS
type LogDirInfo struct {
	Path        string `json:"path"`
	OS          string `json:"os"`
	Description string `json:"description"`
	Standard    string `json:"standard"`
}

// GetLogDirInfo returns detailed information about the log directory
func GetLogDirInfo() (*LogDirInfo, error) {
	logDir, err := GetLogDir()
	if err != nil {
		return nil, err
	}

	info := &LogDirInfo{
		Path: logDir,
		OS:   runtime.GOOS,
	}

	switch runtime.GOOS {
	case "windows":
		info.Description = "Windows Local AppData logs directory"
		info.Standard = "Windows Application Data Guidelines"
	case "darwin":
		info.Description = "macOS Library Logs directory"
		info.Standard = "macOS File System Programming Guide"
	case "linux":
		info.Description = "Linux XDG state directory or system logs"
		info.Standard = "XDG Base Directory Specification"
	default:
		info.Description = "Fallback logs directory"
		info.Standard = "Default behavior"
	}

	return info, nil
}
