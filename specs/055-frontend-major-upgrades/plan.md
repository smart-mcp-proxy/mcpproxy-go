# Implementation Plan: Frontend Major-Dependency Migration

**Branch**: `055-frontend-major-upgrades` | **Date**: 2026-05-25 | **Spec**: [spec.md](./spec.md)
**Input**: Feature specification from `/specs/055-frontend-major-upgrades/spec.md`

## Summary

Migrate the embedded Vue 3 web UI (`frontend/`) across four bundled major upgrades from the closed Dependabot PR #498 — Tailwind CSS 3→4, DaisyUI 4→5, Vite 5→8, TypeScript 5→6 (plus `vue-tsc` 2→3) — so `npm run build` and both `make build` / `make build-server` succeed and the UI renders with **zero visual regression** against the pre-migration baseline. No Go/backend code changes (FR-007); only frontend sources, config, lockfile, and the re-embedded `web/frontend/dist` bytes change.

## Technical Context

**Language/Version**: TypeScript 6 / Vue 3.5; Node ≥22.18 (raised from 18). Go 1.24 only re-embeds the built bytes — unchanged.
**Primary Dependencies**: `tailwindcss@4` + `@tailwindcss/postcss`, `daisyui@5`, `vite@8`, `typescript@6`, `vue-tsc@3`, `@vitejs/plugin-vue` (version that pairs with Vite 8), `@tailwindcss/typography` (v4-compatible).
**Storage**: N/A — pure presentation layer; no data entities, no DB, no API contract changes.
**Testing**: `vue-tsc` type-check + `vite build` (must exit 0); Vitest unit suite under `frontend/tests/`; Playwright screenshot sweep for visual parity vs. baseline.
**Target Platform**: Static bundle served at base `/ui/`, embedded into the Go binary via `//go:embed all:frontend/dist` (copied to `web/frontend/dist` by the Makefile).
**Project Type**: Web (frontend only; backend untouched).
**Performance Goals**: Build output served identically — same routes, same `/ui/` base path, same asset-loading contract.
**Constraints**: Visual parity is the hard gate (SC-003). No backend behavior change (FR-007). CI Node must satisfy new engines.
**Scale/Scope**: ~30 `.vue` views/components, one CSS entry (`src/assets/main.css`), 32 DaisyUI themes, 4 config files (`tailwind.config.cjs`, `postcss.config.cjs`, `tsconfig.json`, `vite.config.ts`), `.nvmrc`, `package.json`.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Status | Notes |
|-----------|--------|-------|
| I. Performance at Scale | ✅ N/A | No backend/search/index change. |
| II. Actor-Based Concurrency | ✅ N/A | No Go concurrency code touched. |
| III. Configuration-Driven Architecture | ✅ Pass | Tray/core contract unchanged; UI still reads state via REST. |
| IV. Security by Default | ✅ Pass | No auth/quarantine/binding change. Dependency upgrade *reduces* advisory exposure. |
| V. Test-Driven Development | ⚠️ Adapted | A frontend migration has no new logic to red-green. "Test-first" is satisfied by capturing the **pre-migration Playwright baseline before touching deps**, then re-running the same sweep as the regression gate. Existing Vitest suite must stay green. |
| VI. Documentation Hygiene | ✅ Pass | Update `CLAUDE.md` frontend-stack notes if commands change; `frontend/README.md` if dev setup changes. No README user-facing change. |

**Gate result**: PASS. No violations → Complexity Tracking not required.

## Project Structure

### Documentation (this feature)

```text
specs/055-frontend-major-upgrades/
├── spec.md
├── plan.md              # This file
├── research.md          # Phase 0 — per-major migration decisions
├── quickstart.md        # Phase 1 — the migration runbook + verification
├── checklists/
│   └── requirements.md
└── verification/
    ├── baseline/        # Pre-migration screenshots (captured first)
    ├── after/           # Post-migration screenshots
    ├── baseline.spec.ts # Reusable Playwright sweep
    └── report.html      # Final rich report
```

`data-model.md` and `contracts/` are intentionally omitted — this feature introduces no data entities and no API/contract surface (FR-007). Recorded here so the absence is deliberate, not an oversight.

### Source Code (repository root)

```text
frontend/
├── package.json            # bump 4 majors + engines node>=22.18
├── package-lock.json       # regenerated
├── .nvmrc                  # 18 → 22
├── postcss.config.cjs      # tailwindcss/autoprefixer → @tailwindcss/postcss
├── tailwind.config.cjs     # → removed/minimized (v4 is CSS-first)
├── tsconfig.json           # drop baseUrl (TS6 TS5101), keep paths
├── vite.config.ts          # Vite 8 / plugin-vue compatibility, base '/ui/' preserved
├── src/
│   ├── assets/main.css     # @tailwind dirs → @import "tailwindcss"; @plugin "daisyui" {...}
│   ├── components/*.vue     # utility renames (flex-shrink-0→shrink-0, bg-opacity-*→/opacity)
│   │   └── JsonViewer.vue   # @apply in scoped <style> → add @reference
│   └── views/*.vue          # utility renames
└── dist/                    # rebuilt; copied to web/frontend/dist for //go:embed

web/frontend/dist/           # embed target (Makefile copies here; re-embedded bytes only)
```

**Structure Decision**: Existing `frontend/` Vue project is retained as-is; this is an in-place dependency + config migration. The Go embed pipeline (`Makefile frontend-build` → `web/frontend/dist` → `//go:embed`) is unchanged in shape — only the embedded bytes differ.

## Phase 0 — Research

See [research.md](./research.md). Resolves the four major-version migrations, the `@apply`-in-scoped-style gotcha, deprecated-utility renames, and the Node-version bump. No open NEEDS CLARIFICATION remain — the spec's Assumptions section already fixed the ambiguous calls (themes preserved, Node bump in scope, parity judged vs. current `main`).

## Phase 1 — Design & Runbook

See [quickstart.md](./quickstart.md) for the ordered migration runbook and the verification protocol (baseline → migrate → re-sweep → diff). The verification harness (`baseline.spec.ts`) is shared between the pre- and post-migration sweeps so the comparison is apples-to-apples.

## Complexity Tracking

No constitution violations — section intentionally empty.
