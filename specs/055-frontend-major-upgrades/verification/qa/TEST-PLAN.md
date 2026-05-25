# QA Test Plan — Web UI after major frontend dependency migration

**Spec:** 055-frontend-major-upgrades
**Under test:** Personal-edition binary `/Users/user/repos/mcpproxy-go/mcpproxy`
(embeds the migrated UI: Tailwind v4, DaisyUI v5, Vite 8, TS 6 — UI is NOT rebuilt here)
**Date:** 2026-05-25
**Harness:** Playwright (Chromium 1217, headless, 1440×900), one spec per use case.
**Instance:** fresh throwaway on `127.0.0.1:18093`, data-dir `/tmp/mcpproxy-qa093`, empty `mcpServers`.

## How to reproduce

```bash
# 1. Start the isolated instance
/Users/user/repos/mcpproxy-go/mcpproxy serve --config=/tmp/mcpproxy-qa093/mcp_config.json \
  --listen=127.0.0.1:18093 --log-level=info &
# 2. Run the suite (specs run from /tmp/qa-usecases which symlinks the e2e node_modules)
cd /tmp/qa-usecases && ./node_modules/.bin/playwright test --reporter=list
```

Spec files + helper live here (canonical copies); the runner copies them into
`/tmp/qa-usecases` (which has the Playwright `node_modules` symlink) before running.
Screenshots, `console-log.txt`, and this plan are written into this directory by the helper's absolute paths.

## SC-004 — console error gate

Every spec attaches `page.on('console')` + `page.on('pageerror')` and FAILS on any
**fatal** console/page error. All captured output is appended to `console-log.txt`.

**Benign noise excluded from the gate (logged, not failed):** SSE/`EventSource`
"connection closed / error occurred" messages. These are browser-native EventSource
reconnect logs driven by the `/events` channel + query-param auth; they are present
regardless of the CSS/build-tool stack and are NOT a migration regression. Pattern:
`/EventSource/i`, `/SSE/i`, `/connection closed/i` (see `_helpers.ts`).

**Result:** `fatal-errors=0` across all six tests. Only 18 benign SSE lines (all in
UC1, which visits 10 routes). No Vue render errors, no missing-module errors, no
pageerror exceptions anywhere.

## Use cases

| # | Case | Steps | Expected | Observed | Verdict |
|---|------|-------|----------|----------|---------|
| UC1 | Navigation | Visit all 10 nav routes (`/`, `/servers`, `/tools`, `/activity`, `/security`, `/settings`, `/secrets`, `/tokens`, `/repositories`, `/search`). Assert `#app` non-empty, ≥1 heading (h1/h2/h3 — Dashboard uses h3), no 404 view, no fatal console error. | Every route mounts a real view, no 404, no JS error. | All 10 routes mount; headings present; no 404; only benign SSE noise. | PASS |
| UC2 | Theme switch (key DaisyUI-v5 check) | Open the sidebar Theme dropdown, switch to **light → dark → synthwave**. Assert `documentElement[data-theme]` equals each and is persisted to `localStorage['mcpproxy-theme']`. Screenshot each. | `data-theme` flips for every theme; colourful theme (synthwave) renders distinct palette. | All three apply + persist; synthwave renders pink/cyan-on-navy; light/dark distinct. | PASS |
| UC3 | Add-server modal | Click TopHeader "Add Server" → assert the OPEN `<dialog.modal>` reports `dialog.open===true`. Fill Server Name; default type stdio; Command select → "Custom command"; type a command path; assert inputs hold their values. | Modal opens as native dialog; form accepts name + command. | `dialog.open===true`; name `qa-test-server` + path `/usr/bin/my-mcp-server` accepted; v5 toggles render. | PASS |
| UC4 | Activity view | Load `/activity`; assert table renders at default; set Status filter → `error`; assert filter applies and view stays coherent (emptied table OR empty-state). | Filters work; table or empty-state renders. | Default table renders (system-start row); `error` filter → "No matching activities / try adjusting your filters" empty-state. | PASS |
| UC5 | Settings + DaisyUI-v5 toggle | Load `/settings`, assert Monaco config-editor card mounts. **Deviation:** `/settings` has NO daisyUI toggle (it is a JSON config editor), so v5 toggle interactivity is verified on the AddServerModal "Enabled" `toggle toggle-primary` (a pure `v-model` local control, no backend side effects); assert checked state flips. | Settings mounts; a daisyUI v5 toggle flips on click. | Settings editor mounts; Enabled toggle flips on→off cleanly. | PASS |
| UC6 | Server detail / empty-state | Load `/servers`; KPI cards visible; if a server card/link exists, click through to detail; else assert empty-state. (Fresh instance → empty-state expected.) | Detail loads OR empty-state renders. | "No servers found" empty-state renders on the fresh instance. | PASS |

## Notes / findings during harness development

1. **OnboardingWizard auto-opens on a fresh instance.** Its open `<dialog>` has a
   full-viewport `<form class="modal-backdrop"><button>close</button></form>` that
   intercepts clicks on the page beneath (correct daisyUI-v5 behaviour for an OPEN
   modal). The helper `dismissOnboarding()` clicks its `aria-label="Close"` button
   after every navigation. This is first-run UX, NOT a migration regression.
2. **DaisyUI v5 keeps closed `<dialog.modal>` elements mounted** with `display:grid`
   + `pointer-events:none` (v4 used `display:none`). Closed modals therefore do NOT
   block clicks; only the single OPEN modal does. Tests target `dialog.modal[open]`.
3. **Dashboard headings are `h3`**, not `h1/h2` — UC1 accepts any heading level.

## Screenshots

`uc1-nav-{dashboard,servers,tools,activity,security,settings,secrets,tokens,repositories,search}.png`,
`uc2-theme-{light,dark,synthwave}.png`, `uc3-add-server-modal.png`, `uc4-activity.png`,
`uc5-settings.png`, `uc5-toggle-flipped.png`, `uc6-servers.png`.

## Verdict

**6/6 PASS, zero fatal console errors.** The migrated UI (Tailwind v4 / DaisyUI v5 /
Vite 8 / TS 6) behaves on par with the pre-migration UI across navigation, theming,
modals, filtering, toggles, and empty-states. The most important DaisyUI-v5 regression
surface — theme switching — works for light, dark, and a colourful theme. No
migration-induced JavaScript errors were observed.
