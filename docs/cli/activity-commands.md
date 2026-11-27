---
id: activity-commands
title: Activity Commands
sidebar_label: Activity Commands
sidebar_position: 3
description: CLI commands for querying and monitoring activity logs
keywords: [activity, logging, audit, cli, monitoring, observability]
---

# Activity Commands

The `mcpproxy activity` command group provides CLI access to activity logs for debugging, monitoring, and compliance.

## Overview

```
mcpproxy activity
├── list          List activity records with filtering
├── watch         Watch activity stream in real-time
├── show <id>     Show activity details
├── summary       Show activity statistics
├── export        Export activity records
├── sessions      List work sessions with their failure facts
└── session <id>  Show one work session's timeline around its failures
```

## Output Formats

All activity commands support multiple output formats:

| Flag | Description |
|------|-------------|
| `--output table` | Human-readable table (default) |
| `--output json` or `--json` | JSON output for scripting |
| `--output yaml` | YAML output |

You can also set the default via environment variable:

```bash
export MCPPROXY_OUTPUT=json
```

---

## activity list

List activity records with filtering and pagination.

### Usage

```bash
mcpproxy activity list [flags]
```

### Flags

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--type` | `-t` | | Filter by type (comma-separated for multiple): `tool_call`, `system_start`, `system_stop`, `internal_tool_call`, `config_change`, `policy_decision`, `quarantine_change`, `server_change`, `credential_broker` |
| `--server` | `-s` | | Filter by server name |
| `--tool` | | | Filter by tool name |
| `--status` | | | Filter by status: `success`, `error`, `blocked`, `rejected` (shed by a concurrency limit, see [Concurrency Limits](../configuration/config-file.md#concurrency-limits--request-queueing)) |
| `--intent-type` | | | Filter by intent operation type: `read`, `write`, `destructive` |
| `--request-id` | | | Filter by HTTP request ID for log correlation |
| `--parent-id` | | | List child tool calls of a `code_execution` activity (value = the parent record `request_id`) |
| `--error-class` | | | Filter failed tool calls by class: `network`, `timeout`, `http`, `jsonrpc`, `tool_error`, `session_terminated`, `auth`, `proxy_policy`, `proxy_internal`, `cancelled` |
| `--fault-domain` | | | Filter failed tool calls by fault domain: `upstream`, `proxy`, `client` |
| `--no-icons` | | | Disable emoji icons in output (use text instead) |
| `--session` | | | Filter by MCP session ID |
| `--profile` | | | Filter by the profile in effect when the call ran; `-` selects records with none |
| `--client` | | | Filter by client id; `-` selects records with none |
| `--token` | | | Filter by the token name in effect; `-` selects records with none. `--agent` is a deprecated alias (prints a notice; conflicting values exit `1`) |
| `--client-name` | | | Filter by the client's self-reported name (advisory, never authoritative) |
| `--view` | | `all` | Filter by view: `calls` (tool_call, internal_tool_call), `system` (every other type), `all`; overridden by `--type` |
| `--start-time` | | | Filter records after this time (RFC3339) |
| `--end-time` | | | Filter records before this time (RFC3339) |
| `--from` | | | Alias of `--start-time`; also accepts a relative shorthand: `-1h`, `-24h`, `-7d`, `-30d`, or any `-<N>m`/`-<N>h`/`-<N>d` |
| `--to` | | | Alias of `--end-time`; same relative shorthand as `--from` |
| `--limit` | `-n` | 50 | Max records to return (1-100) |
| `--offset` | | 0 | Pagination offset |

A `--tool` value of the form `server:tool` (e.g. `github:create_issue`) is
split into `--server github --tool create_issue` — the REST filters compare
bare tool names, so sending `server:tool` verbatim would match nothing. An
explicit `--server` that disagrees with the prefix is a contradiction: the
command exits `1` with `--server <a> conflicts with the server in --tool
<b>:<t>` before making any request, rather than silently keeping one value
and dropping the other.

### Examples

```bash
# List recent activity
mcpproxy activity list

# List last 10 tool calls
mcpproxy activity list --type tool_call --limit 10

# List errors from github server
mcpproxy activity list --server github --status error

# List only read operations
mcpproxy activity list --intent-type read

# List destructive operations for audit
mcpproxy activity list --intent-type destructive --limit 100

# Find activity by request ID (useful for error correlation)
mcpproxy activity list --request-id a1b2c3d4-e5f6-7890-abcd-ef1234567890

# List the sub-calls one code_execution made (value = the parent's request_id)
mcpproxy activity list --parent-id a1b2c3d4-e5f6-7890-abcd-ef1234567890

# List activity as JSON
mcpproxy activity list -o json

# List multiple event types (comma-separated)
mcpproxy activity list --type tool_call,config_change

# List system lifecycle events
mcpproxy activity list --type system_start,system_stop

# List internal tool calls (retrieve_tools, call_tool_*, upstream_servers, etc.)
mcpproxy activity list --type internal_tool_call

# List configuration changes
mcpproxy activity list --type config_change

# List activity from today
mcpproxy activity list --start-time "$(date -u +%Y-%m-%dT00:00:00Z)"

# Paginate through results
mcpproxy activity list --limit 20 --offset 40
```

### Output (Table)

```
ID               SRC  TYPE         SERVER      TOOL           CLIENT  PROFILE              INTENT  STATUS   DURATION   TIME
01JFXYZ123ABC    MCP  tool_call    github      create_issue   cursor  work-full (binding)  write   success  245ms      2 min ago
01JFXYZ123ABD    CLI  tool_call    filesystem  read_file      -       -                    read    error    125ms      5 min ago
01JFXYZ123ABE    MCP  policy       private     get_secret     ~Zed    -                    -       blocked  0ms        10 min ago

Showing 3 of 150 records (page 1)
```

**Client and Profile columns**: `CLIENT` is the client id the call was bound to; when a record carries only the client's self-reported name it is shown as `~<name>` (advisory: a client can claim any name); otherwise `-`. `PROFILE` is the profile in effect with its source in parentheses (`pin`, `binding`, `url`, `session`, `anonymous`, `none`), or `-`. Records written before Profiles v3 show `-`. `activity show` also prints the `Client`, `Profile`, `Token` and `Block reason` of a record.

**Intent Column**: Shows the declared operation type (`read`, `write`, `destructive`) or `-` if no intent was declared.

**Source Indicators:**
- `MCP` - AI agent call via MCP protocol
- `CLI` - Direct CLI command (`mcpproxy call tool-read --tool-name <server:tool>` and its `tool-write` and `tool-destructive` siblings)
- `API` - REST API call

### Output (JSON)

```json
{
  "activities": [
    {
      "id": "01JFXYZ123ABC",
      "type": "tool_call",
      "source": "mcp",
      "server_name": "github",
      "tool_name": "create_issue",
      "status": "success",
      "duration_ms": 245,
      "timestamp": "2025-01-15T10:30:00Z",
      "request_id": "a1b2c3d4-e5f6-7890-abcd-ef1234567890"
    }
  ],
  "total": 150,
  "limit": 50,
  "offset": 0
}
```

### Exit Codes

| Code | Meaning |
|------|---------|
| 0 | Success |
| 1 | Error fetching activity |
| 2 | Invalid filter parameters |

---

## activity watch

Watch activity stream in real-time via SSE.

### Usage

```bash
mcpproxy activity watch [flags]
```

### Flags

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--type` | `-t` | | Filter by type (comma-separated for multiple): `tool_call`, `system_start`, `system_stop`, `internal_tool_call`, `config_change`, `policy_decision`, `quarantine_change`, `server_change`, `credential_broker` |
| `--server` | `-s` | | Filter by server name |
| `--profile`, `--client`, `--token`, `--client-name` | | | Same scope filters as `activity list`, applied to the `attribution` of each streamed event (`-` selects events with none); `--agent` is a deprecated alias of `--token` |
| `--view` | | `all` | Filter by view: `calls`, `system`, `all`; overridden by `--type` |
| `--from` | | | Only print records at/after this time (RFC3339 or relative: `-1h`, `-24h`, `-7d`, `-30d`) |
| `--to` | | | Stop watching once this time has passed (RFC3339 or relative); `watch` exits cleanly instead of reconnecting once `--to` is in the past |

### Examples

```bash
# Watch all activity
mcpproxy activity watch

# Watch only tool calls from github
mcpproxy activity watch --type tool_call --server github

# Watch system and config events
mcpproxy activity watch --type system_start,system_stop,config_change

# Watch with JSON output (NDJSON)
mcpproxy activity watch -o json

# Press Ctrl+C to stop
```

### Output (Table - Streaming)

```
[10:30:45] [MCP] github:create_issue ✓ 245ms
[10:30:46] [CLI] filesystem:write_file ✗ 125ms permission denied
[10:30:47] [MCP] private:get_data ⊘ BLOCKED policy:no-external
^C
Received interrupt, stopping...
```

Source indicators (`[MCP]`, `[CLI]`, `[API]`) show how the tool call was triggered.

### Output (JSON - NDJSON)

```json
{"type":"activity.tool_call.completed","id":"01JFXYZ123ABC","source":"mcp","server":"github","tool":"create_issue","status":"success","duration_ms":245}
{"type":"activity.tool_call.completed","id":"01JFXYZ123ABD","source":"cli","server":"filesystem","tool":"write_file","status":"error","error":"permission denied"}
```

### Behavior

- Automatically reconnects on connection loss (exponential backoff)
- Exits cleanly on SIGINT (Ctrl+C) or SIGTERM
- Buffers high-volume events to prevent terminal flooding
- **Filters out `call_tool_*` internal tool calls** to avoid duplicates (every dispatch — successful, failed, or shed — has a corresponding `tool_call` or rejection record)

### Exit Codes

| Code | Meaning |
|------|---------|
| 0 | Normal exit (user interrupt) |
| 1 | Connection error (after retries exhausted) |

---

## activity show

Show full details of a specific activity record.

### Usage

```bash
mcpproxy activity show <id> [flags]
```

### Arguments

| Argument | Description |
|----------|-------------|
| `id` | Activity record ID (ULID format) |

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--include-response` | false | Show full response (may be large) |

### Examples

```bash
# Show activity details
mcpproxy activity show 01JFXYZ123ABC

# Show with full response body
mcpproxy activity show 01JFXYZ123ABC --include-response

# Show as JSON
mcpproxy activity show 01JFXYZ123ABC -o json
```

### Output (Table)

```
Activity Details
================

ID:           01JFXYZ123ABC
Type:         tool_call
Source:       MCP (AI agent via MCP protocol)
Server:       github
Tool:         create_issue
Status:       success
Duration:     245ms
Timestamp:    2025-01-15T10:30:00Z
Session ID:   mcp-session-abc123

Arguments:
  {
    "title": "Bug report: Login fails",
    "body": "When clicking login...",
    "labels": ["bug", "priority-high"]
  }

Response:
  Issue #123 created successfully
  URL: https://github.com/owner/repo/issues/123
```

**Code execution links:** when the record is a `code_execution` parent, the
detail view prints a hint (`Use 'mcpproxy activity list --parent-id <request_id>'
to see sub-calls`). When the record is one of those sub-calls, it prints a
`Parent ID:` line instead. See
[Drill into a `code_execution`](#drill-into-a-code_execution).

**Source Values:**
- `MCP (AI agent via MCP protocol)` - Tool called by AI agent
- `CLI (CLI command)` - Tool called via a `mcpproxy call tool-read --tool-name <server:tool>` (or `tool-write`, `tool-destructive`) CLI command
- `API (REST API)` - Tool called via REST API directly

### Failure classification

A failed tool call adds an `Error class` line to the table (for example
`Error class:  http (fault: upstream, HTTP 502)`), and the same data appears as
`error_class`, `fault_domain` and `upstream_http_status` in `-o json` and
`-o yaml`. The fields are absent on successful calls and on older records. See
[Failure classification](../features/activity-log.md#failure-classification-error_class-and-fault_domain)
for the class table.

```bash
# Every call the upstream answered with an HTTP error, as JSON
mcpproxy activity list --error-class http -o json | jq '.activities[] | {server_name, tool_name, upstream_http_status}'

# Failures that were mcpproxy's own doing, not the upstream's
mcpproxy activity list --fault-domain proxy
```

### Exit Codes

| Code | Meaning |
|------|---------|
| 0 | Success |
| 1 | Activity not found |
| 2 | Invalid ID format |

---

## activity summary

Show aggregated activity statistics for a time period.

### Usage

```bash
mcpproxy activity summary [flags]
```

### Flags

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--period` | `-p` | 24h | Time period: `1h`, `24h`, `7d`, `30d` |
| `--by` | | | Group by: `server`, `tool`, `status` |
| `--profile`, `--client`, `--token` | | | Count only records made under this profile, client id or token name (`-` selects records with none); `--agent` is a deprecated alias of `--token`. `--client-name` is accepted but always rejected: `client_name is not supported on this endpoint; filter by client` |
| `--from` | | | Alias of `--period`, as a relative shorthand: `-1h`, `-24h`, `-7d`, `-30d` (any other value is rejected — summary has no arbitrary range) |

`--to` is accepted for symmetry with `list`/`watch`/`export` but always
rejected on `summary`; use `--period` or `--from`.

### Examples

```bash
# Show 24-hour summary
mcpproxy activity summary

# Show weekly summary
mcpproxy activity summary --period 7d

# Show summary grouped by server
mcpproxy activity summary --by server

# Show as JSON
mcpproxy activity summary -o json
```

### Output (Table)

```
Activity Summary (last 24h)
===========================

METRIC          VALUE
───────         ─────
Total Events    150
Calls           128 (3.9% failed)
Successful      142 (94.7%)
Errors          5 (3.3%)
Blocked         3 (2.0%)

TOP SERVERS
───────────
github          75 calls
filesystem      45 calls
database        20 calls

TOP TOOLS
─────────
github:create_issue      30 calls
filesystem:read_file     25 calls
database:query           15 calls
```

### Output (JSON)

```json
{
  "period": "24h",
  "total_count": 150,
  "call_count": 128,
  "call_error_count": 5,
  "success_count": 142,
  "error_count": 5,
  "blocked_count": 3,
  "rejected_count": 0,
  "success_rate": 0.947,
  "top_servers": [
    {"name": "github", "count": 75},
    {"name": "filesystem", "count": 45}
  ],
  "top_tools": [
    {"server": "github", "tool": "create_issue", "count": 30},
    {"server": "filesystem", "tool": "read_file", "count": 25}
  ]
}
```

### Exit Codes

| Code | Meaning |
|------|---------|
| 0 | Success |
| 1 | Error fetching summary |
| 2 | Invalid period format |

---

## activity export

Export activity records for compliance and auditing.

### Usage

```bash
mcpproxy activity export [flags]
```

### Flags

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--output` | | | Destination file path (stdout if not specified); not the global `-o/--output` format flag |
| `--format` | `-f` | json | Export file format: `json` (JSON Lines), `csv` |
| `--include-bodies` | | false | Include full request/response bodies |
| `--parent-id` | | | Export only child tool calls of a `code_execution` activity (value = the parent record `request_id`) |
| `--error-class`, `--fault-domain` | | | Export only failed tool calls of this class / fault domain (values as for `activity list`) |
| *(filter flags)* | | | Same filters as `activity list`, including `--profile`, `--client`, `--token` and `--client-name`. CSV output appends `profile`, `profile_source`, `client_id`, `client_name`, `token_name` and `block_reason` after `parent_id` |

### Examples

```bash
# Export all activity as JSON Lines to file
mcpproxy activity export --output activity.jsonl

# Export as CSV
mcpproxy activity export --format csv --output activity.csv

# Export with full bodies for compliance
mcpproxy activity export --include-bodies --output full-audit.jsonl

# Export specific time range
mcpproxy activity export \
  --start-time 2025-01-01T00:00:00Z \
  --end-time 2025-01-31T23:59:59Z \
  --output january-2025.jsonl

# Export to stdout for piping
mcpproxy activity export --format csv | gzip > activity.csv.gz

# Export errors only
mcpproxy activity export --status error --output errors.jsonl

# Export specific event types
mcpproxy activity export --type tool_call,internal_tool_call --output tool-calls.jsonl

# Export config changes for audit
mcpproxy activity export --type config_change --output config-audit.jsonl

# Export the sub-calls of one code_execution run
mcpproxy activity export --parent-id a1b2c3d4-e5f6-7890-abcd-ef1234567890 --output sandbox-run.jsonl
```

### Output (JSON - JSON Lines)

```json
{"id":"01JFXYZ123ABC","type":"tool_call","source":"mcp","server_name":"github","tool_name":"create_issue","status":"success","duration_ms":245,"timestamp":"2025-01-15T10:30:00Z"}
{"id":"01JFXYZ123ABD","type":"tool_call","source":"cli","server_name":"filesystem","tool_name":"read_file","status":"error","duration_ms":125,"timestamp":"2025-01-15T10:30:01Z"}
```

### Output (CSV)

```csv
id,type,source,server_name,tool_name,status,duration_ms,timestamp,error_message
01JFXYZ123ABC,tool_call,mcp,github,create_issue,success,245,2025-01-15T10:30:00Z,
01JFXYZ123ABD,tool_call,cli,filesystem,read_file,error,125,2025-01-15T10:30:01Z,permission denied
```

### Exit Codes

| Code | Meaning |
|------|---------|
| 0 | Success |
| 1 | Error exporting |
| 2 | Invalid file path or permissions |

---

## activity sessions

List work sessions. A work session is one agent's run of work: one credential, one client, one project, until it has been idle for 30 minutes (see [Work sessions and the failure timeline](../features/activity-log.md#work-sessions-and-the-failure-timeline)). Each row carries the session's calls, its failures and the reason of the last failure.

An agent token sees only its own sessions, and only the part of each that touched servers in its reach.

### Usage

```bash
mcpproxy activity sessions [flags]
```

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--failed` | `false` | Only sessions with at least one failure |
| `--no-blocked` | `false` | With `--failed`: leave out sessions whose only failures are policy blocks (refusals by MCPProxy itself). It changes which sessions are listed, not the `FAILURES` and `LAST FAILURE` shown for them |
| `--token` | | Filter by agent token name |
| `--client` | | Filter by client id |
| `--profile` | | Filter by profile (administrators only) |
| `--run` | | Filter by run name, the `X-MCPProxy-Run` label a caller sends |
| `--error-class` | | Filter by the error class of the last failure: `network`, `timeout`, `http`, `jsonrpc`, `tool_error`, `session_terminated`, `auth`, `proxy_policy`, `proxy_internal`, `cancelled` |
| `--since` | | Only sessions whose last recorded activity is at or after this time (RFC 3339, or relative: `-1h`, `-24h`, `-7d`) |
| `--until` | | Only sessions started at or before this time (RFC 3339 or relative) |
| `--sort` | `last_activity`; `last_failure` with `--failed` | `last_activity` or `last_failure`, newest first |
| `--limit`, `-n` | 25 | Max sessions to return (1-100) |
| `--offset` | 0 | Pagination offset |

A flag with a value the endpoint would refuse fails before any request is made.

### Examples

```bash
# Runs active in the last 24 hours that have failures (with --failed the
# newest failure is first)
mcpproxy activity sessions --failed --since -24h

# The same, without sessions whose only failures were policy blocks
mcpproxy activity sessions --failed --no-blocked --since -24h

# The sessions of one agent token
mcpproxy activity sessions --token ci-agent

# One named REST run
mcpproxy activity sessions --run nightly-build-417

# As JSON
mcpproxy activity sessions --failed -o json
```

### Output (Table)

```
ID                   TOKEN     CLIENT       PROJECT  STARTED                DURATION  CALLS  FAILURES       LAST FAILURE          REASON
ws-4f2a91c07b3d55e1  ci-agent  claude-code  shop     2026-11-26 22:00:04Z   2h13m     412    3 (1 blocked)  2026-11-27 00:13:20Z  The upstream server could not be reached.
ws-07e9c4dd3185bb35  ci-agent  claude-code  billing  2026-11-26 22:00:09Z*  3h2m      1960   1              2026-11-26 23:41:07Z  The upstream server needs a login.

* the timeline of this session no longer reaches back to its start (retention trimmed it)
Showing 2 of 2 sessions
Timeline of one session: mcpproxy activity session <id>
```

- `CLIENT` is the client id when the session has a client binding, else the name the client reported with a `~` in front (a client can claim any name), else the surface the calls arrived on (`mcp`, or for REST calls `rest`, `cli`, `webui`, `tray`).
- A `RUN` column appears when at least one listed session has a run name.
- `--since` and `--until` select by a session's recorded activity (its last activity for `--since`, its start for `--until`), not by when it failed: a session that failed 25 hours ago and made its last call 23 hours ago is listed by `--failed --since -24h`.
- `DURATION` runs from the session's start to its last recorded activity; a session that is still open is marked `(active)`.
- `FAILURES` is the lifetime count. Policy blocks are part of it and are shown in brackets.
- `REASON` is the short reason the server recorded for the last failure (`last_failure.reason`), or `-` when it recorded none. It is a fixed sentence for the kind of failure and never contains argument or response text.
- When sessions match and `--offset` is past the last of them, the command says how many match, instead of reporting that none do.

### Output (JSON)

`-o json` prints the data object of `GET /api/v1/work-sessions` with its values unchanged: `sessions`, `total`, `limit`, `offset`. `-o yaml` prints the same fields; in YAML a character a terminal would act on (a control character, a text-direction override) is shown as visible text such as `\u202e`, so use JSON when you need the exact text. See the [REST API reference](../api/rest-api.md#work-sessions) for the fields.

### Exit Codes

| Code | Meaning |
|------|---------|
| 0 | Success (an empty list is a success) |
| 1 | Invalid or unparsable flag value or argument, the daemon is not running, or the daemon refused the request |

---

## activity session

Show the timeline of one work session: its calls, the refusals by MCPProxy itself, and the upstream events (a lost connection, an exited process, a login that is required) of the servers it called, in time order.

### Usage

```bash
mcpproxy activity session <id> [flags]
```

### Arguments

| Argument | Description |
|----------|-------------|
| `id` | Work session id (`ws-...`), from `activity sessions` |

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--at` | `first_failure` | Where the view opens: `first_failure`, `failure:<n>`, `row:<activity id>` or `end` |
| `--before` | 20 | Rows before the position, or before each failure with `--failures` (0-250) |
| `--after` | 20 | Rows after the position (0-250). With `--failures` the default is 0. One page holds 500 rows, so `--before` and `--after` together can be at most 499 |
| `--failures` | `false` | Print every stored failure with the rows before it. Cannot be combined with `--at` |
| `--include-bodies` | `false` | Include argument and response bodies |

Without `--at` and without `--failures`, the view opens at the first failure that is still stored; a session with no stored failure opens at the first row of its timeline. With `--failures`, a session with no stored failure prints `No stored failures in this session.` and no rows.

### Examples

```bash
# What went wrong first, and what the agent did before it
mcpproxy activity session ws-4f2a91c07b3d55e1

# Every failure of the run, each with the 5 rows before it
mcpproxy activity session ws-4f2a91c07b3d55e1 --failures --before 5

# The third failure, with the bodies of the calls around it
mcpproxy activity session ws-4f2a91c07b3d55e1 --at failure:3 --include-bodies

# The last row of the session and the 50 before it
mcpproxy activity session ws-4f2a91c07b3d55e1 --at end --before 50

# Around one row from `activity list`
mcpproxy activity session ws-4f2a91c07b3d55e1 --at row:01JFXYZ123ABC
```

### Output (Table)

```
Session ws-4f2a91c07b3d55e1 · token ci-agent · client claude-code · project shop · ended
Started 2026-11-26 22:00:04Z · last activity 2026-11-27 00:13:20Z · 2h13m · 412 calls · 3 failures (1 blocked)

Failure 1 of 3 · rows 37-41 of 418
       #   TIME                      KIND            TARGET               STATUS   DURATION  ID                          DETAIL
       37  2026-11-26 22:41:07.112Z  call            github:list_issues   success  212ms     01JFXYZ123ABC0000000000037  -
       38  2026-11-26 22:41:09.480Z  call            github:get_issue     success  187ms     01JFXYZ123ABC0000000000038  -
       39  2026-11-26 22:41:10.002Z  UPSTREAM EVENT  github               error    -         01JFXYZ123ABC0000000000039  connection_lost x3 · MCPX_UPSTREAM_CONNECTION_LOST · The connection to the server was lost
FAIL   40  2026-11-26 22:41:10.731Z  call            github:create_issue  error    30.0s     01JFXYZ123ABC0000000000040  network: The upstream server could not be reached.
FAIL   41  2026-11-26 22:41:12.004Z  call            github:list_issues   error    1ms       01JFXYZ123ABC0000000000041  network: The upstream server could not be reached.

The timeline has more rows (earlier and later). Widen the page with --before / --after (each up to 250, together up to 499), or move it with --at failure:<n>, --at row:<id>, --at end.
Every failure with its context: mcpproxy activity session ws-4f2a91c07b3d55e1 --failures
```

- The line above the table says where the page is. It is one of: `Failure 1 of 3` (the page is positioned at a failure); `After failure 2 of 3` (the positioned row is not a failure, with `--at end` or `--at row:<id>`); `Before the first of 3 failures`; `No stored failures`; and `Failures up to the position: 1 of 3`, used when the positioned row was not returned and it is therefore not known whether it is that failure or a later row. Whenever the positioned row was not returned (removed since the server found it, or not readable with the caller's credential), the line also adds `(row N, the position, was not returned)`. When the page has no rows at all, there is no table: the command prints `No rows were returned at this position; the timeline has N rows.` and suggests `--at first_failure` or `--at end`.
- The first column marks the counted row of a failed attempt: `FAIL`, or `BLOCK` when MCPProxy itself refused the call.
- `KIND` is the row's kind as the endpoint names it (`call`, `internal_call`, `policy_decision`, `prompt`, `other`). Three kinds are not requests of the agent and are written in capitals so they stand apart: `UPSTREAM EVENT`, `SESSION START`, `SESSION END`. An upstream event from before the session started, shown because the server was already down, says so in `DETAIL`.
- `#` is the row's index in the timeline. `ID` is the activity id, for `activity show <id>` and `--at row:<id>`.
- Times are UTC. The `TIME` of a row is to the millisecond; the two times in the header are to the second.
- When retention trimmed the start of the session's timeline, a note under the header says up to which time. When the session failed more often than the log still shows, a note gives both counts; failure numbers count the stored failures only.
- With `--include-bodies` the argument and response bodies follow the table. Each is one line of JSON (the arguments as an object, the response as a quoted string, so a line break reads `\n`), capped at 2,000 characters. `-o json` prints a body as the server returns it, without this cap. That is not always the original text: the activity log shortens a response over its size limit (the row is marked `response_truncated`), and a value flagged as sensitive is returned masked. A row whose bodies were removed by retention says so.

### All failures of a session

`--failures` prints each stored failure with `--before` rows ahead of it (and `--after` rows behind it, when given). Rows that are context of more than one failure are printed once, under a heading that names the failures they belong to:

```
Failures 1-2 of 3
...
Failure 3 of 3
...
```

With `-o json` or `-o yaml` this view is one object:

```json
{
  "session": { "id": "ws-4f2a91c07b3d55e1", "failures": 3 },
  "rows": [
    { "index": 39, "kind": "upstream_event", "at": "2026-11-26T22:41:10.002Z", "activity": { "id": "01JFXYZ123ABC0000000000039" } },
    { "index": 40, "kind": "call", "at": "2026-11-26T22:41:10.731Z", "failed": true, "activity": { "id": "01JFXYZ123ABC0000000000040" } },
    { "index": 41, "kind": "call", "at": "2026-11-26T22:41:12.004Z", "failed": true, "activity": { "id": "01JFXYZ123ABC0000000000041" } },
    { "index": 387, "kind": "call", "at": "2026-11-27T00:13:19.870Z", "activity": { "id": "01JFXYZ123ABC0000000000387" } },
    { "index": 388, "kind": "call", "at": "2026-11-27T00:13:20.412Z", "failed": true, "activity": { "id": "01JFXYZ123ABC0000000000388" } }
  ],
  "failures": [ { "number": 1, "index": 40 }, { "number": 2, "index": 41 }, { "number": 3, "index": 388 } ],
  "before": 1,
  "after": 0,
  "total_rows": 418,
  "failure_total": 3,
  "lifetime_failures": 3
}
```

This is the output of `--failures --before 1`, with `session` and each `activity` shortened here. `rows` holds every context row in timeline order, each once, in the shape of the timeline endpoint's rows; a failure's own row is part of it. `failures` gives the number of each failure among the stored ones and the `index` of its row. Two fields appear only when they apply: `earlier_failures_not_stored` is `true` when retention removed earlier failures, and `failures_not_returned` counts stored failures the server named without returning their row (removed while the session was read, or not readable with the caller's credential).

Failures are read by their number among the stored ones, and rows by their index. If retention removes older rows of the session while the command runs, numbers and indexes move; the command notices and starts over. A second or third attempt that reads a stable session prints the result and exits 0. Only when all three attempts are interrupted does the command exit 1, say that the session changed and ask you to run it again (with the server's last answer, when a refusal is how the change showed).

A session that is still active can also grow while it is read: a new call, or the upstream events of a server the session calls for the first time, add rows. Every row and every failure is still returned once, but the `index` of rows read before and after such a change may differ by the rows added ahead of them. The table output says so when it read more than one failure of an active session; for stable indexes, read the session after it has ended. Under a heading, a failure that was not returned is left out of the numbers (`Failures 1, 3 of 3`).

### Output (JSON)

Without `--failures`, `-o json` prints the data object of `GET /api/v1/work-sessions/{id}/timeline` with its values unchanged: `session`, `rows`, `position`, `total_rows`, `failure_index`, `failure_total`, `lifetime_failures`, `has_before`, `has_after`, and `earlier_failures_not_stored` when retention removed earlier failures. `-o yaml` prints the same fields, with terminal control characters shown as visible text, as for `activity sessions`. See the [REST API reference](../api/rest-api.md#work-sessions).

### Exit Codes

| Code | Meaning |
|------|---------|
| 0 | Success |
| 1 | Invalid or unparsable flag value or argument, the daemon is not running, the session was not found, the daemon refused the request, or (`--failures`) the session kept changing through three attempts to read it |

A `--config` file that cannot be loaded fails before either command runs, as it does for every `mcpproxy` command.

An id that matches no session exits 1 with:

```
Error: work session "ws-nope" not found: no session with this id is stored, or it is not yours to read. List sessions with: mcpproxy activity sessions
```

An agent token gets the same answer for a session that belongs to another token; an administrator can read every session. A `404` that does not come from this endpoint (a reverse proxy without the route, for example) is reported with its own text.

---

## Common Workflows

### The morning after: which agent run failed, and why

Several agents ran overnight. Two commands reach the failure:

```bash
# 1. The runs of the last 24 hours that have failures, newest failure first
mcpproxy activity sessions --failed --since -24h

# 2. Open one at its first failure, with the 20 rows before it
mcpproxy activity session ws-4f2a91c07b3d55e1
```

The timeline shows the failed call, its reason, what the same agent did before, and the upstream events of the servers the session called (a lost connection, an exited process, a required login) at the time they happened. An event next to a failure is a likely cause when it is on the server the failed call went to; events of the session's other servers are listed too. To see every failure of the run, add `--failures`. To read what one call sent and received, pass its id to `mcpproxy activity show <id> --include-response`.

Give each agent its own token (`mcpproxy token create --name <name> --profile <profile>`), or have each script send an `X-MCPProxy-Run` header, so that each run is its own session. Over SSH on a headless machine the commands work as they do locally; add `-o json` to feed a script:

```bash
# Ids of the sessions that failed on a network error
mcpproxy activity sessions --failed --error-class network -o json | jq -r '.sessions[].id'
```

### Debug a Failed Tool Call

```bash
# 1. Find recent errors
mcpproxy activity list --status error --limit 5

# 2. Get details for specific error
mcpproxy activity show <id-from-step-1> --include-response
```

### Drill into a `code_execution`

A `code_execution` call runs a sandboxed script that may call several upstream
tools. Each of those sub-calls is recorded as its own `tool_call` activity with
its own `request_id`, plus a `parent_id` pointing at the `request_id` of the
`code_execution` record that spawned it. That link is what makes a sandboxed run
auditable instead of a single opaque row.

```bash
# 1. Spot the code_execution in the list (type internal_tool_call, tool code_execution)
mcpproxy activity list --type internal_tool_call --limit 20

# 2. Open it — the detail view prints its Request ID and the drill-in hint
mcpproxy activity show <id-from-step-1>

# 3. List the sub-calls it made, using the parent's request_id
mcpproxy activity list --parent-id <request-id-from-step-2>

# 4. From any sub-call, walk back up to the parent
mcpproxy activity show <child-id>                       # prints "Parent ID: ..."
mcpproxy activity list --request-id <parent-id-from-step-4>
```

In table output, sub-calls are indented with `└` in the TOOL column, and
`activity watch` marks them inline with `[child of <parent_id>]`. In JSON output
the `parent_id` field is present on every child record, so the whole run can be
reassembled:

```bash
# All sub-calls of one run as JSON
mcpproxy activity list --parent-id <parent-request-id> -o json | jq '.activities[] | {tool_name, status, duration_ms}'
```

### Monitor Agent Behavior

```bash
# Watch what an AI agent is doing in real-time
mcpproxy activity watch

# Or filter to specific server
mcpproxy activity watch --server github
```

### Compliance Export

```bash
# Monthly audit export
mcpproxy activity export \
  --start-time "$(date -u -v-1m +%Y-%m-01T00:00:00Z)" \
  --include-bodies \
  --output audit-$(date +%Y-%m).jsonl
```

### Session Analysis

```bash
# The newest rows of one work session, as the activity list shows them
# (one page of up to 100; page with --offset)
mcpproxy activity list --session <session-id> --limit 100 -o json | jq '.'

# The end of the same session as a timeline, with upstream events merged in
mcpproxy activity session <session-id> --at end --before 250
```

### Quick Statistics

```bash
# Quick overview of today's activity
mcpproxy activity summary --period 24h

# Weekly report as JSON
mcpproxy activity summary --period 7d -o json
```

---

## Error Handling

### Error Format (Table)

```
Error: Activity not found: 01JFXYZ123ABC
Hint: Use 'mcpproxy activity list' to find valid activity IDs
```

### Error Format (JSON)

```json
{
  "error": {
    "code": "ACTIVITY_NOT_FOUND",
    "message": "Activity not found: 01JFXYZ123ABC",
    "guidance": "Use 'mcpproxy activity list' to find valid activity IDs",
    "recovery_command": "mcpproxy activity list --limit 10"
  }
}
```

### Error Codes

| Code | Description |
|------|-------------|
| `ACTIVITY_NOT_FOUND` | Activity ID does not exist |
| `INVALID_ID_FORMAT` | Activity ID is not valid ULID |
| `INVALID_TYPE` | Unknown activity type |
| `INVALID_STATUS` | Unknown status value |
| `INVALID_TIME_FORMAT` | Time not in RFC3339 format |
| `INVALID_TIME_RANGE` | End time before start time |
| `CONNECTION_ERROR` | Cannot connect to daemon |
| `EXPORT_ERROR` | Error writing export file |

---

## Event Types Reference

The activity log captures the following event types:

| Type | Description |
|------|-------------|
| `tool_call` | Every tool call made through MCPProxy to upstream servers |
| `system_start` | MCPProxy server startup events |
| `system_stop` | MCPProxy server shutdown events |
| `internal_tool_call` | Internal proxy tool calls (`retrieve_tools`, `call_tool_*`, `code_execution`, `upstream_servers`, etc.) |
| `config_change` | Configuration changes (server added/removed/updated) |
| `policy_decision` | Tool calls blocked by policy rules |
| `quarantine_change` | Server quarantine/unquarantine events |
| `server_change` | Server enable/disable/restart events |

:::note Duplicate Filtering for call_tool_*
By default, all `call_tool_*` internal records are filtered out from `activity list`, `activity watch`, and the Web UI — every dispatch (successful, failed, or shed) has a corresponding upstream `tool_call` or rejection record carrying the same `request_id`, so showing both would double-count the call.

To include all internal tool calls in API responses, use `include_call_tool=true` query parameter.
:::

### Multi-Type Filtering

You can filter by multiple types using comma-separated values:

```bash
# Filter by multiple types
mcpproxy activity list --type tool_call,internal_tool_call

# System lifecycle events
mcpproxy activity list --type system_start,system_stop

# All config-related events
mcpproxy activity list --type config_change,quarantine_change,server_change
```

---

## Tips

- Use `--json` output for piping to `jq` for complex filtering
- The watch command automatically reconnects on network issues
- Export streams directly to file without loading all records in memory
- Time filters use RFC3339 format: `2025-01-15T10:30:00Z`
- Combine with `grep` and `jq` for advanced filtering

```bash
# Find all tool calls with "github" in JSON output
mcpproxy activity list -o json | jq '.activities[] | select(.server_name | contains("github"))'

# Count errors by server
mcpproxy activity list --status error -o json | jq -r '.activities[].server_name' | sort | uniq -c
```
