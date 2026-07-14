// Spec 085 US3 — pre-dispatch argument validation (FR-013) and the
// self-healing invalid_params error renderer shared by Path A (pre-dispatch)
// and Path B (best-effort upstream classification). Contract:
// specs/085-compact-router/contracts/invalid-params-error.md.
package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"go.uber.org/zap"
)

// inputSchemaURI is the opaque in-memory resource URI used when compiling a
// tool's input schema. Mirrors internal/outputvalidation: a relative name
// would resolve against the process cwd and leak that path into validation
// error messages, which are embedded in agent-visible error bodies.
const inputSchemaURI = "mem://inputschema/schema"

// inputSchemaKey identifies a compiled input schema. The primary key is the
// Spec-032 tool hash (research.md R3 — tool-definition changes and index
// rebuilds naturally invalidate it); the schema content hash is a guard for
// hashless entries (tests, partial index rows) so two different schemas can
// never share a cache slot.
type inputSchemaKey struct {
	toolHash   string
	schemaHash uint64
}

// inputSchemaEntry is a memoized compile outcome: either compiled is non-nil,
// or sentinel marks the schema uncompilable (fail-open forever, FR-013b).
type inputSchemaEntry struct {
	compiled *jsonschema.Schema
	sentinel bool
}

// inputValidator validates call_tool_* arguments against the target tool's
// stored ParamsJSON before dispatch (Spec 085 FR-013). Safe for concurrent
// use. Compilation is memoized; validation of valid args is the only
// happy-path cost (no schema is ever serialized on success, SC-006).
type inputValidator struct {
	logger  *zap.Logger
	cache   sync.Map // inputSchemaKey -> *inputSchemaEntry
	skipped atomic.Int64
}

func newInputValidator(logger *zap.Logger) *inputValidator {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &inputValidator{logger: logger}
}

// SkippedCount returns how many dispatches proceeded with validation skipped
// (FR-013b fail-open counter — uncompilable/unsupported schemas).
func (v *inputValidator) SkippedCount() int64 {
	return v.skipped.Load()
}

// validateArgs checks args against the tool's stored paramsJSON.
//
//   - ok=true,  skipped=false → args are valid (or there is no schema): dispatch.
//   - ok=true,  skipped=true  → fail-open (FR-013b): the schema cannot be
//     compiled; dispatch exactly as a schemaless proxy would, counted in logs.
//   - ok=false             → validation failed; verr carries the detail for
//     the self-healing error. The caller must NOT dispatch (FR-013).
func (v *inputValidator) validateArgs(toolID, toolHash, paramsJSON string, args map[string]interface{}) (ok bool, verr error, skipped bool) {
	// No stored schema → nothing to validate; not a "skip" (nothing was skipped).
	if strings.TrimSpace(paramsJSON) == "" {
		return true, nil, false
	}

	entry := v.getOrCompile(toolID, toolHash, paramsJSON)
	if entry.sentinel {
		v.skipped.Add(1)
		v.logger.Debug("pre-dispatch validation skipped: uncompilable input schema (fail-open)",
			zap.String("tool", toolID),
			zap.Int64("validation_skipped", v.skipped.Load()))
		return true, nil, true
	}

	// Nil args validate as an empty object — exactly what a schemaless proxy
	// would have dispatched.
	if args == nil {
		args = map[string]interface{}{}
	}

	// Round-trip through jsonschema.UnmarshalJSON so numbers become
	// json.Number (required for draft 2020-12 numeric comparisons).
	jsonBytes, err := json.Marshal(args)
	if err != nil {
		// Cannot marshal our own map: fail open, never block on our failure.
		v.skipped.Add(1)
		v.logger.Warn("pre-dispatch validation skipped: failed to marshal args",
			zap.String("tool", toolID), zap.Error(err))
		return true, nil, true
	}
	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(jsonBytes))
	if err != nil {
		v.skipped.Add(1)
		v.logger.Warn("pre-dispatch validation skipped: failed to decode args instance",
			zap.String("tool", toolID), zap.Error(err))
		return true, nil, true
	}

	if err := entry.compiled.Validate(instance); err != nil {
		return false, err, false
	}
	return true, nil, false
}

// getOrCompile memoizes schema compilation per (tool hash, schema bytes).
func (v *inputValidator) getOrCompile(toolID, toolHash, paramsJSON string) *inputSchemaEntry {
	key := inputSchemaKey{toolHash: toolHash, schemaHash: hashInputSchema(paramsJSON)}
	if val, ok := v.cache.Load(key); ok {
		return val.(*inputSchemaEntry)
	}

	entry := compileInputSchema(paramsJSON)
	if entry.sentinel {
		v.logger.Warn("uncompilable input schema; pre-dispatch validation disabled for this tool (FR-013b fail-open)",
			zap.String("tool", toolID))
	}
	actual, _ := v.cache.LoadOrStore(key, entry)
	return actual.(*inputSchemaEntry)
}

// compileInputSchema compiles paramsJSON, returning a sentinel entry on any
// failure (unparseable JSON, invalid/unsupported schema constructs).
func compileInputSchema(paramsJSON string) *inputSchemaEntry {
	doc, err := jsonschema.UnmarshalJSON(strings.NewReader(paramsJSON))
	if err != nil {
		return &inputSchemaEntry{sentinel: true}
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource(inputSchemaURI, doc); err != nil {
		return &inputSchemaEntry{sentinel: true}
	}
	sch, err := c.Compile(inputSchemaURI)
	if err != nil {
		return &inputSchemaEntry{sentinel: true}
	}
	return &inputSchemaEntry{compiled: sch}
}

// hashInputSchema returns an FNV-64a hash of the schema bytes (cache-key guard).
func hashInputSchema(paramsJSON string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(paramsJSON))
	return h.Sum64()
}

// --- Self-healing error rendering (shared by Path A and Path B) ------------

// invalidParamsErrorResult renders the contract error body: full stored
// schema + deterministic one-line hint, via mcp.NewToolResultError (same
// envelope as the existing detailed errors). Mode-independent by construction
// — it never consults tool_response_mode (US3 scenario 3). Deterministic:
// json.Marshal sorts map keys and the detail line is stable for a given
// failure.
func invalidParamsErrorResult(toolID, paramsJSON, detail string) *mcp.CallToolResult {
	body := map[string]interface{}{
		"error":      fmt.Sprintf("invalid arguments for %s: %s", toolID, detail),
		"error_type": "invalid_params",
		"tool":       toolID,
		"hint": fmt.Sprintf(
			"Fix arguments to match input_schema, then retry. For the full definition call describe_tool({tool_ids:[%q]}).",
			toolID),
	}
	// input_schema is the tool's FULL stored ParamsJSON object — always full,
	// even in compact mode: that is what caps lossiness at one retry.
	var schema interface{}
	if err := json.Unmarshal([]byte(paramsJSON), &schema); err == nil {
		body["input_schema"] = schema
	}
	jsonResponse, _ := json.Marshal(body)
	return mcp.NewToolResultError(string(jsonResponse))
}

// oneLineValidationDetail flattens a santhosh v6 multi-line validation error
// ("jsonschema validation failed with '<uri>'\n- at '<loc>': <msg>…") into a
// deterministic single line for the contract's "error" field.
func oneLineValidationDetail(err error) string {
	lines := strings.Split(strings.TrimSpace(err.Error()), "\n")
	details := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "- "))
		if line == "" || strings.HasPrefix(line, "jsonschema validation failed") {
			continue
		}
		details = append(details, line)
	}
	detail := strings.Join(details, "; ")
	if detail == "" {
		detail = "arguments do not match the input schema"
	}
	const maxDetail = 500
	if len(detail) > maxDetail {
		detail = detail[:maxDetail-3] + "..."
	}
	return detail
}

// jsonRPCInvalidParamsCode is the JSON-RPC 2.0 "Invalid params" error code.
const jsonRPCInvalidParamsCode = -32602

// invalidParamsMessageRe is the Path B best-effort classifier for untyped
// upstream errors. Deliberately conservative: it must never reclassify
// transport/auth/timeout failures (FR-013 scenario 2), so it matches only
// unambiguous argument-validation phrasing.
var invalidParamsMessageRe = regexp.MustCompile(
	`(?i)\binvalid (params|parameters|arguments?)\b|\bmissing required (property|parameter|argument|field)\b`)

// httpStatusMessageRe guards the best-effort branch: strings that carry an
// HTTP status shape stay on the existing HTTP-aware error path.
var httpStatusMessageRe = regexp.MustCompile(`status code \d|HTTP \d`)
