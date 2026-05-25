# Contract: `internal/outputvalidation` package + config

Pure package, no server/storage imports. Unit-testable in isolation.

## Validator interface

```go
package outputvalidation

// Validator validates a tool's structured output against its declared JSON Schema,
// applying size/depth guards first. Safe for concurrent use.
type Validator struct {
    maxBytes int
    maxDepth int
    cache    sync.Map // key: cacheKey{toolKey, schemaHash} -> *compiled (or sentinel)
    logger   *zap.Logger
}

func New(maxBytes, maxDepth int, logger *zap.Logger) *Validator

// Validate checks `structured` (the decoded CallToolResult.StructuredContent, may be nil)
// against schemaJSON for the tool identified by toolKey ("server:tool").
//
//   - schemaJSON == ""        -> Verdict{OutcomePass}            (FR-A7 no-op)
//   - structured == nil       -> Verdict{OutcomePass}            (FR-A8 nothing-to-validate; caller applies
//                                                                  missing_structured_content posture in strict)
//   - schema uncompilable     -> Verdict{OutcomePass} + one-time warn   (FR-A9)
//   - byte/depth guard breach -> Verdict{OutcomeViolate, GuardHit:...}  (FR-A6, no schema validation done)
//   - schema violation        -> Verdict{OutcomeViolate, Reason:...}    (FR-A2)
//   - valid                   -> Verdict{OutcomePass}                   (FR-A3 caller forwards unchanged)
func (v *Validator) Validate(toolKey, schemaJSON string, structured any) Verdict
```

`Validate` MUST NOT mutate `structured`. The caller passes the already-decoded `StructuredContent`; the validator only reads it. Guards run on a one-time `json.Marshal` (byte size) + recursive walk (depth) of the value.

## Verdict

```go
type Outcome int
const ( OutcomePass Outcome = iota; OutcomeViolate )

type Verdict struct {
    Outcome  Outcome
    Reason   string // violation detail (keyword + instance path), "" on pass
    GuardHit string // "", "max_bytes", or "max_depth"
}

func (v Verdict) IsViolation() bool { return v.Outcome == OutcomeViolate }
```

## Caller contract (server side)

In `handleCallToolVariant` (`mcp.go`), after the upstream call and before/at `forwardContentResult`:

```
if cfg.OutputValidation.IsEnabled() && !result.IsError {        // FR-A10 skip errors
    schema := lookupOutputSchemaJSON(server, tool)              // from ToolMetadata.OutputSchemaJSON
    verdict := validator.Validate(server+":"+tool, schema, result.StructuredContent)
    switch {
    case !verdict.IsViolation():
        // forward unchanged (FR-A3)
    case verdict.OutcomeViolate && cfg.OutputValidation.IsStrict():
        emitActivityPolicyDecision(server, tool, sid, "blocked", verdict.Reason)   // FR-A5
        return mcp.NewToolResultError("output schema validation failed: " + verdict.Reason)
    default: // warn
        emitActivityPolicyDecision(server, tool, sid, "warning", verdict.Reason)   // FR-A5/FR-A11
        // forward original payload unchanged
    }
}
```

Missing `structuredContent` in strict mode + `missing_structured_content=block` ⇒ caller blocks even though `Validate` returned Pass (the posture decision lives with the caller, which knows the mode).

## Config contract (`mcp_config.json`)

```json
{
  "output_validation": {
    "mode": "warn",
    "max_bytes": 5242880,
    "max_depth": 64,
    "missing_structured_content": "allow"
  }
}
```

- Absent block ⇒ defaults (mode `warn`).
- `mode: "off"` ⇒ validator never invoked (FR-A4); zero added overhead (SC-006).
- Backward compatible: existing configs without the block keep working, gaining warn-mode audit only for tools that declare an output schema.
