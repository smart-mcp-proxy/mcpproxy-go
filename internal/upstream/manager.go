package upstream

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"mcpproxy-go/internal/config"
	"mcpproxy-go/internal/oauth"
	"mcpproxy-go/internal/secret"
	"mcpproxy-go/internal/storage"
	"mcpproxy-go/internal/transport"
	"mcpproxy-go/internal/upstream/core"
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
	storage         *storage.BoltDB
	notificationMgr *NotificationManager
	secretResolver  *secret.Resolver

	// tokenReconnect keeps last reconnect trigger time per server when detecting
	// newly available OAuth tokens without explicit DB events (e.g., when CLI
	// cannot write due to DB lock). Prevents rapid retrigger loops.
	tokenReconnect map[string]time.Time

	// Context for shutdown coordination
	shutdownCtx    context.Context
	shutdownCancel context.CancelFunc
}

// NewManager creates a new upstream manager
func NewManager(logger *zap.Logger, globalConfig *config.Config, storage *storage.BoltDB, secretResolver *secret.Resolver) *Manager {
	shutdownCtx, shutdownCancel := context.WithCancel(context.Background())
	manager := &Manager{
		clients:         make(map[string]*managed.Client),
		logger:          logger,
		globalConfig:    globalConfig,
		storage:         storage,
		notificationMgr: NewNotificationManager(),
		secretResolver:  secretResolver,
		tokenReconnect:  make(map[string]time.Time),
		shutdownCtx:     shutdownCtx,
		shutdownCancel:  shutdownCancel,
	}

	// Set up OAuth completion callback to trigger connection retries (in-process)
	tokenManager := oauth.GetTokenStoreManager()
	tokenManager.SetOAuthCompletionCallback(func(serverName string) {
		logger.Info("OAuth completion callback triggered, attempting connection retry",
			zap.String("server", serverName))
		if err := manager.RetryConnection(serverName); err != nil {
			logger.Warn("Failed to trigger connection retry after OAuth completion",
				zap.String("server", serverName),
				zap.Error(err))
		}
	})

	// Start database event monitor for cross-process OAuth completion notifications
	if storage != nil {
		go manager.startOAuthEventMonitor(shutdownCtx)
	}

	return manager
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

	// Check if existing client exists and if config has changed
	var clientToDisconnect *managed.Client
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

			// Remove from map immediately to prevent new operations
			delete(m.clients, id)
			// Save reference to disconnect outside lock
			clientToDisconnect = existingClient
		} else {
			m.logger.Debug("Server configuration unchanged, keeping existing client",
				zap.String("id", id),
				zap.String("name", serverConfig.Name),
				zap.String("current_state", existingClient.GetState().String()),
				zap.Bool("is_connected", existingClient.IsConnected()))
			// Update the client's config reference to the new config but don't recreate the client
			// Use thread-safe setter to avoid race with GetServerState()
			m.mu.Unlock()
			existingClient.SetConfig(serverConfig)
			return nil
		}
	}

	// Create new client but don't connect yet
	client, err := managed.NewClient(id, serverConfig, m.logger, m.logConfig, m.globalConfig, m.storage, m.secretResolver)
	if err != nil {
		m.mu.Unlock()
		// Disconnect old client if we failed to create new one
		if clientToDisconnect != nil {
			_ = clientToDisconnect.Disconnect()
		}
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

	// IMPORTANT: Release lock before disconnecting to prevent deadlock
	m.mu.Unlock()

	// Disconnect old client outside lock to avoid blocking other operations
	if clientToDisconnect != nil {
		_ = clientToDisconnect.Disconnect()
	}

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

		// Connect to server with timeout to prevent hanging
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := client.Connect(ctx); err != nil {
			// Check if this is an OAuth error - don't fail AddServer for OAuth
			errStr := err.Error()
			isOAuthError := strings.Contains(errStr, "OAuth") ||
				strings.Contains(errStr, "oauth") ||
				strings.Contains(errStr, "authorization required")

			if isOAuthError {
				m.logger.Info("Server requires OAuth authorization - connection will complete after OAuth login",
					zap.String("id", id),
					zap.String("name", serverConfig.Name),
					zap.Error(err))
				// Don't return error - server is added successfully, just needs OAuth
				return nil
			}

			// For non-OAuth errors, still return error
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
	// Get client reference while holding lock briefly
	m.mu.Lock()
	client, exists := m.clients[id]
	if exists {
		// Remove from map immediately to prevent new operations
		delete(m.clients, id)
	}
	m.mu.Unlock()

	// Disconnect outside the lock to avoid blocking other operations
	if exists {
		m.logger.Info("Removing upstream server",
			zap.String("id", id),
			zap.String("state", client.GetState().String()))
		_ = client.Disconnect()
		m.logger.Debug("upstream.Manager.RemoveServer: disconnect completed", zap.String("id", id))
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
	type clientSnapshot struct {
		id      string
		name    string
		enabled bool
		client  *managed.Client
	}

	m.mu.RLock()
	snapshots := make([]clientSnapshot, 0, len(m.clients))
	for id, client := range m.clients {
		name := ""
		if client != nil && client.Config != nil {
			name = client.Config.Name
		}
		snapshots = append(snapshots, clientSnapshot{
			id:      id,
			name:    name,
			enabled: client != nil && client.Config != nil && client.Config.Enabled,
			client:  client,
		})
	}
	m.mu.RUnlock()

	var allTools []*config.ToolMetadata
	connectedCount := 0

	for _, snapshot := range snapshots {
		client := snapshot.client
		if client == nil {
			continue
		}

		if !snapshot.enabled {
			continue
		}
		if !client.IsConnected() {
			m.logger.Debug("Skipping disconnected client",
				zap.String("id", snapshot.id),
				zap.String("server", snapshot.name),
				zap.String("state", client.GetState().String()))
			continue
		}
		connectedCount++

		tools, err := client.ListTools(ctx)
		if err != nil {
			m.logger.Error("Failed to list tools from client",
				zap.String("id", snapshot.id),
				zap.String("server", snapshot.name),
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
	m.logger.Debug("CallTool: starting",
		zap.String("tool_name", toolName),
		zap.Any("args", args))

	// Parse tool name to extract server and tool components
	parts := strings.SplitN(toolName, ":", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid tool name format: %s (expected server:tool)", toolName)
	}

	serverName := parts[0]
	actualToolName := parts[1]

	m.logger.Debug("CallTool: parsed tool name",
		zap.String("server_name", serverName),
		zap.String("actual_tool_name", actualToolName))

	m.mu.RLock()
	defer m.mu.RUnlock()

	m.logger.Debug("CallTool: acquired read lock, searching for client",
		zap.String("server_name", serverName),
		zap.Int("total_clients", len(m.clients)))

	// Find the client for this server
	var targetClient *managed.Client
	for _, client := range m.clients {
		if client.Config.Name == serverName {
			targetClient = client
			break
		}
	}

	if targetClient == nil {
		m.logger.Error("CallTool: no client found",
			zap.String("server_name", serverName))
		return nil, fmt.Errorf("no client found for server: %s", serverName)
	}

	m.logger.Debug("CallTool: client found",
		zap.String("server_name", serverName),
		zap.Bool("enabled", targetClient.Config.Enabled),
		zap.Bool("connected", targetClient.IsConnected()),
		zap.String("state", targetClient.GetState().String()))

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

	m.logger.Debug("CallTool: calling client.CallTool",
		zap.String("server_name", serverName),
		zap.String("actual_tool_name", actualToolName))

	// Call the tool on the upstream server with enhanced error handling
	result, err := targetClient.CallTool(ctx, actualToolName, args)

	m.logger.Debug("CallTool: client.CallTool returned",
		zap.String("server_name", serverName),
		zap.String("actual_tool_name", actualToolName),
		zap.Error(err),
		zap.Bool("has_result", result != nil))
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

		if client.Config.Quarantined {
			m.logger.Info("Skipping quarantined client",
				zap.String("id", id),
				zap.String("name", client.Config.Name))
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

		if client.GetState() == types.StateError && !client.ShouldRetry() {
			info := client.GetConnectionInfo()
			m.logger.Debug("Client backoff active, skipping connect attempt",
				zap.String("id", id),
				zap.String("name", client.Config.Name),
				zap.Int("retry_count", info.RetryCount),
				zap.Time("last_retry_time", info.LastRetryTime))
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
	// Cancel shutdown context to stop OAuth event monitor
	if m.shutdownCancel != nil {
		m.shutdownCancel()
	}

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
			m.logger.Warn("Client disconnect failed",
				zap.String("server", client.Config.Name),
				zap.Error(err))
		}
	}

	return lastError
}

// HasDockerContainers checks if any connected servers are running Docker containers
func (m *Manager) HasDockerContainers() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, client := range m.clients {
		if client.IsDockerCommand() {
			return true
		}
	}
	return false
}

// GetStats returns statistics about upstream connections
// GetStats returns statistics about all managed clients.
// Phase 6 Fix: Lock-free implementation to prevent deadlock with async operations.
func (m *Manager) GetStats() map[string]interface{} {
	// Phase 6: Copy client references while holding lock briefly
	m.mu.RLock()
	clientsCopy := make(map[string]*managed.Client, len(m.clients))
	for id, client := range m.clients {
		clientsCopy[id] = client
	}
	totalCount := len(m.clients)
	m.mu.RUnlock()

	// Now process clients without holding lock to avoid deadlock
	connectedCount := 0
	connectingCount := 0
	serverStatus := make(map[string]interface{})

	for id, client := range clientsCopy {
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

	// Call GetTotalToolCount without holding manager lock
	totalTools := m.GetTotalToolCount()

	return map[string]interface{}{
		"connected_servers":  connectedCount,
		"connecting_servers": connectingCount,
		"total_servers":      totalCount,
		"servers":            serverStatus,
		"total_tools":        totalTools,
	}
}

// GetTotalToolCount returns the total number of tools across all servers.
// Uses cached counts to avoid excessive network calls (2-minute cache per server).
// Phase 6 Fix: Lock-free implementation to prevent deadlock.
func (m *Manager) GetTotalToolCount() int {
	// Phase 6: Copy client references while holding lock briefly
	m.mu.RLock()
	clientsCopy := make([]*managed.Client, 0, len(m.clients))
	for _, client := range m.clients {
		clientsCopy = append(clientsCopy, client)
	}
	m.mu.RUnlock()

	// Now process clients without holding lock
	totalTools := 0
	for _, client := range clientsCopy {
		if client == nil || client.Config == nil || !client.Config.Enabled || !client.IsConnected() {
			continue
		}

		// Use non-blocking cached count to avoid stalling API handlers
		totalTools += client.GetCachedToolCountNonBlocking()
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

// RetryConnection triggers a connection retry for a specific server
// This is typically called after OAuth completion to immediately use new tokens
func (m *Manager) RetryConnection(serverName string) error {
	m.mu.RLock()
	client, exists := m.clients[serverName]
	m.mu.RUnlock()

	if !exists {
		return fmt.Errorf("server not found: %s", serverName)
	}

	// If the client is already connected or connecting, do not force a
	// reconnect. This prevents Ready→Disconnected flapping when duplicate
	// OAuth completion events arrive.
	if client.IsConnected() {
		m.logger.Info("Skipping retry: client already connected",
			zap.String("server", serverName),
			zap.String("state", client.GetState().String()))
		return nil
	}
	if client.IsConnecting() {
		m.logger.Info("Skipping retry: client already connecting",
			zap.String("server", serverName),
			zap.String("state", client.GetState().String()))
		return nil
	}

	// Log detailed state prior to retry and token availability in persistent store
	// This helps diagnose cases where the core client reports "already connected"
	// while the managed state is Error/Disconnected.
	state := client.GetState().String()
	isConnected := client.IsConnected()
	isConnecting := client.IsConnecting()

	// Check persistent token presence (daemon uses BBolt-backed token store)
	var hasToken bool
	var tokenExpires time.Time
	if m.storage != nil {
		ts := oauth.NewPersistentTokenStore(client.Config.Name, client.Config.URL, m.storage)
		if tok, err := ts.GetToken(context.Background()); err == nil && tok != nil {
			hasToken = true
			tokenExpires = tok.ExpiresAt
		}
	}

	m.logger.Info("Triggering connection retry after OAuth completion",
		zap.String("server", serverName),
		zap.String("state", state),
		zap.Bool("is_connected", isConnected),
		zap.Bool("is_connecting", isConnecting),
		zap.Bool("has_persistent_token", hasToken),
		zap.Time("token_expires_at", tokenExpires))

	// Trigger connection attempt in background to avoid blocking
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()

		// Important: Ensure a clean reconnect only if not already connected.
		// Managed state guards above should make this idempotent.
		if derr := client.Disconnect(); derr != nil {
			m.logger.Debug("Disconnect before retry returned",
				zap.String("server", serverName),
				zap.Error(derr))
		}

		if err := client.Connect(ctx); err != nil {
			m.logger.Warn("Connection retry after OAuth failed",
				zap.String("server", serverName),
				zap.Error(err))
		} else {
			m.logger.Info("Connection retry after OAuth succeeded",
				zap.String("server", serverName))
		}
	}()

	return nil
}

// startOAuthEventMonitor monitors the database for OAuth completion events from CLI processes
func (m *Manager) startOAuthEventMonitor(ctx context.Context) {
	m.logger.Info("Starting OAuth event monitor for cross-process notifications")

	ticker := time.NewTicker(5 * time.Second) // Check every 5 seconds
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			m.logger.Info("OAuth event monitor stopped due to context cancellation")
			return
		case <-ticker.C:
			if err := m.processOAuthEvents(); err != nil {
				m.logger.Warn("Failed to process OAuth events", zap.Error(err))
			}

			// Also scan for newly available tokens to handle cases where the CLI
			// could not write a DB event due to a lock. If we see a persisted
			// token for an errored OAuth server, trigger a reconnect once.
			m.scanForNewTokens()
		}
	}
}

// processOAuthEvents checks for and processes unprocessed OAuth completion events
func (m *Manager) processOAuthEvents() error {
	if m.storage == nil {
		m.logger.Debug("processOAuthEvents: no storage available, skipping")
		return nil
	}

	m.logger.Debug("processOAuthEvents: checking for OAuth completion events...")
	events, err := m.storage.GetUnprocessedOAuthCompletionEvents()
	if err != nil {
		m.logger.Error("processOAuthEvents: failed to get events", zap.Error(err))
		return fmt.Errorf("failed to get OAuth completion events: %w", err)
	}

	if len(events) == 0 {
		m.logger.Debug("processOAuthEvents: no unprocessed events found")
		return nil
	}

	m.logger.Info("processOAuthEvents: found unprocessed OAuth completion events", zap.Int("count", len(events)))

	for _, event := range events {
		m.logger.Info("Processing OAuth completion event from database",
			zap.String("server", event.ServerName),
			zap.Time("completed_at", event.CompletedAt))

		// Skip retry if client is already connected/connecting to avoid flapping
		m.mu.RLock()
		c, exists := m.clients[event.ServerName]
		m.mu.RUnlock()
		if exists && (c.IsConnected() || c.IsConnecting()) {
			m.logger.Info("Skipping retry for OAuth event: client already connected/connecting",
				zap.String("server", event.ServerName),
				zap.String("state", c.GetState().String()))
		} else {
			// Trigger connection retry
			if err := m.RetryConnection(event.ServerName); err != nil {
				m.logger.Warn("Failed to retry connection for OAuth completion event",
					zap.String("server", event.ServerName),
					zap.Error(err))
			} else {
				m.logger.Info("Successfully triggered connection retry for OAuth completion event",
					zap.String("server", event.ServerName))
			}
		}

		// Mark event as processed
		if err := m.storage.MarkOAuthCompletionEventProcessed(event.ServerName, event.CompletedAt); err != nil {
			m.logger.Error("Failed to mark OAuth completion event as processed",
				zap.String("server", event.ServerName),
				zap.Error(err))
		}

		// Clean up old events periodically (when processing events)
		if err := m.storage.CleanupOldOAuthCompletionEvents(); err != nil {
			m.logger.Warn("Failed to cleanup old OAuth completion events", zap.Error(err))
		}
	}

	return nil
}

// scanForNewTokens checks persistent token store for each client in Error state
// and triggers a reconnect if a token is present. This complements DB-based
// events and handles DB lock scenarios.
func (m *Manager) scanForNewTokens() {
	if m.storage == nil {
		return
	}

	m.mu.RLock()
	clients := make(map[string]*managed.Client, len(m.clients))
	for id, c := range m.clients {
		clients[id] = c
	}
	m.mu.RUnlock()

	now := time.Now()
	for id, c := range clients {
		// Get config in a thread-safe manner to avoid race conditions
		cfg := c.GetConfig()

		// Only consider enabled, HTTP/SSE servers not currently connected
		if !cfg.Enabled || c.IsConnected() {
			continue
		}

		state := c.GetState()
		// Focus on Error state likely due to OAuth/authorization
		if state != types.StateError {
			continue
		}

		// Rate-limit triggers per server
		if last, ok := m.tokenReconnect[id]; ok && now.Sub(last) < 10*time.Second {
			continue
		}

		// Check for a persisted token
		ts := oauth.NewPersistentTokenStore(cfg.Name, cfg.URL, m.storage)
		tok, err := ts.GetToken(context.Background())
		if err != nil || tok == nil {
			continue
		}

		m.logger.Info("Detected persisted OAuth token; triggering reconnect",
			zap.String("server", cfg.Name),
			zap.Time("token_expires_at", tok.ExpiresAt))

		// Remember trigger time and retry connection
		m.tokenReconnect[id] = now
		_ = m.RetryConnection(cfg.Name)
	}
}

// StartManualOAuth performs an in-process OAuth flow for the given server.
// This avoids cross-process DB locking by using the daemon's storage directly.
func (m *Manager) StartManualOAuth(serverName string, force bool) error {
	m.mu.RLock()
	client, exists := m.clients[serverName]
	m.mu.RUnlock()
	if !exists {
		return fmt.Errorf("server not found: %s", serverName)
	}

	cfg := client.Config
	m.logger.Info("Starting in-process manual OAuth",
		zap.String("server", cfg.Name),
		zap.Bool("force", force))

	// Preflight: if server does not appear to require OAuth, avoid starting
	// OAuth flow and return an informative error (tray will show it).
	// Attempt a short no-auth initialize to confirm.
	if !oauth.ShouldUseOAuth(cfg) && !force {
		m.logger.Info("OAuth not applicable based on config (no headers, protocol)", zap.String("server", cfg.Name))
		return fmt.Errorf("OAuth is not supported or not required for server '%s'", cfg.Name)
	}

	// Create a transient core client that uses the daemon's storage
	coreClient, err := core.NewClientWithOptions(cfg.Name, cfg, m.logger, m.logConfig, m.globalConfig, m.storage, false, m.secretResolver)
	if err != nil {
		return fmt.Errorf("failed to create core client for OAuth: %w", err)
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()

		if force {
			coreClient.ClearOAuthState()
		}

		// Preflight no-auth check: try a quick connect without OAuth to
		// determine if authorization is actually required. If initialize
		// succeeds, inform and return early.
		if !force {
			cpy := *cfg
			cpy.Headers = cfg.Headers // preserve headers
			// Try HTTP/SSE path with no OAuth
			noAuthTransport := transport.DetermineTransportType(&cpy)
			if noAuthTransport == "http" || noAuthTransport == "streamable-http" || noAuthTransport == "sse" {
				m.logger.Info("Running preflight no-auth initialize to check OAuth requirement", zap.String("server", cfg.Name))
				testClient, err2 := core.NewClientWithOptions(cfg.Name, &cpy, m.logger, m.logConfig, m.globalConfig, m.storage, false, m.secretResolver)
				if err2 == nil {
					tctx, tcancel := context.WithTimeout(ctx, 10*time.Second)
					_ = testClient.Connect(tctx)
					tcancel()
					if testClient.GetServerInfo() != nil {
						m.logger.Info("Preflight succeeded without OAuth; skipping OAuth flow", zap.String("server", cfg.Name))
						return
					}
				}
			}
		}

		m.logger.Info("Triggering OAuth flow (in-process)", zap.String("server", cfg.Name))
		if err := coreClient.ForceOAuthFlow(ctx); err != nil {
			m.logger.Warn("In-process OAuth flow failed",
				zap.String("server", cfg.Name),
				zap.Error(err))
			return
		}
		m.logger.Info("In-process OAuth flow completed successfully",
			zap.String("server", cfg.Name))
		// Immediately attempt reconnect with new tokens
		if err := m.RetryConnection(cfg.Name); err != nil {
			m.logger.Warn("Failed to trigger reconnect after in-process OAuth",
				zap.String("server", cfg.Name),
				zap.Error(err))
		}
	}()

	return nil
}

// InvalidateAllToolCountCaches invalidates tool count caches for all clients
// This should be called when tools are known to have changed (e.g., after indexing)
func (m *Manager) InvalidateAllToolCountCaches() {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, client := range m.clients {
		client.InvalidateToolCountCache()
	}

	m.logger.Debug("Invalidated tool count caches for all clients",
		zap.Int("client_count", len(m.clients)))
}
