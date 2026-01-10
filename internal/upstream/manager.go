package upstream

import (
	"context"
	"fmt"
	"maps"
	"os/exec"
	"slices"
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

// Docker recovery constants - internal implementation defaults
const (
	dockerCheckInterval      = 30 * time.Second // How often to check Docker availability
	dockerMaxRetries         = 10               // Maximum consecutive failures before giving up (0 = unlimited)
	dockerRetryInterval1     = 2 * time.Second  // First retry - Docker might just be paused
	dockerRetryInterval2     = 5 * time.Second  // Second retry
	dockerRetryInterval3     = 10 * time.Second // Third retry
	dockerRetryInterval4     = 30 * time.Second // Fourth retry
	dockerRetryInterval5     = 60 * time.Second // Fifth+ retry (max backoff)
	dockerHealthCheckTimeout = 3 * time.Second  // Timeout for docker info command
)

// getDockerRetryInterval returns the retry interval for a given attempt number (exponential backoff)
func getDockerRetryInterval(attempt int) time.Duration {
	switch {
	case attempt <= 0:
		return dockerRetryInterval1
	case attempt == 1:
		return dockerRetryInterval2
	case attempt == 2:
		return dockerRetryInterval3
	case attempt == 3:
		return dockerRetryInterval4
	default:
		return dockerRetryInterval5 // Max backoff
	}
}

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
	shuttingDown   bool           // Flag to prevent reconnections during shutdown
	shutdownWg     sync.WaitGroup // Tracks all background goroutines for clean shutdown

	// Docker recovery state
	dockerRecoveryMu     sync.RWMutex
	dockerRecoveryActive bool
	dockerRecoveryState  *storage.DockerRecoveryState
	dockerRecoveryCancel context.CancelFunc
	storageMgr           *storage.Manager // Reference to storage manager for Docker state persistence

	// Tool discovery callback for notifications/tools/list_changed handling
	toolDiscoveryCallback func(ctx context.Context, serverName string) error
}

func cloneServerConfig(cfg *config.ServerConfig) *config.ServerConfig {
	if cfg == nil {
		return nil
	}

	clone := *cfg

	if cfg.Args != nil {
		clone.Args = slices.Clone(cfg.Args)
	}

	if cfg.Env != nil {
		clone.Env = maps.Clone(cfg.Env)
	}

	if cfg.Headers != nil {
		clone.Headers = maps.Clone(cfg.Headers)
	}

	if cfg.OAuth != nil {
		o := *cfg.OAuth
		if cfg.OAuth.Scopes != nil {
			o.Scopes = slices.Clone(cfg.OAuth.Scopes)
		}
		clone.OAuth = &o
	}

	if cfg.Isolation != nil {
		iso := *cfg.Isolation
		if cfg.Isolation.ExtraArgs != nil {
			iso.ExtraArgs = slices.Clone(cfg.Isolation.ExtraArgs)
		}
		clone.Isolation = &iso
	}

	return &clone
}

// NewManager creates a new upstream manager
func NewManager(logger *zap.Logger, globalConfig *config.Config, boltStorage *storage.BoltDB, secretResolver *secret.Resolver, storageMgr *storage.Manager) *Manager {
	shutdownCtx, shutdownCancel := context.WithCancel(context.Background())
	manager := &Manager{
		clients:         make(map[string]*managed.Client),
		logger:          logger,
		globalConfig:    globalConfig,
		storage:         boltStorage,
		notificationMgr: NewNotificationManager(),
		secretResolver:  secretResolver,
		tokenReconnect:  make(map[string]time.Time),
		shutdownCtx:     shutdownCtx,
		shutdownCancel:  shutdownCancel,
		storageMgr:      storageMgr,
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
	if boltStorage != nil {
		manager.shutdownWg.Add(1)
		go manager.startOAuthEventMonitor(shutdownCtx)
	}

	// Start Docker recovery monitor only when Docker is actually in use
	if storageMgr != nil && manager.shouldEnableDockerRecovery() {
		manager.shutdownWg.Add(1)
		go manager.startDockerRecoveryMonitor(shutdownCtx)
	} else if storageMgr != nil {
		manager.logger.Info("Docker recovery monitor disabled (docker isolation off or recovery disabled in config)")
	}

	return manager
}

// shouldEnableDockerRecovery returns true when Docker recovery should run based on config.
// It respects docker_recovery.enabled=false and only enables monitoring when Docker
// isolation is turned on or any server is explicitly using Docker commands.
func (m *Manager) shouldEnableDockerRecovery() bool {
	if m == nil || m.globalConfig == nil {
		return false
	}

	// Allow explicit opt-out via docker_recovery.enabled=false (defaults to enabled)
	if m.globalConfig.DockerRecovery != nil && !m.globalConfig.DockerRecovery.IsEnabled() {
		return false
	}

	// Global Docker isolation enabled
	if m.globalConfig.DockerIsolation != nil && m.globalConfig.DockerIsolation.Enabled {
		return true
	}

	// Detect servers that explicitly use Docker (e.g., docker run/exec commands)
	for _, srv := range m.globalConfig.Servers {
		if srv == nil {
			continue
		}

		if srv.Isolation != nil && srv.Isolation.Enabled {
			return true
		}

		if strings.Contains(srv.Command, "docker") {
			return true
		}
	}

	return false
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

// SetToolDiscoveryCallback sets the callback for triggering tool re-indexing when
// upstream servers send notifications/tools/list_changed notifications.
// This callback will be passed to all new clients created by the manager.
func (m *Manager) SetToolDiscoveryCallback(callback func(ctx context.Context, serverName string) error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.toolDiscoveryCallback = callback
	m.logger.Debug("Tool discovery callback set on manager")
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

	// Set up tool discovery callback for notifications/tools/list_changed handling
	if m.toolDiscoveryCallback != nil {
		client.SetToolDiscoveryCallback(m.toolDiscoveryCallback)
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

// ShutdownAll disconnects all clients and ensures all Docker containers are stopped
// This should be called during application shutdown to ensure clean exit
func (m *Manager) ShutdownAll(ctx context.Context) error {
	m.logger.Info("Shutting down all upstream servers")

	// Cancel shutdown context to stop background goroutines (OAuth monitor, Docker recovery)
	if m.shutdownCancel != nil {
		m.shutdownCancel()
	}

	// Wait for background goroutines to exit (with timeout)
	waitDone := make(chan struct{})
	go func() {
		m.shutdownWg.Wait()
		close(waitDone)
	}()

	select {
	case <-waitDone:
		m.logger.Info("All background goroutines exited cleanly")
	case <-time.After(3 * time.Second):
		m.logger.Warn("Background goroutines did not exit within 3 seconds, proceeding with shutdown")
	case <-ctx.Done():
		m.logger.Warn("Shutdown context cancelled while waiting for goroutines", zap.Error(ctx.Err()))
	}

	// Set shutdown flag to prevent any reconnection attempts
	m.mu.Lock()
	m.shuttingDown = true
	clientMap := make(map[string]*managed.Client, len(m.clients))
	for id, client := range m.clients {
		clientMap[id] = client
	}
	m.mu.Unlock()

	if len(clientMap) == 0 {
		m.logger.Debug("No upstream servers to shutdown")
		return nil
	}

	m.logger.Info("Disconnecting all upstream servers (in parallel)",
		zap.Int("count", len(clientMap)))

	// Disconnect all clients in parallel using goroutines
	// This ensures shutdown is fast even with many servers
	var wg sync.WaitGroup
	for id, client := range clientMap {
		if client == nil {
			continue
		}

		wg.Add(1)
		// Launch goroutine immediately without calling GetConfig() first
		// GetConfig() will be called inside goroutine but we'll use client.Disconnect()
		// which handles locking internally and won't block the shutdown loop
		go func(clientID string, c *managed.Client) {
			defer wg.Done()

			// Try to get server name, but use a fallback if it blocks
			// This prevents the entire shutdown from hanging on one stuck server
			serverName := clientID // Fallback to clientID
			if cfg := c.GetConfig(); cfg != nil {
				serverName = cfg.Name
			}

			m.logger.Debug("Disconnecting server",
				zap.String("id", clientID),
				zap.String("server", serverName))

			if err := c.Disconnect(); err != nil {
				m.logger.Warn("Error disconnecting server",
					zap.String("id", clientID),
					zap.String("server", serverName),
					zap.Error(err))
			} else {
				m.logger.Debug("Successfully disconnected server",
					zap.String("id", clientID),
					zap.String("server", serverName))
			}
		}(id, client)
	}

	// Wait for all disconnections to complete (with timeout)
	// Use a shorter timeout (5 seconds) for the disconnect phase
	// If servers are stuck in "Connecting" state, their disconnection will hang
	// In that case, we'll force-proceed to container cleanup which can handle it
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	disconnectTimeout := 5 * time.Second
	disconnectTimer := time.NewTimer(disconnectTimeout)
	defer disconnectTimer.Stop()

	select {
	case <-done:
		m.logger.Info("All upstream servers disconnected successfully")
	case <-disconnectTimer.C:
		m.logger.Warn("Disconnect phase timed out after 5 seconds, forcing cleanup")
		m.logger.Warn("Some servers may not have disconnected cleanly (likely stuck in Connecting state)")
		// Don't wait for stuck goroutines - proceed with container cleanup anyway
	case <-ctx.Done():
		m.logger.Warn("Shutdown context cancelled, forcing cleanup",
			zap.Error(ctx.Err()))
		// Don't wait for stuck goroutines - proceed with container cleanup anyway
	}

	// Additional cleanup: Find and stop ALL mcpproxy-managed containers
	// This catches any orphaned containers from previous crashes AND any containers
	// from servers that were stuck in "Connecting" state and couldn't disconnect
	m.logger.Info("Starting Docker container cleanup phase")
	m.cleanupAllManagedContainers(ctx)

	m.logger.Info("All upstream servers shut down successfully")
	return nil
}

// cleanupAllManagedContainers finds and stops all Docker containers managed by mcpproxy
// Uses labels to identify containers across all instances
func (m *Manager) cleanupAllManagedContainers(ctx context.Context) {
	m.logger.Info("Cleaning up all mcpproxy-managed Docker containers")

	// Find all containers with our management label
	listCmd := exec.CommandContext(ctx, "docker", "ps", "-a",
		"--filter", "label=com.mcpproxy.managed=true",
		"--format", "{{.ID}}\t{{.Names}}\t{{.Label \"com.mcpproxy.server\"}}")

	output, err := listCmd.Output()
	if err != nil {
		m.logger.Debug("No Docker containers found or Docker unavailable", zap.Error(err))
		return
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) == 0 || lines[0] == "" {
		m.logger.Debug("No mcpproxy-managed containers found")
		return
	}

	m.logger.Info("Found mcpproxy-managed containers to cleanup",
		zap.Int("count", len(lines)))

	// Grace period for graceful shutdown
	gracePeriod := 10 * time.Second
	graceCtx, graceCancel := context.WithTimeout(ctx, gracePeriod)
	defer graceCancel()

	containerIDs := []string{}
	for _, line := range lines {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) >= 1 {
			containerID := parts[0]
			containerName := ""
			serverName := ""
			if len(parts) >= 2 {
				containerName = parts[1]
			}
			if len(parts) >= 3 {
				serverName = parts[2]
			}

			m.logger.Info("Stopping container",
				zap.String("container_id", containerID),
				zap.String("container_name", containerName),
				zap.String("server", serverName))

			containerIDs = append(containerIDs, containerID)

			// Try graceful stop first
			stopCmd := exec.CommandContext(graceCtx, "docker", "stop", containerID)
			if err := stopCmd.Run(); err != nil {
				m.logger.Warn("Graceful stop failed, will force kill",
					zap.String("container_id", containerID),
					zap.Error(err))
			} else {
				m.logger.Info("Container stopped gracefully",
					zap.String("container_id", containerID))
			}
		}
	}

	// Force kill any remaining containers after grace period
	if graceCtx.Err() != nil || len(containerIDs) > 0 {
		m.logger.Info("Force killing any remaining containers")

		killCtx, killCancel := context.WithTimeout(ctx, 5*time.Second)
		defer killCancel()

		for _, containerID := range containerIDs {
			// Check if container is still running
			psCmd := exec.CommandContext(killCtx, "docker", "ps", "-q",
				"--filter", "id="+containerID)
			if output, err := psCmd.Output(); err == nil && len(strings.TrimSpace(string(output))) > 0 {
				// Still running, force kill
				m.logger.Info("Force killing container",
					zap.String("container_id", containerID))

				killCmd := exec.CommandContext(killCtx, "docker", "kill", containerID)
				if err := killCmd.Run(); err != nil {
					m.logger.Error("Failed to force kill container",
						zap.String("container_id", containerID),
						zap.Error(err))
				} else {
					m.logger.Info("Container force killed",
						zap.String("container_id", containerID))
				}
			}
		}
	}

	m.logger.Info("Container cleanup completed")
}

// ForceCleanupAllContainers is a public wrapper for emergency container cleanup
// This is called when graceful shutdown fails and containers must be force-removed
// Only removes containers owned by THIS instance (matching instance ID)
func (m *Manager) ForceCleanupAllContainers() {
	m.logger.Warn("Force cleanup requested - removing all managed containers for this instance")

	// Create a short-lived context for force cleanup (30 seconds max)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Find all containers with our management label AND our instance ID
	instanceID := core.GetInstanceID()
	listCmd := exec.CommandContext(ctx, "docker", "ps", "-a",
		"--filter", "label=com.mcpproxy.managed=true",
		"--filter", fmt.Sprintf("label=com.mcpproxy.instance=%s", instanceID),
		"--format", "{{.ID}}\t{{.Names}}")

	output, err := listCmd.Output()
	if err != nil {
		m.logger.Warn("Failed to list managed containers for force cleanup", zap.Error(err))
		return
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) == 0 || lines[0] == "" {
		m.logger.Info("No managed containers found during force cleanup")
		return
	}

	m.logger.Warn("Force removing managed containers",
		zap.Int("count", len(lines)))

	// Force remove each container (skip graceful stop)
	for _, line := range lines {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) < 1 {
			continue
		}

		containerID := parts[0]
		containerName := ""
		if len(parts) >= 2 {
			containerName = parts[1]
		}

		m.logger.Warn("Force removing container",
			zap.String("id", containerID[:12]),
			zap.String("name", containerName))

		// Use docker rm -f to force remove (kills and removes in one step)
		rmCmd := exec.CommandContext(ctx, "docker", "rm", "-f", containerID)
		if err := rmCmd.Run(); err != nil {
			m.logger.Error("Failed to force remove container",
				zap.String("id", containerID[:12]),
				zap.String("name", containerName),
				zap.Error(err))
		} else {
			m.logger.Info("Container force removed successfully",
				zap.String("id", containerID[:12]),
				zap.String("name", containerName))
		}
	}

	m.logger.Info("Force cleanup completed")
}

// forceCleanupClient forces cleanup of a specific client's Docker container
func (m *Manager) forceCleanupClient(client *managed.Client) {
	containerID := client.GetContainerID()
	if containerID == "" {
		m.logger.Debug("No container ID for force cleanup",
			zap.String("server", client.GetConfig().Name))
		return
	}

	m.logger.Warn("Force cleaning up container for client",
		zap.String("server", client.GetConfig().Name),
		zap.String("container_id", containerID[:12]))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Force remove container
	rmCmd := exec.CommandContext(ctx, "docker", "rm", "-f", containerID)
	if err := rmCmd.Run(); err != nil {
		m.logger.Error("Failed to force remove container",
			zap.String("server", client.GetConfig().Name),
			zap.String("container_id", containerID[:12]),
			zap.Error(err))
	} else {
		m.logger.Info("Container force removed successfully",
			zap.String("server", client.GetConfig().Name),
			zap.String("container_id", containerID[:12]))
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

// DiscoverTools discovers all tools from all connected upstream servers.
// Security: Tools from quarantined servers are NOT discovered to prevent
// Tool Poisoning Attacks (TPA) from exposing potentially malicious tool descriptions.
func (m *Manager) DiscoverTools(ctx context.Context) ([]*config.ToolMetadata, error) {
	type clientSnapshot struct {
		id          string
		name        string
		enabled     bool
		quarantined bool
		client      *managed.Client
	}

	m.mu.RLock()
	snapshots := make([]clientSnapshot, 0, len(m.clients))
	for id, client := range m.clients {
		name := ""
		quarantined := false
		if client != nil && client.Config != nil {
			name = client.Config.Name
			quarantined = client.Config.Quarantined
		}
		snapshots = append(snapshots, clientSnapshot{
			id:          id,
			name:        name,
			enabled:     client != nil && client.Config != nil && client.Config.Enabled,
			quarantined: quarantined,
			client:      client,
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

		// Security: Skip quarantined servers to prevent TPA exposure
		if snapshot.quarantined {
			m.logger.Debug("Skipping quarantined client for tool discovery",
				zap.String("id", snapshot.id),
				zap.String("server", snapshot.name))
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
	// Check if we're shutting down - prevent reconnections during shutdown
	m.mu.RLock()
	if m.shuttingDown {
		m.mu.RUnlock()
		m.logger.Debug("Skipping ConnectAll - manager is shutting down")
		return nil
	}
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

		// Skip clients where user explicitly logged out (waiting for manual re-login)
		if client.IsUserLoggedOut() {
			m.logger.Debug("Skipping client - user explicitly logged out, waiting for manual login",
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
	// Cancel shutdown context to stop OAuth event monitor and Docker recovery
	if m.shutdownCancel != nil {
		m.shutdownCancel()
	}

	// Wait for background goroutines to exit
	m.shutdownWg.Wait()

	m.mu.RLock()
	clients := make([]*managed.Client, 0, len(m.clients))
	for _, client := range m.clients {
		clients = append(clients, client)
	}
	m.mu.RUnlock()

	if len(clients) == 0 {
		m.logger.Debug("No clients to disconnect")
		return nil
	}

	m.logger.Info("Disconnecting all clients in parallel", zap.Int("count", len(clients)))

	// NEW: Disconnect all clients in PARALLEL for faster shutdown
	var wg sync.WaitGroup
	errChan := make(chan error, len(clients))

	for _, client := range clients {
		if client == nil {
			continue
		}

		wg.Add(1)
		go func(c *managed.Client) {
			defer wg.Done()

			serverName := c.GetConfig().Name
			m.logger.Debug("Disconnecting client", zap.String("server", serverName))

			// NEW: Create per-client timeout context (10 seconds max per client)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			// Try to disconnect with timeout
			done := make(chan error, 1)
			go func() {
				done <- c.Disconnect()
			}()

			select {
			case err := <-done:
				if err != nil {
					m.logger.Warn("Client disconnect failed",
						zap.String("server", serverName),
						zap.Error(err))
					errChan <- fmt.Errorf("disconnect %s: %w", serverName, err)
				} else {
					m.logger.Debug("Client disconnected successfully",
						zap.String("server", serverName))
				}
			case <-ctx.Done():
				m.logger.Error("Client disconnect timeout - forcing cleanup",
					zap.String("server", serverName))
				// Force cleanup for this client if it has a container
				if c.IsDockerCommand() {
					m.forceCleanupClient(c)
				}
				errChan <- fmt.Errorf("disconnect %s: timeout", serverName)
			}
		}(client)
	}

	// Wait for all disconnections to complete
	wg.Wait()
	close(errChan)

	// Collect errors
	var errs []error
	for err := range errChan {
		errs = append(errs, err)
	}

	m.logger.Info("All clients disconnected",
		zap.Int("total", len(clients)),
		zap.Int("errors", len(errs)))

	if len(errs) > 0 {
		return fmt.Errorf("disconnect errors: %v", errs)
	}
	return nil
}

// HasDockerContainers checks if any Docker containers owned by THIS instance are actually running
func (m *Manager) HasDockerContainers() bool {
	// Check if any containers with our labels AND our instance ID are running
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	instanceID := core.GetInstanceID()
	listCmd := exec.CommandContext(ctx, "docker", "ps", "-q",
		"--filter", "label=com.mcpproxy.managed=true",
		"--filter", fmt.Sprintf("label=com.mcpproxy.instance=%s", instanceID))

	output, err := listCmd.Output()
	if err != nil {
		// Docker not available or error listing - assume no containers
		return false
	}

	// If output is not empty, we have running containers
	containerIDs := strings.TrimSpace(string(output))
	return containerIDs != ""
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
		// Skip nil clients (can happen during shutdown)
		if client == nil {
			continue
		}

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

		if serverInfo := client.GetServerInfo(); serverInfo != nil && serverInfo.ProtocolVersion != "" {
			status["protocol_version"] = serverInfo.ProtocolVersion
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
	// Check if we're shutting down - prevent reconnections during shutdown
	if m.shuttingDown {
		m.mu.RUnlock()
		m.logger.Debug("Skipping RetryConnection - manager is shutting down",
			zap.String("server", serverName))
		return nil
	}
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

// verifyContainerHealthy checks if a Docker container is actually running and responsive
// This is critical for detecting "zombie" containers where the socket is open but the process is frozen
func (m *Manager) verifyContainerHealthy(client *managed.Client) (bool, error) {
	// Only check Docker-based servers
	if !client.IsDockerCommand() {
		return true, nil // Non-Docker servers don't need container health check
	}

	containerID := client.GetContainerID()
	if containerID == "" {
		return false, fmt.Errorf("no container ID available")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Check 1: Container exists and is running
	inspectCmd := exec.CommandContext(ctx, "docker", "inspect",
		"--format", "{{.State.Running}},{{.State.Status}}",
		containerID)

	output, err := inspectCmd.Output()
	if err != nil {
		return false, fmt.Errorf("container not found or unreachable: %w", err)
	}

	parts := strings.Split(strings.TrimSpace(string(output)), ",")
	if len(parts) < 2 {
		return false, fmt.Errorf("unexpected docker inspect output: %s", string(output))
	}

	running := parts[0] == "true"
	status := parts[1]

	if !running {
		return false, fmt.Errorf("container not running (status: %s)", status)
	}

	m.logger.Debug("Container health check passed",
		zap.String("container_id", containerID[:12]),
		zap.String("status", status))

	return true, nil
}

// ReconnectResult tracks the results of a ForceReconnectAll operation
type ReconnectResult struct {
	TotalServers      int
	AttemptedServers  int
	SuccessfulServers []string
	FailedServers     map[string]error
	SkippedServers    map[string]string // server name -> skip reason
}

// ForceReconnectAll triggers reconnection attempts for all managed clients.
// For Docker-based servers, this includes container health verification to catch frozen containers.
// Returns detailed results about which servers were reconnected, skipped, or failed.
func (m *Manager) ForceReconnectAll(reason string) *ReconnectResult {
	result := &ReconnectResult{
		SuccessfulServers: []string{},
		FailedServers:     make(map[string]error),
		SkippedServers:    make(map[string]string),
	}
	m.mu.RLock()
	clientMap := make(map[string]*managed.Client, len(m.clients))
	for id, client := range m.clients {
		clientMap[id] = client
	}
	m.mu.RUnlock()

	result.TotalServers = len(clientMap)

	if len(clientMap) == 0 {
		m.logger.Debug("ForceReconnectAll: no managed clients registered")
		return result
	}

	m.logger.Info("ForceReconnectAll: processing managed clients",
		zap.Int("client_count", len(clientMap)),
		zap.String("reason", reason))

	for id, client := range clientMap {
		if client == nil {
			result.SkippedServers[id] = "nil client"
			continue
		}

		// CRITICAL: For Docker servers, verify container health even if connected
		// This catches frozen containers when Docker is paused/resumed
		if client.IsConnected() {
			if client.IsDockerCommand() {
				healthy, err := m.verifyContainerHealthy(client)
				if !healthy {
					m.logger.Warn("Container unhealthy despite active connection - forcing reconnect",
						zap.String("server", id),
						zap.Error(err))
					// Fall through to reconnect logic
				} else {
					result.SkippedServers[id] = "container healthy"
					m.logger.Debug("Skipping reconnect - container healthy",
						zap.String("server", id))
					continue
				}
			} else {
				// Non-Docker server, connection state is sufficient
				result.SkippedServers[id] = "already connected"
				m.logger.Debug("Skipping reconnect - already connected",
					zap.String("server", id))
				continue
			}
		}

		// Filter: Only reconnect Docker-based servers (skip HTTP/SSE/non-Docker stdio)
		if !client.IsDockerCommand() {
			result.SkippedServers[id] = "not a Docker server"
			m.logger.Debug("Skipping reconnect - not a Docker server",
				zap.String("server", id),
				zap.String("reason", reason))
			continue
		}

		cfg := cloneServerConfig(client.GetConfig())
		if cfg == nil {
			result.SkippedServers[id] = "failed to clone config"
			continue
		}

		if !cfg.Enabled {
			result.SkippedServers[id] = "server disabled"
			m.logger.Debug("Skipping reconnect - server disabled",
				zap.String("server", id))
			continue
		}

		result.AttemptedServers++

		m.logger.Info("ForceReconnectAll: rebuilding Docker client",
			zap.String("server", cfg.Name),
			zap.String("id", id),
			zap.String("reason", reason))

		m.RemoveServer(id)

		// Small delay to allow container/process cleanup before restart
		time.Sleep(200 * time.Millisecond)

		if err := m.AddServer(id, cfg); err != nil {
			result.FailedServers[id] = err
			m.logger.Error("ForceReconnectAll: failed to rebuild client",
				zap.String("server", cfg.Name),
				zap.String("id", id),
				zap.Error(err))
		} else {
			result.SuccessfulServers = append(result.SuccessfulServers, id)
			m.logger.Info("ForceReconnectAll: successfully rebuilt client",
				zap.String("server", cfg.Name),
				zap.String("id", id))
		}
	}

	m.logger.Info("ForceReconnectAll completed",
		zap.Int("total", result.TotalServers),
		zap.Int("attempted", result.AttemptedServers),
		zap.Int("successful", len(result.SuccessfulServers)),
		zap.Int("failed", len(result.FailedServers)),
		zap.Int("skipped", len(result.SkippedServers)))

	return result
}

// startOAuthEventMonitor monitors the database for OAuth completion events from CLI processes
func (m *Manager) startOAuthEventMonitor(ctx context.Context) {
	defer m.shutdownWg.Done()
	m.logger.Info("Starting OAuth event monitor for cross-process notifications")

	ticker := time.NewTicker(5 * time.Second) // Check every 5 seconds
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			m.logger.Info("OAuth event monitor stopped due to context cancellation")
			return
		case <-ticker.C:
			// Check if context was cancelled before processing
			select {
			case <-ctx.Done():
				return
			default:
			}

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

// StartManualOAuthQuick starts OAuth and returns browser status immediately.
// Unlike StartManualOAuth (fully async, no result) or StartManualOAuthWithInfo (fully sync, blocks),
// this returns browser status quickly but continues the OAuth callback handling in background.
//
// This is the recommended method for API endpoints that need to return browser_opened status.
func (m *Manager) StartManualOAuthQuick(serverName string) (*core.OAuthStartResult, error) {
	m.mu.RLock()
	client, exists := m.clients[serverName]
	m.mu.RUnlock()
	if !exists {
		return nil, fmt.Errorf("server not found: %s", serverName)
	}

	cfg := client.Config
	m.logger.Info("Starting quick OAuth flow (returns browser status immediately)",
		zap.String("server", cfg.Name))

	// Preflight: if server does not appear to require OAuth, return error
	if !oauth.ShouldUseOAuth(cfg) {
		m.logger.Info("OAuth not applicable based on config", zap.String("server", cfg.Name))
		return nil, fmt.Errorf("OAuth is not supported or not required for server '%s'", cfg.Name)
	}

	// Create a transient core client that uses the daemon's storage
	coreClient, err := core.NewClientWithOptions(cfg.Name, cfg, m.logger, m.logConfig, m.globalConfig, m.storage, false, m.secretResolver)
	if err != nil {
		return nil, fmt.Errorf("failed to create core client for OAuth: %w", err)
	}

	// Use a long-running context for the OAuth callback (30 minutes)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)

	// Clear OAuth state for fresh flow
	coreClient.ClearOAuthState()

	// Start the quick OAuth flow - this returns immediately with browser status
	result, err := coreClient.StartOAuthFlowQuick(ctx)
	if err != nil {
		cancel()
		return result, err
	}

	// Set up reconnection after OAuth completes (in background)
	go func() {
		defer cancel()

		// Wait a bit for OAuth to complete (the callback handling runs in background)
		// Then trigger reconnect
		time.Sleep(2 * time.Second)

		// Check if OAuth completed by looking for token
		if m.storage != nil {
			serverKey := oauth.GenerateServerKey(cfg.Name, cfg.URL)
			token, _ := m.storage.GetOAuthToken(serverKey)
			if token != nil && token.AccessToken != "" {
				m.logger.Info("OAuth token obtained, triggering reconnect",
					zap.String("server", cfg.Name))
				if err := m.RetryConnection(cfg.Name); err != nil {
					m.logger.Warn("Failed to trigger reconnect after OAuth",
						zap.String("server", cfg.Name),
						zap.Error(err))
				}
			}
		}

		// Also set up a watcher for OAuth completion
		tokenManager := oauth.GetTokenStoreManager()
		for i := 0; i < 60; i++ { // Check for 2 minutes
			if tokenManager.HasRecentOAuthCompletion(cfg.Name) {
				m.logger.Info("OAuth completion detected, triggering reconnect",
					zap.String("server", cfg.Name))
				if err := m.RetryConnection(cfg.Name); err != nil {
					m.logger.Warn("Failed to trigger reconnect after OAuth completion",
						zap.String("server", cfg.Name),
						zap.Error(err))
				}
				return
			}
			time.Sleep(2 * time.Second)
		}
	}()

	return result, nil
}

// StartManualOAuthWithInfo performs an in-process OAuth flow and returns the auth URL and browser status.
// This is used by Phase 3 (Spec 020) to return structured information about the OAuth flow start.
// Unlike StartManualOAuth, this method waits for the auth URL to be obtained before returning.
func (m *Manager) StartManualOAuthWithInfo(serverName string, force bool) (*core.OAuthStartResult, error) {
	m.mu.RLock()
	client, exists := m.clients[serverName]
	m.mu.RUnlock()
	if !exists {
		return nil, fmt.Errorf("server not found: %s", serverName)
	}

	cfg := client.Config
	m.logger.Info("Starting in-process manual OAuth with info tracking",
		zap.String("server", cfg.Name),
		zap.Bool("force", force))

	// Preflight: if server does not appear to require OAuth, avoid starting
	if !oauth.ShouldUseOAuth(cfg) && !force {
		m.logger.Info("OAuth not applicable based on config (no headers, protocol)", zap.String("server", cfg.Name))
		return nil, fmt.Errorf("OAuth is not supported or not required for server '%s'", cfg.Name)
	}

	// Create a transient core client that uses the daemon's storage
	coreClient, err := core.NewClientWithOptions(cfg.Name, cfg, m.logger, m.logConfig, m.globalConfig, m.storage, false, m.secretResolver)
	if err != nil {
		return nil, fmt.Errorf("failed to create core client for OAuth: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)

	if force {
		coreClient.ClearOAuthState()
	}

	// Preflight no-auth check
	if !force {
		cpy := *cfg
		cpy.Headers = cfg.Headers
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
					cancel()
					return &core.OAuthStartResult{
						BrowserOpened: false,
						CorrelationID: fmt.Sprintf("oauth-%s-%d", cfg.Name, time.Now().UnixNano()),
					}, nil
				}
			}
		}
	}

	m.logger.Info("Triggering OAuth flow with result tracking (in-process)", zap.String("server", cfg.Name))

	// Run the OAuth flow synchronously to get the result
	result, err := coreClient.ForceOAuthFlowWithResult(ctx)
	cancel()

	if err != nil {
		m.logger.Warn("In-process OAuth flow failed",
			zap.String("server", cfg.Name),
			zap.Error(err))
		return result, err
	}

	m.logger.Info("In-process OAuth flow completed successfully",
		zap.String("server", cfg.Name),
		zap.String("correlation_id", result.CorrelationID))

	// Immediately attempt reconnect with new tokens
	if reconnErr := m.RetryConnection(cfg.Name); reconnErr != nil {
		m.logger.Warn("Failed to trigger reconnect after in-process OAuth",
			zap.String("server", cfg.Name),
			zap.Error(reconnErr))
	}

	return result, nil
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

// Docker Recovery Methods

// startDockerRecoveryMonitor monitors Docker availability and triggers recovery when needed
func (m *Manager) startDockerRecoveryMonitor(ctx context.Context) {
	defer m.shutdownWg.Done()
	m.logger.Info("Starting Docker recovery monitor")

	// Load existing recovery state (always persist for reliability)
	if m.storageMgr != nil {
		if state, err := m.storageMgr.LoadDockerRecoveryState(); err == nil && state != nil {
			m.dockerRecoveryMu.Lock()
			m.dockerRecoveryState = state
			m.dockerRecoveryMu.Unlock()
			m.logger.Info("Loaded existing Docker recovery state",
				zap.Int("failure_count", state.FailureCount),
				zap.Bool("docker_available", state.DockerAvailable),
				zap.Time("last_attempt", state.LastAttempt))
		}
	}

	// Initial check
	if err := m.checkDockerAvailability(ctx); err != nil {
		// Check if context was cancelled before logging
		if ctx.Err() != nil {
			return
		}

		m.logger.Warn("Docker unavailable on startup, starting recovery", zap.Error(err))
		go m.handleDockerUnavailable(ctx)
		return
	}

	// Periodic monitoring
	ticker := time.NewTicker(dockerCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			m.logger.Info("Docker recovery monitor shutting down")
			return
		case <-ticker.C:
			if err := m.checkDockerAvailability(ctx); err != nil {
				// Check if context was cancelled before logging
				if ctx.Err() != nil {
					return
				}

				m.logger.Warn("Docker became unavailable, starting recovery", zap.Error(err))
				go m.handleDockerUnavailable(ctx)
				return // Exit monitor, handleDockerUnavailable will restart it
			}
		}
	}
}

// checkDockerAvailability checks if Docker daemon is running and responsive
func (m *Manager) checkDockerAvailability(ctx context.Context) error {
	checkCtx, cancel := context.WithTimeout(ctx, dockerHealthCheckTimeout)
	defer cancel()

	cmd := exec.CommandContext(checkCtx, "docker", "info", "--format", "{{json .ServerVersion}}")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker unavailable: %w", err)
	}
	return nil
}

// handleDockerUnavailable handles Docker unavailability with exponential backoff
func (m *Manager) handleDockerUnavailable(ctx context.Context) {
	m.dockerRecoveryMu.Lock()
	if m.dockerRecoveryActive {
		m.dockerRecoveryMu.Unlock()
		return // Already in recovery
	}
	m.dockerRecoveryActive = true

	// Initialize state if needed
	if m.dockerRecoveryState == nil {
		m.dockerRecoveryState = &storage.DockerRecoveryState{
			LastAttempt:     time.Now(),
			FailureCount:    0,
			DockerAvailable: false,
			RecoveryMode:    true,
		}
	}
	m.dockerRecoveryMu.Unlock()

	defer func() {
		m.dockerRecoveryMu.Lock()
		m.dockerRecoveryActive = false
		m.dockerRecoveryMu.Unlock()
	}()

	// Use internal constants for retry logic
	maxRetries := dockerMaxRetries

	m.dockerRecoveryMu.RLock()
	attempt := m.dockerRecoveryState.FailureCount
	m.dockerRecoveryMu.RUnlock()

	m.logger.Info("Docker recovery started",
		zap.Int("resumed_from_attempt", attempt),
		zap.Int("max_retries", maxRetries))

	recoveryCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	m.dockerRecoveryMu.Lock()
	m.dockerRecoveryCancel = cancel
	m.dockerRecoveryMu.Unlock()

	startTime := time.Now()

	for {
		// Check if max retries exceeded
		if maxRetries > 0 && attempt >= maxRetries {
			m.logger.Error("Docker recovery max retries exceeded",
				zap.Int("attempts", attempt),
				zap.Int("max_retries", maxRetries))
			m.saveDockerRecoveryState(&storage.DockerRecoveryState{
				LastAttempt:      time.Now(),
				FailureCount:     attempt,
				DockerAvailable:  false,
				RecoveryMode:     false,
				LastError:        "max retries exceeded",
				AttemptsSinceUp:  attempt,
				LastSuccessfulAt: time.Time{},
			})
			return
		}

		// Get retry interval based on attempt number (exponential backoff)
		currentInterval := getDockerRetryInterval(attempt)

		select {
		case <-ctx.Done():
			return
		case <-time.After(currentInterval):
			// Check if context was cancelled while waiting
			if ctx.Err() != nil {
				return
			}

			attempt++

			err := m.checkDockerAvailability(recoveryCtx)
			if err == nil {
				// Check before logging success
				if ctx.Err() != nil {
					return
				}

				// Docker is back!
				elapsed := time.Since(startTime)
				m.logger.Info("Docker recovery successful",
					zap.Int("total_attempts", attempt),
					zap.Duration("total_duration", elapsed))

				// Clear recovery state
				m.dockerRecoveryMu.Lock()
				m.dockerRecoveryState = nil
				m.dockerRecoveryMu.Unlock()

				if m.storageMgr != nil {
					_ = m.storageMgr.ClearDockerRecoveryState()
				}

				// Trigger reconnection of Docker-based servers
				go func() {
					// Check if context was cancelled before reconnecting
					if ctx.Err() != nil {
						return
					}

					result := m.ForceReconnectAll("docker_recovered")

					// Check again before logging
					if ctx.Err() != nil {
						return
					}

					if len(result.FailedServers) > 0 {
						m.logger.Warn("Some servers failed to reconnect after Docker recovery",
							zap.Int("error_count", len(result.FailedServers)))
					} else {
						m.logger.Info("Successfully reconnected servers after Docker recovery",
							zap.Int("reconnected", len(result.SuccessfulServers)))
					}
				}()

				// Restart monitoring
				m.shutdownWg.Add(1)
				go m.startDockerRecoveryMonitor(ctx)
				return
			}

			// Check if context was cancelled before logging retry
			if ctx.Err() != nil {
				return
			}

			// Still unavailable, save state
			m.logger.Info("Docker recovery retry",
				zap.Int("attempt", attempt),
				zap.Duration("elapsed", time.Since(startTime)),
				zap.Duration("next_retry_in", getDockerRetryInterval(attempt)),
				zap.Error(err))

			m.saveDockerRecoveryState(&storage.DockerRecoveryState{
				LastAttempt:      time.Now(),
				FailureCount:     attempt,
				DockerAvailable:  false,
				RecoveryMode:     true,
				LastError:        err.Error(),
				AttemptsSinceUp:  attempt,
				LastSuccessfulAt: time.Time{},
			})
		}
	}
}

// saveDockerRecoveryState saves recovery state to persistent storage
func (m *Manager) saveDockerRecoveryState(state *storage.DockerRecoveryState) {
	m.dockerRecoveryMu.Lock()
	m.dockerRecoveryState = state
	m.dockerRecoveryMu.Unlock()

	if m.storageMgr != nil {
		if err := m.storageMgr.SaveDockerRecoveryState(state); err != nil {
			m.logger.Warn("Failed to save Docker recovery state", zap.Error(err))
		}
	}
}

// GetDockerRecoveryStatus returns the current Docker recovery status
func (m *Manager) GetDockerRecoveryStatus() *storage.DockerRecoveryState {
	// Short-circuit when Docker recovery is disabled (e.g., docker isolation off)
	if !m.shouldEnableDockerRecovery() {
		return &storage.DockerRecoveryState{
			LastAttempt:      time.Now(),
			FailureCount:     0,
			DockerAvailable:  true,  // Considered available because Docker isn't required
			RecoveryMode:     false, // Never enter recovery when disabled
			LastSuccessfulAt: time.Now(),
		}
	}

	m.dockerRecoveryMu.RLock()
	defer m.dockerRecoveryMu.RUnlock()

	if m.dockerRecoveryState == nil {
		// Check current Docker availability
		if err := m.checkDockerAvailability(context.Background()); err == nil {
			return &storage.DockerRecoveryState{
				LastAttempt:      time.Now(),
				FailureCount:     0,
				DockerAvailable:  true,
				RecoveryMode:     false,
				LastSuccessfulAt: time.Now(),
			}
		}
		return &storage.DockerRecoveryState{
			LastAttempt:     time.Now(),
			FailureCount:    0,
			DockerAvailable: false,
			RecoveryMode:    false,
		}
	}

	// Return copy of current state
	stateCopy := *m.dockerRecoveryState
	return &stateCopy
}

// ClearOAuthToken clears the OAuth token for a specific server.
// This is called by the OAuth logout flow to remove stored credentials.
// Works for both explicit OAuth config and discovered OAuth (server-announced OAuth).
func (m *Manager) ClearOAuthToken(serverName string) error {
	m.mu.RLock()
	client, exists := m.clients[serverName]
	m.mu.RUnlock()

	if !exists {
		return fmt.Errorf("server not found: %s", serverName)
	}

	// Get the server config for logging (but don't require explicit OAuth config)
	serverConfig := client.GetConfig()
	hasExplicitOAuth := serverConfig != nil && serverConfig.OAuth != nil

	// Clear token from persistent storage
	// This works for both explicit OAuth config and discovered OAuth (server-announced)
	if m.storageMgr != nil {
		if err := m.storageMgr.ClearOAuthState(serverName); err != nil {
			m.logger.Warn("Failed to clear OAuth state from storage",
				zap.String("server", serverName),
				zap.Bool("has_explicit_oauth", hasExplicitOAuth),
				zap.Error(err))
			// Continue - storage might not have the token
		}
	}

	// Note: We don't need to clear in-memory OAuth state on the managed client
	// because the managed client will pick up the cleared state from storage
	// on the next connection attempt via DisconnectServer + reconnection flow.

	m.logger.Info("OAuth token cleared",
		zap.String("server", serverName),
		zap.Bool("has_explicit_oauth", hasExplicitOAuth))

	return nil
}

// DisconnectServer disconnects a specific server without removing its configuration.
// This is used after OAuth logout to force re-authentication on next connect.
func (m *Manager) DisconnectServer(serverName string) error {
	m.mu.RLock()
	client, exists := m.clients[serverName]
	m.mu.RUnlock()

	if !exists {
		return fmt.Errorf("server not found: %s", serverName)
	}

	// Disconnect the client
	if err := client.Disconnect(); err != nil {
		m.logger.Warn("Error during server disconnect",
			zap.String("server", serverName),
			zap.Error(err))
		// Continue - we want to mark as disconnected regardless
	}

	m.logger.Info("Server disconnected",
		zap.String("server", serverName))

	return nil
}

// SetUserLoggedOut marks a server as explicitly logged out by the user.
// This prevents automatic reconnection until the user explicitly logs in again.
func (m *Manager) SetUserLoggedOut(serverName string, loggedOut bool) error {
	m.mu.RLock()
	client, exists := m.clients[serverName]
	m.mu.RUnlock()

	if !exists {
		return fmt.Errorf("server not found: %s", serverName)
	}

	client.SetUserLoggedOut(loggedOut)

	m.logger.Info("Server user logged out state changed",
		zap.String("server", serverName),
		zap.Bool("logged_out", loggedOut))

	return nil
}

// RefreshOAuthToken triggers a token refresh for a specific server.
// This is called by the RefreshManager for proactive token refresh.
// TODO: This will be fully implemented in Phase 3 (US1) with RefreshManager integration.
func (m *Manager) RefreshOAuthToken(serverName string) error {
	m.mu.RLock()
	client, exists := m.clients[serverName]
	m.mu.RUnlock()

	if !exists {
		return fmt.Errorf("server not found: %s", serverName)
	}

	// Get the server config to check if it uses OAuth
	serverConfig := client.GetConfig()
	if serverConfig == nil || serverConfig.OAuth == nil {
		return fmt.Errorf("server does not use OAuth: %s", serverName)
	}

	// TODO: Implement actual token refresh in Phase 3
	// For now, force a reconnection which will trigger token refresh if needed
	client.ForceReconnect("manual_token_refresh")

	m.logger.Info("OAuth token refresh requested",
		zap.String("server", serverName))

	return nil
}
