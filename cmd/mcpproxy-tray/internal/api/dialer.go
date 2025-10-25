//go:build darwin || windows

package api

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// CreateDialer creates a custom dialer for the given endpoint
// Supports unix://, npipe://, http://, and https:// schemes
func CreateDialer(endpoint string) (func(context.Context, string, string) (net.Conn, error), string, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return nil, "", fmt.Errorf("invalid endpoint URL: %w", err)
	}

	switch parsed.Scheme {
	case "unix":
		// Unix domain socket (macOS/Linux)
		if runtime.GOOS == "windows" {
			return nil, "", fmt.Errorf("unix domain sockets not supported on Windows")
		}

		socketPath := parsed.Path
		if socketPath == "" {
			socketPath = parsed.Opaque // Handle unix:///path (three slashes)
		}

		dialer := func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socketPath)
		}

		// Return socket path as the base URL for HTTP requests
		return dialer, "http://localhost", nil

	case "npipe":
		// Windows named pipe
		if runtime.GOOS != "windows" {
			return nil, "", fmt.Errorf("named pipes only supported on Windows")
		}

		// Named pipe path is in the form npipe:////./pipe/mcpproxy
		pipePath := parsed.Opaque
		if pipePath == "" {
			pipePath = parsed.Path
		}

		// Remove leading slashes (npipe:////./pipe/name → //./pipe/name)
		pipePath = strings.TrimPrefix(pipePath, "//")

		dialer := func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialNamedPipe(ctx, pipePath)
		}

		// Return dummy URL for HTTP requests
		return dialer, "http://localhost", nil

	case "http", "https":
		// Standard TCP connection - use default dialer
		return nil, endpoint, nil

	default:
		return nil, "", fmt.Errorf("unsupported endpoint scheme: %s (expected unix://, npipe://, http://, or https://)", parsed.Scheme)
	}
}

// DetectSocketPath attempts to detect the socket path from the environment
// Priority: ENV > config file > data dir default
func DetectSocketPath(dataDir string) string {
	// 1. Check environment variable
	if envEndpoint := os.Getenv("MCPPROXY_TRAY_ENDPOINT"); envEndpoint != "" {
		return envEndpoint
	}

	// 2. Try to read from config file
	if dataDir == "" {
		dataDir = getDefaultDataDir()
	}
	configPath := filepath.Join(dataDir, "mcp_config.json")
	if trayEndpoint := readTrayEndpointFromConfig(configPath); trayEndpoint != "" {
		return trayEndpoint
	}

	// 3. Default socket path based on platform and data dir
	return getDefaultSocketPath(dataDir)
}

// getDefaultSocketPath returns the default socket path for the platform
func getDefaultSocketPath(dataDir string) string {
	if dataDir == "" {
		dataDir = getDefaultDataDir()
	}

	// Expand ~ if present
	if strings.HasPrefix(dataDir, "~/") {
		home, _ := os.UserHomeDir()
		dataDir = filepath.Join(home, dataDir[2:])
	}

	if runtime.GOOS == "windows" {
		// Windows: Named pipe with username
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

	// Unix: Socket in data directory
	socketPath := filepath.Join(dataDir, "mcpproxy.sock")
	return fmt.Sprintf("unix://%s", socketPath)
}

// getDefaultDataDir returns the default data directory
func getDefaultDataDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".mcpproxy")
}

// readTrayEndpointFromConfig reads tray_endpoint from config file
func readTrayEndpointFromConfig(configPath string) string {
	// This is a simplified implementation
	// In production, we'd properly parse the JSON config file
	// For now, return empty string to use default detection
	return ""
}
