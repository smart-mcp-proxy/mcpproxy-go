import { test, expect } from '@playwright/test';
import { goto, captureConsole, flushConsole, assertNoConsoleErrors, SHOT_DIR } from './_helpers';

// UC5 — Settings + daisyUI v5 toggle interactivity.
// DEVIATION (documented): the /settings view is a Monaco-based config editor
// and contains NO daisyUI toggle. To honour the intent ("toggle a daisyUI
// toggle/checkbox and assert its state flips under v5") we (a) confirm the
// Settings view mounts with its editor, then (b) drive a real daisyUI v5
// `toggle toggle-primary` — the AddServerModal "Enabled" toggle, a pure
// v-model local-state control (no backend side effects) — and assert its
// checked state flips. This is the cleanest v5 toggle interactivity probe.
test('UC5 settings mounts + daisyUI v5 toggle flips', async ({ page }) => {
  const cap = captureConsole(page, 'UC5-settings-toggle');

  // (a) Settings view mounts (Monaco config editor card).
  await goto(page, '/settings');
  await expect(page.locator('h1', { hasText: /Configuration/i })).toBeVisible();
  await expect(page.locator('.card-title', { hasText: /Configuration Editor/i })).toBeVisible();
  await page.screenshot({ path: `${SHOT_DIR}/uc5-settings.png`, fullPage: false });

  // (b) daisyUI v5 toggle interactivity via the AddServerModal "Enabled" toggle.
  const addBtn = page.locator('button.btn-primary', { hasText: /Add Server/i }).first();
  await addBtn.click();
  await page.waitForTimeout(400);
  const dialog = page.locator('dialog.modal[open]');
  await expect(dialog).toBeVisible();

  const toggle = dialog.locator('input.toggle.toggle-primary[type="checkbox"]').first();
  await expect(toggle, 'daisyUI v5 toggle not found').toBeVisible();
  const before = await toggle.isChecked();
  await toggle.click();
  await page.waitForTimeout(300);
  const after = await toggle.isChecked();
  expect(after, 'daisyUI v5 toggle state did not flip').toBe(!before);
  await page.screenshot({ path: `${SHOT_DIR}/uc5-toggle-flipped.png`, fullPage: false });

  flushConsole('UC5-settings-toggle', cap);
  assertNoConsoleErrors(cap);
});
