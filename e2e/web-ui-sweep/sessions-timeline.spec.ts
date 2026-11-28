// Work-session list and failure timeline sweep (Spec 124 PR-3, FR-060..FR-065).
//
// Two kinds of test:
//  - Live: a named REST run (X-MCPProxy-Run) is seeded with good calls and two
//    refused calls, then found in Activity -> Sessions with "Failed only" and
//    opened at its first failure. A second run fails earlier and is active
//    later, so the two orders of the list differ. These read the real doors
//    (GET /api/v1/work-sessions and .../timeline). Needs the fixture upstream
//    (SWEEP_SERVER_NAME).
//  - Fixture: one timeline page is fulfilled from data, because an upstream
//    event, a trimmed start and failures removed by retention cannot be
//    produced on a throwaway instance on demand. The shell, router and the
//    embedded bundle are real.
//
// Launcher: scripts/run-web-smoke.sh. SWEEP_SCREENSHOT_DIR keeps PNGs.
import { test, expect, Page } from '@playwright/test'
import * as fs from 'node:fs'
import * as path from 'node:path'

const BASE = process.env.MCPPROXY_BASE_URL || 'http://127.0.0.1:18080'
const KEY = process.env.MCPPROXY_API_KEY || ''
const SERVER = process.env.SWEEP_SERVER_NAME || ''
const RUN = `sweep-124-${Date.now()}`
/** Fails before RUN does and makes a call after it: last in failure order, first in activity order. */
const RUN_B = `${RUN}-b`
const FIXTURE_WS = 'ws-fixture-124'

let seeded = ''
let seededB = ''

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
  const file = test.info().outputPath(`${name}.png`)
  await page.screenshot({ path: file, fullPage: false })
  await test.info().attach(name, { path: file, contentType: 'image/png' })
  const dir = process.env.SWEEP_SCREENSHOT_DIR
  if (!dir) return
  fs.mkdirSync(dir, { recursive: true })
  fs.copyFileSync(file, path.join(dir, `${name}.png`))
}

const headers = (extra: Record<string, string> = {}): Record<string, string> => ({
  'Content-Type': 'application/json',
  ...(KEY ? { 'X-API-Key': KEY } : {}),
  ...extra,
})

/** One tool call over REST under the run name: no MCP connection behind it. */
async function restCall(tool: string, args: unknown, run = RUN) {
  const r = await fetch(`${BASE}/api/v1/tools/call`, {
    method: 'POST',
    headers: headers({ 'X-MCPProxy-Run': run }),
    body: JSON.stringify({ tool_name: 'call_tool_read', arguments: { name: tool, args } }),
  })
  await r.text()
}

const FIRST_FAILURE = 'no_such_tool_spec_124_first'
const SECOND_FAILURE = 'no_such_tool_spec_124_second'
const GOOD_BEFORE = 12
const ATTEMPTS = GOOD_BEFORE + 4

const getJSON = async (route: string): Promise<any> => (await fetch(`${BASE}/api/v1${route}`, { headers: headers() })).json()

/**
 * Twelve good calls (two rows each: more than the 20 rows of context), a call
 * MCPProxy refuses (a tool the server does not have), a good call, a second
 * refused call and a last good one. Two failures tell "first" from "last".
 * Around it, the second run: one refused call before, one good call after.
 * Returns once every attempt is recorded.
 */
async function seedRun(): Promise<string> {
  await restCall(`${SERVER}:${FIRST_FAILURE}`, {}, RUN_B)
  for (let i = 1; i <= GOOD_BEFORE; i++) await restCall(`${SERVER}:echo`, { msg: `sweep 124 step ${i}` })
  await restCall(`${SERVER}:${FIRST_FAILURE}`, {})
  await restCall(`${SERVER}:echo`, { msg: 'sweep 124 between the failures' })
  await restCall(`${SERVER}:${SECOND_FAILURE}`, {})
  await restCall(`${SERVER}:echo`, { msg: 'sweep 124 after the failure' })
  await restCall(`${SERVER}:echo`, { msg: 'sweep 124 second run, after the first one failed' }, RUN_B)
  for (let i = 0; i < 60; i++) {
    const body = await getJSON(`/work-sessions?run=${encodeURIComponent(RUN)}&failed=true`)
    const session = body?.data?.sessions?.[0]
    const other = (await getJSON(`/work-sessions?run=${encodeURIComponent(RUN_B)}&failed=true`))?.data?.sessions?.[0]
    if (session?.id && session.attempts >= ATTEMPTS && session.failures >= 2 && other?.id && other.attempts >= 2) {
      seededB = other.id
      return session.id
    }
    await new Promise((resolve) => setTimeout(resolve, 250))
  }
  throw new Error(`the seeded run ${RUN} never appeared as a failed work session with ${ATTEMPTS} attempts`)
}

// ---- fixture page ------------------------------------------------------------

const at = (ms: number) => new Date(Date.UTC(2026, 10, 26, 22, 40, 0, 0) + ms).toISOString()
const call = (id: string, ms: number, extra: Record<string, unknown> = {}) => ({
  id, type: 'tool_call', status: 'success', timestamp: at(ms), server_name: 'github', tool_name: 'list_issues', duration_ms: 42, request_id: `req-${id}`, ...extra,
})

function fixturePage(failureIndex: number, before = 20) {
  const page = fixtureBase(failureIndex)
  // A wider request reaches one more row; the door then has nothing earlier.
  if (before > 20) {
    page.rows.unshift({ index: 39, kind: 'call', at: at(-9_000), activity: call('f39', -9_000, { tool_name: 'search_issues' }) })
    page.has_before = false
  }
  return page
}

function fixtureBase(failureIndex: number): any {
  return {
    session: {
      id: FIXTURE_WS, token_name: 'nightly-bot', client_name: 'overnight-agent', project_name: 'payments', surface: 'mcp',
      status: 'ended', started_at: at(-2_400_000), last_activity_at: at(65_000), ended_at: at(65_000), duration_ms: 2_465_000,
      attempts: 120, failures: 7, policy_blocks: 1, stored_failures: 5, first_failure_at: at(-1_680_000),
      last_failure: { at: at(1_000), error_class: 'proxy_policy', server: 'github', tool: 'delete_repo', reason: 'MCPProxy refused the call.', policy_block: true },
      connection_count: 3, truncated_start_at: at(-1_800_000),
    },
    rows: [
      { index: 40, kind: 'call', at: at(-4_200), activity: call('f40', -4_200) },
      { index: 41, kind: 'call', at: at(-2_100), activity: call('f41', -2_100, { tool_name: 'get_issue' }) },
      {
        index: 42, kind: 'upstream_event', at: at(-350),
        activity: {
          id: 'f42', type: 'upstream_event', status: 'error', timestamp: at(-350), server_name: 'github',
          error_message: "Refreshing the upstream server's OAuth token failed.",
          metadata: { kind: 'oauth_refresh_failed', code: 'MCPX_OAUTH_REFRESH_FAILED', count: 1, first_at: at(-350), last_at: at(-350) },
        },
      },
      {
        index: 43, kind: 'call', at: at(250), failed: true,
        activity: call('f43', 250, { status: 'error', error_class: 'auth', error_message: 'The upstream server needs a login.' }),
      },
      {
        index: 44, kind: 'policy_decision', at: at(1_000), failed: true, policy_block: true, bodies_removed: true,
        activity: call('f44', 1_000, { type: 'policy_decision', status: 'blocked', tool_name: 'delete_repo' }),
      },
      {
        index: 45, kind: 'upstream_event', at: at(30_000),
        activity: {
          id: 'f45', type: 'upstream_event', status: 'success', timestamp: at(30_000), server_name: 'github',
          metadata: { kind: 'connection_restored', count: 1, message: 'The connection to the upstream server was restored.' },
        },
      },
      { index: 46, kind: 'call', at: at(31_000), activity: call('f46', 31_000) },
      { index: 47, kind: 'session_end', at: at(65_000) },
    ],
    position: failureIndex === 3 ? 44 : 43,
    total_rows: 48,
    failure_index: failureIndex,
    failure_total: 5,
    lifetime_failures: 7,
    earlier_failures_not_stored: true,
    has_before: true,
    has_after: false,
  }
}

/** Fulfils the fixture session's timeline; records the `at` of every request. */
async function mockTimeline(page: Page): Promise<string[]> {
  const asked: string[] = []
  await page.route(new RegExp(`/api/v1/work-sessions/${FIXTURE_WS}/timeline(\\?.*)?$`), async (route) => {
    const position = new URL(route.request().url()).searchParams.get('at') || ''
    asked.push(position)
    const failureIndex = position === 'failure:3' ? 3 : 2
    const before = Number(new URL(route.request().url()).searchParams.get('before') ?? 20)
    await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ success: true, data: fixturePage(failureIndex, before) }) })
  })
  return asked
}

const sidewaysScroll = (page: Page) => page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth)

test.beforeAll(async () => {
  if (SERVER) seeded = await seedRun()
})

test('two actions from the list to the first failure: "Failed only", then the row', async ({ page }) => {
  test.skip(!SERVER, 'needs the fixture upstream (SWEEP_SERVER_NAME)')
  await page.setViewportSize({ width: 1280, height: 900 })
  await open(page, '/activity?view=sessions')
  await expect(page.locator('[data-test="work-sessions-table"]')).toBeVisible()

  // Action 1: Failed only. The filter is the door's, and it lands in the address.
  const filtered = page.waitForResponse((response) => /\/api\/v1\/work-sessions\?/.test(response.url()) && response.url().includes('failed=true'))
  await page.locator('[data-test="work-sessions-failed-toggle"]').check()
  await expect(page).toHaveURL(/[?&]failed=true/)
  // The rows of the unfiltered list stay on screen, inert, until the filtered answer is shown.
  const filteredURL = new URL((await filtered).url())
  await expect(page.locator('[data-test="work-sessions-table-wrap"]')).not.toHaveAttribute('aria-busy', 'true')
  // Failed sessions read newest failure first: the control says so, the door
  // was asked for it, and the rows are in that order.
  await expect(page.locator('[data-test="work-sessions-sort"]')).toHaveValue('last_failure')
  expect(filteredURL.searchParams.get('sort')).toBe('last_failure')
  const lastFailures = (await page.locator('[data-test="work-sessions-row"] [data-test="ws-last-failure"]').allInnerTexts()).map((text) => text.slice(0, 23))
  expect(lastFailures.every((text) => /^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}\.\d{3}$/.test(text)), lastFailures.join(' | ')).toBe(true)
  const row = page.locator(`[data-test="work-sessions-row"][data-session="${seeded}"]`)
  await expect(row).toBeVisible()
  // The rows are the door's, in the door's order. The second run failed first
  // and was active last, so the two orders put the two runs the other way round.
  const shownIds = () => page.locator('[data-test="work-sessions-row"]').evaluateAll((rows) => rows.map((r) => r.getAttribute('data-session')))
  const doorIds = async (sort: string) => ((await getJSON(`/work-sessions?failed=true&sort=${sort}&limit=25`)).data.sessions as any[]).map((s) => s.id)
  const byFailure = await shownIds()
  expect(byFailure).toEqual(await doorIds('last_failure'))
  expect(byFailure.indexOf(seeded), 'newest failure first').toBeLessThan(byFailure.indexOf(seededB))
  await page.locator('[data-test="work-sessions-sort"]').selectOption('last_activity')
  await expect(page).toHaveURL(/[?&]sort=last_activity/)
  await expect.poll(async () => { const ids = await shownIds(); return ids.indexOf(seededB) < ids.indexOf(seeded) }, { message: 'latest activity first' }).toBe(true)
  expect(await shownIds()).toEqual(await doorIds('last_activity'))
  await page.locator('[data-test="work-sessions-sort"]').selectOption('last_failure')
  await expect.poll(async () => (await shownIds())[0]).toBe(byFailure[0])

  // "Include blocked" off: both runs only have calls MCPProxy refused, so both
  // leave the list, and come back with it on. The filter is the door's.
  const withoutBlocked = page.waitForResponse((response) => /\/api\/v1\/work-sessions\?/.test(response.url()) && response.url().includes('include_blocked=false'))
  await page.locator('[data-test="work-sessions-include-blocked"]').uncheck()
  await withoutBlocked
  await expect(row).toHaveCount(0)
  await expect(page.locator(`[data-test="work-sessions-row"][data-session="${seededB}"]`)).toHaveCount(0)
  await page.locator('[data-test="work-sessions-include-blocked"]').check()
  await expect(row).toBeVisible()
  await expect(page.locator(`[data-test="work-sessions-row"][data-session="${seededB}"]`)).toBeVisible()
  await expect(page).not.toHaveURL(/include_blocked/)
  await expect(row.locator('[data-test="ws-client"]')).toContainText(RUN)
  await expect(row.locator('[data-test="ws-failures"] .font-mono')).toHaveText('2')
  await expect(row.locator('[data-test="ws-last-failure"]')).toContainText(/\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}/)
  // Every row left has a failure.
  const failures = await page.locator('[data-test="work-sessions-row"] [data-test="ws-failures"] .font-mono').allInnerTexts()
  expect(failures.every((text) => Number(text) > 0), `failures column: ${failures.join(',')}`).toBe(true)

  // 1280 px: no sideways scroll, on the page or inside the table (Spec 134 SC-006).
  const wrap = await page.evaluate(() => {
    const el = document.querySelector('[data-test="work-sessions-table"]')!.parentElement!
    return el.scrollWidth - el.clientWidth
  })
  expect(wrap, 'table wrapper scrolls sideways').toBeLessThanOrEqual(0)
  expect(await sidewaysScroll(page), 'page scrolls sideways').toBeLessThanOrEqual(0)
  await shot(page, 'sessions-list-1280-light')

  // Action 2: the row's Timeline link, by keyboard. Every timeline request from here on is recorded.
  const timelineRequests: string[] = []
  page.on('request', (request) => {
    if (request.url().includes(`/work-sessions/${seeded}/timeline`)) timelineRequests.push(request.url())
  })
  await row.locator(`[data-test="ws-open-${seeded}"]`).focus()
  await page.keyboard.press('Enter')
  await expect(page).toHaveURL(new RegExp(`[?&]ws=${seeded}`))
  const positioned = page.locator('[data-test="timeline-row"][data-position="true"]')
  await expect(positioned).toHaveAttribute('data-failed', 'true')
  // The operator lands ON the failure: it is on screen without scrolling.
  await expect(positioned).toBeInViewport()
  // The FIRST failure, not the last one.
  await expect(positioned).toContainText(`${SERVER}:${FIRST_FAILURE}`)
  await expect(positioned).toContainText('Blocked')
  await expect(positioned.locator('[data-test="timeline-time"]')).toHaveText(/^\d{2}:\d{2}:\d{2}\.\d{3}$/)
  await expect(page.locator('[data-test="timeline-failure-label"]')).toHaveText('failure 1 of 2')
  // The page is the door's default page: the same rows, the same position, 20 rows before it.
  const door = (await getJSON(`/work-sessions/${seeded}/timeline`)).data
  const shownIndices = await page.locator('[data-test="timeline-row"]').evaluateAll((rows) => rows.map((r) => Number(r.getAttribute('data-index'))))
  expect(shownIndices).toEqual(door.rows.map((r: any) => r.index))
  expect(Number(await positioned.getAttribute('data-index'))).toBe(door.position)
  expect(shownIndices.filter((index) => index < door.position).length, 'rows before the first failure').toBe(20)
  expect(door.rows.find((r: any) => r.index === door.position).activity.tool_name).toBe(FIRST_FAILURE)
  // The calls before the failure are on the page, and the start of the session.
  await expect(page.locator('[data-test="timeline-row"][data-kind="call"]').first()).toContainText(`${SERVER}:echo`)
  await expect(page.locator('[data-test="timeline-cli"]')).toHaveText(`mcpproxy activity session ${seeded}`)
  expect(await sidewaysScroll(page), 'timeline: page scrolls sideways').toBeLessThanOrEqual(0)
  await shot(page, 'timeline-1280-light')

  // Bodies come only on request: no request so far asked for them, and
  // opening one row asks once, for that row.
  const firstCall = page.locator('[data-test="timeline-row"][data-kind="call"]').first()
  const index = await firstCall.getAttribute('data-index')
  await expect(page.locator('main')).not.toContainText('sweep 124 step')
  expect(timelineRequests.length).toBeGreaterThan(0)
  expect(timelineRequests.filter((u) => u.includes('include_bodies')), 'bodies asked for before any row was opened').toEqual([])
  await firstCall.locator(`[data-test="timeline-expand-${index}"]`).click()
  await expect(page.locator(`[data-test="timeline-detail-${index}"]`)).toContainText('sweep 124 step')
  const withBodies = timelineRequests.filter((u) => u.includes('include_bodies=true')).map((u) => new URL(u).searchParams)
  expect(withBodies.length).toBe(1)
  expect(withBodies[0].get('at')).toMatch(/^row:/)
  expect([withBodies[0].get('before'), withBodies[0].get('after')]).toEqual(['0', '0'])
  await expect(page.locator('[data-test="timeline-cli"]')).toHaveText(`mcpproxy activity session ${seeded} --include-bodies`)

  // One step on: the second failure, and the highlight with it.
  await page.locator('[data-test="timeline-next-failure"]').click()
  await expect(page.locator('[data-test="timeline-failure-label"]')).toHaveText('failure 2 of 2')
  await expect(page.locator('[data-test="timeline-row"][data-position="true"]')).toContainText(`${SERVER}:${SECOND_FAILURE}`)
  await expect(page.locator('[data-test="timeline-next-failure"]')).toBeDisabled()
  await expect(page).toHaveURL(/[?&]at=failure(:|%3A)2/)

  // The address is the state: a reload opens the same session at the same row.
  await page.reload()
  await page.waitForLoadState('domcontentloaded')
  await expect(page.locator('[data-test="timeline-row"][data-position="true"]')).toContainText(`${SERVER}:${SECOND_FAILURE}`)
  await expect(page.locator('[data-test="timeline-session-id"]')).toHaveText(seeded)

  // Back: the list, with the filter it was opened from.
  await page.locator('[data-test="timeline-back"]').click()
  await expect(page).not.toHaveURL(/[?&]ws=/)
  await expect(page.locator('[data-test="work-sessions-failed-toggle"]')).toBeChecked()
  await expect(page.locator(`[data-test="work-sessions-row"][data-session="${seeded}"]`)).toBeVisible()
})

test('a session row keeps its link to the Tool calls view filtered by the session (FR-065)', async ({ page }) => {
  test.skip(!SERVER, 'needs the fixture upstream (SWEEP_SERVER_NAME)')
  await page.setViewportSize({ width: 1440, height: 900 })
  await open(page, '/activity?view=sessions&failed=true')
  const seen: string[] = []
  page.on('request', (request) => {
    if (/\/api\/v1\/activity\?/.test(request.url())) seen.push(request.url())
  })
  await page.locator(`[data-test="ws-calls-link-${seeded}"]`).click()
  await expect(page).toHaveURL(new RegExp(`[?&]session=${seeded}`))
  await expect(page).not.toHaveURL(/view=sessions/)
  await expect(page.locator('[data-test="activity-row"]').first()).toBeVisible()
  expect(seen.length, 'no activity request was issued').toBeGreaterThan(0)
  for (const request of seen) expect(new URL(request).searchParams.get('work_session_id'), request).toBe(seeded)

  // And the way back: a call's detail opens its session timeline at that row.
  // The call made after both failures: not where a dropped position would land.
  const calls = (await getJSON(`/activity?work_session_id=${seeded}&type=tool_call&limit=100`)).data.activities as any[]
  const rowId = calls.find((a) => a.arguments?.msg === 'sweep 124 after the failure')?.id
  expect(rowId, 'the call made after the failures is recorded').toBeTruthy()
  const opener = page.locator(`[data-test="activity-open-${rowId}"]`)
  await expect(opener).toBeVisible()
  await opener.click()
  await page.locator('[data-test="activity-open-timeline"]').click()
  await expect(page).toHaveURL(new RegExp(`[?&]ws=${seeded}`))
  expect(new URL(page.url()).searchParams.get('at')).toBe(`row:${rowId}`)
  const landed = page.locator('[data-test="timeline-row"][data-position="true"]')
  await expect(landed).toHaveCount(1)
  await expect(landed).not.toHaveAttribute('data-failed', 'true')
  const landedIndex = await landed.getAttribute('data-index')
  await landed.locator(`[data-test="timeline-expand-${landedIndex}"]`).click()
  await expect(page.locator(`[data-test="timeline-detail-${landedIndex}"]`)).toContainText(`Row id: ${rowId}`)
})

test('timeline rows: an upstream event above the failure it explains, retention notes, failure steps', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  const asked = await mockTimeline(page)
  await open(page, `/activity?view=sessions&ws=${FIXTURE_WS}&at=failure:2`)

  const row = (index: number) => page.locator(`[data-test="timeline-row"][data-index="${index}"]`)
  await expect(row(43)).toHaveAttribute('data-position', 'true')
  // Checked once the page is on screen: the request has been made by then.
  expect(asked[0]).toBe('failure:2')
  await expect(row(43)).toHaveAttribute('aria-current', 'true')
  // The position is shown, not only marked: an outline that no other row has.
  await expect(row(43)).toHaveCSS('outline-style', 'solid')
  await expect(row(44)).toHaveCSS('outline-style', 'none')
  await expect(row(39)).toHaveCount(0)
  await expect(row(43)).toContainText('The upstream server needs a login.')

  // The event sits one row above the failed call, 600 ms earlier, and says what happened.
  await expect(row(42)).toHaveAttribute('data-kind', 'upstream_event')
  await expect(row(42)).toContainText('Token refresh failed')
  await expect(row(42)).toContainText('MCPX_OAUTH_REFRESH_FAILED')
  // In time order, oldest first: the cause is ABOVE the call it explains.
  const order = await page.locator('[data-test="timeline-row"]').evaluateAll((rows) => rows.map((r) => Number(r.getAttribute('data-index'))))
  expect(order).toEqual([...order].sort((a, b) => a - b))
  expect(order.indexOf(43) - order.indexOf(42), 'the event row sits directly above the failed call').toBe(1)
  const [eventTime, failureTime] = [await row(42).locator('[data-test="timeline-time"]').innerText(), await row(43).locator('[data-test="timeline-time"]').innerText()]
  expect(eventTime.slice(-4)).toBe('.650')
  expect(failureTime.slice(-4)).toBe('.250')

  // Told apart by more than the words: a call, an event, a failed call, a
  // block, a recovery and the end of the session each have their own surface.
  const background = (index: number) => row(index).locator('td').first().evaluate((td) => getComputedStyle(td).backgroundColor)
  const surfaces = { call: await background(41), event: await background(42), failed: await background(43), blocked: await background(44), recovery: await background(45), end: await background(47) }
  expect(new Set([surfaces.call, surfaces.event, surfaces.failed, surfaces.recovery, surfaces.end]).size, JSON.stringify(surfaces)).toBe(5)
  expect(surfaces.blocked).not.toBe(surfaces.call)
  await expect(row(47)).toContainText('Session ended')
  await expect(page.locator('[data-test="timeline-expand-47"]')).toHaveCount(0)

  await expect(page.locator('[data-test="timeline-truncated-start"]')).toContainText('removed by retention')
  await expect(page.locator('[data-test="timeline-earlier-failures"]')).toContainText('7 failures')
  await expect(page.locator('[data-test="timeline-earlier-failures"]')).toContainText('5 are still in the log')
  await expect(page.locator('[data-test="timeline-facts"]')).toContainText('nightly-bot')
  await expect(page.locator('[data-test="timeline-facts"]')).toContainText('3 connections')

  // A row whose bodies retention removed says so and asks for nothing.
  const before = asked.length
  await page.locator('[data-test="timeline-expand-44"]').click()
  await expect(page.locator('[data-test="timeline-detail-44"]')).toContainText('removed by retention')
  expect(asked.length).toBe(before)
  await shot(page, 'timeline-fixture-1440-light')
  await page.locator('[data-test="timeline-expand-44"]').click()

  // Next failure: the address moves, the door is asked for that failure.
  await expect(page.locator('[data-test="timeline-failure-label"]')).toHaveText('failure 2 of 5')
  await page.locator('[data-test="timeline-next-failure"]').focus()
  await page.keyboard.press('Enter')
  await expect(page).toHaveURL(/[?&]at=failure(:|%3A)3/)
  await expect(page.locator('[data-test="timeline-failure-label"]')).toHaveText('failure 3 of 5')
  expect(asked.at(-1)).toBe('failure:3')
  // The highlight moved with it.
  await expect(row(44)).toHaveAttribute('data-position', 'true')
  await expect(row(43)).not.toHaveAttribute('data-position', 'true')
  await expect(row(44)).toHaveCSS('outline-style', 'solid')
  await expect(row(43)).toHaveCSS('outline-style', 'none')
  await expect(page.locator('[data-test="timeline-cli"]')).toHaveText(`mcpproxy activity session ${FIXTURE_WS} --at failure:3`)

  // Load earlier rows: the same position, a wider page.
  const wider = page.waitForRequest((request) => request.url().includes(`/work-sessions/${FIXTURE_WS}/timeline`) && request.url().includes('before=70'))
  await page.locator('[data-test="timeline-load-earlier"]').click()
  await wider
  // The wider page holds a row the first one did not, around the same position.
  await expect(row(39)).toContainText('github:search_issues')
  await expect(row(44)).toHaveAttribute('data-position', 'true')
  await expect(page.locator('[data-test="timeline-cli"]')).toHaveText(`mcpproxy activity session ${FIXTURE_WS} --at failure:3 --before 70`)

  // No filter of the page narrows a timeline: the strip is put away here.
  await expect(page.locator('[data-test="activity-filter-strip"]')).toBeHidden()

  // The Sessions tab leaves the timeline for the list, where the Filters control is.
  await page.locator('[data-test="activity-view-tab-sessions"]').click()
  await expect(page).not.toHaveURL(/[?&]ws=/)
  await expect(page.locator('[data-test="work-sessions"]')).toBeVisible()
  await expect(page.locator('[data-test="activity-filters-toggle"]')).toBeVisible()
})

test('an unknown session shows "Session not found" and a way back', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await open(page, '/activity?view=sessions&ws=ws-does-not-exist&failed=true')
  const state = page.locator('[data-test="timeline-not-found"]')
  await expect(state).toContainText('Session not found')
  await expect(page.locator('[data-test="timeline-row"]')).toHaveCount(0)
  await expect(page.locator('[data-test="activity-filter-strip"]')).toBeHidden()
  await state.locator('[data-test="timeline-back-to-list"]').click()
  await expect(page).not.toHaveURL(/[?&]ws=/)
  await expect(page).toHaveURL(/[?&]failed=true/)
  await expect(page.locator('[data-test="work-sessions"]')).toBeVisible()
})

test('the connections table is still there, one level down', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await open(page, '/activity?view=sessions')
  const section = page.locator('[data-test="sessions-connections"]')
  await expect(section.locator('summary')).toContainText('Client connections')
  await expect(section.locator('[data-test="sessions-table"], [data-test="sessions-empty"]')).toBeHidden()
  await section.locator('summary').focus()
  await page.keyboard.press('Enter')
  await expect(section.locator('[data-test="sessions-table"], [data-test="sessions-empty"]')).toBeVisible()
})

test('390 px: a row with an agent token and a project shows both (FR-060)', async ({ page }) => {
  // The live run uses the API key and names no project, so this row is fulfilled.
  await page.setViewportSize({ width: 390, height: 844 })
  const session = fixtureBase(2).session
  await page.route(/\/api\/v1\/work-sessions\?/, async (route) => {
    await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ success: true, data: { sessions: [session], total: 1, limit: 25, offset: 0 } }) })
  })
  await open(page, '/activity?view=sessions')
  const phoneRow = page.locator(`[data-test="work-sessions-row"][data-session="${FIXTURE_WS}"]`)
  await expect(phoneRow).toBeVisible()
  await expect(phoneRow.locator('[data-test="ws-token-narrow"]')).toBeVisible()
  await expect(phoneRow.locator('[data-test="ws-token-narrow"]')).toHaveText('nightly-bot')
  await expect(phoneRow.locator('[data-test="ws-client"]')).toContainText('overnight-agent')
  await expect(phoneRow.locator('[data-test="ws-project"]')).toBeVisible()
  await expect(phoneRow.locator('[data-test="ws-project"]')).toHaveText('payments')
  await expect(phoneRow.locator('[data-test="ws-truncated"]')).toBeVisible()
  // Each of them is inside the screen, not cut off by the table's edge.
  for (const hook of ['ws-token-narrow', 'ws-project']) {
    const box = (await phoneRow.locator(`[data-test="${hook}"]`).boundingBox())!
    expect(box.x, `${hook} starts on screen`).toBeGreaterThanOrEqual(0)
    expect(box.x + Math.min(box.width, 40), `${hook} is on screen`).toBeLessThanOrEqual(390)
  }
  expect(await sidewaysScroll(page), 'page scrolls sideways').toBeLessThanOrEqual(0)
})

test('390 px: list and timeline fit, controls are 44 px tall', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await mockTimeline(page)
  const small = () => page.locator('[data-test="work-sessions"] button, [data-test="work-sessions"] a.btn, [data-test="work-sessions"] a[data-test^="ws-calls-link-"], [data-test="work-sessions"] select, [data-test="work-sessions"] label, [data-test="work-sessions"] summary, [data-test="session-timeline"] button, [data-test="session-timeline"] a.btn').evaluateAll((els) =>
    // What a closed <details> holds is not shown, though Chromium still gives it a box.
    els.filter((el) => { const r = (el as HTMLElement).getBoundingClientRect(); const closed = el.tagName !== 'SUMMARY' && el.closest('details:not([open])'); return !closed && r.width > 0 && r.height > 0 && r.height < 43.5 }).map((el) => `${el.tagName}.${(el as HTMLElement).className}`.slice(0, 80)))

  await open(page, '/activity?view=sessions&failed=true')
  await expect(page.locator('[data-test="work-sessions"]')).toBeVisible()
  expect(await sidewaysScroll(page), 'list: page scrolls sideways').toBeLessThanOrEqual(0)
  expect(await small(), 'list controls under 44 px').toEqual([])
  if (SERVER) {
    // A populated row shows every fact of FR-060 at this width, folded into
    // the cells that are left, and keeps the FR-065 link.
    const phoneRow = page.locator(`[data-test="work-sessions-row"][data-session="${seeded}"]`)
    await expect(phoneRow).toBeVisible()
    await expect(phoneRow.locator('[data-test="ws-client"]')).toBeVisible()
    await expect(phoneRow.locator('[data-test="ws-started-narrow"]')).toBeVisible()
    await expect(phoneRow.locator('[data-test="ws-started-narrow"]')).toContainText(/\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2} · \d+(ms|s)/)
    await expect(phoneRow.locator('[data-test="ws-reason-narrow"]')).toBeVisible()
    await expect(phoneRow.locator('[data-test="ws-reason-narrow"]')).toContainText(/\d{2}:\d{2}:\d{2}\.\d{3}/)
    await expect(phoneRow.locator('[data-test="ws-failures"] .font-mono')).toHaveText('2')
    await expect(phoneRow.locator(`[data-test="ws-calls-link-${seeded}"]`)).toBeVisible()
    await expect(phoneRow.locator(`[data-test="ws-open-${seeded}"]`)).toBeVisible()
  }
  // No cell is cut off: the table fits its wrapper and the Timeline button is whole.
  const fit = await page.evaluate(() => {
    const table = document.querySelector('[data-test="work-sessions-table"]')
    if (!table) return null
    const wrap = table.parentElement!
    const button = table.querySelector('a[data-test^="ws-open-"]')!.getBoundingClientRect()
    const heads = [...table.querySelectorAll('thead th')].filter((th) => (th as HTMLElement).offsetWidth > 0)
    return {
      wrap: wrap.scrollWidth - wrap.clientWidth,
      buttonRight: button.right - wrap.getBoundingClientRect().right,
      clippedHeads: heads.filter((th) => th.scrollWidth > (th as HTMLElement).clientWidth + 1).map((th) => th.textContent?.trim()),
    }
  })
  if (SERVER) expect(fit, 'the seeded run is listed, so there is a table').not.toBeNull()
  if (fit) {
    expect(fit.wrap, 'list table scrolls sideways at 390 px').toBeLessThanOrEqual(0)
    expect(fit.buttonRight, 'Timeline button past the table edge').toBeLessThanOrEqual(0)
    expect(fit.clippedHeads, 'clipped column headers').toEqual([])
  }
  await shot(page, 'sessions-list-390-light')

  // The connections section, opened: its row buttons are 44 px tall too.
  await page.locator('[data-test="sessions-connections"] summary').click()
  expect(await small(), 'connections controls under 44 px').toEqual([])

  await open(page, `/activity?view=sessions&ws=${FIXTURE_WS}&at=failure:2`)
  await expect(page.locator('[data-test="timeline-row"][data-position="true"]')).toBeVisible()
  expect(await sidewaysScroll(page), 'timeline: page scrolls sideways').toBeLessThanOrEqual(0)
  expect(await small(), 'timeline controls under 44 px').toEqual([])
  // The subject of a row keeps a readable width, and its result is printed in the same cell.
  const subject = await page.locator('[data-test="timeline-row"][data-position="true"] td').nth(1).boundingBox()
  expect(subject!.width, 'width of "What happened" at 390 px').toBeGreaterThanOrEqual(150)
  await expect(page.locator('[data-test="timeline-row"][data-position="true"] [data-test="timeline-result-narrow"]')).toHaveText('Error')
  await page.locator('[data-test="timeline-row"][data-position="true"]').scrollIntoViewIfNeeded()
  await shot(page, 'timeline-fixture-390-light')
})

test('dark theme: list and timeline', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await mockTimeline(page)
  await open(page, '/activity?view=sessions', 'dark')
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark')
  await expect(page.locator('[data-test="work-sessions"]')).toBeVisible()
  await shot(page, 'sessions-list-1440-dark')
  await open(page, `/activity?view=sessions&ws=${FIXTURE_WS}&at=failure:2`)
  await expect(page.locator('[data-test="timeline-row"][data-position="true"]')).toBeVisible()
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark')
  // The failed row is tinted with the dark theme's token, not the light one.
  const surfaces = await page.locator('[data-test="timeline-row"][data-index="43"] td').first().evaluate((td) => {
    const probe = document.createElement('div')
    probe.style.background = 'var(--status-error-soft)'
    td.appendChild(probe)
    const token = getComputedStyle(probe).backgroundColor
    probe.remove()
    return { cell: getComputedStyle(td).backgroundColor, token }
  })
  expect(surfaces.cell).toBe(surfaces.token)
  // And that token is the dark theme's: a dark tint, not the light one.
  const lightness = await page.evaluate((color) => {
    const c = document.createElement('canvas').getContext('2d')!
    c.fillStyle = color
    c.fillRect(0, 0, 1, 1)
    const [r, g, b] = c.getImageData(0, 0, 1, 1).data
    return (r + g + b) / 3
  }, surfaces.cell)
  expect(lightness, `failed row background ${surfaces.cell}`).toBeLessThan(128)
  await shot(page, 'timeline-fixture-1440-dark')
  await page.evaluate(() => window.localStorage.removeItem('mcpproxy-theme'))
})
