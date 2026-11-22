# Design: OAuth login for MCP clients, next to API keys

**Issue**: [#1971](https://github.com/smart-mcp-proxy/mcpproxy-go/issues/1971) · **Date**: 2026-11-22 · **Status**: Draft for approval
**Scope**: personal edition. Server edition and the tunnel (Spec 089) reuse the same parts later.

## Intent

Today every MCP client needs a credential pasted into its config file. The user wants a second way in: the client is given only a URL, opens the browser, the user approves, and the client is connected. Both ways stay available and the user chooses per client.

The open question this design answers: **after an OAuth login, how does MCPProxy know which client this is and what it may reach?**

Answer: MCPProxy suggests, the user confirms. The client's self-reported name only pre-fills the consent page. The identity is the approved registration. Access comes from the profile bound to the client, the same binding API-key clients use.

## Goals

- A client configured with `http://127.0.0.1:8080/mcp/oauth` and nothing else can log in through the MCP authorization flow (RFC 9728 discovery, RFC 8414 metadata, RFC 7591 registration, authorization code + PKCE S256, refresh).
- One click on the consent page in the common case.
- An OAuth client is an ordinary row in the Clients hub: same profile binding, same activity attribution, same reassign and revoke.
- A client may hold an API key, OAuth logins, or both.
- Connect offers the choice "API key" or "Sign in with browser" for clients that support OAuth.

## Non-goals

- Replacing API keys or agent tokens. Headless clients keep them.
- OAuth on `/mcp`, `/mcp/all`, `/mcp/code`, `/mcp/call`, `/mcp/p/<slug>`, `/mcp/s/<server>`. Version 1 serves one OAuth endpoint, `/mcp/oauth`. `/mcp` behaves exactly as today.
- Confidential clients (client secrets), Client ID Metadata Documents, token introspection endpoints.
- Server edition behaviour and the tunnel. The data model leaves room for both (see "Later").
- Approving a login from the CLI.

## Anti-goals

- A client gets access because of the name it reports. Never.
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
3. **Suggest and confirm (chosen).** The server computes a suggestion (which client, which profile, whether this replaces an earlier login). The consent page shows it pre-filled. The user approves with one click or changes it. Chosen by Algis on 2026-11-22.

## How it works

### Flow

1. The client calls `/mcp/oauth` without a token. Answer: `401` with `WWW-Authenticate: Bearer resource_metadata="…"`.
2. The client reads `/.well-known/oauth-protected-resource/mcp/oauth` and `/.well-known/oauth-authorization-server`, then registers at `POST /oauth/register`.
3. The client opens the browser at `GET /oauth/authorize`. The server validates the request (known `client_id`, exact redirect URI match, `code_challenge_method=S256`, `resource` equal to the `/mcp/oauth` URL), stores an **authorization request** (10 minute life) and redirects the browser to `/ui/authorize?request=<id>`.
4. The Web UI consent page loads the request and its suggestion through the REST API, with the admin API key.
5. Approve: the page posts the decision; the answer carries the redirect URL with a single-use code. The page sends the browser there. Deny: redirect with `error=access_denied`.
6. The client exchanges the code at `POST /oauth/token` with its PKCE verifier and gets an access token and a refresh token.

### The consent page

```
"Cursor" wants to connect to MCPProxy
Name reported by the app. MCPProxy cannot verify it.
Returns to: http://127.0.0.1:54321/callback

Client    (•) Cursor (already connected with an API key)
          ( ) New client  [ Cursor 2          ]
Profile   [ dev-readonly ▾ ]   locked
          Approving gives this app the same access as Cursor.
[x] Replace the previous browser login of Cursor (approved 3 Nov)

                                   [ Deny ]   [ Approve ]
```

Rules for the page:

- The reported name is always labelled as unverified. When an existing client is selected, the page says in words that the app receives that client's access.
- **No admin key in this browser**: the page cannot load the request. It shows "Open this request from the MCPProxy tray, or paste the API key". The core emits an SSE event for a new authorization request; the tray shows a notification and a menu item "Approve sign-in for Cursor…" that opens the same URL with the key, the way it opens the Web UI today.
- **Bypassable binding**: when the chosen profile is a named one and `require_mcp_auth` is off, approval would be refused with `binding_bypassable_without_auth`. The page shows that refusal before the button is pressed, with the fixes the binding guard itself lists (`internal/runtime/binding_guard.go`: turn `require_mcp_auth` on, or set `anonymous_profile`). No exception for OAuth clients.
- The page is never framed (`frame-ancestors 'none'`). Approval is an API-key-authenticated REST call, not a cookie form, so there is no cross-site request to forge.

### The suggestion

Computed on the server, returned with the authorization request. First match wins:

| # | Evidence | Client | Profile | Extra |
|---|---|---|---|---|
| 1 | This `client_id` already has an approved grant | the grant's client | the client's binding | "Signed in before" |
| 2 | An approved grant exists whose registration had the same reported name (normalised) and the same redirect URI set | that grant's client | its binding | "replace previous login" pre-checked |
| 3 | Reported name matches the registry (below), and a Clients row with that id exists | that row | its binding | |
| 4 | Reported name matches the registry, no row yet | new client with the registry id | see below | registry icon and display name |
| 5 | Nothing matches | new custom client, id is a slug of the name, made unique | see below | |

Registry match: the name a client registers with is not the name it sends at `initialize`, so `ClientInfoNames` alone would rarely match. The registry gains `OAuthClientNames` per client (PR-1, filled from observed registrations and public documentation, best effort like `ClientInfoNames`). A reported name matches a registry client when, after normalisation and case folding, it equals the client's id, display name, a `ClientInfoNames` alias or an `OAuthClientNames` entry. Row 2 is what stops the pile-up for clients the registry does not know: the first approval teaches MCPProxy which client that name and redirect set belong to.

Profile for a new client: if no profiles are configured, "All servers". If profiles exist, nothing is pre-selected and Approve stays disabled until the user picks one. This is the same rule as `profile_required` in the `credentials` tool. Binding mode defaults to `locked`.

Consent is shown every time, including row 1. A public client has no secret and loopback redirects match on any port, so a different local process can reuse a known `client_id`. The click is the check.

### Identity and data

Three new records, in their own bbolt buckets. They are keyed by client, not by upstream server, so the Spec 116 server purge set does not apply; their cleanup is described under "Revoke and forget".

- **Registration** (`oauth_registrations`): opaque random `client_id`, reported name (sanitised with `clientidentity`), redirect URIs, optional `software_id` / `software_version`, created time, state `pending | approved | denied`.
- **Grant** (`oauth_grants`): one approved login. Fields: id, `client_id` of the registration, **Clients hub client id**, approved time, approving actor, last used, revoked flag, `user_id` (empty in the personal edition). Holds the hash and 12-character prefix of the current refresh token and the token family id.
- **Authorization request / code**: short-lived, memory only. A restart drops them and the client starts again.

Tokens:

- Access token: opaque, prefix `mcp_oat_`, HMAC-SHA256 hashed like agent tokens, 1 hour, accepted only on `/mcp/oauth`.
- Refresh token: opaque, prefix `mcp_ort_`, rotated on every use, 30 days sliding. Reuse of an already rotated refresh token revokes the grant (OAuth 2.1 rule for public clients).

**One client, several credentials.** A Clients hub row is the identity. Under it: at most one API key (today's `mcp_cli_` record) and any number of OAuth grants. The profile binding belongs to the client, not to a credential. An access token authenticates to the same `AuthContext` a client credential produces: `AuthTypeAgent`-class caller, `Kind=client`, the client's id, the client's binding. Everything downstream (profile resolution, pin boundary, activity `client_id`, `/tools?client=`) is unchanged.

The invariant for the builder: **one binding per client id, read by both the API-key path and the OAuth path, changed by one writer** (`ClientsService`, which already owns `SetBinding`, `BulkAssign`, `Add` and `Forget`). Today the binding is stored on the API-key record. How to hold it for a client that has only OAuth grants is the builder's call (a credential-less client record, or a small `client_bindings` bucket that both paths read). Reassign, rename, delete-profile (`profile_in_use`, `reassign_to`, dangling means deny-all) and the bypassable-binding check must cover OAuth-only clients exactly as they cover API-key clients.

Activity rows gain the credential kind `oauth` and the grant id, so two logins of one client can be told apart. Issue, approve, deny, refresh-reuse and revoke are recorded as `profile_change`-family rows through `CredentialsService`, with the actor in context.

### Canonical address and where OAuth is served

- The resource and the issuer are built from the **origin of the request**, after the existing host validation (loopback names and `trusted_hosts`). `http://127.0.0.1:8080` and `http://localhost:8080` are two resources; a client stays on the one it was configured with. The metadata documents, the `resource` check at authorize and the audience of a token all use that origin, and a token is refused on any other.
- The consent redirect stays on the same origin. If the browser holds the admin key under another origin, the page takes the "no admin key" path and the tray opens the request on the right origin.
- Version 1 serves `/mcp/oauth`, `/oauth/*` and the metadata only to requests that arrive from a loopback address with a loopback host. Anything else gets 404, so a LAN listen exposes no open registration. The tunnel lifts this later under its own rules.
- `/mcp/oauth` serves the same tool surface as `/mcp` (it follows `routing_mode`). It is registered ahead of the `/mcp/` catch-all. Behaviour change to note in the docs: today `/mcp/oauth` falls into that catch-all and answers like `/mcp`.
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

The registry gains a flag for clients known to support the MCP authorization flow. For those, Connect (Web UI, CLI `mcpproxy connect <client> --auth oauth|key`) offers both. "Sign in with browser" writes only the `/mcp/oauth` URL into the client's config, no header. The diff preview and backup notice are unchanged. Default stays "API key" in version 1.

### Limits and abuse

- `POST /oauth/register`: body at most 16 KiB; rate limited per remote address; at most 50 pending registrations, oldest dropped. A registration never authorised is deleted after 15 minutes; one authorised but never approved, after 24 hours.
- Redirect URIs: `http` on `127.0.0.1`, `[::1]` or `localhost` (any port at authorize time, per RFC 8252), or `https`. Exact match otherwise. Custom schemes are refused in version 1.
- `/oauth/authorize` and `/oauth/token`: rate limited; codes are single-use, 60 seconds, bound to client, redirect URI and PKCE challenge.
- All `/oauth/*` and `/.well-known/*` endpoints are unauthenticated by nature and answer nothing about servers, profiles or existing clients.

### Configuration

`mcp_oauth_enabled` (default **on**, env `MCPPROXY_MCP_OAUTH_ENABLED`). Off: `/mcp/oauth`, `/oauth/*` and the metadata documents answer 404 and existing grants stop authenticating until it is on again.

## Delivery

1. **PR-1, backend**: metadata, registration, authorize, token, storage, `/mcp/oauth` handler, authentication into the existing client `AuthContext`, the single binding, REST for authorization requests (get with suggestion, approve, deny), activity rows, limits.
2. **PR-2, surfaces**: consent page, macOS tray notification, Clients hub credentials list, `credentials` / REST / CLI list and revoke. **PR-2b**: the same notification in the Go tray (Windows, Linux); until then those users paste the key or open the Web UI from the tray first.
3. **PR-3, Connect and docs**: registry flag, the Connect choice, `docs/features/client-oauth-login.md`, updates to connect-clients, profiles, REST reference.

Each PR is test-first. End to end: a scripted public client runs the whole flow against a built core (`scripts/test-client-oauth-e2e.sh`), including refresh rotation, reuse detection, revoke on a live session and the bypassable-binding refusal.

## Calls made here (not decided by Algis)

- One OAuth endpoint, `/mcp/oauth`, as the issue proposes; tokens are bound to it.
- Feature on by default.
- Consent every time, with no silent re-approval.
- Token lifetimes (1 hour / 30 days sliding) and the numeric limits above.
- Default Connect choice stays "API key".
- No CLI approval, no custom-scheme redirects, no confidential clients in version 1.
- Registry gains `OAuthClientNames`; suggestion row 2 learns from earlier approvals.
- Resource and issuer follow the request origin; OAuth is loopback-only and off in server edition for version 1.
- "Revoke client" revokes the API key and all grants; forgetting a client deletes its grants.
- `/mcp/oauth` follows `routing_mode`; Go tray notification is a follow-up PR.
- Document location: `specs/131-client-oauth-login/design.md`, to become the input of `speckit.specify`.

## Left to the builder

- Where the per-client binding is stored (see the invariant).
- Bucket layouts, the exact REST paths and payloads, SSE event name.
- Whether access tokens are separate rows or derived from the grant record.
- Rate-limit implementation, reusing the existing limiter where it fits.

## Later

- **Server edition**: the consent page needs the user's session; the grant carries `user_id`; selectable profiles and clients are the user's own.
- **Tunnel (Spec 089)**: same endpoints behind the tunnel origin, consent protected by the proxy-managed credential, tokens bound to the tunnel resource URI. Client ID Metadata Documents would give hosted clients a domain-verified name and can strengthen suggestion row 2.
