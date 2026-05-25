import { test, expect } from '@playwright/test';
import { goto, captureConsole, flushConsole, assertNoConsoleErrors, SHOT_DIR } from './_helpers';

// UC6 — Servers list -> detail (or empty-state on a fresh instance).
test('UC6 servers list empty-state, click through to detail if present', async ({ page }) => {
  const cap = captureConsole(page, 'UC6-server-detail');
  await goto(page, '/servers');

  // KPI cards always render on the Servers view.
  await expect(page.locator('[data-test="kpi-card-total"]')).toBeVisible();

  const cardCount = await page.locator('[data-test="server-card"], .card').count();
  const serverLink = page.locator('a[href*="/servers/"]').first();

  if (await serverLink.count() > 0) {
    // A server exists — click through to detail.
    await serverLink.click();
    await page.waitForTimeout(800);
    expect(page.url()).toContain('/servers/');
    expect(await page.locator('h1, h2').count()).toBeGreaterThan(0);
  } else {
    // Fresh instance: assert the empty-state renders.
    const bodyText = (await page.locator('body').innerText()).toLowerCase();
    expect(/no servers found|no servers available/.test(bodyText),
      'empty-state not rendered on fresh instance').toBeTruthy();
  }

  await page.screenshot({ path: `${SHOT_DIR}/uc6-servers.png`, fullPage: false });

  flushConsole('UC6-server-detail', cap);
  assertNoConsoleErrors(cap);
});
