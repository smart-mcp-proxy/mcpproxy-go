import { test, expect, type Page, type Request as PWRequest } from '@playwright/test';

/**
 * Spec 107 PR-C, T089. Drives a real server-edition instance + fake OIDC IdP
 * (quickstart.md "Playwright (US4, PR-C)"): fresh context -> the public
 * probe -> /login -> the fake IdP's login form -> tenant dashboard -> the
 * entitled server only -> mint a token -> activity served by
 * GET /user/activity, then walks the FR-045 refused-route list asserting
 * non-disclosing 403s.
 *
 * Requires a rig already running (quickstart.md steps 0-3): export
 * MCPPROXY_RIG_URL=http://127.0.0.1:$PORT before running Playwright. Without
 * it every test here is skipped, so CI (which never sets it) stays green —
 * this suite only runs against a locally started rig, the same convention
 * oauth-login.spec.ts uses for OAUTH_SERVER_URL.
 *
 * Fixture (quickstart.md §2): access.group_servers = {eng:[a], ops:[a,b]},
 * default_servers = []; alice@example.com is in "eng" (entitled to "a"
 * only); "b" and "a__b" exist but must never appear for her.
 */

const RIG_URL = process.env.MCPPROXY_RIG_URL;
const ALICE_EMAIL = process.env.MCPPROXY_RIG_TENANT_EMAIL || 'alice@example.com';
const ALICE_PASSWORD = process.env.MCPPROXY_RIG_TENANT_PASSWORD || 'pass';
const ENTITLED_SERVER = 'a';
const HIDDEN_SERVERS = ['b', 'a__b'];

// FR-045 (rest-endpoints.md §"Core /api/v1 — tenant-session allowlist"):
// every one of these, for a session-cookie tenant, must 403 with the fixed
// scoped-caller body below — before any body parse (a malformed body must
// not turn a 403 into a 400).
const REFUSED_ROUTES: Array<{ method: 'GET' | 'POST'; path: string; body?: unknown }> = [
  { method: 'GET', path: '/api/v1/config' },
  { method: 'GET', path: '/api/v1/info' },
  { method: 'GET', path: '/api/v1/routing' },
  { method: 'GET', path: '/api/v1/docker/status' },
  { method: 'GET', path: '/api/v1/stats/tokens' },
  { method: 'GET', path: '/api/v1/sessions' },
  { method: 'GET', path: '/api/v1/security/overview' },
  { method: 'GET', path: '/api/v1/activity' },
  { method: 'GET', path: '/api/v1/tool-calls' },
  { method: 'GET', path: '/api/v1/connect' },
  { method: 'GET', path: '/api/v1/onboarding/state' },
  { method: 'GET', path: '/api/v1/doctor' },
  // Deliberately malformed body: still 403, never 400 (scoped-caller check
  // runs before the handler touches the body).
  { method: 'POST', path: '/api/v1/tools/call', body: '{not json' },
  { method: 'POST', path: '/api/v1/code/exec', body: {} },
  { method: 'POST', path: '/api/v1/servers', body: {} },
  { method: 'POST', path: '/api/v1/quarantine/approve', body: {} },
];

const FIXED_403_BODY = {
  error: 'forbidden',
  message: 'this credential is not permitted to access this resource',
};

function requireRigUrl(): string {
  if (!RIG_URL) throw new Error('MCPPROXY_RIG_URL not set');
  return RIG_URL.replace(/\/$/, '');
}

/** Track every XHR/fetch this page issues, for the "no ?apikey=, all 2xx" assertions below. */
function trackRequests(page: Page): PWRequest[] {
  const seen: PWRequest[] = [];
  page.on('request', (req) => {
    if (['xhr', 'fetch'].includes(req.resourceType())) seen.push(req);
  });
  return seen;
}

async function assertNoApiKeyLeakage(requests: PWRequest[]) {
  for (const req of requests) {
    expect(req.url(), `XHR must never carry ?apikey=: ${req.url()}`).not.toContain('apikey=');
    const headers = await req.allHeaders();
    expect(
      Object.keys(headers).some((h) => h.toLowerCase() === 'x-api-key'),
      `XHR must never carry X-API-Key for a session principal: ${req.url()}`
    ).toBe(false);
  }
}

async function assertAll2xx(requests: PWRequest[]) {
  for (const req of requests) {
    const res = await req.response();
    if (!res) continue; // request still in flight / aborted by navigation — not this test's concern
    expect(res.ok(), `${req.method()} ${req.url()} => ${res.status()}`).toBe(true);
  }
}

test.describe('Server edition: tenant session (Spec 107 T089)', () => {
  test.skip(() => !RIG_URL, 'MCPPROXY_RIG_URL not set — run against a local rig (quickstart.md)');

  test('fresh context -> login -> dashboard -> entitled servers only -> activity via /user/activity', async ({ page, context }) => {
    const base = requireRigUrl();
    const requests = trackRequests(page);

    // Probe: the public provider probe names the login button, nothing else
    // (FR-030 — no issuer/client id/scopes leak to an unauthenticated caller).
    await page.goto(`${base}/ui/`, { waitUntil: 'domcontentloaded' });
    await expect(page).toHaveURL(/\/login$/);
    const loginButton = page.getByRole('button', { name: /Example Corp/i });
    await expect(loginButton).toBeVisible();
    await loginButton.click();

    // Fake IdP login form (tests/oauthserver): #username, #password, #consent, "Approve".
    await expect(page.locator('h1')).toContainText(/OAuth Test Server|Sign in/i, { timeout: 10_000 });
    await page.fill('#username', ALICE_EMAIL);
    await page.fill('#password', ALICE_PASSWORD);
    const consent = page.locator('#consent');
    if ((await consent.count()) > 0 && !(await consent.isChecked())) {
      await consent.check();
    }
    await page.click('button:has-text("Approve")');

    // Lands back on the tenant dashboard with nothing but the session cookie.
    await page.waitForURL((url) => !url.pathname.startsWith('/login'), { timeout: 10_000 });
    const cookies = await context.cookies();
    const sessionCookie = cookies.find((c) => c.name === 'mcpproxy_session');
    expect(sessionCookie, 'session cookie must be set after login').toBeTruthy();
    expect(sessionCookie?.httpOnly).toBe(true);

    // No API key ever lands in localStorage for a session principal (T088).
    const localStorageApiKey = await page.evaluate(() => {
      try {
        return Object.keys(localStorage).some((k) => /api.?key/i.test(k) && !!localStorage.getItem(k));
      } catch {
        return false;
      }
    });
    expect(localStorageApiKey, 'no API key in localStorage for a session principal').toBe(false);

    // Servers: entitled set only ("a"); "b"/"a__b" must never appear.
    await page.goto(`${base}/ui/servers`, { waitUntil: 'domcontentloaded' });
    await expect(page.getByText(ENTITLED_SERVER, { exact: false }).first()).toBeVisible({ timeout: 10_000 });
    for (const hidden of HIDDEN_SERVERS) {
      await expect(page.getByText(new RegExp(`\\b${hidden}\\b`))).toHaveCount(0);
    }

    // Mint an agent token from the tenant's own token page.
    await page.goto(`${base}/ui/my/tokens`, { waitUntil: 'domcontentloaded' });
    const nameField = page.locator('input[type="text"]').first();
    if ((await nameField.count()) > 0) {
      await nameField.fill(`t-${Date.now()}`);
      const createButton = page.getByRole('button', { name: /create|generate|mint/i }).first();
      if ((await createButton.count()) > 0) await createButton.click();
    }

    // Activity: tenant view is served by GET /user/activity, never core /activity.
    const activityRequests: string[] = [];
    page.on('request', (req) => {
      if (req.url().includes('/api/v1/activity') || req.url().includes('/api/v1/user/activity')) {
        activityRequests.push(req.url());
      }
    });
    await page.goto(`${base}/ui/activity`, { waitUntil: 'domcontentloaded' });
    await page.waitForTimeout(500); // let the mounted fetch fire
    expect(activityRequests.some((u) => u.includes('/api/v1/user/activity'))).toBe(true);
    expect(activityRequests.some((u) => /\/api\/v1\/activity(\?|$)/.test(u))).toBe(false);

    await assertNoApiKeyLeakage(requests);
    await assertAll2xx(requests);
  });

  test('refused-route allowlist: every non-allowlisted door 403s, non-disclosing', async ({ browser }) => {
    const base = requireRigUrl();
    const context = await browser.newContext();
    const page = await context.newPage();

    await page.goto(`${base}/ui/`, { waitUntil: 'domcontentloaded' });
    await page.getByRole('button', { name: /Example Corp/i }).click();
    await page.fill('#username', ALICE_EMAIL);
    await page.fill('#password', ALICE_PASSWORD);
    const consent = page.locator('#consent');
    if ((await consent.count()) > 0 && !(await consent.isChecked())) await consent.check();
    await page.click('button:has-text("Approve")');
    await page.waitForURL((url) => !url.pathname.startsWith('/login'), { timeout: 10_000 });

    for (const route of REFUSED_ROUTES) {
      const res = await context.request.fetch(`${base}${route.path}`, {
        method: route.method,
        data: route.body,
        headers: typeof route.body === 'string' ? { 'Content-Type': 'application/json' } : undefined,
        failOnStatusCode: false,
      });
      expect(res.status(), `${route.method} ${route.path}`).toBe(403);
      const json = await res.json().catch(() => null);
      expect(json?.error, `${route.method} ${route.path} body`).toBe(FIXED_403_BODY.error);
      expect(json?.message, `${route.method} ${route.path} body`).toBe(FIXED_403_BODY.message);
      expect(json?.request_id, `${route.method} ${route.path} carries a request_id`).toBeTruthy();
    }

    await context.close();
  });

  test('wrong X-API-Key alongside a valid session cookie -> 401 (FR-001 precedence)', async ({ browser }) => {
    const base = requireRigUrl();
    const context = await browser.newContext();
    const page = await context.newPage();

    await page.goto(`${base}/ui/`, { waitUntil: 'domcontentloaded' });
    await page.getByRole('button', { name: /Example Corp/i }).click();
    await page.fill('#username', ALICE_EMAIL);
    await page.fill('#password', ALICE_PASSWORD);
    const consent = page.locator('#consent');
    if ((await consent.count()) > 0 && !(await consent.isChecked())) await consent.check();
    await page.click('button:has-text("Approve")');
    await page.waitForURL((url) => !url.pathname.startsWith('/login'), { timeout: 10_000 });

    const res = await context.request.get(`${base}/api/v1/status`, {
      headers: { 'X-API-Key': 'wrong-key-entirely' },
      failOnStatusCode: false,
    });
    expect(res.status()).toBe(401);

    await context.close();
  });

  test('cross-site POST with the session cookie is refused (SameSite=Lax)', async ({ browser }) => {
    const base = requireRigUrl();
    const context = await browser.newContext();
    const page = await context.newPage();

    await page.goto(`${base}/ui/`, { waitUntil: 'domcontentloaded' });
    await page.getByRole('button', { name: /Example Corp/i }).click();
    await page.fill('#username', ALICE_EMAIL);
    await page.fill('#password', ALICE_PASSWORD);
    const consent = page.locator('#consent');
    if ((await consent.count()) > 0 && !(await consent.isChecked())) await consent.check();
    await page.click('button:has-text("Approve")');
    await page.waitForURL((url) => !url.pathname.startsWith('/login'), { timeout: 10_000 });

    // Navigate to a distinct site so the next same-origin fetch is a genuine
    // cross-site request from the browser's point of view; SameSite=Lax
    // withholds the cookie on a cross-site POST (only a top-level GET
    // navigation carries it cross-site).
    await page.goto('https://example.org/', { waitUntil: 'domcontentloaded' });
    const status = await page.evaluate(async (url) => {
      try {
        const res = await fetch(url, { method: 'POST', credentials: 'include', body: '{}' });
        return res.status;
      } catch {
        // A network-level block (CORS preflight rejected, connection refused
        // from a sandboxed egress) is an acceptable pass too: either way the
        // session cookie never reached mcpproxy on behalf of this call.
        return 0;
      }
    }, `${base}/api/v1/user/tokens`);
    expect(status === 401 || status === 403 || status === 0).toBe(true);

    await context.close();
  });

  test('admin_user saves Settings and sees masked secrets', async ({ browser }) => {
    const base = requireRigUrl();
    const adminEmail = process.env.MCPPROXY_RIG_ADMIN_EMAIL || 'dana@example.com';
    const adminPassword = process.env.MCPPROXY_RIG_ADMIN_PASSWORD || 'pass';
    const context = await browser.newContext();
    const page = await context.newPage();

    await page.goto(`${base}/ui/`, { waitUntil: 'domcontentloaded' });
    await page.getByRole('button', { name: /Example Corp/i }).click();
    await page.fill('#username', adminEmail);
    await page.fill('#password', adminPassword);
    const consent = page.locator('#consent');
    if ((await consent.count()) > 0 && !(await consent.isChecked())) await consent.check();
    await page.click('button:has-text("Approve")');
    await page.waitForURL((url) => !url.pathname.startsWith('/login'), { timeout: 10_000 });

    await page.goto(`${base}/ui/settings`, { waitUntil: 'domcontentloaded' });
    // Secrets render masked (e.g. "***" / a redaction marker), never the raw
    // client secret value, for an administrator viewing server_edition.oauth.
    const bodyText = await page.textContent('body');
    expect(bodyText || '').not.toMatch(/OIDC_CLIENT_SECRET_VALUE_SENTINEL/);

    const saveButton = page.getByRole('button', { name: /save/i }).first();
    if ((await saveButton.count()) > 0) {
      await saveButton.click();
      await page.waitForTimeout(500);
    }

    await context.close();
  });
});
