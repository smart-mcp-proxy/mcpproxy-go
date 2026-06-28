import { test, expect } from '@playwright/test';

const BASE = 'http://127.0.0.1:18243';
const KEY = 'test-mcp3243-key';
const SHOTS = '/tmp/uitest-mcp3243/shots';

test.beforeEach(async ({ page }) => {
  // Suppress the first-run onboarding wizard modal (its backdrop would
  // intercept clicks on the header switcher).
  await page.request.post(`${BASE}/api/v1/onboarding/mark`, {
    headers: { 'X-API-Key': KEY, 'Content-Type': 'application/json' },
    data: { engaged: true },
  });
  // Reset default to "all servers" so each run starts clean.
  await page.request.put(`${BASE}/api/v1/profiles/active`, {
    headers: { 'X-API-Key': KEY, 'Content-Type': 'application/json' },
    data: { profile: '' },
  });
  // Seed the API key into localStorage before the SPA boots so the very first
  // render never races into an auth-error modal.
  await page.addInitScript((k) => { try { localStorage.setItem('mcpproxy-api-key', k); } catch {} }, KEY);
  await page.goto(`${BASE}/?apikey=${KEY}`);
  await page.waitForLoadState('domcontentloaded');
  await expect(page.locator('[data-test="profile-switcher-button"]')).toBeVisible();
});

test('1 default shows All servers', async ({ page }) => {
  await expect(page.locator('[data-test="profile-switcher-active"]')).toHaveText('All servers');
  await page.screenshot({ path: `${SHOTS}/01-default-all-servers.png` });
});

test('2 open menu lists profiles with server + tool counts', async ({ page }) => {
  await page.locator('[data-test="profile-switcher-button"]').click();
  await expect(page.locator('[data-test="profile-switcher-menu"]')).toBeVisible();
  const dev = page.locator('[data-test="profile-option-dev"]');
  await expect(dev).toContainText('dev');
  await expect(dev).toContainText('2 servers');
  await expect(page.locator('[data-test="profile-option-solo"]')).toContainText('1 server');
  // All-servers active badge present by default.
  await expect(page.locator('[data-test="profile-option-all"] [data-test="profile-active-badge"]')).toBeVisible();
  await page.screenshot({ path: `${SHOTS}/02-menu-open.png` });
});

test('3 select dev updates label + badge', async ({ page }) => {
  await page.locator('[data-test="profile-switcher-button"]').click();
  await page.locator('[data-test="profile-option-dev"]').click();
  await expect(page.locator('[data-test="profile-switcher-active"]')).toHaveText('dev');
  await page.screenshot({ path: `${SHOTS}/03-selected-dev.png` });
  // Reopen — badge moved to dev.
  await page.locator('[data-test="profile-switcher-button"]').click();
  await expect(page.locator('[data-test="profile-active-badge-dev"]')).toBeVisible();
  await page.screenshot({ path: `${SHOTS}/04-dev-active-in-menu.png` });
});

test('5 clear back to All servers', async ({ page }) => {
  await page.locator('[data-test="profile-switcher-button"]').click();
  await page.locator('[data-test="profile-option-dev"]').click();
  await expect(page.locator('[data-test="profile-switcher-active"]')).toHaveText('dev');
  await page.locator('[data-test="profile-switcher-button"]').click();
  await page.locator('[data-test="profile-option-all"]').click();
  await expect(page.locator('[data-test="profile-switcher-active"]')).toHaveText('All servers');
  await page.screenshot({ path: `${SHOTS}/05-cleared.png` });
});
