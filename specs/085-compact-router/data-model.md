# Phase 1 Data Model: Compact Router

This feature adds **no persisted storage** and **no new index fields** (FR-008). The entities
below are in-memory/serialization types plus one config field. Existing types they read from
are noted with real paths.

## 1. Compact Signature

Deterministic one-line rendering of a tool's parameters plus derived description/lossy state.
Produced by `internal/toolsig.Render`; consumed by the compact entry builder and (post-migration)
the bench `compact_sig` arm.

```go
// internal/toolsig/signature.go
type Signature struct {
    Sig   string // "(origin*:str, ttl:int=3600, account~:obj)"; "()" for no-params
    Desc  string // first-sentence verbatim prefix (may be "")
    Lossy bool   // true iff any param collapsed under "~" (FR-004)
}

func Render(paramsJSON, description string) (Signature, error)
func FirstSentence(description string) string
```

**Source fields** (read-only): `config.ToolMetadata.ParamsJSON` (the tool input schema JSON) and
`config.ToolMetadata.Description` (`internal/index/bleve.go` `ToolDocument`).

**Invariants**:
- **Never-elide-required** (FR-003 / SC-004): every name in the schema `required` array appears
  in `Sig` with its `*` marker, unconditionally — even when its type collapses to `~:obj`.
- **Deterministic** (FR-019): identical `(paramsJSON, description)` ⇒ identical `Signature`
  bytes. Required params in `required`-array order; optional params in sorted-name order; no Go
  map iteration order leaks into output.
- **Lossy is legible** (FR-004): `Lossy == true` ⟺ `Sig` contains at least one `~`. This is a
  strict biconditional and the reason the malformed case below renders `(~)`, not `()`.
- **Malformed schema** is fail-soft for rendering: unparseable `ParamsJSON` renders **`(~)`** (bare
  lossy marker, no params) with `Lossy=true` — **not** `()` (which would break the Lossy⟺`~`
  invariant above) — rather than erroring the whole response. Mirrors the existing tolerant
  `inputSchema` fallback at mcp.go:1433. (Distinct from validation fail-open, R3; see
  contracts/signature-grammar.md E11.)

See [contracts/signature-grammar.md](contracts/signature-grammar.md) for the full grammar and
worked examples.

## 2. Signature Cache

Process-local memoization keyed by the Spec-032 tool hash (FR-008).

```go
// internal/toolsig/cache.go
type Cache struct {
    mu sync.RWMutex
    m  map[string]Signature // key: ToolMetadata.Hash
}

func (c *Cache) Get(hash, paramsJSON, description string) Signature // compile-on-miss, memoize
func (c *Cache) Warm(hash, paramsJSON, description string)          // called from indexing path
```

**Key**: `ToolMetadata.Hash` (`hash.ComputeToolHashWithOutputSchema`, covers desc + schema, so a
changed description or schema yields a new key — no stale first-sentence).
**Ownership** (one instance): created by `Runtime` and passed into `NewMCPProxyServer` (stored
`p.sigCache`). The **same** instance is warmed by the indexing path and read by the request path —
never two caches (see research.md R9; a compile-count test enforces this).
**Lifecycle**: warmed in `runtime.applyDifferentialToolUpdate` after `BatchIndexTools`
(added + modified tools) and the full-reindex branch. Never persisted; re-warms on restart via
normal reindex. Bounded by live tool count.
**Test hook**: exposes a compile counter so a test can assert post-index `retrieve_tools` adds
zero compilations (FR-008 "not per request").

## 3. Response Mode (config)

```go
// internal/config/config.go — beside RoutingMode (~290)
ToolResponseMode string `json:"tool_response_mode,omitempty" mapstructure:"tool-response-mode"`
```

- Values: `""` (⇒ `full`) | `"full"` | `"compact"`. Validated beside the `routing_mode` block
  (config.go ~1650); invalid non-empty value ⇒ `ValidationError{Field: "tool_response_mode"}`.
- **Orthogonal** to `RoutingMode` (which selects the *tool surface*; this selects *serialization
  within retrieve_tools mode*).
- Env: `MCPPROXY_TOOL_RESPONSE_MODE` (explicit alias, loader.go); flag `--tool-response-mode`.
- Hot-reload requires two wiring fixes (research.md R6): (1) a `tool_response_mode` clause in
  `DetectConfigChanges` (`internal/runtime/config_hotreload.go`) so an apply of only this field is
  not swallowed as "no changes"; (2) the effective mode is read from **`p.currentConfig()`** (live
  snapshot, `profile_resolver.go:38`), **not** the construction-time `p.config` the retrieve path
  uses today (mcp.go:1236).
- **Phase 1 default is `full`** (FR-016); the flip to `compact` is a separate one-line change.

**Per-call override** (`detail`, FR-005): optional `retrieve_tools` parameter, enum
`compact|full`, **no default** — when unset the configured `ToolResponseMode` applies; when set
it overrides for that call only. Effective-mode resolution:

```
mode = p.currentConfig().ToolResponseMode        // live snapshot, not p.config
effectiveMode = detailParam if present else (mode if non-empty else "full")
```
(Implemented as `p.effectiveToolResponseMode(detail)`.)

## 4. Compact Entry vs Full Entry (serialization)

Produced by `buildToolEntry(result, mode, opts)` (new `internal/server/mcp_entry_builder.go`).

**Full entry** (mode=full — byte-identical to today, mcp.go:1451–1489):
```json
{ "name": "...", "description": "...", "inputSchema": {…}, "score": 0.9,
  "server": "...", "annotations": {…}, "call_with": "call_tool_write",
  "usage_count": 3, "last_used": "…" }        // stats fields only when include_stats
```

**Compact entry** (mode=compact):
```json
{ "id": "digitalocean:cdn_create", "score": 0.94, "lossy": false,
  "sig": "(origin*:str, ttl:int=3600, certificate_id:str)",
  "desc": "Create a CDN for a Spaces bucket" }
```
- `id` = `server:tool` (`result.Tool.ServerName` + `result.Tool.Name`, normalized to
  `server:tool` exactly as today's `name`). Ranking order preserved (SC-002).
- No `inputSchema`, no `description` (full), no `annotations` block — those move to
  `describe_tool` / self-healing errors.
- Compact response adds top-level `hint` (FR-009): a single deterministic line explaining the
  `~` marker and `describe_tool`, counted in measured size.

**Unchanged across both modes**: top-level `query`, `total`, `usage_instructions`,
`disabled`/`remediation` (Spec 049), `notice`, `session_risk` (Spec 035), `debug`,
`usage_summary`. Compact only trims the per-entry schema/description bulk.

## 5. describe_tool (built-in tool)

```
describe_tool(tool_ids: [str])   // 1..5 ids, "<server>:<tool>"
→ { "definitions": [ {full-mode entry}, … ],
    "errors": [ {"id": "...", "error": "not_found|invisible|…", "remediation": "..."}, … ] }
```
- Batch ≤5; >5 ⇒ single clear error naming the limit (no partial dump).
- Each id resolved through the search visibility pipeline (profile, agent scope, callability,
  quarantine/disabled) before returning a definition (FR-011). Invisible/unknown ⇒ per-id error,
  batch still succeeds (FR-010).
- Definition body reuses `buildToolEntry(..., full)` with the ranked-only key `score`
  **stripped**; equality with the full-mode `retrieve_tools` entry is over `{name, description,
  inputSchema, server, annotations, call_with}` (a definition has no `score` — it is not a ranked
  result; see contracts/describe_tool.md).
- Own tool definition ≤ ~150 tokens (FR-011), counted with tiktoken `cl100k_base` (the bench's
  pinned encoder).

See [contracts/describe_tool.md](contracts/describe_tool.md).

## 6. Self-healing Error

Argument-validation failure carrying the failing tool's full schema + hint. Rendered by an
extended `createDetailedErrorResponse` (mcp.go:4767) / the pre-dispatch validator.

```json
{ "error": "invalid arguments for github:create_issue: 'title' is required",
  "error_type": "invalid_params",
  "tool": "github:create_issue",
  "input_schema": { … full JSON schema … },
  "hint": "Fix arguments to match input_schema and retry; call describe_tool for the full definition." }
```
- Attached **only** for argument/validation errors (pre-dispatch typed error, or best-effort
  classification of an upstream InvalidParams). **Not** attached for transport/auth/timeout/
  upstream-crash (FR-013) — those keep today's `createDetailedErrorResponse` shape.
- Mode-independent (FR-013 / US3 scenario 3): identical in full and compact.
- Zero cost on the happy path (SC-006): only the error branch builds it.

See [contracts/invalid-params-error.md](contracts/invalid-params-error.md).

## 7. Flip Gates (profiler-emitted, FR-018)

Not a runtime type — metrics the spec-083 profiler emits into its report to authorize the
Phase-2 default flip:

| Metric | Gate | Source |
|---|---|---|
| Ranked-ID identity (compact vs full), per query | 100% | live arm, 47-query golden set |
| Discovery-response tokens p50/p95/max | median ≤1,000; max ≤1,500 | live arm |
| Lossy-signature rate across frozen corpus | <20% | `internal/toolsig` over 45-tool corpus |
| describe_tool calls per completed task | <0.3 (informational) | E2E suite |

## Entity relationships

```
ToolMetadata{ParamsJSON,Description,Hash}  ──Render──▶  Signature{Sig,Desc,Lossy}
      (index, unchanged)                       │  cached by Hash (Cache)
                                               ▼
retrieve_tools ──buildToolEntry(mode)──▶ full entry | compact entry (+hint)
describe_tool  ──buildToolEntry(full)──▶ full definition (post visibility check)
call_tool_*    ──validateArgs(ParamsJSON)──▶ invalid_params ──▶ self-healing error(+input_schema)
```
