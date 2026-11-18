// Skills tab of Server Detail (spec 125 PR-4a, specs/125-skills-over-mcp T046).
//
// Drives the Web UI served by a REAL mcpproxy binary (embedded frontend) against
// cmd/skillsfixture registered as a manual-trust upstream with enable_skills on:
//   1. the Skills tab is offered and lists the three fixture skills as
//      "Awaiting review";
//   2. a skill's SKILL.md opens in the viewer, a 150 KiB file pages with Load more,
//      and a binary file is listed but not offered for viewing;
//   3. approving a skill turns its chip to Approved;
//   4. changing an APPROVED skill's upstream bytes and pressing Refresh while it
//      is open marks the open review stale (it shows the approved version, so it
//      offers no Approve) until it is re-opened; then it is Changed, with the
//      file-set diff and a line diff against the approved copy, and approving
//      binds the new hash;
//   5. changing a PENDING skill's bytes while it is open marks the review stale
//      and keeps its Approve button on screen but disabled;
//   6. block and unblock;
//   7. no uncaught page error anywhere.
//
// Normally run by scripts/run-skills-tab-sweep.sh, which starts everything and
// sets the environment (docs/development/web-ui-verification.md):
//   MCPPROXY_BASE_URL, MCPPROXY_API_KEY  the throwaway core
//   SWEEP_SERVER_NAME                    the registered fixture upstream (default skillsfx)
//   SKILLS_FIXTURE_URL                   http://127.0.0.1:<port> of cmd/skillsfixture
//   SKILLS_SWEEP_STATE_DIR               optional: where GET /skills is saved after
//                                        the approve, block and unblock steps
//   SWEEP_SCREENSHOT_DIR                 optional: where the screenshots are saved
import { test, expect, Page } from '@playwright/test'
import fs from 'node:fs'
import path from 'node:path'

const BASE = process.env.MCPPROXY_BASE_URL || 'http://127.0.0.1:18080'
const KEY = process.env.MCPPROXY_API_KEY || ''
const SERVER = process.env.SWEEP_SERVER_NAME || 'skillsfx'
const FIXTURE = process.env.SKILLS_FIXTURE_URL || ''

test.skip(!FIXTURE, 'SKILLS_FIXTURE_URL is not set: start cmd/skillsfixture and register it as an upstream')
// One stateful serial group: each scenario builds on the records the previous
// one wrote (approved, changed, blocked). A retry would replay a scenario
// against state its first attempt already changed and always fail, hiding the
// original error, so this group never retries (the config retries once in CI).
test.describe.configure({ mode: 'serial', retries: 0 })

const pageErrors: string[] = []

// The key never goes into a page URL: a goto failure message (and so the
// results file and the HTML report) would carry it. The Web UI reads it from
// localStorage instead (frontend/src/services/api.ts initializeAPIKey), which an
// init script sets on the core's origin before any page script runs.
function url(route: string): string {
  return `${BASE}/ui${route}`
}

// The Web UI's own SSE request carries the key as ?apikey=, so a trace's
// network log would hold it: this spec records no traces (failure screenshots
// show the page only, never the address bar).
test.use({ trace: 'off' })

async function shot(page: Page, name: string, focus?: string) {
  // The detail pane sits below the list; scroll the part worth seeing into view.
  if (focus) await page.locator(focus).first().scrollIntoViewIfNeeded()
  const body = await page.screenshot({ fullPage: false })
  await test.info().attach(`skills-tab-${name}`, { body, contentType: 'image/png' })
  const dir = process.env.SWEEP_SCREENSHOT_DIR
  if (dir) {
    fs.mkdirSync(dir, { recursive: true })
    fs.writeFileSync(path.join(dir, `skills-tab-${name}.png`), body)
  }
}

// Read back the core's own record of every skill after a step, for the
// launcher's results file.
async function saveState(step: string) {
  const dir = process.env.SKILLS_SWEEP_STATE_DIR
  if (!dir) return
  const res = await fetch(`${BASE}/api/v1/servers/${SERVER}/skills`, { headers: { 'X-API-Key': KEY } })
  expect(res.status, `GET /skills after ${step}`).toBe(200)
  const body = await res.json()
  fs.mkdirSync(dir, { recursive: true })
  fs.writeFileSync(path.join(dir, `state-after-${step}.json`), JSON.stringify(body.data ?? body, null, 2))
}

/** The fixture's mutate endpoint wants this header (and refuses an Origin). */
const MUTATE_HEADERS = { 'X-Skills-Fixture': 'mutate', 'Content-Type': 'text/plain' }

const row = (page: Page, skill: string) => page.locator(`[data-test="skill-row-${skill}"]`)
const chip = (page: Page, skill: string) => page.locator(`[data-test="skill-chip-${skill}"]`)

test.beforeEach(async ({ page }) => {
  page.on('pageerror', err => pageErrors.push(err.message))
  if (KEY) {
    await page.addInitScript(({ key, origin }) => {
      if (window.location.origin === origin) window.localStorage.setItem('mcpproxy-api-key', key)
    }, { key: KEY, origin: new URL(BASE).origin })
  }
})

test('the Skills tab lists the fixture skills as awaiting review', async ({ page }) => {
  // The core reads skills on refresh; make sure the three records exist.
  const refreshed = await fetch(`${BASE}/api/v1/servers/${SERVER}/skills/refresh`, { method: 'POST', headers: { 'X-API-Key': KEY } })
  expect(refreshed.status, 'POST /skills/refresh').toBe(200)
  await page.goto(url(`/servers/${SERVER}?tab=skills`))
  await page.waitForLoadState('domcontentloaded')
  const closeWizard = page.locator('[data-test="close-wizard"]')
  if (await closeWizard.isVisible().catch(() => false)) await closeWizard.click()
  await expect(page.locator('[data-test="skills-tab"]')).toBeVisible()
  await expect(page.locator('[data-test="skills-tab"]')).toHaveClass(/tab-active/)
  for (const s of ['plain', 'with-files', 'big']) {
    await expect(chip(page, s)).toHaveText('Awaiting review')
  }
  await shot(page, '01-list')
})

test('views SKILL.md, pages a large file, and does not offer a binary file', async ({ page }) => {
  await page.goto(url(`/servers/${SERVER}?tab=skills`))
  await row(page, 'plain').click()
  await page.locator('[data-test="skill-view-SKILL.md"]').click()
  await expect(page.locator('[data-test="skill-viewer-text"]')).toContainText('name: plain')
  await shot(page, '02-viewer', '[data-test="skill-viewer"]')

  await row(page, 'big').click()
  // Every part is awaited as a response, and its text as a rendered update: the
  // Load more button is hidden while a part loads, so its absence alone is not
  // the end of the file. The end is a part with no next_offset.
  const isPart = (offset: number) => (r: { url(): string }) => {
    const u = new URL(r.url())
    return u.pathname.endsWith('/skills/content') && u.searchParams.get('file') === 'data/large.txt' &&
      Number(u.searchParams.get('offset') ?? 0) === offset
  }
  const text = page.locator('[data-test="skill-viewer-text"]')
  const loadMore = page.locator('[data-test="skill-load-more"]')
  let [res] = await Promise.all([page.waitForResponse(isPart(0)), page.locator('[data-test="skill-view-data/large.txt"]').click()])
  let shown = 0
  let loads = 0
  for (;;) {
    expect(res.status()).toBe(200)
    const body = await res.json()
    const part = body.data ?? body
    shown += part.text.length
    await expect.poll(() => text.evaluate(e => (e.textContent ?? '').length)).toBe(shown)
    if (part.next_offset === undefined || part.next_offset === null) break
    expect(loads, 'the file ends within ten parts').toBeLessThan(10)
    if (loads === 0) {
      await expect(text).toContainText('line 000001')
      await expect(text).not.toContainText('line 003000')
    }
    await expect(loadMore).toBeVisible()
    ;[res] = await Promise.all([page.waitForResponse(isPart(part.next_offset)), loadMore.click()])
    loads++
  }
  await expect(loadMore).toHaveCount(0)
  expect(loads, '150 KiB needs more than one 64 KiB part').toBeGreaterThanOrEqual(2)
  await expect(text).toContainText('line 003840')
  await shot(page, '03-paged', '[data-test="skill-viewer"]')

  await row(page, 'with-files').click()
  await expect(page.locator('[data-test="skill-file-logo.bin"]')).toContainText(/binary/i)
  await expect(page.locator('[data-test="skill-view-logo.bin"]')).toHaveCount(0)
  await expect(page.locator('[data-test="skill-view-notes.txt"]')).toBeVisible()
})

test('approve turns the chip to Approved', async ({ page }) => {
  await page.goto(url(`/servers/${SERVER}?tab=skills`))
  await row(page, 'plain').click()
  await page.locator('[data-test="skill-approve"]').click()
  await expect(chip(page, 'plain')).toHaveText('Approved')
  await expect(page.locator('[data-test="skills-notice"]')).toContainText(/approved/i)
  await expect(page.locator('[data-test="skill-approve"]')).toHaveCount(0)
  await saveState('approve')
  await shot(page, '04-approved')
})

test('an upstream change plus Refresh shows Changed with a file diff and a line diff', async ({ page }) => {
  const changed = '---\nname: plain\ndescription: test skill plain\n---\n# plain\nA new instruction line.\n'
  const refused = await fetch(`${FIXTURE}/_fixture/mutate?skill=plain&file=SKILL.md`, { method: 'POST', body: changed })
  expect(refused.status, 'mutate without the fixture header is refused').toBe(403)
  await page.goto(url(`/servers/${SERVER}?tab=skills`))
  await row(page, 'plain').click()
  await expect(page.locator('[data-test="skill-stale"]')).toHaveCount(0)
  const res = await fetch(`${FIXTURE}/_fixture/mutate?skill=plain&file=SKILL.md`, { method: 'POST', body: changed, headers: MUTATE_HEADERS })
  expect(res.status).toBe(204)
  await page.locator('[data-test="skills-refresh"]').click()
  await expect(chip(page, 'plain')).toHaveText('Changed')
  // The open review was of the approved version: it is stale until re-opened.
  await expect(page.locator('[data-test="skill-stale"]')).toContainText('This skill changed while you were reviewing it')
  // The review on screen is still the approved version, so it offers no Approve;
  // the new version's Approve appears only after the re-open below.
  await expect(page.locator('[data-test="skill-approve"]')).toHaveCount(0)
  await shot(page, '05a-stale', '[data-test="skill-stale"]')
  await page.locator('[data-test="skill-reopen"]').click()
  await expect(page.locator('[data-test="skill-stale"]')).toHaveCount(0)
  await expect(page.locator('[data-test="skill-diff-SKILL.md"]')).toHaveAttribute('data-state', 'changed')
  await page.locator('[data-test="skill-line-diff-SKILL.md"]').click()
  const added = page.locator('[data-test="skill-diff-line"][data-op="add"]')
  await expect(added).toHaveCount(1)
  await expect(added).toContainText('A new instruction line.')
  await shot(page, '05-changed-diff', '[data-test="skill-line-diff"]')
  await page.locator('[data-test="skill-approve"]').click()
  await expect(chip(page, 'plain')).toHaveText('Approved')
  await saveState('approve-changed')
})

test('a pending skill changed while open is stale, with Approve disabled until re-opened', async ({ page }) => {
  await page.goto(url(`/servers/${SERVER}?tab=skills`))
  await row(page, 'with-files').click()
  const approve = page.locator('[data-test="skill-approve"]')
  // Pending and not stale: Approve is offered and enabled.
  await expect(approve).toBeEnabled()
  await expect(page.locator('[data-test="skill-stale"]')).toHaveCount(0)
  const res = await fetch(`${FIXTURE}/_fixture/mutate?skill=with-files&file=notes.txt`, {
    method: 'POST', body: 'Supporting notes\nsecond line\na third line added upstream\n', headers: MUTATE_HEADERS,
  })
  expect(res.status).toBe(204)
  await page.locator('[data-test="skills-refresh"]').click()
  await expect(page.locator('[data-test="skill-stale"]')).toContainText('This skill changed while you were reviewing it')
  // Still pending (never approved), so the chip does not move...
  await expect(chip(page, 'with-files')).toHaveText('Awaiting review')
  // ...and SkillsPanel keeps the reviewed version on screen: its Approve stays,
  // disabled, so the changed bytes cannot be approved unseen.
  await expect(approve).toHaveCount(1)
  await expect(approve).toBeDisabled()
  await shot(page, '05b-pending-stale', '[data-test="skill-stale"]')
  await page.locator('[data-test="skill-reopen"]').click()
  await expect(page.locator('[data-test="skill-stale"]')).toHaveCount(0)
  await expect(approve).toBeEnabled()
})

test('block and unblock', async ({ page }) => {
  await page.goto(url(`/servers/${SERVER}?tab=skills`))
  await row(page, 'with-files').click()
  await page.locator('[data-test="skill-block"]').click()
  await expect(chip(page, 'with-files')).toHaveText('Blocked')
  await expect(page.locator('[data-test="skill-approve"]')).toHaveCount(0)
  await saveState('block')
  await shot(page, '06-blocked')
  await page.locator('[data-test="skill-unblock"]').click()
  await expect(chip(page, 'with-files')).toHaveText('Awaiting review')
  await saveState('unblock')
})

test('the tab survives a reload through ?tab=skills and no page error was thrown', async ({ page }) => {
  await page.goto(url(`/servers/${SERVER}?tab=skills`))
  await expect(page.locator('[data-test="skills-panel"]')).toBeVisible()
  await page.reload()
  await expect(page.locator('[data-test="skills-tab"]')).toHaveClass(/tab-active/)
  expect(pageErrors, 'uncaught page errors').toEqual([])
})
