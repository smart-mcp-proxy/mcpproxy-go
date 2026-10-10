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

### Admin key vs. agent tokens

Two kinds of credential authenticate against the REST API:

- **Admin API key** — full access to every endpoint.
- **Agent tokens** (`mcp_agt_` prefix, see [Agent Tokens](../features/agent-tokens.md)) — scoped, read-oriented access. Agent tokens may **read** (`GET /api/v1/servers`, diagnostics, config/registry reads, `GET /api/v1/index/search`) but may **not** perform mutating operations that touch server/security/config state. These return **`403 Forbidden`** with `operation requires admin access` for an agent token, mirroring the MCP `upstream_servers`/`quarantine_security` denylist so the two surfaces cannot drift. Socket (tray) connections authenticate as admin and are unaffected. Gated routes:
  - **Servers** — add, remove, patch, enable, disable, restart, reconnect, quarantine, unquarantine, login, logout, config-to-secret, discover-tools, refresh, tool approve/block, the bulk `enable_all`/`disable_all`/`restart_all`, and the security scanner (`scan`, `scan/cancel`, `security/approve`, `security/reject`).
  - **Config** — `POST /config/apply`, `PATCH /config`, `PATCH /config/docker-isolation` (config can add/remove/enable/disable servers). `POST /config/validate` is read-only and stays open.
  - **Registries** — `POST/PUT/DELETE /registries[/{id}]` (source management) and `POST /registries/{id}/servers/{serverId}/add`. Registry browsing (`GET`) stays open.

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

Get server status and statistics. The `data` object carries `running`, `edition`, `listen_addr`, `routing_mode`, `upstream_stats`, `started_at`, `timestamp` and the blocks below. (An earlier version of this page showed a different shape; it was stale.)

**Response (abridged):**
```json
{
  "success": true,
  "data": {
    "running": true,
    "edition": "personal",
    "listen_addr": "127.0.0.1:8080",
    "routing_mode": "retrieve_tools",
    "upstream_stats": { "total_servers": 5, "connected_servers": 4, "quarantined_servers": 1, "total_tools": 42 },
    "telemetry": { "enabled": false, "source": "env", "disabled_by": "MCPPROXY_TELEMETRY=false" }
  }
}
```

**`telemetry`** is the effective telemetry state of the running core, so a UI can say whether telemetry is on and why. It is withheld from scoped callers (agent tokens), like `activation`.

| Field | Description |
|-------|-------------|
| `enabled` | Whether the core sends telemetry. Always equal to the resolved state: an environment opt-out wins over the config file. |
| `source` | `env` (an environment variable disabled it), `config` (`telemetry.enabled` is set in the config file, true or false) or `default` (unset, which means on). |
| `disabled_by` | Present only when `source` is `env`: `DO_NOT_TRACK`, `CI` or `MCPPROXY_TELEMETRY=false`. |

`GET /api/v1/config` keeps returning the stored `telemetry.enabled`, which can differ from `enabled` here when an environment variable overrides it. A dev (non-release) build never transmits whatever `enabled` says.

While an environment variable forces telemetry off, `POST /api/v1/config/apply` and `PATCH /api/v1/config` answer `422` and write nothing if the document would change `telemetry.enabled` (the value is judged after decoding, so a miscased key is caught too). A document that leaves `telemetry.enabled` as stored is accepted.

### Servers

#### GET /api/v1/servers

List all upstream servers with unified health status.

**Query Parameters:**

| Parameter | Type | Description |
|-----------|------|-------------|
| `profile` | string | Restrict to the named profile's effective servers (intersected with the servers the caller may see). Each row's `tool_count` becomes the number of that server's tools **visible** under the profile, and `stats` are recomputed over the returned rows. An unknown or unreachable profile returns `404 profile not found`. `-` returns `400`. `client` and `token` are not server filters and return `400 unsupported_scope_filter` |

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
all three channels **for an authenticated admin only** (issue #1167). An
agent token, a server-edition non-admin user and an unauthenticated caller
keep getting masked values no matter how the flag is set. The flag has no
effect at all on the `/events` SSE stream or on the `upstream_stats` block of
`GET /api/v1/status`: those payloads are produced once for a mixed-privilege
audience with no caller to check, so they are always masked. An admin
subscribed to `/events` with the flag on receives the notify-only form of
`servers.changed` and re-fetches the raw values through `GET /api/v1/servers`. This is **not normally needed**: the Web UI / macOS
tray / CLI can edit, delete, and convert-to-secret without ever seeing
the plaintext, because the PATCH endpoint deep-merges (omitted keys are
preserved) and the [`config-to-secret`](#post-apiv1serversnameconfig-to-secret)
endpoint reads the real value server-side. Flip the flag only if you
need to inspect a raw value through the API for debugging.

On the MCP channel the flag also requires an **authenticated** caller
(issue #1148): an unauthenticated `/mcp` client is admin only for backward
compatibility, and gets the masked values regardless of the flag. The REST
API always requires an API key, so it is unaffected.

Redaction is not limited to headers. The same responses — and the `/events`
SSE `servers.changed` payloads, which go through the identical redactor — also
mask env values, URL query credentials, `oauth.extra_params`, `oauth.scopes`
and credential-shaped **argv tokens** (`--api-key sk-…`, `--endpoint=ghp_…`),
using one shared rule set so the REST, SSE and MCP doors cannot drift.

Two rules decide, in that order, and **both** run on every field:

1. The **field name** — `Authorization`, `GITHUB_TOKEN`, `?access_token=`,
   `--api-key`. This is what keeps a payload readable: it says *which*
   credential is configured without revealing it.
2. The **value's own shape** — an AWS key, a GitHub token, a PEM block, a
   high-entropy blob — wherever it sits. A credential under a benign name
   (`env: {BUILD_ID: ghp_…}`, `?opaque=ghp_…`, a custom header) is invisible to
   rule 1 and obvious to rule 2, so rule 2 runs over everything rule 1 left
   alone. In a URL it runs per component, so the readable
   `scheme://user:••••(N chars)@host/db` rendering survives while a second
   credential elsewhere in the same URL is still masked.

Three consequences for clients:

- A masked value **echoed back on a write** is reverted only when it can be
  bound to a key it cannot be moved away from — a map key for `env` / `headers`
  / `oauth.extra_params`, a query-parameter name plus the stored scheme and
  `host:port` for `url`, a field name for `oauth.client_secret` /
  `client_id` / `redirect_uri`. So a read-modify-write that edits one field
  never persists another field's mask over the real secret.
- Everything else is **refused** with `400`, never reverted: `args`,
  `oauth.scopes`, `isolation.extra_args`, a mask moved to a different key or
  host, and any field added to the server config later. An argv slot (like a
  scope) has no key to bind a stored secret to — only its index and its
  neighbours, all of them caller-supplied in the same request — so an echoed
  mask is refused rather than reverted. Resend the real value, or omit the
  field to leave the stored one unchanged. `POST /api/v1/servers` refuses
  *every* echoed mask, since on create there is no stored value to bind one to.
  See [Upstream servers](../configuration/upstream-servers.md#how-you-edit-them).
- `GET /api/v1/servers/{id}/logs` scrubs credentials out of the returned log
  lines (mcpproxy logs the upstream URL with its query string, and a child MCP
  server may print its own API key), matching the MCP `tail_log` operation.

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
          "status": "ready",
          "usable": true,
          "actions": []
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
          "action": "login",
          "status": "sign_in_required",
          "usable": false,
          "actions": ["login"]
        }
      }
    ]
  }
}
```

**Health Object Fields:**

| Field | Type | Description |
|-------|------|-------------|
| `level` | string | Severity signal for badge/tray coloring only: `healthy`, `degraded`, or `unhealthy`. **No surface may render this as text** (Spec 109 FR-011) — render `status` through the one label table instead |
| `admin_state` | string | Admin state: `enabled`, `disabled`, or `quarantined` |
| `summary` | string | Human-readable status message |
| `detail` | string | Optional additional context about the status |
| `action` | string | Suggested remediation, always equal to `actions[0]` (or empty when `actions` is empty): `login`, `restart`, `enable`, `approve`, `view_logs`, `set_secret`, `configure`, `edit_url`, or empty |
| `status` | string | (Spec 109 FR-010) The one status vocabulary every surface renders as text: `ready`, `connecting`, `sign_in_required`, `needs_review`, `needs_secret`, `needs_config`, `error`, `disabled` |
| `usable` | boolean | (Spec 109 FR-010) True only when `status == "ready"` — whether the server can currently serve tool calls |
| `actions` | string[] | (Spec 109 FR-012) Every applicable next step, in priority order: `login` > `set_secret` > `configure` > `edit_url` > `approve` > `restart` > `view_logs` > `enable` |

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
  "isolation": {"enabled_override": true, "image": "node:20"}
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
    "available_modes": ["retrieve_tools", "direct", "code_execution"],
    "tool_response_mode": "full",
    "direct_tool_response_mode": "full",
    "pending_routing_mode": "",
    "restart_required": false
  }
}
```

| Field | Meaning |
|-------|---------|
| `routing_mode` | The mode `/mcp` is **actually serving** — recorded when the server bound it at startup, not read back from the config. A config change (API or hand-edited file) cannot rebind `/mcp`. |
| `pending_routing_mode` | The mode the next start will adopt, when it differs from the served one. Empty when nothing is pending. |
| `restart_required` | True exactly when `pending_routing_mode` is set. |
| `tool_response_mode` | Spec 085 serialization of `retrieve_tools` results, resolved (`full` when unset). Hot-reloadable. |
| `direct_tool_response_mode` | Spec 102 serialization of direct-surface listings, resolved (`full` when unset). Hot-reloadable. |

A restart-gated change is persisted immediately and reported as pending; later
changes to other settings apply hot and leave it pending. Re-applying the mode
that is already being served clears it.

See [Routing Modes](../features/routing-modes.md) for details on each mode.

#### MCP tool surface: compact responses and describe_tool (Spec 085)

The MCP endpoints (not REST) additionally expose progressive-disclosure discovery in the `retrieve_tools` routing mode:

- **`tool_response_mode`** config (`full` default | `compact`, hot-reloadable via `POST /api/v1/config/apply`) controls `retrieve_tools` serialization only. In `compact` mode each entry is `{id, score, sig, desc, lossy}` — a one-line parameter signature (`*` = required, `~` = lossy) plus a first-sentence description — instead of full `inputSchema`, and the response carries one top-level `hint` line. Ranking is identical between modes.
- **`detail`** — optional per-call `retrieve_tools` parameter (`compact` | `full`) overriding the configured mode for that call.
- **`describe_tool`** — built-in second-stage tool (retrieve_tools mode only): accepts 1–5 `server:tool` ids and returns full definitions (`name`, `description`, `inputSchema`, `server`, `annotations`, `call_with`) with per-id errors for ids that do not resolve. It applies the same visibility pipeline as search (profile scope, agent-token scope, quarantine, tool approval, disabled) and never returns a definition `retrieve_tools` could not. Per-id error codes: `not_found`, `quarantined`, `pending_approval`, `changed`, `disabled` (Spec 099 retired `invisible`: an out-of-scope id now reports `not_found`, indistinguishable from an id that does not exist — see the [breaking-change note](https://github.com/smart-mcp-proxy/mcpproxy-go/blob/main/CHANGELOG.md)).
- **`describe_tool` check mode (Spec 099)** — the same built-in with `check: true` answers availability instead of definitions: up to 50 ids, an optional `filters` object (`read_only_only`, `exclude_destructive`, `exclude_open_world` — the REST body of `POST /api/v1/preflight` calls the same object `policy`), and a response of `{verdict, checked_at, request_id, results[]}` carrying the same reason codes this endpoint returns. It is the in-band twin of `POST /api/v1/preflight`, evaluated by the same evaluator, and differs deliberately: always the agent-token disclosure tier (never a hash, never `server_not_in_scope`), the session's own scope with no `profile` parameter, no hash pins (`expect_hashes` is a reserved field name and is rejected), and no wait budget. See [Required-Tools Preflight](../features/tools-preflight.md#in-band-describe_tool-check-mode).

### Tools

#### GET /api/v1/tools

Global tools overview (spec 050, issue #437): every tool from **every** configured
server — including disabled servers and individually disabled / config-denied tools —
enriched with approval state and 30-day usage. Read-only; consumers apply their own
search/filter/sort over the full set. For relevance-ranked discovery use
`GET /api/v1/index/search` instead.

> **Quarantine visibility:** `GET /api/v1/index/search` withholds the tools of
> **quarantined** servers — their descriptions/schemas are the Tool Poisoning
> Attack payload quarantine exists to contain, so the REST search path applies
> the same server-level visibility gate as MCP `retrieve_tools`. Response shape
> is unchanged (bare tool `name` + separate `server_name`); only which tools
> appear is filtered. `GET /api/v1/tools` (above) is the unfiltered operator
> overview and still lists quarantined servers' tools with their state.

**Query Parameters (view-as):**

| Parameter | Type | Description |
|-----------|------|-------------|
| `client` | string | Administrator only. Show what this client would see and be able to call. The client's credential, binding and current state are resolved exactly as its connection resolves them. A caller that is not an administrator gets `403 operation requires admin access`; an unknown client gets `404 client not found` |
| `profile` | string | Show what this profile would expose. An unknown profile, or one a non-administrator cannot reach, returns the same `404 profile not found` |

`client` and `profile` are mutually exclusive (`400 use either client or profile, not both`), `-` is not valid here (`400`), and `token` is not a tools filter (`400 unsupported_scope_filter`).

With either parameter every row gains `profile_tier` (the tool's tier under the subject's profile; `tier` stays the tool's intrinsic tier) and `access {visible, callable, reason}`. `visible` means the subject's discovery would list the tool; `callable` means a real call would succeed, and it equals the outcome of one for every row. `reason` is empty when callable, otherwise it names the first failing step of the access chain (`credential`, `profile`, `server_in_scope`, `tool_rule`, `tier_cap`, `token_permission`, `global_gate`, `server_state`, `tool_approval`), where the profile decision reports its own reasons: `server_not_in_profile`, `denied_by_rule`, `unannotated_hidden`, `above_tier_cap`. `read_only_mode` gates management operations only, never an upstream tool, so it is never a `reason` for an upstream row. Rows of a quarantined server are not listed, with or without view-as.

An administrator gets every row. A non-administrator (`profile=` only) gets just the rows that are visible under the profile, without a `reason`, plus `counts {visible, hidden}` for the rest; excluded rows, their tiers and their reasons are administrator-only. `stats` are recomputed over the returned rows. Without `client` or `profile` the response is unchanged.

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

Operator-tier callers (admin API key, Unix socket, named pipe) additionally
receive each approved tool's current schema-hash pin in `hash`
(`sha256/v{N}:{hex}`) — the authoring surface for `POST /api/v1/preflight`
pins. Agent tokens never receive hashes.

#### GET /api/v1/servers/{name}/tools

List tools for a specific server. Carries the same operator-tier `hash` pin
field as the global listing.

#### POST /api/v1/preflight

Required-tools preflight (Spec 098): a deterministic, side-effect-free
availability check for a caller-supplied list of tool IDs. It performs **zero
upstream calls** and mutates no runtime state — verdicts are computed from
local state only (tool index, approval records, connection-state snapshot,
config policy). The HTTP status reports whether the **check executed**, never
what it found: a fully blocked set is still a `200` carrying
`verdict: "blocked"` in the body. See
[Required-Tools Preflight](../features/tools-preflight.md) for the feature
guide and `mcpproxy tools preflight` for the CLI wrapper.

**Request Body:**
```json
{
  "tools": [
    { "id": "gh-ops:sync_issues" },
    { "id": "ctl:echo", "pin_hash": "sha256/v1:9f86d081884c7d65..." }
  ],
  "profile": "work",
  "policy": { "read_only_only": true },
  "wait_ms": 5000
}
```

| Field | Type | Description |
|-------|------|-------------|
| `tools` | array | Required, 1–100 entries — the limit applies to the **raw** array, before dedup. Each entry carries `id` (`<server>:<tool>`) and optional `pin_hash`. Duplicate IDs are deduplicated (one result per unique ID); duplicates carrying **different** `pin_hash` values are a `400`. |
| `profile` | string | Optional. Evaluate under this profile's server scope so verdicts match a profile-pinned session's view. Unknown profile: `400`. Omitted: unscoped operator view. |
| `policy` | object | Optional annotation filters, Spec 094 semantics: `read_only_only`, `exclude_destructive`, `exclude_open_world` (evaluated in that fixed order; the first excluding filter owns the verdict). |
| `wait_ms` | integer | Optional, 0–10000. Poll local state while every failure is retryable-class (see below). Values over the cap are a `400`, not a silent clamp. |

**Response** (standard `APIResponse{data}` envelope):
```json
{
  "success": true,
  "data": {
    "verdict": "blocked",
    "checked_at": "2026-08-15T06:00:00Z",
    "waited_ms": 0,
    "tools": [
      {
        "id": "gh-ops:sync_issues",
        "status": "ready",
        "hash": "sha256/v1:9f86d081884c7d65..."
      },
      {
        "id": "slack:post_message",
        "status": "unavailable",
        "reason": "server_disabled",
        "retryable": false,
        "action": "enable",
        "detail": "Server \"slack\" is disabled.",
        "remediation": "Enable the server (mcpproxy upstream enable <server>)."
      }
    ]
  }
}
```

Results are ordered by first occurrence of each unique ID in the request. A
`ready` result omits all failure fields — `ready` is a status, not a reason. An
`action` with no value is **omitted**, not `"none"` (matching the health-action
vocabulary). A malformed ID (missing the `:` separator) gets a **per-ID**
`not_found` with a format hint in `detail`, never a request-level error — one
bad entry cannot mask verdicts for the rest. `not_found` results may carry
`did_you_mean` (up to 3 nearest caller-visible IDs). `waited_ms` is present
whenever `wait_ms` was requested, including as `0` (see wait semantics).

**Failure reasons** (closed enum, Spec 098 FR-003). Evolution is additive-only;
treat unknown codes as non-retryable. `server_saturated` is reserved and never
emitted. When multiple states co-occur for one ID, exactly one reason is
reported per the fixed precedence order (server-level states before tool-level;
see the feature page).

| `reason` | `retryable` | Default `action` | Set verdict | CLI exit |
|---|---|---|---|---|
| `server_initializing` | true | — (omitted) | `degraded_retryable` | 10 |
| `server_unhealthy` | true | best-effort from diagnostics (`restart`/`login`/`view_logs`; default `view_logs`) | `degraded_retryable` | 10 |
| `server_disabled` | false | `enable` | `blocked` | 11 |
| `server_quarantined` | false | `approve` | `blocked` | 11 |
| `tool_pending_approval` | false | `approve` | `blocked` | 11 |
| `tool_changed` | false | `approve` | `blocked` | 11 |
| `tool_blocked_by_user` | false | `enable` | `blocked` | 11 |
| `oauth_required` | false | `login` | `blocked` | 11 |
| `hash_mismatch` | false | `configure` | `blocked` | 11 |
| `server_not_in_scope` (operator tier only) | false | `configure` | `blocked` | 11 |
| `tool_denied_by_config` | false | `configure` | `blocked` | 11 |
| `missing_annotation` | false | `configure` | `blocked` | 11 |
| `policy_filtered` | false | — (omitted) | `blocked` | 11 |
| `not_found` | false | `configure` | `unknown_ids` | 12 |
| `server_not_configured` | false | `configure` | `unknown_ids` | 12 |
| `tool_blocked_by_profile` (operator tier only) | false | `configure` | `blocked` | 11 |

`tool_blocked_by_profile` (added for issue #1548) means the effective profile
tool policy — the token's pin or client binding, plus any `profile` in the
body, all of which must admit the tool — excludes an existing tool (tier cap,
deny rule, unannotated handling). `detail` is the exact `blocked by profile: ...`
text `call_tool_*` would return. A caller at the agent-token tier receives the
scope-silent `not_found` instead, matching discovery, which hides the tool.

A `tool_pending_approval` occurrence for a tool the server's discovery
snapshot contains but that has **no stored approval record yet** carries
`action: "restart"` instead of `approve`, with a detail/remediation that
points at re-discovering the server (`upstream_servers operation="refresh"`
or `mcpproxy upstream restart <server>`): nothing is listed to approve until
the server's next discovery pass files the record. The reason code, exit code
and telemetry counter are unchanged.

The set-level `verdict` is the worst class present:
`unknown_ids` > `blocked` > `degraded_retryable` > `ready`.

**Status codes:**

- `200` — the check executed; the availability verdict is data in the body.
- `400` — validation error: invalid JSON, empty or oversized (>100 raw entries)
  `tools`, a duplicate ID with conflicting `pin_hash` values, `wait_ms` out of
  range, or an unknown `profile`. The body is read strictly — at most 1 MiB,
  exactly one JSON object, and no unknown fields — so a mistyped key (`wait`
  for `wait_ms`, `pin` for `pin_hash`) fails loudly instead of silently
  weakening the check a pipeline then trusts.
- `401` — missing or invalid credentials.
- `503` — the check could not run honestly: the runtime is unavailable, an
  index/storage/snapshot read failed (reduced-fidelity verdicts are never
  emitted), or the activity record could not be persisted.

A request rejected with `400`/`503` executed no preflight and writes **no**
activity record.

**`wait_ms` semantics:** polling happens only while **every** current failure
is retryable-class (`server_initializing` / `server_unhealthy`). The endpoint
re-evaluates local state on a floor interval of ≥250 ms until every tool is
ready, a non-retryable failure appears (waiting cannot help, so it resolves
immediately), or the deadline passes; it always resolves with current reasons —
never hangs. Waiting capacity is a small fixed semaphore (4 slots) dedicated to
preflight; when it is exhausted the request degrades gracefully — it resolves
immediately with current verdicts and `waited_ms: 0` instead of queuing or
failing.

**Disclosure tiers:**

- **Operator tier** (admin API key, Unix socket, Windows named pipe — plus the
  server edition's OAuth **admin**): full results — `hash` pins on ready
  results, `did_you_mean` suggestions, and the `server_not_in_scope` diagnosis
  when a supplied `profile` excludes an existing server (with a `detail` noting
  that a session under that profile sees `not_found`).
- **Agent-token tier** (agent tokens, the server edition's ordinary OAuth users,
  and the whole in-band `describe_tool` check surface): scope-silence — an
  out-of-scope ID's entire result is
  byte-indistinguishable from an ordinary `not_found` (same wording; no hashes;
  no `did_you_mean` crossing the scope boundary). `did_you_mean` is computed
  over the caller-visible index only and never suggests a quarantined server's
  tools.

**Activity-record guarantee:** every request answered `200` writes an activity
record **synchronously, before the response is returned** — request ID,
requested-ID count (unique IDs, after dedup), set verdict, and per-tool reason
codes (tool IDs and enum
codes only; no descriptions, no arguments, no hashes; local-only, never
telemetry). Correlate via the `X-Request-Id` response header and
`mcpproxy activity list --request-id <id>`.

**Hash pins** (`pin_hash`): format `sha256/v{N}:{hex}`. The hash schema version
is embedded so a proxy-side hash-algorithm bump is distinguishable from genuine
upstream drift (both report `hash_mismatch`, with different `detail`). Current
pins are discoverable on ready preflight results and on the operator-tier tool
listings above.

### Attention

#### GET /api/v1/attention

The one needs-attention list every surface renders (the Web UI Home page, header pill and sidebar badge, the macOS tray and Home section, `mcpproxy attention`, the first line of `mcpproxy status` and the first section of `mcpproxy doctor`). Both editions. See [Needs Attention](../features/needs-attention.md) for the kinds, ranks and fixes.

Administrators see every item. A scoped caller (an agent token, or a non-admin user session) sees only the items whose server it may enumerate, never a client or setting item, with `count` recomputed from the narrowed list.

```json
{
  "success": true,
  "data": {
    "count": 2,
    "generated_at": "2026-09-25T06:12:03Z",
    "items": [
      {
        "id": "sign_in_required:server:github",
        "kind": "sign_in_required",
        "rank": 10,
        "subject": {"type": "server", "id": "github", "name": "github"},
        "summary": "github: sign in required",
        "detail": "OAuth · api.githubcopilot.com",
        "fix": {"verb": "login", "label": "Sign in", "target": "/servers/github"},
        "since": "2026-09-25T06:02:11Z"
      },
      {
        "id": "server_review:server:github",
        "kind": "server_review",
        "rank": 50,
        "subject": {"type": "server", "id": "github", "name": "github"},
        "summary": "github: waiting for review",
        "fix": {"verb": "review", "label": "Review", "target": "/review/github"},
        "since": "2026-09-25T06:01:40Z"
      }
    ]
  }
}
```

Items are sorted by `rank` ascending, then by `subject.name`; `id` (`kind:type:subject[:state]`) is stable, so a client can diff successive lists. `fix.target` is a Web UI route, and a fix never approves anything by itself. The SSE event `attention.changed` (`{count, ids}`) is emitted when the set of ids changes; see [Real-time Updates](#real-time-updates).

### Review

#### GET /api/v1/review

The review queue: one row per server awaiting review, either a quarantined server (`kind: server_review`) or a trusted server with new or changed tools (`kind: tool_review`, with `pending` and `changed` counts). `count` is the number of rows, the number the Review queue badge shows. For a scoped caller (an agent token or a non-admin user session) the queue lists only the servers that caller may see.

```json
{
  "success": true,
  "data": {
    "count": 2,
    "servers": [
      {"server": "filesystem", "kind": "server_review", "quarantined": true, "tools_captured": 14,
       "tier_counts": {"read": 9, "write": 3, "destructive": 2, "unannotated": 0, "unknown": 0},
       "scan": {"verdict": "clean", "risk_score": 0}, "since": "2026-09-25T06:01:40Z"},
      {"server": "github", "kind": "tool_review", "quarantined": false, "pending": 0, "changed": 1,
       "since": "2026-09-25T06:10:00Z"}
    ]
  }
}
```

#### GET /api/v1/servers/{id}/review

The review payload of one server: a summary of the server (secrets in the URL, headers, environment and command line are always redacted) and every tool with its captured definition. A server the caller cannot see answers the same `404` as a missing one.

```json
{
  "success": true,
  "data": {
    "server": {"name": "filesystem", "transport": "stdio", "quarantined": true, "trust_mode": "manual",
               "scan": {"verdict": "clean", "risk_score": 0, "coverage": "current", "tools_scanned": 2},
               "definitions_captured": true},
    "tools": [
      {"name": "edit_file", "description": "Make line-based edits to a text file", "input_schema": {"type": "object"},
       "annotations": {"destructiveHint": true}, "tier": "destructive", "approval_status": "pending",
       "disabled": false, "scan_verdict": "clean"},
      {"name": "search_code", "description": "Search the code", "annotations": null, "tier": "unknown",
       "approval_status": "changed", "scan_verdict": "warnings",
       "previous": {"description": "Search files", "input_schema": {}, "annotations": null},
       "diff": {"description": "@@ -1 +1 @@\n-Search files\n+Search the code", "input_schema": "", "annotations": ""}}
    ]
  }
}
```

- `tier` is `read`, `write`, `destructive`, `unannotated` (annotations captured, no hints) or `unknown` (nothing captured, a record from before the review screen). It comes from one function, so the Web UI, macOS, `mcpproxy tools list --tier` and the MCP `quarantine_security` inspect operations show the same value.
- `approval_status` is `approved`, `pending` (shown as "New, needs review") or `changed` ("Changed, needs review").
- `scan.coverage` says whether the scan verdict describes the definitions in the payload: `current` (the latest completed scan analysed every captured definition as it is now), `stale` (some definitions were added or changed after that scan; `scan.unscanned_tools` lists them), `not_captured` (no definitions captured), `tools_not_scanned` (the scan completed but exported no tool definitions), `scanning` (a scan is running) or `none` (no completed scan). Show `risk_score` only for `current`. `scan.tools_scanned` is the number of definitions that scan exported.
- `default_allowed` (always present) is the review screens' fail-closed default selection: `true` for an approved tool, or for a pending or changed `read` tool with `scan_verdict` `clean` that is not held; `false` for everything else (write, destructive, unannotated, unknown, not scanned, warnings, dangerous, held, or already disabled). A client that finds no such field (an older core) treats it as `false`.
- `scan_verdict` is `dangerous`, `warnings`, `clean` or `not_scanned`. `clean` means the latest scan covered this tool's current definition and found nothing; a tool whose definition changed after the scan is `not_scanned` (or carries its held verdict).
- `definitions_captured: false` returns `tools: []`; `POST /api/v1/servers/{id}/discover-tools` captures the definitions without indexing them. After a baseline scan has listed a still-quarantined server's tools, MCPProxy runs the same capture itself.
- Descriptions are returned verbatim and must be rendered as inert text.

The review decisions use these routes (all existing):

| Decision | Route |
|---|---|
| Approve server | `POST /api/v1/servers/{id}/security/approve` with an optional `{"force": true, "block": ["tool", ...]}`; the `block` tools are blocked in the same transaction that records the integrity baseline, before the server is unquarantined |
| Reject server | `POST /api/v1/servers/{id}/security/reject` |
| Approve tools | `POST /api/v1/servers/{id}/tools/approve` |
| Reject (block) tools | `POST /api/v1/servers/{id}/tools/block` |

`POST /api/v1/servers/{id}/unquarantine` is kept for API compatibility only; no first-party surface calls it. See [Review Commands](../cli/review-commands.md).

### Catalog

#### GET /api/v1/catalog/search

Search every enabled catalog source (registry) at once. Both editions; open to any authenticated caller, with `added` the only field that depends on the caller's scope.

| Parameter | Description |
|---|---|
| `q` | Free-text query. Empty returns `results: []` and fills `sections` (`official` and `popular`, up to 12 each) |
| `source` | Narrow to one catalog source id, applied before ranking and the limit |
| `limit` | Maximum results (default 20, maximum 50) |
| `tag` | Not supported: catalog entries carry no tags, so a non-empty value returns `400` |

```json
{
  "success": true,
  "data": {
    "query": "github",
    "results": [
      {"source": "official", "id": "io.github.github/github-mcp-server", "title": "GitHub",
       "publisher": "github", "verified": true, "official": true, "popularity": {"stars": 21000},
       "description": "GitHub's official MCP server", "transport": "http",
       "install": {"url": "https://api.githubcopilot.com/mcp/"},
       "required_inputs": [{"name": "GITHUB_TOKEN", "secret_like": true}], "added": false}
    ],
    "sections": null,
    "unavailable": [{"source": "community", "reason": "timeout after 5s"}]
  }
}
```

Results are ranked by how well the name matches the query first (the publisher equals the query, then an exact name, a name prefix, a name word, a substring or description, and last a match through the namespace alone, which is how `io.github.*` entries match), then official source, verified publisher, popularity (a missing value counts as zero), title and id. `verified` means the publisher owns the source repository (or the entry comes from a trusted Docker or reference source), `official` means the entry comes from a built-in source, and `title` is the server's own title when it has one. The order is identical on the Web UI, macOS, the CLI (`mcpproxy catalog search`) and the MCP `search_servers` tool. A source that fails or times out is listed in `unavailable` and the other sources' results are still returned. When the daemon has a recent listing of that source (at most 24 hours old, kept in memory and filled by every successful fetch), the matches come from it instead: those results carry `from_cache: true` and the `unavailable` entry gains `fallback: "cached_listing"` and `cached_at`, so the source still reads as unavailable. An empty `q` lists `popular` before `official` in every surface, and `official` starts with the curated reference servers. `added` is true when a configured server visible to the caller has the same source and install target, and then `added_server_name` names it. Adding an entry stays `POST /api/v1/registries/{id}/servers/{serverId}/add`, which always quarantines the new server; see [Registry Add](../features/registry-add.md).

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
VS Code, Codex, Gemini, OpenCode, ZCode, …).

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
| `proxy_url` | string | **This** instance's MCP endpoint. Config-derived (no file read), so it is present on the overall listing too. |
| `registered_url` | string | The endpoint the client's existing entry actually points at, projected through the same sanitizer as a connect preview's `existing_entry_summary.endpoint`: **scheme, host and path only** — query (the `?apikey=` carrier), userinfo and fragment are dropped, and a value that is not an absolute URL is not echoed at all. Resolved only by an on-demand read. |
| `endpoint_match` | string | How `registered_url` relates to `proxy_url`: `"this"` (the entry addresses this instance), `"other"` (it addresses a different one), `"unknown"` (the entry has no comparable endpoint, e.g. a stdio command). Empty when `connected` is false or nothing was read. |

A client that is installed but not yet content-checked reads as
`exists=true, connected=false, access_state="unknown"`. Resolving `connected`
requires an explicit per-client read (the per-client status route below,
connect/disconnect, or the CLI `mcpproxy connect` command), which is where a
privacy prompt may legitimately appear.

**`connected` alone is not "connected to this instance."** It means an
mcpproxy-shaped entry is present in that client's config — and an entry merely
*named* `mcpproxy` counts, even when its URL addresses another instance on
another port. Consumers that need the stronger claim must check
`endpoint_match == "this"`; a UI showing a bare "Connected" on
`endpoint_match == "other"` is reporting someone else's link as its own.

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

`DELETE` removes the entry **and revokes the client's credential** (the
`client-<id>` token stops authenticating at once, a `forget` change record with
`disconnected: true` is written). The result names it in `credential_revoked`;
a client with no credential leaves the field empty. If the entry was removed but
the revoke failed, the response is still `200`: `credential_revoke_error` carries
the reason and `DELETE /api/v1/clients/{client}` retries the revoke. Undoing a
disconnect (below) restores the file only: the credential stays revoked and a
later connect mints a new one. Apart from that, connect/disconnect are unchanged
except that a permission-denied config access
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

`POST` optionally accepts `precondition_token` (Spec 091) — the opaque token
from the preview this write was confirmed against:

```json
{ "server_name": "mcpproxy", "force": true, "precondition_token": "…" }
```

When supplied, the core recomputes the token at write time and, if it no longer
matches, responds **`409 Conflict`** having written **nothing** (the check runs
before any backup or write). `force=true` does not override a stale token. When
omitted, behavior is exactly as before.

The `409` body carries a top-level `action` discriminating the two conflict
kinds:

| `action` | Meaning | Caller should |
|----------|---------|---------------|
| `precondition_failed` | The preview is stale — the config file, the existing entry, or the entry mcpproxy would now write has changed. | Re-fetch the preview; do not blindly retry. |
| `already_exists` | Pre-existing semantics: an entry with that name is present and `force` was not set. | Confirm with the user, then retry with `force=true` (and a fresh token). |

See [Connect Clients](../features/connect-clients.md) for the token's contents
and threat model.

**Client credential (Spec 108).** The body also accepts `profile` (a profile
name; `""` is All servers; omitted means All servers for a fresh credential and
the existing binding on a reconnect, including over an expired or revoked
credential: a revoked record keeps its prior pin and mode, so a profile-locked
client is never silently widened to All servers), `mode` (`locked` or `switchable`) and
`keyless`. The write embeds a per-client `mcp_cli_` credential, never the admin
API key, and the result carries `credential` (masked), `token_name`, `profile`,
`mode`, `keyless` and, for a reconnect over an active credential, `rotation`
(`finalized`). Refusals write nothing: `400 {error, field}` for an unknown
profile, an invalid mode, or `keyless` with `require_mcp_auth` on or with a
profile; `409 binding_bypassable_without_auth` (`bindings`, `fixes`) when the
binding would be bypassable while `require_mcp_auth` is off; `409` with
`conflicting_token` when `client-<id>` is held by a regular agent token.

#### PUT /api/v1/clients/{client}/binding

Reassigns a client's profile and/or mode (personal edition, administrator
only). Body `{"profile": "<name or empty for All servers>", "mode": "locked|switchable"}`
(`profile` required; `mode` optional — omitted keeps the current mode, except
that an empty profile means switchable). Applies only to a client that holds an
active client credential; otherwise `409 {code: "no_client_credential"}` and
nothing is minted. Updates the credential in the token store, clears the stored
`set_profile` selection of every live session of that credential, sends
`notifications/tools/list_changed` to each and writes one `profile_change`
activity record; the client's config file is never touched. A reassignment that
would leave the binding bypassable is refused with `409
binding_bypassable_without_auth`. `PATCH /api/v1/config`, `POST
/api/v1/config/apply` and `PATCH /api/v1/config/docker-isolation` answer the
same `409` when the write would create that condition. The response is
`{client, warnings}`: the full client row (see `GET /api/v1/clients`) and the
warnings about it.

#### GET /api/v1/connect/{client}/preview

Returns the exact change a subsequent connect would make — target config path,
format (`json`/`toml`), server key, entry name, and the exact entry contents —
**without** modifying the file or creating a backup (Spec 078 US1). An embedded
client credential is masked in the payload (`credential`, always
`mcp_cli_••••`; `contains_api_key` is always `false` since connect never
writes the admin API key); `profile`, `mode` and `keyless` echo the requested
intent (`?profile=&mode=&keyless=`); `entry_exists` distinguishes a create from
an overwrite of a same-named entry. Reads the config on demand to classify create-vs-overwrite,
so on macOS this may raise an App-Data prompt; a denial returns `403` +
remediation. Optional `?server_name=` mirrors the name a subsequent connect
would use.

Spec 091 adds three response fields:

| Field | Type | Meaning |
|-------|------|---------|
| `existing_entry_summary` | object, present only when `entry_exists=true` | Sanitized description of the entry that would be **replaced**: `entry_name` (the key it actually lives under, which may differ from `server_name` when the write adopts an endpoint-equivalent entry), `type`, `endpoint` (query string, `user:pass@` userinfo and fragment stripped), `command`, `header_names` and `env_names` — **names only, never values**. Built by whitelist projection, so no other config content can reach the response. |
| `precondition_token` | string, always present | Opaque keyed HMAC binding this preview to the exact pre-write state. Pass it to `POST` (above) to make a stale preview unwritable. Per-core-instance key, never persisted. |
| `connect_refusal` | string, optional | The verbatim reason a subsequent connect would refuse regardless of user intent (today: a non-create-capable client such as **OpenCode** with no config present). Treat as "Connect unavailable". |

The preview evaluates the refusal with the write's own guard, so the two cannot
drift. Full semantics: [Connect Clients](../features/connect-clients.md).

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

### Clients (personal edition)

Client routes live under `/api/v1/clients` and are administrator-only. They do
not exist in the server edition, which has no per-client credentials.

#### GET /api/v1/clients

Every client row (`kind` is `supported`, `other` or `custom`) and response-level
`warnings[]`. The Spec 109 presence fields (`id`, `display_name`, `kind`, `icon`,
`state`, `installed`, `connected`, `config_path`, `display_path`, `last_seen`,
`active_sessions`, `calls_24h`, `reload_hint`) are unchanged; Profiles v3 adds:

| Field | Meaning |
|-------|---------|
| `credential_state` | `client`, `admin_key`, `none`, `revoked`, `expired` or `unknown` |
| `credential_checked_at` | When the classification was last read from the client's config (present when it came from an observation) |
| `token_name`, `profile`, `profile_title`, `profile_mode`, `profile_source` | The binding. `profile_source` is `pin` (locked) or `binding` (switchable) and is empty unless `credential_state` is `client` |
| `profile_missing` | The bound profile no longer exists: the client is denied everything |
| `expires_at`, `rotation_pending`, `blocked_24h` | Credential expiry, an unfinished rotation, blocked calls in the last 24 hours |

The list never reads a client config (macOS shows no App-Data prompt for it).
`credential_state` comes from the token store, else from the last on-demand
classification (`GET /clients/{client}`, `GET /connect/{client}`, the admin-key
upgrade preview), else it is `unknown`. A `custom` row is a credential added
with `POST /clients`; a revoked custom row is omitted here and still served by
the detail route.

`warnings[]` items are `{code, severity, client_id?, message, action?, bindings?, fixes?}`.
Codes: `anonymous_denied_by_binding_guard`, `client_holds_admin_key` (action
`upgrade_admin_key_holders`), `client_credential_expiring`,
`client_rotation_pending` (severity `info`), `profile_missing` and
`client_token_name_conflict`.

| Query | Meaning |
|-------|---------|
| `profile` | Rows whose active credential is bound to this profile (a dangling pin included); `-` = bound to All servers. A row with no active credential matches no profile |
| `client` | Exact client id (also `other:<id>` and custom ids) |

The filters apply after `warnings` are computed over the full set. An unknown
value is not an error. `token` is not a clients filter (`400 unsupported_scope_filter`).

#### GET /api/v1/clients/{client}

The row plus `sessions[]` (the latest 20, each with its `profile` and
`profile_source`). This is the one read that may open the client's config: it
runs the full rotation reconciler first, classifies the credential the config
holds and records the observation. No scope filter is honoured here.

#### POST /api/v1/clients

Adds a custom client (one not in the connect registry). Body `{id,
display_name?, profile?, mode?, expires_in?, purpose?}`; `expires_in` defaults to and is
capped at 365 days; `purpose` is an unenforced note of at most 500 characters.
`201 {client, credential, snippet}`: `credential` is the
`mcp_cli_` secret, shown once; `snippet.generic_http` is a paste-ready JSON
config that carries it in the `X-API-Key` header. Refusals: `400 {error, field}`
(the id rule, a supported client's id, profile, mode, `expires_in`,
`display_name`, `purpose`), `409 binding_bypassable_without_auth`, `409` with
`conflicting_token`. Every value is screened first: a credential, the API key or
a detected secret answers `400 {code: "secret_in_argument", field}` and nothing is
created. A malformed body answers `400` with a sanitized text that never quotes a
caller key or value (`invalid request body: unrecognised field (its name is not
echoed)`, `... field "<known field>" has the wrong type`, `... malformed JSON at
byte <n>`). The add writes one `profile_change` record with `change: issue`
(Spec 115; it used to be `assign`) and publishes `credentials.changed`.

Client rows (list and detail) add the credential's `revoked_at`, `issuer
{actor_kind, actor_name?, surface}`, `purpose`, `lease` (lifetime at issue of at
most 24 h) and `profile_state` (`ok`, `dangling`, `none`). An exact
`?client=<id>` filter also returns that client's revoked custom row.

#### POST /api/v1/clients/{client}/rotate

Replaces the secret without invalidating the old one mid-flight. For a
supported client it rewrites the entry through connect (staged rotation, the
binding is kept, finalized when the write succeeds, rolled back when it
fails); body `{precondition_token?}` binds it to a connect preview, and a
mismatch is the connect `409` with `action: "precondition_failed"`. Response
`{client, connect, rotation: {state: "finalized"}}`. For a custom client the
response is `{client, credential, snippet, rotation: {state: "pending"}}`; both
secrets authenticate until `POST /clients/{client}/rotate/finalize` or 24 hours.
A client with no active credential is `409 no_client_credential`.

#### POST /api/v1/clients/{client}/rotate/finalize

Promotes the pending secret; the old one stops authenticating. Idempotent.
`{client, rotation: {state: "finalized"}}`.

#### DELETE /api/v1/clients/{client}

Revokes the client's credential. With `disconnect=true` a supported client's
config entry is removed first, but the credential is revoked either way:
revocation never waits for a file write. Response `{revoked, disconnected,
disconnect_error?}`. No credential record: `409 no_client_credential`.

#### POST /api/v1/clients/bulk-assign

Body `{from_profile, to_profile, mode?}` (both required; `""` = All servers).
Moves every client bound to `from_profile`. The FR-008a guard and the
credential precondition apply per client: `{moved: [ids], skipped: [{client_id,
code, error}]}`. Each moved client writes an `assign` record and emits
`client.binding_changed`.

#### POST /api/v1/clients/upgrade-admin-key-holders

Moves every supported client whose config still holds the admin API key onto a
per-client credential. Body `{profile?, mode?, apply?, precondition_token?}`.
Without `apply` it returns `{preview: [{client_id, display_name, display_path,
diff, credential: "mcp_cli_••••", profile, mode, precondition_token}],
precondition_token, guard?, next_step?}`; nothing is written or minted, `guard`
reports the refusal an apply would get, and `next_step` is
`rotate_admin_api_key` when nothing holds the key. With `apply: true` a sent
`precondition_token` that no longer matches is `409` with `code:
"precondition_failed"`; a named profile that would make the bindings
bypassable is `409 binding_bypassable_without_auth` (nothing minted, no file
written); otherwise `{upgraded, failed: [{client_id, error}], next_step?}`. The
guard applies only when a named profile is given.

### Profiles

Profile routes exist in both editions. Every mutating route is administrator
only and writes one `profile_change` record; none is gated by `read_only_mode`.
A write that would let a bound client escape its profile by omitting its
credential while `require_mcp_auth` is off is refused `409
binding_bypassable_without_auth` (personal edition) and writes nothing.

#### GET /api/v1/profiles

`{profiles: ProfileView[], anonymous_profile?}`. A `ProfileView` has the config
fields as stored (`name`, `title`, `description`, `servers`, `max_tier`,
`unannotated`, `tools {allow, deny, classify}`, `code_execution`,
`management_tools`, `switchable_to`), the derived `effective_servers`,
`effective_unannotated`, `effective_code_execution`, `is_legacy`,
`tool_counts {read, write, destructive, unannotated_hidden}` (visible tools by
the tier the profile gives them), the deprecated v2 `tool_count`, `calls_24h`,
`blocked_24h`, and `used_by {clients, tokens, anonymous_profile}`.

A caller that is not an administrator sees only profiles its entitlement
reaches (an unreachable one is omitted, never shown empty), with `servers`,
rule entries and `switchable_to` narrowed to what it may see. `used_by` and
`anonymous_profile` are administrator-only and omitted, not emptied, otherwise.

#### GET /api/v1/profiles/{name}

One `ProfileView`. A non-administrator gets the same `404 profile not found`
for an unreachable profile as for an unknown one.

#### POST /api/v1/profiles, PUT /api/v1/profiles/{name}

Body is a `ProfileConfig`. `POST` answers `201 {profile, warnings}`; `PUT`
answers `200` with the same shape and requires the body's name, when it is sent,
to equal the path (`409 name_mismatch`). A `PUT` body whose `name` is omitted or
an empty string takes the path's name (accepted by the spec as a convenience for
the Web UI and CLI); a different non-empty name is always refused. `409 profile_exists`; `400 {error, field}` names the
offending field with the unchanged validator text. `active` and `try` are
reserved by the REST API. A `PUT` whose only change is `tools.classify` is
recorded as `classify`.

#### POST /api/v1/profiles/{name}/rename

Body `{new_name}`. Moves token pins, client bindings, other profiles'
`switchable_to` and the `anonymous_profile` in one write; tokens move first, so
a failure never widens a scope. `{profile, moved: {clients, tokens}}`.

#### DELETE /api/v1/profiles/{name}

Query `reassign_to` and `force`. `409 profile_in_use` (with `used_by`) while
clients or tokens point at it, unless `reassign_to` names another existing
profile (every pin moves there) or `force` leaves them dangling (deny-all).
`409 profile_is_anonymous_profile` whenever it is the `anonymous_profile` and
`reassign_to` is absent, even with `force`. Both paths remove the name from every
`switchable_to`. `{deleted, moved, anonymous_profile_moved_to?}`.

#### GET /api/v1/profiles/{name}/effective-tools

Query `client`, `server`, `reason`. One row per catalog tool: `server`, `tool`,
`intrinsic_tier`, `profile_tier`, `access {visible, callable, reason}`,
`classification_stale`. With `client=` the client's credential is evaluated under
this profile. An administrator also gets `counts.callable`, `counts.by_reason`
and `stale_classifications`; every other caller gets only visible rows and
`counts {visible, hidden}`, and `client=` / `reason=` are `403`.

#### POST /api/v1/profiles/try

Body `{profile, query, limit?}` (default 10, maximum 50). Evaluates a draft
profile as `retrieve_tools` would, with policy applied before the limit, and
returns the hits, `hidden_by_profile` and up to 100 `hidden {server, tool,
reason}`. Nothing is persisted, no record is written, the guard does not run.

#### GET /api/v1/profiles/active, PUT /api/v1/profiles/active

Deprecated. Both send `Deprecation: true` and a `Link` header whose
`successor-version` is `/api/v1/profiles`. Behaviour and `active_profile.changed`
are unchanged.

### Access explain

#### GET /api/v1/access/explain

Query `tool` (an upstream `server:tool`) and exactly one of `client`, `token`,
`profile` or `anonymous=true`. Administrator only. Returns the ordered chain of
gates a real call would meet (`credential`, `profile`, `server_in_scope`,
`tool_rule`, `tier_cap`, `token_permission`, `global_gate`, `server_state`,
`tool_approval`), each `pass`, `fail` or `skip`; the `verdict` (`allowed` =
callable, `hidden` = not visible, `blocked` = visible but not callable); the
`first_failure`; and `fixes[]` for it in preference order, each `{step, action,
target, label}`. The chain is the one every discovery and dispatch path walks, so
`allowed` equals what a real call does. Errors: `400 exactly one of client, token,
profile, anonymous is required`, `400 use client=<id> for a client credential`
(a `client-` token name), `400 access/explain covers upstream tools (server:tool)
only`, `404` for an unknown client, token or profile.

### Tokens

`GET /api/v1/tokens` rows add `kind` (`agent` or `client`), `client_id`,
`profile_mode` and `legacy_scope` (true when `allowed_servers` is not `["*"]` or
the permissions are not all three). `?profile=<name>` matches the **current**
`profile_pin` of either kind (`-` = unpinned) and `?token=<name>` is an exact
name; an unknown value returns no rows. `POST /api/v1/tokens` accepts `profile`
(the Profiles v3 spelling of `profile_pin`; with it, omitted `allowed_servers`
and `permissions` default to `["*"]` and all three). A name starting with
`client-` is `400 {error, field: "name"}`.

Spec 115: token rows add `revoked_at`, `issuer`, `purpose`, `lease` and
`profile_state`. `POST /api/v1/tokens` accepts `purpose`, screens every value
(including each `allowed_servers` and `permissions` element and the raw
`expires_in`) for secrets first (`400 {code: "secret_in_argument", field}`, nothing
created), answers malformed bodies with a sanitized text, writes a
`profile_change` record with `change: issue` and publishes `credentials.changed`.
`DELETE /api/v1/tokens/{name}` stamps `revoked_at`, writes `change: revoke` (only
when the token was not already revoked) and publishes `credentials.changed`.
Neither route enforces the binding guard; the MCP `credentials` tool does (see
[Credential lifecycle over MCP](../features/mcp-credential-lifecycle.md)).

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
- `profiles.changed` - A profile was created, updated, renamed, deleted or the `anonymous_profile` changed (an invalidation: refetch `GET /api/v1/profiles`)
- `client.binding_changed` - A client's profile or mode was reassigned (an invalidation: refetch `GET /api/v1/clients`)
- `credentials.changed` - A client credential or agent token was issued or revoked, or a client was forgotten, on any surface (`{kind, id, token_name, change, profile}`, an invalidation with no secret: refetch `GET /api/v1/clients` and `GET /api/v1/tokens`)
- `attention.changed` - The [Needs attention](../features/needs-attention.md) list changed (`{count, ids}`, narrowed per subscriber; refetch `GET /api/v1/attention`).

The stream is rendered **per connection**. An admin subscriber (API key, Web UI,
tray over the unix socket) receives every event exactly as the event bus
published it. For an agent token limited by `allowed_servers` (issue #1166):

| Event | Delivered to a scoped subscriber |
|-------|----------------------------------|
| Names a server outside the scope, through `server_name`, `server`, `target_server` or `affected_entity` — every `activity.*`, `oauth.*` and `security.*` event | **No.** The whole frame is dropped: blanking the name still discloses the mutation, its timing, and how many servers are hidden. |
| `servers.changed` | **Yes, always** — it is coalesced last-write-wins and carries renderable state. The embedded server list is narrowed, `stats` recomputed, and a coalescer extra naming an out-of-scope server is removed. |
| `config.reloaded`, `config.saved`, `secrets.changed` | **No.** They announce mutations of the admin config document, which `GET /api/v1/config` already answers `403` for this caller. |
| `profiles.changed`, `client.binding_changed`, `credentials.changed` | **No.** They name profiles, clients and bindings a scoped caller may not reach. `profiles.changed {name, change: create|update|delete|anonymous, previous_name?}` is an invalidation, not a log: refetch `GET /api/v1/profiles`. One event per changed profile, published after the new configuration is live, for service writes and hand edits alike. |
| Everything else (`active_profile.changed`, `activity.system.*`, `sensitive_data.detected`, `security.scanner_changed`, …) | **Yes**, unchanged: no server identity to scope. |

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
    "launched_by": "tray",
    "pid": 4711,
    "update_policy": {
      "enabled": true,
      "channel": "stable",
      "nudges_suppressed": false
    },
    "update": {
      "available": true,
      "latest_version": "v1.3.0",
      "release_url": "https://github.com/smart-mcp-proxy/mcpproxy-go/releases/tag/v1.3.0",
      "checked_at": "2025-01-15T10:30:00Z",
      "is_prerelease": false,
      "install_channel": "homebrew",
      "update_command": "brew upgrade mcpproxy",
      "behind_summary": "8 releases / ~14 weeks behind",
      "releases_behind": 8,
      "weeks_behind": 14
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
| `launched_by` | string | Durable launch provenance of the running core (Spec 092 FR-001a): `tray` when a tray spawned it, `installer` when the macOS PKG postinstall did, `""` when user-launched or unknown. Always present. A tray uses this to decide whether it may stop and respawn a stale core it did not itself start — an empty value means consent is required. |
| `pid` | integer | OS process id of the running core (Spec 092 FR-002). A tray that only *attached* to a core holds no process handle for it and the core exposes no shutdown endpoint, so this is the mechanism behind the consent-gated "restart the stale core" action. |
| `update_policy` | object | Effective, hot-reloadable update policy (Spec 092 FR-015). **Always present**, including every field, because the `update` object below is absent both when checking is disabled *and* when no check has produced a result yet — its absence cannot tell a client whether it is allowed to check. |
| `update_policy.enabled` | boolean | Whether **automatic** update checks are allowed: `update_check.enabled`, with `MCPPROXY_DISABLE_AUTO_UPDATE=true` winning over it. A *user-initiated* "Check for Updates" stays available even when this is `false`. The macOS tray gates its Sparkle feed checks on this field. |
| `update_policy.channel` | string | Tracked release channel: `stable` or `rc`. **Derived from the running build's own version** — a released stable build always reports `stable` (never RC) and a released RC build always reports `rc`, regardless of `update_check.channel` / `MCPPROXY_ALLOW_PRERELEASE_UPDATES` (those only affect dev/unstamped builds). The tray maps `rc` onto the Sparkle `beta` feed channel, and additionally clamps to `stable` when its own app bundle is a stable release. |
| `update_policy.nudges_suppressed` | boolean | The core runs in a CI / non-interactive context: UI surfaces must stay quiet while machine-readable fields keep reporting the facts. |
| `update` | object | Update information (may be null if not checked yet; omitted entirely when update checking is disabled via `update_check.enabled: false` or `MCPPROXY_DISABLE_AUTO_UPDATE=true`) |
| `update.available` | boolean | Whether a newer version is available |
| `update.latest_version` | string | Latest version available on GitHub |
| `update.release_url` | string | URL to the GitHub release page |
| `update.checked_at` | string | ISO 8601 timestamp of last update check |
| `update.is_prerelease` | boolean | Whether the latest version is a prerelease |
| `update.check_error` | string | Error message if update check failed |
| `update.install_channel` | string | Detected install channel: `homebrew`, `dmg`, `deb`, `rpm`, `docker`, `go-install`, `windows-installer`, `tarball`, or `unknown`. Always present once detected, even when no update is available. See [Version Updates](/features/version-updates) for how detection works. |
| `update.update_command` | string | Exact one-line update command for the detected channel. Only present when an update is available **and** the channel has a safe command (`homebrew`, `deb`, `rpm`, `go-install`); omitted for `dmg`/`windows-installer`/`tarball`/`docker`/`unknown` so a possibly-wrong command is never suggested. |
| `update.behind_summary` | string | Human-readable delta clause, e.g. `8 releases / ~14 weeks behind` (Spec 079 FR-002). **Render this verbatim** rather than rebuilding it from the numbers below — it is authored once in the core so the CLI, Web UI banner and both trays cannot word it differently. Only present when an update is available and the delta could be resolved. |
| `update.releases_behind` | integer | Releases on the offered channel between the running version and the offered one. Absent when unknown. |
| `update.releases_behind_saturated` | boolean | `releases_behind` is a **lower bound**: the running build predates the scanned release window, so older releases were never counted. Clients render `N+`. Omitted when false. |
| `update.weeks_behind` | integer | Whole weeks between the two releases' publish dates. `0` is a real value (a same-week release); *absent* means unknown, so do not conflate them. |

:::note Delta fields degrade silently
The four `behind_*` / `*_behind` fields are best-effort enrichment: resolving them needs the release list and publish dates, which is a second GitHub request. When that request fails, is rate-limited, or the running build has no release record, the fields are simply **absent** and every surface renders exactly the message it rendered before the delta existed. A missing delta never sets `check_error` and never suppresses the nudge.
:::

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

List recent MCP sessions.

**Query Parameters:**

| Parameter | Type | Description |
|-----------|------|-------------|
| `limit` | integer | Max sessions (1-100, default: 10) |
| `offset` | integer | Pagination offset (default: 0) |
| `parent_id` | string | Return only the sub-calls of one `code_execution` (value = the parent record's `request_id`) |
| `status` | string | Filter by session status: `active`, `closed`. Any other value returns `400`. |
| `profile` | string | Sessions whose **latest effective** profile is this name; `-` selects sessions with none |
| `client` | string | Sessions whose credential is bound to this client id; `-` selects sessions with none |
| `token` | string | Sessions that initialized with this token name; `-` selects sessions with none. `agent` is an alias of `token`; naming two different tokens returns `400` |

Each row carries `client_id`, `token_name`, `profile` and `profile_source` (`pin`, `binding`, `url`, `session`, `anonymous` or `none`); they are empty on sessions recorded before Profiles v3. `client_name` is not a sessions filter and returns `400` (`client_name is not supported on this endpoint; filter by client`). The scope filters are applied before the `limit` truncation, so `total` is the filtered count.

The `status` filter is applied during the storage walk, **before** the `limit`
truncation, so a long-running session that is still active is returned even when
newer sessions would otherwise fill the page. When `status` is set, `total`
counts the matching sessions rather than every stored session.

Caveat (spec 082): handshake-only sessions are not persisted, so a connected but
idle client does not appear until its first tool call.

#### GET /api/v1/sessions/{id}

Get session details.

### Activity

Track and audit AI agent tool calls. See [Activity Log](../features/activity-log.md) for detailed documentation.

#### GET /api/v1/activity

List activity records with filtering and pagination.

**Query Parameters:**

| Parameter | Type | Description |
|-----------|------|-------------|
| `type` | string | Filter by type: `tool_call`, `policy_decision`, `quarantine_change`, `server_change`, `preflight` |
| `server` | string | Filter by server name |
| `tool` | string | Filter by tool name |
| `session_id` | string | Filter by MCP session ID |
| `status` | string | Filter by status: `success`, `error`, `blocked` |
| `profile` | string | Filter by the profile in effect when the call ran (also matches the legacy `metadata.profile`); `-` selects records with none |
| `client` | string | Filter by client id (the client's binding); `-` selects records with none |
| `token` | string | Filter by the token name in effect (also matches records written before Profiles v3 through their stored agent name); `-` selects records with none. `agent` is an alias of `token`; naming two different tokens returns `400` |
| `client_name` | string | Filter by the client's self-reported `clientInfo.name`. Advisory, never authoritative: a client can claim any name. Accepted by `GET /activity` and `GET /activity/export` only |
| `start_time` | string | Filter after this time (RFC3339) |
| `end_time` | string | Filter before this time (RFC3339) |
| `limit` | integer | Max records (1-100, default: 50) |
| `offset` | integer | Pagination offset (default: 0) |

**Scope attribution (Profiles v3).** Every record written for an MCP or REST request carries the values in effect when the call ran: `profile`, `profile_source`, `client_id`, `client_name`, `token_name`, and `block_reason` for a blocked call. They are never rewritten, so reassigning a client later does not change history. Records with no request context (system events, configuration changes, concurrency-limiter rejections) are unattributed, and a `profile_change` record carries the new profile, the client id and the token name.

A non-administrator caller sees another token's `profile`, `profile_source`, `client_id` and `token_name` blanked, and its own `profile`, `client` and `token` filters evaluate that same view, so a filter cannot be used to learn what another token did.

The same `profile`, `client` and `token` filters (and the `agent` alias) apply to `GET /api/v1/activity/summary`, `GET /api/v1/activity/usage` and `GET /api/v1/activity/export`; `client_name` is rejected on `/activity/summary` and `/activity/usage` with `400`. Under a scope filter `/activity/usage` is computed from the matching records of the requested window (per-tool figures are window-bounded rather than lifetime, and the global tokens-saved headline is omitted). Export CSV appends `profile,profile_source,client_id,client_name,token_name,block_reason` after `parent_id`.

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
