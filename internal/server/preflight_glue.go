package server

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/index"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/preflight"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime/stateview"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/toolannotations"
)

// This file is the ONLY bridge between the pure evaluator in internal/preflight
// and this package's index / storage / stateview / config wiring. Everything the
// evaluator can see arrives through the four narrow read interfaces implemented
// below, which is what makes FR-006 (zero upstream I/O, zero runtime mutation)
// structural: none of these adapters holds an upstream manager, a client, or a
// writer, so no preflight code path can reach a transport even by mistake.
//
// Two deliberate omissions, both load-bearing:
//
//   - serverToolNames is NOT used. It falls back to a live ListTools when the
//     StateView snapshot is cold, which would turn a "stat-only" preflight into
//     upstream I/O on exactly the servers most likely to be unhealthy.
//   - index.Manager.ForProfile is NOT used. It lazily CREATES and caches a
//     per-profile Bleve index — a mutation. Profile semantics here are
//     "shared-index existence + profile scope filter" (FR-010, plan decision 8).

// RunPreflight evaluates one preflight request against local state only.
//
// Errors: preflight.ErrRuntimeUnavailable when the process cannot evaluate
// honestly, preflight.ErrUnknownProfile for a profile the config does not
// define, and a wrapped infrastructure error for a failed index/storage/config
// read — the served surface maps those to 503 rather than fabricating a reason
// code (FR-006).
func (p *MCPProxyServer) RunPreflight(ctx context.Context, params preflight.Params) (preflight.Outcome, error) {
	cfg, err := p.preflightRuntimeConfig()
	if err != nil {
		return preflight.Outcome{}, err
	}

	// ONE config snapshot and ONE profile index for the scope AND the tool
	// policy (issue #1548): resolving them from two reads would let a hot
	// reload between them pair a new server scope with an old tool policy.
	idx := p.profileIndexFor(cfg)
	scope, policies, err := p.resolvePreflightScope(params, cfg, idx)
	if err != nil {
		return preflight.Outcome{}, err
	}

	tier := params.Tier
	if tier == "" {
		tier = preflight.TierOperator
	}

	return p.evaluatePreflight(ctx, params.Tools, tier, scope, params.Filters, nil, policies)
}

// preflightRuntimeConfig is the shared "can this process answer at all" guard:
// no storage, no index or no live config is the degraded state FR-006 names,
// and every front door must refuse rather than evaluate blind.
func (p *MCPProxyServer) preflightRuntimeConfig() (*config.Config, error) {
	if p == nil || p.storage == nil || p.index == nil {
		return nil, preflight.ErrRuntimeUnavailable
	}
	cfg := p.currentConfig()
	if cfg == nil {
		return nil, preflight.ErrRuntimeUnavailable
	}
	return cfg, nil
}

// evaluatePreflight is the ONE evaluation seam. Both front doors — the REST
// surface's RunPreflight (which resolves its scope from profile NAMES) and the
// in-band check mode's RunPreflightForSession (which projects the session's own
// visibility predicate, spec 099 FR-003/FR-009a) — end here, so the two
// surfaces cannot drift in what they read or how they read it.
// evaluatePreflight runs one evaluation. indexReader selects the CORPUS an id
// resolves against and the corpus did_you_mean draws from; nil means the shared
// search index, which is every surface except spec 102's direct describe.
// policies are the Spec 108 profile tool policies in effect for the caller
// (issue #1548); every one of them must admit a tool for it to be ready, and an
// empty list means no profile policy applies.
func (p *MCPProxyServer) evaluatePreflight(
	ctx context.Context,
	refs []preflight.ToolRef,
	tier preflight.Tier,
	scope *preflight.Scope,
	filters toolannotations.Filters,
	indexReader preflight.IndexReader,
	policies []preflightProfilePolicy,
) (preflight.Outcome, error) {
	cfg, err := p.preflightRuntimeConfig()
	if err != nil {
		return preflight.Outcome{}, err
	}

	// ONE snapshot for the whole request: it supplies both the connection state
	// and the tool annotations, so every tool in a batch is judged against the
	// same instant and the annotation filters see exactly what the spec 094
	// discovery filters see.
	state, annotations, err := p.preflightSnapshot()
	if err != nil {
		return preflight.Outcome{}, err
	}

	if indexReader == nil {
		indexReader = &preflightIndexReader{index: p.index, annotations: annotations}
	}

	ec := preflight.EvalContext{
		Index:     indexReader,
		Approvals: &preflightApprovalReader{storage: p.storage},
		State:     state,
		Policy:    &preflightConfigPolicy{proxy: p, cfg: cfg},
		Tier:      tier,
		Scope:     scope,
		Filters:   filters,
		// With a real snapshot in hand, a configured server missing from it is
		// state the supervisor has not published yet (startup / reconcile /
		// config-add windows) — the evaluator answers the retryable
		// server_initializing verdict instead of ready/not_found (FR-005).
		// The 503 refusal (ErrRuntimeUnavailable) is reserved for a wired
		// runtime with no snapshot object at all (preflightSnapshot above).
		RequireRuntimeEntry: state != nil,
	}
	if len(policies) > 0 {
		ec.ToolPolicy = &preflightToolPolicyReader{
			policies:    policies,
			annotations: p.preflightAnnotationSource(state),
		}
	}

	results, err := preflight.Evaluate(ctx, ec, refs)
	if err != nil {
		return preflight.Outcome{}, err
	}
	return preflight.Outcome{
		Verdict: preflight.VerdictForResults(results),
		Results: results,
	}, nil
}

// resolvePreflightScope turns the request's profile NAMES into the effective
// evaluation scope: token scope ∩ token pin ∩ requested profile.
//
// A requested profile that does not exist is a caller error (400). A token pin
// that no longer matches a configured profile keeps its name as a restriction
// over an EMPTY server set, which intersects to deny-all: the pin is a
// narrowing the operator applied to that token, so losing the profile it names
// must never hand the token a wider view than it had yesterday. The agent then
// sees every id as not_found (tier scope-silence) — a loud, correct answer that
// names the removed profile in the logs, rather than a silent widening.
//
// It also returns the profile tool policies the same names put in effect (issue
// #1548): the token pin's and the requested profile's, both compiled in idx —
// the snapshot dispatch decides against. Both must admit a tool, so an explicit
// request profile can only NARROW what the pin allows, never widen it, exactly
// as it narrows the server scope.
func (p *MCPProxyServer) resolvePreflightScope(params preflight.Params, cfg *config.Config, idx *profileIndex) (*preflight.Scope, []preflightProfilePolicy, error) {
	inputs := preflight.ScopeInputs{Restricted: params.Restricted, TokenServers: params.TokenServers}
	var policies []preflightProfilePolicy

	if pin := params.TokenProfilePin; pin != "" {
		inputs.TokenPinName = pin
		if scope := profileScopeForSlugIn(cfg, pin); scope != nil {
			inputs.TokenPinServers = scope.AllowedServerNames()
			policies = appendProfilePolicy(policies, idx, pin)
		} else {
			inputs.TokenPinServers = nil
			if p.logger != nil {
				p.logger.Warn("preflight: agent-token profile_pin no longer matches any configured profile; evaluating under a deny-all scope",
					zap.String("profile_pin", pin))
			}
		}
	}

	if name := params.Profile; name != "" {
		scope := profileScopeForSlugIn(cfg, name)
		if scope == nil {
			return nil, nil, fmt.Errorf("%w: %q", preflight.ErrUnknownProfile, name)
		}
		inputs.RequestedProfileName = name
		inputs.RequestedProfileServers = scope.AllowedServerNames()
		policies = appendProfilePolicy(policies, idx, name)
	}

	return preflight.ResolveScope(inputs), policies, nil
}

// appendProfilePolicy adds the compiled policy of the named profile, if the
// snapshot compiles one, labelled for disclosure: the REST caller either named
// the profile itself or holds the credential pinned to it, so the operator-tier
// detail may name it (Spec 108 D39). The agent-token tier never shows the
// detail at all.
func appendProfilePolicy(policies []preflightProfilePolicy, idx *profileIndex, slug string) []preflightProfilePolicy {
	if idx == nil || slug == "" {
		return policies
	}
	policy := idx.PolicyFor(slug)
	if policy == nil {
		return policies
	}
	subject := refusalSubject{Slug: slug, Disclose: true}
	if pc := idx.lookup(slug); pc != nil {
		subject.Title = pc.Title
	}
	return append(policies, preflightProfilePolicy{policy: policy, subject: subject})
}

// RunPreflightForSession evaluates one IN-BAND preflight — describe_tool check
// mode (spec 099 FR-003/FR-009/FR-009a).
//
// Two things are pinned here rather than passed in, because they are the
// security contract of the in-band surface:
//
//   - The tier is ALWAYS the agent-token tier. /mcp is unauthenticated by
//     default and its middleware hands such requests a full admin context for
//     back-compat, so no auth context in band proves anything about who is
//     calling; a caller-selectable tier would be a caller-selectable
//     disclosure. Operators wanting scope names and hashes use the REST
//     surface over an authenticated channel (FR-009).
//   - The scope is the SESSION's, never a request parameter: check mode takes
//     no `profile`, so an agent cannot re-point or widen its own view by
//     asking (FR-009a).
func (p *MCPProxyServer) RunPreflightForSession(ctx context.Context, refs []preflight.ToolRef, filters toolannotations.Filters) (preflight.Outcome, error) {
	scope, policies, err := p.sessionPreflightView(ctx)
	if err != nil {
		return preflight.Outcome{}, err
	}
	return p.evaluatePreflight(ctx, refs, preflight.TierAgentToken, scope, filters, nil, policies)
}

// sessionPreflightScope projects the session's OWN visibility predicate onto a
// preflight scope (FR-009a). See sessionPreflightView.
func (p *MCPProxyServer) sessionPreflightScope(ctx context.Context) (*preflight.Scope, error) {
	scope, _, err := p.sessionPreflightView(ctx)
	return scope, err
}

// sessionPreflightView projects the session's OWN visibility predicate onto a
// preflight scope (FR-009a), and returns the profile tool policies in effect
// for the session (issue #1548).
//
// It deliberately does not re-derive the scope from names the way the REST path
// does. The composition an MCP session is subject to — agent-token
// allowed_servers ∩ agent-token profile pin ∩ the session's active profile
// (path-pinned or set_profile) — already exists as exactly two functions:
// resolveActiveProfile and serverInScope, which retrieve_tools and describe_tool
// use on every call. Enumerating the servers an evaluation could name and
// filtering them through that same predicate makes the invariant structural: a
// check can never see a tool the same session's retrieve_tools cannot, because
// it is the same test, not a reimplementation of it. In particular it inherits
// the deny-all resolution of a token pin whose profile was deleted, and
// CanAccessServer's rule that an agent token with an EMPTY allowed_servers list
// grants nothing (which a name-based intersection would read as "no
// restriction").
//
// The Spec 108 v3 resolution dispatch decides against (pin > url > session >
// binding > anonymous) is applied ON TOP, and can only narrow: its scope —
// including an authoritative deny-all (the FR-008a anonymous binding guard, a
// dangling binding or anonymous base) — must also admit the server, and its
// compiled policy must admit the tool. When the legacy resolver names a
// different profile (the name toolVisibleToSession's policy gate reads), that
// profile's policy applies too. The non-recording resolver is used, so the
// check writes no recorded resolution of its own. It is not a pure read: like
// every request, it re-validates a stored set_profile selection and clears one
// that is no longer admissible (FR-022). That adds nothing the request has not
// already done — mcp-go runs the WithToolFilter chain (filterProfileV3Tools,
// on the indexed and direct servers alike) at tools/call ahead of this
// handler, and that filter resolves through the recording resolver — and the
// verdict never depends on it: an inadmissible selection resolves to the
// fail-closed base view either way.
//
// A nil scope means unrestricted, and is returned only when there is no auth
// context and no profile in effect under either resolver.
func (p *MCPProxyServer) sessionPreflightView(ctx context.Context) (*preflight.Scope, []preflightProfilePolicy, error) {
	authCtx := auth.AuthContextFromContext(ctx)
	profileName, profileScope, idx := p.resolveActiveProfileWithIndex(ctx)

	var res ProfileResolution
	if idx != nil {
		res = p.resolveProfileV3(ctx, idx)
	}
	var policies []preflightProfilePolicy
	if res.Policy != nil {
		policies = append(policies, preflightProfilePolicy{policy: res.Policy, subject: profileRefusalSubject(res, idx)})
	}
	policyName := profileName
	if res.Scope != nil {
		policyName = res.Name
	}
	if policyName != "" && idx != nil {
		if policy := idx.PolicyFor(policyName); policy != nil && policy != res.Policy {
			policies = append(policies, preflightProfilePolicy{policy: policy})
		}
	}

	if authCtx == nil && profileScope == nil && res.Scope == nil {
		return nil, policies, nil
	}

	names, err := p.preflightServerUniverse()
	if err != nil {
		return nil, nil, err
	}
	allowed := make([]string, 0, len(names))
	for _, name := range names {
		if !p.serverInScope(authCtx, profileScope, name) {
			continue
		}
		if res.Scope != nil && !res.Scope.Allows(name) {
			continue
		}
		allowed = append(allowed, name)
	}
	return preflight.NewScope(profileName, allowed), policies, nil
}

// preflightServerUniverse is every server name an evaluation could legitimately
// reach: stored upstreams (the authority the evaluator's ServerPolicy reads),
// the indexed corpus (the authority did_you_mean suggests from) and the live
// config. A name outside this union is not "out of scope" — it is not
// configured, which the precedence chain answers before the scope gate ever
// runs.
//
// A failed read is an error, never a smaller universe: silently shrinking it
// would turn a storage failure into a scope verdict.
func (p *MCPProxyServer) preflightServerUniverse() ([]string, error) {
	seen := make(map[string]struct{})
	names := make([]string, 0, 16)
	add := func(name string) {
		if name == "" {
			return
		}
		if _, ok := seen[name]; ok {
			return
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}

	upstreams, err := p.storage.ListUpstreams()
	if err != nil {
		return nil, fmt.Errorf("preflight: list upstream servers: %w", err)
	}
	for _, server := range upstreams {
		if server != nil {
			add(server.Name)
		}
	}

	indexed, err := p.index.GetAllIndexedServerNames()
	if err != nil {
		return nil, fmt.Errorf("preflight: list indexed servers: %w", err)
	}
	for _, name := range indexed {
		add(name)
	}

	if cfg := p.currentConfig(); cfg != nil {
		for _, server := range cfg.Servers {
			if server != nil {
				add(server.Name)
			}
		}
	}
	return names, nil
}

// ---------------------------------------------------------------------------
// IndexReader
// ---------------------------------------------------------------------------

type preflightIndexReader struct {
	index *index.Manager
	// annotations resolves a tool's MCP annotations from the connection-state
	// snapshot. It is REQUIRED for the annotation-filter slot to work: the Bleve
	// documents carry identity and text only (index.BleveIndex.GetToolsByServer
	// hydrates name/description/params/hash), so a tool read back from the index
	// always has nil Annotations. Without this, every filtered preflight would
	// report missing_annotation — including for tools that do declare the hint —
	// and policy_filtered would be unreachable. nil disables enrichment.
	annotations func(serverName, toolName string) *config.ToolAnnotations
}

func (r *preflightIndexReader) ToolsByServer(serverName string) ([]preflight.IndexedTool, error) {
	tools, err := r.index.GetToolsByServer(serverName)
	if err != nil {
		return nil, fmt.Errorf("index read for server %q: %w", serverName, err)
	}
	out := make([]preflight.IndexedTool, 0, len(tools))
	for _, tool := range tools {
		if tool == nil {
			continue
		}
		entry := preflight.IndexedTool{Name: tool.Name, Annotations: tool.Annotations}
		if entry.Annotations == nil && r.annotations != nil {
			entry.Annotations = r.annotations(serverName, bareToolName(tool.Name))
		}
		out = append(out, entry)
	}
	return out, nil
}

// directCatalogIndexReader is the IndexReader for spec 102's direct surface: the
// published catalog, filtered to what THIS session can list (T054).
//
// Two things follow from using it, and both are the point. Id resolution
// happens against the same snapshot tools/list rendered from, so a check can
// never disagree with the listing about whether a tool exists. And
// `did_you_mean` is drawn from the same filtered corpus, so a suggestion cannot
// name a tool the caller could not list — the disclosure a shared-index corpus
// would reintroduce for exactly the ids most likely to be mistyped.
// It holds a RESOLVED slice rather than the catalog plus a predicate: the
// visibility predicate is storage-backed, and re-running it inside every
// ToolsByServer would both cost a read per catalog entry per server and let a
// concurrent write make two reader calls disagree about the same tool within one
// evaluation.
type directCatalogIndexReader struct {
	entries []*directCatalogEntry
}

func (r *directCatalogIndexReader) ToolsByServer(serverName string) ([]preflight.IndexedTool, error) {
	var out []preflight.IndexedTool
	for _, entry := range r.entries {
		if entry.ServerName != serverName {
			continue
		}
		// The catalog carries the UPSTREAM annotations, so unlike the index
		// reader there is nothing to enrich from a second source — which is also
		// why the annotation filters see the same values dispatch does.
		out = append(out, preflight.IndexedTool{
			Name:        entry.ServerName + ":" + entry.ToolName,
			Annotations: entry.Annotations,
		})
	}
	if out == nil {
		out = []preflight.IndexedTool{}
	}
	return out, nil
}

func (r *directCatalogIndexReader) IndexedServerNames() ([]string, error) {
	seen := make(map[string]struct{})
	names := make([]string, 0, 8)
	for _, entry := range r.entries {
		if _, ok := seen[entry.ServerName]; ok {
			continue
		}
		seen[entry.ServerName] = struct{}{}
		names = append(names, entry.ServerName)
	}
	return names, nil
}

// bareToolName strips the "<server>:" prefix the index stores on canonical
// names.
func bareToolName(name string) string {
	if idx := strings.Index(name, ":"); idx >= 0 {
		return name[idx+1:]
	}
	return name
}

func (r *preflightIndexReader) IndexedServerNames() ([]string, error) {
	names, err := r.index.GetAllIndexedServerNames()
	if err != nil {
		return nil, fmt.Errorf("index server list: %w", err)
	}
	return names, nil
}

// ---------------------------------------------------------------------------
// ToolPolicyReader (issue #1548)
// ---------------------------------------------------------------------------

// preflightProfilePolicy is one compiled Spec 108 profile policy in effect for
// a preflight caller, with what an operator-tier refusal may say about it.
type preflightProfilePolicy struct {
	policy  *profile.CompiledPolicy
	subject refusalSubject
}

// preflightToolPolicyReader answers the evaluator's profile-policy question
// with the decision dispatch makes: CompiledPolicy.Decide over
// profile.IntrinsicTier of the tool's effective annotations — the same
// identity seam handleCallToolVariant and the access explainer read — and
// the refusal text profileToolPolicyRefusal renders for dispatch. Nothing here
// re-implements a tier cap, a rule match or the unannotated handling.
type preflightToolPolicyReader struct {
	policies    []preflightProfilePolicy
	annotations func(serverName, toolName string) (*config.ToolAnnotations, bool)
}

func (r *preflightToolPolicyReader) ProfileToolDecision(serverName, toolName string) preflight.ProfileToolDecision {
	if len(r.policies) == 0 {
		return preflight.ProfileToolDecision{}
	}
	annotations, found := r.annotations(serverName, toolName)
	intrinsic := profile.IntrinsicTier(annotations, found)
	for _, pp := range r.policies {
		admitted, reason, tier := pp.policy.Decide(serverName, toolName, intrinsic)
		if admitted {
			continue
		}
		// server_not_in_profile is normally answered earlier by the scope
		// gate built from the same profile. If the two ever disagree, fail
		// closed: a policy that does not admit the tool is not ready.
		detail, _ := profileToolPolicyRefusal(reason, tier, pp.policy.Cap, serverName, toolName, pp.subject)
		return preflight.ProfileToolDecision{Blocked: true, Detail: detail}
	}
	return preflight.ProfileToolDecision{}
}

// preflightAnnotationSource returns the effective-annotation lookup the
// profile policy decides from. With the request's own stateview snapshot it
// resolves through resolveExactToolIdentityIn on THAT snapshot — the identity
// read dispatch's tier gate uses — so every tool in a batch is classified
// against one instant. Annotations and Found do not depend on the persisted
// server record (it only decides hydration), so none is read per tool. A
// snapshot a test injects falls back to the live EffectiveAnnotations seam.
func (p *MCPProxyServer) preflightAnnotationSource(state preflight.StateReader) func(serverName, toolName string) (*config.ToolAnnotations, bool) {
	if snapshot, ok := state.(*preflightStateSnapshot); ok && snapshot != nil && snapshot.proxy != nil {
		return func(serverName, toolName string) (*config.ToolAnnotations, bool) {
			identity := snapshot.proxy.resolveExactToolIdentityIn(snapshot.servers, serverName, toolName, nil)
			return identity.Annotations, identity.Found
		}
	}
	return p.EffectiveAnnotations
}

// ---------------------------------------------------------------------------
// ApprovalReader
// ---------------------------------------------------------------------------

type preflightApprovalReader struct {
	storage *storage.Manager
}

// ToolApproval maps the storage seam onto the evaluator's contract: "no record"
// must come back as (nil, nil) — the evaluator decides what it means (the
// implicit-approved default, or pending for a discovered tool under an active
// gate, Spec 105 FR-009) — while a genuine BBolt failure must come back as an
// error so the request answers 503 instead of silently reporting a tool as
// approved.
//
// The record is resolved through the SAME reader dispatch uses
// (readToolApprovalRecord): the exact raw-name record wins, and a legacy
// collapsed record — a pre-105 "ns:erase" filed under "erase" — lends the
// namespaced name its lock or user block but never its approval. Reading the
// exact key alone here made preflight answer `ready` for a tool the retrieve
// gate refused as pending (gap FR009-G3).
func (r *preflightApprovalReader) ToolApproval(serverName, toolName string) (*preflight.ApprovalState, error) {
	record, err := readToolApprovalRecord(r.storage, serverName, toolName)
	if err != nil {
		if errors.Is(err, storage.ErrToolApprovalNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("tool approval read for %s:%s: %w", serverName, toolName, err)
	}
	if record == nil {
		return nil, nil
	}
	return &preflight.ApprovalState{
		Status:            record.Status,
		Disabled:          record.Disabled,
		CurrentHash:       record.CurrentHash,
		HashSchemaVersion: record.HashSchemaVersion,
	}, nil
}

// ---------------------------------------------------------------------------
// StateReader
// ---------------------------------------------------------------------------

// preflightSnapshot takes ONE lock-free stateview snapshot for the whole
// request and derives both reads that need it: the connection state and the
// per-tool annotation lookup. Sharing the snapshot means every tool in a batch
// is judged against the same instant, and the annotations the filters see are
// the same ones the spec 094 discovery filters see.
//
// A proxy with NO runtime wired at all is a pure-unit construction: both reads
// come back nil and the evaluator makes no connection-state claim, which is
// honest, whereas a fabricated "ready" or "unhealthy" would not be. But once a
// runtime IS wired, a missing supervisor / stateview / snapshot is the degraded
// process state FR-006 names: the served surface must refuse with 503 rather
// than evaluate blind, so that case returns ErrRuntimeUnavailable.
func (p *MCPProxyServer) preflightSnapshot() (preflight.StateReader, func(serverName, toolName string) *config.ToolAnnotations, error) {
	// The one injectable seam (nil in production): a test can supply the
	// snapshot a fixture without a live supervisor cannot produce, and still
	// exercise every line of glue below the snapshot read.
	if p.preflightStateSource != nil {
		return p.preflightStateSource()
	}
	if p.mainServer == nil || p.mainServer.runtime == nil {
		return nil, nil, nil
	}
	supervisor := p.mainServer.runtime.Supervisor()
	if supervisor == nil {
		return nil, nil, fmt.Errorf("%w: the supervisor is not running", preflight.ErrRuntimeUnavailable)
	}
	view := supervisor.StateView()
	if view == nil {
		return nil, nil, fmt.Errorf("%w: no connection-state view", preflight.ErrRuntimeUnavailable)
	}
	snapshot := view.Snapshot()
	if snapshot == nil {
		return nil, nil, fmt.Errorf("%w: the connection-state snapshot is empty", preflight.ErrRuntimeUnavailable)
	}

	servers := snapshot.Servers
	annotations := func(serverName, toolName string) *config.ToolAnnotations {
		status, ok := servers[serverName]
		if !ok || status == nil {
			return nil
		}
		for _, tool := range status.Tools {
			// The snapshot stores bare names on the live path and canonical
			// "server:tool" names when they came from ToolMetadata; match both.
			if tool.Name == toolName || tool.Name == serverName+":"+toolName {
				return tool.Annotations
			}
		}
		return nil
	}
	return &preflightStateSnapshot{servers: servers, proxy: p}, annotations, nil
}

type preflightStateSnapshot struct {
	servers map[string]*stateview.ServerStatus
	// proxy supplies the live-connection token comparison the dispatch-side
	// identity read makes (liveConnectionEpoch); nil for a snapshot a test
	// injects without a proxy, which then makes no token claim.
	proxy *MCPProxyServer
}

// ToolIdentity is the preflight projection of the dispatch-side identity
// read (resolveExactToolIdentity: Spec 105 FR-009, research D4; astra r2
// C2), resolved against the SAME snapshot the connection verdict reads, so a
// batch is judged against one instant and preflight can never disagree with
// dispatch about what the snapshot lists. Hydration takes the persisted
// server record, as dispatch does (codex r6 G1); the evaluator has already
// answered quarantined / disabled from the storage-backed ServerPolicy
// ahead of this read, so the record here only keeps the identity's own
// verdict on the same footing as dispatch's.
func (s *preflightStateSnapshot) ToolIdentity(serverName, toolName string) preflight.ToolIdentity {
	if s.proxy == nil {
		return preflight.ToolIdentity{}
	}
	identity := s.proxy.resolveExactToolIdentityIn(s.servers, serverName, toolName, s.proxy.persistedServerRecord(serverName))
	return preflight.ToolIdentity{
		Known:         identity.ServerKnown,
		Hydrated:      identity.SnapshotHydrated,
		DiscoveryDone: identity.DiscoveryDone,
		Found:         identity.Found,
	}
}

func (s *preflightStateSnapshot) ServerRuntime(serverName string) (preflight.ServerRuntime, bool) {
	status, ok := s.servers[serverName]
	if !ok || status == nil {
		return preflight.ServerRuntime{}, false
	}
	state := preflightRuntimeState(status.State)
	if state == preflight.RuntimeStateUnknown {
		// An unmapped actor state ("idle", "unknown") is not evidence of
		// anything: report "no entry" so the evaluator stays silent about the
		// connection rather than guessing.
		return preflight.ServerRuntime{}, false
	}
	return preflight.ServerRuntime{
		State:  state,
		Detail: preflightRuntimeDetail(status),
	}, true
}

// preflightRuntimeState maps the stateview's lowercased actor-state string
// (supervisor.updateStateView writes strings.ToLower(ConnectionState.String()))
// onto the evaluator's normalized vocabulary.
func preflightRuntimeState(state string) preflight.ServerRuntimeState {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "ready", "connected":
		return preflight.RuntimeStateReady
	case "connecting":
		return preflight.RuntimeStateConnecting
	case "discovering":
		return preflight.RuntimeStateDiscovering
	case "authenticating":
		return preflight.RuntimeStateAuthenticating
	case "pending auth", "pending_auth":
		return preflight.RuntimeStatePendingAuth
	case "disconnected":
		return preflight.RuntimeStateDisconnected
	case "error":
		return preflight.RuntimeStateError
	default:
		// "idle" (disabled/quarantined servers, already caught by the config
		// gates above the connection gates) and "unknown".
		return preflight.RuntimeStateUnknown
	}
}

// preflightRuntimeDetail prefers the spec 044 classified diagnostic over the raw
// last error: it is the same text the health surfaces show, so an operator sees
// one explanation, not two.
func preflightRuntimeDetail(status *stateview.ServerStatus) string {
	if status.Diagnostic != nil {
		if status.Diagnostic.Remediation != "" {
			return status.Diagnostic.Remediation
		}
		if status.Diagnostic.Cause != "" {
			return status.Diagnostic.Cause
		}
	}
	return status.LastError
}

// ---------------------------------------------------------------------------
// ConfigPolicy
// ---------------------------------------------------------------------------

type preflightConfigPolicy struct {
	proxy *MCPProxyServer
	cfg   *config.Config
	// servers memoizes the stored upstream record for the lifetime of ONE
	// request. Beyond saving a BBolt read per gate, it gives the whole batch a
	// consistent view: every tool in one preflight is judged against the same
	// server record, even if the config changes mid-evaluation. The read ERROR
	// is memoized alongside it, so a failure stays a failure for every tool in
	// the batch instead of resolving differently on a retry within one request.
	servers map[string]serverRecordResult
}

type serverRecordResult struct {
	record *config.ServerConfig
	err    error
}

// serverRecord reads (and memoizes) one stored upstream. "No such server" comes
// back as (nil, nil) — a verdict the evaluator can state — while any other read
// failure comes back as an error, because a record the process could not read
// says nothing about whether the server is configured.
func (c *preflightConfigPolicy) serverRecord(serverName string) (*config.ServerConfig, error) {
	if c.servers == nil {
		c.servers = make(map[string]serverRecordResult)
	}
	if cached, ok := c.servers[serverName]; ok {
		return cached.record, cached.err
	}

	record, err := c.proxy.storage.GetUpstreamServer(serverName)
	switch {
	case err == nil:
		// A nil record with a nil error is not a shape the storage seam
		// produces, but treating it as "not configured" is the honest reading.
	case errors.Is(err, storage.ErrUpstreamNotFound):
		record, err = nil, nil
	default:
		record, err = nil, fmt.Errorf("upstream record read for %q: %w", serverName, err)
	}

	c.servers[serverName] = serverRecordResult{record: record, err: err}
	return record, err
}

// ServerPolicy reads the server record from STORAGE — the same authority the
// dispatch gates consult (config.db is authoritative), so preflight and dispatch
// cannot disagree about enabled/quarantined state. A missing record is
// Found:false; an UNREADABLE one is an error, which the served surface answers
// with 503 rather than reporting the server as not configured (FR-005/FR-008: a
// reason code is a claim about proxy state, and a failed read supports none).
func (c *preflightConfigPolicy) ServerPolicy(serverName string) (preflight.ServerPolicy, error) {
	serverConfig, err := c.serverRecord(serverName)
	if err != nil {
		return preflight.ServerPolicy{}, err
	}
	if serverConfig == nil {
		return preflight.ServerPolicy{}, nil
	}
	return preflight.ServerPolicy{
		Found:                  true,
		Enabled:                serverConfig.Enabled,
		Quarantined:            serverConfig.Quarantined,
		AutoApproveToolChanges: serverConfig.IsAutoApproveToolChanges(),
	}, nil
}

// ToolConfigDenied delegates to the single call-time authority so the
// enabled_tools/disabled_tools verdict is byte-identical to the one dispatch
// applies (it prefers the live runtime config, falling back to the stored
// record).
func (c *preflightConfigPolicy) ToolConfigDenied(serverName, toolName string) (bool, error) {
	record, err := c.serverRecord(serverName)
	if err != nil {
		return false, err
	}
	return c.proxy.isToolConfigDenied(serverName, toolName, record), nil
}

func (c *preflightConfigPolicy) QuarantineEnabled() bool {
	return c.cfg.IsQuarantineEnabled()
}

// ---------------------------------------------------------------------------
// ServerController surface
// ---------------------------------------------------------------------------

// RunPreflight exposes the preflight evaluator on the ServerController surface
// the REST layer talks to (precedent: GetToolApprovalStatus). internal/httpapi
// never touches index/storage/stateview directly.
func (s *Server) RunPreflight(ctx context.Context, params preflight.Params) (preflight.Outcome, error) {
	if s == nil || s.mcpProxy == nil {
		return preflight.Outcome{}, preflight.ErrRuntimeUnavailable
	}
	return s.mcpProxy.RunPreflight(ctx, params)
}

// RecordPreflight writes one preflight's activity record synchronously and
// returns the write error (Spec 098 FR-014). It is exposed on the controller
// surface because the served preflight must persist the record BEFORE it
// answers 200 — a failure here is a 503, not a logged warning.
func (s *Server) RecordPreflight(rec runtime.PreflightActivity) error {
	if s == nil || s.runtime == nil {
		return runtime.ErrActivityUnavailable
	}
	return s.runtime.RecordPreflight(rec)
}
