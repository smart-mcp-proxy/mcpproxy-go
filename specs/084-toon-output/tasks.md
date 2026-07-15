---
description: "Task list for feature 084 — Adaptive TOON Output for Tool Results"
---

# Tasks: Adaptive TOON Output for Tool Results

**Input**: Design documents from `specs/084-toon-output/`
**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/

**Tests**: REQUIRED. The spec is test-heavy (determinism, never-larger, detection-parity, byte-identity
are all property/regression invariants) and the constitution mandates TDD (Principle V). Every
implementation task is paired with a failing test written first.

**Organization**: grouped by user story. US1 (adaptive core, P1) is the MVP; US2 (operator control,
P1) makes it operable; US3 (safety chain, P2) proves the invariants; US4 (profiler, P2) measures it.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: can run in parallel (different files, no dependency)
- **[Story]**: US1 / US2 / US3 / US4 (or none for setup/foundational/polish)

---

## Phase 1: Setup

- [x] T001 Add `github.com/toon-format/toon-go v0.0.0-20251202084852-7ca0e27c4e8c` to `go.mod`/`go.sum`
      (`go get`, then `go mod tidy`). Coordinate with spec-083 / PR #851: if 083 merged first the
      require line is already present — verify and skip the `go get`, keep `go mod tidy`. (research D-DEP)
- [x] T002 [P] Create `internal/toonenc/` package skeleton with a package doc comment stating the
      layering rule (stdlib + toon-go only; imported by both `internal/server` and `bench/arms`).

**Checkpoint**: dependency resolves; package compiles empty.

---

## Phase 2: Foundational (blocks all stories)

**⚠️ Types, marker, and config plumbing that every story builds on.**

- [x] T003 [P] Test `internal/toonenc/mode_test.go`: `ParseMode` table (`""`→off, valid enums, invalid→false).
- [x] T004 [P] `internal/toonenc/mode.go`: `type Mode string`; `ModeOff/ModeAdaptive/ModeAlways`; `ParseMode`.
- [x] T005 [P] Test `internal/toonenc/marker_test.go`: assert `Marker` equals the exact contract bytes
      (contracts/marker-format.md); assert emission = `Marker+"\n"+body`.
- [x] T006 [P] `internal/toonenc/marker.go`: `const Marker` + `AssembleEmission(body string) string`.
- [x] T007 [P] Declare value types in `internal/toonenc/types.go`: `Classification`, `NotTabularReason`,
      `Decision`, `Outcome` (per data-model.md §2–§3). Types only; no logic.
- [x] T008 Test `internal/config/config_toon_test.go`: top-level defaults (`off`/`15`),
      `ResolveToonOutput(sc)` precedence (per-server non-empty > global > off), and validation happy path.
- [x] T009 `internal/config/config.go`: add top-level `ToonOutput string` + `ToonMinSavingsPct int`
      (near line 138) and `DefaultConfig` defaults (near line 1358: `"off"`, `15`).
- [x] T010 `internal/config/config.go`: add `ServerConfig.ToonOutput string` (near line 445) and
      `ResolveToonOutput(sc *ServerConfig) string` resolver — **string-only, NO `toonenc` import**
      (finding 4). Caller parses to `toonenc.Mode` via `toonenc.ParseMode` at the server/bench boundary.

**Checkpoint**: types, marker, and config fields exist and validate; no encoding behavior yet.

---

## Phase 3: User Story 1 — Adaptive TOON on tabular results (P1) 🎯 MVP

**Goal**: With `adaptive` on, a uniform tabular result is TOON-encoded (marker + hint + body) and
smaller by the threshold; a nested/non-tabular result is byte-identical to `off`.

**Independent Test**: call a tool returning a uniform 100-row array → TOON + marker, smaller by ≥
threshold; call a tool returning a nested object → identical to `off`.

### Tests (write first, must fail)

- [x] T011 [P] [US1] `internal/toonenc/classifier_test.go`: tabular ≥4-object array; envelope
      (single-key object → inner array); reject <4 rows, non-object elements, nested values, empty
      array, non-JSON; 90%-present union key set; ragged→`ReasonTooRagged`. (FR-003b)
- [x] T012 [P] [US1] `internal/toonenc/encoder_test.go`: **determinism** (encode twice ⇒ identical
      `out`+`Decision`, FR-011); **randomized-key-order determinism** (finding 5 — N shuffled-key JSON
      serializations of one nested fixture ⇒ byte-identical `out`, proving `canonicalToon` not map order
      fixes output); **never-larger** property over fixtures in `adaptive` (FR-004/SC-003); threshold
      table (≥threshold→Encoded, near-tie→PassthroughBelowThreshold); nested/scalar/short→
      PassthroughNotTabular; marker round-trips via `toon.DecodeString`. (FR-003, FR-005, FR-011)

### Implementation

- [x] T013 [US1] `internal/toonenc/classifier.go`: `Classify(v interface{}) Classification` — pure,
      deterministic (sorted-key set), flat-scalar-only v1, envelope unwrap. (FR-003b, FR-011)
- [x] T013b [US1] `internal/toonenc/canonical.go`: `canonicalToon(v interface{}) interface{}` —
      recursively rewrite every `map[string]interface{}` into a key-sorted `toon.NewObject`; arrays,
      scalars, and `json.Number` pass through. Guarantees deterministic bytes without trusting toon-go's
      map handling (finding 5). Covered by the T012 randomized-key test. (FR-011)
- [x] T014 [US1] `internal/toonenc/encoder.go`: `EncodeBlock(text, mode, minSavingsPct, retainedBudget)`
      — parse(UseNumber)→classify→`canonicalToon`→`toon.MarshalString`→assemble→size-compare (adaptive
      path); returns `Decision`. **Pure function — no logging/metrics** (FR-006 observability is the
      caller's job, see T-ERR). Depends on T013, T013b. (FR-003, FR-004, FR-005, FR-011)
- [x] T015 [US1] `internal/server/toon_encode.go`:
      `(p *MCPProxyServer) encodeToonBlocks(serverName, toolName, contentTrust string,
      args map[string]interface{}, result *mcp.CallToolResult)
      (detectionText string, decisions []toonenc.Decision)` — `toolName`/`args` feed the T-ERR log
      fields (issue 4) and the truncation cache key; `contentTrust` drives the spotlight reconstruction
      (round-3). Parses the resolved mode; when `off`, returns **`("", nil)`** — empty `detectionText`
      so `runAsyncDetection` falls back to today's `response`, zero behavior change (issue 2); does NOT
      synthesize a rendering. Else walks the content blocks rewriting each `TextContent` with
      `EncodeBlock`, and builds `detectionText` as a **best-effort reconstruction of the detection-relevant
      content the OFF path scans** (NOT byte-identical to `response`, which is unachievable — see
      data-model §7): all-blocks rendering (text + `[image:…]`/`[audio:…]`/unknown placeholders,
      `content_forward.go:112-126`) → truncate each block with the same `p.truncator` budget/`toolName`/`args`
      → spotlight-wrap untrusted text via `security.SpotlightUntrusted` when `contentTrust` is untrusted.
      Passes `retainedBudget` from `p.truncator.SimpleTruncateBudget()` to `EncodeBlock`.
- [x] T016 [US1] `internal/server/mcp.go`: insert the `encodeToonBlocks(serverName, actualToolName,
      contentTrust, args, result)` call in `handleCallToolVariant` **after the Spec 069 raw-byte
      measurement (~2100) and before `forwardContentResult` (~2102)** — do NOT move the byte measurement
      at 2099-2100 (finding 8); `contentTrust` is already in scope at this point. Hold `detectionText` +
      `decisions` for later threading. (research D-SEAM)
- [x] T017 [US1] Thread decisions into the `tool_call` activity metadata: `internal/runtime/event_bus.go`
      `EmitActivityToolCallCompleted` gains a `toon_output` payload key (when non-nil);
      `internal/runtime/activity_service.go` `handleToolCall` (~470) merges it into `metadata`.
      Update `emitActivityToolCallCompleted` (mcp.go:589) + call site (mcp.go:2148) to pass decisions. (FR-010)
- [x] T018 [US1] E2E `internal/server/e2e_test.go` (or a focused server test): adaptive + uniform array
      → response carries `Marker` and is smaller by ≥ threshold; adaptive + nested object → byte-identical
      to `off`; activity metadata records the outcome. (SC-006, US1-AC1/AC2)

**Checkpoint**: MVP — adaptive encoding works end-to-end with marker + activity decision.

---

## Phase 4: User Story 2 — Operator control: off / adaptive / always, per-server (P1)

**Goal**: enable globally, per-server disable, `always` for benchmarking, revert — all via config +
hot-reload, no restart.

**Independent Test**: toggle config values, observe encoding change on the next call without restart.

### Tests (write first, must fail)

- [x] T019 [P] [US2] `internal/config/config_toon_test.go` (extend): validation edge cases — invalid
      `toon_output` (top-level + per-server) → `ValidationError`; `toon_min_savings_pct` out of [1,90]
      → error; per-server override precedence over global. (FR-001)
- [x] T020 [P] [US2] `internal/toonenc/encoder_test.go` (extend): `always` mode (finding 1 — FR-009 is
      normative: encodes ANY JSON value) — nested object → Encoded; scalar/bool/number → Encoded; tabular
      below the adaptive threshold → Encoded; **non-JSON text → passthrough (no marker)**; still honors
      the too-small-budget guard. (FR-009)
- [x] T021 [P] [US2] `internal/runtime/config_hotreload_test.go`: a lone `toon_output` /
      `toon_min_savings_pct` edit is reported as a changed field (not "no changes"). (FR-001)

### Implementation

- [x] T022 [US2] `internal/config/config.go` `ValidateDetailed` (~1600): enum + range validators for
      top-level and per-server `toon_output` / `toon_min_savings_pct`, clear `Field`/`Message`. (FR-001)
- [x] T023 [US2] `internal/toonenc/encoder.go`: add the `always` mode gate — encode any JSON-parseable
      value regardless of tabular classification or size comparison; non-JSON → passthrough; still
      subject to the too-small-budget guard (finding 1, FR-009). Classification is recorded for the
      Decision but does not gate encoding in this mode.
- [x] T024 [US2] `internal/runtime/config_hotreload.go` (~95): add change-detection entries for
      `toon_output` and `toon_min_savings_pct` (per-server already covered by the `Servers` DeepEqual). (FR-001)
- [x] T025 [US2] E2E: enable global `adaptive` → tabular encodes; add per-server `off` → that server
      passes through while others encode; set global `off` → all byte-identical — each within one
      hot-reload cycle, no restart. (SC-005, US2-AC1/AC2/AC3/AC4)

**Checkpoint**: full operator surface working and hot-reloadable.

---

## Phase 5: User Story 3 — Safety-chain ordering unaffected (P2)

**Goal**: sanitisation sees the raw result, detection sees pre-encoding text, truncation runs after
encoding, encoder errors fall back to passthrough, structured validation is unaffected, and the
feature never touches non-`call_tool_*` surfaces.

**Independent Test**: known-secret fixture on/off → identical detection; oversized tabular result →
marked truncated with intact notice; encoder error → data preserved.

### Tests (write first, must fail)

- [x] T026 [P] [US3] Server test: output sanitisation (redact) runs on the raw result **before**
      encoding — a redacted secret is absent from the encoded TOON body. (FR-007a)
- [x] T027 [P] [US3] Detection-**finding**-parity test (round-3 — compare finding SETS, not raw bytes):
      for each fixture, run detection with TOON on vs off and assert the **`DetectionEvent` sets are
      equal** — compare `{detector, rule/category, matched span content}` per finding, NOT the raw
      `detection_text` bytes (which legitimately differ by the truncation banner's timestamped cache key
      and are not expected to match). Fixtures: (1) within-limit tabular; (2) **mixed-content** (secret
      in a text block NEXT TO an image block — proves the all-blocks rendering, finding 6); (3)
      **over-limit** result whose secret survives truncation — findings still equal because
      `detectionText` is truncated with the same budget (issue 5); (4) **untrusted-content** fixture —
      proves the spotlight reconstruction (`contentTrust` passed in) doesn't change the finding set.
      Also assert `off` mode leaves `detection_text` empty so the detector scans today's `response`
      unchanged (issue 2). Run against the sensitive-data security corpus. (FR-007b, SC-004)
- [x] T028 [P] [US3] Truncation test: oversized tabular result → encoded then truncated; standard
      truncation notice present; marker/hint at head, not truncated away. **Budget-boundary cases
      (finding 2)**: with `SimpleTruncateBudget()` just below `len(Marker)+1+MinToonRowBytes` →
      passthrough; just at/above → encode proceeds; assert in both `adaptive` and `always`. (FR-008/FR-009)
- [x] T029 [P] [US3] Error-fallback test (issue 3 — distinguish the two failure classes): a
      **genuine encoder failure** (JSON parses + classifies, but `canonicalToon`/`toon.MarshalString`
      errors — inject via a fault, e.g. a value toon-go rejects, or a seam-level failing marshal
      wrapper) → `out == text`, `Outcome: passthrough-error`. Separately, a **parse failure / non-JSON**
      block → `Outcome: passthrough-not-tabular` (ordinary, NOT passthrough-error, NOT logged). Neither
      surfaces as a tool-call error (`EncodeBlock` is pure). (FR-006)
- [x] T030 [P] [US3] Structured-content test: a result with `StructuredContent` → output-schema
      validation still evaluates the original structured result; TOON text rewrite doesn't affect it. (FR-010b)
- [x] T031 [P] [US3] Surface-isolation tests: `retrieve_tools`, `code_execution`, and direct-mode server
      tools produce byte-identical output for every mode value (off/adaptive/always). (FR-013, FR-014)

### Implementation

- [x] T031b [US3] `internal/truncate/truncator.go`: add `SimpleTruncateBudget() int` — returns
      `limit - min(200, limit/2)` (the content bytes the `simpleTruncate` path actually retains, since
      encoded TOON is non-JSON and always hits that path), `0` when `limit == 0` (unlimited). Unit test
      for the messageSpace boundary at small limits (finding 2).
- [x] T032 [US3] `internal/toonenc/encoder.go`: add the too-small-**budget** guard using the
      `retainedBudget` param + `MinToonRowBytes` (`retainedBudget > 0 && retainedBudget <
      len(Marker)+1+MinToonRowBytes` → passthrough). NOT based on the raw `ToolResponseLimit` (finding 2).
      Precedence in every mode. (FR-008/FR-009)
- [x] T-ERR [US3] Error observability at the server seam (finding 3, FR-006): in `encodeToonBlocks`
      (T015), on any block with `Outcome == passthrough-error` emit a `zap.Warn` (server, tool, block
      index) and increment a fallback counter (reuse the telemetry registry / existing non-fatal-fallback
      mechanism). Test with a forced encode error asserts BOTH the warn (zap observer core) and the
      counter increment.
- [x] T033 [US3] Detection-text thread: `internal/runtime/event_bus.go` adds an **optional**
      `detection_text` payload key (only when non-empty); `internal/runtime/activity_service.go`
      `runAsyncDetection` (~551) scans `detection_text` when present/non-empty, **else falls back to
      `response`** (so `off` and all non-`call_tool_*` paths are unchanged — issue 2). Server passes the
      best-effort reconstructed `detectionText` (from T015 — all-blocks + same-budget truncation +
      spotlight via `contentTrust`; `""` when `off`) through `emitActivityToolCallCompleted`. The
      contract is finding parity, not byte parity (see T027, data-model §7). (FR-007b)
- [x] T034 [US3] Confirm `applyOutputValidation` (mcp.go:2107) still receives `forwarded` whose
      `StructuredContent` is the original (encoder only rewrites `TextContent`) — add a guard/comment if
      needed; no functional change expected. (FR-010b)

**Checkpoint**: every security/truncation invariant proven; feature confined to the one surface.

---

## Phase 6: User Story 4 — Measured by the profiler (P2)

**Goal**: the spec-083 profiler's results arm exercises the exact production encoder over the
result-fixtures corpus and reports per-class savings + decision counts.

**Independent Test**: run the profiler's adaptive-encoder arm on the result-fixtures corpus; report
contains per-class savings and decision counts.

### Tests (write first, must fail)

> **CROSS-BRANCH-BLOCKED on PR #851 (2026-07-14)**: `bench/arms/` does not exist on this branch —
> the spec-083 profiler harness it implements against lives on the 083 branch (PR #851) and has not
> been merged into `main`/this worktree. T035/T036 must follow once #851 lands and this branch
> rebases (the `internal/toonenc` import surface they need — `EncodeBlock`, `Decision`, `Mode` —
> is complete and stable). Matching sequencing: spec 085 T040/T043.

- [ ] T035 [P] [US4] **CROSS-BRANCH-BLOCKED on PR #851** (`bench/arms/` lives only on
      `083-discovery-profiler`; do after #851 merges and this branch rebases — do not fabricate
      `bench/arms/` here): `bench/arms/toon_results_test.go`: the arm reports the three SC-001 metrics
      (savings on encoded subset; informational savings across all tabular; byte-identical passthrough on
      non-tabular) and the four decision counts; asserts it calls `toonenc.EncodeBlock` (same code path). (SC-001)

### Implementation

- [ ] T036 [US4] **CROSS-BRANCH-BLOCKED on PR #851** (same rationale as T035):
      `bench/arms/toon_results.go`: implement the spec-083 `Arm` interface for the results
      corpus, importing `internal/toonenc` and calling `toonenc.EncodeBlock` per fixture block; register
      it in the arm index. Depends on the spec-083 harness (PR #851) being merged. (FR-012)

**Checkpoint**: the feature proves/disproves itself every release run.

---

## Phase 7: Polish & Cross-Cutting

- [x] T037 [P] `internal/server/mcp.go` `buildCallToolVariantTool` (~615): echo the marker/decode-hint
      contract in the `call_tool_read|write|destructive` descriptions so agents learn it in-session. (FR-005)
- [x] T038 [P] `docs/configuration.md`: document `toon_output` / `toon_min_savings_pct` + per-server
      override; `docs/features/toon-output.md`: new feature doc (adaptive rationale, modes, safety chain).
- [x] T039 [P] `CLAUDE.md` "Recent Changes": add the 084 line (new deps note: toon-go added).
- [ ] T040 Full gate: `go test -race ./internal/...`, `go test ./bench/...`, `./scripts/test-api-e2e.sh`,
      `/opt/homebrew/bin/golangci-lint run --config .github/.golangci.yml ./...`. All green before PR.

---

## Dependencies & Execution Order

- **Setup (T001–T002)** → **Foundational (T003–T010)** → user stories.
- **US1 (T011–T018)**: the MVP; depends only on Foundational. T013+T013b→T014→T015→T016; T017 depends
  on T016; T018 depends on T016–T017.
- **US2 (T019–T025)**: depends on Foundational + US1 seam (T016). T023 (always) extends T014.
- **US3 (T026–T034 + T031b, T-ERR)**: depends on US1 seam (T015–T017). T032 extends T014; T033 +
  T-ERR extend T015+T017; T032 depends on T031b (`SimpleTruncateBudget`).
- **US4 (T035–T036)**: depends on US1 encoder (T014) + external spec-083 harness (#851).
- **Polish (T037–T040)**: after the stories it documents.

### Parallel opportunities

- Foundational: T003/T004, T005/T006, T007 are independent files → parallel; T008–T010 share `config.go` → serial.
- US1 tests T011/T012 parallel; impl T013→T014 serial (same package, dependency).
- US3 tests T026–T031 are independent test files → all parallel.

---

## FR / SC Coverage Matrix

| Requirement | Task(s) |
|-------------|---------|
| FR-001 config + per-server + hot-reload | T009, T010, T022, T024, T008, T019, T021 |
| FR-002 off byte-identical | T018, T025 |
| FR-003 adaptive decision (JSON/classify/size) | T011, T012, T013, T013b, T014 |
| FR-004 never-larger | T012 (property), T014 |
| FR-005 marker + decode hint | T005, T006, T012, T018, T037 |
| FR-006 error → passthrough + logged/counted | T029 (encoder), T-ERR (seam log+count), T014 |
| FR-007a sanitise before encode | T026, T016 (seam position) |
| FR-007b detection sees pre-encoding all-blocks text | T027 (incl. mixed-content), T033 |
| FR-008 truncate after encode + too-small budget guard | T028, T031b, T032, T016 |
| FR-009 always mode (any JSON value) | T020, T023 |
| FR-010 activity decision metadata | T017, T018 |
| FR-010b structured-content validation unaffected | T030, T034 |
| FR-011 determinism (canonicalToon) | T012 (incl. randomized-key), T013, T013b, T014 |
| FR-012 profiler results arm | T035, T036 |
| FR-013 retrieve_tools not encoded | T031 |
| FR-014 surface isolation (code_exec/direct/listings) | T031 |
| SC-001 profiler three metrics | T035, T036 |
| SC-002 off regression (byte + record) | T018, T025 |
| SC-003 never-larger across corpus | T012 |
| SC-004 detection parity | T027 |
| SC-005 enable/per-server/revert hot-reload E2E | T025 |
| SC-006 marker on encoded, none on passthrough | T018 |

**Every FR-001..FR-014 and SC-001..SC-006 maps to at least one task. No unmapped requirement.**

---

## Notes

- Commit convention (spec §Commit Message Conventions): `Related #<issue>`, no `Fixes/Closes`, no
  Claude co-authorship trailer.
- `always` mode is documented benchmark-only; FR-004 never-larger is asserted for `adaptive` only.
- The encoder reads config fresh per call (mirrors `applyOutputSanitisation`), so hot-reload needs no
  cache invalidation — only the change-detection entries (T024) to acknowledge the reload.
