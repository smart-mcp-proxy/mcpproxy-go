# Tasks: Agent-Discoverable Disabled Tools

**Input**: Design documents from `/specs/049-agent-discoverable-disabled-tools/`
**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/mcp-deltas.md, quickstart.md

**Tests**: REQUIRED — Constitution Principle V (TDD) and repo CLAUDE.md mandate a failing test before implementation.

**Organization**: By user story (US1 P1 → US2 P2 → US3 P3). US1 is the MVP.

## Path Conventions

Single Go project. Backend only under `internal/`. No frontend (UI lock badges shipped in #468).

---

## Phase 1: Setup (Shared Infrastructure)

- [X] T001 Verify branch `049-agent-discoverable-disabled-tools` is rebased on `main` containing #468 (`git merge-base --is-ancestor 3ffa41e9 HEAD`); `go build ./cmd/mcpproxy` succeeds.

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: The shared classifier + additive response types every story depends on. MUST complete before US1/US2/US3.

- [X] T002 [P] Add additive types to `internal/contracts/types.go`: `DisabledToolStatus` string consts (`server_disabled`,`disabled_by_config`,`disabled_by_user`,`pending_approval`,`disabled_unknown`); `LockedToolEntry{Name,Server,Description,Status}`; extend the retrieve_tools response wrapper with `Disabled []LockedToolEntry \`json:"disabled,omitempty"\`` and `Remediation map[string]string \`json:"remediation,omitempty"\``; extend the upstream_servers server entry with `Tools *ServerToolCounts \`json:"tools,omitempty"\``; define `ServerToolCounts` with all six int fields `omitempty`.
- [X] T003 Write failing table test `internal/runtime/tool_disabled_classify_test.go` for `ClassifyDisabledTool(serverName, toolName) DisabledToolStatus`: cases for each of the 5 states, first-match precedence (config-denied + user-disabled → `disabled_by_config`), and storage-error → `disabled_unknown`.
- [X] T004 Implement `ClassifyDisabledTool` in `internal/runtime/tool_quarantine.go` (pure, request-time, read-only): precedence server-off → `IsToolConfigDenied` → approval.Disabled → approval pending/changed → unknown. No BBolt writes. Make T003 green.
- [X] T005 Add `internal/runtime` micro-benchmark `BenchmarkClassifyDisabledTool` over 1k tools; assert classify loop stays well under the 100ms discovery budget (Constitution I).

**Checkpoint**: classifier + types exist and are unit-green; no handler wired yet.

---

## Phase 3: User Story 1 — Agent discovers locked capability on demand (P1) 🎯 MVP

**Goal**: `retrieve_tools` opt-in `include_disabled` returns locked tools with status + remediation; default path unchanged.

**Independent test**: quickstart.md §2 (default unchanged) + §3 (opt-in returns `printEnv` as `disabled_by_config` and a user-disabled tool as `disabled_by_user`, callable-first, capped, remediation keyed only by present statuses).

- [X] T006 [US1] Write failing test `internal/server/mcp_disabled_discovery_test.go`: (a) `include_disabled` absent/false → response byte-identical to baseline fixture (FR-002/SC-001); (b) flag true → callable results first in existing order, `disabled[]` after, cap `min(limit,10)` enforced (FR-006/SC-004); (c) `remediation` map contains exactly the present statuses (FR-005); (d) agent-scope filter applied before classification — locked tools on inaccessible servers absent (FR-007).
- [X] T007 [US1] Add `include_disabled` bool param (default false) to the `retrieve_tools` tool registration + one-sentence description (FR-001/FR-014) in `internal/server/mcp.go`.
- [X] T008 [US1] In `handleRetrieveToolsWithMode` (`internal/server/mcp.go`): in the result loop, when `isToolCallable` is false, always count it; when `include_disabled` is true, classify via `ClassifyDisabledTool` and append a `LockedToolEntry` to a `disabledResults` slice (after applying the existing agent-scope check first). Append `disabledResults` after callable results, capped at `min(limit,10)`; build the `remediation` map from present statuses only. Make T006 green.
- [X] T009 [US1] Add an in-memory `include_disabled` usage counter (spec-042 style, no persistence) in `internal/server/mcp.go`; assert via test it increments only when the flag is true (FR-013).

**Checkpoint**: US1 independently shippable — agents can opt into seeing locked tools with correct remediation.

---

## Phase 4: User Story 2 — Agent nudged toward opt-in when blocked (P2)

**Goal**: status-aware rejection points to the opt-in path; zero-callable-result nudge.

**Independent test**: quickstart.md §4 (config-denied call rejection distinct + pointer) and §5 (0-callable query returns one-line count nudge, no inline entries).

- [X] T010 [US2] Write failing test in `internal/server/mcp_disabled_discovery_test.go`: (a) calling a config-denied tool returns the operator-policy message AND the `include_disabled:true` pointer, distinct from the user-disabled message (extends existing `TestBlockedToolMessageFor`); (b) a query with 0 callable matches but ≥1 locked match yields a one-line "N … retry with include_disabled:true" note in the result text and NO inline locked entries (FR-009).
- [X] T011 [US2] Extend `blockedToolMessageFor` in `internal/server/mcp.go` to append the `retrieve_tools include_disabled:true` pointer for the config/user/pending branches (the status-aware split itself shipped in #468). Keep the legacy substring "Tool is disabled and not callable." in the non-config branch for back-compat.
- [X] T012 [US2] In `handleRetrieveToolsWithMode`: when callable results == 0 and the always-on dropped-counter > 0 and `include_disabled` is false, append the one-line count nudge to the result text (count only). Make T010 green.

**Checkpoint**: an agent ignorant of the flag still reliably finds it.

---

## Phase 5: User Story 3 — Servers expose hidden-capability counts (P3)

**Goal**: `upstream_servers` list/get carries conditional per-server tool counts.

**Independent test**: quickstart.md §1 — `everything` has a `tools` block with `disabled_by_config>=1` and `disabled_by_user>=1`; a fully-callable server has no `tools` block.

- [X] T013 [US3] Write failing test in `internal/server/mcp_disabled_discovery_test.go`: server with ≥1 non-callable tool → entry has `ServerToolCounts` with zero reasons omitted; fully-callable server → no `tools` field at all (FR-010/SC-005).
- [X] T014 [US3] In the `upstream_servers` list/get handler (`internal/server/mcp.go`): walk the StateView snapshot tools per server (reuse the `getVisibleToolCount` traversal), tally callable + per-status via `ClassifyDisabledTool`, attach `*ServerToolCounts` only when a non-callable count > 0, omit zero sub-keys. Make T013 green.

**Checkpoint**: all three stories complete and independently verifiable.

---

## Phase 6: Polish & Cross-Cutting

- [ ] T015 [P] Regenerate OpenAPI: run the generator, then `./scripts/verify-oas-coverage.sh`; commit `oas/swagger.yaml` + `oas/docs.go` for the new `include_disabled` param and `disabled`/`remediation`/`tools` shapes.
- [ ] T016 [P] Docs: add a one-line note to the CLAUDE.md built-in-tools section and `docs/` (code_execution / MCP reference) describing `include_disabled` and the 5 statuses (Constitution VI).
- [ ] T017 Run full local verification per `quickstart.md` §0–§6 (build, curl, live MCP) and record pass/fail inline in quickstart.md; then `go test ./internal/runtime/ ./internal/server/ -run 'ClassifyDisabledTool|DisabledDiscovery|BlockedToolMessage' -count=1`, `./scripts/test-api-e2e.sh`, `golangci-lint` clean.
- [ ] T018 Open PR from `049-agent-discoverable-disabled-tools` → `main` (squash; commit policy: no Co-Authored-By, author = human; `Related #468`); ensure CI green before requesting merge.

---

## Dependencies & Execution Order

- **Setup (T001)** → **Foundational (T002–T005)** → user stories.
- **US1 (T006–T009)** depends only on Foundational. **MVP = Phase 1+2+3.**
- **US2 (T010–T012)** depends on Foundational; T012 depends on the dropped-counter introduced in T008 (US1) — implement US1 before US2.
- **US3 (T013–T014)** depends only on Foundational (uses `ClassifyDisabledTool`); independent of US1/US2 and may proceed in parallel with US2 after US1.
- **Polish (T015–T018)** after all targeted stories.

## Parallel Opportunities

- T002 [P] (types) parallel with T003 (classifier test) — different files.
- After Foundational: US3 (T013–T014) [P] with US2 (T010–T012) — different handlers/sections, both reuse the frozen classifier.
- T015 [P] and T016 [P] in Polish — different files.

## Implementation Strategy

Ship **US1 as the MVP** (Phases 1–3): it alone delivers the core value (agents can discover locked tools with correct remediation). US2 and US3 are incremental hardening/efficiency layers added after, each independently testable via its quickstart section.
