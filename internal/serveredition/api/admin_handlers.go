//go:build server

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"
	teamsauth "github.com/smart-mcp-proxy/mcpproxy-go/internal/serveredition/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/serveredition/multiuser"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/serveredition/users"
)

// adminTokenRevoker revokes every agent token an owner holds. Implemented by
// *storage.Manager; optional, so the handlers work in tests and in deployments
// with no token storage wired.
type adminTokenRevoker interface {
	RevokeAgentTokensForOwner(userID string) (int, error)
}

// AdminHandlers provides admin-only REST endpoints.
type AdminHandlers struct {
	userStore      *users.UserStore
	activityFilter *multiuser.ActivityFilter
	sessionManager *teamsauth.SessionManager
	adminEmails    []string
	sharedServers  []*config.ServerConfig
	config         *config.Config
	configPath     string
	managementSvc  interface{} // management.Service - kept as interface{} to avoid circular imports
	tokenRevoker   adminTokenRevoker
	logger         *zap.SugaredLogger
}

// SetTokenRevoker wires the agent-token store used when disabling a user.
// Optional: with no revoker, disabling still disables the account and the
// storage-level owner gate still refuses that user's tokens; only the permanent
// burn of already-minted credentials is skipped. Passing nil clears it.
func (h *AdminHandlers) SetTokenRevoker(revoker adminTokenRevoker) {
	h.tokenRevoker = revoker
}

// NewAdminHandlers creates a new AdminHandlers instance.
func NewAdminHandlers(
	userStore *users.UserStore,
	activityFilter *multiuser.ActivityFilter,
	sessionManager *teamsauth.SessionManager,
	adminEmails []string,
	sharedServers []*config.ServerConfig,
	cfg *config.Config,
	configPath string,
	managementSvc interface{},
	logger *zap.SugaredLogger,
) *AdminHandlers {
	return &AdminHandlers{
		userStore:      userStore,
		activityFilter: activityFilter,
		sessionManager: sessionManager,
		adminEmails:    adminEmails,
		sharedServers:  sharedServers,
		config:         cfg,
		configPath:     configPath,
		managementSvc:  managementSvc,
		logger:         logger,
	}
}

// RegisterRoutes registers admin routes on the provided router.
//
// NOTE: enforcement lives in each handler's own requireAdmin call, NOT in
// middleware. Production (internal/serveredition/setup.go) applies only
// authMiddleware.Middleware() to this group; ServerEditionAuthMiddleware's
// AdminOnly() is not used there. Every route added here MUST call requireAdmin
// as its first statement, or it ships unguarded.
func (h *AdminHandlers) RegisterRoutes(r chi.Router) {
	r.Get("/admin/users", h.listUsers)
	r.Post("/admin/users/{id}/disable", h.disableUser)
	r.Post("/admin/users/{id}/enable", h.enableUser)
	r.Get("/admin/activity", h.getActivity)
	r.Get("/admin/sessions", h.listSessions)
	r.Get("/admin/dashboard", h.getDashboard)
	r.Get("/admin/servers", h.listAdminServers)
	r.Post("/admin/servers/{name}/shared", h.toggleSharedServer)
	r.Post("/admin/servers/{name}/enable", h.enableAdminServer)
	r.Post("/admin/servers/{name}/disable", h.disableAdminServer)
	r.Post("/admin/servers/{name}/restart", h.restartAdminServer)
}

// RegisterRoutesWithPrefix registers admin routes with a path prefix.
func (h *AdminHandlers) RegisterRoutesWithPrefix(r chi.Router, prefix string) {
	r.Get(prefix+"/admin/users", h.listUsers)
	r.Post(prefix+"/admin/users/{id}/disable", h.disableUser)
	r.Post(prefix+"/admin/users/{id}/enable", h.enableUser)
	r.Get(prefix+"/admin/activity", h.getActivity)
	r.Get(prefix+"/admin/sessions", h.listSessions)
	r.Get(prefix+"/admin/dashboard", h.getDashboard)
	r.Get(prefix+"/admin/servers", h.listAdminServers)
	r.Post(prefix+"/admin/servers/{name}/shared", h.toggleSharedServer)
	r.Post(prefix+"/admin/servers/{name}/enable", h.enableAdminServer)
	r.Post(prefix+"/admin/servers/{name}/disable", h.disableAdminServer)
	r.Post(prefix+"/admin/servers/{name}/restart", h.restartAdminServer)
}

// --- Response types ---

// UserProfileResponse represents a user profile in admin responses.
type UserProfileResponse struct {
	ID          string `json:"id"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	Provider    string `json:"provider"`
	LastLoginAt string `json:"last_login_at"`
	Disabled    bool   `json:"disabled"`
}

// SessionResponse represents a session in admin responses.
type SessionResponse struct {
	ID        string `json:"id"`
	UserID    string `json:"user_id"`
	UserEmail string `json:"user_email,omitempty"`
	CreatedAt string `json:"created_at"`
	ExpiresAt string `json:"expires_at"`
	UserAgent string `json:"user_agent,omitempty"`
	IPAddress string `json:"ip_address,omitempty"`
	Expired   bool   `json:"expired"`
}

// --- Handlers ---

// listUsers returns all users.
func (h *AdminHandlers) listUsers(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}

	allUsers, err := h.userStore.ListUsers()
	if err != nil {
		h.logger.Errorw("failed to list users", "error", err)
		writeError(w, http.StatusInternalServerError, "Failed to list users")
		return
	}

	profiles := make([]*UserProfileResponse, 0, len(allUsers))
	for _, u := range allUsers {
		profiles = append(profiles, &UserProfileResponse{
			ID:          u.ID,
			Email:       u.Email,
			DisplayName: u.DisplayName,
			Provider:    u.Provider,
			LastLoginAt: u.LastLoginAt.Format("2006-01-02T15:04:05Z"),
			Disabled:    u.Disabled,
		})
	}

	writeJSON(w, http.StatusOK, profiles)
}

// disableUser disables a user and revokes all their sessions AND agent tokens.
//
// Disabling is the documented remediation for a compromised account, so it has
// to reach every credential that speaks for that account, not just the
// interactive ones. Sessions and JWTs were already covered; agent tokens are
// separate records that nothing here touched, and each one carries the user's
// UserID into every downstream authorisation and activity decision.
//
// Two mechanisms, deliberately:
//
//   - storage.Manager's owner gate (installed in internal/serveredition/setup.go)
//     refuses a disabled owner's tokens on the authentication path, which is what
//     makes the remediation take effect IMMEDIATELY and covers tokens minted in
//     the same instant, without this handler having to win a race;
//   - the revocation below burns them permanently, so RE-ENABLING the account
//     does not hand the attacker their stolen token back. Revoked is a soft
//     delete: the records stay visible to the operator, and the owner mints new
//     tokens once they are re-enabled.
//
// Token revocation is non-fatal on error, exactly like session revocation: the
// account is disabled either way, and the owner gate is the part that must not
// be missed.
func (h *AdminHandlers) disableUser(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}

	userID := chi.URLParam(r, "id")
	if userID == "" {
		writeError(w, http.StatusBadRequest, "User ID is required")
		return
	}

	user, err := h.userStore.GetUser(userID)
	if err != nil {
		h.logger.Errorw("failed to get user for disable", "user_id", userID, "error", err)
		writeError(w, http.StatusInternalServerError, "Failed to get user")
		return
	}
	if user == nil {
		writeError(w, http.StatusNotFound, "User not found")
		return
	}

	user.Disabled = true
	if err := h.userStore.UpdateUser(user); err != nil {
		h.logger.Errorw("failed to disable user", "user_id", userID, "error", err)
		writeError(w, http.StatusInternalServerError, "Failed to disable user")
		return
	}

	// Revoke all sessions for the disabled user.
	if err := h.sessionManager.RevokeUserSessions(userID); err != nil {
		h.logger.Errorw("failed to revoke sessions for disabled user", "user_id", userID, "error", err)
		// Non-fatal: user is disabled even if session revocation fails.
	}

	// Revoke the agent tokens they minted. See this handler's doc comment for
	// why this is done in addition to the authentication-path owner gate.
	if h.tokenRevoker != nil {
		revoked, err := h.tokenRevoker.RevokeAgentTokensForOwner(userID)
		if err != nil {
			h.logger.Errorw("failed to revoke agent tokens for disabled user", "user_id", userID, "error", err)
			// Non-fatal: the owner gate already refuses this user's tokens.
		} else if revoked > 0 {
			h.logger.Infow("revoked agent tokens for disabled user", "user_id", userID, "count", revoked)
		}
	}

	h.logger.Infow("user disabled", "user_id", userID, "email", user.Email)
	writeJSON(w, http.StatusOK, map[string]string{"message": "User disabled"})
}

// enableUser enables a previously disabled user.
//
// Their agent tokens are NOT un-revoked. disableUser burns them precisely so
// that re-enabling an account cannot resurrect a credential that may be why the
// account was disabled; the owner mints new ones.
func (h *AdminHandlers) enableUser(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}

	userID := chi.URLParam(r, "id")
	if userID == "" {
		writeError(w, http.StatusBadRequest, "User ID is required")
		return
	}

	user, err := h.userStore.GetUser(userID)
	if err != nil {
		h.logger.Errorw("failed to get user for enable", "user_id", userID, "error", err)
		writeError(w, http.StatusInternalServerError, "Failed to get user")
		return
	}
	if user == nil {
		writeError(w, http.StatusNotFound, "User not found")
		return
	}

	user.Disabled = false
	if err := h.userStore.UpdateUser(user); err != nil {
		h.logger.Errorw("failed to enable user", "user_id", userID, "error", err)
		writeError(w, http.StatusInternalServerError, "Failed to enable user")
		return
	}

	h.logger.Infow("user enabled", "user_id", userID, "email", user.Email)
	writeJSON(w, http.StatusOK, map[string]string{"message": "User enabled"})
}

// getActivity returns activity for all users, optionally filtered by user_id.
func (h *AdminHandlers) getActivity(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}

	if h.activityFilter == nil {
		writeJSON(w, http.StatusOK, ActivityListResponse{
			Items: []struct{}{},
			Total: 0,
		})
		return
	}

	limit := parseIntParam(r, "limit", 50)
	offset := parseIntParam(r, "offset", 0)
	userIDFilter := r.URL.Query().Get("user_id")

	var (
		records interface{}
		total   int
		err     error
	)

	if userIDFilter != "" {
		records, total, err = h.activityFilter.GetFilteredActivity(r.Context(), userIDFilter, limit, offset)
	} else {
		records, total, err = h.activityFilter.GetUserActivity(r.Context(), limit, offset)
	}

	if err != nil {
		h.logger.Errorw("failed to get admin activity", "error", err)
		writeError(w, http.StatusInternalServerError, "Failed to get activity")
		return
	}

	// NOTE: raw storage records, serialised as-is — safe only while
	// activityFilter is nil (setup.go wires nil today). Whoever wires it must
	// mask first, as the personal-edition activity API does
	// (httpapi.maskActivityPayloads), or flagged credentials are served in
	// cleartext (audit F13).
	writeJSON(w, http.StatusOK, ActivityListResponse{
		Items: records,
		Total: total,
	})
}

// listSessions returns all active sessions with user info.
func (h *AdminHandlers) listSessions(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}

	allSessions, err := h.userStore.ListSessions()
	if err != nil {
		h.logger.Errorw("failed to list sessions", "error", err)
		writeError(w, http.StatusInternalServerError, "Failed to list sessions")
		return
	}

	// Build a user lookup cache for enriching session responses.
	userCache := make(map[string]*users.User)
	responses := make([]*SessionResponse, 0, len(allSessions))

	for _, s := range allSessions {
		resp := &SessionResponse{
			ID:        s.ID,
			UserID:    s.UserID,
			CreatedAt: s.CreatedAt.Format("2006-01-02T15:04:05Z"),
			ExpiresAt: s.ExpiresAt.Format("2006-01-02T15:04:05Z"),
			UserAgent: s.UserAgent,
			IPAddress: s.IPAddress,
			Expired:   s.IsExpired(),
		}

		// Look up user email.
		if _, ok := userCache[s.UserID]; !ok {
			user, err := h.userStore.GetUser(s.UserID)
			if err == nil && user != nil {
				userCache[s.UserID] = user
			}
		}
		if user, ok := userCache[s.UserID]; ok {
			resp.UserEmail = user.Email
		}

		responses = append(responses, resp)
	}

	writeJSON(w, http.StatusOK, responses)
}

// DashboardResponse contains admin dashboard data.
type DashboardResponse struct {
	TotalUsers     int                  `json:"total_users"`
	ActiveUsers    int                  `json:"active_users"`
	ActiveSessions int                  `json:"active_sessions"`
	TotalServers   int                  `json:"total_servers"`
	HealthyServers int                  `json:"healthy_servers"`
	ToolCalls24h   int                  `json:"tool_calls_24h"`
	ErrorRate24h   float64              `json:"error_rate_24h"`
	RecentUsers    []*DashboardUser     `json:"recent_users"`
	RecentActivity []*DashboardActivity `json:"recent_activity"`
}

// DashboardUser is a user summary for the dashboard.
type DashboardUser struct {
	ID          string `json:"id"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	Role        string `json:"role"`
	LastLoginAt string `json:"last_login_at"`
}

// DashboardActivity is an activity summary for the dashboard.
type DashboardActivity struct {
	ID         string `json:"id"`
	Type       string `json:"type"`
	ToolName   string `json:"tool_name,omitempty"`
	ServerName string `json:"server_name,omitempty"`
	Status     string `json:"status"`
	Timestamp  string `json:"timestamp"`
	UserEmail  string `json:"user_email,omitempty"`
}

// getDashboard returns admin dashboard overview data.
func (h *AdminHandlers) getDashboard(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}

	resp := DashboardResponse{
		RecentUsers:    make([]*DashboardUser, 0),
		RecentActivity: make([]*DashboardActivity, 0),
	}

	// Servers
	resp.TotalServers = len(h.sharedServers)
	for _, sc := range h.sharedServers {
		if sc.Enabled {
			resp.HealthyServers++
		}
	}

	// Users
	allUsers, err := h.userStore.ListUsers()
	if err != nil {
		h.logger.Errorw("dashboard: failed to list users", "error", err)
	} else {
		resp.TotalUsers = len(allUsers)
		now := time.Now()
		for _, u := range allUsers {
			if !u.Disabled {
				resp.ActiveUsers++
			}
		}
		// Recent users (last 5 by login time)
		for i, u := range allUsers {
			if i >= 5 {
				break
			}
			role := "user"
			if u.Email != "" {
				for _, admin := range h.adminEmails {
					if admin == u.Email {
						role = "admin"
						break
					}
				}
			}
			_ = now // used above
			resp.RecentUsers = append(resp.RecentUsers, &DashboardUser{
				ID:          u.ID,
				Email:       u.Email,
				DisplayName: u.DisplayName,
				Role:        role,
				LastLoginAt: u.LastLoginAt.Format(time.RFC3339),
			})
		}
	}

	// Sessions
	allSessions, err := h.userStore.ListSessions()
	if err != nil {
		h.logger.Errorw("dashboard: failed to list sessions", "error", err)
	} else {
		for _, s := range allSessions {
			if !s.IsExpired() {
				resp.ActiveSessions++
			}
		}
	}

	// Activity (last 5 entries)
	if h.activityFilter != nil {
		records, _, err := h.activityFilter.GetUserActivity(r.Context(), 5, 0)
		if err != nil {
			h.logger.Errorw("dashboard: failed to get activity", "error", err)
		} else {
			for _, rec := range records {
				resp.RecentActivity = append(resp.RecentActivity, &DashboardActivity{
					ID:         rec.ID,
					Type:       string(rec.Type),
					ToolName:   rec.ToolName,
					ServerName: rec.ServerName,
					Status:     string(rec.Status),
					Timestamp:  rec.Timestamp.Format(time.RFC3339),
					UserEmail:  rec.UserEmail,
				})
			}
		}
	}

	writeJSON(w, http.StatusOK, resp)
}

// ToggleSharedRequest represents the request body for toggling shared status.
type ToggleSharedRequest struct {
	Shared bool `json:"shared"`
}

// toggleSharedServer toggles a server's shared status.
func (h *AdminHandlers) toggleSharedServer(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}

	name := chi.URLParam(r, "name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "Server name is required")
		return
	}

	var req ToggleSharedRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	// Find the server in the config.
	var found *config.ServerConfig
	for _, sc := range h.config.Servers {
		if strings.EqualFold(sc.Name, name) {
			found = sc
			break
		}
	}
	if found == nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("Server %q not found", name))
		return
	}

	found.Shared = req.Shared
	found.Updated = time.Now().UTC()

	// Save config.
	if err := config.SaveConfig(h.config, h.configPath); err != nil {
		h.logger.Errorw("failed to save config after toggling shared", "server", name, "error", err)
		writeError(w, http.StatusInternalServerError, "Failed to save configuration")
		return
	}

	h.logger.Infow("server shared status toggled", "server", name, "shared", req.Shared)
	// Issue #1148, round 8: `found` is a raw *config.ServerConfig — every env
	// value, header, oauth.client_secret and URL credential the operator
	// configured. Echoing it back handed the whole upstream credential set to
	// any admin session over the server edition's REST API. Build the echo from
	// the shared redacted view instead, exactly as every personal-edition door
	// does.
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"message": fmt.Sprintf("Server %q shared status set to %v", name, req.Shared),
		"server":  oauth.RedactedConfigView("", found),
	})
}

// listAdminServers returns all configured servers for admin management.
// When the management service is available, it returns live connection status and stats.
func (h *AdminHandlers) listAdminServers(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}

	type lister interface {
		ListServers(ctx context.Context) ([]*contracts.Server, *contracts.ServerStats, error)
	}
	mgmt, ok := h.managementSvc.(lister)
	if !ok {
		// Issue #1148, round 8: this fallback wrote the raw
		// []*config.ServerConfig straight to JSON — the exact shape
		// `quarantine_security list_quarantined` was fixed for in round 1,
		// still open on the server edition's admin API. One shared view.
		writeJSON(w, http.StatusOK, oauth.RedactedConfigViews("", h.config.Servers))
		return
	}

	servers, stats, err := mgmt.ListServers(r.Context())
	if err != nil {
		h.logger.Errorw("failed to list servers via management service", "error", err)
		// Fallback to config-only listing on error.
		writeJSON(w, http.StatusOK, oauth.RedactedConfigViews("", h.config.Servers))
		return
	}

	// Build a lookup for the shared flag from config (contracts.Server doesn't have it).
	sharedMap := make(map[string]bool, len(h.config.Servers))
	for _, sc := range h.config.Servers {
		sharedMap[sc.Name] = sc.Shared
	}

	// Enrich the response with the shared flag.
	type enrichedServer struct {
		*contracts.Server
		Shared bool `json:"shared"`
	}
	// Issue #1148, round 8: management.ListServers hands back RAW
	// contracts.Server values — the personal edition redacts them at its own
	// serialization boundary (httpapi.redactServerSecrets), and this door,
	// which is the server edition's boundary onto the same structs, did not.
	// The copy matters: the projection may still share maps/slices with the
	// stored config, and RedactServerSecretFields replaces rather than mutates
	// them.
	enriched := make([]*enrichedServer, len(servers))
	for i, s := range servers {
		redacted := *s
		oauth.RedactServerSecretFields(&redacted)
		enriched[i] = &enrichedServer{Server: &redacted, Shared: sharedMap[s.Name]}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"servers": enriched,
		"stats":   stats,
	})
}

// enableAdminServer enables an upstream server via the management service.
func (h *AdminHandlers) enableAdminServer(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}

	name := chi.URLParam(r, "name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "Server name is required")
		return
	}

	type enabler interface {
		EnableServer(ctx context.Context, name string, enabled bool) error
	}
	mgmt, ok := h.managementSvc.(enabler)
	if !ok {
		writeError(w, http.StatusInternalServerError, "Management service not available")
		return
	}

	if err := mgmt.EnableServer(r.Context(), name, true); err != nil {
		h.logger.Errorw("failed to enable server", "server", name, "error", err)
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to enable server: %v", err))
		return
	}

	h.logger.Infow("admin enabled server", "server", name)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"server": name, "action": "enable", "success": true,
	})
}

// disableAdminServer disables an upstream server via the management service.
func (h *AdminHandlers) disableAdminServer(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}

	name := chi.URLParam(r, "name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "Server name is required")
		return
	}

	type enabler interface {
		EnableServer(ctx context.Context, name string, enabled bool) error
	}
	mgmt, ok := h.managementSvc.(enabler)
	if !ok {
		writeError(w, http.StatusInternalServerError, "Management service not available")
		return
	}

	if err := mgmt.EnableServer(r.Context(), name, false); err != nil {
		h.logger.Errorw("failed to disable server", "server", name, "error", err)
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to disable server: %v", err))
		return
	}

	h.logger.Infow("admin disabled server", "server", name)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"server": name, "action": "disable", "success": true,
	})
}

// restartAdminServer restarts an upstream server via the management service.
func (h *AdminHandlers) restartAdminServer(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}

	name := chi.URLParam(r, "name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "Server name is required")
		return
	}

	type restarter interface {
		RestartServer(ctx context.Context, name string) error
	}
	mgmt, ok := h.managementSvc.(restarter)
	if !ok {
		writeError(w, http.StatusInternalServerError, "Management service not available")
		return
	}

	if err := mgmt.RestartServer(r.Context(), name); err != nil {
		h.logger.Errorw("failed to restart server", "server", name, "error", err)
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to restart server: %v", err))
		return
	}

	h.logger.Infow("admin restarted server", "server", name)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"server": name, "action": "restart", "success": true,
	})
}

// --- Helpers ---

// requireAdmin checks that the request is from an admin user.
// Returns false and writes an error response if not admin.
func (h *AdminHandlers) requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	ac := auth.AuthContextFromContext(r.Context())
	if ac == nil || !ac.IsAuthenticated() {
		writeError(w, http.StatusUnauthorized, "Authentication required")
		return false
	}
	if !ac.IsAdmin() {
		writeError(w, http.StatusForbidden, "Admin access required")
		return false
	}
	return true
}
