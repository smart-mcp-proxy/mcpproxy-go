// Activity calls view sweep for Spec 134 PR 5 (FR-045..FR-049, SC-005, SC-006):
// five data columns and one action column with no horizontal scroll at 1280 px,
// the row detail, one Sort control, one filter popover that opens over the
// results, the fixed boundary between filters and results, the chip overflow
// at 390 px, Group repeats on by default, and the Sessions view.
//
// Needs the fixture upstream (SWEEP_SERVER_NAME); seeds its own calls over /mcp.
// Launcher: scripts/run-web-smoke.sh. SWEEP_SCREENSHOT_DIR keeps PNGs.
import { test, expect, Page } from '@playwright/test'
import * as fs from 'node:fs'
import * as path from 'node:path'

const BASE = process.env.MCPPROXY_BASE_URL || 'http://127.0.0.1:18080'
const KEY = process.env.MCPPROXY_API_KEY || ''
const SERVER = process.env.SWEEP_SERVER_NAME || ''

function url(route: string): string {
  const sep = route.includes('?') ? '&' : '?'
  return KEY ? `${BASE}/ui${route}${sep}apikey=${encodeURIComponent(KEY)}` : `${BASE}/ui${route}`
}

async function open(page: Page, route: string, theme?: string) {
  if (theme) {
    await page.goto(url('/'))
    await page.evaluate((t) => window.localStorage.setItem('mcpproxy-theme', t), theme)
  }
  await page.goto(url(route))
  await page.waitForLoadState('domcontentloaded')
  const close = page.locator('[data-test="close-wizard"]')
  if (await close.isVisible().catch(() => false)) await close.click()
  await page.locator('main').first().waitFor({ state: 'visible' })
  await page.waitForTimeout(600)
}

async function shot(page: Page, name: string) {
  const dir = process.env.SWEEP_SCREENSHOT_DIR
  if (!dir) return
  fs.mkdirSync(dir, { recursive: true })
  await page.screenshot({ path: path.join(dir, `${name}.png`), fullPage: false })
}

/** Makes >= 50 calls over /mcp: one long repeated run, a second tool, a few more. */
async function seedCalls() {
  const rpc = async (body: unknown, sid?: string) => {
    const headers: Record<string, string> = { 'Content-Type': 'application/json', Accept: 'application/json, text/event-stream' }
    if (KEY) headers['X-API-Key'] = KEY
    if (sid) headers['Mcp-Session-Id'] = sid
    const r = await fetch(`${BASE}/mcp`, { method: 'POST', headers, body: JSON.stringify(body) })
    await r.text()
    return r.headers.get('mcp-session-id') || sid || ''
  }
  const sid = await rpc({ jsonrpc: '2.0', id: 1, method: 'initialize', params: { protocolVersion: '2025-03-26', capabilities: {}, clientInfo: { name: 'Sweep Agent', version: '1' } } })
  await rpc({ jsonrpc: '2.0', method: 'notifications/initialized' }, sid)
  let id = 2
  const call = (tool: string, args: unknown) => rpc({ jsonrpc: '2.0', id: id++, method: 'tools/call', params: { name: 'call_tool_read', arguments: { name: tool, args, intent: { operation_type: 'read', reason: `sweep ${id}` } } } }, sid)
  for (let i = 0; i < 14; i++) await call(`${SERVER}:echo`, { msg: 'repeat' })
  for (let i = 0; i < 6; i++) await call(`${SERVER}:ping`, {})
  for (let i = 0; i < 30; i++) await call(`${SERVER}:echo`, { msg: `m${i}` })
  await call(`${SERVER}:no_such_tool`, {})
}

test.beforeAll(async () => {
  test.skip(!SERVER, 'needs the fixture upstream (SWEEP_SERVER_NAME)')
  const r = await fetch(`${BASE}/api/v1/activity?limit=1&type=tool_call`, { headers: KEY ? { 'X-API-Key': KEY } : {} })
  const body: any = await r.json()
  if ((body?.data?.total ?? 0) < 50) await seedCalls()
})

test('1280 px: five data columns and one action column, no horizontal scroll, no clipped cell', async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 900 })
  await open(page, '/activity')
  const headers = (await page.locator('[data-test="activity-table"] thead th').allInnerTexts()).map((t) => t.replace(/[↑↓]/g, '').trim())
  expect(headers).toEqual(['Time', 'Tool', 'Result', 'Client', 'Duration', 'Open details'])
  const first = page.locator('[data-test="activity-row"]').first()
  await expect(first.locator('[data-test="activity-tool-label"]')).toContainText(`${SERVER}:`)
  await expect(first.locator('button[data-test^="activity-open-"]')).toHaveCount(1)
  const m = await page.evaluate(() => {
    const wrap = document.querySelector('[data-test="activity-table"]')!.parentElement!
    const clipped = [...document.querySelectorAll('[data-test="activity-table"] td')].filter((td) => td.scrollWidth > td.clientWidth + 1 && getComputedStyle(td).textOverflow !== 'ellipsis' && !td.querySelector('.truncate')).length
    return { wrap: wrap.scrollWidth - wrap.clientWidth, doc: document.documentElement.scrollWidth - window.innerWidth, clipped }
  })
  expect(m.wrap, 'table wrapper scrolls sideways').toBeLessThanOrEqual(0)
  expect(m.doc, 'page scrolls sideways').toBeLessThanOrEqual(0)
  expect(m.clipped, 'clipped cells').toBe(0)
  await shot(page, 'calls-1280-light')
})

test('row detail opens from the keyboard and holds type, details, server link and attribution; Escape closes', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await open(page, '/activity')
  const btn = page.locator('[data-test="activity-row"] button[data-test^="activity-open-"]').first()
  await btn.focus()
  await page.keyboard.press('Enter')
  const panel = page.locator('[data-test="activity-detail-panel"]')
  await expect(panel.locator('[data-test="activity-detail-type"]')).toContainText('Tool Call')
  await expect(panel.locator('[data-test="activity-detail-details"]')).toBeVisible()
  await expect(panel.locator(`a[href$="/servers/${SERVER}"]`).first()).toBeVisible()
  await expect(panel.locator('[data-test="activity-drawer-attribution"]')).toBeVisible()
  await page.waitForTimeout(600)
  await shot(page, 'detail-1440-light')
  await page.keyboard.press('Escape')
  await expect(panel.locator('[data-test="activity-detail-type"]')).toHaveCount(0)
})

test('Sort control: every key in both directions, by keyboard', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await open(page, '/activity')
  const sel = page.locator('[data-test="activity-sort"]')
  const values = await sel.locator('option').evaluateAll((os) => os.map((o) => (o as HTMLOptionElement).value))
  expect(values).toHaveLength(10)
  expect(new Set(values.map((v) => v.split(':')[0]))).toEqual(new Set(['timestamp', 'type', 'server_name', 'status', 'duration_ms']))
  await sel.focus()
  await sel.selectOption('duration_ms:asc')
  const ms = async () => (await page.locator('[data-test="activity-row"]').evaluateAll((rs) => rs.map((r) => r.querySelector('td:nth-child(5)')?.textContent?.trim() || '')))
  const toNum = (t: string) => (t.endsWith('ms') ? parseFloat(t) : t.endsWith('s') ? parseFloat(t) * 1000 : NaN)
  const asc = (await ms()).map(toNum).filter((n) => !Number.isNaN(n))
  expect([...asc].sort((a, b) => a - b)).toEqual(asc)
  await expect(page.locator('[data-test="activity-group-toggle"]')).toBeDisabled()
})

test('Group repeats is on by default, expands every member, and a stored off is respected', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await open(page, '/activity')
  await page.evaluate(() => window.localStorage.removeItem('mcpproxy.activity.groupRepeats'))
  await page.reload()
  await page.locator('main').waitFor()
  await expect(page.locator('[data-test="activity-group-toggle"]')).toHaveAttribute('aria-pressed', 'true')
  const run = page.locator('[data-test="activity-run-count"]').first()
  await expect(run).toBeVisible()
  const n = parseInt((await run.innerText()).replace(/\D/g, ''), 10)
  await run.click()
  await expect(page.locator('[data-test="activity-run-member"]')).toHaveCount(n - 1)
  await shot(page, 'run-expanded-1440-light')
  await page.locator('[data-test="activity-group-toggle"]').click()
  await page.reload()
  await page.locator('main').waitFor()
  await expect(page.locator('[data-test="activity-group-toggle"]')).toHaveAttribute('aria-pressed', 'false')
  await page.evaluate(() => window.localStorage.removeItem('mcpproxy.activity.groupRepeats'))
})

test('the filter popover opens over the results without moving them; the boundary is fixed in every state', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await open(page, '/activity')
  await page.evaluate(() => window.localStorage.setItem('mcpproxy.activity.filtersExpanded', 'false'))
  await page.reload()
  await page.locator('main').waitFor()
  const top = async () => (await page.locator('[data-test="activity-results-card"]').boundingBox())!.y
  const rowsTop = async () => (await page.locator('[data-test="activity-row"]').first().boundingBox())!.y
  const y0 = await top()
  const r0 = await rowsTop()
  const toggle = page.locator('[data-test="activity-filters-toggle"]')
  await toggle.click()
  const pop = page.locator('[data-test="activity-filter-popover"]')
  await expect(pop).toBeVisible()
  await expect(pop).toBeFocused()
  expect(await top(), 'results card moved when the popover opened').toBeCloseTo(y0, 0)
  expect(await rowsTop(), 'first row moved when the popover opened').toBeCloseTo(r0, 0)
  const pos = await pop.evaluate((n) => getComputedStyle(n).position)
  expect(pos).toBe('absolute')
  for (const label of ['Filter by server', 'Filter by status', 'Filter by authentication type', 'Filter by sensitive data', 'Filter by session', 'Filter activity from date and time', 'Filter activity to date and time']) {
    await expect(pop.locator(`[aria-label="${label}"]`)).toBeVisible()
  }
  await shot(page, 'popover-1440-light')
  await page.keyboard.press('Escape')
  await expect(pop).toHaveCount(0)
  await expect(toggle).toBeFocused()

  // The same boundary with an empty result and with an error-free filtered one.
  await open(page, '/activity?status=rejected&type=tool_call')
  await expect(page.locator('text=No matching activities')).toBeVisible()
  expect(await top(), 'boundary differs when results are empty').toBeCloseTo(y0 + 0, -1)
  await shot(page, 'empty-1440-light')
  await open(page, '/activity?status=error')
  const withChip = await top()
  expect(Math.abs(withChip - y0), 'one chip moved the boundary').toBeLessThanOrEqual(1)
})

test('390 px: many long filters overflow into a list that removes each; no sideways scroll; body buttons 44 px', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  const long = encodeURIComponent('a-very-long-server-name-for-the-overflow-check')
  await open(page, `/activity?view=calls&status=success&server=${long}&auth_type=admin&session=ws-0123456789abcdef0123456789&type=tool_call,policy_decision,internal_tool_call`)
  const more = page.locator('[data-test="activity-filter-overflow"]')
  await expect(more).toBeVisible()
  const total = await page.locator('[data-test="activity-active-filters"] button').count()
  await more.click()
  const list = page.locator('[data-test="activity-filter-overflow-list"]')
  await expect(list.locator('button')).toHaveCount(total)
  await shot(page, 'overflow-390-light')
  await list.locator('[data-test="activity-overflow-chip-server"]').click()
  await expect(page.locator('[data-test="activity-filter-chip-server"]')).toHaveCount(0)
  const m = await page.evaluate(() => ({ doc: document.documentElement.scrollWidth - window.innerWidth }))
  expect(m.doc, 'page scrolls sideways').toBeLessThanOrEqual(0)
  // only the chip row may scroll sideways
  const scrolling = await page.evaluate(() => [...document.querySelectorAll('main *')].filter((e) => e.scrollWidth > e.clientWidth + 1 && ['auto', 'scroll'].includes(getComputedStyle(e).overflowX) && !e.matches('[data-test="activity-active-filters"], pre, code, .json-viewer, [data-test="activity-filter-chips"] *')).map((e) => e.getAttribute('data-test') || e.className.toString().slice(0, 40)))
  expect(scrolling, 'containers that scroll sideways').toEqual([])
  const small = await page.evaluate(() => [...document.querySelectorAll('main button, main a.btn')].filter((b) => {
    const r = b.getBoundingClientRect()
    return r.width > 0 && r.height > 0 && r.height < 43.5 && !b.closest('.sr-only')
  }).map((b) => `${b.getAttribute('data-test') || b.textContent?.trim().slice(0, 20)}:${Math.round(b.getBoundingClientRect().height)}`))
  expect(small, 'body buttons under 44 px').toEqual([])
})

test('390 px: the calls table fits, the row detail is a full-width panel, the popover is usable', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await open(page, '/activity')
  expect(await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth)).toBeLessThanOrEqual(0)
  const wrap = await page.evaluate(() => { const w = document.querySelector('[data-test="activity-table"]')!.parentElement!; return w.scrollWidth - w.clientWidth })
  expect(wrap).toBeLessThanOrEqual(0)
  await shot(page, 'calls-390-light')
  await page.locator('[data-test="activity-filters-toggle"]').click()
  const pop = page.locator('[data-test="activity-filter-popover"]')
  await expect(pop).toBeVisible()
  const box = (await pop.boundingBox())!
  expect(box.x).toBeGreaterThanOrEqual(0)
  expect(box.x + box.width).toBeLessThanOrEqual(390.5)
  await shot(page, 'popover-390-light')
  await page.keyboard.press('Escape')
  await page.locator('[data-test="activity-row"] button[data-test^="activity-open-"]').first().click()
  await page.waitForTimeout(600)
  const panel = (await page.locator('[data-test="activity-detail-panel"]').boundingBox())!
  expect(panel.width).toBeGreaterThanOrEqual(389)
  await shot(page, 'detail-390-light')
})

test('Sessions view keeps its panel; /sessions still redirects', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await open(page, '/activity?view=sessions')
  await expect(page.locator('[data-test="activity-view-tab-sessions"]')).toHaveAttribute('aria-selected', 'true')
  await expect(page.locator('[data-test="activity-table"]')).toBeHidden()
  await shot(page, 'sessions-1440-light')
  await open(page, '/sessions')
  await expect(page).toHaveURL(/\/ui\/activity\?(?:[^#]*&)?view=sessions/)
})

test('dark theme: calls table and open popover', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await open(page, '/activity', 'dark')
  await shot(page, 'calls-1440-dark')
  await page.locator('[data-test="activity-filters-toggle"]').click()
  await expect(page.locator('[data-test="activity-filter-popover"]')).toBeVisible()
  await shot(page, 'popover-1440-dark')
  await page.keyboard.press('Escape')
  await page.evaluate(() => window.localStorage.removeItem('mcpproxy-theme'))
})
