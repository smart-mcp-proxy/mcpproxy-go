import { test, expect, Page } from '@playwright/test';

const BASE = 'http://127.0.0.1:18091/ui';
const KEY = 'uitest';
const OUT = '/Users/user/repos/mcpproxy-go/specs/055-frontend-major-upgrades/verification/baseline';

// Routes confirmed from frontend/src/router/index.ts (createWebHistory, BASE_URL=/ui/)
// and surfaced nav from frontend/src/components/SidebarNav.vue
async function goto(page: Page, path: string) {
  // include apikey on every navigation so the SPA api client is authenticated
  const sep = path.includes('?') ? '&' : '?';
  await page.goto(`${BASE}${path}${sep}apikey=${KEY}`, { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(800);
}

async function shot(page: Page, name: string) {
  await page.screenshot({ path: `${OUT}/${name}.png`, fullPage: true });
}

test('01 Servers (home/dashboard)', async ({ page }) => {
  await goto(page, '/');
  await page.waitForTimeout(800);
  await shot(page, '01-servers-home');
});

test('02 Servers list', async ({ page }) => {
  await goto(page, '/servers');
  await page.waitForTimeout(800);
  await shot(page, '02-servers-list');
});

test('03 Global Tools', async ({ page }) => {
  await goto(page, '/tools');
  await page.waitForTimeout(800);
  await shot(page, '03-tools');
});

test('04 Activity', async ({ page }) => {
  await goto(page, '/activity');
  await page.waitForTimeout(800);
  await shot(page, '04-activity');
});

test('05 Security (Quarantine)', async ({ page }) => {
  await goto(page, '/security');
  await page.waitForTimeout(800);
  await shot(page, '05-security-quarantine');
});

test('06 Settings', async ({ page }) => {
  await goto(page, '/settings');
  await page.waitForTimeout(800);
  await shot(page, '06-settings');
});

test('07 Secrets', async ({ page }) => {
  await goto(page, '/secrets');
  await page.waitForTimeout(800);
  await shot(page, '07-secrets');
});

test('08 Agent Tokens', async ({ page }) => {
  await goto(page, '/tokens');
  await page.waitForTimeout(800);
  await shot(page, '08-tokens');
});

test('09 Add Server modal (dashboard)', async ({ page }) => {
  await goto(page, '/');
  await page.waitForTimeout(800);
  // The dashboard surfaces "Add Server" buttons that open the AddServerModal <dialog>.
  // A closed daisyUI <dialog class="modal"> keeps a backdrop close button in the DOM that
  // can intercept Playwright pointer events, so invoke the visible button's native click
  // directly to fire the Vue @click handler that sets showAddServer = true.
  const clicked = await page.evaluate(() => {
    const btns = Array.from(document.querySelectorAll('button')) as HTMLButtonElement[];
    const target = btns.find(b => /add server/i.test(b.textContent || '') && (b as HTMLElement).offsetParent !== null);
    if (target) { target.click(); return true; }
    return false;
  });
  expect(clicked).toBe(true);
  await page.waitForTimeout(800);
  // AddServerModal renders a <dialog class="modal"> with :open bound to show
  await expect(page.locator('dialog.modal[open]').first()).toBeVisible();
  await shot(page, '09-add-server-modal');
});
