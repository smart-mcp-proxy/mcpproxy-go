package server

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
)

// directToolStampMetaKey is the _meta key under which renderDirectTools
// stamps a rendered direct-mode tool with the identity of the publication
// that produced it. Namespaced per the MCP _meta convention, mirroring
// aggregatedPromptServerMetaKey.
const directToolStampMetaKey = "app.mcpproxy/direct-tool-identity"

// directToolStamp is the value stored under directToolStampMetaKey (Spec 105
// FR-008).
//
// It exists so the scope and callability filters can authorize a rendered
// tool against the EXACT identity of the publication that produced it,
// without a second catalog lookup by display name. A lookup can resolve
// against whatever catalog generation happens to be published RIGHT NOW,
// which — during the SetTools-then-publish window every rebuild opens — can
// be a different generation than the one that rendered and registered this
// particular tool object and its handler (the publication-skew an origin flip
// exploits: "a__b__c" can mean server "a" tool "b__c" in one generation and
// server "a__b" tool "c" in the next, and a catalog lookup mid-window answers
// for whichever generation happens to be live, not the one this tool came
// from). Riding the stamp on the tool itself makes the filter's answer
// inseparable from the tool it is filtering.
//
// A private struct rather than bare strings, mirroring aggregatedPromptStamp,
// so an upstream tool that happens to carry our key in its own _meta can never
// be mistaken for a stamp (a string there does not type-assert to this
// struct), and the upstream's own Meta travels with it for restoration by
// stripDirectToolStamp. Marshalling it directly (which only an unfiltered
// reader could do) yields "{}": the fields are unexported.
type directToolStamp struct {
	owner    string
	rawName  string
	tier     string // requiredPermission ("read"/"write"/"destructive"/"")
	upstream *mcp.Meta
}

// stampDirectTool returns tool with a directToolStamp recording entry's
// identity written into its _meta, preserving whatever _meta the tool already
// carried (there is none today — direct tools are rendered from upstream
// definitions that do not set one — but mirroring stampAggregatedPromptServer
// keeps the two internal-stamp mechanisms symmetric and safe if that ever
// changes).
func stampDirectTool(tool mcp.Tool, entry *directCatalogEntry) mcp.Tool {
	meta := &mcp.Meta{AdditionalFields: map[string]any{}}
	if tool.Meta != nil {
		meta.ProgressToken = tool.Meta.ProgressToken
		for k, v := range tool.Meta.AdditionalFields {
			meta.AdditionalFields[k] = v
		}
	}
	meta.AdditionalFields[directToolStampMetaKey] = directToolStamp{
		owner:    entry.ServerName,
		rawName:  entry.ToolName,
		tier:     entry.RequiredPermission,
		upstream: tool.Meta,
	}
	tool.Meta = meta
	return tool
}

// readDirectToolStamp reads the identity stamped by stampDirectTool. ok is
// false for a tool that carries no stamp: every built-in (registered outside
// renderDirectTools), or any mcp.Tool value a caller constructs directly
// rather than through it (as unit tests exercising the discovery filters do).
func readDirectToolStamp(tool mcp.Tool) (directToolStamp, bool) {
	if tool.Meta == nil {
		return directToolStamp{}, false
	}
	stamp, ok := tool.Meta.AdditionalFields[directToolStampMetaKey].(directToolStamp)
	return stamp, ok
}

// stripDirectToolStamp returns tool with the internal identity stamp removed
// and its _meta restored to exactly what was there before stampDirectTool ran
// (nil stays nil), so client-visible output never carries mcpproxy's internal
// bookkeeping — for every caller, administrators included (Spec 105 FR-008).
// The registered tool itself is never mutated: mcp-go hands filters the
// stored value, and only the copy's Meta pointer is replaced.
func stripDirectToolStamp(tool mcp.Tool) mcp.Tool {
	stamp, ok := readDirectToolStamp(tool)
	if !ok {
		return tool
	}
	tool.Meta = stamp.upstream
	return tool
}

// stripDirectToolStampFilter is the TERMINAL tool filter registered on the
// direct server. mcp-go runs WithToolFilter filters in registration order,
// feeding each filter's output to the next (both for tools/list and for the
// call-time re-evaluation of a single tool), so registering this one LAST
// guarantees the scope and callability filters above it still see the stamp
// they authorize against, while every tool that survives them — for every
// caller, including administrators, who never went through those two filters'
// per-tool loop before this change — leaves with it removed.
func stripDirectToolStampFilter(_ context.Context, tools []mcp.Tool) []mcp.Tool {
	if len(tools) == 0 {
		return tools
	}
	out := make([]mcp.Tool, len(tools))
	for i, tool := range tools {
		out[i] = stripDirectToolStamp(tool)
	}
	return out
}

// directIdentityInScope is directEntryInScope's identity-only twin: the same
// scope+tier predicate, evaluated against a bare (owner, tier) pair rather
// than a *directCatalogEntry, so a caller resolving through a stamp (Spec 105
// FR-008) and a caller resolving through the catalog run the identical check.
func directIdentityInScope(
	authCtx *auth.AuthContext,
	profileScope *profile.ProfileScope,
	isScopedAgent bool,
	owner, tier string,
) bool {
	if !profileScope.Allows(owner) {
		return false
	}
	if !isScopedAgent {
		return true
	}
	if !authCtx.CanAccessServer(owner) {
		return false
	}
	if tier != "" && !authCtx.HasPermission(tier) {
		return false
	}
	return true
}

// requiredPermissionForDirectTool derives the agent-token permission a direct
// tool requires from its annotations. It reuses the same variant->operation-type
// mapping that call-time authorization uses (see handleDirectToolCall in
// mcp_routing.go) so discovery filtering can never diverge from execution
// enforcement.
func requiredPermissionForDirectTool(annotations *config.ToolAnnotations) string {
	return contracts.ToolVariantToOperationType[contracts.DeriveCallWith(annotations)]
}

// filterDirectModeToolsForAuth filters tools/list for scoped agent tokens and
// for any request with an active profile.
//
// Direct mode registers upstream tools globally as server__tool. Without this
// filter, scoped agent tokens prevent execution but still disclose tool names,
// descriptions, and schemas for servers outside their scope. Call-time auth is
// still authoritative; this filter only removes tools that the current token
// could not call from discovery responses.
//
// The profile filter (Spec 057) is applied to EVERY auth type, not just agent
// tokens: an unauthenticated /mcp/p/<slug> connection runs as an admin context
// yet must still be profile-filtered, exactly as it is on the retrieve_tools
// path (see indexedToolVisible). Direct mode previously honored no profile at
// all — a profile-pinned token saw and could call every server in its token
// scope — so the pin was enforced on one routing mode and not the other.
func (p *MCPProxyServer) filterDirectModeToolsForAuth(ctx context.Context, tools []mcp.Tool) []mcp.Tool {
	if len(tools) == 0 {
		return tools
	}

	authCtx := auth.AuthContextFromContext(ctx)
	_, profileScope := p.resolveActiveProfile(ctx)
	isScopedAgent := authCtx != nil && authCtx.Type == auth.AuthTypeAgent

	// Spec 105 FR-008 (FR008-G7): a tool with no registration identity is
	// withheld from EVERY caller, administrators included, so this filter can
	// no longer short-circuit for an unscoped/no-profile caller the way it
	// used to — that early return skipped the per-tool loop entirely and, with
	// it, the withholding check below.

	filtered := make([]mcp.Tool, 0, len(tools))
	for _, tool := range tools {
		if stamp, stamped := readDirectToolStamp(tool); stamped {
			// Authorize against the identity STAMPED on this exact tool
			// object — see directToolStamp's doc comment for why this must
			// never be re-derived from a fresh catalog lookup by name.
			if stamp.rawName == "" {
				// Defense in depth: buildDirectCatalog refuses to admit an
				// empty raw name, so a stamp should never carry one.
				continue
			}
			if !directIdentityInScope(authCtx, profileScope, isScopedAgent, stamp.owner, stamp.tier) {
				continue
			}
			filtered = append(filtered, tool)
			continue
		}

		// No stamp: either a built-in (registered outside renderDirectTools)
		// or an mcp.Tool value constructed directly rather than rendered from
		// the catalog (unit tests exercising this filter against a published
		// catalog). Fall back to the pre-105 name-based resolution unchanged.
		//
		// Residual (cross-review round 1): mcp-go's SessionWithTools mechanism
		// merges a session's OWN tools into the listing under the SAME name a
		// global tool may already hold, before any filter runs, and dispatches
		// to the session's handler in preference to the global one
		// (handleToolCall, server.go). A session tool registered that way would
		// arrive here unstamped and fall through to this catalog lookup, which
		// could resolve the name against the UNRELATED global entry — the exact
		// identity/handler skew this file exists to close, reopened one layer
		// up. mcpproxy-go registers no session-specific tools anywhere today
		// (grep SessionWithTools/GetSessionTools/AddSessionTools turns up
		// nothing outside mcp-go itself), so this is not reachable in
		// production; it becomes load-bearing the moment that changes, and
		// whatever adds session tools to this surface MUST stamp them too.
		//
		// Resolve through the catalog, NOT by re-parsing the display name.
		// ParseDirectToolName splits on the first "__", which mis-splits a server
		// name that itself contains "__" — so this filter could scope-check one
		// origin while dispatch executed another. The catalog resolves by the same
		// mapping the handler was registered from (D10/FR-011).
		entry, decision := p.resolveDirectTool(tool.Name)

		switch decision {
		case directResolveBuiltin:
			// A tool this proxy serves itself. It has no owning upstream server,
			// so neither profile scope nor token server-scope applies.
			filtered = append(filtered, tool)
			continue
		case directResolveDenied:
			// A catalog exists and does not admit this name: an unknown tool, or
			// one withheld for a display-name collision, or one refused
			// admission for carrying no registration identity (FR008-G7).
			// Dropping it is the point — re-parsing would pick an origin the
			// catalog refused to choose.
			continue
		case directResolveNoCatalog:
			// Nothing published yet — a proxy still coming up. Fall back to the
			// pre-catalog behaviour rather than deny, or startup would serve an
			// empty listing to everyone.
			serverName, _, _ := ParseDirectToolName(tool.Name)
			if !profileScope.Allows(serverName) {
				continue
			}
			if isScopedAgent {
				// With no catalog there is no permission tier to check, and the
				// retired directToolPermissions map behaved identically — a
				// missing tier DROPPED the tool. Failing closed on an unknown
				// tier is the safe direction, and this window is one rebuild
				// long.
				continue
			}
			filtered = append(filtered, tool)
			continue
		case directResolveFound:
		}

		if !directEntryInScope(authCtx, profileScope, isScopedAgent, entry) {
			continue
		}

		filtered = append(filtered, tool)
	}

	return filtered
}

// directEntryInScope is the scope+tier half of the direct listing gate, factored
// out of the loop above so describe_tool can apply the SAME test rather than a
// second copy of it (Spec 102 FR-011/SC-007).
//
// Sharing it is the point: listing-parity is the whole contract of describe on
// this surface — no id may be describable-but-unlisted, and none listed-but-
// undescribable — and a mirrored predicate is exactly how that drifts. The
// remaining half (agent callability) is directEntryCallable below.
func directEntryInScope(
	authCtx *auth.AuthContext,
	profileScope *profile.ProfileScope,
	isScopedAgent bool,
	entry *directCatalogEntry,
) bool {
	if entry == nil {
		return false
	}
	// The tier is the catalog entry's, derived from UPSTREAM annotations
	// exactly as dispatch derives it. Deriving it from the registered
	// mcp.Tool would read mcp-go's NewTool defaults — destructiveHint=true on
	// essentially every tool — and hide the catalog from read- and
	// write-scoped tokens while dispatch happily allowed the same calls
	// (D13 rule 3).
	return directIdentityInScope(authCtx, profileScope, isScopedAgent, entry.ServerName, entry.RequiredPermission)
}

// builtinPromptNames is the set of prompt display names mcpproxy serves itself
// (not aggregated from an upstream server). Built from the prompt constructors
// so it can never drift from registerPrompts / RefreshPrompts. These names carry
// no "server__" prefix and must stay visible to every caller regardless of
// agent-token scope or active profile.
var builtinPromptNames = map[string]struct{}{
	setupServerPrompt().Name:        {},
	troubleshootServerPrompt().Name: {},
}

// filterAggregatedPromptsForAuth filters prompts/list AND prompts/get for scoped
// agent tokens and for any request with an active profile. It is the prompt
// analogue of filterDirectModeToolsForAuth and the list-side half of the
// aggregated-prompt gate (the handler-side half is
// authorizeAggregatedPromptServer): without it a scoped agent token could
// discover any upstream server's prompt even when the tool filters hid that
// server (PR #973 review, finding F1). mcp-go enforces this on both list and
// get (server.go filteredPrompts / passesPromptFilters, v1.0.0), so a prompt
// dropped here is neither discoverable nor retrievable.
//
// Built-in prompts are always kept. The owning server of an upstream prompt is
// the canonical name stamped into the registered prompt's _meta at publication
// (Spec 104 FR-016g) — the same server its handler dispatches to, read from
// the same snapshot mcp-go is filtering. It is NOT re-parsed from the display
// name: "a__b__c" splits on the first "__" into owner "a", while the handler
// dispatches to "a__b", so a token scoped to "a" alone could list and fetch
// "a__b"'s prompt. An upstream prompt with no stamp cannot have come from
// buildAggregatedServerPrompts and has no registration identity to authorize
// it against, so it is dropped for EVERY caller — administrators and
// unrestricted agent tokens included, not only scoped ones (Spec 105
// FR-008/FR-006, SC-005 exception) — fail closed.
//
// The internal stamp is stripped from every prompt returned, for every caller,
// so the client-visible _meta is exactly what the upstream sent.
//
// Unlike the tool filter there is no permission-tier check: prompts have no
// read/write/destructive variant, so server scope (CanAccessServer) plus profile
// scope (Allows) is the complete gate.
func (p *MCPProxyServer) filterAggregatedPromptsForAuth(ctx context.Context, prompts []mcp.Prompt) []mcp.Prompt {
	if len(prompts) == 0 {
		return prompts
	}

	authCtx := auth.AuthContextFromContext(ctx)
	_, profileScope := p.resolveActiveProfile(ctx)
	isScopedAgent := authCtx != nil && authCtx.Type == auth.AuthTypeAgent
	enforce := isScopedAgent || profileScope != nil
	allowed := promptServerAllowed(authCtx, profileScope)

	filtered := make([]mcp.Prompt, 0, len(prompts))
	for _, prompt := range prompts {
		if _, isBuiltin := builtinPromptNames[prompt.Name]; isBuiltin {
			filtered = append(filtered, prompt)
			continue
		}

		serverName, stamped := aggregatedPromptServer(prompt)
		if !stamped {
			// Spec 105 FR-008/FR-006: no registration identity to authorize
			// against. Withheld from EVERY caller, administrators included
			// (SC-005 exception) — this can no longer be conditioned on
			// `enforce`, since an unrestricted or administrator caller must
			// also never see a no-identity prompt.
			if p.logger != nil {
				p.logger.Warn("dropping aggregated prompt with no canonical-owner stamp",
					zap.String("prompt", prompt.Name))
			}
			continue
		}
		if enforce && !allowed(serverName) {
			continue
		}

		filtered = append(filtered, stripAggregatedPromptServer(prompt))
	}

	return filtered
}

// promptServerAllowed returns the per-server access predicate for one caller:
// profile scope (Allows) plus, for scoped agent tokens, server scope
// (CanAccessServer). profileScope.Allows tolerates a nil receiver (returns
// true), so the scoped-agent-without-profile case falls through correctly.
// It is the ONE definition of "may this caller touch prompts on server X",
// shared by the list/get filter and by every aggregated prompt handler.
func promptServerAllowed(authCtx *auth.AuthContext, profileScope *profile.ProfileScope) func(serverName string) bool {
	isScopedAgent := authCtx != nil && authCtx.Type == auth.AuthTypeAgent
	return func(serverName string) bool {
		if !profileScope.Allows(serverName) {
			return false
		}
		return !isScopedAgent || authCtx.CanAccessServer(serverName)
	}
}

// authorizeAggregatedPromptServer is the handler-side gate every aggregated
// prompt handler runs against its OWN canonical server before dispatching
// (Spec 104 FR-016g, cross-review P1). The list/get filter authorizes against
// the owner record; that record and the registered handlers are published in
// two steps, and a display-name collision winner can differ between refreshes
// (upstream ListPrompts iterates a map), so a filter-only design has a window
// in which a caller allowed for the RECORDED owner invokes a handler that
// dispatches to a different server. Binding the check to the handler closure,
// which knows its server by construction, closes that window regardless of
// what the record says.
//
// Reachability: the list/get filter reads the owner from the SAME registered
// prompt (see filterAggregatedPromptsForAuth), so an owner mismatch can no
// longer get past the filter. What CAN reach this gate is a scope change
// between the two checks of one request — an administrator deleting the
// caller's pinned profile, or dropping this server from it, in that
// millisecond window — because scope is resolved per check, not per request.
// Dispatch is still blocked. The residual is cosmetic: mcp-go maps a handler
// error to INTERNAL_ERROR, and that code is not controllable from a handler,
// so a probe timed inside such a window could tell "registered but hidden"
// from "absent" by the error code even though the message is identical.
func (p *MCPProxyServer) authorizeAggregatedPromptServer(ctx context.Context, serverName string) error {
	authCtx := auth.AuthContextFromContext(ctx)
	_, profileScope := p.resolveActiveProfile(ctx)
	if promptServerAllowed(authCtx, profileScope)(serverName) {
		return nil
	}
	if p.logger != nil {
		p.logger.Warn("aggregated prompt handler denied: caller not authorized for the prompt's canonical server",
			zap.String("server", serverName))
	}
	return errPromptNotFound
}

// errPromptNotFound is the sentinel an aggregated prompt handler returns for a
// caller that may not reach its server. The handler wraps it in the exact
// message mcp-go uses for an unregistered name ("prompt '<name>' not found:
// prompt not found"), so the message cannot separate "hidden" from "absent".
var errPromptNotFound = mcpserver.ErrPromptNotFound
