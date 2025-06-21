package server

import (
	"context"
	"fmt"
	"net/http"
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

// ServerStatus represents the current status of the server
type ServerStatus struct {
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
	serverCtx  context.Context
	cancelFunc context.CancelFunc

	// Status reporting
	status   ServerStatus
	statusMu sync.RWMutex
	statusCh chan ServerStatus
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

	server := &Server{
		config:          cfg,
		logger:          logger,
		storageManager:  storageManager,
		indexManager:    indexManager,
		upstreamManager: upstreamManager,
		cacheManager:    cacheManager,
		truncator:       truncator,
		statusCh:        make(chan ServerStatus, 10), // Buffered channel for status updates
		status: ServerStatus{
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
	return s.status
}

// StatusChannel returns a channel that receives status updates
func (s *Server) StatusChannel() <-chan interface{} {
	// Create a new channel that converts ServerStatus to interface{}
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

	// Start background connection attempts
	s.updateStatus("Connecting", "Connecting to upstream servers...")
	go s.backgroundConnections()

	// Start background tool discovery and indexing
	go s.backgroundToolIndexing()

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
func (s *Server) backgroundConnections() {
	ctx := context.Background()

	// Initial connection attempt
	s.connectAllWithRetry(ctx)

	// Start periodic reconnection attempts for failed connections
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.connectAllWithRetry(ctx)
		case <-ctx.Done():
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
func (s *Server) backgroundToolIndexing() {
	// Initial indexing after a short delay to let connections establish
	time.Sleep(2 * time.Second)
	s.discoverAndIndexTools(context.Background())

	// Re-index every 5 minutes
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.discoverAndIndexTools(context.Background())
		}
	}
}

// loadConfiguredServers loads servers from config and storage, then adds them to upstream manager
func (s *Server) loadConfiguredServers() error {
	// First load servers from config file
	for _, serverConfig := range s.config.Servers {
		if serverConfig.Enabled {
			// Store in persistent storage
			id, err := s.storageManager.AddUpstream(serverConfig)
			if err != nil {
				s.logger.Warn("Failed to store server config",
					zap.String("name", serverConfig.Name),
					zap.Error(err))
				continue
			}

			// Add to upstream manager without connecting
			if err := s.upstreamManager.AddServerConfig(id, serverConfig); err != nil {
				s.logger.Warn("Failed to add upstream server config",
					zap.String("name", serverConfig.Name),
					zap.Error(err))
			}
		}
	}

	// Then load any additional servers from storage
	storedServers, err := s.storageManager.ListUpstreams()
	if err != nil {
		return fmt.Errorf("failed to list stored upstreams: %w", err)
	}

	for _, serverConfig := range storedServers {
		if serverConfig.Enabled {
			// Check if already added from config
			if !s.isServerInConfig(serverConfig.Name) {
				if err := s.upstreamManager.AddServerConfig(serverConfig.Name, serverConfig); err != nil {
					s.logger.Warn("Failed to add stored upstream server config",
						zap.String("id", serverConfig.Name),
						zap.String("name", serverConfig.Name),
						zap.Error(err))
				}
			}
		}
	}

	return nil
}

// isServerInConfig checks if a server name is already in the config
func (s *Server) isServerInConfig(name string) bool {
	for _, serverConfig := range s.config.Servers {
		if serverConfig.Name == name {
			return true
		}
	}
	return false
}

// Start starts the MCP proxy server
func (s *Server) Start(ctx context.Context) error {
	s.logger.Info("Starting MCP proxy server")

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
	s.logger.Info("Shutting down MCP proxy server...")

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

// GetQuarantinedServers returns information about quarantined servers for tray UI
func (s *Server) GetQuarantinedServers() ([]map[string]interface{}, error) {
	quarantinedServers, err := s.storageManager.ListQuarantinedUpstreamServers()
	if err != nil {
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
	return s.storageManager.QuarantineUpstreamServer(serverName, false)
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

	s.serverCtx, s.cancelFunc = context.WithCancel(ctx)

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
		return fmt.Errorf("server is not running")
	}

	// Notify about server stopping
	s.updateStatus("Stopping", "Server is stopping...")

	// Cancel the server context first
	if s.cancelFunc != nil {
		s.cancelFunc()
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

	s.running = false

	// Notify about server stopped
	s.updateStatus("Stopped", "Server has been stopped")

	return nil
}

// startCustomHTTPServer creates a custom HTTP server that handles both /mcp and /mcp/ routes
func (s *Server) startCustomHTTPServer(streamableServer *server.StreamableHTTPServer) error {
	mux := http.NewServeMux()

	// Handle both /mcp and /mcp/ patterns
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		// Redirect /mcp to /mcp/ for consistency
		if r.URL.Path == "/mcp" {
			http.Redirect(w, r, "/mcp/", http.StatusMovedPermanently)
			return
		}
		streamableServer.ServeHTTP(w, r)
	})

	mux.HandleFunc("/mcp/", func(w http.ResponseWriter, r *http.Request) {
		streamableServer.ServeHTTP(w, r)
	})

	// Create HTTP server with better defaults for restart scenarios
	s.httpServer = &http.Server{
		Addr:    s.config.Listen,
		Handler: mux,
		// Add timeouts to prevent hanging connections
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	s.logger.Info("Starting custom HTTP server with /mcp routing",
		zap.String("addr", s.config.Listen))

	// Listen and serve - this is a blocking call
	if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		s.updateStatus("Error", fmt.Sprintf("Failed to start HTTP server on %s: %v", s.config.Listen, err))
		return fmt.Errorf("HTTP server failed: %w", err)
	}

	return nil
}

// SaveConfiguration saves the current configuration to the persistent config file
func (s *Server) SaveConfiguration() error {
	// Get current servers from storage
	servers, err := s.storageManager.ListUpstreamServers()
	if err != nil {
		return fmt.Errorf("failed to list upstream servers: %w", err)
	}

	// Update config with current servers
	s.config.Servers = servers

	// Save to persistent config file
	configPath := config.GetConfigPath(s.config.DataDir)
	if err := config.SaveConfig(s.config, configPath); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	s.logger.Info("Configuration saved",
		zap.String("path", configPath),
		zap.Int("servers", len(servers)))

	return nil
}

// ReloadConfiguration reloads the configuration and updates running servers
func (s *Server) ReloadConfiguration() error {
	// Load configuration from file
	configPath := config.GetConfigPath(s.config.DataDir)
	newConfig, err := config.LoadFromFile(configPath)
	if err != nil {
		return fmt.Errorf("failed to reload config: %w", err)
	}

	// Update internal config
	s.config = newConfig

	// Reload configured servers
	if err := s.loadConfiguredServers(); err != nil {
		return fmt.Errorf("failed to reload servers: %w", err)
	}

	s.logger.Info("Configuration reloaded",
		zap.String("path", configPath),
		zap.Int("servers", len(newConfig.Servers)))

	return nil
}

// OnUpstreamServerChange should be called when upstream servers are modified
func (s *Server) OnUpstreamServerChange() {
	// Save configuration to persist changes
	if err := s.SaveConfiguration(); err != nil {
		s.logger.Error("Failed to save configuration after upstream change", zap.Error(err))
	}

	// Trigger background tool discovery to update index
	go func() {
		ctx := context.Background()
		if err := s.discoverAndIndexTools(ctx); err != nil {
			s.logger.Error("Failed to update tool index after upstream change", zap.Error(err))
		}
	}()

	// Update status
	s.updateStatus(s.status.Phase, "Upstream servers updated")
}
