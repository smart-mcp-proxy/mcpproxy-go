import { test, expect, type Page } from '@playwright/test'
import { createHash, randomBytes } from 'node:crypto'
import { mkdirSync } from 'node:fs'
import { join } from 'node:path'

// Spec 132 (issue #1971) consent page, driven in a real browser against a core
// started with MCPPROXY_MCP_OAUTH_ENABLED=true (no profiles configured, so a
// client's profile is "All servers"). A scripted public client does register ->
// authorize and follows the 302 to /ui/authorize?request=<id>; the admin API key
// reaches the page through localStorage, never a URL.
//
// Environment (exported by the launcher / by hand):
//   MCPPROXY_BASE_URL  e.g. http://127.0.0.1:19700   (must be the 127.0.0.1 origin)
//   MCPPROXY_API_KEY   the admin API key
//   SWEEP_SCREENSHOT_DIR  optional: keep a PNG of every step there
const BASE = process.env.MCPPROXY_BASE_URL || ''
const KEY = process.env.MCPPROXY_API_KEY || ''
const REDIRECT = 'http://127.0.0.1:54321/callback'
const shotDir = process.env.SWEEP_SCREENSHOT_DIR || ''

test.describe.configure({ mode: 'serial' })
test.skip(!BASE || !KEY, 'MCPPROXY_BASE_URL and MCPPROXY_API_KEY are required (core with MCPPROXY_MCP_OAUTH_ENABLED=true)')

// The standing sweep runs against a core with browser login off (the default):
// skip loudly there instead of failing.
let oauthServed = false
test.beforeAll(async () => {
  try {
    oauthServed = (await fetch(`${BASE}/.well-known/oauth-authorization-server`)).status === 200
  } catch {
    oauthServed = false
  }
})
test.beforeEach(() => {
  test.skip(!oauthServed, 'browser login is not served by this core (start it with MCPPROXY_MCP_OAUTH_ENABLED=true)')
})

const t = (page: Page, id: string) => page.locator(`[data-test="${id}"]`)

async function shot(page: Page, name: string) {
  if (!shotDir) return
  mkdirSync(shotDir, { recursive: true })
  await page.screenshot({ path: join(shotDir, `${name}.png`), fullPage: true })
}

async function sleep(ms: number) {
  await new Promise((r) => setTimeout(r, ms))
}

// fetch that waits out the public endpoints' 429 (the limiter is not under test).
async function fetchRetry(url: string, init: RequestInit = {}): Promise<Response> {
  for (let i = 0; i < 5; i++) {
    const res = await fetch(url, { ...init, redirect: 'manual' })
    if (res.status !== 429) return res
    await sleep((Number(res.headers.get('retry-after')) || 3) * 1000)
  }
  throw new Error(`still rate limited: ${url}`)
}

const admin = (path: string, init: RequestInit = {}) =>
  fetch(`${BASE}/api/v1${path}`, { ...init, headers: { 'X-API-Key': KEY, 'Content-Type': 'application/json', ...(init.headers || {}) } })

interface Pending { clientId: string; verifier: string; requestUrl: string; resource: string }

async function startSignIn(clientName: string, clientPath: string, state: string): Promise<Pending> {
  const reg = await fetchRetry(`${BASE}/oauth/register`, {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ redirect_uris: [REDIRECT], client_name: clientName }),
  })
  expect(reg.status).toBe(201)
  const clientId = (await reg.json()).client_id as string
  const verifier = randomBytes(48).toString('base64url')
  const challenge = createHash('sha256').update(verifier).digest('base64url')
  const resource = `${BASE}${clientPath}`
  const q = new URLSearchParams({
    response_type: 'code', client_id: clientId, redirect_uri: REDIRECT, code_challenge: challenge,
    code_challenge_method: 'S256', resource, state,
  })
  const auth = await fetchRetry(`${BASE}/oauth/authorize?${q}`)
  expect(auth.status).toBe(302)
  const location = auth.headers.get('location') || ''
  expect(location).toContain('/ui/authorize?request=')
  return { clientId, verifier, requestUrl: location, resource }
}

async function exchange(p: Pending, code: string) {
  const res = await fetchRetry(`${BASE}/oauth/token`, {
    method: 'POST', headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
    body: new URLSearchParams({
      grant_type: 'authorization_code', code, redirect_uri: REDIRECT, client_id: p.clientId,
      code_verifier: p.verifier, resource: p.resource,
    }),
  })
  expect(res.status).toBe(200)
  return (await res.json()) as { access_token: string }
}

// The client's redirect URI has no listener: answer it so the navigation lands.
async function catchCallback(page: Page) {
  await page.route('http://127.0.0.1:54321/**', (route) => route.fulfill({ status: 200, contentType: 'text/html', body: '<p>client callback</p>' }))
}

async function withKey(page: Page) {
  await page.addInitScript((k) => localStorage.setItem('mcpproxy-api-key', k), KEY)
}

async function loginCount(client: string): Promise<number> {
  const res = await admin(`/clients/${client}`)
  if (res.status !== 200) return 0
  const body = await res.json()
  return ((body.data?.credentials as any[]) || []).filter((c) => c.kind === 'browser_login' && c.state === 'active').length
}

test('a. per-client address: fixed client, All servers, approve, code + state, tools/list', async ({ page }) => {
  await withKey(page)
  await catchCallback(page)
  const p = await startSignIn('Cursor IDE', '/mcp/oauth/cursor', 'state-a')
  await page.goto(p.requestUrl)
  await expect(t(page, 'authorize-client-fixed')).toContainText('Cursor')
  await expect(t(page, 'authorize-reported')).toContainText('MCPProxy cannot verify that.')
  await expect(t(page, 'authorize-client-select')).toHaveCount(0)
  await expect(t(page, 'authorize-profile-select')).toHaveValue('__all__')
  await expect(t(page, 'authorize-access')).toContainText('All servers')
  await shot(page, 'a-per-client')
  await t(page, 'authorize-approve').click()
  await page.waitForURL(/127\.0\.0\.1:54321\/callback/)
  const back = new URL(page.url())
  expect(back.searchParams.get('state')).toBe('state-a')
  const code = back.searchParams.get('code') || ''
  expect(code).not.toBe('')
  const tok = await exchange(p, code)

  const headers = { 'Content-Type': 'application/json', Accept: 'application/json, text/event-stream', Authorization: `Bearer ${tok.access_token}` }
  const init = await fetch(`${BASE}/mcp/oauth/cursor`, {
    method: 'POST', headers,
    body: JSON.stringify({ jsonrpc: '2.0', id: 1, method: 'initialize', params: { protocolVersion: '2025-06-18', capabilities: {}, clientInfo: { name: 'cursor', version: '1' } } }),
  })
  expect(init.status).toBe(200)
  const sid = init.headers.get('mcp-session-id') || ''
  const list = await fetch(`${BASE}/mcp/oauth/cursor`, {
    method: 'POST', headers: { ...headers, ...(sid ? { 'Mcp-Session-Id': sid } : {}) },
    body: JSON.stringify({ jsonrpc: '2.0', id: 2, method: 'tools/list', params: {} }),
  })
  expect(list.status).toBe(200)
  expect(await list.text()).toContain('"tools"')
})

test('b. generic address: "Cursor" pre-selects the existing cursor client, approve unchanged', async ({ page }) => {
  await withKey(page)
  await catchCallback(page)
  const before = await loginCount('cursor')
  const p = await startSignIn('Cursor', '/mcp/oauth', 'state-b')
  await page.goto(p.requestUrl)
  await expect(t(page, 'authorize-client-select')).toHaveValue('cursor')
  await expect(t(page, 'authorize-suggestion-note')).toHaveText('Suggested because the app calls itself "cursor". MCPProxy cannot verify that.')
  await expect(t(page, 'authorize-access')).toContainText('the same access as')
  await shot(page, 'b-suggested')
  await t(page, 'authorize-approve').click()
  await page.waitForURL(/127\.0\.0\.1:54321\/callback/)
  const back = new URL(page.url())
  expect(back.searchParams.get('state')).toBe('state-b')
  await exchange(p, back.searchParams.get('code') || '')
  expect(await loginCount('cursor')).toBeGreaterThan(before)
})

test('c. generic address: switching to "New client" pre-fills id and name; cursor is unchanged', async ({ page }) => {
  await withKey(page)
  await catchCallback(page)
  const before = await loginCount('cursor')
  const p = await startSignIn('Cursor', '/mcp/oauth', 'state-c')
  await page.goto(p.requestUrl)
  await expect(t(page, 'authorize-client-select')).toHaveValue('cursor')
  await t(page, 'authorize-client-select').selectOption('__new__')
  await expect(t(page, 'authorize-suggestion-note')).toHaveCount(0)
  const newId = await t(page, 'authorize-new-id').inputValue()
  expect(newId).not.toBe('')
  expect(newId).not.toBe('cursor')
  expect(await t(page, 'authorize-new-name').inputValue()).not.toBe('')
  await expect(t(page, 'authorize-approve')).toBeEnabled()
  await shot(page, 'c-new-client')
  await t(page, 'authorize-approve').click()
  await page.waitForURL(/127\.0\.0\.1:54321\/callback/)
  await exchange(p, new URL(page.url()).searchParams.get('code') || '')
  expect((await admin(`/clients/${newId}`)).status).toBe(200)
  expect(await loginCount(newId)).toBe(1)
  expect(await loginCount('cursor')).toBe(before)
})

test('d. generic address, unknown name: "New client" is selected', async ({ page }) => {
  await withKey(page)
  const p = await startSignIn('Totally Unknown App', '/mcp/oauth', 'state-d')
  await page.goto(p.requestUrl)
  await expect(t(page, 'authorize-client-select')).toHaveValue('__new__')
  await expect(t(page, 'authorize-suggestion-note')).toHaveCount(0)
  expect(await t(page, 'authorize-new-id').inputValue()).toBe('totally-unknown-app')
  await shot(page, 'd-unknown')
})

test('e. deny: access_denied and the original state', async ({ page }) => {
  await withKey(page)
  await catchCallback(page)
  const p = await startSignIn('Cursor IDE', '/mcp/oauth/cursor', 'state-e')
  await page.goto(p.requestUrl)
  await t(page, 'authorize-deny').click()
  await page.waitForURL(/127\.0\.0\.1:54321\/callback/)
  const back = new URL(page.url())
  expect(back.searchParams.get('error')).toBe('access_denied')
  expect(back.searchParams.get('state')).toBe('state-e')
  expect(back.searchParams.get('code')).toBeNull()
})

// With no key the app shell also opens its own "Authentication Required" dialog
// (every page does). The consent page makes no request until a key is set, by
// either door: the dialog's field, or the page's own paste field once the dialog
// is dismissed.
test('f1. no API key: only the open-from-tray text and no detail request; the app dialog sets the key', async ({ page }) => {
  const p = await startSignIn('Cursor IDE', '/mcp/oauth/cursor', 'state-f1')
  const detailRequests: string[] = []
  page.on('request', (r) => { if (r.url().includes('/api/v1/oauth/authorizations')) detailRequests.push(r.url()) })
  await page.goto(p.requestUrl)
  await expect(t(page, 'authorize-no-key')).toContainText('Open this request from the MCPProxy tray, or paste the API key')
  await expect(t(page, 'authorize-approve')).toHaveCount(0)
  await expect(t(page, 'authorize-deny')).toHaveCount(0)
  await expect(page.getByText('Authentication Required')).toBeVisible()
  await shot(page, 'f1-no-key')
  expect(detailRequests).toEqual([])
  await page.getByPlaceholder('Enter your API key...').fill(KEY)
  await page.getByRole('button', { name: 'Set Key' }).click()
  await expect(t(page, 'authorize-client-fixed')).toContainText('Cursor')
  expect(detailRequests.length).toBeGreaterThan(0)
  await shot(page, 'f1-key-set')
})

test("f2. no API key: the page's own paste field works once the app dialog is dismissed", async ({ page }) => {
  const p = await startSignIn('Cursor IDE', '/mcp/oauth/cursor', 'state-f2')
  const detailRequests: string[] = []
  page.on('request', (r) => { if (r.url().includes('/api/v1/oauth/authorizations')) detailRequests.push(r.url()) })
  await page.goto(p.requestUrl)
  await expect(t(page, 'authorize-no-key')).toBeVisible()
  await page.getByText('Dismiss (pages stay empty)').click()
  expect(detailRequests).toEqual([])
  await t(page, 'authorize-key-input').fill(KEY)
  await t(page, 'authorize-key-submit').click()
  await expect(t(page, 'authorize-client-fixed')).toContainText('Cursor')
  expect(detailRequests.length).toBeGreaterThan(0)
  await shot(page, 'f2-key-pasted')
})

test('g. a markup-shaped client name is shown as text: no element, no dialog', async ({ page }) => {
  await withKey(page)
  let dialog = ''
  page.on('dialog', async (d) => { dialog = d.message(); await d.dismiss() })
  const p = await startSignIn('<img src=x onerror=alert(1)>', '/mcp/oauth', 'state-g')
  await page.goto(p.requestUrl)
  await expect(t(page, 'authorize-reported')).toBeVisible()
  await page.waitForTimeout(500)
  expect(dialog).toBe('')
  await expect(page.locator('img[src="x"]')).toHaveCount(0)
  await expect(t(page, 'authorize-reported')).toContainText('cannot verify')
  await shot(page, 'g-markup-name')
})

test('h. the consent page is never framed (any case)', async ({ request }) => {
  for (const path of ['/ui/authorize?request=x', '/ui/Authorize?request=x']) {
    const res = await request.get(`${BASE}${path}`)
    expect(res.status(), path).toBe(200)
    expect(res.headers()['content-security-policy'], path).toContain("frame-ancestors 'none'")
    expect(res.headers()['x-frame-options'], path).toBe('DENY')
  }
})

// ---------------------------------------------------------------------------
// Spec 132 T061 (web part): /ui/clients credential list and revoke, and the
// browser-login switch on /ui/settings. The core is expected to hold
// `mcp_oauth_enabled` in its config (not only through MCPPROXY_MCP_OAUTH_ENABLED,
// which locks the Settings switch).
// ---------------------------------------------------------------------------

// A client with an API key (POST /clients) that signed in once through the page.
async function clientWithLogin(page: Page, suffix: string): Promise<string> {
  await catchCallback(page)
  const id = `e2e-hub-${suffix}-${randomBytes(3).toString('hex')}`
  const created = await admin('/clients', { method: 'POST', body: JSON.stringify({ id, display_name: `Hub ${suffix}` }) })
  expect(created.status, await created.clone().text()).toBeLessThan(300)
  const p = await startSignIn(`Hub ${suffix} app`, `/mcp/oauth/${id}`, `state-${suffix}`)
  await page.goto(p.requestUrl)
  await t(page, 'authorize-approve').click()
  await page.waitForURL(/127\.0\.0\.1:54321\/callback/)
  await exchange(p, new URL(page.url()).searchParams.get('code') || '')
  expect(await loginCount(id)).toBe(1)
  return id
}

async function credentials(client: string): Promise<any[]> {
  const res = await admin(`/clients/${client}`)
  expect(res.status).toBe(200)
  return (await res.json()).data?.credentials || []
}

async function openClient(page: Page, id: string) {
  await page.goto(`${BASE}/ui/clients`)
  await t(page, `client-manage-${id}`).click()
  await expect(t(page, `client-credentials-${id}`)).toBeVisible()
  // Spec 134 FR-060: the revocations sit in the closed "Disconnect or remove" group.
  await page.locator(`[data-test="client-manage-remove-${id}"] summary`).click()
}

test('i. /ui/clients lists the API key and the login; revoking the API key alone leaves the login', async ({ page }) => {
  await withKey(page)
  const id = await clientWithLogin(page, 'key')
  await openClient(page, id)
  await expect(t(page, 'credential-api_key')).toBeVisible()
  const loginRow = page.locator('[data-test^="credential-login-"]')
  await expect(loginRow).toHaveCount(1)
  await expect(t(page, 'credential-reported-name')).toContainText(/hub key app/i) // the core normalises the reported name
  await shot(page, 'i-clients-credentials')

  await t(page, 'revoke-api-key').click()
  await shot(page, 'i-revoke-api-key-dialog')
  await t(page, 'revoke-credential-confirm').click()
  await expect(t(page, 'revoke-api-key')).toHaveCount(0)
  await expect(t(page, 'credential-state-api_key')).toContainText('revoked')

  const creds = await credentials(id)
  expect(creds.find((c) => c.kind === 'api_key')?.state).toBe('revoked')
  expect(creds.filter((c) => c.kind === 'browser_login' && c.state === 'active')).toHaveLength(1)
  await expect(page.locator('[data-test^="revoke-login-"]')).toBeVisible()
})

test('j. /ui/clients revokes one login through the dialog; Cancel changes nothing', async ({ page }) => {
  await withKey(page)
  const id = await clientWithLogin(page, 'login')
  await openClient(page, id)
  const revoke = page.locator('[data-test^="revoke-login-"]')
  await expect(revoke).toHaveCount(1)
  await revoke.click()
  await t(page, 'revoke-credential-cancel').click()
  expect(await loginCount(id)).toBe(1)

  await revoke.click()
  await shot(page, 'j-revoke-login-dialog')
  await t(page, 'revoke-credential-confirm').click()
  await expect(revoke).toHaveCount(0)
  expect(await loginCount(id)).toBe(0)
  // the API key is untouched
  expect((await credentials(id)).find((c) => c.kind === 'api_key')?.state).toBe('active')
  await expect(t(page, 'credential-api_key')).toBeVisible()
  await shot(page, 'j-after-revoke')
})

test('k. /ui/settings shows the browser-login switch with its badge; toggling it changes mcp_oauth_enabled', async ({ page }) => {
  await withKey(page)
  const enabled = async () => ((await (await admin('/config')).json()).data?.config?.mcp_oauth_enabled ?? false) as boolean
  expect(await enabled(), 'start the scratch core with "mcp_oauth_enabled": true in its config').toBe(true)

  await page.goto(`${BASE}/ui/settings?tab=security`)
  const row = t(page, 'setting-row-mcp_oauth_enabled')
  await expect(row).toBeVisible()
  await expect(t(page, 'setting-badge-mcp_oauth_enabled')).toHaveText('Experimental')
  await expect(row).toContainText('Browser login for MCP clients')
  const toggle = t(page, 'setting-toggle-mcp_oauth_enabled')
  await expect(toggle).toBeChecked()
  await shot(page, 'k-settings-on')

  const save = async () => {
    await page.getByRole('button', { name: 'Save changes' }).first().click()
    await sleep(1500)
  }
  await toggle.uncheck()
  await save()
  expect(await enabled()).toBe(false)
  // off needs no restart and hides the OAuth paths again
  expect((await fetch(`${BASE}/.well-known/oauth-authorization-server`)).status).toBe(404)
  await shot(page, 'k-settings-off')

  await page.reload()
  await expect(t(page, 'setting-toggle-mcp_oauth_enabled')).not.toBeChecked()
  await t(page, 'setting-toggle-mcp_oauth_enabled').check()
  await save()
  expect(await enabled()).toBe(true)
  expect((await fetch(`${BASE}/.well-known/oauth-authorization-server`)).status).toBe(200)
})
