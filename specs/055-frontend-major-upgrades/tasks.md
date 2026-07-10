# Tasks: Frontend Major-Dependency Migration

**Feature**: 055-frontend-major-upgrades | **Branch**: `055-frontend-major-upgrades`
**Input**: plan.md, research.md, quickstart.md, spec.md

All paths relative to repo root `/Users/user/repos/mcpproxy-go`. Frontend sources under `frontend/`.

## Phase 0: Baseline (MUST precede all dependency changes)

- [x] T001 Capture pre-migration Playwright baseline of all main web-UI views into `specs/055-frontend-major-upgrades/verification/baseline/` and save the reusable sweep as `specs/055-frontend-major-upgrades/verification/baseline.spec.ts` (build current binary, serve throwaway mcpproxy, screenshot Servers/Global Tools/Activity/Quarantine/Settings + wizard). **Blocks every task that edits `frontend/`.**

## Phase 1: Setup (dependency + config migration)

- [x] T002 Update `frontend/package.json`: bump `tailwindcss@^4`, add `@tailwindcss/postcss@^4`, `daisyui@^5`, `vite@^8`, `typescript@^6`, `vue-tsc@^3`, Vite-8-compatible `@vitejs/plugin-vue`, v4-compatible `@tailwindcss/typography`; raise `engines.node` to `>=22.18.0`; keep remaining #498 minor/patch bumps.
- [x] T003 [P] Update `frontend/.nvmrc` from `18` to `22`.
- [x] T004 Regenerate lockfile: `cd frontend && rm -rf node_modules package-lock.json && npm install` (resolve any peer-dep conflicts by bumping the conflicting package to its Vite-8 major, e.g. Vitest/@vitest/*).

## Phase 2: Foundational (config rewrite — blocks the build)

- [x] T005 Rewrite `frontend/postcss.config.cjs` to `{ plugins: { '@tailwindcss/postcss': {} } }` (drop `tailwindcss` + `autoprefixer`).
- [x] T006 Rewrite the head of `frontend/src/assets/main.css`: replace the three `@tailwind` directives with `@import "tailwindcss";`, `@plugin "@tailwindcss/typography";`, and the `@plugin "daisyui" { themes: light --default, dark --prefersdark, ... sunset; }` block (all 32 themes per research.md Decision 2). Leave the `@layer`/custom CSS below unchanged.
- [x] T007 [P] Delete `frontend/tailwind.config.cjs` (content auto-detected; themes/plugins now in CSS).
- [x] T008 [P] Remove `"baseUrl": "."` from `frontend/tsconfig.json`; keep `paths: { "@/*": ["./src/*"] }`.

## Phase 3: User Story 1 — UI builds and renders identically (P1)

**Goal**: `npm run build` is green and every view/DaisyUI component renders as before.
**Independent test**: `cd frontend && npm run build` exits 0; serve rebuilt UI and sweep all views — no visual regression vs. baseline.

- [x] T009 [US1] Add `@reference "@/assets/main.css";` to the top of the scoped `<style>` block in `frontend/src/components/JsonViewer.vue` (before its `@apply` rules) so v4 resolves `@apply bg-base-200` etc.
- [x] T010 [P] [US1] Rename removed v3 utility `flex-shrink-0` → `shrink-0` across all `frontend/src/**/*.vue` (class strings only; leave scoped-CSS `flex-shrink:` declarations).
- [x] T011 [P] [US1] Convert `bg-opacity-50` (and any `text-opacity-*`/`border-opacity-*`) to slash-opacity form in affected files (e.g. `frontend/src/components/AuthErrorModal.vue` `bg-black bg-opacity-50` → `bg-black/50`).
- [x] T012 [US1] Run `cd frontend && npm run build`; fix any `vue-tsc` (TS6), PostCSS (Tailwind v4), or Vite 8 errors until it exits 0 and writes `frontend/dist`.
- [x] T013 [US1] Re-grep build output / browser console for Tailwind v4 "unknown utility" warnings and DaisyUI v5 class drift; apply remaining utility renames (`overflow-ellipsis`→`text-ellipsis`, `decoration-clone`→`box-decoration-clone`, `shadow-sm`→`shadow-xs`, etc.) as needed.

## Phase 4: User Story 2 — Binary embeds new build, both editions build (P1)

**Goal**: `make build` and `make build-server` produce working binaries serving the upgraded UI.
**Independent test**: both build commands succeed; the binary serves the rebuilt UI at `/ui/`.

- [x] T014 [US2] Run `make build` (runs `frontend-build` → copies `frontend/dist` to `web/frontend/dist` → re-embeds); confirm a working `mcpproxy` binary.
- [x] T015 [US2] Run `go build -tags server ./cmd/mcpproxy`; confirm the server edition builds unaffected (FR-007: no Go change).
- [x] T016 [US2] Stand up the rebuilt binary on a throwaway data-dir and confirm `/ui/` serves the upgraded bundle (assets load under the `/ui/` base, no 404s) — validates the Vite 8 asset-path contract.

## Phase 5: User Story 3 — Frontend tests + CI green (P2)

**Goal**: Vitest suite passes under the new toolchain; CI frontend jobs green.
**Independent test**: `npm run test` green locally; `Build Frontend`/`frontend-test` green on the branch.

- [x] T017 [US3] Run `cd frontend && npm run test` (Vitest); fix any Vite-8/TS-6 incompatibility (bump Vitest major if required) until green.
- [x] T018 [P] [US3] Verify CI Node version in `.github/workflows/` satisfies `>=22.18`; bump the `Build Frontend`/`frontend-test` job Node version if it pins an older release.

## Phase 6: Verification & Polish (cross-cutting)

- [x] T019 Re-run `verification/baseline.spec.ts` against the migrated, rebuilt binary; write screenshots to `specs/055-frontend-major-upgrades/verification/after/`.
- [x] T020 Diff `after/` vs `baseline/` view-by-view (SC-003) and check the browser console for new errors (SC-004); fix any regression and re-run.
- [x] T021 [P] frontend-design polish pass: confirm DaisyUI v5 components (buttons, cards, badges, modals, tabs, toggles, dropdowns) render on par with or better than the old UI across light + dark themes; address any v5 spacing/token drift.
- [x] T022 [P] QA test plan + Playwright scripts covering main use cases (theme switch, navigation, wizard/add-server, activity filters, server detail) saved under `specs/055-frontend-major-upgrades/verification/`.
- [x] T023 Compose self-contained rich HTML report at `specs/055-frontend-major-upgrades/verification/report.html` (before/after pairs, QA results, pass/fail summary).
- [ ] T024 [P] Update `CLAUDE.md` frontend-stack notes (Tailwind v4 / DaisyUI v5 / Vite 8 / TS 6) and `frontend/README.md` dev-setup if commands changed.

## Dependencies & Execution Order

- **T001 (baseline) blocks everything** that edits `frontend/`.
- Phase 1 (T002–T004) → Phase 2 (T005–T008) → US1 build (T009–T013) are sequential on the build.
- US2 (T014–T016) depends on a green US1 build.
- US3 (T017–T018) depends on the migrated package set (T004) and can run after T012.
- Verification (T019–T020) depends on US2. Polish (T021–T024) after verification.

## Parallel Opportunities

- T003 ∥ T002 prep; T007 ∥ T008 (different files); T010 ∥ T011 (different files); T021/T022/T024 in Phase 6.
- The QA-script authoring (T022) and frontend-design polish (T021) can be delegated to parallel subagents once the build is green.

## MVP Scope

User Story 1 + User Story 2 (green `npm run build` + `make build`/`make build-server` serving the upgraded UI with zero visual regression) is the releasable MVP. US3 closes the CI/Dependabot loop.
