import { defineConfig } from '@playwright/test'

// Reuse the project's installed Chromium 1217 (e2e/playwright/node_modules).
// Symlink node_modules here, or run from a dir that has @playwright/test.
export default defineConfig({
  testDir: '.',
  timeout: 40000,
  fullyParallel: false,
  workers: 1,
  retries: 0,
  use: {
    headless: true,
    viewport: { width: 1440, height: 900 },
    launchOptions: {
      executablePath:
        process.env.PW_CHROMIUM ||
        '/Users/user/Library/Caches/ms-playwright/chromium-1217/chrome-mac-arm64/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing',
    },
  },
})
