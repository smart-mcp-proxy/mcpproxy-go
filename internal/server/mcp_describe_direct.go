package server

import (
	"context"
	"strings"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/preflight"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/toolannotations"
)

// Spec 102 US2 — describe_tool's id resolver for the DIRECT surface.
//
// Every other surface resolves describe_tool ids through toolVisibleToSession,
// which reads the SEARCH INDEX. That is the wrong authority here, in both
// directions:
//
//   - The index is filtered at the TOOL level (pending/changed tools are kept
//     out of it), while the direct listing is filtered at the SERVER level and
//     deliberately retains those tools for a non-agent session. An index-backed
//     resolver would therefore answer not_found for ids the same session can
//     see in its own tools/list — making deferral strictly worse than full
//     mode, since the agent could neither read the schema from the listing nor
//     recover it from describe.
//   - Conversely the index contains tools from servers the direct listing never
//     projected, which would be a disclosure.
//
// So this surface resolves through the CATALOG — the exact snapshot tools/list
// rendered from — and applies the exact gates the listing applies. Both halves
// come from shared predicates (directEntryInScope, directEntryCallable) rather
// than from a second implementation of the same rules, because listing↔describe
// parity is the whole contract and a mirrored rule set is how it drifts.

// resolveDirectDescribeID maps one caller-supplied describe_tool id onto its
// catalog entry, or reports that this session may not see it.
//
// Both id forms are accepted (FR-011) and NEITHER is derived from the other:
// the display form is an exact key of the catalog's display map and the
// canonical form an exact key of its canonical map. Re-parsing a display name
// would split "we__ird__do_thing" on the first "__" into ("we", "ird__do_thing")
// — a tool that does not exist, on a server that does not either.
//
// Invisible and nonexistent are ONE answer on purpose: a distinct code would
// confirm that a tool the session may not see exists.
func (p *MCPProxyServer) resolveDirectDescribeID(ctx context.Context, id string) (*directCatalogEntry, bool) {
	return p.resolveDirectDescribeIDIn(ctx, p.loadDirectCatalog(), id)
}

// resolveDirectDescribeIDIn resolves against a CALLER-SUPPLIED snapshot, so a
// batch can pin one generation for its whole lifetime. A rebuild landing between
// two ids of the same request would otherwise let them be answered from
// different catalogs — and the check-mode planner and its evaluator corpus from
// different ones again.
func (p *MCPProxyServer) resolveDirectDescribeIDIn(ctx context.Context, cat *directCatalog, id string) (*directCatalogEntry, bool) {
	if cat == nil {
		// Nothing published yet. Unlike the discovery filters — which fall back
		// to permissive so a proxy still coming up does not serve an empty
		// listing — describe answers not_found: there is no snapshot to answer
		// FROM, and inventing one from the index would reintroduce exactly the
		// wrong authority this resolver exists to avoid.
		//
		// The two therefore disagree for as long as no catalog exists, in the
		// SAFE direction (listed, not describable). In production that window is
		// empty: the constructor publishes an empty catalog before serving its
		// first request (Phase 2 T025), so loadDirectCatalog is non-nil from
		// then on. It is reachable only by a test that builds a bare proxy.
		return nil, false
	}

	id = strings.TrimSpace(id)
	if id == "" {
		return nil, false
	}

	entry, ok := cat.Lookup(id)
	if !ok {
		// The canonical form. splitServerTool is deliberately NOT used to
		// re-derive a display name from it: the canonical map is keyed by the
		// same (server, tool) pair the handler was registered from.
		entry, ok = cat.LookupCanonical(id)
	}
	if !ok || entry == nil {
		return nil, false
	}

	if !p.directEntryVisibleToSession(ctx, entry) {
		return nil, false
	}
	return entry, true
}

// directEntryVisibleToSession applies the same gates this session's tools/list
// applies to the same entry: profile scope, agent-token server scope, the
// operation-permission tier, and agent callability.
func (p *MCPProxyServer) directEntryVisibleToSession(ctx context.Context, entry *directCatalogEntry) bool {
	authCtx := auth.AuthContextFromContext(ctx)
	_, profileScope := p.resolveActiveProfile(ctx)
	isScopedAgent := authCtx != nil && authCtx.Type == auth.AuthTypeAgent

	if !directEntryInScope(authCtx, profileScope, isScopedAgent, entry) {
		return false
	}
	return p.directEntryCallable(authCtx, entry)
}

// suggestDirectToolID corrects an id that differs from a listed one only by
// letter case (Spec 102 T055/T056).
//
// Case is NEVER folded on a resolution path: server and tool names are exact
// keys in the approval, quarantine, profile and agent-scope stores, so
// accepting a miscased id would route a call around gates keyed on the exact
// name. The correction is therefore only ever SUGGESTED.
//
// This is the direct-surface sibling of suggestCanonicalToolID, deliberately
// separate rather than a seam inside it: that function resolves through the
// search index and through toolVisibleToSession, and the retrieve surfaces must
// keep it byte-identical. What the two share is the discipline — a correction
// is offered only when the corrected id is VISIBLE to this session, so it can
// never confirm that an out-of-scope, over-tier or withheld tool exists.
//
// The correction is returned in the grammar the caller used, since this surface
// accepts both and an agent that typed a display name wants one back.
func (p *MCPProxyServer) suggestDirectToolID(ctx context.Context, id string) (string, bool) {
	cat := p.loadDirectCatalog()
	if cat == nil {
		return "", false
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return "", false
	}

	for _, display := range cat.DisplayNames() {
		entry, ok := cat.Lookup(display)
		if !ok || entry == nil {
			continue
		}
		canonical := entry.ServerName + ":" + entry.ToolName

		var corrected string
		switch {
		case strings.EqualFold(display, id) && display != id:
			corrected = display
		case strings.EqualFold(canonical, id) && canonical != id:
			corrected = canonical
		default:
			continue
		}

		if !p.directEntryVisibleToSession(ctx, entry) {
			return "", false
		}
		return corrected, true
	}
	return "", false
}

// ---------------------------------------------------------------------------
// check mode (T052)
// ---------------------------------------------------------------------------

// directCheckPlan is the id mapping for ONE direct-surface check-mode call.
//
// preflight.Evaluate accepts canonical "<server>:<tool>" ids only. Handing it a
// display-form id verbatim answers not_found for every tool an agent copied out
// of its own tools/list — so ids are canonicalized on the way in and the
// caller's own form is restored on the way out, in both the response and the
// activity record (D14). An operator correlating a complaint has only the
// agent's ids to search by.
type directCheckPlan struct {
	// order is the caller's ids, deduplicated, in first-occurrence order. The
	// evaluator answers in request order; this is the order the CALLER asked in,
	// which is what the response must preserve.
	order []string

	// refs are the canonical ids handed to the evaluator, for the visible
	// subset only.
	refs []preflight.ToolRef

	// canonicalID maps each caller id to the canonical id it was evaluated
	// under. It is deliberately many-to-one: two caller ids may name the same
	// tool in the two accepted grammars, and both must get that tool's verdict.
	canonicalID map[string]string

	// gated is the set of caller ids answered WITHOUT consulting the evaluator.
	gated map[string]struct{}

	// suggestions is the caller-VISIBLE corpus a gated id's did_you_mean is
	// drawn from, in both accepted grammars. It is the same corpus the
	// evaluator's own not_found path uses on this surface
	// (directCatalogIndexReader), which is what keeps a gated id and an absent
	// one answering alike: both suggest from what this session can list, and
	// neither can name anything it cannot.
	suggestions []string
}

// notFound answers one gated id exactly as the evaluator answers an absent one:
// preflight's own constructor, plus suggestions from the visible corpus.
//
// Going through preflight.NotFound rather than assembling a lookalike is what
// makes "this session may not see it" and "it does not exist" the same bytes.
// Suggestions are offered in the grammar the caller used, since this surface
// accepts both and an agent that typed a display name wants a display name back.
func (plan directCheckPlan) notFound(callerID string) preflight.Result {
	result := preflight.NotFound(callerID)
	if suggestions := preflight.Suggest(callerID, plan.suggestions); len(suggestions) > 0 {
		result.DidYouMean = suggestions
	}
	return result
}

// planDirectCheck partitions a raw id array into "evaluate this" and "answer
// not_found without asking".
//
// The gate deliberately runs BEFORE the evaluator rather than after it. The
// evaluator knows nothing about the two rules that hide a tool on this surface
// — the operation-permission tier and a withheld display-name collision — and
// it would happily answer `server_quarantined` or `ready` for an id this
// session cannot list. Both answers disclose that the tool exists; the listing
// says it does not. Gating first means one answer, and it is the evaluator's own
// not_found bytes (preflight.NotFound), so a gated id is indistinguishable from
// an absent one.
func (p *MCPProxyServer) planDirectCheck(ctx context.Context, cat *directCatalog, visible []*directCatalogEntry, rawIDs []string) directCheckPlan {
	plan := directCheckPlan{
		canonicalID: make(map[string]string, len(rawIDs)),
		gated:       make(map[string]struct{}, len(rawIDs)),
	}

	// Built once per call from the catalog, filtered by this session's own
	// visibility predicate — the same snapshot the evaluator's index reader
	// gets, so a gated id and an evaluated one suggest from one corpus.
	for _, entry := range visible {
		plan.suggestions = append(plan.suggestions,
			entry.DisplayName,
			entry.ServerName+":"+entry.ToolName)
	}

	seen := make(map[string]struct{}, len(rawIDs))
	seenCanonical := make(map[string]struct{}, len(rawIDs))
	for _, raw := range rawIDs {
		// Trimmed and deduplicated exactly as normalizeDescribeCheckIDs does, so
		// the two surfaces agree on what one id means.
		id := strings.TrimSpace(raw)
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		plan.order = append(plan.order, id)

		entry, ok := p.resolveDirectDescribeIDIn(ctx, cat, id)
		if !ok {
			plan.gated[id] = struct{}{}
			continue
		}

		// preflight ids are "<server>:<tool>", split on the FIRST colon. A
		// server name containing a colon therefore cannot be named in that
		// grammar at all: canonicalizing would hand the evaluator a different
		// (server, tool) pair and it would answer about another tool — or about
		// nothing, confidently. Refusing to evaluate is the lesser failure, and
		// it is loud: the id is listed and DESCRIBABLE (definition mode reads
		// the snapshot directly and never canonicalizes), only its availability
		// verdict is unavailable. Recorded as a known limitation in
		// specs/102-schema-deferred/tasks.md rather than papered over.
		if strings.Contains(entry.ServerName, ":") {
			plan.gated[id] = struct{}{}
			continue
		}

		canonical := entry.ServerName + ":" + entry.ToolName
		plan.canonicalID[id] = canonical
		// Two caller ids may legitimately name ONE tool — "gh__read_file" and
		// "gh:read_file" in the same batch are both valid on this surface. They
		// share a canonical ref (deduped here so the evaluator is asked once)
		// and both get that ref's verdict back under their own id. Gating the
		// second, as an earlier draft did, answered not_found for a tool the
		// caller could see and had just been told about.
		if _, queued := seenCanonical[canonical]; !queued {
			seenCanonical[canonical] = struct{}{}
			plan.refs = append(plan.refs, preflight.ToolRef{ID: canonical})
		}
	}

	return plan
}

// restore rebuilds the outcome in the CALLER's ids and the caller's order,
// splicing the gated ids back in.
//
// Evaluator results pass through with only their id rewritten: status, reason,
// retryable, action, detail and remediation are projected, never translated
// (D14). A listed-but-pending tool therefore still reports
// tool_pending_approval rather than a flattened not_found.
func (plan directCheckPlan) restore(outcome preflight.Outcome) preflight.Outcome {
	evaluated := make(map[string]preflight.Result, len(outcome.Results))
	for i := range outcome.Results {
		result := outcome.Results[i]
		evaluated[result.ID] = result
	}

	results := make([]preflight.Result, 0, len(plan.order))
	for _, callerID := range plan.order {
		if _, gated := plan.gated[callerID]; gated {
			results = append(results, plan.notFound(callerID))
			continue
		}
		result, ok := evaluated[plan.canonicalID[callerID]]
		if !ok {
			// The evaluator dropped an id it was handed. Answering "ready"
			// because nothing came back would be the worst possible default.
			results = append(results, plan.notFound(callerID))
			continue
		}
		result.ID = callerID
		results = append(results, result)
	}

	return preflight.Outcome{
		// Recomputed rather than carried over: the gated ids the evaluator never
		// saw still count toward the batch verdict.
		Verdict: preflight.VerdictForResults(results),
		Results: results,
	}
}

// visibleDirectEntries snapshots the catalog entries this session can list, in
// the catalog's own order.
func (p *MCPProxyServer) visibleDirectEntriesIn(ctx context.Context, cat *directCatalog) []*directCatalogEntry {
	if cat == nil {
		return nil
	}
	out := make([]*directCatalogEntry, 0, cat.Len())
	for _, name := range cat.DisplayNames() {
		entry, ok := cat.Lookup(name)
		if !ok || entry == nil || !p.directEntryVisibleToSession(ctx, entry) {
			continue
		}
		out = append(out, entry)
	}
	return out
}

// runDirectCheck evaluates one direct-surface check-mode batch.
func (p *MCPProxyServer) runDirectCheck(
	ctx context.Context,
	rawIDs []string,
	filters toolannotations.Filters,
) (preflight.Outcome, error) {
	// ONE snapshot for the whole call: the plan, the suggestion corpus and the
	// evaluator's index reader all resolve against the same generation, so a
	// rebuild mid-request cannot make them disagree about what exists.
	cat := p.loadDirectCatalog()
	visible := p.visibleDirectEntriesIn(ctx, cat)
	plan := p.planDirectCheck(ctx, cat, visible, rawIDs)

	// Every id was gated: there is nothing to evaluate, and calling the
	// evaluator with an empty ref set would still cost a state snapshot.
	if len(plan.refs) == 0 {
		return plan.restore(preflight.Outcome{}), nil
	}

	scope, err := p.sessionPreflightScope(ctx)
	if err != nil {
		return preflight.Outcome{}, err
	}

	// The visible corpus is resolved ONCE, here, and handed to the reader as a
	// fixed slice. Recomputing it per reader call would re-run a storage-backed
	// callability check for every catalog entry on every ToolsByServer — and,
	// worse, would let a storage write land mid-evaluation and make
	// IndexedServerNames and ToolsByServer disagree about the same tool.
	reader := &directCatalogIndexReader{entries: visible}

	outcome, err := p.evaluatePreflight(ctx, plan.refs, preflight.TierAgentToken, scope, filters, reader)
	if err != nil {
		return preflight.Outcome{}, err
	}
	return plan.restore(outcome), nil
}
