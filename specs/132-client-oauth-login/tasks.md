# Tasks: Browser login for MCP clients (opt-in OAuth)

**Input**: [plan.md](./plan.md), [spec.md](./spec.md), [research.md](./research.md), [data-model.md](./data-model.md), [contracts/](./contracts/), [design.md](./design.md)

**Tests**: required (constitution V). Within every group the test tasks come first and must fail before the implementation task starts.

**Format**: `[ID] [P?] [Story] Description`. `[P]` = different files, no dependency on an unfinished task. Stories: US1 update changes nothing · US2 sign a known client in · US3 manage and revoke · US4 browser without the key · US5 unknown client · US6 Connect choice · US7 turn off again.

Tasks are grouped by the three delivery PRs. Each PR is merged before the next starts, and each ends with the same gate (see "PR gate").

---

## PR-1 — Backend

### Phase 1: Setup

- [ ] T001 Create package `internal/mcplogin/` with `doc.go` stating its scope (pure protocol logic; no new external dependencies; may import only `internal/auth`, `internal/clientidentity` and `internal/upstream/limiter` from this repo) and add it to the import-boundary check if one applies (`scripts/` lint config)
- [ ] T002 [P] Create `tests/oauthclient/` with a scripted public client (`client.go`: discovery, register, authorize with a loopback listener, exchange, refresh) used by tests and the e2e script

### Phase 2: Foundational — switch and default-off equivalence (US1)

**Goal**: the switch exists, and with it off nothing answers differently. Blocks everything else.

- [ ] T003 [US1] Test `TestConfig_MCPOAuthEnabled_DefaultOffAndOmitted` in `internal/config/mcp_oauth_test.go`: default false, absent from a saved default config, env `MCPPROXY_MCP_OAUTH_ENABLED` overrides and is not persisted
- [ ] T004 [US1] Add `MCPOAuthEnabled` (`mcp_oauth_enabled,omitempty`) to `internal/config/config.go`, `FieldMCPOAuthEnabled` + `envOverride` in `internal/config/process_overrides.go` and `internal/config/loader.go`, hot field in `internal/runtime/config_hotreload.go`
- [ ] T005 [US1] Test `TestOAuthLogin_DefaultOff_RoutesUnchanged` in `internal/server/oauth_login_default_off_test.go`: for `/mcp/oauth`, `/mcp/oauth/`, `/mcp/oauth/cursor`, `/oauth/register`, `/oauth/authorize`, `/oauth/token`, both `/.well-known/oauth-*` paths, over GET/POST with and without credentials, the complete mux with the new registrations answers exactly like the mux without them (status, body, `Location`, auth-relevant headers), including `/oauth`, `/oauth/`, `/oauth/x`, `/.well-known/oauth-protected-resource` and slash/no-slash variants
- [ ] T006 [US1] Test `TestOAuthLogin_DefaultOff_NoStorage` in `internal/server/oauth_login_default_off_test.go`: after the requests of T005 the bbolt bucket list and `MetaBucket` keys are unchanged
- [ ] T007 [US1] Implement the three-state gate in `internal/server/oauth_login_routes.go` (`oauthLoginGate(prev, served)`: off+never-used → `prev`, off+used → 404, on → `served`; loopback peer + loopback Host + personal edition else 404) and register the exact patterns and two subtrees listed in research R1 (no `/oauth/` subtree) in `startCustomHTTPServer` (`internal/server/server.go`), with `served` a 501 stub for now
- [ ] T008 [P] [US1] Test and implement `peerIsLoopback(r)` in `internal/server/oauth_login_routes.go` (`oauth_login_routes_test.go`: IPv4, IPv6, non-loopback, unix socket, malformed)
- [ ] T009 [US1] Run `make swagger`; add `mcp_oauth_enabled` to `docs/configuration/config-file.md`

**Checkpoint**: `./scripts/test-api-e2e.sh` and the CI unit matrix pass unchanged.

### Phase 3: Protocol logic (leaf package, all [P] among themselves)

- [ ] T010 [P] [US2] Tests then code for token formats in `internal/mcplogin/token.go` (`token_test.go`): generate/parse `mcp_oat_`, `mcp_ort_<grant>_<secret>_<mac>`, MAC verify with a labelled key, constant-time compare, rejects of every malformed shape
- [ ] T011 [P] [US2] Tests then code for PKCE S256 in `internal/mcplogin/pkce.go` (RFC 7636 vector, wrong verifier, length bounds)
- [ ] T012 [P] [US2] Tests then code for redirect URI rules in `internal/mcplogin/redirect.go`: loopback http any port at match time, https exact, fragment refused, custom scheme refused, at most 5
- [ ] T013 [P] [US2] Tests then code for resource and origin in `internal/mcplogin/resource.go`: origin from request, `/mcp/oauth` vs `/mcp/oauth/{client}`, client id via `auth.ValidClientID`, `localhost` ≠ `127.0.0.1`
- [ ] T014 [P] [US2] Tests then code for metadata documents in `internal/mcplogin/metadata.go`, golden files in `internal/mcplogin/testdata/` matching `contracts/oauth-endpoints.md`
- [ ] T015 [P] [US2] Tests then code for registration validation in `internal/mcplogin/registration.go`: effective values, refusal of secret-based methods and other grant types, name sanitised with `clientidentity`
- [ ] T016 [P] [US2] Tests then code for the in-memory request and code store in `internal/mcplogin/requests.go`: decided once (race test with two approvers), expiry at 10 min / 60 s with a clock seam, cap of 20, cancel by client and cancel all, code single use
- [ ] T017 [P] [US2] Tests then code for limits in `internal/mcplogin/limits.go`: per-key `limiter.TokenRates`, the three default rates, `Retry-After`

### Phase 4: Storage

- [ ] T018 [US2] Tests in `internal/storage/oauth_login_test.go` for registrations: create, get, state moves, expiry sweep (15 min / 24 h), cap of 50 over `pending` and `authorized` rows together with the oldest dropped; a registration is deleted with its last grant record; there is no `denied` state
- [ ] T019 [US2] Tests in `internal/storage/oauth_login_test.go` for grants: `CommitLogin` stores the grant (with `redirect_uri`), promotes the registration to `approved`, creates the OAuth-side binding whenever the client has none (new client, and API-key-only client getting its first login, with both records equal afterwards), the replaced login's revocation and the first-grant marker in one tx and writes nothing when its revision check fails, when the registration is gone, or when a new-client decision finds the id present, lookup by access hash, `RotateOrRevoke` (current secret → rotate; authentic old secret → revoke `replay`; unknown, revoked or expired → no write and no lifetime extension, asserted by tx id), lapse after 30 days, retention of dead grants
- [ ] T020 [US2] Implement `internal/storage/oauth_login.go` for T018–T019 with lazy buckets `oauth_registrations`, `oauth_grants` (+ `by_access_hash`), marker `oauth_login_ever_used`
- [ ] T021 [US3] Tests in `internal/storage/client_binding_dual_test.go`: `writeClientBindingTx` writes the API-key record and `oauth_client_bindings` in one tx for every writer (update, mint for an OAuth-only client, reconnect, connect rollback, `RepinProfile`, `RestorePins`); a mint for an OAuth-only client snapshots its binding into `PriorProfilePin`/`PriorProfileMode`, and abort or undo restores it to the OAuth record without touching grants; works for an OAuth-only client; fails when neither exists; startup repair takes the API-key record; source test that no other code in `internal/storage` assigns `ProfilePin`/`ProfileMode` of a client record
- [ ] T022 [US3] Test `TestDowngradeView_APIKeyRecordStaysCurrent` in `internal/storage/client_binding_dual_test.go`: after a reassign, a rename and a reconnect of a client with both credentials, the raw `agent_tokens` record carries the current pin and mode (FR-028)
- [ ] T023 [US3] Tests in `internal/storage/oauth_login_client_ops_test.go` then code in `internal/storage/oauth_login.go`, `internal/storage/client_credentials.go`, `internal/storage/agent_token_repin.go`: implement `writeClientBindingTx` for T021–T022 and the four separate operations `ForgetClient` (deletes grants, OAuth binding, orphan registrations), `RevokeClient` (marks key and all grants revoked, keeps them), `RevokeClientAPIKey`, and connect rollback that leaves grants untouched

### Phase 5: Runtime service, binding, guard

- [ ] T024 [US2] Test first in `internal/auth/agent_token_identity_test.go`, then add `CredentialKindOAuth` and `AuthContext.GrantID` to `internal/auth/context.go` and `auth.ClientAuthContext(ClientIdentity)` to `internal/auth/agent_token.go`; `(*AgentToken).AuthContext` uses it; both paths give equal contexts except kind, grant id, prefix and generation (research R3); tests in `internal/server` that work-session grouping separates two logins of one client and that session-backed activity carries the grant id
- [ ] T025 [US2] Source test `TestCredentialKindAgentTokenSitesClassified` in `internal/auth/credential_kind_sites_test.go`: every comparison with `CredentialKindAgentToken` in `internal/` is listed as "applies to oauth" or "API-key specific"; fix the sites it finds
- [ ] T026 [US2] Tests in `internal/runtime/oauth_login_service_test.go` for `Describe(requestID)`: fixed client on a per-client address, generic address lists clients with none pre-selected, `profile_required`, `previous_login` only for the same registration, guard verdict included, reported name never selects anything
- [ ] T027 [US2] Tests in `internal/runtime/oauth_login_service_test.go` for `Preview` (guard verdict and access sentence for a proposed client, profile and mode; `complete: false` while a choice is missing; writes nothing) and `Approve`/`Deny`: approval stores nothing in the database and records the decision on the request; the exchange commits binding and grant after re-checking revision and guard, and answers `invalid_grant` with a `deny` row (approving administrator, `trigger: client`) when either fails; approve → revoke client → exchange, approve → forget and recreate client → exchange, and approve → feature off → on → exchange all answer `invalid_grant` and store nothing; so do two approved new-client requests with the same id (the second), and an exchange whose registration was evicted or deleted after approval; `approve`/`deny` rows carry `request_id`; `already_decided`, `binding_changed` on a stale revision, `client_fixed`, `profile_required`, new custom client id rules (`identity_exists`, `reserved_identity`), `replace_grant` revokes the old login, activity rows `approve`/`deny` with the request actor; and for `Exchange`/`Refresh`: PKCE, client and redirect binding, resource substitution, a token request whose origin differs from the stored issuer (refused first, nothing consumed), code expiry and reuse within the tombstone window, refresh expiry without extension, rotation, authentic replay, forged token with a real grant id; activity rows `issue` and `replay_revoke` naming `approved_by` with `trigger: client`; `revoke` naming the request actor
- [ ] T028 [US2] Implement `internal/runtime/oauth_login_service.go` (`OAuthLoginService`: `Describe`, `Preview`, `Approve`, `Deny`, `Exchange`, `Refresh`, `Authenticate`, `ListGrants`, `RevokeGrant`) under `bindingWriteMu`; wire in `internal/runtime/runtime.go`; add `approve`, `deny`, `replay_revoke` to `internal/profile/contract.go`; add events to `internal/runtime/events.go` and to `adminConfigEventTypes` in `internal/httpapi/sse_scope.go`
- [ ] T029 [US3] Tests in `internal/runtime/clients_service_test.go`, `internal/runtime/profiles_service_test.go` and the config-funnel tests (`internal/runtime/config_funnel_test.go`): `SetBinding` and `BulkAssign` on an OAuth-only client; `Forget` deletes grants; client revoke revokes key and all grants; profile delete reports an OAuth-only client in `profile_in_use`, `reassign_to` moves it, a failed funnel commit restores it; rename moves its pin in both records; minting an API key for an OAuth-only client keeps one binding
- [ ] T030 [US3] Implement T029 in `internal/runtime/clients_service.go`, `internal/runtime/clients_service_connect.go`, `internal/runtime/credentials_service.go`, `internal/runtime/profiles_service.go` and `internal/runtime/config_funnel.go` (revoke cascade, single-grant revoke, API-key-only revoke, grants in `List`/`Get`)
- [ ] T031 [US2] Tests in `internal/runtime/binding_guard_strict_test.go` and `internal/server/profile_binding_guard_test.go`: `GuardState.OAuthBindings` is filled by every snapshot producer (clients service, credentials service, config funnel, offline CLI, anonymous-profile change); an OAuth-only client with a named pin and a live grant is an active binding in delta, fixes, conservative and strict-offline guards; lapsed or revoked grants are not; a first approval is evaluated through its candidate entry before anything is stored; a client with both credentials counts once; a storage read error refuses the change; approval with `require_mcp_auth` off returns `binding_bypassable_without_auth` with the guard's fixes
- [ ] T032 [US2] Implement T031 in `internal/runtime/binding_guard.go`, `internal/server/profile_binding_guard.go`, `internal/runtime/config_funnel.go` and the offline guard wiring in `cmd/mcpproxy`

### Phase 6: HTTP endpoints and authentication

- [ ] T033 [US2] Tests in `internal/server/oauth_login_http_test.go` for metadata and the `401` challenge on `/mcp/oauth` and `/mcp/oauth/{client}`, unknown client id → 404 everywhere
- [ ] T034 [US2] Tests in `internal/server/oauth_login_http_test.go` for `/oauth/register`, `/oauth/authorize` (no redirect on bad client/redirect URI, `404` without redirect for an unknown per-client resource, `invalid_target`, redirects that keep an existing callback query and return `state` only when sent and unchanged including reserved characters, `no-store`, `Referrer-Policy`) and `/oauth/token` (every row of the error table in `contracts/oauth-endpoints.md`, a current refresh token after 30 days, `resource` substitution, size and rate limits)
- [ ] T035 [US2] Implement `internal/server/oauth_login_http.go` and replace the 501 stub from T007
- [ ] T036 [US2] Tests in `internal/server/oauth_login_auth_test.go`: `mcp_oat_` accepted only on its own resource and origin; API key, `mcp_cli_`, `mcp_agt_`, no token → 401 on OAuth addresses even with `require_mcp_auth` off; `mcp_oat_` on `/mcp` and routing-mode routes → 401 while the feature is on or was used, unchanged anonymous behaviour while off and never used; revoked or lapsed grant → 401 on the next request of an open session; the name sent at `initialize` is stored on the grant and the mismatch flag is derived as in research R14; profile scope and activity attribution equal the same client's `mcp_cli_` request
- [ ] T037 [US2] Implement `oauthResourceAuth` and the `mcp_oat_` branch of `mcpAuthMiddleware` in `internal/server/server.go`; record `grant_id` and credential kind on activity rows
- [ ] T038 [US7] Tests in `internal/server/oauth_login_toggle_test.go`: on → off after use gives 404 on all OAuth paths and cancels pending and approved requests with their unconsumed codes; forgetting the last client keeps 404; off → on restores logins; toggling needs no restart; cancellation happens while the config is applied on both the API update and the file-reload path and still holds when `EventTypeConfigReloaded` is delayed or dropped; a quick off → on leaves no approved request usable
- [ ] T039 [US7] Implement the toggle handling: gates read live config and the marker per request in `internal/server/oauth_login_routes.go`; a synchronous cancel-all hook under `bindingWriteMu` in the config apply path (`internal/runtime/config_hotreload.go` and the commit path in `internal/runtime/commit.go`) when `mcp_oauth_enabled` goes true → false

### Phase 7: Admin REST

- [ ] T040 [US2] Tests in `internal/httpapi/oauth_login_test.go` for `GET /oauth/authorizations`, `GET /oauth/authorizations/{id}`, `POST …/preview`, `POST …/approve`, `POST …/deny`: payloads and error codes of `contracts/rest-admin.md`, uniform 404, `403` for agent tokens and client credentials, `404 feature_disabled` when off
- [ ] T041 [US2] Implement `internal/httpapi/oauth_login.go`, register under `/api/v1` in `internal/httpapi/server.go`, classify the GETs for `scope_route_table_guard_test.go`, run `make swagger`
- [ ] T042 [US3] Tests then code in `internal/httpapi/clients.go` and `internal/httpapi/client_bindings.go`: `credentials[]` (with `observed_name`, `name_mismatch`) and `browser_login_disabled` on client payloads in the three states (off never used: payload unchanged; on; off after use: logins listed, disabled true), `DELETE /clients/{client}/logins/{grant_id}`, `DELETE /clients/{client}/api-key` (tests in `internal/httpapi/clients_routes_test.go`); update `internal/contracts` types and regenerate frontend types

### Phase 8: End to end and real clients

- [ ] T043 [US2] Write `scripts/test-client-oauth-e2e.sh` following `scripts/test-oauth-token-encryption-e2e.sh`, covering the seven numbered steps of `quickstart.md` §2 with `tests/oauthclient`
- [ ] T044 [US2] Run the real-client check of `research.md` R13 for Claude Code, Cursor, VS Code and MCP Inspector; record `specs/132-client-oauth-login/verification/real-clients.md`. **If none completes the flow over plain HTTP, stop and report before PR-2.**
- [ ] T045 Write `docs/features/client-oauth-login.md` (backend scope: enabling, addresses, tokens, turning off, downgrade, the HTTP note, REST) and add it to the docs sidebar/allowlist; add the CLAUDE.md "Recent Changes" entry by hand
- [ ] T046 PR gate for PR-1 (see below)

---

## PR-2 — Surfaces

### Phase 9: Consent page (US2, US5, US4)

- [ ] T047 [P] [US2] Add API methods and types for authorizations and client credentials in `frontend/src/services/api.ts` and `frontend/src/types/`
- [ ] T048 [US2] Vitest `frontend/tests/unit/authorize-request.spec.ts`: fixed client view, unverified-name label, access sentence, profile shown not asked when bound, Approve disabled until a profile is chosen when `profile_required`, replace-login checkbox only with `previous_login`, guard verdict fetched through the preview call whenever the selection is complete or changes, refusal with fixes shown and Approve disabled until it is clean, `already_decided` and `binding_changed` handling, navigation to `redirect_url`
- [ ] T049 [US2] Implement `frontend/src/views/AuthorizeRequest.vue` and the `/authorize` route in `frontend/src/router/index.ts`, reusing `GuardRefusal.vue`, `ProfileChip.vue`
- [ ] T050 [US5] Vitest in `frontend/tests/unit/authorize-request.spec.ts` for the generic address: nothing pre-selected, "New client" with editable id and name, existing-client choice shows the access sentence, `identity_exists` / `reserved_identity` errors
- [ ] T051 [US5] Implement the generic-address branch in `frontend/src/views/AuthorizeRequest.vue`
- [ ] T052 [US4] Vitest then code: with no API key in storage `AuthorizeRequest.vue` shows only "Open from the tray or paste the API key", makes no request-detail call, and continues after a pasted key (`frontend/tests/unit/authorize-request-nokey.spec.ts`)
- [ ] T053 [US4] Test then code: `frame-ancestors 'none'` (and `X-Frame-Options: DENY`) on `/ui/authorize` in `internal/server/server.go` (`internal/server/oauth_login_http_test.go`)

### Phase 10: Tray (US4)

- [ ] T054 [US4] macOS tray: handle `oauth.authorization_pending` / `oauth.authorization_decided` in `native/macos/MCPProxy/MCPProxy/Core/CoreProcessManager.swift`, keep pending requests in `State/AppState.swift`, add menu items "Approve sign-in for <client>…" in `MCPProxyApp.swift` opening `/authorize?request=<id>` through `WebUILink`, add `sendSignInRequest` to `Services/NotificationService.swift`; Swift unit tests for the state reducer
- [ ] T055 [US4] Verify T054 with `mcpproxy-ui-test` (menu item appears, opens the request) per `docs/development/macos-tray.md`; screenshot into `verification/`

### Phase 11: Clients hub and management (US3)

- [ ] T056 [US3] Vitest `frontend/tests/unit/client-credentials.spec.ts`: credential list per client (API key, logins with state), revoke one login, revoke API key only, "browser login disabled" label with logins still listed, initialize-name mismatch warning per login
- [ ] T057 [US3] Implement `frontend/src/components/clients/ClientCredentials.vue` and use it in `frontend/src/views/Clients.vue`
- [ ] T058 [P] [US3] Tests then code: `credentials` MCP tool lists logins and revokes one (`grant=`), never creates one — `internal/server/mcp_credentials*.go` and its tests
- [ ] T059 [P] [US3] Tests then code: CLI `mcpproxy client show` lists credentials and `mcpproxy client revoke-login <id> <grant-id>` in `cmd/mcpproxy/client_cmd.go`, `cmd/mcpproxy/client_binding_cmd.go`
- [ ] T060 [P] [US1] Settings switch: optional `badge` on `SettingField`, entry for `mcp_oauth_enabled` in `frontend/src/views/settings/fields.ts`, rendering in `frontend/src/views/Settings.vue`, mirror in `native/macos/MCPProxy/MCPProxy/Settings/SettingsCatalog.swift`; vitest that no other view mentions browser login while off
- [ ] T061 [US3] Playwright sweep for `/ui/authorize`, `/ui/clients`, `/ui/settings` with HTML report per `docs/development/web-ui-verification.md`; extend `scripts/test-client-oauth-e2e.sh` with approval through the page
- [ ] T062 Update `docs/features/client-oauth-login.md` (consent page, tray, managing logins), `docs/features/mcp-credential-lifecycle.md`, `docs/cli/profile-commands.md`, `docs/api/rest-api.md`
- [ ] T063 PR gate for PR-2

---

## PR-3 — Connect and docs

### Phase 12: Connect choice (US6)

- [ ] T064 [US6] Tests in `internal/connect/clients_test.go` and `internal/connect/connect_test.go`: `SupportsBrowserLogin` set only for clients that passed T044; `ConnectOptions.Auth=oauth` builds `…/mcp/oauth/<client-id>` with no header for each config format; the minter is not called; preview masks nothing and shows no credential; refused when the feature is off or the client is unsupported
- [ ] T065 [US6] Implement T064 in `internal/connect/clients.go`, `internal/connect/connect.go`, `internal/connect/preview.go`; expose `supports_browser_login` on the connect payload (`internal/contracts`, `make swagger`)
- [ ] T066 [P] [US6] CLI `mcpproxy connect <client> --auth key|oauth` in `cmd/mcpproxy/connect_cmd.go` and `cmd/mcpproxy/connect_credential.go`, with tests
- [ ] T067 [P] [US6] Web UI choice in `frontend/src/components/ClientConnectList.vue` (shown only when the feature is on and the client supports it; "API key" default), vitest `frontend/tests/unit/connect-auth-choice.spec.ts`
- [ ] T068 [P] [US6] macOS Connect view choice in `native/macos/MCPProxy/MCPProxy/Views/ConnectClientView.swift` and `ConnectClientModel.swift`
- [ ] T069 [US6] Extend `scripts/test-client-oauth-e2e.sh`: `connect --auth oauth` writes only the address; Playwright sweep for the Connect screen
- [ ] T070 Finish docs: `docs/features/client-oauth-login.md` (Connect, verified clients list), `docs/features/connect-clients.md`, `docs/features/profiles.md` (credentials of a client), `docs/features/agent-tokens.md` cross-link; CLAUDE.md entry
- [ ] T071 PR gate for PR-3; comment on issue #1971 with what shipped and how to enable it

---

## PR gate (T046, T063, T071)

1. `go test -race` for the touched packages with the CI `-skip` regex, plus `-tags server` for `internal/server`, `internal/httpapi`, `internal/storage`.
2. Both `golangci-lint` runs (`.github/.golangci.yml`, bare and `--build-tags server`).
3. `./scripts/test-api-e2e.sh` (run alone) and `./scripts/test-client-oauth-e2e.sh`.
4. `make swagger` clean; frontend `npm run test:unit` and build when `frontend/` changed.
5. Cross-model review (opencode `github-copilot/gpt-6.1-sol`, chunked by file, at most 10 rounds); security-sensitive PR-1 also gets one Opus subagent review.
6. PR title `feat(oauth): … (Spec 132 PR-n)`, body via `--body-file`, `Related #1971`, no QA report in the body.

## Dependencies

- Phase 2 blocks everything. Phase 3 tasks are mutually parallel. Phase 4 needs T010. Phase 5 needs Phases 3–4. Phase 6 needs Phase 5. Phase 7 needs T028 and T030. Phase 8 needs Phases 6–7.
- PR-2 needs PR-1 merged. Phase 10 needs T028 (events). Phase 11 needs T042.
- PR-3 needs T044 (which clients qualify) and PR-2 merged.
- Story order: US1 (Phase 2) → US2 backend (3–7) with US3 backend and US7 alongside → US2/US5/US4 surfaces → US3 surfaces → US6.

## Parallel opportunities

- T010–T017 (eight files in `internal/mcplogin`).
- T002 with Phase 2.
- T058, T059, T060 with the Clients hub work.
- T066, T067, T068 after T065.

## Implementation strategy

- **MVP = PR-1.** It is usable with the scripted client and the admin REST, proves the default-off promise and settles the real-client question (T044) before any UI is built.
- Stop points: after T009 (switch only, safe to merge alone if PR-1 grows too large), after T044 (go/no-go for PR-2).
- Counts: 71 tasks. PR-1 46 (US1 7, US2 27, US3 7, US7 2, shared 3), PR-2 17, PR-3 8.
