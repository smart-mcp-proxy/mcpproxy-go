//go:build !server

package httpapi

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/clientidentity"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/connect"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	internalRuntime "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

type clientPresence struct {
	ID                   string          `json:"id"`
	DisplayName          string          `json:"display_name"`
	Kind                 string          `json:"kind"`
	Icon                 string          `json:"icon,omitempty"`
	State                string          `json:"state"`
	Installed            bool            `json:"installed"`
	Connected            bool            `json:"connected"`
	ConnectionUnverified bool            `json:"connection_unverified,omitempty"`
	ConfigPath           string          `json:"config_path,omitempty"`
	DisplayPath          string          `json:"display_path,omitempty"`
	LastSeen             *time.Time      `json:"last_seen"`
	ActiveSessions       int             `json:"active_sessions"`
	Calls24h             int             `json:"calls_24h"`
	ReloadHint           string          `json:"reload_hint,omitempty"`
	Sessions             []clientSession `json:"sessions,omitempty"`

	// Spec 108-f ClientView additions (data-model §7). The Spec 109 fields above
	// are unchanged; everything below is additive.
	//
	// credential_state is what the client's connection carries (client |
	// admin_key | none | revoked | expired | unknown). The stat-only list never
	// reads a config: it reports the token store, else the last on-demand
	// observation (credential_checked_at says when), else unknown.
	CredentialState     profile.CredentialState `json:"credential_state"`
	CredentialCheckedAt *time.Time              `json:"credential_checked_at,omitempty"`
	TokenName           string                  `json:"token_name,omitempty"`
	Profile             string                  `json:"profile,omitempty"`
	ProfileTitle        string                  `json:"profile_title,omitempty"`
	ProfileMode         string                  `json:"profile_mode,omitempty"`
	// profile_source is pin (locked) or binding (switchable) and is empty when
	// credential_state is not client.
	ProfileSource   string     `json:"profile_source,omitempty"`
	ProfileMissing  bool       `json:"profile_missing,omitempty"`
	ExpiresAt       *time.Time `json:"expires_at,omitempty"`
	RotationPending bool       `json:"rotation_pending,omitempty"`
	Blocked24h      int        `json:"blocked_24h"`

	// resolvedCredential is the classification of the on-demand config read of a
	// detail request; never serialised.
	resolvedCredential profile.CredentialState
}

type clientSession struct {
	ID            string    `json:"id"`
	WorkSessionID string    `json:"work_session_id,omitempty"`
	StartedAt     time.Time `json:"started_at"`
	LastActivity  time.Time `json:"last_activity"`
	// Profile and ProfileSource are the session's latest effective resolution
	// (Spec 108-e FR-033); empty on legacy sessions.
	Profile       string `json:"profile,omitempty"`
	ProfileSource string `json:"profile_source,omitempty"`
}

// clientSessionCap bounds the sessions a detail read returns (newest first).
const clientSessionCap = 20

type clientsResponse struct {
	Clients  []clientPresence          `json:"clients"`
	Routing  interface{}               `json:"routing"`
	Warnings []internalRuntime.Warning `json:"warnings"`
}

// presenceContext is what one presence read learned that the decorator reuses:
// the onboarding state (observations) and the recent sessions.
type presenceContext struct {
	state    *storage.OnboardingState
	sessions []*contracts.MCPSession
}

// handleGetClients godoc
// @Summary     List clients
// @Description Lists every client row (supported, other and custom) with its presence, its credential and its profile binding, plus response-level warnings. The list is content-read-free (Spec 075): credential_state comes from the token store, else the last on-demand observation (credential_checked_at), else unknown. It runs only the time-based half of the rotation reconciler. The profile and client filters match the CURRENT binding and are applied AFTER warnings are computed over the full set.
// @Tags        clients
// @Produce     json
// @Security    ApiKeyAuth
// @Security    ApiKeyQuery
// @Param       profile query string false "Rows whose active credential is bound to this profile; - = bound to All servers"
// @Param       client  query string false "Exact client id"
// @Success     200 {object} contracts.APIResponse{data=clientsResponse} "Client rows, routing and warnings"
// @Failure     403 {object} contracts.ErrorResponse "Administrator credentials required"
// @Router      /api/v1/clients [get]
func (s *Server) handleGetClients(w http.ResponseWriter, r *http.Request) {
	// Spec 108-f FR-031: profile and client are honoured here; token is not (a
	// client row is not addressed by token) and stays a 400.
	if !rejectUnsupportedScopeFilters(w, r, "profile", "client") {
		return
	}
	if s.clientsService != nil {
		_ = s.clientsService.ReconcileTimeOnly(r.Context())
	}
	rows, warnings, err := s.clientRows(r.Context(), false, "")
	if err != nil {
		s.writeError(w, r, http.StatusServiceUnavailable, err.Error())
		return
	}
	rows = filterClientRows(rows, r.URL.Query().Get("profile"), r.URL.Query().Get("client"))
	s.writeSuccess(w, clientsResponse{Clients: rows, Routing: s.clientRoutingPayload(), Warnings: warnings})
}

// handleGetClient godoc
// @Summary     Get one client
// @Description Returns one client row with its sessions (newest 20, each with its latest effective profile). This is the only read that may open the client's config file: it runs the full rotation reconciler first, classifies the credential the config holds and records the observation. No scope filter is honoured on this route.
// @Tags        clients
// @Produce     json
// @Security    ApiKeyAuth
// @Security    ApiKeyQuery
// @Param       client path string true "Client id"
// @Success     200 {object} contracts.APIResponse{data=clientPresence} "Client row"
// @Failure     403 {object} contracts.ErrorResponse "Administrator credentials required"
// @Failure     404 {object} contracts.ErrorResponse "client not found"
// @Router      /api/v1/clients/{client} [get]
func (s *Server) handleGetClient(w http.ResponseWriter, r *http.Request) {
	if !rejectUnsupportedScopeFilters(w, r) {
		return
	}
	id := chi.URLParam(r, "client")
	s.reconcileClient(r, id)
	rows, _, err := s.clientRows(r.Context(), true, id)
	if err != nil {
		s.writeError(w, r, http.StatusServiceUnavailable, err.Error())
		return
	}
	for _, row := range rows {
		if row.ID == id {
			if row.resolvedCredential != "" {
				s.recordCredentialObservation(id, row.resolvedCredential)
			}
			s.writeSuccess(w, row)
			return
		}
	}
	s.writeError(w, r, http.StatusNotFound, "client not found")
}

// filterClientRows applies the FR-031 list filters to the CURRENT binding:
// profile=X keeps rows with an ACTIVE client credential bound to X (a dangling
// pin included), profile=- those bound to All servers (an empty pin); a row
// with no active credential matches no profile. client= is an exact id match.
// An unknown value is not an error: no rows.
func filterClientRows(rows []clientPresence, profileFilter, clientFilter string) []clientPresence {
	if profileFilter == "" && clientFilter == "" {
		return rows
	}
	out := make([]clientPresence, 0, len(rows))
	for _, row := range rows {
		if clientFilter != "" && row.ID != clientFilter {
			continue
		}
		if profileFilter != "" {
			if row.CredentialState != profile.CredentialStateClient {
				continue
			}
			if profileFilter == "-" && row.Profile != "" || profileFilter != "-" && row.Profile != profileFilter {
				continue
			}
		}
		out = append(out, row)
	}
	return out
}

// clientRoutingPayload is the routing portion of the Clients hub response.
// Keep it in the same shape as GET /routing: the Endpoint & mode tab can use
// either response without translating or guessing a restart-pending state.
func (s *Server) clientRoutingPayload() map[string]interface{} {
	routingMode := config.RoutingModeRetrieveTools
	toolResponseMode := config.ToolResponseModeFull
	directToolResponseMode := config.DirectToolResponseModeFull
	codeExecutionEnabled := false
	if cfg, err := s.controller.GetConfig(); err == nil && cfg != nil {
		if cfg.RoutingMode != "" {
			routingMode = cfg.RoutingMode
		}
		if cfg.ToolResponseMode != "" {
			toolResponseMode = cfg.ToolResponseMode
		}
		if cfg.DirectToolResponseMode != "" {
			directToolResponseMode = cfg.DirectToolResponseMode
		}
		codeExecutionEnabled = cfg.EnableCodeExecution
	}
	routingMode = s.servedRoutingMode(routingMode)
	pendingRoutingMode, restartRequired := s.pendingRoutingMode(routingMode)
	description := "BM25 search via retrieve_tools + call_tool variants (default)"
	switch routingMode {
	case config.RoutingModeDirect:
		description = "All upstream tools exposed directly via serverName__toolName naming"
	case config.RoutingModeCodeExecution:
		description = "JavaScript orchestration via code_execution tool with tool catalog"
	}
	return map[string]interface{}{
		"routing_mode":              routingMode,
		"description":               description,
		"endpoints":                 map[string]interface{}{"default": "/mcp", "direct": "/mcp/all", "code_execution": "/mcp/code", "retrieve_tools": "/mcp/call"},
		"available_modes":           []string{config.RoutingModeRetrieveTools, config.RoutingModeDirect, config.RoutingModeCodeExecution},
		"code_execution_enabled":    codeExecutionEnabled,
		"tool_response_mode":        toolResponseMode,
		"direct_tool_response_mode": directToolResponseMode,
		"pending_routing_mode":      pendingRoutingMode,
		"restart_required":          restartRequired,
	}
}

// clientRows builds the decorated ClientView rows and the response-level
// warnings. withSessions is the detail read: it may open detailID's config and
// returns that row's sessions.
func (s *Server) clientRows(ctx context.Context, withSessions bool, detailID string) ([]clientPresence, []internalRuntime.Warning, error) {
	rows, pctx, err := s.clientPresence(withSessions, detailID)
	if err != nil {
		return nil, nil, err
	}
	var records []auth.AgentToken
	if s.clientsService != nil {
		if records, err = s.clientsService.Records(); err != nil {
			return nil, nil, err
		}
	}
	cfg, _ := s.controller.GetConfig()
	stats := s.clientStats(ctx)
	now := time.Now()
	if s.clientsService != nil {
		now = s.clientsService.Now()
	}

	rows = s.decorateClientRows(rows, records, pctx, stats, cfg, now, withSessions, detailID)

	var warnings []internalRuntime.Warning
	if s.clientsService != nil {
		states := map[string]profile.CredentialState{}
		for _, row := range rows {
			if row.CredentialState == profile.CredentialStateAdminKey {
				states[row.ID] = profile.CredentialStateAdminKey
			}
		}
		warnings = s.clientsService.Warnings(states)
	}
	if warnings == nil {
		warnings = []internalRuntime.Warning{}
	}
	return rows, warnings, nil
}

// clientStats reads the 24 h rollup by client (administrator scope).
func (s *Server) clientStats(ctx context.Context) internalRuntime.ActivityStats24h {
	if p, ok := s.profiles().(interface {
		Stats24h(context.Context, internalRuntime.ViewerScope) internalRuntime.ActivityStats24h
	}); ok {
		return p.Stats24h(ctx, internalRuntime.ViewerScope{})
	}
	return internalRuntime.ActivityStats24h{ByProfile: map[string]internalRuntime.Counter{}, ByClient: map[string]internalRuntime.Counter{}}
}

// decorateClientRows adds the Spec 108-f fields to the presence rows and
// appends the custom-client rows (F11, F12). records is the credential store
// view (kind=client records and name-conflicting regular tokens).
func (s *Server) decorateClientRows(rows []clientPresence, records []auth.AgentToken, pctx *presenceContext, stats internalRuntime.ActivityStats24h, cfg *config.Config, now time.Time, withSessions bool, detailID string) []clientPresence {
	byID := map[string]*auth.AgentToken{}
	for i := range records {
		if records[i].Kind == auth.KindClient {
			byID[records[i].ClientID] = &records[i]
		}
	}
	titleOf := func(name string) string {
		if cfg != nil {
			for i := range cfg.Profiles {
				if cfg.Profiles[i].Name == name {
					return cfg.Profiles[i].Title
				}
			}
		}
		return ""
	}
	profileExists := func(name string) bool {
		if cfg == nil {
			return false
		}
		for i := range cfg.Profiles {
			if cfg.Profiles[i].Name == name {
				return true
			}
		}
		return false
	}
	var observed map[string]storage.ClientCredentialObservation
	if pctx != nil && pctx.state != nil {
		observed = pctx.state.ClientCredentialObserved
	}
	stateOf := func(rec *auth.AgentToken) profile.CredentialState {
		if s.clientsService != nil {
			return s.clientsService.StateOf(rec)
		}
		return profile.CredentialStateClient
	}

	for i := range rows {
		row := &rows[i]
		row.Blocked24h = stats.ByClient[row.ID].Blocked
		rec := byID[row.ID]
		s.decorateCredential(row, rec, observed, stateOf, now)
		if rec != nil {
			row.TokenName = rec.Name
			row.Profile = rec.ProfilePin
			row.ProfileMode = rec.ProfileMode
			row.ProfileTitle = titleOf(rec.ProfilePin)
			exp := rec.ExpiresAt
			row.ExpiresAt = &exp
			row.RotationPending = rec.PendingHash != ""
			if rec.ProfilePin != "" && !profileExists(rec.ProfilePin) {
				row.ProfileMissing = true
			}
			if row.CredentialState == profile.CredentialStateClient {
				row.ProfileSource = string(profile.SourceBinding)
				if rec.ProfileMode == auth.ProfileModeLocked {
					row.ProfileSource = string(profile.SourcePin)
				}
			}
		}
	}

	// Custom clients: a kind=client record whose id is not in the connect registry.
	known := map[string]bool{}
	for i := range rows {
		known[rows[i].ID] = true
	}
	customIDs := make([]string, 0)
	for id, rec := range byID {
		if connect.FindClient(id) != nil || known[id] {
			continue
		}
		if stateOf(rec) == profile.CredentialStateRevoked && id != detailID {
			continue // a revoked custom row is omitted from the list (still served by detail)
		}
		customIDs = append(customIDs, id)
	}
	sort.Strings(customIDs)
	for _, id := range customIDs {
		rec := byID[id]
		row := clientPresence{
			ID: id, DisplayName: rec.DisplayName, Kind: "custom", Installed: false,
			CredentialState: stateOf(rec), TokenName: rec.Name, Profile: rec.ProfilePin, ProfileMode: rec.ProfileMode,
			ProfileTitle: titleOf(rec.ProfilePin), RotationPending: rec.PendingHash != "",
			Blocked24h: stats.ByClient[id].Blocked, Calls24h: stats.ByClient[id].Calls,
		}
		if row.DisplayName == "" {
			row.DisplayName = id
		}
		exp := rec.ExpiresAt
		row.ExpiresAt = &exp
		if rec.ProfilePin != "" && !profileExists(rec.ProfilePin) {
			row.ProfileMissing = true
		}
		active := row.CredentialState == profile.CredentialStateClient
		row.Connected = active
		row.State = "other"
		if active {
			row.ProfileSource = string(profile.SourceBinding)
			if rec.ProfileMode == auth.ProfileModeLocked {
				row.ProfileSource = string(profile.SourcePin)
			}
			row.State = "connected_never_seen"
		}
		if pctx != nil {
			for _, sess := range pctx.sessions {
				if sess.ClientID != id {
					continue
				}
				if rec.ConnectedAt != nil && sess.StartTime.Before(*rec.ConnectedAt) {
					continue
				}
				if active {
					row.State = "connected_seen"
				}
				if sess.Status == "active" {
					row.ActiveSessions++
				}
				if row.LastSeen == nil || sess.LastActivity.After(*row.LastSeen) {
					at := sess.LastActivity
					row.LastSeen = &at
				}
				if withSessions && id == detailID && len(row.Sessions) < clientSessionCap {
					row.Sessions = append(row.Sessions, clientSession{ID: sess.ID, WorkSessionID: sess.WorkSessionID, StartedAt: sess.StartTime, LastActivity: sess.LastActivity, Profile: sess.Profile, ProfileSource: sess.ProfileSource})
				}
			}
		}
		rows = append(rows, row)
	}
	return rows
}

// decorateCredential sets credential_state and credential_checked_at by the
// F11 precedence: an on-demand classification of THIS request (detail read),
// else the token store, else the last persisted observation, else unknown for a
// connected presence row and none otherwise.
func (s *Server) decorateCredential(row *clientPresence, rec *auth.AgentToken, observed map[string]storage.ClientCredentialObservation, stateOf func(*auth.AgentToken) profile.CredentialState, now time.Time) {
	switch {
	case row.resolvedCredential != "" && row.resolvedCredential != profile.CredentialStateUnknown:
		row.CredentialState = row.resolvedCredential
		at := now
		row.CredentialCheckedAt = &at
	case rec != nil:
		row.CredentialState = stateOf(rec)
	default:
		if obs, ok := observed[row.ID]; ok && obs.State != "" {
			row.CredentialState = profile.CredentialState(obs.State)
			at := obs.At
			row.CredentialCheckedAt = &at
		} else if row.Connected && row.Kind == "supported" {
			row.CredentialState = profile.CredentialStateUnknown
		} else {
			row.CredentialState = profile.CredentialStateNone
		}
	}
}

func (s *Server) clientPresence(withSessions bool, detailID string) ([]clientPresence, *presenceContext, error) {
	statuses := map[string]connect.ClientStatus{}
	if svc := s.getConnectService(); svc != nil {
		for _, status := range svc.GetAllStatus() {
			statuses[status.ID] = status
		}
	}
	state, err := s.controller.GetOnboardingState()
	if err != nil {
		return nil, nil, err
	}
	usage := s.controller.UsageSnapshot()
	sessions, total, err := s.controller.GetRecentSessions(storage.SessionFilter{Limit: 100})
	if err != nil {
		return nil, nil, err
	}
	// Session retention is currently capped, but the controller contract is not.
	// Do not let another client's newer sessions hide this client's active rows
	// or detail history when a controller retains more than this first page.
	if total > len(sessions) {
		sessions, _, err = s.controller.GetRecentSessions(storage.SessionFilter{Limit: total})
		if err != nil {
			return nil, nil, err
		}
	}
	pctx := &presenceContext{state: state, sessions: sessions}
	result := make([]clientPresence, 0, len(statuses))
	for _, def := range connect.GetAllClients() {
		status := statuses[def.ID]
		resolved := profile.CredentialState("")
		if withSessions && def.ID == detailID && s.getConnectService() != nil {
			// The list remains metadata-only. An explicit detail read is the
			// sole presence API allowed to inspect only the requested client's config.
			if st, getErr := s.getConnectService().GetStatus(detailID); getErr == nil {
				status = st
				resolved = profile.CredentialState(st.CredentialState)
			}
		}
		lastSeen := latestClientSeen(state.ClientLastSeen, def.ClientInfoNames)
		row := clientPresence{ID: def.ID, DisplayName: def.Name, Kind: "supported", Icon: def.Icon, Installed: status.Exists, ConfigPath: status.ConfigPath, DisplayPath: status.DisplayPath, ReloadHint: def.ReloadHint, LastSeen: lastSeen, resolvedCredential: resolved}
		row.Calls24h = usage.ClientCallsSince(def.ClientInfoNames, time.Now().Add(-24*time.Hour))
		connectedAt := state.ClientConnectedAt[def.ID]
		disconnectedAt := state.ClientDisconnectedAt[def.ID]
		// A disconnect ends the previous connection generation. Historical
		// initialise evidence remains useful as "last seen", but only a later
		// initialise can make the client connected again.
		seenAfterDisconnect := lastSeen != nil && (disconnectedAt.IsZero() || lastSeen.After(disconnectedAt))
		row.Connected = !connectedAt.IsZero() || seenAfterDisconnect || (withSessions && status.Connected)
		if seenAfterDisconnect {
			row.State = "connected_seen"
		} else if !connectedAt.IsZero() || (withSessions && status.Connected) {
			row.State = "connected_never_seen"
		} else if status.Exists {
			row.State = "installed"
			row.ConnectionUnverified = true
		} else {
			row.State = "not_installed"
		}
		for _, session := range sessions {
			if !clientMatches(session.ClientName, def.ClientInfoNames) {
				continue
			}
			// Connect and disconnect both divide client generations. Session rows
			// from before the latest lifecycle change are historical and must not
			// inflate the active count or reappear in the expanded detail.
			generationStartedAt := connectedAt
			if disconnectedAt.After(generationStartedAt) {
				generationStartedAt = disconnectedAt
			}
			if !generationStartedAt.IsZero() && session.StartTime.Before(generationStartedAt) {
				continue
			}
			if session.Status == "active" {
				row.ActiveSessions++
			}
			if withSessions && len(row.Sessions) < clientSessionCap {
				row.Sessions = append(row.Sessions, clientSession{ID: session.ID, WorkSessionID: session.WorkSessionID, StartedAt: session.StartTime, LastActivity: session.LastActivity, Profile: session.Profile, ProfileSource: session.ProfileSource})
			}
		}
		result = append(result, row)
	}
	// A client that initializes with an unrecognised clientInfo.name is real
	// local activity, not a manually-created client. Preserve that evidence as
	// an `other:` row without granting it a guessed configuration path.
	known := make(map[string]bool)
	for _, def := range connect.GetAllClients() {
		for _, alias := range def.ClientInfoNames {
			known[clientidentity.NormalizeRaw(alias)] = true
		}
	}
	for _, session := range sessions {
		identity := clientidentity.FromRaw(session.ClientName)
		if identity.Key == "" || known[identity.RawNormalized] {
			continue
		}
		id := "other:" + identity.PublicID
		found := -1
		for i := range result {
			if result[i].ID == id {
				found = i
				break
			}
		}
		if found < 0 {
			result = append(result, clientPresence{ID: id, DisplayName: identity.DisplayName, Kind: "other", State: "other"})
			found = len(result) - 1
		}
		if session.Status == "active" {
			result[found].ActiveSessions++
		}
		if result[found].LastSeen == nil || session.LastActivity.After(*result[found].LastSeen) {
			at := session.LastActivity
			result[found].LastSeen = &at
		}
		if withSessions && len(result[found].Sessions) < clientSessionCap {
			result[found].Sessions = append(result[found].Sessions, clientSession{ID: session.ID, WorkSessionID: session.WorkSessionID, StartedAt: session.StartTime, LastActivity: session.LastActivity, Profile: session.Profile, ProfileSource: session.ProfileSource})
		}
	}
	// An unknown client can have initialized without making a tool call, so it
	// has no session row. Preserve that evidence too, while deliberately
	// omitting configuration paths: MCP clientInfo has no trustworthy path.
	for name, seenAt := range state.ClientLastSeen {
		identity := clientidentity.FromStoredKey(name)
		if identity.Key == "" || known[identity.RawNormalized] {
			continue
		}
		id := "other:" + identity.PublicID
		found := -1
		for i := range result {
			if result[i].ID == id {
				found = i
				break
			}
		}
		if found < 0 {
			result = append(result, clientPresence{ID: id, DisplayName: identity.DisplayName, Kind: "other", State: "other"})
			found = len(result) - 1
		}
		if result[found].LastSeen == nil || seenAt.After(*result[found].LastSeen) {
			at := seenAt
			result[found].LastSeen = &at
		}
	}
	return result, pctx, nil
}

func latestClientSeen(seen map[string]time.Time, aliases []string) *time.Time {
	var latest time.Time
	for _, alias := range aliases {
		if at := seen[strings.ToLower(alias)]; at.After(latest) {
			latest = at
		}
	}
	if latest.IsZero() {
		return nil
	}
	return &latest
}
func clientMatches(name string, aliases []string) bool {
	for _, alias := range aliases {
		if strings.EqualFold(strings.TrimSpace(name), alias) {
			return true
		}
	}
	return false
}
