package server

import (
	"context"

	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
)

// sessionIDFromContext returns the stable mcp-go per-session id available at
// tool-call time, or "" when no session is bound to the request. This is the
// id confirmed (Profiles v2 T2 first subtask) to be exposed by mcp-go via
// server.ClientSessionFromContext for both streamable-HTTP and SSE transports;
// no synthetic fallback is required.
func sessionIDFromContext(ctx context.Context) string {
	if sess := mcpserver.ClientSessionFromContext(ctx); sess != nil {
		return sess.SessionID()
	}
	return ""
}

// profilePinFromContext returns the agent-token profile_pin bound to the
// request, or "" when the token is unpinned. Profiles v2 T3 (per-agent-token
// profile_pin, Spec 028) will populate this from the auth context; until then
// the hook is intentionally inert and always returns "", so the precedence
// chain below degrades to URL > session > none.
func profilePinFromContext(_ context.Context) string {
	return ""
}

// currentConfig returns the live configuration snapshot (hot-reload safe),
// falling back to the construction-time config if the runtime is unavailable.
func (p *MCPProxyServer) currentConfig() *config.Config {
	if p.mainServer != nil && p.mainServer.runtime != nil {
		if cfg := p.mainServer.runtime.Config(); cfg != nil {
			return cfg
		}
	}
	return p.config
}

// profileScopeForSlug builds a ProfileScope for the named profile from the live
// config, or returns nil when the slug does not match a configured profile.
func (p *MCPProxyServer) profileScopeForSlug(slug string) *profile.ProfileScope {
	if slug == "" {
		return nil
	}
	cfg := p.currentConfig()
	if cfg == nil {
		return nil
	}
	for i := range cfg.Profiles {
		if cfg.Profiles[i].Name == slug {
			return profile.NewProfileScope(slug, cfg.Profiles[i].EffectiveServers(cfg))
		}
	}
	return nil
}

// resolveActiveProfile computes the effective profile for the current request,
// applying the Profiles v2 resolution precedence (highest wins):
//
//  1. agent-token profile_pin   — server-enforced (T3 hook; "" until then)
//  2. URL /mcp/p/<slug> scope    — explicit, per-request override of the session default
//  3. set_profile session state  — the base /mcp endpoint default for the session lifetime
//  4. none                       — nil scope ⇒ no profile filtering (admin / all servers)
//
// It returns the resolved profile slug ("" when none) and the matching
// ProfileScope ("" ⇒ nil). A session selection that no longer matches any
// configured profile is treated as stale: it is cleared and resolution falls
// through to "none".
func (p *MCPProxyServer) resolveActiveProfile(ctx context.Context) (string, *profile.ProfileScope) {
	// 1. Agent-token pin (T3 hook). When present it is authoritative and bounds
	//    everything below; a non-matching pin slug falls through (defensive).
	if pin := profilePinFromContext(ctx); pin != "" {
		if scope := p.profileScopeForSlug(pin); scope != nil {
			return pin, scope
		}
	}

	// 2. Explicit URL profile (Spec 057). Authoritative for this request, so it
	//    overrides any stored session selection on the same connection.
	if urlScope := profile.ProfileScopeFromContext(ctx); urlScope != nil {
		return urlScope.Name, urlScope
	}

	// 3. Session selection set via the set_profile tool on the base /mcp endpoint.
	if p.sessionStore != nil {
		if sid := sessionIDFromContext(ctx); sid != "" {
			if name := p.sessionStore.GetActiveProfile(sid); name != "" {
				if scope := p.profileScopeForSlug(name); scope != nil {
					return name, scope
				}
				// Stored profile vanished from config — drop the stale selection.
				p.sessionStore.SetActiveProfile(sid, "")
			}
		}
	}

	// 4. No profile in effect.
	return "", nil
}
