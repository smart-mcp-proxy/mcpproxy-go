import { test, expect, request } from '@playwright/test'

// MCP-2484 (Codex fix on #685): on a fresh install the MCP "Server instructions"
// textarea is PREFILLED with the built-in default, and clicking Save WITHOUT
// editing must persist it as config.instructions (the prefilled value is dirty).
//
// Scenario: fresh install → open Settings → Advanced → MCP accordion →
// default text is IN the textarea → Save without editing → reload →
// instructions still present AND persisted to config.

const BASE = process.env.MCP_BASE || 'http://127.0.0.1:18482'
const KEY = process.env.MCP_KEY || 'uitest2484'

async function getConfigInstructions(): Promise<string> {
  const ctx = await request.newContext()
  const resp = await ctx.get(`${BASE}/api/v1/config`, { headers: { 'X-API-Key': KEY } })
  const body = await resp.json()
  await ctx.dispose()
  return body?.data?.config?.instructions ?? body?.config?.instructions ?? ''
}

test('prefilled instructions persist on Save without editing', async ({ page }) => {
  // fresh install: config.instructions starts empty
  expect(await getConfigInstructions()).toBe('')

  await page.goto(`${BASE}/ui/settings?apikey=${KEY}`)
  await page.waitForLoadState('domcontentloaded')

  // Advanced tab → expand the "MCP server instructions" accordion
  await page.locator('[data-test="settings-tab-advanced"]').click()
  await page.locator('[data-test="settings-accordion-mcp"]').click()

  const textarea = page.locator('[data-test="setting-textarea-instructions"]')
  await expect(textarea).toBeVisible()

  // the default text is IN the textarea (not just the placeholder)
  const prefilled = await textarea.inputValue()
  expect(prefilled.trim().length).toBeGreaterThan(0)

  // Save WITHOUT editing — the prefilled value must be dirty & savable
  const save = page.locator('[data-test="settings-apply-mcp"]')
  await expect(save).toBeEnabled()
  await save.click()

  // persisted to config
  await expect.poll(async () => await getConfigInstructions(), { timeout: 5000 }).toBe(prefilled)

  // survives a reload (still IN the textarea, loaded from saved config now)
  await page.reload()
  await page.waitForLoadState('domcontentloaded')
  await page.locator('[data-test="settings-tab-advanced"]').click()
  await page.locator('[data-test="settings-accordion-mcp"]').click()
  expect((await textarea.inputValue()).trim()).toBe(prefilled.trim())
})

test('Reset to default repopulates after a custom edit', async ({ page }) => {
  await page.goto(`${BASE}/ui/settings?apikey=${KEY}`)
  await page.waitForLoadState('domcontentloaded')
  await page.locator('[data-test="settings-tab-advanced"]').click()
  await page.locator('[data-test="settings-accordion-mcp"]').click()

  const textarea = page.locator('[data-test="setting-textarea-instructions"]')
  const def = (await textarea.inputValue()).trim()
  expect(def.length).toBeGreaterThan(0)

  await textarea.fill('my custom instructions')
  expect(await textarea.inputValue()).toBe('my custom instructions')

  await page.locator('[data-test="setting-reset-instructions"]').click()
  expect((await textarea.inputValue()).trim()).toBe(def)
})
