// Servers table sweep for Spec 134 PR 2 (FR-020..FR-026, SC-001, SC-005).
//
// The server list is fulfilled from a fixture by intercepting GET
// /api/v1/servers (the list is data, not behaviour under test here), so the
// 34- and 300-server fleets are exact and cost nothing to start. Everything
// else (shell, SSE, router, the real bundle served by mcpproxy) is real.
//
// Launcher: scripts/run-web-smoke.sh.
import { test, expect, Page } from '@playwright/test'

const BASE = process.env.MCPPROXY_BASE_URL || 'http://127.0.0.1:18080'
const KEY = process.env.MCPPROXY_API_KEY || ''

function url(route: string): string {
  const sep = route.includes('?') ? '&' : '?'
  return KEY ? `${BASE}/ui${route}${sep}apikey=${encodeURIComponent(KEY)}` : `${BASE}/ui${route}`
}

interface Fx { name: string; status: 'ready' | 'error' | 'sign_in_required' | 'needs_secret' | 'disabled' | 'needs_review' | 'connecting' }

function toServer(f: Fx, i: number) {
  const enabled = f.status !== 'disabled'
  const actions: Record<string, string[]> = { error: ['restart'], sign_in_required: ['login'], needs_secret: ['set_secret'], needs_review: ['approve'], disabled: ['enable'] }
  return {
    name: f.name, protocol: i % 2 ? 'http' : 'stdio', enabled, quarantined: f.status === 'needs_review',
    connected: f.status === 'ready', connecting: f.status === 'connecting', tool_count: 1 + (i % 9),
    last_error: f.status === 'error' ? 'failed to connect: exec: "nope": executable file not found in $PATH' : undefined,
    command: i % 2 ? undefined : 'nope', url: i % 2 ? 'https://example.invalid/mcp' : undefined,
    health: {
      level: f.status === 'ready' ? 'healthy' : 'unhealthy',
      admin_state: f.status === 'disabled' ? 'disabled' : f.status === 'needs_review' ? 'quarantined' : 'enabled',
      summary: f.status === 'ready' ? `Connected (${1 + (i % 9)} tools)` : ({ error: 'Server error', sign_in_required: 'Sign-in required', needs_secret: 'Secret required', needs_review: 'Needs review', connecting: 'Connecting', disabled: 'Disabled' } as Record<string, string>)[f.status], status: f.status, usable: f.status === 'ready', actions: actions[f.status] ?? [],
      action: (actions[f.status] ?? [])[0] ?? '',
    },
  }
}

async function mockServers(page: Page, fleet: Fx[]) {
  const servers = fleet.map(toServer)
  await page.route(/\/api\/v1\/servers(\?.*)?$/, async route => {
    if (route.request().method() !== 'GET') return route.continue()
    await route.fulfill({ contentType: 'application/json', body: JSON.stringify({ success: true, data: { servers } }) })
  })
}

async function open(page: Page, route: string) {
  await page.goto(url(route))
  await page.waitForLoadState('domcontentloaded')
  const close = page.locator('[data-test="close-wizard"]')
  if (await close.isVisible().catch(() => false)) await close.click()
}

// 28 usable, 6 enabled and not usable, none disabled (spec User Story 1), with
// the broken ones spread through configuration order.
function fleet34(): Fx[] {
  const broken: Fx['status'][] = ['error', 'sign_in_required', 'needs_secret', 'error', 'needs_review', 'connecting']
  const out: Fx[] = []
  let b = 0
  for (let i = 0; i < 34; i++) {
    if (i % 6 === 2 && b < 6) out.push({ name: `srv-${String(i).padStart(3, '0')}`, status: broken[b++] })
    else out.push({ name: `srv-${String(i).padStart(3, '0')}`, status: 'ready' })
  }
  while (b < 6) { out[33 - b] = { name: out[33 - b].name, status: broken[b] }; b++ }
  return out
}

test('SC-001: 34 servers at 1440x900: at least 14 rows in the first screen and all 6 unusable among them', async ({ page }, info) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  const fleet = fleet34()
  expect(fleet.filter(f => f.status !== 'ready')).toHaveLength(6)
  await mockServers(page, fleet)
  await open(page, '/servers?filter=all')
  await expect(page.locator('[data-test="server-row"]').first()).toBeVisible()
  const rows = page.locator('[data-test="server-row"]')
  await expect(rows).toHaveCount(34)
  const view = page.viewportSize()!
  // Rows wholly above the viewport bottom and above the fixed page footer.
  const footerTop = await page.evaluate(() => { const f = document.querySelector('footer'); return f ? f.getBoundingClientRect().top : Infinity })
  const limit = Math.min(view.height, footerTop)
  const inView = await rows.evaluateAll((els, h) => els.filter(el => { const r = el.getBoundingClientRect(); return r.top >= 0 && r.bottom <= h }).map(el => el.getAttribute('data-server')), limit)
  info.annotations.push({ type: 'rows-in-first-screen', description: String(inView.length) })
  console.log(`SC-001 rows in first screen: ${inView.length}`)
  expect(inView.length).toBeGreaterThanOrEqual(14)
  const unusable = fleet.filter(f => f.status !== 'ready').map(f => f.name)
  for (const name of unusable) expect(inView, `${name} is not in the first screen`).toContain(name)
  await page.screenshot({ path: info.outputPath('servers-1440.png') })
  await info.attach('servers-1440', { path: info.outputPath('servers-1440.png'), contentType: 'image/png' })
})

test('rows: command-not-found reason on one line, name opens detail, action only when actions is not empty', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await mockServers(page, [{ name: 'good', status: 'ready' }, { name: 'broken', status: 'error' }])
  await open(page, '/servers?filter=all')
  const broken = page.locator('[data-server="broken"]')
  await expect(broken.locator('[data-test="server-row-reason"]')).toHaveText(/^Command not found/)
  const reason = await broken.locator('[data-test="server-row-reason"]').evaluate(el => ({ ws: getComputedStyle(el).whiteSpace, h: el.getBoundingClientRect().height }))
  expect(reason.ws).toBe('nowrap')
  await expect(broken.locator('[data-test="server-card-primary-action"]')).toHaveText('Restart')
  await expect(page.locator('[data-server="good"] [data-test="server-card-primary-action"]')).toHaveCount(0)
  await broken.locator('[data-test="server-detail-link"]').click()
  await expect(page).toHaveURL(/\/ui\/servers\/broken/)
})

test('keyboard: name link, more menu by Tab, a menu item activates', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await mockServers(page, [{ name: 'broken', status: 'error' }])
  await open(page, '/servers?filter=all')
  const row = page.locator('[data-server="broken"]')
  await row.locator('[data-test="server-detail-link"]').focus()
  // Tab from the name: the primary action, then the more button, which opens the menu on focus.
  for (let i = 0; i < 4; i++) {
    await page.keyboard.press('Tab')
    if (await row.locator('[data-test="server-card-menu"]').isVisible()) break
  }
  await expect(row.locator('[data-test="server-card-menu"]')).toBeVisible()
  for (let i = 0; i < 12; i++) {
    await page.keyboard.press('Tab')
    if (await page.evaluate(() => document.activeElement?.getAttribute('data-test') === 'server-card-menu-logs')) break
  }
  await expect(row.locator('[data-test="server-card-menu-logs"]')).toBeFocused()
  await page.keyboard.press('Enter')
  await expect(page).toHaveURL(/\/ui\/servers\/broken\?tab=logs/)
})

test('filters: chips count and set ?filter=, More filters keep Connected/Enabled/Quarantined, legacy status works', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await mockServers(page, fleet34())
  await open(page, '/servers')
  await expect(page.locator('[data-test="kpi-card-total"]')).toHaveText('All (34)')
  await expect(page.locator('[data-test="servers-chip-not-online"]')).toHaveText('Not online (6)')
  await page.locator('[data-test="servers-chip-not-online"]').click()
  await expect(page).toHaveURL(/filter=not-online/)
  await expect(page.locator('[data-test="server-row"]')).toHaveCount(6)
  await page.locator('[data-test="kpi-card-total"]').click()
  await expect(page).toHaveURL(/filter=all/)
  await page.locator('[data-test="servers-more-filters-toggle"]').click()
  await expect(page.locator('[data-test="kpi-card-connected"]')).toHaveText('Connected (28)')
  await page.locator('[data-test="kpi-card-quarantined"]').click()
  await expect(page).toHaveURL(/status=quarantined/)
  await expect(page.locator('[data-test="server-row"]')).toHaveCount(1)
  await open(page, '/servers?status=enabled')
  await expect(page.locator('[data-test="server-row"]')).toHaveCount(34)
})

test('phone 390x844: rows stack, no sideways scroll, body buttons at least 44 px, bare visit defaults to Not online', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await mockServers(page, fleet34())
  await open(page, '/servers')
  await expect(page.locator('[data-test="server-row"]')).toHaveCount(6)
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
  const small = await page.locator('main button, main a.btn, main summary, main input').evaluateAll(els =>
    els.filter(el => { const r = (el as HTMLElement).getBoundingClientRect(); return r.width > 0 && r.height > 0 && r.height < 43.5 }).map(el => `${el.tagName}.${(el as HTMLElement).className}`.slice(0, 80)))
  expect(small, `controls under 44 px: ${small.join(' | ')}`).toEqual([])
  const row = page.locator('[data-test="server-row"]').first()
  const name = await row.locator('[data-test="server-detail-link"]').boundingBox()
  const reason = await row.locator('[data-test="server-row-reason"]').boundingBox()
  expect(reason!.y).toBeGreaterThan(name!.y)
  await page.screenshot({ path: test.info().outputPath('servers-390.png') })
  await test.info().attach('servers-390', { path: test.info().outputPath('servers-390.png'), contentType: 'image/png' })
})

test('FR-026: 300 servers: first row within 2 s, scripted scroll sees exactly the fixture ids', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  const fleet: Fx[] = Array.from({ length: 300 }, (_, i) => ({ name: `bulk-${String(i).padStart(3, '0')}`, status: (['ready', 'ready', 'ready', 'error', 'disabled', 'needs_review'] as const)[i % 6] }))
  await mockServers(page, fleet)
  const started = Date.now()
  await page.goto(url('/servers?filter=all'))
  await page.locator('[data-test="server-row"]').first().waitFor({ state: 'visible', timeout: 2000 })
  const firstRowMs = Date.now() - started
  test.info().annotations.push({ type: 'first-row-ms', description: String(firstRowMs) })
  console.log(`FR-026 first row after ${firstRowMs} ms`)
  expect(firstRowMs).toBeLessThan(2000)

  const seen = new Set<string>()
  const collect = async () => { for (const id of await page.locator('[data-test="server-row"]').evaluateAll(els => els.map(e => e.getAttribute('data-server') || ''))) seen.add(id) }
  await collect()
  const total = await page.evaluate(() => document.documentElement.scrollHeight)
  for (let y = 0; y <= total + 900; y += 400) {
    await page.evaluate(top => window.scrollTo(0, top), y)
    await page.waitForTimeout(30)
    await collect()
  }
  expect([...seen].sort()).toEqual(fleet.map(f => f.name).sort())
})
