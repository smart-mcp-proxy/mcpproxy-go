# Tasks: Encrypt Upstream OAuth Tokens at Rest

**Input**: [spec.md](spec.md), [plan.md](plan.md)

**Format**: `[ID] [P?] [Story] Description`. `[P]` = can run in parallel with other `[P]` tasks in the same phase (different files). Stories: US1 (enable and protect), US2 (no key / wrong key loses nothing), US3 (bad key reference), US4 (disable/downgrade), US5 (docs).

**TDD rule**: in each phase the "Tests first" tasks are written and committed **failing** against the parent commit before any implementation task of that phase. A test that passes before its implementation is vacuous and must be redesigned (for the raw-file scan, the no-key control proves the scan can see a marker).

**Scope rule**: three PRs (spec "Scope Boundary"), each a branch from `main`: **PR-1** = Phase 0; **PR-2** = Phases 1-4 plus Phase 6a; **PR-3** = Phase 5 plus Phase 6b. Phase 7 gates run on PR-2 and PR-3. Anything in spec.md "Out of Scope" is not done here; review findings that belong there are added to that list.

---

## PR-1

## Phase 0: docs correction with today's facts (FR-040, FR-041)

- [ ] T090 [P] [US5] `docs/features/oauth-authentication.md`: fix lines 83, 216-224, 402 per plan "Docs plan / PR-1" (tokens are readable JSON in `config.db`, protected by file permissions; remove the keyring table)
- [ ] T091 [P] [US5] `docs/docker-isolation.md:515`, `docs/features/docker-isolation.md:435`, `docs/features/sandbox-isolation.md:94,109`: state plainly that `config.db` holds the tokens in readable form; `specs/023-oauth-state-persistence/plan.md:31` annotation; the `bbolt.go` comment
- [ ] T092 [US5] Grep gate: `grep -rniE 'keyring' docs/features/oauth-authentication.md` returns no storage claim; probe the published URLs after merge

## PR-2

## Phase 1: `internal/tokenseal` leaf package (FR-001..006)

### Tests first

- [ ] T001 [P] [US1] `internal/tokenseal/tokenseal_test.go`: round trip; 1 000 seals of the same plaintext give 1 000 distinct nonces and ciphertexts; envelope matches `^enc:v1:[0-9a-f]{16}:[A-Za-z0-9+/=]+$`; `Classify` table (`{…}` → plaintext, `enc:v1:` → sealed + kid, `enc:v2:` → unsupported, empty / other → malformed); `Seal` rejects plaintext over 1 MiB and a bucket name or bucket key containing a NUL byte (`Open` likewise)
- [ ] T002 [P] [US2] `internal/tokenseal/open_errors_test.go`: `Open` with nil key → `no_key`; other key → `key_mismatch` with both kids; prefix kid edited to any value other than the configured kid → `key_mismatch` (no decrypt attempted); prefix kid edited to the configured kid, other bucket key, or other bucket name → `auth_failed`; damaged header or empty payload → `malformed` under every key state; precedence header → key state → payload, so characters outside the base64 alphabet or a `=` anywhere other than as the last one or two payload characters → `malformed` only with the matching key and `no_key` / `key_mismatch` under no key / another key; with the matching key, a payload of base64-alphabet characters with at most two trailing `=` that does not decode (wrong length for its padding) or is shorter than nonce + tag (truncation), and any GCM failure → `auth_failed`; explicit cases `AAAA=` → `auth_failed`, `AA=A` → `malformed`; `enc:v2:` → `unsupported_version`
- [ ] T003 [P] [US3] `internal/tokenseal/key_test.go`: `ParseKey` table (32 bytes std base64 with and without padding, surrounding whitespace and one trailing newline accepted; 31/33 bytes, URL alphabet with `-`/`_`, empty, non-base64 rejected with a fixed message containing no input); HKDF known-answer vectors for the record key and kid; `fmt.Sprintf("%v|%+v|%#v", k)` and `json.Marshal(k)` contain neither the raw key nor its base64 (`String`/`GoString` print the key id only)
- [ ] T004 [P] [US1] `internal/tokenseal/leaf_test.go`: `go list -deps` shows only standard-library packages (pattern of `TestCommitviewIsLeaf`)

### Implementation

- [ ] T005 [US1] `internal/tokenseal/tokenseal.go`: `Key`, `ParseKey`, `ID`, `Seal`, `Open`, `Classify`, `Shape`, `Reason`, `OpenError` per plan D1-D3, D5 (makes T001-T004 pass)

## Phase 2: storage codec seam and fail-closed writers (FR-010..016)

### Tests first

- [ ] T010 [P] [US1] `internal/storage/oauth_seal_test.go`: with `WithOAuthTokenKey(k)`, every write method (`SaveOAuthToken`, `UpdateOAuthTokenIfBound`, `SaveOAuthClientIfBound` all modes, `UpdateOAuthToken`, `UpdateOAuthClientCredentials`, `SaveOAuthClientCredentialsIf*`, `ClearOAuthClient*`) leaves an `enc:v1` value whose kid is `k.ID()`; every read method returns the same record it would for a plaintext row; revision increases per write as today; a write over a plaintext row sets `meta/oauth_seal_compact_advised` in the same transaction, a write over a sealed row does not
- [ ] T011 [P] [US1] `internal/storage/oauth_plaintext_golden_test.go`: without a key, the bytes written for a token save, a DCR save and a refresh-style update equal `json.Marshal` of the record (SC-003)
- [ ] T012 [P] [US2] `internal/storage/oauth_undecryptable_test.go`: one table over every mutator listed in FR-013 (including the unconditional ones with no production caller) against an undecryptable row (no key / other key / swapped envelope): returns `ErrOAuthTokenUndecryptable` with the right reason and the raw bytes are unchanged (exception, FR-013: with no key, `SaveOAuthToken` overwrites a corrupt non-`enc:` value as before the feature, while any `enc:` row is always refused, and with a key every undecodable row is refused); key-only deletes (`DeleteOAuthToken`, `ClearOAuthTokenForStandaloneLogout`, `ClearOAuthState`, the Spec 116 purge (`purgeServerTx`, driven through `purgetest`), commit `OAuthClears`) still remove the row; reads return the typed error, not "not found"
- [ ] T013 [P] [US2] `internal/storage/oauth_list_tolerant_test.go`: one undecryptable row and one plaintext row with invalid JSON among three good rows → `ListOAuthTokens` returns the three; `ListOAuthTokenRows` puts every row in exactly one class (`plaintext | sealed | undecryptable`) with `key_mismatch` as a reason only; `CleanupOrphanedOAuthTokens` deletes a decodable orphan, keeps both bad rows (also when their key prefix names no configured server) and reports them
- [ ] T014 [P] [US1] `internal/storage/oauth_sweep_test.go`: seed several hundred ~1 KiB plaintext rows (spanning many leaf pages) plus sealed and undecryptable rows; a plaintext row with invalid JSON and one over the 1 MiB `Seal` bound are skipped with a warning (observed logger: bucket key and reason `malformed` / `too_large`, never the value) and left unchanged while the other plaintext rows are sealed; `SealPlaintextOAuthTokens` leaves zero valid plaintext rows, touches no sealed or undecryptable row, and sets the marker only when it sealed ≥1 row; with `putOAuthRecordHook` failing on the second write the whole transaction rolls back (first row still plaintext); no-op without a key
- [ ] T015 [P] [US1] `internal/storage/oauth_codec_source_test.go`: `TestOAuthBucketValuesOnlyThroughCodec` per plan D6 (stdlib `go/parser` + `go/types`, receiver type `OAuthTokenRecord`; bucket access on `OAuthTokenBucket` only in the named exemptions; `UpstreamRecord`/`OAuthCompletionEvent` calls in `oauth_binding.go` not flagged) and `TestStorageOpenSitesPassOAuthKey` (every non-test `storage.NewManager(`/`storage.NewBoltDB(` call in the module passes `WithOAuthTokenKey`; the delete-only standalone `auth logout` open passes an explicit nil, named as the one exemption); both fail on `main`
- [ ] T016 [US1] `internal/storage/oauth_raw_file_test.go`: `TestOAuthTokensNotInRawFile` per plan test plan (markers for access token, refresh token, client secret; sweep → close → `bbolt.Compact` → markers absent from the compacted file; a login-style write with the key set never appears in the live file; control run without a key finds every marker)

### Implementation

- [ ] T020 [US1] `internal/storage/options.go`: `Option`, `WithOAuthTokenKey`; variadic `opts` on `NewBoltDB` and `NewManager`; `BoltDB.oauthKey`
- [ ] T021 [US1] `internal/storage/oauth_binding.go`: `encodeOAuthRecord`, `decodeOAuthRecord`, `ErrOAuthTokenUndecryptable`; `putOAuthRecord(bucket, k, key, rec)` with the explicit key parameter, the plaintext→envelope marker write and `putOAuthRecordHook`; `getOAuthRecordTx` takes the key (makes T010, T011 pass)
- [ ] T022 [US2] `internal/storage/bbolt.go`: route the eight raw decode sites (plan D6) through `decodeOAuthRecord`; `SaveOAuthToken` reads first and refuses an undecryptable row; tolerant `ListOAuthTokens`; new `ListOAuthTokenRows`, `OAuthTokenRow` (makes T012, T013 pass)
- [ ] T023 [US2] `internal/storage/manager.go`: `CleanupOrphanedOAuthTokens` keeps and reports undecryptable rows; `Manager` wrappers for `ListOAuthTokenRows` and the sweep; `SealPlaintextOAuthTokens` (collect, then write) + marker key constant in `models.go` (makes T013, T014 pass)
- [ ] T024 [US1] Pass `WithOAuthTokenKey(nil)` at every open site of plan "Open-site inventory" (Phase 3 supplies the key) so T015 passes; no behaviour change yet
- [ ] T025 [US1] Make T016 pass (no code expected beyond T021-T023; if it fails, the gap is a missed write path)

## Phase 3: configuration and key resolution (FR-020..024)

### Tests first

- [ ] T030 [P] [US3] `internal/config/oauth_token_key_test.go`: `ResolveOAuthTokenKey` table (empty → nil; literal; `${file:}` via a temp file; `${env:}`; `MCPPROXY_OAUTH_TOKEN_ENCRYPTION_KEY`; `_FILE`; env beats field; both env → `ErrOAuthTokenKeyBothSet`; unresolvable (missing file, unset env var, absent keyring entry), unusable (oversized, NUL, not a regular file, relative path), empty, malformed key → the four fixed sentences as `*StartupError` with exit 4, plus the both-set and bad-reference errors; only the unresolvable sentence is the one the standalone `auth logout` exception keys on); returned `KeySource` kind for each row and `InDataDir` true for a path under `data_dir` reached through a symlink; a grep over every returned error that it contains neither the temp path nor the key; `Config.Validate` accepts a well-formed reference to a missing file and rejects a malformed reference
- [ ] T031 [P] [US3] `cmd/mcpproxy/oauth_token_key_exit_test.go`: `classifyError` maps each resolution failure to `ExitCodeConfigError` (4) through `errors.As(*config.StartupError)`, with an error message chosen so the string heuristics alone would not match
- [ ] T032 [P] [US3] `internal/runtime/config_hotreload_fields_test.go` (existing canary goes red when the field is added) + `internal/runtime/oauth_token_key_restart_test.go`: editing the field is `RequiresRestart`, `pinRestartGated` keeps the running value, a save after reload writes the reference string unchanged; the key file is deleted after start and an unrelated hot-reload edit still applies
- [ ] T033 [P] [US3] `internal/oauth` redaction test: `RedactedConfig` masks a literal key, also with `reveal_secret_headers` on; `${file:}`, `${env:}` and `${keyring:}` references come back verbatim (FR-024); a GET → `/api/v1/config/apply` round trip of the masked document leaves the stored field unchanged; a whitespace-spelled reference such as `${ file:/abs/path}`, which validation accepts, comes back as written while a non-reference is masked (one shared recogniser); no output contains the key value, key bytes or key-source contents (the public key id and key-source metadata are allowed)
- [ ] T038 [P] [US3] `cmd/mcpproxy` test: with an unreadable `${file:}` key reference, `upstream list` and `doctor` (socket path) run and exit 0; `call`/`auth login` (storage-open) exit 4 and the DB bytes are unchanged; `db compact` and `db stats` run without resolving the key, also with both environment variables set; standalone `auth logout --server=<server>` with an unresolvable source (missing key file, unset env var, absent keyring entry) still deletes the row, while both env vars set, a bad reference and an unusable (oversized, NUL, not a regular file, relative path), empty or malformed value exit 4

### Implementation

- [ ] T034 [US3] `internal/config/config.go`: field `OAuthTokenEncryptionKey`; `Validate` does the syntax-only check. `internal/config/oauth_token_key.go`: `ResolveOAuthTokenKey`, `KeySource`, env constants, `ExitCodeOAuthTokenKeyError = 4`, errors as `NewStartupError` (makes T030, T031 pass)
- [ ] T035 [US3] `internal/runtime/config_hotreload.go` `restartGatedConfigFields` + `internal/runtime/restart_gated.go` `pinRestartGated` (makes T032 pass); redaction code (a field-specific rule: literal always masked, references verbatim, including `${file:}`) as T033 requires
- [ ] T036 [US1] Replace the `nil` keys of T024 with the resolved key at every open site; `runtime.New` resolves once before `storage.NewManager` and keeps key + `KeySource`; shared `openStorageForCLI(cfg)` helper in `cmd/mcpproxy`; `internal/upstream/cli/client.go` returns a resolution error instead of falling back to in-memory storage, except for the delete-only standalone `auth logout` path, which opens with a nil key when the key source is missing or unreadable (file absent, env var unset, keyring entry absent); a `NewBoltDB` error after successful resolution keeps the existing fallback (makes T038 pass)
- [ ] T037 [P] [US3] Confirm no Settings row on either side (`fields.ts`, `SettingsCatalog.swift`; `scripts/check-settings-parity.py` needs no change); `make swagger` (`oas/swagger.yaml`, `oas/docs.go`)

## Phase 4: boot sweep, OAuth read paths and health (FR-016, FR-031)

### Tests first

- [ ] T040 [P] [US1] `internal/runtime/oauth_seal_boot_test.go`: with a key and plaintext rows, after `runtime.New` every valid plaintext row is sealed, and a hook asserts the sweep ran (after the shutdown marker is armed) before the upstream manager, the refresh manager and every other OAuth consumer were constructed; a transaction-level sweep failure (fault hook) makes `runtime.New` close storage and return an error
- [ ] T041 [P] [US2] `internal/oauth/persistent_token_store_undecryptable_test.go`: `GetToken` returns the typed error (not `transport.ErrNoToken`); `HasPersistedTokenBound` and `GetPersistedRefreshToken` return it through their new error results; a login (`WithLoginClient` save of token + DCR) against an undecryptable row fails and leaves the bytes; refresh manager `Start` schedules the decodable servers and skips the undecryptable one
- [ ] T047 [P] [US2] `internal/upstream` tests over the plan D8 inventory: for each site, an undecryptable row produces the typed error / `OAuthTokenUndecryptable` last error and never starts an OAuth flow (`connection_oauth.go` precheck paths, `client.go:852`, `manager.go:3000`, `manager.go:3547`); plus the read-path inventory source test (fails when a new production caller of `GetOAuthToken*`/`HasPersistedToken*`/`GetPersistedRefreshToken` is not listed)
- [ ] T042 [P] [US2] `internal/health/calculator_oauth_undecryptable_test.go` + runtime server-list test: an undecryptable row yields `unhealthy`, action `configure`, the reason in the detail, code `MCPX_OAUTH_TOKEN_UNDECRYPTABLE`; never `login`, also when the server is connected (anonymous connection, a later call needs authentication); a connected server that needs no login stays healthy and the static-header exclusion still applies
- [ ] T043 [P] [US2] `internal/diagnostics` catalog contract test fails until the new code has a registry entry and `docs/errors/MCPX_OAUTH_TOKEN_UNDECRYPTABLE.md`
- [ ] T056 [P] [US2] login refusal test (PR-2; the richer pre-flight UX stays in PR-3): `mcpproxy auth login --server=<server>` and the REST login trigger and the connection path against an undecryptable row return `MCPX_OAUTH_TOKEN_UNDECRYPTABLE` and make no HTTP request to the authorization server or DCR endpoint (fixture counts requests)

### Implementation

- [ ] T044 [US1] Boot sweep call in `runtime.New` after `storage.NewManager` and after the shutdown marker is armed (`ArmShutdownMarker`, an intentional preceding operation), before any OAuth consumer is constructed, with an info log of the count and kid; a sweep error closes storage and is returned (startup refuses), per-row unsealable rows are skipped with a warning (makes T040 pass)
- [ ] T045 [US2] `internal/oauth/persistent_token_store.go`, `internal/oauth/config.go` (`HasPersistedTokenBound` / `HasPersistedToken` / `GetPersistedRefreshToken` signature changes), `internal/oauth/refresh_manager.go`, `internal/upstream/core/connection_oauth.go`, `internal/upstream/core/client.go`, `internal/upstream/core/oauth_refresh.go`, `internal/upstream/core/oauth_refresh_replay.go`, `internal/upstream/manager.go`: propagate the typed error per plan D8, wrap as `diagnostics.OAuthTokenUndecryptable` (makes T041, T047 pass)
- [ ] T046 [US2] `internal/diagnostics/codes.go`, `registry.go`, `classifier.go`, `docs/errors/MCPX_OAUTH_TOKEN_UNDECRYPTABLE.md` (+ `docs/errors/README.md`, `website/sidebars.js` OAuth error group); `internal/health/calculator.go` mapping (an undecryptable row wins over `Connected`, never `login`) and the runtime server-list path that today suppresses the diagnostic for connected servers; `internal/runtime/runtime.go:2839` authenticated path keeps the reason (makes T042, T043 pass)
- [ ] T057 [US2] Login refusal guard (read the row first, refuse before discovery, DCR or callback server) in `internal/upstream/cli` login, the REST login handler and the connection path (makes T056 pass; PR-2)

## Phase 6a: feature documentation (FR-041, FR-042)

- [ ] T061 [P] [US5] New `docs/features/oauth-token-encryption.md` per plan "Docs plan / PR-2" (including which delivery protects against disk/VM snapshots, i.e. only where the captured storage does not hold the means to recover the key: TPM-bound credential, external secret manager, key on another volume; and which only against copies of `config.db`, notably a host-key-only `LoadCredentialEncrypted=` credential, a plain `LoadCredential=` file and a same-volume Compose secret); add it to `website/sidebars.js` next to `features/oauth-authentication` (the `features/**` glob in `docusaurus.config.js` already publishes it); link it from `docs/features/oauth-authentication.md`
- [ ] T062 [P] [US5] `docs/configuration/config-file.md`, `docs/configuration/environment-variables.md`, `docs/configuration.md` tables
- [ ] T063 [P] [US5] Extend the PR-1 isolation-page and OAuth-page sentences with the encryption condition and the same-user caveat

## PR-3

## Phase 5: doctor, CLI, compaction and richer login pre-flight UX (FR-030, FR-032)

### Tests first

- [ ] T050 [P] [US2] `internal/management/diagnostics_oauth_storage_test.go`: section counts for a mixed bucket form a partition that sums to the row total, with `undecryptable_by_reason`; per-server reasons; warnings `literal_key`, `key_file_in_data_dir`, `plaintext_rows_with_key`, `compaction_advised`, `backup_file_present` computed from the runtime's `KeySource` and data dir; a scoped caller sees only its visible servers and no kid; no output contains key bytes or the reference path
- [ ] T051 [P] [US4] `cmd/mcpproxy/auth_tokens_cmd_test.go`: `status` text and `-o json`; `encrypt` and `--dry-run`; `decrypt --yes` (and refusal without `--yes`); no key → exit 4; locked DB → exit 3; exit 1 when rows remain in the unwanted state; config reference unreadable while `MCPPROXY_OAUTH_TOKEN_ENCRYPTION_KEY_FILE` supplies the key → works; several hundred rows spanning many leaf pages all converted; after `decrypt`, every row decodes with plain `json.Unmarshal` (downgrade simulation)
- [ ] T052 [P] [US1] `cmd/mcpproxy/db_cmd_test.go`: `db compact` with the default `--keep-backup=true` keeps the marker and prints the `config.db.bak` note; `--keep-backup=false` removes the marker; doctor shows `backup_file_present` until the `.bak` is deleted

### Implementation

- [ ] T053 [US2] `internal/contracts/types.go` `OAuthTokenStorageReport`; runtime accessor (key, `KeySource`, rows, marker, `.bak` presence); `internal/management/diagnostics.go` fill + `RuntimeWarnings`; doctor CLI rendering; `make swagger` (makes T050 pass)
- [ ] T054 [US4] `cmd/mcpproxy/auth_tokens_cmd.go` under `auth`, through `openStorageForCLI` (makes T051 pass)
- [ ] T055 [US1] `cmd/mcpproxy/db_cmd.go` marker handling and note (makes T052 pass)

## Phase 6b: surface documentation

- [ ] T064 [P] [US5] `docs/features/oauth-token-encryption.md`: doctor output, downgrade / disable / rotation with the offline key form (plan "Docs plan / PR-3"; rotation = decrypt with the old key, change the key, encrypt, then `db compact --keep-backup=false`, delete any existing `config.db.bak`, warn about copies made during the plaintext interval)
- [ ] T065 [P] [US5] `docs/cli/command-reference.md` (`auth tokens`, the doctor section), `docs/api/rest-api.md` (`oauth_token_storage` in `GET /api/v1/diagnostics`), `docs/cli/db-commands.md` (compaction and backup note)

## Phase 7: verification (PR-2 and PR-3)

- [ ] T070 [US1] (PR-2) e2e step with the OAuth test fixture (`oauth-e2e-testing` setup): login with a key → raw scan of `config.db` → restart with the key reference removed (health detail, bytes unchanged) → restart with a missing key file (exit 4, bytes unchanged) → restart with key (authenticated, no browser) (US1, US2, US3, SC-001, SC-002)
- [ ] T071 [US1] (PR-2) `BenchmarkGetOAuthToken{Plaintext,Sealed}` mean ns/op and allocs/op recorded in the PR (SC-005, informational)
- [ ] T072 Gates: golangci-lint v2 bare and `--build-tags server`; `go test -race` over the packages in plan "Technical Context" and the CI `-tags server` lane; `./scripts/test-api-e2e.sh`; swagger verify; `scripts/check-settings-parity.py`; `scripts/check-upstream-write-sites.sh`
- [ ] T073 SC-004 sweep: grep all captured test logs, errors, CLI and REST outputs for the test key bytes, its base64 and the key-file contents; the public key id and key-source metadata are allowed (also assert that `GET /api/v1/secrets/config` reports presence only for a `${env:}` / `${keyring:}` key reference and returns no value, and that daemon key resolution for use happens once; the config file itself intentionally keeps a literal key or the original `${...}` reference; assert only that a save never replaces a reference with resolved key material, that `GET /api/v1/config` masks a literal key even with `reveal_secret_headers` and shows a reference as written; an operator-written `${file:}` path is not a violation, resolved values and key-file contents are)
- [ ] T074 Cross-model review rounds per the maintainer's reviewer ladder (crypto + storage change: request the hard-problem reviewer for Phases 1-2)

## Dependencies

PR-1 is independent and merges first. PR-2: Phase 1 → Phase 2 → Phase 3 (T036 needs T024) → Phase 4; Phase 6a can start once Phase 3 fixes the field and env names. PR-3 branches from `main` after PR-2 merges: Phase 5, then Phase 6b. Phase 7 per PR. Within a phase, `[P]` test tasks are independent; implementation tasks run in listed order.
