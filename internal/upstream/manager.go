package upstream

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"mcpproxy-go/internal/config"
)

// Manager manages connections to multiple upstream MCP servers
type Manager struct {
	clients map[string]*Client
	mu      sync.RWMutex
	logger  *zap.Logger
}

// NewManager creates a new upstream manager
func NewManager(logger *zap.Logger) *Manager {
	return &Manager{
		clients: make(map[string]*Client),
		logger:  logger,
	}
}

// AddServerConfig adds a server configuration without connecting
func (m *Manager) AddServerConfig(id string, serverConfig *config.ServerConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Remove existing client if it exists
	if existingClient, exists := m.clients[id]; exists {
		existingClient.Disconnect()
		delete(m.clients, id)
	}

	// Create new client but don't connect yet
	client, err := NewClient(id, serverConfig, m.logger)
	if err != nil {
		return fmt.Errorf("failed to create client for server %s: %w", serverConfig.Name, err)
	}

	m.clients[id] = client
	m.logger.Info("Added upstream server configuration",
		zap.String("id", id),
		zap.String("name", serverConfig.Name))

	return nil
}

// AddServer adds a new upstream server and connects to it (legacy method)
func (m *Manager) AddServer(id string, serverConfig *config.ServerConfig) error {
	if err := m.AddServerConfig(id, serverConfig); err != nil {
		return err
	}

	// Connect to server
	ctx := context.Background()
	if client, exists := m.GetClient(id); exists {
		if err := client.Connect(ctx); err != nil {
			return fmt.Errorf("failed to connect to server %s: %w", serverConfig.Name, err)
		}
	}

	return nil
}

// RemoveServer removes an upstream server
func (m *Manager) RemoveServer(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if client, exists := m.clients[id]; exists {
		client.Disconnect()
		delete(m.clients, id)
		m.logger.Info("Removed upstream server", zap.String("id", id))
	}
}

// GetClient returns a client by ID
func (m *Manager) GetClient(id string) (*Client, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	client, exists := m.clients[id]
	return client, exists
}

// GetAllClients returns all clients
func (m *Manager) GetAllClients() map[string]*Client {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make(map[string]*Client)
	for id, client := range m.clients {
		result[id] = client
	}
	return result
}

// DiscoverTools discovers all tools from all connected upstream servers
func (m *Manager) DiscoverTools(ctx context.Context) ([]*config.ToolMetadata, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var allTools []*config.ToolMetadata

	for id, client := range m.clients {
		if !client.IsConnected() {
			m.logger.Warn("Skipping disconnected client", zap.String("id", id))
			continue
		}

		tools, err := client.ListTools(ctx)
		if err != nil {
			m.logger.Error("Failed to list tools from client",
				zap.String("id", id),
				zap.Error(err))
			continue
		}

		if tools != nil {
			allTools = append(allTools, tools...)
		}
	}

	m.logger.Info("Discovered tools from upstream servers",
		zap.Int("total_tools", len(allTools)),
		zap.Int("connected_servers", len(m.clients)))

	return allTools, nil
}

// CallTool calls a tool on the appropriate upstream server
func (m *Manager) CallTool(ctx context.Context, toolName string, args map[string]interface{}) (interface{}, error) {
	// Parse tool name to extract server and tool components
	parts := strings.SplitN(toolName, ":", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid tool name format: %s (expected server:tool)", toolName)
	}

	serverName := parts[0]
	actualToolName := parts[1]

	m.mu.RLock()
	defer m.mu.RUnlock()

	// Find the client for this server
	var targetClient *Client
	for _, client := range m.clients {
		if client.config.Name == serverName {
			targetClient = client
			break
		}
	}

	if targetClient == nil {
		return nil, fmt.Errorf("no connected client found for server: %s", serverName)
	}

	// Check connection status and provide detailed error information
	connectionStatus := targetClient.GetConnectionStatus()
	if !targetClient.IsConnected() {
		if connecting, ok := connectionStatus["connecting"].(bool); ok && connecting {
			return nil, fmt.Errorf("client for server %s is currently connecting", serverName)
		}

		// Include last error if available
		if lastError, ok := connectionStatus["last_error"].(string); ok && lastError != "" {
			return nil, fmt.Errorf("client for server %s is not connected (last error: %s)", serverName, lastError)
		}

		return nil, fmt.Errorf("client for server %s is not connected", serverName)
	}

	// Call the tool on the upstream server
	return targetClient.CallTool(ctx, actualToolName, args)
}

// ConnectAll connects to all configured servers that should retry
func (m *Manager) ConnectAll(ctx context.Context) error {
	m.mu.RLock()
	clients := make(map[string]*Client)
	for id, client := range m.clients {
		clients[id] = client
	}
	m.mu.RUnlock()

	var lastError error
	for id, client := range clients {
		if !client.IsConnected() && client.ShouldRetry() {
			if err := client.Connect(ctx); err != nil {
				m.logger.Warn("Failed to connect to upstream server (will retry with backoff)",
					zap.String("id", id),
					zap.Error(err))
				lastError = err
			}
		}
	}

	return lastError
}

// DisconnectAll disconnects from all servers
func (m *Manager) DisconnectAll() error {
	m.mu.RLock()
	clients := make([]*Client, 0, len(m.clients))
	for _, client := range m.clients {
		clients = append(clients, client)
	}
	m.mu.RUnlock()

	var lastError error
	for _, client := range clients {
		if err := client.Disconnect(); err != nil {
			lastError = err
		}
	}

	return lastError
}

// GetStats returns statistics about upstream connections
func (m *Manager) GetStats() map[string]interface{} {
	m.mu.RLock()
	defer m.mu.RUnlock()

	connectedCount := 0
	connectingCount := 0
	totalCount := len(m.clients)

	serverStatus := make(map[string]interface{})
	for id, client := range m.clients {
		// Get detailed connection status
		connectionStatus := client.GetConnectionStatus()

		status := map[string]interface{}{
			"connected":    connectionStatus["connected"],
			"connecting":   connectionStatus["connecting"],
			"retry_count":  connectionStatus["retry_count"],
			"should_retry": connectionStatus["should_retry"],
			"name":         client.config.Name,
			"url":          client.config.URL,
			"protocol":     client.config.Protocol,
		}

		if connected, ok := connectionStatus["connected"].(bool); ok && connected {
			connectedCount++
		}

		if connecting, ok := connectionStatus["connecting"].(bool); ok && connecting {
			connectingCount++
		}

		if lastRetryTime, ok := connectionStatus["last_retry_time"].(time.Time); ok && !lastRetryTime.IsZero() {
			status["last_retry_time"] = lastRetryTime
		}

		if lastError, ok := connectionStatus["last_error"].(string); ok {
			status["last_error"] = lastError
		}

		if serverName, ok := connectionStatus["server_name"].(string); ok {
			status["server_name"] = serverName
		}

		if serverVersion, ok := connectionStatus["server_version"].(string); ok {
			status["server_version"] = serverVersion
		}

		if client.GetServerInfo() != nil {
			info := client.GetServerInfo()
			status["protocol_version"] = info.ProtocolVersion
		}

		serverStatus[id] = status
	}

	return map[string]interface{}{
		"connected_servers":  connectedCount,
		"connecting_servers": connectingCount,
		"total_servers":      totalCount,
		"servers":            serverStatus,
		"total_tools":        m.GetTotalToolCount(),
	}
}

// GetTotalToolCount returns the total number of tools across all servers
func (m *Manager) GetTotalToolCount() int {
	ctx := context.Background()
	tools, err := m.DiscoverTools(ctx)
	if err != nil {
		m.logger.Error("Failed to discover tools for count", zap.Error(err))
		return 0
	}
	return len(tools)
}

// ListServers returns information about all registered servers
func (m *Manager) ListServers() map[string]*config.ServerConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()

	servers := make(map[string]*config.ServerConfig)
	for id, client := range m.clients {
		servers[id] = client.config
	}
	return servers
}
