package httpapi

import (
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
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
	rows, err := s.clientPresence(false)
	if err != nil {
		s.writeError(w, r, http.StatusServiceUnavailable, err.Error())
		return
	}
	s.writeSuccess(w, clientsResponse{Clients: rows})
}

func (s *Server) handleGetClient(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "client")
	rows, err := s.clientPresence(true)
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

func (s *Server) clientPresence(withSessions bool) ([]clientPresence, error) {
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
	sessions, _, err := s.controller.GetRecentSessions(100, "")
	if err != nil {
		return nil, err
	}
	result := make([]clientPresence, 0, len(statuses))
	for _, def := range connect.GetAllClients() {
		status := statuses[def.ID]
		lastSeen := latestClientSeen(state.ClientLastSeen, def.ClientInfoNames)
		row := clientPresence{ID: def.ID, DisplayName: def.Name, Kind: "supported", Icon: def.Icon, Installed: status.Exists, ConfigPath: status.ConfigPath, DisplayPath: status.DisplayPath, ReloadHint: def.ReloadHint, LastSeen: lastSeen}
		connectedAt := state.ClientConnectedAt[def.ID]
		row.Connected = !connectedAt.IsZero() || lastSeen != nil
		if lastSeen != nil {
			row.State = "connected_seen"
		} else if !connectedAt.IsZero() {
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
			if session.Status == "active" {
				row.ActiveSessions++
			}
			if withSessions {
				row.Sessions = append(row.Sessions, clientSession{ID: session.ID, WorkSessionID: session.WorkSessionID, StartedAt: session.StartTime, LastActivity: session.LastActivity})
			}
		}
		result = append(result, row)
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
