package server

import (
	"context"
	"strings"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Visibility reasons returned by the resolvers below (Spec 085 FR-011).
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

// The two resolvers share one set of step helpers (serverInScope,
// describeGateReason, isToolCallable) so they can never drift apart, but they
// are deliberately NOT the same predicate:
//
//   - indexedToolVisible — SEARCH visibility. Reproduces the merge-base
//     retrieve_tools filter semantics exactly (FR-006/FR-007 byte-identity):
//     scope → isToolCallable, nothing else. See the merge-base filter loop
//     (main: internal/server/mcp.go ~:1345-1363) and isToolCallable
//     (main: ~:5306-5357), which never consult ServerConfig.Quarantined or a
//     pending/changed approval Status at this point.
//   - toolVisibleToSession — describe_tool visibility. STRICTLY NARROWER:
//     the contract (contracts/describe_tool.md §Visibility pipeline) adds the
//     index-presence, server-quarantine and pending/changed-approval gates on
//     top of the search gates. Because it only ever ADDS gates, describe_tool
//     can never return a definition the same session's retrieve_tools would
//     not (FR-011, Constitution IV) — the invariant is an upper bound, and
//     holds by construction.

// toolVisibleToSession is describe_tool's id resolver (Spec 085 FR-011,
// research.md R10). Check order per the contract:
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

	serverName, toolName = normalizeServerTool(serverName, toolName)
	if !p.serverInScope(authCtx, profileScope, serverName) {
		return false, visReasonServerNotInScope
	}
	// describe_tool-only strict gates (contract steps 3–4) — ordered BEFORE
	// callability so a quarantined/pending id reports its real lock, not a
	// generic "disabled".
	if gateReason := p.describeGateReason(serverName, toolName); gateReason != "" {
		return false, gateReason
	}
	if !p.isToolCallable(serverName, toolName) {
		return false, visReasonToolNotCallable
	}
	return true, ""
}

// indexedToolVisible is the SEARCH visibility step for tools that are index
// hits by construction (the retrieve_tools result loop), for callers that
// have already resolved the per-request scope once. It is behavior-preserving
// with the merge-base inline filter (main: internal/server/mcp.go
// ~:1345-1363): profile+agent scope, then isToolCallable — server quarantine
// and pending/changed approvals are deliberately NOT gated here, because the
// merge-base FULL-mode result set did not gate them (FR-006). The quarantine
// second pass (collectQuarantinedToolMatches + `seen` dedupe) keeps handling
// quarantined servers exactly where it always did.
func (p *MCPProxyServer) indexedToolVisible(authCtx *auth.AuthContext, profileScope *profile.ProfileScope, serverName, toolName string) (visible bool, reason string) {
	serverName, toolName = normalizeServerTool(serverName, toolName)

	// Profile scope (Spec 057) + agent-token server scope (Spec 028) —
	// applied BEFORE any classification so an agent never learns a tool
	// exists on a server it cannot access.
	if !p.serverInScope(authCtx, profileScope, serverName) {
		return false, visReasonServerNotInScope
	}

	// Callability: disabled/blocked tools are non-existent for discovery.
	if !p.isToolCallable(serverName, toolName) {
		return false, visReasonToolNotCallable
	}

	return true, ""
}

// describeGateReason evaluates the describe_tool-only gates (contract steps
// 3–4) that search does NOT apply:
//
//	(3) server-level quarantine: a quarantined server's tool definitions
//	    (descriptions/schemas) are withheld — potential TPA payloads.
//	(4) tool-level approval (Spec 032): pending/changed tools are locked
//	    pending review. Same gating as the call path (mcp.go
//	    handleCallToolVariant): only when quarantine is enabled and the
//	    server doesn't skip it.
//
// Returns "" when neither gate fires.
func (p *MCPProxyServer) describeGateReason(serverName, toolName string) string {
	serverConfig, err := p.storage.GetUpstreamServer(serverName)
	if err == nil && serverConfig != nil && serverConfig.Quarantined {
		return visReasonServerQuarantined
	}

	if (p.config == nil || p.config.IsQuarantineEnabled()) &&
		serverConfig != nil && !serverConfig.IsQuarantineSkipped() {
		if approval, aerr := p.storage.GetToolApproval(serverName, toolName); aerr == nil && approval != nil {
			switch approval.Status {
			case storage.ToolApprovalStatusPending:
				return visReasonToolPendingApproval
			case storage.ToolApprovalStatusChanged:
				return visReasonToolChangedApproval
			}
		}
	}
	return ""
}

// normalizeServerTool strips the indexed "server:tool" prefix from toolName
// (result.Tool.Name keeps the prefix when ServerName is set) — exactly like
// isToolCallable, so approval/config lookups key consistently.
func normalizeServerTool(serverName, toolName string) (string, string) {
	if strings.Contains(toolName, ":") {
		if parts := strings.SplitN(toolName, ":", 2); len(parts) == 2 {
			if serverName == "" {
				serverName = parts[0]
			}
			toolName = parts[1]
		}
	}
	return serverName, toolName
}

// serverInScope is the scope step shared by both resolvers — the former
// serverDiscoverable closure (agent-token scope, Spec 049 FR-007, + profile
// scope, Spec 057). Also used directly by the quarantined-tool discovery
// pass, so the three can never drift.
func (p *MCPProxyServer) serverInScope(authCtx *auth.AuthContext, profileScope *profile.ProfileScope, serverName string) bool {
	if authCtx != nil && !authCtx.IsAdmin() && !authCtx.CanAccessServer(serverName) {
		return false
	}
	return profileScope.Allows(serverName)
}

// toolIndexed reports whether the tool is present in the shared search index
// (describe_tool visibility step 1 — ids resolve against the same corpus
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
