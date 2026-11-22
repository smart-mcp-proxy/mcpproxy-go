# Data Model: Browser login for MCP clients

All persistent records live in `config.db` in buckets created on first write. Nothing is created while the feature is off.

## Registration — bucket `oauth_registrations`, key `client_id`

| Field | Type | Rule |
|---|---|---|
| `client_id` | string | 22 random base62 chars, server-generated |
| `reported_name` | string | `client_name` after `clientidentity.SanitizeDisplay`, at most 128 bytes; display only |
| `redirect_uris` | []string | 1 to 5; each loopback `http` or `https`; no fragment |
| `software_id`, `software_version` | string | optional, sanitised, display only |
| `token_endpoint_auth_method` | string | always `none` |
| `grant_types` | []string | always `authorization_code`, `refresh_token` |
| `state` | enum | `pending` → `authorized` → `approved` |
| `created_at`, `authorized_at` | time | |

Lifecycle: `pending` rows are deleted 15 minutes after creation, `authorized` (reached `/oauth/authorize`, no login stored yet, whether the request was denied, abandoned or is still open) 24 hours after `authorized_at`. At most 50 rows that are not `approved`; the oldest is dropped. A row becomes `approved` in the transaction that stores its first grant, lives while at least one grant record references it, and is deleted with its last grant record. A denial is not a registration state.

## Grant (browser login) — bucket `oauth_grants`, key `grant_id`

| Field | Type | Rule |
|---|---|---|
| `grant_id` | string | 22 random base62 chars |
| `registration_id` | string | the `client_id` above |
| `client` | string | Clients hub client id; passes `auth.ValidClientID` |
| `redirect_uri` | string | the registered redirect URI actually used; `redirect_host` in listings comes from it |
| `resource` | string | full URI approved, immutable |
| `issuer` | string | origin approved, immutable |
| `refresh_hash`, `refresh_prefix` | string | HMAC-SHA256 of the current refresh secret; first 12 chars of the token |
| `access_hash`, `access_expires_at` | string, time | the one live access token |
| `refresh_expires_at` | time | now + 30 days on every rotation |
| `approved_at`, `approved_by` | time, actor | |
| `last_used_at` | time | updated like agent tokens, off the request path |
| `observed_client_name`, `observed_at` | string, time | name sent at `initialize` on this login, sanitised; for the advisory mismatch warning only |
| `display_prefix` | string | 12 chars shown in lists and used as the context's token prefix |
| `revoked`, `revoked_at`, `revoke_reason` | bool, time, enum | `admin`, `client_revoked`, `replaced`, `replay` |
| `user_id` | string | empty in the personal edition |

Sub-bucket `by_access_hash`: `access_hash → grant_id`.

A grant is created at the token exchange, not at approval (research R15).

State: `live` (not revoked, `refresh_expires_at` in the future) → `lapsed` (refresh expired) or `revoked`. Neither comes back. A lapsed or revoked grant is kept for display for 30 days, then deleted.

## Client binding for browser logins — bucket `oauth_client_bindings`, key client id

| Field | Type | Rule |
|---|---|---|
| `profile_pin` | string | profile name, or empty for All servers |
| `profile_mode` | enum | `locked` requires a non-empty pin; All servers is `switchable` |
| `display_name` | string | for a custom client created at consent, at most 64 runes |
| `updated_at` | time | |

Invariants:

- Exists exactly for clients that have at least one grant record; it is created in the transaction that stores the client's first grant, for a new client and for an existing API-key client alike, from the client's current binding.
- If the client also has an API-key record, `profile_pin` and `profile_mode` equal that record's `ProfilePin` and `ProfileMode`. One storage function writes both in one transaction. On startup a mismatch is repaired from the API-key record and logged.
- **Binding revision**: `sha256(client | pin | mode)` truncated, computed on read; a client that does not exist yet has the fixed revision `absent`. The consent page sends back the revision it displayed.

## Ever-used marker — `MetaBucket` key `oauth_login_ever_used`

RFC 3339 time of the first stored grant. Written in the same transaction as that grant. Never deleted.

## Authorization request — memory only

| Field | Rule |
|---|---|
| `id` | 22 random base62 chars; appears in the consent URL |
| `registration_id`, `redirect_uri`, `state`, `code_challenge` | validated at `/oauth/authorize`; stored verbatim |
| `resource`, `issuer`, `path_client` | `path_client` is the client id from the resource path, empty for the generic address |
| `status` | `pending` → `approved` \| `denied` \| `expired` \| `cancelled`; one decision, compare-and-set. An `approved` request can still become `cancelled` until its code is consumed (client revoked or forgotten, feature turned off) |
| `decision` | set on approval: client (existing or new), profile, mode, binding revision seen, replace choice, approving actor |
| `expires_at` | created + 10 minutes |

At most 20 pending requests; a new one beyond that is refused with `temporarily_unavailable`.

## Authorization code — memory only

`code` (32 random bytes, stored hashed), bound to request id, registration, redirect URI, challenge, resource, issuer, chosen client and the decision. Single use, 60 seconds. Consuming it, re-checking revision and guard, and storing the grant and any new binding is one step (research R15); the entry then stays as a tombstone `{consumed_at, grant_id}` for 10 minutes. A second presentation within that time revokes that grant (`replay`); after it, the code is simply unknown.

## Relationships

```text
Client (Clients hub id)
 ├─ 0..1 API key        agent_tokens (Kind=client)      holds ProfilePin/ProfileMode
 ├─ 0..1 binding copy   oauth_client_bindings           equal to the above when both exist
 └─ 0..n Grant          oauth_grants ──► Registration   oauth_registrations
```

## Derived views

- **Credential list of a client** (Clients hub, REST, `credentials get`): the API key if any, then grants ordered by `approved_at` descending, each with state, prefix, `approved_at`, `last_used_at`, reported name, redirect host. Never a secret or a hash.
- **Active named binding** (guard, `profile_in_use`): a client with a named pin and either a live API key or a live grant.
- **Client state** on the hub gains `browser_login_disabled` when the client has grants and the feature is off. The logins stay listed.
- **Name mismatch** per login: derived from `observed_client_name` (research R14).

## Auth context of a browser-login request

`Type=agent`, `TokenKind=client`, `ClientID=<client>`, `AgentName=client-<client>`, `ProfilePin`, `ProfileMode` from the binding, `CredentialKind=oauth`, `GrantID`, `TokenPrefix=<display_prefix>`, `TokenGeneration=<grant id>`, the client rate limit, `AllowedServers=["*"]`, all three permission tiers. Built by the same helper as the `mcp_cli_` path. The grant id is also kept on the MCP session record.
