// Sweep for Spec 134 PR 7 (FR-060, FR-061, FR-062): the Clients table and its Manage
// side panel, the agent-tokens table, and the Usage charts, at 1280 and 390 px, by
// keyboard. Seeded over REST through profiles-seed.ts; the core needs a fixture upstream
// (SWEEP_SERVER_NAME) for the seed and the Usage data.
//
// Launcher: scripts/run-web-smoke.sh.
import { test, expect, Page } from '@playwright/test'
import path from 'node:path'
import { CLIENT_ID, SERVER, api, cleanupProfiles, cleanupScopeActivity, openManagePanel, seedProfiles, seedScopeActivity } from './profiles-seed'

const BASE = process.env.MCPPROXY_BASE_URL || 'http://127.0.0.1:18080'
const KEY = process.env.MCPPROXY_API_KEY || ''
const REPORT_DIR = process.env.SWEEP_REPORT_DIR || './playwright-report'
const LEGACY_TOKEN = `e2e-own-list-${Date.now().toString(36)}`

function url(route: string): string {
  const sep = route.includes('?') ? '&' : '?'
  return KEY ? `${BASE}/ui${route}${sep}apikey=${encodeURIComponent(KEY)}` : `${BASE}/ui${route}`
}
async function open(page: Page, route: string) {
  await page.goto(url(route))
  await page.waitForLoadState('domcontentloaded')
  const close = page.locator('[data-test="close-wizard"]')
  if (await close.isVisible().catch(() => false)) await close.click()
  await page.locator('main').first().waitFor({ state: 'visible' })
  await page.waitForTimeout(500)
}
async function noSideways(page: Page, label: string) {
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
  expect(overflow, `${label}: the document scrolls sideways by ${overflow}px`).toBeLessThanOrEqual(0)
}

test.describe.configure({ mode: 'serial' })
test.skip(!SERVER, 'needs a fixture upstream (SWEEP_SERVER_NAME)')

test.beforeAll(async () => {
  await cleanupProfiles([], [LEGACY_TOKEN])
  await seedProfiles()
  await api('POST', '/tokens', { name: LEGACY_TOKEN, allowed_servers: [SERVER], permissions: ['read'], expires_in: '720h' })
})
test.afterAll(async () => {
  await cleanupProfiles([], [LEGACY_TOKEN])
  await cleanupScopeActivity()
})

test('Clients at 1280: one row and one button per client, Manage by keyboard, four groups, Escape returns focus', async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 900 })
  await open(page, '/clients')
  const table = page.locator('[data-test="clients-table"]')
  await expect(table.locator('thead th')).toHaveText(['Client', 'State', 'Reach', 'Last seen', 'Actions'])
  const rows = table.locator('tbody tr')
  for (const row of await rows.all()) await expect(row.locator('button')).toHaveCount(1)
  expect(await table.evaluate(el => el.scrollWidth - el.clientWidth), 'the table has no horizontal scroll').toBeLessThanOrEqual(0)
  await noSideways(page, 'Clients 1280')

  const manage = page.locator(`[data-test="client-manage-${CLIENT_ID}"]`)
  await manage.focus()
  await page.keyboard.press('Enter')
  const panel = page.locator(`[data-test="client-manage-panel-${CLIENT_ID}"]`)
  await expect(panel).toBeVisible()
  await expect(page.locator('[data-test="client-manage-title"]')).toBeFocused()
  await expect(panel.locator('h3')).toHaveText(['What it can use', 'How it connects', 'Key'])
  await expect(panel.locator('summary')).toHaveText('Disconnect or remove')
  await expect(panel.locator(`[data-test="client-manage-remove-${CLIENT_ID}"]`)).not.toHaveAttribute('open', /.*/)
  await panel.locator('summary').click()
  await expect(panel.locator(`[data-test="client-forget-${CLIENT_ID}"]`)).toBeVisible()
  await page.screenshot({ path: path.join(REPORT_DIR, 'pr7-clients-manage-1280.png') })
  await page.keyboard.press('Escape')
  await expect(panel).toHaveCount(0)
  await expect(manage).toBeFocused()
})

test('?focus=<id> opens the Manage panel; an explicit Tokens tab does not', async ({ page }) => {
  await open(page, `/clients?focus=${CLIENT_ID}`)
  await expect(page.locator(`[data-test="client-manage-panel-${CLIENT_ID}"]`)).toBeVisible()
  await open(page, `/clients?tab=tokens&focus=${CLIENT_ID}`)
  await expect(page.locator('[data-test="client-manage-root"]')).toHaveCount(0)
})

test('Clients at 390: no sideways scroll, full-width panel, body buttons at least 44 px', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await open(page, '/clients')
  await noSideways(page, 'Clients 390')
  const manage = page.locator(`[data-test="client-manage-${CLIENT_ID}"]`)
  expect((await manage.boundingBox())!.height, 'Manage height').toBeGreaterThanOrEqual(44)
  await manage.click()
  const panel = page.locator(`[data-test="client-manage-panel-${CLIENT_ID}"]`)
  await expect(panel).toBeVisible()
  await panel.locator('summary').click()
  expect((await panel.boundingBox())!.width, 'the panel is full width on a phone').toBeGreaterThanOrEqual(388)
  await noSideways(page, 'Manage panel 390')
  const short = await panel.locator('button:visible').evaluateAll(buttons =>
    buttons.map(b => ({ label: (b.textContent || b.getAttribute('aria-label') || '').trim(), h: b.getBoundingClientRect().height })).filter(b => b.h < 44))
  expect(short, 'panel buttons under 44 px').toEqual([])
  await page.screenshot({ path: path.join(REPORT_DIR, 'pr7-clients-manage-390.png') })
})

test('Tokens at 1280: no horizontal scroll, no clipped content, every action reachable; the badge names the configured limits', async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 900 })
  await open(page, '/clients?tab=tokens')
  const table = page.locator('[data-test="tokens-table"]')
  await expect(table).toBeVisible()
  expect(await table.evaluate(el => el.scrollWidth - el.clientWidth), 'tokens table scroll').toBeLessThanOrEqual(0)
  await noSideways(page, 'Tokens 1280')
  await expect(page.locator(`[data-test="token-legacy-badge-${LEGACY_TOKEN}"]`)).toHaveText('Own server list')
  await expect(page.locator('body')).not.toContainText('Legacy scope badge')
  const row = page.locator(`[data-test="token-row-${LEGACY_TOKEN}"]`)
  for (const name of ['Regenerate', 'Revoke']) await expect(row.getByRole('button', { name })).toBeVisible()
  // Nothing in a cell is cut off: every cell's content fits its box.
  const clipped = await table.evaluate(el => Array.from(el.querySelectorAll('td, th')).filter(c => (c as HTMLElement).scrollWidth > (c as HTMLElement).clientWidth + 1).map(c => (c.textContent || '').trim().slice(0, 30)))
  expect(clipped, 'clipped cells').toEqual([])
  await page.locator(`[data-test="token-expand-${LEGACY_TOKEN}"]`).click()
  const facts = page.locator(`[data-test="token-facts-${LEGACY_TOKEN}"]`)
  for (const label of ['Kind', 'Mode', 'Prefix', 'Last used']) await expect(facts).toContainText(label)
  await page.screenshot({ path: path.join(REPORT_DIR, 'pr7-tokens-1280.png') })
})

test('Tokens at 390: no sideways scroll, action buttons at least 44 px', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await open(page, '/clients?tab=tokens')
  await noSideways(page, 'Tokens 390')
  const row = page.locator(`[data-test="token-row-${LEGACY_TOKEN}"]`)
  for (const name of ['Regenerate', 'Revoke']) expect((await row.getByRole('button', { name }).boundingBox())!.height, name).toBeGreaterThanOrEqual(44)
  await page.screenshot({ path: path.join(REPORT_DIR, 'pr7-tokens-390.png') })
})

test('Usage keeps its four charts with data at 1280 and 390, no sideways scroll', async ({ page }) => {
  await seedScopeActivity()
  for (const width of [1280, 390]) {
    await page.setViewportSize({ width, height: 900 })
    await open(page, '/usage?window=all')
    await expect(page.locator('[data-test="usage-charts"]')).toBeVisible()
    for (const id of ['usage-call-histogram', 'usage-error-rate-chart', 'usage-response-size-ranking', 'usage-timeline']) {
      await expect(page.locator(`[data-test="${id}"]`), id).toBeAttached()
    }
    await expect(page.locator('[data-test="usage-call-histogram"] canvas')).toBeVisible()
    await expect(page.locator('[data-test="usage-timeline"] canvas')).toBeVisible()
    await noSideways(page, `Usage ${width}`)
    await page.screenshot({ path: path.join(REPORT_DIR, `pr7-usage-${width}.png`), fullPage: true })
  }
})
