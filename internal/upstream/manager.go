package upstream

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"mcpproxy-go/internal/config"
	"mcpproxy-go/internal/upstream/managed"
	"mcpproxy-go/internal/upstream/types"
)

// Manager manages connections to multiple upstream MCP servers
type Manager struct {
	clients         map[string]*managed.Client
	mu              sync.RWMutex
	logger          *zap.Logger
	logConfig       *config.LogConfig
	globalConfig    *config.Config
	notificationMgr *NotificationManager
}

// NewManager creates a new upstream manager
func NewManager(logger *zap.Logger, globalConfig *config.Config) *Manager {
	return &Manager{
		clients:         make(map[string]*managed.Client),
		logger:          logger,
		globalConfig:    globalConfig,
		notificationMgr: NewNotificationManager(),
	}
}

// SetLogConfig sets the logging configuration for upstream server loggers
func (m *Manager) SetLogConfig(logConfig *config.LogConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.logConfig = logConfig
}

// AddNotificationHandler adds a notification handler to receive state change notifications
func (m *Manager) AddNotificationHandler(handler NotificationHandler) {
	m.notificationMgr.AddHandler(handler)
}

// AddServerConfig adds a server configuration without connecting
func (m *Manager) AddServerConfig(id string, serverConfig *config.ServerConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check if existing client exists and if config has changed
	if existingClient, exists := m.clients[id]; exists {
		existingConfig := existingClient.Config

		// Compare configurations to determine if reconnection is needed
		configChanged := existingConfig.URL != serverConfig.URL ||
			existingConfig.Protocol != serverConfig.Protocol ||
			existingConfig.Command != serverConfig.Command ||
			!equalStringSlices(existingConfig.Args, serverConfig.Args) ||
			!equalStringMaps(existingConfig.Env, serverConfig.Env) ||
			!equalStringMaps(existingConfig.Headers, serverConfig.Headers) ||
			existingConfig.Enabled != serverConfig.Enabled ||
			existingConfig.Quarantined != serverConfig.Quarantined

		if configChanged {
			m.logger.Info("Server configuration changed, disconnecting existing client",
				zap.String("id", id),
				zap.String("name", serverConfig.Name),
				zap.String("current_state", existingClient.GetState().String()),
				zap.Bool("is_connected", existingClient.IsConnected()))
			_ = existingClient.Disconnect()
			delete(m.clients, id)
		} else {
			m.logger.Debug("Server configuration unchanged, keeping existing client",
				zap.String("id", id),
				zap.String("name", serverConfig.Name),
				zap.String("current_state", existingClient.GetState().String()),
				zap.Bool("is_connected", existingClient.IsConnected()))
			// Update the client's config reference to the new config but don't recreate the client
			existingClient.Config = serverConfig
			return nil
		}
	}

	// Create new client but don't connect yet
	client, err := managed.NewClient(id, serverConfig, m.logger, m.logConfig, m.globalConfig)
	if err != nil {
		return fmt.Errorf("failed to create client for server %s: %w", serverConfig.Name, err)
	}

	// Set up notification callback for state changes
	if m.notificationMgr != nil {
		notifierCallback := StateChangeNotifier(m.notificationMgr, serverConfig.Name)
		// Combine with existing callback if present
		existingCallback := client.StateManager.GetStateChangeCallback()
		client.StateManager.SetStateChangeCallback(func(oldState, newState types.ConnectionState, info *types.ConnectionInfo) {
			// Call existing callback first (for logging)
			if existingCallback != nil {
				existingCallback(oldState, newState, info)
			}
			// Then call notification callback
			notifierCallback(oldState, newState, info)
		})
	}

	m.clients[id] = client
	m.logger.Info("Added upstream server configuration",
		zap.String("id", id),
		zap.String("name", serverConfig.Name))

	return nil
}

// Helper functions for comparing slices and maps
func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalStringMaps(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// AddServer adds a new upstream server and connects to it (legacy method)
func (m *Manager) AddServer(id string, serverConfig *config.ServerConfig) error {
	if err := m.AddServerConfig(id, serverConfig); err != nil {
		return err
	}

	if !serverConfig.Enabled {
		m.logger.Debug("Skipping connection for disabled server",
			zap.String("id", id),
			zap.String("name", serverConfig.Name))
		return nil
	}

	// Check if client exists and is already connected
	if client, exists := m.GetClient(id); exists {
		if client.IsConnected() {
			m.logger.Debug("Server is already connected, skipping connection attempt",
				zap.String("id", id),
				zap.String("name", serverConfig.Name))
			return nil
		}

		// Connect to server
		ctx := context.Background()
		if err := client.Connect(ctx); err != nil {
			return fmt.Errorf("failed to connect to server %s: %w", serverConfig.Name, err)
		}
	} else {
		m.logger.Error("Client not found after AddServerConfig - this should not happen",
			zap.String("id", id),
			zap.String("name", serverConfig.Name))
	}

	return nil
}

// RemoveServer removes an upstream server
func (m *Manager) RemoveServer(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if client, exists := m.clients[id]; exists {
		m.logger.Info("Removing upstream server",
			zap.String("id", id),
			zap.String("state", client.GetState().String()))
		_ = client.Disconnect()
		delete(m.clients, id)
	}
}

// GetClient returns a client by ID
func (m *Manager) GetClient(id string) (*managed.Client, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	client, exists := m.clients[id]
	return client, exists
}

// GetAllClients returns all clients
func (m *Manager) GetAllClients() map[string]*managed.Client {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make(map[string]*managed.Client)
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
		if !client.Config.Enabled {
			continue
		}
		if !client.IsConnected() {
			m.logger.Debug("Skipping disconnected client", zap.String("id", id), zap.String("state", client.GetState().String()))
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
	var targetClient *managed.Client
	for _, client := range m.clients {
		if client.Config.Name == serverName {
			targetClient = client
			break
		}
	}

	if targetClient == nil {
		return nil, fmt.Errorf("no client found for server: %s", serverName)
	}

	if !targetClient.Config.Enabled {
		return nil, fmt.Errorf("client for server %s is disabled", serverName)
	}

	// Check connection status and provide detailed error information
	if !targetClient.IsConnected() {
		state := targetClient.GetState()
		if targetClient.IsConnecting() {
			return nil, fmt.Errorf("server '%s' is currently connecting - please wait for connection to complete (state: %s)", serverName, state.String())
		}

		// Include last error if available with enhanced context
		if lastError := targetClient.GetLastError(); lastError != nil {
			// Enrich OAuth-related errors at source
			lastErrStr := lastError.Error()
			if strings.Contains(lastErrStr, "OAuth authentication failed") ||
				strings.Contains(lastErrStr, "Dynamic Client Registration") ||
				strings.Contains(lastErrStr, "authorization required") {
				return nil, fmt.Errorf("server '%s' requires OAuth authentication but is not properly configured. OAuth setup failed: %s. Please configure OAuth credentials manually or use a Personal Access Token - check mcpproxy logs for detailed setup instructions", serverName, lastError.Error())
			}

			if strings.Contains(lastErrStr, "OAuth metadata unavailable") {
				return nil, fmt.Errorf("server '%s' does not provide valid OAuth configuration endpoints. This server may not support OAuth or requires manual authentication setup: %s", serverName, lastError.Error())
			}

			return nil, fmt.Errorf("server '%s' is not connected (state: %s) - connection failed with error: %s", serverName, state.String(), lastError.Error())
		}

		return nil, fmt.Errorf("server '%s' is not connected (state: %s) - use 'upstream_servers' tool to check server configuration", serverName, state.String())
	}

	// Call the tool on the upstream server with enhanced error handling
	result, err := targetClient.CallTool(ctx, actualToolName, args)
	if err != nil {
		// Enrich errors at source with server context
		errStr := err.Error()

		// OAuth-related errors
		if strings.Contains(errStr, "OAuth authentication failed") ||
			strings.Contains(errStr, "authorization required") ||
			strings.Contains(errStr, "invalid_token") ||
			strings.Contains(errStr, "Unauthorized") {
			return nil, fmt.Errorf("server '%s' authentication failed for tool '%s'. OAuth/token authentication required but not properly configured. Check server authentication settings and ensure valid credentials are available: %w", serverName, actualToolName, err)
		}

		// Permission/scope errors
		if strings.Contains(errStr, "insufficient_scope") || strings.Contains(errStr, "access_denied") {
			return nil, fmt.Errorf("server '%s' denied access to tool '%s' due to insufficient permissions or scopes. Check OAuth scopes configuration or token permissions: %w", serverName, actualToolName, err)
		}

		// Rate limiting
		if strings.Contains(errStr, "429") || strings.Contains(errStr, "rate limit") || strings.Contains(errStr, "too many requests") {
			return nil, fmt.Errorf("server '%s' rate limit exceeded for tool '%s'. Please wait before making more requests or check API quotas: %w", serverName, actualToolName, err)
		}

		// Connection issues
		if strings.Contains(errStr, "connection refused") || strings.Contains(errStr, "no such host") {
			return nil, fmt.Errorf("server '%s' connection failed for tool '%s'. Check if the server URL is correct and the server is running: %w", serverName, actualToolName, err)
		}

		// Tool-specific errors
		if strings.Contains(errStr, "tool not found") || strings.Contains(errStr, "unknown tool") {
			return nil, fmt.Errorf("tool '%s' not found on server '%s'. Use 'retrieve_tools' to see available tools: %w", actualToolName, serverName, err)
		}

		// Generic error with helpful context
		return nil, fmt.Errorf("tool '%s' on server '%s' failed: %w. Check server configuration, authentication, and tool parameters", actualToolName, serverName, err)
	}

	return result, nil
}

// ConnectAll connects to all configured servers that should retry
func (m *Manager) ConnectAll(ctx context.Context) error {
	m.mu.RLock()
	clients := make(map[string]*managed.Client)
	for id, client := range m.clients {
		clients[id] = client
	}
	m.mu.RUnlock()

	m.logger.Debug("ConnectAll starting",
		zap.Int("total_clients", len(clients)))

	var wg sync.WaitGroup
	for id, client := range clients {
		m.logger.Debug("Evaluating client for connection",
			zap.String("id", id),
			zap.String("name", client.Config.Name),
			zap.Bool("enabled", client.Config.Enabled),
			zap.Bool("is_connected", client.IsConnected()),
			zap.Bool("is_connecting", client.IsConnecting()),
			zap.String("current_state", client.GetState().String()),
			zap.Bool("quarantined", client.Config.Quarantined))

		if !client.Config.Enabled {
			m.logger.Debug("Skipping disabled client",
				zap.String("id", id),
				zap.String("name", client.Config.Name))

			if client.IsConnected() {
				m.logger.Info("Disconnecting disabled client", zap.String("id", id), zap.String("name", client.Config.Name))
				_ = client.Disconnect()
			}
			continue
		}

		// Check connection eligibility with detailed logging
		if client.IsConnected() {
			m.logger.Debug("Client already connected, skipping",
				zap.String("id", id),
				zap.String("name", client.Config.Name))
			continue
		}

		if client.IsConnecting() {
			m.logger.Debug("Client already connecting, skipping",
				zap.String("id", id),
				zap.String("name", client.Config.Name))
			continue
		}

		m.logger.Info("Attempting to connect client",
			zap.String("id", id),
			zap.String("name", client.Config.Name),
			zap.String("url", client.Config.URL),
			zap.String("command", client.Config.Command),
			zap.String("protocol", client.Config.Protocol))

		wg.Add(1)
		go func(id string, c *managed.Client) {
			defer wg.Done()

			if err := c.Connect(ctx); err != nil {
				m.logger.Error("Failed to connect to upstream server",
					zap.String("id", id),
					zap.String("name", c.Config.Name),
					zap.String("state", c.GetState().String()),
					zap.Error(err))
			} else {
				m.logger.Info("Successfully initiated connection to upstream server",
					zap.String("id", id),
					zap.String("name", c.Config.Name))
			}
		}(id, client)
	}

	wg.Wait()
	return nil
}

// DisconnectAll disconnects from all servers
func (m *Manager) DisconnectAll() error {
	m.mu.RLock()
	clients := make([]*managed.Client, 0, len(m.clients))
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
		// Get detailed connection info from state manager
		connectionInfo := client.GetConnectionInfo()

		status := map[string]interface{}{
			"state":        connectionInfo.State.String(),
			"connected":    connectionInfo.State == types.StateReady,
			"connecting":   client.IsConnecting(),
			"retry_count":  connectionInfo.RetryCount,
			"should_retry": client.ShouldRetry(),
			"name":         client.Config.Name,
			"url":          client.Config.URL,
			"protocol":     client.Config.Protocol,
		}

		if connectionInfo.State == types.StateReady {
			connectedCount++
		}

		if client.IsConnecting() {
			connectingCount++
		}

		if !connectionInfo.LastRetryTime.IsZero() {
			status["last_retry_time"] = connectionInfo.LastRetryTime
		}

		if connectionInfo.LastError != nil {
			status["last_error"] = connectionInfo.LastError.Error()
		}

		if connectionInfo.ServerName != "" {
			status["server_name"] = connectionInfo.ServerName
		}

		if connectionInfo.ServerVersion != "" {
			status["server_version"] = connectionInfo.ServerVersion
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
		if !client.Config.Enabled || !client.IsConnected() {
			continue
		}

		// Quick check if client is actually reachable before making network call
		if !client.IsConnected() {
			continue
		}

		// Use timeout for UI status updates (30 seconds for SSE servers)
		// This allows time for SSE servers to establish connections and respond
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)

		m.logger.Debug("Starting ListTools for tool counting",
			zap.Duration("timeout", 30*time.Second))
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
		servers[id] = client.Config
	}
	return servers
}
