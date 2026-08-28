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

// describeNotFoundRemediation is the standard "gone between listing and
// describe" hint (spec edge case). Deliberately reused for out-of-scope ids so
// the remediation text never confirms that an invisible tool exists.
//
// Spec 102 FR-009 made it surface-neutral: describe_tool is now registered on
// the direct surface too, where retrieve_tools is not exposed at all, so the
// old text told a stuck agent to call a tool it cannot see.
const describeNotFoundRemediation = "Tool not found or no longer available; list tools again."

// describeMalformedIDRemediation is the answer to an id that does not parse.
// Promoted from an inline literal so the surface-neutral wording lives in one
// place.
//
// It names ONLY the canonical form on purpose, even though the shared tool_ids
// prose advertises both (FR-011). This string is emitted exclusively by the
// INDEXED surface — the direct surface answers every unresolvable id with the
// plain not-found remediation instead, since a distinct "malformed" code there
// would tell a caller which of its ids were well-formed. So the one surface
// that can produce it accepts colon ids only, and telling its caller otherwise
// would send them round a loop that cannot succeed.
const describeMalformedIDRemediation = "Tool ids must use '<server>:<tool>' format, exactly as listed."

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
		mcp.WithDescription("Return full JSON Schema + long description for listed tools. Use when a signature is marked lossy ('~') or you need the exact schema before calling. With check:true it returns one availability verdict per id, not schemas ('ready', or a reason code with retryable/action), to gate a plan before its first call."),
		mcp.WithTitleAnnotation("Describe Tool"),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithOpenWorldHintAnnotation(false),
		mcp.WithArray("tool_ids",
			mcp.Required(),
			mcp.Description("Tool ids as listed: '<server>:<tool>' or '<server>__<tool>'. Max 5, or 50 with check:true."),
			mcp.WithStringItems(),
		),
		mcp.WithBoolean("check",
			mcp.Description("Check availability only, no schemas (default false)."),
		),
		// The three annotation filters carry no per-property prose: their names
		// and semantics are the ones the discovery surfaces already teach, and
		// restating them here priced ~18 tokens on every session of every
		// surface that registers this tool (FR-007/FR-015). The surface that
		// taught them is no longer named either: describe_tool is registered on
		// the direct surface too (Spec 102 FR-009), where retrieve_tools does
		// not exist.
		mcp.WithObject("filters",
			mcp.Description("check:true only. Annotation filters."),
			mcp.Properties(map[string]any{
				"read_only_only":      map[string]any{"type": "boolean"},
				"exclude_destructive": map[string]any{"type": "boolean"},
				"exclude_open_world":  map[string]any{"type": "boolean"},
			}),
		),
	)
}

// resolveDescribeDefinition resolves one id to its definition inputs on the
// given surface, or returns the per-id error entry to report instead.
//
// The two surfaces answer from different authorities and with different id
// grammars; everything downstream — the shared builder, the score deletion, the
// additive output_schema — is common.
func (p *MCPProxyServer) resolveDescribeDefinition(
	ctx context.Context,
	surface describeSurface,
	id string,
) (*config.ToolMetadata, toolEntryOpts, map[string]interface{}) {
	if surface == describeSurfaceDirect {
		entry, ok := p.resolveDirectDescribeID(ctx, id)
		if !ok {
			// One answer for malformed, unknown, withheld and invisible: on this
			// surface a distinct code would confirm the tool exists.
			remediation := describeNotFoundRemediation
			if corrected, ok := p.suggestDirectToolID(ctx, id); ok {
				remediation = fmt.Sprintf("Tool not found. Tool ids are case-sensitive — did you mean '%s'?", corrected)
			}
			return nil, toolEntryOpts{}, describeToolIDError(id, describeErrNotFound, remediation)
		}
		// The catalog entry carries the UPSTREAM annotations. Letting the
		// builder fall back to its StateView lookup would silently downgrade a
		// listed-but-pending destructive tool to call_with "read", because the
		// StateView does not carry it (D10).
		return entry.toolMetadata(), toolEntryOpts{annotationsOverride: entry.Annotations}, nil
	}

	serverName, toolName, ok := splitServerTool(id)
	if !ok {
		return nil, toolEntryOpts{}, describeToolIDError(id, describeErrNotFound, describeMalformedIDRemediation)
	}

	visible, reason := p.toolVisibleToSession(ctx, serverName, toolName)
	if !visible {
		code, remediation := p.describeVisibilityError(reason, serverName, toolName)
		if reason == visReasonNotIndexed {
			if canonical, ok := p.suggestCanonicalToolID(ctx, serverName, toolName); ok {
				remediation = fmt.Sprintf("Tool not found. Tool ids are case-sensitive — did you mean '%s'?", canonical)
			}
		}
		return nil, toolEntryOpts{}, describeToolIDError(id, code, remediation)
	}

	meta := p.lookupIndexedTool(serverName, toolName)
	if meta == nil {
		// Disappeared between the visibility check and the lookup.
		return nil, toolEntryOpts{}, describeToolIDError(id, describeErrNotFound, describeNotFoundRemediation)
	}
	return meta, toolEntryOpts{}, nil
}

// applyDescribeOutputSchema attaches the tool's declared output schema to a
// definition (Spec 102 D2/FR-006). Absent — not empty — when the tool declares
// none, and absent when what it declares is not valid JSON, since emitting a
// broken schema is worse than emitting none.
func applyDescribeOutputSchema(entry map[string]interface{}, outputSchemaJSON string) {
	if outputSchemaJSON == "" {
		return
	}
	raw := json.RawMessage(outputSchemaJSON)
	if !json.Valid(raw) {
		return
	}
	entry["output_schema"] = raw
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

// describeSurface names the id-resolution authority for ONE describe_tool
// registration (Spec 102 FR-011).
//
// describe_tool is a single builder and a single handler on purpose — the
// schemas and the response shape must not drift between surfaces. What DOES
// differ is which corpus an id resolves against, and the answer is not a
// property of the request: /mcp and the retrieve_tools routing mode resolve
// against the search index, /mcp/all and the direct routing mode against the
// published catalog. So the surface is bound at REGISTRATION time rather than
// sniffed from the context, which would make it spoofable and untestable.
type describeSurface int

const (
	// describeSurfaceIndexed is every pre-102 registration: ids resolve through
	// toolVisibleToSession against the shared search index. Byte-identical to
	// the pre-102 behaviour, which the frozen describe-plain corpus pins.
	describeSurfaceIndexed describeSurface = iota
	// describeSurfaceDirect is the direct enumeration surface: ids resolve
	// through the published catalog, in either accepted form.
	describeSurfaceDirect
)

// describeToolHandler builds the describe_tool handler for one surface.
func (p *MCPProxyServer) describeToolHandler(surface describeSurface) mcpserver.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return p.handleDescribeToolOnSurface(ctx, request, surface)
	}
}

// handleDescribeTool is the indexed-surface handler, kept as a method so the
// pre-102 registrations and their tests are untouched.
func (p *MCPProxyServer) handleDescribeTool(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return p.handleDescribeToolOnSurface(ctx, request, describeSurfaceIndexed)
}

// handleDescribeToolOnSurface implements the describe_tool built-in (Spec 085 US2,
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
func (p *MCPProxyServer) handleDescribeToolOnSurface(ctx context.Context, request mcp.CallToolRequest, surface describeSurface) (*mcp.CallToolResult, error) {
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
		return p.handleDescribeToolCheck(ctx, request, mode, surface, sessionID, requestID)
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
		meta, opts, idErr := p.resolveDescribeDefinition(ctx, surface, id)
		if idErr != nil {
			idErrors = append(idErrors, idErr)
			continue
		}

		// FR-010: the definition IS the full-mode entry (same builder, so the
		// shared fields cannot drift) minus the ranked-only score — a lookup
		// is not a ranked search.
		entry := p.buildToolEntry(&config.SearchResult{Tool: meta}, config.ToolResponseModeFull, opts)
		delete(entry, "score")

		// Spec 102 D2: `output_schema` is ADDITIVE and lands at the
		// definition-assembly seam, not inside buildFullToolEntry — putting it
		// in the shared builder would change full-mode retrieve_tools bytes for
		// every tool that declares one (R2), which FR-010 forbids.
		applyDescribeOutputSchema(entry, meta.OutputSchemaJSON)

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
