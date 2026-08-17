package server

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
)

// maxDescribeToolIDs caps a describe_tool batch (Spec 085 FR-010). Matches the
// search default k and keeps describe_tool from becoming a bulk-dump loophole.
const maxDescribeToolIDs = 5

// describeToolTokenBudget is the ceiling on the marshalled describe_tool
// definition under tiktoken cl100k_base (Spec 099 FR-015). It replaces the
// spec-085 budget of 150: check mode costs ~+108 tokens once per session on two
// surfaces — about one upstream tool schema — and the ceiling is deliberately
// tight (the definition measures 243) so the next prose addition has to argue
// for itself. If it is ever hit, the answer is shorter prose or a trimmed
// parameter set, not a raised ceiling.
const describeToolTokenBudget = 250

// describeErr* are the per-id error codes of the describe_tool contract
// (specs/085-compact-router/contracts/describe_tool.md).
//
// Spec 099 FR-011 retires `invisible`: an out-of-scope id now reports
// not_found, byte-indistinguishable from an id that does not exist. A distinct
// code confirmed that a tool the session may not see exists — the leak the
// contract's shared not-found remediation was already written to prevent, and
// the one the spec-098 evaluator prevents on every other surface. The
// vocabulary is now not_found | quarantined | pending_approval | changed |
// disabled.
const (
	describeErrNotFound        = "not_found"
	describeErrQuarantined     = "quarantined"
	describeErrPendingApproval = "pending_approval"
	describeErrChanged         = "changed"
	describeErrDisabled        = "disabled"
)

// describeNotFoundRemediation is the standard "gone between search and
// describe" hint (spec edge case). Deliberately reused for out-of-scope ids so
// the remediation text never confirms that an invisible tool exists.
const describeNotFoundRemediation = "Tool not found or no longer available; re-run retrieve_tools."

// buildDescribeToolTool constructs the describe_tool definition (Spec 085
// FR-010/FR-011, Spec 099 FR-001/FR-002/FR-007). ONE builder feeds both
// surfaces that register the tool — the default /mcp server and the
// retrieve_tools routing mode — so the two schemas cannot drift.
//
// The definition is budgeted at ≤describeToolTokenBudget tokens under tiktoken
// cl100k_base (the profiler's pinned encoder) — keep the prose short. The
// budget rose from 150 to 250 with check mode (Spec 099 FR-015); the exact
// bytes are additionally pinned by the tools/list goldens, so a prose edit
// shows up as a reviewable diff rather than silent drift under the ceiling.
func buildDescribeToolTool() mcp.Tool {
	return mcp.NewTool("describe_tool",
		mcp.WithDescription("Return full JSON Schema + long description for specific tools found via retrieve_tools. Use when a compact signature is marked lossy ('~') or you need the exact schema before calling. With check:true it returns one availability verdict per id instead of schemas ('ready', or a reason code with retryable/action), to gate a plan before its first call."),
		mcp.WithTitleAnnotation("Describe Tool"),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithOpenWorldHintAnnotation(false),
		mcp.WithArray("tool_ids",
			mcp.Required(),
			mcp.Description("Tool ids in '<server>:<tool>' format from retrieve_tools. Max 5, or 50 with check:true."),
			mcp.WithStringItems(),
		),
		mcp.WithBoolean("check",
			mcp.Description("Check availability only, no schemas (default: false)."),
		),
		// The three annotation filters carry no per-property prose: their names
		// and semantics are the ones retrieve_tools already teaches, and
		// restating them here priced ~18 tokens on every session of two
		// surfaces (FR-007/FR-015).
		mcp.WithObject("filters",
			mcp.Description("check:true only. Annotation filters, as in retrieve_tools."),
			mcp.Properties(map[string]any{
				"read_only_only":      map[string]any{"type": "boolean"},
				"exclude_destructive": map[string]any{"type": "boolean"},
				"exclude_open_world":  map[string]any{"type": "boolean"},
			}),
		),
	)
}

// describeToolIDError renders one per-id error entry.
func describeToolIDError(id, code, remediation string) map[string]interface{} {
	return map[string]interface{}{
		"id":          id,
		"error":       code,
		"remediation": remediation,
	}
}

// describeVisibilityError maps a toolVisibleToSession reason to the contract's
// per-id error code + remediation, reusing the existing Spec 049 remediation
// text where applicable. Scope failures produce the plain not-found answer —
// code AND remediation — so the response never confirms that an out-of-scope
// tool exists (Spec 099 FR-011; the remediation was already shared, only the
// code moved).
func (p *MCPProxyServer) describeVisibilityError(reason, serverName, toolName string) (code, remediation string) {
	switch reason {
	case visReasonServerNotInScope:
		return describeErrNotFound, describeNotFoundRemediation
	case visReasonServerQuarantined:
		return describeErrQuarantined, disabledToolRemediation(contracts.DisabledStatusServerQuarantined)
	case visReasonToolPendingApproval:
		return describeErrPendingApproval, disabledToolRemediation(contracts.DisabledStatusPendingApproval)
	case visReasonToolChangedApproval:
		return describeErrChanged, disabledToolRemediation(contracts.DisabledStatusPendingApproval)
	case visReasonToolNotCallable:
		return describeErrDisabled, disabledToolRemediation(p.classifyDisabledTool(serverName, toolName))
	default: // visReasonNotIndexed and anything future
		return describeErrNotFound, describeNotFoundRemediation
	}
}

// handleDescribeTool implements the describe_tool built-in (Spec 085 US2,
// FR-010/011/012): a batch of 1–5 "<server>:<tool>" ids resolves to full
// definitions — field-equal to the full-mode retrieve_tools rendering over
// {name, description, inputSchema, server, annotations, call_with}, with the
// ranked-only score absent — plus per-id errors for ids that don't resolve.
//
// Every id runs through p.toolVisibleToSession — retrieve_tools' search gates
// (scope → callability) plus the STRICTER describe-only contract gates
// (index presence, server quarantine, pending/changed approval). Because it
// only adds gates on top of search's, describe_tool can never return a
// definition the same session's search would not (FR-011, Constitution IV).
// The handler never consults the response mode: output is identical under
// full and compact (FR-012).
//
// Spec 099 adds the `check: true` branch (mcp_describe_check.go), which answers
// verdict-only availability from the shared preflight evaluator instead of
// definitions. Everything below it is the definition path, unchanged.
func (p *MCPProxyServer) handleDescribeTool(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	p.recordMCPSurface()
	p.recordBuiltinTool("describe_tool")

	startTime := time.Now()
	var sessionID string
	if sess := mcpserver.ClientSessionFromContext(ctx); sess != nil {
		sessionID = sess.SessionID()
	}
	requestID := mintCorrelationID("describe_tool")

	// Spec 099 FR-001/FR-012a: mode selection and its strict validation run
	// before tool_ids is touched, so a misused new parameter is reported as
	// what it is. A rejected request executes nothing and therefore records
	// nothing — no verdict, and no internal_tool_call either, matching the REST
	// surface's 400 class.
	mode, err := parseDescribeToolMode(request)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if mode.check {
		return p.handleDescribeToolCheck(ctx, request, mode, sessionID, requestID)
	}

	emitError := func(errMsg string, args map[string]interface{}) {
		p.emitActivityInternalToolCall("describe_tool", "", "", "", sessionID, requestID,
			"error", errMsg, time.Since(startTime).Milliseconds(), args, nil, nil, "")
	}

	ids, err := request.RequireStringSlice("tool_ids")
	if err != nil {
		emitError(err.Error(), nil)
		return mcp.NewToolResultError(fmt.Sprintf("Missing required parameter 'tool_ids': %v", err)), nil
	}
	args := map[string]interface{}{"tool_ids": ids}
	if len(ids) == 0 {
		errMsg := "Missing required parameter 'tool_ids': provide 1-5 tool ids in '<server>:<tool>' format"
		emitError(errMsg, args)
		return mcp.NewToolResultError(errMsg), nil
	}
	// Anti-bulk-loophole (spec edge case): >5 ids fails the whole batch — no
	// partial dump.
	if len(ids) > maxDescribeToolIDs {
		errMsg := fmt.Sprintf("too many tool_ids: %d (max %d). Narrow your selection.", len(ids), maxDescribeToolIDs)
		emitError(errMsg, args)
		return mcp.NewToolResultError(errMsg), nil
	}

	definitions := make([]map[string]interface{}, 0, len(ids))
	idErrors := make([]map[string]interface{}, 0)
	for _, id := range ids {
		serverName, toolName, ok := splitServerTool(id)
		if !ok {
			idErrors = append(idErrors, describeToolIDError(id, describeErrNotFound,
				"Tool ids must use '<server>:<tool>' format, exactly as returned by retrieve_tools."))
			continue
		}

		visible, reason := p.toolVisibleToSession(ctx, serverName, toolName)
		if !visible {
			code, remediation := p.describeVisibilityError(reason, serverName, toolName)
			if reason == visReasonNotIndexed {
				if canonical, ok := p.suggestCanonicalToolID(ctx, serverName, toolName); ok {
					remediation = fmt.Sprintf("Tool not found. Tool ids are case-sensitive — did you mean '%s'?", canonical)
				}
			}
			idErrors = append(idErrors, describeToolIDError(id, code, remediation))
			continue
		}

		meta := p.lookupIndexedTool(serverName, toolName)
		if meta == nil {
			// Disappeared between the visibility check and the lookup.
			idErrors = append(idErrors, describeToolIDError(id, describeErrNotFound, describeNotFoundRemediation))
			continue
		}

		// FR-010: the definition IS the full-mode entry (same builder, so the
		// shared fields cannot drift) minus the ranked-only score — a lookup
		// is not a ranked search.
		entry := p.buildToolEntry(&config.SearchResult{Tool: meta}, config.ToolResponseModeFull, toolEntryOpts{})
		delete(entry, "score")
		definitions = append(definitions, entry)
	}

	response := map[string]interface{}{
		"definitions": definitions,
		"errors":      idErrors,
	}

	jsonResult, err := json.Marshal(response)
	if err != nil {
		emitError(err.Error(), args)
		return mcp.NewToolResultError(fmt.Sprintf("Failed to serialize definitions: %v", err)), nil
	}

	activityArgs := injectAuthMetadata(ctx, args)
	p.emitActivityInternalToolCall("describe_tool", "", "", "", sessionID, requestID,
		"success", "", time.Since(startTime).Milliseconds(), activityArgs, response, nil, "")

	return mcp.NewToolResultText(string(jsonResult)), nil
}
