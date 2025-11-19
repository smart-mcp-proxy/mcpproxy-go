package socket

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// DetectSocketPath detects the socket/pipe endpoint for daemon communication.
// Priority: MCPPROXY_TRAY_ENDPOINT env → config file → default path
func DetectSocketPath(dataDir string) string {
	// 1. Check environment variable
	if envEndpoint := os.Getenv("MCPPROXY_TRAY_ENDPOINT"); envEndpoint != "" {
		return envEndpoint
	}

	// 2. Use default path based on data directory
	return GetDefaultSocketPath(dataDir)
}

// GetDefaultSocketPath returns the default socket/pipe path for the platform.
func GetDefaultSocketPath(dataDir string) string {
	if dataDir == "" {
		dataDir = getDefaultDataDir()
	}

	// Expand ~ if present
	if strings.HasPrefix(dataDir, "~/") {
		home, _ := os.UserHomeDir()
		dataDir = filepath.Join(home, dataDir[2:])
	}

	if runtime.GOOS == "windows" {
		return getWindowsPipePath(dataDir)
	}

	// Unix: Socket in data directory
	socketPath := filepath.Join(dataDir, "mcpproxy.sock")
	return fmt.Sprintf("unix://%s", socketPath)
}

// getWindowsPipePath returns Windows named pipe path with username.
func getWindowsPipePath(dataDir string) string {
	username := os.Getenv("USERNAME")
	if username == "" {
		username = "default"
	}

	// Check if using default data dir
	defaultDataDir := getDefaultDataDir()
	if dataDir == defaultDataDir {
		// Simple pipe name for default location
		return fmt.Sprintf("npipe:////./pipe/mcpproxy-%s", username)
	}

	// Custom data dir: add hash for uniqueness
	hash := sha256.Sum256([]byte(dataDir))
	hashStr := fmt.Sprintf("%x", hash[:4])
	return fmt.Sprintf("npipe:////./pipe/mcpproxy-%s-%s", username, hashStr)
}

// getDefaultDataDir returns the default data directory.
func getDefaultDataDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".mcpproxy")
}

// IsSocketAvailable checks if the socket/pipe exists and is accessible.
func IsSocketAvailable(endpoint string) bool {
	if strings.HasPrefix(endpoint, "unix://") {
		// Unix socket: check file exists
		path := strings.TrimPrefix(endpoint, "unix://")
		_, err := os.Stat(path)
		return err == nil
	}

	if strings.HasPrefix(endpoint, "npipe://") {
		// Windows named pipe: try to connect
		// For now, assume available if endpoint is well-formed
		// TODO: Implement actual pipe connection check
		return true
	}

	return false
}
