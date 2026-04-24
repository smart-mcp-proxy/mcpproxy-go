---
description: "Task list for feature 044-retention-telemetry-v3"
---

# Tasks: Retention Telemetry Hygiene & Activation Instrumentation

**Input**: Design documents from `/specs/044-retention-telemetry-v3/`
**Prerequisites**: plan.md (complete), spec.md (complete), research.md (complete), data-model.md (complete), contracts/heartbeat-v3.json (complete)

**Tests**: Included. The spec's constitution Principle V (TDD) and FR-011 (anonymity self-check) both require tests as first-class artifacts. One failing test precedes every implementation task.

**Organization**: Tasks grouped by user story so each story is independently implementable and shippable.

## Format

`- [ ] [TaskID] [P?] [Story?] Description with file path`

- **[P]**: can run in parallel (different files, no dependencies on incomplete tasks)
- **[Story]**: `[US1]`..`[US4]` label ties the task to its spec user story
- Absolute paths omitted where repo-relative paths are unambiguous

## Path Conventions

- Go code: `internal/telemetry/`, `internal/server/`, `internal/httpapi/`, `cmd/mcpproxy/`
- Swift tray: `native/macos/MCPProxy/Sources/MCPProxy/`
- Packaging: `packaging/macos/`
- Docs: `docs/features/`

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Minor scaffolding. The existing `internal/telemetry` package already has v3 schema version and the build system is mature — no structural setup needed.

- [ ] T001 Confirm `SchemaVersion = 3` constant in `internal/telemetry/telemetry.go` remains 3 (spec 044 extends v3, does not re-bump); add a short comment noting spec 044 additions above the constant.
- [ ] T002 Add a new BBolt bucket-name constant `ActivationBucketName = "activation"` in `internal/telemetry/activation.go` (file created in Phase 2).
- [ ] T003 [P] Extend `docs/features/telemetry.md` skeleton with placeholder sections "Environment Classification", "Activation Tracking", and "Launch Source" (content filled in polish phase).

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Shared primitives that every user story depends on — the BBolt activation store, the env_kind detection shell, and the payload-builder anonymity self-check.

CRITICAL: No user story work begins until this phase is complete.

- [ ] T004 Create `internal/telemetry/env_kind.go` defining the `EnvKind` enum type (string alias) with constants `EnvKindInteractive`, `EnvKindCI`, `EnvKindCloudIDE`, `EnvKindContainer`, `EnvKindHeadless`, `EnvKindUnknown`; export `AllEnvKinds()` helper for validation.
- [ ] T005 Create `internal/telemetry/launch_source.go` defining `LaunchSource` enum type with constants `LaunchSourceInstaller`, `LaunchSourceTray`, `LaunchSourceLoginItem`, `LaunchSourceCLI`, `LaunchSourceUnknown`; export `AllLaunchSources()`.
- [ ] T006 [P] Create `internal/telemetry/env_markers.go` defining the `EnvMarkers` struct with exactly five boolean fields (`HasCIEnv`, `HasCloudIDEEnv`, `IsContainer`, `HasTTY`, `HasDisplay`) and their JSON tags per data-model.md.
- [ ] T007 [P] Create `internal/telemetry/activation.go` stub: define `ActivationState` struct per data-model.md, `ActivationBucketName` constant, and an `ActivationStore` interface with methods `Load(db *bbolt.DB) (ActivationState, error)`, `Save(db *bbolt.DB, st ActivationState) error`, `MarkFirstConnectedServer`, `MarkFirstMCPClient`, `MarkFirstRetrieveToolsCall`, `RecordMCPClient(name string)`, `IncrementRetrieveToolsCall()`, `AddTokensSaved(n int)`, `SetInstallerPending(v bool)`, `IsInstallerPending() bool`.
- [ ] T008 Create `internal/telemetry/anonymity.go` with `ScanForPII(payloadJSON []byte) error` and `AnonymityBlockedPrefixes = []string{"/Users/", "/home/", "C:\\Users\\", "/var/folders/"}`; function returns a typed error identifying which prefix or env-var value was detected.
- [ ] T009 Write failing test file `internal/telemetry/anonymity_test.go` with cases: (a) payload containing `/Users/alice/` fails, (b) payload with `GITHUB_TOKEN` value fails, (c) clean payload passes, (d) payload with non-boolean `env_markers.has_ci_env` fails.
- [ ] T010 Implement `ScanForPII` to satisfy T009 by using a combination of `bytes.Contains` for prefix checks and `json.Unmarshal` into a strict `EnvMarkers` struct to catch type mismatches.

**Checkpoint**: Foundation complete. Tests for T009 pass. User-story phases can begin in parallel.

---

## Phase 3: User Story 1 — Ground-truth CI classification (Priority: P1) 🎯 MVP

**Goal**: Client computes and transmits `env_kind` + `env_markers` so the dashboard no longer relies on version-rule heuristics.

**Independent Test**: Run mcpproxy in four contexts (desktop, GitHub Actions runner, Docker container, headless Linux) and verify the heartbeat payload carries the right `env_kind` for each, with `env_markers` booleans matching.

### Tests (write first — must fail before implementation)

- [ ] T011 [P] [US1] Create `internal/telemetry/env_kind_test.go` table-driven test: one row per decision-tree branch (interactive-mac, interactive-linux-tty, interactive-linux-display, ci-github, ci-gitlab, ci-jenkins, cloud-ide-codespaces, cloud-ide-gitpod, container-dockerenv, container-containerenv, container-envvar, headless-linux, unknown-fallback). Each row injects a fake env map + fake file prober + fake TTY checker.
- [ ] T012 [P] [US1] Add test to `internal/telemetry/env_kind_test.go` verifying `DetectEnvKindOnce()` returns the same value across 100 concurrent goroutines and that the underlying `DetectEnvKind` is invoked exactly once.
- [ ] T013 [P] [US1] Add test to `internal/telemetry/env_kind_test.go` verifying `EnvMarkers` populated by detection contains the exact booleans from the decision tree (has_ci_env true when CI var present, is_container true when `/.dockerenv` exists, etc.).

### Implementation

- [ ] T014 [US1] Implement `DetectEnvKind(env map[string]string, fs FileProber, osName string, ttyChecker TTYChecker) (EnvKind, EnvMarkers)` in `internal/telemetry/env_kind.go` following the ordered decision tree from research.md R1.
- [ ] T015 [US1] Implement `DetectEnvKindOnce()` wrapper in `internal/telemetry/env_kind.go` using `sync.Once` to cache the result at package scope; expose `ResetEnvKindForTest()` behind a build tag `//go:build testing` (or as an unexported symbol called only from `_test.go`).
- [ ] T016 [US1] Add a small `defaultFileProber` that wraps `os.Stat` and a `defaultTTYChecker` that wraps `golang.org/x/term.IsTerminal(int(os.Stdin.Fd()))` in `internal/telemetry/env_kind.go`.
- [ ] T017 [US1] Extend `telemetry.HeartbeatPayload` in `internal/telemetry/telemetry.go` with `EnvKind string` and `EnvMarkers *EnvMarkers` fields per data-model.md.
- [ ] T018 [US1] Update `telemetry.Service.buildPayload` (or equivalent in `telemetry.go`) to call `DetectEnvKindOnce()` and populate `EnvKind` + `EnvMarkers`.
- [ ] T019 [US1] Extend `internal/httpapi/server.go` `/api/v1/status` handler to include `env_kind` and `env_markers` from the telemetry service (read-only snapshot per FR-018).
- [ ] T020 [US1] Run `go test -race ./internal/telemetry/...` and confirm T011–T013 pass.

**Checkpoint**: US1 shippable. `env_kind` + `env_markers` flow end-to-end.

---

## Phase 4: User Story 4 — Privacy & anonymity preservation (Priority: P1)

**Goal**: Payload builder scans serialized output for PII leaks and rejects violations; anonymous_id remains byte-identical.

**Independent Test**: Inject a synthetic payload containing `/Users/alice` and confirm the payload builder rejects it; verify existing v2 payloads still emit the same `anonymous_id` as pre-044.

### Tests (write first)

- [ ] T021 [P] [US4] Extend `internal/telemetry/payload_privacy_test.go` with a test that builds a full v3 payload and scans it with `ScanForPII`; test passes when no prefix matches.
- [ ] T022 [P] [US4] Add a test to `internal/telemetry/payload_privacy_test.go` that corrupts a payload with a synthetic `"env_markers":{"has_ci_env":"yes"}` (string instead of bool) and asserts `ScanForPII` or the strict unmarshal rejects it.
- [ ] T023 [P] [US4] Add a test to `internal/telemetry/telemetry_test.go` comparing `anonymous_id` between a v2 build and a v3 build for the same fixture — assert byte-identical (FR-017).

### Implementation

- [ ] T024 [US4] Wire `ScanForPII` into `telemetry.Service.buildPayload` — call after `json.Marshal`, before HTTP POST; on failure, log `telemetry anonymity violation (not transmitted)` at error level and increment a new `anonymity_violations_total` counter registered with the existing `CounterRegistry` in `internal/telemetry/registry.go`.
- [ ] T025 [US4] Add a runtime-detected blocked-value list: on startup, populate `anonymity.BlockedValues` with `os.Hostname()` result, the last path component of `os.UserHomeDir()`, and the values of any env var from `{GITHUB_TOKEN, GITLAB_TOKEN, OPENAI_API_KEY, ANTHROPIC_API_KEY, GOOGLE_API_KEY}` when non-empty. Implement in `internal/telemetry/anonymity.go`.
- [ ] T026 [US4] Run `go test -race ./internal/telemetry/...` and confirm T021–T023 pass and T009 still passes.

**Checkpoint**: US4 shippable independently (and co-ships with US1).

---

## Phase 5: User Story 2 — Activation funnel visibility (Priority: P1)

**Goal**: BBolt activation bucket records monotonic first-ever flags + 24h counters; MCP `initialize` records `clientInfo.name`; `retrieve_tools` bumps counter; heartbeat + `/api/v1/status` expose state.

**Independent Test**: Fresh BBolt → all flags false. Connect a server → next heartbeat carries `first_connected_server_ever=true`. Restart process → still true. Dispatch an MCP `initialize` from a client identifying as `claude-code` → subsequent payload lists `claude-code`. Call `retrieve_tools` 3x → `retrieve_tools_calls_24h=3`.

### Tests (write first)

- [ ] T027 [P] [US2] Create `internal/telemetry/activation_test.go` with test cases: (a) empty BBolt → Load returns zero-value struct, (b) Save then Load round-trips, (c) monotonic flag cannot flip true→false through Save, (d) MCP clients list deduplicates and caps at 16 (17th insertion is dropped), (e) path-like client name recorded as "unknown", (f) 24h window decay resets the counter after simulated 25h elapsed.
- [ ] T028 [P] [US2] Add test `TestRetrieveToolsBucket` to `activation_test.go` verifying 100 concurrent `IncrementRetrieveToolsCall` calls result in count=100 with no races (use `-race`).
- [ ] T029 [P] [US2] Add test `TestTokensSavedBucketing` to `activation_test.go` with table: 0→"0", 50→"1_100", 500→"100_1k", 5000→"1k_10k", 50000→"10k_100k", 500000→"100k_plus".
- [ ] T030 [P] [US2] Add integration test `internal/server/mcp_initialize_test.go` (or extend existing) that invokes an MCP `initialize` with `params.clientInfo.name = "claude-code"` and asserts a telemetry hook records the client (use an in-memory fake `ActivationStore`).
- [ ] T031 [P] [US2] Add integration test `internal/server/mcp_retrieve_tools_test.go` (or extend existing) that calls the builtin `retrieve_tools` and asserts `IncrementRetrieveToolsCall` fires.

### Implementation

- [ ] T032 [US2] Implement `ActivationStore` methods in `internal/telemetry/activation.go`: `Load`, `Save`, `MarkFirstConnectedServer`, `MarkFirstMCPClient`, `MarkFirstRetrieveToolsCall`, `RecordMCPClient` (with sanitization per research R7), `IncrementRetrieveToolsCall`, `AddTokensSaved`, `SetInstallerPending`, `IsInstallerPending`.
- [ ] T033 [US2] Implement token-saved bucketing helper `BucketTokens(n int) string` in `internal/telemetry/activation.go` per FR-009.
- [ ] T034 [US2] Implement 24h window decay logic inside `IncrementRetrieveToolsCall` / emit-time reset in `internal/telemetry/activation.go`.
- [ ] T035 [US2] Implement client-name sanitizer `sanitizeClientName(raw string) string` in `internal/telemetry/activation.go` — regex `^[a-z0-9][a-z0-9-_.]{0,63}$`; reject `/`, `\\`, `..`, `@`; fall back to `"unknown"`.
- [ ] T036 [US2] Extend `telemetry.HeartbeatPayload` in `internal/telemetry/telemetry.go` with `Activation *ActivationState` field.
- [ ] T037 [US2] Update `telemetry.Service.buildPayload` to load activation state from BBolt, compute `RetrieveToolsCalls24h` and `EstimatedTokensSaved24hBucket` at emit time, and embed in the payload.
- [ ] T038 [US2] Hook MCP `initialize` handler in `internal/server/mcp.go`: on successful handshake, call `activationStore.MarkFirstMCPClient()` + `activationStore.RecordMCPClient(params.clientInfo.name)`. Plumb the store through via the existing runtime dependency-injection path (likely via `runtime.Runtime` or `server.Server` field).
- [ ] T039 [US2] Hook builtin `retrieve_tools` in the relevant handler under `internal/server/` (e.g., `mcp_builtin.go` or wherever `retrieve_tools` is dispatched): on each call, invoke `activationStore.IncrementRetrieveToolsCall()` + `activationStore.MarkFirstRetrieveToolsCall()`; estimate tokens saved from the returned result and call `AddTokensSaved`.
- [ ] T040 [US2] Hook upstream-server connection-success event in `internal/runtime/` (wherever connect success is emitted): call `activationStore.MarkFirstConnectedServer()`.
- [ ] T041 [US2] Extend `/api/v1/status` handler in `internal/httpapi/server.go` to include the `activation` snapshot (read-only, loaded via `ActivationStore.Load`).
- [ ] T042 [US2] Extend `cmd/mcpproxy/telemetry_cmd.go` `telemetry status` subcommand to display activation flags + counters in the output.
- [ ] T043 [US2] Run `go test -race ./internal/telemetry/... ./internal/server/...` and confirm T027–T031 pass.

**Checkpoint**: US2 shippable. Activation funnel data flowing.

---

## Phase 6: User Story 3 — Auto-start default ON (Priority: P2)

**Goal**: On macOS first-launch the tray registers itself as a login item by default; tray exposes state via socket; core populates `autostart_enabled`; installer launches tray with `MCPPROXY_LAUNCHED_BY=installer`.

**Independent Test**: Install mcpproxy fresh on macOS 13+, complete the first-run dialog with defaults, log out+in, confirm tray returns, confirm first heartbeat carries `autostart_enabled=true` and `launch_source=login_item` (subsequent) or `installer` (first).

### Tests (write first)

- [ ] T044 [P] [US3] Create `internal/telemetry/launch_source_test.go` table-driven: env=installer → installer; handshake=tray → tray; ppid-is-launchd → login_item (mocked); tty=true → cli; fallthrough → unknown.
- [ ] T045 [P] [US3] Create `internal/telemetry/autostart_test.go` covering the socket-mediated reader: when tray responds true → state `true`; when tray returns 500 → state `nil`; when tray not running → state `nil`.
- [ ] T046 [P] [US3] Add test to `internal/telemetry/activation_test.go` verifying `installer_heartbeat_pending` is set on startup when env var present and cleared after one heartbeat (fake `BuildPayload`).
- [ ] T047 [P] [US3] Create `native/macos/MCPProxy/Sources/MCPProxy/Tests/AutoStartTests.swift` stub (XCTest) that asserts the first-run dialog's "Launch at login" checkbox state is ON by default; run via `mcpproxy-ui-test` screenshot verification if XCTest harness is unavailable.

### Implementation

- [ ] T048 [US3] Implement `DetectLaunchSource(env, handshake, ppidChecker, ttyChecker) LaunchSource` + `DetectLaunchSourceOnce()` in `internal/telemetry/launch_source.go` per research R3.
- [ ] T049 [US3] Implement `autostart.go` reader in `internal/telemetry/autostart.go`: on macOS/Windows, send a request to the tray socket's `/autostart` endpoint (1h TTL cache in the telemetry service); on Linux, return `nil`. Gracefully handle socket absent → `nil`.
- [ ] T050 [US3] Extend `telemetry.HeartbeatPayload` with `LaunchSource string` and `AutostartEnabled *bool` fields (pointer for tri-state per data-model.md).
- [ ] T051 [US3] Update `telemetry.Service.buildPayload` to: (a) on first heartbeat with `installer_heartbeat_pending=true`, emit `launch_source=installer` and clear the flag, (b) otherwise emit `DetectLaunchSourceOnce()` result, (c) populate `AutostartEnabled` from `autostart.Read()`.
- [ ] T052 [US3] Set `activationStore.SetInstallerPending(true)` at process startup in the runtime wire-up (likely `cmd/mcpproxy/serve.go` or equivalent) when `os.Getenv("MCPPROXY_LAUNCHED_BY") == "installer"`.
- [ ] T053 [US3] Create `native/macos/MCPProxy/Sources/MCPProxy/AutoStart.swift`: wrapper around `SMAppService.mainApp` with `register()`, `unregister()`, and `isEnabled()` methods; log failures without crashing.
- [ ] T054 [US3] Create `native/macos/MCPProxy/Sources/MCPProxy/FirstRunDialog.swift`: SwiftUI sheet presented on first launch with a "Launch at login" checkbox defaulting to ON; on dismiss, call `AutoStart.register()` if checked; persist "first-run-completed" marker via UserDefaults.
- [ ] T055 [US3] Extend the tray's socket route handler in `native/macos/MCPProxy/Sources/MCPProxy/` to expose `GET /autostart` returning `{"enabled": true|false}` based on `AutoStart.isEnabled()`.
- [ ] T056 [US3] On macOS tray launch in `MCPProxyApp.swift` (or equivalent entry point): if first-run marker is absent, present `FirstRunDialog`; otherwise proceed silently.
- [ ] T057 [US3] Create `packaging/macos/postinstall.sh` per research R10; mark executable; ensure it is invoked by the DMG's post-install step or embedded pkg.
- [ ] T058 [US3] Update macOS DMG build script (`scripts/build.sh` or the relevant target under `packaging/macos/`) to include `postinstall.sh` and ensure it has the correct permissions in the final artifact.
- [ ] T059 [US3] Build and replace the tray per `CLAUDE.md` instructions (`swiftc -target arm64-apple-macosx13.0 ...`); restart tray; use `mcp__mcpproxy-ui-test__screenshot_window` to capture the first-run dialog and verify checkbox state.
- [ ] T060 [US3] Run `go test -race ./internal/telemetry/...` and confirm T044–T046 pass.

**Checkpoint**: US3 shippable. macOS auto-start default ON reaches users.

---

## Phase 7: Polish & Cross-cutting

**Purpose**: Integration tests, docs, and release prep that span all user stories.

- [ ] T061 Add end-to-end test `internal/telemetry/e2e_payload_v3_test.go` that starts a fake HTTP sink, drives the telemetry service through one heartbeat, and asserts the POST body matches `specs/044-retention-telemetry-v3/contracts/heartbeat-v3.json`.
- [ ] T062 Extend `./scripts/test-api-e2e.sh` with a check that `/api/v1/status` returns the new fields (`env_kind`, `env_markers`, `activation`, `launch_source`, `autostart_enabled`).
- [ ] T063 [P] Update `docs/features/telemetry.md` sections stubbed in T003: document the 5 new fields, the env_kind decision tree, the bucketing scheme, and the opt-out behavior.
- [ ] T064 [P] Update `CLAUDE.md` "Active Technologies" entry for 044-retention-telemetry-v3 with a short summary of the activation bucket and env_kind detection (already added by agent-context script — verify accuracy).
- [ ] T065 [P] Update `oas/swagger.yaml` to document the new fields in `/api/v1/status`.
- [ ] T066 Run `./scripts/run-linter.sh` and address any findings.
- [ ] T067 Run `./scripts/test-api-e2e.sh` end-to-end and confirm exit 0.
- [ ] T068 Run `go test -race ./internal/...` full suite and confirm no regressions.
- [ ] T069 Quickstart verification: follow `specs/044-retention-telemetry-v3/quickstart.md` end-to-end on a local install; record any deviations.
- [ ] T070 Open a PR against the base branch with title `feat(telemetry): payload v3 with env_kind, activation, auto-start default` and link this spec.

---

## Dependencies

- Phase 1 (Setup) → Phase 2 (Foundational) → Phases 3–6 (User Stories, parallelizable) → Phase 7 (Polish).
- Within each user story, Tests tasks precede Implementation tasks (TDD).
- US1 (env_kind) and US4 (anonymity) share `telemetry.HeartbeatPayload` edits — US1 adds `EnvKind` + `EnvMarkers`, US4 wires the scanner; they can co-ship in one commit if desired, or be split (T017 before T024).
- US2 (activation) depends on the BBolt store from Phase 2 (T007) but is independent of US1/US4.
- US3 (auto-start) depends on Phase 2 activation stub (for `installer_heartbeat_pending`) and on US2's BBolt store being wired (T032), but does not depend on US1 or US4.

## Parallel Execution Examples

**Within Phase 2 Foundational** (after T004, T005 complete):
- T006, T007 can run in parallel (different files).

**Within Phase 3 US1**:
- T011, T012, T013 are all test files in the same package — mark [P] but run serially under `go test -race` to avoid flaky Once-reset interactions.
- T014, T016 touch the same file — serial.

**Across user stories**:
- After Phase 2 checkpoint, a single developer can start US1 + US4 in one session (tightly coupled) and delegate US2 + US3 to a second session.

## MVP Scope

MVP = Phase 1 + Phase 2 + Phase 3 (US1) + Phase 4 (US4) + Phase 7 polish.

- Ships: ground-truth env_kind classification + anonymity-preserving payload scanner.
- Defers: activation funnel (US2) and auto-start default (US3) to follow-up releases without blocking the retention-dashboard unlock.

## Format Validation

Every task above follows `- [ ] [TID] [P?] [US?] Description with file path`. Checked manually:
- All tasks have a checkbox (✓)
- All tasks have a TID (T001–T070)
- All user-story tasks have a `[US#]` label (✓)
- All tasks include a file path or unambiguous target (✓)
- Setup (T001–T003) and Foundational (T004–T010) and Polish (T061–T070) intentionally omit `[US#]` labels (✓)
