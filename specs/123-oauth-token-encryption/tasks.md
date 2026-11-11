# Tasks: Encrypt Upstream OAuth Tokens at Rest

**Input**: [spec.md](spec.md), [plan.md](plan.md)

**Format**: `[ID] [P?] [Story] Description`. `[P]` = can run in parallel with other `[P]` tasks in the same phase (different files). Stories: US1 (enable and protect), US2 (missing/wrong key loses nothing), US3 (bad key reference), US4 (disable/downgrade), US5 (docs).

**TDD rule**: in each phase the "Tests first" tasks are written and committed **failing** against the parent commit before any implementation task of that phase. A test that passes before its implementation is vacuous and must be redesigned (for the raw-file scan, the no-key control proves the scan can see a marker).

**Scope rule**: one PR (branch `123-oauth-token-encryption` from `main`). Anything in spec.md "Out of Scope" is not done here; review findings that belong there are added to that list.

---

## Phase 1: `internal/tokenseal` leaf package (FR-001..006)

### Tests first

- [ ] T001 [P] [US1] `internal/tokenseal/tokenseal_test.go`: round trip; 1 000 seals of the same plaintext give 1 000 distinct nonces and ciphertexts; envelope matches `^enc:v1:[0-9a-f]{16}:[A-Za-z0-9+/=]+$`; `Classify` table (`{…}` → plaintext, `enc:v1:` → sealed + kid, `enc:v2:` → unsupported, empty / other → malformed)
- [ ] T002 [P] [US2] `internal/tokenseal/open_errors_test.go`: `Open` with nil key → `no_key`; other key → `key_mismatch` with both kids; same key but other bucket key, other bucket name, or kid edited in the prefix → `auth_failed`; truncated payload, bad base64 → `malformed`; `enc:v2:` → `unsupported_version`
- [ ] T003 [P] [US3] `internal/tokenseal/key_test.go`: `ParseKey` table (32 bytes std base64 with and without padding, surrounding whitespace and one trailing newline accepted; 31/33 bytes, URL alphabet with `-`/`_`, empty, non-base64 rejected with a fixed message containing no input); HKDF known-answer vectors for the record key and kid; `fmt.Sprintf("%v|%+v|%#v", k)` and `json.Marshal(k)` contain neither the raw key nor its base64
- [ ] T004 [P] [US1] `internal/tokenseal/leaf_test.go`: `go list -deps` shows only standard-library packages (pattern of `TestCommitviewIsLeaf`)

### Implementation

- [ ] T005 [US1] `internal/tokenseal/tokenseal.go`: `Key`, `ParseKey`, `ID`, `Seal`, `Open`, `Classify`, `Shape`, `Reason`, `OpenError` per plan D1-D3, D5 (makes T001-T004 pass)

## Phase 2: storage codec seam and fail-closed writers (FR-010..015)

### Tests first

- [ ] T010 [P] [US1] `internal/storage/oauth_seal_test.go`: with `WithOAuthTokenKey(k)`, every write method (`SaveOAuthToken`, `UpdateOAuthTokenIfBound`, `SaveOAuthClientIfBound` all modes, `UpdateOAuthToken`, `UpdateOAuthClientCredentials`, `SaveOAuthClientCredentialsIf*`, `ClearOAuthClient*`) leaves an `enc:v1` value whose kid is `k.ID()`; every read method returns the same record it would for a plaintext row; revision increases per write as today
- [ ] T011 [P] [US1] `internal/storage/oauth_plaintext_golden_test.go`: without a key, the bytes written for a token save, a DCR save and a refresh-style update equal `json.Marshal` of the record (SC-003)
- [ ] T012 [P] [US2] `internal/storage/oauth_undecryptable_test.go`: table over every mutator listed in FR-013 against an undecryptable row (no key / other key / swapped envelope): returns `ErrOAuthTokenUndecryptable` with the right reason and the raw bytes are unchanged; key-only deletes (`DeleteOAuthToken`, `ClearOAuthTokenForStandaloneLogout`, `ClearOAuthState`, the Spec 116 purge (`purgeServerTx`, driven through `purgetest`), commit `OAuthClears`) still remove the row; reads return the typed error, not "not found"
- [ ] T013 [P] [US2] `internal/storage/oauth_list_tolerant_test.go`: one undecryptable row and one plaintext row with invalid JSON among three good rows → `ListOAuthTokens` returns the three; `ListOAuthTokenRows` classifies all five; `CleanupOrphanedOAuthTokens` deletes a decodable orphan, keeps both bad rows (also when their key prefix names no configured server) and reports them
- [ ] T014 [P] [US1] `internal/storage/oauth_sweep_test.go`: `SealPlaintextOAuthTokens` seals plaintext rows only, in one transaction (an injected failure on the second row leaves the first plaintext), skips sealed-other-key and undecryptable rows, sets `meta/oauth_seal_compact_advised` only when it sealed ≥1 row, is a no-op without a key
- [ ] T015 [P] [US1] `internal/storage/oauth_codec_source_test.go`: `TestOAuthBucketValuesOnlyThroughCodec` (no `MarshalBinary`/`UnmarshalBinary` on `oauth_tokens` values outside the codec in `oauth_binding.go`) and `TestStorageOpenSitesPassOAuthKey` (every non-test `storage.NewManager(`/`storage.NewBoltDB(` call in the module passes `WithOAuthTokenKey`); both fail on `main`
- [ ] T016 [US1] `internal/storage/oauth_raw_file_test.go`: `TestOAuthTokensNotInRawFile` per plan test plan (markers for access token, refresh token, client secret; sweep → close → `bbolt.Compact` → markers absent from the compacted file; a login-style write with the key set never appears in the live file; control run without a key finds every marker)

### Implementation

- [ ] T020 [US1] `internal/storage/options.go`: `Option`, `WithOAuthTokenKey`; variadic `opts` on `NewBoltDB` and `NewManager`; `BoltDB.oauthKey`
- [ ] T021 [US1] `internal/storage/oauth_binding.go`: `encodeOAuthRecord`, `decodeOAuthRecord`, `ErrOAuthTokenUndecryptable`; `putOAuthRecord` / `getOAuthRecordTx` use them (makes T010, T011 pass)
- [ ] T022 [US2] `internal/storage/bbolt.go`: route the nine raw decode sites through `decodeOAuthRecord`; `SaveOAuthToken` reads first and refuses an undecryptable row; tolerant `ListOAuthTokens`; new `ListOAuthTokenRows`, `OAuthTokenRow` (makes T012, T013 pass)
- [ ] T023 [US2] `internal/storage/manager.go`: `CleanupOrphanedOAuthTokens` keeps and reports undecryptable rows; `Manager` wrappers for `ListOAuthTokenRows` and the sweep; `SealPlaintextOAuthTokens` + marker key constant in `models.go` (makes T013, T014 pass)
- [ ] T024 [US1] Pass `WithOAuthTokenKey` at every open site of plan "Open-site inventory" (temporarily with `nil`; Phase 3 supplies the key) so T015 passes; `internal/upstream/cli/client.go` returns a key error instead of falling back to in-memory storage
- [ ] T025 [US1] Make T016 pass (no code expected beyond T021-T023; if it fails, the gap is a missed write path)

## Phase 3: configuration and key resolution (FR-020..024)

### Tests first

- [ ] T030 [P] [US3] `internal/config/oauth_token_key_test.go`: `ResolveOAuthTokenKey` table (empty → nil; literal; `${file:}` via a temp file; `${env:}`; `MCPPROXY_OAUTH_TOKEN_ENCRYPTION_KEY`; `_FILE`; env beats field; both env → `ErrOAuthTokenKeyBothSet`; missing file, empty file, malformed key → the three fixed sentences); a grep over every returned error that it contains neither the temp path nor the key; `Config.Validate` fails on each bad input
- [ ] T031 [P] [US3] `cmd/mcpproxy/oauth_token_key_exit_test.go`: `classifyError` maps each resolution failure to `ExitCodeConfigError` (4)
- [ ] T032 [P] [US3] `internal/runtime/config_hotreload_fields_test.go` (existing canary goes red when the field is added) + `internal/runtime/oauth_token_key_restart_test.go`: editing the field is `RequiresRestart`, `pinRestartGated` keeps the running value, a save after reload writes the reference string unchanged
- [ ] T033 [P] [US3] redaction test (REST `GET /api/v1/config` and `mcpproxy config show`-style output) with a literal key → masked

### Implementation

- [ ] T034 [US3] `internal/config/config.go`: field `OAuthTokenEncryptionKey`; `Validate` calls `ResolveOAuthTokenKey`. `internal/config/oauth_token_key.go`: `ResolveOAuthTokenKey`, env constants, errors (makes T030, T031 pass)
- [ ] T035 [US3] `internal/runtime/config_hotreload.go` `restartGatedConfigFields` + `internal/runtime/restart_gated.go` `pinRestartGated` (makes T032 pass); redaction rule if T033 is red
- [ ] T036 [US1] Replace the `nil` keys of T024 with `config.ResolveOAuthTokenKey(cfg)` at every open site; `runtime.New` resolves once before `storage.NewManager` and keeps the key for doctor
- [ ] T037 [P] [US3] Settings parity skip list (`frontend/src/views/settings/fields.ts` comment beside `credential_encryption_key`, `scripts/check-settings-parity.py`); `make swagger` (`oas/swagger.yaml`, `oas/docs.go`)

## Phase 4: runtime boot, OAuth read paths and health (FR-016, FR-031)

### Tests first

- [ ] T040 [P] [US1] `internal/runtime/oauth_seal_boot_test.go`: with a key and plaintext rows, after boot every row is sealed, and a hook asserts the sweep ran before `CleanupOrphanedOAuthTokens`, the refresh manager start and the first upstream connect
- [ ] T041 [P] [US2] `internal/oauth/persistent_token_store_undecryptable_test.go`: `GetToken`, `HasPersistedTokenBound`, `GetPersistedRefreshToken` return the typed error; a login (`WithLoginClient` save of token + DCR) against an undecryptable row fails and leaves the bytes; refresh manager `Start` schedules the decodable servers and skips the undecryptable one
- [ ] T042 [P] [US2] `internal/health/calculator_oauth_undecryptable_test.go` + runtime server-list test: an undecryptable row yields `unhealthy`, action `configure`, the reason in the detail, code `MCPX_OAUTH_TOKEN_UNDECRYPTABLE`; never `login`
- [ ] T043 [P] [US2] `internal/diagnostics` catalog contract test fails until the new code has a registry entry and `docs/errors/MCPX_OAUTH_TOKEN_UNDECRYPTABLE.md`

### Implementation

- [ ] T044 [US1] Boot sweep call in runtime boot (`internal/runtime/lifecycle.go`, before `CleanupOrphanedOAuthTokens` at :76) with an info log of the count and kid (makes T040 pass)
- [ ] T045 [US2] `internal/oauth/persistent_token_store.go`, `internal/oauth/config.go`, `internal/oauth/refresh_manager.go`, `internal/upstream/core/connection_oauth.go`: propagate the typed error, wrap as `diagnostics.OAuthTokenUndecryptable` (makes T041 pass)
- [ ] T046 [US2] `internal/diagnostics/codes.go`, `registry.go`, `classifier.go`, `docs/errors/MCPX_OAUTH_TOKEN_UNDECRYPTABLE.md`; `internal/health/calculator.go` mapping; `internal/runtime/runtime.go:2839` authenticated path keeps the reason (makes T042, T043 pass)

## Phase 5: doctor and CLI (FR-030, FR-032)

### Tests first

- [ ] T050 [P] [US2] `internal/management/diagnostics_oauth_storage_test.go`: section counts for a mixed bucket; per-server reasons; warnings `literal_key`, `key_file_in_data_dir` (including through a symlink), `plaintext_rows_with_key`, `compaction_advised`; a scoped caller sees only its visible servers and no kid; no output contains key bytes or the reference path
- [ ] T051 [P] [US4] `cmd/mcpproxy/auth_tokens_cmd_test.go`: `status` text and `-o json`; `encrypt` and `--dry-run`; `decrypt --yes` (and refusal without `--yes`); no key → exit 4; locked DB → exit 3; exit 1 when rows remain in the unwanted state; after `decrypt`, every row decodes with plain `json.Unmarshal` (downgrade simulation)
- [ ] T052 [P] [US1] `cmd/mcpproxy/db_cmd_test.go`: `db compact` removes the marker from the compacted file and prints the retained-backup note only when the marker was set

### Implementation

- [ ] T053 [US2] `internal/contracts/types.go` `OAuthTokenStorageReport`; runtime accessor; `internal/management/diagnostics.go` fill + `RuntimeWarnings`; doctor CLI rendering; `make swagger` (makes T050 pass)
- [ ] T054 [US4] `cmd/mcpproxy/auth_tokens_cmd.go` under `auth` (makes T051 pass)
- [ ] T055 [US1] `cmd/mcpproxy/db_cmd.go` marker clear and note (makes T052 pass)

## Phase 6: documentation (FR-040..042)

- [ ] T060 [P] [US5] `docs/features/oauth-authentication.md`: fix lines 83, 216-224, 402 (remove the keyring table); link the new page
- [ ] T061 [P] [US5] New `docs/features/oauth-token-encryption.md` per plan "Docs plan"; add to the docs include list in `website/docusaurus.config.js` (if it enumerates pages) and `website/sidebars.js`
- [ ] T062 [P] [US5] `docs/configuration/config-file.md`, `docs/configuration/environment-variables.md`, `docs/configuration.md` tables
- [ ] T063 [P] [US5] `docs/docker-isolation.md:515`, `docs/features/docker-isolation.md:435`, `docs/features/sandbox-isolation.md:94,109` qualifiers; `docs/cli/command-reference.md` (`auth tokens`), `docs/cli/db-commands.md` (compaction note); `specs/023-oauth-state-persistence/plan.md:31` annotation; the `bbolt.go` comment that says the file holds OAuth tokens
- [ ] T064 [US5] Grep gate: `grep -rniE 'keyring' docs/features/oauth-authentication.md` returns no storage claim; probe the published URLs after merge

## Phase 7: verification

- [ ] T070 [US1] e2e step with the OAuth test fixture (`oauth-e2e-testing` setup): login with a key → raw scan of `config.db` → restart without key (health detail, bytes unchanged) → restart with key (authenticated, no browser) (US1, US2, SC-001, SC-002)
- [ ] T071 [US1] `BenchmarkGetOAuthToken{Plaintext,Sealed}` recorded in the PR (SC-005)
- [ ] T072 Gates: golangci-lint v2 bare and `--build-tags server`; `go test -race` over the packages in plan "Technical Context" and the CI `-tags server` lane; `./scripts/test-api-e2e.sh`; swagger verify; `scripts/check-settings-parity.py`; `scripts/check-upstream-write-sites.sh`
- [ ] T073 SC-004 sweep: grep all captured test logs and outputs for the test key bytes, its base64 and the temp key paths
- [ ] T074 Cross-model review rounds per the maintainer's reviewer ladder (crypto + storage change: request the hard-problem reviewer for Phase 1-2)

## Dependencies

Phase 1 → Phase 2 → Phase 3 (T036 needs T024) → Phase 4 → Phase 5. Phase 6 can start after Phase 3 fixes the field and env names. Phase 7 last. Within a phase, `[P]` test tasks are independent; implementation tasks run in listed order.
