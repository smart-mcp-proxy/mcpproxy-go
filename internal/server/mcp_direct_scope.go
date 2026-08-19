package server

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
)

// requiredPermissionForDirectTool derives the agent-token permission a direct
// tool requires from its annotations. It reuses the same variant->operation-type
// mapping that call-time authorization uses (see handleDirectToolCall in
// mcp_routing.go) so discovery filtering can never diverge from execution
// enforcement.
func requiredPermissionForDirectTool(annotations *config.ToolAnnotations) string {
	return contracts.ToolVariantToOperationType[contracts.DeriveCallWith(annotations)]
}

func (p *MCPProxyServer) setDirectToolPermissions(perms map[string]string) {
	p.directToolPermsMu.Lock()
	defer p.directToolPermsMu.Unlock()

	if len(perms) == 0 {
		p.directToolPerms = nil
		return
	}

	copied := make(map[string]string, len(perms))
	for name, perm := range perms {
		copied[name] = perm
	}
	p.directToolPerms = copied
}

func (p *MCPProxyServer) lookupDirectToolPermission(directName string) (string, bool) {
	p.directToolPermsMu.RLock()
	defer p.directToolPermsMu.RUnlock()

	perm, ok := p.directToolPerms[directName]
	return perm, ok
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
		serverName, _, ok := ParseDirectToolName(tool.Name)
		if !ok {
			filtered = append(filtered, tool)
			continue
		}

		if !profileScope.Allows(serverName) {
			continue
		}

		if !isScopedAgent {
			filtered = append(filtered, tool)
			continue
		}

		if !authCtx.CanAccessServer(serverName) {
			continue
		}

		requiredPerm, ok := p.lookupDirectToolPermission(tool.Name)
		if !ok {
			continue
		}

		if requiredPerm != "" && !authCtx.HasPermission(requiredPerm) {
			continue
		}

		filtered = append(filtered, tool)
	}

	return filtered
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
// analogue of filterDirectModeToolsForAuth and the ONLY access check on the
// aggregated-prompt get path: buildAggregatedServerPrompts wires each prompt
// handler straight to Manager.GetPrompt with zero auth checks, so without this
// filter a scoped agent token could fetch any upstream server's prompt over
// prompts/get even when the tool filters hid that server (PR #973 review,
// finding F1). mcp-go enforces this on both list and get (server.go
// filteredPrompts / passesPromptFilters, v0.57.0), so a prompt dropped here is
// neither discoverable nor retrievable.
//
// Built-in prompts are always kept. A display name that does not parse into
// server__prompt is also kept rather than dropped: the reverse mapping is
// best-effort (a server or prompt name that itself contains "__" can mis-split;
// that pre-existing ambiguity is out of scope for F1) and must never panic or
// blackhole a prompt whose owner cannot be identified.
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
	if !isScopedAgent && profileScope == nil {
		return prompts
	}

	filtered := make([]mcp.Prompt, 0, len(prompts))
	for _, prompt := range prompts {
		if _, isBuiltin := builtinPromptNames[prompt.Name]; isBuiltin {
			filtered = append(filtered, prompt)
			continue
		}

		serverName, _, ok := ParseDirectToolName(prompt.Name)
		if !ok {
			// Unidentifiable owner — keep rather than break (see doc comment).
			filtered = append(filtered, prompt)
			continue
		}

		// profileScope.Allows tolerates a nil receiver (returns true), so the
		// scoped-agent-without-profile case falls through correctly.
		if !profileScope.Allows(serverName) {
			continue
		}

		if isScopedAgent && !authCtx.CanAccessServer(serverName) {
			continue
		}

		filtered = append(filtered, prompt)
	}

	return filtered
}
