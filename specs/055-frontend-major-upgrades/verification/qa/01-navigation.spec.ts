import { test, expect } from '@playwright/test';
import { goto, captureConsole, flushConsole, assertNoConsoleErrors, SHOT_DIR } from './_helpers';

// UC1 — Navigation: visit every nav route, assert the SPA mounts and no route 404s.
const ROUTES: { path: string; label: string }[] = [
  { path: '/',            label: 'dashboard' },
  { path: '/servers',     label: 'servers' },
  { path: '/tools',       label: 'tools' },
  { path: '/activity',    label: 'activity' },
  { path: '/security',    label: 'security' },
  { path: '/settings',    label: 'settings' },
  { path: '/secrets',     label: 'secrets' },
  { path: '/tokens',      label: 'tokens' },
  { path: '/repositories',label: 'repositories' },
  { path: '/search',      label: 'search' },
];

test('UC1 navigation — all nav routes mount with no console errors', async ({ page }) => {
  const cap = captureConsole(page, 'UC1-navigation');

  for (const r of ROUTES) {
    await goto(page, r.path);

    // SPA must have mounted: #app present and non-empty.
    const appHtml = await page.evaluate(() => document.querySelector('#app')?.innerHTML.length ?? 0);
    expect(appHtml, `#app empty on ${r.path}`).toBeGreaterThan(50);

    // A heading should be visible (every view renders an h1/h2/h3 — the
    // Dashboard uses h3 section headings). Guards against a blank-screen mount.
    const headingCount = await page.locator('h1, h2, h3').count();
    expect(headingCount, `no heading rendered on ${r.path}`).toBeGreaterThan(0);

    // Guard against the router 404 / not-found fallback.
    const bodyText = (await page.locator('body').innerText()).toLowerCase();
    expect(bodyText.includes('404') && bodyText.includes('not found'),
      `route ${r.path} hit a 404 view`).toBeFalsy();

    await page.screenshot({ path: `${SHOT_DIR}/uc1-nav-${r.label}.png`, fullPage: false });
  }

  flushConsole('UC1-navigation', cap);
  assertNoConsoleErrors(cap);
});
