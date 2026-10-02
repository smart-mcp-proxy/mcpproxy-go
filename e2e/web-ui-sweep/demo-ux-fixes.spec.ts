// Demo UX fixes sweep (Spec 108 T149/T151, Spec 109 T161-T163, plan demo-ux-fixes).
//
// Drives the Web UI served by a REAL mcpproxy binary and checks, in layout terms
// a unit test cannot, the two findings that are about pixels:
//  - the token Profile chip stays on ONE line, inside its cell (a long profile
//    title used to wrap inside a fixed-height badge and the border cut through
//    the second line);
//  - radio and checkbox labels sit next to their control (the unlayered daisyUI
//    shim used to push the text to the far right).
// Both run at 1440x900 and 900x900 and attach screenshots to the report.
//
// Launcher: scripts/run-web-smoke.sh (docs/development/web-ui-verification.md).
import { test, expect, Page } from '@playwright/test'
import path from 'node:path'
import { SERVER, api, cleanupProfiles, seedProfiles } from './profiles-seed'

const BASE = process.env.MCPPROXY_BASE_URL || 'http://127.0.0.1:18080'
const KEY = process.env.MCPPROXY_API_KEY || ''
const REPORT_DIR = process.env.SWEEP_REPORT_DIR || './playwright-report'

const LONG_PROFILE = 'e2e-long-title'
const LONG_TITLE = 'Work Read-only for very long client names 2026'
const LONG_TOKEN = 'e2e-long-chip'
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

async function shot(page: Page, name: string) {
  await page.screenshot({ path: path.join(REPORT_DIR, `demo-ux-fixes-${name}.png`), fullPage: false })
}

test.describe.configure({ mode: 'serial' })
test.skip(!SERVER, 'needs a fixture upstream (SWEEP_SERVER_NAME)')

test.beforeAll(async () => {
  await cleanupProfiles([LONG_PROFILE], [LONG_TOKEN])
  await seedProfiles()
  await api('POST', '/profiles', { name: LONG_PROFILE, title: LONG_TITLE, servers: [SERVER], max_tier: 'read' })
  await api('POST', '/tokens', { name: LONG_TOKEN, profile: LONG_PROFILE, expires_in: '1d' })
})
test.afterAll(async () => {
  await cleanupProfiles([LONG_PROFILE], [LONG_TOKEN])
})

for (const viewport of VIEWPORTS) {
  const label = `${viewport.width}x${viewport.height}`

  test(`token profile chip is one line inside its cell at ${label}`, async ({ page }) => {
    await page.setViewportSize(viewport)
    await open(page, '/clients?tab=tokens')
    const chip = page.locator(`[data-test="token-profile-${LONG_TOKEN}"]`)
    await expect(chip).toBeVisible()
    await expect(chip).toHaveAttribute('title', LONG_TITLE)

    const measured = await chip.evaluate((el) => {
      const rect = el.getBoundingClientRect()
      const cell = el.closest('td')!.getBoundingClientRect()
      // One line: a Range over the inner text node reports a single client rect.
      const inner = el.querySelector('.truncate') ?? el
      const range = document.createRange()
      range.selectNodeContents(inner)
      return {
        scrollHeight: (el as HTMLElement).scrollHeight,
        clientHeight: (el as HTMLElement).clientHeight,
        scrollWidth: (inner as HTMLElement).scrollWidth,
        clientWidth: (inner as HTMLElement).clientWidth,
        lines: range.getClientRects().length,
        insideCell: rect.left >= cell.left - 1 && rect.right <= cell.right + 1 && rect.top >= cell.top - 1 && rect.bottom <= cell.bottom + 1,
        hasTitle: el.hasAttribute('title'),
      }
    })
    expect(measured.scrollHeight).toBeLessThanOrEqual(measured.clientHeight + 1)
    expect(measured.lines).toBe(1)
    expect(measured.insideCell).toBe(true)
    if (measured.scrollWidth > measured.clientWidth) expect(measured.hasTitle).toBe(true)
    await shot(page, `token-chip-${label}`)
  })

  test(`create-profile radio labels sit next to their radios at ${label}`, async ({ page }) => {
    await page.setViewportSize(viewport)
    await open(page, '/profiles?create=1')
    const dialog = page.locator('[data-test="profile-create-dialog"]')
    await expect(dialog).toBeVisible()
    const radios = dialog.locator('[data-test^="profile-create-start-"]')
    expect(await radios.count()).toBeGreaterThan(1)
    for (let n = 0; n < (await radios.count()); n++) {
      const gap = await radios.nth(n).evaluate((radio) => {
        const text = radio.closest('label')!.querySelector('.label-text')!.getBoundingClientRect()
        return text.left - radio.getBoundingClientRect().right
      })
      expect(gap, `radio ${n}`).toBeLessThanOrEqual(16)
      expect(gap, `radio ${n}`).toBeGreaterThanOrEqual(0)
    }
    await shot(page, `radio-labels-create-${label}`)
  })

  test(`custom-client and bulk-move radio labels sit next to their radios at ${label}`, async ({ page }) => {
    await page.setViewportSize(viewport)
    await open(page, '/clients')
    const cases: Array<{ open: string; radios: string; shot: string }> = [
      { open: '[data-test="clients-add-other"]', radios: '[data-test^="custom-client-mode-"]', shot: 'custom-client' },
      { open: '[data-test="clients-bulk-move"]', radios: '[data-test^="bulk-mode-"]', shot: 'bulk-move' },
    ]
    for (const c of cases) {
      const opener = page.locator(c.open)
      if (!(await opener.isVisible().catch(() => false))) {
        test.info().annotations.push({ type: 'skipped', description: `${c.shot}: ${c.open} not available on this seed` })
        continue
      }
      await opener.click()
      const radios = page.locator(`dialog[open] ${c.radios}`)
      if ((await radios.count()) === 0) {
        test.info().annotations.push({ type: 'skipped', description: `${c.shot}: no radios in the dialog on this seed` })
        await page.keyboard.press('Escape')
        continue
      }
      for (let n = 0; n < (await radios.count()); n++) {
        const gap = await radios.nth(n).evaluate((radio) => {
          const text = radio.closest('label')!.querySelector('.label-text')!.getBoundingClientRect()
          return text.left - radio.getBoundingClientRect().right
        })
        expect(gap, `${c.shot} radio ${n}`).toBeLessThanOrEqual(16)
      }
      await shot(page, `radio-labels-${c.shot}-${label}`)
      await page.keyboard.press('Escape')
    }
  })
}
