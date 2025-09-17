package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"
)

// ServerController provides API access to server functionality
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

	// Config and OAuth
	ReloadConfiguration() error
	GetConfigPath() string
	GetLogDir() string
	TriggerOAuthLogin(serverName string) error
}

// SetupRoutes adds API routes to the provided mux
func SetupRoutes(mux *http.ServeMux, server ServerController, logger *zap.SugaredLogger) {
	h := &handlers{
		server: server,
		logger: logger,
	}
	// Status endpoints
	mux.HandleFunc("/api/status", h.handleStatus)
	mux.HandleFunc("/api/events", h.handleEvents)

	// Server management
	mux.HandleFunc("/api/servers", h.handleServers)
	mux.HandleFunc("/api/servers/", h.handleServerActions)

	// Control endpoints
	mux.HandleFunc("/api/control/start", h.handleStart)
	mux.HandleFunc("/api/control/stop", h.handleStop)
	mux.HandleFunc("/api/control/reload", h.handleReload)

	// OAuth endpoints
	mux.HandleFunc("/api/oauth/", h.handleOAuth)
}

// handlers provides HTTP API handlers for tray communication
type handlers struct {
	server ServerController
	logger *zap.SugaredLogger
}

// handleStatus returns the current server status
func (h *handlers) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	status := h.server.GetStatus()

	// Convert to a consistent format
	response := map[string]interface{}{
		"running":        h.server.IsRunning(),
		"listen_addr":    h.server.GetListenAddress(),
		"upstream_stats": h.server.GetUpstreamStats(),
		"status":         status,
		"timestamp":      time.Now().Unix(),
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		h.logger.Error("Failed to encode status response", "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

// handleEvents provides SSE endpoint for real-time status updates
func (h *handlers) handleEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	// Get status channel
	statusCh := h.server.StatusChannel()

	// Send initial status
	initialStatus := map[string]interface{}{
		"running":        h.server.IsRunning(),
		"listen_addr":    h.server.GetListenAddress(),
		"upstream_stats": h.server.GetUpstreamStats(),
		"status":         h.server.GetStatus(),
		"timestamp":      time.Now().Unix(),
	}

	if err := h.writeSSEEvent(w, "status", initialStatus); err != nil {
		h.logger.Error("Failed to write initial SSE event", "error", err)
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
				"running":        h.server.IsRunning(),
				"listen_addr":    h.server.GetListenAddress(),
				"upstream_stats": h.server.GetUpstreamStats(),
				"status":         status,
				"timestamp":      time.Now().Unix(),
			}

			if err := h.writeSSEEvent(w, "status", response); err != nil {
				h.logger.Error("Failed to write SSE event", "error", err)
				return
			}
			flusher.Flush()
		}
	}
}

// writeSSEEvent writes a Server-Sent Event
func (h *handlers) writeSSEEvent(w http.ResponseWriter, event string, data interface{}) error {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return err
	}

	_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, string(jsonData))
	return err
}

// handleServers handles server listing and bulk operations
func (h *handlers) handleServers(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		servers, err := h.server.GetAllServers()
		if err != nil {
			h.logger.Error("Failed to get servers", "error", err)
			http.Error(w, "Failed to get servers", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]interface{}{
			"servers": servers,
		}); err != nil {
			h.logger.Error("Failed to encode servers response", "error", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
		}

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleServerActions handles individual server actions
func (h *handlers) handleServerActions(w http.ResponseWriter, r *http.Request) {
	// Parse server name from path: /api/servers/{name}/{action}
	path := strings.TrimPrefix(r.URL.Path, "/api/servers/")
	parts := strings.Split(path, "/")
	if len(parts) < 2 {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}

	serverName := parts[0]
	action := parts[1]

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

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
		err = h.server.EnableServer(serverName, req.Enabled)
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
			err = h.server.QuarantineServer(serverName, true)
		} else {
			err = h.server.UnquarantineServer(serverName)
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
		h.logger.Error("Server action failed", "server", serverName, "action", action, "error", err)
		result["error"] = err.Error()
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(result); err != nil {
		h.logger.Error("Failed to encode action response", "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

// handleStart starts the server
func (h *handlers) handleStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	err := h.server.StartServer(r.Context())
	result := map[string]interface{}{
		"success": err == nil,
		"action":  "start",
	}

	if err != nil {
		result["error"] = err.Error()
		h.logger.Error("Failed to start server", "error", err)
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(result); err != nil {
		h.logger.Error("Failed to encode start response", "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

// handleStop stops the server
func (h *handlers) handleStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	err := h.server.StopServer()
	result := map[string]interface{}{
		"success": err == nil,
		"action":  "stop",
	}

	if err != nil {
		result["error"] = err.Error()
		h.logger.Error("Failed to stop server", "error", err)
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(result); err != nil {
		h.logger.Error("Failed to encode stop response", "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

// handleReload reloads the server configuration
func (h *handlers) handleReload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	err := h.server.ReloadConfiguration()
	result := map[string]interface{}{
		"success": err == nil,
		"action":  "reload",
	}

	if err != nil {
		result["error"] = err.Error()
		h.logger.Error("Failed to reload configuration", "error", err)
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(result); err != nil {
		h.logger.Error("Failed to encode reload response", "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

// handleOAuth triggers OAuth login for a server
func (h *handlers) handleOAuth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse server name from path: /api/oauth/{name}
	serverName := strings.TrimPrefix(r.URL.Path, "/api/oauth/")
	if serverName == "" {
		http.Error(w, "Server name required", http.StatusBadRequest)
		return
	}

	err := h.server.TriggerOAuthLogin(serverName)
	result := map[string]interface{}{
		"success": err == nil,
		"action":  "oauth",
		"server":  serverName,
	}

	if err != nil {
		result["error"] = err.Error()
		h.logger.Error("Failed to trigger OAuth", "server", serverName, "error", err)
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(result); err != nil {
		h.logger.Error("Failed to encode OAuth response", "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}
