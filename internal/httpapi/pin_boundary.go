package httpapi

import (
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Spec 118 (issue #1686): the profile pin is part of the visibility boundary.
//
// This file is THE seam through which the shared enumeration predicate learns
// the pin's reach (FR-020). auth.CanEnumerateServer takes only a context and
// has no configuration; the reach of a pin lives in the live configuration.
// So the two authentication paths that mint an agent token's AuthContext —
// handleAgentTokenAuth for every REST request and newSSECallerContextRefresher
// before every identity-bearing SSE frame — pass the context through
// bindCallerToPin before it reaches a handler, and from then on
// AuthContext.AllowedServers IS the caller's visible set (grant ∩ reach).
// Every door, every embed, every count and the grant-as-filter seams
// (scopeAllowedServers, the usage cache key) read that one set, and the one
// predicate caller outside this package (internal/management/diagnostics.go)
// is served by the same context. Nothing is cached on the credential or the
// connection beyond one request or one frame (FR-003).

// bindCallerToPin resolves a pinned agent token's visible set from the live
// configuration and returns the AuthContext to run the request under.
//
// Fail closed, twice: a configuration that cannot be read, and a pin naming
// no configured profile, an empty profile, or a profile with no overlap with
// the grant, all yield an empty visible set (FR-002) — the same shape as a
// token granted nothing. Administrators and unpinned callers pass through
// untouched (auth.AuthContext.BoundToPinReach).
func (s *Server) bindCallerToPin(ac *auth.AuthContext) *auth.AuthContext {
	if !authContextHasPin(ac) {
		return ac
	}
	cfg := s.controller.GetCurrentConfig()
	if cfg == nil {
		return ac.BoundToPinReach(nil)
	}
	return ac.BoundToPinReach(profileReach(cfg, ac.ProfilePin))
}

// profileReach is the reach of a pin (Spec 118 "Definitions"): the named
// profile's effective server set, restricted to configured servers. A name
// that matches no profile has nil reach.
func profileReach(cfg *config.Config, name string) []string {
	if cfg == nil || name == "" {
		return nil
	}
	for i := range cfg.Profiles {
		if cfg.Profiles[i].Name == name {
			return cfg.Profiles[i].EffectiveServers(cfg)
		}
	}
	return nil
}

// authContextHasPin reports whether an AuthContext belongs to a
// non-administrator bound to a profile (an agent token with a pin).
func authContextHasPin(ac *auth.AuthContext) bool {
	return ac != nil && !ac.IsAdmin() && ac.ProfilePin != ""
}
