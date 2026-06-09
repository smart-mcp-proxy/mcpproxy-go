import { test, expect, Page } from '@playwright/test';

const BASE = 'http://127.0.0.1:18091';
const KEY = 'uitest';
const SHOTS = '/tmp/uitest-1821/shots';

// Synthetic upstreams covering the three OAuth states the CTA must handle.
const STATS = { total_servers: 3, connected_servers: 0, quarantined_servers: 1, total_tools: 0, docker_containers: 0 };
const baseServer = (over: any) => ({
  id: over.name, url: 'https://example.com/mcp', protocol: 'streamable-http',
  enabled: true, quarantined: false, connected: false, connecting: false,
  authenticated: false, status: 'disconnected', reconnect_count: 0, tool_count: 0,
  created: '2026-06-09T00:00:00Z', updated: '2026-06-09T00:00:00Z', ...over,
});

const SERVERS = [
  baseServer({
    name: 'github-oauth',
    last_error: 'OAuth authentication required: no valid token',
    health: { level: 'unhealthy', admin_state: 'enabled', summary: 'Authentication required', detail: 'Sign in to continue', action: 'login' },
    // Even though a stale UNKNOWN-style bug-report fix step is attached, the
    // OAuth code must suppress it and the calm panel must replace the red one.
    diagnostic: {
      code: 'MCPX_OAUTH_LOGIN_REQUIRED', severity: 'warn',
      user_message: 'Sign in required.', docs_url: 'https://docs.mcpproxy.app/features/oauth',
      fix_steps: [{ type: 'link', label: 'Report a bug', url: 'https://github.com/smart-mcp-proxy/mcpproxy-go/issues/new' }],
    },
  }),
  baseServer({
    name: 'gdrive-expired',
    last_error: 'token refresh failed: invalid_grant',
    health: { level: 'unhealthy', admin_state: 'enabled', summary: 'Session expired', action: 'login' },
    diagnostic: { code: 'MCPX_OAUTH_REFRESH_EXPIRED', severity: 'error', user_message: 'Your session expired.' },
  }),
  baseServer({
    name: 'slack-quarantined-oauth',
    quarantined: true,
    health: { level: 'unhealthy', admin_state: 'enabled', summary: 'Authentication required', action: 'login' },
    diagnostic: { code: 'MCPX_OAUTH_LOGIN_REQUIRED', severity: 'warn' },
  }),
];

async function mockServers(page: Page) {
  // Subresources (tools/logs/etc.) — empty so ServerDetail renders cleanly.
  await page.route(u => /\/api\/v1\/servers\/[^?]+\//.test(u.pathname + '/'), async route => {
    await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ success: true, data: { tools: [], logs: [], servers: [] } }) });
  });
  // List endpoint.
  await page.route(u => u.pathname === '/api/v1/servers', async route => {
    await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ success: true, data: { servers: SERVERS, stats: STATS } }) });
  });
}

test('OAuth sign-in CTA — servers list + detail', async ({ page }) => {
  await mockServers(page);

  // Servers list — status chips should read amber "Sign-in required".
  await page.goto(`${BASE}/ui/servers?apikey=${KEY}`);
  await page.waitForLoadState('domcontentloaded');
  await page.locator('[data-test="server-card-title"]').first().waitFor({ timeout: 15000 });
  await page.waitForTimeout(500);
  await page.screenshot({ path: `${SHOTS}/01-servers-list.png`, fullPage: true });

  const chips = await page.locator('.card .badge', { hasText: 'Sign-in required' }).count();
  expect(chips).toBeGreaterThanOrEqual(2);

  // Detail: login-required → calm amber SignInPanel, "Log in", no bug report.
  await page.goto(`${BASE}/ui/servers/github-oauth?apikey=${KEY}`);
  await page.waitForLoadState('domcontentloaded');
  await page.locator('[data-test="oauth-signin-panel"]').waitFor({ timeout: 15000 });
  await page.waitForTimeout(400);
  await page.screenshot({ path: `${SHOTS}/02-detail-login.png`, fullPage: true });

  const panel = page.locator('[data-test="oauth-signin-panel"]');
  await expect(panel).toHaveClass(/alert-warning/);
  await expect(page.locator('[data-test="oauth-signin-login-btn"]')).toContainText('Log in');
  await expect(page.locator('[data-test="server-status-badge"]')).toContainText('Sign-in required');
  expect(await page.content()).not.toContain('issues/new');

  // Detail: expired session → error tone, "Re-login".
  await page.goto(`${BASE}/ui/servers/gdrive-expired?apikey=${KEY}`);
  await page.waitForLoadState('domcontentloaded');
  await page.locator('[data-test="oauth-signin-panel"]').waitFor({ timeout: 15000 });
  await page.waitForTimeout(400);
  await page.screenshot({ path: `${SHOTS}/03-detail-reauth.png`, fullPage: true });
  await expect(page.locator('[data-test="oauth-signin-panel"]')).toHaveClass(/alert-error/);
  await expect(page.locator('[data-test="oauth-signin-login-btn"]')).toContainText('Re-login');

  // Detail: login + quarantined → calm panel PLUS the quarantine-gate note.
  await page.goto(`${BASE}/ui/servers/slack-quarantined-oauth?apikey=${KEY}`);
  await page.waitForLoadState('domcontentloaded');
  await page.locator('[data-test="oauth-signin-panel"]').waitFor({ timeout: 15000 });
  await page.waitForTimeout(400);
  await page.screenshot({ path: `${SHOTS}/04-detail-quarantined.png`, fullPage: true });
  await expect(page.locator('[data-test="oauth-signin-quarantine-note"]')).toContainText('Approve');
});
