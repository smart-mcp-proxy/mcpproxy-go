package server

import (
	"context"
	"strings"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Visibility reasons returned by toolVisibleToSession (Spec 085 FR-011).
// Callers use them to pick a response shape: scope failures are SILENT
// (an agent never learns a tool exists on a server it cannot access), the
// rest surface as locked/not-found with remediation.
const (
	visReasonNotIndexed          = "not_indexed"
	visReasonServerNotInScope    = "server_not_in_scope"
	visReasonServerQuarantined   = "server_quarantined"
	visReasonToolPendingApproval = "tool_pending_approval"
	visReasonToolChangedApproval = "tool_changed_approval"
	visReasonToolNotCallable     = "tool_not_callable"
)

// toolVisibleToSession is the ONE search-visibility predicate (Spec 085
// FR-011, research.md R10): retrieve_tools' result filtering and
// describe_tool's id resolution both go through it, so describe_tool can
// never return a definition the same session's retrieve_tools would not.
// Extracted from the retrieve handler's serverDiscoverable closure + its
// inline callable/quarantine passes, preserving the canonical order:
//
//	index presence → profile+agent scope → server quarantine →
//	tool approval (pending/changed, Spec 032) → isToolCallable
//
// The empty reason means visible.
func (p *MCPProxyServer) toolVisibleToSession(ctx context.Context, serverName, toolName string) (visible bool, reason string) {
	if !p.toolIndexed(serverName, toolName) {
		return false, visReasonNotIndexed
	}
	authCtx := auth.AuthContextFromContext(ctx)
	_, profileScope := p.resolveActiveProfile(ctx)
	return p.indexedToolVisible(authCtx, profileScope, serverName, toolName)
}

// indexedToolVisible is toolVisibleToSession minus the index-presence step,
// for callers whose tool is an index hit by construction (the retrieve_tools
// result loop) and that have already resolved the per-request scope once.
func (p *MCPProxyServer) indexedToolVisible(authCtx *auth.AuthContext, profileScope *profile.ProfileScope, serverName, toolName string) (visible bool, reason string) {
	// Callers may pass the indexed "server:tool" form as the tool name
	// (result.Tool.Name keeps the prefix when ServerName is set). Normalize
	// exactly like isToolCallable so the approval lookup keys agree.
	if strings.Contains(toolName, ":") {
		if parts := strings.SplitN(toolName, ":", 2); len(parts) == 2 {
			if serverName == "" {
				serverName = parts[0]
			}
			toolName = parts[1]
		}
	}

	// (2) profile scope (Spec 057) + agent-token server scope (Spec 028) —
	// applied BEFORE any classification so an agent never learns a tool
	// exists on a server it cannot access.
	if !p.serverInScope(authCtx, profileScope, serverName) {
		return false, visReasonServerNotInScope
	}

	// (3) server-level quarantine: a quarantined server's tool definitions
	// (descriptions/schemas) are withheld — potential TPA payloads.
	serverConfig, err := p.storage.GetUpstreamServer(serverName)
	if err == nil && serverConfig != nil && serverConfig.Quarantined {
		return false, visReasonServerQuarantined
	}

	// (4) tool-level approval (Spec 032): pending/changed tools are locked
	// pending review. Same gating as the call path (mcp.go
	// handleCallToolVariant): only when quarantine is enabled and the server
	// doesn't skip it.
	if (p.config == nil || p.config.IsQuarantineEnabled()) &&
		serverConfig != nil && !serverConfig.IsQuarantineSkipped() {
		if approval, aerr := p.storage.GetToolApproval(serverName, toolName); aerr == nil && approval != nil {
			switch approval.Status {
			case storage.ToolApprovalStatusPending:
				return false, visReasonToolPendingApproval
			case storage.ToolApprovalStatusChanged:
				return false, visReasonToolChangedApproval
			}
		}
	}

	// (5) callability: disabled/blocked tools are non-existent for discovery.
	if !p.isToolCallable(serverName, toolName) {
		return false, visReasonToolNotCallable
	}

	return true, ""
}

// serverInScope is the scope step of the shared visibility pipeline —
// the former serverDiscoverable closure (agent-token scope, Spec 049 FR-007,
// + profile scope, Spec 057). Shared by the retrieve result loop, the
// quarantined-tool discovery pass, and toolVisibleToSession so they can
// never drift.
func (p *MCPProxyServer) serverInScope(authCtx *auth.AuthContext, profileScope *profile.ProfileScope, serverName string) bool {
	if authCtx != nil && !authCtx.IsAdmin() && !authCtx.CanAccessServer(serverName) {
		return false
	}
	return profileScope.Allows(serverName)
}

// toolIndexed reports whether the tool is present in the shared search index
// (visibility step 1 — describe_tool resolves ids against the same corpus
// search ranks over).
func (p *MCPProxyServer) toolIndexed(serverName, toolName string) bool {
	return p.lookupIndexedTool(serverName, toolName) != nil
}

// lookupIndexedTool resolves a (server, tool) pair to its indexed metadata —
// the same corpus retrieve_tools ranks over, so describe_tool definitions and
// search entries render from identical inputs. nil when absent.
func (p *MCPProxyServer) lookupIndexedTool(serverName, toolName string) *config.ToolMetadata {
	tools, err := p.index.GetToolsByServer(serverName)
	if err != nil {
		return nil
	}
	full := serverName + ":" + toolName
	for _, tool := range tools {
		if tool.Name == full || tool.Name == toolName {
			return tool
		}
	}
	return nil
}

// splitServerTool splits a "<server>:<tool>" id. ok=false when the id has no
// server prefix.
func splitServerTool(id string) (serverName, toolName string, ok bool) {
	parts := strings.SplitN(id, ":", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}
