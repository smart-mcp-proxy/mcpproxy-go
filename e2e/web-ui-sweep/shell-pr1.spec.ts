// Shell sweep for Spec 134 PR 1 (FR-010..FR-015): skip link, palette never opens
// on Tab, phone overlay menu, footer disclosure, header Add server, theme menu
// by keyboard, "View as" in the page filter rows, no sideways scroll at 390 px.
//
// Launcher: scripts/run-web-smoke.sh.
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
  await page.waitForTimeout(500)
}

const ROUTES = ['/', '/servers', '/tools', '/activity', '/usage', '/clients', '/review', '/secrets', '/settings']

test('first Tab lands on "Skip to content", opens no dialog, and activating it focuses main', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await open(page, '/servers')
  await page.evaluate(() => (document.activeElement as HTMLElement | null)?.blur())
  await page.keyboard.press('Tab')
  await expect(page.locator('[data-test="skip-link"]')).toBeFocused()
  await expect(page.locator('dialog[open]')).toHaveCount(0)
  await page.keyboard.press('Enter')
  await expect(page.locator('#main-content')).toBeFocused()
})

test('Tab through the header never opens the palette; the search button, Ctrl+K and "/" do', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await open(page, '/')
  await page.evaluate(() => (document.activeElement as HTMLElement | null)?.blur())
  for (let i = 0; i < 6; i++) {
    await page.keyboard.press('Tab')
    await expect(page.locator('dialog[open]')).toHaveCount(0)
  }
  await page.locator('[data-test="header-search-input"]').focus()
  await expect(page.locator('dialog[open]')).toHaveCount(0)
  await page.keyboard.press('Enter')
  await expect(page.locator('dialog[data-test="command-palette"][open]')).toHaveCount(1)
  await expect(page.locator('[data-test="palette-input"]')).toBeFocused()
  await page.keyboard.press('Escape')
  await expect(page.locator('dialog[open]')).toHaveCount(0)
  await expect(page.locator('[data-test="header-search-input"]')).toBeFocused()
  await page.locator('main').click({ position: { x: 5, y: 5 } })
  await page.keyboard.press('/')
  await expect(page.locator('dialog[data-test="command-palette"][open]')).toHaveCount(1)
  // Escape as a user would: once the palette's field has focus.
  await expect(page.locator('[data-test="palette-input"]')).toBeFocused()
  await page.keyboard.press('Escape')
  await expect(page.locator('dialog[open]')).toHaveCount(0)
  await page.keyboard.press('Control+k')
  await expect(page.locator('dialog[data-test="command-palette"][open]')).toHaveCount(1)
})

test('phone: menu button opens the overlay, Escape closes it and returns focus; closed overlay holds no tab stops', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await open(page, '/')
  const toggle = page.locator('[data-test="header-drawer-toggle"]')
  await expect(toggle).toHaveAttribute('aria-expanded', 'false')
  expect(await page.locator('[data-test="sidebar-overlay"]').getAttribute('inert')).not.toBeNull()
  await toggle.focus()
  await page.keyboard.press('Enter')
  await expect(toggle).toHaveAttribute('aria-expanded', 'true')
  expect(await page.locator('[data-test="sidebar-overlay"]').getAttribute('inert')).toBeNull()
  await expect(page.locator('[data-test="sidebar-item-servers"]')).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(toggle).toHaveAttribute('aria-expanded', 'false')
  await expect(toggle).toBeFocused()
})

test('phone: opening the menu moves focus into it and Tab stays inside until it closes', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await open(page, '/')
  await page.locator('[data-test="header-drawer-toggle"]').focus()
  await page.keyboard.press('Enter')
  await expect.poll(() => page.evaluate(() => !!document.activeElement?.closest('[data-test="sidebar-overlay"]'))).toBe(true)
  for (let i = 0; i < 40; i++) {
    await page.keyboard.press('Tab')
    // Focus is in the menu, or has left the document (body); never in the inert page behind it.
    expect(await page.evaluate(() => !document.activeElement?.closest('.drawer-content')), `Tab ${i}`).toBe(true)
  }
  await page.keyboard.press('Escape')
  await expect(page.locator('[data-test="header-drawer-toggle"]')).toBeFocused()
})

test('phone: project links stay reachable in the footer disclosure', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await open(page, '/')
  const details = page.locator('[data-test="footer-links-compact"]')
  await expect(details).toBeVisible()
  await expect(page.locator('[data-test="footer-links-wide"]')).toBeHidden()
  await details.locator('summary').click()
  for (const name of ['Homepage', 'GitHub', 'Docs', 'Discussions']) {
    await expect(details.getByRole('link', { name })).toBeVisible()
  }
})

for (const width of [1440, 390]) {
  test(`header at ${width}px: Add server is reachable and leads to /add-server`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 })
    await open(page, '/')
    const add = page.locator('[data-test="header-add-server"]')
    await expect(add).toBeVisible()
    await add.focus()
    await page.keyboard.press('Enter')
    await expect(page).toHaveURL(/\/ui\/add-server/)
  })
}

test('theme menu is keyboard-operable: Enter opens, choice by Enter, Escape closes and returns focus', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await open(page, '/')
  const summary = page.locator('[data-test="sidebar-item-theme"]')
  await summary.focus()
  await page.keyboard.press('Enter')
  await expect(page.locator('[data-test="theme-option-dark"]')).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(page.locator('[data-test="theme-option-dark"]')).toBeHidden()
  await expect(summary).toBeFocused()
  await page.keyboard.press('Enter')
  await page.locator('[data-test="theme-option-dark"]').focus()
  await page.keyboard.press('Enter')
  await expect.poll(() => page.evaluate(() => document.documentElement.getAttribute('data-theme'))).toBe('dark')
  await page.evaluate(() => localStorage.setItem('mcpproxy-theme', 'system'))
})

for (const route of ['/servers', '/tools', '/activity', '/usage', '/clients', '/clients?tab=tokens']) {
  test(`"View as" sits in the filter row of ${route} when something can be filtered, never in the header`, async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 })
    await open(page, route)
    await expect(page.locator('header [data-test="viewing-filter"]')).toHaveCount(0)
    await expect(page.locator('header [data-test="header-viewing-slot"]')).toHaveCount(0)
    // Shown only under the present visibility rule; when it is shown, it is inside <main>.
    const control = page.locator('[data-test="viewing-filter"]')
    if (await control.count()) await expect(page.locator('main [data-test="viewing-filter"]')).toHaveCount(1)
  })
}

test('390px header: menu, logo, search, online, attention and both Add controls are fully visible, 44px tall and do not overlap', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  for (const route of ['/', '/servers', '/activity']) {
    await open(page, route)
    const ids = ['header-drawer-toggle', 'header-logo', 'header-search-icon', 'header-status-pill', 'header-attention-pill-button', 'header-add-server', 'header-add-menu']
    const boxes: Array<{ id: string; x: number; y: number; w: number; h: number }> = []
    for (const id of ids) {
      const el = page.locator(`header [data-test="${id}"]`)
      if (id === 'header-attention-pill-button' && (await el.count()) === 0) continue
      await expect(el, `${id} on ${route}`).toBeVisible()
      const b = (await el.boundingBox())!
      expect(b.x, `${id} left edge on ${route}`).toBeGreaterThanOrEqual(0)
      expect(b.x + b.width, `${id} right edge on ${route}`).toBeLessThanOrEqual(390)
      expect(b.height, `${id} height on ${route}`).toBeGreaterThanOrEqual(44)
      // Nothing is painted over it: the element (or its child) is what the centre hits.
      const hit = await el.evaluate((node) => {
        const r = node.getBoundingClientRect()
        const at = document.elementFromPoint(r.x + r.width / 2, r.y + r.height / 2)
        return !!at && (at === node || node.contains(at))
      })
      expect(hit, `${id} is covered on ${route}`).toBe(true)
      boxes.push({ id, x: b.x, y: b.y, w: b.width, h: b.height })
    }
    for (let i = 0; i < boxes.length; i++) {
      for (let j = i + 1; j < boxes.length; j++) {
        const a = boxes[i], c = boxes[j]
        const overlap = a.x < c.x + c.w - 0.5 && c.x < a.x + a.w - 0.5 && a.y < c.y + c.h - 0.5 && c.y < a.y + a.h - 0.5
        expect(overlap, `${a.id} overlaps ${c.id} on ${route}`).toBe(false)
      }
    }
  }
})

for (const route of ROUTES) {
  test(`390px: ${route} has no horizontal page scroll and the shell controls stay in the viewport`, async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    await open(page, route)
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth)
    expect(overflow, `overflow on ${route}`).toBeLessThanOrEqual(0)
    for (const id of ['header-drawer-toggle', 'header-search-icon', 'header-add-server', 'header-add-menu']) {
      const box = await page.locator(`[data-test="${id}"]`).first().boundingBox()
      expect(box, id).not.toBeNull()
      expect(box!.x + box!.width, `${id} right edge`).toBeLessThanOrEqual(390.5)
    }
  })
}

test('a view-as value carried onto a page with no scope shows a removable "not applied on this page" chip', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await open(page, '/settings?profile=e2e-carried')
  const chip = page.locator('[data-test="carried-scope-chip-profile"]')
  await expect(chip).toContainText('not applied on this page')
  await page.locator('[data-test="carried-scope-remove-profile"]').focus()
  await page.keyboard.press('Enter')
  await expect(chip).toHaveCount(0)
  await expect(page).not.toHaveURL(/profile=/)
})

test('no link in the shell or the sidebar leads to the not-found page', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await open(page, '/')
  const hrefs = await page.locator('aside a[href^="/ui/"], header a[href^="/ui/"]').evaluateAll((els) =>
    [...new Set(els.map((e) => (e as HTMLAnchorElement).getAttribute('href')!))],
  )
  expect(hrefs.length).toBeGreaterThan(5)
  for (const href of hrefs) {
    await page.goto(KEY ? `${BASE}${href}${href.includes('?') ? '&' : '?'}apikey=${encodeURIComponent(KEY)}` : `${BASE}${href}`)
    await page.locator('main').first().waitFor({ state: 'visible' })
    await expect(page.locator('main'), href).not.toContainText(/not found|404/i)
  }
})
