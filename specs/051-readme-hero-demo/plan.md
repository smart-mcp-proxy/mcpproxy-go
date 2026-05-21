# README Hero Banner + Demo GIF — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the top of `README.md` with a CodexBar-style designed hero banner plus one cohesive ~16s demo GIF (tray menu → web-UI server cards → activity log), produced by a scripted, re-runnable pipeline.

**Architecture:** A checked-in `docs/social.html` is rendered to `docs/social.png` via headless Chromium. The demo GIF is assembled from a macOS-tray still-frame montage (captured via the `mcpproxy-ui-test` MCP) and two real-motion web-UI video segments (captured via Playwright `recordVideo`), stitched with ffmpeg into `docs/demo.gif`. A `scripts/demo/` folder holds all generators so assets can be regenerated, not hand-made once.

**Tech Stack:** HTML/CSS (banner), headless Chromium (existing Playwright/Chromium at `e2e/playwright/node_modules`, pinned 1217), Playwright/TypeScript (web-UI capture), `mcpproxy-ui-test` MCP (tray capture), ffmpeg (stitch/encode), bash (orchestration).

**Prerequisites (verify before Task 1):**
- `ffmpeg` available: `which ffmpeg` (install via `brew install ffmpeg` if missing).
- Built `mcpproxy` binary at repo root (`make build`).
- The `mcpproxy-ui-test` MCP server built per CLAUDE.md (`/tmp/mcpproxy-ui-test`) and the MCPProxy tray `.app` running, for Task 5 only.

---

## File Structure

| File | Responsibility |
|------|----------------|
| `docs/social.html` | The 1280×640 banner source (frosted-tiles design) |
| `docs/social.png` | Rendered banner (committed; also GitHub social preview) |
| `docs/demo.gif` | Final stitched demo (committed, README embed) |
| `docs/demo.webp` | Optional smaller variant (committed) |
| `scripts/demo/render-banner.sh` | social.html → social.png via headless Chromium |
| `scripts/demo/capture-tray.md` | UI-test MCP shot-list for the tray montage |
| `scripts/demo/capture-webui.spec.ts` | Playwright spec: boot throwaway mcpproxy, seed demo servers + a flagged activity, record 2 web-UI segments |
| `scripts/demo/playwright.config.ts` | Pins Chromium 1217, viewport, recordVideo dir |
| `scripts/demo/build-demo.sh` | ffmpeg: tray montage + 2 webm → demo.gif + demo.webp |
| `scripts/demo/README.md` | How to regenerate every asset (run order) |
| `README.md` | Hero rewrite (lines ~1–25) |

---

## Task 1: Scaffold `scripts/demo/` and verify ffmpeg

**Files:**
- Create: `scripts/demo/README.md`

- [ ] **Step 1: Verify ffmpeg present**

Run: `which ffmpeg && ffmpeg -version | head -1`
Expected: a path and a version line. If missing: `brew install ffmpeg`, then re-run.

- [ ] **Step 2: Create the folder + run-order doc**

Create `scripts/demo/README.md`:

```markdown
# Demo asset pipeline

Regenerates the README hero banner and demo GIF. Run from repo root.

1. `scripts/demo/render-banner.sh`            # docs/social.html -> docs/social.png
2. Follow `scripts/demo/capture-tray.md`      # produces /tmp/demo-tray/frame-*.png
3. `cd scripts/demo && ../../e2e/playwright/node_modules/.bin/playwright test capture-webui.spec.ts`
                                              # produces /tmp/demo-webui/*.webm
4. `scripts/demo/build-demo.sh`               # stitches -> docs/demo.gif + docs/demo.webp

All outputs are committed under docs/. social.png is also uploaded manually to
GitHub Settings -> Social preview (one-time, cannot be scripted).
```

- [ ] **Step 3: Commit**

```bash
git add scripts/demo/README.md
git commit -m "docs(051): scaffold demo asset pipeline folder"
```

---

## Task 2: Build the hero banner source (`docs/social.html`)

**Files:**
- Create: `docs/social.html`

- [ ] **Step 1: Write the banner HTML**

Create `docs/social.html` (1280×640, frosted-tiles design, brand palette):

```html
<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<style>
  * { margin:0; padding:0; box-sizing:border-box; }
  html,body { width:1280px; height:640px; overflow:hidden; }
  body {
    font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;
    background:#0f172a; position:relative; color:#fff;
    display:flex; flex-direction:column; align-items:center; justify-content:center;
  }
  /* aurora blobs */
  .blob { position:absolute; border-radius:50%; filter:blur(90px); opacity:.55; }
  .b1 { width:520px; height:520px; left:-80px; top:-120px; background:#3b82f6; }
  .b2 { width:560px; height:560px; right:-120px; bottom:-160px; background:#006644; opacity:.6; }
  .b3 { width:360px; height:360px; left:60%; top:-140px; background:#60a5fa; opacity:.4; }
  /* faint dot grid with radial mask */
  .grid {
    position:absolute; inset:0;
    background-image:radial-gradient(rgba(255,255,255,.08) 1px, transparent 1px);
    background-size:26px 26px;
    -webkit-mask:radial-gradient(circle at 50% 45%, #000 0%, transparent 72%);
            mask:radial-gradient(circle at 50% 45%, #000 0%, transparent 72%);
  }
  .stack { position:relative; text-align:center; z-index:2; }
  .brandrow { display:flex; align-items:center; justify-content:center; gap:18px; margin-bottom:14px; }
  .logo { width:56px; height:56px; border-radius:14px;
          background:linear-gradient(135deg,#3b82f6,#006644);
          display:flex; align-items:center; justify-content:center; font-size:30px; }
  .name { font-size:30px; font-weight:800; letter-spacing:.5px; }
  h1 { font-size:64px; font-weight:800; line-height:1.05; letter-spacing:-1px;
       background:linear-gradient(90deg,#93c5fd 0%,#ffffff 45%,#86efac 100%);
       -webkit-background-clip:text; background-clip:text; color:transparent; }
  .sub { font-size:22px; color:#94a3b8; margin-top:16px; }
  .tiles { display:flex; gap:16px; justify-content:center; margin-top:40px; }
  .tile { font-size:20px; color:#e2e8f0; padding:14px 22px; border-radius:14px;
          background:rgba(255,255,255,.06); border:1px solid rgba(255,255,255,.12);
          -webkit-backdrop-filter:blur(8px); backdrop-filter:blur(8px); }
  .url { position:absolute; bottom:34px; font-size:18px; color:#64748b; z-index:2; }
</style>
</head>
<body>
  <div class="blob b1"></div><div class="blob b2"></div><div class="blob b3"></div>
  <div class="grid"></div>
  <div class="stack">
    <div class="brandrow"><div class="logo">🛡️</div><div class="name">MCPProxy</div></div>
    <h1>Supercharge AI Agents, Safely</h1>
    <div class="sub">One safe endpoint in front of every MCP server.</div>
    <div class="tiles">
      <div class="tile">🔍 Discover</div>
      <div class="tile">🛡️ Quarantine</div>
      <div class="tile">🔗 Federate</div>
      <div class="tile">⚡ −99% tokens</div>
    </div>
  </div>
  <div class="url">mcpproxy.app</div>
</body>
</html>
```

- [ ] **Step 2: Eyeball it in a browser**

Run: `open docs/social.html`
Expected: navy banner, gradient-clipped headline "Supercharge AI Agents, Safely", four frosted pills, aurora glow. Window is 1280×640.

- [ ] **Step 3: Commit**

```bash
git add docs/social.html
git commit -m "feat(051): add hero banner source (frosted-tiles social.html)"
```

---

## Task 3: Render banner → `docs/social.png`

**Files:**
- Create: `scripts/demo/render-banner.sh`
- Create (output): `docs/social.png`

- [ ] **Step 1: Write the render script**

Create `scripts/demo/render-banner.sh`:

```bash
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
```

- [ ] **Step 2: Make executable and run**

Run:
```bash
chmod +x scripts/demo/render-banner.sh && scripts/demo/render-banner.sh
```
Expected: "Wrote .../docs/social.png".

- [ ] **Step 3: Verify dimensions (retina → 2560×1280)**

Run: `sips -g pixelWidth -g pixelHeight docs/social.png`
Expected: pixelWidth 2560, pixelHeight 1280 (2× scale of 1280×640). If 1280×640, the `--force-device-scale-factor=2` flag was ignored — acceptable but note it.

- [ ] **Step 4: Commit**

```bash
git add scripts/demo/render-banner.sh docs/social.png
git commit -m "feat(051): render social.png banner via headless chromium"
```

---

## Task 4: Write the tray capture shot-list

**Files:**
- Create: `scripts/demo/capture-tray.md`

- [ ] **Step 1: Write the shot-list**

Create `scripts/demo/capture-tray.md`:

```markdown
# Tray capture shot-list (mcpproxy-ui-test MCP)

Goal: ~12 still frames of the macOS tray menu showing upstream servers + health,
to be assembled into a ~5s montage (beat 1 of the demo GIF).

Prereq: MCPProxy tray .app running with 3-4 healthy demo servers configured;
`mcpproxy-ui-test` MCP connected (bundle id com.smartmcpproxy.mcpproxy.dev).

Capture into /tmp/demo-tray/ as frame-01.png, frame-02.png, ... in this order:

1. frame-01..03: `screenshot_status_bar_menu` — closed → menu opening (3 shots)
2. frame-04..06: `list_menu_items` then `screenshot_status_bar_menu` with the
   "Upstream Servers" submenu expanded (servers + green health dots)
3. frame-07..09: hover/select a single server submenu (its tools count, status)
4. frame-10..12: `screenshot_status_bar_menu` returning to the top menu (close)

Naming MUST be zero-padded frame-NN.png so ffmpeg globbing is ordered.
mkdir -p /tmp/demo-tray before capturing.
```

- [ ] **Step 2: Commit**

```bash
git add scripts/demo/capture-tray.md
git commit -m "docs(051): add tray capture shot-list for demo montage"
```

---

## Task 5: Capture tray frames (manual, via UI-test MCP)

**Files:**
- Create (output, NOT committed): `/tmp/demo-tray/frame-*.png`

> This task runs the `mcpproxy-ui-test` MCP tools. It is interactive and macOS-only.
> The frames are scratch input to Task 7; they are not committed.

- [ ] **Step 1: Prep demo config + start tray**

Ensure 3–4 demo servers exist in the tray's config (e.g. `github`, `filesystem`,
`fetch`, `time`) and the `.app` is running per CLAUDE.md tray build steps.

Run: `mkdir -p /tmp/demo-tray`

- [ ] **Step 2: Capture frames per the shot-list**

Use the MCP tools in this order, saving each returned screenshot to
`/tmp/demo-tray/frame-NN.png`:
- `check_accessibility` (sanity)
- `screenshot_status_bar_menu` ×3 (frames 01–03)
- `list_menu_items` then `screenshot_status_bar_menu` ×3 (frames 04–06)
- `click_menu_item` into a server, `screenshot_status_bar_menu` ×3 (frames 07–09)
- `screenshot_status_bar_menu` ×3 (frames 10–12)

- [ ] **Step 3: Verify frame set**

Run: `ls /tmp/demo-tray/frame-*.png | wc -l`
Expected: 12 (or however many the shot-list produced; ≥8 minimum).

No commit (scratch files).

---

## Task 6: Capture web-UI video via Playwright

**Files:**
- Create: `scripts/demo/playwright.config.ts`
- Create: `scripts/demo/capture-webui.spec.ts`
- Create (output, NOT committed): `/tmp/demo-webui/*.webm`

- [ ] **Step 1: Write the Playwright config**

Create `scripts/demo/playwright.config.ts`:

```ts
import { defineConfig } from '@playwright/test';
export default defineConfig({
  testDir: '.',
  timeout: 60000,
  fullyParallel: false,
  workers: 1,
  retries: 0,
  use: {
    headless: true,
    viewport: { width: 1280, height: 800 },
    baseURL: 'http://127.0.0.1:18082',
    launchOptions: {
      executablePath: '/Users/user/Library/Caches/ms-playwright/chromium-1217/chrome-mac-arm64/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing',
    },
    recordVideo: { dir: '/tmp/demo-webui', size: { width: 1280, height: 800 } },
  },
});
```

- [ ] **Step 2: Write the capture spec**

Create `scripts/demo/capture-webui.spec.ts`. It assumes a throwaway mcpproxy is
already running on :18082 with demo servers + at least one flagged activity (Step 3
boots it). The spec records two videos: server cards, then activity log.

```ts
import { test, expect } from '@playwright/test';

const KEY = 'uitest';

test('server cards walkthrough', async ({ page }) => {
  await page.goto(`/ui/?apikey=${KEY}`);
  await page.waitForLoadState('domcontentloaded');
  // Dashboard with server cards
  await page.locator('[data-test="server-card"]').first().waitFor({ timeout: 15000 });
  await page.waitForTimeout(1500);                       // let cards settle (green health)
  await page.locator('[data-test="server-card"]').first().hover();
  await page.waitForTimeout(1000);
  await page.locator('[data-test="server-card"]').first().click(); // into server detail
  await page.waitForLoadState('domcontentloaded');
  await page.waitForTimeout(2500);                       // dwell on detail
});

test('activity log walkthrough', async ({ page }) => {
  await page.goto(`/ui/activity?apikey=${KEY}`);
  await page.waitForLoadState('domcontentloaded');
  await page.locator('[data-test="activity-row"]').first().waitFor({ timeout: 15000 });
  await page.waitForTimeout(1500);
  // expand the flagged (sensitive-data) row
  const flagged = page.locator('[data-test="activity-row"]').filter({ hasText: /flag|sensitive|critical/i }).first();
  await (await flagged.count() ? flagged : page.locator('[data-test="activity-row"]').first()).click();
  await page.waitForTimeout(2500);                       // dwell on detail
});
```

> NOTE on selectors: verify `data-test="server-card"`, `data-test="activity-row"`
> exist in `frontend/src/`. If a selector is missing, grep the relevant Vue view for
> the real `data-test` attribute and update the spec — do NOT invent one. If activity
> rows lack `data-test`, add it to the component in this task (follow the project's
> existing `data-test` convention) and rebuild the frontend with `make build`.

- [ ] **Step 3: Boot a throwaway mcpproxy with demo data**

Run (adapts the CLAUDE.md UI-test pattern; seeds 4 servers):
```bash
pkill -f 'mcpproxy serve.*18082' 2>/dev/null; sleep 1
rm -rf /tmp/mcpproxy-demo/{config.db,index.bleve,logs} 2>/dev/null; mkdir -p /tmp/mcpproxy-demo
cat > /tmp/mcpproxy-demo/mcp_config.json <<'EOF'
{ "listen":"127.0.0.1:18082","data_dir":"/tmp/mcpproxy-demo","api_key":"uitest",
  "enable_web_ui":true,"enable_socket":false,"telemetry":{"enabled":false},
  "mcpServers":[
    {"name":"github","command":"npx","args":["-y","@modelcontextprotocol/server-github"],"protocol":"stdio","enabled":true},
    {"name":"filesystem","command":"npx","args":["-y","@modelcontextprotocol/server-filesystem","/tmp"],"protocol":"stdio","enabled":true},
    {"name":"fetch","command":"uvx","args":["mcp-server-fetch"],"protocol":"stdio","enabled":true},
    {"name":"time","command":"uvx","args":["mcp-server-time"],"protocol":"stdio","enabled":true}
  ] }
EOF
./mcpproxy serve --config=/tmp/mcpproxy-demo/mcp_config.json --listen=127.0.0.1:18082 --log-level=info > /tmp/mcpproxy-demo/server.log 2>&1 &
until curl -sf -H "X-API-Key: uitest" http://127.0.0.1:18082/api/v1/status >/dev/null; do sleep 1; done
echo "mcpproxy demo up on :18082"
```

To guarantee a flagged activity row exists, make one tool call carrying a fake
secret (detected by the sensitive-data scanner):
```bash
curl -s -H "X-API-Key: uitest" -X POST http://127.0.0.1:18082/api/v1/... # see note
```
> NOTE: if no simple REST path triggers a tool call, instead use the CLI:
> `./mcpproxy call_tool_read ...` is not guaranteed; simplest reliable path is to let
> the activity log show normal `tool_call` rows and drop the `/flag/` filter in the
> spec (it already falls back to the first row). Flagged-row is best-effort.

- [ ] **Step 4: Run the capture**

Run:
```bash
cd scripts/demo
ln -sfn /Users/user/repos/mcpproxy-go/e2e/playwright/node_modules ./node_modules
./node_modules/.bin/playwright test --config=playwright.config.ts --reporter=list
```
Expected: 2 passed. Two `.webm` files appear in `/tmp/demo-webui/`.

- [ ] **Step 5: Verify videos**

Run: `ls -la /tmp/demo-webui/*.webm && echo "count: $(ls /tmp/demo-webui/*.webm | wc -l)"`
Expected: 2 webm files, each non-zero size.

- [ ] **Step 6: Commit the specs (not the videos)**

```bash
git add scripts/demo/playwright.config.ts scripts/demo/capture-webui.spec.ts
git commit -m "feat(051): playwright web-ui capture for demo (server cards + activity)"
```

---

## Task 7: Stitch the demo GIF (`build-demo.sh`)

**Files:**
- Create: `scripts/demo/build-demo.sh`
- Create (output): `docs/demo.gif`, `docs/demo.webp`

- [ ] **Step 1: Write the build script**

Create `scripts/demo/build-demo.sh`:

```bash
#!/usr/bin/env bash
# Stitch tray montage (stills) + 2 web-UI webm segments -> docs/demo.gif + .webp
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
TRAY=/tmp/demo-tray
WEB=/tmp/demo-webui
WORK=$(mktemp -d)
W=900; FPS=15

[ -d "$TRAY" ] || { echo "missing $TRAY (run Task 5)"; exit 1; }
ls "$WEB"/*.webm >/dev/null 2>&1 || { echo "missing $WEB/*.webm (run Task 6)"; exit 1; }

# 1) tray montage: each still held 0.42s, gentle crossfade via framerate ramp
ffmpeg -y -framerate 1/0.42 -pattern_type glob -i "$TRAY/frame-*.png" \
  -vf "scale=${W}:-2:flags=lanczos,fps=${FPS}" -pix_fmt yuv420p "$WORK/seg0.mp4"

# 2) normalize the two web-UI segments to same width/fps (ordered: cards, then activity)
i=1
for v in $(ls "$WEB"/*.webm | sort); do
  ffmpeg -y -i "$v" -vf "scale=${W}:-2:flags=lanczos,fps=${FPS}" -pix_fmt yuv420p "$WORK/seg${i}.mp4"
  i=$((i+1))
done

# 3) concat
: > "$WORK/list.txt"
for f in "$WORK"/seg0.mp4 "$WORK"/seg1.mp4 "$WORK"/seg2.mp4; do echo "file '$f'" >> "$WORK/list.txt"; done
ffmpeg -y -f concat -safe 0 -i "$WORK/list.txt" -c copy "$WORK/full.mp4"

# 4) palette-optimized GIF
ffmpeg -y -i "$WORK/full.mp4" -vf "fps=${FPS},scale=${W}:-2:flags=lanczos,palettegen=stats_mode=diff" "$WORK/pal.png"
ffmpeg -y -i "$WORK/full.mp4" -i "$WORK/pal.png" \
  -lavfi "fps=${FPS},scale=${W}:-2:flags=lanczos,paletteuse=dither=bayer:bayer_scale=3" "$ROOT/docs/demo.gif"

# 5) webp (smaller, also autoplays in README)
ffmpeg -y -i "$WORK/full.mp4" -vcodec libwebp -filter:v "fps=${FPS},scale=${W}:-2" \
  -lossless 0 -compression_level 6 -q:v 55 -loop 0 -an "$ROOT/docs/demo.webp"

echo "Wrote docs/demo.gif ($(du -h "$ROOT/docs/demo.gif" | cut -f1)) and docs/demo.webp ($(du -h "$ROOT/docs/demo.webp" | cut -f1))"
rm -rf "$WORK"
```

- [ ] **Step 2: Run it**

Run: `chmod +x scripts/demo/build-demo.sh && scripts/demo/build-demo.sh`
Expected: "Wrote docs/demo.gif (...) and docs/demo.webp (...)".

- [ ] **Step 3: Verify size + dimensions budget**

Run:
```bash
GIFKB=$(du -k docs/demo.gif | cut -f1); echo "gif KB: $GIFKB"
sips -g pixelWidth docs/demo.gif
test "$GIFKB" -lt 8192 && echo "OK under 8MB" || echo "TOO BIG — lower W to 760 or FPS to 12 in build-demo.sh and re-run"
```
Expected: width 900, "OK under 8MB". If too big, reduce `W` or `FPS` and re-run Step 2.

- [ ] **Step 4: Eyeball the GIF**

Run: `open docs/demo.gif`
Expected: tray menu beat → server cards beat → activity log beat, ~16s, loops.

- [ ] **Step 5: Commit**

```bash
git add scripts/demo/build-demo.sh docs/demo.gif docs/demo.webp
git commit -m "feat(051): build stitched demo gif (tray + web-ui)"
```

---

## Task 8: Rewrite the README hero

**Files:**
- Modify: `README.md:1-25`

- [ ] **Step 1: Read the current top of README**

Run: `sed -n '1,26p' README.md`
Confirm lines 1–25 match the block being replaced (H1, doc link, old video comment,
YouTube thumbnail, the two `mcpproxy.app/images/menu_*.png` `<div>` block).

- [ ] **Step 2: Replace lines 1–25 with the new hero**

Replace from `# MCPProxy – Smart Proxy for AI Agents` through the closing `</div>` of
the tray-thumbnail block (the `<em>` caption line) with:

```markdown
# 🛡️ MCPProxy — Supercharge AI Agents, Safely

> One safe endpoint in front of every MCP server.

[![Release](https://img.shields.io/github/v/release/smart-mcp-proxy/mcpproxy-go?sort=semver)](https://github.com/smart-mcp-proxy/mcpproxy-go/releases)
[![Build](https://github.com/smart-mcp-proxy/mcpproxy-go/actions/workflows/unit-tests.yml/badge.svg)](https://github.com/smart-mcp-proxy/mcpproxy-go/actions/workflows/unit-tests.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/smart-mcp-proxy/mcpproxy-go)](https://goreportcard.com/report/github.com/smart-mcp-proxy/mcpproxy-go)
[![Go Reference](https://pkg.go.dev/badge/github.com/smart-mcp-proxy/mcpproxy-go.svg)](https://pkg.go.dev/github.com/smart-mcp-proxy/mcpproxy-go)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![GitHub stars](https://img.shields.io/github/stars/smart-mcp-proxy/mcpproxy-go?style=social)](https://github.com/smart-mcp-proxy/mcpproxy-go/stargazers)

<a href="https://mcpproxy.app" target="_blank" rel="noopener">
  <img src="docs/social.png" alt="MCPProxy — Supercharge AI Agents, Safely" width="100%" />
</a>

<p align="center">
  <img src="docs/demo.gif" alt="MCPProxy demo: tray menu, server cards, and activity log" width="900" />
</p>

<p align="center">
  <strong>📺 <a href="https://youtu.be/2aKrgJnbbcw">Watch the full walkthrough</a></strong> &nbsp;·&nbsp;
  <strong>📚 <a href="https://docs.mcpproxy.app/">Read the docs</a></strong> &nbsp;·&nbsp;
  <strong>🌐 <a href="https://mcpproxy.app">mcpproxy.app</a></strong>
</p>
```

- [ ] **Step 3: Verify nothing below "Why MCPProxy?" changed**

Run: `grep -n '## Why MCPProxy?' README.md`
Expected: exactly one match; content after it unchanged. Also:
Run: `grep -c 'mcpproxy.app/images/menu_' README.md`
Expected: `0` (old thumbnails removed).

- [ ] **Step 4: Verify asset links resolve locally**

Run: `ls docs/social.png docs/demo.gif`
Expected: both exist (relative paths in README will resolve on github.com).

- [ ] **Step 5: Commit**

```bash
git add README.md
git commit -m "docs(051): new README hero — banner + demo gif"
```

---

## Task 9: Final verification + handoff

**Files:** none (verification only)

- [ ] **Step 1: Confirm all committed assets present**

Run: `git ls-files docs/social.html docs/social.png docs/demo.gif docs/demo.webp scripts/demo/`
Expected: all listed, including the 5 `scripts/demo/` files.

- [ ] **Step 2: Render README locally to sanity-check markdown**

Run: `grep -n 'docs/social.png\|docs/demo.gif' README.md`
Expected: banner `<img src="docs/social.png">` and `<img src="docs/demo.gif">` present.

- [ ] **Step 3: Push branch + open PR**

```bash
git push -u origin feat/051-readme-hero-demo
gh pr create --base main --title "docs(051): README hero banner + demo GIF" \
  --body "Implements specs/051-readme-hero-demo. Adds frosted-tiles hero banner (docs/social.html -> docs/social.png), one cohesive demo GIF (tray + web UI, docs/demo.gif), and the scripted scripts/demo/ pipeline. README hero rewritten. Follow-up: upload docs/social.png to Settings -> Social preview."
```

- [ ] **Step 4: Remind user of the one manual step**

Print: "Upload `docs/social.png` to GitHub → Settings → Social preview to control
HN/Slack/X unfurls. This is the only step that cannot be scripted."

---

## Self-review notes (author)

- **Spec coverage:** banner (T2–3) ✓, demo GIF tray+webUI (T4–7) ✓, README hero with
  remove/demote/keep rules (T8) ✓, scripted pipeline (T1,3,4,6,7 + scripts/demo/README) ✓,
  social-preview reminder (T9) ✓, keep existing screenshots (untouched by T8) ✓.
- **Known soft spots flagged inline, not hidden:** (a) `data-test` selectors in the
  Playwright spec must be verified against `frontend/src/` — instruction says grep, do
  not invent; (b) the flagged-activity row is best-effort with a first-row fallback;
  (c) retina scale factor may be ignored by headless Chromium — acceptable.
- **Non-TDD task types:** asset generation uses verification steps (dimensions, file
  size, visual eyeball) in place of unit tests, which is the correct analog here.
