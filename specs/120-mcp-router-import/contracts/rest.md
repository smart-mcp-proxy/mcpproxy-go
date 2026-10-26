# REST contract additions

## `POST /api/v1/servers/import/report` (new)

Auth: API key + `requireServerOp(auth.ServerOpAdd, ...)`.

Request: `{"source": "mcp-router"}` — `source` must be in the closed enum (`mcp-router`; extensible). No count is sent.
Responses: `204` for a valid request whether or not an event was recorded (the response never reveals opt-out state); `400` for an unknown `source`.
Effect: `Service.RecordImportRun(source)` evaluates event-time telemetry eligibility under the service's own synchronisation (config plus env overrides) and, if eligible, increments the run counter for that source.

## `POST /api/v1/servers` (extended)

`AddServerRequest.disabled_tools []string` (create only), applied to the new server's `DisabledTools`. `enabled_tools` is not accepted on this surface. Documented in `docs/api/rest-api.md` and `oas/swagger.yaml`; no change for callers that omit it.

## Web UI phase (P5, non-gating)

New endpoints, so no existing contract changes:

- `GET /api/v1/servers/import/mcp-router` → `{found, input_kind, path, workspaces:[{id,name}], server_count}` (stat plus a snapshot count under the same rules).
- `POST /api/v1/servers/import/mcp-router` body `{path?, workspace?, apply:false|true, secrets:"keyring"|"inline", inline_credentials_ok:false|true, servers?:[names]}` → plan view (same `ItemView` DTO as the CLI JSON) when `apply:false`, outcomes when `apply:true`. Same authorization as other server-adding operations. Records telemetry in-process through `Service.RecordImportRun`. Never bypasses quarantine; placeholder rejection (FR-006b) applies before any destination reference is inserted.
