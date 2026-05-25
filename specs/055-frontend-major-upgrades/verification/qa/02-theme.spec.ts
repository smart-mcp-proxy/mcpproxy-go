import { test, expect } from '@playwright/test';
import { goto, captureConsole, flushConsole, assertNoConsoleErrors, SHOT_DIR } from './_helpers';

// UC2 — Theme switch (most important DaisyUI-v5 regression check).
// The theme dropdown lives in SidebarNav footer; each <a> calls
// systemStore.setTheme(name) which sets document.documentElement[data-theme].
const THEMES = ['light', 'dark', 'synthwave'];

async function currentTheme(page) {
  return page.evaluate(() => document.documentElement.getAttribute('data-theme'));
}

test('UC2 theme switch — light, dark, synthwave apply via data-theme', async ({ page }) => {
  const cap = captureConsole(page, 'UC2-theme');
  await goto(page, '/');

  // Open the theme dropdown trigger (the "Theme" ghost button in the sidebar footer).
  const trigger = page.locator('.dropdown-top [role="button"]', { hasText: 'Theme' }).first();
  await expect(trigger, 'theme dropdown trigger not found').toBeVisible();

  // Map theme name -> visible displayName in the dropdown list.
  const display: Record<string, string> = { light: 'Light', dark: 'Dark', synthwave: 'Synthwave' };

  for (const theme of THEMES) {
    // daisyUI dropdowns open on focus. Blur first (clicking an entry leaves the
    // tree focused, which makes a subsequent click "intercepted"), then focus
    // the trigger to open the menu.
    await page.locator('body').evaluate((b) => (document.activeElement as HTMLElement)?.blur());
    await page.waitForTimeout(100);
    await trigger.focus();
    await page.waitForTimeout(250);

    // The <a> entry text contains the displayName (plus a color-swatch span).
    // Match by substring rather than anchored regex. force:true because the
    // dropdown-content positioning can confuse Playwright's visibility check.
    const entry = page.locator('.dropdown-content a').filter({ hasText: display[theme] }).first();
    await entry.click({ force: true });
    await page.waitForTimeout(400);

    const applied = await currentTheme(page);
    expect(applied, `data-theme did not switch to ${theme}`).toBe(theme);

    // Verify persistence hook ran.
    const stored = await page.evaluate(() => localStorage.getItem('mcpproxy-theme'));
    expect(stored, `theme not persisted for ${theme}`).toBe(theme);

    await page.screenshot({ path: `${SHOT_DIR}/uc2-theme-${theme}.png`, fullPage: false });
  }

  flushConsole('UC2-theme', cap);
  assertNoConsoleErrors(cap);
});
