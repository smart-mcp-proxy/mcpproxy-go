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

// filterDirectModeToolsForAuth filters tools/list for scoped agent tokens.
//
// Direct mode registers upstream tools globally as server__tool. Without this
// filter, scoped agent tokens prevent execution but still disclose tool names,
// descriptions, and schemas for servers outside their scope. Call-time auth is
// still authoritative; this filter only removes tools that the current token
// could not call from discovery responses.
func (p *MCPProxyServer) filterDirectModeToolsForAuth(ctx context.Context, tools []mcp.Tool) []mcp.Tool {
	if len(tools) == 0 {
		return tools
	}

	authCtx := auth.AuthContextFromContext(ctx)
	if authCtx == nil || authCtx.Type != auth.AuthTypeAgent {
		return tools
	}

	filtered := make([]mcp.Tool, 0, len(tools))
	for _, tool := range tools {
		serverName, _, ok := ParseDirectToolName(tool.Name)
		if !ok {
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
