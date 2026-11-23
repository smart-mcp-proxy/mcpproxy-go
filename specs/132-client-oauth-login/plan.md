# Implementation Plan: Browser login for MCP clients (opt-in OAuth)

**Branch**: `claude/oauth-api-key-client-role-eb4179` (spec) | **Date**: 2026-11-22 | **Spec**: [spec.md](./spec.md)
**Input**: [spec.md](./spec.md) and the approved, cross-reviewed [design.md](./design.md)

## Summary

MCPProxy becomes an optional OAuth 2.1 authorization server for its own MCP endpoint. A client given only `/mcp/oauth/<client-id>` (or the generic `/mcp/oauth`) registers itself, the administrator approves on a consent page, and the client gets short-lived tokens. The address fixes which client it is; the reported name decides nothing (on the generic address it may pre-select a client on the consent page, which the administrator confirms or changes). A browser login is one more credential under an existing Clients hub row and uses that client's single profile binding. The feature is off by default and, until first used, leaves every existing answer unchanged.

Technical approach: one new stdlib-only leaf package for the protocol logic, lazy bbolt buckets in `internal/storage`, one runtime service next to `ClientsService`, HTTP handlers registered once and gated per request, a small extension of `mcpAuthMiddleware`, then the UI and Connect surfaces.

## Technical Context

**Language/Version**: Go 1.26 (backend), TypeScript 5.9 / Vue 3.5 (Web UI), Swift 5.9 (macOS tray)
**Primary Dependencies**: standard library only for the new code (`crypto/hmac`, `crypto/sha256`, `crypto/rand`, `net/http`, `net/netip`); existing chi router, bbolt, zap. **No new dependencies.**
**Storage**: bbolt `config.db`. New lazy buckets `oauth_registrations`, `oauth_grants`, `oauth_client_bindings`; marker `oauth_login_ever_used` in `MetaBucket`. Authorization requests and codes are memory only.
**Testing**: `go test -race` unit and integration tests, `scripts/test-client-oauth-e2e.sh`, vitest for the consent page and Clients hub, Playwright sweep for touched views, the macOS tray via `mcpproxy-ui-test`.
**Target Platform**: personal edition on macOS, Windows, Linux. Server edition answers 404.
**Project Type**: web application (Go backend + Vue frontend + native tray)
**Performance Goals**: access-token validation adds one HMAC and one bbolt read per MCP request, the same cost as a client credential today.
**Constraints**: default-off equivalence with the previous release (FR-002); loopback only (FR-010); no secret in client configs (SC-003); downgrade never widens an API-key client (FR-028).
**Scale/Scope**: tens of clients, a few logins each; at most 50 pending registrations.

## Constitution Check

| Principle | Status | Note |
|---|---|---|
| I. Performance at Scale | Pass | No change to search, indexing or routing. Token check is O(1). |
| II. Actor-Based Concurrency | Pass with note | Writes go through the existing `bindingWriteMu` that `ClientsService` and `CredentialsService` already share. The in-memory request/code store uses one mutex (short critical sections, no I/O). See Complexity Tracking. |
| III. Configuration-Driven | Pass | One config field, env override, hot reload without restart. |
| IV. Security by Default | Pass | Off by default, loopback only, consent every time, binding guard extended to cover logins, no weakening of API-key auth. |
| V. TDD | Pass | Every task starts with a failing test; default-off equivalence test is written first. |
| VI. Documentation Hygiene | Pass | New feature page, updates to connect, profiles, REST and config references, CLAUDE.md entry written by hand. |

Re-check after Phase 1: unchanged.

## Project Structure

### Documentation (this feature)

```text
specs/132-client-oauth-login/
├── design.md            # approved design (decisions, alternatives)
├── spec.md
├── plan.md              # this file
├── research.md
├── data-model.md
├── quickstart.md
├── contracts/
│   ├── oauth-endpoints.md   # public protocol surface
│   └── rest-admin.md        # admin REST, SSE, CLI, MCP tool
├── checklists/requirements.md
└── tasks.md
```

### Source Code (repository root)

```text
internal/mcplogin/                     # NEW leaf, stdlib only: pure protocol logic
  token.go        # mcp_oat_/mcp_ort_ formats, MAC, parse, hash
  pkce.go         # S256 verify
  redirect.go     # redirect URI rules (loopback any port, https exact)
  metadata.go     # RFC 9728 / RFC 8414 documents for an origin + path
  registration.go # RFC 7591 request validation, effective values
  resource.go     # origin + path -> resource URI, client id from path
  requests.go     # in-memory authorization requests and codes, decided once
  limits.go       # per-key limiter wrapper (limiter.TokenRates), caps

internal/storage/
  oauth_login.go          # buckets, registration/grant CRUD, rotate-or-revoke tx,
                          # ever-used marker, forget-client purge
  client_credentials.go   # UpdateClientCredentialBinding also writes oauth_client_bindings (one tx)

internal/auth/
  context.go              # CredentialKindOAuth
  agent_token.go          # helper building the client AuthContext from (clientID, pin, mode)

internal/runtime/
  oauth_login_service.go  # OAuthLoginService: suggest, approve, deny, exchange, refresh, revoke, list
  clients_service.go      # one binding writer covers OAuth-only clients; Forget/Revoke cascade
  credentials_service.go  # list/get/revoke grants
  binding_guard.go        # active binding includes clients with live grants
  events.go               # oauth.authorization_pending

internal/server/
  oauth_login_routes.go   # always-registered, per-request gated handlers
  oauth_login_http.go     # /oauth/register|authorize|token, /.well-known/*
  server.go               # mcpAuthMiddleware: mcp_oat_ branch; route registration
  profile_binding_guard.go# guard evaluator reads OAuth bindings

internal/httpapi/
  oauth_login.go          # /api/v1/oauth/authorizations*, grants under /clients
internal/config/          # mcp_oauth_enabled: struct, default, env, hot reload, swagger
internal/connect/         # SupportsBrowserLogin flag, URL-only entry
cmd/mcpproxy/             # connect --auth, client logins / revoke-login

frontend/src/views/AuthorizeRequest.vue          # consent page (/ui/authorize)
frontend/src/components/clients/ClientCredentials.vue
frontend/src/views/settings/fields.ts            # switch + "Experimental" badge
native/macos/MCPProxy/MCPProxy/                  # SSE case, notification, menu item

scripts/test-client-oauth-e2e.sh
tests/oauthclient/                                # scripted public client used by e2e
docs/features/client-oauth-login.md
```

**Structure Decision**: follow the existing split. Protocol logic that needs no runtime lives in a leaf package so it can be unit-tested and reused by the tunnel (Spec 089). Everything stateful sits where its neighbours already are: storage in `internal/storage`, the service next to `ClientsService`, HTTP in `internal/server`, admin REST in `internal/httpapi`.

## Delivery

Three PRs, each merged before the next starts.

| PR | Contents | User stories |
|---|---|---|
| **PR-1 backend** | config switch and default-off equivalence; `internal/mcplogin`; storage; service; public endpoints; `mcp_oat_` authentication; single binding and guard; admin REST for requests; activity; limits; e2e script; real-client check | US1, US2 (via REST), US3 (backend), US7 |
| **PR-2 surfaces** | consent page; "no key" path; macOS tray notification; Clients hub credentials; `credentials` tool, REST and CLI list/revoke | US2, US3, US4, US5 |
| **PR-3 Connect and docs** | registry flag; Connect choice (Web UI, CLI); docs; CLAUDE.md entry | US6 |

PR-2b (Go tray notification for Windows/Linux) is a follow-up outside this plan.

## Key decisions taken in planning

Full reasoning is in [research.md](./research.md).

1. **Routes are always registered and gated per request.** `http.ServeMux` cannot drop a pattern, and the mux is built once (`startCustomHTTPServer`). Only exact paths are registered (`/oauth/register`, `/oauth/authorize`, `/oauth/token`, `/.well-known/oauth-authorization-server`, `/mcp/oauth`, `/.well-known/oauth-protected-resource/mcp/oauth`) plus the two subtrees `/mcp/oauth/` and `/.well-known/oauth-protected-resource/mcp/oauth/`, each with its exact no-slash companion already in the list, so the mux never issues a trailing-slash redirect that did not exist before. Each new pattern wraps the handler that answered that path before: the default `mcpHandler` for `/mcp/oauth*`, the root handler for `/oauth/*` and `/.well-known/oauth-*`. Off and never used: delegate. Off and used: 404. On: serve.
2. **Authenticate into the existing client context.** A valid `mcp_oat_` token yields the same `AuthContext` as that client's `mcp_cli_` credential (agent type, `TokenKind=client`, `ClientID`, pin, mode), with `CredentialKind=oauth`. Every `CredentialKind` switch is audited by a test.
3. **Binding storage.** The API-key record keeps `ProfilePin`/`ProfileMode`. `oauth_client_bindings` holds the binding for clients with grants. One transaction-local helper, `writeClientBindingTx`, is the only code that writes a client's pin or mode, and every writer calls it inside its own transaction: binding update, mint and reconnect, connect rollback, profile repin and restore (`RepinProfile`, `RestorePins`). A source test forbids any other assignment to those fields in `internal/storage`.
4. **Guard.** `GuardState` gains `OAuthBindings` (client, pin, mode, live). Every producer of a current or candidate state fills it, an approval adds its not-yet-stored binding to the candidate, and the delta, fixes, conservative and offline evaluators read both sources, deduplicated by client. A storage error while building a state refuses the change.
5. **Loopback rule.** New helper `peerIsLoopback(r)` on `r.RemoteAddr`, combined with the existing `httpx.IsLoopbackHost(r.Host)`. Tray socket connections are not loopback TCP and get 404 on the public OAuth endpoints.
6. **Rate limiting.** A private `limiter.TokenRates` instance keyed by endpoint + remote address. No new dependency.
7. **Activity.** New `profile.ChangeKind` values `approve`, `deny`, `replay_revoke`; `issue` (first token exchange of a grant) and `revoke` are reused. Rows written on the public token endpoint (`issue`, `replay_revoke`) take the administrator from the grant's `approved_by` and add `trigger: client`.
8. **Settings.** `SettingField` gains an optional `badge` ("Experimental"); mirrored in the macOS settings catalogue.

## Verification

- `go test -race ./internal/mcplogin/... ./internal/storage/... ./internal/runtime/... ./internal/server/... ./internal/httpapi/...` with the CI `-skip` regex.
- Both golangci-lint runs (bare and `--build-tags server`).
- `./scripts/test-api-e2e.sh` (unchanged, proves US1) and `./scripts/test-client-oauth-e2e.sh`.
- `make swagger` and `swagger-verify`.
- Web UI: vitest + Playwright sweep report for `/ui/authorize`, `/ui/clients`, `/ui/settings`.
- macOS tray: build, swap, `mcpproxy-ui-test` for the menu item.
- Real-client check (SC-005), recorded in `specs/132-client-oauth-login/verification/`.
- Cross-model review per PR (opencode `gpt-6.1-sol`, at most 10 rounds).

## Complexity Tracking

| Deviation | Why needed | Simpler alternative rejected because |
|---|---|---|
| Mutex around the in-memory request/code store | "Decided once" (FR-020) is a compare-and-set on shared state with no I/O | A channel-owned actor adds a goroutine and a request/response type for five map operations; the existing services already use `bindingWriteMu` for the same reason |
| Second binding record (`oauth_client_bindings`) beside the API-key record | A client may have only browser logins, and an older binary must still read the right profile from the API-key record (FR-028) | Moving the binding to one new record breaks downgrade; a credential-less `AgentToken` record fails `ValidateTokenInvariants` on older binaries |

## Review

Cross-model review of plan, research, data model, contracts and tasks, round 1 (opencode `gpt-6.1-sol`, 2026-11-22): 15 findings, all accepted. Every binding writer goes through one helper; the guard state carries OAuth bindings including the candidate; forget, revoke-client, revoke-key and connect rollback are separate storage operations; exact route patterns avoid new trailing-slash redirects; the full client auth context and grant id are specified; codes carry issuer and a consumed tombstone; the initialize-name warning has storage and a hook; refresh expiry is checked before rotation; profile delete/rename files are in scope; listings after turn-off keep logins; redirect construction, unknown-client 404 at authorize, activity coverage, task order and the package boundary are corrected.

Round 2 (same reviewer): eight findings, all accepted. A connect on a client with only browser logins snapshots and restores its binding; the login is stored at the token exchange, with revision and guard re-checked there (research R15); the consent page gets a read-only preview call for the guard verdict (R16); the grant keeps the redirect URI used; the token endpoint checks the request origin against the stored issuer first; replay rows name the approving administrator; `browser_login_disabled` reflects current state only; the registration cap covers all non-approved rows.

Round 3: four findings, all accepted. Revoke, forget and turn-off cancel approved requests and unconsumed codes under the same lock as the commit; the commit creates the OAuth-side binding whenever it is absent; the registration is promoted in the commit and has no `denied` state; activity rows before a login carry `request_id`, and a refusal at exchange names the approving administrator.

Round 4: three findings, all accepted. Turn-off cancels requests synchronously while the config is applied, not from an event; the commit re-checks that a new client id is still absent and that the registration still exists.
