# Contract: REST, SSE and activity changes

No new REST route is added. All changes below are additive, except the deliberate `assign` → `issue` change on custom client add (FR-010).

## REST

### `GET /api/v1/tokens`, `GET /api/v1/tokens/{name}` (`tokenInfoResponse`)

New optional fields:

```json
{ "revoked_at": "2026-10-10T12:41:07Z",
  "issuer": { "actor_kind": "api_key", "actor_name": "", "surface": "mcp" },
  "purpose": "Daily research digest; assumes read-only on library servers",
  "lease": true,
  "profile_state": "ok" }
```

### `POST /api/v1/tokens`, `DELETE /api/v1/tokens/{name}`

Request, response and status codes are unchanged. Behaviour change: the request now goes through `runtime.CredentialsService`, which writes `profile_change{change: issue|revoke, surface: api|cli}` and publishes `credentials.changed`. The binding guard is **not** enforced here (spec A9). `POST` accepts an optional `purpose` (≤ 500 characters). `name` and `purpose` pass the shared service screen (data-model §8.2) before anything is minted; a secret-shaped value answers 400 naming the field, with no token created, nothing stored, no audit record and no event.

### `GET /api/v1/clients`, `GET /api/v1/clients/{id}` (custom client rows)

The client credential object gains `revoked_at`, `issuer`, `purpose`, `lease` and `profile_state`.

### `POST /api/v1/clients` (custom client add)

Request and response are unchanged, and the request gains an optional `purpose`. `id`, `display_name`, `profile`, `mode`, `expires_in` and `purpose` pass the shared service screen (data-model §8.2) before anything is minted; a secret-shaped value answers 400 naming the field, with no client created, nothing stored, no audit record and no event. The audit record changes from `change: assign` to `change: issue` (FR-010). The OpenAPI description, `docs/api/rest-api.md` and `oas/swagger.yaml` are updated.

## SSE `/events`

New event `credentials.changed`:

```
event: credentials.changed
data: {"type":"credentials.changed","data":{"kind":"token","id":"research-task-42","token_name":"research-task-42","change":"revoke","profile":"daily-research"},"timestamp":"..."}
```

| Publisher | When |
|---|---|
| `CredentialsService.IssueClient/IssueToken` | after mint |
| `CredentialsService.Revoke` | after a revoke with `changed: true` |
| `ClientsService.forgetLockedOpt` | after forget (closes G6) |

Frontend: `stores/system.ts` relays the event as the window event `mcpproxy:credentials.changed`. Its consumers are the clients store, `AgentTokens.vue` and the profiles store (`used_by`).

## Activity

- `profile_change.metadata.change` gains `issue` and `revoke` (data-model §3). The enums golden `internal/profile/testdata/contract/enums.json` and the frontend `types/contracts.ts` are updated.
- Activity UI labels: `issue` + client → "Issued client credential", `issue` + token → "Issued token", `revoke` + client → "Revoked client credential", `revoke` + token → "Revoked token".
- `internal_tool_call` records for `credentials` follow [mcp-credentials-tool.md §Activity body](mcp-credentials-tool.md#activity-body-of-a-credentials-call).
- Redaction backstop: `oauth.AuditRedaction` masks any string value that contains `mcp_agt_` or `mcp_cli_` followed by at least 8 characters. Shorter display prefixes stay readable. Every built-in tool's activity body and exports use this redaction.
