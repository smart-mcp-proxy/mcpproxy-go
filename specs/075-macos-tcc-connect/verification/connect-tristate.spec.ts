import { test, expect, Page } from '@playwright/test';

const BASE = 'http://127.0.0.1:18083';
const UI = `${BASE}/ui/?apikey=uitest`;
const SHOTS = '/Users/user/repos/mcpproxy-go-2833/specs/075-macos-tcc-connect/verification';

// Spec 075 (MCP-2833): the stat-only listing reports access_state for each
// client. We mock it to exercise all four content-access outcomes plus a
// content-resolved connection in one view.
const LISTING = [
  {
    id: 'codex', name: 'Codex CLI', config_path: '/Users/u/.codex/config.toml',
    exists: true, connected: false, supported: true, icon: 'codex', access_state: 'unknown',
  },
  {
    id: 'claude-code', name: 'Claude Code', config_path: '/Users/u/.claude.json',
    exists: true, connected: false, supported: true, icon: 'claude-code',
    access_state: 'denied',
    remediation:
      "macOS blocked mcpproxy from reading Claude Code's configuration (Privacy & Security ▸ App Data).\n" +
      'Fix: System Settings ▸ Privacy & Security ▸ App Data ▸ enable mcpproxy,\n' +
      'or run: tccutil reset SystemPolicyAppData com.smartmcpproxy.mcpproxy\n' +
      '(dev builds: com.smartmcpproxy.mcpproxy.dev)',
  },
  {
    id: 'cursor', name: 'Cursor', config_path: '/Users/u/.cursor/mcp.json',
    exists: true, connected: false, supported: true, icon: 'cursor', access_state: 'malformed',
  },
  {
    id: 'opencode', name: 'OpenCode', config_path: '/Users/u/.config/opencode/opencode.json',
    exists: true, connected: true, supported: true, icon: 'opencode', access_state: 'accessible',
  },
  {
    id: 'vscode', name: 'VS Code', config_path: '/Users/u/Library/Application Support/Code/User/mcp.json',
    exists: false, connected: false, supported: true, icon: 'vscode', access_state: 'absent',
  },
];

async function setupMocks(page: Page, perClient?: any) {
  await page.route('**/api/v1/onboarding/state', (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        success: true,
        data: {
          has_connected_client: true, has_configured_server: false,
          connected_client_count: 0, connected_client_ids: [],
          configured_server_count: 0, state: { engaged: false },
          should_show_wizard: false, first_mcp_client_ever: false,
          mcp_clients_seen_ever: [], incomplete_tab_count: 0,
        },
      }),
    })
  );
  // Per-client GET (explicit "Check access"). Registered before the listing so
  // the more specific path wins.
  await page.route('**/api/v1/connect/**', (route) => {
    if (route.request().method() !== 'GET') return route.continue();
    return route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ success: true, data: perClient }),
    });
  });
  await page.route('**/api/v1/connect', (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ success: true, data: LISTING }),
    })
  );
}

async function openModal(page: Page) {
  await page.goto(UI);
  await page.waitForLoadState('domcontentloaded');
  await page.getByRole('button', { name: 'Connect Clients' }).click();
  await expect(page.locator('text=Connect MCPProxy to AI Agents')).toBeVisible();
  await expect(page.getByText('Claude Code', { exact: true })).toBeVisible();
}

test('US1+US2: tri-state listing renders denied banner, malformed badge, neutral unknown', async ({ page }) => {
  await setupMocks(page);
  await openModal(page);

  // Denied → actionable remediation banner with the exact tccutil command.
  const banner = page.locator('[data-test="connect-denied-banner"]');
  await expect(banner).toBeVisible();
  await expect(banner).toContainText('tccutil reset SystemPolicyAppData com.smartmcpproxy.mcpproxy');
  await expect(page.locator('[data-test="connect-blocked-badge"]')).toBeVisible();
  await expect(page.locator('[data-test="connect-copy-tccutil"]')).toBeVisible();

  // Malformed → distinct badge, NOT a denial banner.
  await expect(page.locator('[data-test="connect-malformed-badge"]')).toBeVisible();

  // Unknown installed → neutral: Check-access action present, no banner for it.
  await expect(page.locator('[data-test="connect-check-access"]')).toBeVisible();

  // Connected → Disconnect; absent → Config not found.
  await expect(page.locator('button.btn-ghost.text-error')).toContainText('Disconnect');
  await expect(page.locator('text=Config not found')).toBeVisible();

  await page.screenshot({ path: `${SHOTS}/01-tristate-listing.png`, fullPage: true });
});

test('US2: copy resets command from the denial banner', async ({ page }) => {
  await setupMocks(page);
  await page.context().grantPermissions(['clipboard-read', 'clipboard-write']);
  await openModal(page);

  await page.locator('[data-test="connect-copy-tccutil"]').click();
  await expect(page.locator('[data-test="connect-copy-tccutil"]')).toContainText('Copied');
  const clip = await page.evaluate(() => navigator.clipboard.readText());
  expect(clip).toBe('tccutil reset SystemPolicyAppData com.smartmcpproxy.mcpproxy');

  await page.screenshot({ path: `${SHOTS}/02-copied-command.png`, fullPage: true });
});

test('US1: explicit Check-access resolves an unknown client to denied on demand', async ({ page }) => {
  await setupMocks(page, {
    id: 'codex', name: 'Codex CLI', config_path: '/Users/u/.codex/config.toml',
    exists: true, connected: false, supported: true, icon: 'codex',
    access_state: 'denied',
    remediation:
      "macOS blocked mcpproxy from reading Codex CLI's configuration (Privacy & Security ▸ App Data).\n" +
      'or run: tccutil reset SystemPolicyAppData com.smartmcpproxy.mcpproxy',
  });
  await openModal(page);

  // The Codex row starts unknown (neutral) — only one denial banner so far
  // (the pre-denied Claude Code row).
  await expect(page.locator('[data-test="connect-denied-banner"]')).toHaveCount(1);

  await page.locator('[data-test="connect-check-access"]').click();

  // After the explicit read, Codex resolves to denied → a second banner.
  await expect(page.locator('[data-test="connect-denied-banner"]')).toHaveCount(2);
  await page.screenshot({ path: `${SHOTS}/03-check-access-resolves-denied.png`, fullPage: true });
});
