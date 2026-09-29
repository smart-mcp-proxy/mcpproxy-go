//go:build !server

package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/connect"
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
}

type clientSession struct {
	ID            string    `json:"id"`
	WorkSessionID string    `json:"work_session_id,omitempty"`
	StartedAt     time.Time `json:"started_at"`
	LastActivity  time.Time `json:"last_activity"`
}

type clientsResponse struct {
	Clients []clientPresence `json:"clients"`
	Routing interface{}      `json:"routing"`
}

func (s *Server) handleGetClients(w http.ResponseWriter, r *http.Request) {
	// This endpoint does not honour scope filters until Spec 108 adds the
	// per-client authorization model. Reject them before touching local client
	// configuration or session state so a caller never receives an unfiltered
	// inventory by accident.
	if !rejectUnsupportedScopeFilters(w, r) {
		return
	}
	rows, err := s.clientPresence(false, "")
	if err != nil {
		s.writeError(w, r, http.StatusServiceUnavailable, err.Error())
		return
	}
	s.writeSuccess(w, clientsResponse{Clients: rows, Routing: s.clientRoutingPayload()})
}

func (s *Server) handleGetClient(w http.ResponseWriter, r *http.Request) {
	if !rejectUnsupportedScopeFilters(w, r) {
		return
	}
	id := chi.URLParam(r, "client")
	rows, err := s.clientPresence(true, id)
	if err != nil {
		s.writeError(w, r, http.StatusServiceUnavailable, err.Error())
		return
	}
	for _, row := range rows {
		if row.ID == id {
			s.writeSuccess(w, row)
			return
		}
	}
	s.writeError(w, r, http.StatusNotFound, "client not found")
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

func (s *Server) clientPresence(withSessions bool, detailID string) ([]clientPresence, error) {
	statuses := map[string]connect.ClientStatus{}
	if svc := s.getConnectService(); svc != nil {
		for _, status := range svc.GetAllStatus() {
			statuses[status.ID] = status
		}
	}
	state, err := s.controller.GetOnboardingState()
	if err != nil {
		return nil, err
	}
	usage := s.controller.UsageSnapshot()
	sessions, total, err := s.controller.GetRecentSessions(100, "")
	if err != nil {
		return nil, err
	}
	// Session retention is currently capped, but the controller contract is not.
	// Do not let another client's newer sessions hide this client's active rows
	// or detail history when a controller retains more than this first page.
	if total > len(sessions) {
		sessions, _, err = s.controller.GetRecentSessions(total, "")
		if err != nil {
			return nil, err
		}
	}
	result := make([]clientPresence, 0, len(statuses))
	for _, def := range connect.GetAllClients() {
		status := statuses[def.ID]
		if withSessions && def.ID == detailID && s.getConnectService() != nil {
			// The list remains metadata-only. An explicit detail read is the
			// sole presence API allowed to inspect only the requested client's config.
			if resolved, getErr := s.getConnectService().GetStatus(detailID); getErr == nil {
				status = resolved
			}
		}
		lastSeen := latestClientSeen(state.ClientLastSeen, def.ClientInfoNames)
		row := clientPresence{ID: def.ID, DisplayName: def.Name, Kind: "supported", Icon: def.Icon, Installed: status.Exists, ConfigPath: status.ConfigPath, DisplayPath: status.DisplayPath, ReloadHint: def.ReloadHint, LastSeen: lastSeen}
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
			if withSessions {
				row.Sessions = append(row.Sessions, clientSession{ID: session.ID, WorkSessionID: session.WorkSessionID, StartedAt: session.StartTime, LastActivity: session.LastActivity})
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
			known[normalizeRawClientName(alias)] = true
		}
	}
	for _, session := range sessions {
		rawIdentity := normalizeRawClientName(session.ClientName)
		name := sanitizeOtherClientName(session.ClientName)
		if rawIdentity == "" || known[rawIdentity] {
			continue
		}
		if name == "" {
			name = "Unknown client"
		}
		id := otherClientID(name, rawIdentity)
		found := -1
		for i := range result {
			if result[i].ID == id {
				found = i
				break
			}
		}
		if found < 0 {
			result = append(result, clientPresence{ID: id, DisplayName: name, Kind: "other", State: "other"})
			found = len(result) - 1
		}
		if session.Status == "active" {
			result[found].ActiveSessions++
		}
		if result[found].LastSeen == nil || session.LastActivity.After(*result[found].LastSeen) {
			at := session.LastActivity
			result[found].LastSeen = &at
		}
		if withSessions {
			result[found].Sessions = append(result[found].Sessions, clientSession{ID: session.ID, WorkSessionID: session.WorkSessionID, StartedAt: session.StartTime, LastActivity: session.LastActivity})
		}
	}
	// An unknown client can have initialized without making a tool call, so it
	// has no session row. Preserve that evidence too, while deliberately
	// omitting configuration paths: MCP clientInfo has no trustworthy path.
	for name, seenAt := range state.ClientLastSeen {
		rawIdentity := normalizeRawClientName(name)
		name = sanitizeOtherClientName(name)
		if rawIdentity == "" || known[rawIdentity] {
			continue
		}
		if name == "" {
			name = "Unknown client"
		}
		id := otherClientID(name, rawIdentity)
		found := -1
		for i := range result {
			if result[i].ID == id {
				found = i
				break
			}
		}
		if found < 0 {
			result = append(result, clientPresence{ID: id, DisplayName: name, Kind: "other", State: "other"})
			found = len(result) - 1
		}
		if result[found].LastSeen == nil || seenAt.After(*result[found].LastSeen) {
			at := seenAt
			result[found].LastSeen = &at
		}
	}
	return result, nil
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

// sanitizeOtherClientName applies the same rendering boundary as persisted
// client presence: clientInfo.name is supplied by a peer and can otherwise
// inject terminal controls or produce unbounded rows from older session data.
func sanitizeOtherClientName(raw string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(raw)) {
		if r < 0x20 || r == 0x7f {
			continue
		}
		if b.Len()+len(string(r)) > 128 {
			break
		}
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}

// normalizeRawClientName is used only for identity and alias matching. It must
// run before display sanitization: stripping an escape prefix from an unknown
// client must not turn it into a trusted supported-client alias.
func normalizeRawClientName(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

// otherClientID keeps the client row stable without exposing an untrusted full
// name. The display prefix is bounded and a hash of the raw normalized identity
// prevents distinct long names that share a display prefix from collapsing.
func otherClientID(displayName, rawIdentity string) string {
	digest := sha256.Sum256([]byte(rawIdentity))
	return "other:" + displayName + "-" + hex.EncodeToString(digest[:12])
}
