# Phase 1 Data Model: Output-Schema Validation

No new persistent storage (no BBolt bucket, no migration). Two existing in-memory/config entities are extended and one config entity is added.

## 1. `ToolMetadata` (extended) — `internal/config/config.go`

Existing struct gains one field, mirroring `ParamsJSON`.

```go
type ToolMetadata struct {
    // ... existing fields ...
    ParamsJSON       string `json:"params_json"`         // existing: input schema
    OutputSchemaJSON string `json:"output_schema_json,omitempty"` // NEW: declared output schema, raw bytes
}
```

- **Source**: populated at discovery in `internal/upstream/core/client.go` from `mcp.Tool.RawOutputSchema` (preferred) or marshalled `mcp.Tool.OutputSchema`.
- **Empty** ⇒ tool declares no output schema ⇒ validation is a no-op (FR-A7).
- Carried wherever `ToolMetadata` already travels (index/state); not separately persisted.

## 2. `OutputValidationConfig` (new) — `internal/config/config.go`

```go
type OutputValidationConfig struct {
    Mode                     string `json:"mode,omitempty" mapstructure:"mode"`                                               // "off" | "warn" | "strict"; default "warn"
    MaxBytes                 int    `json:"max_bytes,omitempty" mapstructure:"max-bytes"`                                     // structured payload byte cap; default 5<<20
    MaxDepth                 int    `json:"max_depth,omitempty" mapstructure:"max-depth"`                                     // nesting depth cap; default 64
    MissingStructuredContent string `json:"missing_structured_content,omitempty" mapstructure:"missing-structured-content"`   // "allow" | "block"; default "allow"
}
```

- Hung off the root config: `OutputValidation *OutputValidationConfig `json:"output_validation,omitempty"`` (pointer, like `SensitiveDataDetection`).
- `DefaultOutputValidationConfig()` returns `{Mode:"warn", MaxBytes:5<<20, MaxDepth:64, MissingStructuredContent:"allow"}`.
- Helpers: `IsEnabled()` (Mode != "off"), `IsStrict()` (Mode == "strict"), `EffectiveMaxBytes()`/`EffectiveMaxDepth()` (apply defaults when zero). A `nil` pointer behaves as defaults (warn). Hot-reloadable via the existing config watcher.

**Validation rules**: unknown `Mode`/`MissingStructuredContent` values fall back to the default with a logged warning; non-positive `MaxBytes`/`MaxDepth` fall back to defaults.

## 3. `Verdict` (new, transient) — `internal/outputvalidation`

Returned by the validator; never persisted.

```go
type Outcome int
const (
    OutcomePass    Outcome = iota // valid, or no schema, or nothing-to-validate (no-op)
    OutcomeViolate                // schema or guard violation
)

type Verdict struct {
    Outcome   Outcome
    Reason    string // human-readable violation (keyword + instance location), empty on pass
    GuardHit  string // "" | "max_bytes" | "max_depth" when a guard tripped
}
```

- `Outcome=OutcomePass` always means "forward unchanged" — the caller never mutates the payload.
- `Outcome=OutcomeViolate` ⇒ caller blocks (strict) or forwards + audits (warn) per mode.

## 4. Validation Failure Record (reused) — activity log

No new type. `emitActivityPolicyDecision(server, tool, sessionID, decision, reason)`:
- `decision = "blocked"` (strict) or `"warning"` (warn) — `"warning"` is a newly-used value of the existing field.
- `reason = Verdict.Reason` (truncated; no response payload contents).
- Surfaces in `mcpproxy activity list` / `activity show` (SC-005).

## Entity relationships

```
mcp.Tool (upstream, mcp-go)
   └─ RawOutputSchema ──capture──▶ ToolMetadata.OutputSchemaJSON
                                          │ (at call time, keyed by server:tool)
OutputValidationConfig ──mode/guards──▶ Validator ──Verdict──▶ forwardContentResult caller (mcp.go)
                                          │ compiled-schema sync.Map cache (server:tool + schema-hash)
CallToolResult.StructuredContent ──validate copy──▶ Verdict ──▶ block | tag+forward ──▶ emitActivityPolicyDecision
```
