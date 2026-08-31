# Phase 1 Data Model: token-bench (spec 103)

Entities are described by what they hold and what must be true of them. Field names are
indicative; the binding shapes are in [contracts/](./contracts/).

---

## ReplaySession

One unit of real recorded work, reassembled from exported activity JSONL.

| Field | Meaning |
|---|---|
| `work_session_id` | The spec-082 grouping key: one client, one project, across reconnects. This is the unit of work for US1. |
| `calls` | Ordered `ReplayCall` list. |
| `usability` | See below — computed once at load, never re-derived downstream. |
| `call_count`, `span` | Derived counts for reporting. |

**Usability flags** (each a reason a session may be partly or wholly unusable):

- `truncated` — one or more calls had their stored response truncated at capture.
- `bodies_missing` — the export was taken without bodies (the default), so content is absent.
- `sensitive` — one or more calls carry the sensitive-data flag.
- `unreplayable` — the tools referenced no longer resolve against the configured fleet.

**Validation rules**

- A session with `truncated` calls MUST NOT contribute a silent token total. It either
  contributes an annotated figure derived from pre-truncation byte sizes, or is excluded and
  counted in the exclusion report (FR-002, FR-003, SC-008).
- `sensitive` is a **best-effort** reducer. There is no persisted sensitivity field; the flag
  is derived at export from detection metadata that is added asynchronously after initial
  persistence, so its absence does not prove a record is clean. This limitation MUST be stated
  wherever the flag is relied upon (Principle IV).
- A session MUST NOT carry its call contents into any report structure. Only counts, sizes and
  derived measurements propagate (FR-006).

**State transitions**: `loaded → grouped → classified → {scored | excluded}`. Exclusion is
terminal and always counted.

---

## ReplayCall

One recorded tool call inside a session.

| Field | Meaning |
|---|---|
| `tool_name`, `server_name` | Identity, used to resolve the call against a mode's tool surface. |
| `request_bytes`, `response_bytes` | **Byte lengths, measured pre-truncation — NOT token counts.** They support an explicitly-estimated response cost when bodies are absent; they cannot produce a measured one, because tokenizing requires the text. |
| `response_truncated` | Whether the stored content was cut. |
| `status` | success / error / blocked / rejected. |
| `parent_id` | Links a code-execution sub-call to the call that issued it. |
| `has_sensitive_data` | Derived at export from detection metadata, which is added asynchronously after initial persistence. Best-effort (see above). |

**Validation rules**

- **Tool-surface cost does not depend on recorded content at all** — it is computed from the
  fleet's tool definitions under each mode plus the call sequence. It is fully `measured` with
  bodies off. This is the term the modes actually change, and it is US1's headline.
- **Response cost DOES require the text.** With bodies off it may only be reported as an
  explicit `estimated` figure derived from byte length, never as `measured`; with bodies on it
  becomes `measured`. The two MUST NOT be presented interchangeably.
- A call whose `parent_id` resolves MUST be attributed to its parent's session, so
  code-execution sub-calls are neither double-counted nor orphaned.

---

## ModeCell

One point in the matrix. **There are exactly 5 distinct behaviours plus a baseline**; see
[contracts/mode-matrix.md](./contracts/mode-matrix.md).

| Field | Meaning |
|---|---|
| `id` | Stable cell identifier used as a report key. |
| `endpoint` | Which mounted MCP endpoint serves this cell. Selects the routing-mode axis with no config change; the serialization axes still need config. |
| `discovery_serialization` | full / compact / not-applicable. |
| `direct_serialization` | full / deferred / not-applicable. |
| `capabilities` | Binary conditions applicable to this cell (batching, stored scripts, validate-before-dispatch). |
| `skipped`, `skip_reason`, `collapses_onto` | Populated for the 7 redundant combinations of the 3-axis product; each names the valid cell it collapses onto. |

**Validation rules**

- An ignored axis MUST be recorded as not-applicable, never as a default value — a default
  would imply a measurement that was never taken (FR-017).
- A skipped row MUST carry a reason code AND the cell it collapses onto, and MUST NOT be
  rendered as a zero. These combinations are configurable and redundant, NOT impossible.
- Cell identity MUST be stable across runs so results are comparable over time (FR-028).

---

## RunRecord

The raw outcome of one unit of work under one mode cell. This is the traceable unit behind
every headline figure (FR-029).

| Field | Meaning |
|---|---|
| `cell_id`, `unit_id` | What was run, and under which cell. |
| `tokens` | Input / output / cache-read, kept separate (FR-014). |
| `accounting_source` | `deterministic-tokenizer` or `provider-usage`. **Load-bearing** — records from different sources must never be summed. |
| `attempts` | Ordered attempt outcomes for first-attempt-success derivation. |
| `retries_corrective`, `retries_infrastructure` | Counted separately (FR-011). |
| `completion` | The suite's pass verdict, or `no-signal`. Never inferred. |
| `partial` | True if the run did not finish; such records are excluded from headlines (FR-032). |

**Validation rules**

- `accounting_source` is mandatory. A record without it cannot enter any aggregate.
- `completion: no-signal` excludes the record from completion-dependent figures rather than
  counting it as either success or failure.
- For provider-sourced records, the number of provider responses summed MUST be recorded, so a
  partially-captured unit is distinguishable from a genuinely cheap one.

---

## Measurement

An aggregate over `RunRecord`s, and the only thing a report renders.

| Field | Meaning |
|---|---|
| `value`, `unit` | The figure. |
| `provenance` | measured / computed / estimated — **per row**, not only per section. |
| `accounting_source` | Inherited from its records; an aggregate spanning sources is forbidden. |
| `runs`, `spread` | Required for any model-dependent figure (FR-021). |
| `fleet_shape` | Tool count + definition-size distribution the figure holds for (FR-016 / IC-004). |
| `withheld`, `withheld_reason` | Set instead of a value when the figure cannot be computed honestly. |

**Validation rules**

- A cross-source aggregate MUST be **withheld with a stated reason**, never computed. This
  reuses the harness's existing withhold-rather-than-compute pattern.
- A model-dependent figure with `runs < 4` MUST NOT be marked as a headline (FR-021).
- A figure without `fleet_shape` MUST NOT be published as a percentage.
- `provenance` at row level is required because FR-013 permits measured and estimated figures
  to coexist inside one section — the existing section-level badge cannot express that.

---

## PayloadDecomposition

Attribution of tool-definition cost for one fleet shape (FR-024, FR-025).

| Field | Meaning |
|---|---|
| `fleet_shape` | Which corpus/fleet this describes. |
| `share_names`, `share_descriptions`, `share_annotations`, `share_schemas` | The four attributions. |
| `achievable_ceiling` | Recomputed **per corpus** — never carried forward as a constant. |
| `spec102_verdict` | Explicit `confirmed` or `corrected`, with the delta. |

**Validation rule**: the ceiling MUST be recomputed for each fleet shape. Reusing a previously
published ceiling is precisely the error spec 102 made when it projected from one corpus.

---

## Relationships

```text
ReplaySession 1──* ReplayCall
ReplaySession *──* ModeCell        → RunRecord   (US1: deterministic, one record per pair)
TaskSet       *──* ModeCell        → RunRecord   (US2: live loop, k runs per pair)
RunRecord     *──1 Measurement                    (aggregated, never across accounting sources)
FleetShape    1──1 PayloadDecomposition
```

## Invariants that must hold across the whole model

1. **No recorded content leaves the loader.** Sessions and calls surrender counts and sizes;
   nothing downstream can reach a body.
2. **Accounting sources never mix.** Enforced at aggregation by withholding.
3. **Silence is never success.** Truncated, partial, no-signal and skipped are each distinct
   from zero and each are reported.
4. **Every published percentage carries its fleet shape.**
