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

// profileRequestIndexKey is an unexported context key for the (index,
// snapshot) PAIR a /mcp/p/<slug> request was admitted and scoped against
// (profileMiddleware / serveProfileURL). It is a package-private companion
// to profile.WithProfileScope, not exported through the profile package,
// because both the writer (Server) and the reader (MCPProxyServer) already
// live in this package.
//
// Carrying the pair — not merely its cfg — lets every downstream profile
// decision on this request, set_profile's admission included, reuse the
// index the gate already built rather than re-resolving cfg's own index
// through a second Published() lookup: a request that paused between
// admission and here must still decide with the exact index it was
// admitted against, never one a later publication's Published() would hand
// back for the live runtime.Config() read at that later moment (Spec 105 PR
// D review round 9, MUST-FIX 1 — set_profile on /mcp/p/<slug> read its own
// independent runtime.Config() and could therefore admit or scope against a
// config one reload ahead of, or behind, the one the URL gate used).
type profileRequestIndexKey struct{}

// withProfileRequestIndex returns a context carrying idx as the exact
// (index, snapshot) pair downstream profile resolution on this request must
// decide over.
func withProfileRequestIndex(ctx context.Context, idx *profileIndex) context.Context {
	return context.WithValue(ctx, profileRequestIndexKey{}, idx)
}

// profileRequestIndexFromContext returns the pair withProfileRequestIndex
// injected, or (nil, false) when the request did not enter through a path
// that pins one (e.g. the base /mcp endpoint, or a bare test server).
func profileRequestIndexFromContext(ctx context.Context) (*profileIndex, bool) {
	idx, ok := ctx.Value(profileRequestIndexKey{}).(*profileIndex)
	return idx, ok
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
	return profileScopeForSlugIn(p.currentConfig(), slug)
}

// profileScopeForSlugIn is profileScopeForSlug against an explicit config
// snapshot, for callers that must not re-read the live config between a
// check and the scope they build from it.
func profileScopeForSlugIn(cfg *config.Config, slug string) *profile.ProfileScope {
	if slug == "" || cfg == nil {
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
// through to "none". Resolution reads the live config snapshot once — unless
// the request came in through /mcp/p/<slug>, in which case it decides with the
// exact (index, snapshot) PAIR profileMiddleware already admitted the request
// against (profileRequestIndexFromContext), consumed directly via
// resolveActiveProfileFromIndex — never a fresh runtime.Config() read, and
// never a second, independent index lookup of its own. Extracting only the
// pair's cfg and handing it to resolveActiveProfileIn (which resolves the
// index again through profileIndexFor(cfg): an O(1) Published(cfg) match that
// falls back to a fleet-sized For(cfg) build on a miss) would let a request
// that paused across two publications between admission and this call land on
// a snapshot neither of profileIndexFor's two warmed slots covers any more —
// exactly the pair-acquisition bypass rounds 11/13 closed on the admission
// path (profileIndexCurrent/Acquire), reopened here on the downstream
// resolution path a paused request reaches next (round 15 MUST-FIX). Using
// the already-resolved pair outright cannot miss: there is no lookup to fall
// back from. Callers that hold only a snapshot — never an admitted pair —
// use resolveActiveProfileIn, which still resolves the index via
// profileIndexFor(cfg).
func (p *MCPProxyServer) resolveActiveProfile(ctx context.Context) (string, *profile.ProfileScope) {
	if injected, ok := profileRequestIndexFromContext(ctx); ok {
		return p.resolveActiveProfileFromIndex(ctx, injected)
	}
	return p.resolveActiveProfileIn(ctx, p.currentConfig())
}

// resolveActiveProfileIn is resolveActiveProfile against an explicit config
// snapshot. handleSetProfile admits a selection against one snapshot and must
// report the effective scope from that same snapshot: re-reading the live
// config here would let a hot reload between the two hand back a payload whose
// `active_profile` and `servers` disagree (or drop the just-stored selection
// as stale) — Spec 105 PR D critique round 1.
//
// Every tier below that resolves a profile by name (pin, session selection)
// does it through profileIndexFor(cfg)'s precomputed serverPos/members data
// (EffectiveServersFor) instead of profileScopeForSlugIn, which rebuilds a
// fleet-sized "known servers" set on every single call. resolveActiveProfile
// runs on every admitted scoped READ (retrieve_tools, describe_tool,
// call_tool_*, code_execution — every consumer of resolveActiveProfile in
// mcp.go), so that rebuild previously happened once per pinned or
// session-profiled call, at the fleet's cost, not the pin/session profile's
// own: identical output, no timing promise broken, at O(profile size)
// instead (Spec 105 PR D review round 14 MUST-FIX; the wildcard grant here
// is deliberate — this tier renders the profile's OWN full membership, never
// intersected with the caller's AllowedServers, exactly as
// profileScopeForSlugIn always did; a caller-specific view is applied
// separately downstream, e.g. handleSetProfile's own EffectiveServersFor
// call and callerVisibleServers' pre-105 equivalent).
func (p *MCPProxyServer) resolveActiveProfileIn(ctx context.Context, cfg *config.Config) (string, *profile.ProfileScope) {
	return p.resolveActiveProfileFromIndex(ctx, p.profileIndexFor(cfg))
}

// resolveActiveProfileFromIndex is resolveActiveProfileIn over an ALREADY
// resolved (index, snapshot) pair — the seam handleSetProfile uses to
// finish its own admission and payload rendering on the EXACT SAME pair
// profileIndexCurrent gave it, rather than resolving cfg's index a second,
// independent time. Two different lookups of "the index for this cfg" can
// legitimately disagree: profileIndexCurrent's bare-proxy fallback caches by
// cfg's pointer identity (TestHandleSetProfile_ScopedRefusalTouchesOnlySlug-
// AndPin relies on exactly that to reuse a manually warmed index), while a
// test config a caller mutates IN PLACE between calls (cfg.Profiles = nil to
// simulate a deleted profile, never replacing the *config.Config pointer —
// TestSetProfileClearReportsPinnedScope,
// TestReadCache_DeletedPinnedProfileRevokesCachedAccess) makes that cached
// index's positions describe a Profiles slice the pointer no longer holds.
// Resolving the name and rendering its servers from two independently
// (re-)resolved indices could therefore pair a stale position with the
// current (shorter) Profiles slice and index out of range; resolving both
// from the ONE pair the caller already has cannot.
func (p *MCPProxyServer) resolveActiveProfileFromIndex(ctx context.Context, idx *profileIndex) (string, *profile.ProfileScope) {
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
		if scope := profileScopeFromIndex(idx, pin); scope != nil {
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
				if scope := profileScopeFromIndex(idx, name); scope != nil {
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

// profileScopeFromIndex builds the ProfileScope for slug's FULL membership
// (declared servers ∩ configured servers, unintersected with any caller
// credential — see resolveActiveProfileIn's doc comment) from idx, or nil
// when idx's snapshot has no such profile. It is profileScopeForSlugIn's
// O(profile size) counterpart, resolving through the index's precomputed
// data instead of rebuilding a fleet-sized set on every call.
func profileScopeFromIndex(idx *profileIndex, slug string) *profile.ProfileScope {
	if idx == nil || idx.cfg == nil {
		return nil
	}
	candidate := idx.position(slug)
	if candidate < 0 {
		return nil
	}
	return profile.NewProfileScope(slug, idx.effectiveServersForCandidate(candidate, []string{"*"}))
}
