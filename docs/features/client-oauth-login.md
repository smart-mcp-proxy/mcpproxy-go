---
id: client-oauth-login
title: Browser Login for MCP Clients
sidebar_label: Browser Login for MCP Clients
sidebar_position: 7
description: Let MCP clients sign in to MCPProxy through the browser (OAuth 2.1 with PKCE) next to API keys. Opt-in, loopback only, personal edition. Connect can write the sign-in address for clients that passed a real-client check (Claude Code so far).
keywords: [oauth, browser login, mcp client, pkce, dynamic client registration, rfc 9728, rfc 8414, rfc 8707, cursor, claude code, vscode]
---

# Browser Login for MCP Clients

MCPProxy can act as a small OAuth 2.1 authorization server so that an MCP client
that supports OAuth can sign in instead of carrying an API key in its config file.
It is **off by default** and **experimental**. An administrator approves each sign-in on
a consent page or through the REST API (see [Approving a sign-in](#approving-a-sign-in))
and manages the logins in the Clients page, the CLI or the `credentials` MCP tool
(see [Managing logins](#managing-logins)).

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

### The consent page

The request opens in the Web UI at `/ui/authorize?request=<id>` (the page is never framed:
`frame-ancestors 'none'` and `X-Frame-Options: DENY`). It shows the name the app reports,
labelled as unverified, where the browser returns to, and one sentence on whose access is being
handed over. Everything the app reports is shown as plain text.

- **Per-client address**: the client is fixed. A client that has a profile binding shows it,
  without asking; a client with none and configured profiles needs a profile chosen before
  Approve is enabled. "Replace the previous browser login" appears only when this same
  registration signed in before.
- **Generic address**: the page opens with a client selected and you can change it. When the
  reported name equals exactly one existing client's id, display name or known client name
  (`Claude Code (my-server)` is also tried as `Claude Code`), that client is pre-selected with
  the note `Suggested because the app calls itself "<name>". MCPProxy cannot verify that.`
  Otherwise "New client" is selected, with an editable id and name filled from the reported name.
  The suggestion decides nothing: approval carries the client the page sent, and a request with
  no client is refused (`client_required`).
- The binding guard's verdict is fetched through the preview call whenever the choice is
  complete or changes; a refusal is shown with its fixes and Approve stays disabled.
- Without the admin API key in this browser the page asks you to open the request from the
  MCPProxy tray or paste the key, and makes no request until then.

### From the macOS tray

The tray is the way to reach the consent page with the key in the browser. When an MCP client
asks to sign in, the core publishes `oauth.authorization_pending`; the tray then

- sends one notification ("Sign-in request") naming the app, and
- adds a menu row **Approve sign-in for \<app\>…** per waiting request, newest first (five rows,
  the rest in a "N more sign-in requests" submenu).

The row opens `/ui/authorize?request=<id>` the way "Open Web UI in Browser" opens the Web UI (the
API key travels in the same `?apikey=` parameter, which the page moves into local storage). The
tray never approves or denies anything itself; the decision is made on the page.

A row disappears when the request is decided (`oauth.authorization_decided`) or expires (10
minutes). On connect, reconnect and every periodic refresh the tray reloads
`GET /api/v1/oauth/authorizations`, so a request made while the tray was not listening still
shows, and stale ones are dropped. While browser login is off the endpoint answers `404
feature_disabled` and the tray shows nothing.

The app name is chosen by the client, so the tray treats it as untrusted text: control and
invisible characters are removed, white space is collapsed, it is cut to 40 characters, and it is
only ever shown as plain text.

## Managing logins

Everything here is for administrators. An agent token or a client credential cannot see
or revoke a login on any surface.

**Settings.** One switch, "Browser login for MCP clients", marked Experimental, in
Security & Access (the tray Settings window has the same switch). It takes effect without a
restart. While it is off and was never used, no other page mentions browser login.

**Clients page.** Expand a client to see its credentials: the API key and every login, each
with its state (`active`, `lapsed` when the refresh token expired, `revoked`), when it was
approved and last used, the name the app reported and the host it returns to. Those names
come from the app, so they are shown as plain text. A login whose client introduces itself
at `initialize` under a different name than the one it registered shows a "name mismatch"
note; it is advice, never a block. "Revoke login" revokes that one login, "Revoke API key"
revokes the key alone (the logins and the profile binding stay); both ask first. While the
switch is off after use, the page says browser login is disabled and still lists the
logins so they can be revoked.

**CLI.**

```bash
mcpproxy client show cursor                 # lists the API key and each login
mcpproxy client revoke-login cursor <grant-id>
```

**MCP.** The administrator `credentials` tool lists logins (`list` with `kind=browser_login`,
and they appear under `kind=all`), `get client=<id>` includes them under `logins`, and
`revoke client=<id> grant=<grant_id>` revokes one. `revoke client=<id>` without `grant`
revokes everything under the client. No operation creates a login: only the person on the
consent page can approve one. See [credential lifecycle over MCP](./mcp-credential-lifecycle.md).

**REST.** `GET /api/v1/clients` and `GET /api/v1/clients/<id>` list each login under `credentials`
(`kind: browser_login`, state `active`, `lapsed` or `revoked`, never the token). Revoke one
with `DELETE /api/v1/clients/<id>/logins/<grant_id>`; the next request on that login,
including on an open session, is refused. Forgetting a client removes all its logins.

## Connecting a client to browser login

Connect can write the sign-in address instead of an API key. The client is given only
`http://127.0.0.1:8080/mcp/oauth/<client-id>`; there is no header, no bridge argument and no
credential in its config file, and the client credential store is not touched.

```bash
mcpproxy connect claude-code --auth oauth          # key is the default: --auth key
mcpproxy connect claude-code --auth oauth --force  # switch an entry that is already there
```

In the Web UI and the macOS app, the Connect form shows **API key** (selected) or **Sign in
with browser** after the diff and the backup notice, and before the Connect button. It is
shown only while browser login is on and only for a client in the table below; while the
switch is off the form is the one it always was. Choosing the browser shows the exact entry
that will be written and the note below, and hides the profile and keyless controls: the
access is chosen on the consent page.

What Connect does with `--auth oauth` (REST `auth: "oauth"`, preview `?auth=oauth`):

- It is **refused**, with nothing written, while `mcp_oauth_enabled` is off, for a client that
  is not in the table, in the server edition, and together with `--profile`, `--lock`,
  `--switchable` or `--keyless`.
- The preview and the result carry `auth: "oauth"`, no credential and no masked value. The
  preview token binds the choice, so a key write cannot ride an oauth preview.
- **Reconnecting replaces the one entry, it never adds a second.** Key to browser and
  browser to key both need `--force` (the Web UI and the app do this when you confirm the
  replace).
- **Connect never revokes an API key.** After switching to the browser the client's existing
  key keeps working until you revoke it (Revoke API key on the Clients page). The preview and
  the result say so. `mcpproxy disconnect <client>` removes the entry and revokes the client's
  API key as before; it does not revoke browser logins (use the Clients page or
  `mcpproxy client revoke-login <id> <grant-id>`).
- Disconnect, Undo and the status read recognise an entry written this way. Undo of an oauth
  connect restores the earlier config and revokes nothing, since the connect minted nothing.
- `GET /api/v1/connect` and `GET /api/v1/connect/{client}` report `supports_browser_login`
  (the client is in the table) and `browser_login_available` (it is in the table **and** the
  feature is on in this edition). A status read of a client whose entry holds the address
  reports `auth: "oauth"`.

### Clients checked so far

A client is marked as supporting browser login only after a recorded check with the real
client (`specs/132-client-oauth-login/verification/real-clients.md`). Passing the MCP SDK is
not enough.

| Client | Offered by Connect | What was verified |
|---|---|---|
| Claude Code | **yes** (`claude-code`) | Claude Code CLI 2.1.318 on macOS completed the login flow over plain HTTP loopback: it accepted the `http://127.0.0.1` issuer, registered itself, sent the authorize request for the per-client address (`resource` equal to it), and after approval reported "Connected" and made MCP calls with the token. **Not** verified: token refresh, the order of the metadata requests, the browser-and-callback path (the check pasted the redirect URL at the CLI prompt), tool calls inside an interactive session, the generic `/mcp/oauth` address, Windows and Linux |
| Cursor | no | not verified (needs its real config and a GUI) |
| VS Code, GitHub Copilot | no | not verified (needs its real settings and a GUI) |
| MCP Inspector | not a Connect client | not verified (needs a TTY and a browser) |
| MCP TypeScript SDK 1.32.2 | not a Connect client | login, refresh and tool calls verified with a script |
| Every other Connect client | no | not checked |

Until a client is checked, use its API key. A client can still be pointed at an address by
hand; Connect just does not offer it.

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
checked in `specs/132-client-oauth-login/verification/real-clients.md`; the MCP
TypeScript SDK client flow and the Claude Code CLI login flow are verified so far (Cursor, VS
Code and MCP Inspector are not; see [Clients checked so far](#clients-checked-so-far)).
