# Activity Log QA Report (2025-12-28)

## Environment
- Repo: /Users/user/repos/mcpproxy-go
- mcpproxy version: v0.11.5 (`./mcpproxy --version`)
- Daemon: running (CLI connected via socket)
- Notes: existing activity records from `obsidian-pilot`

## Test Cases

### TC1: List activity (table)
Command:
```bash
./mcpproxy activity list
```
Expected (docs/cli/activity-commands.md, docs/features/activity-log.md):
- Table of recent activity entries.
- IDs shown should be usable with `activity show` (UX expectation: copy/paste from list works).
Actual:
```
ID             TYPE       SERVER          TOOL                  STATUS   DURATION  TIME
01KDHP8MPG61F  tool_call  obsidian-pilot  search_by_date_tool   success  33ms      21 minutes ago
01KDHP8A810JX  tool_call  obsidian-pilot  search_by_regex_tool  success  511ms     21 minutes ago
01KDHP67QK45X  tool_call  obsidian-pilot  search_by_date_tool   success  70ms      23 minutes ago

Showing 3 of 3 records
```
Result: Partial pass. List works but IDs are truncated to 13 chars, which are not accepted by `activity show`.

### TC2: Show activity using ID from list
Command:
```bash
./mcpproxy activity show 01KDHP8MPG61F
```
Expected (docs/cli/activity-commands.md):
- Full activity details for the listed ID.
Actual:
```
Error: activity not found: 01KDHP8MPG61F
Hint: Use 'mcpproxy activity list' to view recent activities
```
Result: Fail. ID from list is not usable for `activity show`.

### TC3: Show activity using full ID from JSON list
Commands:
```bash
./mcpproxy activity list -o json
./mcpproxy activity show 01KDHP8MPG61FT5D671Q9DNXMD
./mcpproxy activity show 01KDHP8MPG61FT5D671Q9DNXMD -o json
```
Expected (docs/cli/activity-commands.md):
- Table output should show ID/type/server/tool/status/duration/timestamp.
- JSON output should contain activity fields directly.
Actual (table):
```
Activity Details
================

ID:           
Type:         
Server:       
Tool:         
Status:       
Duration:     0ms
Timestamp:    
```
Actual (json):
```
{
  "activity": {
    "duration_ms": 33,
    "id": "01KDHP8MPG61FT5D671Q9DNXMD",
    "request_id": "1766899077779672000-obsidian-pilot-search_by_date_tool",
    "response": "{...}",
    "server_name": "obsidian-pilot",
    "session_id": "mcp-session-bd7f5529-93a8-4e0c-baf3-9447f07f07f2",
    "status": "success",
    "timestamp": "2025-12-28T05:17:57.839949Z",
    "tool_name": "search_by_date_tool",
    "type": "tool_call"
  }
}
```
Result: Fail for table output; data exists but is nested under `activity` in JSON, so table rendering reads empty fields.

### TC4: Activity summary
Command:
```bash
./mcpproxy activity summary
```
Expected (docs/cli/activity-commands.md, specs/017-activity-cli-commands/contracts/cli-commands.md):
- Summary metrics for default 24h window.
Actual:
```
Error: API returned status 404: {"success":false,"error":"Activity not found"}

Hint: Use 'mcpproxy activity list' to view recent activities
```
Result: Fail. The summary endpoint appears missing; the request likely routes to `/api/v1/activity/{id}` with `id=summary`.

### TC5: Export activity
Command:
```bash
./mcpproxy activity export --format json
```
Expected (docs/cli/activity-commands.md):
- JSONL stream; full request/response bodies only when `--include-bodies` is provided.
Actual (excerpt):
```
{"id":"01KDHP8MPG61FT5D671Q9DNXMD","type":"tool_call","server_name":"obsidian-pilot","tool_name":"search_by_date_tool","response":"{...}","status":"success","duration_ms":33,"timestamp":"2025-12-28T05:17:57.839949Z",...}
```
Result: Pass (functional stream), but `response` bodies appear even without `--include-bodies`.

### TC6: Tool call via CLI and activity recording
Commands:
```bash
./mcpproxy call tool --tool-name=upstream_servers --json_args='{"operation":"list"}'
./mcpproxy activity list
```
Expected (docs/features/activity-log.md):
- New tool call should appear in activity log if all tool calls are recorded.
Actual:
- No new activity record; list unchanged.
Result: Needs clarification. Either admin calls are intentionally excluded or activity logging is missing for CLI tool calls.

## Issues / Observations
1) Activity summary endpoint missing or misrouted: `/api/v1/activity/summary` returns 404 with "Activity not found".
2) List truncates ULIDs to 13 chars, but `activity show` requires full ID. Docs show short IDs; UX mismatch.
3) `activity show` table output is empty because API returns `data.activity` while CLI expects `data` to be the record. This conflicts with `specs/016-activity-log-backend/contracts/activity-api.yaml`, which defines `data` as `ActivityRecord`.
4) Export `--include-bodies` flag appears ignored; responses are included by default (docs/spec imply bodies are optional).
5) CLI tool calls do not generate activity log entries (needs product decision/clarification).

## References
- docs/cli/activity-commands.md
- docs/features/activity-log.md
- specs/017-activity-cli-commands/contracts/cli-commands.md
- specs/016-activity-log-backend/contracts/activity-api.yaml
