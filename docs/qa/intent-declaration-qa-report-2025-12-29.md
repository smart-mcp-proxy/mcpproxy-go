# Intent Declaration QA Report (2025-12-29)

## Environment
- Repo: /Users/user/repos/mcpproxy-go
- mcpproxy version: v0.11.5
- Daemon: running (socket mode)

## Scope
Intent declaration and tool variants per specs/018-intent-declaration.

## Test Cases

### TC1: CLI subcommands for intent variants
Command:
```bash
./mcpproxy call --help
```
Expected (specs/018-intent-declaration/quickstart.md):
- Subcommands: tool-read, tool-write, tool-destructive.
Actual:
```
Available Commands:
  tool             Call a specific tool on an upstream server or built-in tool (legacy)
  tool-destructive Call a tool with destructive intent
  tool-read        Call a tool with read-only intent
  tool-write       Call a tool with write intent
```
Result: Pass (new variants available). Legacy call tool still present (see Issues).

### TC2: Tool-read call with intent fields
Command:
```bash
./mcpproxy call tool-read --tool-name=obsidian-pilot:search_by_date_tool \
  --json_args='{"date_type":"modified","days_ago":1,"operator":"within"}' \
  --reason="QA retest" --sensitivity=internal
```
Expected:
- Intent operation_type=read enforced automatically.
- Call succeeds for read-only tool.
Actual (trimmed):
```
Intent-Based Tool Call
Tool: obsidian-pilot:search_by_date_tool
Variant: call_tool_read (operation_type=read)
Sensitivity: internal
Reason: QA retest
...
Tool call completed successfully.
```
Result: Pass.

### TC3: Activity list shows intent
Command:
```bash
./mcpproxy activity list --limit 5
```
Expected (specs/018-intent-declaration/quickstart.md):
- INTENT column shows declared operation for tool calls.
Actual (icons removed for ASCII):
```
ID                          SRC  TYPE       SERVER          TOOL                  INTENT  STATUS   DURATION  TIME
01KDM7F0Y0MPRQ3KKNC5FZNKM9  MCP  tool_call  obsidian-pilot  search_by_date_tool   read    success  65ms      just now
...
```
Result: Pass.

### TC4: Activity show includes intent details
Command:
```bash
./mcpproxy activity show 01KDM7F0Y0MPRQ3KKNC5FZNKM9
```
Expected:
- Intent section with tool variant, operation type, sensitivity, and reason.
Actual:
```
Intent Declaration:
  Tool Variant:      call_tool_read
  Operation Type:    read
  Data Sensitivity:  internal
  Reason:            QA retest
```
Result: Pass.

### TC5: Invalid sensitivity rejects intent
Command:
```bash
./mcpproxy call tool-read --tool-name=obsidian-pilot:search_by_date_tool \
  --json_args='{"date_type":"modified","days_ago":1,"operator":"within"}' \
  --sensitivity=secret
```
Expected:
- Validation error for invalid sensitivity.
Actual:
```
Error: ... Invalid intent.data_sensitivity 'secret': must be public, internal, private, or unknown
```
Result: Pass.

### TC6: retrieve_tools returns call_with guidance
Command:
```bash
./mcpproxy call tool --tool-name=retrieve_tools \
  --json_args='{"query":"search notes","limit":2}' -o json
```
Expected (specs/018-intent-declaration/quickstart.md):
- Each tool includes call_with and usage_instructions.
Actual (trimmed):
```
"tools":[{"call_with":"call_tool_write",..."name":"rebuild_search_index_tool"},
         {"call_with":"call_tool_write",..."name":"search_by_date_tool"}],
"usage_instructions":"Use the 'call_with' field to select the appropriate tool variant..."
```
Result: Pass (fields present). Note: read-only tools default to call_tool_write when no server annotations are present.

## Issues / Observations
1) specs/018-intent-declaration/quickstart.md uses `--args` in CLI examples, but CLI uses `--json_args`.
2) Quickstart says legacy call_tool is removed; CLI still exposes `mcpproxy call tool` (marked legacy).
3) retrieve_tools `call_with` defaults to write for obsidian-pilot tools that appear read-only; server annotations likely missing.
4) CLI output uses icons for intent in table output; no flag to disable icons is documented.
5) Invalid intent rejected before tool execution does not appear in activity log (activity list --status error returned none after TC5).

## References
- specs/018-intent-declaration/quickstart.md
- specs/018-intent-declaration/data-model.md
- specs/018-intent-declaration/plan.md
