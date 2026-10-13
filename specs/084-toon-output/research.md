# Phase 0 Research: Adaptive TOON Output

All decisions below are grounded in the actual code paths on branch `084-toon-output`. File:line
references are to the worktree as read during planning.

---

## D-SEAM: Where the encoder sits in the call_tool render pipeline

**Decision**: Insert the encoder in `handleCallToolVariant` (`internal/server/mcp.go`) **between
`applyOutputSanitisation` (line ~2094) and `forwardContentResult` (line ~2102)** — specifically
**after the Spec 069 raw-byte measurement at mcp.go:2099-2100**, so that measurement keeps seeing the
pre-encoding bytes. The encoder walks the sanitised `*mcp.CallToolResult`'s `TextContent` blocks
(mirroring the walk in `forwardContentResult`, `internal/server/content_forward.go:95`), and per
block either replaces `tc.Text` with `marker + "\n" + TOONbody` or leaves it untouched. It returns
(a) the mutated result, (b) the **pre-encoding full text rendering across all blocks** for the
detection scan, and (c) a `[]toonenc.Decision` (one per text block).

**Why this exact position** — the actual ordered pipeline in the current code is (verified
mcp.go:2094-2148; note sanitisation runs FIRST, then the byte measurement):

1. `applyOutputSanitisation(ctx, …, result)` (mcp.go:2094) — Spec 054 Track B: redact/strip mutate
   text blocks in place, block returns early. **Runs first, on the raw upstream result.**
2. `activityResponseBytes := rawByteSize(result)` (mcp.go:2099) +
   `activityRequestBytes := rawByteSize(activityArgs)` (mcp.go:2100) — Spec 069 A1: raw
   pre-truncation, **pre-encoding** size measurement of the (already-sanitised) result.
3. **← ENCODER SEAM (new), inserted between line 2100 and line 2102 →**
4. `forwardContentResult(result, p.truncator, …)` (mcp.go:2102) — truncates `TextContent` only,
   returns `forwarded`, `response` text, `wasTruncated`.
5. `applyOutputValidation(ctx, …, forwarded)` (mcp.go:2107) — structured-content schema validation.
6. `spotlightForwarded(…, forwarded)` + `response = forwardedText(forwarded, response)`
   (mcp.go:2114-2115).
7. `emitActivityToolCallCompleted(…, response, …, activityResponseBytes)` (mcp.go:2148) — the
   `response` string becomes `ActivityRecord.Response`; the detection scan input becomes
   `detection_text` (D-DETECT), not `response`.

**Do not move the byte measurement.** Placing the seam *after* step 2 is deliberate:
`activityResponseBytes` (the Spec 069 `response_bytes` telemetry) must stay the pre-encoding raw size
so it remains comparable across the feature being on/off, and so FR-010's `bytes_before` (recorded
separately in the `toon_output` metadata) is the authoritative pre-encoding figure. Encoding before
the measurement would silently redefine an existing metric.

Placing the encoder at step 3 satisfies three FRs simultaneously:

- **FR-007(a)** — sanitisation (step 2) sees the raw result; the encoder's input is already
  sanitised. No reordering of the security-critical pass.
- **FR-008 / US3-AC2** — "encode first, then truncate": the truncator (step 4) now operates on the
  already-encoded text, so truncation applies to the final rendered payload and the marker/hint
  (prepended, at the head of the block) are never truncated away.
- **FR-010b** — `applyOutputValidation` (step 5) receives `forwarded`, whose `StructuredContent` is
  copied verbatim from the upstream result (`content_forward.go:133`). The encoder only rewrites
  `TextContent`, never `StructuredContent`, so structured-content validation still evaluates the
  original structured result.

**Alternatives rejected**:
- *Encode inside `forwardContentResult`*: rejected — it would couple encoding to the cache/truncate
  helper (shared by the legacy JSON-wrap fallback and read_cache) and make the "encode before
  truncate" ordering implicit and fragile. A dedicated seam keeps the ordering explicit and testable.
- *Encode after `forwardContentResult` (post-truncation)*: rejected — violates FR-008 (would encode
  an already-truncated, possibly-invalid-JSON payload) and FR-004 (can't compare against the exact
  passthrough emission once it's been truncated).
- *Encode before `applyOutputSanitisation`*: rejected — violates FR-007(a); the encoder would hide
  secrets from the redact/block pass.

---

## D-DETECT: FR-007(b) — the sensitive-data scan must see pre-encoding text

**Decision**: Thread a **`detection_text`** string from the encoder seam through
`emitActivityToolCallCompleted` → the `ToolCallCompleted` event payload → `runAsyncDetection`. When
present, `runAsyncDetection` (`internal/runtime/activity_service.go:551`) scans `detection_text`
instead of the agent-facing `response`; when absent (feature off, or non-`call_tool_*` paths) it
falls back to today's `response`, so behavior is byte-identical when `off`.

**Why a separate channel**: Today the async detector scans the same `response` string that becomes
`ActivityRecord.Response` (activity_service.go:551, called from `handleToolCall` after the record is
saved). If the encoder rewrites `response` to TOON, the detector would scan TOON — which FR-007(b)
forbids ("MUST receive the pre-encoding text rendering of the block, not the TOON encoding"). The
two roles must be decoupled:

- `ActivityRecord.Response` = the agent-facing final text (post-encode, post-truncate) — preserves
  today's "what we sent the agent" semantics for the activity UI.
- detection scan input = the text the feature-off path would scan for this input, computed from the
  **pre-encoding** blocks (before the TOON rewrite).

The contract is **finding parity, not input-byte parity** (round-3): FR-007b requires the detector to
produce the same *findings* on vs off for identical inputs — it does NOT require `detection_text` to be
byte-identical to the off-path `response`, which is unachievable (the off `response` is refreshed after
`spotlightForwarded`, and `Truncator.Truncate` bakes a timestamped cache key into its banner). The seam
therefore builds `detection_text` as a **best-effort reconstruction** of the detection-relevant content
(data-model §7): (1) all-blocks rendering with the **same rules as `forwardContentResult`** (finding 6:
`TextContent` verbatim + `[image:…]`/`[audio:…]`/unknown placeholders, `content_forward.go:112-126`) —
so a secret beside a non-text block is scanned; (2) truncated with the **same `p.truncator` budget /
`toolName` / `args`** (issue 5) — so an over-limit secret is dropped exactly as off would drop it; (3)
spotlight-wrapped via `security.SpotlightUntrusted` driven by a new **`contentTrust`** seam parameter —
so untrusted framing matches. The only residual byte difference is the truncation banner's timestamped
cache key, which carries **no upstream data** and so can neither add nor suppress a finding — hence the
*finding set* is identical though the strings are not. **Off ⇒ empty (issue 2)**: when the mode is
`off` the seam emits an empty `detection_text`; `getStringPayload(evt.Payload, "detection_text")`
returns `""` → `runAsyncDetection` falls back to today's `response`, so the disabled path is unchanged.
The plumbing is a single optional payload field; every existing (non-`call_tool_*`) call site keeps its
behavior.

**Rationale for keeping sanitisation vs detection separate**: FR-007 deliberately splits the two
security stages. (a) *sanitisation* (`applyOutputSanitisation`, Spec 054 — redact/block/strip) is the
enforcing pass and already runs pre-encode by position (D-SEAM). (b) *detection*
(`runAsyncDetection`, Spec 026 — observational scan that populates activity metadata) is async and
must be fed the pre-encode text explicitly. Both then produce identical findings with the feature on
or off, for identical inputs (SC-004).

---

## D-MARKER: Exact marker + decode-hint format (FR-005)

**Decision**: A single deterministic **strict-ASCII** line, prepended to the TOON body with one `\n`:

```
[mcpproxy:toon/v1] TOON-encoded JSON (toon-format.org); decode to JSON before reuse - tool arguments must still be sent as JSON.
```

Constant `toonenc.Marker` holds the exact bytes. The emitted block is `Marker + "\n" + toonBody`.
Passthrough blocks carry no marker (FR-005, US1-AC2 byte-identity). See
[contracts/marker-format.md](./contracts/marker-format.md).

**Why this shape**:
- **Deterministic + one line** (FR-005, FR-011): a fixed constant — no timestamps, no counts, no
  interpolation. Byte-identical every call.
- **Strict ASCII** (finding 7): the separator is ` - ` (space-hyphen-space), NOT an em dash. This
  avoids tokenizer/encoding ambiguity and keeps the marker's own byte cost stable and counted in the
  size comparison (FR-003c). A test asserts `len(Marker) == utf8.RuneCountInString(Marker)`.
- **Mode-agnostic wording** (finding 1): says "TOON-encoded JSON", NOT "tabular JSON" — `always` mode
  (FR-009, normative) encodes *any* JSON value, so a "tabular" claim would be false there. One marker
  serves both `adaptive` (always tabular) and `always` (any JSON).
- **Self-identifying + versioned** (`/v1`): agents and the profiler can detect the encoding and its
  version; a future classifier v2 that changes the body can bump the marker.
- **Decode hint carries the two facts agents need**: (1) it's TOON → decode before reuse; (2) tool
  **arguments must stay JSON** — the spec's explicit edge case ("Agents that echo results back into
  tool args"), since input parsing is out of scope.
- The wording is also surfaced in the `call_tool_*` tool descriptions (`buildCallToolVariantTool`,
  mcp.go:615) so agents learn the contract in-session (spec Assumptions).

**Alternatives rejected**: Unicode bracket sentinels (`⟦…⟧`) or an em dash separator — rejected for
tokenizer/portability risk (finding 7). A structured JSON header line — rejected as heavier than a
one-line marker and defeating the savings on small tables. A per-mode marker variant — rejected;
"TOON-encoded JSON" is already truthful in both modes, so one constant suffices.

---

## D-CONFIG: Config field shape + precedence + validation (FR-001)

**Decision**:
- **Top-level** (`internal/config/config.go`, near `ToolResponseLimit` at line 138):
  - `ToonOutput string   `json:"toon_output,omitempty" mapstructure:"toon-output"``
  - `ToonMinSavingsPct int `json:"toon_min_savings_pct,omitempty" mapstructure:"toon-min-savings-pct"``
- **Per-server** (`ServerConfig`, near the other per-server fields at line ~445):
  - `ToonOutput string `json:"toon_output,omitempty" mapstructure:"toon_output"``
  - A plain `string` (not `*string`): `""`/absent = **inherit global**; any of `off|adaptive|always`
    = **override**. This matches FR-001's exact wording ("whose non-empty value overrides the global")
    and is simpler than the `*bool`/`*Duration` tri-state used where the zero value is meaningful.
    For a string enum the empty string already unambiguously means "unset", and `off` is the explicit
    "force off" override — no pointer needed to disambiguate.
  - `toon_min_savings_pct` is **global-only** — FR-001 grants a per-server override only for
    `toon_output` ("a per-server ServerConfig field of the same name").
- **Precedence** (per-server > global > default): a **string-only** resolver
  `Config.ResolveToonOutput(sc *ServerConfig) string` mirroring `Config.ResolveInitTimeout` — returns
  the per-server value if non-empty, else the global, else `"off"`. It returns a raw `string`, NOT a
  `toonenc.Mode` (finding 4): `internal/config` is imported almost everywhere, so it must not take a
  dependency on `internal/toonenc`. The server/bench caller parses the string into a `toonenc.Mode`
  via `toonenc.ParseMode` at the boundary, once per tool call.
- **Defaults** (`DefaultConfig`, config.go:1358): `ToonOutput: "off"`, `ToonMinSavingsPct: 15`.
- **Validation** (`ValidateDetailed`, config.go:1578): `toon_output` ∈ {`off`,`adaptive`,`always`}
  (empty allowed at top level → treated as `off`); each per-server `toon_output` ∈ the same set OR
  empty; `toon_min_savings_pct` ∈ [1,90] (0/unset → default 15). Invalid values append a
  `ValidationError` with a clear `Field`/`Message` (e.g. `field: "toon_output"`, `"must be one of:
  off, adaptive, always"`), matching the existing `tools_limit` / interval validators.

**Hot-reload** (`internal/runtime/config_hotreload.go`): per-server changes are already caught by the
`reflect.DeepEqual(oldCfg.Servers, newCfg.Servers)` check (config_hotreload.go:86). Add two
top-level entries near the `ToolResponseLimit` check (line 95) so a lone `toon_output` /
`toon_min_savings_pct` edit is reported as a change rather than "no changes detected". No restart is
required: the encoder reads `p.config.ToonOutput` / `ResolveToonOutput` **fresh on every call** — the
same pattern `applyOutputSanitisation` uses when it reads `p.config.OutputSanitisation`
(output_sanitisation.go:75) — and `p.config` is swapped atomically on reload. The change-detection
entries exist only to acknowledge/log the reload (FR-001, US2-AC3).

**Alternatives rejected**: `ToonOutput *string` per-server — rejected; adds pointer ceremony for no
disambiguation benefit given the string-enum domain. A single enum type instead of `string` —
deferred; a typed alias (`type ToonMode string`) lives in `internal/toonenc` and config stores the
raw string to keep `internal/config` free of a `toonenc` import (config is imported very widely).

---

## D-DECIDE: The adaptive decision (FR-003, FR-004, FR-006, FR-009)

**Decision**: `toonenc.EncodeBlock(text string, mode Mode, minSavingsPct int, retainedBudget int)
(out string, d Decision)` implements the whole per-block decision, deterministically. See
[contracts/encoder-decision.md](./contracts/encoder-decision.md) for the exact algorithm; the key
per-finding points:

1. **Parse** `text` as JSON with `json.Decoder` + `UseNumber()` (determinism — no float round-trip)
   and require EOF (reject trailing garbage). Non-JSON (plain text / base64 / binary) →
   `passthrough-not-tabular` in **every** mode; return `text` unchanged.
2. **Classify** tabular-uniform (see D-CLASSIFY). In `adaptive`, not-tabular → `passthrough-not-tabular`.
   In `always`, classification is computed for the record but does **NOT** gate encoding (finding 1 /
   FR-009: always mode encodes *any* JSON value, tabular or not).
3. **Order + Encode** (finding 5): first canonicalize with `canonicalToon(v)` — recursively rewrite
   every `map[string]interface{}` into a key-sorted `toon.NewObject`, leaving arrays/scalars/
   `json.Number` untouched — then `toon.MarshalString(ordered)`. This makes the output bytes a pure
   function of the parsed value **without** trusting toon-go's (unproven) map-key normalization; Go's
   randomized map iteration can never leak into the result. Encode error → `passthrough-error`, return
   `text` unchanged; the **caller** (server seam) logs + counts (finding 3 / FR-006 — the pure encoder
   does not log). Applies to both modes (always mode's arbitrary nested JSON needs the recursion too).
4. **Assemble** the emission `Marker + "\n" + toonBody`; measure `len(emission)` vs `len(text)`
   (the *exact passthrough emission* per FR-003c — the verbatim block, not a recompacted form).
5. **Too-small-budget guard** (finding 2, FR-008/FR-009, precedence in every mode): if
   `retainedBudget > 0` and `retainedBudget < len(Marker)+1+MinToonRowBytes`, passthrough with
   `Outcome: passthrough-below-threshold`. `retainedBudget` is the truncator's **actual** kept-content
   budget (`limit - min(200, limit/2)` for the `simpleTruncate` path that encoded TOON always hits —
   it is not valid JSON, so `analyzeJSONStructure` fails, truncator.go:234), supplied by the server
   via a new `Truncator.SimpleTruncateBudget()` helper. Basing the guard on the raw `ToolResponseLimit`
   (the earlier draft) was wrong by ~200 bytes — the truncator keeps `limit - messageSpace`, not `limit`.
6. **Mode gate**:
   - `always`: emit the TOON (subject to steps 1, 3, and the step-5 guard) regardless of size —
     `Outcome: encoded`. Non-JSON → passthrough, no marker (edge case).
   - `adaptive`: emit only if tabular AND `len(emission) <= len(text) * (100 - minSavingsPct) / 100`
     (integer math, floors conservatively). Meets threshold → `Outcome: encoded`; else
     `Outcome: passthrough-below-threshold`, return `text` unchanged.
   - `off`: never reached (the seam skips the encoder entirely when the resolved mode is `off`).

`Decision` carries `{BlockIndex, Mode, Classification, PassthroughEmissionBytes, EncodedEmissionBytes,
ThresholdPct, Outcome}` for the activity record and the profiler.

**FR-004 never-larger**: holds by construction — in `adaptive` a block is only rewritten when the
encoded emission is *strictly smaller by the threshold*; in `always` FR-004 is not asserted (it's the
documented benchmark mode, spec US2-AC4). Property test: for every fixture, in `adaptive`,
`len(emitted) <= len(passthrough)`.

---

## D-CLASSIFY: The tabular-uniform predicate (FR-003b, FR-011)

**Decision**: `toonenc.Classify(v interface{}) Classification` over a `json.Number`-decoded value,
implementing exactly the spec v1 rules:

- Unwrap **envelope**: if `v` is an object with **exactly one key** whose value is an array, classify
  that inner array (records the `envelope` sub-classification).
- Require an **array of ≥ 4 elements**, every element a JSON **object** (not scalar/array/null).
  Empty arrays and arrays of non-objects → **not tabular**.
- Every field value in every row must be a **scalar or null** — any nested object/array anywhere in a
  row disqualifies the whole array (v1 is flat-only).
- **Union key set**: a key is included iff it is present in **≥ 90%** of rows (ragged rows tolerated
  up to 10%); key order is irrelevant (classification compares key *sets*, computed from a sorted
  key list so the predicate is deterministic).
- Classification result ∈ {`tabular` (with row/col counts + envelope flag), `not-tabular` (with a
  reason enum: `not-json`, `not-array`, `too-few-rows`, `non-object-elements`, `nested-values`,
  `too-ragged`)}.

**Determinism (FR-011)**: keys sorted before comparison; counts derived by a single ordered pass; no
map iteration order leaks into the result. The classifier never encodes — it's a pure predicate — so
the "uniform enough" 90% rule and the FR-004 size comparison are independent backstops (spec edge
case: "arrays of *almost*-uniform objects").

**Rationale for flat-only v1**: the spec (Assumptions, FR-003b) deliberately scopes v1 to flat scalar
rows; nested values are future work gated by profiler measurement. The size comparison is the safety
net regardless of classifier quality, so a conservative classifier only ever *loses* savings, never
regresses correctness.

---

## D-PROFILER: FR-012 profiler results arm

**Decision**: Add `bench/arms/toon_results.go` implementing the spec-083 bench `Arm` interface for
the **results** corpus (alongside the existing `bench/arms/toon.go` *listing* arm). It imports
`internal/toonenc` and calls **the exact production `toonenc.EncodeBlock`** on each result fixture,
reporting per-class savings (encoded subset, all-tabular subset, non-tabular passthrough) and the
decision counts (encoded / passthrough-not-tabular / passthrough-below-threshold / passthrough-error)
— the SC-001 three-metric split.

**Grounding**: the spec-083 profiler (`bench/`, arms in `bench/arms/`, `Arm` interface in
`bench/arms/arm.go`, results-cost machinery in `bench/respcost.go`) arrives via **PR #851** and is a
prerequisite (spec Assumptions; FR-012/SC-001 run "before this feature's measurement gate"). The
importable `internal/toonenc` package (D-SEAM structure decision) is what makes "exercises this exact
production code path" literally true — the arm and the server share one encoder. The result-fixtures
corpus is owned by spec 083; this arm consumes it and adds the adaptive decision layer on top of the
raw-TOON measurement 083 already produces.

**Coordination note**: `github.com/toon-format/toon-go` is a `go.mod` dependency on branch `083`
already, but **not yet on `084`**. If 083/#851 merges first, the require line is already present;
otherwise this feature's first task adds it (`go get github.com/toon-format/toon-go@<pinned>`, same
pinned pseudo-version 083 uses: `v0.0.0-20251202084852-7ca0e27c4e8c`). Either way the plan owns
ensuring the dependency and the profiler arm land together.

---

## D-ACTIVITY: Recording the decision (FR-010)

**Decision**: Merge the per-block decisions into the existing `tool_call` `ActivityRecord.Metadata`
under a `toon_output` key, keyed per block index for multi-block responses:

```json
"toon_output": {
  "mode": "adaptive",
  "blocks": [
    {"index": 0, "outcome": "encoded", "classification": "tabular",
     "bytes_before": 8123, "bytes_after": 5140, "threshold_pct": 15}
  ]
}
```

**Grounding**: `ActivityRecord.Metadata` is a free-form `map[string]interface{}`
(`internal/storage/activity_models.go:87`) already carrying `tool_variant`, `intent`,
`content_trust`, `profile` (built in `activity_service.go:470-490`). The decision map is threaded on
the `ToolCallCompleted` event payload (`event_bus.go:409` adds a `toon_output` payload key when
non-nil) and merged into `metadata` in `handleToolCall` alongside the existing keys — no new struct
field, no BBolt migration. Multi-block responses record one entry per text block; `bytes_before` /
`bytes_after` are set only on `encoded` outcomes (FR-010).

**Alternatives rejected**: a first-class struct field on `ActivityRecord` — rejected; the existing
`Metadata` map is exactly the right home (mirrors how Spec 026 detection results are attached via
`UpdateActivityMetadata`, activity_service.go:954), and avoids a storage-model change + migration.
