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

// describeErr* are the per-id error codes of the describe_tool contract
// (specs/085-compact-router/contracts/describe_tool.md).
const (
	describeErrNotFound        = "not_found"
	describeErrInvisible       = "invisible"
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
// FR-010/FR-011). The definition is budgeted at ≤150 tokens under tiktoken
// cl100k_base (the profiler's pinned encoder) — keep the prose short.
func buildDescribeToolTool() mcp.Tool {
	return mcp.NewTool("describe_tool",
		mcp.WithDescription("Return full JSON Schema + long description for specific tools found via retrieve_tools. Use when a compact signature is marked lossy ('~') or you need the exact schema before calling."),
		mcp.WithTitleAnnotation("Describe Tool"),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithOpenWorldHintAnnotation(false),
		mcp.WithArray("tool_ids",
			mcp.Required(),
			mcp.Description("1-5 tool ids in '<server>:<tool>' format, from retrieve_tools results."),
			mcp.WithStringItems(),
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
// text where applicable. Scope failures reuse the not-found remediation so the
// response never confirms that an out-of-scope tool exists.
func (p *MCPProxyServer) describeVisibilityError(reason, serverName, toolName string) (code, remediation string) {
	switch reason {
	case visReasonServerNotInScope:
		return describeErrInvisible, describeNotFoundRemediation
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
func (p *MCPProxyServer) handleDescribeTool(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	p.recordMCPSurface()
	p.recordBuiltinTool("describe_tool")

	startTime := time.Now()
	var sessionID string
	if sess := mcpserver.ClientSessionFromContext(ctx); sess != nil {
		sessionID = sess.SessionID()
	}
	requestID := fmt.Sprintf("%d-describe_tool", time.Now().UnixNano())

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
