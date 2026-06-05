# Tasks: Configurable tool-discovery & health-check intervals

**Branch**: `074-discovery-intervals` · **Spec**: [spec.md](./spec.md) · **Plan**: [plan.md](./plan.md) · **Issue**: #608

TDD: write the failing test first for each behavioural sub-task, then implement. Run the **full** suite before pushing (storage canary + approval-hash canary).

## Phase 1 — Ping liveness (User Story 1, P1)

- [ ] **T001** Add `Ping(ctx) error` wrapper to `internal/upstream/core/client.go` delegating to the mcp-go client.
- [ ] **T002** [test] Add a health-path test (mock/fake core client behind an interface seam) asserting that a health-check cycle calls `Ping` and does **not** call `ListTools`.
- [ ] **T003** Rewrite `performHealthCheck` in `internal/upstream/managed/client.go` to probe via `Ping` (5s timeout), preserving the existing error classification (`isConnectionError` → record failure/SetError; transient tolerated; success → record success). Remove the health path's use of `acquireListToolsContext`/`publishListToolsResult`.
- [ ] **T004** Confirm Docker-server skip, logged-out skip, OAuth-backoff, and error-state reconnect branches still behave (no `tools/list` anywhere in the health path).

## Phase 2 — Config schema, resolver, validation (User Story 2 + 3, P1/P2)

- [ ] **T005** [test] Resolver precedence table test: per-server override > global > built-in default (30s / 5m); pointer-to-`0s` = disabled at each level; nil = inherit.
- [ ] **T006** Add `*Duration` fields `HealthCheckInterval` / `ToolDiscoveryInterval` to `Config` (global) and `ServerConfig` (per-server) in `internal/config/config.go`, with json/mapstructure/swaggertype tags matching neighbours. Add the two default constants. **Do not** set them non-nil in `DefaultConfig()`.
- [ ] **T007** Implement `ResolveHealthCheckInterval` / `ResolveToolDiscoveryInterval` (per-server → global → default; `<=0` ⇒ disabled).
- [ ] **T008** [test] Validation bounds test: `0s` accepted; health-check `2s`/`2h` rejected, `5s`/`1h`/`30s` accepted; tool-discovery `10s`/`48h` rejected, `30s`/`24h` accepted; both global and per-server; clear error strings.
- [ ] **T009** Extend `Config.Validate()` to enforce the ranges for every non-nil pointer (global + each server).

## Phase 3 — Wire intervals into the loops

- [ ] **T010** `internal/upstream/managed/client.go` `backgroundHealthCheck`: replace fixed ticker with a resettable timer that re-resolves `ResolveHealthCheckInterval(mc.GetConfig())` each cycle; skip probing when resolved `<=0`; honour hot-reload.
- [ ] **T011** `internal/runtime/lifecycle.go` `backgroundToolIndexing`: replace fixed `5m` ticker with a resettable timer reading `ResolveToolDiscoveryInterval(nil)`; skip the periodic sweep when `<=0` (keep connect-time + reactive discovery).

## Phase 4 — Storage canary

- [ ] **T012** Copy the two new `ServerConfig` fields into `UpstreamRecord` round-trip (or add to the explicit-exclusion list with a comment) so `TestSaveServerSyncFieldCoverage` passes. Verify the approval-hash stability test still passes (fields must not enter `calculateToolApprovalHash`).

## Phase 5 — UI

- [ ] **T013** Web UI: add `discovery` accordion to `frontend/src/views/settings/fields.ts` (two `duration` fields + help text). Sync `frontend/dist` → `web/frontend/dist` before any binary verification (embed gotcha).
- [ ] **T014** macOS: add the mirror `ConfigSection` to `native/macos/MCPProxy/MCPProxy/Settings/SettingsCatalog.swift` (two `.duration` fields).

## Phase 6 — Docs + builds

- [ ] **T015** Update `docs/configuration.md` (both keys, defaults, ranges, `0s=disabled`, per-server override, ping change). Let the swagger pre-push hook regenerate `oas/swagger.yaml`.
- [ ] **T015a** Document the **Docker no-op** (FR-014): `health_check_interval` does not apply to Docker-isolated servers (container-level liveness); `tool_discovery_interval` does. Add to `docs/configuration.md` **and** the `health_check_interval` help string in both `frontend/src/views/settings/fields.ts` and `native/macos/.../SettingsCatalog.swift`.
- [ ] **T016** Verify both editions build: `go build ./cmd/mcpproxy` and `go build -tags server ./cmd/mcpproxy`.

## Phase 7 — Verification gate

- [ ] **T017** `go test -race ./internal/...` + full suite + `./scripts/test-api-e2e.sh` green.
- [ ] **T018** QA: idle proxy against a test upstream, confirm `ping` traffic and **no** health-loop `tools/list`; confirm settings persist + validate in Web UI; confirm `0s` disables. Keep QA artifacts local (do not commit reports).

## Definition of done

All acceptance scenarios (US1–US3) pass; SC-001..SC-006 demonstrated; full suite + e2e green; both builds compile; docs updated; dual-AI review accepted; PR opened with `Related #608`.
