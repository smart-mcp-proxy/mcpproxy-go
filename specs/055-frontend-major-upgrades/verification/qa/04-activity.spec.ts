import { test, expect } from '@playwright/test';
import { goto, captureConsole, flushConsole, assertNoConsoleErrors, SHOT_DIR } from './_helpers';

// UC4 — Activity view: load /activity, exercise the Status filter dropdown,
// assert the table or empty-state renders.
test('UC4 activity — filters work, table/empty-state renders', async ({ page }) => {
  const cap = captureConsole(page, 'UC4-activity');
  await goto(page, '/activity');

  // Page mounted (heading present).
  expect(await page.locator('h1, h2').count()).toBeGreaterThan(0);

  // KPI summary cards (data-test) are part of the activity dashboard.
  await expect(page.locator('[data-test="kpi-card-total"]')).toBeVisible();

  // At the default (unfiltered) view the activity table renders (a fresh
  // instance always has at least the "system start" record).
  await expect(page.locator('table'), 'activity table not rendered at default view').toBeVisible();

  // Exercise the Status filter select (success/error/blocked/all).
  const statusSelect = page.locator('select').filter({ has: page.locator('option[value="blocked"]') }).first();
  await expect(statusSelect, 'Status filter select not found').toBeVisible();
  await statusSelect.selectOption('error');
  await page.waitForTimeout(500);
  expect(await statusSelect.inputValue(), 'status filter did not apply').toBe('error');

  // With the "error" filter (no error rows on a fresh instance), the view must
  // still render coherently: either an emptied table or an empty-state message.
  const hasTable = await page.locator('table').count();
  const bodyText = (await page.locator('body').innerText()).toLowerCase();
  const hasEmpty = /no matching activit|no activit|no records|no results|no data|adjusting your filters|empty/.test(bodyText);
  expect(hasTable > 0 || hasEmpty,
    'filtered view rendered neither a table nor an empty-state').toBeTruthy();

  // Reset filter back to All.
  await statusSelect.selectOption('');
  await page.waitForTimeout(300);

  await page.screenshot({ path: `${SHOT_DIR}/uc4-activity.png`, fullPage: false });

  flushConsole('UC4-activity', cap);
  assertNoConsoleErrors(cap);
});
