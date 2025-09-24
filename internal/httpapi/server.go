package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"go.uber.org/zap"

	"mcpproxy-go/internal/config"
	"mcpproxy-go/internal/contracts"
	"mcpproxy-go/internal/logs"
	"mcpproxy-go/internal/observability"
	internalRuntime "mcpproxy-go/internal/runtime"
	"mcpproxy-go/internal/secret"
)

const asyncToggleTimeout = 5 * time.Second

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
	QuarantineServer(serverName string, quarantined bool) error
	GetQuarantinedServers() ([]map[string]interface{}, error)
	UnquarantineServer(serverName string) error

	// Tools and search
	GetServerTools(serverName string) ([]map[string]interface{}, error)
	SearchTools(query string, limit int) ([]map[string]interface{}, error)

	// Logs
	GetServerLogs(serverName string, tail int) ([]string, error)

	// Config and OAuth
	ReloadConfiguration() error
	GetConfigPath() string
	GetLogDir() string
	TriggerOAuthLogin(serverName string) error

	// Secrets management
	GetSecretResolver() *secret.Resolver
	GetCurrentConfig() interface{}
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
func (s *Server) apiKeyAuthMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

			// If API key is empty, authentication is disabled
			if cfg.APIKey == "" {
				next.ServeHTTP(w, r)
				return
			}

			// Validate API key
			if !s.validateAPIKey(r, cfg.APIKey) {
				s.writeError(w, http.StatusUnauthorized, "Invalid or missing API key")
				return
			}

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
	s.logger.Debug("Core middleware configured (logging, recovery, request ID)")

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

		// Server management
		r.Get("/servers", s.handleGetServers)
		r.Route("/servers/{id}", func(r chi.Router) {
			r.Post("/enable", s.handleEnableServer)
			r.Post("/disable", s.handleDisableServer)
			r.Post("/restart", s.handleRestartServer)
			r.Post("/login", s.handleServerLogin)
			r.Get("/tools", s.handleGetServerTools)
			r.Get("/logs", s.handleGetServerLogs)
		})

		// Search
		r.Get("/index/search", s.handleSearchTools)

		// Secrets management
		r.Route("/secrets", func(r chi.Router) {
			r.Get("/refs", s.handleGetSecretRefs)
			r.Get("/config", s.handleGetConfigSecrets)
			r.Post("/migrate", s.handleMigrateSecrets)
		})
	})

	// SSE events (protected by API key)
	s.router.With(s.apiKeyAuthMiddleware()).Get("/events", s.handleSSEEvents)

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

func (s *Server) handleGetServers(w http.ResponseWriter, _ *http.Request) {
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

func (s *Server) handleEnableServer(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "id")
	if serverID == "" {
		s.writeError(w, http.StatusBadRequest, "Server ID required")
		return
	}

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

func (s *Server) handleDisableServer(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "id")
	if serverID == "" {
		s.writeError(w, http.StatusBadRequest, "Server ID required")
		return
	}

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

func (s *Server) handleRestartServer(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "id")
	if serverID == "" {
		s.writeError(w, http.StatusBadRequest, "Server ID required")
		return
	}

	done := make(chan error, 1)
	go func() {
		if err := s.controller.EnableServer(serverID, false); err != nil {
			done <- err
			return
		}
		time.Sleep(100 * time.Millisecond)
		done <- s.controller.EnableServer(serverID, true)
	}()

	select {
	case err := <-done:
		if err != nil {
			s.logger.Error("Failed to restart server", "server", serverID, "error", err)
			s.writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to restart server: %v", err))
			return
		}
		s.logger.Debug("Server restart completed synchronously", "server", serverID)
	case <-time.After(asyncToggleTimeout):
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
		Async:   false, // restart is handled synchronously
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

func (s *Server) handleServerLogin(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "id")
	if serverID == "" {
		s.writeError(w, http.StatusBadRequest, "Server ID required")
		return
	}

	if err := s.controller.TriggerOAuthLogin(serverID); err != nil {
		s.logger.Error("Failed to trigger OAuth login", "server", serverID, "error", err)
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

func (s *Server) handleGetServerTools(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "id")
	if serverID == "" {
		s.writeError(w, http.StatusBadRequest, "Server ID required")
		return
	}

	tools, err := s.controller.GetServerTools(serverID)
	if err != nil {
		s.logger.Error("Failed to get server tools", "server", serverID, "error", err)
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

	logs, err := s.controller.GetServerLogs(serverID, tail)
	if err != nil {
		s.logger.Error("Failed to get server logs", "server", serverID, "error", err)
		s.writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to get logs: %v", err))
		return
	}

	// Convert log strings to typed log entries
	logEntries := make([]contracts.LogEntry, len(logs))
	for i, logLine := range logs {
		logEntries[i] = *contracts.ConvertLogEntry(logLine, serverID)
	}

	response := contracts.GetServerLogsResponse{
		ServerName: serverID,
		Logs:       logEntries,
		Count:      len(logEntries),
	}

	s.writeSuccess(w, response)
}

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
	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	flusher, ok := w.(http.Flusher)
	if !ok {
		s.writeError(w, http.StatusInternalServerError, "Streaming not supported")
		return
	}

	// Get status & event channels
	statusCh := s.controller.StatusChannel()
	eventsCh := s.controller.EventsChannel()

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

	if err := s.writeSSEEvent(w, "status", initialStatus); err != nil {
		s.logger.Error("Failed to write initial SSE event", "error", err)
		return
	}
	flusher.Flush()

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
			if err := s.writeSSEEvent(w, "ping", pingData); err != nil {
				s.logger.Error("Failed to write SSE heartbeat", "error", err)
				return
			}
			flusher.Flush()
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

			if err := s.writeSSEEvent(w, "status", response); err != nil {
				s.logger.Error("Failed to write SSE event", "error", err)
				return
			}
			flusher.Flush()
		case evt, ok := <-eventsCh:
			if !ok {
				eventsCh = nil
				continue
			}

			eventPayload := map[string]interface{}{
				"payload":   evt.Payload,
				"timestamp": evt.Timestamp.Unix(),
			}

			if err := s.writeSSEEvent(w, string(evt.Type), eventPayload); err != nil {
				s.logger.Error("Failed to write runtime SSE event", "error", err)
				return
			}
			flusher.Flush()
		}
	}
}

func (s *Server) writeSSEEvent(w http.ResponseWriter, event string, data interface{}) error {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return err
	}

	_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, string(jsonData))
	return err
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
