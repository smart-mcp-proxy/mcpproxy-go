# Tasks: Configurable tool-discovery & health-check intervals

**Branch**: `074-discovery-intervals` · **Spec**: [spec.md](./spec.md) · **Plan**: [plan.md](./plan.md) · **Issue**: #608

TDD: write the failing test first for each behavioural sub-task, then implement. Run the **full** suite before pushing (storage canary + approval-hash canary).

## Phase 1 — Ping liveness (User Story 1, P1)

- [x] **T001** Add `Ping(ctx) error` wrapper to `internal/upstream/core/client.go` delegating to the mcp-go client.
- [x] **T002** [test] Add a health-path test (mock/fake core client behind an interface seam) asserting that a health-check cycle calls `Ping` and does **not** call `ListTools`.
- [x] **T003** Rewrite `performHealthCheck` in `internal/upstream/managed/client.go` to probe via `Ping` (5s timeout), preserving the existing error classification (`isConnectionError` → record failure/SetError; transient tolerated; success → record success). Remove the health path's use of `acquireListToolsContext`/`publishListToolsResult`.
- [x] **T004** Confirm Docker-server skip, logged-out skip, OAuth-backoff, and error-state reconnect branches still behave (no `tools/list` anywhere in the health path).

## Phase 2 — Config schema, resolver, validation (User Story 2 + 3, P1/P2)

- [x] **T005** [test] Resolver precedence table test: per-server override > global > built-in default (30s / 5m); pointer-to-`0s` = disabled at each level; nil = inherit.
- [x] **T006** Add `*Duration` fields `HealthCheckInterval` / `ToolDiscoveryInterval` to `Config` (global) and `ServerConfig` (per-server) in `internal/config/config.go`, with json/mapstructure/swaggertype tags matching neighbours. Add the two default constants. **Do not** set them non-nil in `DefaultConfig()`.
- [x] **T007** Implement `ResolveHealthCheckInterval` / `ResolveToolDiscoveryInterval` (per-server → global → default; `<=0` ⇒ disabled).
- [x] **T008** [test] Validation bounds test: `0s` accepted; health-check `2s`/`2h` rejected, `5s`/`1h`/`30s` accepted; tool-discovery `10s`/`48h` rejected, `30s`/`24h` accepted; both global and per-server; clear error strings.
- [x] **T009** Extend `Config.Validate()` to enforce the ranges for every non-nil pointer (global + each server).

## Phase 3 — Wire intervals into the loops

- [x] **T010** `internal/upstream/managed/client.go` `backgroundHealthCheck`: replace fixed ticker with a resettable timer that re-resolves `ResolveHealthCheckInterval(mc.GetConfig())` each cycle; skip probing when resolved `<=0`; honour hot-reload.
- [x] **T011** `internal/runtime/lifecycle.go` `backgroundToolIndexing`: replace fixed `5m` ticker with a resettable timer reading `ResolveToolDiscoveryInterval(nil)`; skip the periodic sweep when `<=0` (keep connect-time + reactive discovery).

## Phase 4 — Storage canary

- [x] **T012** Copy the two new `ServerConfig` fields into `UpstreamRecord` round-trip (or add to the explicit-exclusion list with a comment) so `TestSaveServerSyncFieldCoverage` passes. Verify the approval-hash stability test still passes (fields must not enter `calculateToolApprovalHash`).

## Phase 5 — UI

- [x] **T013** Web UI: add `discovery` accordion to `frontend/src/views/settings/fields.ts` (two `duration` fields + help text). Sync `frontend/dist` → `web/frontend/dist` before any binary verification (embed gotcha).
- [x] **T014** macOS: add the mirror `ConfigSection` to `native/macos/MCPProxy/MCPProxy/Settings/SettingsCatalog.swift` (two `.duration` fields).

## Phase 6 — Docs + builds

- [x] **T015** Update `docs/configuration.md` (both keys, defaults, ranges, `0s=disabled`, per-server override, ping change). Let the swagger pre-push hook regenerate `oas/swagger.yaml`.
- [x] **T015a** Document the **Docker no-op** (FR-014): `health_check_interval` does not apply to Docker-isolated servers (container-level liveness); `tool_discovery_interval` does. Add to `docs/configuration.md` **and** the `health_check_interval` help string in both `frontend/src/views/settings/fields.ts` and `native/macos/.../SettingsCatalog.swift`.
- [x] **T016** Verify both editions build: `go build ./cmd/mcpproxy` and `go build -tags server ./cmd/mcpproxy`.

## Phase 7 — Verification gate

- [ ] **T017** `go test -race ./internal/...` + full suite + `./scripts/test-api-e2e.sh` green.
- [ ] **T018** QA on the built binary (`make build`; kill existing instances first — DB lock; throwaway data-dir). Keep all capture artifacts local (do **not** commit QA reports/screenshots).

  **Why wire/upstream-level capture is required**: the health-check ping and the discovery sweep are *internal* calls — they do NOT appear in `mcpproxy activity` or the REST activity log. Prove `ping` vs `tools/list` at the wire or the upstream's view; mcpproxy's `--log-level=debug` log corroborates *cadence* and the Docker skip but not the raw method.

  **Observability method per transport**:
  - **Remote HTTP/SSE** → **mitmproxy** in reverse mode in front of the upstream; point the upstream URL at it; log each JSON-RPC `method`+timestamp. For HTTPS use regular mode + `HTTPS_PROXY` + trust the mitmproxy CA; reverse mode avoids that for a single target.
    ```bash
    mitmdump -p 8888 --mode reverse:https://real-upstream/mcp -s /tmp/mcp-methods.py
    # addon: def request(flow): print(time.time(), json.loads(flow.request.content).get("method"))
    # then set upstream url -> http://127.0.0.1:8888/mcp
    ```
  - **Local HTTP** (server you control) → a tiny MCP server that logs `method`+time on receipt (simplest, no TLS).
  - **stdio** (mitmproxy can't see it — no network) → wrap `command` in a tee shim: `exec tee -a /tmp/up-in.jsonl | "$@" | tee -a /tmp/up-out.jsonl`, then `grep -c '"method":"ping"'` vs `'"method":"tools/list"'` with timestamps.
  - **Docker** → mcpproxy debug log only (verify the *skip*; no ping expected).

  **Scenarios (map to Success Criteria)**:
  1. **Default (keys unset)** → `ping` ≈ every 30s; `tools/list` ≈ every 5m; zero health-loop `tools/list`. (SC-001 + no regression / SC-005)
  2. **Configured** `health_check_interval:"5s"`, `tool_discovery_interval:"30s"` → cadence matches in the capture. (SC-002)
  3. **Disabled** `"0s"` each → no pings / no periodic `tools/list` (the one connect-time list still appears). (SC-003)
  4. **Per-server override** on one of two servers → only that one changes cadence. (SC-006)
  5. **Validation** → boot with `"2s"`/`"2h"` rejected with a clear error; `curl -X PATCH /api/v1/config` with bad values → 400 + message. (SC-004)
  6. **Docker no-op (FR-014)** → Docker upstream + `health_check_interval:"5s"` → debug log shows "Skipping health check for Docker server", no ping to it; `tool_discovery_interval` still lists it.

  **Corroborating logs**: `grep -E "Health check|Skipping health check|Discovering and indexing" ~/.mcpproxy/logs/main.log`; per-server via `mcpproxy upstream logs <name> --tail 200 --follow`.

  **Also**: confirm both global interval settings appear, validate, and persist in the Web UI (Settings → Advanced → "Tool discovery & health checks") and the macOS app.

## Definition of done

All acceptance scenarios (US1–US3) pass; SC-001..SC-006 demonstrated; full suite + e2e green; both builds compile; docs updated; dual-AI review accepted; PR opened with `Related #608`.
