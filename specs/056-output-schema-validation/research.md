# Phase 0 Research: Output-Schema Validation

## Decision 1: JSON Schema validation library

**Decision**: Use `github.com/santhosh-tekuri/jsonschema/v6` (v6.0.2), promoting it from indirect to a direct dependency.

**Rationale**:
- Already present in the module graph (pulled transitively), so no new third-party supply-chain surface — only a `go.mod` `require` promotion.
- Most complete and spec-compliant Go JSON Schema implementation: supports draft 2020-12 (the draft MCP `outputSchema` uses), `$ref`, formats, and rich error paths (instance location + keyword location), which we surface in the FR-A5 violation description.
- `Compile`/`Validate` split maps cleanly onto our per-tool compiled-schema cache: compile once, validate per call.

**Alternatives considered**:
- `github.com/google/jsonschema-go` v0.4.2 (also indirect): newer, but less battle-tested error reporting and fewer draft-coverage guarantees than santhosh-tekuri.
- `github.com/xeipuuv/gojsonschema`: unmaintained, draft-07 only — rejected.
- Hand-rolled structural checks: rejected — reinvents a validator, fails FR-A2 generality.

## Decision 2: Where to capture the output schema (FR-A1)

**Decision**: At tool discovery in `internal/upstream/core/client.go` (~line 284, where `mcp.Tool.InputSchema` is already marshalled into `ToolMetadata.ParamsJSON`), also serialize the tool's output schema into a new `ToolMetadata.OutputSchemaJSON string`. Prefer `tool.RawOutputSchema` (raw `json.RawMessage`, preserves the upstream's exact schema bytes); fall back to marshalling `tool.OutputSchema` when raw is absent.

**Rationale**: Mirrors the existing input-schema persistence path exactly, so the schema is available at call time without a second upstream round-trip and without a new storage bucket. Raw bytes avoid lossy re-encoding and keep a stable hash for the cache key.

**Alternatives considered**:
- Re-fetch the tool definition at call time: rejected — extra latency, racy with reconnects.
- New BBolt bucket for output schemas: rejected — `ToolMetadata` already travels with the tool; no migration needed.

## Decision 3: Hook point for validation (FR-A2, FR-A3)

**Decision**: Add an optional validator hook to `forwardContentResult` in `internal/server/content_forward.go` — the single proxied-response chokepoint, called from `handleCallToolVariant` at `mcp.go:1794` and `:2166`. Validation reads `ctr.StructuredContent`, runs guards then schema validation on a decoded copy, and returns a `Verdict`. On success the original `StructuredContent` is forwarded byte-identically (validate-a-copy, never strip-then-validate). The caller (`mcp.go`) translates the verdict into a block (strict) or a forward + `emitActivityPolicyDecision` (warn).

**Rationale**: One chokepoint = one place to enforce, consistent with how truncation already works there. Keeping the block/tag decision in `mcp.go` (which owns activity logging + error result construction) keeps `content_forward.go` and the validator pure.

**Alternatives considered**:
- Validate inside each `handleCallTool*` variant: rejected — duplicated logic across two call sites.
- Validate in the upstream client layer: rejected — the upstream layer must stay transport-only (Constitution 3-layer rule); policy belongs at the proxy boundary.

## Decision 4: Guards before validation (FR-A6)

**Decision**: Before compiling/validating, run two cheap guards on the structured payload: (a) marshalled byte size vs `max_bytes`; (b) recursive nesting depth vs `max_depth`. A breach short-circuits to a guard-violation verdict (no schema validation). Defaults: `max_bytes = 5 MiB`, `max_depth = 64` — generous enough never to trip legitimate tool output, tight enough to bound adversarial nesting/DoS.

**Rationale**: Schema validation cost grows with payload size/nesting; an adversarial deeply-nested blob could be expensive. Guarding first protects the proxy (Constitution I) and gives a clear, cheap failure mode.

**Alternatives considered**: Validate-then-measure — rejected (does the expensive work on exactly the inputs we want to reject early).

## Decision 5: Modes & defaults (FR-A4, FR-A8)

**Decision**: `output_validation.mode` ∈ {`off`, `warn`, `strict`}, default `warn`. `missing_structured_content` ∈ {`allow`, `block`}, default `allow`. In `warn`, violations forward the original payload and emit a `policy_decision` activity record (status e.g. `warning`). In `strict`, violations return an MCP error result and emit a `policy_decision` (status `blocked`). A declared-but-absent `structuredContent` is a no-op in `warn`; in `strict` it follows `missing_structured_content`.

**Rationale**: `warn` default means turning the feature on never breaks a working agent (the ContextForge #4042 lesson) — operators see audit signal first, then escalate to `strict`. `allow`-by-default for missing structured content matches the many tools that declare an output schema but still return only text.

**Alternatives considered**: default `strict` — rejected (breaks under-declaring tools on upgrade, violates backward-compat assumption).

## Decision 6: Schema-compile cache & uncompilable schemas (FR-A9)

**Decision**: `Validator` holds a `sync.Map` keyed by `server:tool` + FNV/SHA hash of the schema bytes → compiled `*jsonschema.Schema` (or a sentinel "uncompilable"). First call compiles; subsequent calls reuse. If `Compile` fails, store the sentinel, log one diagnostic warning per tool, and treat the tool as no-schema (no-op) thereafter — never block traffic on the proxy's inability to compile a schema.

**Rationale**: Read-mostly cache → `sync.Map` is the idiomatic, lock-light choice (Constitution II benchmark-justified exception). Hashing the schema bytes invalidates the cache automatically if a tool's schema changes (forward-compatible with Track D pinning, which is out of scope here).

**Alternatives considered**: `map` + `sync.RWMutex` — acceptable but `sync.Map` better fits the write-once-read-many access pattern.

## Decision 7: Activity record shape (FR-A5, FR-A11)

**Decision**: Reuse `emitActivityPolicyDecision(server, tool, sessionID, decision, message)` already used for blocked decisions. Use `decision="blocked"` in strict, `decision="warning"` in warn (add the status if not present), with `message` carrying the validator's violation string (keyword + instance location, truncated). No payload contents are logged beyond the violation description (avoid leaking response data).

**Rationale**: Zero new logging plumbing; failures appear in `mcpproxy activity list` filterable by status, satisfying SC-005.

**Alternatives considered**: A bespoke validation-event type — rejected (the activity log's `policy_decision` already models exactly this).
