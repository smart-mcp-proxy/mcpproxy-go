# Tray Glance — compact activity, clients, and 24h histogram in the macOS menu

**Status:** Design (2026-07-29, revised after cross-model review)
**Epic:** `ux-audit` → `ux-audit-macos-sweep` (roadmap.yaml, P0); folds in `action-log-transparency` → `action-log-tray-menu`
**Reference:** [CodexBar](https://github.com/steipete/CodexBar) — rich SwiftUI content hosted inside a plain `NSMenu`

## Problem

The macOS tray menu shows what MCPProxy *is* (servers, profiles, version, update state) but nothing about what it is *doing*. To see whether an agent is calling tools, which client is connected, or whether calls are failing, the user must open the Web UI. The activity log is a headline feature of the product and is invisible from the surface people actually keep open.

## Goal

A compact glance section at the top of the tray menu answering three questions, without turning the menu into a dashboard:

1. What just happened? — the five most recent tool calls
2. Who is connected? — active MCP clients
3. How busy has it been? — calls per hour over the last 24h (in a submenu)

Constraints, in priority order: never irritate (the menu stays a menu — fast, keyboard-navigable, VoiceOver-friendly); never lie (no stale data presented as live, no metric labelled as something it is not); never regress menu-open cost (spec 048's zero-network menu open is an invariant).

## Decisions

| Question | Decision | Rationale |
|---|---|---|
| Presentation | Plain `NSMenuItem`s for all text rows; a hosted SwiftUI view **only** for the histogram, inside its submenu | Apple documents that custom menu-item views receive mouse events but **not** keyboard events — rows built as SwiftUI buttons would not be keyboard-selectable and would need hand-rolled accessibility. The rows are text plus an icon; native items give keyboard navigation, highlighting, and VoiceOver for free. Only the chart genuinely needs custom drawing. |
| What is visible on open | Activity + clients; histogram in a submenu | Keeps the menu short. The histogram is a visualisation, not a glance signal. |
| Histogram metric | Calls per hour, errors as a distinct stacked segment | No hourly token series exists: `UsageTimeBucket` carries `calls`, `errors`, `total_resp_bytes`, and the endpoint reports `token_source: "bytes"` — a size proxy. Real tokenisation (`cl100k_base`) accumulates only per session (`MCPSession.total_tokens`), never per activity record. Plotting calls is exact and needs no storage change. A real token histogram is a separate roadmap item. |
| Liveness | Consume `activity.*` SSE payloads directly; keep the 30s poll as reconciliation | `CoreProcessManager.swift:683` matches `case "activity"`, which the core never emits, so activity is 30s-polled today. Merely fixing the name is not enough: that branch calls `refreshActivity()`, so prefix-matching would fire a REST GET per event — network amplification, not push. The `activity.tool_call.completed` payload already carries `server_name`, `tool_name`, `session_id`, `request_id`, `status`, `error_message`, `duration_ms` (`internal/runtime/event_bus.go:444`) — everything a row renders. Note the field is `error_message`, matching `ActivityEntry`; a row built from a payload key named `error` would silently lose the failure detail until the next poll. |
| Integration | Extract a `Menu/Glance/` component; no view caching, but suppress structural rebuilds while the menu is open | With plain items, `rebuildMenu()`'s `removeAllItems()` is as cheap for glance rows as for today's items — the caching scheme an earlier draft proposed is unnecessary and would not have prevented churn anyway. But live SSE rows make the existing debounced `objectWillChange → rebuildMenu()` sink (`MCPProxyApp.swift:114`) fire during active traffic, i.e. potentially while the user is reading the menu. See "Rebuilds while the menu is open" below. |

## Architecture

New component under `native/macos/MCPProxy/MCPProxy/Menu/Glance/`:

- **`GlanceSection.swift`** — the only thing `MCPProxyApp` talks to: `func items(for state: AppState) -> [NSMenuItem]`, returning plain, actionable `NSMenuItem`s plus one submenu item.
- **`GlanceSelection.swift`** — the display rules (which records qualify, ordering, capping). Pure functions over `[ActivityEntry]` / `[MCPSession]`, which is what makes them unit-testable without AppKit.
- **`GlanceFormatting.swift`** — status icon, row label composition, relative time, middle truncation. Salvaged from the dead `Menu/TrayMenu.swift` (511 lines, referenced nowhere, already contains `activityIcon(for:)`, `activitySummaryText(for:)`, `relativeTime(_:)`); that file is then deleted.
- **`ActivityHistogramView.swift`** — SwiftUI Charts bar chart hosted in the submenu's single custom item, built when the submenu opens.

Changes to existing files:

- `MCPProxyApp.swift` — `rebuildMenu()` inserts `glance.items(for: appState)` after the status header, before "Needs Attention", plus the histogram submenu item. Roughly five lines; no new layout code in an already 1120-line file.
- `CoreProcessManager.swift:683` — replace the dead `case "activity"` with handlers for `activity.tool_call.completed` and `activity.internal_tool_call.completed` that build a row from the event payload and prepend it, with no REST call. `activity.tool_call.started` is deliberately ignored (the core does not persist started events either — `activity_service.go:418`).
- `APIClient.swift:341,353` — decode `last_activity` (the field the API emits) instead of `last_active`.
- `APIClient.swift` — add a usage-aggregate call and its response model, and a *separate* glance-activity call carrying the type filter.
- `AppState.swift` — add `@Published var glanceActivity: [ActivityEntry]`, `usageTimeline: [UsageBucket]?` and `callsThisHour: Int?` alongside the existing `recentActivity` / `recentSessions`. The usage fields are optional so that "not loaded yet" is distinguishable from "loaded, and the proxy was idle" — a valid usage response can legitimately carry an empty timeline.

`AppState.recentActivity` is **not** narrowed. The native Dashboard deliberately renders the full activity log from that property — security scans, quarantine changes, OAuth events — precisely so a quiet proxy still shows something (`Views/DashboardView.swift:572`). Filtering it for the tray's benefit would silently gut that view, so the glance section gets its own feed instead.

## Layout

```
MCPProxy v0.52.0                    ●
12 calls this hour · 2 clients
─────────────────────────────────────
Recent
  ✓ github:create_issue          12s
  ✓ obsidian:search_notes         1m
  ✕ jira:get_issue · auth failed  3m
  ✓ retrieve_tools "docker"       5m
  ✓ code_execution                8m
  Open Activity…
─────────────────────────────────────
Clients
  ● Claude Code       8 calls · now
  ● Cursor            4 calls · 2m
─────────────────────────────────────
Activity (24h)                      ▸
─────────────────────────────────────
Servers (12)                        ▸
Profile: default                    ▸
…
```

Row anatomy: status icon (shape *and* colour, never colour alone), `server:tool` for upstream calls or the built-in's name for discovery/execution calls, relative age right-aligned. A failed row appends the first clause of `errorMessage`, truncated to one line, with the full message in the tooltip. UI strings are English, matching the rest of the menu; the mockup follows the repo's own `docs/superpowers/specs/macos-design-guide.md` (semantic colours, SF Symbols).

## Data flow

**Menu open performs zero network requests.** Everything renders from `AppState`. Three background feeds keep it current.

### Activity

Live rows come from SSE. On `activity.tool_call.completed` / `activity.internal_tool_call.completed`, the tray builds an entry from the event payload and prepends it — no GET per event. A dedicated poll every 30 seconds reconciles that optimistic list with the server's canonical records (storage-assigned ids and the asynchronously-computed sensitive-data flags): `GET /api/v1/activity?type=tool_call,internal_tool_call&limit=50`. This is a fourth call on the existing refresh cycle rather than a reuse of the Dashboard's feed, for the reason given above.

**Which records qualify** — evaluated in this order, in `GlanceSelection`, identically for polled records and SSE payloads:

1. **Exclude** any record whose tool is a management built-in (`upstream_servers`, `quarantine_security`), regardless of status. These are proxy administration — what "exclude internal mcpproxy events" asks for. This rule wins over everything below.
2. **Include** every `type == "tool_call"` record. These are the real upstream calls.
3. **Include** a remaining `type == "internal_tool_call"` record when its tool is `retrieve_tools`, `code_execution`, or `describe_tool`, **or** when its status is not `success`.

Rule 3's second clause matters more than it looks. The backend deliberately keeps *failed* `call_tool_*` wrapper records while suppressing successful ones, precisely because a failed wrapper has no corresponding upstream `tool_call` record (`internal/storage/activity_models.go:255`). A plain built-in allowlist would discard the only trace of a call that never reached its server — hiding exactly the failures this section exists to surface. Rule 1 is stated first because management built-ins fail like any other internal call, and their failures are still administration noise.

The poll fetches 50 records and the section shows the first five that qualify. The page is deliberately oversized because rule 1 runs on the client: the server-side `type` filter admits management built-ins (they *are* `internal_tool_call` records), and storage applies its filter before truncating to the limit (`internal/storage/activity.go:147`), so a burst of `upstream_servers` calls would otherwise fill a small page and push real tool calls out of view. Fifty is not a guarantee — an agent that makes fifty consecutive management calls will legitimately see the empty state — but it moves the failure mode from "routine" to "pathological". If fewer than five qualify, the section shows what it has.

This selection is presentation policy, not tray-held state, so it stays within the "tray holds no state" rule (CLAUDE.md).

### Header summary and histogram

Both come from one call on the existing 30-second background loop: `GET /api/v1/activity/usage?window=24h&top=1`. It is served from an in-memory snapshot behind a short TTL cache and never scans the log; `top=1` trims the per-tool payload the tray does not use.

The header reads **"calls this hour"**, not "in the last hour", and that wording is load-bearing. Buckets are aligned to the UTC hour (`rec.Timestamp.UTC().Truncate(time.Hour)`, `internal/runtime/usage_aggregate.go:229`), so they are neither a rolling 60-minute window nor guaranteed to be dense — the response omits hours with no activity. The header therefore selects the bucket whose start equals the current UTC hour and shows zero when that bucket is absent. Taking "the most recent bucket" instead would display a count from hours ago as if it were current.

An earlier draft used `GET /api/v1/activity/summary?period=1h` here. That was wrong on both counts: the handler sets `Limit = 0` and loads every matching record (`internal/httpapi/activity.go:553`) — a log scan, not a pre-aggregate — and it counts *all* activity types, so labelling its total "calls" would be false.

Because the timeline is already in `AppState`, the histogram submenu renders instantly with no fetch of its own.

Chart semantics: a bucket's `calls` **includes** its `errors`, so the stacked bars plot `calls - errors` and `errors` as two segments — stacking the raw fields would double-count failures. The endpoint returns only buckets that exist, so missing hours are synthesised as zero to keep a stable 24-hour axis. Timeline buckets are global by contract (not filtered by server, tool, or status), so the label stays "Activity (24h)" with no implication of filtering. The chart item carries an accessibility label summarising the series (total calls, peak hour, error count) since a bar chart is opaque to VoiceOver.

### Clients

`GET /api/v1/sessions?limit=100` (the API maximum) on the existing poll, filtered client-side to `status == "active"`, showing up to five.

The page size is deliberately the maximum rather than a tidy 25. Storage walks sessions newest-first **by start time** and truncates to `limit` (`internal/storage/manager.go:1355`); only that already-truncated subset is then sorted by last activity (`internal/runtime/runtime.go:1340`). A long-lived session that started days ago but is active right now therefore falls outside any small page — the tray would show "no connected clients" while a client is actively calling tools. Session records are compact scalars, so a 100-record page every 30 seconds is an acceptable cost for correctness. If that payload ever becomes a concern, the right fix is a server-side `status` filter on `/api/v1/sessions`, recorded here as a follow-up rather than smuggled into this work.

Documented caveat: per spec 082, handshake-only sessions are not persisted, so an idle background agent does not appear until its first call.

### Clicks

An activity row and a client row both open the Web UI activity log filtered by that record's **session** — `/ui/activity?session=<id>`, the query parameter the Activity view already reads on mount (`frontend/src/views/Activity.vue:1333`). Filtering by `request_id` was considered and rejected: the REST API supports it but the Web UI does not read such a route parameter, so it would have required a frontend change that this design excludes.

`GlanceSection` does not build these URLs itself. It cannot: it is handed only `AppState`, whose `webUIBaseURL` is scheme/host/port by design, and a first-time browser session needs the API key appended — the existing `openWebUI()` action fetches `currentAPIKey` from the core manager for exactly this reason (`MCPProxyApp.swift:985`). Glance rows therefore carry a target/action pair pointing back at the app delegate, which opens the authenticated URL through the same path as today's menu items. Reusing that path also keeps the key handling in one place rather than duplicating it in a new component.

### Rebuilds while the menu is open

Live rows create a problem the current menu does not have. Every `AppState` change feeds a debounced sink that calls `rebuildMenu()`, which calls `removeAllItems()` (`MCPProxyApp.swift:114`, `:523`) — and the menu is not dismissed when that happens. Today the only sources of change are server state and a 30-second poll, so this is rare. With SSE rows, a busy agent changes state every few hundred milliseconds, potentially while the user is reading the menu or navigating it with the keyboard, and re-creating the item that owns the histogram submenu would disturb an open submenu.

So `rebuildMenu()` gains a tracking guard: while the menu is open it does **not** restructure. Glance rows update in place — setting `attributedTitle` on an existing `NSMenuItem` is exactly how live menus are meant to work — and any structural change (a new row appearing, the section becoming empty) is deferred, flagged dirty, and applied on `menuDidClose`. A menu that grows or shrinks under the cursor is precisely the "irritating" behaviour this design set out to avoid, so this guard is a requirement, not an optimisation.

## States

| Situation | Behaviour |
|---|---|
| Core stopped or disconnected | The whole glance block is hidden **and its state is cleared** — `glanceActivity` emptied, `usageTimeline` and `callsThisHour` set back to `nil`. Clearing is not redundant with hiding: the connection path flips to `.connected` before the first refresh completes (`CoreProcessManager.swift:330`), so without a reset the menu would briefly present the previous core's numbers as live. Optional fields alone do not prevent this; they only make the cleared state expressible. |
| No activity yet | One muted row, "No tool calls yet" — not an empty section with a header. |
| No active clients | One muted row, "No connected clients". |
| Usage data not loaded yet (`callsThisHour == nil`) | The header omits the call count (clients only); the histogram submenu shows a muted "Loading…" row. |
| Usage loaded but the proxy was idle | The header shows "0 calls this hour" and the chart shows a flat 24-hour axis — deliberately distinct from the loading state above, which is why these fields are optional rather than defaulting to zero. |
| Long tool names | Middle-truncated, keeping the server prefix and the tail of the tool name; full text in the tooltip. |

## Testing

Swift unit tests in the existing `swift-test` CI job. `GlanceSelection` and `GlanceFormatting` are pure functions, so the bulk of the logic tests need no AppKit:

- **Regression, SSE naming**: `activity.tool_call.completed` reaches the activity handler (fails today).
- **Regression, no amplification**: handling one completed event issues zero REST requests.
- **Regression, session decoding**: `last_activity` decodes into the model (nil today).
- **Selection**: upstream `tool_call` always included; the three discovery/execution built-ins included; **a failed `call_tool_write` record is included** (the finding above, pinned by a test); **a failed `upstream_servers` record is still excluded** (rule 1 beats rule 3); five qualifying records selected from a 25-record page containing noise.
- **Header bucket**: with a sparse timeline whose newest bucket is three hours old, the header shows zero — not that bucket's count.
- **Dashboard non-regression**: `AppState.recentActivity` still carries non-tool-call types after the glance feed is added.
- **Open-menu stability**: with the menu tracking, a burst of SSE events updates row titles and performs no structural rebuild; the deferred rebuild runs once on close.
- **Reconnect hygiene**: after a disconnect, glance state is cleared, so a menu built between `.connected` and the first successful refresh shows the empty/loading states rather than the previous core's data.
- **Optimistic rows**: a row built from an SSE payload carries the failure text from `error_message` (a payload read keyed on `error` would render a failed call as if it had no detail).
- **Chart data**: `calls - errors` and `errors` segments never double-count; missing hours synthesised as zero across a 24-hour axis.
- Formatting: relative time, label composition, middle truncation.
- Visibility: block hidden when the core is stopped; empty-state rows.
- **Invariant, spec 048**: opening the main menu issues no requests. This requires the glance component to depend on a narrow data-source protocol rather than the concrete `APIClient` actor, so a counting stub can be injected — a small extraction, included in this work.

Visual verification by screenshotting the menu with `mcpproxy-ui-test` per `docs/development/macos-tray.md` (requires Screen Recording permission), including a VoiceOver pass over the rows and the chart's accessibility label.

## Scope note

This is not "rendering plus two one-line fixes". The honest inventory: a new menu component (four files), a rewritten SSE activity path that consumes payloads instead of refetching, two field-level bug fixes, two new API client methods plus response models (usage aggregate, filtered glance activity), three new `AppState` fields, a data-source protocol extraction for testability, deletion of a dead 511-line file, and the test suite above.

## Non-goals

Real token histogram (separate roadmap item: accumulate `TokenMetrics` into hourly buckets in `internal/runtime/usage_aggregate.go`, extend the contract, regenerate OAS). Sensitive-data badges on activity rows. Go/systray parity. Any Web UI change. Rewriting the menu rebuild model into a diffing implementation — it fixes a problem nobody reported, at the cost of risk in the one UI every user sees.

## Follow-ups

- Server-side `status` filter on `/api/v1/sessions`, which would let the tray stop fetching a 100-session page to find the active ones.
- Real token histogram (see Non-goals).

## Follow-ups this unblocks

Completing `ux-audit-macos-sweep` unblocks the `action-log-transparency` epic and the remaining `analytics-default-landing` task, both of which currently sit behind `ux-audit` in the dependency graph.
