# Activity Log Web UI

The Activity Log page provides real-time monitoring and analysis of all activity across your MCP servers through a web-based interface.

## Overview

Access the Activity Log by navigating to `/ui/activity` in the MCPProxy web interface or by clicking "Activity Log" in the sidebar navigation.

## Features

### Activity Table

The main view displays activities in a table with the following columns:

| Column | Description |
|--------|-------------|
| Time | Timestamp with relative time display (e.g., "5m ago") |
| Type | Activity type with icon indicator |
| Server | Link to the server that generated the activity |
| Details | Tool name or action description |
| Status | Color-coded badge (green=success, red=error, orange=blocked) |
| Duration | Execution time in ms or seconds |

### Activity Types

| Type | Icon | Description |
|------|------|-------------|
| Tool Call | 🔧 | MCP tool invocations |
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
- **Type**: Tool Call, Policy Decision, Quarantine Change, Server Change
- **Server**: Dynamically populated from activity data
- **Status**: Success, Error, Blocked
- **Date Range**: From/To datetime pickers to filter by time period

Filters combine with AND logic. Active filters are displayed as badges below the filter controls.

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

Query parameters for filtering:
- `type`: Filter by activity type
- `server`: Filter by server name
- `status`: Filter by status
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
