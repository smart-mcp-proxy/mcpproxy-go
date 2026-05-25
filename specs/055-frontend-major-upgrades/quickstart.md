# Quickstart: Frontend Major-Dependency Migration Runbook

Ordered steps + the verification protocol. Execute in order; the verification gate (step 8) is mandatory and compares against the pre-migration baseline captured in step 0.

## Step 0 — Capture the baseline (BEFORE touching any dependency)

Build the current binary, stand up a throwaway mcpproxy, and Playwright-sweep all main views into `verification/baseline/`. The reusable sweep is saved as `verification/baseline.spec.ts`. **This is lost the instant deps change — it must run first.**

## Step 1 — `package.json`

- Bump: `tailwindcss@^4`, add `@tailwindcss/postcss@^4`, `daisyui@^5`, `vite@^8`, `typescript@^6`, `vue-tsc@^3`, `@vitejs/plugin-vue@<vite-8-compatible major>`, `@tailwindcss/typography@<v4-compatible>`. Bump Vitest + `@vitest/*` if peer-incompatible with Vite 8.
- `engines.node` → `>=22.18.0`.
- Keep the rest of the #498 group's minor/patch bumps (FR-010).

## Step 2 — `.nvmrc`

`18` → `22`.

## Step 3 — `postcss.config.cjs`

```js
module.exports = { plugins: { '@tailwindcss/postcss': {} } }
```

## Step 4 — `src/assets/main.css` head

Replace:
```css
@tailwind base;
@tailwind components;
@tailwind utilities;
```
with:
```css
@import "tailwindcss";
@plugin "@tailwindcss/typography";
@plugin "daisyui" {
  themes: light --default, dark --prefersdark, cupcake, bumblebee, emerald,
          corporate, synthwave, retro, cyberpunk, valentine, halloween,
          garden, forest, aqua, lofi, pastel, fantasy, wireframe, black,
          luxury, dracula, cmyk, autumn, business, acid, lemonade, night,
          coffee, winter, dim, nord, sunset;
}
```
Keep the existing `@layer components` / `@layer utilities` blocks and all custom CSS below unchanged.

## Step 5 — Delete `tailwind.config.cjs`

v4 auto-detects content; themes + plugins now live in CSS. (No `@config` reference added.)

## Step 6 — `tsconfig.json`

Remove `"baseUrl": "."`. Keep `paths: { "@/*": ["./src/*"] }`.

## Step 7 — Scoped `@apply` + utility renames

- `src/components/JsonViewer.vue`: add `@reference "@/assets/main.css";` at the top of the scoped `<style>` block (before the `@apply` rules).
- Rename across `src/**/*.vue`: `flex-shrink-0` → `shrink-0`; `bg-opacity-50` (and any `*-opacity-*`) → slash form (`bg-black/50`).
- `vite.config.ts`: confirm `base: '/ui/'` and `@` alias retained; adjust only if Vite 8 needs API changes.

## Step 8 — Build + verification gate

1. `cd frontend && rm -rf node_modules package-lock.json && npm install` → regenerate lockfile. (Or `npm ci` after lockfile exists.)
2. `npm run build` → must exit 0, write `frontend/dist`. Fix any `vue-tsc`/PostCSS/Vite errors.
3. `npm run test` (Vitest) → green (FR-009).
4. `make build` and `go build -tags server ./cmd/mcpproxy` → both succeed (FR-006).
5. Stand up the rebuilt binary; re-run `verification/baseline.spec.ts` against it → screenshots to `verification/after/`.
6. Diff `after/` vs `baseline/` view-by-view → zero visual regression (SC-003). Check browser console for new errors (SC-004).
7. Build the rich HTML report (`verification/report.html`) embedding before/after pairs.

## Acceptance (maps to spec Success Criteria)

- SC-001 `npm ci && npm run build` exits 0 → step 8.2
- SC-002 `make build` / `make build-server` serve upgraded UI → step 8.4–8.5
- SC-003 zero visual regression → step 8.6
- SC-004 no new console errors → step 8.6
- SC-005 CI `Build Frontend` / `frontend-test` green → after push
- SC-006 next Dependabot frontend group has no breaking majors → post-merge
