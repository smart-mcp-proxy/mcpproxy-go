# Implementation Plan: MCP Router Importer

**Branch**: `claude/spec-mcp-router-import-7ae3fb` (spec dir `120-mcp-router-import`) | **Date**: 2026-10-20 | **Spec**: [spec.md](spec.md)
**Input**: spec.md (clarified, two Sol review rounds applied), research.md (verified MCP Router format)

## Summary

Add `mcpproxy import --from mcp-router [--path] [--dry-run] [--workspace] [--yes] [--secrets-inline]`. A new package `internal/mcprouter` locates MCP Router's data, takes a verified private snapshot of its SQLite database (or reads an export file), maps each server onto `config.ServerConfig` using the existing `internal/configimport` name sanitiser, validator and secret classifier, and produces a **Plan** (to-import / skipped / conflict / unmappable / needs-configuration, per-field handling, secret handling, clients to reconnect). The CLI renders the plan, asks one confirmation, stores secret-like values in the OS keyring, adds servers one at a time through the daemon (or the config file when no daemon runs), and reports the true per-server outcome. The daemon records one closed-enum telemetry count (`import_source`) when told that a run committed at least one addition. A Web UI entry reuses the same package through the existing import endpoints and is the last, droppable phase.

`upstream import` is **not** changed.

## Technical Context

**Language/Version**: Go 1.26 (existing). Vue 3.5 / TypeScript 5.9 for the optional Web UI phase.
**Primary Dependencies**: Existing: `internal/configimport`, `internal/secret`, `internal/cliclient`, `internal/telemetry`, `internal/config`, cobra. **One new dependency: `modernc.org/sqlite` (pure Go, no CGO)**, imported only by `internal/mcprouter`. Justified in D1.
**Storage**: Reads MCP Router's SQLite (read-only, via private snapshot). Writes MCPProxy config through the existing add path. Keyring through `internal/secret`.
**Testing**: `go test -race` unit tests with generated SQLite fixtures (built in-test with the same driver, so no committed binary files), table-driven mapping tests, CLI tests with the existing `cmd/mcpproxy` harness, httpapi handler tests, telemetry payload/privacy tests, one E2E script step.
**Target Platform**: macOS, Windows (MCP Router's real platforms), Linux (best effort). All three are existing release targets; `modernc.org/sqlite` supports them and needs no CGO, so cross-compilation is unchanged.
**Project Type**: single Go module plus embedded frontend.
**Performance Goals**: SC-002, 50 servers in < 5 s each for dry-run and apply (dominated by the snapshot copy and keyring writes).
**Constraints**: never write to MCP Router's files; never print/log/telemeter a secret; quarantine always set; no behavior change to `upstream import`; no new global state.
**Scale/Scope**: tens of servers per user; up to hundreds must stay readable.

## Constitution Check

| Principle | Status |
|-----------|--------|
| I Performance at scale | Pass. Offline one-shot command; no hot-path impact. Binary-size cost of D1 is measured and gated. |
| II Actor-based concurrency | Pass. Sequential command; no new shared mutable state. Telemetry uses the existing registry. |
| III Configuration-driven | Pass. Servers land in `mcp_config.json` via the existing add path; no new config keys. |
| IV Security by default | Pass, and the core of the design: quarantine always set (FR-012), secrets to keyring (FR-011), untrusted-source handling (FR-004a, FR-006b, FR-014), no decryption of foreign credentials (A-4). stdio isolation (Docker) is applied by the normal runtime path to the imported servers, unchanged. |
| V TDD | Pass. Each task pair is test-first; fixtures built in test. Lint with both CI invocations (bare and `--build-tags server`). |
| VI Documentation hygiene | Tasks include docs page, CLI docs, telemetry doc, REST docs/swagger, CLAUDE.md Recent Changes line. |

Post-design re-check: the only deviation is the new dependency (D1), documented with measurement and an exit hatch.

## Decisions

### D1. SQLite reader: `modernc.org/sqlite`, isolated, with a measured gate

Options: (a) `modernc.org/sqlite` (pure Go); (b) shell out to `sqlite3` (absent on Windows, so unusable for half of MCP Router's users); (c) a hand-written reader of the SQLite file format including WAL replay; (d) export-file only.

Chosen: **(a)**. Measured on 2026-10-20 (`go1.26`, darwin/arm64, unstripped): a hello-world binary grows from 2.5 MB to 9.4 MB with the driver, +6.9 MB, against the current 46.9 MB `mcpproxy` build, +15% unstripped; the release build is stripped, so the real delta is smaller. Rejected (c): a format reader with WAL replay is ~600 lines of subtle code whose failure mode is silently wrong data. Rejected (d) as the primary path: the export drops remote servers (research §3), which is the main value.

Gate (task T003): measure the **stripped** release-binary delta on linux/amd64, darwin/arm64 and windows/amd64. If any exceeds 10 MB, stop and escalate; the fallback is shipping (d) alone (Story 6 and the export path need no driver) and keeping the direct-DB reader behind a build tag. The dependency is imported by exactly one package so that fallback is a build-tag change.

### D2. Snapshot protocol (FR-005)

All source files are opened through an `os.Root` (Go 1.24+) rooted at the validated data directory (D3), so a path can neither leave the root nor be swapped for a symlink that does. Copies are made **from the open descriptors**.

1. For each attempt: open `mcprouter.db` (or the named workspace db), its `-wal` (if present) and `-shm` (if present) via the root; `Fstat` each and require a regular file; record identity (`os.SameFile` handle plus size, mtime) and which sidecars existed.
2. Enforce limits before copying: total bytes of db+wal ≤ **1 GiB** (otherwise `ErrSourceTooLarge`, advice: use the export file; MCP Router also stores request logs in these databases).
3. Copy the descriptors into a `0700` directory from `os.MkdirTemp`; then re-`Fstat`/re-stat the *names* through the root and compare to step 1. Any difference (size, mtime, different file identity, a sidecar appeared, vanished or was replaced, i.e. checkpoint/truncate/recreate) discards the copy and retries.
4. Open the copy read-only with a context carrying a 30 s deadline, run `PRAGMA quick_check`; failure counts as a failed attempt.
5. Up to 3 attempts, then `ErrNoConsistentSnapshot` (exit class (e)) with advice to close MCP Router or use the export file.
6. The temp directory is removed on every exit path including SIGINT (the command's signal-aware context).

`quick_check` establishes structure, not generation consistency; the pre/post identity comparison plus the retry bound is the consistency argument, and "a torn snapshot is never read as consistent" is enforced by tests that rewrite, checkpoint, truncate and replace the WAL during the copy. SQLite's online backup or `VACUUM INTO` would be stronger but opens the live database (it may create `-shm`), which FR-005 forbids.

### D3. Path containment and hostile input (FR-004a, FR-007, FR-014)

`Locate(path)` returns an `Input{Kind, Root, Files}`. Kinds: `data_dir`, `database`, `export_file`. Root handling: for `data_dir` the root is the directory itself (canonicalised once with `EvalSymlinks` at the start, which is the user's own `--path` choice, not source data); for `database` the root is its parent directory and the only candidates are that file plus sidecars; for `export_file` no root, the single file is read once with a 4 MiB cap.

Everything after that goes through `os.OpenRoot(root)`: containment is component-aware and symlink-escape-resistant by construction, with no string-prefix test and no check-then-open gap. Workspace databases: the workspace list comes from the `workspaces` table of the snapshot of `mcprouter.db`, but `localConfig.databasePath` is untrusted data; a candidate is accepted only if it is exactly `mcprouter.db` or `workspaces/<id>/database.db` with `<id>` matching `[A-Za-z0-9._-]{1,64}` and not `.`/`..`. Others produce warning `workspace_path_rejected` (id only) and nothing is opened. If the `workspaces` table is absent (very old databases), fall back to one workspace `local-default` backed by the file given.

**Bounds** (all before materialising data, all cancellable via context): ≤ 5,000 servers; name ≤ 256 bytes; every other string field ≤ 64 KiB; `args` ≤ 1,024 entries; JSON columns decoded with a size cap and depth ≤ 8; SQL queries read with a 30 s deadline. Over-limit rows are failed individually (`row_too_large`). Remote URLs must parse as `http`/`https` with a host (other schemes → unmappable, a check added by this importer because `ValidateServerForLoad` does not do it). Names and any other source-controlled text rendered to a terminal are escaped (`strconv.Quote` style) so control characters and ANSI sequences cannot reach the screen. `command`/`args` paths are never opened.

### D4. Secret handling (FR-006a, FR-009 to FR-011, FR-017)

- **Provenance from every branch.** `ResolveArgs` returns, for each argument slot, where the value came from: literal, parameter default (with the parameter's `sensitive` flag and name classification), or env (with that env name's classification). A slot is `sensitive` when its source was a `sensitive` parameter, a secret-like-named parameter, **or a secret-like-named env var**, so `args=["${API_KEY}"]` with `env.API_KEY="ordinary-looking-secret"` and no schema is still protected, masked and compared as sensitive.
- **Classification** reuses the `configimport` name-based classifier through new exported wrappers (`ClassifyEnv`, `ClassifyHeaders`; `Reclassify` is already exported, `describeFields` is not). Plain-text bearer tokens are always secret-like. Parameters marked `sensitive`, or whose name classifies secret-like, carry **provenance**: each argument slot produced from such a parameter is marked `sensitive` in the plan.
- **Output never serialises the internal plan or `configimport.ImportedServer`** (its `Server` field holds raw values). The CLI/JSON/Web output is built from a separate `ItemView` DTO that has no value fields. Its `summary` is generated by the importer and does **not** use `configimport`'s summary (which relies on `oauth.LiveRedaction`, whose masks keep trailing bytes and length): command basename, then each argument rendered as-is *unless* it sits in a `sensitive` slot or the existing redactor would alter it, in which case the **whole argument value** is replaced with the fixed mask `‹redacted›`; for remote servers the URL is shown as scheme, host and path with userinfo dropped and **every query value and the fragment** replaced by `‹redacted›` (fixed mask, no tail bytes, no length). This also applies to entries blocked as inline credentials. Canary tests use innocuously named parameters and positional values to prove it (SC-004).
- **Reference names** are `mcpr-import-<kind>-<hex16>` where `<hex16>` is the first 16 hex chars of `sha256(destinationServerName 0x00 kind 0x00 key)`; `kind` is `env`/`header`/`arg`. Fixed length (no 64-character truncation problem), unique per destination server and field (so no allocation collision inside a run), deterministic (re-run reuses them, FR-017), namespaced by the `mcpr-import-` prefix. This deliberately does not use `secret.RefName`, whose truncation and collision-suffixing are wrong for this purpose.
- **Ownership and overwrite.** Before writing a reference, `Resolve` any existing value and remember it. A reference is only ever overwritten if it carries our prefix. Rollback restores the remembered prior value, or deletes the entry when there was none, and touches nothing else.
- **Per-server protocol, serialised.** The whole apply phase runs under one cross-process **import lock** (`import.lock` in the config directory, `O_CREATE|O_EXCL`, stale-lock detection by pid and age, released on every exit path), in both sink modes, so two importers can never interleave their keyring writes, adds and compensations. Under the lock, for each server: re-check that the name is still absent (a concurrent import may have added it: then it is `already_present`, and **no reference is touched**); mark each reference "possibly written" *before* `Store` (a timed-out store may still complete, and a store can succeed before its registry update fails); `Store` all; add the server.
  - **Definite rejection** (an HTTP response carrying an error status, or the file save returning an error before anything was renamed into place): compensate this server's references only.
  - **Anything else** (transport error, timeout, unreadable or undecodable response): the daemon's create transaction does not observe the request context, so absence on a later read does **not** prove the add will not still commit. The outcome is `uncertain`: references are kept, never deleted; a later read that finds the server present upgrades it to `added`; one that finds it absent leaves it `uncertain`, with the reference names listed and the advice to re-run (a re-run is idempotent: it finds the server or adds it and reuses the same deterministic references).
  - Compensation errors and timeouts are reported as indeterminate cleanup (names only).
- **Keyring unavailable** (`KeyringAvailability()`), non-dry run, secret-like values present, no `--secrets-inline`: stop before any write, exit class (f).
- **macOS gate.** Outside the tray, `internal/secret` refuses keyring writes on macOS unless `MCPPROXY_KEYRING_WRITE=1` (a maintainer safety gate against the "Keychain Not Found" modal; `KeyringAvailability()` returns false with that reason). MCP Router's users are mostly on macOS with API keys in env, so a plain-terminal first run would hit exit 24. The importer does **not** override the gate (assumption O1). The plan view and the refusal both print the exact remedy (`MCPPROXY_KEYRING_WRITE=1 mcpproxy import ...` or `--secrets-inline`) and the unavailable reason verbatim. If this proves the main drop-off in the experiment, a follow-up can ask the running daemon (which the tray starts opted in) to store the secrets over REST. Same behavior as `upstream add --secret-env`.
- **Inline credentials** (a token in a URL query, `--api-key=...` in an argument; detected with the same classifier the import preview uses) are listed as `blocked_inline_credential` and block the server unless `--secrets-inline`.
- **Source `${...}`** after placeholder resolution makes the entry unmappable (FR-006b). The importer's own `${keyring:...}` references are inserted afterwards, by construction, never by re-scanning source text.

### D5. Placeholder resolution (FR-006)

`ResolveArgs(args, env, params)` is a direct port of `substituteArgsParameters` as documented in research §2 (whole-argument match only; `${user_config.x}` → param `x` default, then non-empty env `user_config.x`, then param `user_config.x` default; `${x}` → non-empty env `x`, then param `x` default). Table tests cover both placeholder forms, conflicting values, empty env values and partial-string placeholders (left literal, then caught by FR-006b).

### D6. Apply contract, sinks, daemon discovery (FR-012a, FR-016a)

- `internal/mcprouter.Apply(ctx, plan, Sink, secrets)` returns `[]ServerOutcome{Name, Status(added|failed|uncertain), Reason, Enabled, Quarantined, OrphanRefs}`.
- **Sink choice is frozen for the run** and decided by a tri-state probe in `import_cmd.go`: *reachable* (daemon answers `GET /api/v1/status` with valid credentials, via the configured endpoint or socket) → `daemonSink`; *definitively absent* (no socket file, connection refused on the default local endpoint) → `fileSink`; *inaccessible* (auth failure, timeout, explicit endpoint that fails) → stop with an error, never fall back to file mode, never switch sinks after an add error.
- `daemonSink` calls `cliclient.AddServer` with `Enabled`, `Quarantined=true` and the new `DisabledTools`, then **reads the state back through the existing `GetServers` projection** (no new REST route; it carries `enabled` and `quarantined`) to fill what is actually stored (FR-012 read-back); the same read serves reconciliation of `uncertain` outcomes. A read-back failure keeps the add result and marks the state `unknown`.
- `fileSink` (daemon definitively absent): runs under the same import lock; per server it **reloads** the config from disk, re-checks the name, appends, saves with `config.SaveConfig`. A failed candidate is discarded, never carried into a later save. Residual limitation, documented: a daemon started *during* the run reloads the file as a replacement.
- `AddServerRequest` in `internal/cliclient` and `internal/httpapi` gains `disabled_tools []string` (create only), applied to `ServerConfig.DisabledTools`. Create-time `enabled_tools` is **not** added on this surface; the documented behavior is simply that only `disabled_tools` is accepted.
- **Plan-time comparison for idempotency (FR-015/016/016a).** Items compare on: transport, command, URL, args, env/header **names**. For argument slots marked `sensitive`, the canonical comparison token is the constant `<sensitive>`; an existing server's argument at that position that is **any** `${keyring:<name>}` reference (importer-generated or operator-renamed) compares equal to it, without resolving values or comparing reference names (FR-016a). An existing literal or any other reference form at that position is a conflict naming `args`. Existing servers whose args differ elsewhere are conflicts naming `args`.
- **Exit classification** is a pure function of plan classes plus outcomes: `Classify(plan, outcomes)`; it does not depend on apply outcomes alone. Telemetry eligibility depends only on outcomes with status `added`.
- `applyImportedServers*` (used by `upstream import`) are untouched; their dropping of `Enabled` and swallowing of add errors is a recorded follow-up issue, out of scope. The new sinks do not reuse them.

### D7. Telemetry (FR-022 to FR-024)

- `telemetry.ImportSource` closed enum `{mcp-router, other}` (extensible); `Service.RecordImportRun(src)` is a **service-owned** operation that evaluates event-time eligibility (`EffectiveTelemetryEnabled` plus env overrides) under the service's own synchronisation, the way the heartbeat path does (see the race documented near `telemetry.go:771`), and only then increments `CounterRegistry.importCounts`. The registry counter participates in `Drain()`/`Snapshot()`/`Reset()`/`HasPendingCounters()`. The payload field is `import_source_counts` (object, omitted when empty, keys from the fixed enum, non-negative integer values). `ScanForPII` gets the structural rule `import_source_field_invalid`. `SchemaVersion` takes the next value (16 on main when written, so 17; re-check at implementation time).
- **Schema bump hygiene.** The existing guard test only hunts obsolete v12 spellings. The bump task enumerates the current-version (v16 at the time of writing) pins by `rg` over code, tests, fixtures (`internal/telemetry/testdata`) and docs, updates them, extends the guard to the current to next transition, and leaves historical activation/cohort thresholds alone. Its own commit.
- **Endpoint** `POST /api/v1/servers/import/report` with body `{"source":"mcp-router"}` only (the count of servers is *not* sent: unnecessary, and the daemon client can target a non-loopback endpoint). Auth `requireServerOp(auth.ServerOpAdd, handler)`; unknown `source` returns 400; otherwise 204, whether or not the event was recorded (the response never reveals opt-out state). The handler calls `Service.RecordImportRun`.
- The CLI calls it once, only when the daemon sink was used and at least one outcome is `added`. A daemonless run is not counted (clarified). The Web UI path calls `Service.RecordImportRun` in-process from its own handler.
- Race test under `-race`: opt-out toggled concurrently with recording; disabled → import → enabled emits nothing.

### D8. Exit codes (FR-021)

A new band 20-24, assigned centrally in `classifyError` (`cmd/mcpproxy/main.go`) through a typed error `importOutcomeError` (same mechanism as `preflightVerdictError`), documented in `docs/cli-management-commands.md`:

| Code | Meaning (FR-021) |
|------|------------------|
| 0 | (a) success: all added, nothing to import, already-present only, dry-run planned |
| 20 | (b) partial: at least one added and at least one failed/conflict/unmappable |
| 21 | (c) nothing added although expected |
| 22 | (d) source not found |
| 23 | (e) source unreadable, malformed or no consistent snapshot (also `--dry-run`) |
| 24 | (f) refused by the secret rules |

### D9. Client reconnect detection (FR-008, FR-019)

`ClientsUsingMCPRouter()` opens only the six known client config paths, read-only and size-capped at 1 MiB, parses JSON (`mcpServers`/`servers`) or TOML (Codex `[mcp_servers.*]`), and flags an entry as using MCP Router if either (a) it is the bridge: command `npx`, args containing `connect` and an element containing `@mcp_router/cli`; or (b) it is a remote entry whose URL host is `localhost`/`127.0.0.1`/`[::1]` with port **3282** (MCP Router's fixed aggregator port, `apps/electron/src/main.ts`, default URL `http://localhost:3282/mcp` in `apps/cli/src/commands/connect.ts`). The same predicate drives the FR-008 self-skip in server import. It extracts **only** the boolean: entries are decoded into structs that have no `env`/`headers` fields, so `MCPR_TOKEN` and bearer headers are never in memory. Output maps client to `mcpproxy connect <id>` (`internal/connect` ids); Cline gets the one-line note.

## Project Structure

### Documentation (this feature)

```text
specs/120-mcp-router-import/
├── spec.md  research.md  plan.md  data-model.md  quickstart.md  tasks.md
├── checklists/requirements.md
└── contracts/
    ├── cli.md              # flags, output JSON, exit codes
    └── rest.md             # POST /servers/import/report, Web UI additions
```

### Source Code

```text
internal/mcprouter/                 # NEW, the only importer of modernc.org/sqlite
├── locate.go        # OS default paths, input kind, containment (D3)
├── snapshot.go      # stat-copy-verify (D2)
├── reader.go        # servers/workspaces/projects tables, tolerant of missing columns
├── export.go        # export JSON parser (+ empty-command remote detection)
├── resolve.go       # placeholder resolution (D5)
├── plan.go          # map → configimport.ImportedServer + classes; conflict compare (FR-016a)
├── clients.go       # reconnect detection (D9)
├── apply.go         # secrets + Sink + outcomes (D4, D6)
└── *_test.go + testdata/ (JSON only; DB fixtures are built in-test)
internal/configimport/               # small exports: ClassifyEnv/Headers, Sanitize already exported
internal/telemetry/                  # ImportSource, RecordImportOn, payload field, scan rule, schema bump
internal/cliclient/client.go         # AddServerRequest.DisabledTools, ReportImport()
internal/httpapi/server.go           # AddServerRequest.DisabledTools; POST /servers/import/report (requireServerOp)
cmd/mcpproxy/import_cmd.go          # NEW cobra command, rendering, prompt, exit mapping
cmd/mcpproxy/exit_codes.go           # importOutcomeError + band 20-24
cmd/mcpproxy/main.go                 # classifyError learns importOutcomeError; AddCommand(importCmd)
docs/features/mcp-router-import.md   # user page; docs sidebar entry
docs/cli/import-command.md  docs/features/telemetry.md  docs/api/rest-api.md  oas/swagger.yaml
frontend/src/components/ImportServersPanel.vue  # P3, last
scripts/test-api-e2e.sh              # +1 import step
```

## Phases

1. **P0 Gate and skeleton**: dependency measurement (T003), package skeleton, fixture builder.
2. **P1 Read and plan** (no writes): locate, snapshot, reader, export parser, resolver, plan/mapping/conflicts, clients. Milestone is package-test level (the command arrives in P2).
3. **P2 Apply and CLI**: secrets, sinks, `disabled_tools` plumbing, command, prompts, exit codes, JSON output.
4. **P3 Telemetry**: registry, payload, scan rule, schema bump, endpoint, doc.
5. **P4 Docs, E2E and acceptance**: user page, CLI docs, E2E, recorded acceptance checks (SC-001 timed single-confirmation run, SC-002 50-server timing, SC-009 one manual reconnect check per supported client: Claude Desktop, Cursor, Windsurf, VS Code, Codex), QA pass against a real MCP Router 0.6.4 database in a scratch home, and the hand-off to the website session (final command text, flags, docs URL) so the migration page links to and matches the shipped behavior (FR-026).
6. **P5 Web UI (non-gating, droppable)**: its own request shape (contracts/rest.md), detection row, panel entry. Does not alter the existing endpoints' contracts.

## Test strategy

- **Fixture builder** (`testdata` helper): creates a real SQLite file with MCP Router's `CREATE TABLE` statements and rows via the driver, including an old schema without later columns, WAL mode with an unflushed `-wal`, an encrypted-looking token (`mcpr-secret:v1:...`), hostile values, and workspace rows pointing outside the directory.
- **Added from review**: symlink-swap tests (database, parent directory, sidecar) via `os.Root`; sibling-directory prefix test (`MCP Router-evil`); WAL checkpoint/truncate/replace during copy; commit-succeeds-response-lost reconcile test; partial `Store` failure and timeout; reference collision and restore-prior-value; concurrent `fileSink` imports; stale lock file; legacy servers-only database; oversized/deeply-nested/control-character/non-http-URL fixtures; mixed-class exit classification table; rerun after sensitive-arg keyring replacement and after operator approval; global quarantine disabled read-back; MCP Router endpoint-style client entry.
- **Properties**: no byte of any canary secret in any output channel; dry-run leaves config, keyring and registry unchanged; re-run is a no-op (config file byte-identical); every plan class is exercised; snapshot retry under a concurrently writing goroutine; temp directory always removed.
- **Mutation checks** (per repo practice): disable the containment check, the `${` check, the quarantine flag and the keyring-first order one at a time and confirm a test fails.
- **Real-data QA**: one run against a database produced by the real MCP Router 0.6.4 app in a scratch home, recorded in the PR, not committed.

## Risks

| Risk | Mitigation |
|------|------------|
| Driver size/portability | D1 gate with a build-tag fallback |
| Snapshot of a busy WAL DB | D2 bounded verify loop, fail safe, export-file alternative |
| Keyring behaviors differ per OS; macOS terminal runs are gated off by default | reuse `internal/secret` unchanged; print the exact remedy; tests with a fake provider for available / unavailable / macOS-gated; manual check on macOS and Windows; follow-up path via the daemon if the gate is the main drop-off |
| MCP Router users with 0 servers or only cloud workspaces | explicit "nothing to import" path and message |
| Telemetry schema bump touches many pinned sites | rely on the existing guard test; do the bump as its own commit |
| Scope creep into `upstream import` | out of scope; follow-up issue for its daemon-mode Enabled/error handling |

## Complexity Tracking

| Item | Why needed | Simpler alternative rejected because |
|------|-----------|--------------------------------------|
| New dependency `modernc.org/sqlite` | Reading remote servers/disabled flags/tool toggles needs the database | Export-only loses the main value; shelling out fails on Windows |
| New REST report endpoint | Daemon must own the telemetry event and gate on its own opt-out state | CLI-side recording would be lost with the process and ignore the daemon's opt-out |
| Exit-code band 20-24 | FR-021 needs six distinguishable outcomes | Reusing 1-6 would collide with startup meanings |
