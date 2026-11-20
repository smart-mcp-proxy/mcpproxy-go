# Implementation Plan: Encrypt Upstream OAuth Tokens at Rest

**Branch**: `123-oauth-token-encryption` | **Date**: 2026-11-11 | **Spec**: [spec.md](spec.md) | **Issue**: #1697

## Summary

A new stdlib-only leaf package `internal/tokenseal` seals and opens a byte slice as an `enc:v1` envelope (AES-256-GCM, random 96-bit nonce, HKDF-derived record key and key id, AAD = version + kid + bucket name + bucket key). `internal/storage` gets one encode and one decode function for `oauth_tokens` values and routes the write seam (`putOAuthRecord`) and every value read through them. The key comes from a new top-level config field, `oauth_token_encryption_key`, resolved once per process at the storage-open sites (not in `Config.Validate`) through the existing secret resolver (so `${file:}` works today), overridable by `MCPPROXY_OAUTH_TOKEN_ENCRYPTION_KEY[_FILE]`, restart-gated, and passed to storage as an open option at every open site. Undecryptable rows become a typed error that every writer refuses, that every token read path propagates instead of "no token", and that health and a new diagnostics code report. The daemon seals leftover plaintext rows once in `runtime.New`; offline `mcpproxy auth tokens status|encrypt|decrypt` and the doctor section cover inspection, migration and rollback. Docs are corrected.

Three PRs (spec "Scope Boundary"):

| PR | Contents | Size |
|---|---|---|
| PR-1 | FR-040/041 doc corrections with today's facts (plaintext in `config.db`, file permissions only); spec 023 annotation | docs only |
| PR-2 | D1-D9 except the offline commands: `internal/tokenseal`, storage codec + options + fail-closed writers + tolerant listing + sweep, config field / env / resolution / exit 4 / hot reload / redaction, open sites, read-path inventory (D8), the manual-login refusal against an undecryptable row (D9 key loss), health + diagnostics code, the new docs page and config docs | the one feature PR |
| PR-3 | D10 doctor section, `auth tokens status|encrypt|decrypt`, compaction marker handling in `db compact`, richer login pre-flight UX (the refusal itself is PR-2), CLI/REST docs for those surfaces | surfaces |

PR-2 and PR-3 land in the same release; release notes announce the field only after PR-3. No Web UI or Swift code changes; no Settings parity script change (the field simply has no row on either side).

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

`Seal` returns an error for a plaintext over 1 MiB (`MaxPlaintextLen`, `ErrPlaintextTooLarge`), and `Seal` and `Open` reject a bucket name or bucket key containing a NUL byte (the AAD is NUL-separated, so a NUL would let two different (bucket, key) pairs share one AAD). `Open` applies this precedence: (a) header: an `enc:` value with an unknown version → `unsupported_version`; a damaged header or an empty payload → `malformed`; (b) then key state: no key → `no_key`, kid differs from the configured kid → `key_mismatch` (no decrypt attempted); (c) only with a matching key is the payload examined: a `=` anywhere other than as the last one or two characters of the payload, or any character outside the base64 alphabet → `malformed`; a payload of base64-alphabet characters with at most two trailing `=` that does not decode (wrong length for its padding, which is what a truncated envelope looks like) or decodes shorter than nonce + tag, and any GCM failure → `auth_failed`. `AAAA=` is `auth_failed` (truncation-shaped), `AA=A` is `malformed`. So a payload with invalid characters under no key or another key reports `no_key` or `key_mismatch`.

`Key` has no exported field and no `MarshalJSON`; it implements `String` and `GoString`, which print only the key id, so formatting (`%v`, `%+v`, `%#v`) cannot leak key material (a test asserts the formatted output contains no key byte and no base64 of it, and `json.Marshal(k)` likewise).

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

`putOAuthRecord` and `getOAuthRecordTx` take the key as an explicit parameter: `putOAuthRecord(bucket *bbolt.Bucket, k *tokenseal.Key, key string, rec *OAuthTokenRecord) error` (callers pass `b.oauthKey`; `decrypt` in PR-3 passes `nil`, which is the only plaintext-writing path while a key is configured). `putOAuthRecord` reads the previous value before the put; when it was plaintext and the new value is an envelope it writes the compaction marker into the `meta` bucket through `bucket.Tx()` in the same transaction (FR-016). A package-level test hook `var putOAuthRecordHook func(key string) error` (nil in production) is the fault-injection seam for the sweep and `encrypt`/`decrypt` atomicity tests.

The eight raw decode sites in `bbolt.go` (`GetOAuthToken` :950, `UpdateOAuthClientCredentials` :979, `saveOAuthClientCredentialsIf` :1038, `GetOAuthClientCredentials` :1068, `ClearOAuthClientCredentials` :1092, `UpdateOAuthToken` :1126, `clearOAuthClientCredentialsWhere` :1188, `ListOAuthTokens` :1213) and the `Manager` helpers that read values go through `decodeOAuthRecord`; the `getOAuthRecordTx` users (`GetOAuthTokenBound`, `GetOAuthClientCredentialsBound`, `boundClearTx`, the bound writers) are covered by that function. `SaveOAuthToken` reads the existing value first and refuses when it is undecryptable (FR-013). Exception: with no key configured it overwrites a corrupt value that is not an `enc:` envelope exactly as before this feature; any `enc:`-prefixed row is always refused, and with a key configured every undecodable row is refused. `OAuthTokenRecord.MarshalBinary`/`UnmarshalBinary` stay plain JSON (other buckets' tests and `purgetest` seeding use them).

Codec source test (`TestOAuthBucketValuesOnlyThroughCodec`, FR-010): a textual "near `OAuthTokenBucket`" rule cannot tell `OAuthTokenRecord.UnmarshalBinary` from `UpstreamRecord.UnmarshalBinary` (`oauth_binding.go:76`, inside OAuth write transactions) or `OAuthCompletionEvent.UnmarshalBinary`. The test type-checks `internal/storage` with the standard library only (`go/parser` + `go/types` with `importer.ForCompiler(fset, "source", nil)`; `golang.org/x/tools/go/packages` is not used, since it would turn an indirect module into a direct dependency) and fails on (a) any call of `MarshalBinary`/`UnmarshalBinary` whose receiver type is `OAuthTokenRecord` or `*OAuthTokenRecord` outside `encodeOAuthRecord`/`decodeOAuthRecord`, and (b) any `Get`/`Put`/`ForEach`/`Cursor` on a bucket obtained from `OAuthTokenBucket` outside the codec, the key-only deletes and the listing helpers, each exemption named in the test.

`ListOAuthTokens` becomes tolerant (skips and logs undecryptable rows). New `ListOAuthTokenRows() ([]OAuthTokenRow, error)` returns `{BucketKey, Class, RowKID, Reason}` for every row, for doctor / CLI, where `Class` is the strict partition `plaintext | sealed | undecryptable` and `Reason` is set only for `undecryptable` (`key_mismatch` is a reason, not a separate class). `CleanupOrphanedOAuthTokens` uses decodable records as today plus keeps undecryptable rows unconditionally, reporting the ones whose `extractServerNameFromKey` prefix is not a configured server as "undecryptable orphan" (never deleted, FR-015).

Rejected: sealing inside `MarshalBinary` (no access to the bucket key for AAD, and a package global key would be shared by every open DB in tests); a required positional parameter on `NewManager` (touches ~200 test call sites; the source test of FR-017 gives the same guarantee for production sites).

### D7. Config, environment and resolution

Field `OAuthTokenEncryptionKey string \`json:"oauth_token_encryption_key,omitempty" mapstructure:"oauth_token_encryption_key"\`` next to `APIKey` (`internal/config/config.go:389`). Default empty.

Resolution: `config.ResolveOAuthTokenKey(c *Config) (*tokenseal.Key, KeySource, error)`, modelled on `resolveAPIKeyRef` (`internal/config/api_key_env.go:111`). `KeySource{Kind: literal|env|env_file|ref_file|ref_env|ref_keyring; InDataDir bool}` is computed during resolution (`InDataDir` from the resolved `_FILE` or `${file:}` path after `filepath.EvalSymlinks`, compared with `data_dir`); only the boolean is kept, never the path (SC-004). The runtime keeps the key and the `KeySource` for doctor (D10). Steps:
1. `MCPPROXY_OAUTH_TOKEN_ENCRYPTION_KEY_FILE` and `MCPPROXY_OAUTH_TOKEN_ENCRYPTION_KEY` both non-empty → `ErrOAuthTokenKeyBothSet`.
2. `_FILE` set → `secret.ReadFile` rules (the same function `${file:}` uses). `MCPPROXY_OAUTH_TOKEN_ENCRYPTION_KEY` set → its value.
3. Else the field: a secret reference is expanded with `secret.NewResolver().ExpandSecretRefs`; a literal is used as is.
4. Empty result after step 3 with an empty field → `nil, nil` (off).
5. `tokenseal.ParseKey`. Every resolution error is one of four fixed sentences: unresolvable (source missing or unreadable, `could not resolve the secret reference…`), unusable (source present but not usable: oversized, contains NUL, not a regular file, relative path), empty (`resolved to an empty value`), malformed (`is not a base64-encoded 32-byte key`); plus the both-variables-set error and the bad-reference error. None quotes the reference, path or value. Only "unresolvable" permits the standalone `auth logout` exception (D8).

Where it runs: key resolution for use (obtaining the key to open storage) happens **only at the storage-open sites**, at most once per process for the daemon, never in `Config.Validate`. Separately, the secrets inventory (`GET /api/v1/secrets/config`) may probe whether a `${env:}` / `${keyring:}` reference currently resolves, as it does for every other secret reference; it reports presence only and never returns the value or anything computed from it, so it is not key resolution for use. `Validate` runs inside `config.LoadFromFile` (`loader.go:97`), which every CLI command reaches through `loadCLIConfig` (`cmd/mcpproxy/cli_config.go:29`) and every hot reload reaches (`lifecycle.go:2268`). Resolving there would (a) make `doctor`, `upstream list`, `activity` and other commands that never open `config.db` exit 4 whenever the key path is unreadable from the operator's shell (the systemd credentials directory exists only inside the running unit), (b) reject every later hot reload once a mounted key file is removed or rotated, although the field is restart-gated and the process keeps its key, and (c) read a `${keyring:}` reference on every load, prompting the macOS keychain repeatedly. `Validate` therefore does a syntax-only check (empty, a well-formed `${file:}`/`${env:}`/`${keyring:}` reference, or a literal), reading nothing.

The daemon resolves once in `runtime.New` before `storage.NewManager` (`runtime.go:446`) and keeps the key for the process life; the CLI open sites call the same function through one helper `openStorageForCLI(cfg)`. A resolution failure is returned as `config.NewStartupError(ExitCodeOAuthTokenKeyError, msg)` with a new config-owned constant `ExitCodeOAuthTokenKeyError = 4` (the existing exit-4 constant in package config is `ExitCodeAuditLogError`, `audit_log.go:221`, and `ExitCodeConfigError` lives in `cmd/mcpproxy/exit_codes.go`), so `classifyError` maps it through its `errors.As(*StartupError)` path and not through the message heuristics (`main.go:~1118`). The resolved `*tokenseal.Key` is not stored on `Config` (configs are cloned through JSON and copy-on-write). CLI behaviour with a bad key: daemon start and every command that opens `config.db` through the key-aware storage helpers (`openStorageForCLI`, `appctx`, `internal/upstream/cli`) stop with exit 4; commands that only talk to a running daemon over the socket or REST are unaffected; standalone `auth logout` proceeds when the key source is missing or unreadable (D8); `db compact` and `db stats` open the file directly (`db_cmd.go`) and do not resolve the key.

Offline invocation: because env beats the field, `MCPPROXY_OAUTH_TOKEN_ENCRYPTION_KEY_FILE=/etc/credstore/oauth-key mcpproxy auth tokens decrypt --yes` works whatever path the config references; the docs give that form plus `docker compose run --rm mcpproxy auth tokens …` (same secret mounted) and `systemd-run --pipe -p LoadCredential=oauth-key:/etc/credstore/oauth-key …`. Because the field itself holds the reference, saves write the reference unchanged (a literal key and the original `${...}` reference, including a `${file:}` path, are intentionally persisted in the config file; the invariant is that resolution never replaces a reference with key material and that resolved values never reach logs, errors, CLI or REST output) — no `OverrideForProcess` is needed, unlike `api_key`, whose resolved value replaces the field.

Precedence env > config matches `api_key`/`MCPPROXY_API_KEY[_FILE]`, which is what container operators expect. Rejected: config > env (the `credential_encryption_key` precedent) — it makes the systemd `%d` and Kubernetes `_FILE` recipes unable to override a stale config; reusing `MCPPROXY_CRED_KEY` / `credential_encryption_key` as a fallback — couples rotation and loss of two stores (spec edge case).

Hot reload: add `OAuthTokenEncryptionKey` to `restartGatedConfigFields` (`config_hotreload.go:618`) and to `pinRestartGated` (`restart_gated.go`), satisfying `TestDetectConfigChanges_EveryTopLevelFieldIsDiffed`. A key change mid-process would split the bucket between two keys.

Redaction (FR-024): `GET /api/v1/config` goes through `oauth.RedactedConfig` → `LiveRedaction.RedactNested` (`internal/oauth/configview.go`), a generic walk with value-shaped detection. The policy is final: a **literal** key is always masked in `GET /api/v1/config` and every other config output, regardless of `reveal_secret_headers`; a **reference** (`${file:...}`, `${env:...}`, `${keyring:...}`) is shown exactly as the operator wrote it, including a file path the operator wrote. A reference is recognised by one shared rule in validation and in config output (it tolerates whitespace inside `${ ... }`, as the secret parser does, so `${ file:/abs/path}` is shown as written): everything validation accepts as a reference is shown as written, everything else is masked. What must never appear in any log, error, CLI or REST output is the key value, key bytes, the contents of the key source, or any value from which the key could be recovered; the public key id (HKDF-derived, 16 hex characters) and the key-source metadata (kind, inside the data directory or not) are allowed in logs, health, doctor and CLI output. Tests first (including a whitespace-spelled reference such as `${ file:/abs/path}`, which must come back as written): a literal key is masked on the read door with and without `reveal_secret_headers`; all three reference kinds (including `${file:}`, which the generic reference pattern in `MaskValue` does not cover) come back verbatim; a GET → `/api/v1/config/apply` round trip of the masked document leaves the stored field unchanged (the write twin must revert the mask, or the mask would be persisted over the key). Code changes as needed to make those pass (a field-specific rule in the redaction walk). Settings parity: no change. `scripts/check-settings-parity.py` has no secret skip list (its only exclusion is `WEB_ONLY_PREFIXES = ("server_edition.",)`) and a top-level key absent from both `fields.ts` and `SettingsCatalog.swift` already passes; the task is to confirm neither side gains a row. OAS: `make swagger`.

### D8. Missing or wrong key: degrade, not fail

- Key configured but unresolvable or malformed (including a missing secret mount) → **the process that opens `config.db` exits 4 before opening it** (deterministic operator error, same class as an unresolvable `api_key` reference). Commands that do not open storage are unaffected (D7). This is US3, not US2. One delete-only exception: standalone `auth logout` (a delete by bucket key) opens `config.db` with a nil key when, and only when, the key source is missing or unreadable (file absent, env var unset, keyring entry absent), so key loss stays recoverable; conflicting env vars, a bad reference, and an unusable (oversized, NUL, not a regular file, relative path), empty or malformed value still exit 4 (`config.ResolveOAuthTokenKey` reports the unresolvable class distinctly).
- No key configured, or a different key, with sealed rows → **start and degrade per row**. Non-OAuth servers, and OAuth servers whose rows open, work. Affected servers report the typed error; `internal/health/calculator.go` maps `diagnostics.OAuthTokenUndecryptable` to `unhealthy`, action `configure` (existing `ActionConfigure`), detail "stored OAuth token cannot be decrypted: <reason>". The mapping applies whether or not the server is connected: an undecryptable row never yields action `login`, including a server that connected anonymously and later needs authentication (it gets `configure`); `Connected` no longer suppresses `OAuthTokenUndecryptable` in `internal/health/calculator.go`, and the runtime server-list path (`runtime.go:~3161`) no longer suppresses the diagnostic for connected servers. A connected server that needs no login stays healthy, and the static-header exclusion is unchanged. The refresh manager skips the row (it is not in the tolerant listing) and never schedules it.

Read-path inventory. Every production caller of a token read today maps any error to "absent", which would start an interactive login against a row the login cannot then overwrite. Each site changes as follows (`errors.Is(err, storage.ErrOAuthTokenUndecryptableSentinel)`):

| Site | Today | With the typed error |
|---|---|---|
| `internal/oauth/persistent_token_store.go:129` `GetToken` | any error → `transport.ErrNoToken` (starts the browser flow in mcp-go) | return the typed error (mcp-go `getValidToken` passes a non-`ErrNoToken` error through) |
| `persistent_token_store.go:91` | passes through | unchanged |
| `internal/oauth/config.go:261` `HasPersistedTokenBound` | `(false,false,false)` on error | signature becomes `(hasToken, hasRefresh, isExpired bool, err error)`; `HasPersistedToken` (:255) follows; callers below updated |
| `internal/oauth/config.go:284` `GetPersistedRefreshToken` | `""` on error | `(string, error)`; its callers treat the typed error as "do not refresh, report" |
| `internal/upstream/core/connection_oauth.go:229, 251, 675, 697` | treat `false` as "needs login" | on the typed error, stop before any OAuth flow and set the connection last error to `diagnostics.WrapError(OAuthTokenUndecryptable, …)` |
| `connection_oauth.go:1841, 1860` | `err == nil` guard (presence probes) | treat the typed error as present-but-unusable, not absent |
| `internal/upstream/core/client.go:852` | `err != nil` → no token | propagate as the connection error above |
| `internal/upstream/core/oauth_refresh_replay.go:65`, `oauth_refresh.go:220` (`Load`) | error aborts the replay/refresh | unchanged control flow; the refresh records the code instead of a generic failure, and never falls back to login |
| `internal/upstream/manager.go:2768` (token fingerprint) | `recErr == nil` guard | unchanged (no fingerprint), but no "token missing" event |
| `internal/upstream/manager.go:3000` (access-token read) | `""` on error | `""` plus a debug log; the caller is the header injector, which must not trigger login |
| `internal/upstream/manager.go:3547` (`hasStoredTokens` for dynamic OAuth) | error → false | typed error → true-but-unusable so no login is offered |
| `internal/oauth/refresh_manager.go:914` | `err == nil` guard | unchanged; undecryptable rows are not scheduled |
| `internal/runtime/runtime.go:2839` (`authenticated` in the server list) | error → false | false, and the reason is passed through to the health detail |

A source test lists these call sites by file and function and fails when a new production caller of `GetOAuthToken*`/`HasPersistedToken*`/`GetPersistedRefreshToken` appears without being added to it.

The asymmetry is deliberate: a configured reference that cannot be read is an unambiguous operator error caught before anything is written, while "no key" or "another key" is a state the core can run in safely. Rejected: refusing to start whenever any row is undecryptable (takes every unrelated server down because one row was sealed under a key that is no longer configured; listed as an opt-in strict mode, out of scope 6); treating undecryptable as absent like the broker does (that is exactly the silent re-login the issue rules out, and a fresh login would then overwrite the row).

### D9. Migration, rollback and mixed state

- **Enable**: set the key, restart. The boot sweep (`Manager.SealPlaintextOAuthTokens(ctx) (sealed int, err error)`) runs in `runtime.New` after `storage.NewManager` (`runtime.go:446`) returns and after the shutdown marker is armed (Spec 080 `ArmShutdownMarker`, which constructs the pre-churn store: an intentional operation that precedes the sweep and is not an OAuth consumer), and before the upstream manager, the refresh manager and every other OAuth consumer is constructed. The invariant is that no OAuth consumer exists before the sweep has succeeded, and so before `StartBackgroundInitialization` starts the activity service, orphan cleanup or any connection. No OAuth reader or writer exists at that point, which makes "nothing is in flight" structurally true. It is one `db.Update` that first collects every plaintext `(key, value)` pair with a cursor walk and only then, after the walk, decodes and writes each through `putOAuthRecord` (sealed, new revision): bbolt cursors skip keys when the bucket is mutated during iteration (the same bug documented in `clearOAuthStateTx`, `manager.go:2574`). Sealed and undecryptable rows are skipped. A row that cannot be sealed for a per-row reason (plaintext that is not valid JSON, a record over the 1 MiB `Seal` bound) is skipped with a warning naming the bucket key and the reason (`malformed` or `too_large`), never the value, and left unchanged. A transaction-level failure rolls everything back, closes storage and makes `runtime.New` return the error, so startup either seals every valid plaintext row before any OAuth consumer exists or refuses to start. `putOAuthRecord` sets `meta/oauth_seal_compact_advised = <RFC3339 time>` when it replaced plaintext (D6).
- **Compatibility without a key**: bytes and behaviour are as before for valid plaintext records. Corrupt-row handling changes even without a key, in four ways: listing skips a malformed row instead of failing; the OAuth login entry points refuse a login for a server whose row is malformed (undecryptable sentinel); `SaveOAuthToken` still overwrites a corrupt non-envelope value; any `enc:` row is refused by every writer that starts from the stored record.
- **Mixed rows** during life: plaintext and sealed rows are both read; any write seals, and a write that seals a plaintext row sets the compaction marker in the same transaction. A plaintext row that arrives later (restored backup) is sealed at its next write or next boot.
- **Offline commands** (PR-3; `cmd/mcpproxy/auth_tokens_cmd.go`, subcommand group `tokens` under the existing `auth`): open storage through `openStorageForCLI` with the resolved key and bbolt's normal lock timeout, so a running core gives exit 3 (`ExitCodeDBLocked`). The key comes from the same resolution as the daemon, so the env override is the offline form (D7).
  - `status [-o json]`: the doctor section (D10) from `ListOAuthTokenRows`.
  - `encrypt [--dry-run]`: the sweep; requires a key (else exit 4); exit 1 when undecryptable rows remain.
  - `decrypt --yes`: one `db.Update` that collects every row first, then rewrites each one it can open as plaintext through `putOAuthRecord(bucket, nil, …)` (the only caller passing a nil key while a key is configured); requires the key; exit 1 when any row stays sealed.
- **Downgrade**: documented as `decrypt --yes` with the new binary, then remove the key, then install the old binary. Without that, the old binary's `json.Unmarshal` fails on every sealed row: `ListOAuthTokens` aborts (no refresh scheduling, orphan cleanup disabled), servers show login required, and its bound writers refuse to overwrite the row because they decode first; recovery is to reinstall the new binary or `auth logout`.
- **Disable**: same as downgrade minus the binary change. Removing the key without `decrypt` leaves rows undecryptable (D8) until it is restored.
- **Rotation**: `decrypt --yes` with the old key, change the key, `encrypt`, then finish with offline `db compact --keep-backup=false`. `decrypt` writes plaintext into pages that bbolt frees but does not zero, so without the final compaction the plaintext survives in the file. The recipe also says to delete any existing `config.db.bak` (an earlier default `db compact` left one; `--keep-backup=false` does not remove it) and warns that any copy or backup of `config.db` or the data directory made during the plaintext interval holds the tokens in readable form. Single-step rekey is out of scope 1.
- **Free pages**: bbolt does not zero freed pages, so after the sweep the old plaintext can survive in the file until it is compacted. `mcpproxy db compact` (`cmd/mcpproxy/db_cmd.go`) keeps a backup by default (`--keep-backup=true`, `db_cmd.go:138`), a hard link `config.db.bak` to the pre-compaction file (`db_cmd.go:477-481`), which still holds the freed plaintext. So compaction deletes the marker in the compacted file **only when `--keep-backup=false`**; with a retained backup it carries the marker over and prints that `config.db.bak` holds the old plaintext and should be deleted. Separately, doctor reports `backup_file_present` whenever `config.db.bak` exists in `data_dir` while encryption is on (`--keep-backup=false` does not remove a `.bak` left by an earlier run), so the warning clears when the operator deletes the file.
- **Key loss**: every bound writer, including the login writers `UpdateOAuthTokenIfBound` and `SaveOAuthClientIfBound`, refuses the undecryptable row, so recovery is `mcpproxy auth logout --server=<server>` (or removing the server), then log in. PR-2 includes the guard on the login entry points (`mcpproxy auth login` in `internal/upstream/cli`, the REST login trigger, and the connection path from D8): they read the row first and refuse with `MCPX_OAUTH_TOKEN_UNDECRYPTABLE` before any discovery, DCR or callback-server step. PR-3 only adds the richer pre-flight UX on top (messages, doctor integration).

### D10. Surfaces

- `contracts.Diagnostics` gains `OAuthTokenStorage *contracts.OAuthTokenStorageReport` (`encryption_enabled`, `key_id`, `plaintext`, `sealed`, `undecryptable`, `undecryptable_by_reason{no_key,key_mismatch,auth_failed,unsupported_version,malformed}`, `servers[] {bucket_key, server, reason, row_key_id}`, `compaction_advised`, `warnings[]`). The three counts partition the rows and sum to the row total; a `key_mismatch` row is counted once, under `undecryptable`. Filled in `management.Doctor` (`internal/management/diagnostics.go:91`) from a runtime accessor; undecryptable servers also add one `RuntimeWarnings` entry each so the existing issue count and tray badge reflect them. Spec 118 scoping: a scoped caller sees only the servers in its visible set (the `AllowedServers` the rest of `management.Doctor` already filters by), and counts are computed over that set; the key id and the warnings are administrator-only.
- Warnings, computed from the `KeySource` the runtime kept at resolution (D7), so doctor never re-reads the config or the environment: `literal_key` (`Kind == literal`), `key_file_in_data_dir` (`InDataDir`), `plaintext_rows_with_key` (should be 0 after the sweep; non-zero means a write from an older binary), `compaction_advised` (marker set), `backup_file_present` (`config.db.bak` in `data_dir` while encryption is on).
- Diagnostics code `MCPX_OAUTH_TOKEN_UNDECRYPTABLE` in `internal/diagnostics/codes.go` + `registry.go` (severity error, fix steps: restore the key / check the mount / `auth logout --server=<server>` to discard), `docs/errors/MCPX_OAUTH_TOKEN_UNDECRYPTABLE.md`, catalog contract test.
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
| `internal/upstream/cli/client.go:67` | resolve the key first; a resolution error is returned (exit 4), not downgraded to the in-memory fallback (which would make `auth login` succeed without persisting). A `NewBoltDB` error after a successful resolution (e.g. the DB is locked by a running core) keeps today's in-memory fallback and warning. Exception: the standalone `auth logout` path (delete by bucket key only) opens with a nil key when the key source is missing or unreadable (file absent, env var unset, keyring entry absent); conflicting env vars, a bad reference and an empty, oversized or malformed value still return the exit-4 error |
| `cmd/mcpproxy/debug_purge_report_qa.go`, `cmd/populate-test-data` | dev tools: pass the key too (so the source test has no exemptions) |

`TestStorageOpenSitesPassOAuthKey` (in `internal/storage`, walks the module's non-test `.go` files) fails on any `storage.NewManager(` / `storage.NewBoltDB(` call without `WithOAuthTokenKey`.

## Docs plan

PR-1 (today's facts, no feature):
- `docs/features/oauth-authentication.md`: replace line 83 with "Stores tokens in `config.db` in the data directory"; rewrite "Token Storage" (216-224) to say what is stored (access token, refresh token, DCR client secret), where, that it is readable JSON protected by `0600`/`0700` file permissions, and that a copy of `config.db` yields the tokens (the encrypted-at-rest claim is made only where the key is set and not part of the copy, the startup migration has sealed the rows, the database has been compacted, and the rows were not skipped by the sweep: malformed or over 1 MiB); replace line 402. Remove the keyring table.
- Isolation pages (FR-041) with the same facts; `specs/023-oauth-state-persistence/plan.md:31`: strike-through note "not true; see Spec 123"; the `bbolt.go` comment that says the file holds OAuth tokens.

PR-2 (the feature):
- Extend the PR-1 sentences with "unless `oauth_token_encryption_key` is set" and the same-user caveat.
- New `docs/features/oauth-token-encryption.md` (published by the existing `features/**` glob in `website/docusaurus.config.js`; add it to `website/sidebars.js` next to `features/oauth-authentication`): threat model in the issue's words; **which delivery protects against what** (a disk/VM snapshot is covered only when the captured storage does not hold the means to recover the key: a TPM-bound `LoadCredentialEncrypted=` credential, an external secret manager or cloud secret store, a key on another volume; a host-key-only `LoadCredentialEncrypted=` credential, a plain `LoadCredential=` source file or a Compose file secret on the same volume covers copies of `config.db` / the data directory only, not a whole-disk snapshot); key generation (`openssl rand -base64 32`, `chmod 600`, outside `data_dir`); recipes for systemd (`${file:/run/credentials/mcpproxy.service/oauth-key}` or `MCPPROXY_OAUTH_TOKEN_ENCRYPTION_KEY_FILE=%d/oauth-key`), Docker secrets (`/run/secrets/…`), Kubernetes secret volumes (symlinked files are followed), env fallback, `${keyring:}` on desktops; enable → restart → `db compact --keep-backup=false` → delete any `config.db.bak`; old backups stay plaintext; key loss = `auth logout --server=<server>`, then log in; a missing mount is exit 4 at start; `secret_file_dirs` note once #1721 lands.
- `docs/configuration/config-file.md` and `docs/configuration/environment-variables.md`: the field and both env vars; `docs/configuration.md` tables too (unpublished, kept in sync).
- `docs/errors/MCPX_OAUTH_TOKEN_UNDECRYPTABLE.md`, plus its entries in `docs/errors/README.md` and in the OAuth group of `website/sidebars.js` (error pages are enumerated there, lines ~206-212).

PR-3 (surfaces):
- The new page gains: what doctor shows; downgrade / disable / rotation (decrypt with the old key, change the key, encrypt, then `db compact --keep-backup=false`, delete any `config.db.bak`, and a warning about copies made while rows were plaintext) with `auth tokens decrypt|encrypt` and the offline key form (`MCPPROXY_OAUTH_TOKEN_ENCRYPTION_KEY_FILE=<source> mcpproxy auth tokens …`, `docker compose run`, `systemd-run -p LoadCredential=…`).
- `docs/cli/command-reference.md`: `auth tokens` and the new `doctor` section (doctor is at line ~138); `docs/api/rest-api.md`: the `oauth_token_storage` field of `GET /api/v1/diagnostics` (~line 1779); `docs/cli/db-commands.md`: the compaction and backup note.
- CLAUDE.md "Recent Changes" entry when the implementation lands (not in this spec PR).

## Test plan

| Layer | Test | Proves |
|---|---|---|
| tokenseal | round trip; fresh nonce per seal (1 000 seals, no repeat); AAD mismatch on bucket key / bucket, or prefix kid edited to the configured kid → `auth_failed`; wrong key, or prefix kid edited to any other value → `key_mismatch`; nil key → `no_key`; truncated payload (base64-alphabet characters with at most two trailing `=` that does not decode, or is shorter than nonce + tag; `AAAA=`) → `auth_failed`; damaged header or empty payload → `malformed` under every key state; non-alphabet characters or a `=` other than as the last one or two payload characters (`AA=A`) → `malformed` only with the matching key, and `no_key` / `key_mismatch` under no key / another key (precedence: header, then key state, then payload); `enc:v2:` → `unsupported_version`; NUL in bucket or bucket key rejected by `Seal`/`Open`; plaintext over 1 MiB rejected by `Seal`; `ParseKey` table (31/33 bytes, URL alphabet, whitespace, padding); HKDF known-answer vector; no key material in `%v`/`%+v`/`%#v` (`String`/`GoString` print the key id only); leaf deps test | FR-001..006 |
| storage | every exported OAuth method against plaintext, sealed and undecryptable rows (table over methods); writers refuse undecryptable rows and bytes are unchanged; key-only deletes still delete; `ListOAuthTokens` tolerant + `ListOAuthTokenRows`; orphan cleanup keeps undecryptable rows; no-key bytes golden-equal to `main` (SC-003); sweep is one tx (fault injected through `putOAuthRecordHook`; a per-row unsealable row is skipped with a warning and left unchanged), `SaveOAuthToken` overwrites a corrupt non-envelope row with no key but refuses any `enc:` row and every undecodable row with a key, collects before writing, and leaves zero plaintext rows over several hundred ~1 KiB rows spanning many leaf pages; an ordinary write over a plaintext row sets the marker; source tests for the codec seam (`go/types`), the open sites and the read-path inventory | FR-010..017 |
| **raw file** | `TestOAuthTokensNotInRawFile`: seed three rows with marker strings, open with a key, sweep, close, `bbolt.Compact` into a new file, `os.ReadFile` both files: markers present in the pre-compact file's free pages are allowed only before compaction, **absent from the compacted file**; a login write made with the key set never appears even before compaction; plus the inverse control (no key → markers present) so the scan is not vacuous | SC-001 |
| config | resolution table (field literal / `${file:}` / `${env:}` / env value / env file / both env → error / unresolvable / empty / malformed) returning `*StartupError` exit 4, `KeySource` kind and `InDataDir` (through a symlink), and a grep that no error text contains the path or value; `Validate` is syntax-only (an unreadable `${file:}` path loads fine; a malformed reference fails); hot-reload canary; key file removed after start + unrelated hot-reload edit still applies; redaction: literal always masked (also with `reveal_secret_headers`), references verbatim, read + apply round trip; save writes the reference; bad key → exit 4 for daemon start and key-aware storage-opening commands, socket-only commands and `db compact`/`db stats` unaffected | FR-020..024, SC-004 |
| oauth / runtime | every row of the D8 read-path table returns or records the typed error, none starts a login; refresh manager skips undecryptable rows and schedules the rest; login against an undecryptable row fails and leaves bytes; boot order test (sweep in `runtime.New` before the upstream and refresh managers are constructed; a transaction-level sweep failure makes `runtime.New` close storage and return an error); manual login against an undecryptable row is refused before any discovery/DCR/callback request; `stale_oauth_token_health_test.go`-style health mapping → `configure` + code, including for a connected server (never `login`); sweep warns with bucket key and reason for each skipped plaintext row | FR-013, FR-016, FR-031, US2 |
| management / diagnostics | doctor section counts and warnings, scoped-caller filtering, catalog contract for the new code | FR-030, FR-031 |
| cmd | `auth tokens status|encrypt|decrypt` happy paths, `--dry-run`, locked DB → exit 3, no key → exit 4, `-o json` shape, config reference unreadable but env `_FILE` supplies the key → works; standalone `auth logout` with a missing key file / unset env / absent keyring entry deletes the row, while both-env-set, bad reference and empty/oversized/malformed values still exit 4; `db compact` with the default `--keep-backup=true` keeps the marker and prints the backup note, `--keep-backup=false` clears it; doctor `backup_file_present` while a `.bak` exists | FR-032, D9 |
| e2e | `./scripts/test-api-e2e.sh` unchanged (no key); one new e2e step in the OAuth fixture run with a key (login → raw scan → restart without key → health detail → restart with key → authenticated) | US1, US2 |

## Constitution Check

| Principle | Status | Note |
|---|---|---|
| Security by default | PASS | Opt-in, so no behaviour change by default; when on, fail-closed for writes and loud for reads; key never logged |
| No new dependencies | PASS | stdlib only |
| Test-first | PASS | tasks.md orders failing tests before each implementation group |
| Both editions | PASS | untagged code; broker store untouched |
| Docs accuracy | PASS | false claims removed in PR-1, ahead of the feature |

## Risks

- **A missed open site writes plaintext** → source test (FR-017).
- **A missed decode site misreads an envelope as JSON** → source test (FR-010) plus the per-method table test over sealed rows.
- **Operators believe enabling alone protects old backups** → docs, doctor `compaction_advised`, compact output.
- **Key file placed in the data directory** → doctor warning.
- **Key source on the same disk that gets snapshotted** (plain `LoadCredential=` / Compose file secret) → not detectable by the service; docs state which delivery covers snapshots.
- **A new token read site maps the typed error to "no token" and starts a login** → read-path inventory source test (D8).
- **Downgrade surprise** → docs and release notes; `decrypt` command.
