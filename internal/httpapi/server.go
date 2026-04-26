package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/connect"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/logs"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/management"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/observability"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/reqcontext"
	internalRuntime "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/telemetry"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/transport"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/updatecheck"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/core"
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
	// SubscribeEvents creates a new per-client event subscription channel.
	// Each SSE client should get its own channel to avoid competing for events.
	SubscribeEvents() chan internalRuntime.Event
	// UnsubscribeEvents closes and removes the subscription channel.
	UnsubscribeEvents(chan internalRuntime.Event)

	// Server management
	GetAllServers() ([]map[string]interface{}, error)
	AddServer(ctx context.Context, serverConfig *config.ServerConfig) error // T001: Add server
	RemoveServer(ctx context.Context, serverName string) error              // T002: Remove server
	UpdateServer(ctx context.Context, serverName string, updates *config.ServerConfig) error
	EnableServer(serverName string, enabled bool) error
	GetToolApprovalStatus(serverName, toolName string) (string, error)
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

	// Activity logging (RFC-003)
	ListActivities(filter storage.ActivityFilter) ([]*storage.ActivityRecord, int, error)
	GetActivity(id string) (*storage.ActivityRecord, error)
	StreamActivities(filter storage.ActivityFilter) <-chan *storage.ActivityRecord

	// Tool-level quarantine (Spec 032)
	ListToolApprovals(serverName string) ([]*storage.ToolApprovalRecord, error)
	ApproveTools(serverName string, toolNames []string, approvedBy string) error
	ApproveAllTools(serverName string, approvedBy string) (int, error)
	GetToolApproval(serverName, toolName string) (*storage.ToolApprovalRecord, error)
}

// Server provides HTTP API endpoints with chi router
type Server struct {
	controller         ServerController
	logger             *zap.SugaredLogger
	httpLogger         *zap.Logger // Separate logger for HTTP requests
	router             *chi.Mux
	observability      *observability.Manager
	tokenStore         TokenStore         // Agent token CRUD (T022)
	dataDir            string             // Data directory for HMAC key (T022)
	feedbackSubmitter  FeedbackSubmitter  // Feedback submission (Spec 036)
	connectService     *connect.Service   // Client connect/disconnect operations
	securityController SecurityController // Security scanner operations (Spec 039)

	// telemetryRegistry is the Tier 2 counter aggregator (Spec 042). May be
	// nil before SetTelemetryRegistry is called; middlewares use the nil-safe
	// telemetry helpers so the call sites do not need to nil-check.
	telemetryRegistry *telemetry.CounterRegistry

	// telemetryPayloadProvider returns the live telemetry.Service so the
	// /api/v1/telemetry/payload endpoint can render the next heartbeat payload
	// with runtime stats attached. May be nil before SetTelemetryPayloadProvider
	// is called.
	telemetryPayloadProvider func() *telemetry.Service
}

// SetTelemetryRegistry attaches the Tier 2 counter registry. Spec 042. Must
// be called before the router serves requests for the surface and REST
// endpoint counters to populate.
func (s *Server) SetTelemetryRegistry(reg *telemetry.CounterRegistry) {
	s.telemetryRegistry = reg
}

// SetTelemetryPayloadProvider attaches a provider that returns the live
// telemetry service. Used by the /api/v1/telemetry/payload endpoint to render
// the next heartbeat payload with runtime stats. Spec 042.
func (s *Server) SetTelemetryPayloadProvider(fn func() *telemetry.Service) {
	s.telemetryPayloadProvider = fn
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

// SetTokenStore configures agent token management on the server.
// This must be called after NewServer and before serving requests
// to enable the /api/v1/tokens endpoints.
func (s *Server) SetTokenStore(store TokenStore, dataDir string) {
	s.tokenStore = store
	s.dataDir = dataDir
}

// SetFeedbackSubmitter configures the feedback submission handler (Spec 036).
func (s *Server) SetFeedbackSubmitter(submitter FeedbackSubmitter) {
	s.feedbackSubmitter = submitter
}

// SetConnectService configures the client connect/disconnect service.
func (s *Server) SetConnectService(svc *connect.Service) {
	s.connectService = svc
}

// Router returns the underlying chi.Mux for external route registration.
// This is used by the server edition to mount OAuth routes outside
// the default API key authentication group.
func (s *Server) Router() *chi.Mux {
	return s.router
}

// apiKeyAuthMiddleware creates middleware for API key authentication.
// Connections from Unix socket/named pipe (tray) are trusted and skip API key validation.
// Supports both global API key (admin) and agent tokens (mcp_agt_ prefix) with scope enforcement.
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
				ctx := auth.WithAuthContext(r.Context(), auth.AdminContext())
				next.ServeHTTP(w, r.WithContext(ctx))
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
				s.writeError(w, r, http.StatusUnauthorized, "API key authentication required but not configured. Please set MCPPROXY_API_KEY or configure api_key in config file.")
				return
			}

			// Extract token from request
			token := ExtractToken(r)
			if token == "" {
				s.logger.Warn("TCP connection with missing API key",
					zap.String("path", r.URL.Path),
					zap.String("remote_addr", r.RemoteAddr))
				s.writeError(w, r, http.StatusUnauthorized, "Invalid or missing API key")
				return
			}

			// Check if this is an agent token (mcp_agt_ prefix)
			if strings.HasPrefix(token, auth.TokenPrefixStr) {
				s.handleAgentTokenAuth(w, r, next, token)
				return
			}

			// Check if the token matches the global API key (admin)
			if token == cfg.APIKey {
				s.logger.Debug("TCP connection with valid API key",
					zap.String("path", r.URL.Path),
					zap.String("remote_addr", r.RemoteAddr))
				ctx := auth.WithAuthContext(r.Context(), auth.AdminContext())
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}

			// Token doesn't match anything
			s.logger.Warn("TCP connection with invalid API key",
				zap.String("path", r.URL.Path),
				zap.String("remote_addr", r.RemoteAddr))
			s.writeError(w, r, http.StatusUnauthorized, "Invalid or missing API key")
		})
	}
}

// handleAgentTokenAuth validates an agent token and sets the appropriate AuthContext.
func (s *Server) handleAgentTokenAuth(w http.ResponseWriter, r *http.Request, next http.Handler, token string) {
	if s.tokenStore == nil || s.dataDir == "" {
		s.logger.Warn("Agent token presented but token store not configured",
			zap.String("path", r.URL.Path),
			zap.String("remote_addr", r.RemoteAddr))
		s.writeError(w, r, http.StatusUnauthorized, "Agent tokens are not configured on this server")
		return
	}

	hmacKey, err := auth.GetOrCreateHMACKey(s.dataDir)
	if err != nil {
		s.logger.Error("Failed to get HMAC key for agent token validation", zap.Error(err))
		s.writeError(w, r, http.StatusInternalServerError, "Internal server error")
		return
	}

	agentToken, err := s.tokenStore.ValidateAgentToken(token, hmacKey)
	if err != nil {
		s.logger.Warn("Agent token validation failed",
			zap.String("path", r.URL.Path),
			zap.String("remote_addr", r.RemoteAddr),
			zap.String("error", err.Error()))
		s.writeError(w, r, http.StatusUnauthorized, fmt.Sprintf("Agent token invalid: %s", err.Error()))
		return
	}

	// Update last-used timestamp in background
	go func() {
		if updateErr := s.tokenStore.UpdateAgentTokenLastUsed(agentToken.Name); updateErr != nil {
			s.logger.Warn("Failed to update agent token last-used timestamp",
				zap.String("name", agentToken.Name),
				zap.Error(updateErr))
		}
	}()

	authCtx := &auth.AuthContext{
		Type:           auth.AuthTypeAgent,
		AgentName:      agentToken.Name,
		TokenPrefix:    agentToken.TokenPrefix,
		AllowedServers: agentToken.AllowedServers,
		Permissions:    agentToken.Permissions,
	}
	ctx := auth.WithAuthContext(r.Context(), authCtx)

	s.logger.Debug("Agent token authenticated",
		zap.String("agent_name", agentToken.Name),
		zap.String("token_prefix", agentToken.TokenPrefix),
		zap.String("path", r.URL.Path),
		zap.String("remote_addr", r.RemoteAddr))

	next.ServeHTTP(w, r.WithContext(ctx))
}

// ExtractToken extracts the authentication token from the request.
// It checks (in order): X-API-Key header, Authorization: Bearer header, ?apikey= query param.
// Returns an empty string if no token is found.
func ExtractToken(r *http.Request) string {
	// 1. Check X-API-Key header
	if key := r.Header.Get("X-API-Key"); key != "" {
		return key
	}

	// 2. Check Authorization: Bearer header
	if authHeader := r.Header.Get("Authorization"); authHeader != "" {
		if strings.HasPrefix(authHeader, "Bearer ") {
			if token := strings.TrimPrefix(authHeader, "Bearer "); token != "" {
				return token
			}
		}
	}

	// 3. Check query parameter (for SSE and Web UI initial load)
	if key := r.URL.Query().Get("apikey"); key != "" {
		return key
	}

	return ""
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
	// Request ID middleware MUST be first to ensure all responses have X-Request-Id header
	s.router.Use(RequestIDMiddleware)
	s.router.Use(RequestIDLoggerMiddleware(s.logger)) // Add request_id to logger context
	s.router.Use(s.httpLoggingMiddleware())           // Custom HTTP API logging
	s.router.Use(middleware.Recoverer)
	s.router.Use(s.correlationIDMiddleware()) // Correlation ID and request source tracking
	s.logger.Debug("Core middleware configured (request ID, logging, recovery, correlation ID)")

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
		// Spec 042: Tier 2 telemetry middlewares. Both fetch the registry via
		// a closure so the registry can be installed after route setup.
		r.Use(SurfaceClassifierMiddleware(func() *telemetry.CounterRegistry { return s.telemetryRegistry }))
		r.Use(RESTEndpointHistogramMiddleware(func() *telemetry.CounterRegistry { return s.telemetryRegistry }))

		// Status endpoint
		r.Get("/status", s.handleGetStatus)

		// Info endpoint (server version, web UI URL, etc.)
		r.Get("/info", s.handleGetInfo)

		// Routing mode endpoint
		r.Get("/routing", s.handleGetRouting)

		// Server management
		r.Get("/servers", s.handleGetServers)
		r.Post("/servers", s.handleAddServer)                           // T001: Add server
		r.Post("/servers/import", s.handleImportServers)                // Import from file upload
		r.Post("/servers/import/json", s.handleImportServersJSON)       // Import from JSON/TOML content
		r.Get("/servers/import/paths", s.handleGetCanonicalConfigPaths) // Get canonical config paths
		r.Post("/servers/import/path", s.handleImportFromPath)          // Import from file path
		r.Post("/servers/reconnect", s.handleForceReconnectServers)
		// T076-T077: Bulk operation routes
		r.Post("/servers/restart_all", s.handleRestartAll)
		r.Post("/servers/enable_all", s.handleEnableAll)
		r.Post("/servers/disable_all", s.handleDisableAll)
		r.Route("/servers/{id}", func(r chi.Router) {
			r.Patch("/", s.handlePatchServer)   // Partial update server config
			r.Delete("/", s.handleRemoveServer) // T002: Remove server
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
			// Spec 044: per-server diagnostics with stable error_code.
			r.Get("/diagnostics", s.handleGetServerDiagnostics)
			r.Get("/tool-calls", s.handleGetServerToolCalls)

			// Tool-level quarantine (Spec 032)
			r.Post("/tools/approve", s.handleApproveTools)
			r.Get("/tools/{tool}/diff", s.handleGetToolDiff)
			r.Get("/tools/export", s.handleExportToolDescriptions)

			// Security scanner scan/approval routes (Spec 039)
			r.Post("/scan", s.handleStartScan)
			r.Get("/scan/status", s.handleGetScanStatus)
			r.Get("/scan/report", s.handleGetScanReport)
			r.Post("/scan/cancel", s.handleCancelScan)
			r.Get("/scan/files", s.handleGetScanFiles)
			r.Post("/security/approve", s.handleSecurityApprove)
			r.Post("/security/reject", s.handleSecurityReject)
			r.Get("/integrity", s.handleCheckIntegrity)
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
		// Spec 044: per-server diagnostics + fix invocation.
		r.Post("/diagnostics/fix", s.handleInvokeFix)

		// Telemetry payload preview (Spec 042) — renders the next heartbeat
		// payload with runtime stats attached. No network call is made.
		r.Get("/telemetry/payload", s.handleGetTelemetryPayload)

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
		r.Patch("/config/docker-isolation", s.handlePatchDockerIsolation)

		// Registry browsing (Phase 7)
		r.Get("/registries", s.handleListRegistries)
		r.Get("/registries/{id}/servers", s.handleSearchRegistryServers)

		// Activity logging (RFC-003)
		r.Get("/activity", s.handleListActivity)
		r.Get("/activity/summary", s.handleActivitySummary)
		r.Get("/activity/export", s.handleExportActivity)
		r.Get("/activity/{id}", s.handleGetActivityDetail)

		// Annotation coverage (Spec 035)
		r.Get("/annotations/coverage", s.handleAnnotationCoverage)

		// Agent token management (Spec 028)
		r.Route("/tokens", func(r chi.Router) {
			r.Post("/", s.handleCreateToken)
			r.Get("/", s.handleListTokens)
			r.Route("/{name}", func(r chi.Router) {
				r.Get("/", s.handleGetToken)
				r.Delete("/", s.handleRevokeToken)
				r.Post("/regenerate", s.handleRegenerateToken)
			})
		})

		// Feedback submission (Spec 036)
		r.Post("/feedback", s.handleFeedback)

		// Client connect/disconnect
		r.Get("/connect", s.handleGetConnectStatus)
		r.Post("/connect/{client}", s.handleConnectClient)
		r.Delete("/connect/{client}", s.handleDisconnectClient)

		// Security scanner management routes (Spec 039)
		r.Route("/security", func(r chi.Router) {
			r.Get("/scanners", s.handleListScanners)
			r.Post("/scanners/{id}/enable", s.handleInstallScanner)
			r.Post("/scanners/{id}/disable", s.handleRemoveScanner)
			r.Put("/scanners/{id}/config", s.handleConfigureScanner)
			r.Get("/scanners/{id}/status", s.handleGetScannerStatus)
			r.Get("/overview", s.handleSecurityOverview)

			// Legacy routes (backwards compatibility)
			r.Post("/scanners/install", s.handleInstallScanner)
			r.Delete("/scanners/{id}", s.handleRemoveScanner)

			// Batch scan operations
			r.Post("/scan-all", s.handleScanAll)
			r.Get("/queue", s.handleGetQueueProgress)
			r.Post("/cancel-all", s.handleCancelAllScans)

			// Scan history
			r.Get("/scans", s.handleListScanHistory)
			r.Get("/scans/{jobId}/report", s.handleGetScanReportByJobID)
		})
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

// writeError writes an error response including request_id from the request context
// T014: Updated signature to include request for request_id extraction
func (s *Server) writeError(w http.ResponseWriter, r *http.Request, status int, message string) {
	requestID := reqcontext.GetRequestID(r.Context())
	s.writeJSON(w, status, contracts.NewErrorResponseWithRequestID(message, requestID))
}

// getRequestLogger returns a logger with request_id attached, or falls back to the server logger
// T019: Helper for request-scoped logging
func (s *Server) getRequestLogger(r *http.Request) *zap.SugaredLogger {
	if r == nil {
		return s.logger
	}
	if logger := GetLogger(r.Context()); logger != nil {
		return logger
	}
	return s.logger
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
	// Get routing mode from config
	routingMode := config.RoutingModeRetrieveTools
	if cfg, err := s.controller.GetConfig(); err == nil && cfg != nil && cfg.RoutingMode != "" {
		routingMode = cfg.RoutingMode
	}

	response := map[string]interface{}{
		"running":        s.controller.IsRunning(),
		"edition":        editionValue,
		"listen_addr":    s.controller.GetListenAddress(),
		"upstream_stats": s.controller.GetUpstreamStats(),
		"status":         s.controller.GetStatus(),
		"routing_mode":   routingMode,
		"timestamp":      time.Now().Unix(),
	}

	// Spec 044 (FR-018): expose process-level env_kind + env_markers so the
	// tray and CLI can surface the classifier verdict without waiting for the
	// next heartbeat. DetectEnvKindOnce is cached, so repeated calls are free.
	envKind, envMarkers := telemetry.DetectEnvKindOnce()
	response["env_kind"] = string(envKind)
	response["env_markers"] = envMarkers

	// Spec 044 (T041): expose the activation funnel snapshot alongside
	// env_kind. Read-only — mutation happens on MCP/connect events, never
	// through this endpoint. nil when the telemetry service (or activation
	// store) is not wired (e.g. very early startup).
	if s.telemetryPayloadProvider != nil {
		if svc := s.telemetryPayloadProvider(); svc != nil {
			if store := svc.ActivationStore(); store != nil {
				if db := svc.ActivationDB(); db != nil {
					if st, err := store.Load(db); err == nil {
						response["activation"] = st
					}
				}
			}
		}
	}

	// Spec 044 (US3): expose launch_source + autostart_enabled. launch_source
	// is the cached classifier result (no installer-clearing side-effect here
	// — this endpoint is read-only). autostart_enabled reads the tray-owned
	// sidecar with its 1h TTL; nil on Linux / tray not running / malformed.
	response["launch_source"] = string(telemetry.DetectLaunchSourceOnce())
	response["autostart_enabled"] = telemetry.DefaultAutostartReader().Read()

	s.writeSuccess(w, response)
}

// handleGetRouting godoc
// @Summary Get routing mode information
// @Description Get the current routing mode and available MCP endpoints
// @Tags status
// @Produce json
// @Security ApiKeyAuth
// @Security ApiKeyQuery
// @Success 200 {object} contracts.SuccessResponse "Routing mode information"
// @Router /api/v1/routing [get]
func (s *Server) handleGetRouting(w http.ResponseWriter, _ *http.Request) {
	routingMode := config.RoutingModeRetrieveTools
	if cfg, err := s.controller.GetConfig(); err == nil && cfg != nil && cfg.RoutingMode != "" {
		routingMode = cfg.RoutingMode
	}

	// Build mode description
	var description string
	switch routingMode {
	case config.RoutingModeDirect:
		description = "All upstream tools exposed directly via serverName__toolName naming"
	case config.RoutingModeCodeExecution:
		description = "JavaScript orchestration via code_execution tool with tool catalog"
	default:
		description = "BM25 search via retrieve_tools + call_tool variants (default)"
	}

	response := map[string]interface{}{
		"routing_mode": routingMode,
		"description":  description,
		"endpoints": map[string]interface{}{
			"default":        "/mcp",
			"direct":         "/mcp/all",
			"code_execution": "/mcp/code",
			"retrieve_tools": "/mcp/call",
		},
		"available_modes": []string{
			config.RoutingModeRetrieveTools,
			config.RoutingModeDirect,
			config.RoutingModeCodeExecution,
		},
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

// editionValue identifies the MCPProxy edition (personal or teams).
var editionValue = "personal"

// GetBuildVersion returns the build version from build-time variables.
// This should be set during build using -ldflags.
func GetBuildVersion() string {
	return buildVersion
}

// SetEdition sets the edition value (called from main during startup).
func SetEdition(edition string) {
	editionValue = edition
}

// GetEdition returns the current edition.
func GetEdition() string {
	return editionValue
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
			s.writeError(w, r, http.StatusInternalServerError, "Failed to get servers")
			return
		}

		// Convert []*Server to []Server
		serverValues := make([]contracts.Server, len(servers))
		for i, srv := range servers {
			if srv != nil {
				serverValues[i] = *srv
			}
		}

		// Enrich with quarantine stats
		s.enrichServersWithQuarantineStats(serverValues)

		// Enrich with security scan summary (Spec 039)
		if s.securityController != nil {
			for i := range serverValues {
				if summary := s.securityController.GetScanSummary(r.Context(), serverValues[i].Name); summary != nil {
					serverValues[i].SecurityScan = &contracts.SecurityScanSummary{
						LastScanAt: summary.LastScanAt,
						RiskScore:  summary.RiskScore,
						Status:     summary.Status,
					}
					if summary.FindingCounts != nil {
						serverValues[i].SecurityScan.FindingCounts = &contracts.FindingCounts{
							Dangerous: summary.FindingCounts.Dangerous,
							Warning:   summary.FindingCounts.Warning,
							Info:      summary.FindingCounts.Info,
							Total:     summary.FindingCounts.Total,
						}
					}
				}
			}
		}

		// Redact sensitive header values unless explicitly opted out via
		// `reveal_secret_headers: true` in config. See config.Config field
		// for rationale — the same redaction is applied to the
		// `upstream_servers` MCP tool's list output.
		s.redactServerHeaders(serverValues)

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
		s.writeError(w, r, http.StatusInternalServerError, "Failed to get servers")
		return
	}

	// Convert to typed servers
	servers := contracts.ConvertGenericServersToTyped(genericServers)

	// Enrich with quarantine stats
	s.enrichServersWithQuarantineStats(servers)

	// See note above the management-service path for rationale.
	s.redactServerHeaders(servers)

	stats := contracts.ConvertUpstreamStatsToServerStats(s.controller.GetUpstreamStats())

	response := contracts.GetServersResponse{
		Servers: servers,
		Stats:   stats,
	}

	s.writeSuccess(w, response)
}

// redactServerHeaders walks each server in the slice and replaces sensitive
// header values (Authorization, X-API-Key, Cookie, etc.) with `***REDACTED***`.
// Skips redaction entirely when `reveal_secret_headers: true` is set in the
// loaded config, matching the behaviour of the `upstream_servers` MCP tool.
func (s *Server) redactServerHeaders(servers []contracts.Server) {
	cfg, err := s.controller.GetConfig()
	if err == nil && cfg != nil && cfg.RevealSecretHeaders {
		return
	}
	for i := range servers {
		if len(servers[i].Headers) > 0 {
			servers[i].Headers = oauth.RedactStringHeaders(servers[i].Headers)
		}
	}
}

// enrichServersWithQuarantineStats adds quarantine metrics (pending/changed tool counts)
// to each server in the list. This enables the frontend to show quarantine badges.
func (s *Server) enrichServersWithQuarantineStats(servers []contracts.Server) {
	for i := range servers {
		records, err := s.controller.ListToolApprovals(servers[i].Name)
		if err != nil {
			s.logger.Debugw("Failed to get tool approvals for server",
				"server", servers[i].Name, "error", err)
			continue
		}

		var pending, changed int
		for _, rec := range records {
			switch rec.Status {
			case storage.ToolApprovalStatusPending:
				pending++
			case storage.ToolApprovalStatusChanged:
				changed++
			}
		}

		if pending > 0 || changed > 0 {
			servers[i].Quarantine = &contracts.QuarantineStats{
				PendingCount: pending,
				ChangedCount: changed,
			}
		}
	}
}

// AddServerRequest represents a request to add a new server
type AddServerRequest struct {
	Name           string            `json:"name"`
	URL            string            `json:"url,omitempty"`
	Command        string            `json:"command,omitempty"`
	Args           []string          `json:"args,omitempty"`
	Env            map[string]string `json:"env,omitempty"`
	Headers        map[string]string `json:"headers,omitempty"`
	WorkingDir     string            `json:"working_dir,omitempty"`
	Protocol       string            `json:"protocol,omitempty"`
	Enabled        *bool             `json:"enabled,omitempty"`
	Quarantined    *bool             `json:"quarantined,omitempty"`
	ReconnectOnUse *bool             `json:"reconnect_on_use,omitempty"`
	// Isolation carries per-server Docker isolation overrides (image,
	// network_mode, extra_args, working_dir, enabled). A nil pointer
	// means "do not touch isolation config"; an empty-but-present
	// object on PATCH intentionally clears the overrides.
	Isolation *IsolationRequest `json:"isolation,omitempty"`
}

// IsolationRequest is the request-body representation of
// config.IsolationConfig, using pointer fields for PATCH semantics:
// a nil pointer means "leave this field alone", a present value
// (including empty string or empty slice) means "set it".
type IsolationRequest struct {
	Enabled     *bool     `json:"enabled,omitempty"`
	Image       *string   `json:"image,omitempty"`
	NetworkMode *string   `json:"network_mode,omitempty"`
	ExtraArgs   *[]string `json:"extra_args,omitempty"`
	WorkingDir  *string   `json:"working_dir,omitempty"`
}

// toConfig materializes the request into a config.IsolationConfig.
// Fields left nil on the request do not appear on the resulting struct
// so UpdateServer's merge logic (in Controller) can distinguish them
// from explicit clears.
func (r *IsolationRequest) toConfig() *config.IsolationConfig {
	if r == nil {
		return nil
	}
	out := &config.IsolationConfig{}
	if r.Enabled != nil {
		out.Enabled = config.BoolPtr(*r.Enabled)
	}
	if r.Image != nil {
		out.Image = *r.Image
	}
	if r.NetworkMode != nil {
		out.NetworkMode = *r.NetworkMode
	}
	if r.ExtraArgs != nil {
		out.ExtraArgs = append([]string(nil), (*r.ExtraArgs)...)
	}
	if r.WorkingDir != nil {
		out.WorkingDir = *r.WorkingDir
	}
	return out
}

// handleAddServer godoc
// @Summary Add a new upstream server
// @Description Add a new MCP upstream server to the configuration. New servers are quarantined by default for security.
// @Tags servers
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Security ApiKeyQuery
// @Param server body AddServerRequest true "Server configuration"
// @Success 200 {object} contracts.ServerActionResponse "Server added successfully"
// @Failure 400 {object} contracts.ErrorResponse "Bad request - invalid configuration"
// @Failure 409 {object} contracts.ErrorResponse "Conflict - server with this name already exists"
// @Failure 500 {object} contracts.ErrorResponse "Internal server error"
// @Router /api/v1/servers [post]
func (s *Server) handleAddServer(w http.ResponseWriter, r *http.Request) {
	var req AddServerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, r, http.StatusBadRequest, fmt.Sprintf("Invalid request body: %v", err))
		return
	}

	// Validate required fields
	if req.Name == "" {
		s.writeError(w, r, http.StatusBadRequest, "Server name is required")
		return
	}

	// Must have either URL or command
	if req.URL == "" && req.Command == "" {
		s.writeError(w, r, http.StatusBadRequest, "Either 'url' or 'command' is required")
		return
	}

	// Auto-detect protocol if not specified
	protocol := req.Protocol
	if protocol == "" {
		if req.Command != "" {
			protocol = "stdio"
		} else if req.URL != "" {
			protocol = "streamable-http"
		}
	}

	// Default to enabled=true. Default quarantine follows the global
	// quarantine_enabled flag (issue #370): secure by default, but
	// operators can opt out via quarantine_enabled=false. Explicit
	// values on the request always win.
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	quarantined := true
	if cfgIface := s.controller.GetCurrentConfig(); cfgIface != nil {
		if cfg, ok := cfgIface.(*config.Config); ok && cfg != nil {
			quarantined = cfg.DefaultQuarantineForNewServer()
		}
	}
	if req.Quarantined != nil {
		quarantined = *req.Quarantined
	}

	serverConfig := &config.ServerConfig{
		Name:        req.Name,
		URL:         req.URL,
		Command:     req.Command,
		Args:        req.Args,
		Env:         req.Env,
		Headers:     req.Headers,
		WorkingDir:  req.WorkingDir,
		Protocol:    protocol,
		Enabled:     enabled,
		Quarantined: quarantined,
	}
	if req.ReconnectOnUse != nil {
		serverConfig.ReconnectOnUse = *req.ReconnectOnUse
	}

	// Add server via controller
	logger := s.getRequestLogger(r) // T019: Use request-scoped logger
	if err := s.controller.AddServer(r.Context(), serverConfig); err != nil {
		// Check if it's a duplicate name error
		if strings.Contains(err.Error(), "already exists") {
			s.writeError(w, r, http.StatusConflict, err.Error())
			return
		}
		logger.Error("Failed to add server", "server", req.Name, "error", err)
		s.writeError(w, r, http.StatusInternalServerError, fmt.Sprintf("Failed to add server: %v", err))
		return
	}

	logger.Info("Server added successfully", "server", req.Name, "quarantined", quarantined)
	s.writeSuccess(w, contracts.ServerActionResponse{
		Server:  req.Name,
		Action:  "add",
		Success: true,
	})
}

// handleRemoveServer godoc
// @Summary Remove an upstream server
// @Description Remove an MCP upstream server from the configuration. This stops the server if running and removes it from config.
// @Tags servers
// @Produce json
// @Security ApiKeyAuth
// @Security ApiKeyQuery
// @Param id path string true "Server ID or name"
// @Success 200 {object} contracts.ServerActionResponse "Server removed successfully"
// @Failure 400 {object} contracts.ErrorResponse "Bad request"
// @Failure 404 {object} contracts.ErrorResponse "Server not found"
// @Failure 500 {object} contracts.ErrorResponse "Internal server error"
// @Router /api/v1/servers/{id} [delete]
func (s *Server) handleRemoveServer(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "id")
	if serverID == "" {
		s.writeError(w, r, http.StatusBadRequest, "Server ID required")
		return
	}

	logger := s.getRequestLogger(r) // T019: Use request-scoped logger

	// Remove server via controller
	if err := s.controller.RemoveServer(r.Context(), serverID); err != nil {
		// Check if it's a not found error
		if strings.Contains(err.Error(), "not found") {
			s.writeError(w, r, http.StatusNotFound, err.Error())
			return
		}
		logger.Error("Failed to remove server", "server", serverID, "error", err)
		s.writeError(w, r, http.StatusInternalServerError, fmt.Sprintf("Failed to remove server: %v", err))
		return
	}

	logger.Info("Server removed successfully", "server", serverID)
	s.writeSuccess(w, contracts.ServerActionResponse{
		Server:  serverID,
		Action:  "remove",
		Success: true,
	})
}

// handlePatchServer godoc
// @Summary Partially update an upstream server
// @Description Update specific fields of an existing upstream MCP server configuration.
// @Tags servers
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Security ApiKeyQuery
// @Param id path string true "Server ID or name"
// @Param server body AddServerRequest true "Fields to update (all optional)"
// @Success 200 {object} contracts.SuccessResponse "Server updated successfully"
// @Failure 400 {object} contracts.ErrorResponse "Bad request - no fields or invalid body"
// @Failure 404 {object} contracts.ErrorResponse "Server not found"
// @Failure 500 {object} contracts.ErrorResponse "Internal server error"
// @Router /api/v1/servers/{id} [patch]
func (s *Server) handlePatchServer(w http.ResponseWriter, r *http.Request) {
	serverName := chi.URLParam(r, "id")
	if serverName == "" {
		s.writeError(w, r, http.StatusBadRequest, "Server ID required")
		return
	}

	var req AddServerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, r, http.StatusBadRequest, "Invalid request body")
		return
	}

	// Pre-fetch existing server so we can preserve bool fields the request
	// did not explicitly set. `config.ServerConfig` uses non-pointer bools
	// whose zero value cannot be distinguished from "not set" by the time
	// the update reaches the controller — without this, a PATCH body like
	// `{"args": [...]}` silently disables a previously-enabled server.
	var existingSrv *config.ServerConfig
	if cfg, err := s.controller.GetConfig(); err == nil && cfg != nil {
		for _, sc := range cfg.Servers {
			if sc != nil && sc.Name == serverName {
				existingSrv = sc
				break
			}
		}
	}

	// Build partial update config - only set fields that were provided
	updates := &config.ServerConfig{Name: serverName}
	hasUpdates := false

	if req.URL != "" {
		updates.URL = req.URL
		hasUpdates = true
	}
	if req.Command != "" {
		updates.Command = req.Command
		hasUpdates = true
	}
	if req.Args != nil {
		updates.Args = req.Args
		hasUpdates = true
	}
	if req.Env != nil {
		updates.Env = req.Env
		hasUpdates = true
	}
	if req.Headers != nil {
		updates.Headers = req.Headers
		hasUpdates = true
	}
	if req.WorkingDir != "" {
		updates.WorkingDir = req.WorkingDir
		hasUpdates = true
	}
	if req.Protocol != "" {
		updates.Protocol = req.Protocol
		hasUpdates = true
	}
	if req.Enabled != nil {
		updates.Enabled = *req.Enabled
		hasUpdates = true
	} else if existingSrv != nil {
		updates.Enabled = existingSrv.Enabled
	}
	if req.Quarantined != nil {
		updates.Quarantined = *req.Quarantined
		hasUpdates = true
	} else if existingSrv != nil {
		updates.Quarantined = existingSrv.Quarantined
	}
	if req.ReconnectOnUse != nil {
		updates.ReconnectOnUse = *req.ReconnectOnUse
		hasUpdates = true
	} else if existingSrv != nil {
		updates.ReconnectOnUse = existingSrv.ReconnectOnUse
	}
	if req.Isolation != nil {
		updates.Isolation = req.Isolation.toConfig()
		hasUpdates = true
	}

	if !hasUpdates {
		s.writeError(w, r, http.StatusBadRequest, "No fields to update")
		return
	}

	logger := s.getRequestLogger(r)

	if err := s.controller.UpdateServer(r.Context(), serverName, updates); err != nil {
		if strings.Contains(err.Error(), "not found") {
			s.writeError(w, r, http.StatusNotFound, err.Error())
			return
		}
		logger.Error("Failed to update server", "server", serverName, "error", err)
		s.writeError(w, r, http.StatusInternalServerError, fmt.Sprintf("Failed to update server: %v", err))
		return
	}

	logger.Info("Server updated successfully", "server", serverName)
	s.writeSuccess(w, map[string]interface{}{
		"message":          fmt.Sprintf("Server '%s' updated successfully", serverName),
		"restart_required": true,
	})
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
		s.writeError(w, r, http.StatusBadRequest, "Server ID required")
		return
	}

	// Try to use management service if available
	if mgmtSvc := s.controller.GetManagementService(); mgmtSvc != nil {
		err := mgmtSvc.(interface {
			EnableServer(context.Context, string, bool) error
		}).EnableServer(r.Context(), serverID, true)

		if err != nil {
			s.logger.Error("Failed to enable server via management service", "server", serverID, "error", err)
			s.writeError(w, r, http.StatusInternalServerError, fmt.Sprintf("Failed to enable server: %v", err))
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
		s.writeError(w, r, http.StatusInternalServerError, fmt.Sprintf("Failed to enable server: %v", err))
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
		s.writeError(w, r, http.StatusBadRequest, "Server ID required")
		return
	}

	// Try to use management service if available
	if mgmtSvc := s.controller.GetManagementService(); mgmtSvc != nil {
		err := mgmtSvc.(interface {
			EnableServer(context.Context, string, bool) error
		}).EnableServer(r.Context(), serverID, false)

		if err != nil {
			s.logger.Error("Failed to disable server via management service", "server", serverID, "error", err)
			s.writeError(w, r, http.StatusInternalServerError, fmt.Sprintf("Failed to disable server: %v", err))
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
		s.writeError(w, r, http.StatusInternalServerError, fmt.Sprintf("Failed to disable server: %v", err))
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
		s.writeError(w, r, http.StatusInternalServerError, fmt.Sprintf("Failed to reconnect servers: %v", err))
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
		s.writeError(w, r, http.StatusInternalServerError, "Management service not available")
		return
	}

	result, err := mgmtSvc.RestartAll(r.Context())
	if err != nil {
		s.logger.Error("RestartAll operation failed", "error", err)
		s.writeError(w, r, http.StatusInternalServerError, fmt.Sprintf("Failed to restart all servers: %v", err))
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
		s.writeError(w, r, http.StatusInternalServerError, "Management service not available")
		return
	}

	result, err := mgmtSvc.EnableAll(r.Context())
	if err != nil {
		s.logger.Error("EnableAll operation failed", "error", err)
		s.writeError(w, r, http.StatusInternalServerError, fmt.Sprintf("Failed to enable all servers: %v", err))
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
		s.writeError(w, r, http.StatusInternalServerError, "Management service not available")
		return
	}

	result, err := mgmtSvc.DisableAll(r.Context())
	if err != nil {
		s.logger.Error("DisableAll operation failed", "error", err)
		s.writeError(w, r, http.StatusInternalServerError, fmt.Sprintf("Failed to disable all servers: %v", err))
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
		s.writeError(w, r, http.StatusBadRequest, "Server ID required")
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
			s.writeError(w, r, http.StatusInternalServerError, fmt.Sprintf("Failed to restart server: %v", err))
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
			s.writeError(w, r, http.StatusInternalServerError, fmt.Sprintf("Failed to restart server: %v", err))
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
		s.writeError(w, r, http.StatusBadRequest, "Server ID required")
		return
	}

	s.logger.Info("Manual tool discovery triggered via API", "server", serverID)

	if err := s.controller.DiscoverServerTools(r.Context(), serverID); err != nil {
		s.logger.Error("Failed to discover tools for server", "server", serverID, "error", err)

		if strings.Contains(err.Error(), "not found") {
			s.writeError(w, r, http.StatusNotFound, fmt.Sprintf("Server not found: %s", serverID))
			return
		}

		s.writeError(w, r, http.StatusInternalServerError, fmt.Sprintf("Failed to discover tools: %v", err))
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
// @Description Initiate OAuth authentication flow for a specific upstream MCP server. Returns structured OAuth start response with correlation ID for tracking.
// @Tags servers
// @Produce json
// @Security ApiKeyAuth
// @Security ApiKeyQuery
// @Param id path string true "Server ID or name"
// @Success 200 {object} contracts.OAuthStartResponse "OAuth login initiated successfully"
// @Failure 400 {object} contracts.OAuthFlowError "OAuth error (client_id required, DCR failed, etc.)"
// @Failure 404 {object} contracts.ErrorResponse "Server not found"
// @Failure 500 {object} contracts.ErrorResponse "Internal server error"
// @Router /api/v1/servers/{id}/login [post]
func (s *Server) handleServerLogin(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "id")
	if serverID == "" {
		s.writeError(w, r, http.StatusBadRequest, "Server ID required")
		return
	}

	// Call management service TriggerOAuthLoginQuick (Spec 020 fix: returns actual browser status)
	mgmtSvc, ok := s.controller.GetManagementService().(interface {
		TriggerOAuthLoginQuick(ctx context.Context, name string) (*core.OAuthStartResult, error)
	})
	if !ok {
		s.logger.Error("Management service not available or missing TriggerOAuthLoginQuick method")
		s.writeError(w, r, http.StatusInternalServerError, "Management service not available")
		return
	}

	result, err := mgmtSvc.TriggerOAuthLoginQuick(r.Context(), serverID)
	if err != nil {
		s.logger.Error("Failed to trigger OAuth login", "server", serverID, "error", err)

		// Spec 020: Check for structured OAuth errors and return them directly
		var oauthFlowErr *contracts.OAuthFlowError
		if errors.As(err, &oauthFlowErr) {
			// Add request ID from context for correlation
			oauthFlowErr.RequestID = reqcontext.GetRequestID(r.Context())
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			if encErr := json.NewEncoder(w).Encode(oauthFlowErr); encErr != nil {
				s.logger.Error("Failed to encode OAuth flow error response", "error", encErr)
			}
			return
		}

		var oauthValidationErr *contracts.OAuthValidationError
		if errors.As(err, &oauthValidationErr) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			if encErr := json.NewEncoder(w).Encode(oauthValidationErr); encErr != nil {
				s.logger.Error("Failed to encode OAuth validation error response", "error", encErr)
			}
			return
		}

		// Map errors to HTTP status codes (T019)
		if strings.Contains(err.Error(), "management disabled") || strings.Contains(err.Error(), "read-only") {
			s.writeError(w, r, http.StatusForbidden, err.Error())
			return
		}
		if strings.Contains(err.Error(), "not found") {
			s.writeError(w, r, http.StatusNotFound, fmt.Sprintf("Server not found: %s", serverID))
			return
		}
		s.writeError(w, r, http.StatusInternalServerError, fmt.Sprintf("Failed to trigger login: %v", err))
		return
	}

	// Phase 3 (Spec 020): Return OAuthStartResponse with actual browser status and auth_url
	correlationID := reqcontext.GetCorrelationID(r.Context())
	if correlationID == "" {
		correlationID = reqcontext.GetRequestID(r.Context())
	}

	// Use actual result from StartManualOAuthQuick
	browserOpened := result != nil && result.BrowserOpened
	authURL := ""
	browserError := ""
	if result != nil {
		authURL = result.AuthURL
		browserError = result.BrowserError
	}

	// Determine appropriate message based on browser status
	message := fmt.Sprintf("OAuth authentication started for server '%s'. Please complete authentication in browser.", serverID)
	if !browserOpened && authURL != "" {
		message = fmt.Sprintf("Could not open browser automatically. Please open this URL manually: %s", authURL)
	}

	response := contracts.OAuthStartResponse{
		Success:       true,
		ServerName:    serverID,
		CorrelationID: correlationID,
		BrowserOpened: browserOpened,
		AuthURL:       authURL,
		BrowserError:  browserError,
		Message:       message,
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
		s.writeError(w, r, http.StatusBadRequest, "Server ID required")
		return
	}

	// Call management service TriggerOAuthLogout
	mgmtSvc, ok := s.controller.GetManagementService().(interface {
		TriggerOAuthLogout(ctx context.Context, name string) error
	})
	if !ok {
		s.logger.Error("Management service not available or missing TriggerOAuthLogout method")
		s.writeError(w, r, http.StatusInternalServerError, "Management service not available")
		return
	}

	if err := mgmtSvc.TriggerOAuthLogout(r.Context(), serverID); err != nil {
		s.logger.Error("Failed to trigger OAuth logout", "server", serverID, "error", err)

		// Map errors to HTTP status codes
		if strings.Contains(err.Error(), "management disabled") || strings.Contains(err.Error(), "read-only") {
			s.writeError(w, r, http.StatusForbidden, err.Error())
			return
		}
		if strings.Contains(err.Error(), "not found") {
			s.writeError(w, r, http.StatusNotFound, fmt.Sprintf("Server not found: %s", serverID))
			return
		}
		s.writeError(w, r, http.StatusInternalServerError, fmt.Sprintf("Failed to trigger logout: %v", err))
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
		s.writeError(w, r, http.StatusBadRequest, "Server ID required")
		return
	}

	if err := s.controller.QuarantineServer(serverID, true); err != nil {
		s.logger.Error("Failed to quarantine server", "server", serverID, "error", err)
		s.writeError(w, r, http.StatusInternalServerError, fmt.Sprintf("Failed to quarantine server: %v", err))
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
		s.writeError(w, r, http.StatusBadRequest, "Server ID required")
		return
	}

	if err := s.controller.QuarantineServer(serverID, false); err != nil {
		s.logger.Error("Failed to unquarantine server", "server", serverID, "error", err)
		s.writeError(w, r, http.StatusInternalServerError, fmt.Sprintf("Failed to unquarantine server: %v", err))
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
		s.writeError(w, r, http.StatusBadRequest, "Server ID required")
		return
	}

	// NEW: Call management service instead of controller (T016)
	mgmtSvc, ok := s.controller.GetManagementService().(interface {
		GetServerTools(ctx context.Context, name string) ([]map[string]interface{}, error)
	})
	if !ok {
		s.logger.Error("Management service not available or missing GetServerTools method")
		s.writeError(w, r, http.StatusInternalServerError, "Management service not available")
		return
	}

	tools, err := mgmtSvc.GetServerTools(r.Context(), serverID)
	if err != nil {
		s.logger.Error("Failed to get server tools", "server", serverID, "error", err)

		// Map errors to HTTP status codes (T018)
		if strings.Contains(err.Error(), "not found") {
			s.writeError(w, r, http.StatusNotFound, fmt.Sprintf("Server not found: %s", serverID))
			return
		}
		s.writeError(w, r, http.StatusInternalServerError, fmt.Sprintf("Failed to get tools: %v", err))
		return
	}

	// Convert to typed tools
	typedTools := contracts.ConvertGenericToolsToTyped(tools)

	// Enrich with approval status from storage
	enrichedCount := 0
	var firstErr error
	for i := range typedTools {
		status, err := s.controller.GetToolApprovalStatus(serverID, typedTools[i].Name)
		if err == nil && status != "" {
			typedTools[i].ApprovalStatus = status
			enrichedCount++
		} else if i == 0 {
			firstErr = err
		}
	}
	if firstErr != nil {
		fmt.Printf("[DEBUG] Tool approval enrichment: server=%s enriched=%d/%d first_error=%v\n", serverID, enrichedCount, len(typedTools), firstErr)
	} else {
		fmt.Printf("[DEBUG] Tool approval enrichment: server=%s enriched=%d/%d\n", serverID, enrichedCount, len(typedTools))
	}

	// Sort: pending/changed tools first, then approved
	sort.SliceStable(typedTools, func(i, j int) bool {
		return toolApprovalPriority(typedTools[i].ApprovalStatus) < toolApprovalPriority(typedTools[j].ApprovalStatus)
	})

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
		s.writeError(w, r, http.StatusBadRequest, "Server ID required")
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
		s.writeError(w, r, http.StatusInternalServerError, fmt.Sprintf("Failed to get logs: %v", err))
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
		s.writeError(w, r, http.StatusBadRequest, "Query parameter 'q' required")
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
		s.writeError(w, r, http.StatusInternalServerError, fmt.Sprintf("Search failed: %v", err))
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

	// Get status channel (shared)
	statusCh := s.controller.StatusChannel()

	// Create per-client event subscription to avoid competing for events
	// Each SSE client gets its own channel so all clients receive all events
	eventsCh := s.controller.SubscribeEvents()
	if eventsCh != nil {
		defer s.controller.UnsubscribeEvents(eventsCh)
	}

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
		s.writeError(w, r, http.StatusInternalServerError, "Secret resolver not available")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	// Get all secret references from available providers
	refs, err := resolver.ListAll(ctx)
	if err != nil {
		s.logger.Error("Failed to list secret references", "error", err)
		s.writeError(w, r, http.StatusInternalServerError, "Failed to list secret references")
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
		s.writeError(w, r, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	resolver := s.controller.GetSecretResolver()
	if resolver == nil {
		s.writeError(w, r, http.StatusInternalServerError, "Secret resolver not available")
		return
	}

	// Get current configuration
	cfg := s.controller.GetCurrentConfig()
	if cfg == nil {
		s.writeError(w, r, http.StatusInternalServerError, "Configuration not available")
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
		s.writeError(w, r, http.StatusInternalServerError, "Secret resolver not available")
		return
	}

	// Get current configuration
	cfg := s.controller.GetCurrentConfig()
	if cfg == nil {
		s.writeError(w, r, http.StatusInternalServerError, "Configuration not available")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	// Extract config-referenced secrets and environment variables
	configSecrets, err := resolver.ExtractConfigSecrets(ctx, cfg)
	if err != nil {
		s.logger.Error("Failed to extract config secrets", "error", err)
		s.writeError(w, r, http.StatusInternalServerError, "Failed to extract config secrets")
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
		s.writeError(w, r, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	resolver := s.controller.GetSecretResolver()
	if resolver == nil {
		s.writeError(w, r, http.StatusInternalServerError, "Secret resolver not available")
		return
	}

	var request struct {
		Name  string `json:"name"`
		Value string `json:"value"`
		Type  string `json:"type"`
	}

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		s.writeError(w, r, http.StatusBadRequest, "Invalid JSON payload")
		return
	}

	if request.Name == "" {
		s.writeError(w, r, http.StatusBadRequest, "Secret name is required")
		return
	}

	if request.Value == "" {
		s.writeError(w, r, http.StatusBadRequest, "Secret value is required")
		return
	}

	// Default to keyring if type not specified
	if request.Type == "" {
		request.Type = secretTypeKeyring
	}

	// Only allow keyring type for security
	if request.Type != secretTypeKeyring {
		s.writeError(w, r, http.StatusBadRequest, "Only keyring type is supported")
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
		s.writeError(w, r, http.StatusInternalServerError, fmt.Sprintf("Failed to store secret: %v", err))
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
		s.writeError(w, r, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	resolver := s.controller.GetSecretResolver()
	if resolver == nil {
		s.writeError(w, r, http.StatusInternalServerError, "Secret resolver not available")
		return
	}

	name := chi.URLParam(r, "name")
	if name == "" {
		s.writeError(w, r, http.StatusBadRequest, "Secret name is required")
		return
	}

	// Get optional type from query parameter, default to keyring
	secretType := r.URL.Query().Get("type")
	if secretType == "" {
		secretType = secretTypeKeyring
	}

	// Only allow keyring type for security
	if secretType != secretTypeKeyring {
		s.writeError(w, r, http.StatusBadRequest, "Only keyring type is supported")
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
		s.writeError(w, r, http.StatusInternalServerError, fmt.Sprintf("Failed to delete secret: %v", err))
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
		s.writeError(w, r, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	// Try to use management service if available
	if mgmtSvc := s.controller.GetManagementService(); mgmtSvc != nil {
		diag, err := mgmtSvc.(interface {
			Doctor(context.Context) (*contracts.Diagnostics, error)
		}).Doctor(r.Context())

		if err != nil {
			s.logger.Error("Failed to get diagnostics via management service", "error", err)
			s.writeError(w, r, http.StatusInternalServerError, "Failed to get diagnostics")
			return
		}

		// Spec 042: aggregate doctor results into the telemetry registry.
		recordDoctorTelemetry(s.telemetryRegistry, diag)

		s.writeSuccess(w, diag)
		return
	}

	// Fallback to legacy path if management service not available
	genericServers, err := s.controller.GetAllServers()
	if err != nil {
		s.logger.Error("Failed to get servers for diagnostics", "error", err)
		s.writeError(w, r, http.StatusInternalServerError, "Failed to get servers")
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

// handleGetTelemetryPayload godoc
// @Summary Preview next telemetry heartbeat payload
// @Description Render the exact JSON heartbeat payload that mcpproxy would next send to the telemetry endpoint, without making a network call. Counters in the payload reflect the current in-memory state. Spec 042.
// @Tags telemetry
// @Produce json
// @Security ApiKeyAuth
// @Security ApiKeyQuery
// @Success 200 {object} contracts.SuccessResponse "Telemetry heartbeat payload"
// @Failure 503 {object} contracts.ErrorResponse "Telemetry service unavailable"
// @Router /api/v1/telemetry/payload [get]
func (s *Server) handleGetTelemetryPayload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeError(w, r, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	if s.telemetryPayloadProvider == nil {
		s.writeError(w, r, http.StatusServiceUnavailable, "telemetry service unavailable")
		return
	}

	svc := s.telemetryPayloadProvider()
	if svc == nil {
		s.writeError(w, r, http.StatusServiceUnavailable, "telemetry service unavailable")
		return
	}

	payload := svc.BuildPayload()
	s.writeSuccess(w, payload)
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
		s.writeError(w, r, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	tokenStats, err := s.controller.GetTokenSavings()
	if err != nil {
		s.logger.Error("Failed to calculate token savings", "error", err)
		s.writeError(w, r, http.StatusInternalServerError, fmt.Sprintf("Failed to calculate token savings: %v", err))
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
		s.writeError(w, r, http.StatusMethodNotAllowed, "Method not allowed")
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
		s.writeError(w, r, http.StatusInternalServerError, "Failed to get tool calls")
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
		s.writeError(w, r, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	id := chi.URLParam(r, "id")
	if id == "" {
		s.writeError(w, r, http.StatusBadRequest, "Tool call ID required")
		return
	}

	// Get tool call by ID
	toolCall, err := s.controller.GetToolCallByID(id)
	if err != nil {
		s.logger.Error("Failed to get tool call detail", "id", id, "error", err)
		s.writeError(w, r, http.StatusNotFound, "Tool call not found")
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
		s.writeError(w, r, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	serverID := chi.URLParam(r, "id")
	if serverID == "" {
		s.writeError(w, r, http.StatusBadRequest, "Server ID required")
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
		s.writeError(w, r, http.StatusInternalServerError, "Failed to get server tool calls")
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
		s.writeError(w, r, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	id := chi.URLParam(r, "id")
	if id == "" {
		s.writeError(w, r, http.StatusBadRequest, "Tool call ID required")
		return
	}

	// Parse request body for modified arguments
	var request contracts.ReplayToolCallRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		s.writeError(w, r, http.StatusBadRequest, "Invalid JSON payload")
		return
	}

	// Replay the tool call with modified arguments
	newToolCall, err := s.controller.ReplayToolCall(id, request.Arguments)
	if err != nil {
		s.logger.Error("Failed to replay tool call", "id", id, "error", err)
		s.writeError(w, r, http.StatusInternalServerError, fmt.Sprintf("Failed to replay tool call: %v", err))
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
		s.writeError(w, r, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	cfg, err := s.controller.GetConfig()
	if err != nil {
		s.logger.Error("Failed to get configuration", "error", err)
		s.writeError(w, r, http.StatusInternalServerError, "Failed to get configuration")
		return
	}

	if cfg == nil {
		s.writeError(w, r, http.StatusInternalServerError, "Configuration not available")
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
		s.writeError(w, r, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	var cfg config.Config
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		s.writeError(w, r, http.StatusBadRequest, "Invalid JSON payload")
		return
	}

	// Perform validation
	validationErrors, err := s.controller.ValidateConfig(&cfg)
	if err != nil {
		s.logger.Error("Failed to validate configuration", "error", err)
		s.writeError(w, r, http.StatusInternalServerError, fmt.Sprintf("Validation failed: %v", err))
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
		s.writeError(w, r, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	var cfg config.Config
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		s.writeError(w, r, http.StatusBadRequest, "Invalid JSON payload")
		return
	}

	// Get config path from controller
	cfgPath := s.controller.GetConfigPath()

	// Apply configuration
	result, err := s.controller.ApplyConfig(&cfg, cfgPath)
	if err != nil {
		s.logger.Error("Failed to apply configuration", "error", err)
		s.writeError(w, r, http.StatusInternalServerError, fmt.Sprintf("Failed to apply configuration: %v", err))
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

// handlePatchDockerIsolation godoc
// @Summary      Toggle global Docker isolation
// @Description  Convenience endpoint to flip `docker_isolation.enabled` without resending the full config. Persists to disk via the existing config writer — the file watcher then hot-reloads the change. Returns the new state and whether a restart is required for existing connections to pick it up.
// @Tags         config
// @Accept       json
// @Produce      json
// @Param        payload  body      object{enabled=bool}          true  "New isolation state"
// @Success      200      {object}  contracts.ConfigApplyResult   "Isolation toggle applied"
// @Failure      400      {object}  contracts.ErrorResponse       "Invalid JSON payload"
// @Failure      401      {object}  contracts.ErrorResponse       "Unauthorized - missing or invalid API key"
// @Failure      500      {object}  contracts.ErrorResponse       "Failed to apply configuration"
// @Security     ApiKeyAuth
// @Security     ApiKeyQuery
// @Router       /api/v1/config/docker-isolation [patch]
func (s *Server) handlePatchDockerIsolation(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Enabled *bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		s.writeError(w, r, http.StatusBadRequest, "Invalid JSON payload")
		return
	}
	if payload.Enabled == nil {
		s.writeError(w, r, http.StatusBadRequest, "Field 'enabled' is required")
		return
	}

	// Fetch current config, mutate the single field, and push it back through
	// the existing apply pipeline so we benefit from validation, change
	// detection, disk persistence, and hot-reload without duplicating any of
	// that logic here.
	cfg, err := s.controller.GetConfig()
	if err != nil {
		s.logger.Error("Failed to get configuration for docker-isolation patch", "error", err)
		s.writeError(w, r, http.StatusInternalServerError, "Failed to read configuration")
		return
	}
	if cfg == nil {
		s.writeError(w, r, http.StatusInternalServerError, "Configuration not available")
		return
	}

	if cfg.DockerIsolation == nil {
		cfg.DockerIsolation = config.DefaultDockerIsolationConfig()
	}
	cfg.DockerIsolation.Enabled = *payload.Enabled

	cfgPath := s.controller.GetConfigPath()
	result, err := s.controller.ApplyConfig(cfg, cfgPath)
	if err != nil {
		s.logger.Error("Failed to apply docker-isolation toggle", "error", err)
		s.writeError(w, r, http.StatusInternalServerError, fmt.Sprintf("Failed to apply configuration: %v", err))
		return
	}

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
		s.writeError(w, r, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	var request struct {
		ToolName  string                 `json:"tool_name"`
		Arguments map[string]interface{} `json:"arguments"`
	}

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		s.writeError(w, r, http.StatusBadRequest, "Invalid JSON payload")
		return
	}

	if request.ToolName == "" {
		s.writeError(w, r, http.StatusBadRequest, "Tool name is required")
		return
	}

	// Set request source to CLI for REST API tool calls (typically from CLI)
	// This allows activity logging to distinguish between MCP protocol and CLI calls
	ctx := reqcontext.WithRequestSource(r.Context(), reqcontext.SourceCLI)

	// Call tool via controller
	result, err := s.controller.CallTool(ctx, request.ToolName, request.Arguments)
	if err != nil {
		s.logger.Error("Failed to call tool", "tool", request.ToolName, "error", err)
		s.writeError(w, r, http.StatusInternalServerError, fmt.Sprintf("Failed to call tool: %v", err))
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
func (s *Server) handleListRegistries(w http.ResponseWriter, r *http.Request) {
	registries, err := s.controller.ListRegistries()
	if err != nil {
		s.logger.Error("Failed to list registries", "error", err)
		s.writeError(w, r, http.StatusInternalServerError, fmt.Sprintf("Failed to list registries: %v", err))
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
		s.writeError(w, r, http.StatusBadRequest, "Registry ID is required")
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
		s.writeError(w, r, http.StatusInternalServerError, fmt.Sprintf("Failed to search servers: %v", err))
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
		s.writeError(w, r, http.StatusMethodNotAllowed, "Method not allowed")
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
		s.writeError(w, r, http.StatusInternalServerError, "Failed to get sessions")
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
		s.writeError(w, r, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	id := chi.URLParam(r, "id")
	if id == "" {
		s.writeError(w, r, http.StatusBadRequest, "Session ID required")
		return
	}

	// Get session by ID
	session, err := s.controller.GetSessionByID(id)
	if err != nil {
		s.logger.Error("Failed to get session detail", "id", id, "error", err)
		s.writeError(w, r, http.StatusNotFound, "Session not found")
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
		s.writeError(w, r, http.StatusInternalServerError, "failed to get Docker status")
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

// handleApproveTools handles POST /api/v1/servers/{id}/tools/approve
func (s *Server) handleApproveTools(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "id")
	if serverID == "" {
		s.writeError(w, r, http.StatusBadRequest, "Server ID required")
		return
	}

	var req struct {
		Tools      []string `json:"tools"`
		ApproveAll bool     `json:"approve_all"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, r, http.StatusBadRequest, fmt.Sprintf("Invalid request body: %v", err))
		return
	}

	if req.ApproveAll {
		count, err := s.controller.ApproveAllTools(serverID, "api")
		if err != nil {
			s.writeError(w, r, http.StatusInternalServerError, fmt.Sprintf("Failed to approve tools: %v", err))
			return
		}
		s.writeSuccess(w, map[string]interface{}{
			"approved": count,
			"message":  fmt.Sprintf("Approved %d tools for server %s", count, serverID),
		})
		return
	}

	if len(req.Tools) == 0 {
		s.writeError(w, r, http.StatusBadRequest, "Either 'tools' array or 'approve_all: true' required")
		return
	}

	if err := s.controller.ApproveTools(serverID, req.Tools, "api"); err != nil {
		s.writeError(w, r, http.StatusInternalServerError, fmt.Sprintf("Failed to approve tools: %v", err))
		return
	}

	s.writeSuccess(w, map[string]interface{}{
		"approved": len(req.Tools),
		"tools":    req.Tools,
		"message":  fmt.Sprintf("Approved %d tools for server %s", len(req.Tools), serverID),
	})
}

// handleGetToolDiff handles GET /api/v1/servers/{id}/tools/{tool}/diff
func (s *Server) handleGetToolDiff(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "id")
	toolName := chi.URLParam(r, "tool")

	if serverID == "" || toolName == "" {
		s.writeError(w, r, http.StatusBadRequest, "Server ID and tool name required")
		return
	}

	record, err := s.controller.GetToolApproval(serverID, toolName)
	if err != nil {
		s.writeError(w, r, http.StatusNotFound, fmt.Sprintf("Tool approval record not found: %v", err))
		return
	}

	if record.Status != storage.ToolApprovalStatusChanged {
		s.writeError(w, r, http.StatusNotFound, "No changes detected for this tool")
		return
	}

	s.writeSuccess(w, map[string]interface{}{
		"server_name":          record.ServerName,
		"tool_name":            record.ToolName,
		"status":               record.Status,
		"approved_hash":        record.ApprovedHash,
		"current_hash":         record.CurrentHash,
		"previous_description": record.PreviousDescription,
		"current_description":  record.CurrentDescription,
		"previous_schema":      record.PreviousSchema,
		"current_schema":       record.CurrentSchema,
	})
}

// handleExportToolDescriptions handles GET /api/v1/servers/{id}/tools/export
func (s *Server) handleExportToolDescriptions(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "id")
	if serverID == "" {
		s.writeError(w, r, http.StatusBadRequest, "Server ID required")
		return
	}

	records, err := s.controller.ListToolApprovals(serverID)
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, fmt.Sprintf("Failed to list tool approvals: %v", err))
		return
	}

	format := r.URL.Query().Get("format")
	if format == "" {
		format = "json"
	}

	if format == "text" {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		for _, record := range records {
			fmt.Fprintf(w, "=== %s:%s ===\n", record.ServerName, record.ToolName)
			fmt.Fprintf(w, "Status: %s\n", record.Status)
			fmt.Fprintf(w, "Hash: %s\n", record.CurrentHash)
			if record.CurrentDescription != "" {
				fmt.Fprintf(w, "Description:\n%s\n", record.CurrentDescription)
			}
			if record.CurrentSchema != "" {
				fmt.Fprintf(w, "Schema:\n%s\n", record.CurrentSchema)
			}
			fmt.Fprintln(w)
		}
		return
	}

	// JSON format
	type toolExport struct {
		ServerName  string `json:"server_name"`
		ToolName    string `json:"tool_name"`
		Status      string `json:"status"`
		Hash        string `json:"hash"`
		Description string `json:"description"`
		Schema      string `json:"schema,omitempty"`
	}

	var exports []toolExport
	for _, record := range records {
		exports = append(exports, toolExport{
			ServerName:  record.ServerName,
			ToolName:    record.ToolName,
			Status:      record.Status,
			Hash:        record.CurrentHash,
			Description: record.CurrentDescription,
			Schema:      record.CurrentSchema,
		})
	}

	s.writeSuccess(w, map[string]interface{}{
		"server_name": serverID,
		"tools":       exports,
		"count":       len(exports),
	})
}

// handleAnnotationCoverage godoc
// @Summary Get annotation coverage report
// @Description Reports how many upstream tools have MCP annotations vs don't, broken down by server
// @Tags annotations
// @Produce json
// @Security ApiKeyAuth
// @Security ApiKeyQuery
// @Success 200 {object} contracts.SuccessResponse "Annotation coverage report"
// @Router /api/v1/annotations/coverage [get]
func (s *Server) handleAnnotationCoverage(w http.ResponseWriter, r *http.Request) {
	type serverCoverage struct {
		Name            string  `json:"name"`
		TotalTools      int     `json:"total_tools"`
		AnnotatedTools  int     `json:"annotated_tools"`
		CoveragePercent float64 `json:"coverage_percent"`
	}

	type coverageResponse struct {
		TotalTools      int              `json:"total_tools"`
		AnnotatedTools  int              `json:"annotated_tools"`
		CoveragePercent float64          `json:"coverage_percent"`
		Servers         []serverCoverage `json:"servers"`
	}

	allServers, err := s.controller.GetAllServers()
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, "Failed to get servers")
		return
	}

	resp := coverageResponse{
		Servers: make([]serverCoverage, 0, len(allServers)),
	}

	for _, srv := range allServers {
		name, _ := srv["name"].(string)
		if name == "" {
			continue
		}

		tools, err := s.controller.GetServerTools(name)
		if err != nil {
			// Skip servers whose tools can't be retrieved (disconnected, etc.)
			continue
		}

		sc := serverCoverage{
			Name:       name,
			TotalTools: len(tools),
		}

		for _, tool := range tools {
			if hasAnnotationHints(tool) {
				sc.AnnotatedTools++
			}
		}

		if sc.TotalTools > 0 {
			sc.CoveragePercent = math.Round(float64(sc.AnnotatedTools)/float64(sc.TotalTools)*10000) / 100
		}

		resp.TotalTools += sc.TotalTools
		resp.AnnotatedTools += sc.AnnotatedTools
		resp.Servers = append(resp.Servers, sc)
	}

	if resp.TotalTools > 0 {
		resp.CoveragePercent = math.Round(float64(resp.AnnotatedTools)/float64(resp.TotalTools)*10000) / 100
	}

	s.writeSuccess(w, resp)
}

// hasAnnotationHints checks if a tool map has meaningful annotation hints.
// A tool is considered "annotated" if its Annotations is non-nil AND at least
// one of ReadOnlyHint, DestructiveHint, IdempotentHint, OpenWorldHint is set.
// Title alone does not count as a meaningful annotation.
func hasAnnotationHints(tool map[string]interface{}) bool {
	ann, ok := tool["annotations"]
	if !ok || ann == nil {
		return false
	}

	// Check if it's a *config.ToolAnnotations (direct from stateview)
	if ta, ok := ann.(*config.ToolAnnotations); ok {
		return ta.ReadOnlyHint != nil || ta.DestructiveHint != nil ||
			ta.IdempotentHint != nil || ta.OpenWorldHint != nil
	}

	// Fallback: check as map (e.g., from JSON round-trip)
	if m, ok := ann.(map[string]interface{}); ok {
		for _, key := range []string{"readOnlyHint", "destructiveHint", "idempotentHint", "openWorldHint"} {
			if v, exists := m[key]; exists && v != nil {
				return true
			}
		}
	}

	return false
}

// toolApprovalPriority returns sort priority (lower = first) for approval status
func toolApprovalPriority(status string) int {
	switch status {
	case "pending":
		return 0
	case "changed":
		return 1
	default:
		return 2
	}
}
