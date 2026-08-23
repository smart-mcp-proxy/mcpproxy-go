// Web UI sweep — the core-screen pass a maintainer used to run by hand before
// a release (docs/development/web-ui-verification.md).
//
// It drives the Web UI served by a REAL mcpproxy binary with its embedded
// frontend (never a dev server), against a live stdio fixture upstream, and
// covers: servers list, server detail (+ its security tab), tools page and
// its search, activity log, and settings. Uncaught page exceptions fail the
// sweep too — a screen that renders while throwing is not "working".
//
// Launcher: scripts/run-web-smoke.sh (boots the instance, installs Chromium,
// runs this file). The release QA gate calls that same script from its
// advisory `web-ui-sweep` job.
import { test, expect, Page } from '@playwright/test'

const BASE = process.env.MCPPROXY_BASE_URL || 'http://127.0.0.1:18080'
const KEY = process.env.MCPPROXY_API_KEY || ''
// Name of the fixture upstream the launcher registered, when it registered
// one. Unset ⇒ the sweep sticks to instance-independent structural checks.
const SERVER = process.env.SWEEP_SERVER_NAME || ''

function url(route: string): string {
  const sep = route.includes('?') ? '&' : '?'
  return KEY ? `${BASE}/ui${route}${sep}apikey=${encodeURIComponent(KEY)}` : `${BASE}/ui${route}`
}

/** Uncaught exceptions on the page fail the check that triggered them. */
function watchPageErrors(page: Page): string[] {
  const errors: string[] = []
  page.on('pageerror', (err) => errors.push(String(err)))
  return errors
}

/**
 * Navigate to a Web UI route. `networkidle` never settles here — the UI holds
 * an SSE channel open — so wait for DOM content plus the expected anchor.
 */
async function goto(page: Page, route: string, anchor: string) {
  await page.goto(url(route))
  await page.waitForLoadState('domcontentloaded')
  // A fresh instance may open the onboarding wizard over the page.
  const closeWizard = page.locator('[data-test="close-wizard"]')
  if (await closeWizard.isVisible().catch(() => false)) {
    await closeWizard.click()
  }
  await page.locator(anchor).first().waitFor({ state: 'visible' })
}

test('servers list renders the fleet with KPI counters', async ({ page }) => {
  const errors = watchPageErrors(page)
  await goto(page, '/servers', '[data-test="kpi-card-total"]')

  await expect(page.locator('[data-test="kpi-card-total"]')).toContainText(/\d/)
  if (SERVER) {
    await expect(page.locator('[data-test="server-card-title"]', { hasText: SERVER }).first())
      .toBeVisible()
  }
  expect(errors, `uncaught page errors on /servers: ${errors.join(' | ')}`).toHaveLength(0)
})

test('server detail opens and exposes the security tab', async ({ page }) => {
  test.skip(!SERVER, 'no fixture upstream registered for this sweep')
  const errors = watchPageErrors(page)
  await goto(page, `/servers/${encodeURIComponent(SERVER)}`, '[data-test="security-tab"]')

  await page.locator('[data-test="security-tab"]').click()
  await expect(page.locator('[data-test="server-status-badge"]').first()).toBeVisible()
  expect(errors, `uncaught page errors on /servers/${SERVER}: ${errors.join(' | ')}`).toHaveLength(0)
})

test('tools page lists upstream tools and search narrows them', async ({ page }) => {
  test.skip(!SERVER, 'no fixture upstream registered for this sweep')
  const errors = watchPageErrors(page)
  await goto(page, '/tools', '[data-test="tools-page"]')

  const rows = page.locator('[data-test="tool-row"]')
  // Tool indexing runs in the background after the upstream connects.
  await expect.poll(() => rows.count(), { timeout: 30_000 }).toBeGreaterThan(0)
  const before = await rows.count()

  await page.locator('[data-test="tools-search"]').fill('echo')
  await expect.poll(() => rows.count(), { timeout: 10_000 }).toBeLessThanOrEqual(before)
  await expect(rows.first()).toContainText(/echo/i)
  expect(errors, `uncaught page errors on /tools: ${errors.join(' | ')}`).toHaveLength(0)
})

test('activity log renders and its filter panel expands', async ({ page }) => {
  const errors = watchPageErrors(page)
  // The compact header is the always-present anchor; the KPI stat cards only
  // render once the filter panel is expanded AND a summary has loaded.
  await goto(page, '/activity', '[data-test="activity-filters-toggle"]')

  await page.locator('[data-test="activity-filters-toggle"]').click()
  await expect(page.locator('[data-test="activity-filter-panel"]')).toBeVisible()
  expect(errors, `uncaught page errors on /activity: ${errors.join(' | ')}`).toHaveLength(0)
})

test('settings page renders its tabs and security posture', async ({ page }) => {
  const errors = watchPageErrors(page)
  await goto(page, '/settings', '[data-test="settings-tabs"]')

  await expect(page.locator('[data-test="settings-posture"]')).toBeVisible()
  await expect(page.locator('[data-test="setting-secret-api_key"]')).toBeVisible()
  expect(errors, `uncaught page errors on /settings: ${errors.join(' | ')}`).toHaveLength(0)
})
