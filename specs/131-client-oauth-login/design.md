# Design: OAuth login for MCP clients, next to API keys

**Issue**: [#1971](https://github.com/smart-mcp-proxy/mcpproxy-go/issues/1971) · **Date**: 2026-11-22 · **Status**: Draft for approval
**Scope**: personal edition. Server edition and the tunnel (Spec 089) reuse the same parts later.

## Intent

Today every MCP client needs a credential pasted into its config file. The user wants a second way in: the client is given only a URL, opens the browser, the user approves, and the client is connected. Both ways stay available and the user chooses per client.

The open question this design answers: **after an OAuth login, how does MCPProxy know which client this is and what it may reach?**

Answer: the **address** the client was given says which client it is, and the user confirms on the consent page. Connect writes a per-client address (`/mcp/oauth/cursor`), so nothing is guessed from the name the client reports. That name is shown, labelled unverified, and is never used to pick a client. Access comes from the profile bound to the client, the same binding API-key clients use.

The feature is **off by default** in its first release. With it off, nothing changes for anyone who updates.

## Goals

- With `mcp_oauth_enabled` off (the default), an updated MCPProxy behaves byte for byte as before on every existing path, and asks the user for nothing.
- A client configured with `http://127.0.0.1:8080/mcp/oauth/<client>` (or the generic `/mcp/oauth`) and nothing else can log in through the MCP authorization flow (RFC 9728 discovery, RFC 8414 metadata, RFC 7591 registration, authorization code + PKCE S256, refresh).
- One click on the consent page in the common case.
- An OAuth client is an ordinary row in the Clients hub: same profile binding, same activity attribution, same reassign and revoke.
- A client may hold an API key, OAuth logins, or both.
- Connect offers the choice "API key" or "Sign in with browser" for clients that support OAuth.

## Non-goals

- Replacing API keys or agent tokens. Headless clients keep them.
- OAuth on `/mcp`, `/mcp/all`, `/mcp/code`, `/mcp/call`, `/mcp/p/<slug>`, `/mcp/s/<server>`. Version 1 serves OAuth only under `/mcp/oauth`. `/mcp` behaves exactly as today.
- Confidential clients (client secrets), Client ID Metadata Documents, token introspection endpoints.
- Server edition behaviour and the tunnel. The data model leaves room for both (see "Later").
- Approving a login from the CLI.

## Anti-goals

- A client gets access, or is matched to an existing client, because of the name it reports. Never.
- Matching that works "most of the time". Which client a login belongs to is either determined by the address or chosen by the user.
- An update that changes behaviour or needs an action from a user who did not ask for OAuth.
- A login that silently lands on "All servers" while profiles exist.
- A second, parallel notion of "client" with its own profile rules.
- An OAuth client bound to a profile that it can escape by using `/mcp` without a credential.

## Constraints from the existing code

- A client credential is an `auth.AgentToken` with `Kind=client`, `ClientID`, `ProfilePin`, `ProfileMode` (`internal/auth/agent_token.go`). The binding lives on that record today.
- The connect registry (`internal/connect/clients.go`) knows each supported client's id and the names it reports (`ClientInfoNames`).
- Client names from the wire are untrusted; `internal/clientidentity` already makes them safe to store and show.
- The personal Web UI has no login. It holds the admin API key in `localStorage` after being opened with `?apikey=` (the tray does this). The `mcpproxy_session` cookie is server edition only.
- With `require_mcp_auth` off, a client bound to a named profile could drop its credential and use `/mcp` unconfined. Every binding change that would allow this is refused with `409 binding_bypassable_without_auth`.
- The `credentials` tool and REST `/clients`, `/tokens` share one issue/revoke path, `runtime.CredentialsService`.
- No new dependencies.

## Approaches considered

1. **Automatic by name.** Match `client_name` to the connect registry and assign that client's profile with no question. Rejected: the name is self-asserted, so any local process can call itself "Cursor" and inherit Cursor's access.
2. **User selects everything every time.** Safe. Rejected as the default: tedious, and clients that register again pile up as "Cursor (2)", "Cursor (3)".
3. **Suggest and confirm (chosen).** The consent page arrives pre-filled; the user approves with one click or changes it. Chosen by Algis on 2026-11-22.

How the page is pre-filled was revised the same day, after Algis asked for it to be reliable:

- **3a. Guess from the reported name** (registry list of OAuth registration names, plus learning from earlier approvals). Rejected: clients register under names nobody controls, the list needs upkeep, and a wrong guess puts the wrong client on the page.
- **3b. Per-client address (chosen).** Connect already knows which client it is configuring, so it writes that into the URL. The server reads the client from the path. Deterministic, no list to maintain, no secret in the config file.

## How it works

### Flow

1. The client calls `/mcp/oauth/<client>` (or `/mcp/oauth`) without a token. Answer: `401` with `WWW-Authenticate: Bearer resource_metadata="…"`.
2. The client reads the protected-resource metadata for that exact path and the authorization-server metadata, then registers at `POST /oauth/register`.
3. The client opens the browser at `GET /oauth/authorize`. The server validates the request (known `client_id`, exact redirect URI match, `code_challenge_method=S256`, `resource` equal to the URL of an OAuth endpoint this instance serves on this origin), stores an **authorization request** (10 minute life) with the client's `state`, the full `resource` URI and the issuer, and redirects the browser to `/ui/authorize?request=<id>`.
4. The Web UI consent page loads the request and its pre-filled choices through the REST API, with the admin API key.
5. Approve: the page posts the decision; the answer carries the redirect URL with a single-use code. The page sends the browser there. Deny: redirect with `error=access_denied`. Both redirects are built from the stored, already validated redirect URI and return the client's `state` unchanged.
6. The client exchanges the code at `POST /oauth/token` with its PKCE verifier and gets an access token and a refresh token.

**Resource binding.** The approved `resource` and issuer are stored on the authorization request, the code and the grant, and never change. A `resource` sent at code exchange or refresh must equal the stored one, else `invalid_target`. The audience of every access token comes from the grant, never from the Host or parameters of the current request.

**An authorization request is decided once.** Its state moves `pending → approved | denied | expired | cancelled` in one transaction; at most one code is ever produced, and a second tab gets "already decided". The page carries the revision of what it displayed (client, profile, binding mode). Approval is refused with "this changed, review again" when the client's binding differs from that revision. Forgetting the client or turning the feature off cancels its pending requests.

**Metadata.** Tested against these exact shapes:

- `GET /.well-known/oauth-protected-resource/mcp/oauth/cursor` → `resource: "<origin>/mcp/oauth/cursor"`, `authorization_servers: ["<origin>"]`. Each path has its own document; the generic one is `/.well-known/oauth-protected-resource/mcp/oauth`. A document is never reused across paths.
- `GET /.well-known/oauth-authorization-server` → `issuer: "<origin>"`, the three endpoints, `response_types_supported: ["code"]`, `grant_types_supported: ["authorization_code","refresh_token"]`, `code_challenge_methods_supported: ["S256"]`, `token_endpoint_auth_methods_supported: ["none"]`.
- The registration answer (RFC 7591) states the effective values, `token_endpoint_auth_method: "none"` and the two grant types, and the registration record stores them. A registration that asks for a secret-based method or another grant type is refused.

**Plain HTTP on loopback is a known deviation.** RFC 8414 and the MCP authorization spec ask for an HTTPS authorization server; RFC 8252 only exempts loopback redirect URIs. Version 1 serves the issuer on `http://127.0.0.1` as most local MCP servers do, says so in the docs, and claims support only for clients that pass a real-client test (PR-1 gate: Claude Code, Cursor, VS Code, MCP Inspector, each on a per-client path). When local TLS (`internal/tlslocal`) is on, the same endpoints are served over HTTPS and conform.

### Which client is it

Two kinds of address, both under `/mcp/oauth`:

| Address | Who writes it | Client on the consent page |
|---|---|---|
| `/mcp/oauth/<client-id>` | Connect, or the "Add browser login" action on a Clients row, or the user by hand | **Fixed**: that client. Not selectable |
| `/mcp/oauth` | the user by hand, for a client MCPProxy has never seen | **Chosen by the user**: "New client" (name field pre-filled from the reported name) or an existing client from a list. Nothing is pre-selected among existing clients |

- `<client-id>` is the Clients hub id: a connect-registry id (`cursor`, `claude-code`) or a custom client's id. The path is the `resource` the client sends at authorize (RFC 8707), so the server knows the client before the page is shown.
- An id that is neither in the registry nor an existing custom client answers 404 on every step. Clients are not created by typing a new path.
- The address is not a secret and proves nothing. Any local process can use it. That is why consent is shown every time, and why the page says in words whose access is being handed over.
- A token issued for `/mcp/oauth/cursor` is accepted only there, and only while its grant belongs to `cursor`.
- The same registration (`client_id`) coming back is recognised exactly: the page says "Signed in before" and offers to replace that login.
- After login, the name sent at `initialize` is compared with the client's known names. A mismatch is an advisory warning on the Clients row. It blocks nothing.

### The consent page

```
Sign-in request for Cursor
The app calls itself "Cursor IDE". MCPProxy cannot verify that.
Returns to: http://127.0.0.1:54321/callback

Client    Cursor (already connected with an API key)
Profile   dev-readonly (locked)
          Approving gives this app the same access as Cursor.
[x] Replace the previous browser login (approved 3 Nov)

                                   [ Deny ]   [ Approve ]
```

Rules for the page:

- **Per-client address**: the client is fixed. If it already has a binding, the profile is shown, not asked. If it is new, the profile rule below applies.
- **Generic address**: the user picks the client first.
- **Profile for a client with no binding yet**: if no profiles are configured, "All servers", stored the way it is today for a client credential (no pin, `switchable`; a locked credential with an empty profile is invalid under `ValidateTokenInvariants` and stays so). If profiles exist, nothing is pre-selected and Approve stays disabled until the user picks one; a named profile defaults to `locked`. Same rule as `profile_required` in the `credentials` tool.
- **Replace previous login** is pre-checked only when this exact `client_id` already has a grant. Otherwise the new login is added next to the existing ones.
- **No admin key in this browser**: the page cannot load the request. It shows "Open this request from the MCPProxy tray, or paste the API key". The core emits an SSE event for a new authorization request; the tray shows a notification and a menu item "Approve sign-in for Cursor…" that opens the same URL with the key, the way it opens the Web UI today.
- **Bypassable binding**: when the profile is a named one and `require_mcp_auth` is off, approval would be refused with `binding_bypassable_without_auth`. The page shows that refusal before the button is pressed, with the fixes the binding guard itself lists (`internal/runtime/binding_guard.go`: turn `require_mcp_auth` on, or set `anonymous_profile`). No exception for OAuth clients.
- The page is never framed (`frame-ancestors 'none'`). Approval is an API-key-authenticated REST call, not a cookie form, so there is no cross-site request to forge.

Consent is shown every time. A public client has no secret and loopback redirects match on any port, so a different local process can reuse a known `client_id` or address. The click is the check.

### Identity and data

Three new records, in their own bbolt buckets. They are keyed by client, not by upstream server, so the Spec 116 server purge set does not apply; their cleanup is described under "Revoke and forget".

- **Registration** (`oauth_registrations`): opaque random `client_id`, reported name (sanitised with `clientidentity`), redirect URIs, optional `software_id` / `software_version`, created time, state `pending | approved | denied`.
- **Grant** (`oauth_grants`): one approved login. Fields: id, `client_id` of the registration, **Clients hub client id**, the approved `resource` URI and issuer, approved time, approving actor, last used, revoked flag, `user_id` (empty in the personal edition). Holds the hash and 12-character prefix of the current refresh token.
- **Authorization request / code**: short-lived, memory only. A restart drops them and the client starts again.

Tokens:

- Access token: opaque, prefix `mcp_oat_`, HMAC-SHA256 hashed like agent tokens, 1 hour, accepted only on the OAuth address it was issued for.
- Refresh token: `mcp_ort_<grant-id>_<secret>`, rotated on every use, 30 days sliding. The grant id inside the token is what makes replay detectable after any number of rotations and across restarts: a token that names a live grant but whose secret is not the current one is a replay, and the grant is revoked in the same transaction (OAuth 2.1 rule for public clients). Consuming the old secret and storing the new hash is one transaction. The grant id is random, appears only inside tokens and grants nothing by itself.

**One client, several credentials.** A Clients hub row is the identity. Under it: at most one API key (today's `mcp_cli_` record) and any number of OAuth grants. The profile binding belongs to the client, not to a credential. An access token authenticates to the same `AuthContext` a client credential produces: `AuthTypeAgent`-class caller, `Kind=client`, the client's id, the client's binding. Everything downstream (profile resolution, pin boundary, activity `client_id`, `/tools?client=`) is unchanged.

The invariant for the builder: **one binding per client id, read by both the API-key path and the OAuth path, changed by one writer** (`ClientsService`, which already owns `SetBinding`, `BulkAssign`, `Add` and `Forget`). Where it is stored is fixed here, because a downgrade must never widen access:

- A client with an API key keeps its binding on that record (`ProfilePin`, `ProfileMode`), exactly as today. Those fields stay authoritative for the API-key path and are always kept current, so an older binary reads the right profile.
- A client with grants also has the binding in an OAuth-side record (`oauth_client_bindings`), read by the OAuth path.
- `ClientsService` writes both in one transaction when both exist, including on rotation and reconnect. A startup check repairs a mismatch from the API-key record and logs it.
- With the feature off, no bucket is created and nothing is migrated.

Reassign, rename, delete-profile (`profile_in_use`, `reassign_to`, dangling means deny-all) and the bypassable-binding check must cover OAuth-only clients exactly as they cover API-key clients.

Activity rows gain the credential kind `oauth` and the grant id, so two logins of one client can be told apart. Issue, approve, deny, refresh-reuse and revoke are recorded as `profile_change`-family rows through `CredentialsService`, with the actor in context.

### Canonical address and where OAuth is served

- The resource and the issuer are built from the **origin of the request**, after the existing host validation (loopback names and `trusted_hosts`). `http://127.0.0.1:8080` and `http://localhost:8080` are two resources; a client stays on the one it was configured with. The metadata documents, the `resource` check at authorize and the audience of a token all use that origin, and a token is refused on any other.
- The consent redirect stays on the same origin. If the browser holds the admin key under another origin, the page takes the "no admin key" path and the tray opens the request on the right origin.
- Version 1 serves `/mcp/oauth`, `/oauth/*` and the metadata only to requests that arrive from a loopback address with a loopback host. Anything else gets 404, so a LAN listen exposes no open registration. The tunnel lifts this later under its own rules.
- `/mcp/oauth` and `/mcp/oauth/<client>` serve the same tool surface as `/mcp` (they follow `routing_mode`). While the feature is on they are registered ahead of the `/mcp/` catch-all. While it is off they are not registered at all, so these paths keep falling into the catch-all exactly as today.
- Server edition: the feature answers 404 in version 1 whatever the setting, because consent by admin API key would create grants with no user.

### Revoke and forget

- **Revoke a client** (`credentials revoke client=<id>`, REST, CLI): revokes everything under it, the API key and every grant. This keeps today's meaning, "this client loses access".
- **Revoke one credential**: new, for a single grant (by grant id) or for the API key alone. Other credentials of the client keep working.
- **Forget a client** (`DELETE /clients/{client}`, `ClientsService.Forget`): deletes its grants in the same transaction, and each registration left without a grant.
- **Active binding**: the binding guard and `profile_in_use` treat a client's binding as active while it has an API key that is neither revoked nor expired, or at least one grant that is neither revoked nor lapsed. A grant lapses when its refresh token expires (30 days without use).

### Clients hub

```
Cursor                       profile: dev-readonly (locked)
  API key      mcp_cli_ab12…           last used 2 h ago     [Rotate] [Revoke]
  Browser login  approved 22 Nov       last used 5 min ago   [Revoke]
  Browser login  approved 3 Nov        revoked
```

- Revoking one login stops its access token on the next request and its refresh token at once. Revoking the client revokes everything under it.
- If the name a client sends at `initialize` does not match the name it registered with or the selected client's known names, the row shows an advisory warning. Nothing is blocked.
- `credentials list|get|revoke` (MCP), REST `/clients` and `mcpproxy client …` show and revoke grants. They never issue one: a grant exists only through the consent page.

### Connect

The registry gains a flag for clients known to support the MCP authorization flow. Only while `mcp_oauth_enabled` is on, Connect (Web UI, CLI `mcpproxy connect <client> --auth oauth|key`) offers both for those clients. "Sign in with browser" writes only the client's own address, `/mcp/oauth/<client-id>`, into the client's config, no header and no secret. The diff preview and backup notice are unchanged. The default choice stays "API key". With the feature off, Connect looks and behaves as today.

### Limits and abuse

- `POST /oauth/register`: body at most 16 KiB; rate limited per remote address; at most 50 pending registrations, oldest dropped. A registration never authorised is deleted after 15 minutes; one authorised but never approved, after 24 hours.
- Redirect URIs: `http` on `127.0.0.1`, `[::1]` or `localhost` (any port at authorize time, per RFC 8252), or `https`. Exact match otherwise. Custom schemes are refused in version 1.
- `/oauth/authorize` and `/oauth/token`: rate limited; codes are single-use, 60 seconds, bound to client, redirect URI and PKCE challenge.
- All `/oauth/*` and `/.well-known/*` endpoints are unauthenticated by nature and answer nothing about servers, profiles or existing clients.

### Configuration and upgrade

`mcp_oauth_enabled`, default **off**, env `MCPPROXY_MCP_OAUTH_ENABLED`, also a switch on the Settings page marked "Experimental". No restart is needed: routes are added and removed on config reload.

- **Off (default)**: no new route, no metadata document, no new bucket is created, no Connect option, no Clients hub column, no startup notice. Existing API keys, agent tokens, client credentials, `/mcp` and every routing-mode endpoint are untouched. A test pins this: the route table and the answers on `/mcp/oauth*`, `/oauth/*` and `/.well-known/oauth-*` with the feature off equal those of the previous release.
- **On**: everything in this document. API keys keep working next to it.
- **Turned off again**: two cases, told apart by whether any grant was ever stored.
  - Never used (no grant record): identical to "off" above, including the legacy catch-all on `/mcp/oauth*`.
  - Used: `/mcp/oauth*` answers 404 instead of falling into the `/mcp/` catch-all, so a browser-login client does not quietly continue as an anonymous caller when `require_mcp_auth` is off. `/oauth/*`, the metadata and the consent REST calls answer 404 and pending requests are cancelled. Grants and bindings are kept, so turning it on again restores them. Such a client's row says "browser login disabled".
- **Downgrade** to a binary without the feature: the buckets are ignored, OAuth-only clients fall into the `/mcp/` catch-all as anonymous callers. The docs say to turn `require_mcp_auth` on first, as for pre-108 downgrades.

## Delivery

1. **PR-1, backend**: metadata, registration, authorize, token, storage, `/mcp/oauth` and `/mcp/oauth/<client>` handlers, the off-by-default switch with its unchanged-behaviour test, authentication into the existing client `AuthContext`, the single binding, REST for authorization requests (get with pre-filled choices, approve, deny), activity rows, limits.
2. **PR-2, surfaces**: consent page, macOS tray notification, Clients hub credentials list, `credentials` / REST / CLI list and revoke. **PR-2b**: the same notification in the Go tray (Windows, Linux); until then those users paste the key or open the Web UI from the tray first.
3. **PR-3, Connect and docs**: registry flag, the Connect choice, `docs/features/client-oauth-login.md`, updates to connect-clients, profiles, REST reference.

Each PR is test-first. End to end: a scripted public client runs the whole flow against a built core (`scripts/test-client-oauth-e2e.sh`), including refresh rotation, reuse detection, revoke on a live session and the bypassable-binding refusal.

## Decided by Algis

- OAuth and API keys both stay; the user chooses per client.
- Suggest and confirm on the consent page.
- Which client a login belongs to must be reliable, not guessed from names (2026-11-22).
- Off by default in the first release; an update needs no action and changes nothing for existing users (2026-11-22).

## Calls made here (not decided by Algis)

- Readable per-client address (`/mcp/oauth/<client-id>`) instead of a random one: it carries no authority, so it need not be secret.
- A generic-address login never pre-selects an existing client.
- Consent every time, with no silent re-approval.
- Token lifetimes (1 hour / 30 days sliding) and the numeric limits above.
- Default Connect choice stays "API key".
- No CLI approval, no custom-scheme redirects, no confidential clients in version 1.
- Resource and issuer follow the request origin; OAuth is loopback-only and off in server edition for version 1.
- "Revoke client" revokes the API key and all grants; forgetting a client deletes its grants.
- `/mcp/oauth` follows `routing_mode`; Go tray notification is a follow-up PR.
- Document location: `specs/131-client-oauth-login/design.md`, to become the input of `speckit.specify`.

## Left to the builder

- Bucket layouts, the exact REST paths and payloads, SSE event name.
- Whether access tokens are separate rows or derived from the grant record.
- Rate-limit implementation, reusing the existing limiter where it fits.

## Review

Cross-model review, round 1 (opencode `gpt-6.1-sol`, 2026-11-22): nine findings, all accepted. Replay detection through the grant id in the refresh token, resource and issuer stored on request, code and grant, single-decision consent with a binding revision, fail-closed OAuth paths after the feature was used and turned off, the HTTP-issuer deviation, exact metadata shapes, `state`, downgrade-safe binding storage, and the "All servers" representation.

## Later

- **Server edition**: the consent page needs the user's session; the grant carries `user_id`; selectable profiles and clients are the user's own.
- **Tunnel (Spec 089)**: same endpoints behind the tunnel origin, consent protected by the proxy-managed credential, tokens bound to the tunnel resource URI. Client ID Metadata Documents would give hosted clients a domain-verified name to show on the consent page.
