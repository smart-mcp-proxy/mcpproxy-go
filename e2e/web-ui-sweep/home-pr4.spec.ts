// Home sweep for Spec 134 PR 4 (FR-040, FR-041, SC-011): section order, the
// attention list visible without scrolling (1440x900 and 390x844), See all by
// keyboard, shortcuts by keyboard, the collapsed topology that remembers its
// state, no sideways scroll and 44 px body buttons at 390, and screenshots
// when SWEEP_SCREENSHOT_DIR is set.
//
// Launcher: scripts/run-web-smoke.sh. Data-dependent steps skip loudly.
import { test, expect, Page } from '@playwright/test'
import * as path from 'path'

const BASE = process.env.MCPPROXY_BASE_URL || 'http://127.0.0.1:18080'
const KEY = process.env.MCPPROXY_API_KEY || ''
const SHOTS = process.env.SWEEP_SCREENSHOT_DIR || ''
const LABEL = process.env.SWEEP_LABEL || 'run'

async function open(page: Page, theme = 'light', topology?: 'open' | 'closed', keepWizard = false) {
  const u = `${BASE}/ui/${KEY ? `?apikey=${encodeURIComponent(KEY)}` : ''}`
  await page.goto(u)
  await page.evaluate(([t, tp]) => {
    localStorage.setItem('mcpproxy-theme', t as string)
    if (tp) localStorage.setItem('mcpproxy.home.topology', tp as string)
    else localStorage.removeItem('mcpproxy.home.topology')
  }, [theme, topology ?? ''])
  await page.reload()
  await page.waitForLoadState('domcontentloaded')
  await page.locator('main').first().waitFor({ state: 'visible' })
  await page.waitForTimeout(1200)
  const wiz = page.locator('[data-test="close-wizard"]')
  if (!keepWizard && (await wiz.isVisible().catch(() => false))) await wiz.click()
}

async function shot(page: Page, name: string, full = true) {
  if (!SHOTS) return
  const vp = page.viewportSize()!
  // The app scrolls inside its own container, so a tall viewport is the way to
  // see the whole page.
  if (full) await page.setViewportSize({ width: vp.width, height: 2600 })
  await page.waitForTimeout(300)
  await page.screenshot({ path: path.join(SHOTS, `${LABEL}-${name}.png`) })
  if (full) await page.setViewportSize(vp)
}

const sizes = [
  { w: 1440, h: 900, tag: '1440' },
  { w: 390, h: 844, tag: '390' },
]

for (const s of sizes) {
  test(`Home at ${s.tag}: order, SC-011, no sideways scroll, 44 px buttons`, async ({ page }) => {
    await page.setViewportSize({ width: s.w, height: s.h })
    await open(page, 'light', undefined, true)
    const wizard = page.locator('[data-test="close-wizard"]')
    if (await wizard.isVisible().catch(() => false)) {
      await shot(page, `wizard-${s.tag}`, false)
      await wizard.click()
    }
    const card = page.locator('[data-test="attention-list-card"]')
    const hasItems = await card.isVisible().catch(() => false)
    await shot(page, `home-${s.tag}-light`)

    // No sideways scroll.
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
    expect(overflow).toBeLessThanOrEqual(0)

    if (hasItems) {
      // SC-011: title and the first item (with its fix) are inside the first screen.
      const first = page.locator('[data-test^="attention-item-"]').first()
      await expect(first).toBeInViewport({ ratio: 1 })
      const box = await first.boundingBox()
      expect(box!.y + box!.height).toBeLessThanOrEqual(s.h)
      // Order: attention above usage above recent activity above fleet line.
      const ys = await page.evaluate(() =>
        ['home-attention-list', 'home-usage-strip', 'home-recent-activity', 'home-fleet-line', 'home-shortcuts', 'home-security-scan', 'home-topology']
          .map((id) => document.querySelector(`[data-test="${id}"]`)?.getBoundingClientRect().top ?? -1))
      expect(ys.every((y) => y >= 0)).toBe(true)
      expect([...ys].sort((a, b) => a - b)).toEqual(ys)
    } else {
      test.info().annotations.push({ type: 'skip-part', description: 'no attention items on this data set; SC-011 not exercised' })
    }

    if (s.w < 768) {
      // Body buttons at least 44 px tall.
      const small = await page.evaluate(() =>
        [...document.querySelectorAll('main .btn')].filter((b) => !b.closest('dialog'))
          .filter((b) => (b as HTMLElement).offsetParent !== null)
          .map((b) => ({ t: (b.textContent || '').trim().slice(0, 30), h: b.getBoundingClientRect().height }))
          .filter((b) => b.h < 43.5))
      expect(small).toEqual([])
    }
  })
}

test('See all reveals the rest in place, by keyboard (SC-002)', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await open(page)
  const btn = page.locator('[data-test="attention-see-all"]')
  if (!(await btn.isVisible().catch(() => false))) {
    test.skip(true, 'fewer than six attention items in this data set')
    return
  }
  const total = (await page.locator('[data-test="attention-list-card"] h3').innerText()).match(/\((\d+)\)/)![1]
  await expect(page.locator('[data-test^="attention-item-"]')).toHaveCount(5)
  await btn.focus()
  await page.keyboard.press('Enter')
  await expect(btn).toHaveAttribute('aria-expanded', 'true')
  await expect(page.locator('[data-test^="attention-item-"]')).toHaveCount(Number(total))
  const sixthFix = page.locator('[data-test^="attention-fix-"]').nth(5)
  await sixthFix.focus()
  const href = await sixthFix.getAttribute('href')
  await page.keyboard.press('Enter')
  await expect(page).toHaveURL(new RegExp(href!.replace(/[?]/g, '\\?').replace(/^\/?/, '')))
})

test('shortcuts are reachable and activate by keyboard at 390', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  const routes: Record<string, RegExp> = {
    'home-shortcut-connect': /\/ui\/clients/,
    'home-shortcut-import': /\/ui\/add-server/,
    'home-shortcut-sessions': /\/ui\/sessions|\/ui\/activity/,
    'home-shortcut-add-server': /\/ui\/add-server/,
    'home-shortcut-catalog': /\/ui\/(repositories|add-server)/,
  }
  for (const [id, re] of Object.entries(routes)) {
    await open(page)
    const wizard = page.locator('[data-test="close-wizard"]')
    if (await wizard.isVisible().catch(() => false)) await wizard.click()
    const link = page.locator(`[data-test="${id}"]`)
    await link.focus()
    await expect(link).toBeFocused()
    await page.keyboard.press('Enter')
    await expect(page).toHaveURL(re)
  }
})

test('topology is collapsed by default, opens by keyboard in place, remembers its state', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await open(page)
  const toggle = page.locator('[data-test="home-topology-toggle"]')
  await expect(toggle).toHaveAttribute('aria-expanded', 'false')
  await expect(page.locator('[data-test="dashboard-agents-box"]')).toHaveCount(0)
  await toggle.focus()
  await page.keyboard.press('Enter')
  await expect(toggle).toHaveAttribute('aria-expanded', 'true')
  await expect(page.locator('[data-test="dashboard-agents-box"]')).toBeVisible()
  await expect(page.locator('[data-test="overview-connected-count"]')).toBeVisible()
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
  expect(overflow).toBeLessThanOrEqual(0)
  await shot(page, 'topology-390-light')
  await page.reload()
  await page.locator('main').first().waitFor({ state: 'visible' })
  await expect(page.locator('[data-test="home-topology-toggle"]')).toHaveAttribute('aria-expanded', 'true')
  await page.setViewportSize({ width: 1440, height: 900 })
  await shot(page, 'topology-1440-light')
  await page.locator('[data-test="home-topology-toggle"]').focus()
  await page.keyboard.press('Space')
  await expect(page.locator('[data-test="home-topology-toggle"]')).toHaveAttribute('aria-expanded', 'false')
})

for (const theme of ['dark']) {
  test(`screenshots in ${theme}`, async ({ page }) => {
    for (const s of sizes) {
      await page.setViewportSize({ width: s.w, height: s.h })
      await open(page, theme, 'open')
      const wizard = page.locator('[data-test="close-wizard"]')
      if (await wizard.isVisible().catch(() => false)) await wizard.click()
      await shot(page, `home-${s.tag}-${theme}-topology`)
      await open(page, theme)
      await shot(page, `home-${s.tag}-${theme}`)
    }
  })
}

test('header online indicator no longer prints tools or routing mode', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await open(page)
  const pill = page.locator('[data-test="header-status-pill"]')
  await expect(pill).toBeVisible()
  const text = (await pill.innerText()) + (await pill.getAttribute('aria-label'))
  expect(text).not.toMatch(/tools?\b/i)
  expect(text).not.toMatch(/routing mode/i)
  if (await page.locator('[data-test="home-getting-started"]').isVisible().catch(() => false)) return // first run shows no fleet line
  await expect(page.locator('[data-test="home-fleet-line"]')).toContainText('usable')
  await expect(page.locator('[data-test="home-fleet-mode"]')).toBeVisible()
})
