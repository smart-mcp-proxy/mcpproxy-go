# Tasks: Harden the OAuth Client Role (Spec 135)

**Input**: `spec.md`, `plan.md`, `research.md`, `data-model.md`, `contracts/`, `quickstart.md` in this directory.
**Tests**: required (constitution V, TDD). Every implementation task starts with a failing test named in `plan.md` "Test plan".
**Organisation**: phases follow the plan's three delivery PRs ("Delivery"); inside a PR, tasks carry the user story they serve. Tasks marked `[x]` landed in PR-1 (branch `claude/spec135-pr1-iss`).

Format: `- [ ] T### [P?] [US?] description (file)`. `[P]` = can run in parallel with the other `[P]` tasks of the same phase (different files, no dependency).

---

## Phase 1: Setup (PR-1)

- [x] T001 Bump `github.com/mark3labs/mcp-go` v1.0.0 → v1.6.0 and `go mod tidy` (`go.mod`, `go.sum`) — FR-019, R1
- [x] T002 Record every upstream behaviour change as adopted, neutralised or corrected (`specs/135-oauth-client-hardening/research.md` R1a)
- [x] T003 Neutralise the v1.3 refresh-provenance rule: `oauth.WithRefreshProvenance` and the store answer under it (`internal/oauth/refresh_provenance.go`, `internal/oauth/persistent_token_store.go`, `internal/upstream/core/oauth_refresh.go`); test `TestRefreshProvenance_*` (`internal/oauth/refresh_provenance_test.go`)
- [x] T004 [P] Extend the in-repo test authorization server with `AdvertiseIss`, `IssMode`, `WrongIssuer`, `RejectApplicationType`, `RegistrationRequests()`, `TokenRequestCount()` (`tests/oauthserver/`) — plan "Fake authorization server" (deviation: extends `tests/oauthserver` instead of a new `internal/oauth/oauthtest`)

## Phase 2: Foundational — strict callback parse (PR-1, FR-001, plan D1)

- [x] T005 Test `TestParseCallbackQuery_Strict` / `_KeepsIssPresence` (`internal/oauth/callback_parse_test.go`)
- [x] T006 `oauth.ParseCallbackQuery` + `ErrInvalidCallback` + `CallbackResult{Query url.Values}` (`internal/oauth/callback_parse.go`)
- [x] T007 Loopback callback: parse first, no waiter taken on rejection, waiter channel carries `CallbackResult`, `iss_present` logged (`internal/oauth/config.go`); test `TestLoopbackCallback_StrictParse_WaiterKept`
- [x] T008 [P] Remote callback hands its parsed query on instead of its inline check (`internal/oauth/remote_callback.go`); existing `TestRemoteCallback*`
- [x] T009 [P] Broker callback runs the parse; a rejection keeps the pending flow (`internal/serveredition/api/credential_handlers.go`); test `TestCredentialsCallback_StrictParse_RepeatedCodeRejectedFlowKept` (`-tags server`)
- [x] T010 One waiter per flow: `getAuthorizationURLQuick` returns the registered channel and `waitForOAuthCallbackAsync` consumes it (`internal/upstream/core/connection_oauth.go`); test `TestCallback_ArrivesBeforeAsyncConsumer` (`internal/upstream/core/oauth_callback_handle_test.go`)
- [x] T011 Flow test against the test server: `iss` reaches the flow intact for every `IssMode`, repeated `iss` refused before delivery (`internal/oauth/callback_iss_flow_test.go`)

## Phase 3: User Story 5 — DCR application type (PR-1, P2, FR-016, plan D5)

**Independent test**: registration bodies recorded by `tests/oauthserver` carry `native` for loopback `http` and `web` otherwise.

- [x] T012 [US5] Tests `TestDCR_ApplicationType`, `TestDCR_RejectApplicationType_NoRetry`, `TestApplicationTypeFor` (`internal/oauth/dcr_application_type_test.go`)
- [x] T013 [US5] Registration-body rewrite in the transport wrapper (mcp-go maps `127.0.0.2` to `web`) (`internal/oauth/dcr_application_type.go`, `internal/oauth/transport_wrapper.go`)
- [x] T014 [US5] Docs: "What the callback accepts", "Client registration type", malformed-response troubleshooting (`docs/features/oauth-authentication.md`)

**Checkpoint PR-1**: `go list -m github.com/mark3labs/mcp-go` ≥ v1.4.0; the existing `internal/oauth` and `internal/upstream/core` OAuth suites green.

---

## Phase 4: Enforcement core (PR-2, plan D0)

- [ ] T015 Test `TestResolver_ScopeAndLoginValidatorPathsTerminal`, `TestResolver_LibraryNeverRunsOwnCandidateLoop` (`internal/oauth/issuer_resolver_test.go`)
- [ ] T016 `oauth.ResolveASMetadata` + immutable `Snapshot` used by preflight, scope discovery, login validator, configured URL and wrapper (`internal/oauth/issuer_resolver.go`, `internal/oauth/config.go`, `internal/oauth/discovery.go`)
- [ ] T017 `SendContext` + `GuardedTransport` on the wrapper, direct refresh and broker clients; `CheckAuthorizationURL` (`internal/oauth/guard.go`, `internal/upstream/core/client.go`, `internal/serveredition/broker/oauth_connector.go`); tests `TestGuard_*`, `TestAuthorizationURL_*`

## Phase 5: User Story 1 — wrong-AS sign-in refused (PR-2, P1, FR-002–FR-005, plan D2)

**Independent test**: a mix-up against the test server (honest H, hostile A) sends nothing to A's token endpoint.

- [ ] T018 [US1] Test `TestDecideIssuer_Matrix` (`internal/oauth/issuer_test.go`)
- [ ] T019 [US1] `IssuerSource`, `ExpectedIssuer`, `DecideAuthorizationResponseIssuer`, `IssuerMismatchError` (`internal/oauth/issuer.go`)
- [ ] T020 [US1] Waiter carries `{exp, skip}`; decision in the callback handler before the page; `CallbackResult.Err` (`internal/oauth/config.go`, `internal/oauth/callback_parse.go`); test `TestCallback_DecisionBeforePage`
- [ ] T021 [US1] `exchangeAuthorizationCode` repeats the decision and uses `ProcessAuthorizationCallback` for discovered/configured (`internal/upstream/core/connection_oauth.go`); tests `TestMixUp_HonestIssRefusedInHostileFlow`, `TestLogin_IssAbsentNotAdvertised_UnprotectedFlag`, `TestLogin_ErrorResponseWrongIss`, AST guard `TestOAuthIssuerChecks_AllExchangePaths`
- [ ] T022 [US1] Codes `OAuthIssuerMismatch`, `OAuthIssuerChanged`, `OAuthMetadataIssuerMismatch` + catalog (`internal/diagnostics/codes.go`)

## Phase 6: User Story 4 — discovery issuer equality (PR-2, P2, FR-006–FR-008, plan D3)

- [ ] T023 [US4] Tests `TestDiscovery_Issuer_*`, `TestDiscovery_EscapedPathSiblingNotAccepted`, `TestMetadata_CrossOriginRedirectRefused`, `TestDiscovery_PolicyPartition_*` (`internal/oauth/discovery_issuer_test.go`)
- [ ] T024 [US4] `metadataCandidates` with issuer, equality in `findWorkingMetadataDoc`, `metadataCheckRedirect`, policy-keyed preflight cache, wrapper serves only its snapshot (`internal/oauth/discovery.go`, `internal/oauth/discovery_cache.go`, `internal/oauth/transport_wrapper.go`)

## Phase 7: User Stories 2 and 3 — issuer-bound rows, upgrade without re-login (PR-2, P1, FR-009–FR-013, plan D4)

- [ ] T025 [US2] Record fields `Issuer`, `DiscoveryTarget`, `IssuerSource`, `MixUpProtected` (`internal/storage/models.go`); test `TestRecordJSONUnchangedWithoutIssuer`
- [ ] T026 [US3] `storage.BindOAuthIssuer` CAS + `oauth.EnsureBound` (`internal/storage/oauth_binding.go`, `internal/oauth/persistent_token_store.go`); tests `TestUpgrade_LegacyRowsBindOnFirstUse_*`, `TestBindOAuthIssuer_RevisionRace`
- [ ] T027 [US2] Replace `WithRefreshProvenance` (T003) with the row's binding; `CredentialsIssuer` in strict mode; `SendSnapshot` (`internal/upstream/core/oauth_refresh.go`, `internal/oauth/config.go`); tests `TestSwap_NoCredentialReachesNewAS_*`
- [ ] T028 [US2] Static-client provenance bucket `oauth_static_client_bindings`, `EffectiveExtras`, `ClassifyExtraParam` (`internal/storage/`, `internal/oauth/`); tests `TestStaticProvenance_*`, `TestClassifyExtraParam_PrecedenceAndAliases`
- [ ] T029 [US2] `CredentialCheckRedirect` on all credential clients + AST guard (`internal/oauth/`, `internal/upstream/core/client.go`); tests `TestCredentialClients_*`, `TestCredentialClientsHaveCheckRedirect`
- [ ] T030 [US2] Issuer-transition DCR write and login CAS on the OAuth write counter (`internal/storage/oauth_binding.go`, `internal/upstream/core/oauth_stale_dcr_client.go`); tests `TestDCRWrite_IssuerTransitionDifferentID_Concurrent`, `TestLogin_DelayedAResultAfterBLogin_Discarded`

## Phase 8: User Story 6 — per-server opt-out (PR-2, P2, FR-014–FR-015, plan D6)

- [ ] T031 [US6] `oauth.skip_issuer_validation` at the five wiring points, excluded from `OAuthConfigChanged`, PATCH accepts it alone (`internal/config/config.go`, `internal/oauth/serverfields.go`, `oas/`, `frontend/src/`, `native/macos/MCPProxy/`); test `TestFR015_*`
- [ ] T032 [US6] Resolver and callback skip branches (`internal/oauth/issuer.go`, `internal/oauth/issuer_resolver.go`); tests `TestSkip_*`

---

## Phase 9: User Story 7 — broker `iss` (PR-3, P3, plan D6)

- [ ] T033 [US7] `AuthBrokerConfig.Issuer` → `ConnectorConfig.Issuer`; decision before the error branch (`internal/config/`, `internal/serveredition/broker/oauth_connector.go`, `internal/serveredition/api/credential_handlers.go`); tests `TestBroker_Iss_{Set,Unset,ErrorWithWrongIss}` (`-tags server`)

## Phase 10: Surfaces and polish (PR-3, FR-017–FR-018, plan D7)

- [ ] T034 [P] Doctor `oauth_issuer_status` + `GET /api/v1/diagnostics` + golden (`internal/management/`, `contracts/doctor-issuer-binding.json`)
- [ ] T035 [P] Server projection fields (issuer origin, source, `mix_up_protected`) in REST, `auth status`, Web UI ServerDetail, macOS detail (`internal/contracts/`, `cmd/mcpproxy/`, `frontend/src/views/ServerDetail.vue`, `native/macos/MCPProxy/`)
- [ ] T036 [P] Error pages `docs/errors/MCPX_OAUTH_ISSUER_MISMATCH.md`, `…_ISSUER_CHANGED.md`, `…_METADATA_ISSUER_MISMATCH.md` + on-disk assertion (`internal/diagnostics/catalog_test.go`)
- [ ] T037 `scripts/test-oauth-client-hardening-e2e.sh` with the test server as a helper binary (`tests/oauthserver/cmd/`)
- [ ] T038 Real-provider check (`verification/providers.md`), SC-004
- [ ] T039 CLAUDE.md "Recent Changes" entry at merge

## Dependencies

- PR-1 (T001–T014) first; T003 is replaced by T027.
- PR-2: Phase 4 → Phases 5–8 (US1, US4, US2/US3 share the resolver and snapshot; US6 last because it wraps every check).
- PR-3 after PR-2 (surfaces read the stored binding and codes).

## Implementation strategy

PR-1 changes behaviour only for FR-001 and FR-016. PR-2 is the first PR that can refuse a provider that works today, so it ships with the opt-out (US6). PR-3 adds the surfaces, broker and e2e.
