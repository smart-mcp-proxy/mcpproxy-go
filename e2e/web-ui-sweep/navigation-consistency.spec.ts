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
//
// Review round 2 (109-e medium finding): two config-identical, healthy
// fixture servers both land in the SAME grid row at this breakpoint, where
// CSS grid's `align-items:stretch` equalizes every card in a row regardless
// of content — `distinct.size === 1` passed unconditionally whether or not
// the `.server-card { min-height }` rule this invariant depends on even
// existed. scripts/run-web-smoke.sh now seeds 4 fixture servers, one
// quarantined, so the fleet both spans more than one grid row (stretch can
// no longer paper over a row-to-row difference) and includes a card whose
// content genuinely differs from the rest. The row check below turns the
// previously-silent false pass into an explicit, loud skip whenever the
// fixture set is too small to actually exercise the invariant.
test('server cards render at equal heights at 1440px (Spec 109 FR-013)', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })

  await goto(page, '/servers', '[data-test="kpi-card-total"], [data-test="servers-first-run-empty"]')

  const cards = page.locator('[data-test="server-card"]')
  const count = await cards.count()

  // A fleet with fewer than two servers cannot exercise the invariant, but
  // must not silently report a pass either. scripts/run-web-smoke.sh
  // registers fixture servers (review rounds 1 and 2, 109-e) specifically so
  // this test runs under the release-qa-gate `web-ui-sweep` job from this PR
  // onward; a hand run with no MCPPROXY_FIXTURE_PATH, or a leaner fixture
  // set, still falls back to skipping rather than reporting a false pass.
  test.skip(count < 2, 'fewer than two server cards rendered; nothing to compare')

  const heights: number[] = []
  const tops: number[] = []
  for (let i = 0; i < count; i++) {
    const box = await cards.nth(i).boundingBox()
    expect(box, `card ${i} has no layout box`).not.toBeNull()
    heights.push(Math.round(box!.height))
    tops.push(Math.round(box!.y))
  }

  // Cluster the cards' top offsets into grid rows (a few px of layout jitter
  // within one row is expected; a real row boundary is a much bigger jump).
  const sortedTops = [...tops].sort((a, b) => a - b)
  let rowCount = 1
  for (let i = 1; i < sortedTops.length; i++) {
    if (sortedTops[i] - sortedTops[i - 1] > 8) rowCount++
  }
  // If every card sits in the same grid row, CSS grid's `align-items:stretch`
  // equalizes their heights regardless of content or CSS — the assertion
  // below would pass whether or not the invariant it names actually holds.
  // Skip loudly instead of reporting a pass that tested nothing.
  test.skip(rowCount < 2,
    `all ${count} cards share one grid row at 1440px; CSS grid stretch makes height equality trivially true here — a larger MCPPROXY_FIXTURE_PATH fleet (scripts/run-web-smoke.sh) is needed to span a second row`)

  const distinct = new Set(heights)
  expect(distinct.size, `expected one height across ${count} cards spanning ${rowCount} grid rows, got ${[...distinct].sort((a, b) => a - b).join(', ')}`).toBe(1)
})

// The primary-action row reserves its height even for a `ready` server with
// no button — the row that would otherwise be the only variable-height part
// of an otherwise fixed grid.
//
// Review round 1 (109-e medium finding): the row's "Details" link is
// unconditional — every card renders it whether or not the primary button
// does — so a bounding-box `height > 0` check passes on the Details link
// alone and would still pass with the `min-h-[2.25rem]` reservation this
// test is named after deleted entirely. Reading the CSS `min-height` the
// row's own stylesheet applies, instead of the box Playwright measured
// after layout, tests the reservation itself rather than something else
// that happens to fill the same space.
test('the primary-action row keeps its height with no button (Spec 109 FR-013)', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await goto(page, '/servers', '[data-test="kpi-card-total"], [data-test="servers-first-run-empty"]')

  const rows = page.locator('[data-test="server-card-primary-row"]')
  const count = await rows.count()
  test.skip(count === 0, 'no server cards rendered')

  for (let i = 0; i < count; i++) {
    const row = rows.nth(i)
    const box = await row.boundingBox()
    expect(box, `primary-action row ${i} has no layout box`).not.toBeNull()
    expect(box!.height).toBeGreaterThan(0)

    const minHeightPx = await row.evaluate((el) => parseFloat(getComputedStyle(el).minHeight) || 0)
    expect(minHeightPx, `primary-action row ${i} has no min-height CSS reservation — a "ready" card's Details link would still render without it`).toBeGreaterThan(0)
  }
})
