# API Contract Deltas — Spec 091

No new endpoints. Two additive, backward-compatible deltas on the existing
connect surface.

## 1. `GET /api/v1/connect/{client}/preview` — two new response fields

```json
{
  "config_path": "…",
  "entry": { … },
  "entry_text": "…pending entry, credentials masked…",
  "entry_exists": true,
  "contains_api_key": true,
  "access_state": "accessible",

  "existing_entry_text": "…current entry, credentials masked…",
  "precondition_token": "hex-sha256-opaque"
}
```

- `existing_entry_text` (string, optional): present only when
  `entry_exists=true`; rendered through the SAME masking routine as
  `entry_text`. Backend tests assert no raw secret value from the config file
  appears anywhere in the response.
- `precondition_token` (string, always present): opaque hash over the raw
  pre-write state — config-file existence, target entry presence, and the raw
  entry value (all change kinds: create / add / replace). Never reversible to
  secrets; not persisted.

## 2. `POST /api/v1/connect/{client}` — optional precondition

Request body:

```json
{ "server_name": "mcpproxy", "force": false, "precondition_token": "hex…" }
```

- `precondition_token` (optional): when present, the core recomputes the token
  at write time and responds **409 Conflict** with a reason string when the
  state has drifted (file appeared/disappeared, entry changed — including
  changes hidden by masking). No partial write occurs on conflict.
- Absent token → exactly today's behavior (backward-compatible for the Web UI).
- Gating unchanged: write routes still reject restricted agent tokens; the
  tray's Unix-socket transport carries admin context.

## Consumers

- The native form always sends the token from its rendered preview and never
  sends `force` without a token.
- Web UI ConnectModal is unchanged (may adopt the token later).
