#!/usr/bin/env bash
# Render docs/social.html -> docs/social.png at 1280x640 via headless Chromium.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
CHROME="/Users/$(whoami)/Library/Caches/ms-playwright/chromium-1217/chrome-mac-arm64/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing"
[ -x "$CHROME" ] || { echo "Chromium 1217 not found at: $CHROME"; exit 1; }

"$CHROME" --headless=new --hide-scrollbars --force-device-scale-factor=2 \
  --window-size=1280,640 \
  --screenshot="$ROOT/docs/social.png" \
  --default-background-color=00000000 \
  "file://$ROOT/docs/social.html"

echo "Wrote $ROOT/docs/social.png"
