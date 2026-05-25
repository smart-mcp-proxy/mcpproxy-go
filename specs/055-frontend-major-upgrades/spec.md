# Feature Specification: Frontend Major-Dependency Migration

**Feature Branch**: `055-frontend-major-upgrades`
**Created**: 2026-05-25
**Status**: Draft
**Input**: Migrate the Vue 3 web UI under `frontend/` across four bundled major upgrades that Dependabot grouped into the now-closed PR #498: `tailwindcss` 3.4→4.3, `daisyui` 4.12→5.5, `vite` 5.4→8.0, `typescript` 5.9→6.0 (plus `vue-tsc` 2→3 and `@vitejs/plugin-vue` as needed).

## Background

Dependabot's `frontend-dependencies` group (PR #498) bundled 27 updates, four of which are major-version jumps that break the build and cannot be merged as a routine bump:

- **Tailwind CSS 3 → 4** — moves the PostCSS plugin to a separate package (`@tailwindcss/postcss`) and switches from JS config (`tailwind.config.js` + `@tailwind` directives) to a CSS-first model (`@import "tailwindcss"`).
- **DaisyUI 4 → 5** — requires Tailwind v4 and registers via `@plugin "daisyui"` in CSS rather than the JS `plugins` array.
- **TypeScript 5 → 6** — makes `baseUrl` a hard error (TS5101).
- **Vite 5 → 8** — now rolldown-based; build pipeline and output format may differ.

The web UI is built with `npm run build` and the resulting `frontend/dist` is embedded into the Go binary. A broken frontend build blocks the personal-edition release.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Web UI builds and renders identically after the upgrade (Priority: P1)

A maintainer upgrades the four major frontend dependencies, rebuilds the binary, opens the web UI, and finds every page styled and functioning exactly as before — no visual regressions, no broken DaisyUI components, no console errors.

**Why this priority**: This is the entire purpose of the migration. Without it, the upgrade cannot ship and the dependency debt (and its security-advisory exposure) persists.

**Independent Test**: Run `cd frontend && npm ci && npm run build`; confirm it exits 0 and produces `frontend/dist`. Stand up a fresh mcpproxy on a throwaway data-dir and sweep the main views (Servers, Server Detail, Global Tools, Activity, Quarantine, Settings) with the Playwright screenshot-report workflow; compare against the pre-migration baseline.

**Acceptance Scenarios**:

1. **Given** the upgraded dependencies installed, **When** `npm run build` runs, **Then** it completes without TypeScript, PostCSS, or Vite errors and writes `frontend/dist`.
2. **Given** a running mcpproxy serving the rebuilt UI, **When** a user navigates each main view, **Then** all DaisyUI-styled components (buttons, cards, badges, modals, tabs, toggles, dropdowns) render with the same appearance and behavior as before the upgrade.
3. **Given** the rebuilt UI, **When** a user interacts with the wizard dialog, server detail panels, and activity filters, **Then** they behave identically to the pre-migration build.

---

### User Story 2 - The Go binary embeds the new build and both editions still build (Priority: P1)

A maintainer runs `make build` and gets a working personal-edition binary whose embedded UI is the upgraded build; `make build-server` likewise succeeds.

**Why this priority**: The frontend is shipped only as embedded bytes in the binary. If the embed step or either edition build breaks, the migration is not releasable.

**Independent Test**: Run `make build` and `go build -tags server ./cmd/mcpproxy`; confirm both succeed and the resulting binary serves the upgraded UI.

**Acceptance Scenarios**:

1. **Given** the upgraded `frontend/dist`, **When** `make build` runs, **Then** it embeds the new dist and produces a working `mcpproxy` binary.
2. **Given** the personal edition, **When** built after the migration, **Then** backend behavior is unaffected (only the embedded asset bytes change).

---

### User Story 3 - CI frontend checks pass and the dependency group is unblocked (Priority: P2)

After the migration lands, Dependabot's frontend group no longer carries breaking majors, and the `Build Frontend` / `frontend-test` CI jobs pass on subsequent PRs.

**Why this priority**: Closing the loop on #498 means future frontend Dependabot PRs are routine again, restoring the "merge the chore PRs" workflow.

**Independent Test**: Push the migration branch and confirm `Build Frontend` and `frontend-test` are green.

**Acceptance Scenarios**:

1. **Given** the migration merged to main, **When** CI runs the frontend jobs, **Then** `Build Frontend` and `frontend-test` succeed.
2. **Given** the migrated `package.json`, **When** Dependabot next regenerates the frontend group, **Then** it contains only minor/patch bumps.

### Edge Cases

- **DaisyUI theme tokens**: v5 may rename or restructure theme tokens / CSS variables. Custom theme settings and any hard-coded `data-theme` usage must continue to resolve.
- **Tailwind utility renames**: Tailwind v4 removed/renamed some v3 utilities (e.g., deprecated opacity utilities, `flex-grow`→`grow`). Any affected class strings in `.vue` files must be updated.
- **`@apply` / arbitrary values in scoped styles**: Components using `@apply` or arbitrary-value classes in scoped `<style>` blocks must still compile under the v4 PostCSS pipeline.
- **Vite 8 / rolldown output**: Chunking, asset hashing, or base-path handling differences could break the embedded-asset paths served by the Go server.
- **Test runner compatibility**: `frontend-test` (Vitest or equivalent) must remain compatible with the Vite 8 / TS 6 toolchain.
- **Node engine requirements**: New packages require Node ≥22.18 / ≥24.11; the CI Node version must satisfy this or the build emits EBADENGINE and may fail.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The frontend build (`npm ci && npm run build`) MUST succeed with `tailwindcss@4`, `daisyui@5`, `vite@8`, `typescript@6`, and `vue-tsc@3` installed.
- **FR-002**: Tailwind configuration MUST be migrated to the v4 model — PostCSS wired through `@tailwindcss/postcss`, and Tailwind imported via the CSS-first entry (`@import "tailwindcss"`), replacing the `@tailwind` directives.
- **FR-003**: DaisyUI MUST be registered using the v5 mechanism (`@plugin "daisyui"` in CSS) and all DaisyUI component classes currently used across `frontend/src` MUST render correctly.
- **FR-004**: `frontend/tsconfig.json` MUST type-check under TypeScript 6 without the `baseUrl` deprecation error — by removing `baseUrl` and relying on `paths` resolving relative to the tsconfig directory (verified to pass `vue-tsc` locally).
- **FR-005**: The existing `@/*` import alias MUST continue to resolve in both `vue-tsc` and the Vite build.
- **FR-006**: `make build` MUST embed the upgraded `frontend/dist` into the personal-edition binary, and `make build-server` MUST succeed for the server edition.
- **FR-007**: The migration MUST NOT change any backend Go code or behavior; only frontend sources, config, lockfile, and embedded assets change.
- **FR-008**: Any Tailwind v3 utility classes removed/renamed in v4 MUST be updated in the affected `.vue` files so the rendered output is unchanged.
- **FR-009**: The frontend test suite (`frontend-test`) MUST pass under the upgraded toolchain.
- **FR-010**: The migration SHOULD keep the rest of the original #498 group's minor/patch bumps so the dependency group is fully cleared.

### Key Entities

- **Tailwind config**: The styling configuration — migrated from `tailwind.config.js` + PostCSS directive entry to the v4 CSS-first form. Holds theme customizations and content globs.
- **DaisyUI plugin registration**: How the component library is enabled and themed — moves from the JS plugins array to the CSS `@plugin` directive.
- **`frontend/dist`**: The built static bundle embedded into the Go binary; its byte content changes but its served-path contract must not.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: `cd frontend && npm ci && npm run build` exits 0 on the CI Node version, producing `frontend/dist`.
- **SC-002**: `make build` and `make build-server` both succeed and serve the upgraded UI.
- **SC-003**: A Playwright screenshot sweep of all main web-UI views shows zero visual regressions versus the pre-migration baseline (every DaisyUI component renders as before).
- **SC-004**: No new errors appear in the browser console while navigating the web UI after the upgrade.
- **SC-005**: CI `Build Frontend` and `frontend-test` jobs are green on the migration branch.
- **SC-006**: After merge, the next Dependabot frontend group contains no breaking major upgrades.

## Assumptions

- The four named majors are the only sources of breakage; the remaining ~23 bumps in the original group are non-breaking and can ride along.
- DaisyUI v5 supports the themes/components currently in use; if a component was dropped in v5, an equivalent must be substituted (tracked during planning).
- CI Node version can be raised to ≥22.18 (or ≥24.11) if required by the new package engines; if CI pins an older Node, bumping it is in scope for this feature.
- Visual parity is judged against the current `main` UI as the baseline (captured before the migration).
- `frontend-test` exists and is wired into CI; if it is not, the test acceptance reduces to the build + Playwright sweep.

## Commit Message Conventions *(mandatory)*

### Issue References
- ✅ **Use**: `Related #498` — links to the closed Dependabot PR without reopening/closing behavior.
- ❌ **Do NOT use**: `Fixes`/`Closes`/`Resolves`.

### Co-Authorship
- ❌ Do NOT include `Co-Authored-By: Claude` or "Generated with Claude Code" (per repo convention).

### Example Commit Message
```
chore(frontend): migrate to Tailwind v4 + DaisyUI v5 + Vite 8 + TS 6

Related #498

Migrates the embedded Vue 3 web UI across four bundled major upgrades
that Dependabot grouped into the closed PR #498.

## Changes
- Tailwind v4 CSS-first config + @tailwindcss/postcss
- DaisyUI v5 @plugin registration
- Drop tsconfig baseUrl for TS 6
- Vite 8 build verified; dist re-embedded

## Testing
- npm ci && npm run build green
- make build / make build-server green
- Playwright UI sweep: no visual regressions
```
