package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/server"
	"go.uber.org/zap"

	"mcpproxy-go/internal/cache"
	"mcpproxy-go/internal/config"
	"mcpproxy-go/internal/index"
	"mcpproxy-go/internal/storage"
	"mcpproxy-go/internal/truncate"
	"mcpproxy-go/internal/upstream"
)

// Status represents the current status of the server
type Status struct {
	Phase         string                 `json:"phase"`          // Starting, Ready, Error
	Message       string                 `json:"message"`        // Human readable status message
	UpstreamStats map[string]interface{} `json:"upstream_stats"` // Upstream server statistics
	ToolsIndexed  int                    `json:"tools_indexed"`  // Number of tools indexed
	LastUpdated   time.Time              `json:"last_updated"`
}

// Server wraps the MCP proxy server with all its dependencies
type Server struct {
	config          *config.Config
	logger          *zap.Logger
	storageManager  *storage.Manager
	indexManager    *index.Manager
	upstreamManager *upstream.Manager
	cacheManager    *cache.Manager
	truncator       *truncate.Truncator
	mcpProxy        *MCPProxyServer

	// Server control
	httpServer *http.Server
	running    bool
	mu         sync.RWMutex

	// Separate contexts for different lifecycles
	appCtx       context.Context    // Application-wide context (only cancelled on shutdown)
	appCancel    context.CancelFunc // Application-wide cancel function
	serverCtx    context.Context    // HTTP server context (cancelled on stop/start)
	serverCancel context.CancelFunc // HTTP server cancel function
	shutdown     bool               // Guard against double shutdown

	// Status reporting
	status   Status
	statusMu sync.RWMutex
	statusCh chan Status
}

// NewServer creates a new server instance
func NewServer(cfg *config.Config, logger *zap.Logger) (*Server, error) {
	// Initialize storage manager
	storageManager, err := storage.NewManager(cfg.DataDir, logger.Sugar())
	if err != nil {
		return nil, fmt.Errorf("failed to initialize storage manager: %w", err)
	}

	// Initialize index manager
	indexManager, err := index.NewManager(cfg.DataDir, logger)
	if err != nil {
		storageManager.Close()
		return nil, fmt.Errorf("failed to initialize index manager: %w", err)
	}

	// Initialize upstream manager
	upstreamManager := upstream.NewManager(logger)

	// Initialize cache manager
	cacheManager, err := cache.NewManager(storageManager.GetDB(), logger)
	if err != nil {
		storageManager.Close()
		indexManager.Close()
		return nil, fmt.Errorf("failed to initialize cache manager: %w", err)
	}

	// Initialize truncator
	truncator := truncate.NewTruncator(cfg.ToolResponseLimit)

	// Create a context that will be used for background operations
	ctx, cancel := context.WithCancel(context.Background())

	server := &Server{
		config:          cfg,
		logger:          logger,
		storageManager:  storageManager,
		indexManager:    indexManager,
		upstreamManager: upstreamManager,
		cacheManager:    cacheManager,
		truncator:       truncator,
		appCtx:          ctx,
		appCancel:       cancel,
		statusCh:        make(chan Status, 10), // Buffered channel for status updates
		status: Status{
			Phase:       "Initializing",
			Message:     "Server is initializing...",
			LastUpdated: time.Now(),
		},
	}

	// Set the status change callback now that 'server' is created
	upstreamManager.SetOnStatusChangeCallback(func(serverID string) {
		logger.Info("Detected status change for server, forcing menu update.", zap.String("server_id", serverID))
		server.ForceMenuUpdate()
	})

	// Create MCP proxy server
	mcpProxy := NewMCPProxyServer(storageManager, indexManager, upstreamManager, cacheManager, truncator, logger, server, cfg.DebugSearch, cfg)

	server.mcpProxy = mcpProxy

	return server, nil
}

// GetStatus returns the current server status
func (s *Server) GetStatus() interface{} {
	s.statusMu.RLock()
	defer s.statusMu.RUnlock()
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Create a map representation of the status for the tray
	statusMap := map[string]interface{}{
		"running":        s.running,
		"listen_addr":    s.GetListenAddress(),
		"phase":          s.status.Phase,
		"message":        s.status.Message,
		"upstream_stats": s.status.UpstreamStats,
		"tools_indexed":  s.status.ToolsIndexed,
		"last_updated":   s.status.LastUpdated,
	}

	return statusMap
}

// StatusChannel returns a channel that receives status updates
func (s *Server) StatusChannel() <-chan interface{} {
	// Create a new channel that converts Status to interface{}
	ch := make(chan interface{}, 10)
	go func() {
		defer close(ch)
		for status := range s.statusCh {
			ch <- status
		}
	}()
	return ch
}

// updateStatus updates the current status and notifies subscribers
func (s *Server) updateStatus(phase, message string) {
	s.statusMu.Lock()
	s.status.Phase = phase
	s.status.Message = message
	s.status.LastUpdated = time.Now()
	s.status.UpstreamStats = s.upstreamManager.GetStats()
	s.status.ToolsIndexed = s.getIndexedToolCount()
	status := s.status
	s.statusMu.Unlock()

	// Non-blocking send to status channel
	select {
	case s.statusCh <- status:
	default:
		// If channel is full, skip this update
	}

	s.logger.Info("Status updated", zap.String("phase", phase), zap.String("message", message))
}

// getIndexedToolCount returns the number of indexed tools
func (s *Server) getIndexedToolCount() int {
	stats := s.upstreamManager.GetStats()
	if totalTools, ok := stats["total_tools"].(int); ok {
		return totalTools
	}
	return 0
}

// backgroundConnections handles connecting to upstream servers with retry logic
func (s *Server) backgroundConnections(ctx context.Context) {
	// Start periodic reconnection attempts for failed connections
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.logger.Info("Performing periodic check for disconnected servers...")
			s.connectAllWithRetry(ctx)
		case <-ctx.Done():
			s.logger.Info("Background connections stopped due to context cancellation")
			return
		}
	}
}

// connectAllWithRetry attempts to connect to all servers with exponential backoff
func (s *Server) connectAllWithRetry(ctx context.Context) {
	s.mu.RLock()
	isRunning := s.running
	s.mu.RUnlock()

	// Only update status to "Connecting" if server is not running
	// If server is running, don't override the "Running" status
	if !isRunning {
		stats := s.upstreamManager.GetStats()
		connectedCount := 0
		totalCount := 0
		if serverStats, ok := stats["servers"].(map[string]interface{}); ok {
			totalCount = len(serverStats)
			for _, serverStat := range serverStats {
				if stat, ok := serverStat.(map[string]interface{}); ok {
					if connected, ok := stat["connected"].(bool); ok && connected {
						connectedCount++
					}
				}
			}
		}
		s.updateStatus("Connecting", fmt.Sprintf("Connected to %d/%d servers, retrying...", connectedCount, totalCount))
	}

	// Try to connect with timeout
	connectCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	if err := s.upstreamManager.ConnectAll(connectCtx); err != nil {
		s.logger.Warn("Some upstream servers failed to connect", zap.Error(err))
	}
}

// backgroundToolIndexing handles tool discovery and indexing
func (s *Server) backgroundToolIndexing(ctx context.Context) {
	// Re-index every 5 minutes
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.logger.Info("Performing periodic tool re-indexing...")
			_ = s.discoverAndIndexTools(ctx)
		case <-ctx.Done():
			s.logger.Info("Background tool indexing stopped due to context cancellation")
			return
		}
	}
}

// loadConfiguredServers synchronizes the storage and upstream manager from the current config.
// This is the source of truth when configuration is loaded from disk.
//
//nolint:unparam // function designed to be best-effort, always returns nil by design
func (s *Server) loadConfiguredServers() error {
	s.logger.Info("Synchronizing servers from configuration (config as source of truth)")

	// Get current state for comparison
	currentUpstreams := s.upstreamManager.GetAllServerNames()
	storedServers, err := s.storageManager.ListUpstreamServers()
	if err != nil {
		s.logger.Error("Failed to get stored servers for sync", zap.Error(err))
		storedServers = []*config.ServerConfig{} // Continue with empty list
	}

	// Create maps for efficient lookups
	configuredServers := make(map[string]*config.ServerConfig)
	storedServerMap := make(map[string]*config.ServerConfig)

	for _, serverCfg := range s.config.Servers {
		configuredServers[serverCfg.Name] = serverCfg
	}

	for _, storedServer := range storedServers {
		storedServerMap[storedServer.Name] = storedServer
	}

	// Sync config to storage and upstream manager
	for _, serverCfg := range s.config.Servers {
		// Check if server state has changed
		storedServer, existsInStorage := storedServerMap[serverCfg.Name]
		hasChanged := !existsInStorage ||
			storedServer.Enabled != serverCfg.Enabled ||
			storedServer.Quarantined != serverCfg.Quarantined ||
			storedServer.URL != serverCfg.URL ||
			storedServer.Command != serverCfg.Command ||
			storedServer.Protocol != serverCfg.Protocol

		if hasChanged {
			s.logger.Info("Server configuration changed, updating storage",
				zap.String("server", serverCfg.Name),
				zap.Bool("new", !existsInStorage),
				zap.Bool("enabled_changed", existsInStorage && storedServer.Enabled != serverCfg.Enabled),
				zap.Bool("quarantined_changed", existsInStorage && storedServer.Quarantined != serverCfg.Quarantined))
		}

		// Always sync config to storage (ensures consistency)
		if err := s.storageManager.SaveUpstreamServer(serverCfg); err != nil {
			s.logger.Error("Failed to save/update server in storage", zap.Error(err), zap.String("server", serverCfg.Name))
			continue
		}

		// Sync to upstream manager based on enabled status
		if serverCfg.Enabled {
			// Add server to upstream manager regardless of quarantine status
			// Quarantined servers are kept connected for inspection but blocked for execution
			if err := s.upstreamManager.AddServer(serverCfg.Name, serverCfg); err != nil {
				s.logger.Error("Failed to add/update upstream server", zap.Error(err), zap.String("server", serverCfg.Name))
			} else if hasChanged && existsInStorage && !storedServer.Enabled && serverCfg.Enabled {
				// Only trigger immediate connection if server was specifically enabled (not during startup)
				go func(serverName string) {
					s.logger.Info("Server was enabled, attempting immediate connection", zap.String("server", serverName))
					connectCtx, cancel := context.WithTimeout(s.serverCtx, 5*time.Second)
					defer cancel()
					// Try to connect this specific server immediately
					if client, exists := s.upstreamManager.GetClient(serverName); exists {
						if err := client.Connect(connectCtx); err != nil {
							s.logger.Warn("Immediate connection attempt failed", zap.String("server", serverName), zap.Error(err))
						} else {
							s.logger.Info("Server connected successfully", zap.String("server", serverName))
						}
					}
				}(serverCfg.Name)
			}

			if serverCfg.Quarantined {
				s.logger.Info("Server is quarantined but kept connected for security inspection", zap.String("server", serverCfg.Name))
			}
		} else {
			// Remove from upstream manager only if disabled (not quarantined)
			s.upstreamManager.RemoveServer(serverCfg.Name)
			s.logger.Info("Server is disabled, removing from active connections", zap.String("server", serverCfg.Name))
		}
	}

	// Remove servers that are no longer in config (comprehensive cleanup)
	serversToRemove := []string{}

	// Check upstream manager
	for _, serverName := range currentUpstreams {
		if _, exists := configuredServers[serverName]; !exists {
			serversToRemove = append(serversToRemove, serverName)
		}
	}

	// Check storage for orphaned servers
	for _, storedServer := range storedServers {
		if _, exists := configuredServers[storedServer.Name]; !exists {
			// Add to removal list if not already there
			found := false
			for _, name := range serversToRemove {
				if name == storedServer.Name {
					found = true
					break
				}
			}
			if !found {
				serversToRemove = append(serversToRemove, storedServer.Name)
			}
		}
	}

	// Perform comprehensive cleanup for removed servers
	for _, serverName := range serversToRemove {
		s.logger.Info("Removing server no longer in config", zap.String("server", serverName))

		// Remove from upstream manager
		s.upstreamManager.RemoveServer(serverName)

		// Remove from storage
		if err := s.storageManager.DeleteUpstreamServer(serverName); err != nil {
			s.logger.Error("Failed to delete server from storage", zap.Error(err), zap.String("server", serverName))
		}

		// Remove tools from search index
		if err := s.indexManager.DeleteServerTools(serverName); err != nil {
			s.logger.Error("Failed to delete server tools from index", zap.Error(err), zap.String("server", serverName))
		} else {
			s.logger.Info("Removed server tools from search index", zap.String("server", serverName))
		}
	}

	if len(serversToRemove) > 0 {
		s.logger.Info("Comprehensive server cleanup completed",
			zap.Int("removed_count", len(serversToRemove)),
			zap.Strings("removed_servers", serversToRemove))
	}

	s.logger.Info("Server synchronization completed",
		zap.Int("configured_servers", len(s.config.Servers)),
		zap.Int("removed_servers", len(serversToRemove)))

	return nil
}

// Start starts the MCP proxy server and performs synchronous initialization.
func (s *Server) Start(ctx context.Context) error {
	s.logger.Info("Starting MCP proxy server...")

	// --- Synchronous Initialization ---
	s.updateStatus("Loading", "Loading server configurations...")
	if err := s.loadConfiguredServers(); err != nil {
		s.logger.Error("Failed to load configured servers during startup", zap.Error(err))
		s.updateStatus("Error", fmt.Sprintf("Failed to load servers: %v", err))
		// We still try to continue
	}

	s.updateStatus("Connecting", "Performing initial connection to upstream servers...")
	s.connectAllWithRetry(ctx) // First connection attempt
	s.logger.Info("Initial connection attempt finished.")

	s.updateStatus("Indexing", "Discovering and indexing tools...")
	if err := s.discoverAndIndexTools(ctx); err != nil {
		s.logger.Error("Initial tool discovery failed", zap.Error(err))
		s.updateStatus("Warning", "Initial tool discovery failed. Will retry.")
	}
	s.logger.Info("Initial tool indexing finished.")
	// --- End of Synchronous Initialization ---

	// Start background tasks for periodic checks
	go s.backgroundConnections(s.appCtx)
	go s.backgroundToolIndexing(s.appCtx)

	// Handle graceful shutdown when context is cancelled (for full application shutdown only)
	go func() {
		<-ctx.Done()
		s.logger.Info("Main context cancelled, shutting down server")
		// First shutdown the HTTP server
		if err := s.StopServer(); err != nil {
			s.logger.Error("Error stopping server during context cancellation", zap.Error(err))
		}
		// Then shutdown the rest (only for full application shutdown, not server restarts)
		// We distinguish this by checking if the cancelled context is the application context
		s.mu.Lock()
		alreadyShutdown := s.shutdown
		isAppContext := (ctx == s.appCtx)
		s.mu.Unlock()

		if !alreadyShutdown && isAppContext {
			s.logger.Info("Application context cancelled, performing full shutdown")
			if err := s.Shutdown(); err != nil {
				s.logger.Error("Error during context-triggered shutdown", zap.Error(err))
			}
		} else if !isAppContext {
			s.logger.Info("Server context cancelled, server stop completed")
		}
	}()

	// Determine transport mode based on listen address
	if s.config.Listen != "" && s.config.Listen != ":0" {
		// Start the MCP server in HTTP mode (Streamable HTTP)
		s.logger.Info("Starting MCP server",
			zap.String("transport", "streamable-http"),
			zap.String("listen", s.config.Listen))

		// Update status to show server is now running
		s.updateStatus("Running", fmt.Sprintf("Server is running on %s", s.config.Listen))

		// Create Streamable HTTP server with custom routing
		streamableServer := server.NewStreamableHTTPServer(s.mcpProxy.GetMCPServer())

		// Create custom HTTP server for handling multiple routes
		if err := s.startCustomHTTPServer(streamableServer); err != nil {
			return fmt.Errorf("MCP Streamable HTTP server error: %w", err)
		}
	} else {
		// Start the MCP server in stdio mode
		s.logger.Info("Starting MCP server", zap.String("transport", "stdio"))

		// Update status to show server is now running
		s.updateStatus("Running", "Server is running in stdio mode")

		// Serve using stdio (standard MCP transport)
		if err := server.ServeStdio(s.mcpProxy.GetMCPServer()); err != nil {
			return fmt.Errorf("MCP server error: %w", err)
		}
	}

	return nil
}

// discoverAndIndexTools discovers tools from upstream servers and indexes them
func (s *Server) discoverAndIndexTools(ctx context.Context) error {
	s.logger.Info("Discovering and indexing tools...")

	tools, err := s.upstreamManager.DiscoverTools(ctx)
	if err != nil {
		return fmt.Errorf("failed to discover tools: %w", err)
	}

	if len(tools) == 0 {
		s.logger.Warn("No tools discovered from upstream servers")
		return nil
	}

	// Index tools
	if err := s.indexManager.BatchIndexTools(tools); err != nil {
		return fmt.Errorf("failed to index tools: %w", err)
	}

	s.logger.Info("Successfully indexed tools", zap.Int("count", len(tools)))
	return nil
}

// Shutdown gracefully shuts down the server
func (s *Server) Shutdown() error {
	s.mu.Lock()
	if s.shutdown {
		s.mu.Unlock()
		s.logger.Info("Server already shutdown, skipping")
		return nil
	}
	s.shutdown = true
	s.mu.Unlock()

	s.logger.Info("Shutting down MCP proxy server...")

	// Cancel the server context to stop all background operations
	if s.appCancel != nil {
		s.logger.Info("Cancelling server context to stop background operations")
		s.appCancel()
	}

	// Disconnect upstream servers
	if err := s.upstreamManager.DisconnectAll(); err != nil {
		s.logger.Error("Failed to disconnect upstream servers", zap.Error(err))
	}

	// Close managers
	if s.cacheManager != nil {
		s.cacheManager.Close()
	}

	if err := s.indexManager.Close(); err != nil {
		s.logger.Error("Failed to close index manager", zap.Error(err))
	}

	if err := s.storageManager.Close(); err != nil {
		s.logger.Error("Failed to close storage manager", zap.Error(err))
	}

	s.logger.Info("MCP proxy server shutdown complete")
	return nil
}

// IsRunning returns whether the server is currently running
func (s *Server) IsRunning() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.running
}

// GetListenAddress returns the address the server is listening on
func (s *Server) GetListenAddress() string {
	return s.config.Listen
}

// GetUpstreamStats returns statistics about upstream servers
func (s *Server) GetUpstreamStats() map[string]interface{} {
	stats := s.upstreamManager.GetStats()

	// Enhance stats with tool counts per server
	if servers, ok := stats["servers"].(map[string]interface{}); ok {
		for id, serverInfo := range servers {
			if serverMap, ok := serverInfo.(map[string]interface{}); ok {
				// Get tool count for this server
				toolCount := s.getServerToolCount(id)
				serverMap["tool_count"] = toolCount
			}
		}
	}

	return stats
}

// GetAllServers returns information about all upstream servers for tray UI
func (s *Server) GetAllServers() ([]map[string]interface{}, error) {
	// Check if storage manager is available
	if s.storageManager == nil {
		return []map[string]interface{}{}, nil
	}

	servers, err := s.storageManager.ListUpstreamServers()
	if err != nil {
		// Handle database closed gracefully
		if strings.Contains(err.Error(), "database not open") || strings.Contains(err.Error(), "closed") {
			s.logger.Debug("Database not available for GetAllServers, returning empty list")
			return []map[string]interface{}{}, nil
		}
		return nil, err
	}

	var result []map[string]interface{}
	for _, server := range servers {
		// Get connection status and tool count from upstream manager
		var connected bool
		var connecting bool
		var lastError string
		var toolCount int

		if s.upstreamManager != nil {
			if client, exists := s.upstreamManager.GetClient(server.Name); exists {
				connectionStatus := client.GetConnectionStatus()
				if c, ok := connectionStatus["connected"].(bool); ok {
					connected = c
				}
				if c, ok := connectionStatus["connecting"].(bool); ok {
					connecting = c
				}
				if e, ok := connectionStatus["last_error"].(string); ok {
					lastError = e
				}

				if connected {
					toolCount = s.getServerToolCount(server.Name)
				}
			}
		}

		result = append(result, map[string]interface{}{
			"name":        server.Name,
			"url":         server.URL,
			"command":     server.Command,
			"protocol":    server.Protocol,
			"enabled":     server.Enabled,
			"quarantined": server.Quarantined,
			"created":     server.Created,
			"connected":   connected,
			"connecting":  connecting,
			"tool_count":  toolCount,
			"last_error":  lastError,
		})
	}

	return result, nil
}

// GetQuarantinedServers returns information about quarantined servers for tray UI
func (s *Server) GetQuarantinedServers() ([]map[string]interface{}, error) {
	// Check if storage manager is available
	if s.storageManager == nil {
		return []map[string]interface{}{}, nil
	}

	quarantinedServers, err := s.storageManager.ListQuarantinedUpstreamServers()
	if err != nil {
		// Handle database closed gracefully
		if strings.Contains(err.Error(), "database not open") || strings.Contains(err.Error(), "closed") {
			s.logger.Debug("Database not available for GetQuarantinedServers, returning empty list")
			return []map[string]interface{}{}, nil
		}
		return nil, err
	}

	var result []map[string]interface{}
	for _, server := range quarantinedServers {
		result = append(result, map[string]interface{}{
			"name":        server.Name,
			"url":         server.URL,
			"command":     server.Command,
			"protocol":    server.Protocol,
			"enabled":     server.Enabled,
			"quarantined": server.Quarantined,
			"created":     server.Created,
		})
	}

	return result, nil
}

// UnquarantineServer removes a server from quarantine via tray UI
func (s *Server) UnquarantineServer(serverName string) error {
	return s.QuarantineServer(serverName, false)
}

// EnableServer enables/disables a server and ensures all state is synchronized.
// It acts as the entry point for changes originating from the UI or API.
func (s *Server) EnableServer(serverName string, enabled bool) error {
	s.logger.Info("Request to change server enabled state",
		zap.String("server", serverName),
		zap.Bool("enabled", enabled))

	// First, update the authoritative record in storage.
	if err := s.storageManager.EnableUpstreamServer(serverName, enabled); err != nil {
		s.logger.Error("Failed to update server enabled state in storage", zap.Error(err))
		return fmt.Errorf("failed to update server '%s' in storage: %w", serverName, err)
	}

	// Now that storage is updated, save the configuration to disk.
	// This ensures the file reflects the authoritative state.
	if err := s.SaveConfiguration(); err != nil {
		s.logger.Error("Failed to save configuration after state change", zap.Error(err))
		// Don't return here; the primary state is updated. The file watcher will eventually sync.
	}

	// The file watcher in the tray will detect the change to the config file and
	// trigger ReloadConfiguration(), which calls loadConfiguredServers().
	// This completes the loop by updating the running state (upstreamManager) from the new config.
	s.logger.Info("Successfully persisted server state change. Relying on file watcher to sync running state.",
		zap.String("server", serverName))

	return nil
}

// QuarantineServer quarantines/unquarantines a server
func (s *Server) QuarantineServer(serverName string, quarantined bool) error {
	s.logger.Info("Request to change server quarantine state",
		zap.String("server", serverName),
		zap.Bool("quarantined", quarantined))

	if err := s.storageManager.QuarantineUpstreamServer(serverName, quarantined); err != nil {
		s.logger.Error("Failed to update server quarantine state in storage", zap.Error(err))
		return fmt.Errorf("failed to update quarantine state for server '%s' in storage: %w", serverName, err)
	}

	if err := s.SaveConfiguration(); err != nil {
		s.logger.Error("Failed to save configuration after quarantine state change", zap.Error(err))
	}

	s.logger.Info("Successfully persisted server quarantine state change. Relying on file watcher to sync running state.",
		zap.String("server", serverName))

	return nil
}

// DeleteServer deletes a server from the configuration
func (s *Server) DeleteServer(serverName string) error {
	s.logger.Info("Request to delete server", zap.String("server", serverName))

	// First, remove from storage
	if err := s.storageManager.DeleteUpstreamServer(serverName); err != nil {
		s.logger.Error("Failed to remove server from storage", zap.Error(err))
		return fmt.Errorf("failed to remove server '%s' from storage: %w", serverName, err)
	}

	// Remove from upstream manager if it exists
	if s.upstreamManager != nil {
		s.upstreamManager.RemoveServer(serverName)
	}

	// Remove tools from search index
	if s.indexManager != nil {
		if err := s.indexManager.DeleteServerTools(serverName); err != nil {
			s.logger.Error("Failed to remove server tools from index", zap.String("server", serverName), zap.Error(err))
		} else {
			s.logger.Info("Removed server tools from search index", zap.String("server", serverName))
		}
	}

	// Save configuration to persist the changes
	if err := s.SaveConfiguration(); err != nil {
		s.logger.Error("Failed to save configuration after server deletion", zap.Error(err))
	}

	s.logger.Info("Successfully deleted server", zap.String("server", serverName))

	return nil
}

// getServerToolCount returns the number of tools for a specific server
func (s *Server) getServerToolCount(serverID string) int {
	client, exists := s.upstreamManager.GetClient(serverID)
	if !exists || !client.IsConnected() {
		return 0
	}

	ctx := context.Background()
	tools, err := client.ListTools(ctx)
	if err != nil {
		s.logger.Warn("Failed to get tool count for server",
			zap.String("server_id", serverID),
			zap.Error(err))
		return 0
	}

	return len(tools)
}

// StartServer starts the server if it's not already running
func (s *Server) StartServer(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running {
		return fmt.Errorf("server is already running")
	}

	// Cancel the old context before creating a new one to avoid race conditions
	if s.serverCancel != nil {
		s.serverCancel()
	}

	s.serverCtx, s.serverCancel = context.WithCancel(ctx)

	go func() {
		var serverError error

		defer func() {
			s.mu.Lock()
			s.running = false
			s.mu.Unlock()

			// Only send "Stopped" status if there was no error
			// If there was an error, the error status should remain
			if serverError == nil || serverError == context.Canceled {
				s.updateStatus("Stopped", "Server has stopped")
			}
		}()

		s.mu.Lock()
		s.running = true
		s.mu.Unlock()

		// Notify about server start
		s.updateStatus("Starting", "Server is starting...")

		serverError = s.Start(s.serverCtx)
		if serverError != nil && serverError != context.Canceled {
			s.logger.Error("Server error during background start", zap.Error(serverError))
			s.updateStatus("Error", fmt.Sprintf("Server error: %v", serverError))
		}
	}()

	return nil
}

// StopServer stops the server if it's running
func (s *Server) StopServer() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.running {
		// Return nil instead of error to prevent race condition logs
		s.logger.Debug("Server stop requested but server is not running")
		return nil
	}

	// Notify about server stopping
	s.updateStatus("Stopping", "Server is stopping...")

	// Cancel the server context first
	if s.serverCancel != nil {
		s.serverCancel()
	}

	// Gracefully shutdown HTTP server if it exists
	if s.httpServer != nil {
		// Give the server 5 seconds to shutdown gracefully
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if err := s.httpServer.Shutdown(shutdownCtx); err != nil {
			s.logger.Warn("Failed to gracefully shutdown HTTP server, forcing close", zap.Error(err))
			// Force close if graceful shutdown fails
			if closeErr := s.httpServer.Close(); closeErr != nil {
				s.logger.Error("Error forcing HTTP server close", zap.Error(closeErr))
			}
		}
		s.httpServer = nil
	}

	// Set running to false immediately after server is shut down
	s.running = false

	// Notify about server stopped with explicit status update
	s.updateStatus("Stopped", "Server has been stopped")

	s.logger.Info("Server stop completed")

	return nil
}

// startCustomHTTPServer creates a custom HTTP server that handles MCP endpoints
func (s *Server) startCustomHTTPServer(streamableServer *server.StreamableHTTPServer) error {
	mux := http.NewServeMux()

	// Create a logging wrapper for debugging
	loggingHandler := func(handler http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			s.logger.Info("HTTP request received",
				zap.String("method", r.Method),
				zap.String("path", r.URL.Path),
				zap.String("remote_addr", r.RemoteAddr),
				zap.String("user_agent", r.UserAgent()),
			)
			handler.ServeHTTP(w, r)
		})
	}

	// Standard MCP endpoint according to the specification
	mux.Handle("/mcp", loggingHandler(streamableServer))
	mux.Handle("/mcp/", loggingHandler(streamableServer)) // Handle trailing slash

	// Legacy endpoints for backward compatibility
	mux.Handle("/v1/tool_code", loggingHandler(streamableServer))
	mux.Handle("/v1/tool-code", loggingHandler(streamableServer)) // Alias for python client

	s.mu.Lock()
	s.httpServer = &http.Server{
		Addr:              s.config.Listen,
		Handler:           mux,
		ReadHeaderTimeout: 30 * time.Second,
	}
	s.running = true
	s.mu.Unlock()

	s.logger.Info("Starting HTTP server",
		zap.String("address", s.config.Listen),
		zap.Strings("endpoints", []string{"/mcp", "/mcp/", "/v1/tool_code", "/v1/tool-code"}),
	)
	if err := s.httpServer.ListenAndServe(); err != http.ErrServerClosed {
		s.logger.Error("HTTP server error", zap.Error(err))
		s.mu.Lock()
		s.running = false
		s.mu.Unlock()
		s.updateStatus("Error", fmt.Sprintf("Server failed: %v", err))
		return err
	}

	s.logger.Info("HTTP server stopped")
	return nil
}

// SaveConfiguration saves the current configuration to the persistent config file
func (s *Server) SaveConfiguration() error {
	configPath := s.GetConfigPath()
	if configPath == "" {
		s.logger.Warn("Configuration file path is not available, cannot save configuration")
		return fmt.Errorf("configuration file path is not available")
	}

	s.logger.Info("Saving configuration to file", zap.String("path", configPath))

	// Ensure we have the latest server list from the storage manager
	latestServers, err := s.storageManager.ListUpstreamServers()
	if err != nil {
		s.logger.Error("Failed to get latest server list from storage for saving", zap.Error(err))
		return err
	}
	s.config.Servers = latestServers

	return config.SaveConfig(s.config, configPath)
}

// ReloadConfiguration reloads the configuration from disk
func (s *Server) ReloadConfiguration() error {
	s.logger.Info("Reloading configuration from disk (config as source of truth)")

	// Store old config for comparison
	oldServerCount := len(s.config.Servers)

	// Load configuration from file
	configPath := config.GetConfigPath(s.config.DataDir)
	newConfig, err := config.LoadFromFile(configPath)
	if err != nil {
		return fmt.Errorf("failed to reload config: %w", err)
	}

	// Update internal config
	s.config = newConfig

	// Reload configured servers (this is where the comprehensive sync happens)
	if err := s.loadConfiguredServers(); err != nil {
		return fmt.Errorf("failed to reload servers: %w", err)
	}

	// Trigger tool re-indexing after configuration changes
	go func() {
		s.mu.RLock()
		ctx := s.serverCtx
		s.mu.RUnlock()
		if err := s.discoverAndIndexTools(ctx); err != nil {
			s.logger.Error("Failed to re-index tools after config reload", zap.Error(err))
		}
	}()

	s.logger.Info("Configuration reload completed",
		zap.String("path", configPath),
		zap.Int("old_server_count", oldServerCount),
		zap.Int("new_server_count", len(newConfig.Servers)),
		zap.Int("server_delta", len(newConfig.Servers)-oldServerCount))

	return nil
}

// OnUpstreamServerChange should be called when upstream servers are modified
func (s *Server) OnUpstreamServerChange() {
	s.logger.Info("Upstream server configuration change detected, forcing menu update")

	// This is now the primary method to trigger UI updates for any server change.
	s.ForceMenuUpdate()
}

// OnUpstreamServerStatusChange is called when a server's connection status changes
func (s *Server) OnUpstreamServerStatusChange(serverID string) {
	s.logger.Info("Upstream server status change detected", zap.String("server_id", serverID))

	// Force a menu update to reflect the new connection status
	s.ForceMenuUpdate()
}

// cleanupOrphanedIndexEntries removes index entries for servers that no longer exist
func (s *Server) cleanupOrphanedIndexEntries() {
	s.logger.Debug("Cleaning up orphaned index entries")

	// Get list of active server names
	activeServers := s.upstreamManager.GetAllServerNames()
	activeServerMap := make(map[string]bool)
	for _, serverName := range activeServers {
		activeServerMap[serverName] = true
	}

	// For now, we rely on the batch indexing to effectively replace all content
	// In a more sophisticated implementation, we could:
	// 1. Query the index for all unique server names
	// 2. Compare against active servers
	// 3. Remove orphaned entries
	// This is left as a future enhancement since batch indexing handles most cases

	s.logger.Debug("Finished cleaning up orphaned index entries")
}

// GetConfigPath returns the path to the configuration file for file watching
func (s *Server) GetConfigPath() string {
	return config.GetConfigPath(s.config.DataDir)
}

// ForceMenuUpdate sends a signal to the tray to force a refresh.
func (s *Server) ForceMenuUpdate() {
	s.logger.Debug("ForceMenuUpdate called, refreshing and sending status")

	// Refresh the status object with the latest stats before sending
	s.statusMu.Lock()
	s.status.UpstreamStats = s.upstreamManager.GetStats()
	s.status.ToolsIndexed = s.getIndexedToolCount()
	s.status.LastUpdated = time.Now()
	status := s.status
	s.statusMu.Unlock()

	// Non-blocking send on status channel to trigger menu refresh
	select {
	case s.statusCh <- status:
		s.logger.Debug("Sent status update to channel for menu refresh")
	default:
		s.logger.Warn("Status channel full, skipping menu update signal")
	}
}
