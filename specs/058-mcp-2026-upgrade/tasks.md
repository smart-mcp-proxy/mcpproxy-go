# Tasks: MCP Protocol Upgrade to the 2026-07-28 Spec Revision

**Input**: Design documents from `/specs/058-mcp-2026-upgrade/`
**Prerequisites**: [plan.md](./plan.md), [spec.md](./spec.md) (amended 2026-09-03), [research.md](./research.md), [data-model.md](./data-model.md), [contracts/](./contracts/README.md)

**Tests**: REQUIRED. The project constitution mandates a failing test before implementation for every sub-task, so each phase leads with its test tasks.

**Organization**: grouped by user story. Two ordering constraints override raw priority and are non-negotiable:

1. **Phase 2 pins both protocol hops before anything else.** The library bump alone switches the upstream hop to `2026-07-28` (FR-027 as amended), so until both pins are in place every later test is running against an unintended wire version.
2. **US3 (stateless readiness) must fully land before US1 lifts the client-facing pin.** Lifting first would expose the cross-client cancellation collision and break `set_profile` for real users rather than for tests.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: parallelizable — different files, no dependency on an incomplete task
- **[Story]**: US1–US5 from spec.md

## Path Conventions

Single Go project. Backend paths are repo-relative: `internal/`, `cmd/`, `bench/`. No frontend work in this feature.

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: get the tree compiling on v1.0.0 with no behaviour change.

- [ ] T001 Bump the library to v1.0.0 in `go.mod`/`go.sum` via `go get github.com/mark3labs/mcp-go@v1.0.0 && go mod tidy`, and confirm no new module is added
- [ ] T002 [P] Swap `NewTestStreamableHTTPServer` to the new `servertest` package in `bench/mcpcall_test.go` (3 sites)
- [ ] T003 [P] Swap `NewTestStreamableHTTPServer` to `servertest` in `internal/server/mcp_routing_test.go` (5 sites)
- [ ] T004 [P] Swap `NewTestStreamableHTTPServer` to `servertest` in `internal/upstream/listtools_logging_test.go` (2 sites) and drop the orphaned `mcpserver` import
- [ ] T005 [P] Swap `NewTestStreamableHTTPServer` to `servertest` in `internal/upstream/manager_prompts_test.go` (2 sites)
- [ ] T006 [P] Swap `NewTestStreamableHTTPServer` to `servertest` in `internal/upstream/managed/prompts_test.go` (5 sites)
- [ ] T007 [P] Swap `NewTestStreamableHTTPServer` to `servertest` in `internal/upstream/core/prompts_test.go` (15 sites)
- [ ] T008 Verify `go build ./...`, `go build -tags server -o /dev/null ./cmd/mcpproxy`, `go vet ./...` and `go vet -tags server ./...` all exit 0

**Checkpoint**: tree compiles; `internal/server` fails only `TestProfile_SetProfileSessionScoped` and `TestProfile_SetProfileUnknown`.

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: make the bump provably inert on the wire, and build the harness that stops later tests from passing vacuously. **No user story may start before this phase completes.**

- [ ] T009 Write a failing wire-capture test asserting an unpinned client still negotiates `2025-11-25` against mcpproxy, in `internal/server/protocol_era_test.go`
- [ ] T010 Write a failing wire-capture test asserting mcpproxy's upstream `initialize` still requests `2025-11-25`, in `internal/upstream/core/protocol_pin_test.go`
- [ ] T011 Add a `clientFacingStreamableOptions()` helper applying `server.WithStreamableHTTPProtocolVersions(mcp.LegacyProtocolVersions()...)` as a constant, and route all five `NewStreamableHTTPServer` sites through it in `internal/server/server.go`
- [ ] T012 Pin the upstream-facing handshake to `mcp.LATEST_LEGACY_PROTOCOL_VERSION` in `internal/upstream/core/connection_lifecycle.go`
- [ ] T013 Build the `forEachEra` test helper that pins the client era explicitly on both sides, in `internal/server/era_harness_test.go`
- [ ] T014 Prove the harness bites: assert a deliberately mis-pinned era makes an era assertion fail, in `internal/server/era_harness_test.go`
- [ ] T015 [P] Write four failing hash-stability fixtures (draft-07 refs, plain schema, native `$defs`, refs without a `definitions` block) in `internal/hash/schema_ref_test.go`
- [ ] T016 [P] Canonicalize `#/definitions/` to `#/$defs/` inside `NormalizeJSON` in `internal/hash/hash.go`, widening its doc comment to say it normalizes schema refs as well as key order
- [ ] T017 [P] Write a failing test that an upstream result envelope is not copied wholesale, in `internal/server/content_forward_test.go`
- [ ] T018 [P] Copy result fields explicitly instead of assigning the embedded envelope in `internal/server/content_forward.go` and `internal/server/mcp_routing.go` (FR-016b)
- [ ] T019 Verify the three frozen tool-surface goldens are byte-identical, and record in the PR that they were checked rather than regenerated

**Checkpoint**: full suite green on both editions; negotiated version unchanged in both directions.

---

## Phase 3: User Story 3 — Stateless operation with per-identity curation (P1)

**Goal**: mcpproxy holds no connection state, while agent-token scoping and profile curation keep working.

**Independent test**: two differently-scoped agent tokens connect concurrently; each sees only its permitted tools; the canonical list facade is identical across connections; nothing relies on a session id.

- [ ] T020 [P] [US3] Write failing tests that `tools/list` is byte-identical across two connections in both eras, in `internal/server/stateless_list_test.go`
- [ ] T021 [P] [US3] Write a failing test that `prompts/list` does not change after `set_profile` — this fails on `main` today (FR-012) — in `internal/server/stateless_list_test.go`
- [ ] T022 [P] [US3] Write a failing test that a modern **stdio** request cannot persist a profile selection despite its non-empty session id, in `internal/server/profile_era_test.go`
- [ ] T023 [US3] Add `resolveActiveProfileForList` that skips the session tier, in `internal/server/profile_resolver.go`
- [ ] T024 [US3] Switch the two list filters to the list-only resolver in `internal/server/mcp_direct_scope.go`
- [ ] T025 [US3] Gate the session tier on `!server.IsModernRequest(ctx)` in addition to a non-empty id, in `internal/server/profile_resolver.go`
- [ ] T026 [US3] Validate the slug before the session guard, and return machine-actionable guidance naming the `/mcp/p/<slug>` form on modern requests, in `internal/server/profile_tool.go`
- [ ] T027 [US3] Split `TestProfile_SetProfileSessionScoped` into legacy and modern variants, keeping the original name for the legacy case so the spec's named gate still resolves, in `internal/server/profile_integration_test.go`
- [ ] T028 [US3] Fix `TestProfile_SetProfileUnknown` to assert the unknown-slug message in both eras, in `internal/server/profile_integration_test.go`
- [ ] T029 [P] [US3] Write a failing test that one client cannot cancel another's in-flight request when both use the same JSON-RPC id, in `internal/server/cancel_isolation_test.go`
- [ ] T030 [US3] Add the cross-client cancellation guard (R1) and file the upstream mcp-go issue, touching `internal/server/mcp.go`
- [ ] T031 [P] [US3] Write a failing `-tags server` test that two OAuth users with differing entitlements are isolated with no session, in `internal/serveredition/api/entitlement_stateless_test.go`
- [ ] T032 [P] [US3] Write a failing test that modern-era client name/version reach the activity record via `RequestProtocolInfoFromContext`, in `internal/server/modern_attribution_test.go`
- [ ] T033 [US3] Capture client identity from request protocol info in the `BeforeAny` hook so activation telemetry survives the loss of `AfterInitialize`, in `internal/server/mcp.go`
- [ ] T034 [P] [US3] Correct the false "stable session id" comment in `internal/server/profile_resolver.go` and the false "background cleanup calls RemoveSession" comment in `internal/server/session_store.go`
- [ ] T035 [P] [US3] Update the profile docs for era behaviour in `docs/features/profiles.md`
- [ ] T036 [US3] Decide and record whether per-session token stats become legacy-only or are re-keyed on request-carried identity, in `specs/058-mcp-2026-upgrade/research.md`

**Checkpoint**: US3 independently testable and complete. Only now may a pin lift.

---

## Phase 4: User Story 1 — Protocol negotiation without breakage (P1)

**Goal**: `2026-07-28` and `2025-11-25` clients both work against one instance.

**Independent test**: point a client of each era at the same instance; both discover and call tools; `server/discover` returns the supported set.

- [ ] T037 [P] [US1] Write failing `forEachEra` tests for tool discovery and calling in `internal/server/protocol_era_test.go`
- [ ] T038 [P] [US1] Write a failing test that `server/discover` advertises both versions once unpinned, in `internal/server/protocol_era_test.go`
- [ ] T039 [P] [US1] Write a failing test that an unsupported requested version yields `-32022`, in `internal/server/protocol_era_test.go`
- [ ] T040 [P] [US1] Write failing status-parity tests for `GET`/`DELETE` on the MCP endpoints in both eras, in `internal/server/protocol_era_test.go`
- [ ] T041 [US1] Lift the client-facing legacy pin in `internal/server/server.go`, leaving the helper in place for rollback
- [ ] T042 [US1] Re-verify the frozen goldens and the full suite after the lift, recording the evidence in the PR
- [ ] T043 [US1] Measure the `server/discover`-before-`initialize` cost against non-mcp-go upstreams (python and TypeScript SDK stdio servers, an SSE server) and record it in `specs/058-mcp-2026-upgrade/research.md`
- [ ] T044 [US1] Lift the upstream-facing pin in `internal/upstream/core/connection_lifecycle.go` once T043 shows no regression
- [ ] T045 [US1] Replace the now-vacuous modern-era health probe, since `Ping` is a no-op on modern connections, in `internal/upstream/managed/client.go`
- [ ] T046 [US1] Verify `tools/list_changed` still arrives from a modern upstream without an open listen stream, or add the stream, in `internal/upstream/manager.go`

**Checkpoint**: both eras served; both pins lifted with evidence.

---

## Phase 5: User Story 2 — Routing headers on every hop (P1)

**Goal**: required metadata headers are set, validated and forwarded.

**Independent test**: inspect an upstream-bound request for `Mcp-Method`, `Mcp-Name` and `MCP-Protocol-Version`; send a deliberate mismatch and confirm rejection.

- [ ] T047 [P] [US2] Write a failing wire-capture test enumerating every proxied method and asserting required headers on each, in `internal/upstream/core/headers_test.go`
- [ ] T048 [P] [US2] Write a failing test that a header/body version mismatch returns `-32020` and is not forwarded, in `internal/server/header_validation_test.go`
- [ ] T049 [US2] Ensure required headers are emitted on every forwarded request, per FR-007 as amended (requests only), in `internal/upstream/core/client.go`
- [ ] T050 [US2] Wire header/body mismatch validation on the inbound hop in `internal/server/mcp.go`
- [ ] T051 [P] [US2] Write failing tests for `x-mcp-header` param mirroring and for rejecting an invalid declaration, in `internal/upstream/core/param_headers_test.go`
- [ ] T052 [US2] Decide the FR-009/FR-010 validation layer among discovery, differential tool update, and the quarantine pipeline, and record the choice in `specs/058-mcp-2026-upgrade/research.md`
- [ ] T053 [US2] Implement `x-mcp-header` mirroring and invalid-declaration rejection at the chosen layer
- [ ] T054 [US2] Cover the malformed-Base64 rejection path for `Mcp-Param-*`, including retrieve_tools mode where built-ins carry no declaration, in `internal/upstream/core/param_headers_test.go`

**Checkpoint**: SC-002 and SC-003 demonstrable by wire capture.

---

## Phase 6: User Story 4 — Input-required detection (P2, amended scope)

**Goal**: an upstream `input_required` result is reported clearly instead of leaking or vanishing; unsolicited continuations are refused.

**Independent test**: drive a call against an upstream returning `input_required`; the client receives a structured notice, the activity log records it, and a forged continuation is rejected pre-dispatch.

- [ ] T055 [P] [US4] Write a failing test that an upstream `input_required` result produces a structured `isError` notice with no upstream free text, in `internal/server/input_required_test.go`
- [ ] T056 [P] [US4] Write a failing test that a continuation carrying `inputResponses`/`requestState` is rejected pre-dispatch on `call_tool_*`, the direct surface and legacy `call_tool`, in `internal/server/input_required_test.go`
- [ ] T057 [US4] Add an `input_required` activity status and a detection branch ahead of result-status classification, in `internal/server/activity_result_status.go`
- [ ] T058 [US4] Detect the result and emit the structured notice, keeping raw library errors at debug level only, in `internal/server/mcp.go`
- [ ] T059 [US4] Add the pre-dispatch continuation guard, scoped to the three call surfaces only, in `internal/server/mcp.go`
- [ ] T060 [P] [US4] Record the negotiated upstream era on client status so `doctor` and `upstream list` can show it, in `internal/upstream/core/client.go`
- [ ] T061 [P] [US4] File the two upstream mcp-go issues that gate FR-016a: an exported single-shot `CallTool` accepting MRTR params, and a typed input-required error
- [ ] T062 [US4] Note the detection's dependence on unexported library error text as a fragility, and prefer the exported sentinel where a round-trip cap makes it available, in `specs/058-mcp-2026-upgrade/research.md`

**Checkpoint**: no upstream input request is silently dropped or forwarded as mcpproxy's own.

---

## Phase 7: User Story 5 — Additive feature adoption (P2)

**Goal**: cache hints, deterministic ordering, modern schema defaults and trace correlation.

**Independent test**: inspect list/read responses for valid hints; compare repeated `tools/list` results for stable ordering; confirm trace ids reach activity records.

- [ ] T063 [P] [US5] Write failing tests that list/read responses carry `ttlMs` and an identity-appropriate `cacheScope`, in `internal/server/cache_hints_test.go`
- [ ] T064 [US5] Configure cache hints — private for identity-scoped surfaces, public for shared ones — in `internal/server/mcp_routing.go` and `internal/server/mcp.go`
- [ ] T065 [P] [US5] Write a failing test that repeated `tools/list` calls are byte-identical under unchanged state, in `internal/server/stateless_list_test.go`
- [ ] T066 [US5] Make tool ordering deterministic wherever map iteration currently leaks in, in `internal/server/mcp_routing.go`
- [ ] T067 [P] [US5] Write a failing test that external `$ref`s are not dereferenced and that schemas validate as JSON Schema 2020-12, in `internal/server/mcp_input_validation_test.go`
- [ ] T068 [US5] Install a rejecting loader so no external `$ref` is fetched, and confirm the dialect default, in `internal/server/mcp_input_validation.go`
- [ ] T069 [P] [US5] Write a failing test that `traceparent`/`tracestate`/`baggage` from request `_meta` reach the activity record, in `internal/runtime/trace_context_test.go`
- [ ] T070 [US5] Capture trace context into activity records via a single composite meta propagator, in `internal/server/mcp.go` and `internal/runtime/activity_service.go`
- [ ] T071 [P] [US5] Confirm sensitive-data detection does not scan persisted trace metadata, in `internal/runtime/activity_service.go`
- [ ] T072 [US5] Move per-request log level onto the `_meta` mechanism for modern clients, in `internal/server/mcp.go`

**Checkpoint**: SC-006 (hints present and valid), SC-007 and FR-020/021 demonstrable.

---

## Phase 8: Polish & Cross-Cutting Concerns

- [ ] T073 [P] Document both eras, the pin history and the stateless consequences in `docs/architecture.md`
- [ ] T074 [P] Update `CLAUDE.md`'s MCP Protocol section for the served versions and the amended requirements
- [ ] T075 [P] Add a modern-era correlation key for activity rows whose session id is empty, and check the Web UI grouping in `frontend/src/views/Activity.vue`
- [ ] T076 Address the pre-existing legacy `SessionStore` leak — no idle sweeper, no `RemoveSession` caller — or file it separately, in `internal/server/session_store.go`
- [ ] T077 Run the deprecation-window closeout as its own wire-format PR: rewrite the `set_profile` description honestly, drop the session tier, and regenerate the seven affected goldens
- [ ] T078 Cross-review the spec amendments and the implementation with the configured cross-model reviewer before merge
- [ ] T079 Full-suite verification: `go test -race ./internal/...`, `go test -tags server ./internal/serveredition/... -race`, `./scripts/test-api-e2e.sh`, and golangci-lint v2 with the CI config

---

## Dependencies & Execution Order

```text
Phase 1 (Setup) ──► Phase 2 (Foundational, both pins) ──► Phase 3 (US3)
                                                             │
                                        ┌────────────────────┘
                                        ▼
                              Phase 4 (US1, lifts pins)
                                        │
                          ┌─────────────┼─────────────┐
                          ▼             ▼             ▼
                    Phase 5 (US2)  Phase 6 (US4)  Phase 7 (US5)
                          └─────────────┼─────────────┘
                                        ▼
                                 Phase 8 (Polish)
```

- **Phase 2 blocks everything.** Until both hops are pinned, later tests run against an unintended wire version.
- **US3 blocks US1's pin lift** — not the reverse of what raw priority suggests. Lifting first would expose the cancellation collision to real users.
- **US2, US4, US5 are mutually independent** once both eras are served, and can proceed in parallel.
- T036, T043, T052 and T062 are decision-recording tasks; they gate the task that follows them, not the whole phase.

## Parallel Execution Examples

**Phase 1** — T002 through T007 are six disjoint test files; run them together after T001.

**Phase 2** — three independent tracks: the pins (T009→T012), the hash normalizer (T015→T016), and the result-envelope fix (T017→T018).

**Phase 3** — write T020, T021, T022, T029, T031, T032 together as a failing-test batch, then implement T023–T033 in dependency order. T034 and T035 are documentation and can run at any point.

**Phases 5–7** — one agent per story, given they touch disjoint files.

## Implementation Strategy

**MVP** is Phase 1 + Phase 2: the library is current, the wire is provably unchanged, and two latent defects (tool-hash drift, result-envelope copying) are fixed. That is shippable on its own and carries no protocol risk.

**Increment 2** is Phase 3, which pays down real FR-012 and FR-013 debt that exists on `main` today — independent of whether the new protocol is ever served.

**Increment 3** is Phase 4, the only point at which user-visible protocol behaviour changes, gated on measured evidence.

**Increments 4–6** are Phases 5–7 in any order.

## Task Count

| Phase | Tasks | Story |
|---|---|---|
| 1 Setup | 8 | — |
| 2 Foundational | 11 | — |
| 3 Stateless readiness | 17 | US3 (P1) |
| 4 Negotiation | 10 | US1 (P1) |
| 5 Routing headers | 8 | US2 (P1) |
| 6 Input-required detection | 8 | US4 (P2) |
| 7 Additive adoption | 10 | US5 (P2) |
| 8 Polish | 7 | — |
| **Total** | **79** | |
