package server

import (
	"context"

	mcpserver "github.com/mark3labs/mcp-go/server"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
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
// request, or "" when the token is unpinned. The pin is set on the AuthContext
// by the MCP auth middleware after validating an agent token (Profiles v2 T3,
// Spec 028). Only agent-token contexts can carry a pin; admin / user / tray
// contexts are never pinned.
func profilePinFromContext(ctx context.Context) string {
	if ac := auth.AuthContextFromContext(ctx); ac != nil && ac.Type == auth.AuthTypeAgent {
		return ac.ProfilePin
	}
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

// effectiveToolResponseMode resolves the retrieve_tools serialization mode
// for one call (Spec 085 FR-001/FR-005/FR-015). Precedence: per-call `detail`
// override > configured tool_response_mode > full. It reads the LIVE config
// snapshot via currentConfig() — never the construction-time p.config the
// retrieve path historically used — so a hot-reload (config file or API
// apply) changes the resolved mode on the very next call, without
// reconstructing the server. Unknown detail values fall through to the
// configured mode (the tool schema's enum is the real gate).
func (p *MCPProxyServer) effectiveToolResponseMode(detail string) string {
	if detail == config.ToolResponseModeFull || detail == config.ToolResponseModeCompact {
		return detail
	}
	if cfg := p.currentConfig(); cfg != nil && cfg.ToolResponseMode != "" {
		return cfg.ToolResponseMode
	}
	return config.ToolResponseModeFull
}

// effectiveDirectToolResponseMode resolves the DIRECT enumeration surface's
// serialization mode for one rebuild (Spec 102 FR-001). A separate axis from
// effectiveToolResponseMode above — that one governs retrieve_tools, this one
// governs /mcp/all (and /mcp plus the legacy aliases under routing_mode
// "direct") — and there is no per-call override to weigh, because the direct
// surface exposes no `detail` argument.
//
// Like its sibling it reads the LIVE snapshot via currentConfig(), never the
// construction-time p.config: the mode the catalog was built with is what
// FR-014's hot-reload guard later compares against, so a renderer reading a
// stale config would make that comparison meaningless. The empty value is the
// documented default (config.go's DirectToolResponseModeFull) — nothing in the
// non-test tree ever assigns it — so "" must resolve to full rather than fall
// through to an unknown-mode branch.
func (p *MCPProxyServer) effectiveDirectToolResponseMode() string {
	if cfg := p.currentConfig(); cfg != nil && cfg.DirectToolResponseMode != "" {
		return cfg.DirectToolResponseMode
	}
	return config.DirectToolResponseModeFull
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
	// 1. Agent-token pin (T3). When present it is authoritative and bounds
	//    everything below — including the case where the pinned profile has been
	//    removed from config since the token was minted.
	//
	//    A stale pin resolves to a DENY-ALL scope that keeps the removed
	//    profile's name, never to the next resolver tier. Falling through would
	//    hand the session the token's own (wider) server scope — precisely the
	//    privilege widening the pin exists to prevent — and it would do so
	//    silently, from an operator action (deleting a profile) that reads as a
	//    restriction. This mirrors resolvePreflightScope
	//    (internal/server/preflight_glue.go), which intersects an unresolvable
	//    pin against an empty server set for the same reason, so the session and
	//    preflight paths cannot disagree about what a pinned token may see.
	if pin := profilePinFromContext(ctx); pin != "" {
		if scope := p.profileScopeForSlug(pin); scope != nil {
			return pin, scope
		}
		if p.logger != nil {
			p.logger.Warn("agent-token profile_pin no longer matches any configured profile; resolving to a deny-all scope",
				zap.String("profile_pin", pin))
		}
		return pin, profile.NewProfileScope(pin, nil)
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
