package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"go.uber.org/zap"

	"mcpproxy-go/internal/config"
	"mcpproxy-go/internal/contracts"
	"mcpproxy-go/internal/httpapi"
	"mcpproxy-go/internal/logs"
	"mcpproxy-go/internal/runtime"
	"mcpproxy-go/internal/secret"
	"mcpproxy-go/internal/tlslocal"
	"mcpproxy-go/web"
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
	logger   *zap.Logger
	runtime  *runtime.Runtime
	mcpProxy *MCPProxyServer

	// Server control
	httpServer *http.Server
	running    bool
	listenAddr string
	mu         sync.RWMutex

	serverCtx    context.Context
	serverCancel context.CancelFunc
	shutdown     bool

	statusCh chan interface{}
	eventsCh chan runtime.Event
}

// NewServer creates a new server instance
func NewServer(cfg *config.Config, logger *zap.Logger) (*Server, error) {
	return NewServerWithConfigPath(cfg, "", logger)
}

// NewServerWithConfigPath creates a new server instance with explicit config path tracking
func NewServerWithConfigPath(cfg *config.Config, configPath string, logger *zap.Logger) (*Server, error) {
	rt, err := runtime.New(cfg, configPath, logger)
	if err != nil {
		return nil, err
	}

	server := &Server{
		logger:   logger,
		runtime:  rt,
		statusCh: make(chan interface{}, 10),
		eventsCh: rt.SubscribeEvents(),
	}

	mcpProxy := NewMCPProxyServer(
		rt.StorageManager(),
		rt.IndexManager(),
		rt.UpstreamManager(),
		rt.CacheManager(),
		rt.Truncator(),
		logger,
		server,
		cfg.DebugSearch,
		cfg,
	)

	server.mcpProxy = mcpProxy

	go server.forwardRuntimeStatus()
	server.runtime.StartBackgroundInitialization()

	return server, nil
}

// createSelectiveWebUIProtectedHandler serves the Web UI without authentication.
// Since this handler is only mounted on /ui/*, all paths it receives are UI paths
// that should be served without authentication to allow the SPA to work properly.
// API endpoints are protected separately by the httpAPIServer middleware.
func (s *Server) createSelectiveWebUIProtectedHandler(handler http.Handler) http.Handler {
	// Simply pass through all requests without authentication
	// The handler is only mounted on /ui/* so it won't receive API requests
	return handler
}

// GetStatus returns the current server status
func (s *Server) GetStatus() interface{} {
	status := s.runtime.StatusSnapshot(s.IsRunning())
	if status != nil {
		status["listen_addr"] = s.GetListenAddress()
	}
	return status
}

// TriggerOAuthLogin starts an in-process OAuth flow for the given server name.
// Used by the tray to avoid cross-process DB locking issues during OAuth.
func (s *Server) TriggerOAuthLogin(serverName string) error {
	s.logger.Info("Tray requested OAuth login", zap.String("server", serverName))
	manager := s.runtime.UpstreamManager()
	if manager == nil {
		return fmt.Errorf("upstream manager not initialized")
	}
	if err := manager.StartManualOAuth(serverName, true); err != nil {
		s.logger.Error("Failed to start in-process OAuth", zap.String("server", serverName), zap.Error(err))
		return err
	}
	return nil
}

// StatusChannel returns a channel that receives status updates
func (s *Server) StatusChannel() <-chan interface{} {
	return s.statusCh
}

// EventsChannel exposes runtime events for tray/UI consumers.
func (s *Server) EventsChannel() <-chan runtime.Event {
	return s.eventsCh
}

// updateStatus updates the current status and notifies subscribers
func (s *Server) updateStatus(phase, message string) {
	s.runtime.UpdatePhase(phase, message)
}

func (s *Server) enqueueStatusSnapshot() {
	select {
	case s.statusCh <- s.runtime.StatusSnapshot(s.IsRunning()):
	default:
	}
}

func (s *Server) forwardRuntimeStatus() {
	// Emit initial snapshot so SSE clients have data immediately.
	s.enqueueStatusSnapshot()

	for range s.runtime.StatusChannel() {
		s.enqueueStatusSnapshot()
	}
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
		runtimeCtx := s.runtime.AppContext()
		s.mu.Lock()
		alreadyShutdown := s.shutdown
		isAppContext := (ctx == runtimeCtx)
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

	cfg := s.runtime.Config()
	listenAddr := ""
	if cfg != nil {
		listenAddr = cfg.Listen
	}

	// Determine transport mode based on listen address
	if listenAddr != "" && listenAddr != ":0" {
		// Start the MCP server in HTTP mode (Streamable HTTP)
		s.logger.Info("Starting MCP server",
			zap.String("transport", "streamable-http"),
			zap.String("listen", listenAddr))

		// Create Streamable HTTP server with custom routing
		streamableServer := server.NewStreamableHTTPServer(s.mcpProxy.GetMCPServer())

		// Create custom HTTP server for handling multiple routes
		if err := s.startCustomHTTPServer(ctx, streamableServer); err != nil {
			var portErr *PortInUseError
			if errors.As(err, &portErr) {
				return err
			}
			return fmt.Errorf("MCP Streamable HTTP server error: %w", err)
		}

		actualAddr := s.GetListenAddress()
		if actualAddr == "" {
			actualAddr = listenAddr
		}

		// Update status to show server is now running
		s.updateStatus("Running", fmt.Sprintf("Server is running on %s", actualAddr))
		s.runtime.SetRunning(true)
	} else {
		// Start the MCP server in stdio mode
		s.logger.Info("Starting MCP server", zap.String("transport", "stdio"))

		// Update status to show server is now running
		s.updateStatus("Running", "Server is running in stdio mode")
		s.runtime.SetRunning(true)

		// Serve using stdio (standard MCP transport)
		if err := server.ServeStdio(s.mcpProxy.GetMCPServer()); err != nil {
			return fmt.Errorf("MCP server error: %w", err)
		}
	}

	return nil
}

// discoverAndIndexTools discovers tools from upstream servers and indexes them
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

	if s.eventsCh != nil {
		s.runtime.UnsubscribeEvents(s.eventsCh)
	}

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

	if err := s.runtime.Close(); err != nil {
		s.logger.Error("Failed to close runtime", zap.Error(err))
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

// IsReady returns whether the server is fully initialized and ready to serve requests
// Uses relaxed criteria: ready if at least one upstream server is connected,
// or if no servers are configured/enabled
func (s *Server) IsReady() bool {
	status := s.runtime.CurrentStatus()

	// If server is in error or stopped state, not ready
	if status.Phase == "Error" || status.Phase == "Stopped" {
		return false
	}

	// Get upstream manager to check server connections
	upstreamManager := s.runtime.UpstreamManager()
	if upstreamManager == nil {
		// If no upstream manager, consider ready if server is running
		return status.Phase != "Loading"
	}

	// Check all configured servers
	allClients := upstreamManager.GetAllClients()
	enabledCount := 0
	connectedCount := 0

	for _, client := range allClients {
		if client.Config.Enabled {
			enabledCount++
			if client.IsConnected() {
				connectedCount++
			}
		}
	}

	// Ready if no enabled servers (all disabled or none configured)
	if enabledCount == 0 {
		return true
	}

	// Ready if at least one server is connected
	if connectedCount > 0 {
		return true
	}

	// Still connecting - only ready if we've moved past initial loading
	return status.Phase == "Ready" || status.Phase == "Starting"
}

// GetListenAddress returns the address the server is listening on
func (s *Server) GetListenAddress() string {
	s.mu.RLock()
	addr := s.listenAddr
	s.mu.RUnlock()
	if addr != "" {
		return addr
	}
	if cfg := s.runtime.Config(); cfg != nil {
		return cfg.Listen
	}
	return ""
}

// SetListenAddress updates the configured listen address and optionally persists it to disk.
func (s *Server) SetListenAddress(addr string, persist bool) error {
	if _, _, err := splitListenAddress(addr); err != nil {
		return err
	}

	if err := s.runtime.UpdateListenAddress(addr); err != nil {
		return err
	}

	if persist {
		if err := s.runtime.SaveConfiguration(); err != nil {
			return fmt.Errorf("failed to save configuration: %w", err)
		}
	}

	s.logger.Info("Listen address updated",
		zap.String("listen", addr),
		zap.Bool("persisted", persist))

	return nil
}

const defaultPortSuggestionAttempts = 20

// SuggestAlternateListen attempts to find an available listen address near baseAddr.
func (s *Server) SuggestAlternateListen(baseAddr string) (string, error) {
	return findAvailableListenAddress(baseAddr, defaultPortSuggestionAttempts)
}

// GetUpstreamStats returns statistics about upstream servers
func (s *Server) GetUpstreamStats() map[string]interface{} {
	stats := s.runtime.UpstreamManager().GetStats()

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
	if s.runtime.StorageManager() == nil {
		return []map[string]interface{}{}, nil
	}

	servers, err := s.runtime.StorageManager().ListUpstreamServers()
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

		if s.runtime.UpstreamManager() != nil {
			if client, exists := s.runtime.UpstreamManager().GetClient(server.Name); exists {
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
	if s.runtime.StorageManager() == nil {
		s.logger.Warn("Storage manager is nil in GetQuarantinedServers")
		return []map[string]interface{}{}, nil
	}

	s.logger.Debug("Calling storage manager ListQuarantinedUpstreamServers")
	quarantinedServers, err := s.runtime.StorageManager().ListQuarantinedUpstreamServers()
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
	return s.runtime.EnableServer(serverName, enabled)
}

// QuarantineServer quarantines/unquarantines a server
func (s *Server) QuarantineServer(serverName string, quarantined bool) error {
	return s.runtime.QuarantineServer(serverName, quarantined)
}

// getServerToolCount returns the number of tools for a specific server
// Uses cached tool counts with 2-minute TTL to reduce frequent ListTools calls
func (s *Server) getServerToolCount(serverID string) int {
	client, exists := s.runtime.UpstreamManager().GetClient(serverID)
	if !exists || !client.IsConnected() {
		return 0
	}

	// Use a shorter timeout for tool count requests to avoid blocking SSE updates
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Use the cached tool count to reduce ListTools calls
	count, err := client.GetCachedToolCount(ctx)
	if err != nil {
		// Classify errors to reduce noise from expected failures
		if isTimeoutError(err) {
			// Timeout errors are common for servers that don't support tool listing
			// Log at debug level to reduce noise
			s.logger.Debug("Tool count timeout for server (server may not support tools)",
				zap.String("server_id", serverID),
				zap.String("error_type", "timeout"))
		} else if isConnectionError(err) {
			// Connection errors suggest the server is actually disconnected
			s.logger.Debug("Connection error during tool count retrieval",
				zap.String("server_id", serverID),
				zap.String("error_type", "connection"))
		} else {
			// Other errors might be more significant
			s.logger.Debug("Failed to get tool count for server",
				zap.String("server_id", serverID),
				zap.Error(err))
		}
		return 0
	}

	return count
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
			s.listenAddr = ""
			s.mu.Unlock()
			s.runtime.SetRunning(false)

			// Only send "Stopped" status if there was no error
			// If there was an error, the error status should remain
			if serverError == nil || serverError == context.Canceled {
				s.updateStatus("Stopped", "Server has stopped")
			}
		}()

		s.mu.Lock()
		s.running = true
		s.mu.Unlock()
		s.runtime.SetRunning(true)

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
	if err := s.runtime.UpstreamManager().DisconnectAll(); err != nil {
		s.logger.Error("STOPSERVER - Failed to disconnect upstream servers early", zap.Error(err))
		_ = s.logger.Sync()
	} else {
		s.logger.Info("STOPSERVER - Successfully disconnected all upstream servers early")
		_ = s.logger.Sync()
	}

	// Add a brief wait to ensure Docker containers have time to be cleaned up
	// Only wait if there are actually Docker containers running
	if s.runtime.UpstreamManager().HasDockerContainers() {
		s.logger.Info("STOPSERVER - Docker containers detected, waiting for cleanup to complete")
		_ = s.logger.Sync()
		time.Sleep(3 * time.Second)
		s.logger.Info("STOPSERVER - Docker container cleanup wait completed")
		_ = s.logger.Sync()
	} else {
		s.logger.Debug("STOPSERVER - No Docker containers detected, skipping cleanup wait")
		_ = s.logger.Sync()
	}

	s.updateStatus("Stopping", "Server is stopping...")

	// Cancel the server context after cleanup
	s.logger.Info("STOPSERVER - Cancelling server context")
	_ = s.logger.Sync()
	if s.serverCancel != nil {
		s.serverCancel()
	}

	// HTTP server shutdown is now handled by context cancellation in startCustomHTTPServer
	s.logger.Info("STOPSERVER - HTTP server shutdown is handled by context cancellation")
	_ = s.logger.Sync()

	// Upstream servers already disconnected early in this method
	s.logger.Info("STOPSERVER - Upstream servers already disconnected early")
	_ = s.logger.Sync()

	// Set running to false immediately after server is shut down
	s.running = false
	s.listenAddr = ""
	s.runtime.SetRunning(false)

	// Notify about server stopped with explicit status update
	s.updateStatus("Stopped", "Server has been stopped")

	s.logger.Info("STOPSERVER - All operations completed successfully")
	_ = s.logger.Sync() // Final log flush

	return nil
}

// withHSTS adds HTTP Strict Transport Security headers
func withHSTS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains; preload")
		next.ServeHTTP(w, r)
	})
}

// startCustomHTTPServer creates a custom HTTP server that handles MCP endpoints
func (s *Server) startCustomHTTPServer(ctx context.Context, streamableServer *server.StreamableHTTPServer) error {
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

	// API v1 endpoints with chi router for REST API and SSE
	// TODO: Add observability manager integration
	httpAPIServer := httpapi.NewServer(s, s.logger.Sugar(), nil)
	mux.Handle("/api/", httpAPIServer)
	mux.Handle("/events", httpAPIServer)

	// Mount health endpoints directly on main mux at root level
	healthEndpoints := []string{"/healthz", "/readyz", "/livez", "/ready", "/health"}
	for _, endpoint := range healthEndpoints {
		mux.Handle(endpoint, httpAPIServer)
	}

	s.logger.Info("Registered REST API endpoints", zap.Strings("api_endpoints", []string{"/api/v1/*", "/events"}))
	s.logger.Info("Registered health endpoints", zap.Strings("health_endpoints", healthEndpoints))

	// Web UI endpoints (serves embedded Vue.js frontend) with selective API key protection
	webUIHandler := web.NewHandler(s.logger.Sugar())
	selectiveProtectedWebUIHandler := s.createSelectiveWebUIProtectedHandler(http.StripPrefix("/ui", webUIHandler))
	mux.Handle("/ui/", selectiveProtectedWebUIHandler)
	// Redirect root to web UI
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.Redirect(w, r, "/ui/", http.StatusFound)
		} else {
			http.NotFound(w, r)
		}
	})
	s.logger.Info("Registered Web UI endpoints", zap.Strings("ui_endpoints", []string{"/ui/", "/"}))

	cfg := s.runtime.Config()
	listenAddr := ""
	if cfg != nil {
		listenAddr = cfg.Listen
	}

	listener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		if isAddrInUseError(err) {
			return &PortInUseError{Address: listenAddr, Err: err}
		}
		return fmt.Errorf("failed to bind to %s: %w", listenAddr, err)
	}
	actualAddr := listener.Addr().String()

	s.mu.Lock()
	s.httpServer = &http.Server{
		Addr:              listenAddr,
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
	s.runtime.SetRunning(true)
	s.listenAddr = actualAddr
	s.mu.Unlock()

	// List all registered endpoints for visibility
	allEndpoints := []string{
		"/mcp", "/mcp/", // MCP protocol endpoints
		"/v1/tool_code", "/v1/tool-code", // Legacy MCP endpoints
		"/api/v1/*", "/events", // REST API and SSE endpoints
		"/ui/", "/", // Web UI endpoints
		"/healthz", "/readyz", "/livez", "/ready", "/health", // Health endpoints (at root level)
	}

	// Determine protocol for logging
	protocol := "HTTP"
	if cfg != nil && cfg.TLS != nil && cfg.TLS.Enabled {
		protocol = "HTTPS"
	}

	s.logger.Info(fmt.Sprintf("Starting MCP %s server with enhanced client stability", protocol),
		zap.String("protocol", protocol),
		zap.String("address", actualAddr),
		zap.String("requested_address", listenAddr),
		zap.Strings("endpoints", allEndpoints),
		zap.Duration("read_timeout", 120*time.Second),
		zap.Duration("write_timeout", 120*time.Second),
		zap.Duration("idle_timeout", 180*time.Second),
		zap.String("features", "connection_tracking,graceful_shutdown,enhanced_logging"),
	)

	// Setup error channel for server communication
	serverErrCh := make(chan error, 1)

	// Apply TLS configuration if enabled
	if cfg != nil && cfg.TLS != nil && cfg.TLS.Enabled {
		// Setup TLS configuration
		certsDir := cfg.TLS.CertsDir
		if certsDir == "" {
			certsDir = filepath.Join(cfg.DataDir, "certs")
		}

		tlsCfg, err := tlslocal.EnsureServerTLSConfig(tlslocal.Options{
			Dir:               certsDir,
			RequireClientCert: cfg.TLS.RequireClientCert,
		})
		if err != nil {
			return fmt.Errorf("TLS initialization failed: %w", err)
		}

		// Apply HSTS middleware if enabled
		handler := s.httpServer.Handler
		if cfg.TLS.HSTS {
			handler = withHSTS(handler)
			s.httpServer.Handler = handler
		}

		s.logger.Info("Starting HTTPS server with TLS configuration",
			zap.String("certs_dir", certsDir),
			zap.Bool("require_client_cert", cfg.TLS.RequireClientCert),
			zap.Bool("hsts", cfg.TLS.HSTS),
		)

		// Run the HTTPS server in a goroutine to enable graceful shutdown
		go func() {
			if err := tlslocal.ServeWithTLS(s.httpServer, listener, tlsCfg); err != nil && err != http.ErrServerClosed {
				s.logger.Error("HTTPS server error", zap.Error(err))
				s.mu.Lock()
				s.running = false
				s.listenAddr = ""
				s.mu.Unlock()
				s.runtime.SetRunning(false)
				s.updateStatus("Error", fmt.Sprintf("HTTPS server failed: %v", err))
				serverErrCh <- err
			} else {
				s.logger.Info("HTTPS server stopped gracefully")
				s.mu.Lock()
				s.listenAddr = ""
				s.mu.Unlock()
				serverErrCh <- nil
			}
		}()
	} else {
		s.logger.Info("Starting HTTP server (TLS disabled)")

		// Run the HTTP server in a goroutine to enable graceful shutdown
		go func() {
			if err := s.httpServer.Serve(listener); err != nil && err != http.ErrServerClosed {
				s.logger.Error("HTTP server error", zap.Error(err))
				s.mu.Lock()
				s.running = false
				s.listenAddr = ""
				s.mu.Unlock()
				s.runtime.SetRunning(false)
				s.updateStatus("Error", fmt.Sprintf("HTTP server failed: %v", err))
				serverErrCh <- err
			} else {
				s.logger.Info("HTTP server stopped gracefully")
				s.mu.Lock()
				s.listenAddr = ""
				s.mu.Unlock()
				serverErrCh <- nil
			}
		}()
	}

	// Wait for either context cancellation or server error
	select {
	case <-ctx.Done():
		s.logger.Info("Server context cancelled, initiating graceful shutdown")
		// Gracefully shutdown the HTTP server
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		if err := s.httpServer.Shutdown(shutdownCtx); err != nil {
			s.logger.Warn("HTTP server forced shutdown due to timeout", zap.Error(err))
			s.httpServer.Close()
		} else {
			s.logger.Info("HTTP server shutdown completed gracefully")
		}
		return ctx.Err()
	case err := <-serverErrCh:
		return err
	}
}

// responseWriter wraps http.ResponseWriter to capture the status code
type responseWriter struct {
	http.ResponseWriter
	statusCode    int
	headerWritten bool
}

func (rw *responseWriter) WriteHeader(code int) {
	if !rw.headerWritten {
		rw.statusCode = code
		rw.headerWritten = true
		rw.ResponseWriter.WriteHeader(code)
	}
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
	return s.runtime.SaveConfiguration()
}

// ReloadConfiguration reloads the configuration from disk
func (s *Server) ReloadConfiguration() error {
	return s.runtime.ReloadConfiguration()
}

// OnUpstreamServerChange should be called when upstream servers are modified
func (s *Server) OnUpstreamServerChange() {
	s.runtime.HandleUpstreamServerChange(s.serverCtx)
}

// GetConfigPath returns the path to the configuration file for file watching
func (s *Server) GetConfigPath() string {
	if path := s.runtime.ConfigPath(); path != "" {
		return path
	}
	if cfg := s.runtime.Config(); cfg != nil {
		return config.GetConfigPath(cfg.DataDir)
	}
	return ""
}

// GetLogDir returns the log directory path for tray UI
func (s *Server) GetLogDir() string {
	if cfg := s.runtime.Config(); cfg != nil {
		if cfg.Logging != nil && cfg.Logging.LogDir != "" {
			return cfg.Logging.LogDir
		}
		// Return OS-specific default log directory if not configured
		if defaultLogDir, err := logs.GetLogDir(); err == nil {
			return defaultLogDir
		}
		return cfg.DataDir
	}
	if defaultLogDir, err := logs.GetLogDir(); err == nil {
		return defaultLogDir
	}
	return ""
}

// Configuration management methods

// GetConfig returns the current configuration
func (s *Server) GetConfig() (*config.Config, error) {
	return s.runtime.GetConfig()
}

// ValidateConfig validates a configuration
func (s *Server) ValidateConfig(cfg *config.Config) ([]config.ValidationError, error) {
	return s.runtime.ValidateConfig(cfg)
}

// ApplyConfig applies a new configuration
func (s *Server) ApplyConfig(cfg *config.Config, cfgPath string) (*runtime.ConfigApplyResult, error) {
	return s.runtime.ApplyConfig(cfg, cfgPath)
}

// GetTokenSavings calculates and returns token savings statistics
func (s *Server) GetTokenSavings() (*contracts.ServerTokenMetrics, error) {
	return s.runtime.CalculateTokenSavings()
}

// GetServerTools returns tools for a specific server
func (s *Server) GetServerTools(serverName string) ([]map[string]interface{}, error) {
	s.logger.Debug("GetServerTools called", zap.String("server", serverName))

	if s.runtime.UpstreamManager() == nil {
		return nil, fmt.Errorf("upstream manager not initialized")
	}

	// Get client for the server
	client, exists := s.runtime.UpstreamManager().GetClient(serverName)
	if !exists {
		return nil, fmt.Errorf("server not found: %s", serverName)
	}

	if !client.IsConnected() {
		return nil, fmt.Errorf("server not connected: %s", serverName)
	}

	// Get tools from client
	ctx := context.Background()
	tools, err := client.ListTools(ctx)
	if err != nil {
		s.logger.Error("Failed to get server tools", zap.String("server", serverName), zap.Error(err))
		return nil, err
	}

	// Convert to map format for API
	var result []map[string]interface{}
	for _, tool := range tools {
		toolMap := map[string]interface{}{
			"name":        tool.Name,
			"description": tool.Description,
			"server_name": tool.ServerName,
		}
		// Note: ListTools returns ToolMetadata which doesn't have InputSchema
		// We'd need to get that from the actual tool definition
		result = append(result, toolMap)
	}

	s.logger.Debug("Retrieved server tools", zap.String("server", serverName), zap.Int("count", len(result)))
	return result, nil
}

// SearchTools searches for tools using the index
func (s *Server) SearchTools(query string, limit int) ([]map[string]interface{}, error) {
	s.logger.Debug("SearchTools called", zap.String("query", query), zap.Int("limit", limit))

	if s.runtime.IndexManager() == nil {
		return nil, fmt.Errorf("index manager not initialized")
	}

	// Search tools in the index
	results, err := s.runtime.IndexManager().SearchTools(query, limit)
	if err != nil {
		s.logger.Error("Failed to search tools", zap.String("query", query), zap.Error(err))
		return nil, err
	}

	// Convert to map format for API
	var resultMaps []map[string]interface{}
	for _, result := range results {
		if result.Tool != nil {
			toolData := map[string]interface{}{
				"name":        result.Tool.Name,
				"description": result.Tool.Description,
				"server_name": result.Tool.ServerName,
			}
			// Parse params JSON as input schema if available
			if result.Tool.ParamsJSON != "" {
				var inputSchema map[string]interface{}
				if err := json.Unmarshal([]byte(result.Tool.ParamsJSON), &inputSchema); err == nil {
					toolData["input_schema"] = inputSchema
				}
			}

			// Wrap in search result format with nested tool
			resultMap := map[string]interface{}{
				"tool":  toolData,
				"score": result.Score,
			}
			resultMaps = append(resultMaps, resultMap)
		}
	}

	s.logger.Debug("Search completed", zap.String("query", query), zap.Int("results", len(resultMaps)))
	return resultMaps, nil
}

// GetServerLogs returns recent log lines for a specific server
func (s *Server) GetServerLogs(serverName string, tail int) ([]string, error) {
	s.logger.Debug("GetServerLogs called", zap.String("server", serverName), zap.Int("tail", tail))

	if s.runtime.UpstreamManager() == nil {
		return nil, fmt.Errorf("upstream manager not initialized")
	}

	// Check if server exists
	_, exists := s.runtime.UpstreamManager().GetClient(serverName)
	if !exists {
		return nil, fmt.Errorf("server not found: %s", serverName)
	}

	// For now, return a placeholder indicating logs are not yet implemented
	// TODO: Implement actual log reading from server-specific log files
	logs := []string{
		fmt.Sprintf("Log viewing for server '%s' is not yet implemented", serverName),
		"This feature will be added in a future release",
		"Check the main application logs for server activity",
	}

	s.logger.Debug("Retrieved server logs", zap.String("server", serverName), zap.Int("lines", len(logs)))
	return logs, nil
}

// GetSecretResolver returns the secret resolver instance
func (s *Server) GetSecretResolver() *secret.Resolver {
	return s.runtime.GetSecretResolver()
}

// GetCurrentConfig returns the current configuration
func (s *Server) GetCurrentConfig() interface{} {
	return s.runtime.GetCurrentConfig()
}

// GetToolCalls retrieves tool call history with pagination
func (s *Server) GetToolCalls(limit, offset int) ([]*contracts.ToolCallRecord, int, error) {
	return s.runtime.GetToolCalls(limit, offset)
}

// GetToolCallByID retrieves a single tool call by ID
func (s *Server) GetToolCallByID(id string) (*contracts.ToolCallRecord, error) {
	return s.runtime.GetToolCallByID(id)
}

// GetServerToolCalls retrieves tool call history for a specific server
func (s *Server) GetServerToolCalls(serverName string, limit int) ([]*contracts.ToolCallRecord, error) {
	return s.runtime.GetServerToolCalls(serverName, limit)
}

// ReplayToolCall replays a tool call with modified arguments
func (s *Server) ReplayToolCall(id string, arguments map[string]interface{}) (*contracts.ToolCallRecord, error) {
	return s.runtime.ReplayToolCall(id, arguments)
}

// CallTool calls an MCP tool and returns the result
func (s *Server) CallTool(ctx context.Context, toolName string, arguments map[string]interface{}) (interface{}, error) {
	if s.mcpProxy == nil {
		return nil, fmt.Errorf("MCP proxy not initialized")
	}

	// Create MCP call tool request
	request := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      toolName,
			Arguments: arguments,
		},
	}

	// Call the tool via MCP proxy
	result, err := s.mcpProxy.CallToolDirect(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("tool call failed: %w", err)
	}

	return result, nil
}

// ListRegistries returns the list of available MCP server registries (Phase 7)
func (s *Server) ListRegistries() ([]interface{}, error) {
	return s.runtime.ListRegistries()
}

// SearchRegistryServers searches for servers in a specific registry (Phase 7)
func (s *Server) SearchRegistryServers(registryID, tag, query string, limit int) ([]interface{}, error) {
	return s.runtime.SearchRegistryServers(registryID, tag, query, limit)
}
