# Demo asset pipeline

Regenerates the README hero banner and demo GIF. Run from repo root.

1. `scripts/demo/render-banner.sh`            # docs/social.html -> docs/social.png
2. Follow `scripts/demo/capture-tray.md`      # produces /tmp/demo-tray/frame-*.png
3. `cd scripts/demo && ../../e2e/playwright/node_modules/.bin/playwright test capture-webui.spec.ts`
                                              # produces /tmp/demo-webui/*.webm
4. `scripts/demo/build-demo.sh`               # stitches -> docs/demo.gif + docs/demo.webp

All outputs are committed under docs/. social.png is also uploaded manually to
GitHub Settings -> Social preview (one-time, cannot be scripted).
