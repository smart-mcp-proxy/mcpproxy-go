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
	tokenStore    tokenStore
	hmacKey       []byte
}

// tokenStore defines the interface for agent token storage operations.
// Implemented by *storage.Manager.
type tokenStore interface {
	CreateAgentToken(token auth.AgentToken, rawToken string, hmacKey []byte) error
	ListAgentTokens() ([]auth.AgentToken, error)
	GetAgentTokenByName(name string) (*auth.AgentToken, error)
	RevokeAgentToken(name string) error
	RegenerateAgentToken(name string, newRawToken string, hmacKey []byte) (*auth.AgentToken, error)
}

// NewUserHandlers creates a new UserHandlers instance.
func NewUserHandlers(userStore *users.UserStore, sharedServers []*config.ServerConfig, tokenStore tokenStore, hmacKey []byte, logger *zap.SugaredLogger) *UserHandlers {
	return &UserHandlers{
		userStore:     userStore,
		logger:        logger,
		sharedServers: sharedServers,
		tokenStore:    tokenStore,
		hmacKey:       hmacKey,
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
	r.Route("/user/tokens", func(r chi.Router) {
		r.Get("/", h.listUserTokens)
		r.Post("/", h.createUserToken)
		r.Delete("/{name}", h.revokeUserToken)
		r.Post("/{name}/regenerate", h.regenerateUserToken)
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
	r.Get(prefix+"/user/tokens", h.listUserTokens)
	r.Post(prefix+"/user/tokens", h.createUserToken)
	r.Delete(prefix+"/user/tokens/{name}", h.revokeUserToken)
	r.Post(prefix+"/user/tokens/{name}/regenerate", h.regenerateUserToken)
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
	Ownership   string `json:"ownership"`              // "personal" or "shared"
	UserEnabled *bool  `json:"user_enabled,omitempty"` // Per-user preference for shared servers (nil = no preference, defaults to enabled)
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

	// Load user's shared server preferences
	sharedPrefs, err := h.userStore.GetSharedServerPrefs(userID)
	if err != nil {
		h.logger.Errorw("failed to load shared server prefs", "user_id", userID, "error", err)
		// Non-fatal: proceed without preferences
		sharedPrefs = make(map[string]*users.SharedServerPref)
	}

	shared := make([]*ServerResponse, 0)
	for _, sc := range h.sharedServers {
		if sc.Shared {
			resp := &ServerResponse{
				ServerConfig: sc,
				Ownership:    "shared",
			}
			// Apply user preference if set
			if pref, ok := sharedPrefs[sc.Name]; ok {
				resp.UserEnabled = &pref.Enabled
			}
			shared = append(shared, resp)
		}
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
		if shared.Shared && strings.EqualFold(shared.Name, req.Name) {
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
		if shared.Shared && strings.EqualFold(shared.Name, name) {
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
		if shared.Shared && strings.EqualFold(shared.Name, name) {
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
		if shared.Shared && strings.EqualFold(shared.Name, name) {
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

// enableServer enables or disables a personal or shared server.
// For shared servers, a per-user preference is stored (does not modify the shared config).
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

	var req EnableServerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	// Check if this is a shared server
	for _, shared := range h.sharedServers {
		if shared.Shared && strings.EqualFold(shared.Name, name) {
			// Store per-user preference for the shared server
			if err := h.userStore.SetSharedServerPref(userID, shared.Name, req.Enabled); err != nil {
				h.logger.Errorw("failed to set shared server pref", "user_id", userID, "name", name, "error", err)
				writeError(w, http.StatusInternalServerError, "Failed to update preference")
				return
			}

			h.logger.Infow("shared server user preference set", "user_id", userID, "name", name, "enabled", req.Enabled)
			writeJSON(w, http.StatusOK, &ServerResponse{
				ServerConfig: shared,
				Ownership:    "shared",
				UserEnabled:  &req.Enabled,
			})
			return
		}
	}

	// Personal server: update directly
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

// --- Token request/response types ---

// CreateTokenRequest represents the request body for creating a user token.
type CreateTokenRequest struct {
	Name           string   `json:"name"`
	AllowedServers []string `json:"allowed_servers,omitempty"`
	Permissions    []string `json:"permissions"`
	ExpiresIn      string   `json:"expires_in,omitempty"` // Duration string, e.g. "720h" for 30 days
}

// AgentTokenResponse represents a token in API responses.
type AgentTokenResponse struct {
	Name           string     `json:"name"`
	TokenPrefix    string     `json:"token_prefix"`
	AllowedServers []string   `json:"allowed_servers"`
	Permissions    []string   `json:"permissions"`
	ExpiresAt      time.Time  `json:"expires_at"`
	CreatedAt      time.Time  `json:"created_at"`
	LastUsedAt     *time.Time `json:"last_used_at,omitempty"`
	Revoked        bool       `json:"revoked"`
	RawToken       string     `json:"token,omitempty"` // Only returned on create/regenerate
}

// --- Token handlers ---

// listUserTokens returns all agent tokens owned by the authenticated user.
func (h *UserHandlers) listUserTokens(w http.ResponseWriter, r *http.Request) {
	userID, err := getUserID(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "Authentication required")
		return
	}

	if h.tokenStore == nil {
		writeJSON(w, http.StatusOK, []AgentTokenResponse{})
		return
	}

	allTokens, err := h.tokenStore.ListAgentTokens()
	if err != nil {
		h.logger.Errorw("failed to list agent tokens", "user_id", userID, "error", err)
		writeError(w, http.StatusInternalServerError, "Failed to list tokens")
		return
	}

	// Filter to only tokens owned by this user.
	var userTokens []AgentTokenResponse
	for _, t := range allTokens {
		if t.UserID != userID {
			continue
		}
		userTokens = append(userTokens, AgentTokenResponse{
			Name:           t.Name,
			TokenPrefix:    t.TokenPrefix,
			AllowedServers: t.AllowedServers,
			Permissions:    t.Permissions,
			ExpiresAt:      t.ExpiresAt,
			CreatedAt:      t.CreatedAt,
			LastUsedAt:     t.LastUsedAt,
			Revoked:        t.Revoked,
		})
	}

	if userTokens == nil {
		userTokens = []AgentTokenResponse{}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"tokens": userTokens})
}

// createUserToken creates a new agent token owned by the authenticated user.
func (h *UserHandlers) createUserToken(w http.ResponseWriter, r *http.Request) {
	userID, err := getUserID(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "Authentication required")
		return
	}

	if h.tokenStore == nil {
		writeError(w, http.StatusInternalServerError, "Token store not available")
		return
	}

	var req CreateTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "Token name is required")
		return
	}

	if err := auth.ValidatePermissions(req.Permissions); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("Invalid permissions: %v", err))
		return
	}

	// Parse expiry duration (default 30 days).
	var expiresAt time.Time
	if req.ExpiresIn != "" {
		duration, err := time.ParseDuration(req.ExpiresIn)
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("Invalid expires_in duration: %v", err))
			return
		}
		expiresAt = time.Now().UTC().Add(duration)
	} else {
		expiresAt = time.Now().UTC().Add(30 * 24 * time.Hour) // 30 days default
	}

	rawToken, err := auth.GenerateToken()
	if err != nil {
		h.logger.Errorw("failed to generate token", "user_id", userID, "error", err)
		writeError(w, http.StatusInternalServerError, "Failed to generate token")
		return
	}

	token := auth.AgentToken{
		Name:           req.Name,
		AllowedServers: req.AllowedServers,
		Permissions:    req.Permissions,
		ExpiresAt:      expiresAt,
		CreatedAt:      time.Now().UTC(),
		UserID:         userID,
	}

	if err := h.tokenStore.CreateAgentToken(token, rawToken, h.hmacKey); err != nil {
		h.logger.Errorw("failed to create agent token", "user_id", userID, "name", req.Name, "error", err)
		writeError(w, http.StatusConflict, fmt.Sprintf("Failed to create token: %v", err))
		return
	}

	h.logger.Infow("user token created", "user_id", userID, "name", req.Name)
	writeJSON(w, http.StatusCreated, AgentTokenResponse{
		Name:           token.Name,
		TokenPrefix:    auth.TokenPrefix(rawToken),
		AllowedServers: token.AllowedServers,
		Permissions:    token.Permissions,
		ExpiresAt:      token.ExpiresAt,
		CreatedAt:      token.CreatedAt,
		RawToken:       rawToken,
	})
}

// revokeUserToken revokes an agent token owned by the authenticated user.
func (h *UserHandlers) revokeUserToken(w http.ResponseWriter, r *http.Request) {
	userID, err := getUserID(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "Authentication required")
		return
	}

	if h.tokenStore == nil {
		writeError(w, http.StatusInternalServerError, "Token store not available")
		return
	}

	name := chi.URLParam(r, "name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "Token name is required")
		return
	}

	// Verify the token belongs to this user.
	existing, err := h.tokenStore.GetAgentTokenByName(name)
	if err != nil {
		h.logger.Errorw("failed to get token for revoke", "user_id", userID, "name", name, "error", err)
		writeError(w, http.StatusInternalServerError, "Failed to get token")
		return
	}
	if existing == nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("Token %q not found", name))
		return
	}
	if existing.UserID != userID {
		writeError(w, http.StatusForbidden, "Cannot revoke another user's token")
		return
	}

	if err := h.tokenStore.RevokeAgentToken(name); err != nil {
		h.logger.Errorw("failed to revoke token", "user_id", userID, "name", name, "error", err)
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to revoke token: %v", err))
		return
	}

	h.logger.Infow("user token revoked", "user_id", userID, "name", name)
	writeJSON(w, http.StatusOK, map[string]string{"message": fmt.Sprintf("Token %q revoked", name)})
}

// regenerateUserToken regenerates an agent token owned by the authenticated user.
func (h *UserHandlers) regenerateUserToken(w http.ResponseWriter, r *http.Request) {
	userID, err := getUserID(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "Authentication required")
		return
	}

	if h.tokenStore == nil {
		writeError(w, http.StatusInternalServerError, "Token store not available")
		return
	}

	name := chi.URLParam(r, "name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "Token name is required")
		return
	}

	// Verify the token belongs to this user.
	existing, err := h.tokenStore.GetAgentTokenByName(name)
	if err != nil {
		h.logger.Errorw("failed to get token for regenerate", "user_id", userID, "name", name, "error", err)
		writeError(w, http.StatusInternalServerError, "Failed to get token")
		return
	}
	if existing == nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("Token %q not found", name))
		return
	}
	if existing.UserID != userID {
		writeError(w, http.StatusForbidden, "Cannot regenerate another user's token")
		return
	}

	newRawToken, err := auth.GenerateToken()
	if err != nil {
		h.logger.Errorw("failed to generate new token", "user_id", userID, "error", err)
		writeError(w, http.StatusInternalServerError, "Failed to generate new token")
		return
	}

	updated, err := h.tokenStore.RegenerateAgentToken(name, newRawToken, h.hmacKey)
	if err != nil {
		h.logger.Errorw("failed to regenerate token", "user_id", userID, "name", name, "error", err)
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to regenerate token: %v", err))
		return
	}

	h.logger.Infow("user token regenerated", "user_id", userID, "name", name)
	writeJSON(w, http.StatusOK, AgentTokenResponse{
		Name:           updated.Name,
		TokenPrefix:    updated.TokenPrefix,
		AllowedServers: updated.AllowedServers,
		Permissions:    updated.Permissions,
		ExpiresAt:      updated.ExpiresAt,
		CreatedAt:      updated.CreatedAt,
		RawToken:       newRawToken,
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
