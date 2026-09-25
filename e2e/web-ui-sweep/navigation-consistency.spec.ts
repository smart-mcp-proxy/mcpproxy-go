// Navigation-consistency sweep (Spec 109 "ux-navigation-consistency").
//
// Created here by PR 109-e (T068) for its own independent test — server
// cards render at a stable height whatever state they are in (FR-013) — and
// extended by later PRs in the same spec (109-i, T137) as their own
// navigation-consistency scenarios land. Registered in the Playwright file
// list in scripts/run-web-smoke.sh so the smoke gate runs it from this PR
// onward, and every later addition to this file is gated too.
//
// Launcher: scripts/run-web-smoke.sh (boots a real mcpproxy instance with its
// embedded frontend, never a dev server).
import { test, expect, Page } from '@playwright/test'

const BASE = process.env.MCPPROXY_BASE_URL || 'http://127.0.0.1:18080'
const KEY = process.env.MCPPROXY_API_KEY || ''

function url(route: string): string {
  const sep = route.includes('?') ? '&' : '?'
  return KEY ? `${BASE}/ui${route}${sep}apikey=${encodeURIComponent(KEY)}` : `${BASE}/ui${route}`
}

async function goto(page: Page, route: string, anchor: string) {
  await page.goto(url(route))
  await page.waitForLoadState('domcontentloaded')
  const closeWizard = page.locator('[data-test="close-wizard"]')
  if (await closeWizard.isVisible().catch(() => false)) {
    await closeWizard.click()
  }
  await page.locator(anchor).first().waitFor({ state: 'visible' })
}

// Spec 109 FR-013 / D15: "Every card in a grid has the same height" — a fixed
// grid, not one that jumps as servers move between states (connected,
// quarantined, erroring, disabled, sign-in-required, ...). Measured at
// 1440px, the desktop breakpoint the grid's `lg:grid-cols-3` targets.
test('server cards render at equal heights at 1440px (Spec 109 FR-013)', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })

  await goto(page, '/servers', '[data-test="kpi-card-total"], [data-test="servers-first-run-empty"]')

  const cards = page.locator('[data-test="server-card"]')
  const count = await cards.count()

  // A fleet with fewer than two servers cannot exercise the invariant, but
  // must not silently report a pass either — every environment that runs
  // this sweep with SWEEP_SERVER_NAME set registers at least one server, and
  // CI's fixture set (109-i) adds enough to compare.
  test.skip(count < 2, 'fewer than two server cards rendered; nothing to compare')

  const heights: number[] = []
  for (let i = 0; i < count; i++) {
    const box = await cards.nth(i).boundingBox()
    expect(box, `card ${i} has no layout box`).not.toBeNull()
    heights.push(Math.round(box!.height))
  }

  const distinct = new Set(heights)
  expect(distinct.size, `expected one height across ${count} cards, got ${[...distinct].sort((a, b) => a - b).join(', ')}`).toBe(1)
})

// The primary-action row reserves its height even for a `ready` server with
// no button — the row that would otherwise be the only variable-height part
// of an otherwise fixed grid.
test('the primary-action row keeps its height with no button (Spec 109 FR-013)', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await goto(page, '/servers', '[data-test="kpi-card-total"], [data-test="servers-first-run-empty"]')

  const rows = page.locator('[data-test="server-card-primary-row"]')
  const count = await rows.count()
  test.skip(count === 0, 'no server cards rendered')

  for (let i = 0; i < count; i++) {
    const box = await rows.nth(i).boundingBox()
    expect(box, `primary-action row ${i} has no layout box`).not.toBeNull()
    expect(box!.height).toBeGreaterThan(0)
  }
})
