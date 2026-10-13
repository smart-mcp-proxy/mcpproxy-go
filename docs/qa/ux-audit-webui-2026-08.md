---
title: "Web UI UX Audit — August 2026"
sidebar_label: "Web UI UX Audit (2026-08)"
description: "Heuristic + live-instance UX audit of the MCPProxy Vue Web UI, with a ranked P0-P3 backlog."
---

# Web UI UX Audit — August 2026

**Scope:** every routed view of the personal-edition Web UI (`frontend/src/views/`), in light and dark
themes, in first-run (empty) and populated states, plus reachable error states.
**Build audited:** `v0.60.0`, `main` @ `20a3e50` (`make build`, embedded frontend).
**Method:** two isolated cores (`127.0.0.1:18412` populated, `127.0.0.1:18413` first-run) on scratch
`--data-dir` **and** `--config`; Playwright/Chromium 1440×900 (plus 820px and 390px passes); ~90
screenshots; programmatic WCAG contrast sampling (canvas-resolved computed colors); MCP traffic
generated over the real `/mcp` endpoint (reads, writes, destructive calls, failures, a sensitive-data
hit, repeated identical calls).
**Screenshots are not committed** — each finding names the state to reproduce.

> **Status — this is the audit record, not an open backlog.** Everything below describes **v0.60.0
> @ `20a3e50`** and is written in the present tense as audited. Eight PRs merged on 2026-08-25
> ([#1044](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/1044),
> [#1048](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/1048),
> [#1049](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/1049),
> [#1050](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/1050),
> [#1051](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/1051),
> [#1052](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/1052),
> [#1053](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/1053),
> [#1054](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/1054))
> close the backlog. **§ Resolution** at the end records what a live re-check on **v0.61.0** actually
> confirmed, what it could not reach, and five defects found on `main` that are not in this audit. Re-verify against
> current `main` before re-filing anything from here.

## Heuristics used

Standard: Nielsen's 10, information scent, empty/error/loading-state completeness, WCAG 2.1 AA
contrast and keyboard operation, responsive behaviour, cross-view data consistency.

House rules (from Algis's standing activity/tray UX preferences — treated as normative here):

| # | Rule |
|---|------|
| H1 | Group consecutive same-tool calls into runs |
| H2 | Surface `metadata.intent.reason` as the user-facing context line |
| H3 | Mark only failures — no success checkmarks |
| H4 | Activity belongs at the top level |
| H5 | Show recently-seen clients as *idle*, never an empty "no clients" |
| H6 | Native forms over deep links |
| H7 | Never merge ok + error into one run; split runs on status change |
| H8 | Summary/histogram must visibly match the call log |
| H9 | `code_execution` sub-calls are first-class, with parent ↔ child navigation |

---

## Executive summary

The Web UI is in good shape structurally — the sidebar's Workspace / Observability / System grouping
gives real information scent, the server-detail error card (code + plain-language cause + copyable
remediation + docs link) is best-in-class, and the Agent Tokens empty state is a model for the rest of
the product. Recent work has already landed the hard parts of H2 and H9: the Activity view *does*
render `intent.reason` and *does* carry `parent_id` linkage for `code_execution` sub-calls.

Three problems dominate, and all three sit directly under the epics this audit gates.

**1. The numbers disagree with each other.** Within seconds of each other, on the same 24-hour window,
the Activity Log header reported **70 calls · 6 errors** while the Dashboard's Usage tab reported
**42 tool calls · 6 errors · 14.3% error rate**, and the Activity filter panel's own tiles read
**Total 42 / Success 15 / Errors 4** — which do not sum. Per-call latency is 3–5 ms in the Activity
table and a flat 10 ms p50/p95 for every tool in Usage. This is H8 at product scale: it is the fastest
way to lose a user's trust in an observability surface, and `action-log-transparency` cannot be built
on top of it. (F1, F2, F22)

**2. The default landing page answers no question.** The Dashboard is a static hub diagram with
animated dots. The only real analytics — Calls per tool, Token sinks, Errors & latency, Activity over
time — live in a `v-show` tab that has no route, no sidebar entry, no deep link, and no bookmark. The
hero metric next to it ("32.4% tokens saved / 519 saved") did not move after 16 further tool calls.
This is exactly the gap `analytics-default-landing` exists to close. (F4, F23)

**3. First run leaks users at the empty state.** Given that ~48% of installs are one-and-done, the
Servers page — the page a new user lands on after closing the wizard — shows *"No servers found / No
servers available"* with **no call to action at all**, while the Tools and Agent Tokens pages
in the same build have exemplary empty states with buttons. The Secrets page goes further: with zero
secrets there is **no way to add one from the Web UI at all**. (F3, F8)

Alongside those: the Add Server modal's submit button sits 594px below the viewport and Escape does
not close it (F6); the security-critical "Docker isolation disabled" warning is the *least legible*
text on the Dashboard at **1.28:1** (F9); the sensitive-data drawer prints the AWS key it just flagged
in cleartext (F13); and at 390px the telemetry banner renders as a ~10-character-wide column ~440px
tall (F14).

**Counts:** 4 P0 · 12 P1 · 12 P2 · 8 P3 (36 findings).

**Suggested first slice (all S/M, unblocks the three downstream epics):**
F1 + F2 (one shared 24h aggregate behind both surfaces) → F4 + F23 (route `/usage`, put it in the
sidebar, consider it the default landing) → F3 + F8 (empty-state CTAs) → F5 + F7 (grouping + failure-only
status) → F9 (contrast tokens).

---

## P0 — trust and conversion

### F1. Activity Log and Usage report different totals for the same 24h window
- **View:** `Activity.vue` header; `Dashboard.vue` → Usage tab (`Usage.vue`)
- **Evidence:** Populated instance, both screens loaded within ~20s. Activity header: `70 calls · 6 errors`.
  Usage 24h tiles: `Tool calls 42`, `Errors 6`, `14.3% overall error rate`. Activity's own paginator
  agreed with 70 (`Showing 1-25 of 70`); Usage's "Activity over time" hour buckets summed to ~42.
  Root cause is visible in the data: Activity counts every `type` (including `security_scan` and
  `system_start`) as a "call"; Usage counts only `tool_call`.
- **Heuristic:** H8; Nielsen #4 consistency; #1 visibility of system status.
- **Fix:** One shared 24h aggregate, computed once server-side, consumed by Activity, Usage, tray and
  CLI. Until then, relabel Activity's header to `70 events · 6 errors` and add a `42 tool calls` sub-count.
- **Effort:** M

### F2. Activity's own filter tiles do not sum
- **View:** `Activity.vue` → Filters
- **Evidence:** Tiles read `Total (24h) 42 · Success 15 · Errors 4 · Blocked 0 · Rejected 0`.
  15 + 4 + 0 + 0 = 19, not 42. Simultaneously the header above said `42 calls`. On a later pass the
  header said `70 calls · 6 errors` while the tiles still used a different denominator.
- **Heuristic:** H8; #1 visibility of system status.
- **Fix:** Make the tiles a partition of one denominator and label it (`42 tool calls in the last 24h`);
  add an `Other / internal` bucket for whatever is currently unaccounted. Add a unit test asserting the
  buckets sum to the total.
- **Effort:** S

### F3. First-run Servers page has no call to action
- **View:** `Servers.vue`, first-run instance, zero servers
- **Evidence:** Empty state renders an icon plus *"No servers found"* / *"No servers available"* —
  a restatement, no button, no link. The same build's `Tools.vue` empty state offers **Manage Servers**
  and `AgentTokens.vue` offers **Create Your First Token** with a one-line explanation. Above the empty
  state sit four zero-valued stat tiles and four zero-count filter pills (`All (0) Connected (0)
  Enabled (0) Quarantined (0)`) plus a "Search servers…" box with nothing to search.
- **Heuristic:** Empty-state completeness; #6 recognition over recall; #10 help. Highest-leverage screen
  for the ~48% one-and-done cohort.
- **Fix:** Replace with the AgentTokens pattern: headline, one line of why, and two buttons —
  **Add Server** (primary) and **Browse Registry** (secondary) — plus a third quiet link, *Import from
  your AI client configs*. Hide the stat row and filter pills at zero.
- **Effort:** S

### F4. The default landing page carries no analytics, and the analytics that exist are unroutable
- **View:** `Dashboard.vue` (Overview) / `Usage.vue`
- **Evidence:** Overview is a hub diagram with four animated SVG dots on a 20s loop, a clients box, a
  servers box and six buttons; the only quantitative content is a floating `32.4% tokens saved` chip.
  All real analytics live under the Usage tab, which is `v-show`-toggled inside Dashboard — `Usage.vue`
  has **no entry in `router/index.ts`** and no sidebar item, so it cannot be linked, bookmarked, or
  returned to after a reload.
- **Heuristic:** #1 visibility of system status; information scent; H4 (observability at the top level).
- **Fix:** Give Usage a real route (`/usage`) and a sidebar entry under Observability; make the
  Dashboard's default panel a compact answer to "what happened and what needs me" (24h calls/errors
  sparkline, top tools, servers needing attention, recent failures) with the hub diagram demoted.
- **Effort:** M (route + nav is S; landing redesign is M)

---

## P1 — significant friction on a primary path

### F5. Activity does not group repeated identical calls, and stamps "Success" on every row
- **View:** `Activity.vue` table
- **Evidence:** 12 consecutive `everything:echo` calls with an identical reason produced 12 separate
  rows, each repeating the same 3-line absolute timestamp, the same `📖 Polling the echo endpoint while
  the user …`, and the word `Success`. On one 900px screen: 11 × "Success", 1 × "Error". Real logs are
  far worse — Algis's own export was 1479 calls → 809 runs, with runs up to 100×.
- **Heuristic:** H1, H3, H7; #8 aesthetic and minimalist design.
- **Fix:** Collapse consecutive same-tool + same-status rows into a run (`echo ×12`, expandable),
  splitting on status change so a run never mixes ok and error. Show a status chip only for
  failed/blocked/rejected rows; leave successes blank.
- **Effort:** M

### F6. Add Server modal: submit is below the fold, Escape does not close it, focus never enters the dialog
- **View:** `AddServerModal.vue` from `Servers.vue` / header **+ Add Server**
- **Evidence:** At 1440×900 the modal measured `scrollHeight 1494` vs `clientHeight 900` — 594px of the
  form, including the submit/cancel row, is below the fold with no sticky footer and no visible close
  affordance in the header. After opening, `document.activeElement` was still the triggering
  `button.btn.btn-primary`; pressing Escape left `.modal-box` in the DOM.
- **Heuristic:** #3 user control and freedom; WCAG 2.1.1 keyboard; ARIA dialog pattern; H6.
- **Fix:** Sticky footer with the primary action; scroll the body only; move focus to the first field on
  open; close on Escape and on backdrop click; add a header ✕; restore focus to the trigger on close.
- **Effort:** S

### F7. A quarantined server card offers no way to review it
- **View:** `Servers.vue` card for a quarantined server; `ServerCard.vue`
- **Evidence:** The card shows a yellow *"Server is quarantined"* banner and the actions
  **Enable · Details · Delete** (with a greyed **Scan**, disabled without explanation). Nothing on the
  card leads to the tool-approval review, even though the stat tile above says
  `Quarantined 1 — Need security review`.
- **Heuristic:** #7 flexibility and efficiency; the page states a required action and doesn't afford it.
- **Fix:** Make **Review** the primary action on a quarantined card (deep-link to the server's Security
  tab / pending-tools list); demote Enable; add a `title` on the disabled Scan explaining why.
- **Effort:** S

### F8. Secrets page cannot add a secret
- **View:** `Secrets.vue`
- **Evidence:** Empty state is *"No secrets found / No secrets available"* with no CTA. `AddSecretModal`
  exists, but `showAddSecretModal()` is only reachable from a per-row **Set** button that renders when a
  secret is *referenced in config but not yet set* (`Secrets.vue:138`). With zero rows, the page's stated
  purpose ("Manage secrets stored in your system's secure keyring") is unreachable.
- **Heuristic:** #7; empty-state completeness; the page title promises a capability it doesn't expose.
- **Fix:** Page-level **+ Add Secret** button in the header, and the same CTA inside the empty state.
- **Effort:** S

### F9. Security-critical text fails WCAG AA, worst on the security warnings themselves
- **View:** Dashboard hub chips; `ServerDetail.vue` error card; global primary buttons
- **Evidence** (canvas-resolved computed colors, effective ratio incl. opacity; AA needs 4.5:1 for normal text):

  | Text | Theme | Ratio |
  |---|---|---|
  | `Docker isolation disabled — enable Docker to protect your system` | corporate | **1.28** |
  | `dig <hostname>` remediation box | dark | **1.07** |
  | `https://docs.mcpproxy.app/errors/MCPX_HTTP_DNS_FAILED` | corporate / dark | **1.41 / 1.63** |
  | `Manage in Settings` (telemetry banner) | dark | **2.07** |
  | `Quarantine protection active` | corporate | **2.69** |
  | `4 errors` (Activity header) | corporate | **2.92** |
  | `32.4% tokens saved` | corporate / dark | **3.37 / 3.60** |
  | `Host not found` badge | dark | **3.67** |
  | `Add Server`, `Connect Clients`, `Restart` (white on primary) | both | **4.13** |

  The 4.13 figure is systemic: every filled primary button in the app fails AA by the same margin.
- **Heuristic:** WCAG 2.1 AA (1.4.3).
- **Fix:** Darken the primary token one step (4.13 → ≥4.5 buys AA on every button at once); give warning
  and error surfaces AA-checked on-color foregrounds instead of tinted text on tinted fill; add a
  contrast assertion to the Playwright sweep so this cannot regress.
- **Effort:** M

### F10. Dashboard says no client is connected while Sessions shows a live session
- **View:** `Dashboard.vue` AI Agents box vs `Sessions.vue`
- **Evidence:** With an MCP client mid-session, the Dashboard box showed only
  `Available: Claude Code, Claude Desktop, Cursor, VS Code, Codex CLI, Gemini CLI, OpenCode` and no
  Connected group, while `/sessions` listed `ux-audit-client v1.0 — Active — 13 tool calls — Last active 11m ago`.
- **Heuristic:** H5; #1 visibility of system status; cross-view consistency.
- **Fix:** Drive the box from the same session store as `/sessions`; show recently-seen clients as
  **idle** with a last-seen time rather than omitting them, and make each name link to its session.
- **Effort:** S

### F11. Wrong remediation offered for a DNS failure, and three status vocabularies on one row
- **View:** `Dashboard.vue` attention banner; `Servers.vue` card; `ServerDetail.vue` header
- **Evidence:** For `broken-remote` (`dial tcp: lookup example.invalid: no such host`) the offered
  action is **Restart** on both the Dashboard banner and the card. On the detail page one row of tiles
  reads `Status: Enabled / Active` with a **green check**, `Connection: Offline`, and the page badge
  reads `Disconnected` — three vocabularies, one of them green, for a server that is down.
- **Heuristic:** #1, #4 consistency, #9 help users recover.
- **Fix:** Map `health.action` for DNS/URL failures to **Edit URL** (opens the config tab focused on the
  URL field). Collapse `Enabled/Active`, `Online/Offline` and `Connected/Disconnected` into one
  admin-state + one health-level vocabulary, and never render a success glyph on an unhealthy server.
- **Effort:** M

### F12. Servers card shows a raw Go error dump next to a perfectly good summary
- **View:** `Servers.vue` card
- **Evidence:** The card renders both the badge `Host not found` (good) and a full-width red block:
  `failed to connect: MCP initialize failed during no-auth strategy: transport error: failed to send
  request: failed to send request: Post "https://example.invalid/mcp": dial tcp: lookup example.invalid:
  no such host` — note the duplicated `failed to send request:`. The detail page already renders this
  correctly as *code + summary + Cause + remediation*.
- **Heuristic:** #9 errors in plain language.
- **Fix:** Show the structured summary + code on the card with a **Details** link; keep the raw cause on
  the detail page only. Fix the duplicated wrapper text in the upstream error chain.
- **Effort:** S

### F13. Sensitive-data drawer prints the secret it just flagged
- **View:** `Activity.vue` detail drawer
- **Evidence:** A call flagged `critical · aws_access_key · in arguments · in response` renders, a few
  hundred pixels below the red detection panel, `"message": "AKIAIOSFODNN7EXAMPLE aws key here"` in
  Request Arguments and `"Echo: AKIAIOSFODNN7EXAMPLE aws key here"` in Response Body, both in cleartext
  with a **Copy** button. The same panel also exposes the internal field `_auth_auth_type: "admin"`.
- **Heuristic:** Security-UX: a screen-share, screenshot or exported drawer leaks the credential the
  product exists to protect. #4 consistency (detect then display).
- **Fix:** Mask detected spans by default (`AKIA…MPLE`) with an explicit **Reveal** that is logged;
  strip `_auth_*` internals from the user-facing argument view.
- **Effort:** S

### F14. Mobile (390px) layout collapses
- **View:** all, at 390×844
- **Evidence:** The telemetry banner text renders as a ~10-character-wide column consuming ~440px of
  vertical space — over half the viewport — before any content. The attention banner wraps
  `Host not found` onto 3 lines and the `View All Servers` label onto 3 lines. The Activity table shows
  only **Time** and **Type**; Server, Details, Sensitive, Intent, **Status** and Duration are clipped off
  the right with no horizontal-scroll affordance, so failures are invisible on a phone. The footer
  overlaps the last table row.
- **Heuristic:** Responsive; #1 visibility (status is the clipped column).
- **Fix:** `min-w-0` / `flex-1` on the banner's text child; below `md`, render Activity as stacked cards
  (time + tool + status + reason) instead of a table; make the footer static, not overlaying.
- **Effort:** M

### F15. Security page renders four zero-valued tiles while it is still loading
- **View:** `Security.vue`
- **Evidence:** On the first visit after a cold start, the page showed
  `Scanners Installed 0 · Total Scans 0 · Active Scans 0 · Findings 0` above a
  *"Loading security data…"* spinner, with **Scan All Servers** greyed and **Refreshing…** spinning; the
  Deep-scan card read *"Checking configuration…"*. The same page, once loaded, reads
  `1 · 2 · 0 · 0 · Signatures 7`. The stat row sits outside the `v-if="loading && !initialized"` guard
  (`Security.vue:222`), so `overview = {}` renders as real zeros. Warm reloads settle in ~130 ms; the
  cold first paint is the one users see.
- **Heuristic:** #1 visibility of system status — showing false data is worse than showing none.
- **Fix:** Skeleton the tiles (and the Deep-scan state) until `initialized`; never render a numeral for
  an unknown value.
- **Effort:** S

### F16. Settings promises instant apply; `enable_code_execution` does not take effect
- **View:** `Settings.vue` → Advanced → *Enable code execution tool*
- **Evidence:** The page header states *"Changes save instantly; a badge marks fields that need a
  restart"*, and `settings/fields.ts:191` declares the field with no restart flag.
  `PATCH /api/v1/config {"enable_code_execution": true}` (the same call `api.patchConfig` makes) returned
  `applied_immediately: true, requires_restart: false`, and `GET /api/v1/config` then reported `true` —
  yet the `code_execution` tool still answered *"Code execution is disabled"*, including from a
  brand-new MCP session. (The PATCH response also mislabelled `changed_fields` as `["mcpServers"]`.)
- **Heuristic:** #1 visibility of system status; the UI states an outcome that did not happen.
- **Fix:** Either wire the hot-reload path for this key or mark the field `restart: true` so the badge
  appears; make `changed_fields` report the field actually changed.
- **Effort:** M

### F17. Repositories: the registry cards look like the selector but aren't
- **View:** `Repositories.vue`
- **Evidence:** Three prominent cards (Official MCP Registry / Reference Servers / Docker MCP Catalog)
  sit above a *"Choose registries…"* dropdown, a disabled search box and a disabled **Search** button;
  the body reads *"Select a Registry — Choose a registry from the dropdown to start browsing"*. The cards
  are not clickable and carry no hint that they are display-only.
- **Heuristic:** #6 recognition over recall; affordance/false affordance.
- **Fix:** Make the cards the selector (click to select, selected state visible); drop the dropdown, or
  keep it only for multi-select. Enable the search box once a registry is selected and say so inline.
- **Effort:** S

### F18. Connect modal: three interaction patterns, and "connected" means "config mentions mcpproxy"
- **View:** `ConnectModal.vue`
- **Evidence:** In one list: six rows offering a bare red **Disconnect** text link (no confirm), one row
  reading `Config not found` with no action, and one offering **Review & connect** + **Check access**.
  The footer's primary action is **Connect All** while every actionable row already says Disconnect. More
  seriously, an instance on `:18412` reported those clients as connected because their configs contain a
  `mcpproxy` entry — pointing at a *different* endpoint (`:8080`). The modal never shows which endpoint a
  client is registered to.
- **Heuristic:** #1 visibility; #4 consistency; #5 error prevention (unconfirmed destructive link).
  Also a hard blocker for `remote-access-tunnel`, where "which endpoint am I connected to" is the question.
- **Fix:** Show the registered endpoint per row and mark rows pointing elsewhere as *Connected to another
  instance*. One action pattern for all rows (a button with three states). Confirm before Disconnect.
  Make **Connect All** reflect how many rows it would actually change, and disable it at zero.
- **Effort:** M

---

## P2 — consistency, comprehension, accessibility

### F19. Onboarding wizard has no path for a user with no existing MCP setup, and hides the security choice
- **View:** `OnboardingWizard.vue`, first-run
- **Evidence:** Step 2 ("Servers") is entirely *"pick which servers from your existing AI clients to
  import"*. A user with no prior MCP config sees an empty list and no alternative (no registry browse, no
  manual add) inside the wizard. The footer offers two similar primaries — **Import as active** and
  **Import & quarantine** — with no guidance on which is safer, and the safer one is the visually weaker
  of the two. The security controls sit behind a collapsed `▸ Runtime isolation and MCP server quarantine`
  disclosure. The server list is clipped mid-row with no scrollbar affordance, and there is no **Back**.
- **Heuristic:** #6, #7, #10; safe-by-default.
- **Fix:** Add a *"Nothing to import — browse the registry"* branch. Make **Import & quarantine** the
  single primary and demote the other to a link labelled *Import without review*. Expand the isolation
  panel by default. Add Back, and cap the list height with a visible scrollbar.
- **Effort:** M

### F20. `/search` is an orphan and duplicates two other search surfaces
- **View:** `Search.vue`
- **Evidence:** Routed at `/search` but absent from the sidebar. `?q=echo` does not prefill — the page
  renders its empty state regardless. Its layout is marketing-style (centered *"Powerful Tool Search"*
  with three feature cards: Natural Language / Relevance Scoring / Cross-Server) unlike every other view.
  Meanwhile the header has a *Search tools* box **and** a second greyed **Search** button, and `Tools.vue`
  has its own search + four filter dropdowns.
- **Heuristic:** #4 consistency; #8 minimalist design.
- **Fix:** Pick one: fold the BM25 search into `Tools.vue` (or make `/search` the canonical page, add it
  to the sidebar, honour `?q=`, and drop the marketing panel). Remove the duplicate header button or make
  its enabled state obvious.
- **Effort:** M

### F21. Server Logs tab is unreadable
- **View:** `ServerDetail.vue` → Logs
- **Evidence:** Each entry is a 5-line pretty-printed JSON object whose `message` field contains a second,
  already-formatted log line with escaped quotes:
  `"message": "2026-08-25T06:49:51.879+03:00 | INFO | core/connection_lifecycle.go:235 | Disconnecting from server | {\"server\": \"everything\", \"was_connec…` — clipped at the right edge, no wrap, no
  horizontal scrollbar. Two different timestamps per entry (read time and event time). Controls are only
  *Last 100 lines* and **Refresh** — no level filter, no text filter, no follow/tail.
- **Heuristic:** #8; #10 help with diagnosis. This is the primary debugging surface.
- **Fix:** Render one line per entry (`time · level · message`) with the structured fields behind a
  disclosure; unescape the inner payload; add level + text filters and a follow toggle.
- **Effort:** M

### F22. Usage charts invent tools and quantize latency
- **View:** `Usage.vue`
- **Evidence:** *Calls per tool*, *Token sinks* and *Errors & latency* all list `everything:doesnotexist`
  and `broken-remote:whatever` — names from failed calls to tools that never existed — as first-class
  tools with 100.0% error rates. Every row of the p50/p95 table reads exactly `10 ms`, while the Activity
  table shows 3 ms / 4 ms / 5 ms for those same calls. The *Errors & latency* bar chart has no axis title
  distinguishing error-rate bars from latency.
- **Heuristic:** #4 consistency; data honesty.
- **Fix:** Exclude unresolved tool names from the tool charts (or bucket them as *Unknown tool*);
  fix or hide the latency quantization; label the chart axes.
- **Effort:** M

### F23. The token-savings headline does not move
- **View:** `Dashboard.vue` chip; `Usage.vue` tile
- **Evidence:** `32.4% tokens saved · 519 saved` read identically before and after 16 additional tool
  calls, with the panel stamped *Updated 21s ago*. The chip has no tooltip and no drill-down.
- **Heuristic:** #1 visibility; #2 match with the real world. This is the product's headline claim.
- **Fix:** Verify the aggregation window and refresh path; make the chip link to the Usage breakdown and
  carry a tooltip explaining how the figure is derived.
- **Effort:** S

### F24. Activity counts non-calls as "calls"
- **View:** `Activity.vue` header
- **Evidence:** First-run instance: the header reads `1 call` and the single row is
  `🚀 System Start`. Populated: `70 calls` includes two `security_scan` rows and a `system_start` row.
- **Heuristic:** #2 match with the real world.
- **Fix:** Count `tool_call` for "calls" and label the rest as events (see F1).
- **Effort:** S

### F25. The drawer for internal events is a dead end
- **View:** `Activity.vue` drawer for a `security_scan` row
- **Evidence:** Four fields (Status, ID, Server, Tool, Source) and ~80% whitespace — no scan verdict, no
  findings count, no link to the scan report, though `/security/scans/:jobId` exists. On tool-call rows
  the drawer shows a Session ID as plain text with no link to `/sessions`, even though Sessions links the
  other way with **View Activity**.
- **Heuristic:** #7 flexibility; navigation should be bidirectional.
- **Fix:** Type-specific drawer bodies; link `security_scan` → its report and Session ID → `/sessions`.
- **Effort:** S

### F26. Intent and Sensitive columns are unlabeled emoji
- **View:** `Activity.vue` table
- **Evidence:** The Intent column renders 📖 / ✏️ / ⚠️ for read/write/destructive; the Sensitive column
  renders a red `☢ 1` badge. No legend, no visible key, and colour+emoji is the only encoding.
- **Heuristic:** #6 recognition over recall; WCAG 1.4.1 (colour/glyph as sole indicator).
- **Fix:** Emoji + short word (`read` / `write` / `destructive`), or a legend in the filter panel.
  (The `sr-only` label is already present — surface it visually.)
- **Effort:** S

### F27. Server stat tiles use an ambiguous denominator and double-count
- **View:** `Servers.vue` stat row
- **Evidence:** `Total Servers 4 (3 enabled) · Connected 2 (50% online) · Quarantined 1 (Need security
  review)`. 50% is 2-of-4 (all servers) rather than 2-of-3 (enabled). The one server that is both disabled
  and quarantined is counted in the Dashboard's right rail as **both** `1 disabled` and `1 in quarantine`,
  reading as two problems.
- **Heuristic:** #2; #4.
- **Fix:** State the denominator (`2 of 3 enabled online`); make quarantine and disabled mutually
  exclusive in the summary, or label the overlap.
- **Effort:** S

### F28. Auth-required state shows three error surfaces at once and calls the key optional
- **View:** `AuthErrorModal.vue`
- **Evidence:** With no key: a modal *"Authentication Required"*, a red inline alert behind it, and a
  yellow *"Connection Lost — Reconnecting to server…"* toast bottom-left — three messages for one cause.
  The field is labelled **Enter API Key (optional)** though it is the only way in, and
  **Continue Without Auth** is offered at equal weight (it leads to a shell of zero-valued tiles).
  Instructions assume a tray icon, which headless/server installs do not have.
- **Heuristic:** #9 error messages; #5 error prevention.
- **Fix:** Suppress the inline alert and the reconnect toast while the auth modal is open. Relabel to
  **API key** (required). Remove or heavily demote *Continue Without Auth*. Add the
  `mcpproxy status`/config-file path alongside the tray instruction.
- **Effort:** S

### F29. No "match system" theme; the default is light
- **View:** `SidebarNav.vue` footer → Theme; `stores/system.ts:28-44, 350`
- **Evidence:** 15 daisyUI themes are offered with no grouping and no *System* option;
  `loadTheme()` falls back to `corporate` (light), so a user on a dark OS gets a light UI on first run
  and must find the footer dropdown.
- **Heuristic:** #4 consistency with platform conventions; #7.
- **Fix:** Add **System** and make it the default (`prefers-color-scheme`); group the rest under a
  "More themes" section.
- **Effort:** S

### F30. Accessibility gaps on the main screens
- **View:** `Servers.vue`, `Activity.vue`
- **Evidence:** 5 form controls on `/servers` have no accessible name (no `aria-label`, no `placeholder`,
  no associated `<label>`, not wrapped in one). `/activity` auto-refreshes a live table with **no
  `aria-live` region** (0 found) and no `<caption>`, so a screen-reader user gets no announcement when
  rows change. Escape does not close modals (F6).
- **Heuristic:** WCAG 4.1.2, 1.3.1, 4.1.3; #4.
- **Fix:** Label every control; wrap the row-count line in `aria-live="polite"`; add a table caption.
  Add these three as assertions to the committed Playwright sweep.
- **Effort:** S

---

## P3 — polish

### F31. Unexplained header jargon
`Mode: Retrieve` and the per-card `Manual` badge appear with no tooltip. **Fix:** add `title` text
(`Retrieve = agents search for tools first`; `Manual = added by you, not imported`). **Effort:** S

### F32. Header search looks disabled at rest
The **Search** button next to the header box renders greyed until text is typed, reading as broken next
to an empty box. **Fix:** keep it enabled and no-op on empty, or merge it into the input as an icon
button. **Effort:** S

### F33. Deep-linking without the `/ui` prefix returns a raw Go 404
`http://127.0.0.1:18412/activity` returns a plain-text `404 page not found` instead of the styled
`NotFound.vue`, while `/ui/activity` works. **Fix:** redirect unknown non-API paths to `/ui/<path>`.
**Effort:** S

### F34. Duplicate Risk Score and an unlabeled dot on the server Security tab
`0/100` appears twice (top-right and in the body), plus a bare green dot with `0` beside it.
Scan IDs are truncated to `scan-eve` with no copy affordance. **Fix:** show the score once; label or
drop the dot; make the ID copyable. **Effort:** S

### F35. Date formats disagree within one screen
The Activity table prints `8/25/2026, 6:51:38 AM` (US) while the From/To filters use native
`dd/mm/yyyy, --:--` inputs. **Fix:** one locale source of truth; consider relative-only in the table with
absolute on hover. **Effort:** S

### F36. Assorted
`MCPPROXY active / just started` on the Dashboard reads as a stuck state; `View Activity` in Sessions
clips its label onto two cut-off lines; the Tools table's **Approval** column showed `approved` on all 22
rows (zero variance, wasted width) while descriptions truncate with no tooltip; the collapsed sidebar is
icon-only, relying on hover `title` alone to disambiguate six similar glyphs; the telemetry banner takes
the top of the Dashboard on every visit until dismissed. **Effort:** S each

---

## Resolution

The backlog was closed by eight PRs merged on 2026-08-25:

| PR | Findings it names |
|---|---|
| [#1044](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/1044) `feat(webui): make the analytics dashboard the default landing page` | F4 |
| [#1048](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/1048) `fix(activity): count calls once, so Activity and Usage agree` | F1, F24 |
| [#1049](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/1049) `fix(webui): first-run call to action …` | F3, F4, F19, F36 |
| [#1050](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/1050) `fix(webui): forms & modals UX audit` | F6, F8, F16, F36 |
| [#1051](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/1051) `fix(ui): mask flagged secrets server-side …` | F13, F15, F20, F31, F32, F33, F36 |
| [#1052](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/1052) `fix(webui): make the Activity and Usage numbers say what they mean` | F2, F5, F22, F23, F25, F26, F27, F36 |
| [#1053](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/1053) `fix(webui): honest server, connection and connect-modal states` | F7 and F12 (verified live, below); by its diff and test names also F10 (`live-clients.spec.ts`), F11 (`internal/health/calculator.go`, `ConnectionStatus.vue`), F17 (`repositories-card-selector.spec.ts`), F18 (`connect-modal.spec.ts`, `connect-endpoint-truth.spec.ts`) and F28 (`auth-single-surface.spec.ts`). **Its body text names F1–F16 — that is a mis-paste of the *tray* audit's numbering; the tray PR is [#1055](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/1055) and #1053 touches no Swift file.** F34's coverage is not evidenced either way. |
| [#1054](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/1054) `fix(webui): AA contrast, system theme, mobile layout and a11y gaps` | F9, F14, F21, F29, F30, F32, F35, F36 |

### Re-checked live on v0.61.0

Confirmed fixed against a running core (isolated instance, scratch `--config` **and** `--data-dir`, real
`/mcp` traffic — reads, writes, destructive, failures, a sensitive-data hit, repeated identical calls):

- **F1** — Activity header `22 calls · 3 errors · 1 blocked` reconciles with the Usage tiles `Calls 22 /
  Errors 4`; Usage folds `blocked` into `errors`, Activity splits it, and both match
  `GET /api/v1/activity/usage` (`total_calls: 22, total_errors: 4`). The 70-vs-42 split is gone.
- **F4** — `/ui/` opens the Usage analytics panel; `/ui/overview` and `/ui/usage` are real routes with
  their own titles.
- **F5** — a `Group repeats` control folds runs (`echo ×5`, `2ms–5ms`) and the footer reads
  `1-25 of 56 (63 rows, repeats folded)`. No **visible** Success badge: only `Error` and `Blocked` are
  marked, and the word "Success" is present as an `sr-only` label, which is H3-compliant.
- **F6** — focus enters the Add Server dialog on open, submit sits in a sticky footer, and Escape closes
  it (see the note below on how not to measure that).
- **F7** — a quarantined server card carries **Review · Approve · Scan**.
- **F8** — Secrets has **Add Secret** in the header *and* in the empty state, with a one-line why.
- **F12** — the Go error dump is behind a `Technical details` disclosure under a plain-language summary.
- **F13** — masking is server-side: the drawer renders `AKIA…****` and states that full values come only
  from `mcpproxy activity export --include-bodies`.
- **F20** — `/ui/search` redirects to `/ui/tools`.
- **F24** — the Activity header separates `events` from `calls`.
- **F26** — the Sensitive and Intent columns have real headers.
- **F33** — `/servers` (no `/ui` prefix) redirects to `/ui/servers`; no raw Go 404.

### Nothing left open from this audit

F6 deserves a note, because re-checking it produced a false negative first. Escape sent through the
browser-automation layer appeared not to close the Add Server dialog; it never reached the page's
`document` keydown listener. Dispatching the same event in-page
(`document.dispatchEvent(new KeyboardEvent('keydown', {key: 'Escape'}))`) closes the dialog immediately.
F6 is fixed in full by [#1050](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/1050):
`frontend/src/composables/useModalA11y.ts` supplies Escape-to-close, initial focus into the dialog, a Tab
trap and focus restoration for the five `<dialog :open>` modals, which get none of that from the browser
because they are opened by the `open` attribute rather than `showModal()`. Verified live: opening Add
Server or Add Secret moves focus into the dialog, and an in-page Escape closes it.

The lesson for the sweep this document proposes: **assert modal Escape with an in-page dispatch, not a
synthetic key press**, or the assertion silently tests the automation layer instead of the app.

### Not re-checked

F2, F9, F10, F11, F14, F15, F16, F17, F18, F19, F21, F22, F23, F25, F27, F28, F29, F30, F31, F32, F34,
F35, F36 were not re-verified live; they are recorded as closed on the strength of the PRs above. F28 is the exception in that list: its "key called optional" half is confirmed fixed, but the three-surfaces-at-once state is still reproducible (see new defect 5). F3 and
F19 need a first-run (empty-config) instance, and F9/F14/F29/F30 need the contrast/responsive/a11y sweep,
neither of which this pass ran.

### Corrections to the audit itself

Two findings were not quite right as written, independent of whether they were fixed:

- **F23** ("the token-savings headline does not move") mis-diagnoses a stale aggregation. The figure is a
  per-request *structural* estimate over the current tool catalog
  (`runtime.CalculateTokenSavings` → `tokens.CalculateProxySavings`), so it is window-independent by
  design and correctly does not move when call volume changes. The real complaint is that the label reads
  like a windowed metric — which is what the fix addressed.
- **F31**'s `Manual` half is wrong. That per-card badge is the spec-088 trust-mode badge and it already
  carried a `title` tooltip (`ServerCard.vue` / `utils/trustMode.ts`) at the time of the audit. Only the
  `Mode: Retrieve` half was a real gap.

### New defects found while re-checking (not in the audit above)

These are on `main` today and are **not** regressions of anything listed above:

1. **Quarantining a server through the config file leaves its tools in the search index.** With
   `memory` quarantined (`quarantined: true` written to the config file, hot-reloaded), `retrieve_tools`
   still returns all nine of its tools — name, full description and a `call_with` variant — and the call
   is then refused with `SECURITY BLOCK`. Quarantine exists to keep an unreviewed server's tool
   *descriptions* away from the agent, which is where a Tool Poisoning Attack lives, so this defeats its
   purpose on the file path. `POST /api/v1/servers/{id}/quarantine` does **not** have the bug:
   `Runtime.QuarantineServer` (`internal/runtime/lifecycle.go`) calls `indexManager.DeleteServerTools`
   explicitly. The reload path (`LoadConfiguredServers`, same file) detects the `Quarantined` transition
   — it is in the `hasChanged` test and logged as `quarantined_changed: true` — but only acts on
   `oauthChanged`, so no purge runs. Disabling via the config file is unaffected (a disabled server drops
   out of the active set the orphan sweep uses). The purge belongs on the transition, not on the two API
   handlers that currently carry it.
2. **Tool counts advertise quarantined and disabled servers as available.** Servers page reads
   `Total Tools 22 — Available across all servers` (and the header chip `22 Tools`) while `memory` is
   quarantined; on a second instance the same total counted a server that was quarantined *and* another
   that was disabled *and* disconnected. `GET /api/v1/tools` returns them too, while
   `GET /api/v1/index/search` correctly returns nothing for them — so the count contradicts the index it
   claims to describe, and none of those tools is callable.
3. **A server card can assert two contradictory security states at once**: `memory` renders a green
   `Clean` scan verdict directly above a yellow `Quarantined — needs security review` banner.
4. **The Activity Type column mixes two vocabularies**: humanised (`Quarantine Change`, `Policy
   Decision`, `Internal Tool Call`) and raw snake_case (`tool_quarantine_change`) in the same column, on
   the same screen.
5. **A stale auth error survives a successful sign-in.** With no stored key, `/ui/activity` shows the
   Authentication Required modal *plus* an inline red `Invalid or missing API key / Retry` panel *plus* a
   `Disconnected` chip. Entering a valid key and pressing **Set Key** re-authenticates the header (server
   counts, tool count and the `Live` badge all come back) but the view body keeps the red panel and
   renders zero rows until the user clicks **Retry** by hand. The key half of F28 that *is* fixed is the
   wording — the field now reads `API key (required)`, not optional.

---

### Found by committing the audit's own regression assertions (2026-08-29)

The closing recommendation below — fold the F6 modal and F1 cross-view checks into
`e2e/web-ui-sweep/` — was carried out. Running the completed sweep immediately turned up three
defects **already on main**, none of them regressions of the 36 above, and each one a case the
sweep could not previously see:

1. **The landing page failed AA in both themes, and two of its controls had no accessible name.**
   `Usage.vue` uses the raw `opacity-*` utility for muted text (3.09–4.23:1 measured), and its
   status/sort `<select>`s carry their purpose only in their option text. #1054 fixed exactly this
   failure mode for the `text-base-content/NN` ramp and `.stat-*`, but never covered the raw
   opacity utility — and nothing caught it because the audit walked `/` *before* [#1044](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/1044) made
   Usage the landing page. The committed contrast and accessible-name checks walk `/`, so they
   found it the first time they ran. Fixed by moving those four sites onto the AA-checked
   `--tone-muted` and giving both selects an `aria-label`.

2. **The Activity status pill clips at 390px whenever the status is not one of the four known
   ones.** An unmapped status falls through to a ghost pill carrying the RAW value; `tool_auto_approved`
   measured 145px inside a 93px cell (the table is `table-fixed` below 640px). The existing F14
   phone check never caught it because it measures `rows.first()`, and a `success` row — ~90% of
   traffic — renders an `sr-only` label of zero width, so the assertion was vacuous for the common
   case. It only became real once the new F1 check generated an error/quarantine row and put one
   first. Fixed by letting the pill truncate against the cell rather than sizing the column to
   today's longest label, which would only hold until the next status is added.

**Method note worth keeping:** both were found because a new assertion changed what an *existing*
assertion saw. The sweep shares one instance across its tests, so traffic generated by one check is
data for the next — that coupling is what made the vacuous phone assertion finally bite. Treat a
newly-failing neighbouring test after adding a check as a probable real find, not a flake, and
confirm by running the same sweep against clean `HEAD` (which is how these two were separated from
the change under test).

## Reproducing this audit

```bash
make build
# populated instance
./mcpproxy serve --config=<scratch>/pop/mcp_config.json --data-dir=<scratch>/pop --listen=127.0.0.1:18412
# first-run instance: point --config at a path that does NOT exist yet
./mcpproxy serve --config=<scratch>/empty/mcp_config.json --data-dir=<scratch>/empty --listen=127.0.0.1:18413
```

Populated config used four servers: two healthy stdio (`@modelcontextprotocol/server-everything`,
`@modelcontextprotocol/server-memory`), one unreachable HTTP (`https://example.invalid/mcp`) and one
disabled/quarantined stdio. Traffic was generated over `POST /mcp` with `call_tool_read` /
`call_tool_write` / `call_tool_destructive`, including `intent_reason`, repeated identical calls, failures
mid-run, and one payload containing a fake AWS key. The Web UI is served under `/ui/` — Playwright specs
must use `http://127.0.0.1:<port>/ui/<route>?apikey=<key>`; the bare route returns a Go 404 (F33).
The scrollable element is `main.overflow-y-auto`, not the document.

Worth folding into the committed sweep (`e2e/web-ui-sweep/`): the contrast assertion from F9, the
accessible-name and `aria-live` checks from F30, the modal Escape/focus check from F6, and a
cross-view total-consistency check for F1.
