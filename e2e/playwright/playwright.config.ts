import { defineConfig, devices } from '@playwright/test';

/**
 * Playwright configuration for OAuth E2E tests.
 * These tests verify the browser-based OAuth login flow.
 */
export default defineConfig({
  testDir: '.',
  /* Run tests in files in parallel */
  fullyParallel: true,
  /* Fail the build on CI if you accidentally left test.only in the source code. */
  forbidOnly: !!process.env.CI,
  /* Retry on CI only */
  retries: process.env.CI ? 2 : 0,
  /* Opt out of parallel tests on CI. */
  workers: process.env.CI ? 1 : undefined,
  /* Reporter to use. See https://playwright.dev/docs/test-reporters */
  reporter: process.env.CI ? 'github' : 'html',
  /* Shared settings for all the projects below. */
  use: {
    /* Base URL for the OAuth test server - set by test setup */
    // baseURL: 'http://127.0.0.1:0',

    /* Collect trace when retrying the failed test. */
    trace: 'on-first-retry',

    /* Take screenshot on failure */
    screenshot: 'only-on-failure',
  },

  /* Configure projects for major browsers */
  projects: [
    {
      name: 'chromium',
      use: { ...devices['Desktop Chrome'] },
    },
  ],

  /* Timeout settings */
  timeout: 60000,
  expect: {
    timeout: 10000,
  },

  /* Global setup and teardown */
  globalTimeout: 300000, // 5 minutes for all tests
});
