package server

import (
	"context"
	"errors"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/preflight"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/telemetry"
)

type directCallabilityDecision struct {
	callable       bool
	serverName     string
	toolName       string
	serverConfig   *config.ServerConfig
	approval       *storage.ToolApprovalRecord
	approvalStatus string
	configDenied   bool
	storageErr     error
}

type directCallabilityEvaluator struct {
	proxy         *MCPProxyServer
	serverConfigs map[string]*config.ServerConfig
	serverErrors  map[string]error
	approvals     map[string]*storage.ToolApprovalRecord
	approvalErrs  map[string]error
}

func newDirectCallabilityEvaluator(proxy *MCPProxyServer) *directCallabilityEvaluator {
	return &directCallabilityEvaluator{
		proxy:         proxy,
		serverConfigs: make(map[string]*config.ServerConfig),
		serverErrors:  make(map[string]error),
		approvals:     make(map[string]*storage.ToolApprovalRecord),
		approvalErrs:  make(map[string]error),
	}
}

// filterDirectToolsForAgentCallability hides direct-mode tools that a
// scope-restricted caller (an agent token or an OAuth-authenticated user —
// see isScopeRestrictedCaller) cannot actually invoke because they are
// disabled, quarantined, pending approval, or changed since approval.
// Administrator contexts keep the existing operator-visible discovery
// behavior: server-edition-multiuser-auth.md documents the admin role as the
// one that "sees all activity, manages users" and reviews pending/quarantined
// tools, which a plain "user" account is not (it is caller-bounded exactly
// like an agent token — see cache_authz.go's CallerKindUser case).
func (p *MCPProxyServer) filterDirectToolsForAgentCallability(ctx context.Context, tools []mcp.Tool) []mcp.Tool {
	if len(tools) == 0 {
		return tools
	}

	authCtx := auth.AuthContextFromContext(ctx)
	if !isScopeRestrictedCaller(authCtx) {
		return tools
	}

	evaluator := newDirectCallabilityEvaluator(p)
	filtered := make([]mcp.Tool, 0, len(tools))
	for _, tool := range tools {
		var serverName, toolName string

		if stamp, stamped := readDirectToolStamp(tool); stamped {
			// Spec 105 FR-008: the identity STAMPED on this exact tool object,
			// never re-derived from a fresh catalog lookup (see
			// directToolStamp's doc comment).
			if stamp.rawName == "" {
				continue
			}
			serverName, toolName = stamp.owner, stamp.rawName
		} else {
			// No stamp: fall back to the pre-105 catalog/builtin resolution,
			// exactly as filterDirectModeToolsForAuth does. Same catalog
			// resolution the scope filter uses (D10): the two filters run over
			// the same listing, so if they resolved names differently — one by
			// catalog, one by first-"__" parse — a server whose name contains
			// "__" could be scope-checked as one origin and
			// callability-checked as another.
			//
			// Same residual as filterDirectModeToolsForAuth's fallback branch
			// (see its doc comment): an unstamped mcp-go SESSION tool sharing a
			// global tool's name would resolve here too. Not reachable today —
			// mcpproxy-go registers no session-specific tools on this surface.
			entry, decision := p.resolveDirectTool(tool.Name)

			switch decision {
			case directResolveBuiltin:
				// Built-ins are this proxy's own tools; there is no upstream
				// approval record to evaluate.
				filtered = append(filtered, tool)
				continue
			case directResolveDenied:
				continue
			case directResolveNoCatalog:
				serverName, toolName, _ = ParseDirectToolName(tool.Name)
			case directResolveFound:
				serverName, toolName = entry.ServerName, entry.ToolName
			}
		}

		if evaluator.evaluate(serverName, toolName).callable {
			filtered = append(filtered, tool)
		}
	}

	return filtered
}

// directEntryCallable is the callability half of the direct listing gate,
// for callers that already hold a resolved catalog entry (Spec 102 US2).
//
// Administrator sessions are unfiltered here, exactly as the loop above
// leaves them: the direct listing deliberately RETAINS tool-level
// pending/changed/disabled states for an operator, and describe_tool must
// therefore keep describing them — a listed tool is never undescribable
// (SC-007). Only scope-restricted callers (agent tokens and OAuth
// users — see isScopeRestrictedCaller), which cannot see those tools in
// their own listing, are gated. This MUST stay in parity with
// filterDirectToolsForAgentCallability above — the same SC-007 invariant.
func (p *MCPProxyServer) directEntryCallable(authCtx *auth.AuthContext, entry *directCatalogEntry) bool {
	if entry == nil {
		return false
	}
	if !isScopeRestrictedCaller(authCtx) {
		return true
	}
	return newDirectCallabilityEvaluator(p).evaluate(entry.ServerName, entry.ToolName).callable
}

// directToolCallabilityBlock returns a policy response when a direct-mode tool
// is not callable. It mirrors the call_tool_* policy boundary so direct mode
// cannot bypass disabled-tool, server-quarantine, or tool-approval controls.
func (p *MCPProxyServer) directToolCallabilityBlock(ctx context.Context, serverName, toolName string, args map[string]interface{}) *mcp.CallToolResult {
	result, _ := p.directToolCallabilityBlockWithReason(ctx, serverName, toolName, args)
	return result
}

// directToolCallabilityBlockWithReason is directToolCallabilityBlock plus the
// structured reason key of the gate that fired (issue #969). Direct mode routes
// server-quarantine, pending approval, changed approval, and plain
// not-callable through a SINGLE emit site, so without carrying the key out of
// the evaluator every direct-mode block would be counted as
// tool_not_callable — the availability reason distribution these counters exist
// to measure would be wrong for the whole direct surface. Returns ("" ) when
// the tool is callable.
func (p *MCPProxyServer) directToolCallabilityBlockWithReason(ctx context.Context, serverName, toolName string, args map[string]interface{}) (*mcp.CallToolResult, string) {
	// Unit tests historically construct a minimal MCPProxyServer with no
	// storage. Preserve that narrow behavior; production servers always have
	// storage and therefore enforce the policy below.
	if p.storage == nil {
		return nil, ""
	}

	decision := newDirectCallabilityEvaluator(p).evaluate(serverName, toolName)
	if decision.callable {
		return nil, ""
	}

	return p.directToolCallabilityResult(ctx, decision, args), directBlockReasonKey(decision)
}

// directRefusalKind is the ONE precedence order a direct-mode callability
// block resolves to, shared by directBlockReasonKey (telemetry) and
// directToolCallabilityResult (the response body) so the two can never
// disagree about which gate "fired" for a tool that trips more than one at
// once (PR #1326 review round 2, chunk B: a tool can be BOTH config-denied
// AND pending/changed approval at the same time, and the two functions used
// to classify that case differently — the response said config-denied, the
// telemetry said pending).
//
// The order mirrors the established dispatch precedence every OTHER path
// already uses (handleCallToolVariant / handleCallTool in mcp.go, via
// toolGate.lockStatus): quarantine, then the approval lock (pending/changed),
// then the generic/config-denied block. Direct mode's RESPONSE function had
// drifted from that precedence (config-denied was checked before the
// approval lock); this converges it rather than inventing a third order.
type directRefusalKind int

const (
	directRefusalNone directRefusalKind = iota
	directRefusalQuarantined
	directRefusalPending
	directRefusalChanged
	directRefusalConfigDenied
	directRefusalGeneric
)

// classifyDirectRefusal is the single source of truth for which refusal a
// blocked directCallabilityDecision represents. Both directBlockReasonKey and
// directToolCallabilityResult switch on its result instead of re-deriving
// their own branch order, so they cannot drift apart again.
func classifyDirectRefusal(decision directCallabilityDecision) directRefusalKind {
	switch {
	case decision.serverConfig != nil && decision.serverConfig.Quarantined:
		return directRefusalQuarantined
	case decision.approvalStatus == storage.ToolApprovalStatusPending:
		return directRefusalPending
	case decision.approvalStatus == storage.ToolApprovalStatusChanged:
		return directRefusalChanged
	case decision.configDenied:
		return directRefusalConfigDenied
	default:
		return directRefusalGeneric
	}
}

// directBlockReasonKey classifies a direct-mode callability block onto the
// closed telemetry.BlockReason* enum, from the SAME classification
// directToolCallabilityResult uses, so the counted reason always matches the
// payload the caller was handed.
func directBlockReasonKey(decision directCallabilityDecision) string {
	switch classifyDirectRefusal(decision) {
	case directRefusalQuarantined:
		return telemetry.BlockReasonServerQuarantined
	case directRefusalPending:
		return telemetry.BlockReasonToolPendingApproval
	case directRefusalChanged:
		return telemetry.BlockReasonToolChanged
	default:
		// Disabled server, config-denied tool, per-tool disable, and the
		// storage-error fallback all present as "not callable".
		return telemetry.BlockReasonToolNotCallable
	}
}

// evaluate classifies one direct-mode tool through the SHARED gate primitive
// (Spec 098 FR-002) so direct mode, the call_tool_* variants, code_execution and
// stored scripts cannot disagree about what is callable. The memoized storage
// reads below still exist because this evaluator runs over a whole tool list;
// only the classification moved.
func (e *directCallabilityEvaluator) evaluate(serverName, toolName string) directCallabilityDecision {
	decision := directCallabilityDecision{
		serverName: serverName,
		toolName:   toolName,
	}

	if e.proxy.storage == nil {
		return decision
	}

	serverConfig, serverErr := e.getServerConfig(serverName)
	if serverErr != nil || serverConfig == nil {
		decision.storageErr = serverErr
		return decision
	}
	decision.serverConfig = serverConfig
	configDenied := e.proxy.isToolConfigDenied(serverName, toolName, serverConfig)
	// The server-level gates own the response when they fire, so the fields that
	// SELECT a more specific refusal — the config-denial wording and the
	// approval-lock message below — are only surfaced once the server itself is
	// past them. Pre-098 this was an early return on `!Enabled || Quarantined`;
	// the flag is the same gate, kept so a disabled server still answers
	// "server disabled" rather than "tool pending approval".
	serverGatesPassed := serverConfig.Enabled && !serverConfig.Quarantined
	if serverGatesPassed {
		decision.configDenied = configDenied
	}

	// The LIVE config, like evaluateToolGate: a hot-reloaded quarantine_enabled
	// must take effect on the next call here too, or direct mode would keep
	// waving through tools the other dispatch paths have started refusing.
	cfg := e.proxy.currentConfig()
	quarantineEnabled := cfg == nil || cfg.IsQuarantineEnabled()
	quarantineGate := quarantineEnabled && !serverConfig.IsQuarantineSkipped()

	// The direct catalog IS the direct surface's discovery snapshot: every
	// caller of this evaluator holds a pair resolved through a catalog or
	// registry entry (the registered handler closes over its own entry, the
	// listing filter and describe resolve theirs), and that catalog is built
	// from a live tools/list of the server. So the tool is discovered by
	// construction here, and the classifier's Discovered input is true
	// regardless of what the StateView holds at this instant. The StateView is
	// consulted only for the description the pending body carries. Reading
	// Discovered from the StateView instead re-opened FR009-G5 on this
	// surface: the catalog is rebuilt on servers.changed, which on (re)connect
	// races the runtime's own discovery + checkToolApprovals pass, so a newly
	// added tool sat in the catalog with no record and no StateView entry —
	// and "not discovered, no record" classified as ready.
	const discovered = true
	identity := e.proxy.resolveExactToolIdentity(serverName, toolName)

	approval, approvalErr := e.getToolApproval(serverName, toolName)
	switch {
	case approvalErr == nil:
	case errors.Is(approvalErr, storage.ErrToolApprovalNotFound):
		// Spec 105 FR-009 (research D4): the same no-record rule as the
		// retrieve gate — a catalog tool with no record is pending under an
		// active gate, so /mcp/all cannot admit a name the call_tool_*
		// variants refuse. The reader (lookupToolApproval) already handed a
		// legacy collapsed record's lock to the namespaced name; this covers
		// the record that is genuinely absent.
		approval = implicitPendingApproval(serverName, toolName, discovered, identity.Description, quarantineGate)
		if approval != nil {
			e.proxy.logImplicitPending("direct", serverName, toolName)
		}
	default:
		decision.storageErr = approvalErr
		return decision
	}
	decision.approval = approval

	// approvalStatus drives the RESPONSE shape only, and keeps the pre-098
	// preference for the pending/changed message over the generic block, so a
	// refusal reads exactly as it always did.
	if serverGatesPassed && quarantineGate && approval != nil {
		switch approval.Status {
		case storage.ToolApprovalStatusPending, storage.ToolApprovalStatusChanged:
			decision.approvalStatus = approval.Status
		}
	}

	class := preflight.ClassifyTool(preflight.ClassifyInputs{
		Server: preflight.ServerPolicy{
			Found:                  true,
			Enabled:                serverConfig.Enabled,
			Quarantined:            serverConfig.Quarantined,
			AutoApproveToolChanges: serverConfig.IsQuarantineSkipped(),
		},
		QuarantineEnabled: quarantineEnabled,
		ConfigDenied:      configDenied,
		Approval:          approvalStateFor(approval),
		Discovered:        discovered,
	})
	decision.callable = class.Callable()
	return decision
}

func (e *directCallabilityEvaluator) getServerConfig(serverName string) (*config.ServerConfig, error) {
	if serverConfig, ok := e.serverConfigs[serverName]; ok {
		return serverConfig, e.serverErrors[serverName]
	}

	serverConfig, err := e.proxy.storage.GetUpstreamServer(serverName)
	e.serverConfigs[serverName] = serverConfig
	e.serverErrors[serverName] = err
	return serverConfig, err
}

// getToolApproval memoizes the shared FR-009 reader (lookupToolApproval) per
// (server, raw tool) pair for the lifetime of one evaluator, which runs over a
// whole listing. It reads the same rule the retrieve gate reads — exact record
// wins, a legacy collapsed record restricts but never approves — so the two
// surfaces cannot disagree about one tool.
func (e *directCallabilityEvaluator) getToolApproval(serverName, toolName string) (*storage.ToolApprovalRecord, error) {
	key := serverName + "\x00" + toolName
	if approval, ok := e.approvals[key]; ok {
		return approval, e.approvalErrs[key]
	}
	if err, ok := e.approvalErrs[key]; ok {
		return nil, err
	}

	approval, err := e.proxy.lookupToolApproval(serverName, toolName)
	if approval != nil {
		e.approvals[key] = approval
	}
	e.approvalErrs[key] = err
	return approval, err
}

func (p *MCPProxyServer) directToolCallabilityResult(ctx context.Context, decision directCallabilityDecision, args map[string]interface{}) *mcp.CallToolResult {
	switch classifyDirectRefusal(decision) {
	case directRefusalQuarantined:
		return p.handleQuarantinedToolCall(ctx, decision.serverName, decision.toolName, args)
	case directRefusalPending:
		return toolPendingApprovalResult(decision.serverName, decision.toolName, decision.approval)
	case directRefusalChanged:
		return toolChangedApprovalResult(decision.serverName, decision.toolName, decision.approval)
	case directRefusalConfigDenied:
		return mcp.NewToolResultError(blockedToolMessageFor(true))
	default:
		return mcp.NewToolResultError(p.blockedToolMessage(decision.serverName, decision.toolName))
	}
}
