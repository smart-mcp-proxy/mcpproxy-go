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
)

// ServerController defines the interface for core server functionality
type ServerController interface {
	IsRunning() bool
	GetListenAddress() string
	GetUpstreamStats() map[string]interface{}
	StartServer(ctx context.Context) error
	StopServer() error
	GetStatus() interface{}
	StatusChannel() <-chan interface{}

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
}

// Server provides HTTP API endpoints with chi router
type Server struct {
	controller ServerController
	logger     *zap.SugaredLogger
	router     *chi.Mux
}

// NewServer creates a new HTTP API server
func NewServer(controller ServerController, logger *zap.SugaredLogger) *Server {
	s := &Server{
		controller: controller,
		logger:     logger,
		router:     chi.NewRouter(),
	}

	s.setupRoutes()
	return s
}

// ServeHTTP implements http.Handler
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.router.ServeHTTP(w, r)
}

// setupRoutes configures all API routes
func (s *Server) setupRoutes() {
	// Middleware
	s.router.Use(middleware.Logger)
	s.router.Use(middleware.Recoverer)
	s.router.Use(middleware.RequestID)
	s.router.Use(middleware.Timeout(60 * time.Second))

	// CORS headers for browser access
	s.router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

			if r.Method == "OPTIONS" {
				w.WriteHeader(http.StatusOK)
				return
			}

			next.ServeHTTP(w, r)
		})
	})

	// API v1 routes
	s.router.Route("/api/v1", func(r chi.Router) {
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
	})

	// SSE events
	s.router.Get("/events", s.handleSSEEvents)

	// Legacy API compatibility (for existing tray)
	s.router.Route("/api", func(r chi.Router) {
		r.Get("/status", s.handleLegacyStatus)
		r.Get("/events", s.handleSSEEvents)
		r.Get("/servers", s.handleLegacyGetServers)
		r.Route("/servers/{name}", func(r chi.Router) {
			r.Post("/enable", s.handleLegacyServerAction)
			r.Post("/quarantine", s.handleLegacyServerAction)
		})
		r.Route("/control", func(r chi.Router) {
			r.Post("/start", s.handleLegacyStart)
			r.Post("/stop", s.handleLegacyStop)
			r.Post("/reload", s.handleLegacyReload)
		})
		r.Post("/oauth/{name}", s.handleLegacyOAuth)
	})
}

// JSON response helpers

type APIResponse struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
}

func (s *Server) writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		s.logger.Error("Failed to encode JSON response", "error", err)
	}
}

func (s *Server) writeError(w http.ResponseWriter, status int, message string) {
	s.writeJSON(w, status, APIResponse{
		Success: false,
		Error:   message,
	})
}

func (s *Server) writeSuccess(w http.ResponseWriter, data interface{}) {
	s.writeJSON(w, http.StatusOK, APIResponse{
		Success: true,
		Data:    data,
	})
}

// API v1 handlers

func (s *Server) handleGetServers(w http.ResponseWriter, r *http.Request) {
	servers, err := s.controller.GetAllServers()
	if err != nil {
		s.logger.Error("Failed to get servers", "error", err)
		s.writeError(w, http.StatusInternalServerError, "Failed to get servers")
		return
	}

	s.writeSuccess(w, map[string]interface{}{
		"servers": servers,
		"stats":   s.controller.GetUpstreamStats(),
	})
}

func (s *Server) handleEnableServer(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "id")
	if serverID == "" {
		s.writeError(w, http.StatusBadRequest, "Server ID required")
		return
	}

	if err := s.controller.EnableServer(serverID, true); err != nil {
		s.logger.Error("Failed to enable server", "server", serverID, "error", err)
		s.writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to enable server: %v", err))
		return
	}

	s.writeSuccess(w, map[string]interface{}{
		"server":  serverID,
		"enabled": true,
	})
}

func (s *Server) handleDisableServer(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "id")
	if serverID == "" {
		s.writeError(w, http.StatusBadRequest, "Server ID required")
		return
	}

	if err := s.controller.EnableServer(serverID, false); err != nil {
		s.logger.Error("Failed to disable server", "server", serverID, "error", err)
		s.writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to disable server: %v", err))
		return
	}

	s.writeSuccess(w, map[string]interface{}{
		"server":  serverID,
		"enabled": false,
	})
}

func (s *Server) handleRestartServer(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "id")
	if serverID == "" {
		s.writeError(w, http.StatusBadRequest, "Server ID required")
		return
	}

	// Restart = disable then enable
	if err := s.controller.EnableServer(serverID, false); err != nil {
		s.logger.Error("Failed to disable server for restart", "server", serverID, "error", err)
		s.writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to restart server: %v", err))
		return
	}

	time.Sleep(100 * time.Millisecond) // Brief pause

	if err := s.controller.EnableServer(serverID, true); err != nil {
		s.logger.Error("Failed to re-enable server for restart", "server", serverID, "error", err)
		s.writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to restart server: %v", err))
		return
	}

	s.writeSuccess(w, map[string]interface{}{
		"server":    serverID,
		"restarted": true,
	})
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

	s.writeSuccess(w, map[string]interface{}{
		"server":          serverID,
		"login_triggered": true,
	})
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

	s.writeSuccess(w, map[string]interface{}{
		"server": serverID,
		"tools":  tools,
	})
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

	s.writeSuccess(w, map[string]interface{}{
		"server": serverID,
		"logs":   logs,
		"tail":   tail,
	})
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

	s.writeSuccess(w, map[string]interface{}{
		"query":   query,
		"limit":   limit,
		"results": results,
	})
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

	// Get status channel
	statusCh := s.controller.StatusChannel()

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

// Legacy API handlers for backward compatibility with existing tray

func (s *Server) handleLegacyStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	status := s.controller.GetStatus()
	response := map[string]interface{}{
		"running":        s.controller.IsRunning(),
		"listen_addr":    s.controller.GetListenAddress(),
		"upstream_stats": s.controller.GetUpstreamStats(),
		"status":         status,
		"timestamp":      time.Now().Unix(),
	}

	s.writeJSON(w, http.StatusOK, response)
}

func (s *Server) handleLegacyGetServers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	servers, err := s.controller.GetAllServers()
	if err != nil {
		s.logger.Error("Failed to get servers", "error", err)
		http.Error(w, "Failed to get servers", http.StatusInternalServerError)
		return
	}

	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"servers": servers,
	})
}

func (s *Server) handleLegacyServerAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	serverName := chi.URLParam(r, "name")
	action := strings.TrimPrefix(r.URL.Path, "/api/servers/"+serverName+"/")

	var err error
	var result map[string]interface{}

	switch action {
	case "enable":
		var req struct {
			Enabled bool `json:"enabled"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}
		err = s.controller.EnableServer(serverName, req.Enabled)
		result = map[string]interface{}{
			"success": err == nil,
			"action":  "enable",
			"enabled": req.Enabled,
		}

	case "quarantine":
		var req struct {
			Quarantined bool `json:"quarantined"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}
		if req.Quarantined {
			err = s.controller.QuarantineServer(serverName, true)
		} else {
			err = s.controller.UnquarantineServer(serverName)
		}
		result = map[string]interface{}{
			"success":     err == nil,
			"action":      "quarantine",
			"quarantined": req.Quarantined,
		}

	default:
		http.Error(w, "Unknown action", http.StatusBadRequest)
		return
	}

	if err != nil {
		s.logger.Error("Server action failed", "server", serverName, "action", action, "error", err)
		result["error"] = err.Error()
	}

	s.writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleLegacyStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	err := s.controller.StartServer(r.Context())
	result := map[string]interface{}{
		"success": err == nil,
		"action":  "start",
	}

	if err != nil {
		result["error"] = err.Error()
		s.logger.Error("Failed to start server", "error", err)
	}

	s.writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleLegacyStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	err := s.controller.StopServer()
	result := map[string]interface{}{
		"success": err == nil,
		"action":  "stop",
	}

	if err != nil {
		result["error"] = err.Error()
		s.logger.Error("Failed to stop server", "error", err)
	}

	s.writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleLegacyReload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	err := s.controller.ReloadConfiguration()
	result := map[string]interface{}{
		"success": err == nil,
		"action":  "reload",
	}

	if err != nil {
		result["error"] = err.Error()
		s.logger.Error("Failed to reload configuration", "error", err)
	}

	s.writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleLegacyOAuth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	serverName := chi.URLParam(r, "name")
	if serverName == "" {
		http.Error(w, "Server name required", http.StatusBadRequest)
		return
	}

	err := s.controller.TriggerOAuthLogin(serverName)
	result := map[string]interface{}{
		"success": err == nil,
		"action":  "oauth",
		"server":  serverName,
	}

	if err != nil {
		result["error"] = err.Error()
		s.logger.Error("Failed to trigger OAuth", "server", serverName, "error", err)
	}

	s.writeJSON(w, http.StatusOK, result)
}
