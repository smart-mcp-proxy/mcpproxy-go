# Phase 1 Data Model: Adaptive TOON Output

No persistent schema changes. All entities below are in-memory value types in the new
`internal/toonenc` package, plus reuse of two existing config/storage structures. "Entity" here means
a Go type or a config/metadata shape, not a database table.

---

## 1. Mode (`internal/toonenc.Mode`)

The encoding mode, global or per-server-resolved.

| Value | Meaning |
|-------|---------|
| `off` | Feature disabled. Encoder is never invoked; responses byte-identical to pre-feature (FR-002). Default. |
| `adaptive` | Encode a block iff tabular-uniform AND encoded emission beats passthrough by `toon_min_savings_pct` (FR-003). |
| `always` | Encode every **JSON-parseable** text block regardless of tabular classification or size comparison (benchmark/debug; FR-009 is normative — any JSON value, not just tabular). Non-JSON text still passes through (no marker). Still honors the too-small-budget guard and FR-005–FR-008. |

- Go type: `type Mode string` with constants `ModeOff`, `ModeAdaptive`, `ModeAlways`.
- `ParseMode(s string) (Mode, bool)`: `""` → `ModeOff, true`; valid enum → `(mode, true)`; else
  `("", false)`. Used at the server/bench boundary to turn the config's raw string into a `Mode`.
- **Source of truth**: `Config.ToonOutput` (global) and `ServerConfig.ToonOutput` (per-server),
  resolved to a **raw string** by `Config.ResolveToonOutput(sc)` (per-server non-empty > global >
  `"off"`), then parsed to `Mode` by the caller via `ParseMode` (finding 4 — `internal/config` takes
  no `toonenc` import).
- **Lifecycle**: read fresh per tool call; hot-reloadable (no restart).

## 2. Classification (`internal/toonenc.Classification`)

The deterministic result of the tabular-uniform predicate (FR-003b).

```go
type Classification struct {
    Tabular   bool
    Envelope  bool          // true if unwrapped from a single-key object envelope
    Rows      int           // element count of the classified array
    Cols      int           // size of the ≥90%-present union key set
    Reason    NotTabularReason // set only when Tabular == false
}
```

- `NotTabularReason` enum: `ReasonNotJSON`, `ReasonNotArray`, `ReasonTooFewRows` (<4),
  `ReasonNonObjectElements`, `ReasonNestedValues`, `ReasonTooRagged` (union key set collapses / rows
  disagree beyond the 90% tolerance).
- **Rules** (v1, from FR-003b): array of ≥4 objects; every value scalar-or-null (no nested); union
  key = keys present in ≥90% of rows; single-key-object envelope qualifies; empty arrays and arrays
  of non-objects do not; key order irrelevant.
- **Invariants**: pure function of the parsed value; deterministic (keys sorted before set
  comparison); never mutates input; never encodes.

## 3. Decision (`internal/toonenc.Decision`)

Per-text-block outcome record — feeds both the activity log (FR-010) and the profiler (FR-012).

```go
type Decision struct {
    BlockIndex               int
    Mode                     Mode
    Classification           Classification
    PassthroughEmissionBytes int            // len(original block text) — FR-003c exact passthrough
    EncodedEmissionBytes     int            // len(marker + "\n" + toon body); 0 when passthrough
    ThresholdPct             int            // toon_min_savings_pct in effect
    Outcome                  Outcome
}
```

- `Outcome` enum (string): `OutcomeEncoded`, `OutcomePassthroughNotTabular`,
  `OutcomePassthroughBelowThreshold`, `OutcomePassthroughError`.
- **Invariants** (split by mode — `always` mode encodes any JSON, so a single Tabular-requiring
  invariant would be false there):
  - `Mode == ModeAdaptive` AND `Outcome == OutcomeEncoded` ⇒ `Classification.Tabular == true` AND
    `EncodedEmissionBytes <= PassthroughEmissionBytes * (100 - ThresholdPct) / 100` (which implies
    `EncodedEmissionBytes <= PassthroughEmissionBytes`, FR-004 never-larger).
  - `Mode == ModeAlways` AND `Outcome == OutcomeEncoded` ⇒ the block was JSON-parseable (tabular or
    not); no size bound is asserted (documented benchmark mode, FR-009).
  - `Outcome != OutcomeEncoded` ⇒ the emitted block bytes equal the original block bytes (FR-002/
    passthrough byte-identity), in every mode.

## 4. Marker (`internal/toonenc.Marker`)

The deterministic one-line encoding marker + decode hint (FR-005). See
[contracts/marker-format.md](./contracts/marker-format.md) for the exact bytes and the emission
assembly rule (`Marker + "\n" + toonBody`). Constant string; no fields; part of the agent-facing
response contract.

## 5. Config fields (reused: `internal/config`)

Top-level `Config` (config.go):

| Field | JSON | Type | Default | Validation |
|-------|------|------|---------|------------|
| `ToonOutput` | `toon_output` | `string` | `"off"` | ∈ {off, adaptive, always} or empty |
| `ToonMinSavingsPct` | `toon_min_savings_pct` | `int` | `15` | ∈ [1,90] (0/unset → 15) |

Per-server `ServerConfig` (config.go):

| Field | JSON | Type | Semantics |
|-------|------|------|-----------|
| `ToonOutput` | `toon_output` | `string` (omitempty) | `""`/absent = inherit global; `off\|adaptive\|always` = override (FR-001 precedence) |

Resolver (finding 4 — `internal/config` stays string-only, no `toonenc` import):
`Config.ResolveToonOutput(sc *ServerConfig) string` — returns the per-server value if non-empty, else
the global `Config.ToonOutput`, else `"off"`. It returns a **raw string**; the caller
(`internal/server` seam or `bench/arms`) parses it into a `toonenc.Mode` via `toonenc.ParseMode` at
the boundary. This keeps `internal/config` — a very widely-imported package — free of any dependency
on `internal/toonenc`, and keeps the mode enum owned by the encoder package that uses it.

## 6. Activity metadata shape (reused: `ActivityRecord.Metadata`)

Nested under key `toon_output` on the existing `tool_call` `ActivityRecord.Metadata`
(`internal/storage/activity_models.go:87`). No new struct field, no BBolt migration.

```json
"toon_output": {
  "mode": "adaptive",
  "blocks": [
    {"index": 0, "outcome": "encoded", "classification": "tabular",
     "bytes_before": 8123, "bytes_after": 5140, "threshold_pct": 15},
    {"index": 1, "outcome": "passthrough-not-tabular", "classification": "not-tabular"}
  ]
}
```

- One `blocks[]` entry per text block (per-block-index keying, FR-010).
- `bytes_before`/`bytes_after` present only when `outcome == "encoded"`.
- Written only when the resolved mode ≠ `off` (so `off` responses carry no `toon_output` metadata key
  → SC-002 byte/record parity for the disabled path).

## 7. Detection scan input (transient)

`detection_text string` — the pre-encoding text rendering captured at the encoder seam, threaded on
the `ToolCallCompleted` event payload and consumed by `runAsyncDetection` (activity_service.go:551)
as the scan input (FR-007b). Not persisted as a distinct field; it is the *input* to the async
detector whose *findings* land in metadata via the existing Spec 026 `UpdateActivityMetadata` path.

**Off ⇒ empty, zero behavior change (issue 2)**: when the resolved mode is `off`, the seam emits an
**empty** `detection_text`. An empty payload field makes `runAsyncDetection` fall back to today's
`response` string, so the disabled path is byte-for-byte unchanged (SC-002). The seam therefore
returns `("", nil)` when `off` — it does NOT synthesize a pre-encoding rendering it would then throw
away. `detection_text` is populated only when the mode is `adaptive`/`always` (i.e. only when
encoding could actually diverge the agent-facing response from the scan input).

**Invariant — FINDING parity, not input-byte parity (issue 3, what FR-007b actually requires)**: the
guarantee is *"sensitive-data detection produces the same **findings** with the feature on and off for
identical inputs"* (spec FR-007b / SC-004). It is explicitly **NOT** "detection_text is byte-for-byte
the feature-off `response`" — that is unachievable and was over-promised in the earlier draft:
- (a) the off path refreshes `response` **after `spotlightForwarded`** (mcp.go:2114-2115), so untrusted
  content is wrapped in source-identifying delimiters that differ unless the seam re-applies them; and
- (b) `Truncator.Truncate` mints a **timestamped cache key** into the truncation banner
  (`truncate/truncator.go:255-262`), so any synthetic truncation pass produces a different banner byte
  string than the real forward pass will.

**Definition — best-effort reconstruction (finding 6 + issues 3/5)**: the seam builds `detection_text`
to reproduce the detection-relevant content the off path scans, by:
1. rendering across **ALL content blocks** with the **same rules `forwardContentResult` uses**
   (`content_forward.go:91-136`): each `TextContent` verbatim, plus placeholders for non-text blocks —
   `[image:<mime> len=N]` (`:112`), `[audio:<mime> len=N]` (`:116`), best-effort JSON for unknown
   blocks (`:120`), joined with `\n`. NOT a text-only walk, so a secret beside an image block is scanned
   as today (finding 6);
2. truncating each block with the **same truncator budget** (`p.truncator`, same `toolName`/`args`), so
   an over-limit secret that the off path would truncate away is truncated away here too — parity holds
   past the limit (issue 5);
3. spotlight-wrapping untrusted text via the **same helper** `security.SpotlightUntrusted`, driven by
   the `contentTrust` value **passed into the seam** (new `contentTrust` param, issue 3(a)), so the
   detector sees the same delimiter framing the off path produces.

**Why this yields identical findings despite differing bytes**: the only bytes that differ between
`detection_text` and the real off-path `response` are the truncation banner's timestamped cache key —
and that banner contains **no upstream data** (it is mcpproxy-generated boilerplate: limit/record
counts + a cache key). By construction it can neither produce a new detection nor suppress a real one,
so the *finding set* (detector, rule, matched span content) is identical even though the raw strings
are not. This reconstruction runs only in `adaptive`/`always` mode and is pure text work (no cache
writes — caching stays owned by the real `forwardContentResult` on the encoded result).
