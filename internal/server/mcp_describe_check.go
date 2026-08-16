package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/preflight"
	internalRuntime "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/toolannotations"
)

// Spec 099 — describe_tool check mode: the in-band preflight surface.
//
// The contract, in one place because the pieces break independently:
//
//   - `check: true` selects verdict-only mode; absent or false is today's
//     definition mode, byte-for-byte (FR-001/FR-011).
//   - A result NEVER carries a hash and the tier is ALWAYS agent-token, so
//     out-of-scope and unconfigured servers are byte-indistinguishable from an
//     unknown id (FR-004/FR-009). Nothing about the session's auth context can
//     raise that — /mcp is unauthenticated by default.
//   - Arguments are validated strictly rather than coerced (FR-012a): a
//     request this handler cannot honor exactly is an MCP tool error, never a
//     verdict computed from a guess.
//   - Every EXECUTED check writes its preflight activity record synchronously
//     before answering, and a failed write fails the call (FR-013). A rejected
//     request executes nothing and therefore records nothing, mirroring the
//     REST surface's 400 class.
const (
	// maxDescribeCheckIDs caps a check-mode batch (FR-005). It applies to the
	// RAW array, before trimming and dedup, so the cap is about request size
	// rather than about how much work the evaluator ends up doing — the same
	// reading the REST surface gives its own limit.
	maxDescribeCheckIDs = 50

	// describeCheckReservedHashes is the parameter trimmed from v1 (FR-008).
	// The name stays RESERVED: a request carrying it is rejected rather than
	// silently ignored, so in-band pins can be added later without a window in
	// which a caller believed a pin was checked when it was not.
	describeCheckReservedHashes = "expect_hashes"
)

// describeCheckFilterKeys is the closed set of `filters` members (FR-007), in
// the order the error message lists them.
var describeCheckFilterKeys = []string{"read_only_only", "exclude_destructive", "exclude_open_world"}

// describeToolMode is the parsed, validated shape of one describe_tool request
// as far as MODE selection goes. Filters are only ever populated in check mode.
type describeToolMode struct {
	check   bool
	filters toolannotations.Filters
}

// describeCheckResult is one per-id verdict on the wire (FR-004): the spec-098
// result projected onto the MCP payload, minus every operator-only field.
// Retryable is a pointer so a ready result carries no `retryable: false`, which
// would read as a failure.
type describeCheckResult struct {
	ID          string   `json:"id"`
	Status      string   `json:"status"`
	Reason      string   `json:"reason,omitempty"`
	Retryable   *bool    `json:"retryable,omitempty"`
	Action      string   `json:"action,omitempty"`
	Detail      string   `json:"detail,omitempty"`
	Remediation string   `json:"remediation,omitempty"`
	DidYouMean  []string `json:"did_you_mean,omitempty"`
}

// describeCheckPayload is the check-mode response. It carries neither
// `definitions` nor `errors`: a caller branches on the presence of `verdict`.
type describeCheckPayload struct {
	Verdict   string                `json:"verdict"`
	CheckedAt time.Time             `json:"checked_at"`
	RequestID string                `json:"request_id"`
	Results   []describeCheckResult `json:"results"`
}

// parseDescribeToolMode decides which mode a describe_tool call selects and
// validates everything that only exists in check mode (FR-012a).
//
// It runs BEFORE tool_ids is read, so a request that misuses the new
// parameters is rejected on those grounds rather than on an id error that would
// send the caller looking in the wrong place. Plain-mode tolerance is untouched:
// every shape rejected here is unreachable in a pre-099 request.
func parseDescribeToolMode(request mcp.CallToolRequest) (describeToolMode, error) {
	args := request.GetArguments()

	// Reserved before anything else: the answer must be the same whether or not
	// the caller also asked for check mode.
	if _, ok := args[describeCheckReservedHashes]; ok {
		return describeToolMode{}, fmt.Errorf(
			"parameter '%s' is reserved and not accepted: hash pins are checked via POST /api/v1/preflight or 'mcpproxy tools preflight', not in band",
			describeCheckReservedHashes)
	}

	raw, hasCheck := args["check"]
	var check bool
	if hasCheck {
		value, ok := raw.(bool)
		if !ok {
			// null included: a caller that sent the field meant to use the mode,
			// and coercing it to plain mode would return schemas to a caller
			// expecting verdicts.
			return describeToolMode{}, fmt.Errorf("parameter 'check' must be a boolean, got %s", jsonTypeName(raw))
		}
		check = value
	}

	filtersRaw, hasFilters := args["filters"]
	if hasFilters && !check {
		// Ignoring it would let an agent believe a safety filter was applied
		// when it was not.
		return describeToolMode{}, errors.New("parameter 'filters' requires 'check': true; it does not apply when describe_tool returns definitions")
	}

	mode := describeToolMode{check: check}
	if !hasFilters {
		return mode, nil
	}

	filters, ok := filtersRaw.(map[string]any)
	if !ok {
		return describeToolMode{}, fmt.Errorf("parameter 'filters' must be an object with the boolean members %s, got %s",
			strings.Join(describeCheckFilterKeys, ", "), jsonTypeName(filtersRaw))
	}
	// Membership is checked over the whole object before any value, and in the
	// declared key order rather than in Go's randomized map order, so a request
	// with two mistakes always gets the same message back.
	for _, key := range slices.Sorted(maps.Keys(filters)) {
		if !slices.Contains(describeCheckFilterKeys, key) {
			return describeToolMode{}, fmt.Errorf("unknown member 'filters.%s': the annotation filters are %s",
				key, strings.Join(describeCheckFilterKeys, ", "))
		}
	}
	for _, key := range describeCheckFilterKeys {
		value, ok := filters[key]
		if !ok {
			continue
		}
		flag, isBool := value.(bool)
		if !isBool {
			return describeToolMode{}, fmt.Errorf("member 'filters.%s' must be a boolean, got %s", key, jsonTypeName(value))
		}
		switch key {
		case "read_only_only":
			mode.filters.ReadOnlyOnly = flag
		case "exclude_destructive":
			mode.filters.ExcludeDestructive = flag
		case "exclude_open_world":
			mode.filters.ExcludeOpenWorld = flag
		}
	}
	return mode, nil
}

// jsonTypeName names the JSON type of a decoded argument, for error text that
// tells the caller what they actually sent.
func jsonTypeName(value any) string {
	switch value.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case float64, int, int64:
		return "number"
	case string:
		return "string"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	default:
		return fmt.Sprintf("%T", value)
	}
}

// normalizeDescribeCheckIDs trims and deduplicates the raw id array exactly as
// the REST surface does (FR-006), preserving first-occurrence order. Ids are
// NOT parsed here: a malformed id is a per-id verdict the evaluator produces,
// never a batch failure.
func normalizeDescribeCheckIDs(raw []string) []preflight.ToolRef {
	seen := make(map[string]struct{}, len(raw))
	refs := make([]preflight.ToolRef, 0, len(raw))
	for _, id := range raw {
		id = strings.TrimSpace(id)
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		refs = append(refs, preflight.ToolRef{ID: id})
	}
	return refs
}

// handleDescribeToolCheck answers one check-mode call.
//
// requestID is the correlation id minted by handleDescribeTool: the SAME value
// goes into the response and into the activity record, so an agent can hand a
// human the id that finds the run (FR-004/FR-013).
func (p *MCPProxyServer) handleDescribeToolCheck(
	ctx context.Context,
	request mcp.CallToolRequest,
	mode describeToolMode,
	sessionID, requestID string,
) (*mcp.CallToolResult, error) {
	rawIDs, err := request.RequireStringSlice("tool_ids")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Missing required parameter 'tool_ids': %v", err)), nil
	}
	if len(rawIDs) == 0 {
		// Deliberately NOT the plain-mode wording: telling a check-mode caller
		// to supply "1-5 tool ids" would be wrong by an order of magnitude.
		return mcp.NewToolResultError(
			fmt.Sprintf("Missing required parameter 'tool_ids': provide 1-%d tool ids in '<server>:<tool>' format", maxDescribeCheckIDs)), nil
	}
	if len(rawIDs) > maxDescribeCheckIDs {
		// Anti-bulk-loophole: the whole call fails, nothing is evaluated.
		return mcp.NewToolResultError(
			fmt.Sprintf("too many tool_ids: %d (max %d with check:true). Narrow your selection.", len(rawIDs), maxDescribeCheckIDs)), nil
	}

	refs := normalizeDescribeCheckIDs(rawIDs)

	outcome, err := p.RunPreflightForSession(ctx, refs, mode.filters)
	if err != nil {
		return mcp.NewToolResultError(p.describeCheckRuntimeError(err)), nil
	}
	checkedAt := time.Now().UTC()

	// FR-013: durable BEFORE the verdict is returned. A check nobody can audit
	// afterwards is not answered — the in-band mirror of the REST 503.
	if err := p.recordPreflightActivity(
		describeCheckActivityRecord(ctx, outcome, rawIDs, mode.filters, sessionID, requestID)); err != nil {
		if p.logger != nil {
			p.logger.Error("describe_tool check: preflight activity record could not be persisted",
				zap.String("request_id", requestID), zap.Error(err))
		}
		return mcp.NewToolResultError(
			"Availability check unavailable: the activity record could not be persisted, so no verdict was returned."), nil
	}

	payload := describeCheckResponse(outcome, requestID, checkedAt)
	jsonResult, err := json.Marshal(payload)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to serialize availability check: %v", err)), nil
	}
	return mcp.NewToolResultText(string(jsonResult)), nil
}

// describeCheckRuntimeError maps an evaluation failure onto the message the
// agent sees (FR-012). Both classes say plainly that NO verdict was computed:
// an agent must never read a failure as "nothing is blocked".
func (p *MCPProxyServer) describeCheckRuntimeError(err error) string {
	if errors.Is(err, preflight.ErrRuntimeUnavailable) {
		return "Availability check unavailable: the proxy runtime is not ready to evaluate, so no verdict was computed."
	}
	// An index/storage/snapshot read failed. Reduced-fidelity verdicts are
	// worse than no verdict: the agent would gate its plan on a guess.
	if p.logger != nil {
		p.logger.Error("describe_tool check: preflight evaluation failed", zap.Error(err))
	}
	return "Availability check unavailable: local proxy state could not be read, so no verdict was computed."
}

// describeCheckResponse serializes one outcome (FR-004). No hash is emitted at
// any tier, and no definition field appears at all — that omission is what
// makes the 50-id cap safe.
func describeCheckResponse(outcome preflight.Outcome, requestID string, checkedAt time.Time) describeCheckPayload {
	payload := describeCheckPayload{
		Verdict:   outcome.Verdict,
		CheckedAt: checkedAt,
		RequestID: requestID,
		Results:   make([]describeCheckResult, 0, len(outcome.Results)),
	}
	if payload.Verdict == "" {
		payload.Verdict = preflight.VerdictReady
	}
	for i := range outcome.Results {
		result := outcome.Results[i]
		entry := describeCheckResult{
			ID:         result.ID,
			Status:     result.Status,
			DidYouMean: result.DidYouMean,
		}
		if result.Status != preflight.StatusReady {
			retryable := result.Retryable
			entry.Reason = result.Reason
			entry.Retryable = &retryable
			entry.Action = result.Action
			entry.Detail = result.Detail
			entry.Remediation = result.Remediation
		}
		payload.Results = append(payload.Results, entry)
	}
	return payload
}

// describeCheckActivityRecord builds the FR-013 payload: enum codes, counts and
// tool ids only — the same shape the REST surface writes, plus the surface
// marker that tells the two apart. ids_count is the count of UNIQUE ids, as on
// the REST surface, so the two records mean the same thing; the RAW request that
// produced them is recorded alongside it, so "how many ids did the agent
// actually send" survives the dedup that ids_count reports.
func describeCheckActivityRecord(
	ctx context.Context,
	outcome preflight.Outcome,
	rawIDs []string,
	filters toolannotations.Filters,
	sessionID, requestID string,
) internalRuntime.PreflightActivity {
	record := internalRuntime.PreflightActivity{
		RequestID: requestID,
		SessionID: sessionID,
		Source:    storage.ActivitySourceMCP,
		Surface:   storage.PreflightSurfaceMCPCheck,
		Verdict:   outcome.Verdict,
		Arguments: &internalRuntime.PreflightArguments{
			ToolIDs: rawIDs,
			Filters: describeCheckFilterNames(filters),
		},
		Tools: make([]internalRuntime.PreflightToolOutcome, 0, len(outcome.Results)),
	}
	if record.Verdict == "" {
		record.Verdict = preflight.VerdictReady
	}
	if authCtx := auth.AuthContextFromContext(ctx); authCtx != nil {
		record.UserID = authCtx.UserID
		record.UserEmail = authCtx.Email
	}
	for i := range outcome.Results {
		result := outcome.Results[i]
		entry := internalRuntime.PreflightToolOutcome{ID: result.ID, Status: result.Status}
		if result.Status != preflight.StatusReady {
			entry.Reason = result.Reason
		}
		record.Tools = append(record.Tools, entry)
	}
	return record
}

// describeCheckFilterNames lists the annotation filters in effect, in the order
// describe_tool declares them, for the activity record's raw arguments. Nil when
// none are set, so a filterless check records no filters key at all.
func describeCheckFilterNames(filters toolannotations.Filters) []string {
	var names []string
	for _, key := range describeCheckFilterKeys {
		var on bool
		switch key {
		case "read_only_only":
			on = filters.ReadOnlyOnly
		case "exclude_destructive":
			on = filters.ExcludeDestructive
		case "exclude_open_world":
			on = filters.ExcludeOpenWorld
		}
		if on {
			names = append(names, key)
		}
	}
	return names
}

// recordPreflightActivity writes one preflight record synchronously and returns
// the write error, so the caller can refuse to answer without it (FR-013).
//
// A proxy with no activity service cannot audit a check and therefore cannot
// answer one; tests that exercise the payload install a recorder rather than
// standing up a whole Runtime (mirrors workSessionResolver).
func (p *MCPProxyServer) recordPreflightActivity(record internalRuntime.PreflightActivity) error {
	if p.preflightRecorder != nil {
		return p.preflightRecorder(record)
	}
	if p.mainServer == nil || p.mainServer.runtime == nil {
		return internalRuntime.ErrActivityUnavailable
	}
	return p.mainServer.runtime.RecordPreflight(record)
}
