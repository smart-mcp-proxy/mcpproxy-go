---
description: "Task list for spec 042 — Telemetry Tier 2"
---

# Tasks: Telemetry Tier 2 — Privacy-Respecting Usage Signals

**Input**: Design documents from `/specs/042-telemetry-tier2/`
**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/heartbeat-v2.schema.json, quickstart.md

**Tests**: REQUIRED. Per `CLAUDE.md` autonomous-mode constraint, every sub-task writes a failing Go test before implementation. Race detection (`-race`) is mandatory on counter and registry tests.

**Organization**: Tasks are grouped by user story so each can be implemented and verified independently. Sub-task target size: <2 hours each.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no inter-task dependencies)
- **[Story]**: Maps to a user story phase (e.g., US1, US10) — required for user-story phase tasks; absent for Setup, Foundational, and Polish phases.

## Path Conventions

Single Go module project. All Go source under `internal/` and `cmd/`. New tests live alongside implementation files (`*_test.go`). The macOS tray Swift change is in `native/macos/MCPProxy/`. The web UI change is in `frontend/src/`.

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Verify baseline and identify exact integration points before touching any code.

- [ ] T001 Read `internal/telemetry/telemetry.go` end-to-end and confirm the v1 `Service`, `HeartbeatPayload`, `Start()`, `send()`, and config fields match what `data-model.md` and `research.md` describe. Note any drift.
- [ ] T002 Read `internal/httpapi/middleware.go` and `internal/httpapi/server.go` to identify the existing middleware chain and confirm the Chi router is `chi.Router`. Note the file/line where new middleware should be wired.
- [ ] T003 Read `internal/server/mcp.go` to find the entry point of every built-in MCP tool handler (`handleRetrieveTools`, the call_tool variants, `upstream_servers`, `quarantine_security`, `code_execution`). List file:line for each.
- [ ] T004 Read `internal/cliclient/client.go` (or wherever the CLI HTTP client is defined) to find the HTTP request construction site. Locate where to inject the `X-MCPProxy-Client` header.
- [ ] T005 Locate the macOS tray's Swift HTTP request builder under `native/macos/MCPProxy/` (grep for `URLRequest` and `URLSession.shared`). Record the file path.
- [ ] T006 Locate the web UI fetch wrapper under `frontend/src/api/` (grep for `fetch(` and existing `X-API-Key` injection). Record the file path.
- [ ] T007 Confirm baseline: run `go test ./internal/telemetry/... -race` and `go build ./cmd/mcpproxy` from the worktree root. Both must pass before any code change.

**Checkpoint**: Integration points are documented; baseline is green.

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Build the shared `CounterRegistry`, error category enum, env override logic, and extended config struct that every user story will depend on.

**⚠️ CRITICAL**: No user story phase can begin until this phase is complete.

- [ ] T008 Add new fields (`AnonymousIDCreatedAt`, `LastReportedVersion`, `LastStartupOutcome`, `NoticeShown`) to the `Telemetry` config struct in `internal/config/config.go`. JSON tags as specified in `data-model.md`. Ensure existing v1 fields are unchanged.
- [ ] T009 [P] Write failing test `internal/config/config_test.go::TestTelemetryConfigTier2Roundtrip`: marshal a config with all new fields populated, unmarshal into a fresh struct, assert all values are preserved. Then implement (T008 should make it pass).
- [ ] T010 Create `internal/telemetry/error_categories.go` with the `ErrorCategory` typed string, the eleven `ErrCat*` constants, and `validErrorCategories` map exactly as described in `data-model.md`.
- [ ] T011 [P] Write failing test `internal/telemetry/error_categories_test.go::TestValidErrorCategoriesEnum`: assert every constant is in `validErrorCategories` and that the map size equals 11. Then run; should pass.
- [ ] T012 Create `internal/telemetry/registry.go` with the `Surface` enum (mcp/cli/webui/tray/unknown), the `CounterRegistry` struct (5 atomic surface counters, atomic upstream total, RWMutex-protected built-in/REST/error/doctor maps), and a `NewCounterRegistry()` constructor. No methods yet.
- [ ] T013 [P] Write failing test `internal/telemetry/registry_test.go::TestNewCounterRegistry`: assert all counters start at zero and `Snapshot()` returns a non-nil zero-valued snapshot. Then implement `Snapshot()` skeleton and `Reset()` skeleton in `registry.go` to make it pass.
- [ ] T014 Implement `RecordSurface(s Surface)` on `CounterRegistry` using `atomic.Int64.Add(1)`. Test in `registry_test.go::TestRecordSurfaceConcurrent` — fire 1000 goroutines × 100 increments under `-race`, assert final count is 100000 per surface.
- [ ] T015 Implement `RecordUpstreamTool()` and the bucket-derivation helper `bucketUpstream(n int64) string`. Test in `registry_test.go::TestUpstreamBucketBoundaries` — table test for 0, 1, 10, 11, 100, 101, 1000, 1001 → expected bucket strings.
- [ ] T016 Implement `RecordBuiltinTool(name string)` with the fixed enum allow-list. Unknown names are silently dropped. Test in `registry_test.go::TestRecordBuiltinToolKnownAndUnknown` — assert known names increment, unknown names are no-ops, race-safe.
- [ ] T017 Implement `RecordRESTRequest(method, template, statusClass string)`. Builds nested map keys atomically under the registry lock. Test in `registry_test.go::TestRecordRESTRequestNestedMap` — covers new key, existing key, race safety.
- [ ] T018 Implement `RecordError(category ErrorCategory)` with allow-list check against `validErrorCategories`. Unknown categories are silently dropped. Test in `registry_test.go::TestRecordErrorRejectsUnknown`.
- [ ] T019 Implement `RecordDoctorRun(results []doctor.CheckResult)`. Iterates results, increments pass or fail per check name. Define a minimal local `CheckResult` interface in registry.go to avoid an import cycle if needed; document it. Test in `registry_test.go::TestRecordDoctorRun`.
- [ ] T020 Implement `Snapshot()` in full: returns a `RegistrySnapshot` struct (defined in registry.go) containing the surface map, builtin map, upstream bucket string, REST map (deep-copied), error map (deep-copied), doctor map (deep-copied). Snapshot must NOT mutate the registry. Test `registry_test.go::TestSnapshotDoesNotResetCounters` and `TestSnapshotIsImmutable` (mutating the snapshot does not affect future snapshots).
- [ ] T021 Implement `Reset()` in full: zeros all atomics and clears all maps. Test `registry_test.go::TestResetClearsAll`.
- [ ] T022 Create `internal/telemetry/env_overrides.go` with `IsDisabledByEnv() (bool, string)` returning `(true, "DO_NOT_TRACK")`, `(true, "CI")`, `(true, "MCPPROXY_TELEMETRY=false")`, or `(false, "")`. Precedence per `research.md` R7. Test `internal/telemetry/env_overrides_test.go::TestEnvOverridePrecedence` — table of env-var combinations → expected outcomes. Use `t.Setenv` for hermetic tests.
- [ ] T023 Wire `IsDisabledByEnv()` into `telemetry.NewService(cfg, logger)` so the env check happens once at construction. If disabled by env, the service stores `enabled=false` and the heartbeat goroutine in `Start()` exits immediately. Add test `telemetry_test.go::TestNewServiceDisabledByDoNotTrack`.
- [ ] T024 Extend `HeartbeatPayload` struct in `internal/telemetry/telemetry.go` with all Tier 2 fields from `data-model.md` (schema_version, surface_requests, builtin_tool_calls, upstream_tool_call_count_bucket, rest_endpoint_calls, feature_flags, last_startup_outcome, previous_version, current_version, error_category_counts, doctor_checks, anonymous_id_created_at). All fields use `omitempty` where appropriate per the spec. Add `schema_version: 2` as a constant. Test `telemetry_test.go::TestHeartbeatPayloadV2Marshal` — marshal a populated struct, assert all expected JSON keys are present.
- [ ] T025 Add a `Registry() *CounterRegistry` accessor on `Service` so callers can record events without depending on the internal field.

**Checkpoint**: `go test ./internal/telemetry/... -race` passes. The registry, env overrides, and extended payload struct all exist. No integration points are wired yet.

---

## Phase 3: User Story 1 — Surface Tracking (Priority: P1) 🎯 MVP

**Goal**: Every incoming request to the HTTP server is classified into one of `mcp`, `cli`, `webui`, `tray`, `unknown` and counted.

**Independent Test**: Make one request per surface from each client; run `mcpproxy telemetry show-payload` (after Phase 12 lands the command, or assert via `Snapshot()` directly in tests); confirm each counter is correctly incremented.

### Tests for User Story 1 ⚠️

> Write these tests FIRST. They MUST FAIL before T031–T034 are written.

- [ ] T026 [P] [US1] Write failing test `internal/httpapi/middleware_test.go::TestSurfaceClassifierFromHeader` — table of `X-MCPProxy-Client` header values vs expected `Surface` enum, including missing header → `unknown`, malformed → `unknown`, `tray/v0.21.0` → `SurfaceTray`.
- [ ] T027 [P] [US1] Write failing test `internal/httpapi/middleware_test.go::TestSurfaceMiddlewareIncrementsRegistry` — wire a fake registry, send a request through the middleware, assert the right counter went up.
- [ ] T028 [P] [US1] Write failing test `internal/server/mcp_test.go::TestMCPRequestCountedAsSurfaceMCP` — invoke a built-in MCP handler, assert `registry.surfaceCounts[SurfaceMCP]` is 1 regardless of any header.

### Implementation for User Story 1

- [ ] T029 [US1] Add `parseClientSurface(header string) Surface` helper function in `internal/telemetry/registry.go` (or a sibling file `surface_parser.go`). Splits on `/`, lowercases, maps `tray/cli/webui` → enum, everything else → `SurfaceUnknown`. Tests T026 should pass after this.
- [ ] T030 [US1] Add `SurfaceClassifier(reg *CounterRegistry) func(http.Handler) http.Handler` middleware in `internal/httpapi/middleware.go`. Reads `X-MCPProxy-Client`, classifies, calls `reg.RecordSurface(...)`. Wire it into the `/api/v1` router. Tests T026, T027 should pass.
- [ ] T031 [US1] Wire the surface counter into `internal/server/mcp.go` at the entry of every MCP request. Add `s.telemetryRegistry.RecordSurface(telemetry.SurfaceMCP)` once per inbound MCP request. Test T028 should pass.
- [ ] T032 [US1] In `internal/cliclient/client.go`, add a transport wrapper that sets `X-MCPProxy-Client: cli/<version>` on every request. Get version from the existing `version.AppVersion` constant (or whatever the project uses). Test `internal/cliclient/client_test.go::TestCLIClientSetsHeader` — start an `httptest.Server`, make a request, assert header value.
- [ ] T033 [US1] In the web UI fetch wrapper located in T006, add `'X-MCPProxy-Client': 'webui/<APP_VERSION>'` to the headers object. Build version comes from the existing Vite define. No frontend test required (visual check via build).
- [ ] T034 [US1] In the macOS tray Swift file located in T005, add `request.setValue("tray/\(Bundle.main.shortVersionString)", forHTTPHeaderField: "X-MCPProxy-Client")` to the central URLRequest builder. No new Swift test required.

**Checkpoint**: User Story 1 is independently functional. `Snapshot()` reflects accurate per-surface counts under `-race`.

---

## Phase 4: User Story 2 — Built-in Tool Histogram (Priority: P1)

**Goal**: Daily heartbeat reports counts of each built-in tool call and a bucketed total of upstream tool calls. Upstream tool *names* never appear in the payload.

**Independent Test**: Call `retrieve_tools` 3×, `code_execution` 1×, an upstream tool 15×; assert payload reflects the histogram and the upstream bucket = `"11-100"`.

### Tests for User Story 2 ⚠️

- [ ] T035 [P] [US2] Write failing test `internal/server/mcp_test.go::TestBuiltinToolCounterIncrementsPerCall` — call each built-in handler once, assert each counter is exactly 1.
- [ ] T036 [P] [US2] Write failing test `internal/server/mcp_test.go::TestUpstreamToolCallIncrementsBucket` — invoke a fake upstream proxied call 15 times, assert `Snapshot().UpstreamToolCallCountBucket == "11-100"`.
- [ ] T037 [P] [US2] Write failing test `internal/server/mcp_test.go::TestUpstreamToolNameNotInSnapshot` — call upstream tool with deliberately distinctive name `"my-canary-server:do-secret-thing"`, render `Snapshot()`, marshal to JSON, assert the JSON does not contain `canary` or `secret`.

### Implementation for User Story 2

- [ ] T038 [US2] In `internal/server/mcp.go`, add `s.telemetryRegistry.RecordBuiltinTool("retrieve_tools")` at the entry of `handleRetrieveTools`. Repeat for each built-in handler (`call_tool_read`, `call_tool_write`, `call_tool_destructive`, `upstream_servers`, `quarantine_security`, `code_execution`). Tests T035 should pass.
- [ ] T039 [US2] In the upstream proxy code path of `handleCallToolVariant` (the branch that forwards to a non-builtin tool), call `s.telemetryRegistry.RecordUpstreamTool()` exactly once before any error returns. Tests T036, T037 should pass.

**Checkpoint**: Built-in histogram and upstream bucket are wired correctly. Privacy assertion holds.

---

## Phase 5: User Story 3 — REST API Endpoint Histogram (Priority: P2)

**Goal**: Daily heartbeat reports per-endpoint request counts using Chi route templates and status code classes; raw paths never appear.

**Independent Test**: Make requests to `GET /api/v1/servers`, `POST /api/v1/servers/anything/enable`, `GET /api/v1/nonexistent`. Assert templated keys + `UNMATCHED`, no occurrence of `anything`.

### Tests for User Story 3 ⚠️

- [ ] T040 [P] [US3] Write failing test `internal/httpapi/middleware_test.go::TestRESTEndpointMiddlewareTemplatedKey` — invoke a request that matches `POST /api/v1/servers/{name}/enable`, assert the snapshot has the templated key and not the raw path.
- [ ] T041 [P] [US3] Write failing test `internal/httpapi/middleware_test.go::TestRESTEndpointMiddlewareUnmatched` — invoke a request to `/api/v1/does-not-exist`, assert the counter under `UNMATCHED.4xx` is 1 and the raw path does not appear in the snapshot.
- [ ] T042 [P] [US3] Write failing test `internal/httpapi/middleware_test.go::TestStatusClassDerivation` — table test of status codes 200, 204, 301, 404, 500 → `2xx`, `2xx`, `3xx`, `4xx`, `5xx`.

### Implementation for User Story 3

- [ ] T043 [US3] Extend the existing response-writer wrapper in `internal/httpapi/middleware.go` (or add one) so it captures the status code. If a wrapper already exists for the request-id middleware, reuse it.
- [ ] T044 [US3] Add `EndpointHistogramMiddleware(reg *CounterRegistry) func(http.Handler) http.Handler` in `internal/httpapi/middleware.go`. After the next handler returns, fetches `chi.RouteContext(r.Context()).RoutePattern()`. If empty → `UNMATCHED`. Otherwise builds the key `"<METHOD> <pattern>"` and calls `reg.RecordRESTRequest(method, pattern, statusClass)`. Wire into the `/api/v1` router AFTER the surface classifier middleware.
- [ ] T045 [US3] Wire the registry into the httpapi server constructor so middlewares can reach it (probably via a closure or a struct field). Adjust the existing wiring as needed.

**Checkpoint**: Endpoint histogram works for matched and unmatched paths. Privacy assertion holds.

---

## Phase 6: User Story 4 — Feature-Flag Adoption (Priority: P2)

**Goal**: Heartbeat snapshots feature-flag values from the active config, including OAuth provider type list (no client IDs/URLs).

**Independent Test**: Set known config booleans + two OAuth servers, render `Snapshot()`, assert `feature_flags` matches.

### Tests for User Story 4 ⚠️

- [ ] T046 [P] [US4] Write failing test `internal/telemetry/feature_flags_test.go::TestFeatureFlagSnapshotFromConfig` — table test of config inputs → expected `FeatureFlagSnapshot` outputs, including a server with Google OAuth, GitHub OAuth, and a custom OIDC provider.
- [ ] T047 [P] [US4] Write failing test `internal/telemetry/feature_flags_test.go::TestOAuthProviderTypeClassificationGenericFallback` — server with `https://login.example.com/oauth` → `generic`.

### Implementation for User Story 4

- [ ] T048 [US4] Create `internal/telemetry/feature_flags.go` with `BuildFeatureFlagSnapshot(cfg *config.Config) FeatureFlagSnapshot`. Maps known OAuth host patterns to enum values, falls back to `generic`. Sorts and dedupes the result list. Tests T046, T047 should pass.
- [ ] T049 [US4] Wire `BuildFeatureFlagSnapshot` into the heartbeat render path in `telemetry.go` so the result lands in `payload.FeatureFlags`.

**Checkpoint**: Feature flags are reported correctly with no leakage of OAuth client IDs.

---

## Phase 7: User Story 5 — Startup Outcome (Priority: P2)

**Goal**: Persist the last serve startup outcome in config; report it in the next heartbeat.

**Independent Test**: Start cleanly, observe `last_startup_outcome="success"`. Cause a port conflict, observe `"port_conflict"`.

### Tests for User Story 5 ⚠️

- [ ] T050 [P] [US5] Write failing test `cmd/mcpproxy/serve_test.go::TestRecordStartupOutcomeMapping` — table test of exit codes 0, 2, 3, 4, 5, 99 → expected outcome strings.

### Implementation for User Story 5

- [ ] T051 [US5] Add `recordStartupOutcome(cfg *config.Config, outcome string) error` helper in `cmd/mcpproxy/serve.go` (or a sibling file). Persists to the existing config file. Idempotent.
- [ ] T052 [US5] In `cmd/mcpproxy/serve.go`, after the listener binds and the runtime is ready, call `recordStartupOutcome(cfg, "success")`. In the early-exit error paths (port conflict, db locked, config error, permission error), call with the corresponding outcome before exiting.
- [ ] T053 [US5] Wire `cfg.Telemetry.LastStartupOutcome` into the heartbeat payload render in `telemetry.go`. Test `telemetry_test.go::TestPayloadIncludesStartupOutcome`.

**Checkpoint**: Startup outcomes round-trip through config and into the payload.

---

## Phase 8: User Story 6 — Error Category Histogram (Priority: P2)

**Goal**: Code at known error sites calls `RecordError`; the heartbeat reports counts.

**Independent Test**: Trigger an OAuth refresh failure in a test, assert the counter increments and the payload reports it.

### Tests for User Story 6 ⚠️

- [ ] T054 [P] [US6] Write failing test `internal/oauth/coordinator_test.go::TestOAuthRefreshFailureRecordsErrorCategory` — mock a refresh failure, assert `registry.errorCategories[ErrCatOAuthRefreshFailed] == 1`. Use a fake registry injected into the coordinator.
- [ ] T055 [P] [US6] Write failing test `internal/upstream/managed/client_test.go::TestUpstreamConnectTimeoutRecordsErrorCategory` — similar pattern for connect timeout.
- [ ] T056 [P] [US6] Write failing test `internal/runtime/tool_quarantine_test.go::TestToolQuarantineBlockedRecordsErrorCategory`.

### Implementation for User Story 6

- [ ] T057 [US6] Add a registry getter accessor (or constructor injection) on the OAuth coordinator so it can call `RecordError`. Wire `ErrCatOAuthRefreshFailed` and `ErrCatOAuthTokenExpired` at the appropriate sites in `internal/oauth/coordinator.go`. Test T054 should pass.
- [ ] T058 [US6] Wire `ErrCatUpstreamConnectTimeout` and `ErrCatUpstreamConnectRefused` at the connect-failure sites in `internal/upstream/managed/`. Test T055 should pass.
- [ ] T059 [US6] Wire `ErrCatToolQuarantineBlocked` in `internal/runtime/tool_quarantine.go` at the block site. Test T056 should pass.
- [ ] T060 [US6] Render the error category map into the heartbeat payload in `telemetry.go`. Categories with zero counts MUST be omitted via `omitempty` semantics on the marshal path (use a custom marshal helper if needed).

**Checkpoint**: Three real error sites feed the registry; the payload reflects them.

---

## Phase 9: User Story 7 — Upgrade Funnel (Priority: P3)

**Goal**: Heartbeat reports `previous_version → current_version` and persists the new value only on successful send.

**Independent Test**: Start with `last_reported_version=""`, send heartbeat, observe `previous_version=""`, then a second simulated send updates the persisted value.

### Tests for User Story 7 ⚠️

- [ ] T061 [P] [US7] Write failing test `internal/telemetry/upgrade_funnel_test.go::TestPreviousVersionPersistedOnSuccess` — fake transport returns 200, assert `cfg.Telemetry.LastReportedVersion` is updated.
- [ ] T062 [P] [US7] Write failing test `internal/telemetry/upgrade_funnel_test.go::TestPreviousVersionNotPersistedOnFailure` — fake transport returns 500 (or network error), assert `cfg.Telemetry.LastReportedVersion` is unchanged.

### Implementation for User Story 7

- [ ] T063 [US7] Create `internal/telemetry/upgrade_funnel.go` with helpers to read `cfg.Telemetry.LastReportedVersion` for snapshot and to write it after a successful send.
- [ ] T064 [US7] In `telemetry.Service.send()`, after `resp.StatusCode/100 == 2`, write the new `LastReportedVersion = svc.version` to config and persist via the existing config-save mechanism. Tests T061, T062 should pass.
- [ ] T065 [US7] Wire `previous_version` and `current_version` into the heartbeat payload render.

**Checkpoint**: Upgrade funnel round-trips correctly across success/failure.

---

## Phase 10: User Story 8 — Annual Anonymous-ID Rotation (Priority: P3)

**Goal**: Anonymous ID regenerates every 365 days. Legacy installs are migrated. Clock skew is handled.

**Independent Test**: Set `anonymous_id_created_at` to 366 days ago in test config; render snapshot; assert ID changed and `created_at` is now.

### Tests for User Story 8 ⚠️

- [ ] T066 [P] [US8] Write failing test `internal/telemetry/id_rotation_test.go::TestIDRotatesAfter365Days`.
- [ ] T067 [P] [US8] Write failing test `internal/telemetry/id_rotation_test.go::TestIDDoesNotRotateBefore365Days`.
- [ ] T068 [P] [US8] Write failing test `internal/telemetry/id_rotation_test.go::TestLegacyInstallInitializesCreatedAtWithoutRotating`.
- [ ] T069 [P] [US8] Write failing test `internal/telemetry/id_rotation_test.go::TestClockSkewFutureCreatedAtDoesNotRotate`.

### Implementation for User Story 8

- [ ] T070 [US8] Create `internal/telemetry/id_rotation.go` with `MaybeRotate(cfg *config.Telemetry, now time.Time) (rotated bool)`. Implements the rules from `research.md` R9. Persists changes back to the config struct (the caller is responsible for saving to disk).
- [ ] T071 [US8] Call `MaybeRotate` from `telemetry.Service.Snapshot()` (or its caller). If rotated, persist the config immediately. All four tests should pass.

**Checkpoint**: ID rotation works with legacy migration and clock-skew safety.

---

## Phase 11: User Story 9 — Doctor Check Pass/Fail Rates (Priority: P3)

**Goal**: After every `mcpproxy doctor` invocation, structured results are aggregated into the registry.

**Independent Test**: Run doctor twice in tests (mock results); assert the doctor map in the snapshot has the correct pass/fail totals; flush; assert it's empty.

### Tests for User Story 9 ⚠️

- [ ] T072 [P] [US9] Write failing test `internal/telemetry/registry_test.go::TestRecordDoctorRunAggregates` — pass two `[]CheckResult` slices, assert the map has the summed pass/fail.
- [ ] T073 [P] [US9] Write failing test `cmd/mcpproxy/doctor_cmd_test.go::TestDoctorCommandFeedsRegistry` — run the doctor cobra command with a fake doctor implementation that returns known results, assert registry was called.

### Implementation for User Story 9

- [ ] T074 [US9] Wire `registry.RecordDoctorRun(results)` into `cmd/mcpproxy/doctor_cmd.go` after the doctor run completes. Pass the registry via the existing service injection. Test T073 should pass.
- [ ] T075 [US9] Render the doctor map into the heartbeat payload in `telemetry.go`. Empty map renders as `{}`.

**Checkpoint**: Doctor counts feed into the heartbeat correctly.

---

## Phase 12: User Story 10 — Privacy & Transparency Controls (Priority: P3)

**Goal**: `mcpproxy telemetry show-payload` command, `DO_NOT_TRACK`/`CI` env handling (already done in T022/T023), and the first-run notice on stderr.

**Independent Test**: Wipe `notice_shown` flag, run `mcpproxy serve` once; observe notice on stderr; run again; observe no notice.

### Tests for User Story 10 ⚠️

- [ ] T076 [P] [US10] Write failing test `cmd/mcpproxy/telemetry_cmd_test.go::TestShowPayloadPrintsValidJSON` — capture stdout, run `mcpproxy telemetry show-payload`, assert the output is valid JSON and contains `schema_version: 2`.
- [ ] T077 [P] [US10] Write failing test `cmd/mcpproxy/telemetry_cmd_test.go::TestShowPayloadMakesNoNetworkCall` — wire a fake transport that fails on any call; run the command; assert the test transport was never invoked.
- [ ] T078 [P] [US10] Write failing test `cmd/mcpproxy/serve_test.go::TestFirstRunNoticeOnlyOnce` — run serve startup logic twice (or call the notice helper directly twice with the same config); assert stderr contains the notice on run 1 only.

### Implementation for User Story 10

- [ ] T079 [US10] Add `mcpproxy telemetry show-payload` Cobra subcommand in `cmd/mcpproxy/telemetry_cmd.go`. Loads config, constructs a `telemetry.Service` (or directly a registry + payload renderer), calls `Snapshot()` and prints the marshaled JSON to stdout with two-space indent. No network call. Tests T076, T077 should pass.
- [ ] T080 [US10] Add `MaybePrintFirstRunNotice(cfg *config.Telemetry, w io.Writer) bool` helper in `internal/telemetry/notice.go` (with test). Returns true if printed. Sets `cfg.NoticeShown = true` so the caller can persist.
- [ ] T081 [US10] Call `MaybePrintFirstRunNotice` from `cmd/mcpproxy/serve.go` early in the serve flow (after config load, before telemetry service start). On `true`, persist config. Test T078 should pass.

**Checkpoint**: Privacy controls and transparency CLI all work.

---

## Phase 13: Polish & Cross-Cutting Concerns

**Purpose**: Privacy assertion, documentation, edition build verification, and the e2e smoke run.

- [ ] T082 [P] Create `internal/telemetry/payload_privacy_test.go::TestPayloadHasNoForbiddenSubstrings` — populate every counter, every flag, every error category, every doctor entry; render `Snapshot()`; marshal to JSON; assert the JSON contains none of `localhost`, `127.0.0.1`, `192.168.`, `10.0.`, `/Users/`, `/home/`, `C:\\`, `Bearer `, `apikey=`, `password`, `secret`, `error: `, `failed: `, `Error:`, plus a canary upstream tool name fixture set in the test setup.
- [ ] T083 [P] Update `docs/features/telemetry.md` with: new payload schema (link to `contracts/heartbeat-v2.schema.json`), `DO_NOT_TRACK` and `CI` env var docs, `mcpproxy telemetry show-payload` command, first-run notice text, ID rotation policy.
- [ ] T084 [P] Update `CLAUDE.md` Telemetry CLI section to mention `show-payload` and the env var precedence chain.
- [ ] T085 Run `gofmt -w ./internal/telemetry ./internal/httpapi ./internal/server ./cmd/mcpproxy ./internal/cliclient` and `goimports -w` on all touched files.
- [ ] T086 Run `./scripts/run-linter.sh` and fix any issues.
- [ ] T087 Run `go test -race ./internal/... ./cmd/...` and confirm zero failures, zero race violations.
- [ ] T088 Run `go build ./cmd/mcpproxy` (personal edition) and `go build -tags server ./cmd/mcpproxy` (server edition). Both must compile clean.
- [ ] T089 Run `./scripts/test-api-e2e.sh` and confirm no regressions.
- [ ] T090 Manually verify the quickstart in `quickstart.md` end-to-end on the worktree binary: build, run `mcpproxy telemetry show-payload`, assert output contains `schema_version: 2` and all required fields.
- [ ] T091 Update `cmd/mcpproxy/edition.go` or wherever the version constant lives if needed (no change expected, but verify version string is plumbed into the headers in T032/T033/T034).

**Final Checkpoint**: All tests pass, both editions build, e2e smoke is green, docs updated.

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: No dependencies — read-only exploration.
- **Foundational (Phase 2)**: Depends on Setup. **Blocks all user stories.**
- **User Stories (Phases 3–12)**: All depend on Foundational. Can be implemented in parallel by different developers, but each story phase has internal sequential ordering (test → impl).
- **Polish (Phase 13)**: Depends on all user story phases.

### User Story Dependencies

- **US1 (Surface Tracking)**: independent. MVP candidate.
- **US2 (Built-in Tool Histogram)**: independent. MVP candidate (paired with US1 for the strongest demo).
- **US3 (REST Endpoint Histogram)**: depends only on Foundational. Independent of US1, but US1's middleware wiring is in the same file so coordinate edits.
- **US4–US10**: each independent. No cross-story coupling.

### Within Each User Story

- Test tasks (T0xx with [P] inside the same story) MUST be written before their implementation tasks and MUST FAIL initially.
- Implementation tasks may reference each other but must be executed in declared order.

### Parallel Opportunities

- **Phase 1**: T001–T006 are all read-only and parallelizable.
- **Phase 2**: T009, T011, T013 are parallelizable test creation; T010, T012, T022 are parallelizable impl files. T014–T021 must be sequential (same file).
- **Phase 3+**: All test tasks within a story marked [P] can run in parallel. All non-[P] impl tasks within a story are sequential.
- **Cross-story**: US1 ↔ US2 ↔ US3 implementations can be split across developers if Foundational is done.

---

## Parallel Example: Phase 2 Foundational (test authoring)

```text
# Three developers can author these tests in parallel:
T009 [P] internal/config/config_test.go::TestTelemetryConfigTier2Roundtrip
T011 [P] internal/telemetry/error_categories_test.go::TestValidErrorCategoriesEnum
T013 [P] internal/telemetry/registry_test.go::TestNewCounterRegistry
```

## Parallel Example: User Story 1 tests

```text
T026 [P] [US1] internal/httpapi/middleware_test.go::TestSurfaceClassifierFromHeader
T027 [P] [US1] internal/httpapi/middleware_test.go::TestSurfaceMiddlewareIncrementsRegistry
T028 [P] [US1] internal/server/mcp_test.go::TestMCPRequestCountedAsSurfaceMCP
```

---

## Implementation Strategy

### MVP Scope (Phases 1+2+3 only)

Minimum demonstrable value: **Surface Tracking (US1)** alone delivers the single most-asked product question. After Phase 3 completes, the team can answer "which client surface dominates?" with real data.

### Incremental Delivery

1. **Phases 1+2**: Foundation ready. No user-visible change.
2. **+ Phase 3 (US1)**: Surface tracking works → demo, deploy.
3. **+ Phase 4 (US2)**: Built-in tool histogram → second demo.
4. **+ Phases 5–6 (US3–US4)**: REST + feature flags → third demo.
5. **+ Phases 7–8 (US5–US6)**: Reliability signals → fourth demo.
6. **+ Phases 9–11 (US7–US9)**: Funnel + rotation + doctor → fifth demo.
7. **+ Phase 12 (US10)**: Transparency CLI + first-run notice → ready to ship.
8. **+ Phase 13**: Polish, docs, builds, e2e → final.

### Single-Developer Execution (autonomous mode)

Execute Phases 1 → 2 → 3 → 4 → … → 13 sequentially. Within Phase 2, parallel-test authoring is meaningless for a single developer; do them in order. Each task ≤ 2 hours.

---

## Notes

- **TDD discipline**: every implementation task is preceded by a failing test in the same phase. Verify the test fails before writing implementation, then verify it passes after.
- **Race detection**: counter tests MUST run under `-race`. CI and local runs should both use it.
- **Privacy assertion**: T082's forbidden-substring test is the single most important regression catcher. Do NOT skip it.
- **Edition coverage**: T088 enforces both `personal` and `server` builds.
- **No backwards-compat hacks**: per CLAUDE.md, do not add migration shims for v1 fields. The v1 fields are preserved unchanged; new fields default-zero.
- **Avoid speculative abstractions**: the registry has exactly the methods listed. Do not add a generic `Record(category, key)` API "for future extensibility".
