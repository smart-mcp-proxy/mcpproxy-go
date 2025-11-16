package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
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
	"mcpproxy-go/internal/storage"
	"mcpproxy-go/internal/tlslocal"
	"mcpproxy-go/internal/upstream/types"
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
	httpServer      *http.Server
	listenerManager *ListenerManager
	running         bool
	listenAddr      string
	mu              sync.RWMutex

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
		status["process_pid"] = os.Getpid()
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

// GetDockerRecoveryStatus returns the current Docker recovery status
func (s *Server) GetDockerRecoveryStatus() *storage.DockerRecoveryState {
	return s.runtime.GetDockerRecoveryStatus()
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
func (s *Server) updateStatus(phase runtime.Phase, message string) {
	s.runtime.UpdatePhase(phase, message)
}

func (s *Server) enqueueStatusSnapshot() {
	snapshot := s.runtime.StatusSnapshot(s.IsRunning())
	if snapshot != nil {
		snapshot["listen_addr"] = s.GetListenAddress()
	}
	select {
	case s.statusCh <- snapshot:
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

		s.runtime.SetRunning(true)
	} else {
		// Start the MCP server in stdio mode
		s.logger.Info("Starting MCP server", zap.String("transport", "stdio"))

		// Update status to show server is now running
		s.updateStatus(runtime.PhaseRunning, "Server is running in stdio mode")
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

	// Close MCP proxy server (includes JavaScript runtime pool cleanup)
	if s.mcpProxy != nil {
		if err := s.mcpProxy.Close(); err != nil {
			s.logger.Error("Failed to close MCP proxy server", zap.Error(err))
		}
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
func (s *Server) IsReady() bool {
	status := s.runtime.CurrentStatus()

	switch status.Phase {
	case runtime.PhaseReady:
		return true
	case runtime.PhaseRunning:
		return true
	case runtime.PhaseError,
		runtime.PhaseStopping,
		runtime.PhaseStopped,
		runtime.PhaseInitializing,
		runtime.PhaseLoading,
		runtime.PhaseStarting:
		return false
	default:
		// Future phases fall back to actual running state.
		return s.IsRunning()
	}
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
	if supervisor := s.runtime.Supervisor(); supervisor != nil {
		if view := supervisor.StateView(); view != nil {
			snapshot := view.Snapshot()

			connectedCount := 0
			connectingCount := 0
			totalTools := 0

			serverStats := make(map[string]interface{}, len(snapshot.Servers))

			for name, status := range snapshot.Servers {
				if status == nil {
					continue
				}

				var connInfo *types.ConnectionInfo
				if meta, ok := status.Metadata["connection_info"]; ok {
					if info, ok := meta.(*types.ConnectionInfo); ok {
						connInfo = info
					}
				}

				state := status.State
				if connInfo != nil {
					state = connInfo.State.String()
				}
				if state == "" {
					if status.Enabled {
						if status.Connected {
							state = "Ready"
						} else {
							state = "Disconnected"
						}
					} else {
						state = "Disabled"
					}
				}

				connecting := strings.EqualFold(state, "connecting")

				entry := map[string]interface{}{
					"state":        state,
					"connected":    status.Connected,
					"connecting":   connecting,
					"retry_count":  status.RetryCount,
					"should_retry": false,
					"name":         status.Name,
					"tool_count":   status.ToolCount,
				}

				if entry["name"] == "" {
					entry["name"] = name
				}

				if status.Config != nil {
					if status.Config.URL != "" {
						entry["url"] = status.Config.URL
					}
					if status.Config.Protocol != "" {
						entry["protocol"] = status.Config.Protocol
					}
				}

				if connInfo != nil {
					entry["retry_count"] = connInfo.RetryCount
					if connInfo.LastError != nil {
						entry["last_error"] = connInfo.LastError.Error()
					}
					if !connInfo.LastRetryTime.IsZero() {
						entry["last_retry_time"] = connInfo.LastRetryTime
					}
					if connInfo.ServerName != "" {
						entry["server_name"] = connInfo.ServerName
					}
					if connInfo.ServerVersion != "" {
						entry["server_version"] = connInfo.ServerVersion
					}
				} else {
					if status.LastError != "" {
						entry["last_error"] = status.LastError
					}
					if status.LastErrorTime != nil {
						entry["last_retry_time"] = *status.LastErrorTime
					}
				}

				if status.Connected {
					connectedCount++
				}
				if connecting {
					connectingCount++
				}
				totalTools += status.ToolCount

				serverStats[name] = entry
			}

			return map[string]interface{}{
				"connected_servers":  connectedCount,
				"connecting_servers": connectingCount,
				"total_servers":      len(snapshot.Servers),
				"servers":            serverStats,
				"total_tools":        totalTools,
			}
		}
	}

	stats := s.runtime.UpstreamManager().GetStats()

	// Enhance stats with tool counts per server when falling back
	if servers, ok := stats["servers"].(map[string]interface{}); ok {
		for id, serverInfo := range servers {
			if serverMap, ok := serverInfo.(map[string]interface{}); ok {
				serverMap["tool_count"] = s.getServerToolCount(id)
			}
		}
	}

	return stats
}

// GetAllServers returns information about all upstream servers for tray UI.
// Phase 6: Uses lock-free StateView for instant responses (<1ms) even during tool indexing.
func (s *Server) GetAllServers() ([]map[string]interface{}, error) {
	s.logger.Debug("GetAllServers called (Phase 6: using StateView)")

	// Phase 6: Use Supervisor's StateView for lock-free, instant reads
	supervisor := s.runtime.Supervisor()
	if supervisor == nil {
		s.logger.Warn("GetAllServers: supervisor not available, falling back to storage")
		return s.getAllServersLegacy()
	}

	stateView := supervisor.StateView()
	if stateView == nil {
		s.logger.Warn("GetAllServers: StateView not available, falling back to storage")
		return s.getAllServersLegacy()
	}

	// Get snapshot - this is lock-free and instant
	snapshot := stateView.Snapshot()
	s.logger.Debug("StateView snapshot retrieved", zap.Int("count", len(snapshot.Servers)))

	result := make([]map[string]interface{}, 0, len(snapshot.Servers))
	for _, serverStatus := range snapshot.Servers {
		// Convert StateView ServerStatus to API response format
		connected := serverStatus.Connected
		connecting := serverStatus.State == "connecting"

		status := serverStatus.State
		if status == "" {
			if serverStatus.Enabled {
				if connecting {
					status = "connecting"
				} else if connected {
					status = "ready"
				} else {
					status = "disconnected"
				}
			} else {
				status = "disabled"
			}
		}

		// Extract created time and config fields
		var created time.Time
		var url, command, protocol string
		if serverStatus.Config != nil {
			created = serverStatus.Config.Created
			url = serverStatus.Config.URL
			command = serverStatus.Config.Command
			protocol = serverStatus.Config.Protocol
		}

		result = append(result, map[string]interface{}{
			"name":            serverStatus.Name,
			"url":             url,
			"command":         command,
			"protocol":        protocol,
			"enabled":         serverStatus.Enabled,
			"quarantined":     serverStatus.Quarantined,
			"created":         created,
			"connected":       connected,
			"connecting":      connecting,
			"tool_count":      serverStatus.ToolCount,
			"last_error":      serverStatus.LastError,
			"status":          status,
			"should_retry":    false, // Managed by Actor internally now
			"retry_count":     serverStatus.RetryCount,
			"last_retry_time": nil, // Actor tracks this internally
		})
	}

	s.logger.Debug("GetAllServers completed", zap.Int("server_count", len(result)))
	return result, nil
}

// getAllServersLegacy is the old storage-based implementation, kept as fallback.
// This should rarely be called after Phase 6 integration.
func (s *Server) getAllServersLegacy() ([]map[string]interface{}, error) {
	s.logger.Warn("Using legacy storage-based GetAllServers (slow path)")

	// Check if storage manager is available
	if s.runtime.StorageManager() == nil {
		s.logger.Warn("getAllServersLegacy: storage manager is nil")
		return []map[string]interface{}{}, nil
	}

	servers, err := s.runtime.StorageManager().ListUpstreamServers()
	if err != nil {
		// Handle database closed gracefully
		if strings.Contains(err.Error(), "database not open") || strings.Contains(err.Error(), "closed") {
			s.logger.Debug("Database not available for getAllServersLegacy, returning empty list")
			return []map[string]interface{}{}, nil
		}
		s.logger.Error("ListUpstreamServers failed", zap.Error(err))
		return nil, err
	}

	var result []map[string]interface{}
	for _, server := range servers {
		// Get connection status from upstream manager
		var connected bool
		var connecting bool
		var lastError string
		var toolCount int
		var status string

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
				if st, ok := connectionStatus["state"].(string); ok && st != "" {
					status = st
				}
				if connected {
					toolCount = 0 // Skip slow tool count during indexing
					status = "ready"
				}
			}
		}

		if status == "" {
			if server.Enabled {
				if connecting {
					status = "connecting"
				} else {
					status = "disconnected"
				}
			} else {
				status = "disabled"
			}
		}

		result = append(result, map[string]interface{}{
			"name":            server.Name,
			"url":             server.URL,
			"command":         server.Command,
			"protocol":        server.Protocol,
			"enabled":         server.Enabled,
			"quarantined":     server.Quarantined,
			"created":         server.Created,
			"connected":       connected,
			"connecting":      connecting,
			"tool_count":      toolCount,
			"last_error":      lastError,
			"status":          status,
			"should_retry":    false,
			"retry_count":     0,
			"last_retry_time": nil,
		})
	}

	return result, nil
}

// GetQuarantinedServers returns information about quarantined servers for tray UI
func (s *Server) GetQuarantinedServers() ([]map[string]interface{}, error) {
	s.logger.Debug("GetQuarantinedServers called (Phase 7.1: using StateView)")

	// Phase 7.1: Use StateView for lock-free read
	supervisor := s.runtime.Supervisor()
	if supervisor == nil {
		s.logger.Warn("Supervisor not available, returning empty list")
		return []map[string]interface{}{}, nil
	}

	snapshot := supervisor.StateView().Snapshot()

	result := make([]map[string]interface{}, 0)
	for _, serverStatus := range snapshot.Servers {
		if !serverStatus.Quarantined {
			continue
		}

		// Extract config fields
		var created time.Time
		var url, command, protocol string
		if serverStatus.Config != nil {
			created = serverStatus.Config.Created
			url = serverStatus.Config.URL
			command = serverStatus.Config.Command
			protocol = serverStatus.Config.Protocol
		}

		result = append(result, map[string]interface{}{
			"name":        serverStatus.Name,
			"url":         url,
			"command":     command,
			"protocol":    protocol,
			"enabled":     serverStatus.Enabled,
			"quarantined": true,
			"created":     created,
			"connected":   serverStatus.Connected,
			"tool_count":  serverStatus.ToolCount,
		})

		s.logger.Debug("Added quarantined server to result",
			zap.String("server", serverStatus.Name))
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

// RestartServer restarts an upstream server
func (s *Server) RestartServer(serverName string) error {
	return s.runtime.RestartServer(serverName)
}

// ForceReconnectAllServers triggers reconnection attempts for all managed servers.
func (s *Server) ForceReconnectAllServers(reason string) error {
	s.logger.Info("HTTP API requested force reconnect for all upstream servers",
		zap.String("reason", reason))
	return s.runtime.ForceReconnectAllServers(reason)
}

// QuarantineServer quarantines/unquarantines a server
func (s *Server) QuarantineServer(serverName string, quarantined bool) error {
	return s.runtime.QuarantineServer(serverName, quarantined)
}

// getServerToolCount returns the number of tools for a specific server
// Returns cached tool count only (non-blocking) to avoid stalling SSE/API responses
func (s *Server) getServerToolCount(serverID string) int {
	client, exists := s.runtime.UpstreamManager().GetClient(serverID)
	if !exists {
		return 0
	}

	// Get the cached tool count directly without any blocking calls
	// This is safe to call from SSE/API handlers as it only reads from cache
	count := client.GetCachedToolCountNonBlocking()

	return count
}

// StartServer starts the server if it's not already running
func (s *Server) StartServer(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running {
		return fmt.Errorf("server is already running")
	}

	// CRITICAL: Validate data directory security BEFORE starting background goroutine
	// This ensures permission errors are returned synchronously with proper exit codes
	cfg := s.runtime.Config()
	if cfg != nil && cfg.DataDir != "" {
		if err := ValidateDataDirectory(cfg.DataDir, s.logger); err != nil {
			s.logger.Error("Data directory security validation failed",
				zap.Error(err),
				zap.String("fix", fmt.Sprintf("chmod 0700 %s", cfg.DataDir)))
			return &PermissionError{Path: cfg.DataDir, Err: err}
		}
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
				s.updateStatus(runtime.PhaseStopped, "Server has stopped")
			}
		}()

		s.mu.Lock()
		s.running = true
		s.mu.Unlock()
		s.runtime.SetRunning(true)

		// Notify about server start
		s.updateStatus(runtime.PhaseStarting, "Server is starting...")

		serverError = s.Start(s.serverCtx)
		if serverError != nil && serverError != context.Canceled {
			s.logger.Error("Server error during background start", zap.Error(serverError))
			s.updateStatus(runtime.PhaseError, fmt.Sprintf("Server error: %v", serverError))
		}
	}()

	return nil
}

// StopServer stops the server if it's running
func (s *Server) StopServer() error {
	s.logger.Info("STOPSERVER CALLED - STARTING SHUTDOWN SEQUENCE")
	_ = s.logger.Sync()

	s.mu.Lock()
	// Check if Shutdown() has already been called - prevent duplicate shutdown
	if s.shutdown {
		s.mu.Unlock()
		s.logger.Debug("Server shutdown already in progress via Shutdown(), skipping StopServer")
		return nil
	}
	if !s.running {
		s.mu.Unlock()
		// Return nil instead of error to prevent race condition logs
		s.logger.Debug("Server stop requested but server is not running")
		return nil
	}

	// Capture httpServer reference while holding the lock
	httpServer := s.httpServer
	s.mu.Unlock()

	// Notify about server stopping
	s.logger.Info("STOPSERVER - Server is running, proceeding with stop")
	_ = s.logger.Sync()

	s.updateStatus(runtime.PhaseStopping, "Server is stopping...")

	// STEP 1: Gracefully shutdown HTTP server FIRST to stop accepting new connections
	// This must happen before we disconnect upstream servers to prevent new requests
	if httpServer != nil {
		s.logger.Info("STOPSERVER - Shutting down HTTP server gracefully")
		_ = s.logger.Sync()

		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			s.logger.Warn("STOPSERVER - HTTP server forced shutdown due to timeout", zap.Error(err))
			// Force close if graceful shutdown times out
			if closeErr := httpServer.Close(); closeErr != nil {
				s.logger.Error("STOPSERVER - Failed to force close HTTP server", zap.Error(closeErr))
			}
		} else {
			s.logger.Info("STOPSERVER - HTTP server shutdown completed gracefully")
		}
		_ = s.logger.Sync()
	}

	// STEP 2: Disconnect upstream servers AFTER HTTP server is shut down
	// This ensures no new requests can come in while we're disconnecting
	// Use a FRESH context (not the cancelled server context) for cleanup
	s.logger.Info("STOPSERVER - Disconnecting upstream servers with parallel cleanup")
	_ = s.logger.Sync()

	// NEW: Create dedicated cleanup context with generous timeout (45 seconds)
	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cleanupCancel()

	// NEW: Use ShutdownAll for parallel cleanup with proper container verification
	if err := s.runtime.UpstreamManager().ShutdownAll(cleanupCtx); err != nil {
		s.logger.Error("STOPSERVER - Failed to shutdown upstream servers", zap.Error(err))
		_ = s.logger.Sync()
	} else {
		s.logger.Info("STOPSERVER - Successfully shutdown all upstream servers")
		_ = s.logger.Sync()
	}

	// NEW: Verify all containers stopped with retry loop (instead of arbitrary 3s sleep)
	if s.runtime.UpstreamManager().HasDockerContainers() {
		s.logger.Warn("STOPSERVER - Docker containers still running, verifying cleanup...")
		_ = s.logger.Sync()
		s.verifyContainersCleanedUp(cleanupCtx)
	} else {
		s.logger.Info("STOPSERVER - All Docker containers cleaned up successfully")
		_ = s.logger.Sync()
	}

	// STEP 3: Cancel the server context to signal other components
	s.logger.Info("STOPSERVER - Cancelling server context")
	_ = s.logger.Sync()
	s.mu.Lock()
	if s.serverCancel != nil {
		s.serverCancel()
	}

	// Set running to false immediately after server is shut down
	s.running = false
	s.listenAddr = ""
	s.mu.Unlock()
	s.runtime.SetRunning(false)

	// Notify about server stopped with explicit status update
	s.updateStatus(runtime.PhaseStopped, "Server has been stopped")

	s.logger.Info("STOPSERVER - All operations completed successfully")
	_ = s.logger.Sync() // Final log flush

	return nil
}

// verifyContainersCleanedUp verifies all Docker containers have stopped and forces cleanup if needed
func (s *Server) verifyContainersCleanedUp(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	maxAttempts := 15 // 15 seconds total
	for attempt := 0; attempt < maxAttempts; attempt++ {
		select {
		case <-ctx.Done():
			s.logger.Error("STOPSERVER - Cleanup verification timeout", zap.Error(ctx.Err()))
			_ = s.logger.Sync()
			// Force cleanup as last resort
			s.runtime.UpstreamManager().ForceCleanupAllContainers()
			return
		case <-ticker.C:
			if !s.runtime.UpstreamManager().HasDockerContainers() {
				s.logger.Info("STOPSERVER - All containers cleaned up successfully",
					zap.Int("attempts", attempt+1))
				_ = s.logger.Sync()
				return
			}
			s.logger.Debug("STOPSERVER - Waiting for container cleanup...",
				zap.Int("attempt", attempt+1),
				zap.Int("max_attempts", maxAttempts))
		}
	}

	// Timeout reached - force cleanup
	s.logger.Error("STOPSERVER - Some containers failed to stop gracefully - forcing cleanup")
	_ = s.logger.Sync()
	s.runtime.UpstreamManager().ForceCleanupAllContainers()

	// Give force cleanup a moment to complete
	time.Sleep(2 * time.Second)

	if s.runtime.UpstreamManager().HasDockerContainers() {
		s.logger.Error("STOPSERVER - WARNING: Some containers may still be running after force cleanup")
		_ = s.logger.Sync()
	} else {
		s.logger.Info("STOPSERVER - Force cleanup succeeded - all containers removed")
		_ = s.logger.Sync()
	}
}

func resolveDisplayAddress(actual, requested string) string {
	host, port, err := net.SplitHostPort(actual)
	if err != nil {
		return actual
	}

	if host == "" || host == "::" || host == "0.0.0.0" {
		if reqHost, _, reqErr := net.SplitHostPort(requested); reqErr == nil {
			if reqHost != "" && reqHost != "::" && reqHost != "0.0.0.0" {
				host = reqHost
			} else {
				host = "127.0.0.1"
			}
		} else {
			host = "127.0.0.1"
		}
	}

	return net.JoinHostPort(host, port)
}

// withHSTS adds HTTP Strict Transport Security headers
func withHSTS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains; preload")
		next.ServeHTTP(w, r)
	})
}

// startCustomHTTPServer creates a custom HTTP server that handles MCP endpoints
// It supports both TCP (for browsers) and Unix socket/named pipe (for tray) listeners
func (s *Server) startCustomHTTPServer(ctx context.Context, streamableServer *server.StreamableHTTPServer) error {
	cfg := s.runtime.Config()
	if cfg == nil {
		return fmt.Errorf("configuration not available")
	}

	// CRITICAL: Validate data directory security before starting
	if err := ValidateDataDirectory(cfg.DataDir, s.logger); err != nil {
		s.logger.Error("Data directory security validation failed",
			zap.Error(err),
			zap.String("fix", fmt.Sprintf("chmod 0700 %s", cfg.DataDir)))
		return &PermissionError{Path: cfg.DataDir, Err: err}
	}

	// Create listener manager
	listenerManager := NewListenerManager(&ListenerConfig{
		DataDir:      cfg.DataDir,
		TrayEndpoint: cfg.TrayEndpoint, // From config/CLI/env or auto-detect
		TCPAddress:   cfg.Listen,
		Logger:       s.logger,
	})

	// Create TCP listener (for browsers and remote clients)
	tcpListener, err := listenerManager.CreateTCPListener()
	if err != nil {
		return err
	}

	// Create tray listener (Unix socket or named pipe) if enabled
	var trayListener *Listener
	if cfg.EnableSocket {
		trayListener, err = listenerManager.CreateTrayListener()
		if err != nil {
			s.logger.Warn("Failed to create tray listener, tray will use TCP fallback",
				zap.Error(err))
			// Continue without tray listener - tray will fall back to TCP
		}
	} else {
		s.logger.Info("Socket communication disabled by configuration, clients will use TCP")
	}

	// Store listener manager for cleanup
	s.mu.Lock()
	s.listenerManager = listenerManager
	s.mu.Unlock()

	mux := http.NewServeMux()

	// Create a logging wrapper for debugging client connections
	loggingHandler := func(handler http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			// Extract connection source from context
			source := GetConnectionSource(r.Context())

			// Log incoming request with connection details
			s.logger.Debug("MCP client request received",
				zap.String("method", r.Method),
				zap.String("path", r.URL.Path),
				zap.String("remote_addr", r.RemoteAddr),
				zap.String("source", string(source)),
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
					zap.String("source", string(source)),
					zap.Int("status_code", wrappedWriter.statusCode),
					zap.Duration("duration", duration),
				)
			} else {
				s.logger.Debug("MCP client request completed successfully",
					zap.String("method", r.Method),
					zap.String("path", r.URL.Path),
					zap.String("remote_addr", r.RemoteAddr),
					zap.String("source", string(source)),
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

	// Determine actual TCP address for logging
	var actualAddr, displayAddr string
	if tcpListener != nil {
		actualAddr = tcpListener.Addr().String()
		displayAddr = resolveDisplayAddress(actualAddr, cfg.Listen)
	}

	// Log active listeners
	activeListeners := []string{}
	if tcpListener != nil {
		activeListeners = append(activeListeners, fmt.Sprintf("TCP: %s", displayAddr))
	}
	if trayListener != nil {
		activeListeners = append(activeListeners, fmt.Sprintf("Tray: %s", trayListener.Address))
	}

	s.logger.Info("Active listeners created",
		zap.Strings("listeners", activeListeners),
		zap.Int("count", len(activeListeners)))

	// Create multiplexing listener that combines TCP and tray listeners
	muxListener := &multiplexListener{
		listeners: []*Listener{},
		logger:    s.logger,
	}
	if tcpListener != nil {
		muxListener.listeners = append(muxListener.listeners, tcpListener)
	}
	if trayListener != nil {
		muxListener.listeners = append(muxListener.listeners, trayListener)
	}

	s.mu.Lock()
	s.httpServer = &http.Server{
		Addr:              cfg.Listen,
		Handler:           mux,
		ReadHeaderTimeout: 60 * time.Second,  // Increased for better client compatibility
		ReadTimeout:       120 * time.Second, // Full request read timeout
		WriteTimeout:      120 * time.Second, // Response write timeout
		IdleTimeout:       180 * time.Second, // Keep-alive timeout for persistent connections
		MaxHeaderBytes:    1 << 20,           // 1MB max header size
		// Enable connection state tracking for better debugging
		ConnState: s.logConnectionState,
		// Tag connections with their source (TCP vs Tray)
		ConnContext: func(ctx context.Context, c net.Conn) context.Context {
			// Extract source from tagged connection
			if tc, ok := c.(*taggedConn); ok {
				return TagConnectionContext(ctx, tc.source)
			}
			return TagConnectionContext(ctx, ConnectionSourceTCP) // Default to TCP
		},
	}
	s.running = true
	s.runtime.SetRunning(true)
	s.listenAddr = displayAddr
	s.mu.Unlock()

	// Broadcast running status with resolved listen address so readiness checks succeed immediately.
	s.updateStatus(runtime.PhaseRunning, fmt.Sprintf("Server is running on %s", displayAddr))

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
	if cfg.TLS != nil && cfg.TLS.Enabled {
		protocol = "HTTPS"
	}

	s.logger.Info(fmt.Sprintf("Starting MCP %s server with enhanced client stability", protocol),
		zap.String("protocol", protocol),
		zap.String("address", actualAddr),
		zap.String("requested_address", cfg.Listen),
		zap.Strings("endpoints", allEndpoints),
		zap.Duration("read_timeout", 120*time.Second),
		zap.Duration("write_timeout", 120*time.Second),
		zap.Duration("idle_timeout", 180*time.Second),
		zap.String("features", "connection_tracking,graceful_shutdown,enhanced_logging,dual_listener"),
	)

	// Setup error channel for server communication
	serverErrCh := make(chan error, 1)

	// Apply TLS configuration if enabled
	if cfg.TLS != nil && cfg.TLS.Enabled {
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
			if err := tlslocal.ServeWithTLS(s.httpServer, muxListener, tlsCfg); err != nil && err != http.ErrServerClosed {
				s.logger.Error("HTTPS server error", zap.Error(err))
				s.mu.Lock()
				s.running = false
				s.listenAddr = ""
				s.mu.Unlock()
				s.runtime.SetRunning(false)
				s.updateStatus(runtime.PhaseError, fmt.Sprintf("HTTPS server failed: %v", err))
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
			if err := s.httpServer.Serve(muxListener); err != nil && err != http.ErrServerClosed {
				s.logger.Error("HTTP server error", zap.Error(err))
				s.mu.Lock()
				s.running = false
				s.listenAddr = ""
				s.mu.Unlock()
				s.runtime.SetRunning(false)
				s.updateStatus(runtime.PhaseError, fmt.Sprintf("HTTP server failed: %v", err))
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
		s.logger.Info("Server context cancelled, shutdown will be handled by StopServer")
		// HTTP server shutdown is now handled synchronously in StopServer()
		// to avoid race conditions during graceful shutdown
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
	// Handle cases where conn or RemoteAddr might be nil
	remoteAddr := "unknown"
	if conn != nil {
		if addr := conn.RemoteAddr(); addr != nil {
			remoteAddr = addr.String()
		}
	}

	switch state {
	case http.StateNew:
		s.logger.Debug("New client connection established",
			zap.String("remote_addr", remoteAddr),
			zap.String("state", "new"))
	// StateActive and StateIdle removed - too noisy with keep-alive connections and SSE streams
	// case http.StateActive:
	// 	s.logger.Debug("Client connection active",
	// 		zap.String("remote_addr", conn.RemoteAddr().String()),
	// 		zap.String("state", "active"))
	// case http.StateIdle:
	// 	s.logger.Debug("Client connection idle",
	// 		zap.String("remote_addr", conn.RemoteAddr().String()),
	// 		zap.String("state", "idle"))
	case http.StateHijacked:
		s.logger.Debug("Client connection hijacked (likely for upgrade)",
			zap.String("remote_addr", remoteAddr),
			zap.String("state", "hijacked"))
	case http.StateClosed:
		s.logger.Debug("Client connection closed",
			zap.String("remote_addr", remoteAddr),
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
	s.logger.Debug("GetServerTools called (Phase 7.1: using StateView)", zap.String("server", serverName))

	// Phase 7.1: Use StateView for lock-free cached tool reads
	supervisor := s.runtime.Supervisor()
	if supervisor == nil {
		return nil, fmt.Errorf("supervisor not available")
	}

	snapshot := supervisor.StateView().Snapshot()
	serverStatus, exists := snapshot.Servers[serverName]
	if !exists {
		return nil, fmt.Errorf("server not found: %s", serverName)
	}

	if !serverStatus.Connected {
		return nil, fmt.Errorf("server %s is not connected", serverName)
	}

	// Convert cached tools to API response format
	result := make([]map[string]interface{}, len(serverStatus.Tools))
	for i, tool := range serverStatus.Tools {
		result[i] = map[string]interface{}{
			"name":        tool.Name,
			"description": tool.Description,
			"inputSchema": tool.InputSchema,
			"server_name": serverName,
		}
	}

	s.logger.Debug("Retrieved server tools from cache",
		zap.String("server", serverName),
		zap.Int("count", len(result)))
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

// NotifySecretsChanged notifies the runtime that secrets have changed
func (s *Server) NotifySecretsChanged(ctx context.Context, operation, secretName string) error {
	return s.runtime.NotifySecretsChanged(ctx, operation, secretName)
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
