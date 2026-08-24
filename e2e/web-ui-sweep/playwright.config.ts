import { defineConfig, devices } from '@playwright/test'

// Web UI sweep configuration (docs/development/web-ui-verification.md).
//
// The sweep drives the Web UI SERVED BY A REAL mcpproxy binary (embedded
// frontend, never a dev server), so everything here is parameterised by the
// environment the launcher (scripts/run-web-smoke.sh) exports:
//   MCPPROXY_BASE_URL  — the running instance, e.g. http://127.0.0.1:18080
//   MCPPROXY_API_KEY   — API key of that instance (Web UI accepts ?apikey=)
//   SWEEP_REPORT_DIR   — where the self-contained HTML report is written
//   PW_CHROMIUM        — optional explicit Chromium binary (local macOS runs)
const reportDir = process.env.SWEEP_REPORT_DIR || './playwright-report'

export default defineConfig({
  testDir: '.',
  timeout: 45_000,
  expect: { timeout: 15_000 },
  fullyParallel: false,
  workers: 1,
  forbidOnly: !!process.env.CI,
  // One retry: the sweep is advisory in CI, and a single flake should not cost
  // a maintainer an investigation. Genuine breakage fails both attempts.
  retries: process.env.CI ? 1 : 0,
  reporter: [
    ['list'],
    ['html', { outputFolder: reportDir, open: 'never' }],
  ],
  use: {
    ...devices['Desktop Chrome'],
    headless: true,
    viewport: { width: 1440, height: 900 },
    screenshot: 'only-on-failure',
    video: 'off',
    trace: 'retain-on-failure',
    ...(process.env.PW_CHROMIUM
      ? { launchOptions: { executablePath: process.env.PW_CHROMIUM } }
      : {}),
  },
})
