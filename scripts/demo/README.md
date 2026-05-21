# Demo asset pipeline

Regenerates the README hero banner and the (web-UI-only) demo GIF. Run from repo root.

1. `scripts/demo/render-banner.sh`            # docs/social.html (+ docs/logo.svg) -> docs/social.png
2. Boot a demo mcpproxy and capture the web UI:
   ```
   cd scripts/demo
   ln -sfn ../../e2e/playwright/node_modules ./node_modules
   MCPPROXY_BASE_URL=http://127.0.0.1:18082 MCPPROXY_API_KEY=<key> \
     ./node_modules/.bin/playwright test --config=playwright.config.ts
   ```
   # produces /tmp/demo-webui/<test>/video.webm for the 4 beats
3. `scripts/demo/build-demo.sh`               # stitches the 4 web beats -> docs/demo.gif + docs/demo.webp

The macOS tray menu and native app are shown as **static screenshots** in the README
(`docs/screenshot-macos-tray.png`, `docs/screenshot-macos-activity.png`), not in the GIF.

All outputs are committed under docs/. social.png is also uploaded manually to
GitHub Settings -> Social preview (one-time, cannot be scripted).
