import { test, expect, type Page } from '@playwright/test'
import { mkdirSync, readFileSync, realpathSync } from 'node:fs'
import { homedir } from 'node:os'
import { dirname } from 'node:path'
import { join } from 'node:path'

// Spec 132 (issue #1971): the Connect dialog's browser-login choice, driven in a
// real browser. "+ Add -> Client" lists the supported clients; Claude Code is the
// one that offers "Sign in with browser" when mcp_oauth_enabled is on.
//
// SAFETY: Connect writes the client's real config file under the core's HOME
// (~/.claude.json for Claude Code). Run this only against a core started with a
// scratch HOME (the launcher's smoke core uses the real HOME, so this spec skips
// unless MCPPROXY_SWEEP_SCRATCH_HOME names that scratch HOME, and the core must
// report its Claude Code config path inside it).
//
// Environment:
//   MCPPROXY_BASE_URL / MCPPROXY_API_KEY   the scratch core (browser login ON)
//   MCPPROXY_SWEEP_SCRATCH_HOME=<dir>      the core's scratch HOME (absolute path)
//   SWEEP_SCREENSHOT_DIR                   optional: keep a PNG of every step
//   SWEEP_BROWSER_LOGIN_OFF=1              run the feature-off checks instead
const BASE = process.env.MCPPROXY_BASE_URL || ''
const KEY = process.env.MCPPROXY_API_KEY || ''
const OFF = process.env.SWEEP_BROWSER_LOGIN_OFF === '1'
const shotDir = process.env.SWEEP_SCREENSHOT_DIR || ''

test.describe.configure({ mode: 'serial' })
test.skip(!BASE || !KEY, 'MCPPROXY_BASE_URL and MCPPROXY_API_KEY are required')
const HOME_DIR = (process.env.MCPPROXY_SWEEP_SCRATCH_HOME || '').replace(/\/+$/, '')
test.skip(!HOME_DIR.startsWith('/'), 'needs a core with a scratch HOME (Connect writes client configs); set MCPPROXY_SWEEP_SCRATCH_HOME=<absolute dir>')

// The core itself must say its Claude Code config lives inside the scratch HOME
// and the feature must be in the state this run expects; otherwise fail before
// any click, so a stray flag cannot let the spec touch a real config.
test.beforeAll(async () => {
  const res = await fetch(`${BASE}/api/v1/clients`, { headers: { 'X-API-Key': KEY } })
  const body = (await res.json()) as { data: { clients: { id: string; config_path: string }[] } }
  const cc = body.data.clients.find((c) => c.id === 'claude-code')
  expect(cc, 'core lists no claude-code client').toBeTruthy()
  // Canonical paths: a symlink or a flag naming the real HOME must not pass.
  const scratch = realpathSync(HOME_DIR)
  expect(scratch).not.toBe(realpathSync(homedir()))
  expect(realpathSync(dirname(cc!.config_path)), 'the core writes outside the scratch HOME').toBe(scratch)
  expect(cc!.config_path).toBe(`${HOME_DIR}/.claude.json`)
  // The effective flag, read from the core's own config (the metadata route also
  // answers 404 on an enabled core reached through a non-loopback listener).
  const cfg = (await (await fetch(`${BASE}/api/v1/config`, { headers: { 'X-API-Key': KEY } })).json()) as { data: { config: { mcp_oauth_enabled?: boolean } } }
  expect(!!cfg.data.config.mcp_oauth_enabled, OFF ? 'feature-off run against a core with mcp_oauth_enabled on' : 'feature-on run against a core with mcp_oauth_enabled off').toBe(!OFF)
  if (!OFF) {
    expect((await fetch(`${BASE}/.well-known/oauth-authorization-server`)).status, 'browser login is not served').toBe(200)
  }
})
const CLAUDE_JSON = `${HOME_DIR}/.claude.json`

const t = (page: Page, id: string) => page.locator(`[data-test="${id}"]`)

async function shot(page: Page, name: string) {
  if (!shotDir) return
  mkdirSync(shotDir, { recursive: true })
  await page.screenshot({ path: join(shotDir, `${name}.png`), fullPage: true })
}

async function openConnect(page: Page) {
  await page.addInitScript((k) => localStorage.setItem('mcpproxy-api-key', k), KEY)
  await page.goto(`${BASE}/ui/`, { waitUntil: 'domcontentloaded' })
  // A fresh data dir opens the setup wizard, which embeds the same client list;
  // otherwise open it from "+ Add -> Client".
  await page.locator('header [data-test="header-add-menu"]').waitFor({ state: 'visible' })
  if (!(await t(page, 'client-connect-list').isVisible().catch(() => false))) {
    await page.waitForTimeout(1500)
  }
  if (!(await t(page, 'client-connect-list').isVisible().catch(() => false))) {
    await page.locator('header [data-test="header-add-menu"]').click()
    await t(page, 'add-menu-client').click()
  }
  await expect(t(page, 'client-connect-list')).toBeVisible()
}

test.describe(OFF ? 'browser login off' : 'browser login on', () => {
  test('offers the choice only for Claude Code, previews, connects, undoes', async ({ page }) => {
    test.skip(OFF, 'feature-off run')
    const errors: string[] = []
    page.on('pageerror', (e) => errors.push(e.message))
    const original = readFileSync(CLAUDE_JSON, 'utf8')
    await openConnect(page)

    // Only a client with a recorded real-client check shows the choice.
    await t(page, 'connect-claude-code').click()
    await expect(t(page, 'client-preview-claude-code')).toBeVisible()
    await expect(t(page, 'connect-auth-choice-claude-code')).toBeVisible()
    await expect(t(page, 'connect-auth-key-claude-code')).toBeChecked()
    await shot(page, '01-preview-key')

    await t(page, 'connect-auth-oauth-claude-code').check()
    await expect(t(page, 'connect-auth-oauth-notice-claude-code')).toBeVisible()
    // The diff carries the per-client address and no credential.
    // (the preview is re-fetched when the choice changes, so wait for it)
    await expect(t(page, 'client-preview-entry-claude-code')).toContainText('/mcp/oauth/claude-code')
    const entry = await t(page, 'client-preview-entry-claude-code').innerText()
    expect(entry).not.toMatch(/mcp_agt_|X-API-Key|Authorization/)
    await expect(t(page, 'client-preview-credential-claude-code')).toHaveCount(0)
    await shot(page, '02-preview-oauth')

    // Cancel writes nothing; a second preview works.
    await t(page, 'client-preview-cancel-claude-code').click()
    await expect(t(page, 'client-preview-claude-code')).toHaveCount(0)
    expect(readFileSync(CLAUDE_JSON, 'utf8')).toBe(original) // Cancel wrote nothing
    await t(page, 'connect-claude-code').click()
    await t(page, 'connect-auth-oauth-claude-code').check()
    await expect(t(page, 'client-preview-entry-claude-code')).toContainText('/mcp/oauth/claude-code')

    await t(page, 'client-preview-confirm-claude-code').click()
    await expect(t(page, 'connect-backup-path').or(t(page, 'connect-no-backup'))).toBeVisible()
    await expect(t(page, 'connect-undo-offer')).toBeVisible()
    await shot(page, '03-connected')

    // The client row now reads connected with Disconnect; the file holds the address.
    await expect(t(page, 'connect-disconnect-claude-code')).toBeVisible()
    const written = readFileSync(CLAUDE_JSON, 'utf8')
    // Exactly the credential-free address: no headers, no query, nothing else.
    const entries = Object.values((JSON.parse(written) as { mcpServers: Record<string, Record<string, unknown>> }).mcpServers)
    expect(entries).toHaveLength(1)
    expect(entries[0].url).toBe(`${BASE}/mcp/oauth/claude-code`)
    expect(Object.keys(entries[0]).sort()).toEqual(['type', 'url'])

    // Undo restores the pre-connect state.
    await t(page, 'connect-undo').click()
    await expect(t(page, 'connect-undo-panel')).toBeVisible()
    await shot(page, '04-undo-panel')
    await t(page, 'connect-undo-confirm').click()
    await expect(t(page, 'connect-claude-code')).toBeVisible()
    await shot(page, '05-after-undo')
    expect(readFileSync(CLAUDE_JSON, 'utf8')).toBe(original) // Undo restored the file
    expect(errors).toEqual([])
  })

  test('a client without a real-client check never shows the choice', async ({ page }) => {
    test.skip(OFF, 'feature-off run')
    await openConnect(page)
    for (const id of ['cursor', 'vscode']) {
      const btn = t(page, `connect-${id}`)
      // The scratch HOME holds an (empty) config for each, so the button exists:
      // a missing one must fail the check, not skip it.
      await expect(btn).toBeVisible()
      await btn.click()
      await expect(t(page, `client-preview-${id}`)).toBeVisible()
      await expect(t(page, `connect-auth-choice-${id}`)).toHaveCount(0)
      await t(page, `client-preview-cancel-${id}`).click()
    }
  })

  test('feature off: no browser-login choice anywhere', async ({ page }) => {
    test.skip(!OFF, 'feature-on run')
    await openConnect(page)
    await t(page, 'connect-claude-code').click()
    await expect(t(page, 'client-preview-claude-code')).toBeVisible()
    await expect(t(page, 'connect-auth-choice-claude-code')).toHaveCount(0)
    await expect(t(page, 'connect-auth-oauth-claude-code')).toHaveCount(0)
    await expect(t(page, 'client-preview-credential-claude-code')).toBeVisible()
    await shot(page, '06-feature-off')
  })
})
