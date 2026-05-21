import { test, type Page } from '@playwright/test';

// Records four web-UI walkthroughs as video (saved to outputDir by the `video`
// config). Navigation + dwell driven, robust to frontend version drift, with a
// spinner guard so we never film a loading state. Run against a LIVE mcpproxy:
//   MCPPROXY_BASE_URL=http://127.0.0.1:18082 MCPPROXY_API_KEY=... \
//     ./node_modules/.bin/playwright test --config=playwright.config.ts

const KEY = process.env.MCPPROXY_API_KEY || '';
const q = `?apikey=${KEY}`;

// Seed the API key into localStorage before any page script runs. The frontend
// reads it there (api.ts) without depending on the ?apikey URL param surviving the
// SPA router, and it persists across this context's navigations.
test.beforeEach(async ({ context }) => {
  await context.addInitScript((key) => {
    try { localStorage.setItem('mcpproxy-api-key', key); } catch { /* ignore */ }
  }, KEY);
});

// Settle: domcontentloaded, dismiss the onboarding wizard + auth modal if present,
// wait for any spinner to disappear, then dwell. Never networkidle — SSE keeps the
// network busy forever.
async function ready(page: Page, dwellMs: number) {
  await page.waitForLoadState('domcontentloaded');
  // Dismiss the first-run onboarding wizard if it overlays the page.
  await page.locator('[data-test="close-wizard"]').click({ timeout: 2500 }).catch(() => {});
  // Re-submit the key if the auth modal shows (transient validation failure).
  try {
    const keyInput = page.locator('input[placeholder*="api key" i]').first();
    if (await keyInput.isVisible({ timeout: 2000 })) {
      await keyInput.fill(KEY);
      await page.getByRole('button', { name: /set key/i }).click({ timeout: 3000 });
      await keyInput.waitFor({ state: 'detached', timeout: 6000 }).catch(() => {});
    }
  } catch { /* no modal */ }
  await page
    .locator('.loading, .spinner, [class*="spinner"], [class*="loading"], [role="progressbar"]')
    .first()
    .waitFor({ state: 'detached', timeout: 6000 })
    .catch(() => {});
  await page.waitForTimeout(dwellMs);
}

test('1 servers', async ({ page }) => {
  await page.goto(`/ui/servers${q}`);
  await ready(page, 3000);                 // KPI cards (total/connected/quarantined/tools) + server cards
  await page.mouse.wheel(0, 350);
  await page.waitForTimeout(1600);
  await page.mouse.wheel(0, -350);
  await page.waitForTimeout(700);
});

test('2 tools discovery', async ({ page }) => {
  await page.goto(`/ui/tools${q}`);
  await ready(page, 2800);
  try {
    const search = page.locator('input[type="search"], input[placeholder*="ear" i]').first();
    await search.fill('time', { timeout: 4000 });
    await page.waitForTimeout(2200);
  } catch {
    await page.waitForTimeout(2000);
  }
});

test('3 activity log', async ({ page }) => {
  await page.goto(`/ui/activity${q}`);
  await ready(page, 3000);                 // populated: successes + a sensitive-data flag
  await page.mouse.wheel(0, 300);
  await page.waitForTimeout(2200);
});

test('4 security quarantine', async ({ page }) => {
  await page.goto(`/ui/servers/memory${q}`);
  await ready(page, 3400);                 // ServerDetail: 'Security Quarantine' alert + Approve action
  await page.waitForTimeout(800);
});
