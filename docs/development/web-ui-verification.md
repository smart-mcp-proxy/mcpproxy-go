---
title: "Verifying Web UI Changes"
sidebar_label: "Web UI Verification"
description: "Playwright sweep and HTML report workflow for verifying changes to the Vue Web UI."
---

# Verifying Web UI changes (Playwright + rich HTML report)

When you modify the Web UI (any Vue file under `frontend/src/`), verify it end-to-end with a Playwright sweep that captures screenshots and packages them into a self-contained HTML report. This is the same workflow used to verify Spec 046 v2 — see `specs/046-local-first-onboarding/verification/` for a worked example.

## The standing sweep (one command)

The core-screen sweep is committed and scripted — run it before you hand-roll anything:

```bash
./scripts/run-web-smoke.sh --show-report   # boots a throwaway instance, runs e2e/web-ui-sweep
```

The launcher builds `./mcpproxy` if needed, serves a throwaway instance on `127.0.0.1:18080` under a freshly generated throwaway API key (your own `MCPPROXY_API_KEY` is deliberately ignored — the key ends up in report URLs and traces), installs Chromium from the committed `e2e/web-ui-sweep/package-lock.json` via `npm ci`, and runs [`e2e/web-ui-sweep/web-ui-sweep.spec.ts`](https://github.com/smart-mcp-proxy/mcpproxy-go/blob/main/e2e/web-ui-sweep/web-ui-sweep.spec.ts) — servers list, server detail (+ security tab), tools page and search, activity log, settings — failing on uncaught page exceptions. Pass `MCPPROXY_FIXTURE_PATH=$(go build -o /tmp/mcpfixture ./cmd/mcpfixture && echo /tmp/mcpfixture)` to register a live stdio upstream so the server- and tool-dependent checks run instead of skipping. The HTML report lands in `tmp/web-smoke-artifacts/playwright-report/`.

Alongside it, [`e2e/web-ui-sweep/visual-a11y-sweep.spec.ts`](https://github.com/smart-mcp-proxy/mcpproxy-go/blob/main/e2e/web-ui-sweep/visual-a11y-sweep.spec.ts) guards the *appearance* contract that the 2026-08 UX audit found broken: it walks every visible text node on the main screens in both shipped themes, resolves the effective foreground/background through a canvas (theme tokens are `oklch()`, so a naive `rgb()` parse silently measures nothing) and fails anything below WCAG AA; it also checks the 390/820/1440px layouts, accessible names on every form control, the activity log's `aria-live` region and caption, and the system-theme resolution. Same launcher, same environment variables.

[`e2e/web-ui-sweep/navigation-consistency.spec.ts`](https://github.com/smart-mcp-proxy/mcpproxy-go/blob/main/e2e/web-ui-sweep/navigation-consistency.spec.ts) guards the Spec 109 navigation contract (`specs/109-ux-navigation-consistency`): server-card heights (FR-013); the header and sidebar at 1440/1100/900/390px in both themes (one header row, no horizontal scroll, no clipped control, the phone header holds exactly the drawer toggle, search icon, status pill, attention pill and `+`); the sidebar groups and order; the command palette (⌘K and `/`, no `/index/search` request until there is text, one debounced `limit=8` request, Enter to Tools); the `+ Add` menu (Server, Client, Token); every redirect keeping its query; and the link-map rows that must land filtered with no unfiltered REST request (SC-009). It runs in the same launcher and skips loudly, with a reason, where a fixture has no data for a row.

[`e2e/web-ui-sweep/profiles-clients.spec.ts`](https://github.com/smart-mcp-proxy/mcpproxy-go/blob/main/e2e/web-ui-sweep/profiles-clients.spec.ts) guards the Profiles v3 screens (`specs/108-profiles-v3`, PR 108-i). It needs the fixture upstream (`MCPPROXY_FIXTURE_PATH`) and seeds everything over REST (`profiles-seed.ts`: a read-only profile, `anonymous_profile` pointing at it so the binding guard is satisfied without turning authentication on, and a custom client; no supported client's config file is written), then cleans up after itself. It drives the goal flow (create a profile, Try it, mint a token with it, move a client with the chip), the binding-guard refusal and its fix buttons, rename with its impact list, "Explain access" landing on the focused editor row, the header Viewing chip surviving navigation and Back, the layout at 1440/1100/900/390px (no page-level horizontal scroll, header controls not clipped), a real `Escape` on every dialog with focus returning to its trigger, and an SSE-driven chip update with no reload. The same seed lets `visual-a11y-sweep.spec.ts` measure contrast (both themes), layout and accessible names on `/profiles`, `/profiles/e2e-ro` and `/clients`, including the danger-styled missing-profile chip.

[`e2e/web-ui-sweep/profiles-scope.spec.ts`](https://github.com/smart-mcp-proxy/mcpproxy-go/blob/main/e2e/web-ui-sweep/profiles-scope.spec.ts) guards the profile scope on Tools, Activity, Sessions and Usage (`specs/108-profiles-v3`, PR 108-j). It extends `profiles-seed.ts` with one locked client on a profile that denies `<fixture>:echo`, then makes one allowed and one refused call over MCP with that client's credential (the fixture's tools are all read-only, so a deny rule is what makes the blocked row). It checks that `/tools?client=` renders no unfiltered rows before the scoped answer (the scoped request is held back for 1.5s), greyed rows with their reason words and the Why? explainer closed by a real `Escape` with focus returning to its trigger; that `/usage?profile=` carries the parameter and replaces the tokens-saved tile; that `/sessions?client=` lands on the Activity Sessions view with the client and profile columns and "Tools it sees"; that the chip survives a link and Back and removing it refetches unscoped; that `/activity?client=&status=blocked` shows exactly the refused call with its attribution chips, "Allow in profile…" (landing on the focused editor row) and "Why?"; the layout at 1440/1100/900/390px (no page-level horizontal scroll, chips inside the viewport, the drawer's Attribution section below `md`); and the Tab order to a chip's remove button and Why?. Screenshots are report attachments; `SWEEP_SCREENSHOT_DIR` also keeps them as files. `visual-a11y-sweep.spec.ts` measures contrast on the scoped Tools and Activity pages in both themes.

[`e2e/web-ui-sweep/profiles-unhide.spec.ts`](https://github.com/smart-mcp-proxy/mcpproxy-go/blob/main/e2e/web-ui-sweep/profiles-unhide.spec.ts) guards the Spec 108 un-hide of Spec 109 (`specs/109-ux-navigation-consistency`, PR 109-l). It reuses `profiles-seed.ts` and adds a run-unique agent token and a client credential that expires in 7 days (a revoked token keeps its name, so a fixed name would collide on a re-run). It checks that Profiles is in the sidebar and "+ Add → Profile" opens the create dialog; that the header Viewing chip survives Activity, Usage and Tools and Back restores it; that an expanded Clients row links Activity, Sessions, "Tools it sees" and Usage by client; that an agent-token row links Activity and Usage by token; that `/servers?profile=` lists only that profile's servers with a removable chip; that creating the expiring credential makes a `client_credential_expiring` needs-attention item appear within seconds, first in the list, whose fix lands on `/clients?focus=<id>`; the layout at 1440/900/390px (no page-level horizontal scroll, the fix button at least 24px tall); and the Tab order to the attention fix and each Clients-row link. The binding-guard item cannot be produced through the API (the API refuses a bypassable binding, `409`), so its rendering is pinned by `attention-108-targets.spec.ts` and the live recipe instead.

[`e2e/web-ui-sweep/demo-ux-fixes.spec.ts`](https://github.com/smart-mcp-proxy/mcpproxy-go/blob/main/e2e/web-ui-sweep/demo-ux-fixes.spec.ts) guards the two pixel-level findings of the live Web UI demo (Spec 108 T149 and T151). It seeds a profile titled `Work Read-only for very long client names 2026` and a token pinned to it, then at 1440x900 and 900x900 checks that the token's Profile chip is a single line inside its table cell with the full title on hover (a Range over its text reports one line, and its scroll height does not exceed its client height), and that the radio labels of the create-profile, custom-client and bulk-move dialogs start within 16 px of their radios. It attaches screenshots to the report.

[`e2e/web-ui-sweep/usertest-web-fixes.spec.ts`](https://github.com/smart-mcp-proxy/mcpproxy-go/blob/main/e2e/web-ui-sweep/usertest-web-fixes.spec.ts) guards three findings of the first-run user test (Spec 109 T202 and T201, Spec 108 T169) at 1440x900 and 900x900: the header status pill says "awaiting review" for the sweep's quarantined server (compact form keeps the text in its title, no sideways scroll), a secret-like Manual-add env value is a password input with a Show toggle (never submitted), and profile Try it shows no `[object Object]`, says it uses unsaved edits, and the tool counts read "Saved profile:" while a Deny toggle is unsaved. The wizard import flow is covered by vitest and live QA, because the smoke core uses the real HOME for client paths.

The release QA gate runs this exact script on every tag as its **advisory** `web-ui-sweep` job — see [Release Gate](release-gate.md#web-ui-sweep-t2--advisory). Extend the committed sweep when you add a screen worth guarding on releases; use the ad-hoc pattern below for the deeper, spec-specific verification that ships beside a spec.

## Ad-hoc, spec-specific verification

The pattern, in order:

1. **Stand up a fresh mcpproxy.** Use a throwaway data-dir so persisted state doesn't bleed between runs:
   ```bash
   pkill -f 'mcpproxy serve.*<port>' 2>/dev/null; sleep 1
   rm -rf /tmp/mcpproxy-uitest/{config.db,index.bleve,logs} 2>/dev/null
   cat > /tmp/mcpproxy-uitest/mcp_config.json <<'EOF'
   { "listen": "127.0.0.1:18081", "data_dir": "/tmp/mcpproxy-uitest", "api_key": "uitest", "enable_web_ui": true, "enable_socket": false, "telemetry": {"enabled": false}, "mcpServers": [] }
   EOF
   ./mcpproxy serve --config=/tmp/mcpproxy-uitest/mcp_config.json --listen=127.0.0.1:18081 --log-level=info > /tmp/mcpproxy-uitest/server.log 2>&1 &
   until curl -sf -H "X-API-Key: uitest" http://127.0.0.1:18081/api/v1/status >/dev/null; do sleep 1; done
   ```
2. **Reuse the existing Playwright install.** `e2e/playwright/node_modules` already has Playwright + Chromium 1217. Symlink it into your scratch dir:
   ```bash
   mkdir -p /tmp/uitest && cd /tmp/uitest
   ln -sfn /Users/user/repos/mcpproxy-go/e2e/playwright/node_modules ./node_modules
   ```
3. **Pin the Chromium binary in `playwright.config.ts`** so Playwright doesn't try to download a different version:
   ```ts
   import { defineConfig } from '@playwright/test';
   export default defineConfig({
     testDir: '.', timeout: 30000, fullyParallel: false, workers: 1, retries: 0,
     use: {
       headless: true,
       viewport: { width: 1440, height: 900 },
       launchOptions: {
         executablePath: '/Users/user/Library/Caches/ms-playwright/chromium-1217/chrome-mac-arm64/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing',
       },
     },
   });
   ```
4. **Write the spec.** Use `data-test` attributes already on the components (the project convention). For new components, add them. Drive scenarios with `page.locator('[data-test="..."]')`. Always use `page.waitForLoadState('domcontentloaded')` — `networkidle` hangs because of the SSE channel. Snapshot each state with `page.screenshot({ path: ... })`. Number screenshots in execution order so the report renders left-to-right.
5. **Run.** `./node_modules/.bin/playwright test --reporter=list`. Iterate until green.
6. **Build the rich HTML report.** A short Python script that base64-embeds each PNG and wraps it in a styled `<details>` per scenario produces a single self-contained HTML file the user can open offline. Pattern: top summary card with pass/fail counts, then one collapsible per scenario with `Expected` / `Observed` / inline screenshot. The reference implementation is `/tmp/wizard-v2-verify/build-report.py` from the v2 work — clone it and update the `SCENARIOS` list. Output goes to `specs/<feature>/verification/report.html`.
7. **Drop screenshots + report alongside the spec.** Always commit them with the spec changes — they're part of the trace.
8. **Surface the report.** End your reply with `open <path-to-report.html>` so the user can review without re-running the suite.

Key gotchas:
- The wizard's `<dialog>` element renders as `[open]` only when the Vue store sets it. To assert open/closed state robustly, query the dialog property in `page.evaluate()`, not aria-hidden or styling.
- The default config from a stub file does NOT trigger `applyFirstRunDockerIsolation` — that only runs when the config file is absent at boot. To test the "Docker auto-enabled" path, either let mcpproxy create the config or pre-set `docker_isolation.enabled: true` in your stub.
- For browser-driven verification of subtle states (badge counts, empty/loaded transitions), prefer the Playwright spec over ad-hoc screenshots from the chrome-in-chrome MCP — the spec is reproducible and a CI agent can re-run it.
