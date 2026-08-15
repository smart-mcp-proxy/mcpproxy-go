import { test, expect } from '@playwright/test';

const BASE = 'http://127.0.0.1:18471';
const KEY = 'preflight-t022-key';

// Spec 098 T023: the activity view renders a `preflight` record with its
// verdict, offers it in the type filter, and does not mislabel it as a policy
// decision in the detail drawer.
test('activity view renders the preflight verdict', async ({ page }) => {
  await page.goto(`${BASE}/ui/activity?apikey=${KEY}`);
  await page.waitForLoadState('domcontentloaded');
  await page.waitForTimeout(2500);

  // 1. List row: type label + verdict summary in the details cell.
  const row = page.locator('tbody tr', { hasText: 'Preflight' }).first();
  await expect(row).toBeVisible();
  await expect(row).toContainText('unknown_ids');
  await expect(row).toContainText('server_not_configured x');
  await page.screenshot({ path: '01-activity-list-preflight.png' });

  // 2. Type filter menu offers the new type and actually filters on it.
  await page.locator('.dropdown [role="button"]').first().click();
  await page.waitForTimeout(300);
  const menuEntry = page.locator('.dropdown-content li', { hasText: 'Preflight' }).first();
  await expect(menuEntry).toBeVisible();
  await page.screenshot({ path: '02-type-filter-menu.png' });
  await menuEntry.locator('input[type="checkbox"]').check();
  await page.waitForTimeout(1200);
  await expect(page.locator('tbody tr')).toHaveCount(await page.locator('tbody tr', { hasText: 'Preflight' }).count());
  await page.keyboard.press('Escape');

  // 3. Detail drawer: verdict section present, policy-decision section absent.
  await row.click();
  await page.waitForTimeout(800);
  const drawer = page.locator('.drawer-side.z-50');
  await expect(drawer).toContainText('Preflight Verdict');
  await expect(drawer).toContainText('unknown_ids');
  await expect(drawer).toContainText('ctl:echo');
  await expect(drawer).toContainText('server_not_configured');
  await expect(drawer).not.toContainText('Policy Decision');
  await page.screenshot({ path: '03-activity-detail-preflight.png' });
});
