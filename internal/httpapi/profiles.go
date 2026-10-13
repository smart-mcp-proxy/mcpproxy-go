package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// setActiveProfileDeprecation marks /profiles/active deprecated (Spec 108
// FR-039): a client reads the header, the behaviour is unchanged. The
// successor is the profile listing; the route is removed in a later minor.
func setActiveProfileDeprecation(w http.ResponseWriter) {
	w.Header().Set("Deprecation", "true")
	w.Header().Set("Link", `</api/v1/profiles>; rel="successor-version"`)
}

// handleGetActiveProfile godoc
// @Summary Get the default active profile
// @Description Deprecated (Spec 108 FR-039; sends a Deprecation header, successor GET /api/v1/profiles). Get the server-level default active profile used by UI surfaces (Web UI / tray). Empty string means "all servers". Note: within a live MCP session, the set_profile tool selection takes precedence over this default.
// @Tags profiles
// @Produce json
// @Security ApiKeyAuth
// @Security ApiKeyQuery
// @Success 200 {object} contracts.SuccessResponse "Active profile"
// @Router /api/v1/profiles/active [get]
func (s *Server) handleGetActiveProfile(w http.ResponseWriter, r *http.Request) {
	setActiveProfileDeprecation(w)
	s.activeProfileMu.RLock()
	active := s.activeProfile
	s.activeProfileMu.RUnlock()

	// Spec 107 T085: a tenant session principal reads the hidden active
	// profile back as "" (same omission handleListProfiles applies), never
	// the real slug — the slug's existence is not itself something she may
	// see when every server it resolves to is outside her entitlement.
	if active != "" {
		ac := auth.AuthContextFromContext(r.Context())
		if ac.IsSessionPrincipal() && !ac.IsAdmin() {
			cfg, err := s.controller.GetConfig()
			// Fail CLOSED, not open: a config-read failure must not answer
			// with the real slug just because visibility could not be
			// checked (cross-review round 3 — handleListProfiles already
			// refuses outright on the same error; this door has no such
			// escape hatch, so it clears the value instead).
			if err != nil || cfg == nil || !profileReachable(r.Context(), cfg, active) {
				active = ""
			}
		}
	}

	s.writeSuccess(w, map[string]interface{}{"active_profile": active})
}

// profileReachable reports whether the named profile's effective server
// set has non-empty intersection with the caller's entitlement (Spec 107
// T085) — the same rule handleListProfiles applies per row.
func profileReachable(ctx context.Context, cfg *config.Config, name string) bool {
	for i := range cfg.Profiles {
		if cfg.Profiles[i].Name != name {
			continue
		}
		for _, srv := range cfg.Profiles[i].EffectiveServers(cfg) {
			if canSeeServer(ctx, srv) {
				return true
			}
		}
		return false
	}
	return false
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
// @Description Deprecated (Spec 108 FR-039; sends a Deprecation header, successor GET /api/v1/profiles). Set the server-level default active profile for UI surfaces. The slug must match a configured profile; pass an empty string to clear. This does not affect live MCP sessions, which use the set_profile tool.
// @Tags profiles
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Security ApiKeyQuery
// @Param body body SetActiveProfileRequest true "Profile slug to activate (empty clears)"
// @Success 200 {object} contracts.SuccessResponse "Active profile updated"
// @Failure 400 {object} contracts.ErrorResponse "Invalid request body"
// @Failure 403 {object} contracts.ErrorResponse "Forbidden (agent tokens cannot change the active profile)"
// @Failure 404 {object} contracts.ErrorResponse "Unknown profile"
// @Router /api/v1/profiles/active [put]
func (s *Server) handleSetActiveProfile(w http.ResponseWriter, r *http.Request) {
	setActiveProfileDeprecation(w)
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
	changed := s.activeProfile != slug
	s.activeProfile = slug
	s.activeProfileMu.Unlock()

	// Notify other clients (Web UI, tray) via SSE only on an actual change, so a
	// switch made here is reflected everywhere (Profiles v2 T5). Emitted through
	// an optional capability assertion to avoid widening ServerController.
	if changed {
		if emitter, ok := s.controller.(interface{ EmitActiveProfileChanged(string) }); ok {
			emitter.EmitActiveProfileChanged(slug)
		}
	}

	s.getRequestLogger(r).Infow("default active profile updated", "profile", slug)
	s.writeSuccess(w, map[string]interface{}{"active_profile": slug})
}
