// Reproducible Web-UI verification for the Spec 060 Settings page.
//
// Setup (throwaway instance):
//   rm -rf /tmp/settings-uitest && mkdir -p /tmp/settings-uitest
//   cat > /tmp/settings-uitest/config.json <<'EOF'
//   { "listen":"127.0.0.1:18099","data_dir":"/tmp/settings-uitest","api_key":"settingskey",
//     "enable_socket":false,
//     "telemetry":{"enabled":true,"endpoint":"http://127.0.0.1:1/none"},
//     "mcpServers":[] }
//   (telemetry on — with a dead endpoint so nothing is sent — so the opt-out
//    confirmation can be exercised.)
//   EOF
//   ./mcpproxy serve --config=/tmp/settings-uitest/config.json --log-level=warn &
//
// Run:  BASE_URL=http://127.0.0.1:18099 API_KEY=settingskey \
//         ./node_modules/.bin/playwright test settings.spec.ts
//
// Screenshots are written only when SHOT_DIR is set (kept out of the repo).
import { test, expect } from '@playwright/test'

const BASE = process.env.BASE_URL || 'http://127.0.0.1:18099'
const KEY = process.env.API_KEY || 'settingskey'
const SHOT = process.env.SHOT_DIR // optional; unset in CI

async function shot(page: any, name: string) {
  if (SHOT) await page.screenshot({ path: `${SHOT}/${name}.png`, fullPage: true })
}

test('settings page: sections, search, posture, partial save, danger confirm', async ({ page }) => {
  await page.goto(`${BASE}/ui/settings?apikey=${KEY}`)
  await page.waitForLoadState('domcontentloaded')
  await page.waitForSelector('[data-test="settings-tabs"]')

  // Security tab renders with the at-a-glance posture summary + connect helper.
  await expect(page.locator('[data-test="settings-posture"]')).toBeVisible()
  await expect(page.locator('[data-test="setting-secret-api_key"]')).toBeVisible()
  await expect(page.locator('[data-test="setting-copy-api_key"]')).toBeVisible()
  await expect(page.locator('[data-test="settings-connect-client"]')).toBeVisible()

  // Regenerating the API key asks for confirmation before changing the value.
  const apiKeyVal = await page.locator('[data-test="setting-secret-api_key"]').inputValue()
  await page.locator('[data-test="setting-regenerate-api_key"]').click()
  const regenOpen = await page.evaluate(() => {
    const d = document.querySelector('[data-test="setting-regenerate-confirm-api_key"]') as HTMLDialogElement | null
    return !!d && d.open
  })
  expect(regenOpen).toBeTruthy()
  await page.locator('[data-test="setting-regenerate-cancel-api_key"]').click()
  // value unchanged after cancelling
  expect(await page.locator('[data-test="setting-secret-api_key"]').inputValue()).toBe(apiKeyVal)
  // Listen address validation: a malformed host:port shows an error and blocks Save.
  const origListen = await page.locator('[data-test="setting-text-listen"]').inputValue()
  await page.locator('[data-test="setting-text-listen"]').fill('not-an-address')
  await expect(page.locator('[data-test="setting-error-listen"]')).toBeVisible()
  await expect(page.locator('[data-test="settings-apply-security"]')).toBeDisabled()
  await page.locator('[data-test="setting-text-listen"]').fill(origListen) // restore (clears dirty)
  await expect(page.locator('[data-test="setting-error-listen"]')).toHaveCount(0)

  // doc links: per-field (quarantine) + full config reference in the header
  await expect(page.locator('[data-test="setting-docs-quarantine_enabled"]')).toHaveAttribute('href', /docs\.mcpproxy\.app\/features\/security-quarantine/)
  await expect(page.locator('[data-test="settings-docs-reference"]')).toBeVisible()
  await shot(page, 's01-security')

  // Cross-section search surfaces matching fields from any section.
  await page.locator('[data-test="settings-search"]').fill('docker')
  await expect(page.locator('[data-test="settings-search-results"]')).toBeVisible()
  await shot(page, 's00-search')
  await page.locator('[data-test="settings-search"]').fill('')

  // Partial save: flip a non-dangerous toggle and confirm the saved indicator.
  await page.locator('[data-test="setting-toggle-require_mcp_auth"]').click()
  await page.locator('[data-test="settings-apply-security"]').click()
  await page.waitForSelector(
    '[data-test="settings-saved-security"], [data-test="settings-restart-security"]',
    { timeout: 8000 }
  )
  await shot(page, 's02-security-saved')

  // Dangerous toggle requires explicit confirmation.
  await page.locator('[data-test="setting-toggle-reveal_secret_headers"]').click()
  await page.locator('[data-test="settings-apply-security"]').click()
  const dialogOpen = await page.evaluate(() => {
    const d = document.querySelector('[data-test="settings-confirm-security"]') as HTMLDialogElement | null
    return !!d && d.open
  })
  expect(dialogOpen).toBeTruthy()
  await shot(page, 's03-danger-confirm')
  await page.locator('[data-test="settings-confirm-security"] [data-test="settings-confirm-cancel"]').click()

  // General tab: duration validation + telemetry opt-out confirmation.
  await page.locator('[data-test="settings-tab-general"]').click()
  await expect(page.locator('[data-test="setting-select-routing_mode"]')).toBeVisible()

  // Invalid duration is rejected (error shown, Save blocked).
  await page.locator('[data-test="setting-text-call_tool_timeout"]').fill('not-a-duration')
  await expect(page.locator('[data-test="setting-error-call_tool_timeout"]')).toBeVisible()
  await expect(page.locator('[data-test="settings-apply-general"]')).toBeDisabled()
  await page.locator('[data-test="setting-text-call_tool_timeout"]').fill('90s')
  await expect(page.locator('[data-test="setting-error-call_tool_timeout"]')).toHaveCount(0)

  // Turning telemetry OFF asks for confirmation (info tone).
  await page.locator('[data-test="setting-toggle-telemetry.enabled"]').click()
  await page.locator('[data-test="settings-apply-general"]').click()
  const telemetryConfirm = await page.evaluate(() => {
    const d = document.querySelector('[data-test="settings-confirm-general"]') as HTMLDialogElement | null
    return !!d && d.open
  })
  expect(telemetryConfirm).toBeTruthy()
  await page.locator('[data-test="settings-confirm-general"] [data-test="settings-confirm-cancel"]').click()
  await page.locator('[data-test="settings-tab-advanced"]').click()
  await expect(page.locator('[data-test="settings-accordion-output-sanitisation"]')).toBeVisible()
  await page.locator('[data-test="settings-tab-raw"]').click()

  // Connect-a-client helper opens the shared ConnectModal (done last so the
  // modal doesn't overlay earlier interactions).
  await page.locator('[data-test="settings-tab-security"]').click()
  await page.locator('[data-test="settings-connect-client"]').click()
  await expect(page.getByText('Connect MCPProxy to AI Agents')).toBeVisible()
})
