package upstream

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"mcpproxy-go/internal/config"
	"mcpproxy-go/internal/storage"
)

// Manager manages connections to multiple upstream MCP servers
type Manager struct {
	clients        map[string]*Client
	mu             sync.RWMutex
	logger         *zap.Logger
	logConfig      *config.LogConfig
	globalConfig   *config.Config
	storageManager *storage.Manager
}

// NewManager creates a new upstream manager
func NewManager(logger *zap.Logger, globalConfig *config.Config, storageManager *storage.Manager) *Manager {
	return &Manager{
		clients:        make(map[string]*Client),
		logger:         logger,
		globalConfig:   globalConfig,
		storageManager: storageManager,
	}
}

// SetLogConfig sets the logging configuration for upstream server loggers
func (m *Manager) SetLogConfig(logConfig *config.LogConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.logConfig = logConfig
}

// AddServerConfig adds a server configuration without connecting
func (m *Manager) AddServerConfig(id string, serverConfig *config.ServerConfig) error {
	m.logger.Debug("AddServerConfig called",
		zap.String("id", id),
		zap.String("name", serverConfig.Name),
		zap.Bool("enabled", serverConfig.Enabled))

	m.mu.Lock()
	defer m.mu.Unlock()

	// Remove existing client if it exists
	if existingClient, exists := m.clients[id]; exists {
		m.logger.Debug("Removing existing client", zap.String("id", id))
		_ = existingClient.Disconnect()
		delete(m.clients, id)
	}

	// Create new client but don't connect yet
	client, err := NewClient(id, serverConfig, m.logger, m.logConfig, m.globalConfig, m.storageManager, m)
	if err != nil {
		return fmt.Errorf("failed to create client for server %s: %w", serverConfig.Name, err)
	}

	m.clients[id] = client
	m.logger.Info("Added upstream server configuration",
		zap.String("id", id),
		zap.String("name", serverConfig.Name),
		zap.Bool("enabled", serverConfig.Enabled))

	return nil
}

// AddServer adds a new upstream server and connects to it (legacy method)
func (m *Manager) AddServer(id string, serverConfig *config.ServerConfig) error {
	if err := m.AddServerConfig(id, serverConfig); err != nil {
		return err
	}

	if !serverConfig.Enabled {
		m.logger.Info("Skipping connection for disabled server", zap.String("id", id), zap.String("name", serverConfig.Name))
		return nil
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

func (m *Manager) ForceReconnect(ctx context.Context, clientID string) {
	m.logger.Info("Manager forcing reconnect for client", zap.String("client_id", clientID))
	if client, exists := m.GetClient(clientID); exists {
		client.forceReconnect(ctx)
	} else {
		m.logger.Warn("Failed to force reconnect: client not found", zap.String("client_id", clientID))
	}
}

// RemoveServer removes an upstream server
func (m *Manager) RemoveServer(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if client, exists := m.clients[id]; exists {
		_ = client.Disconnect()
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

// GetAllServerNames returns a slice of all configured server names
func (m *Manager) GetAllServerNames() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	names := make([]string, 0, len(m.clients))
	for name := range m.clients {
		names = append(names, name)
	}
	return names
}

// DiscoverTools discovers all tools from all connected upstream servers
func (m *Manager) DiscoverTools(ctx context.Context) ([]*config.ToolMetadata, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var allTools []*config.ToolMetadata
	connectedCount := 0

	for id, client := range m.clients {
		if !client.config.Enabled {
			continue
		}
		if !client.IsConnected() {
			m.logger.Debug("Skipping disconnected client", zap.String("id", id))
			continue
		}
		connectedCount++

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
		zap.Int("connected_servers", connectedCount))

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
		return nil, fmt.Errorf("no client found for server: %s", serverName)
	}

	if !targetClient.config.Enabled {
		return nil, fmt.Errorf("client for server %s is disabled", serverName)
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
	m.logger.Debug("ConnectAll called")
	m.mu.RLock()
	clients := make(map[string]*Client)
	for id, client := range m.clients {
		clients[id] = client
	}
	m.mu.RUnlock()

	m.logger.Debug("ConnectAll found clients", zap.Int("client_count", len(clients)))

	var wg sync.WaitGroup
	for id, client := range clients {
		if !client.config.Enabled {
			if client.IsConnected() {
				m.logger.Info("Disconnecting disabled client", zap.String("id", id), zap.String("name", client.config.Name))
				_ = client.Disconnect()
			}
			continue
		}

		wg.Add(1)
		go func(id string, c *Client) {
			defer wg.Done()

			// Use the new OAuth-aware connection check
			if !c.shouldAttemptConnection() {
				status := c.GetConnectionStatus()
				connected, _ := status["connected"].(bool)
				connecting, _ := status["connecting"].(bool)
				oauthPending, _ := status["oauth_pending"].(bool)

				m.logger.Debug("Skipping connection attempt",
					zap.String("id", id),
					zap.String("name", c.config.Name),
					zap.Bool("connected", connected),
					zap.Bool("connecting", connecting),
					zap.Bool("oauth_pending", oauthPending))
				return
			}

			m.logger.Debug("Attempting to connect client",
				zap.String("id", id),
				zap.String("name", c.config.Name))

			if err := c.Connect(ctx); err != nil {
				// Only log as error if it's a real error (not OAuth pending)
				if err.Error() != "" {
					m.logger.Error("Failed to connect to upstream server",
						zap.String("id", id),
						zap.String("name", c.config.Name),
						zap.Error(err))
				}
			}
		}(id, client)
	}

	wg.Wait()
	m.logger.Debug("ConnectAll completed")
	return nil
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
// This is optimized to avoid network calls during shutdown for performance
func (m *Manager) GetTotalToolCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	totalTools := 0
	for _, client := range m.clients {
		if !client.config.Enabled || !client.IsConnected() {
			continue
		}

		// Quick check if client is actually reachable before making network call
		connectionStatus := client.GetConnectionStatus()
		if connected, ok := connectionStatus["connected"].(bool); !ok || !connected {
			continue
		}

		// Use a reasonable timeout to allow for Docker container startup and GitHub API calls
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		tools, err := client.ListTools(ctx)
		cancel()
		if err == nil && tools != nil {
			totalTools += len(tools)
		}
		// Silently ignore errors during tool counting to avoid noise during shutdown
	}
	return totalTools
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
