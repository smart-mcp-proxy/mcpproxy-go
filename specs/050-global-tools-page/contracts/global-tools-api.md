# Contract — `GET /api/v1/tools`

Consolidated, read-only listing of every tool across all configured servers.
Single source of truth for the web Tools page and the CLI `tools list` (global).

## Request

```
GET /api/v1/tools
Authentication: X-API-Key header or ?apikey= (REST always authenticated)
```

No query parameters in v1. Filtering/sorting/search are performed by the
consumer (web client, CLI) over the full returned set — deliberate per spec
(audit use case needs the full set; disabled tools must never be filtered out
server-side). `?apikey=` accepted for browser/SSE parity with other endpoints.

## Response `200`

```jsonc
{
  "success": true,
  "data": {
    "tools": [
      {
        "name": "create_issue",
        "server_name": "github",
        "description": "Create a new issue ...",
        "approval_status": "approved",        // "pending" | "changed" | "approved" | ""
        "disabled": false,                     // per-tool user toggle
        "config_denied": false,                // layered config (read-only; not user-overridable)
        "usage": 42,                           // calls in last 30 days
        "last_used": "2026-05-18T09:30:51Z",   // omitted if never used in window
        "annotations": { /* operation-type/risk; existing shape */ }
      }
    ],
    "stats": { "total": 478, "enabled": 450, "disabled": 28, "pending_approval": 0 },
    "partial": false,                          // true if some servers failed
    "failed_servers": []                       // names of servers that errored (omitted if none)
  }
}
```

Derived (consumer-side): `enabled = !disabled && !config_denied`. Risk badge
maps from `annotations` (read/write/destructive — existing classification).

## Behaviour requirements

| # | Requirement | Spec ref |
|---|-------------|----------|
| C1 | Includes tools from **disabled** servers and **individually disabled / config-denied** tools — nothing filtered out server-side. | FR-001, FR-007 |
| C2 | One response covers all servers; no per-server request required by the consumer. | FR-002 |
| C3 | `usage`/`last_used` reflect a fixed 30-day activity window; never-used tools → `usage:0`, `last_used` omitted, no error. | FR-006 |
| C4 | `stats` is internally consistent with `tools` (total = len; disabled counts user-disabled OR config-denied; pending counts pending/changed approval). | FR-004 |
| C5 | If a server cannot be read, the endpoint still returns every tool it could gather and sets `partial:true` + `failed_servers`; it does NOT 500 the whole request. | Edge case |
| C6 | Endpoint is read-only and MUST NOT touch the BM25/Bleve index or alter agent discovery. | FR-016, SC-006 |
| C7 | Requires REST authentication like all `/api/v1` endpoints; returns `X-Request-Id`. | Repo security model |

## Errors

| Status | When |
|--------|------|
| `401` | Missing/invalid API key |
| `500` | Catastrophic failure (e.g., cannot enumerate servers at all). Partial per-server failures use `200` + `partial:true` instead. |

## Batch enable/disable (no new endpoint)

Reuses existing `POST /api/v1/servers/{id}/tools/{tool}/enabled` per target,
grouped by server. Consumer aggregates per-target outcomes into a
success/failure summary. Config-denied targets fail individually (server
rejects) without aborting other targets.
