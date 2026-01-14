# Research: Expand Activity Log

**Date**: 2026-01-12
**Feature**: 024-expand-activity-log

## 1. New Event Types Implementation

### 1.1 System Start/Stop Events

**Research Task**: Where to emit system_start and system_stop events?

**Findings**:
- Server lifecycle is in `cmd/mcpproxy/main.go`:
  - `runServer()` function handles server startup (line 371)
  - Server start: After `srv.StartServer(ctx)` succeeds (line 588)
  - Server stop: Before `srv.Shutdown()` is called (line 597)
- Signal handling is in a goroutine (lines 560-584)
- Runtime is available via `srv.runtime` after `server.NewServerWithConfigPath()`

**Decision**: Emit events from the Server struct in `internal/server/server.go`:
- `system_start` after successful HTTP listener bind and before returning from `StartServer()`
- `system_stop` at the beginning of `Shutdown()` method

**Rationale**: This keeps event emission close to the actual lifecycle transitions and ensures runtime is available.

**Alternatives Considered**:
1. Emit from `cmd/mcpproxy/main.go` - Rejected: Runtime not directly accessible
2. Emit from runtime initialization - Rejected: Too early, server not ready

### 1.2 Internal Tool Call Events

**Research Task**: How to capture internal tool calls (retrieve_tools, call_tool_*, etc.)?

**Findings**:
- Internal tools are registered in `internal/server/mcp.go` `registerTools()` method (line 266)
- Tool handlers: `handleRetrieveTools`, `handleCallToolRead`, `handleCallToolWrite`, `handleCallToolDestructive`
- Existing activity emission pattern uses `emitActivityToolCallCompleted()` helper
- Current events are emitted for upstream tool calls only, not internal tools

**Decision**: Add activity emission in each internal tool handler:
- Use `internal_tool_call` ActivityType (new constant)
- Include tool_name field to identify which internal tool was called
- Store query/arguments in the arguments field
- Include existing intent metadata for call_tool_* handlers

**Rationale**: Follows existing pattern for tool call activity emission.

**Alternatives Considered**:
1. Wrap all tool handlers with a decorator - Rejected: Overcomplicated, Go doesn't have decorators
2. Use middleware on MCP server - Rejected: mark3labs/mcp-go doesn't support middleware pattern

### 1.3 Config Change Events

**Research Task**: Where are config changes made and how to capture them?

**Findings**:
- Server add/remove/update: `internal/management/server_manager.go`
- Config file changes: `internal/config/watcher.go` hot reload
- CLI operations: `cmd/mcpproxy/upstream_cmd.go`
- REST API: `internal/httpapi/servers.go`
- All modifications go through `storage.Manager` and emit `EventTypeServersChanged`

**Decision**: Emit `config_change` activity when:
- Server is added (via ServerManager.AddServer)
- Server is removed (via ServerManager.RemoveServer)
- Server is updated (via ServerManager.UpdateServer)
- Use the existing `EventTypeServersChanged` subscription in ActivityService

**Rationale**: ActivityService already subscribes to runtime events; extend it to handle config changes.

**Alternatives Considered**:
1. Emit from storage layer - Rejected: Storage doesn't have runtime access
2. Emit from each CLI/API handler - Rejected: Duplication and easy to miss

## 2. Activity Filter Multi-Type Support

### 2.1 Backend Filter Logic

**Research Task**: How does the current filter work?

**Findings**:
- `ActivityFilter` struct in `internal/storage/activity_models.go` (line 66)
- Filter has single `Type string` field
- `Matches()` method does exact string match (line 105)

**Decision**: Change `Type` field to `Types []string` and update `Matches()`:
- Accept comma-separated values in API/CLI
- Parse into slice internally
- Match if record type is in the slice (OR logic)
- Empty slice means no type filter (all types)

**Rationale**: Minimal API change, backwards compatible (single type still works).

### 2.2 API Changes

**Research Task**: How does the API currently handle type filter?

**Findings**:
- `GET /api/v1/activity` in `internal/httpapi/activity.go`
- Query parameter `type` is parsed as single string
- Passed to storage filter

**Decision**:
- Accept comma-separated values: `?type=tool_call,config_change`
- Parse and split on comma
- Document in OpenAPI spec

**Rationale**: Standard pattern for multi-value query parameters.

## 3. Web UI Enhancements

### 3.1 Multi-Select Dropdown Component

**Research Task**: What UI library is used?

**Findings**:
- Vue 3 + DaisyUI + Tailwind CSS
- Current type filter in `Activity.vue` uses a simple `<select>` dropdown
- DaisyUI has `dropdown` component but no native multi-select

**Decision**: Create a custom multi-select dropdown using:
- DaisyUI dropdown base
- Checkboxes for each option
- "X selected" display when multiple selected
- Clear all button

**Rationale**: DaisyUI doesn't have built-in multi-select; custom component follows project patterns.

### 3.2 Intent Column

**Research Task**: Where is the activity table defined?

**Findings**:
- `frontend/src/views/Activity.vue` contains the table
- Intent data is already in activity records (Spec 018)
- Metadata field contains `intent.operation_type` and `intent.reason`

**Decision**: Add Intent column between Details and Status:
- Show operation type icon (read/write/destructive)
- Truncate reason text to ~30 chars
- Tooltip with full text on hover

**Rationale**: Intent data already available; just needs display column.

### 3.3 Sortable Columns

**Research Task**: Is there an existing sorting pattern?

**Findings**:
- Current Activity.vue has no client-side sorting
- Data comes pre-sorted from API (by timestamp desc)
- DaisyUI tables support sort indicators

**Decision**: Implement client-side sorting:
- Click header to sort ascending
- Click again to sort descending
- Visual arrow indicator
- Default: Time descending

**Rationale**: Client-side sorting is sufficient for paginated data (max 100 records per page).

### 3.4 JSON Viewer

**Research Task**: Is there an existing JSON viewer?

**Findings**:
- `frontend/src/components/JsonViewer.vue` exists
- Already has syntax highlighting (vue-json-viewer dependency)
- Used in activity detail panel

**Decision**: Verify JsonViewer has:
- Collapsible sections (already implemented)
- Copy button (may need to add)
- Consistent color scheme

**Rationale**: Existing component handles most requirements.

## 4. CLI Multi-Type Filter

### 4.1 Flag Parsing

**Research Task**: How does the current --type flag work?

**Findings**:
- `activity_cmd.go` uses `--type` flag as string
- Passed to API client
- Validates against known types

**Decision**:
- Accept comma-separated values: `--type=tool_call,config_change`
- Split and validate each type
- Pass to API as comma-separated

**Rationale**: Consistent with API, minimal change.

## 5. Documentation

### 5.1 Docusaurus Structure

**Research Task**: Where are the activity log docs?

**Findings**:
- `docs/features/activity-log.md` - Main feature doc
- `docs/cli/activity-commands.md` - CLI reference
- `docs/api/rest-api.md` - API reference

**Decision**: Update all three files:
- Add new event types table in features doc
- Add multi-type filter examples in CLI doc
- Add Web UI section with screenshots

**Rationale**: Complete documentation coverage.

## Summary of Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| System events location | Server.StartServer/Shutdown | Runtime available, closest to lifecycle |
| Internal tool logging | Per-handler emission | Follows existing pattern |
| Config change tracking | ActivityService subscription | Already subscribes to events |
| Multi-type API format | Comma-separated string | Standard REST pattern |
| Multi-select UI | Custom DaisyUI dropdown | No native component |
| Client-side sorting | In-memory sort | Sufficient for 100 records |
| Intent column position | After Details | Logical grouping |
| Documentation | Update 3 files | Complete coverage |
