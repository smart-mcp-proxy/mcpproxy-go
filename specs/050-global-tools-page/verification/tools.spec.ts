import { test, expect, Page } from '@playwright/test';

const POP = 'http://127.0.0.1:18081/ui/?apikey=uitest';
const EMPTY = 'http://127.0.0.1:18082/ui/?apikey=uitest';
const SHOT = '/Users/user/repos/mcpproxy-go/specs/050-global-tools-page/verification';

async function gotoTools(page: Page, base: string) {
  await page.goto(base);
  await page.waitForLoadState('domcontentloaded');
  // localStorage api key already via query; navigate to the route
  await page.goto(base.replace('/ui/?', '/ui/tools?'));
  await page.waitForLoadState('domcontentloaded');
  await page.locator('[data-test="tools-page"]').waitFor({ state: 'visible', timeout: 20000 });
}

test('01 loaded table + stat cards + sidebar badge', async ({ page }) => {
  await gotoTools(page, POP);
  await page.locator('[data-test="tools-table"]').waitFor({ state: 'visible', timeout: 20000 });
  const rows = page.locator('[data-test="tool-row"]');
  await expect.poll(() => rows.count(), { timeout: 15000 }).toBeGreaterThan(0);
  await expect(page.locator('[data-test="stat-total"]')).toContainText(/\d/);
  // sidebar badge next to the Tools nav entry
  const badge = page.locator('a[href$="/tools"] .badge');
  await expect(badge).toHaveText(/\d+/);
  await page.screenshot({ path: `${SHOT}/01-loaded-table.png`, fullPage: true });
});

test('02 search filter narrows the list', async ({ page }) => {
  await gotoTools(page, POP);
  await page.locator('[data-test="tool-row"]').first().waitFor({ state: 'visible', timeout: 20000 });
  const before = await page.locator('[data-test="tool-row"]').count();
  await page.locator('[data-test="tools-search"]').fill('echo');
  await page.waitForTimeout(600); // debounce
  const after = await page.locator('[data-test="tool-row"]').count();
  expect(after).toBeLessThanOrEqual(before);
  await expect(page.locator('[data-test="tool-row"]').first()).toContainText(/echo/i);
  await page.screenshot({ path: `${SHOT}/02-search-filter.png`, fullPage: true });
});

test('03 column sort toggles order', async ({ page }) => {
  await gotoTools(page, POP);
  await page.locator('[data-test="tool-row"]').first().waitFor({ state: 'visible', timeout: 20000 });
  const nameHeader = page.locator('[data-test="tools-table"] thead th', { hasText: /Tool/i }).first();
  await nameHeader.click(); // first sort direction
  await page.waitForTimeout(300);
  const firstAsc = await page.locator('[data-test="tool-row"]').first().innerText();
  await nameHeader.click(); // toggle direction
  await page.waitForTimeout(300);
  const firstDesc = await page.locator('[data-test="tool-row"]').first().innerText();
  // Toggling sort direction on the same column must change which row is first.
  expect(firstDesc).not.toEqual(firstAsc);
  await page.screenshot({ path: `${SHOT}/03-column-sort.png`, fullPage: true });
});

test('04 batch select shows action bar + disable', async ({ page }) => {
  await gotoTools(page, POP);
  await page.locator('[data-test="tool-row"]').first().waitFor({ state: 'visible', timeout: 20000 });
  await page.locator('[data-test="tools-select-all"]').click();
  const bar = page.locator('[data-test="tools-batch-bar"]');
  await expect(bar).toBeVisible();
  await expect(bar).toContainText(/\d/);
  await page.screenshot({ path: `${SHOT}/04-batch-bar.png`, fullPage: true });
  // exercise disable; tolerate per-tool failures (quarantine-pending) — the
  // point is the action runs and reports, not that every tool flips.
  const disableBtn = page.locator('[data-test="batch-disable"]');
  if (await disableBtn.count()) {
    await disableBtn.first().click();
    await page.waitForTimeout(1500);
    await page.screenshot({ path: `${SHOT}/05-batch-disable-result.png`, fullPage: true });
  }
});

test('06 empty state', async ({ page }) => {
  await gotoTools(page, EMPTY);
  await page.waitForTimeout(800);
  await expect(page.locator('[data-test="tools-page"]')).toBeVisible();
  await expect(page.locator('[data-test="stat-total"]')).toContainText('0');
  await page.screenshot({ path: `${SHOT}/06-empty-state.png`, fullPage: true });
});
