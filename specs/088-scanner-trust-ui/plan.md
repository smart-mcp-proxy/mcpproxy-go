# Implementation Plan: Security Scanner Web UI + Trust-Mode Controls

**Branch**: `088-scanner-trust-ui` | **Date**: 2026-07-28 | **Spec**: [spec.md](./spec.md)
**Input**: Feature specification from `/specs/088-scanner-trust-ui/spec.md`

## Summary

Frontend-only feature closing the spec-086 Web UI debt: a tri-mode trust selector (auto | scan | manual) replacing the legacy binary auto-approve toggle and the add-form quarantine checkbox, scan-hold evidence rendering (held_reason / held_verdict / held_signals with TPA-first ordering), a 4-state quarantine banner derived from client-visible payload facts, un-gating the per-server Security tab from deep-scanner presence, a Settings toggle for `security.deep_scan.enabled`, and SSE-driven refresh of scan/approval state. Zero new backend endpoints; the entire read/write surface shipped with spec 086 (PR #919).

## Technical Context

**Language/Version**: TypeScript 5.9, Vue 3.5 (Composition API, `<script setup>`), Tailwind v4
**Primary Dependencies**: Existing only — hand-written `ApiClient` singleton (`frontend/src/services/api.ts`), Pinia stores (`stores/servers.ts`, `stores/system.ts`), generated `types/contracts.ts` (via `cmd/generate-types`), hand-written `types/api.ts`. **No new npm dependency.**
**Storage**: N/A (all state server-side; UI consumes REST + SSE)
**Testing**: vitest + jsdom; include glob is `tests/**/*.spec.ts` — new specs go in `frontend/tests/unit/` by convention; files under `src/**/__tests__` are NOT picked up. Playwright verification sweep per `docs/development/web-ui-verification.md` (port 18081 + throwaway data-dir, `[data-test=...]` locators, `domcontentloaded`).
**Target Platform**: Embedded Web UI (`make build` embeds `frontend/dist` into the Go binary)
**Project Type**: Web frontend within existing Go monorepo
**Performance Goals**: No new polling loops; reuse the single EventSource; SSE-driven refresh ≤1 fetch per event per surface
**Constraints**: Zero new backend endpoints. If a payload field addition becomes unavoidable: edit `cmd/generate-types/main.go` + regen `contracts.ts` + `make swagger` (project checklist). Legacy config fields (`auto_approve_tool_changes`, `skip_quarantine`) are never written by the UI.
**Scale/Scope**: ~6 touched frontend files + 2 new components + ~8 new unit-test specs; 1 line of Go touched at most (none expected)

## Constitution Check

*GATE: evaluated pre-Phase-0 and re-checked post-design — PASS, no violations.*

| Principle | Verdict | Notes |
|-----------|---------|-------|
| I. Performance at Scale | PASS | No search/indexing changes. SSE-driven refresh replaces nothing with polling; ServerDetail keeps its existing scan-status poll only while a scan runs. |
| II. Actor-Based Concurrency | N/A | No Go concurrency touched. |
| III. Configuration-Driven | PASS | UI writes `trust_mode` via the existing PATCH; deep-scan toggle drives the existing hot-reloadable `security.deep_scan.enabled` config path. Tray untouched. |
| IV. Security by Default | PASS | Manual stays the preselected default in add flows; quarantine is *derived* from trust mode (removing the contradictory checkbox strengthens the default posture); rug-pull warning on `auto`. |
| V. TDD | PASS | Failing vitest specs precede each slice (details in tasks.md); existing suite is the regression gate (SC-007). |
| VI. Documentation Hygiene | PASS | `docs/features/security-quarantine.md` gains the trust-mode UI section (086 shipped none); README screenshot/table untouched unless flows change. |
| Core+Tray split / Event-driven / DDD / 3-layer upstream | PASS | Frontend-only; US5 is exactly the constitution's event-driven rule applied to the server page. |

## Project Structure

### Documentation (this feature)

```text
specs/088-scanner-trust-ui/
├── spec.md              # Codex-approved (4 rounds)
├── plan.md              # This file
├── research.md          # Phase 0: consolidated decisions (sources: understand-workflow map + Codex review)
├── data-model.md        # Phase 1: view-model entities + banner state derivation table
├── contracts/
│   └── consumed-api.md  # Phase 1: existing endpoints/events consumed (no new API)
├── quickstart.md        # Phase 1: build/run/verify recipe
├── checklists/requirements.md
└── tasks.md             # Phase 2 (/speckit.tasks — not created here)
```

### Source Code (repository root)

```text
frontend/src/
├── components/
│   ├── TrustModeSelector.vue        # NEW: tri-mode radio-card selector + per-mode copy + auto-warning modal hook
│   ├── HoldEvidenceBadge.vue        # NEW: reason pill + verdict badge + TPA-first signal chips (+N more)
│   ├── AddServerModal.vue           # quarantined checkbox → TrustModeSelector (manual default)
│   └── settings/                    # (rendered by existing SettingsSection.vue — no changes)
├── views/
│   ├── ServerDetail.vue             # selector replaces auto-approve card (~714-760); banner states (~205);
│   │                                # Security tab un-gate (~286); quarantine panel + diff dialog evidence;
│   │                                # skip_quarantine hint replacement (~350-354); SSE listeners
│   ├── Servers.vue                  # trust-mode column/badge in server table
│   ├── ScanReport.vue               # accept ?signal= query to highlight matching finding (best-effort)
│   └── settings/fields.ts           # + security.deep_scan.enabled toggle in securityFields
├── services/api.ts                  # getToolApprovals: export (record source) + /servers/{id}/tools
│                                    # (held_* enrichment) joined client-side; trust_mode PATCH typing
├── stores/servers.ts                # expose per-server trust_mode (already in payload via contracts)
├── types/api.ts                     # ToolApproval + held_* fields (hand-written type catches up to payload)
└── utils/
    ├── trustMode.ts                 # NEW: effective-mode derivation (invalid → manual), mode copy table
    └── holdEvidence.ts              # NEW: TPA-id detection/ordering, reason/verdict presentation mapping

frontend/tests/unit/
├── trust-mode-selector.spec.ts      # NEW
├── trust-mode-derivation.spec.ts    # NEW (unset/invalid/legacy/explicit table)
├── hold-evidence.spec.ts            # NEW (ordering, cap, coverage-vs-findings, stale-clear)
├── quarantine-banner-states.spec.ts # NEW (4 states + no-report CTA)
├── security-tab-ungate.spec.ts      # NEW (zero-scanner install)
├── server-detail-sse-refresh.spec.ts# NEW (scan-settled + servers-changed, stream loss)
├── add-server-trust-mode.spec.ts    # NEW (derived quarantine, manual default)
└── server-detail-auto-approve.spec.ts # UPDATED (toggle → selector)
```

**Structure Decision**: Pure frontend feature inside the existing `frontend/` app; two new leaf components + two pure-function util modules keep `ServerDetail.vue` growth minimal and unit-testable without mounting the 3k-line view.

## Phase 0 → research.md (complete)

All unknowns were resolved before planning via a 4-agent codebase-mapping workflow and a 4-round Codex cross-review of the spec; `research.md` records the decisions (export-as-record-source + held-evidence join from `/servers/{id}/tools`, banner-state derivation limits, capped-evidence display scope, best-effort report linking, SSE event coverage, add-form control replacement).

## Phase 1 → data-model.md, contracts/consumed-api.md, quickstart.md (complete)

- `data-model.md`: view-model entities (TrustModeState, HoldEvidence, QuarantineBannerState, ScanOutcomeSummary) with the exact derivation table for the 4 banner states and the effective-trust-mode rules.
- `contracts/consumed-api.md`: inventory of consumed endpoints/fields/events with "already shipped" provenance (086) — the contract this feature must not break, and the explicit note that `/tools/export` is NOT a valid evidence source.
- `quickstart.md`: build + isolated-instance run + seeded quarantine/held-tool scenarios + Playwright sweep instructions.

## Complexity Tracking

No constitution violations; table not required.
