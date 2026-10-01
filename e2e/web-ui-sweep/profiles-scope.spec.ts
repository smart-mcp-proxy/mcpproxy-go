// Profile scope on Tools, Activity, Sessions and Usage (Spec 108-j, specs/108-profiles-v3 T109).
//
// Drives the Web UI served by a REAL mcpproxy binary (embedded frontend), seeded
// through REST and one MCP session only (profiles-seed.ts): a locked client on a
// profile that denies `<SERVER>:echo`, one allowed call and one refused call.
//
//   1. /tools?client=  loads filtered with no unfiltered rows first, greyed rows
//      with reasons, "Why?" -> the explainer, closed by a REAL Escape.
//   2. /usage?profile= carries the param and replaces tokens saved.
//   3. /sessions?client= -> Spec 109's redirect, client and profile columns,
//      "Tools it sees".
//   4. The chip survives a link and Back; removing it refetches unscoped.
//   5. /activity?client=&status=blocked -> exactly the refused call, attribution
//      chips, "Allow in profile…" and "Why?".
//   6. Layout at 1440 / 1100 / 900 / 390.
//   7. Keyboard.
//   0. The seed closes its MCP session.
//
// Launcher: scripts/run-web-smoke.sh (docs/development/web-ui-verification.md).
import { test, expect, Page } from '@playwright/test'
import fs from 'node:fs'
import path from 'node:path'
import { SCOPE_CLIENT, SCOPE_PROFILE, SERVER, api, cleanupScopeActivity, seedScopeActivity } from './profiles-seed'

const BASE = process.env.MCPPROXY_BASE_URL || 'http://127.0.0.1:18080'
const KEY = process.env.MCPPROXY_API_KEY || ''

function url(route: string): string {
  const sep = route.includes('?') ? '&' : '?'
  return KEY ? `${BASE}/ui${route}${sep}apikey=${encodeURIComponent(KEY)}` : `${BASE}/ui${route}`
}

async function open(page: Page, route: string) {
  await page.goto(url(route))
  await page.waitForLoadState('domcontentloaded')
  const closeWizard = page.locator('[data-test="close-wizard"]')
  if (await closeWizard.isVisible().catch(() => false)) await closeWizard.click()
  await page.locator('main').first().waitFor({ state: 'visible' })
}

async function noHorizontalScroll(page: Page, label: string) {
  const overflow = await page.evaluate(() => {
    const el = document.scrollingElement || document.documentElement
    return el.scrollWidth - window.innerWidth
  })
  expect(overflow, `${label}: the page must not scroll horizontally`).toBeLessThanOrEqual(0)
}

// The HTML reporter owns REPORT_DIR and empties it, so the screenshots travel as
// report attachments; SWEEP_SCREENSHOT_DIR, when set, also keeps them as files.
async function shot(page: Page, name: string) {
  const body = await page.screenshot({ fullPage: false })
  await test.info().attach(`profiles-scope-${name}`, { body, contentType: 'image/png' })
  const dir = process.env.SWEEP_SCREENSHOT_DIR
  if (dir) {
    fs.mkdirSync(dir, { recursive: true })
    fs.writeFileSync(path.join(dir, `profiles-scope-${name}.png`), body)
  }
}

/** Presses Tab until the focused element matches the selector. */
async function tabTo(page: Page, selector: string, max = 120): Promise<boolean> {
  for (let i = 0; i < max; i++) {
    await page.keyboard.press('Tab')
    if (await page.evaluate(sel => document.activeElement?.matches(sel) ?? false, selector)) return true
  }
  return false
}

test.describe.configure({ mode: 'serial' })
test.skip(!SERVER, 'needs a fixture upstream (SWEEP_SERVER_NAME)')

test.beforeAll(async () => {
  await cleanupScopeActivity()
  await seedScopeActivity()
})
test.afterAll(async () => {
  await cleanupScopeActivity()
})

const ECHO = `${SERVER}__echo`

test('0. the seed leaves no live MCP session behind (order-independent specs, QA.1)', async () => {
  // An open seed session stays a "connected" client on the Home dashboard for 30
  // minutes of idle, which is what made the contrast sweep depend on spec order.
  const { status, data } = await api('GET', '/sessions?limit=100')
  expect(status).toBe(200)
  const sessions: Array<{ client_name?: string; status?: string }> = data?.sessions ?? []
  const seeded = sessions.filter(session => session.client_name === 'e2e-scope-client')
  expect(seeded.length, 'the seed made its session').toBeGreaterThan(0)
  expect(seeded.filter(session => session.status === 'active')).toEqual([])
})

test('1. Tools view-as: filtered from the first render, greyed rows with reasons, Why? and a real Escape', async ({ page }) => {
  // Hold the scoped answer back: if the page asked for the unfiltered list first,
  // its rows would be on screen before this response lands (the flash rule 1
  // forbids). The sidebar's own tool-count request is a different caller.
  let scopedSeen = false
  await page.route('**/api/v1/tools?client=*', async route => {
    scopedSeen = true
    await new Promise(resolve => setTimeout(resolve, 1500))
    await route.continue().catch(() => {})
  })
  const firstScoped = page.waitForRequest(request => request.url().includes('/api/v1/tools?client=' + SCOPE_CLIENT))
  await open(page, `/tools?client=${SCOPE_CLIENT}`)
  await firstScoped
  expect(scopedSeen).toBe(true)
  await expect(page.locator('[data-test="scope-chip-client"]')).toContainText('Client: E2E Scope')
  await page.waitForTimeout(500)
  await expect(page.locator('[data-test="tool-row"]'), 'no rows before the scoped answer').toHaveCount(0)
  await page.unroute('**/api/v1/tools?client=*')

  await expect(page.locator('[data-test="tools-view-as-banner"]')).toContainText('Viewing as E2E Scope')
  await expect(page.locator('[data-test="tools-view-as-banner"]')).toContainText('callable')
  await expect(page.locator(`[data-test="tools-row-access-${ECHO}"]`)).toContainText('Denied by rule')
  await expect(page.locator(`[data-test="tools-row-access-${SERVER}-2__echo"]`)).toContainText('Server not in profile')
  await expect(page.locator(`[data-test="tools-row-access-${SERVER}__ping"]`)).toContainText('Callable')
  // Batch actions never edit the viewed subject's access.
  await expect(page.locator('[data-test="tools-select-all"]')).toBeDisabled()

  await shot(page, 'tools-1440')

  const why = page.locator(`[data-test="tools-why-${ECHO}"]`)
  await why.click()
  const dialog = page.locator('[data-test="access-explainer"]')
  await expect(dialog.locator('[data-test="explain-verdict"]')).toBeVisible()
  await expect(dialog.locator('[data-test="explain-step-tool_rule"]')).toContainText('Fail')
  await page.keyboard.press('Escape')
  await expect(dialog.locator('[data-test="explain-verdict"]')).toHaveCount(0)
  // The browser hands focus back to the button that opened the dialog.
  await expect(why).toBeFocused()
})

test('1b. Both client and profile: the conflict state and no scoped /tools request (rule 8)', async ({ page }) => {
  const scoped: string[] = []
  page.on('request', request => {
    if (/\/api\/v1\/tools\?/.test(request.url())) scoped.push(request.url())
  })
  await open(page, `/tools?client=${SCOPE_CLIENT}&profile=${SCOPE_PROFILE}`)
  await expect(page.locator('[data-test="tools-view-as-conflict"]')).toBeVisible()
  await expect(page.locator('[data-test="scope-chip-client"]')).toHaveAttribute('data-conflicting', 'true')
  await page.waitForTimeout(500)
  expect(scoped, 'GET /tools takes one subject, so no request carries either').toEqual([])
  await page.locator('[data-test="tools-view-as-keep-client"]').click()
  await expect(page).not.toHaveURL(/profile=/)
  await expect(page.locator('[data-test="tools-view-as-banner"]')).toBeVisible()
})

test('2. Usage carries the profile and does not print a false "0 tokens saved"', async ({ page }) => {
  const scopedUsage = page.waitForRequest(request => request.url().includes('/api/v1/activity/usage') && request.url().includes(`profile=${SCOPE_PROFILE}`))
  await open(page, `/usage?profile=${SCOPE_PROFILE}`)
  await scopedUsage
  await expect(page.locator('[data-test="usage-scope-chips"] [data-test="scope-chip-profile"]')).toContainText('Profile: E2E Scope RO')
  await expect(page.locator('[data-test="usage-tokens-saved-scoped"]')).toContainText('not computed for a filtered view')
  await expect(page.locator('[data-test="usage-tokens-saved-tile"]')).toHaveCount(0)
  await shot(page, 'usage-1440')
})

test('3. /sessions?client= redirects to the Activity Sessions view with client and profile columns', async ({ page }) => {
  await open(page, `/sessions?client=${SCOPE_CLIENT}`)
  await expect(page).toHaveURL(new RegExp(`/ui/activity\\?.*view=sessions`))
  await expect(page).toHaveURL(new RegExp(`client=${SCOPE_CLIENT}`))
  const row = page.locator('[data-test="sessions-row"]').first()
  await expect(row.locator('[data-test="sessions-client"]')).toContainText('E2E Scope')
  await expect(row.locator('[data-test="sessions-profile"]')).toContainText('E2E Scope RO · locked by credential')
  await row.locator('[data-test^="sessions-tools-it-sees-"]').click()
  await expect(page).toHaveURL(new RegExp(`/ui/tools\\?.*client=${SCOPE_CLIENT}`))
  await expect(page.locator('[data-test="scope-chip-client"]')).toBeVisible()
})

test('4. The chip survives a link and Back; removing it refetches unscoped', async ({ page }) => {
  await open(page, `/tools?client=${SCOPE_CLIENT}`)
  const callsLink = page.locator(`[data-test="tool-row"]:has-text("ping") [data-test="tool-calls-link"]`).first()
  await expect(callsLink).toBeVisible()
  await callsLink.click()
  await expect(page).toHaveURL(new RegExp(`/ui/activity\\?.*client=${SCOPE_CLIENT}`))
  await expect(page.locator('[data-test="scope-chip-client"]')).toBeVisible()
  await page.goBack()
  await expect(page).toHaveURL(new RegExp(`/ui/tools\\?.*client=${SCOPE_CLIENT}`))
  await expect(page.locator('[data-test="scope-chip-client"]')).toBeVisible()
  await expect(page.locator('[data-test="tools-view-as-banner"]')).toBeVisible()

  const unscoped = page.waitForRequest(request => /\/api\/v1\/tools(\?|$)/.test(request.url()) && !request.url().includes('client='))
  await page.locator('[data-test="scope-chip-remove-client"]').click()
  await unscoped
  await expect(page).not.toHaveURL(/client=/)
  await expect(page.locator('[data-test="tools-view-as-banner"]')).toHaveCount(0)
  await expect(page.locator('[data-test^="tools-row-access-"]')).toHaveCount(0)
})

test('5. A blocked call: attribution chips, Allow in profile… and Why?', async ({ page }) => {
  const firstActivity = page.waitForRequest(request => /\/api\/v1\/activity\?/.test(request.url()))
  await open(page, `/activity?client=${SCOPE_CLIENT}&status=blocked`)
  expect((await firstActivity).url()).toContain(`client=${SCOPE_CLIENT}`)
  await expect(page.locator('[data-test="scope-chip-client"]')).toContainText('Client: E2E Scope')
  const rows = page.locator('[data-test="activity-row"]')
  await expect(rows).toHaveCount(1)
  await expect(rows.first()).toContainText('echo')
  await expect(page.locator('[data-test="activity-scope-col"]')).toBeVisible()
  const chips = rows.first().locator('[data-test^="activity-attribution-"]')
  await expect(chips.locator('[data-test="attribution-client"]')).toContainText('E2E Scope')
  await expect(chips.locator('[data-test="attribution-profile"]')).toContainText('E2E Scope RO · locked by credential')
  // The client's own credential (client-<id>) is the client chip already.
  await expect(chips.locator('[data-test="attribution-token"]')).toHaveCount(0)
  await shot(page, 'activity-1440')

  await rows.first().locator('[data-test^="activity-open-"]').click()
  const allow = page.locator('[data-test^="activity-allow-in-profile-"]')
  await expect(allow).toContainText('Allow in profile…')
  await allow.click()
  await expect(page).toHaveURL(new RegExp(`/ui/profiles/${SCOPE_PROFILE}\\?focus=${SERVER}(:|%3A)echo`))
  await expect(page.locator(`[data-test="profile-tool-row-${ECHO}"]`)).toHaveAttribute('aria-current', 'true')

  await page.goBack()
  await expect(page.locator('[data-test="activity-row"]')).toHaveCount(1)
  await page.locator('[data-test="activity-row"]').first().locator('[data-test^="activity-open-"]').click()
  await page.locator('[data-test^="activity-why-"]').click()
  const dialog = page.locator('[data-test="access-explainer"]')
  await expect(dialog).toContainText('Why is this blocked?')
  await expect(dialog.locator('[data-test="explain-note"]')).toContainText('Evaluated against the current configuration')
  await expect(dialog.locator('[data-test="explain-step-tool_rule"]')).toContainText('Fail')
  await expect(dialog.locator('[data-test="explain-fixes"]')).toBeVisible()
  // Escape closes only the explainer: the drawer stays and focus returns to Why?.
  await page.keyboard.press('Escape')
  await expect(dialog.locator('[data-test="explain-verdict"]')).toHaveCount(0)
  await expect(page.locator('[data-test^="activity-why-"]')).toBeFocused()
})

// 1280 is the first width that shows the Scope column; 768-1100 is where it used to clip Status and Duration.
for (const width of [1440, 1280, 1100, 1024, 900, 768, 390]) {
  test(`6. layout at ${width}px: no horizontal scroll, chips not clipped`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 })
    for (const [name, route, chip] of [
      ['tools', `/tools?client=${SCOPE_CLIENT}`, '[data-test="scope-chip-client"]'],
      ['usage', `/usage?profile=${SCOPE_PROFILE}`, '[data-test="scope-chip-profile"]'],
      ['activity', `/activity?client=${SCOPE_CLIENT}&status=blocked`, '[data-test="scope-chip-client"]'],
    ] as const) {
      await open(page, route)
      await expect(page.locator(chip).first()).toBeVisible()
      if (name === 'tools') await expect(page.locator('[data-test="tools-view-as-banner"]')).toBeVisible()
      if (name === 'activity') {
        await expect(page.locator('[data-test="activity-row"]')).toHaveCount(1)
        // The table fits its card: nothing (Status, Duration) is cut off or hidden behind an inner scroll.
        const fit = await page.evaluate(() => {
          const box = document.querySelector('[data-test="activity-row"]')?.closest('.overflow-x-auto')
          const columns = Array.from(document.querySelectorAll('table thead th')).filter(th => (th as HTMLElement).offsetParent !== null)
            .map(th => `${th.textContent?.trim() || '-'}:${Math.round(th.getBoundingClientRect().width)}`)
          return { overflow: box ? box.scrollWidth - box.clientWidth : 0, columns: columns.join(' ') }
        })
        // Phone width keeps its own table-fixed layout (F14), which is not the subject here.
        if (width >= 768) expect(fit.overflow, `activity table at ${width} must fit its card (overflow ${fit.overflow}px, columns ${fit.columns})`).toBeLessThanOrEqual(1)
        // The inline Why? shares the Status cell, so it only shows where the card has room for it.
        const rowWhy = page.locator('[data-test^="activity-row-why-"]').first()
        if (width >= 1280) await expect(rowWhy).toBeVisible()
        else await expect(rowWhy).toBeHidden()
        const status = page.locator('[data-test="activity-row"]').first().locator('.badge').last()
        const statusBox = await status.boundingBox()
        expect(statusBox, `status badge at ${width}`).not.toBeNull()
        expect(statusBox!.x + statusBox!.width, `status badge at ${width} inside the viewport`).toBeLessThanOrEqual(width)
      }
      await noHorizontalScroll(page, `${name} at ${width}`)
      const box = await page.locator(chip).first().boundingBox()
      expect(box, `${name} chip at ${width}`).not.toBeNull()
      expect(box!.x).toBeGreaterThanOrEqual(0)
      expect(box!.x + box!.width).toBeLessThanOrEqual(width)
      await shot(page, `${name}-${width}`)
    }

    // Blocked row detail at this width.
    await page.locator('[data-test="activity-row"]').first().locator('[data-test^="activity-open-"]').click()
    const why = page.locator('[data-test^="activity-why-"]')
    await expect(why).toBeVisible()
    // The drawer panel itself fits the viewport: nothing sits off-screen.
    // The drawer slides in, so wait for the transition to settle before measuring.
    const panelBox = () => page.locator('[data-test="activity-detail-panel"]').boundingBox()
    await expect.poll(async () => { const b = await panelBox(); return b ? b.x + b.width : Infinity }, { message: `drawer panel right edge at ${width}` }).toBeLessThanOrEqual(width + 1)
    const panel = await panelBox()
    expect(panel!.x).toBeGreaterThanOrEqual(0)
    for (const sel of ['[data-test="activity-drawer-attribution"]', '[data-test^="activity-allow-in-profile-"]', '[data-test^="activity-why-"]']) {
      const box = await page.locator(sel).first().boundingBox()
      expect(box, `${sel} at ${width}`).not.toBeNull()
      expect(box!.x, `${sel} left edge at ${width}`).toBeGreaterThanOrEqual(0)
      expect(box!.x + box!.width, `${sel} right edge at ${width}`).toBeLessThanOrEqual(width)
    }
    const target = await why.boundingBox()
    expect(target!.width).toBeGreaterThanOrEqual(24)
    expect(target!.height).toBeGreaterThanOrEqual(24)
    if (width < 1280) {
      // Below xl the Scope column folds away (it widened the table past its card); the drawer carries the chips.
      await expect(page.locator('[data-test="activity-scope-col"]')).toBeHidden()
      await expect(page.locator('[data-test="activity-drawer-attribution"]')).toBeVisible()
      await expect(page.locator('[data-test="activity-drawer-attribution"] [data-test="attribution-client"]')).toBeVisible()
    }
  })
}

test('7. keyboard: every chip remove button and Why? are reachable with Tab, Enter opens, Escape closes', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await open(page, `/tools?client=${SCOPE_CLIENT}`)
  await expect(page.locator(`[data-test="tools-why-${ECHO}"]`)).toBeVisible()
  // Start the Tab sequence at the page title: the header search box opens the
  // command palette (a modal) on focus, which is Spec 109-i's and not under test.
  await page.locator('h1').first().click()
  expect(await tabTo(page, '[data-test="scope-chip-remove-client"]'), 'the chip remove button is reachable by Tab').toBe(true)
  expect(await tabTo(page, `[data-test="tools-why-${ECHO}"]`), 'Why? is reachable by Tab').toBe(true)
  await page.keyboard.press('Enter')
  const dialog = page.locator('[data-test="access-explainer"]')
  await expect(dialog.locator('[data-test="explain-verdict"]')).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(dialog.locator('[data-test="explain-verdict"]')).toHaveCount(0)
  await expect(page.locator(`[data-test="tools-why-${ECHO}"]`)).toBeFocused()
})
