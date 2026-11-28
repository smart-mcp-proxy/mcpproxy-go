---
title: "Activity Log Page"
sidebar_label: "Activity Log"
description: "Real-time monitoring and analysis of MCP server activity in the Web UI."
---

# Activity Log Web UI

The Activity Log page provides real-time monitoring and analysis of all activity across your MCP servers through a web-based interface.

## Overview

Access the Activity Log by navigating to `/ui/activity` in the MCPProxy web interface or by clicking "Activity Log" in the sidebar navigation.

## Features

### Activity Table

The main view displays activities in a sortable table with the following columns:

| Column | Sortable | Description |
|--------|----------|-------------|
| Time | Yes | Timestamp with relative time display (e.g., "5m ago"). Default sort: newest first |
| Type | Yes | Activity type with icon indicator |
| Server | Yes | Link to the server that generated the activity |
| Details | No | Tool name or action description |
| Intent | No | Operation type badge (read/write/destructive) with tooltip showing full intent details |
| Status | Yes | Color-coded badge (green=success, red=error, orange=blocked) |
| Duration | Yes | Execution time in ms or seconds |

**Sorting**: Click any sortable column header to sort. Click again to toggle between ascending/descending. The current sort column and direction are indicated with an arrow.

### Activity Types

| Type | Icon | Description |
|------|------|-------------|
| Tool Call | 🔧 | MCP tool invocations to upstream servers |
| System Start | 🚀 | MCPProxy server startup events |
| System Stop | 🛑 | MCPProxy server shutdown events |
| Internal Tool Call | ⚙️ | Internal proxy tool calls (`retrieve_tools`, `call_tool_*`, `code_execution`, `upstream_servers`, etc.) |
| Config Change | ⚡ | Configuration changes (server added/removed/updated) |
| Policy Decision | 🛡️ | Security policy evaluations |
| Quarantine Change | ⚠️ | Server quarantine status changes |
| Server Change | 🔄 | Server enable/disable/restart events |

### Real-time Updates

Activities appear automatically via Server-Sent Events (SSE):
- New activities are prepended to the list
- Completed activities update their status and duration
- The connection status indicator shows live/disconnected state

### Filtering

Filter activities by:
- **Type**: Multi-select dropdown with checkboxes. Select one or more types to filter (uses OR logic between selected types):
  - Tool Call, System Start, System Stop, Internal Tool Call, Config Change, Policy Decision, Quarantine Change, Server Change
- **Server**: Dynamically populated from activity data
- **Status**: Success, Error, Blocked
- **Date Range**: From/To datetime pickers to filter by time period

Type filters combine with OR logic (show any selected type). Other filters combine with AND logic. Active filters are displayed as badges below the filter controls.

### Activity Details

Click any row to open the detail drawer showing:
- Full metadata (ID, type, timestamp, server, tool, duration, session, source)
- Request arguments displayed in a syntax-highlighted JSON viewer with:
  - Color-coded keys (primary color)
  - Green strings
  - Orange numbers
  - Purple booleans
  - Red null values
  - Copy-to-clipboard button with byte size indicator
- Response data in the same syntax-highlighted JSON viewer with truncation indicator
- Error message (for failed activities)
- Intent declaration (if present)

### Sessions and the failure timeline

The **Sessions** tab (`/ui/activity?view=sessions`) lists [work sessions](../features/activity-log.md#work-sessions-and-the-failure-timeline): one row per agent run, read from `GET /api/v1/work-sessions`. A run is told apart by its credential, its client and its project: two scripts that use the same API key over REST within 30 minutes of each other are one session, unless each sends its own `X-MCPProxy-Run` name. The run name is stored and shown as it was sent, to everyone who can see the session: do not put a secret or a signed URL in it.

| Cell | What it shows |
|------|---------------|
| Agent token | The token the run used, linked to the agent tokens list. A dash when the call used the API key or a client credential. A column of its own on a wide screen; otherwise the first line of the next cell. |
| Client · Project | The client (or the calling surface: `rest`, `cli`, `webui`, `tray`), the run name sent as `X-MCPProxy-Run`, and the project when the client disclosed one. "Start trimmed" marks a session whose first rows retention has removed. |
| Started · Duration | Start time, duration, and whether the session is still active. |
| Calls | The number of attempts. It is a link to the Tool calls view filtered by this session; the link includes the calls MCPProxy refused and drops the page's time range, which this list does not apply. |
| Failures | The number of failures, with the policy blocks among them on a second line. |
| Last failure · Reason | The error class of the last failure, its short reason and `server:tool`, and when it happened, with the first failure under it when there was more than one. "Rebuilt from rows" in the client cell marks a session whose counts cover only the rows still in the log. Below 1280 px these lines fold into the client cell. |
| Timeline | Opens the session's timeline. |

Above the table:

- **Failed only** keeps the sessions that have at least one failure. With it on, **Include blocked** decides whether a call MCPProxy itself refused (quarantine, a profile, a rate limit) counts as a failure. Switch it off to see only sessions where something else failed.
- **Sort** orders by last activity or by last failure, newest first. With "Failed only" on, the list reads newest failure first unless you choose otherwise.
- The filter, the order and the paging are applied by the server, so the list is exact however long the log is. The profile, client and token filters of the page ("View as", the Filters popover) narrow the list too, with two exceptions that a line under the toolbar names when they apply: "unattributed" has no meaning for work sessions, and a signed-in user of the server edition who is not an administrator cannot filter by profile. The page reads the list again every 30 seconds while Auto-refresh is on.

The filter and the order are part of the address (`failed=true`, `include_blocked=false`, `sort=last_failure` or `sort=last_activity` when it is not the default), so a filtered list can be bookmarked. "Clear Filters" in the Filters popover switches "Failed only" off as well.

A session that never failed opens at its most recent row. Below 640 px the last failure and its reason move under the client name.

The table of MCP connections that this tab used to show (capabilities, token count, "Tools it sees", "View Activity") is in the **Client connections** section under the list. It is collapsed until you open it.

**Timeline.** A row's Timeline button opens `/ui/activity?view=sessions&ws=<session id>&at=<position>`. The address names the session and the row the page is positioned at, so it can be shared and reloaded. Without `at` the page opens at the first failure that is still in the log, with the 20 rows before it and the 20 after. A session that never failed opens at its most recent row. A session whose failed rows were all removed by retention still shows its lifetime failure count and opens at its earliest remaining row. `at=failure:<n>` counts the failures that are in the log now, so the same address can open a different row, or none, once retention has removed an earlier failure; to share one particular row, use `at=row:<activity id>` (the address the page has after "Open in session timeline" from a call's details).

- A failed call has a red bar and background; a call MCPProxy refused is amber and says "Blocked". The row the page is positioned at has an outline.
- A call made through a built-in tool is stored as two rows: the upstream call, which is the failure, and the built-in call around it (`call_tool_read` and the like), which ended in the same error. The second one shows one line of that error; its details have the whole text.
- An **upstream event** (connection lost, process exited, token refresh failed, login required, connection restored) is a row of its own with a plug icon, the server name and the fixed message; a problem also has its `MCPX_*` code (a restored connection has none). It is context, placed by time: what happened to a server the session called, at that moment. It is often, but not necessarily, the reason a later call to that server failed; an event of one server can sit above a call to another. Repeats within a minute are one row at the time of the first, with the count and the time of the last.
- **Session started** and **Session ended** rows mark the two ends.
- Times are printed to the millisecond; hover a time for the date.
- **Previous failure** and **Next failure** step through the failures still in the log, and the label says "failure N of M". **Latest rows** jumps to the end of the session.
- **Load earlier rows** and **Load later rows** widen the page around the same position. A step to another failure keeps that width. A page opened at the latest rows of a session that is still running is widened around the row it shows (the address changes to `at=row:<id>`), so new rows do not push the ones on screen away.
- **Refresh** reads the same page again, for a session that is still running.
- The facts at the top repeat what the list row shows and add the first and the last failure to the millisecond. The profile, client and token filters of the page narrow the list of sessions; they do not change a timeline, so the Filters control is put away while a timeline is open and is back on the list.
- The arrow at the end of a row opens its details. The arguments and the response are fetched only then.
- **Copy as CLI command** gives the `mcpproxy activity session <id> ...` command that prints the same position and the same rows in a terminal. Once the arguments or the response of a row are on screen here, the command has `--include-bodies`, which prints the arguments and responses of every row of the page, not only of the rows opened in the browser: keep that in mind before pasting the output into a report.
- **Tool calls of this session** opens the Tool calls view filtered by the session, with the calls MCPProxy refused included (a Type filter names the three row types) and without the page's time range, which a timeline does not apply. Following this link, or the Calls count of a list row, to a session other than the one the Tool calls view was showing also resets that view's "Sensitive data" and "View sub-calls" filters, which are not part of the address. From there, a call's detail panel has **Open in session timeline**, which opens the timeline at that call.

**What retention changes.** A session leaves the list once its last activity is older than `session_retention_days` (7 by default); the cleanup pass applies this, so a long run that is still active stays listed. While it is listed, its counts are lifetime facts and stay exact. There are two exceptions. A session marked **Rebuilt from rows** (in the list and in its timeline) lost its summary, which was rebuilt from the rows still in the log, so its counts cover those rows only; the mark is shown to administrators, an agent token sees such a session without it. And a long-lived connection that goes quiet for longer than the retention window and then continues under the same session id starts a new summary: the session is listed again with the counts of what happened since, and nothing marks it. Under the record or size cap a whole session can be removed before that, oldest last activity first; its address then shows "Session not found". When retention has removed rows, the timeline says so instead of showing a shorter history as if it were complete:

- "The start of this session is no longer in the log" appears at the top when the first rows are gone, and the list row carries "Start trimmed".
- "This session had N failures in all. M are still in the log" appears when failures were removed. The failure steps then count the M that are left.
- A row that was kept without its arguments and response says so when you open its details.

See [retention](../features/activity-log.md#configuration) for the settings. An address that names a session that does not exist, or one you may not see, shows "Session not found" with a link back to the list. An agent token sees only its own sessions, and only the part of each that touched servers in its reach; the profile, the connection count and the "start trimmed" marker are shown to administrators only.

### Pagination

Navigate through large datasets with:
- First/Previous/Next/Last page buttons
- Page size selector (10, 25, 50, 100 per page)
- "Showing X-Y of Z" count display

### Export

Export filtered activities to JSON or CSV format:
1. Apply desired filters
2. Click the Export dropdown
3. Select format (JSON or CSV)
4. File downloads via browser

### Dashboard Widget

The Dashboard includes an Activity Summary widget showing:
- 24-hour totals (total, success, errors)
- 5 most recent activities
- "View All" link to the Activity Log page

### Auto-refresh Toggle

Control real-time updates:
- Toggle on (default): Activities update automatically via SSE
- Toggle off: Manual refresh required, use the refresh button

## API Endpoints

The Activity Log uses these REST API endpoints:

| Endpoint | Description |
|----------|-------------|
| `GET /api/v1/activity` | List activities with filtering |
| `GET /api/v1/activity/{id}` | Get activity details |
| `GET /api/v1/activity/summary` | Get 24h statistics |
| `GET /api/v1/activity/export` | Export activities (JSON/CSV) |
| `GET /api/v1/work-sessions` | List work sessions (the Sessions tab) |
| `GET /api/v1/work-sessions/{id}/timeline` | One page of a session's timeline |

Query parameters for filtering:
- `type`: Filter by activity type (comma-separated for multiple, e.g., `?type=tool_call,config_change`)
- `server`: Filter by server name
- `status`: Filter by status
- `intent_type`: Filter by intent operation type (`read`, `write`, `destructive`)
- `limit`: Maximum records to return
- `offset`: Pagination offset

## SSE Events

Real-time updates are received via these SSE event types:
- `activity.tool_call.started`: Tool call initiated
- `activity.tool_call.completed`: Tool call finished
- `activity.policy_decision`: Policy evaluation result
- `activity`: Generic activity event

## Intent Declaration

For activities with intent declarations (Spec 018), the detail panel displays:
- Operation type with icon (📖 read, ✏️ write, ⚠️ destructive)
- Data sensitivity level
- Reason for the operation

See [CLI Activity Commands](../cli/activity-commands.md) for command-line access to activity data.
