package server

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/server"
	"go.uber.org/zap"

	"mcpproxy-go/internal/cache"
	"mcpproxy-go/internal/config"
	"mcpproxy-go/internal/index"
	"mcpproxy-go/internal/logs"
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
	upstreamManager := upstream.NewManager(logger, cfg)

	// Set logging configuration on upstream manager for per-server logging
	if cfg.Logging != nil {
		upstreamManager.SetLogConfig(cfg.Logging)
	}

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

	// Create MCP proxy server
	mcpProxy := NewMCPProxyServer(storageManager, indexManager, upstreamManager, cacheManager, truncator, logger, server, cfg.DebugSearch, cfg)

	server.mcpProxy = mcpProxy

	// Start background initialization immediately
	go server.backgroundInitialization()

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

// backgroundInitialization handles server initialization in the background
func (s *Server) backgroundInitialization() {
	s.updateStatus("Loading", "Loading configuration and connecting to servers...")

	// Load configured servers from storage and add to upstream manager
	if err := s.loadConfiguredServers(); err != nil {
		s.logger.Error("Failed to load configured servers", zap.Error(err))
		s.updateStatus("Error", fmt.Sprintf("Failed to load servers: %v", err))
		return
	}

	// Start background connection attempts using application context
	s.updateStatus("Connecting", "Connecting to upstream servers...")
	s.mu.RLock()
	appCtx := s.appCtx // Use application context, not server context
	s.mu.RUnlock()
	go s.backgroundConnections(appCtx)

	// Start background tool discovery and indexing using application context
	s.mu.RLock()
	appCtx = s.appCtx // Use application context, not server context
	s.mu.RUnlock()
	go s.backgroundToolIndexing(appCtx)

	// Only set "Ready" status if the server is not already running
	// If server is running, don't override the "Running" status
	s.mu.RLock()
	isRunning := s.running
	s.mu.RUnlock()

	if !isRunning {
		s.updateStatus("Ready", "Server is ready (connections continue in background)")
	}
}

// backgroundConnections handles connecting to upstream servers with retry logic
func (s *Server) backgroundConnections(ctx context.Context) {
	// Initial connection attempt
	s.connectAllWithRetry(ctx)

	// Start periodic reconnection attempts for failed connections (less aggressive)
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.connectAllWithRetry(ctx)
		case <-ctx.Done():
			s.logger.Info("Background connections stopped due to context cancellation")
			return
		}
	}
}

// connectAllWithRetry attempts to connect to all servers with exponential backoff
func (s *Server) connectAllWithRetry(ctx context.Context) {
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

	if connectedCount < totalCount {
		// Only update status to "Connecting" if server is not running
		// If server is running, don't override the "Running" status
		s.mu.RLock()
		isRunning := s.running
		s.mu.RUnlock()

		if !isRunning {
			s.updateStatus("Connecting", fmt.Sprintf("Connected to %d/%d servers, retrying...", connectedCount, totalCount))
		}

		// Try to connect with timeout
		connectCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()

		if err := s.upstreamManager.ConnectAll(connectCtx); err != nil {
			s.logger.Warn("Some upstream servers failed to connect", zap.Error(err))
		}
	}
}

// backgroundToolIndexing handles tool discovery and indexing
func (s *Server) backgroundToolIndexing(ctx context.Context) {
	// Initial indexing after a short delay to let connections establish
	select {
	case <-time.After(2 * time.Second):
		_ = s.discoverAndIndexTools(ctx)
	case <-ctx.Done():
		s.logger.Info("Background tool indexing stopped during initial delay")
		return
	}

	// Re-index every 15 minutes (less aggressive)
	ticker := time.NewTicker(15 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
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

// Start starts the MCP proxy server
func (s *Server) Start(ctx context.Context) error {
	s.logger.Info("Starting MCP proxy server")

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

		s.logger.Info("SERVER SHUTDOWN SEQUENCE COMPLETED")
		_ = s.logger.Sync()
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
	httpServer := s.httpServer
	s.mu.Unlock()

	s.logger.Info("Shutting down MCP proxy server...")

	// Gracefully shutdown HTTP server first to stop accepting new connections
	if httpServer != nil {
		s.logger.Info("Gracefully shutting down HTTP server...")
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		if err := httpServer.Shutdown(ctx); err != nil {
			s.logger.Warn("HTTP server forced shutdown due to timeout", zap.Error(err))
			// Force close if graceful shutdown times out
			httpServer.Close()
		} else {
			s.logger.Info("HTTP server shutdown completed gracefully")
		}
	}

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
	s.logger.Debug("GetQuarantinedServers called")

	// Check if storage manager is available
	if s.storageManager == nil {
		s.logger.Warn("Storage manager is nil in GetQuarantinedServers")
		return []map[string]interface{}{}, nil
	}

	s.logger.Debug("Calling storage manager ListQuarantinedUpstreamServers")
	quarantinedServers, err := s.storageManager.ListQuarantinedUpstreamServers()
	if err != nil {
		// Handle database closed gracefully
		if strings.Contains(err.Error(), "database not open") || strings.Contains(err.Error(), "closed") {
			s.logger.Debug("Database not available for GetQuarantinedServers, returning empty list")
			return []map[string]interface{}{}, nil
		}
		s.logger.Error("Failed to get quarantined servers from storage", zap.Error(err))
		return nil, err
	}

	s.logger.Debug("Retrieved quarantined servers from storage",
		zap.Int("count", len(quarantinedServers)))

	var result []map[string]interface{}
	for _, server := range quarantinedServers {
		serverMap := map[string]interface{}{
			"name":        server.Name,
			"url":         server.URL,
			"command":     server.Command,
			"protocol":    server.Protocol,
			"enabled":     server.Enabled,
			"quarantined": server.Quarantined,
			"created":     server.Created,
		}
		result = append(result, serverMap)

		s.logger.Debug("Added quarantined server to result",
			zap.String("server", server.Name),
			zap.Bool("quarantined", server.Quarantined))
	}

	s.logger.Debug("GetQuarantinedServers completed",
		zap.Int("total_result_count", len(result)))

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

	s.logger.Info("Successfully persisted server quarantine state change",
		zap.String("server", serverName),
		zap.Bool("quarantined", quarantined))

	return nil
}

// getServerToolCount returns the number of tools for a specific server
// Uses shorter timeout and better error handling for UI status display
func (s *Server) getServerToolCount(serverID string) int {
	client, exists := s.upstreamManager.GetClient(serverID)
	if !exists || !client.IsConnected() {
		return 0
	}

	// Use timeout for UI status updates (30 seconds for SSE servers)
	// This allows time for SSE servers to establish connections and respond
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	s.logger.Debug("Starting ListTools operation",
		zap.String("server_id", serverID),
		zap.Duration("timeout", 30*time.Second))

	tools, err := client.ListTools(ctx)
	if err != nil {
		// Classify errors to reduce noise from expected failures
		if isTimeoutError(err) {
			// Timeout errors are common for servers that don't support tool listing
			// Log at debug level to reduce noise
			s.logger.Debug("Tool listing timeout for server (server may not support tools)",
				zap.String("server_id", serverID),
				zap.String("error_type", "timeout"))
		} else if isConnectionError(err) {
			// Connection errors suggest the server is actually disconnected
			s.logger.Debug("Connection error during tool listing",
				zap.String("server_id", serverID),
				zap.String("error_type", "connection"))
		} else {
			// Other errors might be more significant
			s.logger.Warn("Failed to get tool count for server",
				zap.String("server_id", serverID),
				zap.Error(err))
		}
		return 0
	}

	return len(tools)
}

// Helper functions for error classification
func isTimeoutError(err error) bool {
	errStr := err.Error()
	return strings.Contains(errStr, "timeout") ||
		strings.Contains(errStr, "deadline exceeded") ||
		strings.Contains(errStr, "context canceled")
}

func isConnectionError(err error) bool {
	errStr := err.Error()
	return strings.Contains(errStr, "connection refused") ||
		strings.Contains(errStr, "no such host") ||
		strings.Contains(errStr, "connection reset") ||
		strings.Contains(errStr, "broken pipe")
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
	s.logger.Info("STOPSERVER CALLED - STARTING SHUTDOWN SEQUENCE")
	_ = s.logger.Sync()

	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.running {
		// Return nil instead of error to prevent race condition logs
		s.logger.Debug("Server stop requested but server is not running")
		return nil
	}

	// Notify about server stopping
	s.logger.Info("STOPSERVER - Server is running, proceeding with stop")
	_ = s.logger.Sync()

	// Disconnect upstream servers FIRST to ensure Docker containers are cleaned up
	// Do this before canceling contexts to avoid interruption
	s.logger.Info("STOPSERVER - Disconnecting upstream servers EARLY")
	_ = s.logger.Sync()
	if err := s.upstreamManager.DisconnectAll(); err != nil {
		s.logger.Error("STOPSERVER - Failed to disconnect upstream servers early", zap.Error(err))
		_ = s.logger.Sync()
	} else {
		s.logger.Info("STOPSERVER - Successfully disconnected all upstream servers early")
		_ = s.logger.Sync()
	}

	// Add a brief wait to ensure Docker containers have time to be cleaned up
	s.logger.Info("STOPSERVER - Waiting for Docker container cleanup to complete")
	_ = s.logger.Sync()
	time.Sleep(3 * time.Second)
	s.logger.Info("STOPSERVER - Docker container cleanup wait completed")
	_ = s.logger.Sync()

	s.updateStatus("Stopping", "Server is stopping...")

	// Cancel the server context after cleanup
	s.logger.Info("STOPSERVER - Cancelling server context")
	_ = s.logger.Sync()
	if s.serverCancel != nil {
		s.serverCancel()
	}

	// Gracefully shutdown HTTP server if it exists
	s.logger.Info("STOPSERVER - Shutting down HTTP server")
	_ = s.logger.Sync()
	if s.httpServer != nil {
		// Give the server 5 seconds to shutdown gracefully
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if err := s.httpServer.Shutdown(shutdownCtx); err != nil {
			s.logger.Warn("STOPSERVER - Failed to gracefully shutdown HTTP server, forcing close", zap.Error(err))
			// Force close if graceful shutdown fails
			if closeErr := s.httpServer.Close(); closeErr != nil {
				s.logger.Error("STOPSERVER - Error forcing HTTP server close", zap.Error(closeErr))
			}
		} else {
			s.logger.Info("STOPSERVER - HTTP server shutdown successfully")
			_ = s.logger.Sync()
		}
		s.httpServer = nil
	}

	s.logger.Info("STOPSERVER - HTTP server cleanup completed")
	_ = s.logger.Sync()

	// Upstream servers already disconnected early in this method
	s.logger.Info("STOPSERVER - Upstream servers already disconnected early")
	_ = s.logger.Sync()

	// Set running to false immediately after server is shut down
	s.running = false

	// Notify about server stopped with explicit status update
	s.updateStatus("Stopped", "Server has been stopped")

	s.logger.Info("STOPSERVER - All operations completed successfully")
	_ = s.logger.Sync() // Final log flush

	return nil
}

// startCustomHTTPServer creates a custom HTTP server that handles MCP endpoints
func (s *Server) startCustomHTTPServer(streamableServer *server.StreamableHTTPServer) error {
	mux := http.NewServeMux()

	// Create a logging wrapper for debugging client connections
	loggingHandler := func(handler http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			// Log incoming request with connection details
			s.logger.Debug("MCP client request received",
				zap.String("method", r.Method),
				zap.String("path", r.URL.Path),
				zap.String("remote_addr", r.RemoteAddr),
				zap.String("user_agent", r.UserAgent()),
				zap.String("content_type", r.Header.Get("Content-Type")),
				zap.String("connection", r.Header.Get("Connection")),
				zap.Int64("content_length", r.ContentLength),
			)

			// Create response writer wrapper to capture status and errors
			wrappedWriter := &responseWriter{ResponseWriter: w, statusCode: 200}

			// Handle the request
			handler.ServeHTTP(wrappedWriter, r)

			duration := time.Since(start)

			// Log response with timing and status
			if wrappedWriter.statusCode >= 400 {
				s.logger.Warn("MCP client request completed with error",
					zap.String("method", r.Method),
					zap.String("path", r.URL.Path),
					zap.String("remote_addr", r.RemoteAddr),
					zap.Int("status_code", wrappedWriter.statusCode),
					zap.Duration("duration", duration),
				)
			} else {
				s.logger.Debug("MCP client request completed successfully",
					zap.String("method", r.Method),
					zap.String("path", r.URL.Path),
					zap.String("remote_addr", r.RemoteAddr),
					zap.Int("status_code", wrappedWriter.statusCode),
					zap.Duration("duration", duration),
				)
			}
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
		ReadHeaderTimeout: 60 * time.Second,  // Increased for better client compatibility
		ReadTimeout:       120 * time.Second, // Full request read timeout
		WriteTimeout:      120 * time.Second, // Response write timeout
		IdleTimeout:       180 * time.Second, // Keep-alive timeout for persistent connections
		MaxHeaderBytes:    1 << 20,           // 1MB max header size
		// Enable connection state tracking for better debugging
		ConnState: s.logConnectionState,
	}
	s.running = true
	s.mu.Unlock()

	s.logger.Info("Starting MCP HTTP server with enhanced client stability",
		zap.String("address", s.config.Listen),
		zap.Strings("endpoints", []string{"/mcp", "/mcp/", "/v1/tool_code", "/v1/tool-code"}),
		zap.Duration("read_timeout", 120*time.Second),
		zap.Duration("write_timeout", 120*time.Second),
		zap.Duration("idle_timeout", 180*time.Second),
		zap.String("features", "connection_tracking,graceful_shutdown,enhanced_logging"),
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

// responseWriter wraps http.ResponseWriter to capture the status code
type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

// logConnectionState logs HTTP connection state changes for debugging client issues
func (s *Server) logConnectionState(conn net.Conn, state http.ConnState) {
	switch state {
	case http.StateNew:
		s.logger.Debug("New client connection established",
			zap.String("remote_addr", conn.RemoteAddr().String()),
			zap.String("state", "new"))
	case http.StateActive:
		s.logger.Debug("Client connection active",
			zap.String("remote_addr", conn.RemoteAddr().String()),
			zap.String("state", "active"))
	case http.StateIdle:
		s.logger.Debug("Client connection idle",
			zap.String("remote_addr", conn.RemoteAddr().String()),
			zap.String("state", "idle"))
	case http.StateHijacked:
		s.logger.Debug("Client connection hijacked (likely for upgrade)",
			zap.String("remote_addr", conn.RemoteAddr().String()),
			zap.String("state", "hijacked"))
	case http.StateClosed:
		s.logger.Debug("Client connection closed",
			zap.String("remote_addr", conn.RemoteAddr().String()),
			zap.String("state", "closed"))
	}
}

// SaveConfiguration saves the current configuration to the persistent config file
func (s *Server) SaveConfiguration() error {
	configPath := s.GetConfigPath()
	if configPath == "" {
		s.logger.Warn("Configuration file path is not available, cannot save configuration")
		return fmt.Errorf("configuration file path is not available")
	}

	s.logger.Debug("Saving configuration to file", zap.String("path", configPath))

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
	s.logger.Info("Reloading configuration from disk")

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
	s.logger.Debug("About to call loadConfiguredServers")
	if err := s.loadConfiguredServers(); err != nil {
		s.logger.Error("loadConfiguredServers failed", zap.Error(err))
		return fmt.Errorf("failed to reload servers: %w", err)
	}
	s.logger.Debug("loadConfiguredServers completed successfully")

	// Trigger immediate reconnection for servers that were disconnected during config reload
	s.logger.Debug("Starting goroutine for immediate reconnection after config reload")
	go func() {
		s.mu.RLock()
		ctx := s.appCtx // Use application context instead of server context
		s.mu.RUnlock()

		s.logger.Debug("Inside reconnection goroutine", zap.Bool("ctx_is_nil", ctx == nil))
		if ctx == nil {
			s.logger.Error("Application context is nil, cannot trigger reconnection")
			return
		}

		s.logger.Info("Triggering immediate reconnection after config reload")

		// Connect all servers that should be connected
		connectCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()

		if err := s.upstreamManager.ConnectAll(connectCtx); err != nil {
			s.logger.Warn("Some servers failed to reconnect after config reload", zap.Error(err))
		}

		// Wait a bit for connections to establish, then trigger tool re-indexing
		select {
		case <-time.After(2 * time.Second):
			if err := s.discoverAndIndexTools(ctx); err != nil {
				s.logger.Error("Failed to re-index tools after config reload", zap.Error(err))
			}
		case <-ctx.Done():
			s.logger.Info("Tool re-indexing cancelled during config reload")
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
	// This function should primarily trigger re-discovery and re-indexing.
	// It should NOT save the configuration, as that can cause loops.
	// Saving should be done explicitly when the state change is initiated.
	s.logger.Info("Upstream server configuration changed, triggering comprehensive update")
	go func() {
		// Re-index tools from all active servers
		// This will automatically handle removed/disabled servers since they won't be discovered
		if err := s.discoverAndIndexTools(s.serverCtx); err != nil {
			s.logger.Error("Failed to update tool index after upstream change", zap.Error(err))
		}

		// Clean up any orphaned tools in index that are no longer from active servers
		// This handles edge cases where servers were removed abruptly
		s.cleanupOrphanedIndexEntries()
	}()

	// Update status
	s.updateStatus(s.status.Phase, "Upstream servers updated")
}

// cleanupOrphanedIndexEntries removes index entries for servers that are no longer active
func (s *Server) cleanupOrphanedIndexEntries() {
	s.logger.Debug("Checking for orphaned index entries")

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

	s.logger.Debug("Orphaned index cleanup completed",
		zap.Int("active_servers", len(activeServers)))
}

// GetConfigPath returns the path to the configuration file for file watching
func (s *Server) GetConfigPath() string {
	return config.GetConfigPath(s.config.DataDir)
}

// GetLogDir returns the log directory path for tray UI
func (s *Server) GetLogDir() string {
	if s.config.Logging != nil && s.config.Logging.LogDir != "" {
		return s.config.Logging.LogDir
	}
	// Return OS-specific default log directory if not configured
	if defaultLogDir, err := logs.GetLogDir(); err == nil {
		return defaultLogDir
	}
	// Last resort fallback to data directory
	return s.config.DataDir
}
