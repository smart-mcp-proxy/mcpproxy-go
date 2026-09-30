// Profiles v3 Web UI sweep (Spec 108-i, specs/108-profiles-v3 T103).
//
// Drives the Web UI served by a REAL mcpproxy binary (embedded frontend), seeded
// through REST only (profiles-seed.ts): the goal flow "create a Read-only
// profile, assign it to a client, mint a token bound to it", rename and explain,
// the header Viewing chip, layout at four widths, real keyboard Escape on every
// dialog, and SSE-driven chip updates.
//
// Launcher: scripts/run-web-smoke.sh (docs/development/web-ui-verification.md).
import { test, expect, Page } from '@playwright/test'
import path from 'node:path'
import { CLIENT_ID, RO_PROFILE, SERVER, api, cleanupProfiles, seedProfiles } from './profiles-seed'

const BASE = process.env.MCPPROXY_BASE_URL || 'http://127.0.0.1:18080'
const KEY = process.env.MCPPROXY_API_KEY || ''
const REPORT_DIR = process.env.SWEEP_REPORT_DIR || './playwright-report'

const WORK = 'e2e-work-ro'
const WORK2 = 'e2e-work-ro2'
const TOKEN = 'e2e-ci'

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

test.describe.configure({ mode: 'serial' })
test.skip(!SERVER, 'needs a fixture upstream (SWEEP_SERVER_NAME)')

test.beforeAll(async () => {
  await cleanupProfiles([WORK, WORK2], [TOKEN])
  await seedProfiles()
})
test.afterAll(async () => {
  await cleanupProfiles([WORK, WORK2], [TOKEN])
})

test('goal flow: create a profile, deny a tool, try it, mint a token, assign a client', async ({ page }) => {
  await open(page, '/profiles')
  await expect(page.locator('[data-test="profile-card-all-servers"]')).toBeVisible()

  await page.getByRole('button', { name: 'Add profile' }).click()
  const dialog = page.locator('[data-test="profile-create-dialog"]')
  await expect(dialog).toBeVisible()
  await dialog.locator('[data-test="profile-create-name"]').fill(WORK)
  await dialog.locator(`[data-test="profile-create-server-${SERVER}"]`).check()
  await dialog.locator('[data-test="profile-create-start-read"]').check()
  await dialog.locator('[data-test="profile-create-submit"]').click()

  // The editor opens on the new profile.
  await expect(page).toHaveURL(new RegExp(`/ui/profiles/${WORK}`))
  await expect(page.locator('[data-test="profile-editor"]')).toBeVisible()
  const rows = page.locator('[data-test^="profile-tool-row-"]')
  await expect(rows.first()).toBeVisible()

  // A tool above the Read cap shows the reason in words, when the fixture has one.
  const tools = (await api('GET', `/profiles/${WORK}/effective-tools`)).data.tools as Array<{ server: string; tool: string; intrinsic_tier: string; access: { visible: boolean } }>
  const capped = tools.find(row => !row.access.visible)
  if (capped) {
    await expect(page.locator(`[data-test="profile-tool-row-${capped.server}__${capped.tool}"]`)).toContainText('Above tier cap')
  }

  // Try it runs a search under the draft and reports what it hides.
  const target = tools.find(row => row.access.visible)!
  await page.locator('[data-test="profile-try-query"]').fill(target.tool)
  await page.locator('[data-test="profile-try-run"]').click()
  await expect(page.locator('[data-test="profile-try-hidden-count"]')).toContainText('Hidden by profile:')

  // An unannotated tool classified as Read becomes visible after Save (when the fixture has one).
  const unannotated = tools.find(row => row.intrinsic_tier === 'unannotated')
  if (unannotated) {
    const uid = `${unannotated.server}__${unannotated.tool}`
    await page.locator(`[data-test="tool-classify-${uid}"]`).selectOption('read')
    await page.locator('[data-test="profile-editor-save"]').click()
    await expect(page.locator(`[data-test="profile-tool-row-${uid}"]`)).toContainText('Visible')
  }

  // "Create token with this profile" from the card lands on the token dialog, preset.
  await open(page, '/profiles')
  await page.locator(`[data-test="profile-more-${WORK}"]`).click()
  await page.locator(`[data-test="profile-create-token-${WORK}"]`).click()
  await expect(page).toHaveURL(/\/ui\/clients\?.*tab=tokens/)
  const tokenSelect = page.locator('[data-test="token-profile-select"]')
  await expect(tokenSelect).toHaveValue(WORK)
  await page.locator('#token-name').fill(TOKEN)
  await page.locator('dialog[open]').getByRole('button', { name: 'Create Token', exact: true }).click()
  await expect(page.locator(`[data-test="token-row-${TOKEN}"]`)).toBeVisible()
  await expect(page.locator(`[data-test="token-profile-${TOKEN}"]`)).toHaveText(WORK)

  // On Clients, move the client to the new profile: the PUT is observed and the chip reads the source.
  await open(page, '/clients')
  const chip = page.locator(`[data-test="client-profile-chip-${CLIENT_ID}"]`)
  await expect(chip).toBeVisible()
  await chip.click()
  const put = page.waitForResponse(response => response.url().includes(`/clients/${CLIENT_ID}/binding`) && response.request().method() === 'PUT')
  await page.locator(`[data-test="client-profile-option-${CLIENT_ID}-${WORK}"]`).click()
  expect((await put).status()).toBe(200)
  await expect(chip).toContainText(`${WORK} · Read-only · locked by credential`)
  await page.screenshot({ path: path.join(REPORT_DIR, 'profiles-clients-chip.png') })
})

test('narrowing a bound profile is refused by the binding guard, and the fix lands on Settings without saving', async ({ page }) => {
  const tools = (await api('GET', `/profiles/${WORK}/effective-tools`)).data.tools as Array<{ server: string; tool: string; access: { visible: boolean } }>
  const target = tools.find(row => row.access.visible)!
  const id = `${target.server}__${target.tool}`
  await open(page, `/profiles/${WORK}`)
  await page.locator(`[data-test="tool-deny-${id}"]`).check()
  await page.locator('[data-test="profile-editor-save"]').click()

  // Anonymous callers reach e2e-ro, which is wider than the narrowed profile the
  // client is bound to: FR-008a refuses, nothing is written, the draft stays.
  const refusal = page.locator('[data-test="guard-refusal"]')
  await expect(refusal).toBeVisible()
  await expect(refusal.locator('[data-test^="guard-fix-"]')).toHaveCount(2)
  await expect(page.locator('[data-test="profile-save-status"]')).toHaveText('Unsaved changes')
  expect((await api('GET', `/profiles/${WORK}`)).data.tools?.deny ?? []).toEqual([])

  // The fix only navigates: Settings shows the change preselected, unsaved.
  await refusal.locator('[data-test="guard-fix-set_anonymous_profile"]').click()
  await expect(page).toHaveURL(/\/ui\/settings\?.*focus=anonymous_profile/)
  const select = page.locator('[data-test="anonymous-profile-select"]')
  await expect(select).toHaveValue(WORK)
  expect((await api('GET', '/profiles')).data.anonymous_profile).toBe(RO_PROFILE)
  await page.locator('[data-test="anonymous-profile-save"]').click()
  await expect(page.locator('[data-test="anonymous-profile-saved"]')).toBeVisible()
  expect((await api('GET', '/profiles')).data.anonymous_profile).toBe(WORK)

  // With anonymous callers on the same profile, the deny is no longer a bypass.
  await open(page, `/profiles/${WORK}`)
  await page.locator(`[data-test="tool-deny-${id}"]`).check()
  await page.locator('[data-test="profile-editor-save"]').click()
  await expect(page.locator(`[data-test="profile-tool-row-${id}"]`)).toContainText('Denied by rule')
})

test('rename moves the client and the token, and back', async ({ page }) => {
  await open(page, `/profiles/${WORK}`)
  await page.locator('[data-test="profile-rename"]').click()
  const dialog = page.locator('[data-test="profile-rename-dialog"]')
  await expect(dialog).toBeVisible()
  // The impact list names what moves BEFORE anything is posted.
  await expect(dialog.locator(`[data-test="impact-client-${CLIENT_ID}"]`)).toBeVisible()
  await expect(dialog.locator(`[data-test="impact-token-${TOKEN}"]`)).toBeVisible()
  await expect(dialog.locator('[data-test="impact-anonymous"]')).toBeVisible()
  await dialog.locator('[data-test="profile-rename-input"]').fill(WORK2)
  await dialog.locator('[data-test="profile-rename-submit"]').click()
  await expect(page).toHaveURL(new RegExp(`/ui/profiles/${WORK2}`))
  const moved = (await api('GET', `/clients`)).data.clients.find((client: any) => client.id === CLIENT_ID)
  expect(moved.profile).toBe(WORK2)

  await page.locator('[data-test="profile-rename"]').click()
  await dialog.locator('[data-test="profile-rename-input"]').fill(WORK)
  await dialog.locator('[data-test="profile-rename-submit"]').click()
  await expect(page).toHaveURL(new RegExp(`/ui/profiles/${WORK}$`))
})

test('Explain access on a client shows the verdict and a fix that lands on the focused row', async ({ page }) => {
  // The tool the goal flow denied is hidden for the client by rule.
  const tools = (await api('GET', `/profiles/${WORK}/effective-tools`)).data.tools as Array<{ server: string; tool: string; access: { visible: boolean; reason: string } }>
  const hidden = tools.find(row => !row.access.visible)
  test.skip(!hidden, 'no hidden tool to explain')
  const key = `${hidden!.server}:${hidden!.tool}`

  await open(page, `/clients?focus=${CLIENT_ID}`)
  await page.locator(`[data-test="client-explain-${CLIENT_ID}"]`).click()
  const explainer = page.locator('[data-test="access-explainer"]')
  await expect(explainer).toBeVisible()
  await explainer.locator('[data-test="explain-tool-input"]').fill(key)
  await explainer.locator('[data-test="explain-run"]').click()
  await expect(explainer.locator('[data-test="explain-verdict"]')).toContainText('Hidden')
  const fix = explainer.locator('[data-test="explain-fix-allow_in_profile"]')
  await expect(fix).toBeVisible()
  await fix.click()
  await expect(page).toHaveURL(new RegExp(`/ui/profiles/${WORK}\\?focus=`))
  const row = page.locator(`[data-test="profile-tool-row-${hidden!.server}__${hidden!.tool}"]`)
  await expect(row).toHaveAttribute('aria-current', 'true')
})

test('the header has no Profile switcher and the Viewing chip survives navigation and Back', async ({ page }) => {
  await open(page, '/clients')
  await expect(page.locator('header')).not.toContainText('Profile:')
  await expect(page.locator('[data-test="profile-switcher"]')).toHaveCount(0)
  const chip = page.locator('[data-test="viewing-filter-button"]')
  await expect(chip).toContainText('Viewing: all')
  await chip.click()
  await page.locator('[data-test="viewing-profile-select"]').selectOption(WORK)
  await expect(page).toHaveURL(new RegExp(`profile=${WORK}`))
  await expect(chip).toContainText(WORK)
  // Escape closes the popover and keeps the value.
  await page.keyboard.press('Escape')
  await expect(page.locator('[data-test="viewing-filter-popover"]')).toHaveCount(0)
  await expect(chip).toBeFocused()

  // In-app links built with the link map carry the filter: the Clients row's Tools link.
  await page.locator('tbody tr', { hasText: 'E2E laptop' }).first().click()
  await page.getByRole('link', { name: 'Tools', exact: true }).first().click()
  await expect(page).toHaveURL(new RegExp(`/ui/tools.*profile=${WORK}`))
  await expect(chip).toContainText(WORK)
  await page.goBack()
  await expect(page).toHaveURL(new RegExp(`/ui/clients.*profile=${WORK}`))
  await expect(chip).toContainText(WORK)
  await page.locator('[data-test="viewing-filter-clear"]').click()
  await expect(page).not.toHaveURL(/profile=/)
})

for (const width of [1440, 1100, 900, 390]) {
  test(`layout at ${width}px: no page-level horizontal scroll, header controls not clipped`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 })
    for (const route of ['/profiles', `/profiles/${RO_PROFILE}`, '/clients']) {
      await open(page, route)
      await page.waitForTimeout(900)
      const overflow = await page.evaluate(() => ({ scroll: document.scrollingElement!.scrollWidth, inner: window.innerWidth }))
      expect(overflow.scroll, `${route} at ${width}px`).toBeLessThanOrEqual(overflow.inner)
      // The header row and the Viewing chip stay inside the viewport.
      for (const selector of ['[data-test="header-row"]', '[data-test="viewing-filter-button"]', '[data-test="header-add-menu"]']) {
        const box = await page.locator(selector).first().boundingBox()
        if (!box) continue
        expect(box.x, `${selector} left edge at ${width}px`).toBeGreaterThanOrEqual(-1)
        expect(box.x + box.width, `${selector} right edge at ${width}px`).toBeLessThanOrEqual(width + 1)
      }
      await page.screenshot({ path: path.join(REPORT_DIR, `profiles-${route.replaceAll('/', '_')}-${width}.png`) })
    }
  })
}

test('every dialog opens on its first field and closes on a real Escape, returning focus to its trigger', async ({ page }) => {
  async function expectEscape(trigger: ReturnType<Page['locator']>, dialog: ReturnType<Page['locator']>) {
    await trigger.focus()
    await trigger.click()
    await expect(dialog).toHaveJSProperty('open', true)
    // Focus moved into the dialog, onto a real control.
    await expect.poll(() => dialog.evaluate(el => el.contains(document.activeElement) && document.activeElement !== el)).toBe(true)
    await page.keyboard.press('Escape')
    await expect(dialog).toHaveJSProperty('open', false)
    await expect(trigger).toBeFocused()
  }

  await open(page, '/profiles')
  await expectEscape(page.getByRole('button', { name: 'Add profile' }), page.locator('[data-test="profile-create-dialog"]'))

  await open(page, `/profiles/${WORK}`)
  await expectEscape(page.locator('[data-test="profile-rename"]'), page.locator('[data-test="profile-rename-dialog"]'))
  await expectEscape(page.locator('[data-test="profile-delete"]'), page.locator('[data-test="profile-delete-dialog"]'))
  await expectEscape(page.locator('[data-test="profile-assign"]'), page.locator('[data-test="assign-client-dialog"]'))

  await open(page, '/clients')
  await expectEscape(page.locator('[data-test="clients-bulk-move"]'), page.locator('[data-test="bulk-move-dialog"]'))
  await expectEscape(page.locator('[data-test="clients-add-other"]'), page.locator('[data-test="custom-client-dialog"]'))
  const upgrade = page.locator('[data-test="clients-upgrade-admin-key"]')
  if (await upgrade.isVisible().catch(() => false)) {
    await expectEscape(upgrade, page.locator('[data-test="upgrade-admin-key-dialog"]'))
  }

  // The one-time credential dialog: add a client, then Escape the credential.
  await page.locator('[data-test="clients-add-other"]').click()
  const custom = page.locator('[data-test="custom-client-dialog"]')
  await custom.locator('[data-test="custom-client-id"]').fill('e2e-kbd')
  await custom.locator('[data-test="custom-client-submit"]').click()
  const once = page.locator('[data-test="credential-once-dialog"]')
  await expect(once).toHaveJSProperty('open', true)
  await expect(once.locator('[data-test="credential-once-secret"]')).toHaveValue(/^mcp_cli_/)
  await page.keyboard.press('Escape')
  await expect(once).toHaveJSProperty('open', false)
  // The secret does not survive the dialog.
  await expect(page.locator('body')).not.toContainText(/mcp_cli_[0-9a-f]{8}/)
  await api('DELETE', '/clients/e2e-kbd')

  // The explainer, from an expanded client row.
  await open(page, `/clients?focus=${CLIENT_ID}`)
  await expectEscape(page.locator(`[data-test="client-explain-${CLIENT_ID}"]`), page.locator('[data-test="access-explainer"]'))
})

test('a binding changed in the background updates the chip without a reload', async ({ page }) => {
  // The event stream must be open before the change: an SSE event sent earlier is gone.
  const stream = page.waitForResponse(response => response.url().includes('/events'))
  await open(page, '/clients')
  await stream
  await page.waitForTimeout(400)
  const chip = page.locator(`[data-test="client-profile-chip-${CLIENT_ID}"]`)
  await expect(chip).toContainText('locked by credential')
  const reloads: string[] = []
  page.on('framenavigated', frame => reloads.push(frame.url()))
  // Unlocking keeps the client on its profile, so the guard has nothing to refuse.
  const changed = await api('PUT', `/clients/${CLIENT_ID}/binding`, { profile: WORK, mode: 'switchable' })
  expect(changed.status, JSON.stringify(changed.data)).toBe(200)
  await expect(chip).toContainText('switchable', { timeout: 2500 })
  expect(reloads).toEqual([])
})
