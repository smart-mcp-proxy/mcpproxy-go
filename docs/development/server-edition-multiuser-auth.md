---
title: "Server Multi-User Authentication"
sidebar_label: "Server Multi-User Auth"
description: "SSO multi-user authentication for the server edition: generic OIDC (Keycloak, Okta, Auth0, Authentik, Entra) with JWKS-verified ID tokens, plus the Google, GitHub and Microsoft legacy providers, behind a TLS-terminating ingress."
---

# Server Multi-User Authentication (Spec 024, hardened by Spec 107)

Server edition supports SSO multi-user authentication with four identity
providers: the generic **`oidc`** provider (any OpenID Connect Discovery issuer —
Keycloak, Okta, Auth0, Authentik, Microsoft Entra ID — with a JWKS-verified ID
token and a groups claim) and the three legacy providers **`google`**,
**`github`** and **`microsoft`**. All server code is behind `//go:build server`;
the personal edition is unaffected. The complete key table with defaults,
validation text and reload behaviour is
[Server Edition](../configuration/config-file.md#server-edition); this page is
the developer view.

## Server Configuration

```json
{
  "listen": "0.0.0.0:8080",
  "trusted_proxies": ["10.42.0.0/16"],
  "server_edition": {
    "enabled": true,
    "admin_emails": ["admin@company.com"],
    "public_url": "https://mcp.company.com",
    "session_cookie_secure": "auto",
    "oauth": {
      "provider": "oidc",
      "issuer_url": "https://login.company.com/realms/team",
      "client_id": "mcpproxy",
      "client_secret": "${env:OIDC_CLIENT_SECRET}",
      "scopes": ["openid", "profile", "email", "groups"],
      "groups_claim": "groups",
      "email_verified_policy": "refuse_false",
      "display_name": "Company SSO",
      "allowed_domains": ["company.com"]
    },
    "session_ttl": "24h",
    "bearer_token_ttl": "24h"
  }
}
```

A legacy provider needs only `provider`, `client_id`, `client_secret` (and
`tenant_id` for `microsoft`, default `common`); the six `oidc` keys are ignored
for it. `oauth.provider` is validated in one place
(`internal/config/server_edition_config.go`, `Validate`); the Web UI select in
`frontend/src/views/settings/fields.ts` and `users.User.Validate` must change
in lockstep with it.

### The `oidc` provider (`internal/serveredition/auth/oidc_provider.go`)

- **Discovery is lazy.** Nothing is fetched at boot or for readiness: the first
  `GET /api/v1/auth/login` reads `<issuer_url>/.well-known/openid-configuration`,
  checks the document's `issuer` equals the configured value **byte for byte**
  (a trailing slash is a mismatch → `discovery_failed`, both values logged),
  and caches it with a bounded TTL (cache headers respected, clamped to
  5 min – 24 h). Every discovered endpoint (`authorization_endpoint`,
  `token_endpoint`, `jwks_uri` required; `userinfo_endpoint` optional) must be
  absolute `https`; plain `http` is admitted only for a loopback host together
  with `allow_insecure_issuer: true` (`config.IsAllowedOIDCEndpoint` — the same
  rule gates the configured issuer). A violating document is rejected before
  any redirect or any request carrying the client secret.
- **The back channel never follows redirects** and has a 10 s timeout. A 3xx
  from discovery, JWKS, the token endpoint or userinfo is `provider_error`, so
  a misconfigured or compromised IdP cannot downgrade the code or the client
  secret to another origin.
- **Client authentication is chosen once, before the single exchange**, from
  `token_endpoint_auth_methods_supported`: `client_secret_basic` when
  advertised (also the default when the key is absent), else
  `client_secret_post`, else `discovery_failed`. An authorization code is
  single-use, so there is no retry with a second method. PKCE S256 and a
  per-login `nonce` are always sent; `openid` is appended to `scopes` if missing.
- **The ID token is verified before any claim is read**
  (`oidc_jwks.go` parses RSA/EC JWKs with the standard library and
  `golang-jwt/jwt/v5` verifies): signature by `kid` against the cached JWKS
  (an unknown `kid` triggers exactly one JWKS refetch per login), algorithm
  restricted to RS256/RS384/RS512/PS256/PS384/PS512/ES256/ES384/ES512 (never
  `none`, never HS*), `exp`/`nbf`/`iat` with 60 s skew, exact `iss`, `aud`
  containing the client id (with `azp` required and checked when there are
  several audiences), and the nonce stored beside the pending state and
  consumed once. The unverified `parseIDToken` survives only for the three
  legacy providers and is unreachable for `oidc`.
- **Claims.** `sub` and `email` are required (`email_missing`);
  `email_verified` is applied per `email_verified_policy`; groups are read from
  the ID token's `groups_claim` (a flat string array or a single string), then
  from `userinfo` once — and no userinfo claim is used before its `sub` is
  compared with the verified token's `sub` (`userinfo_subject_mismatch`). A
  failed userinfo fetch is `provider_error` (503, store untouched, stored
  groups **not** reset); a claim absent from both, or an Entra overage marker
  (`_claim_names`), stores `[]` and logs a warning naming the user id and the
  claim looked for — never the token.
- **Pending login state** is in-process, 10-minute TTL, capped at 10,000
  entries with the oldest evicted on insert. A second replica is unsupported.
- **Never log tokens or the client secret.** Log lines name the issuer, the
  failed check and the error class only.

### Subject binding and login refusals (every provider)

- A user record stores `(provider, provider_subject_id)` and refreshes both on
  every successful login. Same provider + same normalised email + different
  `sub` is refused (`subject_mismatch`); a configured-provider change re-binds
  on the first login and flags the attempt `provider_rebound`. Email stays the
  lookup key (no store migration). An administrator re-arms the binding for a
  genuinely re-created IdP account through `disable` → `enable`: the enable
  transition sets `subject_rebind_armed_at`, the next successful login consumes
  it (single-use, persisted across restarts, cleared atomically in the same
  store write); a failed attempt does not consume it.
- Every denial renders **one generic page** — `Sign-in was not permitted (ref
  <request id>)` with the same status for every reason. The closed
  `LoginRefusal` enum (`oauth_handler.go`: `authorization_denied`,
  `state_invalid`, `id_token_invalid`, `issuer_mismatch`, `audience_mismatch`,
  `token_expired`, `nonce_mismatch`, `email_missing`, `email_unverified`,
  `domain_not_allowed`, `subject_mismatch`, `userinfo_subject_mismatch`,
  `user_disabled`) reaches only the server log,
  keyed by that request id. The one distinct class is unavailability —
  `discovery_failed`, `provider_error` and the post-verification
  `internal_error` — rendered as `503 Sign-in is temporarily unavailable (ref
  <id>)`; the proxy stays up, readiness is unaffected and the next login
  retries. A failure that is not the user's fault is never rendered as "not
  permitted".
- `redirect_uri` on `GET /api/v1/auth/login` is accepted only as a same-origin
  path (single leading `/`, no `//`, `/\`, scheme, host, backslash or control
  character); anything else becomes `/ui/` silently and flags the attempt
  `redirect_rejected`. The Web UI passes `window.location.pathname`.

### Front door behind an ingress

The published image listens on `0.0.0.0:8080` behind a TLS-terminating ingress.
Three keys make the SSO door safe there; `trusted_hosts` is **not** one of them
(it is DNS-rebinding protection for loopback listeners and never runs on a
non-loopback one).

| Key | Role | Code |
|-----|------|------|
| `server_edition.public_url` (env `MCPPROXY_PUBLIC_URL`) | When set, the sole source of the IdP `redirect_uri` (`<public_url>/api/v1/auth/callback`), of the connect-flow base URL and of the scheme behind the `Secure` cookie decision; `Host` and `X-Forwarded-*` are ignored for those. Unset on a non-loopback listener → boot warning + `doctor` finding (never an error: every existing container deployment must keep booting). When set to https but the callback arrives over http, one warning with the request id is logged and login proceeds | `buildCallbackURL` (`oauth_handler.go`), `connector_provider.go`, `internal/config/env_serveredition.go` |
| `trusted_proxies` (top-level, edition-neutral, env `MCPPROXY_TRUSTED_PROXIES`, **live**) | The only gate on `X-Forwarded-For` / `X-Real-IP` / `X-Forwarded-Proto` / `X-Forwarded-Host`: honoured only when `RemoteAddr` is inside the list, client IP = right-most untrusted hop. Default empty = trust nobody. Every reader evaluates a `func() []string` provider per request — never a slice captured at construction | `config.ForwardedHeaders` (`internal/config/trusted_proxies.go`) — the one reader; consumers: session store, callback, connector base URL, swagger, `httpapi.tagRequestMeta` |
| `server_edition.session_cookie_secure` | `auto` (default: `Secure` when the effective scheme is https — `public_url`, in-process TLS, or a **trusted** `X-Forwarded-Proto: https`), `true`, `false`. `false` × (https `public_url` or `tls.enabled`) is refused at boot, PATCH and apply (`validateServerEditionConfig`, the `*Config`-level bridge that can see `tls`); an explicit `false` elsewhere is honoured with a warning + `doctor` finding | `NewSessionManager(..., securePolicy)`, `setup.go` |

Forced MCP auth: when `server_edition.enabled` is `true`,
`config.EffectiveRequireMCPAuth` makes `mcpAuthMiddleware` behave as if
`require_mcp_auth` were `true` — no credential → 401, a session cookie or user
JWT → 401; agent tokens, the API key and the socket are unchanged. An explicit
`false` is not a validation error: boot logs `require_mcp_auth: false is
overridden to true because server_edition.enabled is true` and `mcpproxy
doctor` names the override. The accessors live in
`internal/config/serveredition_accessors.go` (+ `_stub.go` for the personal
build) so `internal/server` stays edition-neutral.

`mcpproxy doctor` renders these findings from `config.DoctorFindings(cfg)`
(`internal/config/doctor_findings*.go`), registered as a runtime-warning source
on the management service; there is no producer in `cmd/mcpproxy`.

Reload semantics: `enabled`, `oauth.*`, `public_url`, `session_cookie_secure`,
`session_ttl`, `bearer_token_ttl` and `credential_encryption_key` are bound at
setup and reported as `server_edition` with `RequiresRestart=true`
(`server_edition settings are bound at startup`); `admin_emails` is live
(`server_edition.admin_emails`); `trusted_proxies` is live.

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
| `GET /api/v1/auth/provider` | Public | Edition + label probe: returns only `{"display_name": "..."}` (`oauth.display_name`, falling back to the provider family name), no side effects. Never the issuer, client id, tenant, scopes or domains. Registered only when the block is enabled; the personal build answers 404 — the Web UI uses that to detect the edition before any authenticated call |
| `GET /api/v1/auth/login` | Public | Initiate OAuth login flow (PKCE S256, `state`, `nonce`; `?redirect_uri=` must be a same-origin path) |
| `GET /api/v1/auth/callback` | Public | OAuth callback (verifies the ID token for `oidc`, creates session; one generic refusal page) |
| `GET /api/v1/auth/me` | Session/JWT | Get current user profile |
| `POST /api/v1/auth/token` | **Session only** | Mint a user JWT for the REST API and CLI (`/api/v1/user/*`); a JWT is **never** an MCP credential — `/mcp` accepts only agent tokens, the API key and the socket |
| `POST /api/v1/auth/logout` | Session | Invalidate session |
| `GET /api/v1/user/servers` | Session/JWT | List user's servers (personal + shared, filtered by the [group entitlement](#group-access-map-server_editionaccess-and-entitlement-spec-107-pr-c) when `access` is configured) |
| `POST /api/v1/user/servers` | Session/JWT | Add personal upstream server |
| `GET /api/v1/user/activity` | Session/JWT | User's activity log |
| `GET /api/v1/user/diagnostics` | Session/JWT | Server health for user's servers |
| `GET /api/v1/user/tokens` | Session/JWT | List the caller's own agent tokens |
| `POST /api/v1/user/tokens` | **Session only** | Mint an agent token owned by the caller; `allowed_servers` narrowed to the entitlement set, `expires_in` capped at 365 days |
| `POST /api/v1/user/tokens/{name}/regenerate` | **Session only** | Rotate an owned token's secret, re-narrowing `allowed_servers` to the current entitlement |
| `DELETE /api/v1/user/tokens/{name}` | Session/JWT | Revoke/delete an owned token; unlike mint and rotate, revoke keeps accepting a bearer JWT (it extends nothing) |
| `GET /api/v1/admin/users` | Admin | List all users (now includes `groups`, `groups_updated_at`, `subject_rebind_armed_at`) |
| `POST /api/v1/admin/users/{id}/disable` | Admin | Disable a user (revokes their sessions and owned tokens) |
| `POST /api/v1/admin/users/{id}/enable` | Admin | Re-enable a disabled user; on a real `disabled: true → false` transition, arms a single-use subject rebind (`subject_rebind_armed_at`) consumed by the user's next successful login |
| `GET /api/v1/admin/activity` | Admin | All users' activity logs |
| `GET /api/v1/admin/sessions` | Admin | List active sessions |

**"Session only"** means the session cookie exclusively: a bearer user JWT (or an agent token) presented to `POST /auth/token`, `POST /user/tokens` or `POST /user/tokens/{name}/regenerate` is refused with `401` — a derived credential never mints or renews another credential (FR-011). This closes what was previously an indefinite chain: a JWT could renew itself forever through `/auth/token`, and a JWT minted in a session's last second could still mint a 30-day agent token through `/user/tokens` in its own last second. See [Freshness bound](#freshness-bound-and-session-cookie-only-minting-doors-fr-011) below for the resulting staleness guarantee.

## Server Architecture

- **Auth flow**: OAuth 2.0 + PKCE (+ nonce and a JWKS-verified ID token for `oidc`) → Session cookie (`HttpOnly; SameSite=Lax`; `Secure` per `session_cookie_secure`) for the Web UI + JWT bearer (REST API / CLI only). Neither is accepted on `/mcp`; a user reaches tools only through an agent token they own.
- **Server types**: Shared (config file) + Personal (DB rows a user adds through `POST /api/v1/user/servers`). Every upstream connection is the process's single shared connection — there is no per-user connection, per-user workspace or per-user credential on the tool-call path.
- **Isolation**: REST listing scope (users see only shared + own personal servers, further narrowed by the group entitlement below when `server_edition.access` is configured), agent-token `allowed_servers` scope narrowed on every authentication, a tenant session/JWT principal restricted to an explicit allowlist of core REST routes and filtered by the same entitlement (Spec 107 PR-C, [below](#tenant-session-principal-on-core-rest-spec-107-pr-c)), and user-scoped activity logs.
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
- The token cap answers **409** on both editions' surfaces. The deployment-wide
  `auth.MaxTokens` bound counts every stored record, including revoked records,
  and its server-edition message points at an administrator. Server edition also
  enforces `auth.MaxTokensPerOwner` for non-empty owners, preventing one tenant
  from exhausting the shared pool. That owner-specific message tells the caller
  to permanently delete an unused token; soft revocation deliberately does not
  free storage or quota. Ownerless personal-edition tokens retain the original
  deployment-only limit.
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

### Freshness bound and session-cookie-only minting doors (FR-011)

Groups (and admin-role membership) refresh only at login — there is no
background IdP re-query. That makes staleness a real, documented bound rather
than "however long the IdP takes to notice":

```
bound = session_ttl + max(bearer_token_ttl, longest owned agent-token expiry ≤ 365 days)
```

A live session or a bearer JWT is narrowed on its **next request**, and every
owned agent token on its **next authentication** — both *before* the holder
re-logs in, because the scope resolver reads the stored `User.Groups` /
entitlement set fresh on every request rather than trusting anything cached in
the JWT. `admin_emails` users are exempt from the group map, so this bound is
about tenants; the administrator role itself already re-derives from
`admin_emails` on every request ([Role freshness](#role-freshness-issue-1169)).

The bound only holds because the three credential-minting doors —
`POST /auth/token`, `POST /user/tokens`, `POST /user/tokens/{name}/regenerate`
— accept a session cookie only (previous section): without that, a JWT or
token minted in a session's last second could keep re-minting itself past the
session's expiry, making the bound unbounded. `expires_in` on `/user/tokens` is
capped at 365 days by the same `auth.ParseTokenExpiry` rule as core
`/api/v1/tokens` (`9000h` → `400 expires_in must be at most 365 days`).

An administrator `disable` is the immediate remedy — it is not subject to this
bound because it revokes the user's sessions and owned tokens directly through
the owner gate ([Agent tokens and tenant identity](#agent-tokens-and-tenant-identity-issue-1168)),
rather than waiting for staleness to expire.

### Group access map (`server_edition.access`) and entitlement (Spec 107 PR-C)

`server_edition.access` (FR-006/FR-007/FR-009) turns "the IdP put someone in a
group" into server entitlement. It is **absent by default** — today's
Shared-only semantics, unchanged — and becomes **active** the moment the block
is present, with no silent allow-all: `"*"` is the only way to grant every
shared server, and a user whose stored groups match no key and who has no
`default_servers` grant is entitled to **no shared server at all**
(deny-all for non-administrators). See the [config reference](../configuration/config-file.md#server-edition)
for the key table and the per-IdP groups-claim table.

```
grant(u)     = ⋃ access.group_servers[g] for g ∈ u.Groups  ∪  (access.default_servers if no g matches any key)
               where "*" expands to every shared server
entitled(u)  = personal(u) ∪ { s ∈ shared : access == nil ∨ s ∈ grant(u) }
```

`entitledServerNamesFor` (`internal/serveredition/api/user_handlers.go`) is the
**one** function every tenant-facing door consults — REST listing/by-name
doors, the owned-token narrowing on every authentication, and the tenant
session principal below. Administrators keep today's whole-configuration view
(SC-006 parity); the "*" literal survives only for them. A group-excluded
server is indistinguishable from a nonexistent one on every surface (FR-010)
except the still-open Spec 105 items named in [Agent Tokens](../features/agent-tokens.md#server-edition-incident-response).

#### Upgrade-state table

What happens to an existing deployment the moment an operator adds an `access`
block to a config that previously had none:

| State | Outcome |
|-------|---------|
| A pre-upgrade user record (no stored groups) | Decodes with `groups == nil`, which matches no `group_servers` key — the user falls straight to `default_servers` (or deny-all if that is empty too) |
| A live session or bearer JWT the user is already holding | Narrowed to the new (default) grant on its **very next request** — no wait for expiry, no re-login required to lose access |
| Every agent token that user owns | Narrowed to the new grant on its **next authentication** — same immediacy, independent of the request path above |
| An `admin_emails` user | **Unaffected** — administrators are exempt from the group map by construction (FR-009) |
| Groups themselves (as opposed to the grant computed from them) | Refresh only at the user's **next login** — enabling `access` narrows immediately using whatever groups are already stored; it does not requery the IdP |
| Provider changed for the same email (e.g. migrating IdPs) | Automatic rebind on the first login with the new provider, flagged `provider_rebound` |
| Same provider, but the IdP-side subject (`sub`) changed (e.g. account re-created) | Refused as `subject_mismatch` until an administrator runs `disable` (which revokes the user's sessions and tokens) → `enable` (which arms a single-use `subject_rebind_armed_at`) → the user's next successful login consumes it |

### Tenant session principal on core REST (Spec 107 PR-C)

Before this spec, a session cookie or user JWT was accepted only on
`/api/v1/auth/*`, `/api/v1/user/*` and `/api/v1/admin/*`. PR-C additionally
accepts a `user`-typed session/JWT principal on **core** `/api/v1` and `/events`
(the routes the Web UI and CLI otherwise reach only with the admin API key),
gated by a fixed allowlist and the entitlement above:

| Method | Route | Filter |
|---|---|---|
| GET | `/api/v1/status` | `CanEnumerateServer` |
| GET | `/api/v1/servers` | `visibleServers` |
| GET | `/api/v1/servers/{id}/**` (except `/tool-calls` and the static `/servers/import/paths`) | `scopedServerSubtree` (404 parity with a nonexistent server) |
| GET | `/api/v1/tools`, `/api/v1/index/search` | scoped (search is filtered *before* the ranked cut, so a hidden high-ranker can never displace an entitled hit) |
| POST | `/api/v1/preflight` | `ResolveScope` with `Restricted=true` |
| GET | `/api/v1/profiles`, `/api/v1/profiles/active` | tenant projection — profiles with an empty entitled intersection are omitted |
| GET/HEAD | `/events` | `eventVisibleToCaller`; the principal is re-resolved before every frame |

**Every other method+route** under `/api/v1` — `/tools/call`, `/code/exec`,
`/config`, `/servers` add/remove/enable, `/quarantine/*`, `/tokens*`,
`/secrets*`, `/sessions`, `/registries*`, `/telemetry/*`, `/activity*` and
everything else, including routes added later — answers `403` before the
handler runs (so before any body parse), with the same fixed body core REST
already used for a scoped caller:
`{"error":"forbidden","message":"this credential is not permitted to access this resource","request_id":"…"}`.

An `admin_user` session or JWT is an administrator everywhere on core REST
except `CanRevealSecrets`, which stays API-key/socket-only — `GET
/config?reveal=…` and `/info`'s `web_ui_url` return masked values even for an
administrator's session. This is the one place a session/JWT principal is
deliberately weaker than the API key, and it is why the Web UI login alone is
enough for a tenant to use the Configuration and Servers pages, but not enough
to read a raw secret.

## Key Directories

| Directory | Purpose |
|-----------|---------|
| `cmd/mcpproxy/edition.go` | Default edition = "personal" |
| `cmd/mcpproxy/edition_teams.go` | Build-tagged override for server edition |
| `cmd/mcpproxy/serveredition_register.go` | Server feature registration entry point |
| `internal/serveredition/auth/` | OAuth (legacy providers), `oidc_provider.go` + `oidc_jwks.go` (discovery, JWKS, verified ID token), sessions, JWT tokens, middleware, `login_pages.go` (generic refusal / unavailability pages) |
| `internal/config/{server_edition_config,trusted_proxies,serveredition_accessors,env_serveredition,doctor_findings*}.go` | Block schema + validation, `ForwardedHeaders`, edition accessors (`EffectiveRequireMCPAuth`), `MCPPROXY_PUBLIC_URL`, doctor findings |
| `tests/oauthserver/` (`-oidc`) | In-process fake OpenID Provider with discovery, JWKS, userinfo, per-user claims and a tamper matrix (bad signature, wrong `iss`/`aud`, expired, wrong nonce, `alg: none`, HS256, `email_verified: false`, `http` endpoints, redirecting token endpoint) |
| `internal/serveredition/users/` | User/session models, BBolt store |
| `internal/serveredition/multiuser/` | Activity isolation (user-scoped activity queries). The per-user router, tool filter and workspace packages that once lived here had no production caller and were deleted in Spec 107. |
| `internal/serveredition/broker/` | Per-user `oauth_connect` credential store (stored, not injected — see [Auth Broker](../features/auth-broker.md)) |
| `internal/serveredition/api/` | Server REST API endpoints (user, admin, auth) |

## Server Testing

```bash
go test -tags server ./internal/serveredition/... -v -race  # All server unit + integration tests
go build -tags server -o mcpproxy-server ./cmd/mcpproxy     # Build server edition (always -o: a bare build overwrites ./mcpproxy)
go build ./cmd/mcpproxy                                     # Verify personal edition unaffected
```

### Local rig: `scripts/dev-server-edition.sh`

The Spec 107 verification rig runs the whole SSO path against a fake OpenID
Provider, loopback-only, in a scratch directory — it never touches
`~/.mcpproxy`, the tray's core or a real IdP. It is the executable form of
`specs/107-server-edition-sso-hardening/quickstart.md`; the script is the
source of truth and that page is its narrative.

```bash
scripts/dev-server-edition.sh                        # phase b: build, fake IdP, boot, headless login, /auth/me
scripts/dev-server-edition.sh --phase c              # + tenant principal on core REST (PR-C)
scripts/dev-server-edition.sh --phase d --keep       # + token mint, /mcp gate, audit tail (PR-D); keep the scratch dir
scripts/dev-server-edition.sh --idp-args "-token-error bad-signature"   # one US2 tamper case: expects a 403, no session
```

What it does, in order: builds `mcpproxy-server` (`-tags server -o`, never
bare) and the fake IdP (`tests/oauthserver/cmd/server -oidc`) into the scratch
dir; runs `npm ci --prefix tests/echo-rugpull-server` once for the stdio
fixture (`node tests/echo-rugpull-server/index.js`, a deterministic `echo`
tool while `DESC_FILE` is unset); writes `mcp_config.json` with
`server_edition.oauth.provider: "oidc"` pointing at the IdP through
`${env:OIDC_CLIENT_ID}` / `${env:OIDC_CLIENT_SECRET}`; boots the server
edition with **both** `--config` and `--data-dir` on a free `18xxx` port and
waits for `/readyz` + `/api/v1/status` (`edition: server`); performs the
headless login as `alice@example.com` (302 to the IdP with `S256` + `nonce`,
POST the login form without following redirects, then GET the callback) and
prints `/api/v1/auth/me`; repeats the login with
`redirect_uri=https://evil.example/` and asserts the 302 lands on `/ui/`.

Rules it encodes: every wait loop is bounded and every `curl` carries
`--max-time`, so a missing piece (an older branch without the `oidc` provider
answers exit 4 at config load) is reported with the reason, never hung on;
teardown kills only the PIDs it started (never `pkill` by name) and removes
the scratch dir only when it created it via `mktemp` (`--scratch DIR` and
`MCPPROXY_RIG_SCRATCH` are always kept, as is any failed run). Gates for the
script itself: `bash -n scripts/dev-server-edition.sh` and `shellcheck
scripts/dev-server-edition.sh`.

> Note: server-edition `//go:build server` routes are invisible to `swag` / `verify-oas-coverage.sh` (which don't pass `--build-tags server`), so document endpoints here. CI lints twice — bare and with `--build-tags server` — and race-tests `internal/server`, `internal/httpapi` and `internal/storage` under the tag (Spec 107 FR-047); run both lint passes locally before pushing (see the Lint block in `CLAUDE.md`).
