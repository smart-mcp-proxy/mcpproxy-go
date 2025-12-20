package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"go.uber.org/zap"

	"mcpproxy-go/internal/config"
	"mcpproxy-go/internal/contracts"
	"mcpproxy-go/internal/logs"
	"mcpproxy-go/internal/management"
	"mcpproxy-go/internal/observability"
	"mcpproxy-go/internal/reqcontext"
	internalRuntime "mcpproxy-go/internal/runtime"
	"mcpproxy-go/internal/secret"
	"mcpproxy-go/internal/storage"
	"mcpproxy-go/internal/transport"
	"mcpproxy-go/internal/updatecheck"
)

const (
	asyncToggleTimeout = 5 * time.Second
	secretTypeKeyring  = "keyring"
)

// ServerController defines the interface for core server functionality
type ServerController interface {
	IsRunning() bool
	IsReady() bool
	GetListenAddress() string
	GetUpstreamStats() map[string]interface{}
	StartServer(ctx context.Context) error
	StopServer() error
	GetStatus() interface{}
	StatusChannel() <-chan interface{}
	EventsChannel() <-chan internalRuntime.Event

	// Server management
	GetAllServers() ([]map[string]interface{}, error)
	EnableServer(serverName string, enabled bool) error
	RestartServer(serverName string) error
	ForceReconnectAllServers(reason string) error
	GetDockerRecoveryStatus() *storage.DockerRecoveryState
	QuarantineServer(serverName string, quarantined bool) error
	GetQuarantinedServers() ([]map[string]interface{}, error)
	UnquarantineServer(serverName string) error
	GetManagementService() interface{} // Returns the management service for unified operations
	DiscoverServerTools(ctx context.Context, serverName string) error

	// Tools and search
	GetServerTools(serverName string) ([]map[string]interface{}, error)
	SearchTools(query string, limit int) ([]map[string]interface{}, error)

	// Logs
	GetServerLogs(serverName string, tail int) ([]contracts.LogEntry, error)

	// Config and OAuth
	ReloadConfiguration() error
	GetConfigPath() string
	GetLogDir() string
	TriggerOAuthLogin(serverName string) error

	// Secrets management
	GetSecretResolver() *secret.Resolver
	GetCurrentConfig() interface{}
	NotifySecretsChanged(ctx context.Context, operation, secretName string) error

	// Tool call history
	GetToolCalls(limit, offset int) ([]*contracts.ToolCallRecord, int, error)
	GetToolCallByID(id string) (*contracts.ToolCallRecord, error)
	GetServerToolCalls(serverName string, limit int) ([]*contracts.ToolCallRecord, error)
	ReplayToolCall(id string, arguments map[string]interface{}) (*contracts.ToolCallRecord, error)
	GetToolCallsBySession(sessionID string, limit, offset int) ([]*contracts.ToolCallRecord, int, error)

	// Session management
	GetRecentSessions(limit int) ([]*contracts.MCPSession, int, error)
	GetSessionByID(sessionID string) (*contracts.MCPSession, error)

	// Configuration management
	ValidateConfig(cfg *config.Config) ([]config.ValidationError, error)
	ApplyConfig(cfg *config.Config, cfgPath string) (*internalRuntime.ConfigApplyResult, error)
	GetConfig() (*config.Config, error)

	// Token statistics
	GetTokenSavings() (*contracts.ServerTokenMetrics, error)

	// Tool execution
	CallTool(ctx context.Context, toolName string, arguments map[string]interface{}) (interface{}, error)

	// Registry browsing (Phase 7)
	ListRegistries() ([]interface{}, error)
	SearchRegistryServers(registryID, tag, query string, limit int) ([]interface{}, error)

	// Version and updates
	GetVersionInfo() *updatecheck.VersionInfo
	RefreshVersionInfo() *updatecheck.VersionInfo
}

// Server provides HTTP API endpoints with chi router
type Server struct {
	controller    ServerController
	logger        *zap.SugaredLogger
	httpLogger    *zap.Logger // Separate logger for HTTP requests
	router        *chi.Mux
	observability *observability.Manager
}

// NewServer creates a new HTTP API server
func NewServer(controller ServerController, logger *zap.SugaredLogger, obs *observability.Manager) *Server {
	// Create HTTP logger for API request logging
	httpLogger, err := logs.CreateHTTPLogger(nil) // Use default config
	if err != nil {
		logger.Warnf("Failed to create HTTP logger: %v", err)
		httpLogger = zap.NewNop() // Use no-op logger as fallback
	}

	s := &Server{
		controller:    controller,
		logger:        logger,
		httpLogger:    httpLogger,
		router:        chi.NewRouter(),
		observability: obs,
	}

	s.setupRoutes()
	return s
}

// apiKeyAuthMiddleware creates middleware for API key authentication
// Connections from Unix socket/named pipe (tray) are trusted and skip API key validation
func (s *Server) apiKeyAuthMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// SECURITY: Trust connections from tray (Unix socket/named pipe)
			// These connections are authenticated via OS-level permissions (UID/SID matching)
			source := transport.GetConnectionSource(r.Context())
			if source == transport.ConnectionSourceTray {
				s.logger.Debug("Tray connection - skipping API key validation",
					zap.String("path", r.URL.Path),
					zap.String("remote_addr", r.RemoteAddr),
					zap.String("source", string(source)))
				next.ServeHTTP(w, r)
				return
			}

			// Get config from controller
			configInterface := s.controller.GetCurrentConfig()
			if configInterface == nil {
				// No config available (testing scenario) - allow through
				next.ServeHTTP(w, r)
				return
			}

			// Cast to config type
			cfg, ok := configInterface.(*config.Config)
			if !ok {
				// Config is not the expected type (testing scenario) - allow through
				next.ServeHTTP(w, r)
				return
			}

			// SECURITY: API key is REQUIRED for all TCP connections to REST API
			// Empty API key is not allowed - this prevents accidental exposure
			if cfg.APIKey == "" {
				s.logger.Warn("TCP connection rejected - API key not configured",
					zap.String("path", r.URL.Path),
					zap.String("remote_addr", r.RemoteAddr))
				s.writeError(w, http.StatusUnauthorized, "API key authentication required but not configured. Please set MCPPROXY_API_KEY or configure api_key in config file.")
				return
			}

			// TCP connections require API key validation
			if !s.validateAPIKey(r, cfg.APIKey) {
				s.logger.Warn("TCP connection with invalid API key",
					zap.String("path", r.URL.Path),
					zap.String("remote_addr", r.RemoteAddr))
				s.writeError(w, http.StatusUnauthorized, "Invalid or missing API key")
				return
			}

			s.logger.Debug("TCP connection with valid API key",
				zap.String("path", r.URL.Path),
				zap.String("remote_addr", r.RemoteAddr))
			next.ServeHTTP(w, r)
		})
	}
}

// validateAPIKey checks if the request contains a valid API key
func (s *Server) validateAPIKey(r *http.Request, expectedKey string) bool {
	// Check X-API-Key header
	if key := r.Header.Get("X-API-Key"); key != "" {
		return key == expectedKey
	}

	// Check query parameter (for SSE and Web UI initial load)
	if key := r.URL.Query().Get("apikey"); key != "" {
		return key == expectedKey
	}

	return false
}

// correlationIDMiddleware injects correlation ID and request source into context
func (s *Server) correlationIDMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Generate or retrieve correlation ID
			correlationID := r.Header.Get("X-Correlation-ID")
			if correlationID == "" {
				correlationID = reqcontext.GenerateCorrelationID()
			}

			// Inject correlation ID and request source into context
			ctx := reqcontext.WithCorrelationID(r.Context(), correlationID)
			ctx = reqcontext.WithRequestSource(ctx, reqcontext.SourceRESTAPI)

			// Add correlation ID to response headers for client tracking
			w.Header().Set("X-Correlation-ID", correlationID)

			// Continue with enriched context
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// ServeHTTP implements http.Handler
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.router.ServeHTTP(w, r)
}

// setupRoutes configures all API routes
func (s *Server) setupRoutes() {
	s.logger.Debug("Setting up HTTP API routes")

	// Observability middleware (if available)
	if s.observability != nil {
		s.router.Use(s.observability.HTTPMiddleware())
		s.logger.Debug("Observability middleware configured")
	}

	// Core middleware
	s.router.Use(s.httpLoggingMiddleware()) // Custom HTTP API logging
	s.router.Use(middleware.Recoverer)
	s.router.Use(middleware.RequestID)
	s.router.Use(s.correlationIDMiddleware()) // Correlation ID and request source tracking
	s.logger.Debug("Core middleware configured (logging, recovery, request ID, correlation ID)")

	// CORS headers for browser access
	s.router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-API-Key")

			if r.Method == "OPTIONS" {
				w.WriteHeader(http.StatusOK)
				return
			}

			next.ServeHTTP(w, r)
		})
	})

	// Health and readiness endpoints (Kubernetes-compatible with legacy aliases)
	// See healthzHandler() and readyzHandler() for swagger documentation
	livenessHandler := func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}
	readinessHandler := func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if s.controller.IsReady() {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ready":true}`))
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"ready":false}`))
	}

	// Observability endpoints (registered first to avoid conflicts)
	if s.observability != nil {
		if health := s.observability.Health(); health != nil {
			s.router.Get("/healthz", health.HealthzHandler())
			s.router.Get("/readyz", health.ReadyzHandler())
		}
		if metrics := s.observability.Metrics(); metrics != nil {
			s.router.Handle("/metrics", metrics.Handler())
		}
	} else {
		// Register custom health endpoints only if observability is not available
		for _, path := range []string{"/livez", "/healthz", "/health"} {
			s.router.Get(path, livenessHandler)
		}
		for _, path := range []string{"/readyz", "/ready"} {
			s.router.Get(path, readinessHandler)
		}
	}

	// Always register /ready as backup endpoint for tray compatibility
	s.router.Get("/ready", readinessHandler)

	// API v1 routes with timeout and authentication middleware
	s.router.Route("/api/v1", func(r chi.Router) {
		// Apply timeout and API key authentication middleware to API routes only
		r.Use(middleware.Timeout(60 * time.Second))
		r.Use(s.apiKeyAuthMiddleware())

		// Status endpoint
		r.Get("/status", s.handleGetStatus)

		// Info endpoint (server version, web UI URL, etc.)
		r.Get("/info", s.handleGetInfo)

		// Server management
		r.Get("/servers", s.handleGetServers)
		r.Post("/servers/reconnect", s.handleForceReconnectServers)
		// T076-T077: Bulk operation routes
		r.Post("/servers/restart_all", s.handleRestartAll)
		r.Post("/servers/enable_all", s.handleEnableAll)
		r.Post("/servers/disable_all", s.handleDisableAll)
		r.Route("/servers/{id}", func(r chi.Router) {
			r.Post("/enable", s.handleEnableServer)
			r.Post("/disable", s.handleDisableServer)
			r.Post("/restart", s.handleRestartServer)
			r.Post("/login", s.handleServerLogin)
			r.Post("/logout", s.handleServerLogout)
			r.Post("/quarantine", s.handleQuarantineServer)
			r.Post("/unquarantine", s.handleUnquarantineServer)
			r.Post("/discover-tools", s.handleDiscoverServerTools)
			r.Get("/tools", s.handleGetServerTools)
			r.Get("/logs", s.handleGetServerLogs)
			r.Get("/tool-calls", s.handleGetServerToolCalls)
		})

		// Search
		r.Get("/index/search", s.handleSearchTools)

		// Docker recovery status
		r.Get("/docker/status", s.handleGetDockerStatus)

		// Secrets management
		r.Route("/secrets", func(r chi.Router) {
			r.Get("/refs", s.handleGetSecretRefs)
			r.Get("/config", s.handleGetConfigSecrets)
			r.Post("/migrate", s.handleMigrateSecrets)
			r.Post("/", s.handleSetSecret)
			r.Delete("/{name}", s.handleDeleteSecret)
		})

		// Diagnostics
		r.Get("/diagnostics", s.handleGetDiagnostics)
		r.Get("/doctor", s.handleGetDiagnostics) // Alias for consistency with CLI command

		// Token statistics
		r.Get("/stats/tokens", s.handleGetTokenStats)

		// Tool call history
		r.Get("/tool-calls", s.handleGetToolCalls)
		r.Get("/tool-calls/{id}", s.handleGetToolCallDetail)
		r.Post("/tool-calls/{id}/replay", s.handleReplayToolCall)

		// Session management
		r.Get("/sessions", s.handleGetSessions)
		r.Get("/sessions/{id}", s.handleGetSessionDetail)

		// Tool execution
		r.Post("/tools/call", s.handleCallTool)

		// Code execution endpoint (for CLI client mode)
		r.Post("/code/exec", NewCodeExecHandler(s.controller, s.logger).ServeHTTP)

		// Configuration management
		r.Get("/config", s.handleGetConfig)
		r.Post("/config/validate", s.handleValidateConfig)
		r.Post("/config/apply", s.handleApplyConfig)

		// Registry browsing (Phase 7)
		r.Get("/registries", s.handleListRegistries)
		r.Get("/registries/{id}/servers", s.handleSearchRegistryServers)
	})

	// SSE events (protected by API key) - support both GET and HEAD
	s.router.With(s.apiKeyAuthMiddleware()).Method("GET", "/events", http.HandlerFunc(s.handleSSEEvents))
	s.router.With(s.apiKeyAuthMiddleware()).Method("HEAD", "/events", http.HandlerFunc(s.handleSSEEvents))

	// Note: Swagger UI is mounted directly on the main mux (not via HTTP API server)
	// See internal/server/server.go for swagger handler registration

	s.logger.Debug("HTTP API routes setup completed",
		"api_routes", "/api/v1/*",
		"sse_route", "/events",
		"health_routes", "/healthz,/readyz,/livez,/ready")
}

// httpLoggingMiddleware creates custom HTTP request logging middleware
func (s *Server) httpLoggingMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			// Create a response writer wrapper to capture status code
			ww := &responseWriter{ResponseWriter: w, statusCode: 200}

			// Process request
			next.ServeHTTP(ww, r)

			duration := time.Since(start)

			// Log request details to http.log
			s.httpLogger.Info("HTTP API Request",
				zap.String("method", r.Method),
				zap.String("path", r.URL.Path),
				zap.String("query", r.URL.RawQuery),
				zap.String("remote_addr", r.RemoteAddr),
				zap.String("user_agent", r.UserAgent()),
				zap.Int("status", ww.statusCode),
				zap.Duration("duration", duration),
				zap.String("referer", r.Referer()),
				zap.Int64("content_length", r.ContentLength),
			)
		})
	}
}

// responseWriter wraps http.ResponseWriter to capture status code
type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

// Flush implements http.Flusher interface by delegating to the underlying ResponseWriter
func (rw *responseWriter) Flush() {
	if flusher, ok := rw.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// Health and readiness documentation handlers (for swagger generation only)
// The actual handlers are registered in setupRoutes() and may come from observability package

// healthzHandler godoc
// @Summary      Get health status
// @Description  Get comprehensive health status including all component health (Kubernetes-compatible liveness probe)
// @Tags         health
// @Produce      json
// @Success      200 {object} observability.HealthResponse "Service is healthy"
// @Failure      503 {object} observability.HealthResponse "Service is unhealthy"
// @Router       /healthz [get]
func _healthzHandler() {} //nolint:unused // swagger documentation stub

// readyzHandler godoc
// @Summary      Get readiness status
// @Description  Get readiness status including all component readiness checks (Kubernetes-compatible readiness probe)
// @Tags         health
// @Produce      json
// @Success      200 {object} observability.ReadinessResponse "Service is ready"
// @Failure      503 {object} observability.ReadinessResponse "Service is not ready"
// @Router       /readyz [get]
func _readyzHandler() {} //nolint:unused // swagger documentation stub

// JSON response helpers

func (s *Server) writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		s.logger.Error("Failed to encode JSON response", "error", err)
	}
}

func (s *Server) writeError(w http.ResponseWriter, status int, message string) {
	s.writeJSON(w, status, contracts.NewErrorResponse(message))
}

func (s *Server) writeSuccess(w http.ResponseWriter, data interface{}) {
	s.writeJSON(w, http.StatusOK, contracts.NewSuccessResponse(data))
}

// API v1 handlers

// handleGetStatus godoc
// @Summary Get server status
// @Description Get comprehensive server status including running state, listen address, upstream statistics, and timestamp
// @Tags status
// @Produce json
// @Security ApiKeyAuth
// @Security ApiKeyQuery
// @Success 200 {object} contracts.SuccessResponse "Server status information"
// @Failure 500 {object} contracts.ErrorResponse "Internal server error"
// @Router /api/v1/status [get]
func (s *Server) handleGetStatus(w http.ResponseWriter, _ *http.Request) {
	response := map[string]interface{}{
		"running":        s.controller.IsRunning(),
		"listen_addr":    s.controller.GetListenAddress(),
		"upstream_stats": s.controller.GetUpstreamStats(),
		"status":         s.controller.GetStatus(),
		"timestamp":      time.Now().Unix(),
	}

	s.writeSuccess(w, response)
}

// handleGetInfo godoc
// @Summary Get server information
// @Description Get essential server metadata including version, web UI URL, endpoint addresses, and update availability
// @Description This endpoint is designed for tray-core communication and version checking
// @Description Use refresh=true query parameter to force an immediate update check against GitHub
// @Tags status
// @Produce json
// @Param refresh query boolean false "Force immediate update check against GitHub"
// @Security ApiKeyAuth
// @Security ApiKeyQuery
// @Success 200 {object} contracts.APIResponse{data=contracts.InfoResponse} "Server information with optional update info"
// @Failure 500 {object} contracts.ErrorResponse "Internal server error"
// @Router /api/v1/info [get]
func (s *Server) handleGetInfo(w http.ResponseWriter, r *http.Request) {
	listenAddr := s.controller.GetListenAddress()

	// Build web UI URL from listen address (includes API key if configured)
	webUIURL := s.buildWebUIURLWithAPIKey(listenAddr, r)

	// Get version from build info or environment
	version := GetBuildVersion()

	response := map[string]interface{}{
		"version":     version,
		"web_ui_url":  webUIURL,
		"listen_addr": listenAddr,
		"endpoints": map[string]interface{}{
			"http":   listenAddr,
			"socket": getSocketPath(), // Returns socket path if enabled, empty otherwise
		},
	}

	// Add update information - refresh if requested
	refresh := r.URL.Query().Get("refresh") == "true"
	var versionInfo *updatecheck.VersionInfo
	if refresh {
		versionInfo = s.controller.RefreshVersionInfo()
	} else {
		versionInfo = s.controller.GetVersionInfo()
	}
	if versionInfo != nil {
		response["update"] = versionInfo.ToAPIResponse()
	}

	s.writeSuccess(w, response)
}

// buildWebUIURL constructs the web UI URL based on listen address and request
func buildWebUIURL(listenAddr string, r *http.Request) string {
	if listenAddr == "" {
		return ""
	}

	// Determine protocol from request
	protocol := "http"
	if r.TLS != nil {
		protocol = "https"
	}

	// If listen address is just a port, use localhost
	if strings.HasPrefix(listenAddr, ":") {
		return fmt.Sprintf("%s://127.0.0.1%s/ui/", protocol, listenAddr)
	}

	// Use the listen address as-is
	return fmt.Sprintf("%s://%s/ui/", protocol, listenAddr)
}

// buildWebUIURLWithAPIKey constructs the web UI URL with API key included if configured
func (s *Server) buildWebUIURLWithAPIKey(listenAddr string, r *http.Request) string {
	baseURL := buildWebUIURL(listenAddr, r)
	if baseURL == "" {
		return ""
	}

	// Add API key if configured
	cfg, err := s.controller.GetConfig()
	if err == nil && cfg.APIKey != "" {
		return baseURL + "?apikey=" + cfg.APIKey
	}

	return baseURL
}

// buildVersion is set during build using -ldflags
var buildVersion = "development"

// GetBuildVersion returns the build version from build-time variables.
// This should be set during build using -ldflags.
func GetBuildVersion() string {
	return buildVersion
}

// getSocketPath returns the socket path if socket communication is enabled
func getSocketPath() string {
	// This would ideally be retrieved from the config
	// For now, return empty string as socket info is not critical for this endpoint
	return ""
}

// handleGetServers godoc
// @Summary List all upstream MCP servers
// @Description Get a list of all configured upstream MCP servers with their connection status and statistics
// @Tags servers
// @Produce json
// @Security ApiKeyAuth
// @Security ApiKeyQuery
// @Success 200 {object} contracts.GetServersResponse "Server list with statistics"
// @Failure 500 {object} contracts.ErrorResponse "Internal server error"
// @Router /api/v1/servers [get]
func (s *Server) handleGetServers(w http.ResponseWriter, r *http.Request) {
	// Try to use management service if available
	if mgmtSvc := s.controller.GetManagementService(); mgmtSvc != nil {
		// Use new management service path
		servers, stats, err := mgmtSvc.(interface {
			ListServers(context.Context) ([]*contracts.Server, *contracts.ServerStats, error)
		}).ListServers(r.Context())

		if err != nil {
			s.logger.Error("Failed to list servers via management service", "error", err)
			s.writeError(w, http.StatusInternalServerError, "Failed to get servers")
			return
		}

		// Convert []*Server to []Server
		serverValues := make([]contracts.Server, len(servers))
		for i, srv := range servers {
			if srv != nil {
				serverValues[i] = *srv
			}
		}

		// Dereference stats pointer
		var statsValue contracts.ServerStats
		if stats != nil {
			statsValue = *stats
		}

		response := contracts.GetServersResponse{
			Servers: serverValues,
			Stats:   statsValue,
		}
		s.writeSuccess(w, response)
		return
	}

	// Fallback to legacy path if management service not available
	genericServers, err := s.controller.GetAllServers()
	if err != nil {
		s.logger.Error("Failed to get servers", "error", err)
		s.writeError(w, http.StatusInternalServerError, "Failed to get servers")
		return
	}

	// Convert to typed servers
	servers := contracts.ConvertGenericServersToTyped(genericServers)
	stats := contracts.ConvertUpstreamStatsToServerStats(s.controller.GetUpstreamStats())

	response := contracts.GetServersResponse{
		Servers: servers,
		Stats:   stats,
	}

	s.writeSuccess(w, response)
}

// handleEnableServer godoc
// @Summary Enable an upstream server
// @Description Enable a specific upstream MCP server
// @Tags servers
// @Produce json
// @Security ApiKeyAuth
// @Security ApiKeyQuery
// @Param id path string true "Server ID or name"
// @Success 200 {object} contracts.ServerActionResponse "Server enabled successfully"
// @Failure 400 {object} contracts.ErrorResponse "Bad request"
// @Failure 404 {object} contracts.ErrorResponse "Server not found"
// @Failure 500 {object} contracts.ErrorResponse "Internal server error"
// @Router /api/v1/servers/{id}/enable [post]
func (s *Server) handleEnableServer(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "id")
	if serverID == "" {
		s.writeError(w, http.StatusBadRequest, "Server ID required")
		return
	}

	// Try to use management service if available
	if mgmtSvc := s.controller.GetManagementService(); mgmtSvc != nil {
		err := mgmtSvc.(interface {
			EnableServer(context.Context, string, bool) error
		}).EnableServer(r.Context(), serverID, true)

		if err != nil {
			s.logger.Error("Failed to enable server via management service", "server", serverID, "error", err)
			s.writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to enable server: %v", err))
			return
		}

		response := contracts.ServerActionResponse{
			Server:  serverID,
			Action:  "enable",
			Success: true,
			Async:   false, // Management service is synchronous
		}
		s.writeSuccess(w, response)
		return
	}

	// Fallback to legacy async path
	async, err := s.toggleServerAsync(serverID, true)
	if err != nil {
		s.logger.Error("Failed to enable server", "server", serverID, "error", err)
		s.writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to enable server: %v", err))
		return
	}

	if async {
		s.logger.Debug("Server enable dispatched asynchronously", "server", serverID)
	} else {
		s.logger.Debug("Server enable completed synchronously", "server", serverID)
	}

	response := contracts.ServerActionResponse{
		Server:  serverID,
		Action:  "enable",
		Success: true,
		Async:   async,
	}

	s.writeSuccess(w, response)
}

// handleDisableServer godoc
// @Summary Disable an upstream server
// @Description Disable a specific upstream MCP server
// @Tags servers
// @Produce json
// @Security ApiKeyAuth
// @Security ApiKeyQuery
// @Param id path string true "Server ID or name"
// @Success 200 {object} contracts.ServerActionResponse "Server disabled successfully"
// @Failure 400 {object} contracts.ErrorResponse "Bad request"
// @Failure 404 {object} contracts.ErrorResponse "Server not found"
// @Failure 500 {object} contracts.ErrorResponse "Internal server error"
// @Router /api/v1/servers/{id}/disable [post]
func (s *Server) handleDisableServer(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "id")
	if serverID == "" {
		s.writeError(w, http.StatusBadRequest, "Server ID required")
		return
	}

	// Try to use management service if available
	if mgmtSvc := s.controller.GetManagementService(); mgmtSvc != nil {
		err := mgmtSvc.(interface {
			EnableServer(context.Context, string, bool) error
		}).EnableServer(r.Context(), serverID, false)

		if err != nil {
			s.logger.Error("Failed to disable server via management service", "server", serverID, "error", err)
			s.writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to disable server: %v", err))
			return
		}

		response := contracts.ServerActionResponse{
			Server:  serverID,
			Action:  "disable",
			Success: true,
			Async:   false, // Management service is synchronous
		}
		s.writeSuccess(w, response)
		return
	}

	// Fallback to legacy async path
	async, err := s.toggleServerAsync(serverID, false)
	if err != nil {
		s.logger.Error("Failed to disable server", "server", serverID, "error", err)
		s.writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to disable server: %v", err))
		return
	}

	if async {
		s.logger.Debug("Server disable dispatched asynchronously", "server", serverID)
	} else {
		s.logger.Debug("Server disable completed synchronously", "server", serverID)
	}

	response := contracts.ServerActionResponse{
		Server:  serverID,
		Action:  "disable",
		Success: true,
		Async:   async,
	}

	s.writeSuccess(w, response)
}

// handleForceReconnectServers godoc
// @Summary Reconnect all servers
// @Description Force reconnection to all upstream MCP servers
// @Tags servers
// @Produce json
// @Security ApiKeyAuth
// @Security ApiKeyQuery
// @Param reason query string false "Reason for reconnection"
// @Success 200 {object} contracts.ServerActionResponse "All servers reconnected successfully"
// @Failure 500 {object} contracts.ErrorResponse "Internal server error"
// @Router /api/v1/servers/reconnect [post]
func (s *Server) handleForceReconnectServers(w http.ResponseWriter, r *http.Request) {
	reason := r.URL.Query().Get("reason")

	if err := s.controller.ForceReconnectAllServers(reason); err != nil {
		s.logger.Error("Failed to trigger force reconnect for servers",
			"reason", reason,
			"error", err)
		s.writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to reconnect servers: %v", err))
		return
	}

	response := contracts.ServerActionResponse{
		Server:  "*",
		Action:  "reconnect_all",
		Success: true,
	}

	s.writeSuccess(w, response)
}

// T073: handleRestartAll godoc
// @Summary Restart all servers
// @Description Restart all configured upstream MCP servers sequentially with partial failure handling
// @Tags servers
// @Produce json
// @Security ApiKeyAuth
// @Security ApiKeyQuery
// @Success 200 {object} management.BulkOperationResult "Bulk restart results with success/failure counts"
// @Failure 403 {object} contracts.ErrorResponse "Forbidden (management disabled)"
// @Failure 500 {object} contracts.ErrorResponse "Internal server error"
// @Router /api/v1/servers/restart_all [post]
func (s *Server) handleRestartAll(w http.ResponseWriter, r *http.Request) {
	// Get management service from controller
	mgmtSvc, ok := s.controller.GetManagementService().(interface {
		RestartAll(ctx context.Context) (*management.BulkOperationResult, error)
	})
	if !ok {
		s.logger.Error("Failed to get management service")
		s.writeError(w, http.StatusInternalServerError, "Management service not available")
		return
	}

	result, err := mgmtSvc.RestartAll(r.Context())
	if err != nil {
		s.logger.Error("RestartAll operation failed", "error", err)
		s.writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to restart all servers: %v", err))
		return
	}

	s.writeSuccess(w, result)
}

// T074: handleEnableAll godoc
// @Summary Enable all servers
// @Description Enable all configured upstream MCP servers with partial failure handling
// @Tags servers
// @Produce json
// @Security ApiKeyAuth
// @Security ApiKeyQuery
// @Success 200 {object} management.BulkOperationResult "Bulk enable results with success/failure counts"
// @Failure 403 {object} contracts.ErrorResponse "Forbidden (management disabled)"
// @Failure 500 {object} contracts.ErrorResponse "Internal server error"
// @Router /api/v1/servers/enable_all [post]
func (s *Server) handleEnableAll(w http.ResponseWriter, r *http.Request) {
	// Get management service from controller
	mgmtSvc, ok := s.controller.GetManagementService().(interface {
		EnableAll(ctx context.Context) (*management.BulkOperationResult, error)
	})
	if !ok {
		s.logger.Error("Failed to get management service")
		s.writeError(w, http.StatusInternalServerError, "Management service not available")
		return
	}

	result, err := mgmtSvc.EnableAll(r.Context())
	if err != nil {
		s.logger.Error("EnableAll operation failed", "error", err)
		s.writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to enable all servers: %v", err))
		return
	}

	s.writeSuccess(w, result)
}

// T075: handleDisableAll godoc
// @Summary Disable all servers
// @Description Disable all configured upstream MCP servers with partial failure handling
// @Tags servers
// @Produce json
// @Security ApiKeyAuth
// @Security ApiKeyQuery
// @Success 200 {object} management.BulkOperationResult "Bulk disable results with success/failure counts"
// @Failure 403 {object} contracts.ErrorResponse "Forbidden (management disabled)"
// @Failure 500 {object} contracts.ErrorResponse "Internal server error"
// @Router /api/v1/servers/disable_all [post]
func (s *Server) handleDisableAll(w http.ResponseWriter, r *http.Request) {
	// Get management service from controller
	mgmtSvc, ok := s.controller.GetManagementService().(interface {
		DisableAll(ctx context.Context) (*management.BulkOperationResult, error)
	})
	if !ok {
		s.logger.Error("Failed to get management service")
		s.writeError(w, http.StatusInternalServerError, "Management service not available")
		return
	}

	result, err := mgmtSvc.DisableAll(r.Context())
	if err != nil {
		s.logger.Error("DisableAll operation failed", "error", err)
		s.writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to disable all servers: %v", err))
		return
	}

	s.writeSuccess(w, result)
}

// handleRestartServer godoc
// @Summary Restart an upstream server
// @Description Restart the connection to a specific upstream MCP server
// @Tags servers
// @Produce json
// @Security ApiKeyAuth
// @Security ApiKeyQuery
// @Param id path string true "Server ID or name"
// @Success 200 {object} contracts.ServerActionResponse "Server restarted successfully"
// @Failure 400 {object} contracts.ErrorResponse "Bad request"
// @Failure 404 {object} contracts.ErrorResponse "Server not found"
// @Failure 500 {object} contracts.ErrorResponse "Internal server error"
// @Router /api/v1/servers/{id}/restart [post]
func (s *Server) handleRestartServer(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "id")
	if serverID == "" {
		s.writeError(w, http.StatusBadRequest, "Server ID required")
		return
	}

	// Try to use management service if available
	if mgmtSvc := s.controller.GetManagementService(); mgmtSvc != nil {
		err := mgmtSvc.(interface {
			RestartServer(context.Context, string) error
		}).RestartServer(r.Context(), serverID)

		if err != nil {
			// Check if error is OAuth-related (expected state, not a failure)
			errStr := err.Error()
			isOAuthError := strings.Contains(errStr, "OAuth authorization") ||
				strings.Contains(errStr, "oauth") ||
				strings.Contains(errStr, "authorization required") ||
				strings.Contains(errStr, "no valid token")

			if isOAuthError {
				// OAuth required is not a failure - restart succeeded but OAuth is needed
				s.logger.Info("Server restart completed, OAuth login required",
					"server", serverID,
					"error", errStr)

				response := contracts.ServerActionResponse{
					Server:  serverID,
					Action:  "restart",
					Success: true,
					Async:   false,
				}
				s.writeSuccess(w, response)
				return
			}

			// Non-OAuth error - treat as failure
			s.logger.Error("Failed to restart server via management service", "server", serverID, "error", err)
			s.writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to restart server: %v", err))
			return
		}

		response := contracts.ServerActionResponse{
			Server:  serverID,
			Action:  "restart",
			Success: true,
			Async:   false,
		}
		s.writeSuccess(w, response)
		return
	}

	// Fallback to legacy path
	// Use the new synchronous RestartServer method
	done := make(chan error, 1)
	go func() {
		done <- s.controller.RestartServer(serverID)
	}()

	select {
	case err := <-done:
		if err != nil {
			// Check if error is OAuth-related (expected state, not a failure)
			errStr := err.Error()
			isOAuthError := strings.Contains(errStr, "OAuth authorization") ||
				strings.Contains(errStr, "oauth") ||
				strings.Contains(errStr, "authorization required") ||
				strings.Contains(errStr, "no valid token")

			if isOAuthError {
				// OAuth required is not a failure - restart succeeded but OAuth is needed
				s.logger.Info("Server restart completed, OAuth login required",
					"server", serverID,
					"error", errStr)

				response := contracts.ServerActionResponse{
					Server:  serverID,
					Action:  "restart",
					Success: true,
					Async:   false,
				}
				s.writeSuccess(w, response)
				return
			}

			// Non-OAuth error - treat as failure
			s.logger.Error("Failed to restart server", "server", serverID, "error", err)
			s.writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to restart server: %v", err))
			return
		}
		s.logger.Debug("Server restart completed synchronously", "server", serverID)
	case <-time.After(35 * time.Second):
		// Longer timeout for restart (30s connect timeout + 5s buffer)
		s.logger.Debug("Server restart executing asynchronously", "server", serverID)
		go func() {
			if err := <-done; err != nil {
				s.logger.Error("Asynchronous server restart failed", "server", serverID, "error", err)
			}
		}()
	}

	response := contracts.ServerActionResponse{
		Server:  serverID,
		Action:  "restart",
		Success: true,
		Async:   false,
	}

	s.writeSuccess(w, response)
}

// handleDiscoverServerTools godoc
// @Summary Discover tools for a specific server
// @Description Manually trigger tool discovery and indexing for a specific upstream MCP server. This forces an immediate refresh of the server's tool cache.
// @Tags servers
// @Produce json
// @Security ApiKeyAuth
// @Security ApiKeyQuery
// @Param id path string true "Server ID or name"
// @Success 200 {object} contracts.ServerActionResponse "Tool discovery triggered successfully"
// @Failure 400 {object} contracts.ErrorResponse "Bad request (missing server ID)"
// @Failure 404 {object} contracts.ErrorResponse "Server not found"
// @Failure 500 {object} contracts.ErrorResponse "Failed to discover tools"
// @Router /api/v1/servers/{id}/discover-tools [post]
func (s *Server) handleDiscoverServerTools(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "id")
	if serverID == "" {
		s.writeError(w, http.StatusBadRequest, "Server ID required")
		return
	}

	s.logger.Info("Manual tool discovery triggered via API", "server", serverID)

	if err := s.controller.DiscoverServerTools(r.Context(), serverID); err != nil {
		s.logger.Error("Failed to discover tools for server", "server", serverID, "error", err)
		
		if strings.Contains(err.Error(), "not found") {
			s.writeError(w, http.StatusNotFound, fmt.Sprintf("Server not found: %s", serverID))
			return
		}
		
		s.writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to discover tools: %v", err))
		return
	}

	response := contracts.ServerActionResponse{
		Server:  serverID,
		Action:  "discover_tools",
		Success: true,
		Async:   false,
	}
	s.writeSuccess(w, response)
}

func (s *Server) toggleServerAsync(serverID string, enabled bool) (bool, error) {
	errCh := make(chan error, 1)
	go func() {
		errCh <- s.controller.EnableServer(serverID, enabled)
	}()

	select {
	case err := <-errCh:
		return false, err
	case <-time.After(asyncToggleTimeout):
		go func() {
			if err := <-errCh; err != nil {
				s.logger.Error("Asynchronous server toggle failed", "server", serverID, "enabled", enabled, "error", err)
			}
		}()
		return true, nil
	}
}

// handleServerLogin godoc
// @Summary Trigger OAuth login for server
// @Description Initiate OAuth authentication flow for a specific upstream MCP server
// @Tags servers
// @Produce json
// @Security ApiKeyAuth
// @Security ApiKeyQuery
// @Param id path string true "Server ID or name"
// @Success 200 {object} contracts.ServerActionResponse "OAuth login initiated successfully"
// @Failure 400 {object} contracts.ErrorResponse "Bad request (missing server ID)"
// @Failure 404 {object} contracts.ErrorResponse "Server not found"
// @Failure 500 {object} contracts.ErrorResponse "Internal server error"
// @Router /api/v1/servers/{id}/login [post]
func (s *Server) handleServerLogin(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "id")
	if serverID == "" {
		s.writeError(w, http.StatusBadRequest, "Server ID required")
		return
	}

	// NEW: Call management service instead of controller (T017)
	mgmtSvc, ok := s.controller.GetManagementService().(interface {
		TriggerOAuthLogin(ctx context.Context, name string) error
	})
	if !ok {
		s.logger.Error("Management service not available or missing TriggerOAuthLogin method")
		s.writeError(w, http.StatusInternalServerError, "Management service not available")
		return
	}

	if err := mgmtSvc.TriggerOAuthLogin(r.Context(), serverID); err != nil {
		s.logger.Error("Failed to trigger OAuth login", "server", serverID, "error", err)

		// Map errors to HTTP status codes (T019)
		if strings.Contains(err.Error(), "management disabled") || strings.Contains(err.Error(), "read-only") {
			s.writeError(w, http.StatusForbidden, err.Error())
			return
		}
		if strings.Contains(err.Error(), "not found") {
			s.writeError(w, http.StatusNotFound, fmt.Sprintf("Server not found: %s", serverID))
			return
		}
		s.writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to trigger login: %v", err))
		return
	}

	response := contracts.ServerActionResponse{
		Server:  serverID,
		Action:  "login",
		Success: true,
	}

	s.writeSuccess(w, response)
}

// handleServerLogout godoc
// @Summary Clear OAuth token and disconnect server
// @Description Clear OAuth authentication token and disconnect a specific upstream MCP server. The server will need to re-authenticate before tools can be used again.
// @Tags servers
// @Produce json
// @Security ApiKeyAuth
// @Security ApiKeyQuery
// @Param id path string true "Server ID or name"
// @Success 200 {object} contracts.ServerActionResponse "OAuth logout completed successfully"
// @Failure 400 {object} contracts.ErrorResponse "Bad request (missing server ID)"
// @Failure 403 {object} contracts.ErrorResponse "Forbidden (management disabled or read-only mode)"
// @Failure 404 {object} contracts.ErrorResponse "Server not found"
// @Failure 500 {object} contracts.ErrorResponse "Internal server error"
// @Router /api/v1/servers/{id}/logout [post]
func (s *Server) handleServerLogout(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "id")
	if serverID == "" {
		s.writeError(w, http.StatusBadRequest, "Server ID required")
		return
	}

	// Call management service TriggerOAuthLogout
	mgmtSvc, ok := s.controller.GetManagementService().(interface {
		TriggerOAuthLogout(ctx context.Context, name string) error
	})
	if !ok {
		s.logger.Error("Management service not available or missing TriggerOAuthLogout method")
		s.writeError(w, http.StatusInternalServerError, "Management service not available")
		return
	}

	if err := mgmtSvc.TriggerOAuthLogout(r.Context(), serverID); err != nil {
		s.logger.Error("Failed to trigger OAuth logout", "server", serverID, "error", err)

		// Map errors to HTTP status codes
		if strings.Contains(err.Error(), "management disabled") || strings.Contains(err.Error(), "read-only") {
			s.writeError(w, http.StatusForbidden, err.Error())
			return
		}
		if strings.Contains(err.Error(), "not found") {
			s.writeError(w, http.StatusNotFound, fmt.Sprintf("Server not found: %s", serverID))
			return
		}
		s.writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to trigger logout: %v", err))
		return
	}

	response := contracts.ServerActionResponse{
		Server:  serverID,
		Action:  "logout",
		Success: true,
	}

	s.writeSuccess(w, response)
}

// handleQuarantineServer godoc
// @Summary Quarantine a server
// @Description Place a specific upstream MCP server in quarantine to prevent tool execution
// @Tags servers
// @Produce json
// @Security ApiKeyAuth
// @Security ApiKeyQuery
// @Param id path string true "Server ID or name"
// @Success 200 {object} contracts.ServerActionResponse "Server quarantined successfully"
// @Failure 400 {object} contracts.ErrorResponse "Bad request (missing server ID)"
// @Failure 404 {object} contracts.ErrorResponse "Server not found"
// @Failure 500 {object} contracts.ErrorResponse "Internal server error"
// @Router /api/v1/servers/{id}/quarantine [post]
func (s *Server) handleQuarantineServer(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "id")
	if serverID == "" {
		s.writeError(w, http.StatusBadRequest, "Server ID required")
		return
	}

	if err := s.controller.QuarantineServer(serverID, true); err != nil {
		s.logger.Error("Failed to quarantine server", "server", serverID, "error", err)
		s.writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to quarantine server: %v", err))
		return
	}

	response := contracts.ServerActionResponse{
		Server:  serverID,
		Action:  "quarantine",
		Success: true,
	}

	s.writeSuccess(w, response)
}

// handleUnquarantineServer godoc
// @Summary Unquarantine a server
// @Description Remove a specific upstream MCP server from quarantine to allow tool execution
// @Tags servers
// @Produce json
// @Security ApiKeyAuth
// @Security ApiKeyQuery
// @Param id path string true "Server ID or name"
// @Success 200 {object} contracts.ServerActionResponse "Server unquarantined successfully"
// @Failure 400 {object} contracts.ErrorResponse "Bad request (missing server ID)"
// @Failure 404 {object} contracts.ErrorResponse "Server not found"
// @Failure 500 {object} contracts.ErrorResponse "Internal server error"
// @Router /api/v1/servers/{id}/unquarantine [post]
func (s *Server) handleUnquarantineServer(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "id")
	if serverID == "" {
		s.writeError(w, http.StatusBadRequest, "Server ID required")
		return
	}

	if err := s.controller.QuarantineServer(serverID, false); err != nil {
		s.logger.Error("Failed to unquarantine server", "server", serverID, "error", err)
		s.writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to unquarantine server: %v", err))
		return
	}

	response := contracts.ServerActionResponse{
		Server:  serverID,
		Action:  "unquarantine",
		Success: true,
	}

	s.writeSuccess(w, response)
}

// handleGetServerTools godoc
// @Summary Get tools for a server
// @Description Retrieve all available tools for a specific upstream MCP server
// @Tags servers
// @Produce json
// @Security ApiKeyAuth
// @Security ApiKeyQuery
// @Param id path string true "Server ID or name"
// @Success 200 {object} contracts.GetServerToolsResponse "Server tools retrieved successfully"
// @Failure 400 {object} contracts.ErrorResponse "Bad request (missing server ID)"
// @Failure 404 {object} contracts.ErrorResponse "Server not found"
// @Failure 500 {object} contracts.ErrorResponse "Internal server error"
// @Router /api/v1/servers/{id}/tools [get]
func (s *Server) handleGetServerTools(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "id")
	if serverID == "" {
		s.writeError(w, http.StatusBadRequest, "Server ID required")
		return
	}

	// NEW: Call management service instead of controller (T016)
	mgmtSvc, ok := s.controller.GetManagementService().(interface {
		GetServerTools(ctx context.Context, name string) ([]map[string]interface{}, error)
	})
	if !ok {
		s.logger.Error("Management service not available or missing GetServerTools method")
		s.writeError(w, http.StatusInternalServerError, "Management service not available")
		return
	}

	tools, err := mgmtSvc.GetServerTools(r.Context(), serverID)
	if err != nil {
		s.logger.Error("Failed to get server tools", "server", serverID, "error", err)

		// Map errors to HTTP status codes (T018)
		if strings.Contains(err.Error(), "not found") {
			s.writeError(w, http.StatusNotFound, fmt.Sprintf("Server not found: %s", serverID))
			return
		}
		s.writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to get tools: %v", err))
		return
	}

	// Convert to typed tools
	typedTools := contracts.ConvertGenericToolsToTyped(tools)

	response := contracts.GetServerToolsResponse{
		ServerName: serverID,
		Tools:      typedTools,
		Count:      len(typedTools),
	}

	s.writeSuccess(w, response)
}

// handleGetServerLogs godoc
// @Summary Get server logs
// @Description Retrieve log entries for a specific upstream MCP server
// @Tags servers
// @Produce json
// @Security ApiKeyAuth
// @Security ApiKeyQuery
// @Param id path string true "Server ID or name"
// @Param tail query int false "Number of log lines to retrieve" default(100)
// @Success 200 {object} contracts.GetServerLogsResponse "Server logs retrieved successfully"
// @Failure 400 {object} contracts.ErrorResponse "Bad request (missing server ID)"
// @Failure 404 {object} contracts.ErrorResponse "Server not found"
// @Failure 500 {object} contracts.ErrorResponse "Internal server error"
// @Router /api/v1/servers/{id}/logs [get]
func (s *Server) handleGetServerLogs(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "id")
	if serverID == "" {
		s.writeError(w, http.StatusBadRequest, "Server ID required")
		return
	}

	tailStr := r.URL.Query().Get("tail")
	tail := 100 // default
	if tailStr != "" {
		if parsed, err := strconv.Atoi(tailStr); err == nil && parsed > 0 {
			tail = parsed
		}
	}

	logEntries, err := s.controller.GetServerLogs(serverID, tail)
	if err != nil {
		s.logger.Error("Failed to get server logs", "server", serverID, "error", err)
		s.writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to get logs: %v", err))
		return
	}

	response := contracts.GetServerLogsResponse{
		ServerName: serverID,
		Logs:       logEntries,
		Count:      len(logEntries),
	}

	s.writeSuccess(w, response)
}

// handleSearchTools godoc
// @Summary Search for tools
// @Description Search across all upstream MCP server tools using BM25 keyword search
// @Tags tools
// @Produce json
// @Security ApiKeyAuth
// @Security ApiKeyQuery
// @Param q query string true "Search query"
// @Param limit query int false "Maximum number of results" default(10) maximum(100)
// @Success 200 {object} contracts.SearchToolsResponse "Search results"
// @Failure 400 {object} contracts.ErrorResponse "Bad request (missing query parameter)"
// @Failure 500 {object} contracts.ErrorResponse "Internal server error"
// @Router /api/v1/index/search [get]
func (s *Server) handleSearchTools(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	if query == "" {
		s.writeError(w, http.StatusBadRequest, "Query parameter 'q' required")
		return
	}

	limitStr := r.URL.Query().Get("limit")
	limit := 10 // default
	if limitStr != "" {
		if parsed, err := strconv.Atoi(limitStr); err == nil && parsed > 0 && parsed <= 100 {
			limit = parsed
		}
	}

	results, err := s.controller.SearchTools(query, limit)
	if err != nil {
		s.logger.Error("Failed to search tools", "query", query, "error", err)
		s.writeError(w, http.StatusInternalServerError, fmt.Sprintf("Search failed: %v", err))
		return
	}

	// Convert to typed search results
	typedResults := contracts.ConvertGenericSearchResultsToTyped(results)

	response := contracts.SearchToolsResponse{
		Query:   query,
		Results: typedResults,
		Total:   len(typedResults),
		Took:    "0ms", // TODO: Add timing measurement
	}

	s.writeSuccess(w, response)
}

func (s *Server) handleSSEEvents(w http.ResponseWriter, r *http.Request) {
	// Set SSE headers first
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("X-Accel-Buffering", "no") // Disable nginx buffering

	// For HEAD requests, just return headers without body
	if r.Method == "HEAD" {
		w.WriteHeader(http.StatusOK)
		return
	}

	// Write headers explicitly to establish response
	w.WriteHeader(http.StatusOK)

	// Check if flushing is supported (but don't store nil)
	flusher, canFlush := w.(http.Flusher)
	if !canFlush {
		s.logger.Warn("ResponseWriter does not support flushing, SSE may not work properly")
	}

	// Write initial SSE comment with retry hint to establish connection immediately
	fmt.Fprintf(w, ": SSE connection established\nretry: 5000\n\n")

	// Flush immediately after initial comment to ensure browser sees connection
	if canFlush {
		flusher.Flush()
	}

	// Add small delay to ensure browser processes the connection
	time.Sleep(100 * time.Millisecond)

	// Get status & event channels
	statusCh := s.controller.StatusChannel()
	eventsCh := s.controller.EventsChannel()

	s.logger.Debug("SSE connection established",
		"status_channel_nil", statusCh == nil,
		"events_channel_nil", eventsCh == nil)

	// Create heartbeat ticker to keep connection alive
	heartbeat := time.NewTicker(30 * time.Second)
	defer heartbeat.Stop()

	// Send initial status
	initialStatus := map[string]interface{}{
		"running":        s.controller.IsRunning(),
		"listen_addr":    s.controller.GetListenAddress(),
		"upstream_stats": s.controller.GetUpstreamStats(),
		"status":         s.controller.GetStatus(),
		"timestamp":      time.Now().Unix(),
	}

	s.logger.Debug("Sending initial SSE status event", "data", initialStatus)
	if err := s.writeSSEEvent(w, flusher, canFlush, "status", initialStatus); err != nil {
		s.logger.Error("Failed to write initial SSE event", "error", err)
		return
	}
	s.logger.Debug("Initial SSE status event sent successfully")

	// Stream updates
	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			// Send heartbeat ping to keep connection alive
			pingData := map[string]interface{}{
				"timestamp": time.Now().Unix(),
			}
			if err := s.writeSSEEvent(w, flusher, canFlush, "ping", pingData); err != nil {
				s.logger.Error("Failed to write SSE heartbeat", "error", err)
				return
			}
		case status, ok := <-statusCh:
			if !ok {
				return
			}

			response := map[string]interface{}{
				"running":        s.controller.IsRunning(),
				"listen_addr":    s.controller.GetListenAddress(),
				"upstream_stats": s.controller.GetUpstreamStats(),
				"status":         status,
				"timestamp":      time.Now().Unix(),
			}

			if err := s.writeSSEEvent(w, flusher, canFlush, "status", response); err != nil {
				s.logger.Error("Failed to write SSE event", "error", err)
				return
			}
		case evt, ok := <-eventsCh:
			if !ok {
				eventsCh = nil
				continue
			}

			eventPayload := map[string]interface{}{
				"payload":   evt.Payload,
				"timestamp": evt.Timestamp.Unix(),
			}

			if err := s.writeSSEEvent(w, flusher, canFlush, string(evt.Type), eventPayload); err != nil {
				s.logger.Error("Failed to write runtime SSE event", "error", err)
				return
			}
		}
	}
}

func (s *Server) writeSSEEvent(w http.ResponseWriter, flusher http.Flusher, canFlush bool, event string, data interface{}) error {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return err
	}

	// Write SSE formatted event
	_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, string(jsonData))
	if err != nil {
		return err
	}

	// Force flush using pre-validated flusher
	if canFlush {
		flusher.Flush()
	}

	return nil
}

// Secrets management handlers

func (s *Server) handleGetSecretRefs(w http.ResponseWriter, r *http.Request) {
	resolver := s.controller.GetSecretResolver()
	if resolver == nil {
		s.writeError(w, http.StatusInternalServerError, "Secret resolver not available")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	// Get all secret references from available providers
	refs, err := resolver.ListAll(ctx)
	if err != nil {
		s.logger.Error("Failed to list secret references", "error", err)
		s.writeError(w, http.StatusInternalServerError, "Failed to list secret references")
		return
	}

	// Mask the response for security - never return actual secret values
	maskedRefs := make([]map[string]interface{}, len(refs))
	for i, ref := range refs {
		maskedRefs[i] = map[string]interface{}{
			"type":     ref.Type,
			"name":     ref.Name,
			"original": ref.Original,
		}
	}

	response := map[string]interface{}{
		"refs":  maskedRefs,
		"count": len(refs),
	}

	s.writeSuccess(w, response)
}

func (s *Server) handleMigrateSecrets(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	resolver := s.controller.GetSecretResolver()
	if resolver == nil {
		s.writeError(w, http.StatusInternalServerError, "Secret resolver not available")
		return
	}

	// Get current configuration
	cfg := s.controller.GetCurrentConfig()
	if cfg == nil {
		s.writeError(w, http.StatusInternalServerError, "Configuration not available")
		return
	}

	// Analyze configuration for potential secrets
	analysis := resolver.AnalyzeForMigration(cfg)

	// Mask actual values in the response for security
	for i := range analysis.Candidates {
		analysis.Candidates[i].Value = secret.MaskSecretValue(analysis.Candidates[i].Value)
	}

	response := map[string]interface{}{
		"analysis":  analysis,
		"dry_run":   true, // Always dry run via API for security
		"timestamp": time.Now().Unix(),
	}

	s.writeSuccess(w, response)
}

func (s *Server) handleGetConfigSecrets(w http.ResponseWriter, r *http.Request) {
	resolver := s.controller.GetSecretResolver()
	if resolver == nil {
		s.writeError(w, http.StatusInternalServerError, "Secret resolver not available")
		return
	}

	// Get current configuration
	cfg := s.controller.GetCurrentConfig()
	if cfg == nil {
		s.writeError(w, http.StatusInternalServerError, "Configuration not available")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	// Extract config-referenced secrets and environment variables
	configSecrets, err := resolver.ExtractConfigSecrets(ctx, cfg)
	if err != nil {
		s.logger.Error("Failed to extract config secrets", "error", err)
		s.writeError(w, http.StatusInternalServerError, "Failed to extract config secrets")
		return
	}

	s.writeSuccess(w, configSecrets)
}

// handleSetSecret godoc
// @Summary      Store a secret in OS keyring
// @Description  Stores a secret value in the operating system's secure keyring. The secret can then be referenced in configuration using ${keyring:secret-name} syntax. Automatically notifies runtime to restart affected servers.
// @Tags         secrets
// @Accept       json
// @Produce      json
// @Success      200     {object}  map[string]interface{}      "Secret stored successfully with reference syntax"
// @Failure      400     {object}  contracts.ErrorResponse     "Invalid JSON payload, missing name/value, or unsupported type"
// @Failure      401     {object}  contracts.ErrorResponse     "Unauthorized - missing or invalid API key"
// @Failure      405     {object}  contracts.ErrorResponse     "Method not allowed"
// @Failure      500     {object}  contracts.ErrorResponse     "Secret resolver not available or failed to store secret"
// @Security     ApiKeyAuth
// @Security     ApiKeyQuery
// @Router       /api/v1/secrets [post]
func (s *Server) handleSetSecret(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	resolver := s.controller.GetSecretResolver()
	if resolver == nil {
		s.writeError(w, http.StatusInternalServerError, "Secret resolver not available")
		return
	}

	var request struct {
		Name  string `json:"name"`
		Value string `json:"value"`
		Type  string `json:"type"`
	}

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		s.writeError(w, http.StatusBadRequest, "Invalid JSON payload")
		return
	}

	if request.Name == "" {
		s.writeError(w, http.StatusBadRequest, "Secret name is required")
		return
	}

	if request.Value == "" {
		s.writeError(w, http.StatusBadRequest, "Secret value is required")
		return
	}

	// Default to keyring if type not specified
	if request.Type == "" {
		request.Type = secretTypeKeyring
	}

	// Only allow keyring type for security
	if request.Type != secretTypeKeyring {
		s.writeError(w, http.StatusBadRequest, "Only keyring type is supported")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	ref := secret.Ref{
		Type: request.Type,
		Name: request.Name,
	}

	err := resolver.Store(ctx, ref, request.Value)
	if err != nil {
		s.logger.Error("Failed to store secret", "name", request.Name, "error", err)
		s.writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to store secret: %v", err))
		return
	}

	// Notify runtime that secrets changed (this will restart affected servers)
	if runtime := s.controller; runtime != nil {
		if err := runtime.NotifySecretsChanged(ctx, "store", request.Name); err != nil {
			s.logger.Warn("Failed to notify runtime of secret change",
				"name", request.Name,
				"error", err)
		}
	}

	s.writeSuccess(w, map[string]interface{}{
		"message":   fmt.Sprintf("Secret '%s' stored successfully in %s", request.Name, request.Type),
		"name":      request.Name,
		"type":      request.Type,
		"reference": fmt.Sprintf("${%s:%s}", request.Type, request.Name),
	})
}

// handleDeleteSecret godoc
// @Summary      Delete a secret from OS keyring
// @Description  Deletes a secret from the operating system's secure keyring. Automatically notifies runtime to restart affected servers. Only keyring type is supported for security.
// @Tags         secrets
// @Produce      json
// @Param        name   path      string                  true   "Name of the secret to delete"
// @Param        type   query     string                  false  "Secret type (only 'keyring' supported, defaults to 'keyring')"
// @Success      200    {object}  map[string]interface{}  "Secret deleted successfully"
// @Failure      400    {object}  contracts.ErrorResponse "Missing secret name or unsupported type"
// @Failure      401    {object}  contracts.ErrorResponse "Unauthorized - missing or invalid API key"
// @Failure      405    {object}  contracts.ErrorResponse "Method not allowed"
// @Failure      500    {object}  contracts.ErrorResponse "Secret resolver not available or failed to delete secret"
// @Security     ApiKeyAuth
// @Security     ApiKeyQuery
// @Router       /api/v1/secrets/{name} [delete]
func (s *Server) handleDeleteSecret(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		s.writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	resolver := s.controller.GetSecretResolver()
	if resolver == nil {
		s.writeError(w, http.StatusInternalServerError, "Secret resolver not available")
		return
	}

	name := chi.URLParam(r, "name")
	if name == "" {
		s.writeError(w, http.StatusBadRequest, "Secret name is required")
		return
	}

	// Get optional type from query parameter, default to keyring
	secretType := r.URL.Query().Get("type")
	if secretType == "" {
		secretType = secretTypeKeyring
	}

	// Only allow keyring type for security
	if secretType != secretTypeKeyring {
		s.writeError(w, http.StatusBadRequest, "Only keyring type is supported")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	ref := secret.Ref{
		Type: secretType,
		Name: name,
	}

	err := resolver.Delete(ctx, ref)
	if err != nil {
		s.logger.Error("Failed to delete secret", "name", name, "error", err)
		s.writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to delete secret: %v", err))
		return
	}

	// Notify runtime that secrets changed (this will restart affected servers)
	if runtime := s.controller; runtime != nil {
		if err := runtime.NotifySecretsChanged(ctx, "delete", name); err != nil {
			s.logger.Warn("Failed to notify runtime of secret deletion",
				"name", name,
				"error", err)
		}
	}

	s.writeSuccess(w, map[string]interface{}{
		"message": fmt.Sprintf("Secret '%s' deleted successfully from %s", name, secretType),
		"name":    name,
		"type":    secretType,
	})
}

// Diagnostics handler

// handleGetDiagnostics godoc
// @Summary Get health diagnostics
// @Description Get comprehensive health diagnostics including upstream errors, OAuth requirements, missing secrets, and Docker status
// @Tags diagnostics
// @Produce json
// @Security ApiKeyAuth
// @Security ApiKeyQuery
// @Success 200 {object} contracts.Diagnostics "Health diagnostics"
// @Failure 500 {object} contracts.ErrorResponse "Internal server error"
// @Router /api/v1/diagnostics [get]
// @Router /api/v1/doctor [get]
func (s *Server) handleGetDiagnostics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	// Try to use management service if available
	if mgmtSvc := s.controller.GetManagementService(); mgmtSvc != nil {
		diag, err := mgmtSvc.(interface {
			Doctor(context.Context) (*contracts.Diagnostics, error)
		}).Doctor(r.Context())

		if err != nil {
			s.logger.Error("Failed to get diagnostics via management service", "error", err)
			s.writeError(w, http.StatusInternalServerError, "Failed to get diagnostics")
			return
		}

		s.writeSuccess(w, diag)
		return
	}

	// Fallback to legacy path if management service not available
	genericServers, err := s.controller.GetAllServers()
	if err != nil {
		s.logger.Error("Failed to get servers for diagnostics", "error", err)
		s.writeError(w, http.StatusInternalServerError, "Failed to get servers")
		return
	}

	// Convert to typed servers
	servers := contracts.ConvertGenericServersToTyped(genericServers)

	// Collect diagnostics (legacy format)
	var upstreamErrors []contracts.DiagnosticIssue
	var oauthRequired []string
	var missingSecrets []contracts.MissingSecret
	var runtimeWarnings []contracts.DiagnosticIssue

	now := time.Now()

	// Check for upstream errors
	for _, server := range servers {
		if server.LastError != "" {
			upstreamErrors = append(upstreamErrors, contracts.DiagnosticIssue{
				Type:      "error",
				Category:  "connection",
				Server:    server.Name,
				Title:     "Server Connection Error",
				Message:   server.LastError,
				Timestamp: now,
				Severity:  "high",
				Metadata: map[string]interface{}{
					"protocol": server.Protocol,
					"enabled":  server.Enabled,
				},
			})
		}

		// Check for OAuth requirements
		if server.OAuth != nil && !server.Authenticated {
			oauthRequired = append(oauthRequired, server.Name)
		}

		// Check for missing secrets
		missingSecrets = append(missingSecrets, s.checkMissingSecrets(server)...)
	}

	totalIssues := len(upstreamErrors) + len(oauthRequired) + len(missingSecrets) + len(runtimeWarnings)

	response := contracts.DiagnosticsResponse{
		UpstreamErrors:  upstreamErrors,
		OAuthRequired:   oauthRequired,
		MissingSecrets:  missingSecrets,
		RuntimeWarnings: runtimeWarnings,
		TotalIssues:     totalIssues,
		LastUpdated:     now,
	}

	s.writeSuccess(w, response)
}

// handleGetTokenStats godoc
// @Summary Get token savings statistics
// @Description Retrieve token savings statistics across all servers and sessions
// @Tags stats
// @Produce json
// @Security ApiKeyAuth
// @Security ApiKeyQuery
// @Success 200 {object} contracts.SuccessResponse "Token statistics"
// @Failure 500 {object} contracts.ErrorResponse "Internal server error"
// @Router /api/v1/stats/tokens [get]
func (s *Server) handleGetTokenStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	tokenStats, err := s.controller.GetTokenSavings()
	if err != nil {
		s.logger.Error("Failed to calculate token savings", "error", err)
		s.writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to calculate token savings: %v", err))
		return
	}

	s.writeSuccess(w, tokenStats)
}

// checkMissingSecrets analyzes a server configuration for unresolved secret references
func (s *Server) checkMissingSecrets(server contracts.Server) []contracts.MissingSecret {
	var missingSecrets []contracts.MissingSecret

	// Check environment variables for secret references
	for key, value := range server.Env {
		if secretRef := extractSecretReference(value); secretRef != nil {
			// Check if secret can be resolved
			if !s.canResolveSecret(secretRef) {
				missingSecrets = append(missingSecrets, contracts.MissingSecret{
					Name:      secretRef.Name,
					Reference: secretRef.Original,
					Server:    server.Name,
					Type:      secretRef.Type,
				})
			}
		}
		_ = key // Avoid unused variable warning
	}

	// Check OAuth configuration for secret references
	if server.OAuth != nil {
		if secretRef := extractSecretReference(server.OAuth.ClientID); secretRef != nil {
			if !s.canResolveSecret(secretRef) {
				missingSecrets = append(missingSecrets, contracts.MissingSecret{
					Name:      secretRef.Name,
					Reference: secretRef.Original,
					Server:    server.Name,
					Type:      secretRef.Type,
				})
			}
		}
	}

	return missingSecrets
}

// extractSecretReference extracts secret reference from a value string
func extractSecretReference(value string) *contracts.Ref {
	// Match patterns like ${env:VAR_NAME} or ${keyring:secret_name}
	if len(value) < 7 || !strings.HasPrefix(value, "${") || !strings.HasSuffix(value, "}") {
		return nil
	}

	inner := value[2 : len(value)-1] // Remove ${ and }
	parts := strings.SplitN(inner, ":", 2)
	if len(parts) != 2 {
		return nil
	}

	return &contracts.Ref{
		Type:     parts[0],
		Name:     parts[1],
		Original: value,
	}
}

// canResolveSecret checks if a secret reference can be resolved
func (s *Server) canResolveSecret(ref *contracts.Ref) bool {
	resolver := s.controller.GetSecretResolver()
	if resolver == nil {
		return false
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Try to resolve the secret
	_, err := resolver.Resolve(ctx, secret.Ref{
		Type: ref.Type,
		Name: ref.Name,
	})

	return err == nil
}

// Tool call history handlers

// handleGetToolCalls godoc
// @Summary      Get tool call history
// @Description  Retrieves paginated tool call history across all upstream servers or filtered by session ID. Includes execution timestamps, arguments, results, and error information for debugging and auditing.
// @Tags         tool-calls
// @Produce      json
// @Param        limit       query     int                                 false  "Maximum number of records to return (1-100, default 50)"
// @Param        offset      query     int                                 false  "Number of records to skip for pagination (default 0)"
// @Param        session_id  query     string                              false  "Filter tool calls by MCP session ID"
// @Success      200         {object}  contracts.GetToolCallsResponse      "Tool calls retrieved successfully"
// @Failure      401         {object}  contracts.ErrorResponse             "Unauthorized - missing or invalid API key"
// @Failure      405         {object}  contracts.ErrorResponse             "Method not allowed"
// @Failure      500         {object}  contracts.ErrorResponse             "Failed to get tool calls"
// @Security     ApiKeyAuth
// @Security     ApiKeyQuery
// @Router       /api/v1/tool-calls [get]
func (s *Server) handleGetToolCalls(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	// Parse query parameters
	limitStr := r.URL.Query().Get("limit")
	offsetStr := r.URL.Query().Get("offset")
	sessionID := r.URL.Query().Get("session_id")

	limit := 50 // default
	if limitStr != "" {
		if parsed, err := strconv.Atoi(limitStr); err == nil && parsed > 0 && parsed <= 100 {
			limit = parsed
		}
	}

	offset := 0
	if offsetStr != "" {
		if parsed, err := strconv.Atoi(offsetStr); err == nil && parsed >= 0 {
			offset = parsed
		}
	}

	var toolCalls []*contracts.ToolCallRecord
	var total int
	var err error

	// Get tool calls - either filtered by session or all
	if sessionID != "" {
		toolCalls, total, err = s.controller.GetToolCallsBySession(sessionID, limit, offset)
	} else {
		toolCalls, total, err = s.controller.GetToolCalls(limit, offset)
	}

	if err != nil {
		s.logger.Error("Failed to get tool calls", "error", err, "session_id", sessionID)
		s.writeError(w, http.StatusInternalServerError, "Failed to get tool calls")
		return
	}

	response := contracts.GetToolCallsResponse{
		ToolCalls: convertToolCallPointers(toolCalls),
		Total:     total,
		Limit:     limit,
		Offset:    offset,
	}

	s.writeSuccess(w, response)
}

// handleGetToolCallDetail godoc
// @Summary      Get tool call details by ID
// @Description  Retrieves detailed information about a specific tool call execution including full request arguments, response data, execution time, and any errors encountered.
// @Tags         tool-calls
// @Produce      json
// @Param        id   path      string                                  true  "Tool call ID"
// @Success      200  {object}  contracts.GetToolCallDetailResponse     "Tool call details retrieved successfully"
// @Failure      400  {object}  contracts.ErrorResponse                 "Tool call ID required"
// @Failure      401  {object}  contracts.ErrorResponse                 "Unauthorized - missing or invalid API key"
// @Failure      404  {object}  contracts.ErrorResponse                 "Tool call not found"
// @Failure      405  {object}  contracts.ErrorResponse                 "Method not allowed"
// @Security     ApiKeyAuth
// @Security     ApiKeyQuery
// @Router       /api/v1/tool-calls/{id} [get]
func (s *Server) handleGetToolCallDetail(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	id := chi.URLParam(r, "id")
	if id == "" {
		s.writeError(w, http.StatusBadRequest, "Tool call ID required")
		return
	}

	// Get tool call by ID
	toolCall, err := s.controller.GetToolCallByID(id)
	if err != nil {
		s.logger.Error("Failed to get tool call detail", "id", id, "error", err)
		s.writeError(w, http.StatusNotFound, "Tool call not found")
		return
	}

	response := contracts.GetToolCallDetailResponse{
		ToolCall: *toolCall,
	}

	s.writeSuccess(w, response)
}

// handleGetServerToolCalls godoc
// @Summary      Get tool call history for specific server
// @Description  Retrieves tool call history filtered by upstream server ID. Returns recent tool executions for the specified server including timestamps, arguments, results, and errors. Useful for server-specific debugging and monitoring.
// @Tags         tool-calls
// @Produce      json
// @Param        id     path      string                                      true   "Upstream server ID or name"
// @Param        limit  query     int                                         false  "Maximum number of records to return (1-100, default 50)"
// @Success      200    {object}  contracts.GetServerToolCallsResponse        "Server tool calls retrieved successfully"
// @Failure      400    {object}  contracts.ErrorResponse                     "Server ID required"
// @Failure      401    {object}  contracts.ErrorResponse                     "Unauthorized - missing or invalid API key"
// @Failure      405    {object}  contracts.ErrorResponse                     "Method not allowed"
// @Failure      500    {object}  contracts.ErrorResponse                     "Failed to get server tool calls"
// @Security     ApiKeyAuth
// @Security     ApiKeyQuery
// @Router       /api/v1/servers/{id}/tool-calls [get]
func (s *Server) handleGetServerToolCalls(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	serverID := chi.URLParam(r, "id")
	if serverID == "" {
		s.writeError(w, http.StatusBadRequest, "Server ID required")
		return
	}

	// Parse limit parameter
	limitStr := r.URL.Query().Get("limit")
	limit := 50 // default
	if limitStr != "" {
		if parsed, err := strconv.Atoi(limitStr); err == nil && parsed > 0 && parsed <= 100 {
			limit = parsed
		}
	}

	// Get server tool calls
	toolCalls, err := s.controller.GetServerToolCalls(serverID, limit)
	if err != nil {
		s.logger.Error("Failed to get server tool calls", "server", serverID, "error", err)
		s.writeError(w, http.StatusInternalServerError, "Failed to get server tool calls")
		return
	}

	response := contracts.GetServerToolCallsResponse{
		ServerName: serverID,
		ToolCalls:  convertToolCallPointers(toolCalls),
		Total:      len(toolCalls),
	}

	s.writeSuccess(w, response)
}

// Helper to convert []*contracts.ToolCallRecord to []contracts.ToolCallRecord
func convertToolCallPointers(pointers []*contracts.ToolCallRecord) []contracts.ToolCallRecord {
	records := make([]contracts.ToolCallRecord, 0, len(pointers))
	for _, ptr := range pointers {
		if ptr != nil {
			records = append(records, *ptr)
		}
	}
	return records
}

// handleReplayToolCall godoc
// @Summary      Replay a tool call
// @Description  Re-executes a previous tool call with optional modified arguments. Useful for debugging and testing tool behavior with different inputs. Creates a new tool call record linked to the original.
// @Tags         tool-calls
// @Accept       json
// @Produce      json
// @Param        id       path      string                              true  "Original tool call ID to replay"
// @Param        request  body      contracts.ReplayToolCallRequest     false "Optional modified arguments for replay"
// @Success      200      {object}  contracts.ReplayToolCallResponse    "Tool call replayed successfully"
// @Failure      400      {object}  contracts.ErrorResponse             "Tool call ID required or invalid JSON payload"
// @Failure      401      {object}  contracts.ErrorResponse             "Unauthorized - missing or invalid API key"
// @Failure      405      {object}  contracts.ErrorResponse             "Method not allowed"
// @Failure      500      {object}  contracts.ErrorResponse             "Failed to replay tool call"
// @Security     ApiKeyAuth
// @Security     ApiKeyQuery
// @Router       /api/v1/tool-calls/{id}/replay [post]
func (s *Server) handleReplayToolCall(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	id := chi.URLParam(r, "id")
	if id == "" {
		s.writeError(w, http.StatusBadRequest, "Tool call ID required")
		return
	}

	// Parse request body for modified arguments
	var request contracts.ReplayToolCallRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		s.writeError(w, http.StatusBadRequest, "Invalid JSON payload")
		return
	}

	// Replay the tool call with modified arguments
	newToolCall, err := s.controller.ReplayToolCall(id, request.Arguments)
	if err != nil {
		s.logger.Error("Failed to replay tool call", "id", id, "error", err)
		s.writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to replay tool call: %v", err))
		return
	}

	response := contracts.ReplayToolCallResponse{
		Success:      true,
		NewCallID:    newToolCall.ID,
		NewToolCall:  *newToolCall,
		ReplayedFrom: id,
	}

	s.writeSuccess(w, response)
}

// Configuration management handlers

// handleGetConfig godoc
// @Summary      Get current configuration
// @Description  Retrieves the current MCPProxy configuration including all server definitions, global settings, and runtime parameters
// @Tags         config
// @Produce      json
// @Success      200  {object}  contracts.GetConfigResponse  "Configuration retrieved successfully"
// @Failure      401  {object}  contracts.ErrorResponse      "Unauthorized - missing or invalid API key"
// @Failure      500  {object}  contracts.ErrorResponse      "Failed to get configuration"
// @Security     ApiKeyAuth
// @Security     ApiKeyQuery
// @Router       /api/v1/config [get]
func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	cfg, err := s.controller.GetConfig()
	if err != nil {
		s.logger.Error("Failed to get configuration", "error", err)
		s.writeError(w, http.StatusInternalServerError, "Failed to get configuration")
		return
	}

	if cfg == nil {
		s.writeError(w, http.StatusInternalServerError, "Configuration not available")
		return
	}

	// Convert config to contracts type for consistent API response
	response := contracts.GetConfigResponse{
		Config:     contracts.ConvertConfigToContract(cfg),
		ConfigPath: s.controller.GetConfigPath(),
	}

	s.writeSuccess(w, response)
}

// handleValidateConfig godoc
// @Summary      Validate configuration
// @Description  Validates a provided MCPProxy configuration without applying it. Checks for syntax errors, invalid server definitions, conflicting settings, and other configuration issues.
// @Tags         config
// @Accept       json
// @Produce      json
// @Param        config  body      config.Config                       true  "Configuration to validate"
// @Success      200     {object}  contracts.ValidateConfigResponse    "Configuration validation result"
// @Failure      400     {object}  contracts.ErrorResponse             "Invalid JSON payload"
// @Failure      401     {object}  contracts.ErrorResponse             "Unauthorized - missing or invalid API key"
// @Failure      500     {object}  contracts.ErrorResponse             "Validation failed"
// @Security     ApiKeyAuth
// @Security     ApiKeyQuery
// @Router       /api/v1/config/validate [post]
func (s *Server) handleValidateConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	var cfg config.Config
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		s.writeError(w, http.StatusBadRequest, "Invalid JSON payload")
		return
	}

	// Perform validation
	validationErrors, err := s.controller.ValidateConfig(&cfg)
	if err != nil {
		s.logger.Error("Failed to validate configuration", "error", err)
		s.writeError(w, http.StatusInternalServerError, fmt.Sprintf("Validation failed: %v", err))
		return
	}

	response := contracts.ValidateConfigResponse{
		Valid:  len(validationErrors) == 0,
		Errors: contracts.ConvertValidationErrors(validationErrors),
	}

	s.writeSuccess(w, response)
}

// handleApplyConfig godoc
// @Summary      Apply configuration
// @Description  Applies a new MCPProxy configuration. Validates and persists the configuration to disk. Some changes apply immediately, while others may require a restart. Returns detailed information about applied changes and restart requirements.
// @Tags         config
// @Accept       json
// @Produce      json
// @Param        config  body      config.Config                   true  "Configuration to apply"
// @Success      200     {object}  contracts.ConfigApplyResult     "Configuration applied successfully with change details"
// @Failure      400     {object}  contracts.ErrorResponse         "Invalid JSON payload"
// @Failure      401     {object}  contracts.ErrorResponse         "Unauthorized - missing or invalid API key"
// @Failure      500     {object}  contracts.ErrorResponse         "Failed to apply configuration"
// @Security     ApiKeyAuth
// @Security     ApiKeyQuery
// @Router       /api/v1/config/apply [post]
func (s *Server) handleApplyConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	var cfg config.Config
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		s.writeError(w, http.StatusBadRequest, "Invalid JSON payload")
		return
	}

	// Get config path from controller
	cfgPath := s.controller.GetConfigPath()

	// Apply configuration
	result, err := s.controller.ApplyConfig(&cfg, cfgPath)
	if err != nil {
		s.logger.Error("Failed to apply configuration", "error", err)
		s.writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to apply configuration: %v", err))
		return
	}

	// Convert result to contracts type directly here to avoid import cycles
	response := &contracts.ConfigApplyResult{
		Success:            result.Success,
		AppliedImmediately: result.AppliedImmediately,
		RequiresRestart:    result.RequiresRestart,
		RestartReason:      result.RestartReason,
		ChangedFields:      result.ChangedFields,
		ValidationErrors:   contracts.ConvertValidationErrors(result.ValidationErrors),
	}

	s.writeSuccess(w, response)
}

// handleCallTool godoc
// @Summary Call a tool
// @Description Execute a tool on an upstream MCP server (wrapper around MCP tool calls)
// @Tags tools
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Security ApiKeyQuery
// @Param request body object{tool_name=string,arguments=object} true "Tool call request with tool name and arguments"
// @Success 200 {object} contracts.SuccessResponse "Tool call result"
// @Failure 400 {object} contracts.ErrorResponse "Bad request (invalid payload or missing tool name)"
// @Failure 500 {object} contracts.ErrorResponse "Internal server error or tool execution failure"
// @Router /api/v1/tools/call [post]
func (s *Server) handleCallTool(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	var request struct {
		ToolName  string                 `json:"tool_name"`
		Arguments map[string]interface{} `json:"arguments"`
	}

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		s.writeError(w, http.StatusBadRequest, "Invalid JSON payload")
		return
	}

	if request.ToolName == "" {
		s.writeError(w, http.StatusBadRequest, "Tool name is required")
		return
	}

	// Call tool via controller
	result, err := s.controller.CallTool(r.Context(), request.ToolName, request.Arguments)
	if err != nil {
		s.logger.Error("Failed to call tool", "tool", request.ToolName, "error", err)
		s.writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to call tool: %v", err))
		return
	}

	s.writeSuccess(w, result)
}

// handleListRegistries handles GET /api/v1/registries
// handleListRegistries godoc
// @Summary      List available MCP server registries
// @Description  Retrieves list of all MCP server registries that can be browsed for discovering and installing new upstream servers. Includes registry metadata, server counts, and API endpoints.
// @Tags         registries
// @Produce      json
// @Success      200  {object}  contracts.GetRegistriesResponse  "Registries retrieved successfully"
// @Failure      401  {object}  contracts.ErrorResponse          "Unauthorized - missing or invalid API key"
// @Failure      500  {object}  contracts.ErrorResponse          "Failed to list registries"
// @Security     ApiKeyAuth
// @Security     ApiKeyQuery
// @Router       /api/v1/registries [get]
func (s *Server) handleListRegistries(w http.ResponseWriter, _ *http.Request) {
	registries, err := s.controller.ListRegistries()
	if err != nil {
		s.logger.Error("Failed to list registries", "error", err)
		s.writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to list registries: %v", err))
		return
	}

	// Convert to contracts.Registry
	contractRegistries := make([]contracts.Registry, len(registries))
	for i, reg := range registries {
		regMap, ok := reg.(map[string]interface{})
		if !ok {
			s.logger.Warn("Invalid registry type", "registry", reg)
			continue
		}

		contractReg := contracts.Registry{
			ID:          getString(regMap, "id"),
			Name:        getString(regMap, "name"),
			Description: getString(regMap, "description"),
			URL:         getString(regMap, "url"),
			ServersURL:  getString(regMap, "servers_url"),
			Protocol:    getString(regMap, "protocol"),
			Count:       regMap["count"],
		}

		if tags, ok := regMap["tags"].([]interface{}); ok {
			contractReg.Tags = make([]string, 0, len(tags))
			for _, tag := range tags {
				if tagStr, ok := tag.(string); ok {
					contractReg.Tags = append(contractReg.Tags, tagStr)
				}
			}
		}

		contractRegistries[i] = contractReg
	}

	response := contracts.GetRegistriesResponse{
		Registries: contractRegistries,
		Total:      len(contractRegistries),
	}

	s.writeSuccess(w, response)
}

// handleSearchRegistryServers godoc
// @Summary      Search MCP servers in a registry
// @Description  Searches for MCP servers within a specific registry by keyword or tag. Returns server metadata including installation commands, source code URLs, and npm package information for easy discovery and installation.
// @Tags         registries
// @Produce      json
// @Param        id     path      string                                       true   "Registry ID"
// @Param        q      query     string                                       false  "Search query keyword"
// @Param        tag    query     string                                       false  "Filter by tag"
// @Param        limit  query     int                                          false  "Maximum number of results (default 10)"
// @Success      200    {object}  contracts.SearchRegistryServersResponse      "Servers retrieved successfully"
// @Failure      400    {object}  contracts.ErrorResponse                      "Registry ID required"
// @Failure      401    {object}  contracts.ErrorResponse                      "Unauthorized - missing or invalid API key"
// @Failure      500    {object}  contracts.ErrorResponse                      "Failed to search servers"
// @Security     ApiKeyAuth
// @Security     ApiKeyQuery
// @Router       /api/v1/registries/{id}/servers [get]
func (s *Server) handleSearchRegistryServers(w http.ResponseWriter, r *http.Request) {
	registryID := chi.URLParam(r, "id")
	if registryID == "" {
		s.writeError(w, http.StatusBadRequest, "Registry ID is required")
		return
	}

	// Parse query parameters
	query := r.URL.Query().Get("q")
	tag := r.URL.Query().Get("tag")
	limitStr := r.URL.Query().Get("limit")

	limit := 10 // Default limit
	if limitStr != "" {
		if parsedLimit, err := strconv.Atoi(limitStr); err == nil && parsedLimit > 0 {
			limit = parsedLimit
		}
	}

	servers, err := s.controller.SearchRegistryServers(registryID, tag, query, limit)
	if err != nil {
		s.logger.Error("Failed to search registry servers", "registry", registryID, "error", err)
		s.writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to search servers: %v", err))
		return
	}

	// Convert to contracts.RepositoryServer
	contractServers := make([]contracts.RepositoryServer, len(servers))
	for i, srv := range servers {
		srvMap, ok := srv.(map[string]interface{})
		if !ok {
			s.logger.Warn("Invalid server type", "server", srv)
			continue
		}

		contractSrv := contracts.RepositoryServer{
			ID:            getString(srvMap, "id"),
			Name:          getString(srvMap, "name"),
			Description:   getString(srvMap, "description"),
			URL:           getString(srvMap, "url"),
			SourceCodeURL: getString(srvMap, "source_code_url"),
			InstallCmd:    getString(srvMap, "installCmd"),
			ConnectURL:    getString(srvMap, "connectUrl"),
			UpdatedAt:     getString(srvMap, "updatedAt"),
			CreatedAt:     getString(srvMap, "createdAt"),
			Registry:      getString(srvMap, "registry"),
		}

		// Parse repository_info if present
		if repoInfo, ok := srvMap["repository_info"].(map[string]interface{}); ok {
			contractSrv.RepositoryInfo = &contracts.RepositoryInfo{}
			if npm, ok := repoInfo["npm"].(map[string]interface{}); ok {
				contractSrv.RepositoryInfo.NPM = &contracts.NPMPackageInfo{
					Exists:     getBool(npm, "exists"),
					InstallCmd: getString(npm, "install_cmd"),
				}
			}
		}

		contractServers[i] = contractSrv
	}

	response := contracts.SearchRegistryServersResponse{
		RegistryID: registryID,
		Servers:    contractServers,
		Total:      len(contractServers),
		Query:      query,
		Tag:        tag,
	}

	s.writeSuccess(w, response)
}

// Helper functions for type conversion
func getString(m map[string]interface{}, key string) string {
	if val, ok := m[key].(string); ok {
		return val
	}
	return ""
}

func getBool(m map[string]interface{}, key string) bool {
	if val, ok := m[key].(bool); ok {
		return val
	}
	return false
}

// Session management handlers

// handleGetSessions godoc
// @Summary      Get active MCP sessions
// @Description  Retrieves paginated list of active and recent MCP client sessions. Each session represents a connection from an MCP client to MCPProxy, tracking initialization time, tool calls, and connection status.
// @Tags         sessions
// @Produce      json
// @Param        limit   query     int                               false  "Maximum number of sessions to return (1-100, default 10)"
// @Param        offset  query     int                               false  "Number of sessions to skip for pagination (default 0)"
// @Success      200     {object}  contracts.GetSessionsResponse     "Sessions retrieved successfully"
// @Failure      401     {object}  contracts.ErrorResponse           "Unauthorized - missing or invalid API key"
// @Failure      405     {object}  contracts.ErrorResponse           "Method not allowed"
// @Failure      500     {object}  contracts.ErrorResponse           "Failed to get sessions"
// @Security     ApiKeyAuth
// @Security     ApiKeyQuery
// @Router       /api/v1/sessions [get]
func (s *Server) handleGetSessions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	// Parse query parameters
	limitStr := r.URL.Query().Get("limit")
	offsetStr := r.URL.Query().Get("offset")

	limit := 10 // default for sessions
	if limitStr != "" {
		if parsed, err := strconv.Atoi(limitStr); err == nil && parsed > 0 && parsed <= 100 {
			limit = parsed
		}
	}

	offset := 0
	if offsetStr != "" {
		if parsed, err := strconv.Atoi(offsetStr); err == nil && parsed >= 0 {
			offset = parsed
		}
	}

	// Get recent sessions from controller
	sessions, total, err := s.controller.GetRecentSessions(limit)
	if err != nil {
		s.logger.Error("Failed to get sessions", "error", err)
		s.writeError(w, http.StatusInternalServerError, "Failed to get sessions")
		return
	}

	// Convert to non-pointer slice
	sessionList := make([]contracts.MCPSession, 0, len(sessions))
	for _, session := range sessions {
		if session != nil {
			sessionList = append(sessionList, *session)
		}
	}

	response := contracts.GetSessionsResponse{
		Sessions: sessionList,
		Total:    total,
		Limit:    limit,
		Offset:   offset,
	}

	s.writeSuccess(w, response)
}

// handleGetSessionDetail godoc
// @Summary      Get MCP session details by ID
// @Description  Retrieves detailed information about a specific MCP client session including initialization parameters, connection status, tool call count, and activity timestamps.
// @Tags         sessions
// @Produce      json
// @Param        id   path      string                                  true  "Session ID"
// @Success      200  {object}  contracts.GetSessionDetailResponse      "Session details retrieved successfully"
// @Failure      400  {object}  contracts.ErrorResponse                 "Session ID required"
// @Failure      401  {object}  contracts.ErrorResponse                 "Unauthorized - missing or invalid API key"
// @Failure      404  {object}  contracts.ErrorResponse                 "Session not found"
// @Failure      405  {object}  contracts.ErrorResponse                 "Method not allowed"
// @Security     ApiKeyAuth
// @Security     ApiKeyQuery
// @Router       /api/v1/sessions/{id} [get]
func (s *Server) handleGetSessionDetail(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	id := chi.URLParam(r, "id")
	if id == "" {
		s.writeError(w, http.StatusBadRequest, "Session ID required")
		return
	}

	// Get session by ID
	session, err := s.controller.GetSessionByID(id)
	if err != nil {
		s.logger.Error("Failed to get session detail", "id", id, "error", err)
		s.writeError(w, http.StatusNotFound, "Session not found")
		return
	}

	response := contracts.GetSessionDetailResponse{
		Session: *session,
	}

	s.writeSuccess(w, response)
}

// handleGetDockerStatus godoc
// @Summary Get Docker status
// @Description Retrieve current Docker availability and recovery status
// @Tags docker
// @Produce json
// @Security ApiKeyAuth
// @Security ApiKeyQuery
// @Success 200 {object} contracts.SuccessResponse "Docker status information"
// @Failure 500 {object} contracts.ErrorResponse "Internal server error"
// @Router /api/v1/docker/status [get]
func (s *Server) handleGetDockerStatus(w http.ResponseWriter, r *http.Request) {
	status := s.controller.GetDockerRecoveryStatus()
	if status == nil {
		s.writeError(w, http.StatusInternalServerError, "failed to get Docker status")
		return
	}

	response := map[string]interface{}{
		"docker_available":   status.DockerAvailable,
		"recovery_mode":      status.RecoveryMode,
		"failure_count":      status.FailureCount,
		"attempts_since_up":  status.AttemptsSinceUp,
		"last_attempt":       status.LastAttempt,
		"last_error":         status.LastError,
		"last_successful_at": status.LastSuccessfulAt,
	}

	s.writeSuccess(w, response)
}
