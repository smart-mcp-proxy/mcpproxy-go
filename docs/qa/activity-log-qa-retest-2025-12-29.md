# Activity Log QA Retest (2025-12-29)

## Environment
- Repo: /Users/user/repos/mcpproxy-go
- mcpproxy version: v0.11.5
- Daemon: running (socket mode)

## Test Cases

### TC1: Activity list
Command:
```bash
./mcpproxy activity list
```
Expected (docs/cli/activity-commands.md, docs/features/activity-log.md):
- Table of recent activity entries with IDs and status.
Actual (icons removed for ASCII):
```
ID                          SRC  TYPE       SERVER          TOOL                  INTENT  STATUS   DURATION  TIME
01KDHTB1X8EPJKCPG0TAA06EJD  MCP  tool_call  obsidian-pilot  search_by_regex_tool  -       success  156ms     22 hours ago
01KDHRKV88RJTCR7THY2ZNXE2G  MCP  tool_call  obsidian-pilot  read_note_tool        -       success  6ms       22 hours ago
...
```
Result: Pass. IDs are full length (copy/paste works). INTENT column appears with icon placeholders in actual output.

### TC2: Activity summary
Command:
```bash
./mcpproxy activity summary
```
Expected (docs/cli/activity-commands.md):
- Summary metrics for default 24h window and top servers/tools.
Actual:
```
Activity Summary (last 24h)
===========================

METRIC          VALUE
--------------- --------------------
Total Calls     9
Successful      9 (100.0%)
Errors          0 (0.0%)
Blocked         0 (0.0%)

TOP SERVERS
------------------------------
obsidian-pilot       9 calls

TOP TOOLS
----------------------------------------
obsidian-pilot:read_note_tool  3 calls
obsidian-pilot:search_by_regex_tool 2 calls
obsidian-pilot:search_by_date_tool 2 calls
obsidian-pilot:list_notes_tool 1 calls
obsidian-pilot:list_folders_tool 1 calls
```
Result: Pass.

### TC3: Activity show (table)
Command:
```bash
./mcpproxy activity show 01KDHTB1X8EPJKCPG0TAA06EJD
```
Expected:
- Full details for the activity record.
Actual:
```
Activity Details
================

ID:           01KDHTB1X8EPJKCPG0TAA06EJD
Type:         tool_call
Source:       MCP (AI agent via MCP protocol)
Server:       obsidian-pilot
Tool:         search_by_regex_tool
Status:       success
Duration:     156ms
Timestamp:    2025-12-28T06:29:11.208227Z
Session ID:   mcp-session-bd7f5529-93a8-4e0c-baf3-9447f07f07f2
```
Result: Pass.

### TC4: Activity list filter by intent type
Command:
```bash
./mcpproxy activity list --intent-type read --limit 3
```
Expected (specs/018-intent-declaration/quickstart.md):
- Filters records by intent operation type.
Actual (icons removed for ASCII):
```
ID                          SRC  TYPE       SERVER          TOOL                 INTENT  STATUS   DURATION  TIME
01KDM7F0Y0MPRQ3KKNC5FZNKM9  MCP  tool_call  obsidian-pilot  search_by_date_tool  read    success  65ms      just now

Showing 1 of 1 records
```
Result: Pass.

### TC5: Activity export (CSV)
Command:
```bash
./mcpproxy activity export --format csv | head -n 2
```
Expected (docs/cli/activity-commands.md):
- CSV header and rows without embedding response bodies.
Actual:
```
id,type,source,server_name,tool_name,status,error_message,duration_ms,timestamp,session_id,request_id,response_truncated
01KDM7F0Y0MPRQ3KKNC5FZNKM9,tool_call,cli,obsidian-pilot,search_by_date_tool,success,,65,2025-12-29T04:57:01Z,,1766984221549032000-obsidian-pilot-search_by_date_tool,false
```
Result: Pass.

## Issues / Observations
1) docs/cli/activity-commands.md does not mention the new SRC column or the --intent-type filter.
2) INTENT is represented with icons in table output; there is no documented flag to disable icons in activity list.
3) activity export does not expose an --intent-type filter flag in CLI help (only list supports it).

## References
- docs/cli/activity-commands.md
- docs/features/activity-log.md
- specs/018-intent-declaration/quickstart.md
