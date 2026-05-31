---
description: "Task list for 070-registry-easy-upstream-add"
---

# Tasks: Registry — Make Discovery Actual & Easy to Add to Upstream

**Input**: Design documents from `/specs/070-registry-easy-upstream-add/`
**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/add-from-registry.md, quickstart.md

**Tests**: REQUIRED — Constitution V (TDD) and FR-010 mandate tests on all three surfaces + REST + a cross-surface consistency regression. Test tasks come before their implementation (red-green).

**Organization**: By user story (US1–US4 from spec.md). The keystone core op (Foundational) is the shared dependency for US1/US2/US3.

**GATE NOTE**: This tasks list is part of the design submitted at the per-spec design gate (Gate 2). **No task below may begin until the gate is approved.** Implementation happens in an isolated worktree; PRs are opened but never self-merged (Gate 3).

## Path Conventions
Web app: Go backend (`internal/`, `cmd/`) + embedded Vue frontend (`frontend/src/`). Paths are repo-root absolute.

---

## Phase 1: Setup

- [ ] T001 Create isolated worktree `git worktree add ../mcpproxy-go-746 -b 746-registry-add` and confirm `make build` is green before any change (baseline).
- [ ] T002 [P] Add `data-test` attribute convention stubs to `frontend/src/views/Repositories.vue` and `frontend/src/components/AddServerModal.vue` (none exist today) so later Playwright tasks have hooks.

---

## Phase 2: Foundational (BLOCKING — keystone, must complete before US1/US2/US3)

**Purpose**: The single backend core op that every surface calls (FR-001 / CN-001 / CN-004).

- [ ] T003 [P] Extend `registries.ServerEntry` with `RequiredInputs []RequiredInput` and add the `RequiredInput` type in `internal/registries/types.go` (FR-003 plumbing per data-model.md).
- [ ] T004 Add `FindServerByID(ctx, registryID, serverID string, guesser) (*ServerEntry, error)` in `internal/registries/search.go` (reuse `SearchServers`; returns `server_not_found` when absent).
- [ ] T005 [US-core] Write FAILING unit tests for the core op in `internal/server/add_from_registry_test.go`: stdio result→command/args; http result→url; quarantine-by-default true; refusal cases (`no_install_info`, `missing_required_input`, `duplicate_name`, `registry_not_found`, `server_not_found`).
- [ ] T006 [US-core] Implement `AddServerFromRegistry(ctx, req AddFromRegistryRequest) (*config.ServerConfig, error)` in `internal/server/add_from_registry.go`: resolve registry+server, derive validated `config.ServerConfig`, force `Quarantined = cfg.DefaultQuarantineForNewServer()`, persist via `SaveUpstreamServer`. Make T005 pass.
- [ ] T007 [US-core] Implement required-input detection helper (explicit fields + `${VAR}` heuristic) feeding `RequiredInputs`; covered by T005 cases.

**Checkpoint**: Core op green (`go test ./internal/server/ -run TestAddFromRegistry -race`). All surfaces below are thin callers.

---

## Phase 3: User Story 2 — Discover & add from the CLI (P1) 🎯 MVP

**Goal**: Close search→add on the CLI (the genuine net-new gap; `search-servers` list/search already exist).
**Independent test**: `mcpproxy registry list` → `registry search` → `registry add <reg> <id>` → server appears quarantined in `upstream list`.

- [ ] T008 [P] [US2] Add `cliclient` methods `ListRegistries`, `SearchRegistry`, `AddFromRegistry` in `internal/cliclient/client.go` (mirror `GetServers`/`ApproveTools` patterns).
- [ ] T009 [US2] Write FAILING CLI e2e test in `e2e/cli/registry_add_test` (or `scripts/test-*`): list→search→add→assert quarantined entry via running daemon.
- [ ] T010 [US2] Create `cmd/mcpproxy/registry_cmd.go` with `registry list|search|add` group (Cobra), wired to `cliclient` + `internal/cli/output` formatter; register in `cmd/mcpproxy/main.go`. Keep `search-servers` as a back-compat alias.
- [ ] T011 [US2] `registry add` `--env KEY=VALUE`, `--name`, `--enabled` flags; on `missing_required_input` print actionable error naming the `--env` keys. Make T009 pass.

**Checkpoint**: CLI MVP independently deliverable.

---

## Phase 4: User Story 3 — Add from registry via MCP without hand-constructing config (P2→ promoted with core)

**Goal**: `upstream_servers` gains `add_from_registry` by reference.
**Independent test**: MCP `upstream_servers operation=add_from_registry {registry,id}` → quarantined entry equal to manual construction.

- [ ] T012 [US3] Write FAILING MCP handler test in `internal/server/mcp_*_test.go`: `add_from_registry` happy path + `missing_required_input` structured error.
- [ ] T013 [US3] Add `add_from_registry` to the `upstream_servers` operation enum + params (`registry`,`id`,`name`,`env_json`) in the tool schema (`internal/server/mcp.go:629-675`) and dispatch to `AddServerFromRegistry`. Make T012 pass.

---

## Phase 5: User Story 1 — Web UI one-flow add (P1)

**Goal**: Repoint the existing Add button to the backend core op (stop client-side parsing) + prompt for required inputs.
**Independent test**: Playwright — search, click Add, (prompt if required), server appears quarantined; no client-side `install_cmd.split`.

- [ ] T014 [US1] Add REST route `POST /api/v1/registries/{registryId}/servers/{serverId}/add` → `AddServerFromRegistry` in `internal/httpapi/server.go`; FAILING REST/curl test first (in `scripts/test-api-e2e.sh` or a handler test).
- [ ] T015 [US1] Replace `addServerFromRegistry`'s client-side `install_cmd.split` (`frontend/src/services/api.ts:646-678`) with a call to the new REST endpoint (server derives config).
- [ ] T016 [US1] Add required-input prompt UI in `frontend/src/views/Repositories.vue` / `AddServerModal.vue` (render `required_inputs[]`; block add until provided) with `data-test` hooks.
- [ ] T017 [US1] Write Playwright spec `e2e/playwright/registry-add.spec.ts`: search→Add→prompt→quarantined; `make build` to embed UI; run green.

---

## Phase 6: User Story 4 — Keep the registry list current & resilient (P2)

**Goal**: merge-with-defaults, cache freshness/refresh, key-absent skip.
**Independent test**: per quickstart US4.

- [ ] T018 [P] [US4] Change `SetRegistriesFromConfig` to MERGE built-in defaults ∪ config by ID (`internal/registries/registry_data.go:10-42`) + unit test asserting custom entry doesn't drop the 5 defaults (FR-006).
- [ ] T019 [P] [US4] Add `Refresh`/`Invalidate` + age/`stale` to `internal/cache/manager.go`; surface `cache:{age_seconds,stale}` on `GET /registries/{id}/servers` and add `POST /api/v1/registries/{id}/refresh` (FR-007).
- [ ] T020 [US4] Add `RequiresKey` to registry entry + skip/mark `unavailable:{reason}` when key absent without failing overall search (`internal/registries/search.go`); unit test (FR-008/SC-006).

---

## Phase 7: KEYSTONE regression + Polish (FR-010 / CN-004)

- [ ] T021 [US-core] Cross-surface consistency regression `internal/server/consistency_crosssurface_test.go`: add same `(registry,serverId,env,name)` via REST + MCP + CLI add path → assert byte-identical persisted `config.ServerConfig` (modulo `Created`), all `Quarantined==true` (SC-004).
- [ ] T022 [P] Run full gates: `./scripts/run-linter.sh`; `go test ./internal/... -race`; **`go test ./internal/runtime/... -race`** (approval-hash stability canary — memory); `./scripts/test-api-e2e.sh`.
- [ ] T023 [P] Docs: minimal CLAUDE.md MCP-tool + CLI table delta (mind 40k-char gate — `wc -c` first); update `docs/` registry/CLI reference.
- [ ] T024 Apply the gate-approved decisions on O1–O4 (required-input depth, key-demo, spec amendment, P2 scope) before opening the PR.

---

## Dependencies & order

- **Phase 1 → Phase 2** (keystone) blocks Phases 3/4/5.
- **Phase 2 (T003–T007)** is the shared dependency for US1/US2/US3.
- **US2 (Phase 3)** is the MVP — smallest independently shippable slice once the core op exists.
- **US4 (Phase 6)** is independent of US1/2/3 (registry resilience) — can parallelize after Phase 2.
- **T021 (consistency regression)** requires all three add surfaces (T011, T013, T014) present.

## Parallel opportunities
- T002/T003 (different files) in setup/foundational.
- After Phase 2: US2 (T008,T010) ∥ US4 (T018,T019,T020) — disjoint files.
- Polish T022/T023 [P].

## Implementation strategy (MVP-first)
1. **MVP** = Phase 1 + Phase 2 (core op) + Phase 3 (CLI add) → demonstrable closed loop on the automation surface.
2. Then US3 (MCP), US1 (Web UI), US4 (resilience).
3. Finish with T021 keystone regression + gates + docs.

## Gate reminders (doctrine)
- Do not start T001+ until Gate 2 (design) is approved.
- Worktree only; never commit to `main`.
- Open PR with `Related #746`; never self-merge (Gate 3). QA pass + Critic approval required before requesting the pre-merge gate.
