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
| Liveness | Consume `activity.*` SSE payloads directly; keep the 30s poll as reconciliation | `CoreProcessManager.swift:683` matches `case "activity"`, which the core never emits, so activity is 30s-polled today. Merely fixing the name is not enough: that branch calls `refreshActivity()`, so prefix-matching would fire a REST GET per event — network amplification, not push. The `activity.tool_call.completed` payload already carries `server_name`, `tool_name`, `session_id`, `request_id`, `status`, `error`, `duration_ms` (`internal/runtime/event_bus.go:430`) — everything a row renders. |
| Integration | Extract a `Menu/Glance/` component; no view caching needed | With plain items, `rebuildMenu()`'s `removeAllItems()` is as cheap for glance rows as for today's items — the caching scheme an earlier draft proposed is unnecessary, and it would not have prevented churn anyway (items are still removed and re-inserted during menu tracking). The histogram view lives in a submenu that main-menu rebuilds do not touch. |

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
- `APIClient.swift` — add a usage-aggregate call and its response model; widen `recentActivity` to accept a type filter and a larger limit.
- `AppState.swift` — add `@Published var usageTimeline: [UsageBucket]` and `lastHourCalls: Int` alongside the existing `recentActivity` / `recentSessions`.

## Layout

```
MCPProxy v0.52.0                    ●
12 calls in the last hour · 2 clients
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

Live rows come from SSE. On `activity.tool_call.completed` / `activity.internal_tool_call.completed`, the tray builds an entry from the event payload and prepends it — no GET per event. The existing 30-second poll reconciles that optimistic list with the server's canonical records (storage-assigned ids and the asynchronously-computed sensitive-data flags), narrowed to `GET /api/v1/activity?type=tool_call,internal_tool_call&limit=25`.

**Which records qualify** (implemented in `GlanceSelection`, applied identically to polled records and SSE payloads):

- `type == "tool_call"` — always. These are the real upstream calls.
- `type == "internal_tool_call"` — when the tool is `retrieve_tools`, `code_execution`, or `describe_tool`, **or when the record's status is not `success`**.

The second half of that rule matters more than it looks. The backend deliberately keeps *failed* `call_tool_*` wrapper records while suppressing successful ones, precisely because a failed wrapper has no corresponding upstream `tool_call` record (`internal/storage/activity_models.go:255`). A naive built-in allowlist would therefore discard the only trace of a call that never reached its server — hiding exactly the failures this section exists to surface.

Management built-ins (`upstream_servers`, `quarantine_security`) are excluded in both states: they are proxy administration, which is what "exclude internal mcpproxy events" asks for.

The poll fetches 25 records and the section shows the first five that qualify, so unrelated records cannot starve the list the way a 10-record page could. If fewer than five qualify, it shows what it has.

This selection is presentation policy, not tray-held state, so it stays within the "tray holds no state" rule (CLAUDE.md).

### Header summary and histogram

Both come from one call on the existing 30-second background loop: `GET /api/v1/activity/usage?window=24h&top=1`. It is served from an in-memory snapshot behind a short TTL cache and never scans the log; `top=1` trims the per-tool payload the tray does not use. The header's "calls in the last hour" is the most recent bucket; the histogram is the same timeline.

An earlier draft used `GET /api/v1/activity/summary?period=1h` here. That was wrong on both counts: the handler sets `Limit = 0` and loads every matching record (`internal/httpapi/activity.go:553`) — a log scan, not a pre-aggregate — and it counts *all* activity types, so labelling its total "calls" would be false.

Because the timeline is already in `AppState`, the histogram submenu renders instantly with no fetch of its own.

Chart semantics: a bucket's `calls` **includes** its `errors`, so the stacked bars plot `calls - errors` and `errors` as two segments — stacking the raw fields would double-count failures. The endpoint returns only buckets that exist, so missing hours are synthesised as zero to keep a stable 24-hour axis. Timeline buckets are global by contract (not filtered by server, tool, or status), so the label stays "Activity (24h)" with no implication of filtering. The chart item carries an accessibility label summarising the series (total calls, peak hour, error count) since a bar chart is opaque to VoiceOver.

### Clients

`GET /api/v1/sessions?limit=25` on the existing poll, filtered client-side to `status == "active"` (the API has no status parameter), showing up to five. The larger page matters: sessions are returned most-recently-active first regardless of status, so a burst of closed sessions could otherwise crowd out an older but still-active client. Documented caveat: per spec 082, handshake-only sessions are not persisted, so an idle background agent does not appear until its first call.

### Clicks

An activity row and a client row both open the Web UI activity log filtered by that record's **session** — `/activity?session=<id>`, the query parameter the Activity view already reads on mount (`frontend/src/views/Activity.vue:1333`). Filtering by `request_id` was considered and rejected: the REST API supports it but the Web UI does not read such a route parameter, so it would have required a frontend change that this design explicitly excludes.

## States

| Situation | Behaviour |
|---|---|
| Core stopped or disconnected | The whole glance block is hidden. Stale numbers presented as current are worse than nothing; the menu already shows the error and a Retry item. |
| No activity yet | One muted row, "No tool calls yet" — not an empty section with a header. |
| No active clients | One muted row, "No connected clients". |
| Usage data not loaded yet | The header omits the call count (clients only); the histogram submenu shows a muted "Loading…" row. |
| Long tool names | Middle-truncated, keeping the server prefix and the tail of the tool name; full text in the tooltip. |

## Testing

Swift unit tests in the existing `swift-test` CI job. `GlanceSelection` and `GlanceFormatting` are pure functions, so the bulk of the logic tests need no AppKit:

- **Regression, SSE naming**: `activity.tool_call.completed` reaches the activity handler (fails today).
- **Regression, no amplification**: handling one completed event issues zero REST requests.
- **Regression, session decoding**: `last_activity` decodes into the model (nil today).
- **Selection**: upstream `tool_call` always included; the three discovery/execution built-ins included; management built-ins excluded; **a failed `call_tool_write` record is included** (the finding above, pinned by a test); five qualifying records selected from a 25-record page containing noise.
- **Chart data**: `calls - errors` and `errors` segments never double-count; missing hours synthesised as zero across a 24-hour axis.
- Formatting: relative time, label composition, middle truncation.
- Visibility: block hidden when the core is stopped; empty-state rows.
- **Invariant, spec 048**: opening the main menu issues no requests. This requires the glance component to depend on a narrow data-source protocol rather than the concrete `APIClient` actor, so a counting stub can be injected — a small extraction, included in this work.

Visual verification by screenshotting the menu with `mcpproxy-ui-test` per `docs/development/macos-tray.md` (requires Screen Recording permission), including a VoiceOver pass over the rows and the chart's accessibility label.

## Scope note

This is not "rendering plus two one-line fixes". The honest inventory: a new menu component (four files), a rewritten SSE activity path that consumes payloads instead of refetching, two field-level bug fixes, a new API client method plus response model for the usage aggregate, two new `AppState` fields, a data-source protocol extraction for testability, deletion of a dead 511-line file, and the test suite above.

## Non-goals

Real token histogram (separate roadmap item: accumulate `TokenMetrics` into hourly buckets in `internal/runtime/usage_aggregate.go`, extend the contract, regenerate OAS). Sensitive-data badges on activity rows. Go/systray parity. Any Web UI change. Rewriting the menu rebuild model into a diffing implementation — it fixes a problem nobody reported, at the cost of risk in the one UI every user sees.

## Follow-ups this unblocks

Completing `ux-audit-macos-sweep` unblocks the `action-log-transparency` epic and the remaining `analytics-default-landing` task, both of which currently sit behind `ux-audit` in the dependency graph.
