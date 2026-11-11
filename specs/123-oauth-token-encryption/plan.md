# Implementation Plan: Encrypt Upstream OAuth Tokens at Rest

**Branch**: `123-oauth-token-encryption` | **Date**: 2026-11-11 | **Spec**: [spec.md](spec.md) | **Issue**: #1697

## Summary

A new stdlib-only leaf package `internal/tokenseal` seals and opens a byte slice as an `enc:v1` envelope (AES-256-GCM, random 96-bit nonce, HKDF-derived record key and key id, AAD = version + kid + bucket name + bucket key). `internal/storage` gets one encode and one decode function for `oauth_tokens` values and routes the write seam (`putOAuthRecord`) and every value read through them. The key comes from a new top-level config field, `oauth_token_encryption_key`, resolved once at config load through the existing secret resolver (so `${file:}` works today), overridable by `MCPPROXY_OAUTH_TOKEN_ENCRYPTION_KEY[_FILE]`, restart-gated, and passed to storage as an open option at every open site. Undecryptable rows become a typed error that every writer refuses and that doctor, health and a new diagnostics code report. The daemon seals leftover plaintext rows once at boot; offline `mcpproxy auth tokens status|encrypt|decrypt` cover inspection, migration and rollback. Docs are corrected.

One PR. It changes storage, config, runtime boot, oauth read paths, management doctor, diagnostics, `cmd/mcpproxy`, OAS and docs. No Web UI or Swift code changes beyond the Settings parity skip list.

## Technical Context

**Language/Version**: Go 1.26.
**Primary Dependencies**: standard library only (`crypto/aes`, `crypto/cipher`, `crypto/hkdf`, `crypto/rand`, `crypto/sha256`, `encoding/base64`). **No new dependencies.**
**Storage**: bbolt `config.db`, existing `oauth_tokens` bucket (values change shape when a key is set), existing `meta` bucket gets one marker key. No new bucket, no schema migration.
**Testing**: `go test -race ./internal/tokenseal/... ./internal/storage/... ./internal/oauth/... ./internal/config/... ./internal/runtime/... ./internal/management/... ./internal/diagnostics/... ./cmd/mcpproxy/...`; the CI `-tags server` lane; `./scripts/test-api-e2e.sh`; `make swagger` + swagger verify; `scripts/check-settings-parity.py`; `scripts/check-upstream-write-sites.sh`; golangci-lint v2 bare and `--build-tags server`.
**Target Platform**: both editions, Linux / macOS / Windows (pure Go).
**Performance**: SC-005. AES-GCM on a sub-4 KiB record is single-digit microseconds; the AEAD is constructed once per open, not per read.

## Decisions (with alternatives rejected)

### D1. Envelope format and versioning

`enc:v1:<kid>:<payload>` as ASCII bytes in the bbolt value. `<kid>` = 16 lower-case hex chars; `<payload>` = `base64.StdEncoding(nonce[12] || ciphertext || tag[16])`. A plaintext row is recognised by its first byte `{` (every `json.Marshal` of the struct starts with it); a sealed row by the `enc:` prefix. `enc:` with any version but `v1` is `unsupported_version`, so a future `v2` row read by this binary is reported, not misparsed.

Rejected:
- **Binary envelope** (magic byte + version byte + raw bytes). Smaller, but not distinguishable by eye in `bbolt` dumps, and the text form makes the raw-file grep test and operator inspection trivial. The size cost (~33%) is irrelevant at tens of rows.
- **No version prefix, as the broker store does** (`nonce||ct`). It cannot be told apart from corruption or evolved later.
- **A new bucket `oauth_tokens_v2`**. An older binary would see an empty bucket instead of decode errors, which is a nicer downgrade, but it needs a migration and changes to five key-only purge sites (`purge.go:111`, `purge.go:271`, `server_commit.go:173`, `clearOAuthStateTx`, `purgetest.go:168`) and the Spec 116 purge-set contract. Same-bucket envelopes leave the purge set untouched (it is key-only) and the downgrade path is an explicit `decrypt` (D9).

### D2. AEAD, key derivation and nonce handling

AES-256-GCM via `crypto/cipher.NewGCM` (hardware-accelerated, same primitive as the broker store, already reviewed in Spec 024). The configured 32 bytes are input keying material for HKDF-SHA256 (`crypto/hkdf`, stdlib since Go 1.24), no salt:
- record key = `HKDF(ikm, info="mcpproxy oauth_tokens record key v1", 32)`,
- kid = first 8 bytes of `HKDF(ikm, info="mcpproxy oauth_tokens key id v1", 8)`, hex.

Nonce = 12 bytes from `crypto/rand` per seal. With one key, the write volume (rows × refreshes, well under 10⁶ over a key's life) is far below the 2³² random-nonce bound.

Rejected:
- **Using the configured key directly** (broker style). A key reused for `credential_encryption_key` would then be the same AES key for two stores; HKDF separates them for free.
- **Counter nonces**. They need persisted state, and restoring a backup or copying the file would rewind the counter and reuse nonces, which breaks GCM completely.
- **XChaCha20-Poly1305** (`golang.org/x/crypto`). Larger nonce space we do not need; would be a new dependency for this package.
- **AES-GCM-SIV**. Not in the standard library.
- **A KDF from a passphrase** (scrypt/argon2). The key is machine-delivered random material, not a human password.

### D3. AAD binding

`AAD = "mcpproxy-oauth-token" 0x00 "v1" 0x00 kid 0x00 "oauth_tokens" 0x00 bucketKey`. The bucket key (`<server>_<16 hex>`, or a legacy bare name) is what every reader looks up, so swapping two rows' values, or copying a row under another key, fails `Open` as `auth_failed`. Kid and version in the AAD make the plaintext prefix tamper-evident.

Rejected: binding the display name or `rec.ServerName` from inside the record (that is the plaintext we are protecting, and not what the reader asked for); binding the server's incarnation or OAuth epoch (Spec 116) — they change on remove/re-create and on OAuth-config changes, which already delete the row, and the epoch is checked after decode by `OAuthRecordVisible`, so binding it would add a failure mode without a threat it stops.

### D4. What is sealed

The whole `json.Marshal(OAuthTokenRecord)` output, including `Revision` (assigned before marshal by `putOAuthRecord`) and `OAuthEpoch`. Identity for classification of an undecryptable row comes from the bucket key.

Rejected: **field-level encryption** of only `access_token`, `refresh_token`, `client_secret`. It would let status and orphan cleanup run without the key, but it leaves which accounts, scopes, client ids and expiry times exist readable, needs three envelopes per row, and keeps a JSON shape an older binary would half-read (tokens as garbage strings, which it would send upstream). Whole-record sealing fails cleanly instead.

### D5. Where the code lives

New package `internal/tokenseal` (no build tags, imports only the standard library; a `go list -deps` leaf test like `TestCommitviewIsLeaf`):

```go
type Key struct{ /* aead cipher.AEAD; kid string */ }
func ParseKey(b64 string) (*Key, error)            // trims space, std base64 (padding optional), exactly 32 bytes
func (k *Key) ID() string                           // 16 hex
func (k *Key) Seal(bucket, bucketKey string, plaintext []byte) ([]byte, error)
func (k *Key) Open(bucket, bucketKey string, value []byte) ([]byte, error) // nil Key allowed: classifies
func Classify(value []byte) (Shape, kid string)     // ShapePlaintext | ShapeSealedV1 | ShapeUnsupported | ShapeMalformed
type OpenError struct{ Reason Reason; RowKID, KeyKID string } // Reason: no_key|key_mismatch|auth_failed|unsupported_version|malformed
```

`Key` has no exported field, no `String`/`GoString`/`MarshalJSON` that could print material (a test asserts `fmt.Sprintf("%v %+v %#v", k)` contains no key byte and no base64 of it).

Rejected: putting it in `internal/secret` (that package is about resolving references and pulls the keyring dependency into `internal/storage`); moving the broker's helpers now (out of scope 2: its stored format has no AAD and no prefix and must not change; a later PR can make it call `tokenseal` primitives for its own format).

### D6. Storage seam

`BoltDB` gains an unexported `oauthKey *tokenseal.Key`, set by a functional option at open:

```go
type Option func(*openOptions)
func WithOAuthTokenKey(k *tokenseal.Key) Option     // nil = encryption off
func NewBoltDB(dataDir string, logger *zap.SugaredLogger, opts ...Option) (*BoltDB, error)
func NewManager(dataDir string, logger *zap.SugaredLogger, opts ...Option) (*Manager, error)
```

Two functions in `oauth_binding.go`:

- `encodeOAuthRecord(k *tokenseal.Key, key string, rec *OAuthTokenRecord) ([]byte, error)` — `rec.MarshalBinary()`, then `k.Seal(OAuthTokenBucket, key, …)` when `k != nil`.
- `decodeOAuthRecord(k *tokenseal.Key, key string, data []byte) (*OAuthTokenRecord, error)` — `Classify`; plaintext → `UnmarshalBinary`; sealed → `k.Open` then `UnmarshalBinary`; any failure → `*ErrOAuthTokenUndecryptable{Key, Reason, RowKID, KeyKID}` (`errors.Is(err, ErrOAuthTokenUndecryptableSentinel)`).

`putOAuthRecord` and `getOAuthRecordTx` take the key (they are package functions called with a bucket today; they become methods or take `b.oauthKey`). The nine raw decode sites in `bbolt.go` (`GetOAuthToken`, `UpdateOAuthClientCredentials`, `saveOAuthClientCredentialsIf`, `GetOAuthClientCredentials`, `ClearOAuthClientCredentials`, `UpdateOAuthToken`, `clearOAuthClientCredentialsWhere`, `ListOAuthTokens`) and the `Manager` helpers that read values go through `decodeOAuthRecord`. `SaveOAuthToken` reads the existing value first and refuses when it is undecryptable (FR-013). `OAuthTokenRecord.MarshalBinary`/`UnmarshalBinary` stay plain JSON (other buckets' tests and `purgetest` seeding use them); a source test (`TestOAuthBucketValuesOnlyThroughCodec`) fails if a file in `internal/storage` other than `oauth_binding.go` calls them next to `OAuthTokenBucket`.

`ListOAuthTokens` becomes tolerant (skips and logs undecryptable rows). New `ListOAuthTokenRows() ([]OAuthTokenRow, error)` returns `{BucketKey, Shape, RowKID, Reason}` for every row, for doctor / CLI. `CleanupOrphanedOAuthTokens` uses decodable records as today plus keeps undecryptable rows unconditionally, reporting the ones whose `extractServerNameFromKey` prefix is not a configured server as "undecryptable orphan" (never deleted, FR-015).

Rejected: sealing inside `MarshalBinary` (no access to the bucket key for AAD, and a package global key would be shared by every open DB in tests); a required positional parameter on `NewManager` (touches ~200 test call sites; the source test of FR-017 gives the same guarantee for production sites).

### D7. Config, environment and resolution

Field `OAuthTokenEncryptionKey string \`json:"oauth_token_encryption_key,omitempty" mapstructure:"oauth_token_encryption_key"\`` next to `APIKey` (`internal/config/config.go:389`). Default empty.

Resolution: `config.ResolveOAuthTokenKey(c *Config) (*tokenseal.Key, error)`, modelled on `resolveAPIKeyRef` (`internal/config/api_key_env.go:111`):
1. `MCPPROXY_OAUTH_TOKEN_ENCRYPTION_KEY_FILE` and `MCPPROXY_OAUTH_TOKEN_ENCRYPTION_KEY` both non-empty → `ErrOAuthTokenKeyBothSet`.
2. `_FILE` set → `secret.ReadFile` rules (the same function `${file:}` uses). `MCPPROXY_OAUTH_TOKEN_ENCRYPTION_KEY` set → its value.
3. Else the field: a secret reference is expanded with `secret.NewResolver().ExpandSecretRefs`; a literal is used as is.
4. Empty result after step 3 with an empty field → `nil, nil` (off).
5. `tokenseal.ParseKey`. Every error is one of three fixed sentences (`could not resolve the secret reference…`, `resolved to an empty value`, `is not a base64-encoded 32-byte key`), never quoting the reference, path or value.

`Config.Validate` calls it so an unusable key fails every config load with exit 4 (`ExitCodeConfigError`, via `config.NewStartupError`). The resolved `*tokenseal.Key` is not stored on `Config` (configs are cloned through JSON and copy-on-write); the daemon resolves once in `runtime.New` before `storage.NewManager` (`runtime.go:446`) and keeps it for the process life; CLI open sites call the same function. Because the field itself holds the reference, saves write the reference unchanged — no `OverrideForProcess` is needed, unlike `api_key`, whose resolved value replaces the field.

Precedence env > config matches `api_key`/`MCPPROXY_API_KEY[_FILE]`, which is what container operators expect. Rejected: config > env (the `credential_encryption_key` precedent) — it makes the systemd `%d` and Kubernetes `_FILE` recipes unable to override a stale config; reusing `MCPPROXY_CRED_KEY` / `credential_encryption_key` as a fallback — couples rotation and loss of two stores (spec edge case).

Hot reload: add `OAuthTokenEncryptionKey` to `restartGatedConfigFields` (`config_hotreload.go:618`) and to `pinRestartGated` (`restart_gated.go`), satisfying `TestDetectConfigChanges_EveryTopLevelFieldIsDiffed`. A key change mid-process would split the bucket between two keys.

Redaction: REST/CLI config output masks the field (key-name rule in `oauth/redactview.go`; asserted by a test with a literal value). Settings parity: add to the secret/env-only skip list beside `credential_encryption_key` (`frontend/src/views/settings/fields.ts:375` comment and `scripts/check-settings-parity.py`), no Settings row on either surface. OAS: `make swagger`.

### D8. Missing or wrong key: degrade, not fail

- Key configured but unresolvable or malformed → **fail the load, exit 4** (deterministic operator error, same class as an unresolvable `api_key` reference).
- No key, or a different key, with sealed rows → **start and degrade per row**. Non-OAuth servers, and OAuth servers whose rows open, work. Affected servers: `PersistentTokenStore.GetToken` / `oauth.HasPersistedTokenBound` / `GetPersistedRefreshToken` return the typed error instead of "no token"; the upstream connection records it as a `diagnostics.WrapError(OAuthTokenUndecryptable, …)` last error; `internal/health/calculator.go` maps that code to `unhealthy`, action `configure` (existing `ActionConfigure`), detail "stored OAuth token cannot be decrypted: <reason>". `runtime.go:2839` (`authenticated` in the server list) treats the typed error as not-authenticated but passes the reason through. The refresh manager skips the row (it is not in the tolerant listing) and never schedules it.

Rejected: refusing to start whenever any row is undecryptable (takes every unrelated server down for one missing mount; listed as an opt-in strict mode, out of scope 6); treating undecryptable as absent like the broker does (that is exactly the silent re-login the issue rules out, and a fresh login would then overwrite the row).

### D9. Migration, rollback and mixed state

- **Enable**: set the key, restart. The boot sweep (`Manager.SealPlaintextOAuthTokens(ctx) (sealed int, err error)`) runs in `Runtime` boot right after storage opens and before `CleanupOrphanedOAuthTokens` (`lifecycle.go:76`), the refresh manager start and any upstream connect; it is one `db.Update`: for every plaintext row, decode → `putOAuthRecord` (sealed, new revision). Sealed and undecryptable rows are skipped. When `sealed > 0` it writes `meta/oauth_seal_compact_advised = <RFC3339 time>` in the same transaction.
- **Mixed rows** during life: plaintext and sealed rows are both read; any write seals. A plaintext row that arrives later (restored backup) is sealed at its next write or next boot.
- **Offline commands** (`cmd/mcpproxy/auth_tokens_cmd.go`, subcommand group `tokens` under the existing `auth`): open storage with the resolved key and bbolt's normal lock timeout, so a running core gives exit 3 (`ExitCodeDBLocked`).
  - `status [-o json]`: the doctor section (D10) from `ListOAuthTokenRows`.
  - `encrypt [--dry-run]`: the sweep; requires a key (else exit 4); exit 1 when undecryptable rows remain.
  - `decrypt --yes`: one `db.Update` that rewrites every row it can open as plaintext (bypassing the sealer through an explicit `encodeOAuthRecord(nil, …)` path used only here); requires the key; exit 1 when any row stays sealed.
- **Downgrade**: documented as `decrypt --yes` with the new binary, then remove the key, then install the old binary. Without that, the old binary's `json.Unmarshal` fails on every sealed row: `ListOAuthTokens` aborts (no refresh scheduling, orphan cleanup disabled), servers show login required, and its bound writers refuse to overwrite the row because they decode first; recovery is to reinstall the new binary or `auth logout`.
- **Disable**: same as downgrade minus the binary change. Removing the key without `decrypt` leaves rows undecryptable (D8) until it is restored.
- **Rotation**: `decrypt --yes` with the old key, change the key, `encrypt`. Single-step rekey is out of scope 1.
- **Free pages**: bbolt does not zero freed pages, so after the sweep the old plaintext can survive in the file until it is compacted. `mcpproxy db compact` (`cmd/mcpproxy/db_cmd.go`) deletes the marker in the compacted file and, when the marker was present, prints that the retained backup (default `--keep-backup=true`) still holds the old plaintext and should be deleted or compacted with `--keep-backup=false`.

### D10. Surfaces

- `contracts.Diagnostics` gains `OAuthTokenStorage *contracts.OAuthTokenStorageReport` (`encryption_enabled`, `key_id`, `sealed`, `sealed_other_key`, `plaintext`, `undecryptable`, `servers[] {bucket_key, server, reason, row_key_id}`, `compaction_advised`, `warnings[]`). Filled in `management.Doctor` (`internal/management/diagnostics.go:91`) from a runtime accessor; undecryptable servers also add one `RuntimeWarnings` entry each so the existing issue count and tray badge reflect them. Spec 118 scoping: a scoped caller sees only the servers in its visible set (the `AllowedServers` the rest of `management.Doctor` already filters by), and counts are computed over that set; the key id and the warnings are administrator-only.
- Warnings: `literal_key` (field is not a secret reference and no env override), `key_file_in_data_dir` (resolved `_FILE` or `${file:}` path under `data_dir`, after `filepath.EvalSymlinks`), `plaintext_rows_with_key` (should be 0 after the sweep; non-zero means a write from an older binary), `compaction_advised`.
- Diagnostics code `MCPX_OAUTH_TOKEN_UNDECRYPTABLE` in `internal/diagnostics/codes.go` + `registry.go` (severity error, fix steps: restore the key / check the mount / `auth logout <server>` to discard), `docs/errors/MCPX_OAUTH_TOKEN_UNDECRYPTABLE.md`, catalog contract test.
- Logs: kid and reason only; never key bytes, never the reference path (SC-004).
- Telemetry: unchanged (schema 16). Web UI / tray: no change; they render the health detail/action and the doctor warnings they already render.

### D11. Concurrency and Spec 116

All OAuth writers are single `bbolt.Update` transactions (`UpdateOAuthTokenIfBound`, `SaveOAuthClientIfBound`, the login path writing token + DCR client together via `oauth.WithLoginClient`, refresh flights with their FlightGeneration CAS inside the same tx). Sealing happens inside `putOAuthRecord`, inside those transactions, so the transaction shape, the binding checks (`oauthWriteAllowedTx`) and the revision stamping are unchanged and no lock is added. The boot sweep runs before any of those writers exist. The offline commands run under bbolt's exclusive file lock. The Spec 116 purge set, `clearOAuthStateTx`, `oauthKeyBelongsTo`, the commit's `OAuthClears` and the `oauth_completion` bucket are key-only or secret-free and need no change; `scripts/check-upstream-write-sites.sh` needs no new exemption (the new mutators live in `internal/storage`).

## Open-site inventory (FR-017)

| Site | Change |
|---|---|
| `internal/runtime/runtime.go:446` | resolve once, `storage.NewManager(dir, log, storage.WithOAuthTokenKey(k))`, keep `k` for doctor |
| `internal/appctx/context.go:47` | same |
| `cmd/mcpproxy/call_cmd.go:497`, `code_cmd.go:305`, `tools_cmd.go:1098` | same, via a shared `openStorageForCLI(cfg)` helper |
| `cmd/mcpproxy/connect_credential.go:225` | same |
| `internal/upstream/cli/client.go:67` | same; a resolution error is returned, not downgraded to the in-memory fallback (which would make `auth login` succeed without persisting) |
| `cmd/mcpproxy/debug_purge_report_qa.go`, `cmd/populate-test-data` | dev tools: pass the key too (so the source test has no exemptions) |

`TestStorageOpenSitesPassOAuthKey` (in `internal/storage`, walks the module's non-test `.go` files) fails on any `storage.NewManager(` / `storage.NewBoltDB(` call without `WithOAuthTokenKey`.

## Docs plan

- `docs/features/oauth-authentication.md`: replace line 83 with "Stores tokens in `config.db` (plaintext unless `oauth_token_encryption_key` is set)"; rewrite "Token Storage" (216-224) to say what is stored, where, the default exposure and a link to the new section; replace line 402. Remove the keyring table.
- New `docs/features/oauth-token-encryption.md` (published: `website/docusaurus.config.js` include list + `website/sidebars.js` next to `features/oauth-authentication`): threat model in the issue's words; key generation (`openssl rand -base64 32`, `chmod 600`, outside `data_dir`); recipes for systemd `LoadCredential` (`${file:/run/credentials/mcpproxy.service/oauth-key}` or `MCPPROXY_OAUTH_TOKEN_ENCRYPTION_KEY_FILE=%d/oauth-key`), Docker secrets (`/run/secrets/…`), Kubernetes secret volumes (symlinked files are followed), env fallback, `${keyring:}` on desktops; enable → restart → `db compact --keep-backup=false`; old backups stay plaintext; key loss = re-login; what doctor shows; downgrade/disable/rotation with `auth tokens decrypt|encrypt`; `secret_file_dirs` note once #1721 lands.
- `docs/configuration/config-file.md` and `docs/configuration/environment-variables.md`: the field and both env vars; `docs/configuration.md` tables too (unpublished, kept in sync).
- Isolation pages (FR-041); `auth tokens` in `docs/cli/command-reference.md`; the compaction note in `docs/cli/db-commands.md`.
- `specs/023-oauth-state-persistence/plan.md:31`: strike-through note "not true; see Spec 123".
- CLAUDE.md "Recent Changes" entry when the implementation lands (not in this spec PR).

## Test plan

| Layer | Test | Proves |
|---|---|---|
| tokenseal | round trip; fresh nonce per seal (1 000 seals, no repeat); AAD mismatch on bucket key / bucket / kid → `auth_failed`; wrong key → `key_mismatch`; nil key → `no_key`; truncated / bad base64 / `enc:v2:` → `malformed` / `unsupported_version`; `ParseKey` table (31/33 bytes, URL alphabet, whitespace, padding); HKDF known-answer vector; no key material in `%v`/`%#v`; leaf deps test | FR-001..006 |
| storage | every exported OAuth method against plaintext, sealed and undecryptable rows (table over methods); writers refuse undecryptable rows and bytes are unchanged; key-only deletes still delete; `ListOAuthTokens` tolerant + `ListOAuthTokenRows`; orphan cleanup keeps undecryptable rows; no-key bytes golden-equal to `main` (SC-003); sweep is one tx and sets the marker; source tests for the codec seam and the open sites | FR-010..017 |
| **raw file** | `TestOAuthTokensNotInRawFile`: seed three rows with marker strings, open with a key, sweep, close, `bbolt.Compact` into a new file, `os.ReadFile` both files: markers present in the pre-compact file's free pages are allowed only before compaction, **absent from the compacted file**; a login write made with the key set never appears even before compaction; plus the inverse control (no key → markers present) so the scan is not vacuous | SC-001 |
| config | resolution table (field literal / `${file:}` / `${env:}` / env value / env file / both env → error / unresolvable / empty / malformed) with exit-4 classification and a grep that no error text contains the path or value; hot-reload canary; redaction; save writes the reference | FR-020..024, SC-004 |
| oauth / runtime | `PersistentTokenStore` and `HasPersistedTokenBound` return the typed error; refresh manager skips undecryptable rows and schedules the rest; login against an undecryptable row fails and leaves bytes; boot order test (sweep before orphan cleanup and refresh start); `stale_oauth_token_health_test.go`-style health mapping → `configure` + code | FR-013, FR-016, FR-031, US2 |
| management / diagnostics | doctor section counts and warnings, scoped-caller filtering, catalog contract for the new code | FR-030, FR-031 |
| cmd | `auth tokens status|encrypt|decrypt` happy paths, `--dry-run`, locked DB → exit 3, no key → exit 4, `-o json` shape; `db compact` clears the marker and prints the backup note | FR-032, D9 |
| e2e | `./scripts/test-api-e2e.sh` unchanged (no key); one new e2e step in the OAuth fixture run with a key (login → raw scan → restart without key → health detail → restart with key → authenticated) | US1, US2 |

## Constitution Check

| Principle | Status | Note |
|---|---|---|
| Security by default | PASS | Opt-in, so no behaviour change by default; when on, fail-closed for writes and loud for reads; key never logged |
| No new dependencies | PASS | stdlib only |
| Test-first | PASS | tasks.md orders failing tests before each implementation group |
| Both editions | PASS | untagged code; broker store untouched |
| Docs accuracy | PASS | false claims removed in the same PR |

## Risks

- **A missed open site writes plaintext** → source test (FR-017).
- **A missed decode site misreads an envelope as JSON** → source test (FR-010) plus the per-method table test over sealed rows.
- **Operators believe enabling alone protects old backups** → docs, doctor `compaction_advised`, compact output.
- **Key file placed in the data directory** → doctor warning.
- **Downgrade surprise** → docs and release notes; `decrypt` command.
