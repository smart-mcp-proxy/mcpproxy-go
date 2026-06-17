# Contract: Connect status REST payload (delta)

Endpoints are UNCHANGED in path/method/auth. Only the per-client status object
gains additive fields. Existing fields and their meaning are preserved (FR-006).

## GET /api/v1/connect

Returns the list of client statuses.

### Before (per-client object — preserved)

```json
{
  "id": "claude-desktop",
  "name": "Claude Desktop",
  "config_path": "/Users/x/Library/Application Support/Claude/claude_desktop_config.json",
  "exists": true,
  "connected": true,
  "supported": true,
  "icon": "…",
  "server_name": "mcpproxy"
}
```

### After (additive — `access_state`, `remediation`)

```json
{
  "id": "claude-desktop",
  "name": "Claude Desktop",
  "config_path": "…/claude_desktop_config.json",
  "exists": true,
  "connected": false,
  "access_state": "unknown",
  "supported": true,
  "icon": "…"
}
```

Behavioral contract:
- Overall status performs **no content reads**: `exists` reflects `os.Stat`; `access_state` is `"unknown"` for installed clients; `connected` is `false` until a per-client check runs.
- `connected` is authoritative **only** when `access_state == "accessible"`.
- When a content access is blocked: `access_state == "denied"` and `remediation` is populated.
- Old consumers ignoring `access_state`/`remediation` keep working; `connected=false` + `exists=true` reads as "installed, not (yet) connected."

## GET /api/v1/connect/{client}  (per-client, on-demand — NEW or status-refresh semantics)

> If a single-client status route does not already exist, it is introduced here;
> otherwise the existing per-client status path gains the on-demand content read.

- Performs the single client's content read at request time (the only place a
  macOS App-Data prompt may legitimately appear, scoped to user action).
- Response: full `ClientStatus` with resolved `access_state` (`accessible|absent|denied|malformed`),
  `connected`, and `remediation` when denied.

## POST /api/v1/connect/{client} and DELETE /api/v1/connect/{client}

- Unchanged contract. On a permission-denied file access, the error response
  MUST carry the remediation text (HTTP error body), distinct from a generic
  failure or a "not found".

## Non-goals

- No change to request bodies, auth headers, or status codes for success paths.
- No removed/renamed fields.
