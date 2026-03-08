//go:build teams

package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/teams/users"
)

// UserHandlers provides REST endpoints for user server management.
type UserHandlers struct {
	userStore     *users.UserStore
	logger        *zap.SugaredLogger
	sharedServers []*config.ServerConfig
}

// NewUserHandlers creates a new UserHandlers instance.
func NewUserHandlers(userStore *users.UserStore, sharedServers []*config.ServerConfig, logger *zap.SugaredLogger) *UserHandlers {
	return &UserHandlers{
		userStore:     userStore,
		logger:        logger,
		sharedServers: sharedServers,
	}
}

// RegisterRoutes registers all user server management routes on the provided router.
func (h *UserHandlers) RegisterRoutes(r chi.Router) {
	r.Route("/user/servers", func(r chi.Router) {
		r.Get("/", h.listServers)
		r.Post("/", h.createServer)
		r.Get("/{name}", h.getServer)
		r.Put("/{name}", h.updateServer)
		r.Delete("/{name}", h.deleteServer)
		r.Post("/{name}/enable", h.enableServer)
	})
}

// RegisterRoutesWithPrefix registers user server routes with a path prefix.
func (h *UserHandlers) RegisterRoutesWithPrefix(r chi.Router, prefix string) {
	r.Get(prefix+"/user/servers", h.listServers)
	r.Post(prefix+"/user/servers", h.createServer)
	r.Get(prefix+"/user/servers/{name}", h.getServer)
	r.Put(prefix+"/user/servers/{name}", h.updateServer)
	r.Delete(prefix+"/user/servers/{name}", h.deleteServer)
	r.Post(prefix+"/user/servers/{name}/enable", h.enableServer)
}

// --- Request/Response types ---

// CreateServerRequest represents the request body for creating a personal server.
type CreateServerRequest struct {
	Name     string            `json:"name"`
	URL      string            `json:"url,omitempty"`
	Protocol string            `json:"protocol,omitempty"`
	Command  string            `json:"command,omitempty"`
	Args     []string          `json:"args,omitempty"`
	Headers  map[string]string `json:"headers,omitempty"`
}

// UpdateServerRequest represents the request body for updating a personal server.
type UpdateServerRequest struct {
	URL      string            `json:"url,omitempty"`
	Protocol string            `json:"protocol,omitempty"`
	Command  string            `json:"command,omitempty"`
	Args     []string          `json:"args,omitempty"`
	Headers  map[string]string `json:"headers,omitempty"`
	Enabled  *bool             `json:"enabled,omitempty"`
}

// EnableServerRequest represents the request body for enabling/disabling a server.
type EnableServerRequest struct {
	Enabled bool `json:"enabled"`
}

// ServerResponse wraps a ServerConfig with ownership information.
type ServerResponse struct {
	*config.ServerConfig
	Ownership string `json:"ownership"` // "personal" or "shared"
}

// ServerListResponse contains personal and shared servers for a user.
type ServerListResponse struct {
	Personal []*ServerResponse `json:"personal"`
	Shared   []*ServerResponse `json:"shared"`
}

// --- Handlers ---

// listServers returns the user's personal servers and the shared (admin-configured) servers.
func (h *UserHandlers) listServers(w http.ResponseWriter, r *http.Request) {
	userID, err := getUserID(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "Authentication required")
		return
	}

	personalConfigs, err := h.userStore.ListUserServers(userID)
	if err != nil {
		h.logger.Errorw("failed to list user servers", "user_id", userID, "error", err)
		writeError(w, http.StatusInternalServerError, "Failed to list servers")
		return
	}

	personal := make([]*ServerResponse, 0, len(personalConfigs))
	for _, sc := range personalConfigs {
		personal = append(personal, &ServerResponse{
			ServerConfig: sc,
			Ownership:    "personal",
		})
	}

	shared := make([]*ServerResponse, 0, len(h.sharedServers))
	for _, sc := range h.sharedServers {
		shared = append(shared, &ServerResponse{
			ServerConfig: sc,
			Ownership:    "shared",
		})
	}

	writeJSON(w, http.StatusOK, ServerListResponse{
		Personal: personal,
		Shared:   shared,
	})
}

// createServer adds a new personal server for the authenticated user.
func (h *UserHandlers) createServer(w http.ResponseWriter, r *http.Request) {
	userID, err := getUserID(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "Authentication required")
		return
	}

	var req CreateServerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "Server name is required")
		return
	}

	// Check conflict with shared servers.
	for _, shared := range h.sharedServers {
		if strings.EqualFold(shared.Name, req.Name) {
			writeError(w, http.StatusConflict, fmt.Sprintf("Server name %q conflicts with a shared server", req.Name))
			return
		}
	}

	// Check if user already has a server with this name.
	existing, err := h.userStore.GetUserServer(userID, req.Name)
	if err != nil {
		h.logger.Errorw("failed to check existing server", "user_id", userID, "name", req.Name, "error", err)
		writeError(w, http.StatusInternalServerError, "Failed to check existing server")
		return
	}
	if existing != nil {
		writeError(w, http.StatusConflict, fmt.Sprintf("Server %q already exists", req.Name))
		return
	}

	now := time.Now().UTC()
	sc := &config.ServerConfig{
		Name:     req.Name,
		URL:      req.URL,
		Protocol: req.Protocol,
		Command:  req.Command,
		Args:     req.Args,
		Headers:  req.Headers,
		Enabled:  true,
		Created:  now,
		Updated:  now,
	}

	if err := h.userStore.CreateUserServer(userID, sc); err != nil {
		h.logger.Errorw("failed to create user server", "user_id", userID, "name", req.Name, "error", err)
		writeError(w, http.StatusInternalServerError, "Failed to create server")
		return
	}

	h.logger.Infow("user server created", "user_id", userID, "name", req.Name)
	writeJSON(w, http.StatusCreated, &ServerResponse{
		ServerConfig: sc,
		Ownership:    "personal",
	})
}

// getServer returns details for a specific server (personal or shared).
func (h *UserHandlers) getServer(w http.ResponseWriter, r *http.Request) {
	userID, err := getUserID(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "Authentication required")
		return
	}

	name := chi.URLParam(r, "name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "Server name is required")
		return
	}

	// Check personal servers first.
	personal, err := h.userStore.GetUserServer(userID, name)
	if err != nil {
		h.logger.Errorw("failed to get user server", "user_id", userID, "name", name, "error", err)
		writeError(w, http.StatusInternalServerError, "Failed to get server")
		return
	}
	if personal != nil {
		writeJSON(w, http.StatusOK, &ServerResponse{
			ServerConfig: personal,
			Ownership:    "personal",
		})
		return
	}

	// Check shared servers.
	for _, shared := range h.sharedServers {
		if strings.EqualFold(shared.Name, name) {
			writeJSON(w, http.StatusOK, &ServerResponse{
				ServerConfig: shared,
				Ownership:    "shared",
			})
			return
		}
	}

	writeError(w, http.StatusNotFound, fmt.Sprintf("Server %q not found", name))
}

// updateServer updates a personal server configuration.
func (h *UserHandlers) updateServer(w http.ResponseWriter, r *http.Request) {
	userID, err := getUserID(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "Authentication required")
		return
	}

	name := chi.URLParam(r, "name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "Server name is required")
		return
	}

	// Reject updates to shared servers.
	for _, shared := range h.sharedServers {
		if strings.EqualFold(shared.Name, name) {
			writeError(w, http.StatusForbidden, "Cannot update a shared server")
			return
		}
	}

	// Get existing personal server.
	existing, err := h.userStore.GetUserServer(userID, name)
	if err != nil {
		h.logger.Errorw("failed to get user server for update", "user_id", userID, "name", name, "error", err)
		writeError(w, http.StatusInternalServerError, "Failed to get server")
		return
	}
	if existing == nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("Server %q not found", name))
		return
	}

	var req UpdateServerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	// Apply updates to existing server config.
	if req.URL != "" {
		existing.URL = req.URL
	}
	if req.Protocol != "" {
		existing.Protocol = req.Protocol
	}
	if req.Command != "" {
		existing.Command = req.Command
	}
	if req.Args != nil {
		existing.Args = req.Args
	}
	if req.Headers != nil {
		existing.Headers = req.Headers
	}
	if req.Enabled != nil {
		existing.Enabled = *req.Enabled
	}
	existing.Updated = time.Now().UTC()

	if err := h.userStore.UpdateUserServer(userID, existing); err != nil {
		h.logger.Errorw("failed to update user server", "user_id", userID, "name", name, "error", err)
		writeError(w, http.StatusInternalServerError, "Failed to update server")
		return
	}

	h.logger.Infow("user server updated", "user_id", userID, "name", name)
	writeJSON(w, http.StatusOK, &ServerResponse{
		ServerConfig: existing,
		Ownership:    "personal",
	})
}

// deleteServer removes a personal server. Shared servers cannot be deleted.
func (h *UserHandlers) deleteServer(w http.ResponseWriter, r *http.Request) {
	userID, err := getUserID(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "Authentication required")
		return
	}

	name := chi.URLParam(r, "name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "Server name is required")
		return
	}

	// Reject deletion of shared servers.
	for _, shared := range h.sharedServers {
		if strings.EqualFold(shared.Name, name) {
			writeError(w, http.StatusForbidden, "Cannot delete a shared server")
			return
		}
	}

	// Verify the personal server exists before deleting.
	existing, err := h.userStore.GetUserServer(userID, name)
	if err != nil {
		h.logger.Errorw("failed to get user server for delete", "user_id", userID, "name", name, "error", err)
		writeError(w, http.StatusInternalServerError, "Failed to get server")
		return
	}
	if existing == nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("Server %q not found", name))
		return
	}

	if err := h.userStore.DeleteUserServer(userID, name); err != nil {
		h.logger.Errorw("failed to delete user server", "user_id", userID, "name", name, "error", err)
		writeError(w, http.StatusInternalServerError, "Failed to delete server")
		return
	}

	h.logger.Infow("user server deleted", "user_id", userID, "name", name)
	writeJSON(w, http.StatusOK, map[string]string{"message": fmt.Sprintf("Server %q deleted", name)})
}

// enableServer enables or disables a personal server.
func (h *UserHandlers) enableServer(w http.ResponseWriter, r *http.Request) {
	userID, err := getUserID(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "Authentication required")
		return
	}

	name := chi.URLParam(r, "name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "Server name is required")
		return
	}

	// Reject enable/disable of shared servers.
	for _, shared := range h.sharedServers {
		if strings.EqualFold(shared.Name, name) {
			writeError(w, http.StatusForbidden, "Cannot modify a shared server")
			return
		}
	}

	var req EnableServerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	existing, err := h.userStore.GetUserServer(userID, name)
	if err != nil {
		h.logger.Errorw("failed to get user server for enable", "user_id", userID, "name", name, "error", err)
		writeError(w, http.StatusInternalServerError, "Failed to get server")
		return
	}
	if existing == nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("Server %q not found", name))
		return
	}

	existing.Enabled = req.Enabled
	existing.Updated = time.Now().UTC()

	if err := h.userStore.UpdateUserServer(userID, existing); err != nil {
		h.logger.Errorw("failed to enable/disable user server", "user_id", userID, "name", name, "error", err)
		writeError(w, http.StatusInternalServerError, "Failed to update server")
		return
	}

	h.logger.Infow("user server enable toggled", "user_id", userID, "name", name, "enabled", req.Enabled)
	writeJSON(w, http.StatusOK, &ServerResponse{
		ServerConfig: existing,
		Ownership:    "personal",
	})
}

// --- Helpers ---

// getUserID extracts the authenticated user's ID from the request context.
func getUserID(r *http.Request) (string, error) {
	authCtx := auth.AuthContextFromContext(r.Context())
	if authCtx == nil || authCtx.GetUserID() == "" {
		return "", fmt.Errorf("not authenticated")
	}
	return authCtx.GetUserID(), nil
}

// writeJSON writes a JSON response with the given status code.
func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		// Best effort; headers are already sent.
		_ = err
	}
}

// writeError writes a JSON error response.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]interface{}{
		"error":       http.StatusText(status),
		"message":     msg,
		"status_code": status,
	})
}
