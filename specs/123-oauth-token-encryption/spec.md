# Feature Specification: Encrypt Upstream OAuth Tokens at Rest

**Feature Branch**: `123-oauth-token-encryption`
**Created**: 2026-11-11
**Status**: Draft
**Input**: GitHub issue [#1697](https://github.com/smart-mcp-proxy/mcpproxy-go/issues/1697) (upstream OAuth access tokens, refresh tokens and DCR client secrets are stored as plain JSON in the `oauth_tokens` bucket of `config.db`). Thread inputs settled on 2026-10-20: opt-in; both editions; the threat is a copy of `config.db` at rest (backups, disk and VM snapshots, images, a disk mounted elsewhere), explicitly **not** a process running as the same user that can read the key; the key is delivered mainly as a mounted file through the `${file:}` secret provider (systemd `LoadCredential`, Docker and Kubernetes secrets), an environment variable is an acceptable fallback, and the keyring is optional where one exists. The docs that claim the tokens are already encrypted in the system keyring are wrong and are corrected by this feature.

**Related**: #1706 / #1716 / #1717 (`MCPPROXY_API_KEY_FILE` and the `${file:}` provider, merged), #1704 (closed; a stdio upstream can read the data directory, so same-user readers are mitigated by isolation and `enable_socket: false`, not by this feature), #1721 / PR #1884 (open; `secret_file_dirs` allow-list for `${file:}`, which the key reference inherits once it lands), Spec 116 (config commit, purge set, incarnation- and epoch-bound OAuth writes), Spec 024 / `internal/serveredition/broker` (the server edition's separate AES-256-GCM store for per-user credentials).

All code references are to `origin/main` at `9bd568a08` unless stated otherwise.

## Context and Motivation

When MCPProxy signs in to an upstream MCP server over OAuth it persists one `storage.OAuthTokenRecord` per server in the `oauth_tokens` bucket of `config.db`. The record holds the access token, the refresh token, the token type, expiry and scopes, and the DCR client id and client secret (`internal/storage/models.go:612-655`). It is written by `json.Marshal` (`OAuthTokenRecord.MarshalBinary`, `models.go:709`) through the single write seam `putOAuthRecord` (`internal/storage/oauth_binding.go:51`). The file is created `0600` inside a `0700` directory, and that is the only protection. A copy of `config.db` yields long-lived refresh tokens for every connected account. A refresh token keeps working after the proxy's API key is rotated, so it is the most valuable secret in the file.

`internal/storage` has no build tags, so both editions write identical plaintext rows. The server edition's AES store (`internal/serveredition/broker/bbolt_aes.go`, `//go:build server`, key `server_edition.credential_encryption_key`) protects a different bucket (`user_upstream_credentials`) and does nothing for the tokens the proxy itself holds.

Three published statements in `docs/features/oauth-authentication.md` say the opposite of the truth: line 83 ("Stores tokens securely in system keyring"), lines 216-224 (a "Token Storage" section with a Keychain / Credential Manager / Secret Service table), and line 402 ("Tokens are encrypted in system keyring"). An operator who relies on those lines has a false picture of their exposure today, whatever this feature does.

## The Decision

An opt-in, edition-neutral key, `oauth_token_encryption_key`. With a key set, every OAuth record is written as a versioned AES-256-GCM envelope bound to its bucket key. Reads accept the envelope and legacy plaintext. The daemon seals any remaining plaintext rows once at boot. A row that cannot be opened is reported, never deleted or overwritten by a normal write path, so a missing or wrong key is a "fix the key" problem and does not silently log every server out. Without a key, behaviour and bytes on disk are exactly as today.

What this protects: copies of `config.db`, of its backups and of the data directory taken after the rows were sealed and the file was compacted, **as long as the copy does not also contain the key**. A disk or VM snapshot, an image or a disk mounted elsewhere is protected only when the key is not stored on that disk in readable form: a TPM- or host-key-encrypted systemd credential (`LoadCredentialEncrypted=`), a tmpfs-backed Kubernetes or Swarm secret, or a cloud secret store. A plain `LoadCredential=` source file or a Docker Compose file secret that lives on the same volume as the data directory protects only against copies of `config.db` or of the data directory, not against a snapshot of the whole disk. What it does not protect: anything that can read the key, which includes the running process, its environment and memory, and any process running as the same user that can read the key file. The wording in every doc follows that split.

## Scope Boundary

Delivered as three PRs, each reviewable on its own:

- **PR-1 (docs correction, ships first, independent of the feature)**: FR-040 and FR-041 stated with today's facts only (tokens are stored in `config.db` in readable form, protected by file permissions; the isolation pages say so plainly). No mention of a key that does not exist yet; PR-2 adds the encryption sentences.
- **PR-2 (the feature core, one PR)**: the envelope and its leaf package, the config field and environment variables with resolution at the storage-open sites, sealing on write, tolerant reads, fail-closed writers, the boot sweep, the typed error on every token read path with its health mapping and diagnostics code, the config gates (hot reload, OAS, redaction), the new docs page, and the tests including the raw-file grep.
- **PR-3 (operator surfaces)**: the doctor / diagnostics section, offline `mcpproxy auth tokens status|encrypt|decrypt`, the compaction marker and `db compact` handling, and the login pre-flight.

PR-2 and PR-3 ship in the same release: the field is not announced in release notes until PR-3 has merged, because PR-3 carries the supported downgrade path (`decrypt`).

Out of scope (follow-ups, see the list at the end): single-step key rotation, migrating the server-edition broker store onto the shared helper, telemetry, a new OAuth status enum value, encrypting other secrets in `config.db`, and a strict "refuse to start without a key" switch.

## Definitions

- **Envelope**: the stored value of a sealed row: `enc:v1:<kid>:<payload>`, where `<kid>` is 16 lower-case hex characters and `<payload>` is standard base64 (with padding) of `nonce(12) || ciphertext || tag(16)`.
- **Key**: 32 random bytes, configured as standard base64. The **record key** and the **key id** are derived from it with HKDF-SHA256 (plan §Envelope).
- **Key id (kid)**: a public, non-secret fingerprint of the key, used to tell "wrong key" apart from "tampered row" and shown in doctor output.
- **Bucket key**: the bbolt key of the row, `<server>_<16 hex>` or a legacy bare name (`oauthKeyBelongsTo`, `internal/storage/manager.go`). It is bound into the AEAD additional data.
- **Sealed row**: a row whose value starts with `enc:`. **Plaintext row**: a row whose value starts with `{`.
- **Undecryptable row**: a sealed row that cannot be opened with the configured key, for one of five reasons: `no_key` (no key configured), `key_mismatch` (kid differs from the configured kid), `auth_failed` (kid matches but GCM authentication fails: tamper, truncation or a row moved to another bucket key), `unsupported_version` (`enc:` with a version other than `v1`), `malformed` (envelope or inner JSON cannot be parsed, or a plaintext row with invalid JSON).

## User Scenarios & Testing

### User Story 1: An operator turns encryption on and a copy of the database yields no tokens (Priority: P1)

A Linux operator runs MCPProxy under systemd with three OAuth upstreams already signed in. They create a key outside the data directory, deliver it with `LoadCredentialEncrypted=` (or `LoadCredential=` when only copies of `config.db`, not disk snapshots, are the concern), set `oauth_token_encryption_key: "${file:/run/credentials/mcpproxy.service/oauth-key}"` (or `MCPPROXY_OAUTH_TOKEN_ENCRYPTION_KEY_FILE=%d/oauth-key`), restart, and run `mcpproxy db compact --keep-backup=false`. A later backup of `config.db` contains no token, refresh token or client secret in readable form.

**Why this priority**: this is the protection the issue asks for. Without it nothing else in the feature matters.

**Independent Test**: seed a data directory with three plaintext rows that carry known marker strings (`ACCESS-MARKER-…`, `REFRESH-MARKER-…`, `SECRET-MARKER-…`). Start the core with a key, stop it, compact without a backup, and scan the raw bytes of `config.db` for every marker. Repeat with a fresh login made while the key is set.

**Acceptance Scenarios**:

1. **Given** three plaintext rows and a key, **When** the core starts, **Then** the boot sweep seals all three in one transaction before any upstream connects or any refresh is scheduled, and every server stays signed in (same access token, same expiry, same client id).
2. **Given** the core running with a key, **When** a new OAuth login completes (token and DCR client written together through `oauth.WithLoginClient`), **Then** the row is written as an envelope in the same transaction as today, and no marker from it appears in the raw file.
3. **Given** the sweep sealed rows, **When** the operator runs `mcpproxy doctor`, **Then** it reports encryption on, the key id, the counts (sealed 3, plaintext 0, undecryptable 0), and advises `mcpproxy db compact` because freed pages can still hold the old plaintext. A default `db compact` (which keeps `config.db.bak`, the pre-compaction file) does not clear the advice. After `db compact --keep-backup=false` the marker is gone; while any `config.db.bak` remains in the data directory doctor still names it, and once it is deleted no warning remains and a raw scan of `config.db` finds none of the markers.
4. **Given** a key set, **When** a token refresh writes the row, **Then** the row stays sealed, the revision (Spec 116 R7.3) increases as today, and the refresh manager reads the new expiry back.
5. **Given** no key set, **When** the core runs, logs in and refreshes, **Then** every row is written byte-for-byte as before this change (plain JSON, same field order), and doctor reports encryption off without warnings.

---

### User Story 2: A missing or wrong key is reported and loses nothing (Priority: P1)

An operator restarts with the key reference removed from the config (no key configured), or with a different key mounted. Sealed rows stay in the file untouched, every affected server says why it is not signed in, and restoring the right key brings every server back without a browser login. A configured reference whose file is missing (the secret mount is absent) is User Story 3: the core does not start, and the file is not touched either.

**Why this priority**: the issue's explicit requirement. A key mistake that silently turns into "log in again everywhere" makes operators turn the feature off.

**Independent Test**: seal two rows with key A. Start once with no key, once with key B, and once with a row whose envelope was copied under another server's bucket key. Assert the raw values of all rows are byte-identical after each run, the per-server reason, the doctor output, and that restarting with key A restores both servers' signed-in state.

**Acceptance Scenarios**:

1. **Given** sealed rows and no key configured, **When** the core starts, **Then** it starts (exit 0), every non-OAuth server works, each affected server's health is `unhealthy` with detail "stored OAuth token cannot be decrypted: no encryption key configured", action `configure`, and diagnostics code `MCPX_OAUTH_TOKEN_UNDECRYPTABLE`; the UI does not offer "Log in" as the fix.
2. **Given** sealed rows and key B, **When** the core starts, **Then** the reason is `key_mismatch` and doctor prints both key ids (the row's and the configured one), never key material.
3. **Given** an envelope copied from server `a`'s row into server `b`'s row, **When** `b` is read with the right key, **Then** the open fails with `auth_failed` (the AAD binds the bucket key) and `b` is reported, not served `a`'s token.
4. **Given** an undecryptable row, **When** a refresh, a login, a DCR client save or a conditional clear tries to write it through any bound writer, **Then** the write is refused with the undecryptable error and the stored bytes do not change.
5. **Given** an undecryptable row, **When** startup orphan cleanup runs, **Then** it classifies the row by its bucket key only, keeps it when the prefix names a configured server, and never deletes a row it could not decode; the listing of all other rows still succeeds (one bad row no longer aborts `ListOAuthTokens`).
6. **Given** an undecryptable row, **When** the operator runs `mcpproxy auth logout <server>`, removes the server, or changes its OAuth config (Spec 116 OAuth clear), **Then** the row is deleted, because those are deliberate discards that act on the bucket key.
7. **Given** the operator restores key A and restarts, **Then** both servers are signed in with their original tokens.
8. **Given** an undecryptable row, **When** the operator starts a login for that server (`mcpproxy auth login`, the REST login trigger, or the connection path), **Then** it is refused with `MCPX_OAUTH_TOKEN_UNDECRYPTABLE` before any browser or DCR round trip, and the message names the two ways out: restore the key, or `mcpproxy auth logout <server>` and then log in.

---

### User Story 3: The key reference itself is wrong (Priority: P1)

The operator writes `${file:/run/secrets/oauth-key}` but the file is absent, empty, unreadable, or holds something that is not a base64 32-byte key.

**Independent Test**: config load with each bad input; assert the exit code and that no stderr or log line contains the reference path or the key bytes.

**Acceptance Scenarios**:

1. **Given** a key reference that cannot be resolved (including a secret mount that is missing), or resolves to a value that is not standard base64 of exactly 32 bytes, **When** `mcpproxy serve` or any command that opens `config.db` starts, **Then** it exits with code 4 (config error) and a fixed sentence naming the field and the problem class, without the reference, the path or the value, and the raw bytes of every `oauth_tokens` row are unchanged.
2. **Given** both `MCPPROXY_OAUTH_TOKEN_ENCRYPTION_KEY` and `MCPPROXY_OAUTH_TOKEN_ENCRYPTION_KEY_FILE` set, **Then** a command that opens `config.db` fails with exit 4, as `MCPPROXY_API_KEY` / `_FILE` do.
3. **Given** a running core, **When** the operator edits `oauth_token_encryption_key` in the config file, **Then** the reload reports "requires restart", the running process keeps the key it started with, and a save writes back the reference, never the resolved key.
4. **Given** an unresolvable key reference, **When** the operator runs a command that does not open `config.db` (`mcpproxy doctor` against the socket, `upstream list`, `activity list`), **Then** it runs normally: the key is resolved only where storage is opened, and config validation checks only the reference syntax.
5. **Given** a running core, **When** the key file disappears or changes (a Kubernetes secret deleted or rotated) and the operator then makes an unrelated config edit, **Then** the hot reload applies the edit and the running process keeps the key it started with.

---

### User Story 4: Turning it off, and downgrading, are deliberate steps (Priority: P2)

An operator wants to downgrade to a release without this feature, or stop using encryption.

**Independent Test**: seal rows, stop the core, run `mcpproxy auth tokens decrypt --yes`, remove the key, start an older binary's code path (simulated by reading the rows with plain `json.Unmarshal`), assert all rows decode.

**Acceptance Scenarios**:

1. **Given** sealed rows and the key available to the operator's shell (a service's own credential directory such as `/run/credentials/mcpproxy.service/` exists only while the unit runs, so the docs show the offline form `MCPPROXY_OAUTH_TOKEN_ENCRYPTION_KEY_FILE=<key source> mcpproxy auth tokens decrypt --yes`, plus `docker compose run` with the same secret and `systemd-run -p LoadCredential=…`), **When** the core is stopped and the operator runs `mcpproxy auth tokens decrypt --yes`, **Then** every row it can open is rewritten as plaintext JSON in one transaction, undecryptable rows are listed and left as they are, and the command exits non-zero if any row was left sealed.
2. **Given** plaintext rows and a key, **When** the core is stopped and the operator runs `mcpproxy auth tokens encrypt`, **Then** the rows are sealed exactly as the boot sweep would; `--dry-run` reports counts and writes nothing.
3. **Given** the core running, **When** any `auth tokens` subcommand that writes is run, **Then** it fails with exit 3 (database locked) and changes nothing.
4. **Given** the docs, **Then** they state that an older binary cannot read sealed rows (servers show login required and a new login cannot overwrite the row) and that `decrypt` before downgrading is the supported path.

---

### User Story 5: The docs tell the truth (Priority: P2)

**Acceptance Scenarios**:

1. **Given** `docs/features/oauth-authentication.md`, **Then** no line claims tokens are stored in or encrypted by the system keyring; the "Token Storage" section says where they are, that they are plaintext by default, and links to the new encryption section.
2. **Given** the sandbox and Docker isolation pages that say `config.db` holds the upstream OAuth tokens, **Then** they say it holds them in readable form unless `oauth_token_encryption_key` is set, and that even then a same-user process that can read the key file is not stopped.
3. **Given** the new docs section, **Then** it states the threat model in the issue's words, the key formats and delivery recipes (systemd `LoadCredential`, Docker secret, Kubernetes secret volume, env fallback, keyring), which delivery methods protect against disk and VM snapshots and which only against copies of `config.db`, the boot sweep, the compaction step and the backup caveat, key loss (`auth logout <server>`, then log in again), the downgrade path with the offline key form, rotation with today's commands, and that the key file must not live in the data directory.

### Edge Cases

- **Literal key in `mcp_config.json`**: accepted (same as `credential_encryption_key`), but doctor warns that a copy of the data directory includes the config file and therefore the key.
- **Key file inside the data directory**: accepted, doctor warns for the same reason (a resolved `${file:}` path or `_FILE` path under `data_dir`).
- **Same key used for `credential_encryption_key`**: works; HKDF domain separation keeps the derived keys distinct. Docs recommend separate keys so the two stores rotate independently. There is no implicit fallback to `MCPPROXY_CRED_KEY`.
- **Plaintext row appears while a key is set** (restored backup, row written by an older binary sharing the directory): read normally; sealed on its next write or at the next boot. Whichever write seals it also sets the compaction-advised marker in the same transaction, because the overwritten plaintext page is now free but not zeroed.
- **A row that is plaintext but invalid JSON**: today it aborts `ListOAuthTokens`; now it is reported as `malformed` and skipped by listing, like an undecryptable row.
- **Legacy bare-name bucket keys** (no hash suffix): sealed and bound the same way; AAD is whatever the bucket key is.
- **Revision**: assigned before marshal, so it lives inside the sealed payload; the sweep and `encrypt`/`decrypt` write through `putOAuthRecord` and so take a new revision. Nothing is in flight at boot or offline, so no flight sees the bump.
- **Backups made by `BoltDB.Backup`**: page copies; they are exactly as sealed as the file at that moment. Backups taken before enabling keep plaintext forever and must be handled by the operator.
- **`db compact` default `--keep-backup=true`**: the retained backup is the pre-compaction file and still holds freed plaintext pages. Compaction with a retained backup keeps the marker; only `--keep-backup=false` clears it. Independently of the marker, doctor reports any `config.db.bak` in the data directory while encryption is on (`backup_file_present`), because `--keep-backup=false` does not remove a `.bak` left by an earlier run.
- **Key source on the same disk as the data directory**: not detected. The service sees only the delivery path (usually tmpfs under `/run`), not where the source file lives, so a same-filesystem check would be wrong in the common case. The docs carry this instead (see "What this protects").
- **Standalone CLI commands that open the database** (`auth login|logout|status` via `internal/upstream/cli`, `call`, `code`, `tools`, `connect` credential path): they open storage with the same key as the daemon. One that opened without a key would read sealed rows as undecryptable and refuse to overwrite them, but it could write a new plaintext row; a source test forbids such an open site.
- **Server edition**: the same field and behaviour; the broker's `user_upstream_credentials` store is untouched.
- **#1721 `secret_file_dirs`**: when it lands, the key's `${file:}` path is subject to the allow-list like any other reference. Nothing here depends on it.

## Requirements

### Functional Requirements: format and crypto

- **FR-001**: The system MUST seal an OAuth record as `enc:v1:<kid>:<base64(nonce||ciphertext||tag)>` using AES-256-GCM from the Go standard library, with a fresh 96-bit nonce from `crypto/rand` for every seal and no nonce counter or persisted nonce state.
- **FR-002**: The AEAD key and the key id MUST be derived from the configured 32-byte key with HKDF-SHA256 (`crypto/hkdf`) using distinct, versioned info strings, so the configured key is never used directly and a key shared with another store yields unrelated derived keys.
- **FR-003**: The additional data MUST bind the envelope version, the key id, the bucket name and the bucket key, so an envelope moved to another server's bucket key or another bucket fails authentication (`auth_failed`), and a prefix whose kid was edited is either `key_mismatch` (edited to a kid other than the configured one, detected before any decrypt) or `auth_failed` (edited to the configured kid).
- **FR-004**: The whole serialized record MUST be sealed (access token, refresh token, client id, client secret, scopes, expiry, display name, binding epoch, revision); no field of a sealed row is stored in readable form.
- **FR-005**: Reads MUST accept both plaintext rows (first byte `{`) and `enc:v1` envelopes on every read path. A value with any other shape MUST be classified as undecryptable with the reasons defined above.
- **FR-006**: The seal/open primitives MUST live in a new, untagged, stdlib-only leaf package that `internal/storage` imports; `internal/storage` MUST NOT import `internal/serveredition` or `internal/secret`. The server-edition broker's stored format MUST stay unchanged.

### Functional Requirements: storage behaviour

- **FR-010**: Every write to the `oauth_tokens` bucket MUST go through one encode function next to `putOAuthRecord`, and every value decode of that bucket MUST go through one decode function; no other code in `internal/storage` may call `OAuthTokenRecord.MarshalBinary`/`UnmarshalBinary` on that bucket's values. A source test enforces it.
- **FR-011**: With a key configured, every write MUST produce an envelope. Without a key, every write MUST produce the same plaintext JSON bytes as today.
- **FR-012**: A read of an undecryptable row MUST return a typed error (`storage.ErrOAuthTokenUndecryptable` carrying the reason, the bucket key and both key ids when known), distinct from "not found".
- **FR-013**: No writer that starts from the stored record MAY overwrite, replace or delete an undecryptable row: `UpdateOAuthTokenIfBound`, `SaveOAuthClientIfBound`, the revision- or client-id-conditioned instance clears (`DeleteOAuthTokenIfBound`, `ClearOAuthClientIfBound`, `ClearOAuthClientIfBoundAndClientID`) and the unconditional storage-internal mutators (`SaveOAuthToken`, which today puts without reading and so MUST first read and refuse an undecryptable row, `UpdateOAuthToken`, `UpdateOAuthClientCredentials`, `saveOAuthClientCredentialsIf`, `clearOAuthClientCredentialsWhere`, `ClearOAuthClientCredentials`) MUST fail with the typed error and leave the bytes unchanged. Deletes that act on the bucket key alone as a deliberate operator or commit decision (`DeleteOAuthToken`, `ClearOAuthTokenForStandaloneLogout`, `ClearOAuthState`/`clearOAuthStateTx`, the Spec 116 purge set and the commit's `OAuthClears`) MUST keep deleting such rows.
- **FR-014**: Listing MUST be per-row tolerant: `ListOAuthTokens` returns every decodable record and skips undecryptable ones (logging bucket key and reason once per listing), and a new row-level listing returns the classification of every row for status, doctor and the CLI.
- **FR-015**: Orphan cleanup MUST NOT delete a row it could not decode; such rows are kept and reported.
- **FR-016**: With a key configured, the daemon MUST seal every plaintext row in one bbolt transaction in `runtime.New`, immediately after storage opens and before any other component that reads or writes OAuth rows is constructed. The sweep MUST collect the (bucket key, value) pairs first and write after the walk ends (bbolt cursors skip keys when the bucket is mutated during iteration). Sealed rows and undecryptable rows MUST NOT be touched by the sweep. Every write that replaces a plaintext value with an envelope (the sweep, `encrypt`, or an ordinary write over a plaintext row) MUST set a persisted "compaction advised" marker in the same transaction. `mcpproxy db compact` clears the marker only when it keeps no backup (`--keep-backup=false`).
- **FR-017**: Every production site that opens the bbolt database (`runtime.New`, `appctx`, `cmd/mcpproxy` `call`/`code`/`tools`/`connect` paths, `internal/upstream/cli`) MUST open it with the same resolved key as the daemon would; a source test MUST fail on a new open site that does not pass the key option.

### Functional Requirements: configuration and key delivery

- **FR-020**: A top-level config field `oauth_token_encryption_key` (string, default empty = off) MUST accept a secret reference (`${file:}`, `${env:}`, `${keyring:}`) or a literal. The resolved value MUST be standard base64 (padding optional, surrounding whitespace trimmed) of exactly 32 bytes. The key MUST be resolved only where `config.db` is opened (`runtime.New` and the CLI storage-open helper), at most once per process; config validation (which runs on every load, every CLI command and every hot reload) MUST check only the reference syntax and never read the file, the environment or the keyring.
- **FR-021**: Environment variables `MCPPROXY_OAUTH_TOKEN_ENCRYPTION_KEY_FILE` (path, read with the `${file:}` rules: absolute path, regular file, size cap, one trailing newline stripped) and `MCPPROXY_OAUTH_TOKEN_ENCRYPTION_KEY` (value) MUST override the config field; both set MUST be a config error. There MUST be no fallback to `MCPPROXY_CRED_KEY` or `server_edition.credential_encryption_key`.
- **FR-022**: An unresolvable or malformed key MUST stop the process that tried to open `config.db` with exit code 4, carried as a `*config.StartupError` (not left to the string heuristics of `classifyError`), with a fixed sentence that never contains the reference, the path or any key byte, before any byte of the database is written. A syntactically invalid reference MUST fail config validation with exit 4 the same way.
- **FR-023**: The key MUST be bound at startup: a config change to the field MUST be reported as restart-required, the running process MUST keep its key, and every config save MUST write the reference as configured, never the resolved key.
- **FR-024**: Config output on REST, CLI and logs MUST mask a literal value of the field; the field MUST NOT appear as a row in the Web UI or macOS Settings (env/secret-only, like `credential_encryption_key`).

### Functional Requirements: surfaces

- **FR-030**: `mcpproxy doctor` and `GET /api/v1/diagnostics` MUST include an OAuth token storage section: encryption on or off, the configured key id, counts over a strict partition of the rows (`plaintext`, `sealed` = opens with the configured key, `undecryptable`, the last broken down by reason, so a `key_mismatch` row is counted exactly once), the per-server undecryptable list with reason and the row's kid, the compaction advice, and warnings for a literal key, a key file inside the data directory, and a `config.db.bak` in the data directory. It MUST never include key material or the key reference.
- **FR-031**: A server whose stored row is undecryptable MUST surface in its health as `unhealthy`, action `configure`, detail naming the reason, and diagnostics code `MCPX_OAUTH_TOKEN_UNDECRYPTABLE` with a `docs/errors/` page; it MUST NOT be reported as "login required". Every production caller of `GetOAuthToken`, `GetOAuthTokenBound`, `HasPersistedToken*` and `GetPersistedRefreshToken` (inventory in plan D8) MUST distinguish the typed error from "no token"; none may treat it as absent and start an interactive login.
- **FR-032**: `mcpproxy auth tokens status|encrypt|decrypt` MUST be offline commands over the data directory: `status` prints the same section as doctor; `encrypt [--dry-run]` seals plaintext rows; `decrypt --yes` rewrites openable sealed rows as plaintext; both write commands run in one transaction (collect, then write), leave undecryptable rows alone, exit non-zero when any row remains in the unwanted state, and fail with exit 3 when the database is locked by a running core. All support `-o json`. They obtain the key exactly as the daemon does, so the environment override supplies it when the config's reference points at a path that exists only inside the service (systemd credentials directory, container secret mount).

### Functional Requirements: documentation

- **FR-040**: The false keyring claims in `docs/features/oauth-authentication.md` (lines 83, 216-224, 402) MUST be corrected, and `specs/023-oauth-state-persistence/plan.md:31` annotated as wrong.
- **FR-041**: The isolation pages (`docs/docker-isolation.md:515`, `docs/features/docker-isolation.md:435`, `docs/features/sandbox-isolation.md:94,109`) MUST qualify "holds the upstream OAuth tokens" with the encryption condition and the same-user caveat.
- **FR-042**: A new published section MUST document the feature per User Story 5, and `docs/configuration/` MUST list the field and both environment variables. The published docs site must link it from the OAuth page.

### Key Entities

- **OAuth token envelope** (`enc:v1`): version, key id, nonce, ciphertext and tag; AAD = version, kid, bucket name, bucket key.
- **Row classification** (a strict partition, each row in exactly one class): `plaintext`, `sealed` (opens with the configured key), `undecryptable{no_key|key_mismatch|auth_failed|unsupported_version|malformed}`.
- **Key source**: how the key was obtained (`literal`, `env`, `env_file`, `ref_file`, `ref_env`, `ref_keyring`) and whether a resolved file path lies inside the data directory (a boolean; the path itself is never kept). Computed at resolution and kept by the runtime for doctor.
- **Compaction-advised marker**: one key in the existing meta bucket, set by any write that replaced a plaintext value with an envelope, cleared by `db compact --keep-backup=false`.

## Success Criteria

- **SC-001**: After enabling, restarting and compacting without a backup, a byte scan of `config.db` finds none of the seeded access-token, refresh-token or client-secret markers (automated test over the real file).
- **SC-002**: With a missing key, a wrong key, or a swapped envelope, every stored row's bytes are identical before and after a full start, a refresh attempt, a login attempt and orphan cleanup, and restoring the right key restores every server's signed-in state with no browser login.
- **SC-003**: With no key configured, the bytes written for a token save, a DCR save and a refresh are identical to `main`'s for the same inputs.
- **SC-004**: No log line, stderr line, doctor output, REST response or config save made by any test in the feature contains the key bytes or the resolved key file path, checked by a grep over captured output.
- **SC-005** (informational, not a CI gate): `BenchmarkGetOAuthToken{Plaintext,Sealed}` mean ns/op and allocs/op are recorded in PR-2; the expected sealed overhead is single-digit microseconds per read.
- **SC-006**: The three false keyring statements are gone from the published docs.

## Assumptions

- **A1**: Rows are small (one per OAuth server, under 4 KiB) and few (tens), so one transaction for the sweep and for `encrypt`/`decrypt` is cheap.
- **A2**: Standalone CLI commands that open `config.db` run while the core is stopped (bbolt's file lock gives them exit 3 otherwise); the daemon is the normal writer. The operator running them may not have access to the service's credential directory, which is why the environment override is the documented offline form.
- **A3**: Go 1.26's standard library provides `crypto/aes`, `crypto/cipher`, `crypto/hkdf` and `crypto/rand`; no new module is needed.
- **A4**: The `oauth_completion` bucket holds no secrets (server name, binding and timestamps only) and stays plaintext. `PendingAuth` is in memory only.

## Out of Scope (follow-ups)

1. Single-step key rotation (`auth tokens rekey --old-key-file`) and an accepted list of previous keys. Rotation today is `decrypt` with the old key, then `encrypt` with the new one, both offline. The `kid` field makes a later multi-key reader possible without a format change.
2. Moving the server-edition broker's seal/open onto the new leaf package (must keep its `nonce||ct`, no-AAD byte format).
3. Telemetry (`oauth_token_encryption_enabled` would bump the schema from 16).
4. A new `OAuthStatus` value (`undecryptable`) and tray/Web UI copy; this PR uses `error` plus the diagnostics code and existing health fields.
5. Encrypting other readable secrets in `config.db`: literal `env`/`headers` values in the `upstreams` bucket and scanner records that hold environment values (`docs/cli/security-commands.md:218`).
6. A strict mode that refuses to start when sealed rows exist and no key is configured.
7. A `keygen` command; docs use `openssl rand -base64 32`.
8. Online (`--via-core`) versions of the `auth tokens` commands.
9. A doctor check that the key's source file lives on the same filesystem as the data directory (the service sees only the delivery path; see Edge Cases).
10. Removing the unconditional OAuth mutators that have no production caller today (`SaveOAuthToken`, `UpdateOAuthToken`, `UpdateOAuthClientCredentials`, `ClearOAuthClientCredentials`; only `purgetest` and the QA tool call `SaveOAuthToken`). PR-2 makes them refuse undecryptable rows through the shared decode and covers them in one table test.

## Commit Message Conventions

### Issue References
- Use `Related #1697` in commits. Do not use `Fixes`, `Closes` or `Resolves`; the issue is closed by hand after verification.

### Co-Authorship
- Do not include `Co-Authored-By: Claude …` or "Generated with Claude Code" lines.

### Example Commit Message
```
feat(storage): seal upstream OAuth tokens at rest with an opt-in key

Related #1697

With oauth_token_encryption_key set, every oauth_tokens row is written as an
enc:v1 AES-256-GCM envelope bound to its bucket key, plaintext rows are sealed
once at boot, and a row that cannot be opened is reported and never
overwritten. Without a key the bytes on disk are unchanged.

## Changes
- ...

## Testing
- ...
```
