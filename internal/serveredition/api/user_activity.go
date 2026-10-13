//go:build server

package api

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/serveredition/multiuser"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/serveredition/users"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// UserActivityHandlers provides endpoints for user activity and diagnostics.
type UserActivityHandlers struct {
	activityFilter *multiuser.ActivityFilter
	userStore      *users.UserStore
	sharedServers  []*config.ServerConfig
	adminServers   AdminServersProvider
	logger         *zap.SugaredLogger

	// entitlement is the ONE tenant predicate (Spec 107 FR-004) the
	// diagnostics door selects its shared servers through. Installed by
	// setup.go (SetEntitlement) with the same UserHandlers the /user/servers
	// doors use; when absent, a predicate is built over this handler's own
	// user store and admin-servers provider (no access block: today's
	// Shared-only semantics).
	entitlement *UserHandlers

	// activityProjector converts+masks a storage record exactly as core
	// GET /activity does (Spec 107 T086: httpapi.(*Server).ActivityProjector,
	// installed by setup.go via SetActivityProjector). nil only when
	// activityFilter is also nil (setup.go wires both together); the door
	// then answers today's empty shape.
	activityProjector func(*storage.ActivityRecord) contracts.ActivityRecord
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

func (h *UserActivityHandlers) SetAdminServersProvider(provider AdminServersProvider) {
	h.adminServers = provider
}

// SetEntitlement installs the shared entitlement predicate (Spec 107 FR-004):
// the diagnostics door and the /user/servers doors must answer "may this
// user see server N" from one place, so setup.go hands both the same
// UserHandlers.
func (h *UserActivityHandlers) SetEntitlement(e *UserHandlers) {
	h.entitlement = e
}

// SetActivityProjector installs the convert+mask composition core
// GET /activity applies (Spec 107 T086), so this door emits the same JSON
// shape and the same masking for the same record.
func (h *UserActivityHandlers) SetActivityProjector(p func(*storage.ActivityRecord) contracts.ActivityRecord) {
	h.activityProjector = p
}

// entitlementPredicate returns the installed predicate, or one built over
// this handler's own sources (no access block) when none was installed.
func (h *UserActivityHandlers) entitlementPredicate() *UserHandlers {
	if h.entitlement != nil {
		return h.entitlement
	}
	return NewUserHandlers(h.userStore, h.currentAdminServers, nil, nil, h.logger)
}

func (h *UserActivityHandlers) currentAdminServers() []*config.ServerConfig {
	if h.adminServers != nil {
		return h.adminServers()
	}
	return h.sharedServers
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

// getUserActivity returns the current user's activity log (Spec 107 T086,
// contracts/rest-endpoints.md §"user/activity"): records where
// user_id == principal.user_id AND server_name is in the entitlement set,
// both terms evaluated inside storage.ActivityFilter.Matches so `total`
// counts only what the page may contain, then projected and masked through
// the same composition core GET /activity applies.
//
// An admin_user session receives today's empty {items:[],total:0}
// unconditionally (SC-006: this stays the merge-base carve-out — the filter's
// admin branch is never reached from this door; administrators read history
// on core /activity*, which refuses a tenant session non-disclosingly).
func (h *UserActivityHandlers) getUserActivity(w http.ResponseWriter, r *http.Request) {
	ac := auth.AuthContextFromContext(r.Context())
	if ac == nil || !ac.IsUser() {
		writeError(w, http.StatusUnauthorized, "Authentication required")
		return
	}

	if ac.IsAdmin() {
		writeJSON(w, http.StatusOK, ActivityListResponse{Items: []struct{}{}, Total: 0})
		return
	}

	limit := parseIntParam(r, "limit", 50)
	offset := parseIntParam(r, "offset", 0)

	if h.activityFilter == nil {
		writeJSON(w, http.StatusOK, ActivityListResponse{Items: []struct{}{}, Total: 0})
		return
	}

	// The entitlement set is resolved live through the ONE predicate
	// (Spec 107 FR-004), never trusted off ac.AllowedServers: this door is
	// mounted behind ServerEditionAuthMiddleware (setup.go), which builds a
	// plain UserContext and never populates AllowedServers — only the
	// SessionPrincipalResolver path used by core /api/v1 materialises it.
	// Reading ac.AllowedServers directly left storage.ActivityFilter.
	// serverAllowed treating a nil slice as UNRESTRICTED (its documented
	// "nil means unrestricted" contract), so every tenant on this door saw
	// every user's activity for every server, entitled or not
	// (cross-review round 1, P1). entitledServerNames always returns a
	// non-nil slice (empty when nothing is entitled), which is exactly the
	// deny-all shape serverAllowed requires.
	entitled, err := h.entitlementPredicate().entitledServerNames(ac.UserID, false)
	if err != nil {
		h.logger.Errorw("failed to resolve server entitlement for activity", "user_id", ac.UserID, "error", err)
		writeError(w, http.StatusServiceUnavailable, "Server entitlement unavailable")
		return
	}

	filter := storage.DefaultActivityFilter()
	filter.Limit = limit
	filter.Offset = offset
	filter.UserID = ac.UserID
	filter.AllowedServers = entitled

	records, total, err := h.activityFilter.ListActivities(filter)
	if err != nil {
		h.logger.Errorw("failed to get user activity", "error", err)
		writeError(w, http.StatusInternalServerError, "Failed to get activity")
		return
	}

	// Route through the same convert+mask composition core GET /activity
	// applies (Spec 107 T086) when a projector was installed. A nil
	// projector (no setup.go wiring — e.g. an embedder or a test harness
	// exercising activityFilter alone) falls back to the raw records, as
	// this door always did.
	var items interface{}
	switch {
	case h.activityProjector != nil:
		projected := make([]contracts.ActivityRecord, 0, len(records))
		for _, record := range records {
			projected = append(projected, h.activityProjector(record))
		}
		items = projected
	case records == nil:
		items = []struct{}{}
	default:
		items = records
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

	// Add shared servers — the caller's ENTITLED ones, through the one
	// predicate (Spec 107 FR-004; issue #1161 follow-up): setup.go hands
	// every handler `deps.Config.Servers` — the admin's whole server list —
	// so a loop without the predicate reports admin upstreams the admin
	// deliberately did not share, or did not grant to this caller's group,
	// labelled `ownership:"shared"`, to every authenticated user.
	visible, err := h.entitlementPredicate().visibleSharedServers(r, userID)
	if err != nil {
		h.logger.Errorw("failed to resolve server entitlement for diagnostics", "user_id", userID, "error", err)
		writeError(w, http.StatusServiceUnavailable, "Server entitlement unavailable")
		return
	}
	for _, sc := range visible {
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
