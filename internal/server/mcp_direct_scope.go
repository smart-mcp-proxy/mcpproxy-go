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
	if !isScopedAgent && profileScope == nil {
		return tools
	}

	filtered := make([]mcp.Tool, 0, len(tools))
	for _, tool := range tools {
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
			// one withheld for a display-name collision. Dropping it is the point
			// — re-parsing would pick an origin the catalog refused to choose.
			continue
		case directResolveNoCatalog:
			// Nothing published yet — a proxy still coming up. Fall back to the
			// pre-catalog behaviour rather than deny, or startup would serve an
			// empty listing to everyone.
			//
			// The parse cannot fail here: resolveDirectTool already classified a
			// separator-less name as a built-in, so anything reaching this branch
			// has a "__" in it.
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
	if !profileScope.Allows(entry.ServerName) {
		return false
	}
	if !isScopedAgent {
		return true
	}
	if !authCtx.CanAccessServer(entry.ServerName) {
		return false
	}
	// The tier is the catalog entry's, derived from UPSTREAM annotations
	// exactly as dispatch derives it. Deriving it from the registered
	// mcp.Tool would read mcp-go's NewTool defaults — destructiveHint=true on
	// essentially every tool — and hide the catalog from read- and
	// write-scoped tokens while dispatch happily allowed the same calls
	// (D13 rule 3).
	if entry.RequiredPermission != "" && !authCtx.HasPermission(entry.RequiredPermission) {
		return false
	}
	return true
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
// buildAggregatedServerPrompts and is dropped for scoped callers (fail closed).
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
		if enforce {
			if !stamped {
				if p.logger != nil {
					p.logger.Warn("dropping aggregated prompt with no canonical-owner stamp for scoped caller",
						zap.String("prompt", prompt.Name))
				}
				continue
			}
			if !allowed(serverName) {
				continue
			}
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
