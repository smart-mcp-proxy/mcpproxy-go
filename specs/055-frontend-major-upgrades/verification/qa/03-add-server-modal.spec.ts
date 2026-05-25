import { test, expect } from '@playwright/test';
import { goto, captureConsole, flushConsole, assertNoConsoleErrors, SHOT_DIR } from './_helpers';

// UC3 — AddServerModal: open the <dialog class="modal">, assert dialog.open,
// fill server name + a stdio command, assert the form accepts input.
test('UC3 add-server modal — opens, accepts name + stdio command', async ({ page }) => {
  const cap = captureConsole(page, 'UC3-add-server');
  await goto(page, '/');

  // TopHeader "Add Server" button toggles showAddServerModal=true.
  const addBtn = page.locator('button.btn-primary', { hasText: /Add Server/i }).first();
  await expect(addBtn, 'Add Server button not found').toBeVisible();
  await addBtn.click();
  await page.waitForTimeout(400);

  // The modal is a native <dialog class="modal"> bound with :open="show".
  // Several modals exist in the DOM (daisyUI v5 keeps closed <dialog>s mounted
  // with display:grid + pointer-events:none); target the OPEN one.
  const dialog = page.locator('dialog.modal[open]');
  await expect(dialog).toBeVisible();
  const isOpen = await dialog.evaluate((d: HTMLDialogElement) => d.open);
  expect(isOpen, 'dialog.open should be true').toBeTruthy();

  // Fill server name.
  const nameInput = dialog.locator('input[placeholder*="github-server"]');
  await nameInput.fill('qa-test-server');
  expect(await nameInput.inputValue()).toBe('qa-test-server');

  // Default type is stdio. Pick the command from the select; choose "custom"
  // to reveal a free-text command field, then type a command.
  const cmdSelect = dialog.locator('select').first();
  await cmdSelect.selectOption('custom');
  await page.waitForTimeout(200);
  const customCmd = dialog.locator('input[placeholder*="/usr/local/bin"], input[placeholder*="command"], input[placeholder*="Custom"]').first();
  // Fall back to the second text input in the stdio section if placeholder differs.
  const customCmdResolved = (await customCmd.count()) ? customCmd
    : dialog.locator('input[type="text"]').nth(1);
  await customCmdResolved.fill('/usr/bin/my-mcp-server');
  expect(await customCmdResolved.inputValue()).toBe('/usr/bin/my-mcp-server');

  await page.screenshot({ path: `${SHOT_DIR}/uc3-add-server-modal.png`, fullPage: false });

  flushConsole('UC3-add-server', cap);
  assertNoConsoleErrors(cap);
});
