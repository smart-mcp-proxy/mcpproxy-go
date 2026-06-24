package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// ProfileSummary is one entry of the GET /api/v1/profiles listing (Profiles v2 T2).
type ProfileSummary struct {
	Name      string   `json:"name"`
	Servers   []string `json:"servers"`
	ToolCount int      `json:"tool_count"`
}

// serverToolCounts builds a server-name → indexed-tool-count map from the
// controller's server listing, tolerating the numeric type the map value is
// decoded as.
func (s *Server) serverToolCounts() map[string]int {
	counts := map[string]int{}
	servers, err := s.controller.GetAllServers()
	if err != nil {
		return counts
	}
	for _, sv := range servers {
		name, _ := sv["name"].(string)
		if name == "" {
			continue
		}
		switch v := sv["tool_count"].(type) {
		case int:
			counts[name] = v
		case int64:
			counts[name] = int(v)
		case float64:
			counts[name] = int(v)
		}
	}
	return counts
}

// handleListProfiles godoc
// @Summary List configured profiles
// @Description List all configured profiles with their effective servers and indexed tool count (Profiles v2). A profile scopes tool discovery and calls to a named subset of upstream servers.
// @Tags profiles
// @Produce json
// @Security ApiKeyAuth
// @Security ApiKeyQuery
// @Success 200 {object} contracts.SuccessResponse "Profile list"
// @Failure 500 {object} contracts.ErrorResponse "Configuration unavailable"
// @Router /api/v1/profiles [get]
func (s *Server) handleListProfiles(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.controller.GetConfig()
	if err != nil || cfg == nil {
		s.writeError(w, r, http.StatusInternalServerError, "Configuration unavailable")
		return
	}

	toolCounts := s.serverToolCounts()
	out := make([]ProfileSummary, 0, len(cfg.Profiles))
	for i := range cfg.Profiles {
		eff := cfg.Profiles[i].EffectiveServers(cfg)
		tc := 0
		for _, name := range eff {
			tc += toolCounts[name]
		}
		out = append(out, ProfileSummary{
			Name:      cfg.Profiles[i].Name,
			Servers:   eff,
			ToolCount: tc,
		})
	}

	s.writeSuccess(w, map[string]interface{}{"profiles": out})
}

// handleGetActiveProfile godoc
// @Summary Get the default active profile
// @Description Get the server-level default active profile used by UI surfaces (Web UI / tray). Empty string means "all servers". Note: within a live MCP session, the set_profile tool selection takes precedence over this default.
// @Tags profiles
// @Produce json
// @Security ApiKeyAuth
// @Security ApiKeyQuery
// @Success 200 {object} contracts.SuccessResponse "Active profile"
// @Router /api/v1/profiles/active [get]
func (s *Server) handleGetActiveProfile(w http.ResponseWriter, _ *http.Request) {
	s.activeProfileMu.RLock()
	active := s.activeProfile
	s.activeProfileMu.RUnlock()
	s.writeSuccess(w, map[string]interface{}{"active_profile": active})
}

// SetActiveProfileRequest is the body of PUT /api/v1/profiles/active. Either
// "profile" or "active_profile" may be supplied; an empty string clears the
// default selection (back to all servers).
type SetActiveProfileRequest struct {
	Profile       *string `json:"profile,omitempty"`
	ActiveProfile *string `json:"active_profile,omitempty"`
}

// handleSetActiveProfile godoc
// @Summary Set the default active profile
// @Description Set the server-level default active profile for UI surfaces. The slug must match a configured profile; pass an empty string to clear. This does not affect live MCP sessions, which use the set_profile tool.
// @Tags profiles
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Security ApiKeyQuery
// @Param body body SetActiveProfileRequest true "Profile slug to activate (empty clears)"
// @Success 200 {object} contracts.SuccessResponse "Active profile updated"
// @Failure 400 {object} contracts.ErrorResponse "Invalid request body"
// @Failure 404 {object} contracts.ErrorResponse "Unknown profile"
// @Router /api/v1/profiles/active [put]
func (s *Server) handleSetActiveProfile(w http.ResponseWriter, r *http.Request) {
	var req SetActiveProfileRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, r, http.StatusBadRequest, "Invalid request body")
		return
	}

	slug := ""
	switch {
	case req.Profile != nil:
		slug = strings.TrimSpace(*req.Profile)
	case req.ActiveProfile != nil:
		slug = strings.TrimSpace(*req.ActiveProfile)
	}

	if slug != "" {
		cfg, err := s.controller.GetConfig()
		if err != nil || cfg == nil {
			s.writeError(w, r, http.StatusInternalServerError, "Configuration unavailable")
			return
		}
		found := false
		for i := range cfg.Profiles {
			if cfg.Profiles[i].Name == slug {
				found = true
				break
			}
		}
		if !found {
			s.writeError(w, r, http.StatusNotFound, fmt.Sprintf("unknown profile '%s'", slug))
			return
		}
	}

	s.activeProfileMu.Lock()
	s.activeProfile = slug
	s.activeProfileMu.Unlock()

	s.getRequestLogger(r).Infow("default active profile updated", "profile", slug)
	s.writeSuccess(w, map[string]interface{}{"active_profile": slug})
}
