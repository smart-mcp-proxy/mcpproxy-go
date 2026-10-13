// Spec 108 un-hide and Spec 108 attention items (Spec 109-l, specs/109-ux-navigation-consistency T151).
//
// Drives the Web UI served by a REAL mcpproxy binary (embedded frontend), seeded
// through REST (profiles-seed.ts):
//   1. the sidebar Connect group lists Profiles; "+ Add" -> Profile opens the
//      create dialog on /ui/profiles?create=1.
//   2. the header Viewing chip sets a profile; an in-app link carries it and Back restores it.
//   3. a Clients row links Activity / Sessions / Tools it sees / Usage by client.
//   4. an agent-token row links Activity / Usage by token.
//   5. /ui/servers?profile= lists the profile's servers with a removable chip.
//   6. a Spec 108 warning (an expiring client credential) becomes a needs-attention
//      item within seconds, ranked first, and its fix lands on the Clients row.
//   7. layout at 1440 / 900 / 390: no page-level horizontal scroll.
//   8. keyboard: Tab reaches the attention fix and each Clients-row link.
//
// Launcher: scripts/run-web-smoke.sh (docs/development/web-ui-verification.md).
import { test, expect, Page } from '@playwright/test'
import fs from 'node:fs'
import path from 'node:path'
import { CLIENT_ID, RO_PROFILE, SERVER, api, cleanupProfiles, seedProfiles } from './profiles-seed'

const BASE = process.env.MCPPROXY_BASE_URL || 'http://127.0.0.1:18080'
const KEY = process.env.MCPPROXY_API_KEY || ''

// Run-unique: a revoked token keeps its name for activity history (#1437 item 5).
const RUN = Date.now().toString(36)
const TOKEN = `e2e-unhide-tok-${RUN}`
const EXPIRING = `e2e-exp-${RUN}`

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

// The HTML reporter owns the report dir, so screenshots travel as attachments;
// SWEEP_SCREENSHOT_DIR, when set, also keeps them as files.
async function shot(page: Page, name: string) {
  const body = await page.screenshot({ fullPage: false })
  await test.info().attach(`profiles-unhide-${name}`, { body, contentType: 'image/png' })
  const dir = process.env.SWEEP_SCREENSHOT_DIR
  if (dir) {
    fs.mkdirSync(dir, { recursive: true })
    fs.writeFileSync(path.join(dir, `profiles-unhide-${name}.png`), body)
  }
}

/** Presses Tab until the focused element matches the selector. */
async function tabTo(page: Page, selector: string, max = 160): Promise<boolean> {
  for (let i = 0; i < max; i++) {
    await page.keyboard.press('Tab')
    if (await page.evaluate(sel => document.activeElement?.matches(sel) ?? false, selector)) return true
  }
  return false
}

test.describe.configure({ mode: 'serial' })
test.skip(!SERVER, 'needs a fixture upstream (SWEEP_SERVER_NAME)')

test.beforeAll(async () => {
  await cleanupProfiles([], [TOKEN])
  await api('DELETE', `/clients/${EXPIRING}`)
  await seedProfiles()
  const token = await api('POST', '/tokens', { name: TOKEN, profile: RO_PROFILE, expires_in: '30d' })
  if (token.status >= 300) throw new Error(`seeding token failed (${token.status}): ${JSON.stringify(token.data)}`)
})
test.afterAll(async () => {
  await api('DELETE', `/clients/${EXPIRING}`)
  await cleanupProfiles([], [TOKEN])
})

test('1. Profiles is in the sidebar and + Add -> Profile opens the create dialog', async ({ page }) => {
  await open(page, '/')
  await expect(page.locator('[data-test="sidebar-item-profiles"]')).toBeVisible()
  await page.locator('[data-test="header-add-menu"]').click()
  await page.locator('[data-test="add-menu-profile"]').click()
  await expect(page).toHaveURL(/\/ui\/profiles\?create=1|\/ui\/profiles$/)
  await expect(page.locator('[data-test="profile-create-dialog"]')).toBeVisible()
  await page.keyboard.press('Escape')
})

test('2. the Viewing chip sets a profile and an in-app link carries it; Back restores', async ({ page }) => {
  await open(page, '/clients')
  await expect(page.locator('[data-test="viewing-filter"]')).toBeVisible()
  await page.locator('[data-test="viewing-filter-button"]').click()
  await page.locator('[data-test="viewing-profile-select"]').selectOption(RO_PROFILE)
  await expect(page).toHaveURL(new RegExp(`profile=${RO_PROFILE}`))
  await page.keyboard.press('Escape')
  // Sticky: the Clients row's Usage link keeps the profile and adds the client.
  await page.locator('tbody tr', { hasText: 'E2E laptop' }).first().click()
  await page.locator(`[data-test="clients-row-link-usage-${CLIENT_ID}"]`).click()
  await expect(page).toHaveURL(new RegExp(`/ui/usage\\?(?=.*profile=${RO_PROFILE})(?=.*client=${CLIENT_ID})`))
  await expect(page.locator('[data-test="viewing-profile"]')).toBeVisible()
  await page.goBack()
  await expect(page).toHaveURL(new RegExp(`/ui/clients.*profile=${RO_PROFILE}`))
  await page.locator('[data-test="viewing-filter-clear"]').click()
  await expect(page).not.toHaveURL(/profile=/)
})

test('3. a Clients row links Activity, Sessions, Tools it sees and Usage by client', async ({ page }) => {
  await open(page, '/clients')
  await page.locator('tbody tr', { hasText: 'E2E laptop' }).first().click()
  for (const label of ['activity', 'sessions', 'tools', 'usage']) {
    await expect(page.locator(`[data-test="clients-row-link-${label}-${CLIENT_ID}"]`)).toBeVisible()
  }
  await expect(page.locator(`[data-test="clients-row-link-tools-${CLIENT_ID}"]`)).toHaveText('Tools it sees')
  await page.locator(`[data-test="clients-row-link-sessions-${CLIENT_ID}"]`).click()
  await expect(page).toHaveURL(new RegExp(`/ui/activity\\?.*view=sessions.*client=${CLIENT_ID}|/ui/activity\\?.*client=${CLIENT_ID}.*view=sessions`))
})

test('4. an agent-token row links Activity and Usage by token', async ({ page }) => {
  await open(page, '/clients?tab=tokens')
  await expect(page.locator(`[data-test="token-row-${TOKEN}"]`)).toBeVisible()
  await expect(page.locator(`[data-test="token-row-link-activity-${TOKEN}"]`)).toBeVisible()
  await page.locator(`[data-test="token-row-link-usage-${TOKEN}"]`).click()
  await expect(page).toHaveURL(new RegExp(`/ui/usage\\?.*token=${TOKEN}`))
  await expect(page.locator('[data-test="scope-chip-token"]')).toBeVisible()
})

test('5. /servers?profile= lists the profile\'s servers with a removable chip', async ({ page }) => {
  const scoped = page.waitForRequest(request => request.url().includes(`/api/v1/servers?profile=${RO_PROFILE}`))
  await open(page, `/servers?profile=${RO_PROFILE}`)
  await scoped
  await expect(page.locator('[data-test="scope-chip-profile"]')).toContainText('E2E Read-only')
  await expect(page.locator('[data-test="servers-profile-select"]')).toHaveValue(RO_PROFILE)
  // Only the profile's server (the fixture adds -2/-3/-4-quarantined outside it).
  const total = page.locator('[data-test="kpi-card-total"] .stat-value')
  await expect(total).toHaveText('1')
  await expect(page.locator('main').getByRole('link', { name: 'Details' })).toHaveCount(1)
  await page.locator('[data-test="scope-chip-remove-profile"]').click()
  await expect(page).not.toHaveURL(/profile=/)
  await expect(total).not.toHaveText('1')
  await expect(page.locator('main').getByRole('link', { name: 'Details' })).not.toHaveCount(1)
})

test('6. an expiring client credential becomes a needs-attention item, ranked first', async ({ page }) => {
  await open(page, '/')
  const created = await api('POST', '/clients', { id: EXPIRING, profile: RO_PROFILE, mode: 'locked', expires_in: '7d' })
  expect(created.status, JSON.stringify(created.data)).toBeLessThan(300)
  const item = page.locator(`[data-test="attention-item-client_credential_expiring:client:${EXPIRING}"]`)
  await expect(item).toBeVisible({ timeout: 8000 })
  // Ranked above any server item: it is the first row of the list.
  await expect(page.locator('[data-test^="attention-item-"]').first()).toHaveAttribute('data-test', /client_(credential_expiring|holds_admin_key)|anonymous_denied|profile_missing|client_token_name_conflict|client_rotation_pending/)
  await shot(page, 'home-attention-1440')
  await page.locator(`[data-test="attention-fix-client_credential_expiring:client:${EXPIRING}"]`).click()
  await expect(page).toHaveURL(new RegExp(`/ui/clients\\?focus=${EXPIRING}`))
  // The header pill count includes the item.
  const pill = page.locator('[data-test="header-attention-pill"]')
  if (await pill.count()) await expect(pill).toContainText(/[1-9]/)
  const stats = await api('GET', '/attention')
  expect(JSON.stringify(stats.data)).toContain(`client_credential_expiring:client:${EXPIRING}`)
})

test('7. layout at 1440, 900 and 390: no page-level horizontal scroll', async ({ page }) => {
  for (const width of [1440, 900, 390]) {
    await page.setViewportSize({ width, height: 900 })
    await open(page, '/')
    await expect(page.locator(`[data-test="attention-item-client_credential_expiring:client:${EXPIRING}"]`)).toBeVisible({ timeout: 8000 })
    const fix = page.locator(`[data-test="attention-fix-client_credential_expiring:client:${EXPIRING}"]`)
    await expect(fix).toBeVisible()
    const box = await fix.boundingBox()
    expect(box?.height ?? 0, `${width}: the fix button is at least 24px tall`).toBeGreaterThanOrEqual(24)
    await noHorizontalScroll(page, `Home ${width}`)
    await shot(page, `home-${width}`)

    await open(page, '/clients')
    await page.locator('tbody tr', { hasText: 'E2E laptop' }).first().click()
    await expect(page.locator(`[data-test="clients-row-link-sessions-${CLIENT_ID}"]`)).toBeVisible()
    await noHorizontalScroll(page, `Clients ${width}`)
    await shot(page, `clients-${width}`)

    await open(page, `/servers?profile=${RO_PROFILE}`)
    await expect(page.locator('[data-test="scope-chip-profile"]')).toBeVisible()
    await noHorizontalScroll(page, `Servers ${width}`)
    await shot(page, `servers-${width}`)
  }
  await page.setViewportSize({ width: 1440, height: 900 })
})

test('8. keyboard: Tab reaches the attention fix and each Clients-row link; Enter follows it', async ({ page }) => {
  await open(page, '/')
  await expect(page.locator(`[data-test="attention-fix-client_credential_expiring:client:${EXPIRING}"]`)).toBeVisible({ timeout: 8000 })
  // The header search opens the command palette when it takes focus, so the Tab
  // order is started from the list itself (clicking its heading sets the start point).
  await page.locator('[data-test="attention-list-card"] h3').click()
  expect(await tabTo(page, `[data-test="attention-fix-client_credential_expiring:client:${EXPIRING}"]`)).toBe(true)
  await page.keyboard.press('Enter')
  await expect(page).toHaveURL(new RegExp(`/ui/clients\\?focus=${EXPIRING}`))

  await open(page, `/clients?focus=${CLIENT_ID}`)
  await expect(page.locator(`[data-test="clients-row-link-activity-${CLIENT_ID}"]`)).toBeVisible()
  await page.locator('main h1').first().click()
  for (const label of ['activity', 'sessions', 'tools', 'usage']) {
    expect(await tabTo(page, `[data-test="clients-row-link-${label}-${CLIENT_ID}"]`), label).toBe(true)
  }
  await page.keyboard.press('Enter')
  await expect(page).toHaveURL(new RegExp(`/ui/usage\\?.*client=${CLIENT_ID}`))
})
