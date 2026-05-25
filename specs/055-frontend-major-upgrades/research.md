# Phase 0 Research: Frontend Major-Dependency Migration

All four major upgrades, plus the cross-cutting gotchas surfaced by surveying the current `frontend/` tree.

## Current state (verified by inspection)

- `tailwindcss@3.4.17`, `daisyui@4.12.24`, `vite@5.4.20`, `typescript@5.9.2`, `vue-tsc@2.2.12`.
- CSS entry `src/assets/main.css` uses `@tailwind base/components/utilities` + `@layer components` with `@apply`.
- `tailwind.config.cjs` registers `@tailwindcss/typography` + `daisyui` via the JS `plugins` array and lists **32 themes** (`light`…`sunset`) with `darkTheme: "dark"`.
- `postcss.config.cjs` uses `tailwindcss` + `autoprefixer`.
- `tsconfig.json` sets `baseUrl: "."` + `paths: { "@/*": ["./src/*"] }`.
- `vite.config.ts` sets `base: '/ui/'` and `@`→`src` alias.
- `.nvmrc` = `18`; `package.json` engines `node >=18.18.0`.
- Deprecated v3 utilities present in `.vue` files: `flex-shrink-0` (many), `bg-opacity-50` (AuthErrorModal.vue). `flex-grow` not found as a class (only CSS `flex-shrink:` in scoped styles, which is unaffected).
- `@apply` in a Vue **scoped** `<style>`: `src/components/JsonViewer.vue` (`@apply w-full`, `bg-base-200`, `bg-base-content/20`, `bg-base-content/30`).

---

## Decision 1 — Tailwind CSS 3 → 4

**Decision**: Adopt the v4 CSS-first model.
- Install `tailwindcss@4` + `@tailwindcss/postcss`; keep `@tailwindcss/typography` (v4-compatible) but register it via CSS `@plugin`, not JS.
- `postcss.config.cjs`: replace `{ tailwindcss: {}, autoprefixer: {} }` with `{ '@tailwindcss/postcss': {} }`. Drop `autoprefixer` (v4 bundles prefixing + `@import` inlining; a standalone autoprefixer double-prefixes and warns).
- `src/assets/main.css` head: replace the three `@tailwind` directives with a single `@import "tailwindcss";`, then `@plugin "@tailwindcss/typography";` and the DaisyUI `@plugin` block (Decision 2).
- Content detection: v4 auto-detects template sources, so the `content: [...]` glob is dropped. Theme customizations live in CSS `@theme` if needed — currently `theme.extend` is empty, so nothing to port.
- `tailwind.config.cjs`: reduce to the minimum or remove. v4 still reads a JS config if referenced via `@config`, but since the only config content (content globs, plugins, daisyui themes) all move to CSS, the file is **deleted** and `@config` is not used. Cleaner and matches v4 idiom.

**Rationale**: CSS-first is the v4 default and the only path that lets DaisyUI v5 register cleanly. Auto content-detection removes a maintenance footgun.

**Alternatives considered**: Keep `tailwind.config.js` via `@config` — rejected: keeps two sources of truth and DaisyUI v5 still needs the CSS `@plugin`, so the JS file would be half-empty dead weight.

## Decision 2 — DaisyUI 4 → 5

**Decision**: Register via CSS `@plugin "daisyui"` with the theme list inline.
```css
@plugin "daisyui" {
  themes: light --default, dark --prefersdark, cupcake, bumblebee, emerald,
          corporate, synthwave, retro, cyberpunk, valentine, halloween,
          garden, forest, aqua, lofi, pastel, fantasy, wireframe, black,
          luxury, dracula, cmyk, autumn, business, acid, lemonade, night,
          coffee, winter, dim, nord, sunset;
}
```
- `light --default` and `dark --prefersdark` reproduce the old `darkTheme: "dark"` + default behavior.
- All 32 themes from the old config are preserved (Assumption: themes/components in use are all v5-supported). The theme switcher in `src/stores/system.ts` keeps setting `data-theme` on `<html>` — unchanged, since v5 still resolves themes via the `data-theme` attribute.
- The old `base/styled/utils/prefix/logs/themeRoot` flags are v4-era DaisyUI internals; v5 defaults (base+styled on, no prefix, `:root` theme root) match the prior behavior, so none need explicit porting.

**Rationale**: This is the documented v5 registration path and preserves the exact theme set and dark-mode behavior the app already ships.

**Alternatives considered**: Trim the theme list to only those exposed in the switcher — rejected: the switcher (`NavBar.vue`/`SidebarNav.vue`) iterates the full list; trimming would change user-visible options. Out of scope for a no-regression migration.

## Decision 3 — TypeScript 5 → 6 (+ vue-tsc 2 → 3)

**Decision**: Remove `baseUrl` from `tsconfig.json`, keep `paths`.
- TS 6 makes `baseUrl` a hard error pattern (TS5101 deprecation). Since TS 4.1+, `paths` resolve relative to the `tsconfig.json` directory **without** `baseUrl`, so `"@/*": ["./src/*"]` keeps working.
- Bump `vue-tsc` to v3 (required for TS 6 compatibility). The build script `vue-tsc && vite build` is unchanged.
- Verify the `@vue/tsconfig` base (`tsconfig.dom.json`) is compatible with TS 6; bump `@vue/tsconfig` if `vue-tsc@3` requires it.

**Rationale**: Dropping `baseUrl` is the minimal, idiomatic fix and the alias already has an independent resolver in both `tsconfig.paths` and `vite.config` alias.

**Alternatives considered**: Keep `baseUrl` and suppress — not possible; TS 6 errors hard. Switch alias to relative imports — rejected: large diff, no benefit.

## Decision 4 — Vite 5 → 8

**Decision**: Upgrade `vite` to 8 and `@vitejs/plugin-vue` to its Vite-8-compatible major; preserve `base: '/ui/'` and the `@` alias.
- Whatever rolldown-based internals Vite 8 ships, the **output contract that matters is**: assets emitted under `dist/assets/` with hashed names, `index.html` referencing them with the `/ui/` base prefix. The Go server serves `/ui/` from the embed, so as long as `base` stays `/ui/` the embedded-path contract holds (Edge case: "Vite 8 / rolldown output").
- `manualChunks: undefined` is already set; keep it. If Vite 8 changes default chunking in a way that breaks load order, revisit, but single-bundle behavior is preserved by the existing config.
- Vitest is at v2; confirm it runs under the Vite 8 toolchain. If Vitest 2 is incompatible with Vite 8's plugin API, bump Vitest to the compatible major (rides along per FR-009/FR-010). `vitest.config.ts` mirrors the alias and stays in sync.

**Rationale**: `base` is the single load-bearing setting for the embed; everything else is internal. Verify empirically by serving the rebuilt `dist` from a real mcpproxy and diffing rendered output.

**Alternatives considered**: Pin to `rolldown-vite` explicitly — rejected unless `npm install` of `vite@8` doesn't resolve; prefer the published `vite@8`.

## Decision 5 — `@apply` in Vue scoped styles (cross-cutting gotcha)

**Decision**: Add a `@reference` directive to every Vue SFC scoped `<style>` block that uses `@apply` with Tailwind/DaisyUI classes.
- Tailwind v4 processes each SFC `<style>` independently; `@apply` for non-trivial classes (e.g. `bg-base-200`, a DaisyUI token) fails unless the block first imports the design context via `@reference`.
- For `src/components/JsonViewer.vue`: add `@reference "@/assets/main.css";` (or `@reference "tailwindcss";` if the DaisyUI tokens resolve from the typography/daisyui import) at the top of the scoped block. `@reference` pulls in theme/util definitions for `@apply` resolution **without** duplicating CSS output.
- `src/assets/main.css`'s own `@apply` usage (in `@layer components`) is in the main entry processed directly by PostCSS and needs no `@reference`.

**Rationale**: This is the single most likely silent breakage in a Tailwind v4 migration of a Vue app and is explicitly called out in the spec edge cases.

## Decision 6 — Deprecated utility renames (FR-008)

**Decision**: Mechanically rename removed v3 utilities across `.vue` files so rendered output is unchanged.
- `flex-shrink-0` → `shrink-0` (utility class only; scoped-CSS `flex-shrink: 0;` declarations are plain CSS and stay).
- `bg-opacity-50` (+ any `text-opacity-*`/`border-opacity-*`) → slash-opacity form, e.g. `bg-black bg-opacity-50` → `bg-black/50`.
- Re-grep after the build for any v4 "utility not found" warnings and fix the long tail (e.g. `overflow-ellipsis`→`text-ellipsis`, `decoration-clone`→`box-decoration-clone`, `shadow-sm`→`shadow-xs` renames) if they surface.

**Rationale**: v4 removes these outright; leaving them yields missing styles (visual regression) rather than build errors, so they must be caught by both grep and the Playwright parity sweep.

## Decision 7 — Node version bump

**Decision**: Raise `.nvmrc` `18`→`22` and `package.json` engines to `node >=22.18.0`.
- Tailwind v4 / Vite 8 / TS 6 require Node ≥22.18 (or ≥24.11). CI Node version must be bumped to match (Assumption: in scope). Verify the GitHub Actions `Build Frontend` / `frontend-test` jobs pin a satisfying Node; bump if not.

**Rationale**: Avoids `EBADENGINE` and runtime failures; the spec explicitly puts the bump in scope.

## Open risks carried into implementation

1. **DaisyUI v5 token/class drift** — a component class renamed/dropped in v5 → caught by the Playwright parity sweep, fixed per-component.
2. **Vite 8 asset-path change** — verified by serving rebuilt `dist` from a real mcpproxy, not just `npm run build` exit code.
3. **Vitest/plugin-vue peer-dep conflicts** — resolved by bumping the conflicting package to its Vite-8 major (rides along per FR-010).
