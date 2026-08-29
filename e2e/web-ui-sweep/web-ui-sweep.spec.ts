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

  await page.locator('[data-test="tools-search"]').fill('echo')
  // Assert on the SURVIVORS, not on the row count. "count <= before" passed
  // vacuously when the search did nothing at all (the unfiltered set is already
  // <= itself, and the first row happened to match), and a strict "<" would race
  // the background indexer — `before` can be sampled mid-render. Requiring every
  // remaining row to match is race-free and catches a dead search box: the
  // fixture's non-matching tool (`ping`) would still be listed.
  await expect
    .poll(
      async () => {
        const texts = await rows.allTextContents()
        return texts.length > 0 && texts.every((t) => /echo/i.test(t))
      },
      { timeout: 10_000 },
    )
    .toBe(true)
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

// ---------------------------------------------------------------------------
// F1 — Activity and Usage must report the same 24h numbers, and both must match
// the API they claim to describe.
//
// The audit found the header reading "70 calls" for a window whose Usage tab
// read 42: the header was printing the EVENT total under the word "calls",
// with security scans and quarantine changes silently inflating it. Two unit
// suites now cover the pieces — the Go handlers agree on fabricated records,
// and the two render helpers read the right fields — but nothing drove the two
// live SCREENS and compared what they actually paint. That seam is what broke.
//
// The invariant is NOT "every number on both screens is equal". Usage folds
// `blocked` into its error tile while Activity keeps them apart, so:
//   Activity total   = events (all rows)      — the paginator's denominator
//   Activity calls   = tool calls only        = Usage "Calls"
//   Usage "Errors"   = Activity errors + blocked
// Asserting naive equality would fail honestly-correct code.
// ---------------------------------------------------------------------------
test('Activity and Usage report the same 24h numbers as the API', async ({ page, request }) => {
  const headers = KEY ? { 'X-API-Key': KEY } : {}

  const read = async () => {
    const s = await (await request.get(`${BASE}/api/v1/activity/summary?period=24h`, { headers })).json()
    const u = await (await request.get(`${BASE}/api/v1/activity/usage?window=24h`, { headers })).json()
    return { summary: s.data ?? {}, usage: u.data ?? {} }
  }

  const { summary, usage } = await read()
  const total = Number(summary.total_count ?? 0)
  const calls = Number(summary.call_count ?? 0)

  if (total === 0) {
    test.skip(true, 'no activity in the last 24h on this instance')
  }

  // The API-level halves of the invariant. `usage` is served from a cached
  // snapshot while `summary` counts live, so let them converge rather than
  // demanding they agree on the first read.
  await expect
    .poll(
      async () => {
        const now = await read()
        return (
          Number(now.usage.total_calls ?? -1) === Number(now.summary.call_count ?? -2) &&
          Number(now.usage.total_errors ?? -1) ===
            Number(now.summary.error_count ?? 0) + Number(now.summary.blocked_count ?? 0)
        )
      },
      {
        timeout: 20_000,
        message:
          'F1: /activity/summary and /activity/usage never converged — Usage calls must equal ' +
          'Activity calls, and Usage errors must equal Activity errors + blocked',
      },
    )
    .toBe(true)

  // Activity paints the summary's own fields, and labels the total for what it
  // is: "events" when some rows are not calls, "calls" when every row is one.
  const errors = watchPageErrors(page)
  await goto(page, '/activity', '[data-test="activity-compact-summary"]')
  await expect(page.locator('[data-test="activity-compact-total"]')).toHaveText(
    total === calls ? new RegExp(`^${total} calls?$`) : new RegExp(`^${total} events?$`),
  )
  if (total !== calls) {
    // The separate "N calls" segment only renders when some rows are not calls.
    await expect(page.locator('[data-test="activity-compact-calls"]')).toHaveText(
      new RegExp(`^${calls} calls?$`),
    )
  }

  // The Usage tile must show the SAME call count the Activity header does.
  await goto(page, '/usage', '[data-test="usage-view"]')
  await expect(
    page.locator('[data-test="usage-calls-tile"] .stat-value'),
    'Usage "Calls" disagrees with the call count Activity prints for the same window',
  ).toHaveText(new RegExp(`^${calls.toLocaleString('en-US')}$`))

  expect(errors, `page exceptions: ${errors.join(', ')}`).toHaveLength(0)
})
