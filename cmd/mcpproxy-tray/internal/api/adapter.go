package api

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	internalRuntime "mcpproxy-go/internal/runtime"
)

// ServerAdapter adapts the API client to the ServerInterface expected by the tray
type ServerAdapter struct {
	client *Client
}

// NewServerAdapter creates a new server adapter
func NewServerAdapter(client *Client) *ServerAdapter {
	return &ServerAdapter{
		client: client,
	}
}

// IsRunning checks if the server is running via API
func (a *ServerAdapter) IsRunning() bool {
	if _, err := a.client.GetServers(); err != nil {
		return false
	}

	// If we can fetch servers, the API is responsive regardless of count
	return true
}

// GetListenAddress returns the listen address from the core's /api/v1/info endpoint
func (a *ServerAdapter) GetListenAddress() string {
	// Get the actual listen address from the core
	info, err := a.client.GetInfo()
	if err == nil && info != nil {
		if data, ok := info["data"].(map[string]interface{}); ok {
			if addr, ok := data["listen_addr"].(string); ok {
				return addr
			}
		}
	}
	// Return empty string if we couldn't get the address (don't return hardcoded value)
	return ""
}

// GetUpstreamStats returns upstream server statistics
func (a *ServerAdapter) GetUpstreamStats() map[string]interface{} {
	servers, err := a.client.GetServers()
	if err != nil {
		return map[string]interface{}{
			"connected_servers": 0,
			"total_servers":     0,
			"total_tools":       0,
		}
	}

	connectedCount := 0
	totalTools := 0
	for _, server := range servers {
		if server.Connected {
			connectedCount++
		}
		totalTools += server.ToolCount
	}

	return map[string]interface{}{
		"connected_servers": connectedCount,
		"total_servers":     len(servers),
		"total_tools":       totalTools,
	}
}

// StartServer is not supported via API (server is already running)
func (a *ServerAdapter) StartServer(_ context.Context) error {
	return fmt.Errorf("StartServer not supported via API - server is already running")
}

// StopServer is not supported via API (would break tray communication)
func (a *ServerAdapter) StopServer() error {
	return fmt.Errorf("StopServer not supported via API - would break tray communication")
}

// GetStatus returns the current server status
func (a *ServerAdapter) GetStatus() interface{} {
	// Get the actual listen address from the core's /api/v1/info endpoint
	info, err := a.client.GetInfo()
	listenAddr := ""
	if err == nil && info != nil {
		if data, ok := info["data"].(map[string]interface{}); ok {
			if addr, ok := data["listen_addr"].(string); ok {
				listenAddr = addr
			}
		}
	}

	// Fallback to empty if we couldn't get it
	if listenAddr == "" {
		listenAddr = ""  // Empty means tray will show "Status: Running" without address
	}

	servers, serverErr := a.client.GetServers()
	if serverErr != nil {
		return map[string]interface{}{
			"phase":       "Error",
			"message":     fmt.Sprintf("API error: %v", serverErr),
			"running":     false,
			"listen_addr": listenAddr,
		}
	}

	connectedCount := 0
	for _, server := range servers {
		if server.Connected {
			connectedCount++
		}
	}

	return map[string]interface{}{
		"phase":             "Running",
		"message":           fmt.Sprintf("API connected - %d servers", len(servers)),
		"running":           true,
		"listen_addr":       listenAddr,
		"connected_servers": connectedCount,
		"total_servers":     len(servers),
	}
}

// StatusChannel returns the channel for status updates from SSE
func (a *ServerAdapter) StatusChannel() <-chan interface{} {
	// Convert the typed channel to interface{} channel
	ch := make(chan interface{}, 10)

	go func() {
		defer close(ch)
		for update := range a.client.StatusChannel() {
			// Convert StatusUpdate to the format expected by tray
			status := map[string]interface{}{
				"phase":          "Running",
				"message":        "Connected via API",
				"running":        update.Running,
				"listen_addr":    update.ListenAddr,
				"upstream_stats": update.UpstreamStats,
				"timestamp":      update.Timestamp,
			}

			select {
			case ch <- status:
			default:
				// Channel full, skip this update
			}
		}
	}()

	return ch
}

// EventsChannel returns nil as the remote API does not yet proxy runtime events.
func (a *ServerAdapter) EventsChannel() <-chan internalRuntime.Event {
	return nil
}

// GetQuarantinedServers returns quarantined servers
func (a *ServerAdapter) GetQuarantinedServers() ([]map[string]interface{}, error) {
	servers, err := a.client.GetServers()
	if err != nil {
		return nil, err
	}

	var quarantined []map[string]interface{}
	for _, server := range servers {
		if server.Quarantined {
			quarantined = append(quarantined, map[string]interface{}{
				"name":        server.Name,
				"url":         server.URL,
				"command":     server.Command,
				"protocol":    server.Protocol,
				"enabled":     server.Enabled,
				"quarantined": server.Quarantined,
			})
		}
	}

	return quarantined, nil
}

// UnquarantineServer removes a server from quarantine
func (a *ServerAdapter) UnquarantineServer(serverName string) error {
	// This functionality is not available in the current API
	// Would need to be added to the API first
	return fmt.Errorf("UnquarantineServer not yet supported via API for %s", serverName)
}

// EnableServer enables or disables a server
func (a *ServerAdapter) EnableServer(serverName string, enabled bool) error {
	return a.client.EnableServer(serverName, enabled)
}

// QuarantineServer sets quarantine status for a server
func (a *ServerAdapter) QuarantineServer(serverName string, quarantined bool) error {
	// This functionality is not available in the current API
	// Would need to be added to the API first
	return fmt.Errorf("QuarantineServer not yet supported via API for %s (quarantined=%t)", serverName, quarantined)
}

// GetAllServers returns all servers
func (a *ServerAdapter) GetAllServers() ([]map[string]interface{}, error) {
	servers, err := a.client.GetServers()
	if err != nil {
		return nil, err
	}

	var result []map[string]interface{}
	for _, server := range servers {
		result = append(result, map[string]interface{}{
			"name":            server.Name,
			"url":             server.URL,
			"command":         server.Command,
			"protocol":        server.Protocol,
			"enabled":         server.Enabled,
			"quarantined":     server.Quarantined,
			"connected":       server.Connected,
			"connecting":      server.Connecting,
			"tool_count":      server.ToolCount,
			"last_error":      server.LastError,
			"status":          server.Status,
			"should_retry":    server.ShouldRetry,
			"retry_count":     server.RetryCount,
			"last_retry_time": server.LastRetry,
		})
	}

	return result, nil
}

// SetListenAddress is not supported via API control surfaces.
func (a *ServerAdapter) SetListenAddress(_ string, _ bool) error {
	return fmt.Errorf("SetListenAddress not supported via API")
}

// SuggestAlternateListen cannot operate through the remote API adapter.
func (a *ServerAdapter) SuggestAlternateListen(baseAddr string) (string, error) {
	return baseAddr, fmt.Errorf("SuggestAlternateListen not supported via API")
}

// ReloadConfiguration reloads the configuration
func (a *ServerAdapter) ReloadConfiguration() error {
	// This functionality is not available in the current API
	// Would need to be added to the API first
	return fmt.Errorf("ReloadConfiguration not yet supported via API")
}

// GetConfigPath returns the configuration file path
func (a *ServerAdapter) GetConfigPath() string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "~/.mcpproxy/mcp_config.json" // fallback
	}
	return filepath.Join(homeDir, ".mcpproxy", "mcp_config.json")
}

// GetLogDir returns the log directory path
func (a *ServerAdapter) GetLogDir() string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "~/.mcpproxy/logs" // fallback
	}

	// Use platform-specific log directory (same logic as mcpproxy-tray/main.go)
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(homeDir, "Library", "Logs", "mcpproxy")
	case "windows":
		if localAppData := os.Getenv("LOCALAPPDATA"); localAppData != "" {
			return filepath.Join(localAppData, "mcpproxy", "logs")
		}
		if userProfile := os.Getenv("USERPROFILE"); userProfile != "" {
			return filepath.Join(userProfile, "AppData", "Local", "mcpproxy", "logs")
		}
		return filepath.Join(homeDir, ".mcpproxy", "logs")
	default: // linux and others
		return filepath.Join(homeDir, ".mcpproxy", "logs")
	}
}

// TriggerOAuthLogin triggers OAuth login for a server
func (a *ServerAdapter) TriggerOAuthLogin(serverName string) error {
	return a.client.TriggerOAuthLogin(serverName)
}
