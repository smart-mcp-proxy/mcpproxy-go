import { test } from '@playwright/test';

// Records two web-UI walkthroughs as video (saved to /tmp/demo-webui by the
// recordVideo config). Robust to frontend version drift: navigation + dwell
// driven, with best-effort interactions guarded so a missing selector never
// aborts the recording. Run against a LIVE mcpproxy:
//   MCPPROXY_API_KEY=... ./node_modules/.bin/playwright test --config=playwright.config.ts

const KEY = process.env.MCPPROXY_API_KEY || '';
const q = `?apikey=${KEY}`;

async function settle(page: import('@playwright/test').Page, ms: number) {
  await page.waitForLoadState('domcontentloaded');
  await page.waitForTimeout(ms); // SSE keeps the network busy, so never networkidle
}

test('dashboard + server cards walkthrough', async ({ page }) => {
  await page.goto(`/ui/${q}`);
  await settle(page, 2500);                       // KPI cards + server panel render
  // gentle scroll to reveal the server list under the KPI cards
  await page.mouse.wheel(0, 400);
  await page.waitForTimeout(1500);
  await page.mouse.wheel(0, -400);
  await page.waitForTimeout(800);
  // into the servers list
  await page.goto(`/ui/servers${q}`);
  await settle(page, 2500);
  // best-effort: open the first server's detail
  try {
    const firstServerLink = page.locator('a[href*="/servers/"]').first();
    await firstServerLink.click({ timeout: 4000 });
    await settle(page, 2800);
  } catch {
    /* leave on the list view if no clickable server link */
  }
});

test('activity log walkthrough', async ({ page }) => {
  await page.goto(`/ui/activity${q}`);
  await settle(page, 2500);
  // best-effort: expand the first activity entry
  try {
    const firstRow = page
      .locator('[data-test="verify-activity-list"] >> :scope *')
      .filter({ hasText: /.+/ })
      .first();
    await firstRow.click({ timeout: 4000 });
    await page.waitForTimeout(2500);
  } catch {
    await page.waitForTimeout(2500); // just dwell on the list
  }
});
