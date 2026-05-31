import { test, expect, Page } from '@playwright/test';

/**
 * Spec 070 (T017) — Web UI one-flow search → Add → quarantined.
 *
 * Verifies the registry "Add to MCP" button goes through the backend keystone
 * (POST /api/v1/registries/{registryId}/servers/{serverId}/add → AddServerFromRegistry)
 * instead of the old client-side install_cmd parsing, and that a server
 * declaring required inputs prompts the user before adding.
 *
 * Requires the T014 REST route (MCP-765 backend dependency) to be live.
 *
 * Environment variables:
 * - MCPPROXY_URL:       base URL of a running mcpproxy with Web UI (e.g. http://127.0.0.1:18081)
 * - MCPPROXY_API_KEY:   API key (default: uitest)
 * - REGISTRY_ID:        registry to browse (default: first registry returned)
 * - SEARCH_QUERY:       search term that returns at least one addable server (default: "")
 * - REQUIRED_SERVER_ID: optional — a serverId in REGISTRY_ID that declares a required input;
 *                       enables the prompt-flow test. Skipped when unset.
 * - REQUIRED_INPUT_NAME:the input name to fill for REQUIRED_SERVER_ID (default: detected from card).
 */

const MCPPROXY_URL = process.env.MCPPROXY_URL;
const API_KEY = process.env.MCPPROXY_API_KEY || 'uitest';
// docker-mcp-catalog reliably exposes addable stdio servers (docker run …), so
// it is the default target for the no-input happy path. Override per env.
const REGISTRY_ID = process.env.REGISTRY_ID || 'docker-mcp-catalog';
const SEARCH_QUERY = process.env.SEARCH_QUERY || '';

if (!MCPPROXY_URL) {
  throw new Error('MCPPROXY_URL environment variable is required');
}

const api = async (request: any, method: string, path: string) => {
  const res = await request.fetch(`${MCPPROXY_URL}${path}`, {
    method,
    headers: { 'X-API-Key': API_KEY, 'Content-Type': 'application/json' },
  });
  return res;
};

async function openRepositories(page: Page) {
  // Web UI is history-mode under base /ui/ (not hash routing).
  await page.goto(`${MCPPROXY_URL}/ui/repositories?apikey=${encodeURIComponent(API_KEY)}`);
  await page.waitForLoadState('domcontentloaded'); // never networkidle — SSE keeps the channel open
  await expect(page.locator('[data-test="registry-select"]')).toBeVisible({ timeout: 15000 });
}

async function selectRegistryAndSearch(page: Page, registryId: string, query: string) {
  const select = page.locator('[data-test="registry-select"]');
  if (registryId) {
    await select.selectOption(registryId);
  } else {
    // Pick the first non-placeholder option.
    const value = await select.locator('option:not([disabled])').first().getAttribute('value');
    await select.selectOption(value!);
  }
  await page.locator('[data-test="registry-search-input"]').fill(query);
  await page.locator('[data-test="registry-search-button"]').click();
  // Wait for at least one result card.
  await expect(page.locator('[data-test^="registry-server-"]').first()).toBeVisible({ timeout: 15000 });
}

test.describe('Registry one-flow add (Spec 070)', () => {
  test('search → Add (no required input) → server appears quarantined', async ({ page, request }) => {
    await openRepositories(page);
    await selectRegistryAndSearch(page, REGISTRY_ID, SEARCH_QUERY);

    // Add the first server without required inputs (no warning badge).
    const card = page
      .locator('[data-test^="registry-server-"]')
      .filter({ hasNot: page.locator('[data-test^="registry-requires-input-"]') })
      .first();
    await expect(card).toBeVisible();

    const serverId = (await card.getAttribute('data-test'))!.replace('registry-server-', '');
    await card.locator(`[data-test="registry-add-${serverId}"]`).click();

    // Success toast confirms the add (and that no prompt was required).
    await expect(page.locator('[data-test="registry-add-success"]')).toBeVisible({ timeout: 15000 });

    // The added server is present AND quarantined (backend forced it — CN-002).
    const res = await api(request, 'GET', '/api/v1/servers');
    expect(res.ok()).toBeTruthy();
    const body = await res.json();
    const servers = body.data?.servers ?? body.servers ?? [];
    expect(servers.length).toBeGreaterThan(0);
    const added = servers.find((s: any) => (s.quarantined ?? s.health?.admin_state === 'quarantined'));
    expect(added, 'at least one added server should be quarantined').toBeTruthy();
  });

  test('search → Add server that requires input → prompt blocks until provided → quarantined', async ({ page, request }) => {
    const requiredServerId = process.env.REQUIRED_SERVER_ID;
    test.skip(!requiredServerId, 'set REQUIRED_SERVER_ID to a registry server that declares a required input');
    // The required-input server may live in a different registry than the
    // no-input default; let it be targeted independently (defaults: fleur/stripe).
    const requiredRegistry = process.env.REQUIRED_REGISTRY_ID || 'fleur';
    const requiredQuery = process.env.REQUIRED_SEARCH_QUERY || requiredServerId!;

    await openRepositories(page);
    await selectRegistryAndSearch(page, requiredRegistry, requiredQuery);

    const card = page.locator(`[data-test="registry-server-${requiredServerId}"]`);
    await expect(card).toBeVisible();
    await page.locator(`[data-test="registry-add-${requiredServerId}"]`).click();

    // The required-input dialog opens; Add is blocked until the value is filled.
    const dialog = page.locator('[data-test="registry-required-input-dialog"]');
    await expect(dialog).toBeVisible();
    const submit = dialog.locator('[data-test="registry-input-submit"]');
    await expect(submit).toBeDisabled();

    const inputName = process.env.REQUIRED_INPUT_NAME;
    const inputField = inputName
      ? dialog.locator(`[data-test="registry-input-${inputName}"]`)
      : dialog.locator('[data-test^="registry-input-"]').first();
    await inputField.fill('test-value-123');
    await expect(submit).toBeEnabled();
    await submit.click();

    await expect(page.locator('[data-test="registry-add-success"]')).toBeVisible({ timeout: 15000 });
    await expect(dialog).toBeHidden();

    // Verify the env value persisted on the (quarantined) server.
    const res = await api(request, 'GET', '/api/v1/servers');
    const body = await res.json();
    const servers = body.data?.servers ?? body.servers ?? [];
    const added = servers.find((s: any) => s.name === requiredServerId || s.env);
    expect(added).toBeTruthy();
  });
});
