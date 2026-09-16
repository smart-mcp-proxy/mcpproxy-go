---
title: "Server Multi-User Authentication"
sidebar_label: "Server Multi-User Auth"
description: "OAuth-based multi-user authentication for the server edition with Google, GitHub, or Microsoft identity providers."
---

# Server Multi-User Authentication (Spec 024)

Server edition supports OAuth-based multi-user authentication with Google, GitHub, or Microsoft identity providers. All server code is behind `//go:build server`; the personal edition is unaffected.

## Server Configuration

```json
{
  "server_edition": {
    "enabled": true,
    "admin_emails": ["admin@company.com"],
    "oauth": {
      "provider": "google",
      "client_id": "xxx.apps.googleusercontent.com",
      "client_secret": "GOCSPX-xxx",
      "tenant_id": "",
      "allowed_domains": ["company.com"]
    },
    "session_ttl": "24h",
    "bearer_token_ttl": "24h"
  }
}
```

The former knobs `workspace_idle_timeout` and `max_user_servers` never
controlled anything and were removed (Spec 107). A config file that still
carries them loads: the server edition drops each with one startup warning
(`server_edition.max_user_servers is no longer supported and was ignored`,
likewise for `workspace_idle_timeout`) and the next write-back omits them;
`PATCH /api/v1/config` and `/config/apply` refuse them with the same text. The
personal edition carries the whole `server_edition` block as opaque JSON and
neither warns nor validates it. `store_idp_tokens` is likewise accepted and
ignored (one deprecation warning when `true`); see
[IdP Token Storage](../features/idp-token-storage.md).

## Server API Endpoints

| Endpoint | Auth | Description |
|----------|------|-------------|
| `GET /api/v1/auth/login` | Public | Initiate OAuth login flow |
| `GET /api/v1/auth/callback` | Public | OAuth callback (creates session) |
| `GET /api/v1/auth/me` | Session/JWT | Get current user profile |
| `POST /api/v1/auth/token` | Session | Mint a user JWT for the REST API and CLI (`/api/v1/user/*`); a JWT is **never** an MCP credential — `/mcp` accepts only agent tokens, the API key and the socket |
| `POST /api/v1/auth/logout` | Session | Invalidate session |
| `GET /api/v1/user/servers` | Session/JWT | List user's servers (personal + shared) |
| `POST /api/v1/user/servers` | Session/JWT | Add personal upstream server |
| `GET /api/v1/user/activity` | Session/JWT | User's activity log |
| `GET /api/v1/user/diagnostics` | Session/JWT | Server health for user's servers |
| `GET /api/v1/admin/users` | Admin | List all users |
| `POST /api/v1/admin/users/{id}/disable` | Admin | Disable a user |
| `GET /api/v1/admin/activity` | Admin | All users' activity logs |
| `GET /api/v1/admin/sessions` | Admin | List active sessions |

## Server Architecture

- **Auth flow**: OAuth 2.0 + PKCE → Session cookie (Web UI) + JWT bearer (REST API / CLI only). Neither is accepted on `/mcp`; a user reaches tools only through an agent token they own.
- **Server types**: Shared (config file) + Personal (DB rows a user adds through `POST /api/v1/user/servers`). Every upstream connection is the process's single shared connection — there is no per-user connection, per-user workspace or per-user credential on the tool-call path.
- **Isolation**: REST listing scope (users see only shared + own personal servers), agent-token `allowed_servers` scope narrowed on every authentication, and user-scoped activity logs.
- **Admin**: Identified by `admin_emails` config. Sees all activity, manages users.
- **Build tag**: All server code behind `//go:build server`. Personal edition unaffected.

### Role freshness (issue #1169)

`admin_emails` is the single source of truth for the admin role, and **both**
auth paths re-derive it from the config on every request — the session path
always did, and the bearer-JWT path now does too. The `role` claim inside a JWT
is informational only; it is minted at login, never revoked, and must not be
trusted for authorization.

**An `admin_emails` edit takes effect on the next request — no restart.** The
config file is hot-reloadable (`config.LoadFromFile` unmarshals `server_edition`
in full, and it is not one of the restart-pinned fields), and the middleware
reads the role through a `ServerEditionConfigProvider` over the runtime's
current snapshot rather than a pointer captured at wiring time. It is the same
provider (`Dependencies.ConfigProvider`) the admin-servers check uses; do not
add a second mechanism, and do not capture `deps.Config.ServerEdition` in a new
surface that makes a role decision.

On both auth paths, once the file watcher has reloaded the edit:

- Removing someone from `admin_emails` demotes them on their next request, even
  while they hold an unexpired admin JWT, and they can no longer renew an admin
  token via `POST /api/v1/auth/token`. No re-login, no waiting for the JWT to
  expire, and no restart.
- Adding someone promotes them on their next request, without re-login.

Two limits worth stating exactly, because they are what the guarantee does
*not* cover:

- The edit is live from the moment the **reload lands**, not from the moment the
  file is saved. A malformed file is rejected and the previous config stays in
  force (`Config hot-reload failed; keeping previous configuration`), so confirm
  the reload before treating anyone as demoted.
- If a reload produces a config with **no `server_edition` block at all**, the
  provider falls back to the boot-time block rather than emptying the admin
  list. That is the conservative direction — it can only preserve the list the
  process started with, never widen it.

`AdminHandlers.adminEmails` is a separate, boot-time copy used **only** to
label rows in the dashboard response. It is not an access-control input (every
admin route gates on `requireAdmin` → `AuthContext.IsAdmin()`), so a stale label
there is cosmetic. Do not grow an authorization check on top of it.

### Agent tokens and tenant identity (issue #1168)

- Agent-token names are a **per-owner** namespace: two tenants can each hold a
  token called `ci`. Storage resolves by `(owner, name)`; the legacy
  `agent_token_names` index is kept only for ownerless (personal-edition)
  tokens, and there is no migration.
- `/api/v1/user/tokens/{name}` revoke, delete and regenerate answer an
  identical **404** for "does not exist" and "belongs to another tenant", and
  create no longer reports a conflict on another tenant's name. Do not
  reintroduce a "not yours" branch — it is a name-existence oracle.
- Those three do the lookup **inside the mutating transaction** and classify its
  error (`storage.ErrAgentTokenNotFound` → 404). Do not put a `Get…` preflight
  back in front: it opens a TOCTOU window on delete-then-recreate, and its
  fall-through 500 used to interpolate the storage sentinel into the body.
- The token cap answers **409** on both editions' surfaces
  (`ErrAgentTokenLimitReached`). One storage condition, one status. The **body
  differs by edition on purpose**: `auth.MaxTokens` is counted across the whole
  `agent_tokens` bucket, so in the server edition it is a *deployment-wide* cap
  a tenant may be unable to clear, and the message says so and points at an
  administrator. Do not copy the personal edition's "you have reached the
  maximum" wording here. A per-owner quota is issue #1177.
- **A token is only as live as its owner.** `storage.Manager.SetAgentTokenOwnerGate`
  is installed in `setup.go` over the user store, and `ValidateAgentToken`
  consults it for every *owned* token (ownerless personal-edition tokens are
  never gated). A disabled — or deleted — owner's tokens stop authenticating
  immediately, and the gate **fails closed**: a user store that cannot answer
  denies. `POST /api/v1/admin/users/{id}/disable` additionally **revokes** that
  user's tokens, so re-enabling the account does not resurrect a credential that
  may be why it was disabled; `enable` deliberately does not un-revoke, and
  **neither does regenerate** — rotating a revoked token answers `409`
  (`storage.ErrAgentTokenRevoked`) on both editions' surfaces, because rotation
  refreshes a live secret and is not an un-revoke by another name. Delete frees
  the name, so creating a fresh token is the supported path. An admin-facing
  surface for revoking a *specific* tenant's token is issue #1179.
- **The owner gate is installed FIRST in `setup.go`, before any fallible step.**
  `wireServerEditionOAuth` only *logs* `SetupAll`'s error — the process comes up
  serving traffic either way — so a control installed after `cfg.Validate()`,
  `EnsureBuckets()`, `GetOrCreateHMACKey()` or the credential store is simply
  absent whenever one of those fails, and agent tokens would then be ungated.
  Installed first it fails closed instead. Keep new fallible setup steps *below*
  it.
- `RegenerateAgentTokenForOwner`'s `narrowScope` hook can only ever narrow, and
  storage **enforces** that: the hook's return is intersected with the token's
  stored `AllowedServers` before it is persisted (a stored `"*"` counts as
  granting everything, so materialising it into a concrete list still works).
  The contract is not a comment a future caller can violate.
- An agent token's `AuthContext` carries its owner's `UserID` (so its activity
  is attributable) but stays at `AuthTypeAgent`. Per-user surfaces must gate on
  `IsUser()`, never on a non-empty `UserID`.
- The personal-edition admin surface (`/api/v1/tokens/{name}`) operates in the
  ownerless namespace and therefore no longer reaches server-edition users'
  tokens by name. Cross-tenant token administration belongs in
  `admin_handlers` as its own feature.

### Token server scope (`allowed_servers`)

`POST /api/v1/user/tokens` constrains the requested `allowed_servers` to what
the caller may actually reach. `AllowedServers` is the sole input to
`auth.AuthContext.CanAccessServer`, so persisting it verbatim let a tenant mint
themselves a token over the admin's whole inventory.

- **Entitlement is the per-user door's own predicate**: the caller's personal
  servers plus the admin servers flagged `shared`. `NewUserHandlers` receives
  the *whole* configuration, so the `Shared` filter is what keeps the admin's
  private servers out. Reuse `entitledServerNames`; do not write a second
  definition of entitlement.
- **Read the configuration LIVE, never a boot-time slice.** `NewUserHandlers`
  takes an `AdminServersProvider` (a function), wired in `setup.go` to
  `Dependencies.ConfigProvider` over the runtime's current config snapshot. The
  config is hot-reloadable: a handler built on `deps.Config.Servers` as captured
  at process start is blind to every server added afterwards, and a tenant could
  pre-create a personal server named like an admin server that only appears
  later and walk straight through the collision control below. Any new
  server-edition surface that makes an authorisation decision from configuration
  must read it through the provider.
- **An unentitled name is rejected (400), not silently dropped** — a token that
  quietly sees less than asked is worse than a refusal. The message is
  identical for "another tenant's server", "the admin's unshared server" and
  "no such server anywhere": a distinguishing message would be a server-name
  existence oracle, the same defect class as the token-name one above.
- **`"*"`** is the only wildcard the enforcement layer honours (`s == "*" ||
  s == name`, in both `auth` and `jsruntime`), so there are no globs to expand.
  For an admin it stays literal; for a tenant it is materialised into their
  entitled set at mint time, and refused outright when that set is empty. The
  expansion is a snapshot — a server added later needs a new token — and the
  create response echoes the effective list back.
- **An omitted `allowed_servers` stays empty**, which denies every server at the
  agent tier. Do not copy the personal edition's "empty means `["*"]`" default:
  there the only caller is the operator.
- **A personal server may not take a name used anywhere in the admin
  configuration**, shared or private. `AllowedServers` is compared by bare
  string, so "I own a server called X" and "I may reach the admin's X" are
  indistinguishable at enforcement time — a personal server named after an
  admin-private upstream was a way to mint a token over it.
  `entitledServerNames` also drops such a collision for a non-admin, so a row
  that predates the refusal (or an admin who later adds a colliding name) cannot
  resurrect the escalation. **`createServer` answers every unavailable name with
  one body** — `Server name %q is not available` — whether the holder is a
  shared admin server, a private admin server, or the caller's own existing
  server. Two different wordings let a tenant read, from one request, that a
  name they do not own is in the admin's configuration, and enumerate a private
  inventory they cannot list. The residual bit (a tenant can subtract their own
  inventory) closes only when enforcement resolves a server name against an
  owner rather than as a bare string.

#### Scope is stored at mint time and narrowed on every use

`resolveTokenServerScope` runs at mint time and writes the entitled list into
the token record. Since #1272 that stored list is **not the last word**: every
authentication of an *owned* token intersects the stored `allowed_servers` with
the owner's current entitlement (narrow-only, fail-closed), so un-sharing a
server or removing a personal one takes effect on the token's next request
without a revoke. The stored list is still worth keeping tight — it is the upper
bound the per-request narrowing starts from — and revoking or deleting the token
remains the way to withdraw *all* access at once.

Rotation persists the re-check: `POST /api/v1/user/tokens/{name}/regenerate`
re-runs the entitlement predicate and stores the **narrowed** list
(`narrowScopeToEntitled`), echoing it back in the response. It only ever narrows,
and it never rejects — refusing to rotate would leave the wider stored list in
place (harmless at use time, since narrowing happens per request, but
misleading when read back).

**Tokens minted with a literal `"*"` before this constraint existed.** The
enforcement layer honours `"*"` unconditionally, but a tenant-owned token no
longer reaches it with a star: the per-authentication narrowing above
materialises a tenant's `"*"` into their current entitled set on every request
(an administrator's star stays literal), and rotation persists that bounded
list. Such records still read as `"*"` in storage until rotated. There is no
migration and no admin-facing report: `GET /api/v1/user/tokens` is per-caller,
and the server edition has no cross-tenant token listing (that belongs in
`admin_handlers`, as its own feature). An operator who wants the stored lists
tidy audits them straight from storage — every record in the `agent_tokens`
bucket whose `allowed_servers` contains `"*"` and whose `user_id` is non-empty
— and asks the owner to rotate, or revokes them.

## Key Directories

| Directory | Purpose |
|-----------|---------|
| `cmd/mcpproxy/edition.go` | Default edition = "personal" |
| `cmd/mcpproxy/edition_teams.go` | Build-tagged override for server edition |
| `cmd/mcpproxy/serveredition_register.go` | Server feature registration entry point |
| `internal/serveredition/auth/` | OAuth, sessions, JWT tokens, middleware |
| `internal/serveredition/users/` | User/session models, BBolt store |
| `internal/serveredition/multiuser/` | Activity isolation (user-scoped activity queries). The per-user router, tool filter and workspace packages that once lived here had no production caller and were deleted in Spec 107. |
| `internal/serveredition/broker/` | Per-user `oauth_connect` credential store (stored, not injected — see [Auth Broker](../features/auth-broker.md)) |
| `internal/serveredition/api/` | Server REST API endpoints (user, admin, auth) |

## Server Testing

```bash
go test -tags server ./internal/serveredition/... -v -race  # All server unit + integration tests
go build -tags server ./cmd/mcpproxy                        # Build server edition
go build ./cmd/mcpproxy                                     # Verify personal edition unaffected
```

> Note: server-edition `//go:build server` routes are invisible to `swag` / `verify-oas-coverage.sh` / CI lint (which don't pass `--build-tags server`). Lint locally with the tag and document endpoints here.
