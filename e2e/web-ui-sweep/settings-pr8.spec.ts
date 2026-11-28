// Spec 134 PR 8 (FR-050..FR-052): Settings sections, field help, the Save row
// and the profile editor's action row. Launcher: scripts/run-web-smoke.sh.
import { test, expect, Page } from '@playwright/test'

const BASE = process.env.MCPPROXY_BASE_URL || 'http://127.0.0.1:18080'
const KEY = process.env.MCPPROXY_API_KEY || ''

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
  await page.waitForTimeout(600)
}

async function noSidewaysScroll(page: Page) {
  const over = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
  expect(over).toBeLessThanOrEqual(0)
}

test('Settings opens on General with a side list at 1440 px', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await open(page, '/settings')
  await expect(page.locator('[data-test="settings-tab-general"]')).toHaveClass(/tab-active/)
  await expect(page.locator('[data-test="settings-section-general"]')).toBeVisible()
  await expect(page.locator('[data-test="settings-tab-select"]')).toBeHidden()
  const labels = await page.locator('[role="tab"]').allInnerTexts()
  expect(labels.map((l) => l.trim()).slice(0, 2)).toEqual(['General', 'Access and keys'])
})

test('?tab=security and ?focus= still land on their section', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await open(page, '/settings?tab=security')
  await expect(page.locator('[data-test="settings-tab-security"]')).toHaveClass(/tab-active/)
  await open(page, '/settings?focus=require_mcp_auth')
  await expect(page.locator('[data-test="settings-tab-security"]')).toHaveClass(/tab-active/)
  await open(page, '/settings?focus=routing_mode')
  await expect(page.locator('[data-test="settings-tab-general"]')).toHaveClass(/tab-active/)
})

test('at 390 px the sections are a select, nothing scrolls sideways, Save covers nothing', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await open(page, '/settings')
  await expect(page.locator('[data-test="settings-tab-select"]')).toBeVisible()
  await expect(page.locator('[data-test="settings-tabs"]')).toBeHidden()
  await noSidewaysScroll(page)
  await page.locator('[data-test="settings-tab-select"]').selectOption('advanced')
  await expect(page.locator('[data-test="settings-tab-advanced"]')).toHaveClass(/tab-active/)
  await page.locator('[data-test="settings-tab-select"]').selectOption('general')

  const apply = page.locator('[data-test="settings-apply-general"]')
  await apply.scrollIntoViewIfNeeded()
  const box = await apply.boundingBox()
  expect(box).not.toBeNull()
  // The row paints at the button's centre (a disabled button passes pointer
  // events through to its row): nothing else sits over it.
  const topmost = await page.evaluate(([x, y]) => {
    const el = document.elementFromPoint(x, y)
    return el ? !!el.closest('[data-test="settings-save-row-general"]') : false
  }, [box!.x + box!.width / 2, box!.y + box!.height / 2])
  expect(topmost).toBe(true)
  // …and it sits below the last field row of the section.
  const lastRow = await page.locator('[data-test="settings-section-general"] [data-test^="setting-row-"]').last().boundingBox()
  expect(box!.y).toBeGreaterThanOrEqual(lastRow!.y + lastRow!.height - 1)
})

test('long field help shows a summary and the full text behind a keyboard-operable button', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await open(page, '/settings?tab=general')
  const toggle = page.locator('[data-test="settings-section-general"] [data-test^="setting-help-toggle-"]').first()
  await expect(toggle).toBeVisible()
  await toggle.focus()
  await expect(toggle).toHaveAttribute('aria-expanded', 'false')
  await page.keyboard.press('Enter')
  await expect(toggle).toHaveAttribute('aria-expanded', 'true')
  await expect(page.locator('[data-test="settings-section-general"] [data-test^="setting-help-full-"]').first()).toBeVisible()
})

test('the profile editor action row sits after the tool rules and covers nothing', async ({ page, request }) => {
  const res = await request.get(`${BASE}/api/v1/profiles`, { headers: KEY ? { 'X-API-Key': KEY } : {} })
  test.skip(!res.ok(), 'profiles API unavailable')
  const body = await res.json()
  const profiles = (body.data?.profiles ?? body.profiles ?? []) as Array<{ name: string }>
  test.skip(profiles.length === 0, 'no profile in this fixture')
  for (const width of [1440, 390]) {
    await page.setViewportSize({ width, height: 900 })
    await open(page, `/profiles/${encodeURIComponent(profiles[0].name)}`)
    const footer = page.locator('[data-test="profile-editor-footer"]')
    await expect(footer).toBeVisible()
    const f = await footer.boundingBox()
    const t = await page.locator('[data-test="profile-tool-table"]').boundingBox()
    expect(f!.y).toBeGreaterThanOrEqual(t!.y + t!.height - 1)
    const pos = await footer.evaluate((el) => getComputedStyle(el).position)
    expect(pos).toBe('static')
    await noSidewaysScroll(page)
  }
})
