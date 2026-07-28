# Tasks: Security Scanner Web UI + Trust-Mode Controls

**Input**: Design documents from `/specs/088-scanner-trust-ui/`
**Prerequisites**: plan.md, spec.md (Codex-approved), research.md (D1–D10), data-model.md, contracts/consumed-api.md, quickstart.md

**Tests**: Constitution mandates TDD — every slice starts with a failing vitest spec in `frontend/tests/unit/*.spec.ts` (the ONLY path vitest runs). Component/store logic lives in pure utils wherever possible so tests don't mount the 3k-line ServerDetail.

**Organization**: Phases 3–7 map 1:1 to spec user stories US1–US5; each is independently shippable.

## Phase 1: Setup

- [ ] T001 Verify baseline: `cd frontend && npm test` green on branch `088-scanner-trust-ui`; confirm generated `frontend/src/types/contracts.ts` already carries `Tool.held_reason/held_verdict/held_signals` and `Server.trust_mode` (regen via `go run ./cmd/generate-types` ONLY if missing — none expected)

## Phase 2: Foundational (blocking prerequisites for multiple stories)

- [ ] T002 [P] Write failing table-driven spec `frontend/tests/unit/trust-mode-derivation.spec.ts`: effectiveTrustMode() over unset/''/auto/scan/manual/'bogus'/'Scan' (case-sensitive invalid→manual), isDefault/isInvalid flags, per-mode copy table covers BOTH tool-change and admission behavior (research D7, spec FR-001/FR-002)
- [ ] T003 [P] Implement `frontend/src/utils/trustMode.ts` (TrustModeState derivation + mode metadata: label, description, admission note, warning flag for auto) to pass T002
- [ ] T004 [P] Write failing spec `frontend/tests/unit/hold-evidence.spec.ts`: TPA-id extraction `/^tpa\.(TPA-\d{4}-\d{4})\./`, TPA-first ordering, dedupe, display cap collapses heuristics only, "+N more" counts received-but-collapsed only, scan_coverage+clean → precaution (never threat styling), empty reason → no evidence (research D3, FR-008/FR-009)
- [ ] T005 [P] Implement `frontend/src/utils/holdEvidence.ts` (parse/order/present HoldEvidence per data-model.md) to pass T004
- [ ] T006 Switch approvals data source in `frontend/src/services/api.ts`: `getToolApprovals()` → `GET /api/v1/servers/{id}/tools` (encodeURIComponent id; export endpoint lacks held_* — research D1, contracts/consumed-api.md); update `frontend/src/types/api.ts` ToolApproval to the contracts.Tool field shape (held_reason/held_verdict/held_signals, approval_status) and fix compile fallout in consumers (ServerDetail.vue, Dashboard.vue field mapping) — update existing specs `frontend/tests/unit/quarantine-*.spec.ts` fixtures to the new payload shape, suite green

**Checkpoint**: utils + data source ready — user stories can start (US1 needs T002-T003; US2 needs T004-T006)

## Phase 3: User Story 1 — Trust-mode control from the Web UI (P1) 🎯 MVP

**Goal**: View/change per-server trust_mode (tri-mode selector) in ServerDetail + add-server form + server list badge; invalid values surfaced; auto-mode warning; UI writes trust_mode only.

**Independent Test**: On a server detail page flip through all three modes and verify persistence on the list view without CLI/config-file access (spec US1).

- [ ] T007 [P] [US1] Write failing spec `frontend/tests/unit/trust-mode-selector.spec.ts`: renders 3 options with dual-behavior copy, manual default when unset, raw+effective note for invalid values (`data-test=trust-mode-invalid-note`), auto selection requires confirm (emits only after warning ack), emits `update` with chosen mode only
- [ ] T008 [US1] Create `frontend/src/components/TrustModeSelector.vue` (radio-card group, `data-test=trust-mode-selector` / `trust-mode-option-{auto|scan|manual}`, uses utils/trustMode.ts; auto-warning confirm modal covering unscanned changes + unscanned admission) to pass T007
- [ ] T009 [US1] Replace legacy auto-approve card in `frontend/src/views/ServerDetail.vue` (~714-760) with TrustModeSelector: PATCH `{trust_mode}` via api.patchServer, surface `restart_required` notice, never send auto_approve_tool_changes; update `frontend/tests/unit/server-detail-auto-approve.spec.ts` → selector-based expectations (FR-004/FR-005)
- [ ] T010 [P] [US1] Write failing spec `frontend/tests/unit/add-server-trust-mode.spec.ts`: add form shows selector (manual preselected), POST body carries `trust_mode` and OMITS `quarantined`, no independent quarantine checkbox remains on the manual-entry tab (FR-006, research D6)
- [ ] T011 [US1] Replace quarantined checkbox with TrustModeSelector in `frontend/src/components/AddServerModal.vue` (~175-199, 582-592, 804-807); Import tab/registry flows untouched; to pass T010
- [ ] T012 [P] [US1] Add trust-mode badge to server table in `frontend/src/views/Servers.vue` (compact label auto/scan/manual from utils/trustMode.ts, `data-test=server-trust-mode`) (FR-007)
- [ ] T013 [P] [US1] Replace deprecated skip_quarantine hint in `frontend/src/views/ServerDetail.vue` (~350-354) with trust-mode guidance pointing at the Configuration-tab selector (FR-021, research D9)

**Checkpoint**: US1 fully functional — MVP shippable

## Phase 4: User Story 2 — Held-tool evidence with TPA ids (P1)

**Goal**: Hold reason/verdict/signals (TPA-first) rendered on quarantine panel, diff dialog, global Tools page; best-effort report link.

**Independent Test**: Seed a TPA-poisoned tool change (quickstart scenario 2), verify evidence on tools view + diff dialog and report reachability, all UI-only (spec US2).

- [ ] T014 [P] [US2] Write failing spec `frontend/tests/unit/hold-evidence-badge.spec.ts`: HoldEvidenceBadge renders reason pill (scan_findings vs scan_coverage distinct styling), verdict badge, TPA chips before heuristic chips, +N overflow, report-link emit when evidence has TPA ids, nothing rendered when reason empty (FR-008/FR-009/FR-012)
- [ ] T015 [US2] Create `frontend/src/components/HoldEvidenceBadge.vue` (`data-test=hold-evidence-badge` / `hold-tpa-chip`; props: evidence + optional report path; uses utils/holdEvidence.ts) to pass T014
- [ ] T016 [US2] Integrate HoldEvidenceBadge into ServerDetail tool-quarantine panel (per-tool rows ~388 area) and the changed-tool diff sections; diff dialog also shows evidence from the diff endpoint payload (FR-010); evidence link → server's latest scan report route with `?signal=` ids (research D4), degrade to Run-scan CTA when `security_scan.status=='not_scanned'` (FR-011)
- [ ] T017 [P] [US2] Add held-evidence rendering to the global Tools page `frontend/src/views/Tools.vue` held/changed rows (compact: reason icon + first TPA id + count) (FR-008)
- [ ] T018 [P] [US2] Accept `?signal=` query in `frontend/src/views/ScanReport.vue`: highlight findings whose `signals[]` intersect the passed ids (best-effort, no claim when no match) (FR-011); extend `frontend/tests/unit/scan-report-route.spec.ts`

**Checkpoint**: US1+US2 = both P1 stories complete

## Phase 5: User Story 3 — Quarantine banner clarity (P2)

**Goal**: 4-state banner (scan-running / scan-failed / scan-blocked / manual-review) from payload-only facts, with scan summary + report path + scan CTA.

**Independent Test**: Seed each of the four states (quickstart scenario 3) and verify distinct banner copy/actions (spec US3).

- [ ] T019 [P] [US3] Write failing spec `frontend/tests/unit/quarantine-banner-states.spec.ts`: derivation table from data-model.md (priority order: running > failed > blocked > manual), copy never promises auto-approval nor claims admission provenance, failed never styled as threat, no-summary case offers scan CTA (FR-013/FR-014/FR-015)
- [ ] T020 [US3] Implement banner-state derivation in `frontend/src/utils/trustMode.ts` (or `utils/quarantineBanner.ts` if cleaner) + rework the Security-Quarantine banner block in `frontend/src/views/ServerDetail.vue` (~205-217): `data-test=quarantine-banner-state`, verdict/risk/counts row from `security_scan`, View-report link, Retry-scan action on failed, Run-scan CTA on not_scanned; to pass T019

## Phase 6: User Story 4 — Baseline scan visibility + deep-scan setting (P2)

**Goal**: Security tab + Scan Now available with zero deep scanners; skipped scanners shown as skipped; Settings toggle for deep scan.

**Independent Test**: Default install (no deep scanners): run a scan from the Security tab and see results; enable deep scan from Settings (spec US4).

- [ ] T021 [P] [US4] Write failing spec `frontend/tests/unit/security-tab-ungate.spec.ts`: Security tab rendered when `hasEnabledScanners()` is false; Scan Now enabled without Docker; skipped deep scanners render as "skipped" not error (FR-016/FR-017)
- [ ] T022 [US4] Un-gate Security tab in `frontend/src/views/ServerDetail.vue` (~286 v-if + Scan Now docker-disable + tab status dot logic); verify `deep_scan.skipped_scanners` presentation path; to pass T021
- [ ] T023 [P] [US4] Add `security.deep_scan.enabled` toggle FieldDef to securityFields in `frontend/src/views/settings/fields.ts` (`data-test=deep-scan-toggle`, help text: adds Docker-based deep scanners on top of the always-on offline baseline; no restart flag — hot-reloadable) and update Security.vue info alert (~36-44) to point at Settings instead of raw config key (FR-018)

## Phase 7: User Story 5 — Live updates (P3)

**Goal**: ServerDetail refreshes scan summary/banner/approvals on `mcpproxy:scan-settled` + `mcpproxy:servers-changed`; SSE loss regresses nothing.

**Independent Test**: With the page open, run a scan + approve tools via CLI; page reflects both within seconds without reload (spec US5).

- [ ] T024 [P] [US5] Write failing spec `frontend/tests/unit/server-detail-sse-refresh.spec.ts`: scan-settled for matching server → refetch scan summary + approvals (event payload lacks verdict → must refetch, not trust payload); servers-changed → refetch approvals; non-matching server_name ignored; listener cleanup on unmount; no event stream → existing polling path untouched (FR-019/FR-020, research D5)
- [ ] T025 [US5] Add scoped window-event listeners in `frontend/src/views/ServerDetail.vue` (mounted/unmounted, alongside existing MCP-2740 reconcile watch) to pass T024

## Phase 8: Polish & Cross-Cutting

- [ ] T026 [P] Add trust-mode UI + hold-evidence + banner-states section to `docs/features/security-quarantine.md` (086 shipped no docs; constitution VI)
- [ ] T027 Full gates: `cd frontend && npm test` (all suites incl. updated fixtures green), `make build` (embed refresh — stale-embed gotcha), `./scripts/test-api-e2e.sh`, `/opt/homebrew/bin/golangci-lint run --config .github/.golangci.yml ./...` (expected no-op)
- [ ] T028 Playwright verification sweep per `docs/development/web-ui-verification.md` over quickstart scenarios 1-5 (port 18081, throwaway data-dir, `[data-test=...]` locators, domcontentloaded); HTML report → `specs/088-scanner-trust-ui/verification/` (required — frontend touched)

## Dependencies & Execution Order

- Phase 1 → Phase 2 → stories. US1 needs T002-T003 only; US2 needs T004-T006; US3 needs T002-T003 (+banner util); US4 independent after Phase 1; US5 benefits from T006 (approvals refetch path) but is otherwise independent.
- Story order = priority order (US1 → US2 → US3 → US4 → US5); US1 alone = MVP. Stories are independently shippable; cross-story file contention is confined to ServerDetail.vue (T009/T013/T016/T020/T022/T025 touch it — execute those sequentially).
- Parallel opportunities: within Phase 2 (T002-T003 ∥ T004-T005), and every [P]-marked test-writing task can precede its implementation partner while other stories' tasks run.

## Implementation Strategy

MVP = Phase 1-3 (US1). Then US2 completes the P1 pair; US3/US4/US5 are incremental. Commit per task or per coherent pair (test+impl), run the frontend suite at every checkpoint; PR after Phase 8 gates.
