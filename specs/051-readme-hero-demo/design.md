# Design — README hero revamp + demo asset pipeline

**Spec:** 051-readme-hero-demo
**Date:** 2026-05-21
**Status:** Approved (brainstorming) — pending implementation plan
**Owner:** @Dumbris

## Summary

Replace the top of the repo `README.md` with a CodexBar-style hero: a designed
full-width banner plus one cohesive animated demo GIF, in the project's
blue→green-on-navy brand palette. Build the assets through a scripted, re-runnable
pipeline so they can be regenerated rather than hand-crafted once.

This is Phase-1 item #5 from the OSS repo-hygiene work (PR #487 shipped the badge
row; this completes the hero/demo half that could not be auto-generated).

## Motivation

The repo underperforms the product visually. The README currently opens with a bare
YouTube thumbnail link and two small externally-hosted tray screenshots. CodexBar
(steipete) demonstrates the target: emoji H1 + tagline + flat badge row + a designed
banner + one tight product shot. We add motion (a GUI demo GIF) on top of that polish.

## Key constraint that shapes the design

- The **macOS UI-test MCP** (`mcpproxy-ui-test`) produces **still screenshots**, not
  video (`screenshot_window`, `screenshot_status_bar_menu`, `click_menu_item`, …).
- **Playwright** *can* record **real video** of the web UI (`recordVideo` on the
  browser context).

Therefore the "one cohesive GIF" is assembled from two source types:
true-motion web-UI video segments + a tray frame-montage (stills with synthetic
pans/cross-fades), stitched with ffmpeg. The whole thing is scriptable and CI-friendly.

## Decisions (locked during brainstorming)

| Topic | Decision |
|-------|----------|
| Hero treatment | Option A — designed banner (static), with a demo GIF directly beneath |
| Banner style | #2 "frosted tiles" |
| Headline | "Supercharge AI Agents, Safely" |
| Banner palette | navy `#0f172a`, blue `#3b82f6`/`#60a5fa`, green `#005533`/`#006644` |
| Banner pills | 🔍 Discover · 🛡️ Quarantine · 🔗 Federate · ⚡ −99% tokens |
| Demo structure | One cohesive ~16s GIF (not multiple clips) |
| Demo surfaces & order | tray menu → web-UI server cards → web-UI activity log |
| Recording | tray via UI-test MCP (frames); web UI via Playwright (video) |

## README top-of-page structure

Replaces current README lines ~1–25:

```
# 🛡️ MCPProxy — Supercharge AI Agents, Safely
> One safe endpoint in front of every MCP server.        (≤8-word tagline)
[ badge row — already shipped in #487, unchanged ]
[ FULL-WIDTH HERO BANNER ]   docs/social.png, wrapped in <a href="https://mcpproxy.app">
[ ONE COHESIVE DEMO GIF ]    docs/demo.gif
📺 Watch the full walkthrough → (existing YouTube link, demoted to one line)
## Why MCPProxy?             (keep existing bold-lead bullets)
```

- **Remove**: the two `mcpproxy.app/images/menu_*.png` thumbnails (superseded by GIF).
- **Demote**: bare YouTube thumbnail → a one-line text link under the GIF.
- **Keep**: everything from "Why MCPProxy?" downward, including the install/config
  sections and the existing local screenshots used in feature sections.

## Assets

### 1. Hero banner
- `docs/social.html` — checked-in 1280×640 canvas (frosted-tiles design above).
- `docs/social.png` — rendered from it via headless Chromium (reuse the
  Playwright/Chromium already installed at `e2e/playwright/node_modules`).
- Same PNG is uploaded to **GitHub Settings → Social preview** (manual, one-time).

### 2. Demo GIF — storyboard (~16s)
1. **Tray menu** (~5s) — open menubar menu, show upstream servers with live health,
   hover a server. Source: UI-test MCP frame sequence → montage.
2. **Server cards** (~6s) — web UI dashboard, healthy cards, click into a server
   detail. Source: Playwright `recordVideo` (real motion).
3. **Activity log** (~5s) — web UI activity stream; a tool call with a sensitive-data
   flag expands. Source: Playwright `recordVideo`.

Stitch: ffmpeg concat (tray montage + 2 webm segments) → palette-optimized
`docs/demo.gif` (≤900px wide, 15fps, target <8MB) and optional `docs/demo.webp`.

### 3. Existing static screenshots
Keep `docs/screenshots/tray-macos/*.png` (4 files) for lower feature sections. No
re-shoot.

## Production pipeline — `scripts/demo/`

| File | Purpose |
|------|---------|
| `render-banner.sh` | Headless-Chromium screenshot of `social.html` → `social.png` |
| `capture-tray.md` | UI-test MCP shot-list (menu items + order) for the tray montage |
| `capture-webui.spec.ts` | Playwright spec: boot throwaway mcpproxy (CLAUDE.md UI-test pattern), seed 3–4 demo servers, record the two web-UI segments |
| `build-demo.sh` | ffmpeg stitch → `demo.gif` + `demo.webp` |

The Playwright spec follows the existing UI-verification pattern documented in
CLAUDE.md (throwaway data-dir, pinned Chromium 1217, `domcontentloaded` not
`networkidle`, `data-test` selectors).

## Scope / non-goals

**In scope:** banner (`social.html` + `social.png`), one demo GIF, README hero
rewrite, scripted `scripts/demo/` pipeline, social-preview PNG.

**Out of scope (this round):** asciinema/terminal GIF; Diátaxis docs restructure;
website redesign. The glass-pill motif is available to reuse on the website later.

**Requires the user (cannot be scripted):** uploading `social.png` to GitHub
Settings → Social preview.

## Success criteria

- README opens with banner + autoplaying demo GIF; renders correctly on github.com.
- `demo.gif` ≤ 8MB, ≤ 900px wide, autoplays + loops inline.
- All four `scripts/demo/` artifacts run end-to-end and regenerate the committed assets.
- Social-preview PNG produces a correct unfurl on X/Slack/HN (manual verify).
