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
	// visReasonToolNoApprovalRecord is the Spec 105 FR-009 implicit-pending
	// case: the tool is in the server's discovery snapshot and the quarantine
	// gate is active, but NO approval record exists yet (implicitPendingApproval).
	// It is withheld exactly like a stored pending record, but its remediation
	// differs — nothing is listed in the review UI to approve, the server's
	// next discovery pass files the record — so describe_tool answers with
	// the same no-record body dispatch does (toolPendingApprovalResult).
	visReasonToolNoApprovalRecord = "tool_no_approval_record"
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
//
// The pair arrives ALREADY SPLIT into server and RAW tool name (describe's
// splitServerTool, suggestCanonicalToolID's index read) and is consulted
// exactly from here — never normalized a second time (Spec 105 FR-009): a
// raw name that begins with the server's own prefix ("a:erase" on "a",
// canonical id "a:a:erase") would otherwise be gated on the sibling "erase"
// while the index lookup above already resolved its own document, so a
// pending "a:erase" rendered its definition on the approved sibling's gate
// and an approved one was withheld on the pending sibling's.
func (p *MCPProxyServer) toolVisibleToSession(ctx context.Context, serverName, toolName string) (visible bool, reason string) {
	if !p.toolIndexed(serverName, toolName) {
		return false, visReasonNotIndexed
	}
	authCtx := auth.AuthContextFromContext(ctx)
	_, profileScope := p.resolveActiveProfile(ctx)

	if !p.serverInScope(authCtx, profileScope, serverName) {
		return false, visReasonServerNotInScope
	}
	// describe_tool-only strict gates (contract steps 3–4) — ordered BEFORE
	// callability so a quarantined/pending id reports its real lock, not a
	// generic "disabled".
	if gateReason := p.describeGateReason(serverName, toolName); gateReason != "" {
		return false, gateReason
	}
	if !p.isExactToolCallable(serverName, toolName) {
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
//
// The pair arrives ALREADY SPLIT (the retrieve loop derives the raw name from
// the index hit once, config.RawToolName) and is consulted exactly — see
// toolVisibleToSession for why a second normalization is wrong.
func (p *MCPProxyServer) indexedToolVisible(authCtx *auth.AuthContext, profileScope *profile.ProfileScope, serverName, toolName string) (visible bool, reason string) {
	// Profile scope (Spec 057) + agent-token server scope (Spec 028) —
	// applied BEFORE any classification so an agent never learns a tool
	// exists on a server it cannot access.
	if !p.serverInScope(authCtx, profileScope, serverName) {
		return false, visReasonServerNotInScope
	}

	// Callability: disabled/blocked tools are non-existent for discovery.
	if !p.isExactToolCallable(serverName, toolName) {
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
//
// Spec 098 FR-002: both gates now read from the shared toolGate primitive, so
// describe_tool, dispatch and the preflight evaluator consult exactly one
// evaluation of the quarantine/approval state. The gate order (server
// quarantine, then the tool-level lock) is unchanged.
//
// The pair arrives already normalized by toolVisibleToSession, so the gate is
// read exactly rather than normalized a second time.
func (p *MCPProxyServer) describeGateReason(serverName, toolName string) string {
	gate := p.evaluateExactToolGate(serverName, toolName)
	if gate.serverQuarantined() {
		return visReasonServerQuarantined
	}
	switch gate.lockStatus {
	case storage.ToolApprovalStatusPending:
		if isImplicitPendingApproval(gate.approval) {
			return visReasonToolNoApprovalRecord
		}
		return visReasonToolPendingApproval
	case storage.ToolApprovalStatusChanged:
		return visReasonToolChangedApproval
	}
	return ""
}

// normalizeServerTool strips the indexed "server:tool" prefix from toolName
// (result.Tool.Name keeps the prefix when ServerName is set) — exactly like
// isToolCallable, so approval/config lookups key consistently.
//
// Spec 105 FR-009: only the SERVER's own prefix is an indexing artifact. When
// serverName is already known and the first ":"-segment is something else,
// the colon belongs to the raw tool name ("ns:erase" on server "a") and is
// kept, so every gate keyed on this pair — tier classification, approval,
// config denial, callability — evaluates the exact identity that is
// dispatched instead of the suffix tool's.
func normalizeServerTool(serverName, toolName string) (string, string) {
	if prefix, rest, ok := strings.Cut(toolName, ":"); ok {
		switch {
		case serverName == "":
			serverName, toolName = prefix, rest
		case prefix == serverName:
			toolName = rest
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

// lookupIndexedTool resolves a (server, RAW tool) pair to its indexed metadata
// — the same corpus retrieve_tools ranks over, so describe_tool definitions
// and search entries render from identical inputs. nil when absent. The
// indexed Name is the canonical "<server>:<raw>" id and is matched exactly
// (Spec 105 FR-009): a bare-name alternate could only match when a raw name
// equals a sibling's canonical id (raw "a:erase" on server "a" → the "erase"
// doc), rendering one tool's schema under another's id.
func (p *MCPProxyServer) lookupIndexedTool(serverName, toolName string) *config.ToolMetadata {
	tools, err := p.index.GetToolsByServer(serverName)
	if err != nil {
		return nil
	}
	full := serverName + ":" + toolName
	for _, tool := range tools {
		if tool.Name == full {
			return tool
		}
	}
	return nil
}

// splitServerTool splits a "<server>:<tool>" id. Whitespace is never
// significant in a canonical id, so both segments are trimmed. ok=false when
// the id has no server prefix or either segment is blank.
func splitServerTool(id string) (serverName, toolName string, ok bool) {
	parts := strings.SplitN(strings.TrimSpace(id), ":", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	serverName, toolName = strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
	if serverName == "" || toolName == "" {
		return "", "", false
	}
	return serverName, toolName, true
}

// suggestCanonicalToolID resolves an id that differs from an indexed one only
// by letter case to its canonical form.
//
// Spec 102 T056: this stays the INDEX-backed resolver, byte-identical, for the
// retrieve surfaces. The direct surface has its own — suggestDirectToolID in
// mcp_describe_direct.go — because it must draw from the catalog and gate on the
// direct listing's rules (the operation-permission tier and withheld display-
// name collisions, neither of which exists here). The seam is therefore the
// call site in resolveDescribeDefinition rather than a branch inside this
// function: threading a surface flag through here would put direct-only rules
// inside the predicate three retrieve-path callers share. Case is NOT folded on any resolution
// path: server and tool names are exact keys in the approval, quarantine,
// profile and agent-scope stores, so accepting a miscased id would route a
// call around gates keyed on the exact name. The correction is therefore only
// ever suggested — and only when the corrected pair is visible to this
// session, so a suggestion can never confirm that an out-of-scope or
// quarantined tool exists.
func (p *MCPProxyServer) suggestCanonicalToolID(ctx context.Context, serverName, toolName string) (string, bool) {
	servers, err := p.index.GetAllIndexedServerNames()
	if err != nil {
		return "", false
	}
	for _, server := range servers {
		if !strings.EqualFold(server, serverName) {
			continue
		}
		tools, terr := p.index.GetToolsByServer(server)
		if terr != nil {
			continue
		}
		for _, tool := range tools {
			bare := config.RawToolName(tool)
			if !strings.EqualFold(bare, toolName) || (server == serverName && bare == toolName) {
				continue
			}
			if visible, _ := p.toolVisibleToSession(ctx, server, bare); visible {
				return server + ":" + bare, true
			}
		}
	}
	return "", false
}
