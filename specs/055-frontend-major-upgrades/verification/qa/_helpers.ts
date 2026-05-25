import { Page, expect } from '@playwright/test';
import * as fs from 'fs';
import * as path from 'path';

export const BASE = 'http://127.0.0.1:18093';
export const API = '?apikey=uitest';
export const QA_DIR = '/Users/user/repos/mcpproxy-go/specs/055-frontend-major-upgrades/verification/qa';
export const SHOT_DIR = QA_DIR; // screenshots live alongside specs
const CONSOLE_LOG = path.join(QA_DIR, 'console-log.txt');

export interface Captured {
  errors: string[];   // migration-relevant console errors + page errors (FAIL the test)
  benign: string[];   // known pre-existing noise (logged, does NOT fail)
  warnings: string[];
  all: string[];
}

// Known pre-existing console noise that is NOT a frontend-migration regression.
// The SSE/EventSource channel periodically logs connection errors regardless of
// the Tailwind/DaisyUI/Vite stack (it is browser-native + query-param auth). We
// record these in the log but do not fail SC-004 on them.
const BENIGN_PATTERNS = [
  /EventSource/i,
  /SSE/i,
  /connection closed/i,
];
function isBenign(line: string): boolean {
  return BENIGN_PATTERNS.some((re) => re.test(line));
}

// Attach console + pageerror listeners. Returns the live capture buffer.
export function captureConsole(page: Page, label: string): Captured {
  const cap: Captured = { errors: [], benign: [], warnings: [], all: [] };
  page.on('console', (msg) => {
    const t = msg.type();
    const line = `[${label}] ${t}: ${msg.text()}`;
    cap.all.push(line);
    if (t === 'error') {
      if (isBenign(line)) cap.benign.push(line);
      else cap.errors.push(line);
    } else if (t === 'warning') cap.warnings.push(line);
  });
  page.on('pageerror', (err) => {
    const line = `[${label}] pageerror: ${err.message}`;
    cap.all.push(line);
    if (isBenign(line)) cap.benign.push(line);
    else cap.errors.push(line);
  });
  return cap;
}

// Append this test's collected console output to the shared log file.
export function flushConsole(label: string, cap: Captured) {
  const header = `\n===== ${label} =====\n` +
    `fatal-errors=${cap.errors.length} benign(SSE)=${cap.benign.length} warnings=${cap.warnings.length}\n`;
  const body = cap.all.length ? cap.all.join('\n') + '\n' : '(no console output)\n';
  fs.appendFileSync(CONSOLE_LOG, header + body);
}

// SC-004 gate: fail if any console error or page error occurred.
export function assertNoConsoleErrors(cap: Captured) {
  expect(cap.errors, `Console/page errors detected:\n${cap.errors.join('\n')}`).toEqual([]);
}

// On a fresh instance the OnboardingWizard modal auto-opens. Its open <dialog>
// has a full-viewport modal-backdrop that intercepts clicks on the page beneath
// (expected daisyUI v5 behavior for an OPEN modal). Dismiss it so the rest of
// the UI is interactable. This is first-run UX, not a migration regression.
export async function dismissOnboarding(page: Page) {
  const close = page.locator('dialog.modal[open] button[aria-label="Close"]').first();
  if (await close.count()) {
    try {
      await close.click({ timeout: 2000 });
      await page.waitForTimeout(300);
    } catch { /* already gone */ }
  }
}

export async function goto(page: Page, route: string) {
  const url = `${BASE}/ui${route}${API}`;
  await page.goto(url, { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(800);
  await dismissOnboarding(page);
}
