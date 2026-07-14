---
description: "Task list for Compact Router (spec 085)"
---

# Tasks: Compact Router — Progressive-Disclosure Tool Discovery

**Input**: `/specs/085-compact-router/` (plan.md, spec.md, research.md, data-model.md, contracts/)
**Tests**: REQUIRED (Constitution V + spec makes byte-identity/ranked-identity/never-elide/lossy
automated checks). Every implementation task is TDD-paired: write the failing test first.

**Format**: `[ID] [P?] [Story] Description` — `[P]` = parallelizable (different files, no dep).
Real repo paths throughout.

**Story priorities** (from spec): US1 P1 (compact responses), US2 P1 (describe_tool),
US3 P2 (self-healing), US4 P2 (rollout/stability), US5 P3 (profiler gates). US3 is
mode-independent and **independently shippable** (design Phase 0) — it depends only on Setup +
the pre-dispatch schema lookup, not on the compact serialization, so it can land first.

**Revised after Codex round 1** — ⟲#N markers tie tasks to the 8 findings addressed.

---

## Phase 1: Setup (shared scaffolding)

- [ ] T001 [P] Create leaf package `internal/toolsig/` with `doc.go` stating the no-`internal/server`-import
  rule (bench must import it) and the grammar reference to `contracts/signature-grammar.md`.
- [ ] T002 [P] Add `ToolResponseMode string` field to `internal/config/config.go` beside
  `RoutingMode` (~290) with `json:"tool_response_mode,omitempty" mapstructure:"tool-response-mode"`
  and a doc comment (orthogonal to routing_mode; default full). No behavior yet.

---

## Phase 2: Foundational (BLOCKING — must complete before US1/US2)

**⚠️ US1 and US2 cannot begin until the signature compiler, its shared cache, the shared
visibility resolver, and the entry-builder seam exist.**

### Signature compiler (blocks US1, US5)

- [ ] T003 [P] Write failing table test `internal/toolsig/signature_test.go` for `Render` covering
  worked examples **E1–E11** in `contracts/signature-grammar.md` (exact `sig`/`desc`/`lossy`
  bytes), including ⟲**#8** E8 (required name absent from `properties` ⇒ `name*~:any`, never
  dropped), E9 (metachar quoting), E10 (non-null type union ⇒ `str|int`), and E11 (unparseable
  schema ⇒ `(~)`, `Lossy=true`, never `()`), plus a determinism test (shuffled property insertion
  order ⇒ identical output) and the `Lossy == contains(sig,"~")` biconditional. — FR-019/SC-004.
- [ ] T004 [P] Write failing test `internal/toolsig/firstsentence_test.go` for `FirstSentence`
  (⟲**#3**): CJK terminator `。` matches **unconditionally** (E6); ASCII `.` splits **only** at a
  boundary (`e.g. text` splits after the space-followed period; `3.14`/`v1.2` do **not** split);
  no-terminator length-cap fallback (rune-safe); empty ⇒ empty.
- [ ] T005 Implement `internal/toolsig/signature.go` (`Render`, `FirstSentence`) per the grammar
  contract until T003/T004 pass — type map incl. `any` fallback, marker order `*~`, atom
  quoting (§3.5), required-first/optional-sorted, enum≤5 inline, short-scalar defaults, `~` lossy
  collapse, unparseable-schema ⇒ `(~)` (bare lossy marker, never `()`+lossy — E11), never-elide-required
  over missing type. (FR-002/003/004/019)
- [ ] T006 [P] Write failing test `internal/toolsig/cache_test.go`: `Get` compiles-on-miss and
  memoizes; distinct hashes ⇒ distinct entries; concurrent `Get`/`Warm` race-clean under `-race`;
  a test-only compile counter increments once per unique hash.
- [ ] T007 Implement `internal/toolsig/cache.go` (RWMutex-guarded `map[hash]Signature`, `Get`,
  `Warm`, test compile counter) until T006 passes — FR-008 keying.

### ⟲#4 — Single cache owner + wiring (blocks US1's FR-008 guarantee)

- [ ] T008 Wire **one** `*toolsig.Cache`: create it in `Runtime` init and pass it into
  `NewMCPProxyServer` (store `p.sigCache`); confirm the indexing path and the request path hold
  the *same* instance. Add a failing-then-passing **compile-count test** (server-level): index N
  tools, warm the cache, issue a compact `retrieve_tools`, assert the cache's compile counter did
  **not** increase (post-index retrieve = pure cache hit). (FR-008, research.md R9)

### ⟲#5 — Shared visibility resolver (blocks US2; strengthens US1/self-heal)

- [ ] T009 [P] Write failing parity test `internal/server/mcp_visibility_test.go`: for a fixed
  session/agent-token, a tool is returned by `retrieve_tools` **iff** `p.toolVisibleToSession`
  says visible — across profile-scoped, agent-scoped, quarantined, pending/changed, and disabled
  cases.
- [ ] T010 Extract `p.toolVisibleToSession(ctx, server, tool) (bool, reason string)` into new
  `internal/server/mcp_visibility.go` from the `serverDiscoverable` closure (`mcp.go:1324`) + the
  inline callable/quarantine passes, preserving order: index presence → profile+agent scope →
  server quarantine → tool approval (pending/changed) → `isToolCallable`. Rewire
  `handleRetrieveToolsWithMode` to call it (behavior-preserving; guarded by existing retrieve
  tests + the byte-identity golden T011). Make T009 pass. (FR-011, research.md R10)

### Entry-builder extraction refactor (BLOCKS compact mode — spec Assumption; behavior-preserving)

- [ ] T011 Write failing golden byte-identity test `internal/server/mcp_entry_builder_test.go`:
  capture the current full-mode `retrieve_tools` response for a fixed corpus+query and assert the
  post-refactor `json.Marshal` bytes are unchanged (FR-006/SC-003). Include `include_stats` on/off.
- [ ] T012 Extract `buildToolEntry(result, mode, opts) map[string]any` into new
  `internal/server/mcp_entry_builder.go` from `mcp.go` (~1428–1492), preserving the exact full
  entry map (name/description/inputSchema/score/server/annotations/call_with/usage_count/last_used).
  Rewire `handleRetrieveToolsWithMode` to call it for the full path; leave cross-cutting sections
  (~1494–1613) in place. Make T011 pass. **No compact path yet.**

### ⟲#1 — Config validation + hot-reload wiring (blocks US1 toggle, US4)

- [ ] T013 [P] Write failing test `internal/config/config_test.go`: `ValidateDetailed` rejects
  `tool_response_mode:"bogus"` with `Field:"tool_response_mode"`; accepts `""`/`full`/`compact`.
  (FR-001/FR-015)
- [ ] T014 Add the validation clause beside the `routing_mode` block (`config.go` ~1650) until
  T013 passes.
- [ ] T015 ⟲#1a Write failing test then add a `tool_response_mode` clause to `DetectConfigChanges`
  (`internal/runtime/config_hotreload.go`): when only that field differs, `ChangedFields` contains
  `"tool_response_mode"` (else an API apply of only this field is swallowed as "no changes").
- [ ] T016 ⟲#1b Implement `p.effectiveToolResponseMode(detail string) string` reading
  **`p.currentConfig()`** (`profile_resolver.go:38`, live snapshot) — empty⇒`full` — NOT the
  construction-time `p.config` the retrieve path uses at `mcp.go:1236`. Unit-test that a reloaded
  config changes the resolved mode without reconstructing the server.
- [ ] T017 [P] Add `MCPPROXY_TOOL_RESPONSE_MODE` explicit env alias in `internal/config/loader.go`
  (~570 pattern) and `--tool-response-mode` cobra flag in `cmd/mcpproxy`; test env/flag→config.

**Checkpoint**: compiler + owned cache + visibility resolver + entry-builder seam + config +
hot-reload wiring all green; full mode byte-identical. US1/US2 can start.

---

## Phase 3: User Story 1 — Compact discovery responses (P1) 🎯 MVP

**Goal**: compact-mode `retrieve_tools` returns signatures + first-sentence + lossy flag, no full
schema; ranking identical to full.
**Independent test**: golden query in compact mode ⇒ compact entries (no `inputSchema`), required
params marked, desc=first sentence; same query in full mode byte-identical to today.

- [ ] T018 [P] [US1] Write failing test `internal/server/mcp_entry_builder_test.go` (compact case):
  `buildToolEntry(result, compact)` ⇒ `{id, score, sig, desc, lossy}` only, no `inputSchema`/full
  `description`/`annotations`. (FR-002)
- [ ] T019 [P] [US1] ⟲#6 Write failing **full 47-query golden-set** ranked-ID identity test
  `internal/server/mcp_ranked_identity_test.go`: for **every** query in
  `specs/065-evaluation-foundation/datasets/retrieval_golden_v1.json`, the ordered `id` list is
  identical in `full` vs `compact`. This is a **release-blocking US1 test** (SC-002), NOT deferred
  to the US5 profiler. (FR-007/SC-002)
- [ ] T020 [P] [US1] Write failing never-elide test over the 45-tool frozen corpus
  (`specs/083-discovery-profiler/datasets/corpus_v2.tools.json`): every required param appears in
  every rendered `sig` with `*`. (FR-003/SC-004)
- [ ] T021 [US1] Implement the compact branch in `buildToolEntry` using `p.sigCache.Get(hash,
  paramsJSON, description)`; derive `id` from `ServerName`/`Name` exactly as full-mode `name`
  (never re-sort). Make T018–T020 pass. (FR-002/003/004/007)
- [ ] T022 [US1] Add effective-mode resolution: call `p.effectiveToolResponseMode(detail)` (T016)
  in `handleRetrieveToolsWithMode`; register the `detail` param (enum `compact|full`, no default)
  on the `retrieve_tools` definition in `mcp.go:691` and both `mcp_routing.go` builders. Test
  override both directions and unset⇒configured. (FR-005)
- [ ] T023 [US1] Add the deterministic compact `hint` top-level field (FR-009) when mode=compact;
  test its presence/absence and that it counts in the serialized size.
- [ ] T024 [US1] Warm `p.sigCache` from `runtime.applyDifferentialToolUpdate`
  (`internal/runtime/lifecycle.go` ~686/707 after `BatchIndexTools`, and the full-reindex branch
  ~560); extend the T008 compile-count test to assert a post-index compact `retrieve_tools` is a
  cache hit. (FR-008)

**Checkpoint**: US1 functional — compact responses, ranking identical (47/47 queries), required
never elided, cache proven warm.

---

## Phase 4: User Story 2 — describe_tool (P1)

**Goal**: batch ≤5 ids ⇒ full definitions (definition-field equal to full-mode), per-id errors,
same visibility as search.
**Independent test**: valid id ⇒ definition fields identical to full-mode entry; unknown ids ⇒
per-id errors without failing the batch; >5 ⇒ limit error.

- [ ] T025 [P] [US2] ⟲#2 Write failing test `internal/server/mcp_describe_tool_test.go`:
  valid id ⇒ definition **field-equal** to the full-mode `retrieve_tools` entry over
  `{name, description, inputSchema, server, annotations, call_with}` (compare after `delete(entry,
  "score")`); assert the definition carries **no** `score` key. Mixed valid/unknown ⇒ definitions +
  per-id errors, overall success; 6 ids ⇒ limit error, no processing. (FR-010)
- [ ] T026 [P] [US2] Write failing visibility-parity test reusing `p.toolVisibleToSession` (T010):
  agent-token session scoped to server A ⇒ `describe_tool(["B:x"])` returns an error, never a
  definition; quarantined/pending/disabled id ⇒ per-id error — asserted against the **same**
  predicate `retrieve_tools` uses. (FR-011, Constitution IV)
- [ ] T027 [US2] Implement `internal/server/mcp_describe_tool.go` handler: validate 1–5 ids; per id
  run `p.toolVisibleToSession` → on visible, `indexManager.GetToolsByServer` filtered to the tool →
  `buildToolEntry(..., full)` with `score` stripped; per-id errors with remediation codes. Make
  T025/T026 pass. (FR-010/011/012)
- [ ] T028 [US2] Register `describe_tool` in the retrieve_tools routing mode only: default server
  `registerTools` (`mcp.go:689`) and `buildCallToolModeTools` (`mcp_routing.go:354`). Assert (test)
  present there and **absent** from code_execution (`buildCodeExecModeTools`) and direct mode.
  ⟲#8 Assert its definition ≤150 tokens counted with **tiktoken `cl100k_base`** (the bench's pinned
  encoder, so budget and profiler agree). (FR-011)
- [ ] T029 [US2] Test FR-012: identical `describe_tool` output under `full` and `compact` mode.

**Checkpoint**: US1 + US2 — compact discovery with working second stage.

---

## Phase 5: User Story 3 — Self-healing failed calls (P2, independently shippable)

**Goal**: `call_tool_*` argument failures embed the failing tool's full schema + hint;
non-argument failures do not.
**Independent test**: omit a required param ⇒ error includes full schema + hint, upstream not
called; retry with corrected args succeeds.

- [ ] T030 [P] [US3] Write failing test `internal/server/mcp_input_validation_test.go`: missing
  required arg ⇒ `invalid_params` error with full `input_schema` + `hint`, upstream stub records
  **zero** invocations; uncompilable schema ⇒ fail-open (dispatch proceeds, `validation_skipped`
  counted). (FR-013/FR-013b)
- [ ] T031 [P] [US3] Write failing test that non-argument failures (simulated 401/timeout/5xx via
  stub) keep the existing `createDetailedErrorResponse` shape with **no** `input_schema`.
  (FR-013 scenario 2)
- [ ] T032 [US3] Implement `internal/server/mcp_input_validation.go`: `validateArgs(paramsJSON,
  args)` using `github.com/santhosh-tekuri/jsonschema/v6` (already a direct dep), memoized compiled
  schema by tool hash; fail-open on compile error. Make T030 pass.
- [ ] T033 [US3] Wire pre-dispatch validation into `handleCallToolVariant` after args-parse +
  `p.toolVisibleToSession`/callability (`mcp.go` ~1747, before `CallTool` ~1955): on failure return
  the self-healing error and skip dispatch. Schema source = the tool's `ParamsJSON` from the index
  (same source signatures use). (FR-013)
- [ ] T034 [US3] Extend `createDetailedErrorResponse` (`mcp.go:4767`) to classify upstream
  InvalidParams (JSON-RPC `-32602` / best-effort) and attach `input_schema`+`hint` (Path B); leave
  transport/auth/timeout shapes untouched. Make T031 pass. (FR-013)
- [ ] T035 [US3] E2E in `internal/server/e2e_test.go`: invalid call → schema-informed retry →
  success, in both full and compact mode (identical error). (SC-006, US3 scenario 3)

**Checkpoint**: US3 — bounded, self-correcting failure path; zero happy-path cost.

---

## Phase 6: User Story 4 — Rollout control and stability (P2)

**Goal**: default full = today; one-line flip to compact via hot-reload, no restart, no renames.
**Independent test**: toggle `tool_response_mode` on a running proxy ⇒ next-call shape changes, no
restart; default (unset) behaves as full.

- [ ] T036 [P] [US4] Write failing menu-surface test: tools/list in retrieve_tools mode differs
  from a pre-feature snapshot by **exactly** `describe_tool` added + `detail` param added +
  updated `call_tool_*`/`retrieve_tools` descriptions; all existing `retrieve_tools` params
  (limit, include_stats, debug, read_only_only, exclude_destructive, include_disabled, …)
  preserved. (FR-014/SC-003/SC-007)
- [ ] T037 [US4] Update `call_tool_*` descriptions (`buildCallToolVariantTool`, `mcp.go` ~672 and
  the per-variant text ~614–658) and `retrieve_tools` descriptions (`mcp.go:692`,
  `mcp_routing.go:358`) to reference signatures + `describe_tool` instead of "read inputSchema from
  retrieve_tools". Make T036 pass. (FR-014)
- [ ] T038 [US4] E2E mode-toggle test (`e2e_test.go`): on a running proxy, flip full→compact
  **(a)** via the config-file reload path **and (b)** via an API apply that changes only
  `tool_response_mode`; assert the next `retrieve_tools` returns compact with no restart in both;
  unset ⇒ full. (Exercises the T015 DetectConfigChanges clause + T016 currentConfig read.)
  (FR-015/SC-007)
- [ ] T039 [P] [US4] Docs: `docs/configuration.md` (`tool_response_mode` + env + flag),
  `docs/api/rest-api.md`/MCP section (`describe_tool`, `detail`), and the CLAUDE.md built-in-tools
  line. (Constitution VI)

**Checkpoint**: US4 — safe, restart-free rollout (both reload paths); frozen surface + one addition.

---

## Phase 7: User Story 5 — Profiler gates (P3)

**Goal**: spec-083 profiler measures compact mode and emits the flip-gate metrics.
**Independent test**: profiler live run against a compact-mode proxy emits the gate metrics.

- [ ] T040 [US5] ⟲#7 **Sequenced across branches** (research.md R1): `internal/toolsig` ships on
  the `085` branch, but `bench/arms/compact.go` lives only on `083-discovery-profiler` (PR #851)
  and is absent from the `085` tree today. Do this **only after 083 merges to main and `085` is
  rebased on main** (which brings `bench/arms/` in): migrate `bench/arms/compact.go` to import
  `internal/toolsig`, regenerate `bench/arms/testdata/compact_golden.txt` from the shared grammar,
  and update the arm's parity/contract tests. Until then this task is BLOCKED — do not fabricate
  `bench/arms/` on 085. (FR-019 sharing)
- [ ] T041 [US5] Add/confirm the profiler **live** compact arm/flag (FR-017) measuring real
  compact `retrieve_tools` responses with the same pipeline as full (token distributions, component
  breakdown, break-even) in `bench/live*.go` (present in the 085 tree — no 083 dependency).
- [ ] T042 [US5] Emit the flip-gate metrics (FR-018) in the report: per-query ranked-ID identity
  across modes (gate 100% — reuses the T019 corpus), lossy-signature rate on the 45-tool frozen
  corpus (gate <20%), response-token reduction, describe_tool usage counts from the E2E suite
  (informational).
- [ ] T043 [US5] Re-baseline (after T040/T041): run `make bench-discovery` + the live arm; record
  the *measured* compact reduction (SC-001 ≥50% median) and note it **supersedes** the prior
  −52.6%/−92% bench-grammar figures (those used full descriptions; the production grammar truncates
  to first sentence). (SC-001/SC-005, research.md R1)

**Checkpoint**: gates measurable; Phase-2 default-flip decision is now evidence-backed.

---

## Phase 8: Polish & Cross-Cutting

- [ ] T044 [P] Add `oas/swagger.yaml` entries if any REST surface exposes the new fields/tool.
- [ ] T045 [P] Run `quickstart.md` end-to-end as a manual/scripted smoke; fix drift.
- [ ] T046 Full gate: `go test -race ./internal/...`, `./scripts/test-api-e2e.sh`,
  `/opt/homebrew/bin/golangci-lint run --config .github/.golangci.yml ./...`, and
  `go test -tags server ./internal/serveredition/...` (edition-agnostic sanity).

---

## Dependencies & Execution Order

- **Setup (T001–T002)**: immediate.
- **Foundational (T003–T017)**: blocks US1 and US2. Parallel tracks: signature compiler
  (T003–T007) → cache owner (T008); visibility resolver (T009–T010); entry-builder (T011–T012);
  config+reload (T013–T017). T008 depends on T007; T024 (US1) depends on T008.
- **US1 (T018–T024)**: needs compiler + owned cache + entry-builder seam + config/reload +
  effective-mode helper. T019 (47-query identity) is release-blocking within US1.
- **US2 (T025–T029)**: needs the entry-builder seam + the **shared visibility resolver** (T010).
- **US3 (T030–T035)**: needs Setup + the index schema lookup + `p.toolVisibleToSession` for the
  callability check — **can ship first** (design Phase 0; mode-independent).
- **US4 (T036–T039)**: needs US1 (compact path) + T015/T016 for the toggle E2E.
- **US5 (T040–T043)**: T041/T042 need US1 (live compact serialization); **T040/T043 are gated on
  083 (PR #851) merging + the 085 rebase** (finding 7).
- **Polish (T044–T046)**: after desired stories.

### Parallel opportunities
- T001, T002 together.
- T003/T004/T006 (compiler tests) with T013 (config test) — different files.
- T009 (visibility test) parallel with the compiler track.
- T018/T019/T020 (US1 tests) together; T025/T026 (US2 tests) together; T030/T031 (US3 tests)
  together.

### Suggested incremental delivery
1. Setup + Foundational → full mode byte-identical, compiler + owned cache + resolver ready.
2. **US3 first** (self-healing, mode-independent) → ship.
3. US1 (compact, opt-in default full; 47/47 ranked-ID identity gate green) → ship.
4. US2 (describe_tool) → ship (completes progressive disclosure).
5. US4 (rollout polish + FR-014 descriptions + both-path toggle E2E) → ship.
6. US5 (profiler gates) — after 083 merges/rebase → authorizes the separate Phase-2 default flip
   (out of this release, FR-016).

---

## Requirements & Success-Criteria coverage (verification)

| Item | Task(s) |
|---|---|
| FR-001 tool_response_mode config, hot-reload, serialization-only | T002, T014, T015, T016, T022 |
| FR-002 compact entry fields, no schema | T018, T021 |
| FR-003 required marked + never elided (incl. missing-type E8) | T003, T005, T020, T021 |
| FR-004 lossy collapse + flag | T003, T005, T021 |
| FR-005 per-call `detail` override | T022 |
| FR-006 full-mode byte-identical payloads | T011, T012 |
| FR-007 ranked-ID identity across modes | T019, T021 |
| FR-008 index-time hash-keyed cache, single owner, no per-request compile | T006, T007, T008, T024 |
| FR-009 compact hint line | T023 |
| FR-010 describe_tool ≤5, per-id errors, definition-field equal (no score) | T025, T027 |
| FR-011 describe_tool shared-resolver visibility + retrieve mode only + ≤150 tok | T009, T010, T026, T027, T028 |
| FR-012 describe_tool mode-independent | T029 |
| FR-013 pre-dispatch validation + schema-on-arg-error, not on transport | T030, T031, T032, T033, T034 |
| FR-013b fail-open validation | T030, T032 |
| FR-014 no renames; call_tool_*/retrieve_tools descriptions updated | T036, T037 |
| FR-015 hot-reload apply (both paths), invalid value fails validation | T013, T014, T015, T016, T038 |
| FR-016 Phase-1 default full; flip is separate | T002, T014 (default), out-of-release note |
| FR-017 profiler compact arm (live) | T041 |
| FR-018 flip-gate metrics emitted | T042 |
| FR-019 deterministic + shared signature compiler | T003, T005, T040 |
| SC-001 ≥50% median token reduction (measured) | T043 |
| SC-002 100% ranked-ID identity (release-blocking in US1) | T019, T042 |
| SC-003 byte-level full-mode regression + exact surface delta | T011, T036 |
| SC-004 required in 100% of signatures | T020 |
| SC-005 lossy rate <20% | T042, T043 |
| SC-006 one retry succeeds; zero happy-path token cost | T035 |
| SC-007 toggle within one hot-reload cycle; exactly one new tool | T036, T038 |

**Unsatisfiable / flagged items**: none unsatisfiable. Two sequencing/measurement notes:
(1) SC-001's prior −52.6%/−92% figures were measured against the *bench* grammar (full
descriptions); the production first-sentence grammar requires re-measurement (T043) — SC-001 is
profiler-measured, so this is a re-baseline, not a conflict. (2) T040/T043 cannot execute until
083 (PR #851) merges and 085 is rebased (finding 7); US1–US4 do not depend on them.
