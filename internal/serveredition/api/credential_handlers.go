//go:build server

package api

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/serveredition/broker"
)

// Connection-status values surfaced by GET /api/v1/user/credentials. They carry
// no secret material (FR-026).
const (
	credStatusConnected    = "connected"     // a valid, non-expired per-user credential exists
	credStatusExpired      = "expired"       // a credential exists but its access token has expired
	credStatusNotConnected = "not_connected" // no per-user credential exists for this upstream
	credStatusUnavailable  = "unavailable"   // the credential store is disabled (no encryption key)
)

// credentialConnectSuccessRedirect is where the browser lands after a successful
// or denied connect flow. The Web UI surfaces the resulting state.
const credentialConnectSuccessRedirect = "/ui/"

// CredentialHandlers exposes per-user brokered-credential surfaces for the
// server edition: listing connection status, disconnecting, and driving the
// per-user OAuth connect flow (Path B, spec 074 T5). Every operation is scoped
// to the authenticated user (FR-027) and never returns secret values (FR-026).
type CredentialHandlers struct {
	store         broker.CredentialStore
	brokerServers []*config.ServerConfig // admin-configured shared servers; broker ones are filtered at use
	connectors    *connectorProvider
	logger        *zap.SugaredLogger
}

// NewCredentialHandlers builds the handlers over a credential store and the set
// of shared servers (only those carrying an auth_broker block are brokered). The
// audit sink (spec 074 T10) records per-user connect-flow events to the activity
// log; a nil sink disables audit emission.
func NewCredentialHandlers(store broker.CredentialStore, sharedServers []*config.ServerConfig, audit broker.AuditSink, logger *zap.SugaredLogger) *CredentialHandlers {
	if logger == nil {
		logger = zap.NewNop().Sugar()
	}
	return &CredentialHandlers{
		store:         store,
		brokerServers: sharedServers,
		connectors:    newConnectorProvider(store, logger.Desugar(), audit),
		logger:        logger,
	}
}

// ConnectorProvider exposes the shared, connector cache so the credential
// resolver (T6) can mint connect URLs through the same connectors that serve the
// REST connect/callback flow.
func (h *CredentialHandlers) ConnectorProvider() broker.ConnectorProvider {
	return h.connectors
}

// RegisterRoutes registers credential routes on the provided router.
func (h *CredentialHandlers) RegisterRoutes(r chi.Router) {
	r.Route("/user/credentials", func(r chi.Router) {
		r.Get("/", h.listCredentials)
		r.Delete("/{server}", h.deleteCredential)
		r.Get("/{server}/connect", h.connect)
		r.Get("/{server}/callback", h.callback)
	})
}

// RegisterRoutesWithPrefix registers credential routes with a path prefix.
func (h *CredentialHandlers) RegisterRoutesWithPrefix(r chi.Router, prefix string) {
	r.Get(prefix+"/user/credentials", h.listCredentials)
	r.Delete(prefix+"/user/credentials/{server}", h.deleteCredential)
	r.Get(prefix+"/user/credentials/{server}/connect", h.connect)
	r.Get(prefix+"/user/credentials/{server}/callback", h.callback)
}

// --- Response types ---

// CredentialStatus is the non-secret connection view for one brokered upstream.
// It deliberately omits access_token / refresh_token (FR-026).
type CredentialStatus struct {
	Server      string     `json:"server"`
	Mode        string     `json:"mode"`
	Status      string     `json:"status"`
	TokenType   string     `json:"token_type,omitempty"`
	Scopes      []string   `json:"scopes,omitempty"`
	Audience    string     `json:"audience,omitempty"`
	ObtainedVia string     `json:"obtained_via,omitempty"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	UpdatedAt   *time.Time `json:"updated_at,omitempty"`
	// ConnectPath is the actionable connect endpoint for oauth_connect upstreams
	// that are not currently connected (or whose credential expired).
	ConnectPath string `json:"connect_path,omitempty"`
}

// CredentialListResponse wraps the per-user credential statuses.
type CredentialListResponse struct {
	Credentials []CredentialStatus `json:"credentials"`
}

// --- Handlers ---

// listCredentials returns the connection status of every brokered upstream for
// the authenticated user. Secret values are never included (FR-026).
func (h *CredentialHandlers) listCredentials(w http.ResponseWriter, r *http.Request) {
	userID, err := getUserID(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "Authentication required")
		return
	}

	storeEnabled := h.store != nil && h.store.Enabled()

	out := make([]CredentialStatus, 0)
	for _, srv := range h.brokerServerList() {
		status := CredentialStatus{
			Server: srv.Name,
			Mode:   srv.AuthBroker.Mode,
		}

		switch {
		case !storeEnabled:
			status.Status = credStatusUnavailable
		default:
			cred, gerr := h.store.Get(userID, oauth.GenerateServerKey(srv.Name, srv.URL))
			switch {
			case errors.Is(gerr, broker.ErrNotFound):
				status.Status = credStatusNotConnected
			case gerr != nil:
				h.logger.Warnw("failed to load brokered credential", "user_id", userID, "server", srv.Name, "error", gerr)
				status.Status = credStatusUnavailable
			default:
				status.populateFromCredential(cred)
			}
		}

		// Offer an actionable connect path for connect-flow upstreams that are
		// not currently usable.
		if srv.AuthBroker.Mode == config.AuthBrokerModeOAuthConnect &&
			(status.Status == credStatusNotConnected || status.Status == credStatusExpired) {
			status.ConnectPath = connectInitiatePath(srv.Name)
		}

		out = append(out, status)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Server < out[j].Server })
	writeJSON(w, http.StatusOK, CredentialListResponse{Credentials: out})
}

// populateFromCredential fills the non-secret metadata and the connected/expired
// status from a stored credential. It never copies token material (FR-026).
func (s *CredentialStatus) populateFromCredential(cred *broker.UpstreamCredential) {
	if cred.IsExpired() {
		s.Status = credStatusExpired
	} else {
		s.Status = credStatusConnected
	}
	s.TokenType = cred.TokenType
	s.Scopes = cred.Scopes
	s.Audience = cred.Audience
	s.ObtainedVia = cred.ObtainedVia
	if !cred.ExpiresAt.IsZero() {
		t := cred.ExpiresAt
		s.ExpiresAt = &t
	}
	if !cred.UpdatedAt.IsZero() {
		t := cred.UpdatedAt
		s.UpdatedAt = &t
	}
}

// deleteCredential disconnects (revokes) the authenticated user's credential for
// a brokered upstream. Only the caller's own record is affected (FR-027).
func (h *CredentialHandlers) deleteCredential(w http.ResponseWriter, r *http.Request) {
	userID, err := getUserID(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "Authentication required")
		return
	}

	srv, ok := h.lookupServer(w, r)
	if !ok {
		return
	}

	if h.store == nil || !h.store.Enabled() {
		writeError(w, http.StatusServiceUnavailable, "Credential broker is not enabled")
		return
	}

	if err := h.store.Delete(userID, oauth.GenerateServerKey(srv.Name, srv.URL)); err != nil {
		h.logger.Errorw("failed to delete brokered credential", "user_id", userID, "server", srv.Name, "error", err)
		writeError(w, http.StatusInternalServerError, "Failed to disconnect credential")
		return
	}

	h.logger.Infow("brokered credential disconnected", "user_id", userID, "server", srv.Name)
	writeJSON(w, http.StatusOK, map[string]string{
		"message": fmt.Sprintf("Disconnected credential for %q", srv.Name),
	})
}

// connect initiates Path B: it builds the upstream authorize URL bound to the
// authenticated user and redirects the browser there (spec 074 T5, FR-011).
func (h *CredentialHandlers) connect(w http.ResponseWriter, r *http.Request) {
	userID, err := getUserID(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "Authentication required")
		return
	}

	srv, ok := h.lookupServer(w, r)
	if !ok {
		return
	}
	if srv.AuthBroker.Mode != config.AuthBrokerModeOAuthConnect {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("Server %q does not use the OAuth connect flow", srv.Name))
		return
	}

	h.connectors.observeBaseURL(r)
	conn, err := h.connectors.connector(srv)
	if err != nil {
		h.logger.Errorw("failed to build connector", "user_id", userID, "server", srv.Name, "error", err)
		writeError(w, http.StatusInternalServerError, "Failed to initiate connect flow")
		return
	}

	authURL, _, err := conn.BuildAuthorizationURL(userID)
	if err != nil {
		h.logger.Errorw("failed to build authorize URL", "user_id", userID, "server", srv.Name, "error", err)
		writeError(w, http.StatusInternalServerError, "Failed to initiate connect flow")
		return
	}

	h.logger.Infow("brokered credential connect initiated", "user_id", userID, "server", srv.Name)
	http.Redirect(w, r, authURL, http.StatusFound)
}

// callback completes Path B: it validates the state, exchanges the code for a
// per-user upstream credential (persisted by the connector under the initiating
// user), and redirects back to the Web UI. A denied/failed authorization clears
// the pending flow and stores nothing.
func (h *CredentialHandlers) callback(w http.ResponseWriter, r *http.Request) {
	if _, err := getUserID(r); err != nil {
		writeError(w, http.StatusUnauthorized, "Authentication required")
		return
	}

	srv, ok := h.lookupServer(w, r)
	if !ok {
		return
	}
	if srv.AuthBroker.Mode != config.AuthBrokerModeOAuthConnect {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("Server %q does not use the OAuth connect flow", srv.Name))
		return
	}

	conn, err := h.connectors.connector(srv)
	if err != nil {
		h.logger.Errorw("failed to resolve connector for callback", "server", srv.Name, "error", err)
		writeError(w, http.StatusInternalServerError, "Failed to complete connect flow")
		return
	}

	q := r.URL.Query()
	state := q.Get("state")

	// Authorization-server-side error (e.g. user denied consent).
	// The AS error code is coerced to a fixed label for the audit sink so the
	// activity log never captures a raw upstream error string (FR-029).
	if asErr := q.Get("error"); asErr != "" {
		auditReason := sanitizeCallbackError(asErr)
		_ = conn.Deny(state, auditReason)
		h.logger.Infow("brokered credential connect denied", "server", srv.Name, "reason", asErr)
		http.Redirect(w, r, credentialConnectRedirect(srv.Name, asErr), http.StatusFound)
		return
	}

	code := q.Get("code")
	if _, err := conn.Complete(r.Context(), state, code); err != nil {
		h.logger.Warnw("brokered credential connect callback failed", "server", srv.Name, "error", err)
		http.Redirect(w, r, credentialConnectRedirect(srv.Name, "connect_failed"), http.StatusFound)
		return
	}

	h.logger.Infow("brokered credential connected", "server", srv.Name)
	http.Redirect(w, r, credentialConnectRedirect(srv.Name, ""), http.StatusFound)
}

// --- Helpers ---

// lookupServer resolves the {server} path param to a brokered upstream, writing
// a 4xx and returning ok=false when missing/unknown.
func (h *CredentialHandlers) lookupServer(w http.ResponseWriter, r *http.Request) (*config.ServerConfig, bool) {
	name := chi.URLParam(r, "server")
	if decoded, err := url.PathUnescape(name); err == nil {
		name = decoded
	}
	name = strings.TrimSpace(name)
	if name == "" {
		writeError(w, http.StatusBadRequest, "Server name is required")
		return nil, false
	}
	srv := h.brokerServerByName(name)
	if srv == nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("Brokered server %q not found", name))
		return nil, false
	}
	return srv, true
}

// brokerServerList returns the shared servers that carry an auth_broker block.
func (h *CredentialHandlers) brokerServerList() []*config.ServerConfig {
	out := make([]*config.ServerConfig, 0, len(h.brokerServers))
	for _, s := range h.brokerServers {
		if s != nil && s.AuthBroker != nil {
			out = append(out, s)
		}
	}
	return out
}

// brokerServerByName finds a brokered upstream by case-insensitive name.
func (h *CredentialHandlers) brokerServerByName(name string) *config.ServerConfig {
	for _, s := range h.brokerServers {
		if s != nil && s.AuthBroker != nil && strings.EqualFold(s.Name, name) {
			return s
		}
	}
	return nil
}

// sanitizeCallbackError coerces a raw OAuth authorization-server error code into
// a fixed, secret-free label suitable for the audit sink (FR-029). Unknown error
// codes map to the generic "authorization_denied" label rather than leaking the
// upstream's error string.
func sanitizeCallbackError(err string) string {
	switch err {
	case "access_denied":
		return "access_denied"
	case "invalid_scope":
		return "invalid_scope"
	case "server_error":
		return "server_error"
	case "temporarily_unavailable":
		return "temporarily_unavailable"
	case "interaction_required":
		return "interaction_required"
	case "login_required":
		return "login_required"
	case "account_selection_required":
		return "account_selection_required"
	case "consent_required":
		return "consent_required"
	default:
		return "authorization_denied"
	}
}

// credentialConnectRedirect builds the post-callback Web UI redirect, tagging
// the outcome so the UI can surface success or an error without exposing secrets.
func credentialConnectRedirect(serverName, errCode string) string {
	v := url.Values{}
	v.Set("credential_server", serverName)
	if errCode != "" {
		v.Set("credential_error", errCode)
	} else {
		v.Set("credential_connected", "1")
	}
	return credentialConnectSuccessRedirect + "?" + v.Encode()
}
