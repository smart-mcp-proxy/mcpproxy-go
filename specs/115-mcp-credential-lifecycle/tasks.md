# Tasks: MCP Credential Lifecycle (Spec 115, issue #1552)

**Input**: design documents in `/specs/115-mcp-credential-lifecycle/`
**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/, quickstart.md

**TDD rule (constitution V)**: each `test` task is written first and must FAIL for the stated reason, on a build that compiles, before its paired `impl` task starts (see memory `verify-test-bites-build-failure`: a stub may be needed so the failure is behavioural, not a compile error).

**Format**: `[ID] [P?] [Story] Description`. `[P]` means the task can run in parallel with the other `[P]` tasks in its group (different files). Stories: US1 client path, US2 token path, US3 live reassign, US4 authorization, US5 UI review.

---

## Phase 1: Setup

- [ ] T001 Confirm the branch `115-mcp-credential-lifecycle` is based on current `origin/main`. Run the baseline suites once and record the timings in the PR description: `go test -race ./internal/runtime/... ./internal/httpapi/... ./internal/storage/... ./internal/auth/...`, the internal/server race lane with the CI `-skip` regex, and `go test ./internal/server -run 'TestToolsListSnapshot'`.
- [ ] T002 [P] Inventory the tests that assert today's behaviour this spec deliberately changes: custom client add recording `change: assign` (`grep -rn 'ChangeAssign' internal/runtime/*_test.go internal/httpapi/*_test.go`), and the frozen goldens. List them in the PR description as "deliberate updates".
- [ ] T003 [P] Build the live-harness assets in the run dir, without committing them: a ledger variant of `/private/tmp/claude-501/qa070/fixture.py` (appends each `tools/call` to `ledger.jsonl`) with two servers `library` and `tracker` shaped like quickstart §2, and an `mcp.py` copy with `--session-file` and `--header`.

## Phase 2: Foundational (blocks every story)

- [ ] T004 [P] test: `internal/auth/agent_token_lifecycle_fields_test.go`. `RevokedAt`, `Issuer` and `Purpose` round-trip through JSON with `omitempty`. A pre-115 record (fixture JSON without the fields) decodes unchanged. `ValidateTokenInvariants` ignores the new fields. `MaxCredentialPurpose == 500`.
- [ ] T005 impl: `internal/auth/agent_token.go`. Add `RevokedAt *time.Time`, `Issuer *CredentialIssuer`, `Purpose string`, the `CredentialIssuer` type and `MaxCredentialPurpose`.
- [ ] T006 [P] test: `internal/storage/agent_tokens_revoked_at_test.go`. `RevokeAgentToken` stamps `RevokedAt` once, and a second revoke keeps the first time. A new `RevokeAgentTokenReport(name)` returns `(before, after)` so callers can detect `changed`. `ForgetClientCredential` stamps `RevokedAt`. `RevokeAgentTokensForOwner` stamps it too.
- [ ] T007 impl: `internal/storage/agent_tokens.go` and `client_credentials.go` per T006.
- [ ] T008 [P] test: `internal/profile/contract_credentials_test.go` plus a golden update. `ChangeIssue="issue"` and `ChangeRevoke="revoke"` are in the change-kind enum. The credential error codes from contracts/errors.md are in `testdata/contract/enums.json` under `credential_error_codes`. The frontend parity test (`frontend/tests/unit/contracts*.spec.ts`) sees the same lists.
- [ ] T009 impl: `internal/profile/contract.go`, `internal/profile/testdata/contract/enums.json`, `frontend/src/types/contracts.ts`, and `internal/runtime/events.go` (`EventTypeCredentialsChanged = "credentials.changed"`).

**Checkpoint**: the model compiles and the existing suites are green.

---

## Phase 3: Credential service (US1, US2): issue

- [ ] T010 [P] [US2] test: `internal/runtime/credentials_service_token_test.go`, a table for `IssueToken` with `RequireProfile` and `EnforceGuard` set. Cases:
  - profile missing → `missing_argument`
  - `""` → `profile_required`
  - unknown → `unknown_profile`
  - `ExpiresAt` zero → `invalid_expiry`
  - name syntax → `invalid_argument`
  - `client-x` → `reserved_identity`
  - an existing active, expired or revoked name → `identity_exists{state}`
  - 100 stored tokens → `token_limit_reached`
  - success → `allowed_servers ["*"]`, three permissions, `profile_pin`, issuer stamped, purpose stored

  The REST mode (`RequireProfile=false`) keeps the legacy scope path. The test fails with "undefined: CredentialsService" against a stub, so add a minimal stub first.
- [ ] T011 [P] [US1] test: `internal/runtime/credentials_service_client_test.go`, a table for `IssueClient` with `RefuseExistingRecord`. Cases:
  - invalid id → `invalid_argument`
  - connect-registry id (`cursor`) → `reserved_identity`
  - existing active, expired or revoked record → `identity_exists{state}`
  - a regular token holding `client-<id>` → `identity_exists{state:"conflicting_token"}`
  - mode omitted → `locked`; `switchable` only when explicit
  - display_name over 64 or purpose over 500 → `invalid_argument`
  - success → the view from data-model §2
- [ ] T012 [P] [US1][US2] test: `internal/server/profile_binding_guard_tokens_test.go`. For profiles P, the guard verdict for a `guard_bound` token pinned to P equals the verdict for a locked client bound to P, in every path: `BindingGuardDelta`, `BindingGuardFixes`, `BindingGuardActiveBindings`, the anonymous request guard `bindingGuardActive` (coherent and publication-gap branches) and `ConservativeBindingGuard`, across `require_mcp_auth` on/off and `anonymous_profile` unset, narrower or wider. A token and a client in the same state do not collide in the delta's `already` set (keyed by `TokenName`). A REST-style token with `profile_pin` but no `guard_bound` changes no verdict (A13). The existing guard tests run unchanged.
- [ ] T013 impl: `internal/auth/agent_token.go` (`GuardBound`, invariant), `internal/runtime/binding_guard.go` (`ActiveGuardedBinding` replacing `ActiveNamedBinding` at every call site, conservative handling, delta keyed by `TokenName`), `internal/runtime/binding_guard_wiring.go` (`clientCredentialSnapshot` → `guardedBindingSnapshot` including guard-bound tokens) and `internal/server/profile_binding_guard.go` (`bindingGuardActive`, `guardEvaluation.bypassable`, `BindingGuardDelta`, `BindingGuardActiveBindings` use `ActiveGuardedBinding`). `bindingBypassable` is unchanged.
- [ ] T013a [P] [US2] test: `internal/runtime/credentials_guard_persistence_test.go` (FR-012a). Issue a token through `CredentialsService.IssueToken` with `EnforceGuard` under `require_mcp_auth: true`. Then `MutateConfig` and `GuardedApplyConfig` setting `require_mcp_auth: false`, and separately removing or widening a confining `anonymous_profile`, both return `*BindingGuardError` naming the token, and the config is unchanged. Applying the same config through the file-watcher path (`ApplyConfig` without the guard) succeeds, and the server-side `bindingGuardActive` then reports true, so an anonymous `tools/call` is refused with zero mock dispatches. After revoke, the guard releases. Must fail against the round-0 design (issuance-only check).
- [ ] T014 [P] [US1][US2] test: no silent defaults. No MCP-mode request without a valid named profile ever reaches the store; a recording fake store asserts zero `CreateAgentToken`/`MintClientCredentialNamed` calls. A token issued to P and then P force-deleted resolves through `ResolveProfileV3` to `Scope = NewProfileScope(P, nil)`, which denies all.
- [ ] T015 [P] test: atomicity. For each refusal in T010/T011 the store listing before and after is equal, and no secret was generated or returned (`GenerateToken` spy not called before validation passes).
- [ ] T015a [P] test: no fallible step after commit. With a fault-injection store whose `ListAgentTokens` starts failing after a successful `MintClientCredentialNamed`/`CreateAgentToken`, `IssueClient` and `IssueToken` (and REST `POST /api/v1/clients`, now on the same path) return the secret and a view projected from the committed record, with no error; activity/SSE failures injected after the commit likewise do not fail the call. With a store whose mint fails, there is no record, no `profile_change{issue}` and no `credentials.changed`. Must fail against today's `ClientsService.Add` (`clients_service.go:844-850`).
- [ ] T016 [P] test: concurrency, `go test -race -count=50`. 20 goroutines issuing the same token name produce exactly 1 success and 19 `identity_exists`. `IssueToken(P)` racing `ProfilesService.Delete(P)` ends either with the token minted and the delete refused (`profile_in_use`), or with `unknown_profile` and no token.
- [ ] T017 [P] test: audit. A successful issue writes exactly one `profile_change` with `change=issue`, actor and surface, and the diff keys from data-model §3. Its JSON has no secret, no hash and no purpose text. A failed issue writes none.
- [ ] T018 [P] test: event. `credentials.changed` is published once after a successful issue, with `{kind,id,token_name,change,profile}` only. A client issue also publishes `client.binding_changed` (existing listeners).
- [ ] T019 impl: `internal/runtime/credentials_service.go`. Stamp `GuardBound` when `EnforceGuard` is set; project the delivered view from the committed record (no post-commit read; T015a). `CredentialsService`, `CredentialView`, `IssueClientRequest`, `IssueTokenRequest`, `CredentialRef`, a typed error per code (each with a `Code()` method like `NoClientCredentialError`), and the `IssueToken` body moved from `httpapi.handleCreateToken` (validation, `GenerateToken`, HMAC key, `CreateAgentToken`, error classification). All mutations run under the shared `bindingWriteMu`.
- [ ] T020 impl: `internal/runtime/clients_service.go` and `clients_service_connect.go`. Factor `Add` into the issue path used by `CredentialsService.IssueClient` (`issueLocked` returns the committed record from `MintClientCredentialNamed`; the trailing `s.records()` read in `Add` is removed), with an `issueOptions.refuseExisting` flag and the `issue` record kind. Stamp `Issuer` and `Purpose` on mint (`MintClientCredentialNamed` gains an options struct). Make the T011/T017/T018 tests pass, and update the T002-listed tests that expected `assign` on add.
- [ ] T021 impl: `internal/runtime/runtime.go`. Construct `CredentialsService` with the store, the HMAC key func, the config func, the guard func, activity, publish, `&rt.bindingWriteMu` and the `ClientsService`. Expose `(*Runtime).CredentialsService()`.
- [ ] T022 [P] test: `internal/httpapi/tokens_lifecycle_test.go`. The existing `tokens_*_test.go` files pass unchanged. `POST /tokens` now writes `profile_change{issue, surface: api}` and publishes `credentials.changed`, still issues under `require_mcp_auth: false` (A9), and accepts `purpose`. `GET` responses carry `revoked_at`, `issuer`, `purpose`, `lease` and `profile_state`.
- [ ] T023 impl: `internal/httpapi/tokens.go`. `handleCreateToken`/`handleRevokeToken` call `CredentialsService` and map its typed errors to today's status codes and texts. Add the view fields.
- [ ] T024 impl: `internal/httpapi/client_bindings.go`. `handleCreateClient` goes through `CredentialsService.IssueClient` (REST mode: existing record revival allowed, profile optional as today) and accepts `purpose`. Update `oas/swagger.yaml` and `docs/api/rest-api.md` (record kind `issue`, new fields).

## Phase 3b: Credential service: revoke, list, get

- [ ] T025 [P] [US2] test: `Revoke(token)` returns `changed=true` the first time, then `changed=false` with no second record or event. `RevokedAt` is set. An unknown token → `identity_not_found`. An expired token → revoked, `changed=true`. `NotifyBindingChanged(name)` is called once.
- [ ] T026 [P] [US1] test: `Revoke(client)` goes through `forgetLockedOpt` with record kind `revoke` and diff `via: credentials`. A supported client (connect-registry, minted via the fake minter) gets `client_config_untouched=true` and its config file is not read or written (fake reader asserts no call). A held connect claim → `connect_in_progress`. The Clients-page Forget path still writes `forget`.
- [ ] T027 [P] test: `ClientsService.Forget` publishes `credentials.changed{change: forget}` (closes G6).
- [ ] T028 [P] test: `List(filter{kind, profile, state})` and `Get(ref)`. Sorting is correct. `state` precedence is revoked over expired. `lease` holds at exactly 24 h and not at 24 h + 1 s. `profile_state=dangling` appears after a forced delete. A reflection test checks that `CredentialView` has no field named or tagged `*hash*`, `*pending*` or `*secret*`. The server edition lists tokens only.
- [ ] T029 impl: `CredentialsService.Revoke/List/Get`.
- [ ] T030 impl: `forgetLockedOpt` gains a change-kind parameter and the publish call.
- [ ] T031 [P] [US1][US2] test: `internal/server/credentials_revoke_session_test.go`, in process over HTTP. A token or client session initializes. After `CredentialsService.Revoke`, the next POST on the same `Mcp-Session-Id` returns 401 with `token has been revoked`. This is unit-level coverage ahead of the E2E.

**Checkpoint**: the service is complete. REST and CLI behave as before plus audit and event.

---

## Phase 4: The `credentials` MCP tool (US1, US2, US4)

- [ ] T032 [P] [US4] test: `internal/server/mcp_admin_access_test.go`. All existing `profiles` visibility tests (`mcp_profiles_visibility_v3_test.go`, `mcp_profiles_guard_coverage_v3_test.go`) pass unchanged against the extracted `adminToolAccess`, and a new table checks the identical verdict for `credentials` per caller kind: api_key, socket, agent token, client credential, anonymous, server-edition user, api_key with a non-management session profile, api_key with a management profile, and a binding-guarded anonymous.
- [ ] T033 impl: `internal/server/mcp_admin_access.go` (extract `adminToolAccess`; `profilesToolAccess` becomes a call to it).
- [ ] T034 [P] [US4] test: `internal/server/mcp_credentials_tool_access_test.go`. Non-admin `tools/list` lacks `credentials`. A forged `tools/call` returns the text `unknown tool: credentials` and leaves no store change and no `profile_change`. An admin session whose `set_profile` selects a non-management profile is refused and its management refusal is recorded (as for `profiles`). After `set_profile("")` the tool is visible and callable.
- [ ] T035 [P] [US4] test: gates. Hot-reload `read_only_mode: true`, then `disable_management: true`. `create_client`, `create_token` and `revoke` return `{code: read_only_mode|management_disabled, error: <text byte-equal to profiles>}`. `list` and `get` succeed.
- [ ] T036 [P] [US4] test: `credentials` is absent on the `/mcp/all` direct surface. A `code_execution` script calling `call_tool('credentials', …)` or `call_tool('mcpproxy:credentials', …)` fails with no store change.
- [ ] T037 [P] [US4] test: with `require_mcp_auth` off, an anonymous caller is never an admin caller for `credentials`, with or without `anonymous_profile`. With it on, an anonymous caller gets 401 at the middleware (existing).
- [ ] T038 [P] test: the schema and argument decoding follow contracts/mcp-credentials-tool.md:
  - unknown operation → `unknown_operation`
  - an unknown key such as `allowed_servers` → `invalid_argument{field}`
  - a non-string value → `invalid_argument`
  - both or neither of `client`/`token` on get/revoke → `invalid_argument`
- [ ] T038a [P] test: `internal/server/mcp_credentials_tool_secret_input_test.go` (FR-020a). For each string argument (known keys and an unknown key) and each operation, a value containing `mcp_agt_…`, `mcp_cli_…`, the configured API key, or a detector-flagged secret (AWS key, private key header, high-entropy string) answers `secret_in_argument` with the field, before any other code (it wins over `unknown_operation` and `invalid_argument`). The error text, the stored `internal_tool_call` record, the SSE payload, a zap debug observer and the store contain no part of the value beyond the redaction marker. Error texts for `unknown_operation`, `invalid_argument` on ids and `invalid_expiry` do not quote the caller's value.
- [ ] T039 [P] [US1][US2] test: FR-016. An MCP-issued locked client and a pinned token resolve with source `pin`. `set_profile(other)` is refused. `/mcp/p/<other>` with the credential is still pinned. A session selection never applies.
- [ ] T040 [P] [US1][US2] test: FR-017, in process, with two profiles (quickstart E2E-4): a code-enabled restricted profile where admitted nested reads under `/mcp/code` dispatch exactly once and forbidden nested calls zero times, and a code-disabled profile where a forged `code_execution` call is refused before the script runs with zero dispatches. For an issued worker, a tool on another server, an unannotated tool under `unannotated: deny`, and a denied-by-rule tool are refused on `/mcp` `call_tool_*`, `/mcp/call`, `/mcp/code` nested calls and `/mcp/all` direct calls, with the mock's dispatch counter unchanged.
- [ ] T041 [P] [US1][US2] test: the delivery format. It has the keys `client|token`, `credential`, `snippet{generic_http, header_name: "X-API-Key"}` and `delivery{shown_once, endpoint, header_name, alternate_header, install_note}`. Its `links` contain no `apikey`/`api_key`/credential substring. `generic_http` parses as JSON with the secret in `X-API-Key`.
- [ ] T042 [P] test: the activity body. For a successful create, the `internal_tool_call` response stored and the SSE `activity.internal_tool_call.completed` payload both contain `"[REDACTED: one-time credential]"` and not the secret. For list, get and revoke the result is stored as-is. A refusal is stored with `status: error` and the structured body.
- [ ] T043 [P] test: the redaction backstop (`internal/oauth` AuditRedaction plus `redactBuiltinResponseForActivity`). A string value containing `mcp_agt_`/`mcp_cli_` followed by 8 or more characters is masked. The 12-character `token_prefix` display value is kept. Run it over `profiles` and `upstream_servers` activity bodies too.
- [ ] T044 [P] test: logs. A zap observer at debug level across issue, list, get, revoke and a refused issue for both kinds finds no secret. Audit the mcp-go server options and hooks in `internal/server/server.go`/`mcp.go` for tool-result logging, and if any logs results, add a failing case and fix it under T046.
- [ ] T045 impl: `internal/server/mcp_credentials_tool_schema.go` (operations, args, one-line descriptions per contracts).
- [ ] T046 impl: `internal/server/mcp_credentials_tool.go`. `buildCredentialsTool`, `filterCredentialsTool` (called in `filterProfileV3Tools` next to `filterProfilesTool`), `handleCredentials` (access re-check, live gates, dispatch to `CredentialsService`, the secret-shaped input screen first (data-model §8, reusing `internal/security.Detector`), structured errors via `credentialsErrorText` (echo rule in contracts/errors.md), `auditSummaryFor`, `emitActivityInternalToolCall` with the audit body only). Register it in `mcp.go` next to `profiles` and in both `mcp_routing.go` builders.
- [ ] T047 impl: the delivery helpers. Export `CredentialSnippet(secret)` and `CredentialUILinks(ref)` from `internal/httpapi/admin_views.go` (reusing `clientSnippet` and `mcpEndpointURL`) behind the admin-views interface. Fix the `install_note` text (FR-025).
- [ ] T048 impl: the redaction backstop in `internal/oauth` AuditRedaction (value rule) per T043.
- [ ] T049 Goldens. Regenerate once with `MCPPROXY_WRITE_TOOLSLIST_GOLDENS=1` for `default_server`, `default_server_read_only`, `retrieve_tools_mode` and `code_execution_mode`. Add `credentials` to `toolsListAllowedDelta` with a Spec 115 comment. Assert that the `profiles` entries are byte-equal. Measure the cl100k delta with the token-bench helper (SC-006 ≤ 1,200) and put the number in the commit message.

**Checkpoint**: an admin can run the whole lifecycle over MCP, verified at unit level.

---

## Phase 5: End-to-end proof (all stories) — quickstart.md §3

- [ ] T050 E2E harness: add an atomic per-tool dispatch counter to `MockUpstreamServer` in `internal/server/e2e_test.go` (`Dispatches(tool) int64`). Add helpers `newCredentialSession(t, env, header)` (an mcp-go streamable-HTTP client that keeps its session) and `assertRefusedNoDispatch(t, mock, tool, fn)`, which asserts an inner semantic refusal and a zero delta, never HTTP status alone.
- [ ] T051 [US1] `TestE2E_CredentialsLifecycle_FreshClient` (E2E-1).
- [ ] T052 [US2] `TestE2E_CredentialsLifecycle_FreshToken` and `TestE2E_CredentialsLifecycle_TokenExpiry` (E2E-2, `expires_in: "3s"`).
- [ ] T053 [US3] `TestE2E_CredentialsLifecycle_LiveReassign` (E2E-3).
- [ ] T054 [US4] `TestE2E_CredentialsLifecycle_Negatives`, `TestE2E_CredentialsLifecycle_SurfaceParity` (code-enabled and code-disabled profiles) and `TestE2E_CredentialsLifecycle_GuardPersists` (E2E-4: deleted pin, non-admin kinds, write gates, duplicates, invalid input and the conflict race, guard, guard persistence across API config writes and hot reload).
- [ ] T055 `TestE2E_CredentialsLifecycle_SecretSinks` (E2E-5: issued secrets and the API key fed back into every argument and invalid-input path; logs, activity, exports, SSE, notifications, errors, config file, config.db bytes; a revoked session cannot list).
- [ ] T056 Live-harness run on the built binary (`go build -o /tmp/.../mcpproxy ./cmd/mcpproxy`, ports 18920–18929). Run E2E-1 to E2E-6 with administration over MCP only and the ledger file. Write `docs/qa/0.70.0-ux-2026-10-10/remediation/evidence/i1552/EVIDENCE.md` with the raw request/response JSON, ledger deltas and log greps. Do not commit it.

---

## Phase 6: Web UI (US5)

- [ ] T060 [P] [US5] test: `frontend/tests/unit/credentials-copy.spec.ts`. `leaseText` ("Lease ends in 45 min", "Lease ended"), `issuerText` ("via MCP · api_key"), `bindingText` ("Locked to X", "Pinned to X"), `stateBadge` (revoked over expired).
- [ ] T061 [US5] impl: `frontend/src/utils/credentials.ts`, plus the new fields in `frontend/src/types/api.ts` (`revoked_at`, `issuer`, `purpose`, `lease`, `profile_state`).
- [ ] T062 [US5] test+impl: Clients custom rows and `AgentTokens.vue` rows show binding, issuer, lease or expiry, state with `revoked_at`, and the "Stated purpose — not enforced" disclosure. Lease rows carry no `text-warning` class (UI-001, UI-005).
- [ ] T063 [P] [US5] test+impl: the `ProfileEditor.vue` Assigned to section gains an Open link per client (`/clients?client=<id>`), keeps the token link, and has deep-link router tests (UI-002).
- [ ] T064 [P] [US5] test: purpose renders as text, not HTML, under the unenforced label, and is visually separate from the Tools → Callable restrictions.
- [ ] T065 [P] [US5] test+impl: `tierPhrase(max_tier, tool_counts)` in `frontend/src/utils/profiles.ts` returns "Read-only + 1 write exception" or "Read + write + 2 destructive exceptions". Plain "Read-only" appears only with zero counts above the cap. Wire it into `ProfileCard.vue` and the editor (UI-003, G7).
- [ ] T066 [P] [US5] test+impl: `Activity.vue` labels `profile_change` `issue`/`revoke` per contracts, shows the actor and surface, and links the identity and profile (UI-006).
- [ ] T067 [US5] test+impl: `stores/system.ts` relays `credentials.changed` as `mcpproxy:credentials.changed`. The clients store, `AgentTokens.vue` and the profiles store refetch on it (UI-004).
- [ ] T068 [P] [US5] test: with a dirty `ProfileEditor` draft, a `profiles.changed` caused by an MCP update keeps the draft, updates `saved`, and shows Reload (existing behaviour pinned).
- [ ] T069 [P] [US5] test: a lease credential with 10 minutes left shows no "expiring soon" warning and no reconnect copy. A 30-day credential with 2 days left keeps today's warning.
- [ ] T070 [P] [US5] test: every `links` target from T041 resolves in the router to the filtered view: `clients?client=`, `clients?tab=tokens&token=`, `profiles/<name>?tab=tools&reason=callable`, `activity?client=|token=` (UI-007).
- [ ] T071 [US5] Playwright sweep against the live harness after T056 provisioning (`docs/development/web-ui-verification.md`). Take screenshots of quickstart §4 steps 1–7, with before/after for MCP assign and revoke and no reload. Save them under the evidence dir only.

---

## Phase 7: Docs, verification, review

- [ ] T080 [P] Docs: write `docs/features/mcp-credential-lifecycle.md` (walkthrough, tool reference, guard prerequisites, the provisioning-versus-installation boundary with the X-API-Key/Bearer snippet, lease semantics, audit). Add it to the docs-site allowlist and `sidebars.js`.
- [ ] T081 [P] Docs: update `docs/features/profiles.md` (`credentials` beside `profiles`, `management_tools` visibility), `docs/features/agent-tokens.md` (MCP issuance, `issue`/`revoke` records, `purpose`), and `docs/api/rest-api.md` (T024 changes).
- [ ] T082 Run `.specify/scripts/bash/update-agent-context.sh claude` so CLAUDE.md "Recent Changes" gets the Spec 115 line. Run `scripts/gen-roadmap.py` if the roadmap hook flags the new spec dir.
- [ ] T083 Quality gates: both golangci-lint v2 passes (bare and `--build-tags server`); `go test -race ./internal/...`; the CI `-skip` race lane for server/httpapi/storage under `-tags server`; `go test -race ./internal/server -run TestE2E_CredentialsLifecycle`; `./scripts/test-api-e2e.sh`; frontend `npm run build`, vitest and type-check.
- [ ] T084 Cross-review: OpenCode GPT-6.1 Sol, read-only, with briefs chunked by file group (service and guard; MCP tool and redaction; REST; E2E; UI). Verify each finding before fixing it. Cap at 10 fix→re-review rounds per PR, and treat an empty result as UNREVIEWED.
- [ ] T085 Release notes: the `credentials` tool; the MCP issue path enforces the binding guard (A9) and MCP-issued tokens stay standing guard bindings (FR-012a, A13); secret-shaped `credentials` arguments are refused (FR-020a); REST custom client add no longer reports an error after a committed mint; custom client add now records `issue` instead of `assign`; REST/CLI tokens now produce `issue`/`revoke` records and `credentials.changed`.

---

## Dependencies

```
T001–T003 ─► T004–T009 (foundation)
T009 ─► T010–T018 (tests) ─► T019 ─► T020 ─► T021 ─► T022–T024
T012 ─► T013 ─► T013a ─► T019
T015a ─► T019/T020
T021 ─► T025–T031 ─► T029/T030
T029 ─► T032–T044 (tests) ─► T033 ─► T045–T048 ─► T049
T049 ─► T050 ─► T051–T055 ─► T056
T023/T024 (REST fields) ─► T060–T070 ─► T071 (needs T056 provisioning)
T049 + T056 + T071 ─► T080–T085
```

**Parallelism**: inside each test group, the `[P]` tests touch different files. US3 (T053) needs no new production code: it exercises the existing `profiles assign` on MCP-issued identities. UI work (Phase 6) can start once T023/T024 fix the REST field shapes.

**MVP**: Phases 1–5 (US1, US2, US4 and the E2E proof) are the shippable minimum for the capability criteria. Phase 6 satisfies the issue's UI criteria and is required before the issue is closed.

## Follow-ups (not in this spec)

- MCP `rotate` (staged for clients, regenerate for tokens) on `CredentialsService`.
- A unified task review/diff view (issue #1552's suggested follow-up).
- macOS tray display of issuer, lease and purpose.
- Server-edition per-user tokens and OAuth admin callers for `credentials`.
