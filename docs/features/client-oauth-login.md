---
id: client-oauth-login
title: Browser Login for MCP Clients
sidebar_label: Browser Login for MCP Clients
sidebar_position: 7
description: Let MCP clients sign in to MCPProxy through the browser (OAuth 2.1 with PKCE) next to API keys. Opt-in, loopback only, personal edition. Backend and admin REST only in this release.
keywords: [oauth, browser login, mcp client, pkce, dynamic client registration, rfc 9728, rfc 8414, rfc 8707, cursor, claude code, vscode]
---

# Browser Login for MCP Clients

MCPProxy can act as a small OAuth 2.1 authorization server so that an MCP client
that supports OAuth can sign in instead of carrying an API key in its config file.
It is **off by default** and **experimental**: in this release only the backend and
the administrator REST exist. There is no consent page yet, so an administrator
approves each sign-in through the REST API (see [Approving a sign-in](#approving-a-sign-in)).

What it gives you:

- public clients only (no client secret), PKCE `S256`, dynamic client registration
  (RFC 7591), protected-resource and authorization-server metadata (RFC 9728, RFC 8414)
  and the `resource` parameter (RFC 8707);
- one sign-in address per client: `http://127.0.0.1:8080/mcp/oauth/<client-id>`
  (for example `/mcp/oauth/cursor`), or the generic `/mcp/oauth` where the administrator
  chooses the client at approval;
- API keys keep working unchanged on `/mcp` and everywhere else.

It is served to loopback peers and loopback `Host` values only, and only by the
personal edition (`mcpproxy`). In the server edition the feature is unavailable: with the switch on, the OAuth paths answer `404`; with it off and never used, nothing changes, as in the personal edition.

## Enabling

```json
{ "mcp_oauth_enabled": true }
```

or `MCPPROXY_MCP_OAUTH_ENABLED=true` for one process (never persisted). The setting is
read live, so a config change needs no restart; the environment variable is read at start.

With the setting off **and never used**, nothing changes: the OAuth paths behave as they did
before, and no storage bucket is created. A client registration is stored as soon as a client registers, but that does not change what
"off" means. The boundary is the first stored **login** (an authorization code exchanged for
tokens): from then on, off means the OAuth paths answer `404` and the records are kept (see
[Turning it off](#turning-it-off)). `scripts/test-client-oauth-e2e.sh` checks off-and-never-used
before it switches the feature on, and that "on, register only, off" still behaves as before.

## Addresses

| Path | Purpose |
|---|---|
| `/mcp/oauth`, `/mcp/oauth/<client-id>` | The MCP endpoint, same tools as `/mcp`, bearer access token only |
| `/.well-known/oauth-protected-resource/mcp/oauth[/<client-id>]` | Protected-resource metadata (RFC 9728) |
| `/.well-known/oauth-authorization-server` | Authorization-server metadata (RFC 8414) |
| `POST /oauth/register` | Dynamic client registration (RFC 7591) |
| `GET /oauth/authorize` | Starts a sign-in; redirects to `/ui/authorize?request=<id>` |
| `POST /oauth/token` | Code exchange and refresh |

An unknown client id in an address is `404`. Without a valid token the OAuth MCP address
answers `401` with a `WWW-Authenticate: Bearer resource_metadata="..."` challenge that
points at the metadata of that exact address, so a client discovers the per-client path
on its own. Registration, authorize and token are rate limited (`429` with `Retry-After`).

## Tokens

- Access token `mcp_oat_...`, valid for 1 hour, accepted only on the address it was issued for.
- Refresh token `mcp_ort_...`, valid for 30 days from the last rotation, rotated on every use.
  Presenting an old (already rotated) refresh token revokes the whole login.
- An access token is refused on `/mcp`, on another client's address and on a different
  origin (for example `localhost` instead of `127.0.0.1`). An API key, `?apikey=`, an agent
  token or the admin key is refused on the OAuth addresses.

A login belongs to a client and uses that client's profile binding, like its API key.
A binding that would let the client bypass its profile through `/mcp` without authentication
is refused the same way as for API keys (`binding_bypassable_without_auth`); set
`require_mcp_auth` or use an all-servers binding.

## Approving a sign-in

A sign-in is held until an administrator decides it. With the admin API key:

```bash
# pending sign-ins
curl -s -H "X-API-Key: $KEY" http://127.0.0.1:8080/api/v1/oauth/authorizations
# one request, with the client, its binding and its revision
curl -s -H "X-API-Key: $KEY" http://127.0.0.1:8080/api/v1/oauth/authorizations/<id>
# approve (existing client: pass binding_revision when it has a binding)
curl -s -X POST -H "X-API-Key: $KEY" -H 'Content-Type: application/json' \
  http://127.0.0.1:8080/api/v1/oauth/authorizations/<id>/approve \
  -d '{"client":{"id":"cursor"},"mode":"switchable","profile":""}'
```

The answer is `{"data":{"redirect_url":"..."}}`; the client receives the code when its
browser is sent there. Approval writes an audit activity row but stores no login and no
binding: the login is stored when the client exchanges the code, after the binding and guard
are checked again. (The client's registration was already stored when it registered.) `.../deny` ends the
request with `access_denied`; `.../preview` shows the guard verdict without writing.
Full contract: [REST API](../api/rest-api.md).

## Seeing and revoking logins

`GET /api/v1/clients` and `GET /api/v1/clients/<id>` list each login under `credentials`
(`kind: browser_login`, state `active`, `lapsed` or `revoked`, never the token). Revoke one
with `DELETE /api/v1/clients/<id>/logins/<grant_id>`; the next request on that login,
including on an open session, is refused. Forgetting a client removes all its logins.

## Turning it off

Set `mcp_oauth_enabled` back to `false` (or the variable to `false`). After the feature has
been used, the OAuth paths answer `404`, pending sign-ins are cancelled and existing
logins stop working; they are kept, so turning it on again restores them while their
tokens are still valid. `browser_login_disabled` on the client payload is `true` while it
is off after use. API-key access is never affected. A login can still be revoked while off.

## Downgrade

Browser-login records live in their own storage buckets, created lazily on the first write
(a client registration, then the first stored login). A binary that does not know the feature does not serve the OAuth paths, so the
logins simply stop being usable.

## Plain HTTP

The issuer is `http://127.0.0.1:<port>`, not HTTPS. The specifications ask for HTTPS for
non-loopback issuers; loopback HTTP is the common local-tool case. Client behaviour is
checked in `specs/132-client-oauth-login/verification/real-clients.md`; only the MCP
TypeScript SDK client flow is verified so far.
