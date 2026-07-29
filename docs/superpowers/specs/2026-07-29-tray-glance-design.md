# Tray Glance — compact activity, clients, and 24h histogram in the macOS menu

**Status:** Design (2026-07-29)
**Epic:** `ux-audit` → `ux-audit-macos-sweep` (roadmap.yaml, P0); folds in `action-log-transparency` → `action-log-tray-menu`
**Reference:** [CodexBar](https://github.com/steipete/CodexBar) — rich SwiftUI sections hosted inside a plain `NSMenu`

## Problem

The macOS tray menu shows what MCPProxy *is* (servers, profiles, version, update state) but nothing about what it is *doing*. To see whether an agent is calling tools, which client is connected, or whether calls are failing, the user must open the Web UI. The activity log is a headline feature of the product and is invisible from the surface people actually keep open.

## Goal

A compact glance section at the top of the tray menu answering three questions at a glance, without turning the menu into a dashboard:

1. What just happened? — the five most recent tool calls
2. Who is connected? — active MCP clients
3. How busy has it been? — calls per hour over the last 24h (on demand)

Design constraints, in priority order: never irritate (the menu stays a menu — fast, keyboard-navigable, native), never lie (no stale data presented as live, no metric labelled as something it is not), never regress menu-open cost (spec 048's zero-network menu open is an invariant).

## Decisions

| Question | Decision | Rationale |
|---|---|---|
| Presentation | `NSMenu` with SwiftUI views hosted in `NSMenuItem.view` | CodexBar's approach (verified: 31 files use `NSMenuItem`, zero `NSPopover`/`MenuBarExtra`; charts via SwiftUI Charts). A popover on every icon click penalises the common case — quick status check or profile switch. The tray already uses `NSHostingView` (`MCPProxyApp.swift:231,277`), so this is not a new pattern. |
| What is visible on open | Activity + clients; histogram in a submenu | The histogram needs a separate `/activity/usage` fetch. Putting it on the open path would undo spec 048's zero-network menu open. |
| Histogram metric | Calls per hour (errors highlighted) | Hourly token series does not exist. `UsageTimeBucket` carries `calls`, `errors`, `total_resp_bytes`; the endpoint reports `token_source: "bytes"` — a size proxy. Real tokenisation (`cl100k_base`) exists but only accumulates per session (`MCPSession.total_tokens`), never per activity record. Plotting calls is exact and needs no storage change. A real token histogram is a separate roadmap item. |
| Liveness | Fix the SSE feed as part of this work | `CoreProcessManager.swift:683` matches `case "activity"`, which the core never emits (real names: `activity.tool_call.completed`, `activity.internal_tool_call.completed`, …), so activity is 30s-polled today. A section promising "what's happening now" on a 30-second delay is a section users stop trusting. Spec 048 already set a ≤50 ms SSE-reactivity target that activity silently falls outside of. |
| Integration | Cached views + extract into a component | `rebuildMenu()` (`MCPProxyApp.swift:523`, ~300 lines) calls `removeAllItems()` on every state change without dismissing the menu. Plain text items tolerate this; a hosted SwiftUI view would be destroyed and rebuilt under the user's cursor — flicker and lost hover state, and more often once SSE makes updates frequent. |

## Architecture

New component under `native/macos/MCPProxy/MCPProxy/Menu/Glance/`:

- **`GlanceSection.swift`** — the only thing `MCPProxyApp` talks to. Interface: `func items(for state: AppState) -> [NSMenuItem]`. Constructs its `NSMenuItem`s and `NSHostingView`s once, stores them, and returns the same instances on every rebuild; SwiftUI re-renders them from `@Published` state. This is what makes `removeAllItems()` harmless for hosted views.
- **`GlanceView.swift`** — SwiftUI layout for the two sections: five activity rows, then the client list.
- **`ActivityHistogramView.swift`** — SwiftUI Charts bar chart, calls per hour over 24h, errors as a distinct segment. Built only when its submenu opens.
- **`GlanceFormatting.swift`** — status icon, row label composition, relative time, middle-truncation. Salvaged from the dead `Menu/TrayMenu.swift` (511 lines, referenced nowhere, already contains `activityIcon(for:)`, `activitySummaryText(for:)`, `relativeTime(_:)`); that file is deleted afterwards.

Changes to existing files are deliberately small:

- `MCPProxyApp.swift` — `rebuildMenu()` inserts `glance.items(for: appState)` after the status header and before the "Needs Attention" block, plus one submenu item for the histogram. Roughly five lines; no new layout code in an already 1120-line file.
- `CoreProcessManager.swift:683` — match the `activity.` event-name prefix instead of the literal `activity`.
- `APIClient.swift:341,353` — decode `last_activity` (the field the API emits) instead of `last_active`.

Data already exists in `AppState`: `recentActivity: [ActivityEntry]`, `recentSessions: [MCPSession]`, `tokenMetrics`, `activityVersion`. `ActivityEntry` already decodes `type`, `serverName`, `toolName`, `status`, `errorMessage`, `durationMs`, `timestamp`, `hasSensitiveData`. This work is rendering plus two feed fixes, not new plumbing.

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

Row anatomy: status icon (shape + colour), `server:tool` for upstream calls or the built-in's name for discovery/execution calls, and relative age right-aligned. A failed row appends the first clause of `errorMessage`, truncated to fit one line; the untruncated message is in the tooltip.

UI strings are English, matching the rest of the menu. Rows follow the macOS design guide already in the repo (`docs/superpowers/specs/macos-design-guide.md`): semantic colours, SF Symbols, no colour-only status encoding — the status icon carries shape as well as colour.

## Data flow

**Menu open performs zero network requests.** Everything renders from `AppState`.

**Activity** arrives by SSE push once the `activity.` prefix match is fixed; the existing 30-second poll stays as a safety net for missed events. The poll narrows to `GET /api/v1/activity?type=tool_call,internal_tool_call&limit=10`. Note the API already suppresses successful `call_tool_read/write/destructive` wrapper records by default (`ExcludeCallToolSuccess`) so they do not double-count the paired upstream record — the row shows `github:create_issue`, not its wrapper.

The `internal_tool_call` type also covers management built-ins (`upstream_servers`, `quarantine_security`) that the user considers internal noise, and the API cannot filter by tool-name set. The component therefore keeps a three-entry display allowlist (`retrieve_tools`, `code_execution`, `describe_tool`) and takes the first five matching records out of the ten fetched; if fewer than five match, it shows what matched rather than padding the list. This is presentation policy, not tray-held state, so it stays within the "tray holds no state" rule (CLAUDE.md).

**The header summary line** ("12 calls in the last hour") comes from `GET /api/v1/activity/summary?period=1h` — a single pre-aggregated response — added to the existing 30-second background refresh loop, never to the menu-open path. The client count beside it is the length of the filtered active-session list, so header and Clients section always agree.

**Clients** come from the existing `GET /api/v1/sessions?limit=5` poll, filtered client-side to `status == "active"` (no server-side status parameter exists). Documented caveat: per spec 082, handshake-only sessions are not persisted, so an idle background agent does not appear until its first call.

**Histogram** is fetched only when its submenu opens, via that submenu's delegate — never on the main menu open path. It refetches on every submenu open rather than caching client-side; the endpoint (`GET /api/v1/activity/usage?window=24h`) serves an in-memory snapshot behind a short TTL cache and never scans the log, so repeated opens are cheap and always current. While the first response is in flight the submenu shows a muted loading row. Its timeline buckets are global by contract (not filtered by server/tool/status), so the label is "Activity (24h)" with no implication of filtering.

**Clicks**: an activity row opens the Web UI activity log filtered by that record's `request_id`; a client row opens it filtered by that session. URL construction only — no backend work.

## States

| Situation | Behaviour |
|---|---|
| Core stopped or disconnected | The whole glance block is hidden. Stale numbers presented as current are worse than nothing; the menu already shows the error and a Retry item. |
| No activity yet | One muted row, "No tool calls yet" — not an empty section with a header. |
| No active clients | One muted row, "No connected clients". |
| Histogram fetch fails | Muted error row inside the submenu; the rest of the menu is unaffected. |
| Long tool names | Middle-truncated, keeping the server prefix and the tail of the tool name; full text in the tooltip. |

## Testing

Swift unit tests in the existing `swift-test` CI job:

- **Regression, SSE naming**: `activity.tool_call.completed` reaches the activity handler (fails today).
- **Regression, session decoding**: `last_activity` decodes into the model (nil today).
- Formatting: relative time, row label composition, middle truncation.
- Display allowlist: management built-ins excluded, the three discovery/execution built-ins included, upstream `tool_call` records always included.
- Visibility rules: block hidden when the core is stopped; empty-state rows for no activity / no clients.
- **Invariant, spec 048**: a stub API client counting requests records zero calls when the main menu opens.

Visual verification by screenshotting the menu with `mcpproxy-ui-test` per `docs/development/macos-tray.md` (requires Screen Recording permission).

## Non-goals

Real token histogram (separate roadmap item: accumulate `TokenMetrics` into hourly buckets in `internal/runtime/usage_aggregate.go`, extend the contract, regenerate OAS). Sensitive-data badges on activity rows. Go/systray parity. Web UI changes. Rewriting the menu rebuild model into a diffing implementation — it fixes a problem nobody reported, at the cost of risk in the one UI every user sees.

## Follow-ups this unblocks

Completing `ux-audit-macos-sweep` unblocks the `action-log-transparency` epic and the remaining `analytics-default-landing` task, both of which currently sit behind `ux-audit` in the dependency graph.
