# Tasks: Security Scanner Web UI + Trust-Mode Controls

**Input**: Design documents from `/specs/088-scanner-trust-ui/`
**Prerequisites**: plan.md, spec.md (Codex-approved), research.md (D1–D10), data-model.md, contracts/consumed-api.md, quickstart.md

**Tests**: Constitution mandates TDD — every slice starts with a failing vitest spec, written and observed failing BEFORE its implementation task (test/impl pairs are sequential by definition; [P] never applies within a pair). Specs live under `frontend/tests/unit/` (vitest includes `tests/**/*.spec.ts`; files under `src/**/__tests__` are NOT picked up). Component/store logic lives in pure utils wherever possible so tests don't mount the 3k-line ServerDetail.

**Organization**: Phases 3–7 map 1:1 to spec user stories US1–US5; each is independently shippable.

## Phase 1: Setup

- [x] T001 Verify baseline: `cd frontend && npm test` green on branch `088-scanner-trust-ui`; confirm generated `frontend/src/types/contracts.ts` already carries `Tool.held_reason/held_verdict/held_signals` and `Server.trust_mode` (regen via `go run ./cmd/generate-types` ONLY if missing — none expected)

## Phase 2: Foundational (blocking prerequisites for multiple stories)

- [x] T002 Write failing table-driven spec `frontend/tests/unit/trust-mode-derivation.spec.ts`: effectiveTrustMode() over unset/''/auto/scan/manual/'bogus'/'Scan' (case-sensitive invalid→manual), isDefault/isInvalid flags, per-mode copy table covers BOTH tool-change and admission behavior (research D7, spec FR-001/FR-002)
- [x] T003 Implement `frontend/src/utils/trustMode.ts` (TrustModeState derivation + mode metadata: label, description, admission note, warning flag for auto) to pass T002 — sequential after T002 fails; the T002+T003 pair runs in parallel with the T004+T005 pair
- [x] T004 [P] Write failing spec `frontend/tests/unit/hold-evidence.spec.ts`: TPA-id extraction `/^tpa\.(TPA-\d{4}-\d{4})\./` (display label = extracted id; report links carry the FULL raw signal string), TPA-first ordering, dedupe, display cap collapses heuristics only, "+N more" counts received-but-collapsed only, scan_coverage+clean → precaution (never threat styling), empty reason → no evidence (research D3, FR-008/FR-009)
- [x] T005 Implement `frontend/src/utils/holdEvidence.ts` (parse/order/present HoldEvidence per data-model.md) to pass T004 — sequential after T004 fails
- [x] T006a Write failing spec `frontend/tests/unit/tool-approvals-join.spec.ts`: joinHoldEvidence(exportRecords, toolsPayload) keeps EVERY export record (durable source — pending/blocked tools survive disconnected servers and index gaps), joins `held_reason/held_verdict/held_signals` from the tools payload by tool name (`tool_name` ↔ `name` field-name bridge), missing join partner → record unchanged (no evidence), enrichment-fetch failure (rejected /tools call) → ALL export records returned unchanged (best-effort enrichment, never `Promise.all` that drops durable records — Codex plan-review R2), and `ToolApproval` type gains optional held_* fields while the diff payload keeps its own type (list and diff contracts stay distinct — Codex plan-review F1)
- [x] T006b Implement the join in `frontend/src/utils/holdEvidence.ts` + `frontend/src/services/api.ts`: `getToolApprovals()` keeps `GET .../tools/export` as the record source AND fetches `GET /api/v1/servers/{id}/tools` in parallel solely for held_* enrichment (research D1 revised); extend `frontend/src/types/api.ts` ToolApproval with optional held_* fields; Dashboard.vue counting path unchanged; existing `frontend/tests/unit/quarantine-*.spec.ts` stay green

**Checkpoint**: utils + data source ready — user stories can start (US1 needs T002-T003; US2 needs T004-T006b)

## Phase 3: User Story 1 — Trust-mode control from the Web UI (P1) 🎯 MVP

**Goal**: View/change per-server trust_mode (tri-mode selector) in ServerDetail + add-server form + server list badge; invalid values surfaced; auto-mode warning; UI writes trust_mode only.

**Independent Test**: On a server detail page flip through all three modes and verify persistence on the list view without CLI/config-file access (spec US1).

- [x] T007 [P] [US1] Write failing spec `frontend/tests/unit/trust-mode-selector.spec.ts`: renders 3 options with dual-behavior copy, manual default when unset, raw+effective note for invalid values (`data-test=trust-mode-invalid-note`), auto selection requires confirm (emits only after warning ack), emits `update` with chosen mode only
- [x] T008 [US1] Create `frontend/src/components/TrustModeSelector.vue` (radio-card group, `data-test=trust-mode-selector` / `trust-mode-option-{auto|scan|manual}`, uses utils/trustMode.ts; auto-warning confirm modal covering unscanned changes + unscanned admission) to pass T007
- [x] T009 [US1] Replace legacy auto-approve card in `frontend/src/views/ServerDetail.vue` (~714-760) with TrustModeSelector: PATCH `{trust_mode}` via api.patchServer, surface `restart_required` notice, never send auto_approve_tool_changes; update `frontend/tests/unit/server-detail-auto-approve.spec.ts` → selector-based expectations (FR-004/FR-005)
- [x] T010 [P] [US1] Write failing spec `frontend/tests/unit/add-server-trust-mode.spec.ts`: add form shows selector (manual preselected), POST body carries `trust_mode` and OMITS `quarantined`, no independent quarantine checkbox remains on the manual-entry tab (FR-006, research D6)
- [x] T011 [US1] Replace quarantined checkbox with TrustModeSelector in `frontend/src/components/AddServerModal.vue` (~175-199, 582-592, 804-807); Import tab/registry flows untouched; to pass T010
- [x] T012 [P] [US1] Trust-mode badge in server table: first extend a failing assertion into `frontend/tests/unit/trust-mode-selector.spec.ts` (or a small `servers-view-trust-badge.spec.ts`) for the badge rendering, then add it to `frontend/src/views/Servers.vue` (compact label auto/scan/manual from utils/trustMode.ts, `data-test=server-trust-mode`) (FR-007)
- [x] T013 [P] [US1] Replace deprecated skip_quarantine hint: failing assertion first (extend `frontend/tests/unit/server-detail-auto-approve.spec.ts`: hint text contains trust-mode guidance, not skip_quarantine), then update `frontend/src/views/ServerDetail.vue` (~350-354) pointing at the Configuration-tab selector (FR-021, research D9)

**Checkpoint**: US1 fully functional — MVP shippable

## Phase 4: User Story 2 — Held-tool evidence with TPA ids (P1)

**Goal**: Hold reason/verdict/signals (TPA-first) rendered on quarantine panel, diff dialog, global Tools page; best-effort report link.

**Independent Test**: Seed a TPA-poisoned tool change (quickstart scenario 2), verify evidence on tools view + diff dialog and report reachability, all UI-only (spec US2).

- [x] T014 [P] [US2] Write failing spec `frontend/tests/unit/hold-evidence-badge.spec.ts`: HoldEvidenceBadge renders reason pill (scan_findings vs scan_coverage distinct styling), verdict badge, TPA chips before heuristic chips, +N overflow, report-link emit when evidence has TPA ids, nothing rendered when reason empty (FR-008/FR-009/FR-012)
- [x] T015 [US2] Create `frontend/src/components/HoldEvidenceBadge.vue` (`data-test=hold-evidence-badge` / `hold-tpa-chip`; props: evidence + optional report path; uses utils/holdEvidence.ts) to pass T014
- [x] T016 [US2] Integrate HoldEvidenceBadge into ServerDetail tool-quarantine panel (per-tool rows ~388 area) and the changed-tool diff sections; diff dialog also shows evidence from the diff endpoint payload (FR-010); evidence link → server's latest scan report route with repeatable `?signal=` params carrying FULL raw signal strings (never shortened TPA labels — research D4), degrade to Run-scan CTA when `security_scan` is absent from the payload (no scan has run — the field is omitted, never `not_scanned`; Codex plan-review F2) (FR-011)
- [x] T017 [P] [US2] Global Tools page evidence: write failing spec `frontend/tests/unit/tools-view-hold-evidence.spec.ts` first, extend the hand-written `GlobalTool` type in `frontend/src/types/api.ts` (~265) with optional held_* fields (the global `GET /api/v1/tools` payload already carries them), then render compact evidence (reason icon + first TPA id + count) on held/changed rows in `frontend/src/views/Tools.vue` (FR-008, Codex plan-review F4)
- [x] T018 [P] [US2] Report highlighting: failing assertions first in `frontend/tests/unit/scan-report-route.spec.ts`, then accept repeatable `?signal=` params in `frontend/src/views/ScanReport.vue` carrying FULL raw signal strings (e.g. `tpa.TPA-2026-0001.hidden_instruction`) and highlight findings whose `findings[].signals` intersect them exactly (display may shorten to the TPA id; the query never does — Codex plan-review F5); best-effort, no claim when no match (FR-011)

**Checkpoint**: US1+US2 = both P1 stories complete

## Phase 5: User Story 3 — Quarantine banner clarity (P2)

**Goal**: 4-state banner (scan-running / scan-failed / scan-blocked / manual-review) from payload-only facts, with scan summary + report path + scan CTA.

**Independent Test**: Seed each of the four states (quickstart scenario 3) and verify distinct banner copy/actions (spec US3).

- [x] T019 [P] [US3] Write failing spec `frontend/tests/unit/quarantine-banner-states.spec.ts`: derivation table from data-model.md (priority order: running > failed > blocked > manual), copy never promises auto-approval nor claims admission provenance, failed never styled as threat, absent `security_scan` field (the payload omits it when no scan has run) → scan CTA (FR-013/FR-014/FR-015)
- [x] T020 [US3] Implement banner-state derivation in `frontend/src/utils/trustMode.ts` (or `utils/quarantineBanner.ts` if cleaner) + rework the Security-Quarantine banner block in `frontend/src/views/ServerDetail.vue` (~205-217): `data-test=quarantine-banner-state`, verdict/risk/counts row from `security_scan`, View-report link, Retry-scan action on failed, Run-scan CTA when `security_scan` absent; to pass T019

## Phase 6: User Story 4 — Baseline scan visibility + deep-scan setting (P2)

**Goal**: Security tab + Scan Now available with zero deep scanners; skipped scanners shown as skipped; Settings toggle for deep scan.

**Independent Test**: Default install (no deep scanners): run a scan from the Security tab and see results; enable deep scan from Settings (spec US4).

- [x] T021 [P] [US4] Write failing spec `frontend/tests/unit/security-tab-ungate.spec.ts`: Security tab rendered when `hasEnabledScanners()` is false; Scan Now enabled without Docker; skipped deep scanners render as "skipped" not error (FR-016/FR-017)
- [x] T022 [US4] Un-gate Security tab in `frontend/src/views/ServerDetail.vue` (~286 v-if + Scan Now docker-disable + tab status dot logic); verify `deep_scan.skipped_scanners` presentation path; to pass T021
- [x] T023 [P] [US4] Deep-scan setting: failing spec first (`frontend/tests/unit/settings-deep-scan-field.spec.ts`: SECURITY_FIELDS contains a `security.deep_scan.enabled` toggle FieldDef), then add it to `SECURITY_FIELDS` in `frontend/src/views/settings/fields.ts` (~144) (`data-test=deep-scan-toggle`, help text: adds Docker-based deep scanners on top of the always-on offline baseline; hot-reloadable, no restart flag) and update Security.vue info alert (~36-44) to point at Settings instead of the raw config key (FR-018)

## Phase 7: User Story 5 — Live updates (P3)

**Goal**: ServerDetail refreshes scan summary/banner/approvals on `mcpproxy:scan-settled` + `mcpproxy:servers-changed`; SSE loss regresses nothing.

**Independent Test**: With the page open, run a scan + approve tools via CLI; page reflects both within seconds without reload (spec US5).

- [x] T024 [P] [US5] Write failing spec `frontend/tests/unit/server-detail-sse-refresh.spec.ts`: scan-settled for matching server → refetch scan summary + approvals (event payload lacks verdict → must refetch, not trust payload); servers-changed → refetch approvals; non-matching server_name ignored; listener cleanup on unmount; no event stream → existing polling path untouched (FR-019/FR-020, research D5)
- [x] T025 [US5] Add scoped window-event listeners in `frontend/src/views/ServerDetail.vue` (mounted/unmounted, alongside existing MCP-2740 reconcile watch) to pass T024

## Phase 8: Polish & Cross-Cutting

- [x] T026 [P] Add trust-mode UI + hold-evidence + banner-states section to `docs/features/security-quarantine.md` (086 shipped no docs; constitution VI)
- [x] T027 Full gates: `cd frontend && npm test` (all suites incl. updated fixtures green), `make build` (embed refresh — stale-embed gotcha), `./scripts/test-api-e2e.sh`, `/opt/homebrew/bin/golangci-lint run --config .github/.golangci.yml ./...` (expected no-op)
- [ ] T028 Playwright verification sweep per `docs/development/web-ui-verification.md` over quickstart scenarios 1-5 (port 18081, throwaway data-dir, `[data-test=...]` locators, domcontentloaded); HTML report → `specs/088-scanner-trust-ui/verification/` (required — frontend touched)

## Dependencies & Execution Order

- Phase 1 → Phase 2 → stories. US1 needs T002-T003 only; US2 needs T004-T006b; US3 needs T002-T003 (+banner util); US4 independent after Phase 1; US5 benefits from T006b (approvals refetch path) but is otherwise independent.
- Story order = priority order (US1 → US2 → US3 → US4 → US5); US1 alone = MVP. Stories are independently shippable; cross-story file contention is confined to ServerDetail.vue (T009/T013/T016/T020/T022/T025 touch it — execute those sequentially).
- Parallel opportunities: BETWEEN pairs only — the T002→T003 chain runs alongside the T004→T005→T006a→T006b chain, and [P]-marked tasks from different stories may interleave. Within every test→impl pair the failing test strictly precedes the implementation (TDD).

## Implementation Strategy

MVP = Phase 1-3 (US1). Then US2 completes the P1 pair; US3/US4/US5 are incremental. Commit per task or per coherent pair (test+impl), run the frontend suite at every checkpoint; PR after Phase 8 gates.
