package server

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
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

	// One (index, snapshot) pair for the whole call: admission, the stored
	// selection's server list and the effective scope all read cfg, the
	// snapshot the index was built from — never the live config, which may
	// move underneath the call (Spec 105 PR D critique round 1 / codex round 6).
	// Over a URL-scoped request (/mcp/p/<slug>) this is the exact pair
	// serveProfileURL already admitted the request against, injected on the
	// context — never a fresh, independent read (round 9 MUST-FIX 1).
	profiles := p.profileIndexCurrent(ctx)
	if profiles == nil {
		// Acquire's bounded retries could not pair a config snapshot with its
		// index (a publication storm outran them); profileIndexCurrent only
		// ever returns nil for a scoped caller — an administrator falls back
		// to a fresh build instead. Fail closed with the exact uniform
		// refusal every other non-selectable slug gets: no state change, no
		// disclosure, no build (Spec 105 PR D review round 11, MUST-FIX).
		return mcp.NewToolResultError(fmt.Sprintf("unknown profile '%s'", slug)), nil
	}
	cfg := profiles.cfg

	// A non-empty slug must name a configured profile the caller may select
	// (an empty slug clears the selection and is always accepted). The check
	// runs BEFORE any session mutation or success log, so a profile outside
	// the caller's reach — including a pinned token's own pin once it has
	// zero reach (research D1), and a pin MISMATCH (the caller's slug names a
	// different profile than its pin) — is indistinguishable from an unknown
	// one (FR-016b / FR-003): same error, no state change. A pin mismatch is
	// simply a non-selectable profile like any other and must never be
	// decided by an earlier, distinctly-worded branch — that let a pinned
	// caller confirm from the wording alone that it IS pinned, and to what,
	// from a refusal aimed at a different slug (Spec 105 PR D review round 9,
	// MUST-FIX 2; contracts/refusals.md: `set_profile <not selectable>` is
	// one format string).
	//
	// It decides the REQUESTED slug alone, through the per-snapshot index
	// (profileIndex.selectable: one lookup of the slug, one of the pin, one
	// precomputed-reach test), never through the selectable list — that list
	// is one reach computation per configured profile, so a refusal that
	// built it cost 0.3 µs over a one-profile fleet and 63 µs over 4 097 with
	// a byte-identical body: a fleet-population timing oracle (codex review,
	// PR D round 3; spec Definitions: non-disclosing = status, body AND
	// timing class). For the same reason a scoped caller's refusal carries no
	// `available:` list at all; administrators keep the pre-105 discovery
	// affordance (every configured profile — SC-005), the one path that may
	// legitimately enumerate.
	if slug != "" {
		if !profiles.selectable(ctx, slug) {
			if auth.IsScopedCaller(ctx) {
				return mcp.NewToolResultError(fmt.Sprintf("unknown profile '%s'", slug)), nil
			}
			return mcp.NewToolResultError(fmt.Sprintf("unknown profile '%s' (available: %s)", slug, strings.Join(profiles.selectableNames(ctx), ", "))), nil
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
	//
	// Rendered directly through the index's EffectiveServersFor, never
	// scopeServersIn/callerVisibleServers (removed): that pair rebuilt a
	// fleet-sized "known servers" set (profileServersIn → EffectiveServers)
	// for the effective profile AND, on a cleared/no-profile session, walked
	// every configured server a second time to apply the credential
	// (allServerNames + a CanEnumerateServer test per server) — fleet-sized
	// work on a request already admitted for exactly this caller (Spec 105
	// PR D review round 14 MUST-FIX). effectiveProfileName == "" renders the
	// no-profile/cleared-session set; a name no longer present in cfg (a
	// stale pin) renders empty, matching the deny-all scope it resolved to
	// — EffectiveServersFor needs only the resolved NAME, never the scope's
	// own (possibly already caller-filtered, e.g. via a /mcp/p/<slug> URL)
	// server set, so intersecting it again here with the caller's real
	// grant is idempotent, not a second, different filter.
	effectiveProfileName, _ := p.resolveActiveProfileFromIndex(ctx, profiles)
	var allowed []string
	if ac := auth.AuthContextFromContext(ctx); ac != nil {
		allowed = ac.AllowedServers
	}
	return setProfileResult(slug, profiles.EffectiveServersFor(effectiveProfileName, allowed))
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

// selectableProfileNames returns the profile slugs the caller may select. An
// unrestricted caller may select any configured profile; a profile-pinned
// token only its pin, and only while the pin has reach (it exists and its
// server set intersects the token's allowed servers — a deleted pin, an empty
// or ghost profile and a disjoint grant all yield nothing, Spec 105 research
// D1); a scoped caller only the profiles that overlap the servers it can
// enumerate.
//
// This is the same rule profileIndex.selectable evaluates for ONE profile —
// the admission rule on set_profile AND on the /mcp/p/<slug> URL
// (profileMiddleware): a profile entirely outside the caller's reach is
// treated exactly like a nonexistent one (FR-016b, FR-003/004). The LIST is
// fleet-sized work, so only the administrator's `available:` affordance
// renders it; no refusal a scoped caller receives is allowed to compute it.
//
// The result is accumulated by forEachProfileSelectable in configured order;
// see there for why it never returns early. Both build a throwaway index
// over cfg; production callers hold a per-snapshot one and use its methods.
func selectableProfileNames(ctx context.Context, cfg *config.Config) []string {
	if cfg == nil {
		return nil
	}
	return newProfileIndex(cfg).selectableNames(ctx)
}

// forEachProfileSelectable visits EVERY configured profile, in configured
// order, and reports to visit whether the caller may select it (the rule
// documented on selectableProfileNames). See profileIndex.forEachSelectable.
func forEachProfileSelectable(ctx context.Context, cfg *config.Config, visit func(name string, selectable bool)) {
	if cfg == nil {
		return
	}
	newProfileIndex(cfg).forEachSelectable(ctx, visit)
}

// profileIndex is an immutable index over ONE config snapshot: slug →
// profile, plus each profile's precomputed reach set. It lets the
// /mcp/p/<slug> gate and set_profile decide the requested profile (and a
// pinned caller's pin) directly instead of walking cfg.Profiles, so the work
// a scoped refusal costs does not grow with the number of OTHER profiles the
// operator has configured — the whole selectable list is fleet-sized, and a
// refusal that computed it did zero iterations over an empty fleet and one
// EffectiveServers per profile over a populated one: same status and body,
// fleet-population timing oracle (codex review, PR D round 2; FR-004).
//
// Reach is precomputed per profile as a SET of the configured servers it
// declares (a bitset over server positions, members, plus a non-empty flag),
// built once per snapshot: at request time the reach test walks the READER's
// own allowed_servers and tests each against that set — one O(1) membership
// test per granted name, or one "is the set non-empty" test for a wildcard
// or administrator reader — so its cost is the size of the caller's own
// grant and nothing else. A profile the snapshot lacks reads the all-zero
// placeholder (none) at the same cost. Computing reach from the candidate's
// declared list instead cost nothing for a missing profile and |servers| ×
// |declared| for an existing one, so a pinned token asking for its own
// zero-reach pin could tell "deleted" from "exists" by timing (codex review,
// PR D round 3; research D1); walking every configured server and running
// the credential check on each cost one iteration over a fleet of one server
// and 4 096 over a fleet with 4 095 hidden ones — same 404, same zero
// allocations, 36 ns vs 25 µs — so the operator's server population was a
// timing oracle (codex review, PR D round 4).
//
// Duplicate slugs cannot load (ValidateProfiles), but a hand-built config may
// carry them: the first occurrence wins, exactly like every linear lookup in
// this package (profileServersIn, the middleware's own scan).
type profileIndex struct {
	cfg    *config.Config
	byName map[string]int // slug → position in cfg.Profiles

	// serverPos maps a configured server's name to its position in
	// cfg.Servers (first named, non-nil occurrence), so a granted name
	// resolves to its membership bit in O(1).
	serverPos map[string]int

	// words is the bitset length in uint64 words: ceil(len(cfg.Servers)/64).
	// members holds len(cfg.Profiles) consecutive bitsets of that length —
	// bit i of profile p's set is on when cfg.Servers[i] is one of p's
	// declared servers (EffectiveServers as a set). nonEmpty[p] is on when
	// that set has at least one bit — the whole reach test for a reader
	// that may see every server. none is the all-zero placeholder read for
	// a slug the snapshot has no profile for.
	words    int
	members  []uint64
	nonEmpty []bool
	none     []uint64

	// lookupHook, when set, observes every slug the index resolves. It is the
	// seam the traversal-counter tests use to prove the gate and set_profile
	// touch at most the requested slug and the pin; nil in production.
	lookupHook func(slug string)

	// reachHook, when set, observes every unit of work reach performs — one
	// call per membership test. It is the seam the fleet-parity tests use to
	// prove a reach test costs the reader's grant, never the fleet; nil in
	// production.
	reachHook func()
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

	// Reach sets: server name → position once, then one bit per declared
	// server that exists in the snapshot (the EffectiveServers rule; nil
	// entries and empty names excluded — CanAccessServer never grants an
	// empty name, so such a server is reachable by nobody).
	idx.serverPos = make(map[string]int, len(cfg.Servers))
	for i, s := range cfg.Servers {
		if s == nil || s.Name == "" {
			continue
		}
		if _, dup := idx.serverPos[s.Name]; !dup {
			idx.serverPos[s.Name] = i
		}
	}
	idx.words = (len(cfg.Servers) + 63) / 64
	idx.none = make([]uint64, idx.words)
	idx.members = make([]uint64, len(cfg.Profiles)*idx.words)
	idx.nonEmpty = make([]bool, len(cfg.Profiles))
	for p := range cfg.Profiles {
		set := idx.membersOf(p)
		for _, name := range cfg.Profiles[p].Servers {
			if i, ok := idx.serverPos[name]; ok {
				set[i/64] |= 1 << (uint(i) % 64)
				idx.nonEmpty[p] = true
			}
		}
	}
	return idx
}

// membersOf returns profile p's reach bitset, or the all-zero placeholder
// for p < 0 (no such profile).
func (idx *profileIndex) membersOf(p int) []uint64 {
	if p < 0 {
		return idx.none
	}
	return idx.members[p*idx.words : (p+1)*idx.words]
}

// position resolves one slug to its position in cfg.Profiles in O(1), or -1
// when the snapshot has no such profile. The position is also bounds-checked
// against idx.cfg.Profiles' CURRENT length, not merely built at "ok": every
// other profileIndex field (members, nonEmpty, serverPos) was snapshotted at
// construction from an assumed-immutable *config.Config and is safe to
// index by a byName position regardless, but idx.cfg itself is a pointer a
// raw test fixture may mutate IN PLACE after building this index (cfg.
// Profiles = nil to simulate a deleted profile, never replacing the
// pointer — TestSetProfileClearReportsPinnedScope,
// TestReadCache_DeletedPinnedProfileRevokesCachedAccess), which would
// otherwise let a stale byName entry index a Profiles slice that has since
// shrunk out from under it. For a real (never-mutated) snapshot this check
// is always true — the position and idx.cfg.Profiles' length were built
// together — so it costs nothing on the path that matters.
func (idx *profileIndex) position(slug string) int {
	if idx.lookupHook != nil {
		idx.lookupHook(slug)
	}
	if i, ok := idx.byName[slug]; ok && idx.cfg != nil && i < len(idx.cfg.Profiles) {
		return i
	}
	return -1
}

// lookup resolves one slug in O(1), or nil when the snapshot has no such
// profile.
func (idx *profileIndex) lookup(slug string) *config.ProfileConfig {
	if i := idx.position(slug); i >= 0 {
		return &idx.cfg.Profiles[i]
	}
	return nil
}

// profileAt returns the profile at an ALREADY resolved position (candidate <
// 0: no such profile), the int-keyed counterpart to lookup — the seam a
// caller that already paid for position(slug) elsewhere in the same request
// (serveProfileURL's admission gate) uses to fetch the *ProfileConfig
// without resolving the slug a second time through the lookup-hook seam.
func (idx *profileIndex) profileAt(candidate int) *config.ProfileConfig {
	if candidate < 0 || idx.cfg == nil || candidate >= len(idx.cfg.Profiles) {
		return nil
	}
	return &idx.cfg.Profiles[candidate]
}

// EffectiveServersFor is the caller-facing counterpart to
// config.ProfileConfig.EffectiveServers: the servers within profileName's
// declared set — or, for profileName == "", every configured server — that
// allowed also grants (the same "*" / exact-name rule as
// AuthContext.CanAccessServer). It is what every admitted scoped READ
// (set_profile, /mcp/p/<slug>, and the pin/session tiers resolveActiveProfile
// consults on every call) renders its payload/scope from instead of
// re-deriving a fleet-sized "known servers" set on every call the way
// EffectiveServers does.
//
// Its cost is O(len(allowed)) (plus, only for a wildcard grant, one walk of
// the TARGET population — the profile's own declared servers, or every
// configured server for profileName == "", never a SECOND, nested walk per
// grant entry) — never O(len(cfg.Servers)): each granted name is tested
// against the index's precomputed serverPos/members data, built once per
// snapshot, rather than rebuilding a fleet-sized set on every call (Spec 105
// PR D review round 14 MUST-FIX). allowed == nil or empty is deny-all (an
// agent token's empty AllowedServers grants nothing — CanAccessServer's own
// rule) and returns nil without touching the index at all.
//
// The order matches every other EffectiveServers consumer: profile-declared
// order (duplicates kept) for a named profile, config order for profileName
// == "" — reproduced from the reader's own grant via serverPos, so it never
// needs cfg.Servers itself to get there.
func (idx *profileIndex) EffectiveServersFor(profileName string, allowed []string) []string {
	if profileName == "" {
		return idx.effectiveServersForAllowed(allowed)
	}
	return idx.effectiveServersForCandidate(idx.position(profileName), allowed)
}

// effectiveServersForCandidate is EffectiveServersFor for an ALREADY resolved
// profile position — see profileAt.
func (idx *profileIndex) effectiveServersForCandidate(candidate int, allowed []string) []string {
	if idx.cfg == nil || candidate < 0 || candidate >= len(idx.cfg.Profiles) || len(allowed) == 0 {
		return nil
	}
	declared := idx.cfg.Profiles[candidate].Servers
	if hasWildcardGrant(allowed) {
		out := make([]string, 0, len(declared))
		for _, name := range declared {
			if _, ok := idx.serverPos[name]; ok {
				out = append(out, name)
			}
		}
		return out
	}
	grant := make(map[string]struct{}, len(allowed))
	for _, name := range allowed {
		grant[name] = struct{}{}
	}
	out := make([]string, 0, len(declared))
	for _, name := range declared {
		if _, ok := idx.serverPos[name]; !ok {
			continue
		}
		if _, ok := grant[name]; ok {
			out = append(out, name)
		}
	}
	return out
}

// effectiveServersForAllowed is EffectiveServersFor("", allowed): every
// configured server allowed grants, in config order — the "no profile in
// effect" / cleared-selection rendering. A wildcard grant is unrestricted by
// definition (SC-005 administrator parity: the same full list, at the same
// cost administrators already pay for it), so it returns allServerNames
// directly rather than resolving each configured server through allowed. A
// restricted grant resolves each of the reader's OWN entries to its config
// position via serverPos — O(1) each — and sorts the (small) result by that
// position, so it reproduces config order without ever walking cfg.Servers:
// its cost is O(len(allowed) log len(allowed)), never the fleet's.
func (idx *profileIndex) effectiveServersForAllowed(allowed []string) []string {
	if idx.cfg == nil || len(allowed) == 0 {
		return nil
	}
	if hasWildcardGrant(allowed) {
		return allServerNames(idx.cfg)
	}
	type grantHit struct {
		pos  int
		name string
	}
	hits := make([]grantHit, 0, len(allowed))
	seen := make(map[int]struct{}, len(allowed))
	for _, name := range allowed {
		pos, ok := idx.serverPos[name]
		if !ok {
			continue
		}
		if _, dup := seen[pos]; dup {
			continue
		}
		seen[pos] = struct{}{}
		hits = append(hits, grantHit{pos: pos, name: name})
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].pos < hits[j].pos })
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = h.name
	}
	return out
}

// hasWildcardGrant reports whether allowed carries the "*" entry
// AuthContext.CanAccessServer treats as unrestricted.
func hasWildcardGrant(allowed []string) bool {
	for _, name := range allowed {
		if name == "*" {
			return true
		}
	}
	return false
}

// hasMember reports whether profile candidate's reach set is non-empty —
// false for the placeholder (candidate < 0).
func (idx *profileIndex) hasMember(candidate int) bool {
	return candidate >= 0 && idx.nonEmpty[candidate]
}

// reach reports whether the caller can enumerate at least one server of
// profile candidate (-1: no such profile) — the rule behind
// selectableProfileNames (research D1), i.e.
// len(callerVisibleServers(ctx, p.EffectiveServers(cfg))) > 0, without
// either allocation. Its cost is O(|reader grant|), never O(|fleet|): a
// reader that may see every server (administrator, absent context, or a
// wildcard entry) needs one test — is the set non-empty — and a restricted
// reader needs one membership test per entry of its own AllowedServers
// (the same "*" / exact-name rule as AuthContext.CanAccessServer; an empty
// name never matches). It never returns early and does the same work for
// the placeholder set, an empty profile and one declaring every server, so
// neither the outcome, the candidate nor the number of configured servers
// can be told from the work — only the caller's own grant, which it knows.
func (idx *profileIndex) reach(ctx context.Context, candidate int) bool {
	if idx.cfg == nil {
		return false
	}
	if !auth.IsScopedCaller(ctx) {
		idx.step()
		return idx.hasMember(candidate)
	}
	members := idx.membersOf(candidate)
	reach := false
	// IsScopedCaller guarantees a non-nil, non-admin AuthContext.
	for _, name := range auth.AuthContextFromContext(ctx).AllowedServers {
		idx.step()
		hit := false
		if name == "*" {
			hit = idx.hasMember(candidate)
		} else if i, ok := idx.serverPos[name]; ok {
			hit = members[i/64]&(1<<(uint(i)%64)) != 0
		}
		if hit {
			reach = true
		}
	}
	return reach
}

// step reports one unit of reach work to the test seam.
func (idx *profileIndex) step() {
	if idx.reachHook != nil {
		idx.reachHook()
	}
}

// selectable reports whether the caller may select the profile named slug —
// the rule forEachSelectable applies to every profile, evaluated for this
// ONE profile. It is the predicate of the URL gate and of set_profile's
// admission, and must never consult the selectable list: whatever the
// outcome (slug absent, profile out of reach, pin deleted, pin zero-reach,
// pin mismatch, empty fleet) it does the same work — one lookup of the slug,
// one lookup of the pin when the caller is pinned, and exactly one
// allocation-free reach test of the caller's own grant against the
// candidate's precomputed set or the all-zero placeholder when there is none
// — so no branch can be told from another by its cost, and none of it
// depends on how many other profiles exist, on how many servers the
// candidate declares or on how many servers are configured.
func (idx *profileIndex) selectable(ctx context.Context, slug string) bool {
	candidate := idx.position(slug)
	pin := profilePinFromContext(ctx)
	if pin != "" {
		// The pin is the only profile a pinned caller may select; resolve it
		// whether or not the URL named it so a mismatch costs what a match does.
		pinned := idx.position(pin)
		candidate = -1
		if slug == pin {
			candidate = pinned
		}
	}
	reach := idx.reach(ctx, candidate)
	// Administrators (and absent contexts) select any configured profile,
	// including empty or ghost ones (SC-005); everyone else needs reach.
	needsReach := pin != "" || auth.IsScopedCaller(ctx)
	return candidate >= 0 && (!needsReach || reach)
}

// forEachSelectable visits EVERY configured profile, in configured order,
// and reports to visit whether the caller may select it — the same rule as
// selectable, applied to each profile.
//
// It deliberately has no early return and does the same per-profile work
// whatever the outcome: a scoped caller's reach is computed for each profile
// even when a pin already rules it out, and a pinned caller keeps walking
// after its pin is found — a version that answered after one iteration for
// a live pin and after the whole slice for a deleted or zero-reach pin let a
// pinned caller tell those apart by the work its own refusal cost (codex
// review, PR D round 1). It is fleet-sized by nature, so no scoped refusal
// may run it (round 3): it feeds the administrator's `available:` list and
// the tests that pin selectable to it.
func (idx *profileIndex) forEachSelectable(ctx context.Context, visit func(name string, selectable bool)) {
	if idx.cfg == nil {
		return
	}
	pin := profilePinFromContext(ctx)
	// Administrators (and absent contexts) select any configured profile,
	// including empty or ghost ones (SC-005); everyone else needs reach.
	needsReach := pin != "" || auth.IsScopedCaller(ctx)
	for i := range idx.cfg.Profiles {
		p := &idx.cfg.Profiles[i]
		selectable := true
		if needsReach {
			selectable = idx.reach(ctx, i)
		}
		if pin != "" && p.Name != pin {
			selectable = false
		}
		visit(p.Name, selectable)
	}
}

// selectableNames accumulates forEachSelectable into a pre-sized slice, in
// configured order.
func (idx *profileIndex) selectableNames(ctx context.Context) []string {
	if idx.cfg == nil {
		return nil
	}
	names := make([]string, 0, len(idx.cfg.Profiles))
	idx.forEachSelectable(ctx, func(name string, selectable bool) {
		if selectable {
			names = append(names, name)
		}
	})
	return names
}

// profileIndexCache holds the profileIndex of the config snapshot a request
// decides over. Config snapshots are replaced, never mutated in place
// (configsvc copy-on-write; every reload publishes a new *Config), so an
// index is keyed on its snapshot's pointer identity and retains the snapshot
// it was built from (idx.cfg), so its address cannot be recycled under it.
// The zero value is ready to use.
//
// A request must decide over the snapshot that is actually PUBLISHED —
// runtime.Config() is the one atomic publication boundary every reader
// agrees on — never over a snapshot merely prepared ahead of it. Published
// returns the prepared pair whose cfg equals the caller's own read of
// runtime.Config(): the latest pair when it already covers it, or the
// previous one during the narrow window in which the pre-publish observer
// has warmed the NEXT snapshot but configsvc has not yet stored it (warm
// already points past runtime.Config(); previous still matches it). Round 7
// (codex) found that taking the unconditional warm slot — always the latest
// prepared pair, whether or not it was stored yet — let a request be
// admitted against the next config while resolveActiveProfileIn's pin tier,
// reading runtime.Config() independently, still answered the previous one:
// admission and the effective scope could disagree within one request.
// Keeping the last TWO prepared pairs closes that window without ever
// building: the observer runs under updateMu, so at most one
// prepared-but-not-yet-stored pair can exist at any time, and "latest, else
// previous" is therefore always either the published pair or the one about
// to replace it — never a third, older one a request could still be reading
// runtime.Config() as.
//
// For is the fallback for bare test servers (no runtime, so no warm path):
// it never writes the warm slot and builds into the lazy slot, so even a
// caller that reached it with a runtime could not roll the warm index back.
type profileIndexCache struct {
	warm     atomic.Pointer[profileIndex] // latest prepared pair
	previous atomic.Pointer[profileIndex] // second-latest prepared pair (see Published)
	lazy     atomic.Pointer[profileIndex]

	// warmMu serialises warm-slot writers; observed flips once the
	// pre-publish observer has warmed a snapshot, after which the event-driven
	// warm defers to it (a late event's build could otherwise land over a
	// newer observer build).
	warmMu   sync.Mutex
	observed bool

	// lazyBuilds counts For's fallback builds — the seam the tests use to
	// prove no request over a live runtime pays for the index.
	lazyBuilds atomic.Int64
}

// Current returns the latest warmed (index, snapshot) pair — the one the
// warm path most recently prepared, whether or not it has been published
// yet — or nil before any warm path has run. Cache-mechanics callers only
// (bare-cache tests, and callers with no runtime.Config() to match against);
// request paths that must stay consistent with a specific runtime.Config()
// read use Published instead, which is what production wires through
// profileMiddleware and profileIndexCurrent.
func (c *profileIndexCache) Current() *profileIndex {
	return c.warm.Load()
}

// Published returns the prepared pair whose snapshot is published — cfg ==
// the caller's own runtime.Config() read — or nil when neither the latest
// nor the previous prepared pair covers it: a bare cache with no warm path
// (no runtime behind it), or, should the pre-publish observer ever not have
// run on a stored snapshot (it always does — every publication path funnels
// through updateLocked), a snapshot more than one publication further back
// than what warmPublishing has seen. Callers fall back to For(published) in
// that case, counted on lazyBuilds; over a live runtime with the observer
// wired the fallback must never fire.
func (c *profileIndexCache) Published(published *config.Config) *profileIndex {
	if idx := c.warm.Load(); idx != nil && idx.cfg == published {
		return idx
	}
	if idx := c.previous.Load(); idx != nil && idx.cfg == published {
		return idx
	}
	return nil
}

// warmPublishing indexes cfg as the configsvc pre-publish observer, on the
// exact snapshot about to be published, and returns the index. It runs under
// the config update mutex, so it does one insertion per profile and nothing
// else. The prior warm pair is demoted to previous (never dropped outright)
// so a request whose own runtime.Config() read still answers it — because
// this snapshot has not been Store'd yet — can still find it via Published.
// Requests that already hold either pair keep it (Current/Published hand out
// the pointer, not the slot).
func (c *profileIndexCache) warmPublishing(cfg *config.Config) *profileIndex {
	c.warmMu.Lock()
	defer c.warmMu.Unlock()
	c.observed = true
	old := c.warm.Load()
	if old != nil && old.cfg == cfg {
		return old
	}
	idx := newProfileIndex(cfg)
	if old != nil {
		c.previous.Store(old)
	}
	c.warm.Store(idx)
	return idx
}

// warmCurrent indexes the runtime's current snapshot cfg from outside the
// publication path (construction, config events). It is the whole warm path
// for the initial snapshot — NewService stores it without running any
// observer — and belt-and-braces afterwards: once the observer has warmed a
// snapshot it is a no-op, so a late event can never roll the slot back.
func (c *profileIndexCache) warmCurrent(cfg *config.Config) {
	c.warmMu.Lock()
	defer c.warmMu.Unlock()
	if c.observed {
		return
	}
	if idx := c.warm.Load(); idx != nil && idx.cfg == cfg {
		return
	}
	c.warm.Store(newProfileIndex(cfg))
}

// For returns the index for cfg: the warm slot when it covers cfg, else the
// lazy slot, else a build of its own into the lazy slot. It is the fallback
// for bare test servers only (no runtime, so no warm path ever runs); a
// request over a runtime takes Current() and must never reach it
// (TestProfileRequests_NeverBuildTheIndexOverARuntime pins that through the
// lazyBuilds seam). It never writes the warm slot. Two goroutines racing on
// a lazy build may both build; either result is correct and the later Store
// wins.
func (c *profileIndexCache) For(cfg *config.Config) *profileIndex {
	if idx := c.warm.Load(); idx != nil && idx.cfg == cfg {
		return idx
	}
	if idx := c.lazy.Load(); idx != nil && idx.cfg == cfg {
		return idx
	}
	c.lazyBuilds.Add(1)
	idx := newProfileIndex(cfg)
	c.lazy.Store(idx)
	return idx
}

// profileIndexAcquireRetries bounds Acquire's read-and-match loop. Each
// iteration is O(1) (Published never builds), so the bound caps the cost of
// even a publication storm at O(retries) — never a profile-index build.
const profileIndexAcquireRetries = 8

// Acquire returns the prepared pair whose snapshot equals readCfg()'s OWN
// result, read and matched atomically inside the cache — never a separate
// read-then-match at the call site, which a publication landing strictly
// between the two steps could invalidate before Published ever ran (Spec 105
// PR D review round 11, MUST-FIX). profileMiddleware and profileIndexCurrent
// previously read runtime.Config() once, then called Published against that
// stale read; a request paused across two publications between those two
// steps found neither the latest nor the previous prepared pair, and fell
// back to For, which builds the whole fleet inline — fleet-sized work behind
// a status/body identical to the constant-cost refusal every other miss
// gets, exactly the timing-class disclosure D17 exists to close.
//
// Acquire closes it structurally: each iteration re-reads readCfg() (never
// reusing a prior read) and asks Published for THAT read's own snapshot, so
// a publication racing the previous iteration is simply observed on the
// next one — the loop chases a moving config instead of freezing a read
// that may already be stale by the time it is used. It returns nil only
// when readCfg keeps outrunning the bounded retry budget, which the
// pre-publish observer makes structurally near-impossible on any snapshot a
// reader actually reaches (Published matches on the very first iteration in
// the overwhelmingly common case — no race in flight).
//
// Callers decide what a miss means for their own caller shape: production
// call sites (profileMiddleware, profileIndexCurrent for the base /mcp
// endpoint) refuse a scoped caller uniformly on nil (fail closed, O(1),
// never a build) and fall back to For for an administrator-shaped caller,
// whose timing is not contract-bound (SC-005). For itself remains the
// fallback for bare test servers with no runtime to read atomically at all.
func (c *profileIndexCache) Acquire(readCfg func() *config.Config) *profileIndex {
	for i := 0; i < profileIndexAcquireRetries; i++ {
		cfg := readCfg()
		if idx := c.Published(cfg); idx != nil {
			return idx
		}
	}
	return nil
}

// setProfileServerTool wraps buildSetProfileTool as a ServerTool for routing-mode registration.
func (p *MCPProxyServer) setProfileServerTool() mcpserver.ServerTool {
	return mcpserver.ServerTool{Tool: buildSetProfileTool(), Handler: p.handleSetProfile}
}

// profileIndexCurrent returns the profile index a set_profile call decides
// with — and, as idx.cfg, the config snapshot it decides over.
//
// When ctx carries the pair serveProfileURL already admitted this request
// against (profileRequestIndexFromContext — a /mcp/p/<slug> request), THAT
// pair is preferred outright: no build, no runtime.Config() read of any
// kind, because re-reading here is exactly the bug this seam exists to
// close — a reload landing between the gate's admission and this call could
// otherwise hand set_profile a different snapshot than the one the URL gate
// used for the very same request (Spec 105 PR D review round 9, MUST-FIX 1).
//
// Otherwise (the plain /mcp endpoint, which admits no snapshot of its own)
// the pair is taken from the main Server's cache via Acquire, which reads
// currentConfig() and matches it atomically inside the cache (round 7/8:
// taking the cache's unconditional latest pair here let set_profile admit
// and scope a profile that existed only in the config about to be
// published, one publication ahead of what resolveActiveProfileIn would
// independently read moments later; round 11: reading currentConfig() and
// matching it in two separate steps — as a bare Published(currentConfig())
// call did — left a window in which a request paused between the two steps
// missed both prepared pairs and fell back to For, building the whole fleet
// inline for a scoped caller's refusal), so the call never builds an index
// on a scoped path and never pairs a snapshot with an index built from
// another one. On the rare exhaustion of Acquire's bounded retries (a
// publication storm outrunning the loop), a scoped caller gets nil here —
// handleSetProfile fails closed with the same uniform refusal any other
// non-selectable slug gets, O(1), never a build; an administrator-shaped
// caller is not timing-contract-bound (SC-005) and falls back to a fresh
// build over currentConfig(). A proxy with no warmed main Server (bare test
// servers) falls back to a lazily built index over its construction config,
// keyed by identity.
func (p *MCPProxyServer) profileIndexCurrent(ctx context.Context) *profileIndex {
	if injected, ok := profileRequestIndexFromContext(ctx); ok {
		return injected
	}
	if p.mainServer != nil {
		if idx := p.mainServer.profileIndexes.Acquire(p.currentConfig); idx != nil {
			return idx
		}
		if auth.IsScopedCaller(ctx) {
			return nil
		}
		return p.mainServer.profileIndexes.For(p.currentConfig())
	}
	return p.profileIndexes.For(p.currentConfig())
}

// profileIndexFor returns the profile index for an EXPLICIT snapshot cfg —
// the one resolveActiveProfileIn and its callers already hold, never a fresh
// runtime.Config() read (cfg may be older than the live config, e.g. the
// snapshot handleSetProfile admitted a selection against). Over a wired
// runtime it prefers the cache's Published pair — an O(1) pointer-identity
// match against a snapshot the warm path already indexed, never a build —
// and falls back to For only when cfg is not one of the two most recently
// prepared pairs (a snapshot captured further back than the cache retains):
// TestProfileRequests_NeverBuildTheIndexOverARuntime pins that a live
// runtime's own snapshot always hits Published, so this fallback never
// fires there. Unlike profileIndexCurrent it never reads currentConfig()
// itself and never returns nil — cfg is already fixed, so there is nothing
// to (re-)match atomically the way Acquire does.
//
// Without a runtime behind it (bare test construction: p.mainServer == nil)
// it builds fresh on every call rather than consulting p.profileIndexes'
// pointer-identity cache: a bare proxy's cfg is a raw fixture a test may
// mutate IN PLACE between calls (e.g.
// TestReadCache_DeletedPinnedProfileRevokesCachedAccess sets
// proxy.config.Profiles = nil to simulate a deleted pin, never replacing the
// *config.Config pointer) — caching by that same pointer's identity would
// silently keep serving the profile positions of whatever Profiles slice was
// in place the first time this cfg was seen. There is no hot-path cost to
// protect without a runtime behind it.
func (p *MCPProxyServer) profileIndexFor(cfg *config.Config) *profileIndex {
	if p.mainServer != nil {
		if idx := p.mainServer.profileIndexes.Published(cfg); idx != nil {
			return idx
		}
		return p.mainServer.profileIndexes.For(cfg)
	}
	return newProfileIndex(cfg)
}
