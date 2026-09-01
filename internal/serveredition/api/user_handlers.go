//go:build server

package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/serveredition/users"
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
	DeleteAgentToken(name string) error
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
		r.Delete("/{name}/permanent", h.deleteUserToken)
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
	r.Delete(prefix+"/user/tokens/{name}/permanent", h.deleteUserToken)
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
//
// NOTE (issue #937 fallout): config.ServerConfig carries custom
// MarshalJSON/UnmarshalJSON methods, and Go PROMOTES those to any struct that
// embeds it. Without the explicit methods below, encoding/json saw
// ServerResponse as a json.Marshaler/Unmarshaler and delegated the whole value
// to the embedded config — silently dropping `ownership` and `user_enabled`
// from every response, and failing every decode with
// "json: Unmarshal(nil *config.Alias)" because the embedded pointer is nil
// before decoding starts. Keep the methods in sync with the fields here;
// TestServerResponse_JSONRoundTrip pins the behaviour.
type ServerResponse struct {
	*config.ServerConfig
	Ownership   string `json:"ownership"`              // "personal" or "shared"
	UserEnabled *bool  `json:"user_enabled,omitempty"` // Per-user preference for shared servers (nil = no preference, defaults to enabled)
}

// serverResponseWrapperFields holds only the fields ServerResponse adds on top
// of the embedded config, so both JSON methods share one definition.
type serverResponseWrapperFields struct {
	Ownership   string `json:"ownership"`
	UserEnabled *bool  `json:"user_enabled,omitempty"`
}

// MarshalJSON flattens the embedded *config.ServerConfig and the wrapper fields
// into a single JSON object.
//
// The embedded config is marshaled through its OWN MarshalJSON so the #937
// "quarantined" presence semantics (omit an unstated false, always write true)
// are preserved verbatim; the wrapper fields are then spliced on top.
func (r ServerResponse) MarshalJSON() ([]byte, error) {
	fields := map[string]json.RawMessage{}

	if r.ServerConfig != nil {
		raw, err := json.Marshal(r.ServerConfig)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &fields); err != nil {
			return nil, err
		}
	}

	wrapper, err := json.Marshal(serverResponseWrapperFields{
		Ownership:   r.Ownership,
		UserEnabled: r.UserEnabled,
	})
	if err != nil {
		return nil, err
	}
	var wrapperFields map[string]json.RawMessage
	if err := json.Unmarshal(wrapper, &wrapperFields); err != nil {
		return nil, err
	}
	// Wrapper fields win: they are what the endpoint promises.
	for k, v := range wrapperFields {
		fields[k] = v
	}

	return json.Marshal(fields)
}

// UnmarshalJSON decodes a flattened ServerResponse, allocating the embedded
// config first so the config's own UnmarshalJSON (and its "quarantined"
// presence detection) runs against a non-nil target.
func (r *ServerResponse) UnmarshalJSON(data []byte) error {
	if string(bytes.TrimSpace(data)) == "null" {
		return nil
	}

	sc := &config.ServerConfig{}
	if err := json.Unmarshal(data, sc); err != nil {
		return err
	}

	var wrapper serverResponseWrapperFields
	if err := json.Unmarshal(data, &wrapper); err != nil {
		return err
	}

	r.ServerConfig = sc
	r.Ownership = wrapper.Ownership
	r.UserEnabled = wrapper.UserEnabled
	return nil
}

// ServerListResponse contains personal and shared servers for a user.
type ServerListResponse struct {
	Personal []*ServerResponse `json:"personal"`
	Shared   []*ServerResponse `json:"shared"`
}

// sharedServerResponse renders one ADMIN-CONFIGURED shared server for an
// ordinary user, with every secret-bearing field masked.
//
// Issue #1148, applied to the server edition's per-user door. ServerResponse
// EMBEDS the raw *config.ServerConfig, and for a shared server that config is
// the ADMIN's: its `headers` (Authorization, X-API-Key), `env`, URL query
// credentials, `oauth.client_secret` and `auth_broker.client_secret` were
// handed to every authenticated user of the deployment in the clear, on
// listServers, getServer and enableServer alike. This is the same defect class
// the MCP, REST and SSE doors closed; this door that sweep did not reach.
//
// The rules are the SAME ones every other door applies, reached through
// oauth.RedactServerConfigSecrets, which walks the struct rather than
// enumerating it, so a field added to config.ServerConfig (or to the
// build-tagged auth_broker block) is masked because the walk reaches it.
//
// The POLICY is oauth.AuditRedaction, not the LiveRedaction an owner-facing
// surface uses. MaskValue's `••••<last2> (<N> chars)` rendering is an
// affordance for someone editing their OWN credential; here the reader is a
// different tenant who cannot edit this server at all, so the affordance buys
// them nothing while publishing the admin credential's exact length and
// trailing bytes to every user of the deployment — a durable fingerprint, a
// correlation handle across tenants, and a materially smaller search space for
// a low-entropy secret.
//
// It returns a masked COPY: `h.sharedServers` is the LIVE admin configuration,
// and writing a mask through it would be the #1142/#1146 read-modify-write
// corruption with every user of the deployment as the blast radius.
//
// Masking is safe here — and ONLY here — because shared servers are READ-ONLY
// to users: updateServer and deleteServer both answer 403, and enableServer
// stores a per-user preference without touching the shared config. So there is
// no write path that could persist an echoed mask over the real credential,
// which is the hazard every other door on this issue had to guard against.
// PERSONAL servers are deliberately NOT masked: they are the caller's own
// credentials (no cross-tenant disclosure to close), and updateServer replaces
// URL, Args and Headers WHOLESALE from the request body, so masking them
// without a key-bound unmask mirror (oauth.UnmaskLiveHeaders / UnmaskLiveURL +
// oauth.CheckArgvMaskEcho) would let a read-modify-write client persist the
// masks over the user's real secrets.
func sharedServerResponse(sc *config.ServerConfig, userEnabled *bool) *ServerResponse {
	return &ServerResponse{
		ServerConfig: oauth.RedactServerConfigSecrets(sc, oauth.AuditRedaction),
		Ownership:    "shared",
		UserEnabled:  userEnabled,
	}
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
			var userEnabled *bool
			// Apply user preference if set
			if pref, ok := sharedPrefs[sc.Name]; ok {
				userEnabled = &pref.Enabled
			}
			shared = append(shared, sharedServerResponse(sc, userEnabled))
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
			// The caller's own preference belongs on the DETAIL read too.
			// Rendering it as unset made this route disagree with listServers
			// (which threads it from GetSharedServerPrefs) and with the 200 body
			// of .../enable: a user who had disabled a shared server saw only
			// the admin's `enabled` and no `user_enabled` at all.
			//
			// Keyed on shared.Name — the canonical name the preference is
			// stored under — rather than on the case-insensitively matched URL
			// param, matching how listServers and enableServer key it.
			var userEnabled *bool
			if pref, perr := h.userStore.GetSharedServerPref(userID, shared.Name); perr != nil {
				// Non-fatal: the server itself is still worth returning.
				h.logger.Errorw("failed to load shared server pref", "user_id", userID, "name", shared.Name, "error", perr)
			} else if pref != nil {
				userEnabled = &pref.Enabled
			}
			writeJSON(w, http.StatusOK, sharedServerResponse(shared, userEnabled))
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
			writeJSON(w, http.StatusOK, sharedServerResponse(shared, &req.Enabled))
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

// deleteUserToken permanently removes an agent token owned by the authenticated
// user, freeing its name for reuse (unlike revoke, which is a soft delete).
func (h *UserHandlers) deleteUserToken(w http.ResponseWriter, r *http.Request) {
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
		h.logger.Errorw("failed to get token for delete", "user_id", userID, "name", name, "error", err)
		writeError(w, http.StatusInternalServerError, "Failed to get token")
		return
	}
	if existing == nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("Token %q not found", name))
		return
	}
	if existing.UserID != userID {
		writeError(w, http.StatusForbidden, "Cannot delete another user's token")
		return
	}

	if err := h.tokenStore.DeleteAgentToken(name); err != nil {
		h.logger.Errorw("failed to delete token", "user_id", userID, "name", name, "error", err)
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to delete token: %v", err))
		return
	}

	h.logger.Infow("user token deleted", "user_id", userID, "name", name)
	writeJSON(w, http.StatusOK, map[string]string{"message": fmt.Sprintf("Token %q deleted", name)})
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
