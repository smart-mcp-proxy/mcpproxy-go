# Tasks: Output-Schema Validation for Proxied Tool Calls

**Input**: Design documents from `/specs/056-output-schema-validation/`
**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/validator.md, quickstart.md

**Tests**: TDD is mandatory (Constitution V + repo CLAUDE.md). Every implementation task is preceded by a failing test.

**Organization**: Grouped by user story (US1 P1, US2 P2, US3 P3). US1 is the standalone MVP.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Parallelizable (different files, no dependency on an incomplete task) — suitable to fan out to a subagent.
- Paths are repo-root-relative.

---

## Phase 1: Setup

- [x] T001 Promote `github.com/santhosh-tekuri/jsonschema/v6` from indirect to a direct require in `go.mod` (move the line out of the indirect block; run `go mod tidy` and confirm `go build ./...` still compiles). Capture the exact version (v6.0.2).
- [x] T002 Create the empty package skeleton `internal/outputvalidation/doc.go` with a package comment describing the pure-validator contract (no server/storage imports allowed).

---

## Phase 2: Foundational (blocking prerequisites)

These unblock every story. Config + metadata field must exist before wiring.

- [x] T003 [P] Add failing test `internal/config/config_test.go` (or extend existing) `TestDefaultOutputValidationConfig` asserting defaults `{Mode:"warn", MaxBytes:5<<20, MaxDepth:64, MissingStructuredContent:"allow"}` and helper behaviour (`IsEnabled` false only for "off"; `IsStrict`; nil pointer ⇒ defaults).
- [x] T004 Implement `OutputValidationConfig` struct, root-config field `OutputValidation *OutputValidationConfig` (json `output_validation,omitempty`), `DefaultOutputValidationConfig()`, and helpers in `internal/config/config.go`; make T003 pass. Wire the default into config load/normalisation alongside `SensitiveDataDetection`.
- [x] T005 [P] Add `OutputSchemaJSON string` field (json `output_schema_json,omitempty`) to `ToolMetadata` in `internal/config/config.go`.

---

## Phase 3: User Story 1 — Structured output validated against its declared schema (Priority P1) 🎯 MVP

**Goal**: A tool with a declared `outputSchema` returning a non-conforming `structuredContent` is blocked (strict) or forwarded+audited (warn); conforming output passes through byte-identical; no schema ⇒ no-op.

**Independent Test**: `go test ./internal/outputvalidation/...` + `go test ./internal/server/ -run ContentForward` + the E2E stub-tool scenario all green; conforming `structuredContent` is byte-identical.

### Tests first (red) — fan out [P]

- [x] T006 [P] [US1] Write failing `internal/outputvalidation/validator_test.go`: table tests for (a) empty schema ⇒ Pass; (b) nil structured ⇒ Pass; (c) conforming structured ⇒ Pass; (d) violating structured ⇒ Violate with non-empty Reason; (e) uncompilable schema ⇒ Pass + warn logged once; (f) cache reuse (second Validate with same key doesn't recompile — assert via a compile counter or timing hook).
- [x] T007 [P] [US1] Write failing `internal/server/content_forward_test.go`: (a) success path forwards `StructuredContent` byte-identical (deep-equal + no pointer mutation); (b) injected validator returning Violate in strict ⇒ caller-visible block signal; (c) Violate in warn ⇒ original payload forwarded, verdict surfaced; (d) `IsError` upstream result ⇒ validator not invoked.

### Implementation (green)

- [x] T008 [US1] Implement `Validator`, `New(...)`, `Validate(toolKey, schemaJSON, structured)`, `Verdict`/`Outcome` in `internal/outputvalidation/validator.go` per `contracts/validator.md`: per-tool compiled-schema `sync.Map` cache keyed by toolKey+schema-hash; uncompilable ⇒ sentinel + one-time warn; never mutate `structured`. Make T006 pass (guards stubbed/permissive for now — real guards land in US2).
- [x] T009 [US1] Add the validator hook to `forwardContentResult` in `internal/server/content_forward.go`: accept an optional validator + the captured schema + mode posture; on success forward unchanged; return a verdict to the caller. Make T007 pass. Keep the function signature change backward-compatible for the non-validating callers (or add a sibling wrapper).
- [x] T010 [US1] Capture the output schema at discovery: in `internal/upstream/core/client.go` (~line 284, beside the `ParamsJSON` marshal) populate `ToolMetadata.OutputSchemaJSON` from `tool.RawOutputSchema` (fallback: marshal `tool.OutputSchema`). Add/extend a client test asserting a tool with an output schema yields a non-empty `OutputSchemaJSON` and one without yields empty.
- [x] T011 [US1] Wire validation into `handleCallToolVariant` at both `forwardContentResult` call sites (`internal/server/mcp.go:1794` and `:2166`): look up the captured `OutputSchemaJSON` for `server:tool`, skip when `result.IsError` or `mode=off`, run the validator, and translate the verdict — strict ⇒ `emitActivityPolicyDecision(server,tool,sid,"blocked",reason)` + `mcp.NewToolResultError(...)`; warn ⇒ `emitActivityPolicyDecision(...,"warning",reason)` + forward unchanged. Hold a `*outputvalidation.Validator` on the server struct, constructed from config at startup.
- [x] T012 [US1] Integration test `internal/server/*_test.go` exercising T011: a fake upstream tool with an output schema returning conforming → forwarded; violating → blocked in strict (error result) and forwarded in warn; assert exactly one `policy_decision` activity record per failure with the tool/mode/reason.

**Checkpoint**: US1 delivers the MVP — schema validation end-to-end with strict/warn and audit records.

---

## Phase 4: User Story 2 — Oversized / pathological output bounded before validation (Priority P2)

**Goal**: byte-size and nesting-depth guards run before schema validation; a breach is a guard-violation verdict under the active mode.

**Independent Test**: `go test ./internal/outputvalidation/ -run Guard` green; oversized/deep payloads blocked in strict / tagged in warn before compilation.

- [x] T013 [P] [US2] Write failing `internal/outputvalidation/guards_test.go`: (a) payload over `max_bytes` ⇒ guard verdict `GuardHit="max_bytes"`, schema NOT compiled; (b) nesting deeper than `max_depth` ⇒ `GuardHit="max_depth"`; (c) within both ⇒ proceeds to schema validation; (d) depth walk handles arrays + objects + scalars without stack blowup.
- [x] T014 [US2] Implement `guards.go` (byte-size via one-time marshal, recursive depth walk with an explicit bound) and call guards first inside `Validate`; make T013 pass and ensure the US1 "cache not recompiled on guard breach" expectation holds.
- [x] T015 [US2] Extend the server integration test (T012) with a tool whose response trips each guard; assert guard-violation handling + activity record in both modes.

**Checkpoint**: proxy is protected from pathological structured payloads.

---

## Phase 5: User Story 3 — Operator can observe and tune validation (Priority P3)

**Goal**: `mode` off/warn/strict + `missing_structured_content` posture honoured end-to-end; failures discoverable via the activity CLI/API.

**Independent Test**: toggling `mode` changes behaviour as specified; `mcpproxy activity list`/`show` surface validation records with tool/mode/reason.

- [x] T016 [P] [US3] Test: `mode=off` ⇒ validator never invoked (no activity records, payload untouched) — add to server integration test.
- [x] T017 [US3] Implement the `missing_structured_content` posture in the T011 caller: declared-schema + nil `structuredContent` ⇒ no-op in warn; in strict, block iff posture=`block`. Add a test covering the ContextForge #4042 trap (declared schema, text-only response, warn ⇒ forwarded; strict+allow ⇒ forwarded; strict+block ⇒ blocked).
- [x] T018 [P] [US3] Test that a validation failure record is retrievable via `GET /api/v1/activity?type=policy_decision` and via `mcpproxy activity show <id>` with tool/mode/reason fields populated (reuse existing activity test harness).

**Checkpoint**: feature is configurable and auditable.

---

## Phase 6: E2E + Polish & Cross-Cutting

- [ ] T019 [US1] Extend `scripts/test-api-e2e.sh` (or add a sibling stub MCP server under `e2e/`/`scripts/`) with an upstream tool declaring an `outputSchema`; assert via curl: strict-mode block returns an error mentioning schema validation, warn-mode forwards, and a `policy_decision` activity record appears. This is the mandatory curl-based verification.
- [x] T020 [P] Run `go test -race ./internal/outputvalidation/... ./internal/config/... ./internal/server/...` and fix any race/flake.
- [x] T021 [P] Write `docs/features/output-schema-validation.md` (config block, modes table, activity queries) mirroring `quickstart.md`; add the REST/MCP behaviour note. Do NOT expand `CLAUDE.md` (40k char CI gate — at most one line if any).
- [x] T022 [P] Run `./scripts/run-linter.sh` (golangci-lint) and resolve all findings in touched files.
- [ ] T023 Update `oas/swagger.yaml` only if a new/changed REST field is exposed (validation surfaces via existing `/api/v1/activity`; likely no OAS change — confirm with `./scripts/verify-oas-coverage.sh`).
- [x] T024 Final `make build` (personal) + `go build -tags server ./cmd/mcpproxy` (server) to confirm both editions compile unaffected (FR-A12).

---

## Dependencies & Execution Order

- **Setup (T001–T002)** → blocks everything.
- **Foundational (T003–T005)** → blocks all stories (config + metadata field).
- **US1 (T006–T012)** → MVP; depends on Foundational. Within US1: T006/T007/T010 tests are [P]; T008 before T009 before T011 before T012.
- **US2 (T013–T015)** → depends on US1's validator (T008) and server wiring (T011).
- **US3 (T016–T018)** → depends on US1 wiring (T011); independent of US2.
- **Polish (T019–T024)** → after the stories it verifies; T020/T021/T022 are [P].

## Parallel / subagent fan-out opportunities

- **Test-writing wave** (after Foundational): T006, T007, T010-test in parallel subagents — different files, no shared state.
- **Docs + lint + race** (T020, T021, T022) run concurrently at the end.
- **US2 and US3** can proceed in parallel once US1 (T011) lands — they touch disjoint logic (guards vs. config posture/observability), coordinating only on the shared server integration test file (serialize edits to that one file).

## Implementation Strategy

1. **MVP = Phase 1 + 2 + US1 (T001–T012)** — shippable on its own: schema validation with strict/warn + audit.
2. Layer US2 (guards) then US3 (config posture + observability).
3. E2E + polish last; verify with curl (T019) per the mandate.
