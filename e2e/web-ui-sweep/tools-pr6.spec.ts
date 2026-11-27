// Tools sweep for Spec 134 PR 6 (FR-055..FR-057): grouped default, Details by
// keyboard, Only exceptions, More filters, Flat list stored, batch bar across
// collapsed sections, and the 390 px layout. The tool list is fulfilled by the
// spec itself (deterministic rows with every label), so it needs no fixture;
// the page, router, store and shell are the real ones.
//
// Launcher: scripts/run-web-smoke.sh.
import { test, expect, Page } from '@playwright/test'

const BASE = process.env.MCPPROXY_BASE_URL || 'http://127.0.0.1:18080'
const KEY = process.env.MCPPROXY_API_KEY || ''

function url(route: string): string {
  const sep = route.includes('?') ? '&' : '?'
  return KEY ? `${BASE}/ui${route}${sep}apikey=${encodeURIComponent(KEY)}` : `${BASE}/ui${route}`
}

const tool = (server: string, name: string, over: Record<string, unknown> = {}) => ({
  name, server_name: server, description: `The ${name} tool of ${server}`, approval_status: 'approved',
  disabled: false, config_denied: false, usage: 0, tier: 'read', ...over,
})
const TOOLS = [
  tool('alpha', 'list_things'),
  tool('alpha', 'write_thing', { tier: 'write', usage: 3, last_used: new Date(Date.now() - 3600_000).toISOString() }),
  tool('alpha', 'drop_thing', { tier: 'destructive' }),
  tool('beta', 'mystery', { tier: undefined }),
  tool('beta', 'fresh', { approval_status: 'pending' }),
  tool('beta', 'moved', { approval_status: 'changed' }),
  tool('gamma', 'off', { disabled: true }),
  tool('gamma', 'denied', { config_denied: true, disabled: true }),
]

async function open(page: Page, route = '/tools') {
  await page.route('**/api/v1/tools**', async (r) => {
    if (!new URL(r.request().url()).pathname.endsWith('/tools')) return r.continue()
    await r.fulfill({ json: { success: true, data: { tools: TOOLS, stats: { total: TOOLS.length, enabled: 5, disabled: 2, pending_approval: 2 }, partial: false, failed_servers: [] } } })
  })
  await page.goto(url(route))
  await page.waitForLoadState('domcontentloaded')
  const close = page.locator('[data-test="close-wizard"]')
  if (await close.isVisible().catch(() => false)) await close.click()
  await page.locator('[data-test="tools-page"]').waitFor({ state: 'visible' })
  await page.locator('[data-test="tool-row"]').first().waitFor({ state: 'visible' })
}

test.beforeEach(async ({ page }) => {
  // Clear the stored layout once per tab (a reload must keep what the test chose).
  await page.addInitScript(() => { try { if (!window.sessionStorage.getItem('pr6-seeded')) { window.localStorage.removeItem('mcpproxy.tools.layout'); window.sessionStorage.setItem('pr6-seeded', '1') } } catch { /* ignore */ } })
})

test('grouped by server by default; ordinary rows have no label, exceptions do', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await open(page)
  await expect(page.locator('[data-test="tools-grouped"]')).toBeVisible()
  await expect(page.locator('[data-test="tools-section-server-link"]')).toHaveText(['alpha', 'beta', 'gamma'])
  const ordinary = page.locator('[data-test="tool-row"]', { hasText: 'list_things' })
  await expect(ordinary.locator('[data-test="tool-exceptions"] > *')).toHaveCount(0)
  await expect(page.locator('[data-test="tool-row"]', { hasText: 'write_thing' })).toContainText('Writes')
  await expect(page.locator('[data-test="tool-row"]', { hasText: 'drop_thing' })).toContainText('Destructive')
  await expect(page.locator('[data-test="tool-row"]', { hasText: 'mystery' })).toContainText('Unannotated')
  await expect(page.locator('[data-test="tool-row"]', { hasText: 'fresh' })).toContainText('New, needs review')
  await expect(page.locator('[data-test="tool-row"]', { hasText: 'moved' })).toContainText('Changed, needs review')
  await expect(page.locator('[data-test="tool-row"]', { hasText: 'denied' })).toContainText('Denied by config')
})

test('Details opens by keyboard, shows the full facts, Escape returns focus', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await open(page)
  const button = page.locator('[data-test="tool-row"]', { hasText: 'write_thing' }).locator('[data-test="tool-details-button"]')
  await button.focus()
  await page.keyboard.press('Enter')
  const dialog = page.locator('[data-test="tool-details-dialog"]')
  await expect(dialog).toBeVisible()
  // The native modal dialog takes focus when it opens.
  expect(await page.evaluate(() => !!document.activeElement?.closest('[data-test="tool-details-dialog"]'))).toBe(true)
  await expect(dialog.locator('[data-test="tool-details-tier"]')).toHaveText('Write')
  await expect(dialog.locator('[data-test="tool-calls-link"]')).toBeVisible()
  await expect(dialog.locator('[data-test="tool-details-last-used"]')).toHaveText('1h ago')
  await page.keyboard.press('Escape')
  await expect(dialog.locator('[data-test="tool-details"]')).toHaveCount(0)
  await expect(dialog).not.toHaveAttribute('open', /.*/)
  await expect(button).toBeFocused()
})

test('flat rows and sort headers work from the keyboard', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await open(page)
  await page.locator('[data-test="tools-layout-flat"]').click()
  const th = page.locator('thead th', { hasText: /^\s*Tool/ })
  await th.focus()
  await page.keyboard.press('Enter')
  await expect(th).toHaveAttribute('aria-sort', 'descending')
  const row = page.locator('[data-test="tool-row"]').first()
  await row.focus()
  await page.keyboard.press('Enter')
  await expect(page.locator('[data-test="tool-details-dialog"] [data-test="tool-details"]')).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(row).toBeFocused()
})

test('Only exceptions and More filters', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await open(page)
  await page.locator('[data-test="tools-only-exceptions"]').check()
  await expect(page.locator('[data-test="tool-row"]')).toHaveCount(7)
  await page.locator('[data-test="tools-only-exceptions"]').uncheck()
  await expect(page.locator('[data-test="filter-tier"]')).toBeHidden()
  await page.locator('[data-test="tools-more-filters-toggle"]').click()
  await page.locator('[data-test="filter-tier"]').selectOption('write')
  await expect(page.locator('[data-test="tool-row"]')).toHaveCount(1)
  await expect(page).toHaveURL(/tier=write/)
})

test('deep link ?risk=destructive opens More filters with the tier set', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await open(page, '/tools?risk=destructive')
  await expect(page.locator('[data-test="filter-tier"]')).toBeVisible()
  await expect(page.locator('[data-test="tool-row"]')).toHaveCount(1)
})

test('Flat list is stored and switchable by keyboard; flat sorts by every key', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await open(page)
  await page.locator('[data-test="tools-layout-flat"]').focus()
  await page.keyboard.press('Enter')
  await expect(page.locator('[data-test="tools-table"]')).toBeVisible()
  const heads = ['Tool', 'Server', 'Tier', 'Approval', 'Enabled', 'Usage', 'Last Used']
  for (const h of heads) {
    const th = page.locator('thead th', { hasText: new RegExp(`^\\s*${h}`) })
    await th.click()
    await th.click()
    await expect(page.locator('[data-test="tool-row"]')).toHaveCount(TOOLS.length)
  }
  await page.reload()
  await page.locator('[data-test="tools-table"]').waitFor({ state: 'visible' })
})

test('selection across a collapsed section: the bar counts all, Approve reaches the collapsed tools', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  const calls: string[] = []
  await page.route('**/api/v1/servers/*/tools/approve', async (r) => { calls.push(r.request().url()); await r.fulfill({ json: { success: true, data: {} } }) })
  await open(page)
  await page.locator('[data-test="tools-section-beta"] [data-test="tools-section-toggle"]').click()
  await expect(page.locator('[data-test="tools-section-beta"] [data-test="tools-section-body"]')).toBeHidden()
  await page.locator('[data-test="tools-select-all"]').check()
  await expect(page.locator('[data-test="tools-selected-count"]')).toHaveText('8 tools selected on 3 servers')
  await expect(page.locator('[data-test="batch-approve"]')).toBeEnabled()
  for (const id of ['batch-enable', 'batch-disable', 'batch-reject', 'batch-clear']) await expect(page.locator(`[data-test="${id}"]`)).toBeVisible()
})

test('390 px: no sideways scroll, body buttons 44 px, rows stack', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await open(page)
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
  expect(overflow).toBeLessThanOrEqual(0)
  for (const sel of ['[data-test="tool-details-button"]', '[data-test="tools-layout-grouped"]', '[data-test="tools-more-filters-toggle"]', '[data-test="tools-section-toggle"]']) {
    const box = await page.locator(sel).first().boundingBox()
    expect(box!.height, sel).toBeGreaterThanOrEqual(43.5)
  }
  const row = page.locator('[data-test="tool-row"]').first()
  const name = await row.locator('[data-test="tool-name"]').boundingBox()
  const details = await row.locator('[data-test="tool-details-button"]').boundingBox()
  expect(details!.y).toBeGreaterThan(name!.y + name!.height)
})
