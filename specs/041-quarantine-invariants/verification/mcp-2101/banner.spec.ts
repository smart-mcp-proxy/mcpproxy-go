import { test, expect } from '@playwright/test';

const BASE = 'http://127.0.0.1:18082';
const KEY = 'uitest';
const SERVER = 'rugpull';
const STATE = process.env.STATE || 'B';
const OUT = process.env.OUT || `/tmp/mcp-2101-uitest/pw/shot-${STATE}.png`;

async function api(request: any, method: 'GET' | 'POST', path: string) {
  const r = await request.fetch(`${BASE}${path}`, {
    method,
    headers: { 'X-API-Key': KEY, 'Content-Type': 'application/json' },
    data: method === 'POST' ? '{}' : undefined,
  });
  return r;
}

test(`tool-quarantine banner — state ${STATE}`, async ({ page, request }) => {
  // Drive the server-level quarantine flag for the state under test.
  if (STATE === 'A') {
    await api(request, 'POST', `/api/v1/servers/${SERVER}/quarantine`);
  } else {
    await api(request, 'POST', `/api/v1/servers/${SERVER}/unquarantine`);
  }

  await page.goto(`${BASE}/ui/servers/${SERVER}?apikey=${KEY}&tab=tools`);
  await page.waitForLoadState('domcontentloaded');
  // Give the view time to fetch server + tool-approval data and render.
  await page.waitForTimeout(2500);

  const securityBanner = page.locator('[data-test="security-quarantine-banner"]');
  const toolBanner = page.locator('[data-test="tool-quarantine-banner"]');

  if (STATE === 'A') {
    // Quarantined: ONLY the server-level Security Quarantine banner. The
    // tool-level banner must be suppressed even though tools are non-approved.
    await expect(securityBanner).toBeVisible();
    await expect(toolBanner).toHaveCount(0);
  } else if (STATE === 'B') {
    // Not quarantined, baseline pending tools, NO changed tool: neither banner.
    await expect(securityBanner).toHaveCount(0);
    await expect(toolBanner).toHaveCount(0);
  } else if (STATE === 'C') {
    // Not quarantined, a `changed` (rug-pull) tool exists: the tool-level
    // banner appears; the server-level Security banner does not.
    await expect(securityBanner).toHaveCount(0);
    await expect(toolBanner).toBeVisible();
  }

  await page.screenshot({ path: OUT, fullPage: true });
});
