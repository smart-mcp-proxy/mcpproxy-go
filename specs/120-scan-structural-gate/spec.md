# Feature Specification: Structural change gate in front of the scan-mode approval

**Feature Branch**: `120-scan-structural-gate`
**Created**: 2026-10-25
**Status**: Draft, follow-up to spec 119 (PR #1740). Ships in the same release as the `scan` default.
**Input**: Maintainer reply on issue #1713 (2026-10-25) after the external reviews by james-crowley and
kaspermuutra-dotcom and the 2026-10-23 brainstorm. Phase 0 of that decision: a deterministic structural
delta check in front of the scan, audit before-state on the auto-approval event, an upgrade warning, no
classifier, no thresholds, no keyword lists, residual risk stated plainly.

## Why

Spec 119 lets a clean offline scan approve a changed tool contract under `scan`. The scanner reads a
schema as text: no detector reads `required`, `enum`, `pattern`, `default`, `additionalProperties` or the
output schema (`capability_mismatch` decodes `properties` only), and the scanner's tool view carries no
annotations. So a new `bcc` parameter with a clean description, an `openWorldHint` flipped to `false` (which
marks the tool's output as trusted, `ContentTrustForTool`) or a defaulted `webhook_url` all sail through a
clean scan today. The only non-text rule is the safer-direction hint flip (`GATE-HINT-SAFER-FLIP`).

These are structural facts about the contract, decidable without any judgement about text. The gate holds
them whatever the scan says. `manual` is unchanged (RT-02 still governs it); `auto` is unchanged.

## User Stories

### US1 — Structural widenings of a changed contract are held under `scan` (P1)

- **FR-001** A new pure function `contractDelta(approved, live) []Signal` in
  `internal/runtime/tool_contract_delta.go` replaces `saferHintFlip`/`withHintFlip`. It compares the
  approved contract snapshot (description is NOT compared: text is the scanner's) with the live one and
  returns the deterministic hold signals below, in a fixed order, deduplicated. Nothing in it reads a word
  list, an edit distance, a token list or a threshold.
- **FR-002** `checkToolApprovals` calls it at the two spec-119 gate sites: the approved→changed branch
  (against `existing.Current*`, the approved snapshot at that point) and the still-changed re-evaluation
  branch (against `existing.Previous*`). Under `scan` a clean, full-coverage verdict approves the change only
  when `contractDelta` returns no signal. Otherwise the change is held with `held_reason: scan_findings`,
  the verdict as scanned (`clean` when the scan was clean) and the gate signals PREPENDED to the scan's own
  signals, so `storage.MaxToolHeldSignals` never drops them. The signals are re-evaluated and re-recorded on
  every pass the delta stands; a later pass whose delta is empty and whose scan is clean approves (FR-006
  of spec 119).
- **FR-003 `GATE-HINT-CHANGE`** — any change to the EFFECTIVE value of any of the four MCP safety hints
  (`readOnlyHint`, `destructiveHint`, `idempotentHint`, `openWorldHint`), in either direction, with the MCP
  defaults applied (readOnly `false`, destructive `true`, idempotent `false`, openWorld `true`). One signal per
  hint: `GATE-HINT-CHANGE:<hint>:<old>>>:<new>` is rendered as `GATE-HINT-CHANGE:openWorldHint:true>false`.
  The two safer flips (`readOnlyHint` false→true, `destructiveHint` true→false) ALSO emit the spec-119 signal
  `GATE-HINT-SAFER-FLIP`, first, so existing Web/Swift tests and labels keep working.
- **FR-004 `GATE-SCHEMA-SHAPE`** — a recursive keyword-path diff of the input schema. The walk pairs nodes by
  path through `properties`, `items` (object or tuple), `$defs`/`definitions`, `anyOf`/`oneOf`/`allOf`,
  `patternProperties`, `additionalProperties` (when it is a schema) and `not`. Each signal carries a reason
  and a JSON-pointer-like path: `GATE-SCHEMA-SHAPE:<reason>:<path>` (path `/` for the root, `bcc`,
  `attachments/items/url`). Held (any one of):
  - `new-required:<name>` — a name appears in `required` that was not required before (a new or an existing
    property);
  - `type:<path>` — `type` differs (string vs array compared as sets; adding a type is widening, removing one
    still holds: any change). `$ref` differs: same reason;
  - `enum-added:<path>` — the `enum` set gained a member; `enum-dropped:<path>` — the `enum` keyword was
    removed; an `enum` whose members were only removed, or an `enum` added to an existing property, passes
    (narrowing);
  - `pattern:<path>`, `format:<path>` — added, removed or changed. Regex and format changes are never
    narrowing;
  - `bound:<path>:<keyword>` — `minimum`/`exclusiveMinimum`/`minLength`/`minItems`/`minProperties`
    decreased or removed; `maximum`/`exclusiveMaximum`/`maxLength`/`maxItems`/`maxProperties` increased or
    removed; `multipleOf` changed or removed; a non-numeric bound value on either side (draft-4 booleans) is
    compared as text and any difference holds. Tightening or adding a bound passes;
  - `additional-properties:<path>` — `additionalProperties` goes from `false` to anything else (absent,
    `true`, a schema). Setting it to `false` passes; two schemas recurse;
  - `default:<path>` — `default` added or changed (removal passes); `const:<path>` — `const` added, changed
    or removed;
  - `new-property:<name-path>` — a new property (or new `$defs` entry, or new `patternProperties` entry)
    whose subtree carries a `default`, `const` or `enum` anywhere, or that is required. A new property with
    none of those is the benign optional parameter and passes;
  - `combinator:<path>:<keyword>` — `anyOf`/`oneOf` gained members, `allOf` lost members, or a `not`
    changed; equal-length lists recurse pairwise; `items` changing between object and tuple form, or a tuple
    changing length, is `type:<path>`;
  - `approved-unknown:/` — the approved input schema is empty or not valid JSON while the live one is not
    empty (a record from before full contract capture, or a changed record whose `Previous*` was cleared
    because the stored snapshot was not proven approved). Unknown is not a wildcard;
  - `unparsable:/` — the live schema is not valid JSON and differs from the approved text.
  Passes: property removed (required or not), `required` entry removed, enum member removed, bound tightened
  or added, `additionalProperties` set to `false`, `default` removed, text keywords (`description`, `title`,
  `examples`, `$comment`) anywhere, key-order and whitespace differences, and keywords this list does not
  name (recorded as residual risk below).
- **FR-005 `GATE-OUTPUT-SCHEMA`** — `GATE-OUTPUT-SCHEMA:added`, `:removed`, or `:changed` when the output
  schema, with `description`/`title`/`examples`/`$comment` stripped at every level and keys sorted, differs.
  An approved empty output schema versus a live empty one is no change.
- **FR-006** Explicitly rejected (do not build): keyword lists for recipient/URL/path/command names, edit
  distance or similarity thresholds, benign/unsure/dangerous classes, holding every new optional property,
  sticky finding holds, a tool-churn rule, a review-simulate CLI, a tier ceiling, the verifier tier, and the
  `default_trust_mode` knob (a contributor PR).
- **FR-007** The Web review differ (`schemaLines` in `frontend/src/utils/reviewPresentation.ts`) is top-level
  only and stays so; the Go walk recurses. The two share one fixture file,
  `internal/runtime/testdata/contract_delta_cases.json`: Go asserts the exact signals per case; vitest
  asserts that every case the Go gate HOLDS produces at least one review line (nothing held is invisible on
  the review screen).

### US2 — A hash-equal dispatch-tier drop is held (P1)

- **FR-008** The approval hash folds the hints in as their EFFECTIVE fingerprint (`safetyHintsFingerprint`,
  `""` for the MCP defaults), while `contracts.DeriveCallWith` routes an explicit `destructiveHint: true` as
  `call_tool_destructive` and an explicit `readOnlyHint: false` as `call_tool_write` but NO annotations as
  `call_tool_read`. So a tool approved with explicit `{readOnlyHint:false, destructiveHint:true}` whose live
  definition drops its annotations keeps its approval hash while `retrieve_tools` starts naming
  `call_tool_read` for it. Verified by the failing test written first (`TestHashEqualDispatchDrop_*`): it
  bites.
- **FR-009** Rule: on a record whose annotation snapshot was captured (`HashSchemaVersion >=
  storage.AnnotationHashSchemaVersion`), a live definition whose `DeriveCallWith` variant differs from the
  stored snapshot's is a contract change even when the hash is equal. The hash-match shortcut, the formula
  migration and the content-match shortcuts are closed for it; it flows into the change gate like any other
  change (held under `scan` with `GATE-HINT-CHANGE:dispatch:<old>>:<new>`, e.g.
  `GATE-HINT-CHANGE:dispatch:destructive>read`; held under `manual`; approved under `auto`). The record's
  `CurrentHash` then equals its `ApprovedHash`; the revert proof (`previousHintsProveRevert`, non-nil
  previous snapshot) requires the dispatch variant to match too, so the record is restored only when the
  server restores the explicit hints. Any direction holds (a tool that gains an explicit `destructiveHint:
  true` was being dispatched as read until now, which the operator should also see). A `title`-only
  annotation change is not a dispatch change and is absorbed as today.

### US3 — The auto-approval event carries the before-state (P1)

- **FR-010** `tool_auto_approved` for a CHANGED contract (scan-approved, and `auto_approve_tool_changes`
  at both sites) carries the approved contract it replaced: `old_hash`, `old_description`, `old_schema`,
  `old_output_schema`, `old_annotations` (JSON), the new values for each, `approved_by`, `gate_signals`
  (the signals `contractDelta` evaluated — empty on an approval), `scan_verdict` and `scan_bundle` (the
  active TPA bundle's fingerprint, or its version when the fingerprint is empty) when a scan ran. Text fields
  keep the 64 KiB truncation. The first-discovery and pending-promotion `tool_auto_approved` events have no
  approved before-state and are unchanged except for `approved_by`.

### US4 — Upgrade visibility (P2)

- **FR-011** One-time startup `WARN` (runtime `StartBackgroundInitialization`, persisted marker
  `scan_default_notice_done` in BBolt `MetaBucket` next to `baseline_sweep_done`): names every enabled server
  whose `trust_mode` is empty and resolves to `scan` by default (no legacy flag), says they now run under
  `scan` and that clean-scanned held changes are released on the next discovery pass, and names the
  opt-out (`trust_mode: "manual"`). Written once: the marker is set on the first start that sees at least one
  configured server; a fresh install with no servers logs nothing and leaves the marker unset. A marker read
  error logs nothing and writes nothing.
- **FR-012** `config.DoctorFindings` adds the same finding to `mcpproxy doctor` on every run while such
  servers exist (`config.ScanDefaultTrustFinding`).

### US5 — Docs, contracts, model (P2)

- **FR-013** Hold-signal ids are a terminology family: `internal/contracts/terminology.go`
  (`HoldSignalHintChange`, `HoldSignalHintSaferFlip`, `HoldSignalSchemaShape`, `HoldSignalOutputSchema`,
  `AllHoldSignals()`), the golden `testdata/terminology.json` family `hold_signal` with labels, the generated
  `HoldSignal*` consts in `frontend/src/types/contracts.ts`, and `HOLD_SIGNAL_LABELS` / `gateSignalLabel()`
  in `reviewPresentation.ts` used by the hold-evidence chips (short label, raw signal in the title).
- **FR-014** Stale RT-02 wording is fixed: `internal/config/config.go` (`AutoRescanOnChange` doc), the
  `resolveToolQuarantineGate` doc comment, spec 119 FR-007 ("safer flip" → "structural hold signals, spec
  120"), `docs/features/security-quarantine.md` (trust-mode table, "Holds changed definitions for review",
  hold evidence, "Reviewing a held change", and a "What the structural gate does not catch" paragraph:
  paraphrase, one-word edits, slow text drift across releases, behaviour behind an unchanged definition,
  schema keywords the walk does not name), `docs/configuration.md` (no field changes; the `trust_mode`
  rows mention the gate), the spec-119 CHANGELOG entry.
- **FR-015** Quarantine pilot: `qapilot.Contract` gains `Shape`; fixture `BenignWide` (clean text, a new
  defaulted `webhook_url` property); the model approves a scan-mode change only when the live contract does
  not widen the approved shape; QP-20 and scenario S27 (scan: held on the widening, still held on the next
  pass, operator approval re-baselines, a later narrowing with clean text is scan-approved); QP-10's note is
  updated; one mutation per rule family in `tests/qapilot/mutations.json` (hint change, schema shape, output
  schema, hash-equal dispatch), each detected by the scenario suite and the delta table test.

## Residual risk (stated plainly)

Not caught by this gate or by the scan: a well-written paraphrase of a tool description, a one-word edit, slow
text drift over several releases, a server whose behaviour changes while its definition does not, and
structural keywords the walk does not name (`dependentRequired`, `if`/`then`/`else`, `contains`,
`uniqueItems`, vocabulary extensions). Controls that remain: `trust_mode: manual` on servers you do not trust,
a profile `max_tier`, the response pipeline, the per-token rate limit (#1699).

## Test-fixture convention

As spec 119: `setupQuarantineRuntime` pins fixtures with no `trust_mode` to `manual`; tests of the default tier
use `scanServer`.

## Acceptance

1. `scan` server, approved `send_mail` gains a required `bcc` parameter with a clean description: `changed`,
   blocked, `held_reason: scan_findings`, `held_verdict: clean`, `held_signals` begins with
   `GATE-SCHEMA-SHAPE:new-required:bcc`; `ApprovedHash` unmoved; still held on the next pass; approved by an
   operator click.
2. Same with a new optional `labels` parameter (no default/const/enum): scan-approved, `tool_auto_approved`
   event carries `old_schema`, `new_schema`, `gate_signals: []`, `scan_verdict: clean`, `scan_bundle`.
3. `openWorldHint` true→false and nothing else: held with `GATE-HINT-CHANGE:openWorldHint:true>false`.
   `readOnlyHint` false→true: held with `GATE-HINT-SAFER-FLIP` then `GATE-HINT-CHANGE:readOnlyHint:false>true`.
4. Approved explicit `{readOnlyHint:false, destructiveHint:true}`, live drops annotations: hash equal, held
   with `GATE-HINT-CHANGE:dispatch:destructive>read`; restoring the hints restores `approved`.
5. Enum member removed, `maxLength` lowered, `additionalProperties: false` added, optional property removed:
   all scan-approved with a clean text.
6. `manual` server, same widenings: held as before (`awaiting_review`, clean).
7. Upgrade with two enabled servers without `trust_mode`: one WARN naming both on first start, none on the
   second; `mcpproxy doctor` names both on every run; fresh install: nothing.
8. `go run ./cmd/qapilot report` gate, `qapilot mutate` with M15–M18, terminology goldens (Go, vitest) pass.
