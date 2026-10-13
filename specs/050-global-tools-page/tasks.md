# Tasks: Global Tools Overview Page

**Input**: Design documents from `/specs/050-global-tools-page/`
**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/global-tools-api.md
**Tests**: REQUIRED (Constitution V — TDD; spec mandates table/E2E/Playwright coverage).

## Format: `[ID] [P?] [Story] Description`

- **[P]**: parallelizable (different files, no incomplete deps)
- **[Story]**: US1 (global listing), US2 (filter/sort/search), US3 (batch enable/disable), US4 (CLI parity)
- Paths: Go backend `internal/`, CLI `cmd/mcpproxy/`, frontend `frontend/src/`

---

## Phase 1: Setup

- [x] T001 Confirm branch `050-global-tools-page`, deps unchanged (no go.mod / package.json edits expected); create `specs/050-global-tools-page/verification/` dir placeholder.

---

## Phase 2: Foundational (blocking — all stories depend on the consolidated data source)

- [x] T002 [P] Add `ToolUsageStat` type and `AggregateToolUsage(since time.Time) (map[string]ToolUsageStat, error)` test stubs (failing) in `internal/storage/activity_test.go`: cases — empty bucket → empty map; multiple tools across servers; window boundary (record exactly at `since`, just-before excluded); never-used tool absent from map; non-`tool_call` types ignored. Run `go test ./internal/storage/ -run AggregateToolUsage` → RED.
- [x] T003 Implement `ToolUsageStat` + `AggregateToolUsage` in `internal/storage/activity.go`: single reverse cursor pass over `ActivityRecordsBucket`, key `serverName + "\x00" + toolName`, count `Type==tool_call` with `Timestamp >= since`, track max `Timestamp`. `go test -race ./internal/storage/ -run AggregateToolUsage` → GREEN.
- [x] T004 [P] Add `GlobalToolsStats` + `GlobalToolsResponse` types in `internal/contracts/types.go` per data-model.md (reuse existing `Tool.Usage`/`Tool.LastUsed`); add to contract-type registry/test if `internal/httpapi/contracts_test.go` enumerates response types.
- [x] T005 Expose `AggregateToolUsage` to the httpapi layer: add method to the storage accessor the controller already holds (or a thin `ServerController`/management pass-through). Wire so `internal/httpapi` can call it without import cycles. Build must pass.

---

## Phase 3: US1 — See every tool in one place (P1) 🎯 MVP

**Goal**: One endpoint + page listing every tool across all servers (incl. disabled servers / disabled / config-denied) with state, approval, usage. **Independent test**: quickstart §2 + §4 empty/loaded.

- [x] T006 [US1] Failing handler test `internal/httpapi/server_global_tools_test.go`: `GET /api/v1/tools` — multi-server merge; tools from a disabled server present; enrichment (`approval_status`/`disabled`/`config_denied`) applied; `stats` consistent (total=len, disabled counts user-disabled OR config-denied, pending counts pending/changed); `usage`/`last_used` populated from a seeded activity record; `partial:true`+`failed_servers` when one server's tool fetch errors; never 500 on partial. RED.
- [x] T007 [US1] Implement `handleGetGlobalTools` in `internal/httpapi/server.go` + register route `r.Get("/tools", ...)` under `/api/v1`: iterate `controller.GetAllServers()`, per server reuse the existing typed-tool enrichment loop (approval/disabled/config-denied) from `handleGetServerTools`/export path, fold in `AggregateToolUsage(now-30d)`, compute `stats`, collect `failed_servers`. `go test -race ./internal/httpapi/ -run GlobalTools` → GREEN.
- [x] T008 [P] [US1] Add `GET /api/v1/tools` to `oas/swagger.yaml` and `docs/api/rest-api.md`; run `./scripts/verify-oas-coverage.sh`.
- [x] T009 [P] [US1] Add `getGlobalTools()` to `frontend/src/services/api.ts` + TS types for the response.
- [x] T010 [US1] Rewrite `frontend/src/views/Tools.vue` (delete dead grid/list/card code): Activity.vue-style header w/ live total badge, summary cards (Total/Enabled/Disabled/Pending), dense table (Tool, Server, Description truncated, Risk badge, Approval badge, Enabled state, Usage, Last used), loading/empty/partial-error states, row → existing schema modal. `data-test` attributes on header, cards, rows, cells.
- [x] T011 [US1] Add `/tools` route in `frontend/src/router/index.ts` and a WORKSPACE sidebar entry with a live tool-count badge (source the count from the page/store; reuse existing badge pattern).
- [x] T012 [US1] Add `./scripts/test-api-e2e.sh` assertion: `GET /api/v1/tools` returns `success`, `data.stats` keys, `data.tools` array, `stats.total == tools|length`.

**Checkpoint**: US1 independently demoable — open `/tools`, see all tools incl. disabled-server tools; curl shows consistent stats.

---

## Phase 4: US2 — Find and narrow down (P1)

**Goal**: search + filters + sort + paginate over the full set, disabled tools never hidden.

- [x] T013 [US2] In `Tools.vue`: client-side substring search over name+description+server (debounced); MUST NOT exclude disabled/config-denied matches.
- [x] T014 [US2] In `Tools.vue`: server, status (enabled/disabled/config-denied), risk (read/write/destructive), approval dropdown filters — combine with AND; reuse Activity.vue filter-bar layout + `data-test` hooks.
- [x] T015 [US2] In `Tools.vue`: sortable column headers (asc/desc toggle) for every displayed column; pagination preserving active filters+sort across pages (reuse Activity pagination pattern).

**Checkpoint**: filter/sort/search verified at ~700 tools updating <1s (SC-003); disabled tools still surface via search (SC-004).

---

## Phase 5: US3 — Bulk enable/disable (P2)

**Goal**: multi-select + batch enable/disable via existing per-tool endpoint, partial-failure summary.

- [x] T016 [US3] In `Tools.vue`: per-row + "select all currently shown" checkboxes (scoped to active filters on current page); selection-count action bar appears when ≥1 selected; `data-test` hooks.
- [x] T017 [US3] In `Tools.vue`: "Enable selected"/"Disable selected" → group selected by server, call existing `POST /servers/{id}/tools/{tool}/enabled` per tool with progress toast; on completion show per-tool success/failure summary; successfully-changed tools stay changed; refresh list + cards. Config-denied target failure surfaced, not silently swallowed.

**Checkpoint**: select 50+ across servers → Disable selected → states + cards update; simulated partial failure reports which failed (SC-002, SC-005).

---

## Phase 6: US4 — CLI parity (P2)

**Goal**: global `tools list` + `tools enable|disable <server:tool ...>` mirroring the page/data.

- [x] T018 [US4] Failing tests in `cmd/mcpproxy/tools_cmd_test.go`: global `tools list` (no `--server`) hits `/api/v1/tools` and lists all servers' tools; `--status disabled` filter; `-o json` shape; `tools disable a:x b:y` returns per-target summary and exits non-zero on any failure; invalid `server:tool` target reported without aborting valid targets. RED.
- [x] T019 [US4] In `cmd/mcpproxy/tools_cmd.go`: make `--server` optional on `tools list`; when absent, call the consolidated endpoint via the daemon/socket client (fallback message if daemon down), add `--status`/`--risk`/`--approval` flags, extend table columns (state/approval/usage/last-used) and keep `-o json|yaml` consistent.
- [x] T020 [US4] Add `toolsEnableCmd` + `toolsDisableCmd` (`tools enable|disable <server:tool ...>`) to `cmd/mcpproxy/tools_cmd.go`: parse `server:tool` args, group by server, call per-tool enabled endpoint via client, print per-target lines, non-zero exit on any failure, invalid target = per-target error not abort. `go test -race ./cmd/mcpproxy/ -run Tools` → GREEN.
- [x] T021 [P] [US4] Update `docs/cli-management-commands.md` + `CLAUDE.md` CLI section + Tools.vue hints panel CLI snippet to document the new global list + enable/disable.

**Checkpoint**: quickstart §3 passes end-to-end incl. exit codes.

---

## Phase 7: Polish & Verification

- [x] T022 Run `./scripts/run-linter.sh` (golangci-lint) + `npm --prefix frontend run build` + `npm --prefix frontend run lint`; fix all.
- [x] T023 `go test -race ./...` and `./scripts/test-api-e2e.sh` green.
- [x] T024 Playwright sweep per CLAUDE.md → screenshots + self-contained `specs/050-global-tools-page/verification/report.html` covering: empty state, loaded table, search filter, column sort, batch disable, sidebar badge count. Commit screenshots+report.
- [ ] T025 chrome-ext spot check of the live page (sidebar badge, batch action toast) for states hard to assert in Playwright; note in execution_log.md.
- [ ] T026 Update `execution_log.md`; final review of FR-001..020 / SC-001..007 coverage; ensure BM25/`retrieve_tools` untouched (grep diff for index changes — must be none).

---

## Dependencies

- Phase 2 (T002–T005) blocks everything (consolidated data source).
- US1 (Phase 3) blocks US2/US3 (they operate on the page+endpoint) and US4 (consumes same endpoint).
- US2, US3 are frontend-only on top of US1 and can proceed in parallel once US1 lands.
- US4 (CLI) depends only on T007 endpoint, parallel to US2/US3.
- Phase 7 after all stories.

## Parallel Opportunities

- T002 / T004 in parallel (storage vs contracts).
- After T007: US4 (CLI, `cmd/`) ∥ US2+US3 (frontend `Tools.vue`) ∥ T008 (docs/oas).
- T009 ∥ T010 start (api service vs view) then converge.

## Implementation Strategy

**MVP = Phase 1+2+3 (US1)**: consolidated endpoint + read-only global table + sidebar = the issue's core ask, independently shippable. US2/US3/US4 are incremental value layered on the same endpoint. Fan-out plan: one agent on backend (T002–T008,T012), one on frontend (T009–T017), one on CLI (T018–T021) after T007 is green.
