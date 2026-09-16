package server

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
)

// buildSetProfileTool constructs the set_profile MCP tool definition (Profiles
// v2 T2). Factored out so it can be registered on the default server and every
// routing-mode server (call-tool / code-exec) from one source of truth.
//
// The wording below is snapshotted byte-for-byte by the spec-098 FR-015
// tools/list goldens (testdata/toolslist_goldens/), which were captured from
// the pre-098 merge base — editing this text is an intentional MCP-surface
// change that must regenerate them, and is deliberately NOT bundled with
// unrelated fixes. One nuance it therefore still understates: "back to all
// servers" is bounded by the caller's credential, so a profile-pinned token
// that clears its session selection stays inside its pin (handleSetProfile
// reports the pinned scope, not every server).
func buildSetProfileTool() mcp.Tool {
	return mcp.NewTool("set_profile",
		mcp.WithDescription("Switch the active profile for THIS session. A profile scopes tool discovery "+
			"(retrieve_tools) and tool calls to a named subset of upstream servers — useful to focus an agent "+
			"on one task domain (e.g. 'research', 'deploy'). The selection persists for the lifetime of the "+
			"current MCP session and applies to subsequent retrieve_tools / call_tool_* / code_execution calls "+
			"on the base /mcp endpoint without re-indexing. Pass an empty string to clear the selection and go "+
			"back to all servers. Note: an explicit /mcp/p/<slug> URL still overrides the session profile for "+
			"that request, and a profile-pinned agent token cannot switch away from its pinned profile."),
		mcp.WithTitleAnnotation("Set Profile"),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithOpenWorldHintAnnotation(false),
		mcp.WithString("profile",
			mcp.Description("Profile slug to activate for this session (e.g. 'research'). Pass \"\" (empty) to "+
				"clear the active profile and return to all servers."),
		),
	)
}

// handleSetProfile implements the set_profile tool. It validates the requested
// slug against ONE live-config snapshot, records it on the session
// (mutex-guarded, cleared on session close), and returns {active_profile,
// servers} where `active_profile` is the STORED session selection and
// `servers` is what the session can reach after the update.
//
// For a scoped caller (agent token) `servers` is the EFFECTIVE scope —
// resolveActiveProfileIn (pin > URL > session) over the same snapshot,
// intersected with the credential (Spec 105 FR-003): on a URL-scoped endpoint
// the URL governs the reported servers, and clearing a pinned token's
// selection reports active_profile == "" while servers still reports the
// pin's reach. Administrators (API key, socket, anonymous back-compat) keep
// the pre-105 payload byte-for-byte — the selected profile's servers, or every
// configured server on clear — because SC-005 names no FR-003 exception for
// them (an administrator is never pinned, so only the URL tier could differ).
func (p *MCPProxyServer) handleSetProfile(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	slug := strings.TrimSpace(request.GetString("profile", ""))

	sessionID := sessionIDFromContext(ctx)
	if sessionID == "" {
		return mcp.NewToolResultError("set_profile requires an active MCP session; no session id is bound to this request"), nil
	}

	cfg := p.currentConfig()

	// Profiles v2 T3: a profile-pinned agent token may not switch away from its
	// pinned profile.
	pin := profilePinFromContext(ctx)
	if pin != "" && slug != "" && slug != pin {
		return mcp.NewToolResultError(fmt.Sprintf("agent token is pinned to profile '%s' and cannot switch to '%s'", pin, slug)), nil
	}

	// A non-empty slug must name a configured profile the caller may select
	// (an empty slug clears the selection and is always accepted). The
	// selectable set is computed BEFORE any session mutation or success log, so
	// a profile outside the caller's reach — including a pinned token's own
	// pin once it has zero reach (research D1) — is indistinguishable from an
	// unknown one (FR-016b / FR-003): same error, same `available:` list, no
	// state change.
	if slug != "" {
		selectable := selectableProfileNames(ctx, cfg)
		if !slices.Contains(selectable, slug) {
			return mcp.NewToolResultError(fmt.Sprintf("unknown profile '%s' (available: %s)", slug, strings.Join(selectable, ", "))), nil
		}
	}

	p.sessionStore.SetActiveProfile(sessionID, slug)
	if slug != "" {
		p.logger.Info("set_profile: session profile updated",
			zap.String("session_id", sessionID),
			zap.String("profile", slug),
		)
	}

	// SC-005: administrators report the stored selection's own servers.
	if !auth.IsScopedCaller(ctx) {
		return setProfileResult(slug, profileServersIn(cfg, slug))
	}

	// Scoped caller: report what the session can actually reach after the
	// update — the resolver's effective profile (a pin outranks the URL, which
	// outranks the stored selection; a deleted pin is deny-all) bounded by the
	// credential — never the stored selection's own servers when something
	// else governs. Same snapshot as the admission check above.
	_, effective := p.resolveActiveProfileIn(ctx, cfg)
	return setProfileResult(slug, callerVisibleServers(ctx, scopeServersIn(cfg, effective)))
}

// profileServersIn renders the pre-105 set_profile server list for a stored
// selection: the named profile's effective servers in profile-declared order
// (duplicates kept, exactly as EffectiveServers returns them), every
// configured server in config order for an empty slug, and nothing for a slug
// cfg does not know.
func profileServersIn(cfg *config.Config, slug string) []string {
	if slug == "" {
		return allServerNames(cfg)
	}
	if cfg == nil {
		return nil
	}
	for i := range cfg.Profiles {
		if cfg.Profiles[i].Name == slug {
			return cfg.Profiles[i].EffectiveServers(cfg)
		}
	}
	return nil
}

// scopeServersIn renders a resolved ProfileScope as a deterministic server
// list: nil scope ⇒ every configured server (config order); otherwise the
// scope's profile in its declared order filtered by the scope, so the payload
// carries the order every other EffectiveServers consumer uses rather than
// map-iteration order. A scope whose profile cfg no longer names (a deleted
// pin — deny-all — or a URL scope built from an older snapshot) falls back to
// the scope's own set, sorted.
func scopeServersIn(cfg *config.Config, scope *profile.ProfileScope) []string {
	if scope == nil {
		return allServerNames(cfg)
	}
	declared := profileServersIn(cfg, scope.Name)
	if declared == nil {
		names := scope.AllowedServerNames()
		slices.Sort(names)
		return names
	}
	out := make([]string, 0, len(declared))
	for _, name := range declared {
		if scope.Allows(name) {
			out = append(out, name)
		}
	}
	return out
}

// setProfileResult renders the standard set_profile success payload.
func setProfileResult(activeProfile string, servers []string) (*mcp.CallToolResult, error) {
	if servers == nil {
		servers = []string{}
	}
	payload := map[string]interface{}{
		"active_profile": activeProfile,
		"servers":        servers,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to encode set_profile result: %v", err)), nil
	}
	return mcp.NewToolResultText(string(body)), nil
}

// allServerNames returns the names of every configured server (the "all
// servers" set returned when a profile selection is cleared).
func allServerNames(cfg *config.Config) []string {
	if cfg == nil {
		return nil
	}
	names := make([]string, 0, len(cfg.Servers))
	for _, s := range cfg.Servers {
		if s != nil {
			names = append(names, s.Name)
		}
	}
	return names
}

// callerVisibleServers filters a server list through the caller's credential.
// set_profile's payload advertises what the session can reach, so a token
// restricted to server A must never be told about server B — whether the list
// is "all servers" (cleared selection), a profile's full set, or a pinned
// profile's scope (Spec 104 FR-016b).
//
// The predicate is auth.CanEnumerateServer — the same CanAccessServer rule
// serverInScope applies to retrieve_tools / describe_tool — so this surface
// cannot disagree with visibility: admin (API-key / socket / anonymous
// back-compat) and absent contexts pass everything through untouched; any
// non-admin context (agent token, server-edition user) keeps only the servers
// its AllowedServers names, "*" allows all, and an EMPTY list grants nothing.
func callerVisibleServers(ctx context.Context, servers []string) []string {
	if !auth.IsScopedCaller(ctx) {
		return servers
	}
	out := make([]string, 0, len(servers))
	for _, s := range servers {
		if auth.CanEnumerateServer(ctx, s) {
			out = append(out, s)
		}
	}
	return out
}

// selectableProfileNames returns the profile slugs the caller may select. An
// unrestricted caller may select any configured profile; a profile-pinned
// token only its pin, and only while the pin has reach (it exists and its
// server set intersects the token's allowed servers — a deleted pin, an empty
// or ghost profile and a disjoint grant all yield nothing, Spec 105 research
// D1); a scoped caller only the profiles that overlap the servers it can
// enumerate.
//
// This is both the `available:` list of the unknown-slug error and the
// admission rule for a selection — on set_profile AND on the /mcp/p/<slug>
// URL (profileMiddleware): a profile entirely outside the caller's reach is
// treated exactly like a nonexistent one, so the error text cannot be used to
// confirm which profiles the operator has configured (FR-016b, FR-003/004).
//
// The result is accumulated by forEachProfileSelectable in configured order;
// see there for why it never returns early.
func selectableProfileNames(ctx context.Context, cfg *config.Config) []string {
	if cfg == nil {
		return nil
	}
	names := make([]string, 0, len(cfg.Profiles))
	forEachProfileSelectable(ctx, cfg, func(name string, selectable bool) {
		if selectable {
			names = append(names, name)
		}
	})
	return names
}

// forEachProfileSelectable visits EVERY configured profile, in configured
// order, and reports to visit whether the caller may select it (the rule
// documented on selectableProfileNames).
//
// It deliberately has no early return and does the same per-profile work
// whatever the outcome: a scoped caller's reach is computed for each profile
// even when a pin already rules it out, and a pinned caller keeps walking
// after its pin is found. A refusal must be non-disclosing in status, body
// AND timing class (spec Definitions; FR-003/004), and profileMiddleware /
// handleSetProfile consult this predicate before refusing — a version that
// answered after one iteration for a live pin and after the whole slice for
// a deleted or zero-reach pin let a pinned caller tell those apart by the
// work its own refusal cost (codex review, PR D round 1).
func forEachProfileSelectable(ctx context.Context, cfg *config.Config, visit func(name string, selectable bool)) {
	if cfg == nil {
		return
	}
	pin := profilePinFromContext(ctx)
	// Administrators (and absent contexts) select any configured profile,
	// including empty or ghost ones (SC-005); everyone else needs reach.
	needsReach := pin != "" || auth.IsScopedCaller(ctx)
	for i := range cfg.Profiles {
		p := &cfg.Profiles[i]
		selectable := true
		if needsReach {
			selectable = profileHasReach(ctx, cfg, p)
		}
		if pin != "" && p.Name != pin {
			selectable = false
		}
		visit(p.Name, selectable)
	}
}

// profileHasReach reports whether the caller can enumerate at least one of
// p's declared servers that exists in cfg — the reach rule behind
// selectableProfileNames (research D1), i.e.
// len(callerVisibleServers(ctx, p.EffectiveServers(cfg))) > 0, but without
// either allocation, so a profile's reach costs the same whether it has one
// declared server or none. It walks every configured server (a per-snapshot
// constant) and never returns early; a nil p is an empty profile. Only the
// declared-server membership test scales with p's own list — a property of
// the profile the caller asked about, not of the rest of the fleet.
func profileHasReach(ctx context.Context, cfg *config.Config, p *config.ProfileConfig) bool {
	if cfg == nil {
		return false
	}
	var declared []string
	if p != nil {
		declared = p.Servers
	}
	scoped := auth.IsScopedCaller(ctx)
	reach := false
	for _, s := range cfg.Servers {
		if s == nil || !slices.Contains(declared, s.Name) {
			continue
		}
		if !scoped || auth.CanEnumerateServer(ctx, s.Name) {
			reach = true
		}
	}
	return reach
}

// profileIndex is an immutable slug → profile index over ONE config snapshot.
// It lets the /mcp/p/<slug> gate resolve the requested profile (and a pinned
// caller's pin) directly instead of walking cfg.Profiles, so the work a
// scoped refusal costs does not grow with the number of OTHER profiles the
// operator has configured — the whole selectable list is fleet-sized, and a
// refusal that computed it did zero iterations over an empty fleet and one
// EffectiveServers per profile over a populated one: same status and body,
// fleet-population timing oracle (codex review, PR D round 2; FR-004).
//
// Duplicate slugs cannot load (ValidateProfiles), but a hand-built config may
// carry them: the first occurrence wins, exactly like every linear lookup in
// this package (profileServersIn, the middleware's own scan).
type profileIndex struct {
	cfg    *config.Config
	byName map[string]int // slug → position in cfg.Profiles

	// lookupHook, when set, observes every slug the index resolves. It is the
	// seam the traversal-counter test uses to prove the gate touches at most
	// the requested slug and the pin; nil in production.
	lookupHook func(slug string)
}

func newProfileIndex(cfg *config.Config) *profileIndex {
	idx := &profileIndex{cfg: cfg, byName: map[string]int{}}
	if cfg == nil {
		return idx
	}
	idx.byName = make(map[string]int, len(cfg.Profiles))
	for i := range cfg.Profiles {
		if _, dup := idx.byName[cfg.Profiles[i].Name]; !dup {
			idx.byName[cfg.Profiles[i].Name] = i
		}
	}
	return idx
}

// lookup resolves one slug in O(1), or nil when the snapshot has no such
// profile.
func (idx *profileIndex) lookup(slug string) *config.ProfileConfig {
	if idx.lookupHook != nil {
		idx.lookupHook(slug)
	}
	if i, ok := idx.byName[slug]; ok {
		return &idx.cfg.Profiles[i]
	}
	return nil
}

// selectable reports whether the caller may select the profile named slug —
// the rule forEachProfileSelectable applies to every profile, evaluated for
// this ONE profile. It is the URL gate's predicate and must never consult the
// selectable list: whatever the outcome (slug absent, profile out of reach,
// pin deleted, pin zero-reach, pin mismatch, empty fleet) it does the same
// work — one lookup of the slug, one lookup of the pin when the caller is
// pinned, and exactly one allocation-free reach computation, over the
// candidate profile or an empty placeholder when there is none — so no
// branch can be told from another by its cost, and none of it depends on
// how many other profiles exist.
func (idx *profileIndex) selectable(ctx context.Context, slug string) bool {
	candidate := idx.lookup(slug)
	pin := profilePinFromContext(ctx)
	if pin != "" {
		// The pin is the only profile a pinned caller may select; resolve it
		// whether or not the URL named it so a mismatch costs what a match does.
		pinned := idx.lookup(pin)
		candidate = nil
		if slug == pin {
			candidate = pinned
		}
	}
	reach := profileHasReach(ctx, idx.cfg, candidate)
	// Administrators (and absent contexts) select any configured profile,
	// including empty or ghost ones (SC-005); everyone else needs reach.
	needsReach := pin != "" || auth.IsScopedCaller(ctx)
	return candidate != nil && (!needsReach || reach)
}

// profileIndexCache hands out the profileIndex for a config snapshot, built
// once per snapshot pointer. Config snapshots are replaced, never mutated in
// place (configsvc copy-on-write; every reload publishes a new *Config), so
// pointer identity is the cache key; the entry retains the snapshot it was
// built from, so its address cannot be recycled under it. The zero value is
// ready to use.
type profileIndexCache struct {
	last atomic.Pointer[profileIndex]
}

// For returns the index for cfg, building it on the first request after a
// snapshot change. Two goroutines racing on that first request may both
// build; either result is correct and the later Store wins.
func (c *profileIndexCache) For(cfg *config.Config) *profileIndex {
	if idx := c.last.Load(); idx != nil && idx.cfg == cfg {
		return idx
	}
	idx := newProfileIndex(cfg)
	c.last.Store(idx)
	return idx
}

// setProfileServerTool wraps buildSetProfileTool as a ServerTool for routing-mode registration.
func (p *MCPProxyServer) setProfileServerTool() mcpserver.ServerTool {
	return mcpserver.ServerTool{Tool: buildSetProfileTool(), Handler: p.handleSetProfile}
}
