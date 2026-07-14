# Contract: Encoder Decision

**Package**: `internal/toonenc`
**Primary function**: `EncodeBlock(text string, mode Mode, minSavingsPct int, retainedBudget int) (out string, d Decision)`

> `EncodeBlock` is a **pure function**: no logging, no metrics, no I/O. Observability for the
> `passthrough-error` outcome (FR-006 "logged and counted") is the **caller's** responsibility at the
> server seam — see the "Error observability" section and task T-ERR.

This is the single deterministic entry point that both `internal/server` (production) and
`bench/arms` (profiler, FR-012) call. It decides, for one tool-result **text block**, whether to
emit TOON or pass the block through unchanged, and returns a full `Decision` record.

## Inputs

| Param | Meaning |
|-------|---------|
| `text` | The exact text-block content the agent would receive with the feature off (the *passthrough emission*, FR-003c). Already sanitised (redact/strip applied upstream). |
| `mode` | Resolved mode for this call (`ModeAdaptive` or `ModeAlways`; the caller never invokes `EncodeBlock` when the resolved mode is `ModeOff`). |
| `minSavingsPct` | `toon_min_savings_pct` in effect (validated 1–90). Ignored in `ModeAlways`. |
| `retainedBudget` | The truncator's **actual retained-prefix budget** — the number of content bytes it keeps before appending its notice. `0` means "no truncation limit" (unlimited). The server computes it from the truncator, NOT from the raw `ToolResponseLimit` (see "Too-small-limit guard"). |

## Output

- `out`: the block to emit. Either `Marker + "\n" + toonBody` (encoded) or **exactly `text`**
  (passthrough — byte-identical, no marker).
- `d`: the `Decision` (see data-model.md §3).

## Algorithm (deterministic, FR-011)

```
1. PARSE text as JSON:
     dec := json.NewDecoder(strings.NewReader(text)); dec.UseNumber()
     if decode fails OR trailing non-whitespace remains:
         return text, Decision{Outcome: PassthroughNotTabular, Classification:{Reason: ReasonNotJSON}}
         // non-JSON (plain text/base64/binary) never qualifies, in ANY mode — edge case

2. CLASSIFY v := parsed value → Classify(v):    // always computed (informational in always mode)
     ModeAdaptive AND not Classification.Tabular:
         return text, Decision{Outcome: PassthroughNotTabular, Classification: c}
     ModeAlways:
         // NO tabular gate — always mode encodes ANY parseable JSON value (FR-009).
         // Classification is recorded for observability but does not gate encoding.

3. ORDER + ENCODE (determinism, FR-011):
     ordered := canonicalToon(v)   // recursively converts every map[string]interface{} into a
                                   // toon.Object whose fields are sorted by key; arrays/scalars/
                                   // json.Number pass through. Guarantees deterministic bytes
                                   // WITHOUT relying on toon-go's own map-key handling.
     toonBody, err := toon.MarshalString(ordered)
     if err != nil:
         return text, Decision{Outcome: PassthroughError, Classification: c}
         // caller logs + counts (see Error observability); NEVER a tool error

4. ASSEMBLE + MEASURE:
     emission := Marker + "\n" + toonBody
     passBytes := len(text); encBytes := len(emission)

5. TOO-SMALL-LIMIT GUARD (FR-008/FR-009 — precedence in EVERY mode):
     if retainedBudget > 0 && retainedBudget < len(Marker)+1+MinToonRowBytes:
         return text, Decision{Outcome: PassthroughBelowThreshold, ...}
         // the truncator would keep fewer bytes than marker + newline + one data row,
         // so marker+hint+notice+one row can't survive truncation → behave exactly as today

6. MODE GATE:
     ModeAlways:
         return emission, Decision{Outcome: Encoded, EncodedEmissionBytes: encBytes, ...}
     ModeAdaptive:
         if encBytes <= passBytes*(100-minSavingsPct)/100:   // integer floor, conservative
             return emission, Decision{Outcome: Encoded, EncodedEmissionBytes: encBytes, ...}
         return text, Decision{Outcome: PassthroughBelowThreshold, PassthroughEmissionBytes: passBytes, ...}
```

**Constant** `MinToonRowBytes` (package-level, documented): a small fixed estimate of one TOON data
row, e.g. 16. Fixed → determinism preserved.

**`retainedBudget` derivation (finding 2 — the guard must match the truncator, not the raw limit)**:
An encoded block is TOON text, which is **not valid JSON**, so `Truncator.Truncate` fails
`analyzeJSONStructure` and always falls into `simpleTruncate` (`internal/truncate/truncator.go:234`).
`simpleTruncate` keeps `limit - messageSpace` content bytes, where `messageSpace = min(200,
limit/2)`, then appends the notice `"\n\n... [truncated by mcpproxy, cache not available]"`. So the
budget of content bytes actually retained is `limit - min(200, limit/2)`, NOT `limit`. The server
computes this via a new exported helper `Truncator.SimpleTruncateBudget() int` (returns `0` when
`limit == 0` = unlimited) and passes it as `retainedBudget`. The guard then correctly rejects the
case where the marker + one row would be truncated away, which a `responseLimit`-based comparison
would miss by ~200 bytes.

### canonicalToon — determinism without trusting toon-go's map handling (finding 5)

`json.Unmarshal` into `interface{}` yields `map[string]interface{}`, whose Go iteration order is
randomized. Handing that map straight to `toon.MarshalString` makes the output's key order depend on
toon-go's (unproven, version-dependent) normalizer. `canonicalToon(v)` removes that dependency: it
recursively rewrites every object as a `toon.NewObject(fields…)` with fields **sorted by key**
(the same explicit-ordered-fields technique the spec-083 listing arm uses), leaving arrays, strings,
bools, null, and `json.Number` untouched. Output bytes are then a pure function of the parsed value.
This applies in **both** modes — `always` mode encodes arbitrary nested JSON, so it needs the
recursive ordering just as much as tabular `adaptive` does.

## Guarantees

- **G1 (byte-identity, FR-002/US1-AC2)**: every non-`Encoded` outcome returns `out == text` exactly.
- **G2 (never-larger, FR-004/SC-003)**: in `ModeAdaptive`, `Encoded` ⇒ `len(out) <= len(text)`.
  (`ModeAlways` is exempt by spec — documented benchmark mode.)
- **G3 (determinism, FR-011)**: identical `(text, mode, minSavingsPct, retainedBudget)` ⇒ identical
  `out` and identical `d` — guaranteed by `UseNumber()` parse and `canonicalToon`'s recursive
  sorted-key ordering (NOT by trusting toon-go's map normalization).
- **G4 (no data loss, FR-006)**: any failure ⇒ passthrough (`out == text`), never a tool-call error.
  Two DISTINCT failure classes, do not conflate them (issue 3):
  - **Parse failure / non-JSON input** (plain text, base64, binary, malformed JSON, trailing garbage)
    → `Outcome: PassthroughNotTabular`. This is the ordinary "not something we encode" path — it is
    **not** an error, and it is **not** logged or counted (non-JSON tool output is normal traffic).
  - **Encoder failure** — `toon.MarshalString`/`canonicalToon` returning an error on input that
    already parsed as JSON and (in adaptive) classified as encodable → `Outcome: PassthroughError`.
    This is the only outcome the caller logs + counts (FR-006). It should be rare (a toon-go bug or
    an exotic value), which is exactly why it warrants a warn + counter.
  `EncodeBlock` itself is pure; the seam performs the logging/counting for `PassthroughError` only.
- **G5 (surface isolation, FR-013/FR-014)**: `EncodeBlock` is invoked ONLY from the `call_tool_*`
  seam. `retrieve_tools`, code_execution, direct-mode, and listings never call it.

## Error observability (FR-006, finding 3)

`EncodeBlock` returns `Outcome: PassthroughError` **only** on a genuine encoder failure (input parsed
as JSON but `canonicalToon`/`toon.MarshalString` errored), and performs no logging or metrics itself
(keeping it pure and trivially testable). A parse failure / non-JSON block is `PassthroughNotTabular`,
NOT `PassthroughError`, and is never logged. The **server seam** (`encodeToonBlocks`, task T015) is
responsible for FR-006's "logged and counted":

- On any block whose `Decision.Outcome == OutcomePassthroughError`, emit a `zap.Warn` (server name,
  tool name, block index) and increment a counter (a `telemetry` counter reusing the existing
  registry, or a package-level `expvar`/atomic surfaced in logs — pick the mechanism already used
  for other non-fatal server-side fallbacks).
- Task **T-ERR** covers this with a test that forces a genuine ENCODER error — not a parse error —
  and asserts both the warn log (observed via a zap observer core) and the counter increment.

## Test obligations (TDD)

- Property: for a corpus of fixtures, G2 holds in `adaptive` for every fixture.
- Property: G3 — encode each fixture twice, assert identical `out` and `d`.
- **Randomized-key-order determinism (finding 5)**: take a nested-object fixture, generate N JSON
  serializations of it with **shuffled object-key order** in the source text, encode each, and assert
  **all N produce byte-identical `out`**. Proves `canonicalToon` — not source or map order — fixes the
  output. Run in both `adaptive` (tabular fixture) and `always` (nested fixture).
- Table: tabular fixture ≥ threshold → `Encoded`; tabular near-tie < threshold →
  `PassthroughBelowThreshold`; nested/scalar/short/non-object array → `PassthroughNotTabular`;
  non-JSON text → `PassthroughNotTabular` (`ReasonNotJSON`); malformed-after-classify (forced encode
  error) → `PassthroughError`.
- Envelope: single-key object wrapping a qualifying array → `Encoded` with `Classification.Envelope`.
- **Too-small-budget boundary (finding 2)**: with `retainedBudget` set to exactly
  `len(Marker)+1+MinToonRowBytes - 1` → `PassthroughBelowThreshold`; at exactly
  `len(Marker)+1+MinToonRowBytes` → guard passes (encode proceeds if the mode/threshold allow); at
  `retainedBudget == 0` (unlimited) → guard never fires. Assert in both `adaptive` and `always`.
- **`always` mode encodes any JSON (finding 1)**: nested object → `Encoded` (marker present, decode
  round-trips); scalar/`true`/number → `Encoded`; **non-JSON text → passthrough, no marker**; tabular
  below the adaptive threshold → `Encoded` (size gate bypassed).
