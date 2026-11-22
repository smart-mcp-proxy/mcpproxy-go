# Contract: administrator surfaces

All REST routes are under `/api/v1`, use the standard `{success, data}` envelope, require administrator credentials (admin API key or tray socket) and answer `403` to agent tokens and client credentials. With the feature off the sign-in request routes answer `404 feature_disabled`. Client payloads depend on whether the feature was ever used: never used → exactly today's payload, no new fields; used → logins are still listed and `browser_login_disabled` is `true`. Revoking a login works in both on and off-after-use states.

## Sign-in requests

### `GET /oauth/authorizations`

Pending requests, newest first: `[{id, client_label, created_at, expires_at}]`. Used by the tray and by Home.

### `GET /oauth/authorizations/{id}`

```jsonc
{
  "id": "…",
  "status": "pending",
  "expires_at": "…",
  "reported_name": "Cursor IDE",          // unverified, sanitised
  "redirect_uri": "http://127.0.0.1:54321/callback",
  "resource": "http://127.0.0.1:8080/mcp/oauth/cursor",
  "address_kind": "client",               // "client" | "generic"
  "client": {                             // present when address_kind = client
    "id": "cursor", "display_name": "Cursor", "exists": true,
    "has_api_key": true, "logins": 1,
    "binding": { "profile": "dev-readonly", "mode": "locked", "revision": "b1f3…" }
  },
  "existing_clients": [ /* address_kind = generic: {id, display_name, binding} */ ],
  "suggested_new_client": { "id": "acme-agent", "display_name": "Acme Agent" },
  "profiles": [ { "name": "dev-readonly" } ],
  "profile_required": true,               // profiles exist and the client has no binding
  "previous_login": { "grant_id": "…", "approved_at": "…" }, // same registration signed in before
  "guard": null                           // or { "code": "binding_bypassable_without_auth", "bindings": [...], "fixes": [...] }
}
```

`404` for an unknown, expired, decided or cancelled id (one uniform answer).

### `POST /oauth/authorizations/{id}/approve`

```jsonc
{
  "client": { "id": "cursor" },                    // generic address: existing id, or
  // "client": { "new": { "id": "acme-agent", "display_name": "Acme Agent" } },
  "profile": "dev-readonly",                       // "" = All servers; ignored when the client has a binding
  "mode": "locked",
  "binding_revision": "b1f3…",                     // required when the client has a binding
  "replace_grant": "…"                             // optional: the previous_login grant id
}
```

- `200 {redirect_url}`: the page navigates there.
- `409 already_decided`.
- `409 binding_changed`: the binding differs from `binding_revision`; reload.
- `409 binding_bypassable_without_auth`: body as the existing guard refusal.
- `422 profile_required`, `422 client_fixed` (a client other than the one in the address), `422 identity_exists` / `reserved_identity` for a new client id, `404 profile_not_found`.

### `POST /oauth/authorizations/{id}/deny`

`200 {redirect_url}` (carries `error=access_denied`). `409 already_decided`.

## Logins on clients

- `GET /clients` and `GET /clients/{client}`: each client gains

  ```jsonc
  "credentials": [
    { "kind": "api_key", "prefix": "mcp_cli_ab12", "state": "active", "expires_at": "…", "last_used_at": "…" },
    { "kind": "browser_login", "grant_id": "…", "state": "active",       // active | lapsed | revoked
      "approved_at": "…", "last_used_at": "…", "reported_name": "Cursor IDE", "redirect_host": "127.0.0.1",
      "observed_name": "cursor-vscode", "name_mismatch": false }
  ],
  "browser_login_disabled": false
  ```

- `DELETE /clients/{client}/logins/{grant_id}`: revoke one login. `200 {changed}`; `404` unknown.
- `DELETE /clients/{client}/api-key`: revoke the API key alone; the client's logins and binding stay. `404` when the client has no API key.
- Existing `DELETE /clients/{client}` (forget) and the revoke path: cascade to all logins.
- `PUT /clients/{client}/binding`, `POST /clients/bulk-assign`: work for clients that have only browser logins.

## SSE (admin-only)

- `oauth.authorization_pending` `{request_id, client_id, client_label}`
- `oauth.authorization_decided` `{request_id, status}`
- `credentials.changed` (existing) with `kind: "browser_login"`, `change: issue | revoke | replay_revoke`

## Activity

`profile_change` rows with `change` = `approve` | `deny` | `issue` | `revoke` | `replay_revoke`, metadata `client_id`, `grant_id`, `profile`, `actor_kind`, `actor_name`, `surface`. `approve`, `deny` and `revoke` name the administrator who made the request. `issue` (first token exchange) and `replay_revoke` happen on the public token endpoint: they name the administrator who approved the login (`approved_by`) and add `trigger: "client"`. Tool-call rows of a browser login carry `client_id` and `metadata.grant_id`, credential kind `oauth`.

## MCP `credentials` tool

- `list` (`kind` gains `browser_login`), `get client=<id>`: include logins with the fields above.
- `revoke client=<id>`: everything under the client. `revoke client=<id> grant=<grant_id>`: one login.
- No operation creates a login.

## CLI

- `mcpproxy client show <id>`: lists credentials.
- `mcpproxy client revoke-login <id> <grant-id>`.
- `mcpproxy connect <client> --auth key|oauth` (PR-3); `oauth` is refused with a clear message when the feature is off or the client is not marked as supporting it.

## Config

`mcp_oauth_enabled` (bool, default false, hot), env `MCPPROXY_MCP_OAUTH_ENABLED`.
