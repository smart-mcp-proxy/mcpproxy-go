# Verifying Web UI changes (Playwright + rich HTML report)

When you modify the Web UI (any Vue file under `frontend/src/`), verify it end-to-end with a Playwright sweep that captures screenshots and packages them into a self-contained HTML report. This is the same workflow used to verify Spec 046 v2 — see `specs/046-local-first-onboarding/verification/` for a worked example.

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
