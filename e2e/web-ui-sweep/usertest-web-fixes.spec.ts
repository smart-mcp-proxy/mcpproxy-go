// First-run user test fixes sweep (Spec 109 T172-T177, Spec 108 T153; plan
// fix-usertest-web).
//
// Drives the Web UI served by a REAL mcpproxy binary and checks the three
// findings that are about what a user SEES:
//  - the header status pill says "N awaiting review" instead of reading a
//    review-pending install as "0 of N online" (compact form at 900px keeps
//    the full text in its title and never scrolls the header sideways);
//  - a secret-like env value typed in Manual add is masked, with a Show/Hide
//    toggle that changes display only;
//  - profile Try it never prints "[object Object]", says it uses unsaved
//    edits, and the tool counts are labelled as the saved profile.
// The setup wizard's import flow is covered by vitest plus live QA: this
// launcher's core uses the real HOME for canonical client paths.
//
// Launcher: scripts/run-web-smoke.sh (docs/development/web-ui-verification.md).
import { test, expect, Page } from '@playwright/test'
import path from 'node:path'
import { SERVER, api, cleanupProfiles } from './profiles-seed'

const BASE = process.env.MCPPROXY_BASE_URL || 'http://127.0.0.1:18080'
const KEY = process.env.MCPPROXY_API_KEY || ''
const REPORT_DIR = process.env.SWEEP_REPORT_DIR || './playwright-report'

const TRY_PROFILE = 'e2e-try'
const FAKE_SECRET = 'fake-secret-value'
const VIEWPORTS = [
  { width: 1440, height: 900 },
  { width: 900, height: 900 },
]

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

async function shot(page: Page, name: string, label: string) {
  await page.screenshot({ path: path.join(REPORT_DIR, `usertest-web-fixes-${name}-${label}.png`), fullPage: false })
}

test.describe.configure({ mode: 'serial' })
test.skip(!SERVER, 'needs a fixture upstream (SWEEP_SERVER_NAME)')

test.beforeAll(async () => {
  await cleanupProfiles([TRY_PROFILE])
  await api('POST', '/profiles', { name: TRY_PROFILE, title: 'E2E Try', servers: [SERVER], max_tier: 'read' })
})
test.afterAll(async () => {
  await cleanupProfiles([TRY_PROFILE])
})

for (const viewport of VIEWPORTS) {
  const label = `${viewport.width}x${viewport.height}`
  const wide = viewport.width >= 1100

  test(`status pill names servers awaiting review at ${label}`, async ({ page }) => {
    await page.setViewportSize(viewport)
    await open(page, '/')
    const pill = page.locator('[data-test="header-status-pill"]')
    await expect(pill).toBeVisible()
    // The sweep config has a quarantined server, so review is pending.
    if (wide) {
      await expect(pill).toContainText(/\d+ online · \d+ awaiting review/)
    } else {
      await expect(page.locator('[data-test="header-status-compact"]')).toHaveText(/^\d+\/\d+$/)
      await expect(pill).toHaveAttribute('title', /awaiting review/)
    }
    const overflow = await page.evaluate(() => document.scrollingElement!.scrollWidth - document.scrollingElement!.clientWidth)
    expect(overflow, `page scrolls sideways at ${label}`).toBeLessThanOrEqual(1)
    await shot(page, 'pill', label)
  })

  test(`Manual add masks a secret-like value while typing at ${label}`, async ({ page }) => {
    await page.setViewportSize(viewport)
    await open(page, '/add-server?tab=manual')
    await page.locator('[data-test="manual-type-stdio"]').check()
    await page.locator('[data-test="manual-env-add"]').click()
    await page.locator('[data-test="manual-env-name-0"]').fill('API_TOKEN')
    const input = page.locator('[data-test="secret-toggle-value-input"]').first()
    await input.fill(FAKE_SECRET)

    await expect(input).toHaveAttribute('type', 'password')
    expect(await page.locator('body').innerText()).not.toContain(FAKE_SECRET)

    const reveal = page.locator('[data-test="secret-toggle-reveal"]').first()
    await reveal.click()
    await expect(input).toHaveAttribute('type', 'text')
    await shot(page, 'secret-revealed', label)
    await reveal.click()
    await expect(input).toHaveAttribute('type', 'password')
    await shot(page, 'secret-masked', label)
    // Never submitted: the sweep must not add a server.
  })

  test(`profile Try it is readable and labels unsaved edits at ${label}`, async ({ page }) => {
    await page.setViewportSize(viewport)
    await open(page, `/profiles/${TRY_PROFILE}`)
    await expect(page.locator('[data-test="profile-tool-counts"]')).toBeVisible()
    await expect(page.locator('[data-test="profile-try-source"]')).toHaveText('Uses the saved profile')

    // Deny the first row (unsaved edit).
    await page.locator('[data-test^="tool-deny-"]').first().check()
    await expect(page.locator('[data-test="profile-tool-unsaved-note"]')).toBeVisible()
    await expect(page.locator('[data-test="profile-tool-counts"]')).toContainText(/^Saved profile:/)

    await page.locator('[data-test="profile-try-query"]').fill('read')
    await page.locator('[data-test="profile-try-run"]').click()
    const results = page.locator('[data-test="profile-try-results"]')
    await expect(results).toContainText('Hidden by profile')
    expect(await results.innerText()).not.toContain('[object Object]')
    await expect(page.locator('[data-test="profile-try-source"]')).toHaveText('Uses your unsaved edits')
    await shot(page, 'profile-try', label)

    // Discard so the profile on disk stays as seeded.
    await page.locator('[data-test="profile-editor-discard"]').click()
    await expect(page.locator('[data-test="profile-tool-unsaved-note"]')).toHaveCount(0)
  })
}
