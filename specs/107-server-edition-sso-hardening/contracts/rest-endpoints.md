# REST contracts — new and changed routes

Server-edition routes are `//go:build server` and therefore invisible to `swag`, `oas/swagger.yaml`, `scripts/verify-oas-coverage.sh` (greps only `internal/httpapi/server.go`) and CI `verify-oas` — the OAS 3.1 snippets below are the hand-maintained contract, mirrored into `docs/development/server-edition-multiuser-auth.md`'s route table. Edition-neutral config changes (`audit_log`, `trusted_proxies`) do reach `oas/swagger.yaml` via `make swagger` (the `Config` schema). Status parity with an absent resource is the oracle for every hidden name (`project_entitlement_test_oracle`).

## 1. `GET /api/v1/auth/provider` — public edition probe (FR-030, PR-B)

```yaml
/api/v1/auth/provider:
  get:
    summary: Public login-button label and edition probe
    description: No authentication, no side effects (allocates no pending login state). Returns only an operator-chosen label. Personal build answers 404.
    responses:
      '200':
        content: { application/json: { schema: { type: object, additionalProperties: false, required: [display_name], properties: { display_name: { type: string, maxLength: 64 } } } } }
      '404': { description: personal edition, or server_edition.enabled=false }
```

Never returns issuer, client id, tenant id, scopes, domains or provider family.

## 2. `GET /api/v1/auth/me` — additive fields (FR-008, PR-B)

Existing body (`auth_endpoints.go:58-65`) gains `groups: string[]` (the caller's own stored groups; `[]` when none) and `groups_updated_at: string|null` (RFC 3339). Session cookie or user JWT as today.

## 3. `POST /api/v1/auth/token` — session-cookie principal only (FR-011, PR-C)

```yaml
/api/v1/auth/token:
  post:
    summary: Mint a user JWT from a live session
    security: [ { sessionCookie: [] } ]      # a Bearer user JWT or mcp_agt_ token → 401
    responses:
      '200': { description: '{ token, expires_at }' as today }
      '401': { description: no session cookie, or a derived credential was presented }
```

## 4. `GET /api/v1/admin/users` — additive fields (FR-008/FR-023, PR-B)

Each user object (`admin_handlers.go:146-154,183-195`) gains `groups: string[]`, `groups_updated_at: string|null`, `subject_rebind_armed_at: string|null`. `POST /api/v1/admin/users/{id}/enable` additionally sets `subject_rebind_armed_at` on a real `disabled: true → false` transition; `disable` clears it. Administrator session/JWT only, as today.

## 5. `GET /api/v1/user/activity` — wired (FR-002, PR-C)

```yaml
/api/v1/user/activity:
  get:
    summary: The caller's own activity records
    security: [ { sessionCookie: [] }, { userJWT: [] } ]   # agent tokens refused by the server-edition middleware as today
    parameters: [ { name: limit, in: query }, { name: offset, in: query } ]   # unchanged (user_activity.go:38-39); no new query parameters in this spec
    responses:
      '200':
        description: Records where user_id == principal.user_id AND server_name ∈ entitlement set — both terms evaluated inside storage.ActivityFilter.Matches (new authorization-only UserID field beside AllowedServers, activity_models.go:283-299), never by post-filtering a page, so total counts only what the page may contain; projected and masked through the exported httpapi.(*Server).ActivityProjector() func(*storage.ActivityRecord) contracts.ActivityRecord injected via serveredition.Dependencies.ProjectActivity (the door holds storage records, user_activity.go:108-132; the masker is a *Server method on the contract type, activity.go:304, and storageToContractActivity at :383 converts — the projector composes both so the JSON shape and masking equal core /activity); no include_bodies export.
        content: { application/json: { schema: { type: object, properties: { items: { type: array, items: { $ref: '#/components/schemas/ActivityRecord' } }, total: { type: integer } } } } }   # today's shape (user_activity.go:66-70), unchanged
```

An `admin_user` session receives **today's empty `{items:[],total:0}`** (unchanged — the door is wired with a nil filter at HEAD, `setup.go:196`, `user_activity.go:100-105`, and SC-006 lists no exception; administrators read history on core `/activity*`). The filter is consulted only for `user`-typed principals. A tenant never receives a record whose `user_id` is another user's or whose server is outside the entitlement (FR-043(k)).

## 6. `POST /api/v1/user/tokens`, `POST /api/v1/user/tokens/{name}/regenerate` — session-cookie only; `expires_in` capped (FR-011, PR-C)

| Rule | Value |
|---|---|
| Principal | session cookie only; bearer user JWT or agent token → 401 |
| `expires_in` | the core rule, shared through the exported `auth.ParseTokenExpiry` (`internal/auth/agent_token.go`; `httpapi.parseExpiry` at `tokens.go:460` is unexported and `internal/serveredition/api` cannot import `httpapi`, so the rule moves and `parseExpiry` becomes a wrapper): positive, ≤ `8760h` (365 days); `9000h` → 400 `expires_in must be at most 365 days` |
| Default expiry | 30 days (unchanged); an owned token always has `expires_at` |
| `allowed_servers` | ⊆ entitlement set; `"*"` materialised to the set for tenants (never stored literally), literal for `admin_user`; an unentitled name → 400 `server "x" is not available to you` (byte-identical to a nonexistent name) |
| Cap | per owner (FR-037): 101st for the same owner → 409 `you have reached the limit of 100 agent tokens; revoke one to continue` (never a fleet total) |

List (`GET /user/tokens`) and revoke keep accepting a bearer JWT (they extend nothing).

## 7. `GET /api/v1/user/servers*`, `/user/diagnostics`, `/user/credentials*` — entitlement predicate (FR-004, PR-C)

For a tenant: list doors return exactly the entitlement set (+ personal records); by-name doors (`/user/servers/{name}` get/update/delete/enable, credential connect/status/delete) answer for an unentitled or group-excluded name with the **same status and body** as for a nonexistent name (404). Comparison is exact, case-sensitive on the bare name. For an `admin_user` these doors render today's shared (+ personal) projection unchanged.

## 8. Core `/api/v1` — tenant-session allowlist (FR-001/FR-002, PR-C)

Credential precedence in `apiKeyAuthMiddleware` (one source evaluated; a failing present credential is terminal 401; **presence** is header/query *membership* — an empty `X-API-Key:`, `Authorization: Bearer ` or `?apikey=` is present-and-failing, never absent, unlike today's `ExtractToken` which collapses empty to absent, `server.go:592-615`): socket → `X-API-Key` → `Authorization: Bearer` (`mcp_agt_` = agent token; else global key; else user JWT via the edition hook) → `?apikey=` → `mcpproxy_session` cookie **only when none of the three headers/params is present**.

Tenant (`user`-typed) principal — allowed, then filtered by the existing scope predicates:

| Method | Route | Filter |
|---|---|---|
| GET | `/api/v1/status` | `CanEnumerateServer` |
| GET | `/api/v1/servers` | `visibleServers` |
| GET | `/api/v1/servers/{id}/**` except `/servers/{id}/tool-calls` **and except the static `/api/v1/servers/import/paths`** (`server.go:788` — host filesystem paths, not a server subtree; the matcher denies it explicitly *before* the `{id}` rule because a raw-path matcher cannot tell `import` from a server id) | `scopedServerSubtree` (404 parity) |
| GET | `/api/v1/tools` | scoped |
| GET | `/api/v1/index/search` | scoped **before the ranked cut**: `SearchToolsScoped(query, limit, canSeeServer)` (T075a — same query and scores, exhaustive `From`/`Size` paging with no result cap, filter then cut) so a hidden high-ranker can never displace an entitled hit and `total` counts only entitled hits; `total` (= `min(limit, entitled matches)`; matching is boolean, so corpus-independent) is byte-identical across the two fixtures, and so is membership whenever the entitled match count ≤ `limit` (every fixture case, US4.2); with more entitled matches than `limit`, top-K membership, scores and ordering are asserted against the per-fixture derivation only (Spec 105 retrieve oracle: corpus-dependent scores can reorder entitled hits across the cut); the #1166 post-filter for agent tokens is replaced by the same call |
| POST | `/api/v1/preflight` | `ResolveScope` with `Restricted=true` (FR-006) |
| GET | `/api/v1/profiles`, `/api/v1/profiles/active` (the only profile reads that exist, `server.go:771-772`; there is no `/profiles/{slug}` route) | **tenant projection**: omit every profile whose effective server set ∩ entitlement is empty; `GET /profiles/active` → `""` when the global active profile would be omitted |
| GET/HEAD | `/events` | `eventVisibleToCaller`; principal re-resolved per frame (FR-005) |

**Every other method+route** under `/api/v1` (including routes added later) → `403` with the existing scoped-caller body, emitted before the handler (so before any body parse; a malformed body is 403, not 400). Named in the spec: `/tools/call` (every tool name, built-ins included), `/code/exec`, `/tool-calls/{id}/replay`, `/config` (GET/PATCH), `/config/apply`, `/servers` add/remove/enable, `/quarantine/*`, `/tokens*` (personal routes), `/secrets*`, `/sessions`, `/registries*` incl. `/refresh`, `/telemetry/*`, `/onboarding/*`, `/feedback`, `/connect*`, `/code/scripts`, `/diagnostics`, `/doctor`, `/annotations/coverage`, `/info`, `/routing`, `/docker/status`, `/stats/tokens`, `/security/*`, `/activity*`, `/tool-calls`, `/tool-calls/{id}`, `/servers/{id}/tool-calls`.

`admin_user` session principal: administrator everywhere on core REST except `CanRevealSecrets` (raw secret reveal stays API-key/socket-only; `GET /config?reveal=…` and `/info`'s `web_ui_url` return masked values).

The fixed 403 body (existing scoped-caller shape): `{"error":"forbidden","message":"this credential is not permitted to access this resource","request_id":"…"}` — identical for every refused route.

## 9. `/mcp*` — unchanged credential set; forced auth under the server edition (FR-003/FR-029, PR-B)

Accepts exactly `mcp_agt_` agent tokens, the global API key (`X-API-Key` / `Bearer` / `?apikey=`) and the socket. With `server_edition.enabled: true`, no credential → 401 and any unrecognised bearer (session cookie, user JWT) → 401 regardless of `require_mcp_auth`. Body unchanged (`{"error":"Authentication required…"}`); no `WWW-Authenticate` (Spec 089 scope).

## 10. Login/logout (FR-024/FR-028, PR-B)

- `GET /api/v1/auth/login?redirect_uri=<path>`: `redirect_uri` accepted only as a same-origin path (single leading `/`, not `//` or `/\`, no scheme/host/backslash/CR/LF/control — checked on the percent-decoded value `r.URL.Query().Get` returns, so `%2F%2F`, `%5C`, `%0D%0A`, `%00` are caught too); otherwise replaced by `/ui/` silently. Boot: with `public_url` set, setup logs one INFO line with the resolved public URL and callback URL and makes no IdP request (US2.1). Response: 302 to the IdP with `state`, `nonce`, PKCE S256, `redirect_uri=<public_url>/api/v1/auth/callback`.
- `GET /api/v1/auth/callback`: success → 302 to the stored path + `Set-Cookie: mcpproxy_session=…; Path=/; HttpOnly; SameSite=Lax[; Secure]`; an IdP authorization-response error (`?error=access_denied&state=…` or any RFC 6749 §4.1.2.1 code) consumes the state first and is a refusal with reason `authorization_denied` (the IdP's `error_description` is never rendered — today a distinct 400 `missing code parameter` is returned before the state is touched, `oauth_handler.go:163-167`); refusal → one generic page `Sign-in was not permitted (ref <request id>)` with status **403** for every refusal reason; unavailability (`discovery_failed`, `provider_error`, `internal_error`) → **503** `Sign-in is temporarily unavailable (ref <request id>)`. The reason reaches only the server log and the `auth_event` line keyed by `<request id>`.
- `POST /api/v1/auth/logout`: unchanged; writes one `auth_event` (`surface: logout`).

## 11. Not a surface (FR-016)

There is no REST, SSE, MCP, CLI or Web UI route that reads or lists audit lines. `GET /api/v1/activity/export` is unchanged.

## 12. `mcpproxy credential list|status` (FR-034, PR-A)

Output begins with the line `Stored credentials are kept for a future broker and are NOT injected into upstream calls in this release.`; the REST status vocabulary (`connected|expired|not_connected|unavailable`) is unchanged.
