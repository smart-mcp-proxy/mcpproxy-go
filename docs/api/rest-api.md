---
id: rest-api
title: REST API
sidebar_label: REST API
sidebar_position: 1
description: MCPProxy REST API reference
keywords: [api, rest, http, endpoints]
---

# REST API

MCPProxy provides a REST API for server management and monitoring.

:::tip OpenAPI Specification
Interactive API documentation is available at [http://127.0.0.1:8080/swagger/](http://127.0.0.1:8080/swagger/) when MCPProxy is running. The OpenAPI spec file is also available at [`oas/swagger.yaml`](https://raw.githubusercontent.com/smart-mcp-proxy/mcpproxy-go/refs/heads/main/oas/swagger.yaml).
:::

## Authentication

All `/api/v1/*` endpoints require authentication via API key:

```bash
# Using X-API-Key header (recommended)
curl -H "X-API-Key: your-api-key" http://127.0.0.1:8080/api/v1/servers

# Using query parameter
curl "http://127.0.0.1:8080/api/v1/servers?apikey=your-api-key"
```

**Note:** Unix socket connections bypass API key authentication (OS-level auth).

## Base URL

```
http://127.0.0.1:8080/api/v1
```

## Request ID Tracking

All API responses include an `X-Request-Id` header for request tracing and log correlation. This is useful for debugging issues and correlating errors with server logs.

### Request Header

You can optionally provide your own request ID:

```bash
curl -H "X-API-Key: your-api-key" \
     -H "X-Request-Id: my-custom-id-123" \
     http://127.0.0.1:8080/api/v1/servers
```

**Validation rules:**
- Pattern: `^[a-zA-Z0-9_-]{1,256}$`
- Max length: 256 characters
- If missing or invalid, MCPProxy generates a UUID v4

### Response Header

Every response includes the request ID:

```
X-Request-Id: my-custom-id-123
```

### Error Responses

Error responses include the `request_id` in the JSON body for easy correlation:

```json
{
  "success": false,
  "error": "server 'nonexistent' not found",
  "request_id": "a1b2c3d4-e5f6-7890-abcd-ef1234567890"
}
```

### Log Correlation

Use the request ID to find related activity logs:

```bash
# Via CLI
mcpproxy activity list --request-id a1b2c3d4-e5f6-7890-abcd-ef1234567890

# Via API
curl "http://127.0.0.1:8080/api/v1/activity?request_id=a1b2c3d4-e5f6-7890-abcd-ef1234567890"
```

## Endpoints

### Status

#### GET /api/v1/status

Get server status and statistics.

**Response:**
```json
{
  "status": "running",
  "version": "0.11.0",
  "uptime": 3600,
  "servers": {
    "total": 5,
    "connected": 4,
    "quarantined": 1
  },
  "tools": {
    "total": 42
  }
}
```

### Servers

#### GET /api/v1/servers

List all upstream servers with unified health status.

##### Header redaction and the mask format

By default, sensitive header values (`Authorization`, `X-API-Key`, `Cookie`,
`Set-Cookie`, etc.) are replaced with a length-preserving mask of the form
`••••<last2> (<N> chars)` before serialization. This applies to:

- `GET /api/v1/servers` and its single-server children
- The `/events` SSE `servers.changed` payloads
- The `upstream_servers list` MCP tool

The mask preserves enough information to identify which token is in use
(the last two characters + total length) while keeping the secret out of
the response. Values that are already secret references — `${keyring:NAME}`
or `${env:VAR}` — pass through unchanged because they're labels, not
secrets.

Setting `reveal_secret_headers: true` in
[`mcp_config.json`](../configuration/config-file.md) disables redaction on
all three channels. This is **not normally needed**: the Web UI / macOS
tray / CLI can edit, delete, and convert-to-secret without ever seeing
the plaintext, because the PATCH endpoint deep-merges (omitted keys are
preserved) and the [`config-to-secret`](#post-apiv1serversnameconfig-to-secret)
endpoint reads the real value server-side. Flip the flag only if you
need to inspect a raw value through the API for debugging.

The MCP `upstream_servers` tool was the original motivator for redaction
(see [PR #425](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/425)) —
a prompt-injected agent could otherwise read another upstream's PAT via
`upstream_servers list`.

**Response:**
```json
{
  "success": true,
  "data": {
    "servers": [
      {
        "name": "github-server",
        "protocol": "http",
        "enabled": true,
        "connected": true,
        "quarantined": false,
        "tool_count": 15,
        "health": {
          "level": "healthy",
          "admin_state": "enabled",
          "summary": "Connected (15 tools)",
          "action": ""
        }
      },
      {
        "name": "oauth-server",
        "protocol": "http",
        "enabled": true,
        "connected": false,
        "quarantined": false,
        "tool_count": 0,
        "health": {
          "level": "unhealthy",
          "admin_state": "enabled",
          "summary": "Token expired",
          "detail": "OAuth access token has expired",
          "action": "login"
        }
      }
    ]
  }
}
```

**Health Object Fields:**

| Field | Type | Description |
|-------|------|-------------|
| `level` | string | Health level: `healthy`, `degraded`, or `unhealthy` |
| `admin_state` | string | Admin state: `enabled`, `disabled`, or `quarantined` |
| `summary` | string | Human-readable status message |
| `detail` | string | Optional additional context about the status |
| `action` | string | Suggested remediation: `login`, `restart`, `enable`, `approve`, `view_logs`, or empty |

#### PATCH /api/v1/servers/{name}

Partial update of an existing upstream server. All request fields are optional;
omitted fields are preserved as-is.

The map-typed fields `headers` and `env` follow **JSON Merge Patch
([RFC 7396](https://www.rfc-editor.org/rfc/rfc7396))** semantics:

| Value in patch body | Effect on stored map |
|---|---|
| key present with a non-null string value | upsert (add or replace that key) |
| key present with JSON `null` | delete that key |
| key absent from the patch body | preserve as-is |

This is the same convention the MCP `upstream_servers patch` tool uses. It
lets the Web UI / macOS tray / CLI send a minimal diff — keys that match
the server's current masked view (`••••<last2> (<N> chars)` — see
[Header redaction](#header-redaction-and-the-mask-format) below) simply stay
out of the patch body, so the real stored value is never overwritten by the
mask string.

**Request body** ([`AddServerRequest`](https://github.com/smart-mcp-proxy/mcpproxy-go/blob/main/internal/httpapi/server.go) — all fields optional):

```json
{
  "url": "https://api.example.com/mcp",
  "command": "uvx",
  "args": ["mcp-server-foo"],
  "env": {"API_KEY": "new-value", "OLD_VAR": null},
  "headers": {"X-Trace": "on", "X-Stale": null},
  "working_dir": "/path/to/dir",
  "protocol": "http",
  "enabled": true,
  "quarantined": false,
  "auto_approve_tool_changes": true,
  "isolation": {"enabled": true, "image": "node:20"}
}
```

**Examples:**

```bash
# Rotate a Bearer token without touching anything else on the server
curl -X PATCH -H "X-API-Key: $KEY" -H "Content-Type: application/json" \
  -d '{"headers":{"Authorization":"Bearer new-token"}}' \
  http://127.0.0.1:8080/api/v1/servers/synapbus

# Remove a stale header (the JSON null is the delete signal)
curl -X PATCH -H "X-API-Key: $KEY" -H "Content-Type: application/json" \
  -d '{"headers":{"X-Stale":null}}' \
  http://127.0.0.1:8080/api/v1/servers/synapbus

# Upsert one env var and delete another in a single round-trip
curl -X PATCH -H "X-API-Key: $KEY" -H "Content-Type: application/json" \
  -d '{"env":{"LOG_LEVEL":"debug","OBSOLETE":null}}' \
  http://127.0.0.1:8080/api/v1/servers/obsidian-pilot
```

**Notes:**

- Empty string `""` is **set-to-empty**, NOT delete. JSON Merge Patch is
  explicit about this — only the JSON `null` token deletes.
- Boolean fields (`enabled`, `quarantined`, `reconnect_on_use`,
  `auto_approve_tool_changes`) use pointer-style semantics: absent = preserve,
  present = explicit value. `auto_approve_tool_changes` is tri-state — it is
  omitted entirely from `GET /api/v1/servers` responses when never set, so a
  client can distinguish "unset" from an explicit `false`.

#### POST /api/v1/servers/{name}/config-to-secret

Atomically move a header or env value out of `mcp_config.json` and into the
OS keyring. The backend reads the real value from the loaded config, stores
it in the keyring under `secret_name`, and rewrites the config field with
`${keyring:<secret_name>}`. The client never needs to possess the plaintext
— useful when the API redacts sensitive header values on the read path.

**Request body:**

```json
{
  "scope": "header",
  "key": "Authorization",
  "secret_name": "synapbus-auth"
}
```

| Field | Type | Description |
|---|---|---|
| `scope` | string | `header` or `env` |
| `key` | string | The key on the server's headers / env map |
| `secret_name` | string | Name to store the value under in the OS keyring |

**Response (200 OK):**

```json
{
  "success": true,
  "data": {
    "message": "header \"Authorization\" on \"synapbus\" now references keyring secret \"synapbus-auth\"",
    "reference": "${keyring:synapbus-auth}"
  }
}
```

**Failure cases:**

| Status | Cause |
|---|---|
| 400 | Missing `scope` / `key` / `secret_name`, invalid scope, value is already a `${keyring:…}` or `${env:…}` reference, or value is empty |
| 404 | Server or key not found |
| 500 | Secret resolver unavailable, keyring store failed, or config update failed |

This endpoint is what the Web UI and macOS tray "Convert to secret" button
calls. It works even for headers the API redacts (the backend has the real
value on disk).

#### POST /api/v1/servers/{name}/enable

Enable a server.

#### POST /api/v1/servers/{name}/disable

Disable a server.

#### POST /api/v1/servers/{name}/quarantine

Place a server in quarantine to prevent tool execution. No request body required.

#### POST /api/v1/servers/{name}/unquarantine

Remove a server from quarantine to allow tool execution. No request body required.

#### POST /api/v1/servers/{name}/restart

Restart a server.

#### POST /api/v1/servers/{name}/login

Initiate OAuth authentication flow for a server.

**Response (200 OK):**
```json
{
  "success": true,
  "data": {
    "success": true,
    "server_name": "github-server",
    "correlation_id": "a1b2c3d4e5f6789012345678",
    "browser_opened": true,
    "message": "OAuth authentication started for server 'github-server'. Please complete authentication in browser."
  }
}
```

**OAuthStartResponse Fields:**

| Field | Type | Description |
|-------|------|-------------|
| `success` | boolean | Always `true` for successful initiation |
| `server_name` | string | Name of the server being authenticated |
| `correlation_id` | string | Unique ID for tracking this OAuth flow |
| `auth_url` | string | Authorization URL (for manual browser opening) |
| `browser_opened` | boolean | Whether browser was automatically opened |
| `browser_error` | string | Error message if browser opening failed |
| `message` | string | Human-readable status message |

**Error Response (400 Bad Request):**

OAuth errors return structured error responses for better debugging:

```json
{
  "success": false,
  "error_type": "dcr_failed",
  "server_name": "github-server",
  "message": "Dynamic Client Registration failed: 403 Forbidden",
  "suggestion": "Check if the OAuth server requires pre-registered clients",
  "correlation_id": "a1b2c3d4e5f6789012345678",
  "request_id": "req-xyz-123",
  "details": {
    "metadata": {
      "protected_resource_url": "https://api.example.com/.well-known/oauth-protected-resource",
      "authorization_server_url": "https://auth.example.com/.well-known/oauth-authorization-server",
      "status": "ok"
    },
    "dcr": {
      "attempted": true,
      "status": "failed",
      "error": "403 Forbidden"
    }
  },
  "debug_hint": "For logs: mcpproxy upstream logs github-server"
}
```

**OAuthFlowError Fields:**

| Field | Type | Description |
|-------|------|-------------|
| `error_type` | string | Error category: `client_id_required`, `dcr_failed`, `metadata_discovery_failed`, `code_flow_failed` |
| `server_name` | string | Name of the server |
| `message` | string | Human-readable error description |
| `suggestion` | string | Actionable remediation hint |
| `correlation_id` | string | Flow tracking ID |
| `request_id` | string | HTTP request ID for log correlation |
| `details` | object | Diagnostic details (metadata status, DCR status) |
| `debug_hint` | string | CLI command for debugging |

#### POST /api/v1/servers/{name}/logout

Clear OAuth tokens and disconnect a server.

### Tool Quarantine

#### POST /api/v1/servers/{name}/tools/approve

Approve pending or changed tools for a server. See [Tool Quarantine](../features/tool-quarantine.md) for details.

**Request Body:**
```json
{
  "tools": ["create_issue", "delete_repo"]
}
```

Or approve all pending/changed tools:
```json
{
  "approve_all": true
}
```

**Response:**
```json
{
  "success": true,
  "data": {
    "approved": 2,
    "tools": ["create_issue", "delete_repo"],
    "message": "Approved 2 tools for server github-server"
  }
}
```

#### POST /api/v1/servers/{name}/tools/block

Atomically **block** tools = approve **and** disable them in a single server-side
operation. Use this to acknowledge a pending/changed tool (clearing its
quarantine flag) while keeping it hidden from MCP clients. The approve and
disable land in one write per tool, so a tool is never left in the
approved+enabled state.

**Request Body:**
```json
{
  "tools": ["create_issue", "delete_repo"]
}
```

Or block all pending/changed tools:
```json
{
  "block_all": true
}
```

**Response:**
```json
{
  "success": true,
  "data": {
    "blocked": 2,
    "tools": ["create_issue", "delete_repo"],
    "message": "Blocked 2 tools for server github-server"
  }
}
```

Returns `400` if neither `tools` nor `block_all` is provided.

#### GET /api/v1/servers/{name}/tools/{tool}/diff

Get the description/schema diff for a changed tool. The response exposes every
field that participates in the approval hash — description, input schema, and
output schema — so an operator can see exactly what changed. A change may affect
only one of these (for example, an upstream adding a new enum value to the output
schema leaves the description byte-identical).

**Response:**
```json
{
  "success": true,
  "data": {
    "server_name": "github-server",
    "tool_name": "delete_repo",
    "status": "changed",
    "approved_hash": "abc123...",
    "current_hash": "def456...",
    "previous_description": "Delete a repository",
    "current_description": "Delete a repository (modified description)",
    "previous_schema": "...",
    "current_schema": "...",
    "previous_output_schema": "...",
    "current_output_schema": "..."
  }
}
```

#### GET /api/v1/servers/{name}/tools/export

Export all tool descriptions and schemas for a server. Useful for audit and compliance.

**Query Parameters:**
- `format` - Export format: `json` (default) or `text`

### Routing

#### GET /api/v1/routing

Get the current routing mode and available MCP endpoints.

**Response:**
```json
{
  "success": true,
  "data": {
    "routing_mode": "retrieve_tools",
    "description": "BM25 search via retrieve_tools + call_tool variants (default)",
    "endpoints": {
      "default": "/mcp",
      "direct": "/mcp/all",
      "code_execution": "/mcp/code",
      "retrieve_tools": "/mcp/call"
    },
    "available_modes": ["retrieve_tools", "direct", "code_execution"]
  }
}
```

See [Routing Modes](../features/routing-modes.md) for details on each mode.

### Tools

#### GET /api/v1/tools

Global tools overview (spec 050, issue #437): every tool from **every** configured
server — including disabled servers and individually disabled / config-denied tools —
enriched with approval state and 30-day usage. Read-only; consumers apply their own
search/filter/sort over the full set. For relevance-ranked discovery use
`GET /api/v1/index/search` instead.

**Response:**
```json
{
  "success": true,
  "data": {
    "tools": [
      {
        "name": "create_issue",
        "server_name": "github",
        "description": "Create a new GitHub issue",
        "approval_status": "approved",
        "disabled": false,
        "config_denied": false,
        "usage": 42,
        "last_used": "2026-05-18T09:30:51Z"
      }
    ],
    "stats": { "total": 478, "enabled": 450, "disabled": 28, "pending_approval": 0 },
    "partial": false,
    "failed_servers": []
  }
}
```

`enabled` is derived by the consumer as `!disabled && !config_denied`. When a
server cannot be read the endpoint still returns every tool it could gather and
sets `partial: true` with `failed_servers` (it does not fail the whole request).

#### GET /api/v1/servers/{name}/tools

List tools for a specific server.

### Registries

Discover MCP servers in known registries and add them as quarantined upstreams.
The daemon re-derives the runnable config server-side — the client never sends a
config blob. See [Adding servers from registries](../features/registry-add.md)
for the full feature guide (CLI, REST, MCP).

#### GET /api/v1/registries

List configured registries.

#### POST /api/v1/registries

Add a user-supplied custom registry source. JSON body:
`{ "url": "https://…", "protocol": "…", "id": "…", "name": "…" }` (only `url`
required). The source is always tagged `custom`. Errors share a stable
code: `invalid_registry_url` (400), `registries_locked` (403),
`registry_shadows_builtin` / `duplicate_registry` (409).

#### PUT /api/v1/registries/{id}

Edit a user-added custom registry source. JSON body:
`{ "name": "…", "url": "https://…", "servers_url": "https://…" }` (all optional;
an omitted/empty field is left unchanged). Returns `data.registry` echoing the
updated entry. Built-in registries are refused with `registry_shadows_builtin`
(409); an unknown id returns `registry_not_found` (404); a non-https url returns
`invalid_registry_url` (400); a `registries_locked` policy returns 403.

#### DELETE /api/v1/registries/{id}

Remove a user-added custom registry source. Returns `data.registry` echoing the
removed entry. Built-in registries are refused with `registry_shadows_builtin`
(409); an unknown id returns `registry_not_found` (404); a `registries_locked`
policy returns 403.

#### GET /api/v1/registries/{id}/servers

Search a registry's servers (`?search=`, `?tag=`, `?limit=`).

#### POST /api/v1/registries/{id}/servers/{serverId}/add

Add a server from a registry as an upstream (quarantined per the global default).
Optional JSON body carries only overrides (never a config blob):

```json
{ "name": "github-mcp", "env": { "GITHUB_TOKEN": "…" }, "enabled": true }
```

Success returns `data.server` (`name`, `protocol`, `command`, `args`, `url`,
`enabled`, `quarantined`). A missing required input returns
`{"success": false, "code": "missing_required_input", "missing_inputs": [...]}`
— the same cross-surface code emitted by the CLI and MCP surfaces.

#### POST /api/v1/registries/{id}/refresh

Drop a registry's cached server lists. Returns
`{ "registry_id": "...", "cleared": <n> }`.

### Connect (client wizard)

#### GET /api/v1/connect

Lists the connection status of every known MCP client (Claude Desktop, Cursor,
VS Code, Codex, Gemini, OpenCode, …).

As of Spec 075, the overall listing determines each client's installed state
using **file-existence metadata only** (`os.Stat`) and performs **zero config
content reads** — so simply viewing status never triggers the macOS
"wants to access data from other apps" privacy prompt. Each per-client object is
additive-compatible and gains two fields:

| Field | Type | Meaning |
|-------|------|---------|
| `exists` | bool | Config file present (metadata only). |
| `connected` | bool | mcpproxy registered in the config. Authoritative **only** when `access_state == "accessible"`; `false`/unresolved in the overall listing. |
| `access_state` | string | `"unknown"` in the overall listing (not content-checked); resolved to `"accessible"`, `"absent"`, `"malformed"`, or `"denied"` by an on-demand single-client read. |
| `remediation` | string | Present only when `access_state == "denied"`; carries the actionable fix text (App Data toggle + `tccutil reset` command). |

A client that is installed but not yet content-checked reads as
`exists=true, connected=false, access_state="unknown"`. Resolving `connected`
requires an explicit per-client read (the per-client status route below,
connect/disconnect, or the CLI `mcpproxy connect` command), which is where a
privacy prompt may legitimately appear.

#### GET /api/v1/connect/{client}

On-demand single-client status. Reads the one client's config **at request
time** and returns a full `ClientStatus` with `access_state` resolved to
`accessible | absent | malformed | denied` and `connected` set accordingly.
This — like the other per-client routes below (preview, connect/disconnect,
undo) — opens the client's config file at request time, so on macOS an App-Data
privacy prompt may legitimately appear here (scoped to this user action), never
from the overall listing. Unknown client → `404`. A denial is reported **in-band**
(`200` with `access_state="denied"` + `remediation`), not as an HTTP error.

```bash
curl "http://127.0.0.1:8080/api/v1/connect/claude-desktop?apikey=your-api-key"
```

#### POST/DELETE /api/v1/connect/{client}

Connect/disconnect are unchanged except that a permission-denied config access
now returns **`403 Forbidden`** whose error body carries the remediation text
(distinct from a generic `400` or a `404` not-found).

Every connect/disconnect that modifies an **existing** config file first writes
a timestamped backup next to it (`<config>.bak.<YYYYMMDD-HHMMSS>`, same
directory and file mode) and returns its path as `backup_path` in the result.
When two operations land in the same second, a numeric suffix keeps every
backup distinct (`<config>.bak.<YYYYMMDD-HHMMSS>-1`, `-2`, …) — a backup is
never overwritten. Backups accumulate one per operation and are **never
deleted automatically**; there is no retention bound, so an undo (below) can
always find its backup.

#### GET /api/v1/connect/{client}/preview

Returns the exact change a subsequent connect would make — target config path,
format (`json`/`toml`), server key, entry name, and the exact entry contents —
**without** modifying the file or creating a backup (Spec 078 US1). An embedded
API key is masked in the payload (`contains_api_key` flags that a credential is
written); `entry_exists` distinguishes a create from an overwrite of a
same-named entry. Reads the config on demand to classify create-vs-overwrite,
so on macOS this may raise an App-Data prompt; a denial returns `403` +
remediation. Optional `?server_name=` mirrors the name a subsequent connect
would use.

#### POST /api/v1/connect/{client}/undo

One-click undo of the immediately-preceding connect (Spec 078 US3). Body:

```json
{ "server_name": "mcpproxy", "backup_name": "<basename of backup_path from the connect result>" }
```

`backup_name` is the **bare filename** of the backup the connect returned in
`backup_path` — a name, never a path. The server resolves the full path itself
inside that client's own config directory (derived from the client registry, not
the request), so a caller-supplied value can never contribute a directory
component and cannot escape the config dir (defense against path injection).

- **`backup_name` set** — restores the config **byte-for-byte** from that
  backup. This is the only revert that can bring back a pre-existing
  same-named entry that a `force=true` connect overwrote (surgical
  `DELETE /connect/{client}` cannot).
- **`backup_name` empty** — the connect created the file (its result carried no
  `backup_path`); undo deletes the created file, restoring the "no file" state.

Safety semantics:

- Undo **refuses with `409 Conflict`** when the config changed since the
  connect (it verifies the current file is byte-identical to what that connect
  wrote) — it never clobbers later edits. Fall back to
  `DELETE /connect/{client}` for a surgical entry removal.
- A vanished backup returns `404`; a `backup_name` that is a path (contains a
  directory separator) or does not match `<config>.bak.*` for that client
  returns `400`.
- Undo takes its **own safety backup** of the current file before restoring or
  deleting, returned as `backup_path` in the result
  (`action` = `restored` or `deleted`).
- A macOS App-Data denial returns `403` + remediation, like the other
  per-client routes.

##### macOS App Data privacy & Connect

On macOS, client configs (Claude Desktop, Cursor, VS Code, …) live under another
app's container, gated by the **Privacy & Security ▸ App Data** TCC permission.
If mcpproxy is denied, an on-demand read returns `access_state="denied"` with
remediation. Fix it by enabling mcpproxy under **System Settings ▸ Privacy &
Security ▸ App Data**, or reset the decision and retry:

```bash
tccutil reset SystemPolicyAppData com.smartmcpproxy.mcpproxy
# dev builds: com.smartmcpproxy.mcpproxy.dev
```

The overall `GET /api/v1/connect` listing never triggers this prompt (it is
content-read-free); only the per-client routes above (status, preview,
connect/disconnect, undo) can.

### Real-time Updates

#### GET /events

Server-Sent Events (SSE) stream for live updates.

```bash
curl "http://127.0.0.1:8080/events?apikey=your-api-key"
```

Events include:
- `servers.changed` - Server status changed
- `config.reloaded` - Configuration reloaded
- `tools.indexed` - Tool index updated
- `activity.tool_call.started` - Tool call initiated
- `activity.tool_call.completed` - Tool call finished
- `activity.policy_decision` - Tool call blocked by policy

## Error Responses

```json
{
  "error": "error message",
  "code": "ERROR_CODE"
}
```

| Code | Description |
|------|-------------|
| 401 | Unauthorized - Invalid or missing API key |
| 404 | Not Found - Server or resource not found |
| 500 | Internal Server Error |

### Configuration

#### GET /api/v1/config

Get current configuration.

#### POST /api/v1/config/apply

Apply configuration changes.

#### POST /api/v1/config/validate

Validate configuration without applying.

### Diagnostics

#### GET /api/v1/diagnostics

Get system diagnostics.

#### GET /api/v1/doctor

Run health checks (same as `mcpproxy doctor` CLI).

#### GET /api/v1/info

Get application info, version, and update availability.

**Response:**
```json
{
  "success": true,
  "data": {
    "version": "v1.2.3",
    "web_ui_url": "http://127.0.0.1:8080/?apikey=xxx",
    "listen_addr": "127.0.0.1:8080",
    "endpoints": {
      "http": "127.0.0.1:8080",
      "socket": "/Users/user/.mcpproxy/mcpproxy.sock"
    },
    "update": {
      "available": true,
      "latest_version": "v1.3.0",
      "release_url": "https://github.com/smart-mcp-proxy/mcpproxy-go/releases/tag/v1.3.0",
      "checked_at": "2025-01-15T10:30:00Z",
      "is_prerelease": false
    }
  }
}
```

**Response Fields:**

| Field | Type | Description |
|-------|------|-------------|
| `version` | string | Current MCPProxy version |
| `web_ui_url` | string | URL to access the web control panel |
| `listen_addr` | string | Server listen address |
| `endpoints.http` | string | HTTP API endpoint address |
| `endpoints.socket` | string | Unix socket path (empty if disabled) |
| `update` | object | Update information (may be null if not checked yet; omitted entirely when update checking is disabled via `update_check.enabled: false` or `MCPPROXY_DISABLE_AUTO_UPDATE=true`) |
| `update.available` | boolean | Whether a newer version is available |
| `update.latest_version` | string | Latest version available on GitHub |
| `update.release_url` | string | URL to the GitHub release page |
| `update.checked_at` | string | ISO 8601 timestamp of last update check |
| `update.is_prerelease` | boolean | Whether the latest version is a prerelease |
| `update.check_error` | string | Error message if update check failed |

:::tip Update Checking
MCPProxy automatically checks for updates every 4 hours. The update information is exposed via this endpoint and used by the tray application and web UI to show update notifications. Use `?refresh=true` to force an immediate re-check. Checking is controlled by the `update_check` config block (`enabled`, `channel`) — see [Version Updates](/features/version-updates); when disabled, `?refresh=true` performs no check and the `update` object is omitted.
:::

### Docker

#### GET /api/v1/docker/status

Get Docker isolation status.

**Response fields:**

| Field | Type | Description |
|-------|------|-------------|
| `docker_available` | bool | Genuine Docker daemon reachability (result of a real `docker info` probe). |
| `isolation_enabled` | bool | Whether `docker_isolation.enabled` is set in config. The UI treats isolation as "active" only when both this and `docker_available` are true. |
| `recovery_mode` | bool | Whether the Docker recovery monitor is actively retrying. |
| `failure_count` | int | Consecutive recovery failures. |
| `attempts_since_up` | int | Recovery attempts since the daemon was last seen available. |
| `last_attempt` | string | Timestamp of the last recovery attempt. |
| `last_error` | string | Last recovery error message, if any. |
| `last_successful_at` | string | Timestamp of the last successful daemon contact. |

### Secrets

#### GET /api/v1/secrets

List stored secrets.

#### GET /api/v1/secrets/{name}

Get secret metadata (not the value).

### Sessions

#### GET /api/v1/sessions

List active MCP sessions.

#### GET /api/v1/sessions/{id}

Get session details.

### Activity

Track and audit AI agent tool calls. See [Activity Log](../features/activity-log.md) for detailed documentation.

#### GET /api/v1/activity

List activity records with filtering and pagination.

**Query Parameters:**

| Parameter | Type | Description |
|-----------|------|-------------|
| `type` | string | Filter by type: `tool_call`, `policy_decision`, `quarantine_change`, `server_change` |
| `server` | string | Filter by server name |
| `tool` | string | Filter by tool name |
| `session_id` | string | Filter by MCP session ID |
| `status` | string | Filter by status: `success`, `error`, `blocked` |
| `start_time` | string | Filter after this time (RFC3339) |
| `end_time` | string | Filter before this time (RFC3339) |
| `limit` | integer | Max records (1-100, default: 50) |
| `offset` | integer | Pagination offset (default: 0) |

**Response:**
```json
{
  "success": true,
  "data": {
    "activities": [
      {
        "id": "01JFXYZ123ABC",
        "type": "tool_call",
        "server_name": "github-server",
        "tool_name": "create_issue",
        "status": "success",
        "duration_ms": 245,
        "timestamp": "2025-01-15T10:30:00Z"
      }
    ],
    "total": 150,
    "limit": 50,
    "offset": 0
  }
}
```

#### GET /api/v1/activity/{id}

Get full activity record details including request arguments and response data.

#### GET /api/v1/activity/export

Export activity records for compliance and auditing.

**Query Parameters:**

| Parameter | Type | Description |
|-----------|------|-------------|
| `format` | string | Export format: `json` (JSON Lines) or `csv` |
| *(filters)* | | Same filters as list endpoint |

**Example:**
```bash
# Export as JSON Lines
curl -H "X-API-Key: $KEY" "http://127.0.0.1:8080/api/v1/activity/export?format=json"

# Export as CSV
curl -H "X-API-Key: $KEY" "http://127.0.0.1:8080/api/v1/activity/export?format=csv"
```

### Bulk Operations

#### POST /api/v1/servers/enable_all

Enable all servers.

#### POST /api/v1/servers/disable_all

Disable all servers.

#### POST /api/v1/servers/restart_all

Restart all servers.

#### POST /api/v1/servers/reconnect

Reconnect all servers.

## OpenAPI Specification

The complete OpenAPI 3.1 specification is available at:
- `/swagger/` - Interactive Swagger UI
- `/swagger/swagger.yaml` - Raw specification

See `oas/swagger.yaml` in the repository for the complete API reference.
