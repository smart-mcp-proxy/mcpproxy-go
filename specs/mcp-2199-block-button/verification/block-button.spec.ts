import { test, expect, Page } from '@playwright/test';
import * as fs from 'fs';

// MCP-2199 (GH #632): Block / Block All in the Tool Quarantine view.
// The backend POST .../tools/block endpoint is not merged yet, so this sweep
// builds to the agreed contract with page.route mocks (per the issue). It will
// be re-run UNMOCKED after the backend lane merges + a rebase onto origin/main.

const BASE = 'http://127.0.0.1:18099';
const KEY = 'uitest';
const SHOT = '/tmp/mcp2199-pw/shots';
fs.mkdirSync(SHOT, { recursive: true });

const SERVER = {
  name: 'github', protocol: 'stdio', enabled: true, connected: true,
  quarantined: false, tool_count: 1,
};
const TOOL = { name: 'create_issue', description: 'Create an issue', enabled: true };

// A changed (rug-pull) tool surfaces the per-tool quarantine list; a pending one
// rides along. After a block the tool is approved+disabled so it leaves the list.
function approvalsExport(blocked: boolean) {
  return {
    success: true,
    data: {
      count: blocked ? 0 : 2,
      tools: blocked
        ? [
            { tool_name: 'create_issue', status: 'approved', enabled: false, disabled: true, description: 'Create an issue' },
            { tool_name: 'list_repos', status: 'approved', enabled: false, disabled: true, description: 'List repos' },
          ]
        : [
            { tool_name: 'create_issue', status: 'changed', enabled: true, description: 'Create an issue' },
            { tool_name: 'list_repos', status: 'pending', enabled: true, description: 'List repos' },
          ],
    },
  };
}

let blockCalls: Array<{ url: string; body: unknown }> = [];
let blocked = false;

async function installMocks(page: Page) {
  await page.route('**/api/v1/servers**', async (route) => {
    const req = route.request();
    const url = new URL(req.url());
    const p = url.pathname;
    const method = req.method();
    const json = (data: unknown) => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(data) });

    if (p === '/api/v1/servers' && method === 'GET') {
      return json({ success: true, data: { servers: [SERVER] } });
    }
    if (p === '/api/v1/servers/github/tools' && method === 'GET') {
      return json({ success: true, data: { tools: [TOOL] } });
    }
    if (p === '/api/v1/servers/github/tools/export' && method === 'GET') {
      return json(approvalsExport(blocked));
    }
    if (p.match(/\/tools\/.+\/diff$/) && method === 'GET') {
      return json({ success: true, data: {} });
    }
    if (p === '/api/v1/servers/github/tools/block' && method === 'POST') {
      blockCalls.push({ url: p, body: JSON.parse(req.postData() || '{}') });
      blocked = true;
      return json({ success: true, data: { blocked: 1 } });
    }
    return route.continue();
  });
}

async function gotoDetail(page: Page) {
  await page.goto(`${BASE}/ui/servers/github?tab=tools&apikey=${KEY}`);
  await page.waitForLoadState('domcontentloaded');
  await page.locator('[data-test="tool-quarantine-banner"]').waitFor({ state: 'visible', timeout: 15000 });
}

test.beforeEach(() => { blockCalls = []; blocked = false; });

test('Block + Block All render in the Tool Quarantine view', async ({ page }) => {
  await installMocks(page);
  await gotoDetail(page);

  await expect(page.locator('[data-test="quarantine-block-all"]')).toBeVisible();
  await expect(page.locator('[data-test="quarantine-approve-all"]')).toBeVisible();
  await expect(page.locator('[data-test="quarantine-block-create_issue"]')).toBeVisible();
  await expect(page.locator('[data-test="quarantine-block-list_repos"]')).toBeVisible();
  await page.screenshot({ path: `${SHOT}/01-quarantine-with-block-buttons.png`, fullPage: true });
});

test('Block (single) POSTs {tools:[name]} and the tool leaves the quarantine list', async ({ page }) => {
  await installMocks(page);
  await gotoDetail(page);

  await page.locator('[data-test="quarantine-block-create_issue"]').click();

  // The banner disappears once both quarantined tools are gone (block flips the
  // mock to "approved"+disabled, so the list empties on the refetch).
  await page.locator('[data-test="tool-quarantine-banner"]').waitFor({ state: 'hidden', timeout: 15000 });

  expect(blockCalls.length).toBeGreaterThanOrEqual(1);
  expect(blockCalls[0].body).toEqual({ tools: ['create_issue'] });
  await page.screenshot({ path: `${SHOT}/02-after-single-block.png`, fullPage: true });
});

test('Block All POSTs {block_all:true}', async ({ page }) => {
  await installMocks(page);
  await gotoDetail(page);

  await page.locator('[data-test="quarantine-block-all"]').click();
  await page.locator('[data-test="tool-quarantine-banner"]').waitFor({ state: 'hidden', timeout: 15000 });

  expect(blockCalls.length).toBeGreaterThanOrEqual(1);
  expect(blockCalls[0].body).toEqual({ block_all: true });
  await page.screenshot({ path: `${SHOT}/03-after-block-all.png`, fullPage: true });
});
