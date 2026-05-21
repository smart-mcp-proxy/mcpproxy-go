import { defineConfig } from '@playwright/test';

// Captures the live mcpproxy web UI as video for the README demo GIF.
// Point baseURL at a running mcpproxy; pass the API key via MCPPROXY_API_KEY.
export default defineConfig({
  testDir: '.',
  timeout: 60000,
  fullyParallel: false,
  workers: 1,
  retries: 0,
  // The @playwright/test runner manages contexts itself, so use `video` (not the
  // raw recordVideo context option) and collect the .webm files from outputDir.
  outputDir: '/tmp/demo-webui',
  use: {
    headless: true,
    viewport: { width: 1440, height: 900 },
    deviceScaleFactor: 2,
    baseURL: process.env.MCPPROXY_BASE_URL || 'http://127.0.0.1:8080',
    launchOptions: {
      executablePath:
        '/Users/user/Library/Caches/ms-playwright/chromium-1217/chrome-mac-arm64/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing',
    },
    video: { mode: 'on', size: { width: 1440, height: 900 } },
  },
});
