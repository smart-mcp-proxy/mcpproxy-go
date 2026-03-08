//go:build teams

package api

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/teams/multiuser"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/teams/users"
)

// UserActivityHandlers provides endpoints for user activity and diagnostics.
type UserActivityHandlers struct {
	activityFilter *multiuser.ActivityFilter
	userStore      *users.UserStore
	sharedServers  []*config.ServerConfig
	logger         *zap.SugaredLogger
}

// NewUserActivityHandlers creates a new UserActivityHandlers instance.
func NewUserActivityHandlers(
	activityFilter *multiuser.ActivityFilter,
	userStore *users.UserStore,
	sharedServers []*config.ServerConfig,
	logger *zap.SugaredLogger,
) *UserActivityHandlers {
	return &UserActivityHandlers{
		activityFilter: activityFilter,
		userStore:      userStore,
		sharedServers:  sharedServers,
		logger:         logger,
	}
}

// RegisterRoutes registers user activity and diagnostics routes on the provided router.
func (h *UserActivityHandlers) RegisterRoutes(r chi.Router) {
	r.Get("/user/activity", h.getUserActivity)
	r.Get("/user/diagnostics", h.getDiagnostics)
}

// RegisterRoutesWithPrefix registers user activity routes with a path prefix.
func (h *UserActivityHandlers) RegisterRoutesWithPrefix(r chi.Router, prefix string) {
	r.Get(prefix+"/user/activity", h.getUserActivity)
	r.Get(prefix+"/user/diagnostics", h.getDiagnostics)
}

// --- Response types ---

// ActivityListResponse contains paginated activity records.
type ActivityListResponse struct {
	Items interface{} `json:"items"`
	Total int         `json:"total"`
}

// ServerDiagnostic represents health/status for a single server.
type ServerDiagnostic struct {
	Name      string `json:"name"`
	Ownership string `json:"ownership"` // "shared" or "personal"
	Connected bool   `json:"connected"`
	ToolCount int    `json:"tool_count"`
	Protocol  string `json:"protocol,omitempty"`
	Enabled   bool   `json:"enabled"`
}

// DiagnosticsResponse contains diagnostics for user-accessible servers.
type DiagnosticsResponse struct {
	Servers []*ServerDiagnostic `json:"servers"`
}

// --- Handlers ---

// getUserActivity returns the current user's activity log.
func (h *UserActivityHandlers) getUserActivity(w http.ResponseWriter, r *http.Request) {
	_, err := getUserID(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "Authentication required")
		return
	}

	limit := parseIntParam(r, "limit", 50)
	offset := parseIntParam(r, "offset", 0)

	if h.activityFilter == nil {
		writeJSON(w, http.StatusOK, ActivityListResponse{
			Items: []struct{}{},
			Total: 0,
		})
		return
	}

	records, total, err := h.activityFilter.GetUserActivity(r.Context(), limit, offset)
	if err != nil {
		h.logger.Errorw("failed to get user activity", "error", err)
		writeError(w, http.StatusInternalServerError, "Failed to get activity")
		return
	}

	// Ensure empty array in JSON (not null).
	items := interface{}(records)
	if records == nil {
		items = []struct{}{}
	}

	writeJSON(w, http.StatusOK, ActivityListResponse{
		Items: items,
		Total: total,
	})
}

// getDiagnostics returns health/status for servers the user can access.
func (h *UserActivityHandlers) getDiagnostics(w http.ResponseWriter, r *http.Request) {
	userID, err := getUserID(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "Authentication required")
		return
	}

	var diagnostics []*ServerDiagnostic

	// Add shared servers.
	for _, sc := range h.sharedServers {
		diagnostics = append(diagnostics, &ServerDiagnostic{
			Name:      sc.Name,
			Ownership: "shared",
			Connected: false, // MVP: no live connection status
			ToolCount: 0,     // MVP: requires upstream manager integration
			Protocol:  sc.Protocol,
			Enabled:   sc.Enabled,
		})
	}

	// Add user's personal servers.
	personalServers, err := h.userStore.ListUserServers(userID)
	if err != nil {
		h.logger.Errorw("failed to list user servers for diagnostics", "user_id", userID, "error", err)
		writeError(w, http.StatusInternalServerError, "Failed to get diagnostics")
		return
	}

	for _, sc := range personalServers {
		diagnostics = append(diagnostics, &ServerDiagnostic{
			Name:      sc.Name,
			Ownership: "personal",
			Connected: false, // MVP: no live connection status
			ToolCount: 0,     // MVP: requires upstream manager integration
			Protocol:  sc.Protocol,
			Enabled:   sc.Enabled,
		})
	}

	if diagnostics == nil {
		diagnostics = make([]*ServerDiagnostic, 0)
	}

	writeJSON(w, http.StatusOK, DiagnosticsResponse{
		Servers: diagnostics,
	})
}

// --- Helpers ---

// parseIntParam extracts an integer query parameter with a default value.
func parseIntParam(r *http.Request, name string, defaultVal int) int {
	val := r.URL.Query().Get(name)
	if val == "" {
		return defaultVal
	}
	n, err := strconv.Atoi(val)
	if err != nil || n < 0 {
		return defaultVal
	}
	return n
}
