# Contract: administrator surfaces

All REST routes are under `/api/v1`, use the standard `{success, data}` envelope, require administrator credentials (admin API key or tray socket) and answer `403` to agent tokens and client credentials. With the feature off the sign-in request routes answer `404 feature_disabled`. Client payloads depend on whether the feature was ever used: never used → exactly today's payload, no new fields; used → the new fields are present and logins are listed. `browser_login_disabled` reflects only whether the feature is off right now: `false` while on, `true` while off after use. Revoking a login works in both on and off-after-use states.

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
  "existing_clients": [ /* address_kind = generic: {id, display_name, binding, builtin}; builtin=true marks a supported connect-registry client with no records yet, choosable; its record is created when the login is exchanged, not at approval */ ],
  "suggested_new_client": { "id": "acme-agent", "display_name": "Acme Agent" },
  "suggested_client": { "id": "cursor", "matched": "display_name" }, // generic only; present when the first tier with a match (entries with records, then built-ins) has exactly one matching entry, or a known-name tie there is broken as described below (matched: id | display_name | known_name); absent otherwise. On a fresh install "Visual Studio Code" yields `copilot`
  "profiles": [ { "name": "dev-readonly" } ],
  "profile_required": true,               // profiles exist and the client has no binding
  "previous_login": { "grant_id": "…", "client_id": "cursor", "approved_at": "…" }, // same registration signed in before: its newest live login
  "previous_logins": [ { "grant_id": "…", "client_id": "cursor", "approved_at": "…" } ], // newest live login of this registration under each client (a per-client address lists only that client)
  "guard": null                           // only when the address fixes a client that has a binding; else absent, use preview
}
```

`404` for an unknown, expired, decided or cancelled id (one uniform answer).

`suggested_client` is advice for the page only. It is computed on read from the sanitised reported name (trimmed, compared without regard to case) against each entry of `existing_clients`: its id, its display name and, for a connect-registry client, its known client names. The whole name is tried and, when it ends with a parenthesised suffix, also the part before it. Matches are collected over all fields and counted by distinct client id; Entries with records are tried first and built-in entries (`builtin: true`) only when none of those matched; within a tier, exactly one distinct client → present, several → absent (and the built-in tier is then not consulted), none → next tier. A tier with several matches that all matched by a known client name (VS Code's `Visual Studio Code` fits `vscode` and `copilot`; `ClientDef.OAuthRegistrationNames` is matched beside `ClientInfoNames`) is broken by the clientInfo name last seen in the past 30 days, then by the registry's preferred client (`copilot`); any other several-match tier stays absent. `matched` names the first field that matched in the order `id`, `display_name`, `known_name`. The reported name and a new client's display name are stored without Unicode format characters (category Cf: bidi controls, zero-width characters), and so is the name observed at `initialize`; the page shows every such name in an isolated element. Preview and approve take the client from their own body and never fall back to the suggestion: on the generic address, preview without a client answers `200 {"complete": false}` with no `access` and no `guard`, and approve without a client answers `422 client_required`.

### `POST /oauth/authorizations/{id}/preview`

Read-only. Body: the same `client`, `profile`, `mode` as approve. Answer:

```jsonc
{ "complete": true,
  "access": { "client": "cursor", "profile": "dev-readonly", "mode": "locked", "same_access_as_existing_client": true },
  "guard": null }   // or { "code": "binding_bypassable_without_auth", "bindings": [...], "fixes": [...] }
```

`complete: false` (and no verdict) while a required choice is missing. Same `422` codes as approve for an invalid choice. Writes nothing.

### `POST /oauth/authorizations/{id}/approve`

Records the decision and issues the code. The login itself is stored when the client exchanges the code.

```jsonc
{
  "client": { "id": "cursor" },                    // generic address: an existing id or a built-in (builtin: true) without records, or
  // "client": { "new": { "id": "acme-agent", "display_name": "Acme Agent" } },
  "profile": "dev-readonly",                       // "" = All servers; ignored when the client has a binding
  "mode": "locked",
  "binding_revision": "b1f3…",                     // required when the client has a binding
  "replace_grant": "…"                             // optional: the grant id of the previous login under the chosen client (previous_logins)
}
```

- `200 {redirect_url}`: the page navigates there.
- `409 already_decided`.
- `409 binding_changed`: the binding differs from `binding_revision`; reload.
- `409 binding_bypassable_without_auth`: body as the existing guard refusal.
- `422 client_required` (generic address, no client in the body), `422 profile_required`, `422 client_fixed` (a client other than the one in the address), `422 identity_exists` / `reserved_identity` for a new client id, `422 invalid_replace_grant` (a live login of this registration that belongs to another client; a grant of another registration is ignored), `404 profile_not_found`, `404 client_not_found` (the chosen client no longer exists; the request itself is still pending).

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

`profile_change` rows with `change` = `approve` | `deny` | `issue` | `revoke` | `replay_revoke`, metadata `client_id`, `request_id` (rows written before a login exists: `approve`, `deny`), `grant_id` (rows about a stored login: `issue`, `revoke`, `replay_revoke`), `profile`, `actor_kind`, `actor_name`, `surface`. `approve`, `revoke` and a `deny` clicked on the page name the administrator who made the request. Rows written on the public token endpoint (`issue`, `replay_revoke`, and a `deny` caused by the re-check at exchange) name the administrator who approved (`approved_by`) and add `trigger: "client"`. Tool-call rows of a browser login carry `client_id` and `metadata.grant_id`, credential kind `oauth`.

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
