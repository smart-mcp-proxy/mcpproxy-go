# API Contract Deltas — Spec 091 (rev 2, post-panel-review)

No new endpoints. Additive, backward-compatible deltas on the existing connect
surface.

## 1. `GET /api/v1/connect/{client}/preview` — three new response fields

```json
{
  "config_path": "…",
  "entry": { … },
  "entry_text": "…pending entry, credential masked at construction…",
  "entry_exists": true,
  "contains_api_key": true,
  "access_state": "accessible",

  "existing_entry_summary": {
    "entry_name": "old-proxy",
    "type": "http",
    "endpoint": "http://127.0.0.1:8080/mcp",
    "command": null,
    "header_names": ["X-API-Key"],
    "env_names": []
  },
  "precondition_token": "opaque",
  "connect_refusal": null
}
```

- `existing_entry_summary` (object, optional): present only when
  `entry_exists=true`. Built **by construction from non-secret projections
  only** — entry name (which may differ from the requested `server_name` when
  the write would adopt an equivalent entry), transport type, endpoint with
  **query parameters stripped**, command, and the **names** (never values) of
  headers and environment variables. No field of this object ever carries an
  arbitrary value from the config file except the query-stripped endpoint and
  command path. Backend tests feed entries containing rotated API keys,
  bearer headers, env secrets, `?apikey=` URLs, and `user:pass@` URLs and
  assert none of those values appear anywhere in the serialized response
  (userinfo is stripped from endpoints along with the query).
- `precondition_token` (string, always present): opaque **HMAC-SHA256** over a
  canonical **length-prefixed** encoding of: config path, file existence, the
  *resolved* target entry name (after the same equivalent-entry adoption the
  write performs) and its raw value, and the exact pending entry that would be
  written. Keyed with a per-core-instance random in-memory key (tokens are
  single-session by design), so it is neither reversible nor usable as an
  offline confirmation oracle for masked values. Never persisted.
- `connect_refusal` (string, optional): populated when the write would refuse
  regardless of user intent (e.g. a non-create-capable client with an absent
  config — OpenCode); carries the same verbatim reason the write would return.
  Consumers must treat its presence as "Connect unavailable".

## 2. `POST /api/v1/connect/{client}` — precondition + discriminated conflict

Request body:

```json
{ "server_name": "mcpproxy", "force": true, "precondition_token": "opaque" }
```

- `precondition_token` (optional): when present, the core recomputes at write
  time and responds **409** when stale, writing nothing.
- The 409 body is machine-discriminable via `action`:
  - `"action": "precondition_failed"` — token drift → the client re-previews.
  - `"action": "already_exists"` — existing semantics (entry exists,
    `force=false`, no token involved).
- A replace-classified flow sends `force=true` **with** the token — the token,
  not the absence of force, is the overwrite safety. `force=true` with a stale
  token still yields `precondition_failed` and no write.
- Absent token → exactly today's behavior (Web UI/CLI unchanged).
- Gating unchanged: write routes still reject restricted agent tokens; the
  tray's socket transport carries admin context. The tray sends mutating
  requests in strict-socket mode (client-side; no TCP fallback for writes).

## Consumers

- The native form always sends the token from its rendered preview; replace
  flows add `force=true`; it never sends `force` without a token.
- Web UI ConnectModal is unchanged (may adopt the token later).
