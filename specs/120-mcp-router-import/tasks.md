# Tasks: MCP Router Importer (`mcpproxy import --from mcp-router`)

**Input**: `specs/120-mcp-router-import/` (spec.md, plan.md, research.md, data-model.md, contracts/cli.md, contracts/rest.md, quickstart.md)
**Tests**: REQUIRED (constitution V: test first; each implementation task follows its test task and the test must be seen failing first).
**Routing note** (global model routing): T-tasks marked **[opus-review]** touch security-sensitive code (containment, secrets, apply, telemetry gate) and get an independent Sonnet/Opus subagent review in addition to the Sol review; the rest is Sonnet-implementable.

## Format: `- [ ] T### [P?] [US#?] Description with file path`

`[P]` = parallelisable (different files, no unfinished dependency). Paths are repo-relative. `internal/mcprouter` is the new package; it is the **only** importer of `modernc.org/sqlite` (plan D1).

## Path conventions

Go: `internal/mcprouter/`, `internal/configimport/`, `internal/telemetry/`, `internal/cliclient/`, `internal/httpapi/`, `cmd/mcpproxy/`. Docs: `docs/`. Frontend (P5 only): `frontend/src/`.

---

## Phase 1: Setup

- [ ] T001 Create package skeleton `internal/mcprouter/doc.go` (package comment: purpose, "only importer of modernc.org/sqlite", untrusted-input rules) and empty `locate.go`, `snapshot.go`, `reader.go`, `export.go`, `resolve.go`, `plan.go`, `clients.go`, `apply.go`, `view.go` so later tasks only fill them.
- [ ] T002 Add `modernc.org/sqlite` to `go.mod`/`go.sum` (`go get modernc.org/sqlite@v1.60.1`, run `go mod tidy`); import it only from `internal/mcprouter/reader.go`. Add a CI guard `scripts/check-sqlite-import-scope.sh` (fails if any other package imports it) and call it from the same workflow that runs `scripts/check-upstream-write-sites.sh`.
- [ ] T003 **Dependency gate (plan D1).** Build the release binary with the release flags (`-ldflags "-s -w"`) with and without the driver for linux/amd64, darwin/arm64, windows/amd64 (`GOOS=... GOARCH=... go build`), record the stripped-size deltas in `specs/120-mcp-router-import/research.md` under a new section "Dependency measurement". If any delta exceeds 10 MB: STOP, put the direct-DB reader behind a build tag `mcprouter_db` (default off) and ship the export-file path only; note it in the PR. Otherwise proceed.
- [ ] T004 [P] Write the in-test fixture builder `internal/mcprouter/fixture_test.go` (+ `internal/mcprouter/fixturegen/` helper exposed as `TestFixtureWriteDir` with `-args -out <dir>` for quickstart): creates a real SQLite data directory with MCP Router's `CREATE TABLE` statements (servers, projects, workspaces), WAL mode with an unflushed `-wal`, rows for: local stdio, remote (SSE), remote-streamable, disabled, tool_permissions with `false` entries, plain bearer token, `mcpr-secret:v1:` encrypted token, `${param}`/`${user_config.x}` args with `input_params`, hostile values (control chars, `${keyring:x}`, `file:` URL, 70 KiB string), a second local workspace under `workspaces/<id>/database.db`, a workspace row whose `databasePath` points outside the directory, and a legacy variant without later columns and without a `workspaces` table. Also writes `mcp-servers-<date>.json` in MCP Router's export shape, including an empty-command remote entry. Canary secret strings are constants shared by tests.
- [ ] T005 [P] Add the canary scanner helper `internal/mcprouter/canary_test.go`: `AssertNoCanary(t, ...)` checks stdout, stderr, JSON/YAML output, log buffers, the config file and a telemetry payload for any canary substring. Used by every later story.

## Phase 2: Foundational (blocks all stories): read, resolve, plan (no writes)

**Goal**: produce a correct `Plan` from any supported input. No CLI, no writes.

- [ ] T006 [P] Test `internal/mcprouter/locate_test.go`: default per-OS paths (macOS, Windows, Linux via an injectable `Env{GOOS, Home, AppData}`), input kind detection (`data_dir`, `database`, `export_file`, unrecognised file → error naming what was expected), symlink-swap of data dir contents, sibling-directory prefix (`MCP Router-evil`), absolute and `..` workspace `databasePath` rejected with warning `workspace_path_rejected`, workspace id pattern, regular-file requirement. **[opus-review]**
- [ ] T007 Implement `internal/mcprouter/locate.go` per plan D3: `Locate`, `DefaultLocations`, all file access through `os.OpenRoot`. **[opus-review]**
- [ ] T008 [P] Test `internal/mcprouter/snapshot_test.go`: stable copy; WAL present/absent; concurrent writer goroutine; WAL checkpoint/truncate/replace/disappear during copy forces retry; file-replacement with identical size+mtime detected via identity; 3 failed attempts → `ErrNoConsistentSnapshot`; size cap 1 GiB → `ErrSourceTooLarge`; temp dir mode 0700 and always removed (also on context cancel). **[opus-review]**
- [ ] T009 Implement `internal/mcprouter/snapshot.go` per plan D2 (descriptor copy, pre/post identity, `quick_check`, 30 s deadline, bounded retries).
- [ ] T010 [P] Test `internal/mcprouter/reader_test.go`: reads all fixture rows; missing columns take defaults; missing `workspaces` table → single `local-default`; limits (5,000 servers, 256-byte name, 64 KiB fields, 1,024 args, JSON depth 8) fail the row with `row_too_large`/`bad_json` instead of the run; `tool_permissions` false → `ToolOff`; bearer state `none|plain|encrypted`; remote workspaces counted not read; presence of workflow/hook/skill/log tables → `NotImported` flags (table existence and row counts only, never contents).
- [ ] T011 Implement `internal/mcprouter/reader.go` (queries by column-existence probe; context deadlines; the only file importing the driver).
- [ ] T012 [P] Test `internal/mcprouter/export_test.go`: export shape parsed; empty-command entry flagged `RemoteURLNotExported`; unknown shape rejected; 4 MiB cap; `FromExport` set (disabled/tool toggles/token unknown).
- [ ] T013 [P] Implement `internal/mcprouter/export.go`.
- [ ] T014 [P] Test `internal/mcprouter/resolve_test.go`: table tests for plan D5 and its provenance (`${x}` env-first, `${user_config.x}` default-first, empty env treated absent, partial-string `--a=${x}` left literal, conflicting values); provenance marks a slot sensitive for sensitive param, secret-named param **and secret-like env name** (`args=["${API_KEY}"]` with ordinary-looking value); leftover or source-originated `${...}` (incl. `${keyring:x}`, `${env:x}`) → `unresolved_placeholder`.
- [ ] T015 Implement `internal/mcprouter/resolve.go` (`ResolveArgs` returning per-slot provenance).
- [ ] T016 Export `ClassifyEnv` and `ClassifyHeaders` wrappers in `internal/configimport/summary.go` (thin, with a test in `internal/configimport/summary_classify_test.go`); no behavior change to existing callers.
- [ ] T017 [P] Test `internal/mcprouter/plan_test.go`: mapping table (FR-006) for every kind; `enabled = !disabled`; `DisabledTools`; protocol `stdio|sse|streamable-http`; bearer plain → `Authorization: Bearer` header classified secret; bearer encrypted → class `needs_configuration`, server disabled, no header; unmappable reasons (empty command, no URL, non-http(s) URL, invalid name, `${...}` leftovers, inline credential without `--secrets-inline`); self/MCP Router-endpoint skip (`skipped_self`, port 3282); name sanitising, collisions (`collision`, default workspace wins, `--workspace` filter); comparison against existing servers per FR-016a (already_present vs conflict naming fields only; sensitive slot token accepts any `${keyring:*}`; operator-edited enabled/quarantine ignored); `NotCarried` lists; every item `Quarantined=true`.
- [ ] T018 Implement `internal/mcprouter/plan.go` and `internal/mcprouter/view.go` (the `ItemView` DTO with no value fields, importer-owned summary with fixed `‹redacted›` masks for sensitive argument values and for URL userinfo/query/fragment; names escaped for terminals). **[opus-review]**
- [ ] T019 [P] Test `internal/mcprouter/view_test.go`: canaries in env, headers, args (innocuous parameter names, positional values), URL query, userinfo never appear in `ItemView` JSON/YAML/table output; no tail bytes or lengths in masks; `Plan`/`Item` are never marshalled (compile-time assertion that they have no `json` tags + a reflection test).
- [ ] T020 [P] Test `internal/mcprouter/clients_test.go`: six client config fixtures (JSON, VS Code `servers`, Codex TOML); bridge entry detected; endpoint entry (`localhost:3282`) detected; `MCPR_TOKEN` and headers never decoded (structs lack the fields; canary test); 1 MiB cap; Cline note; missing files ignored; files only opened read-only.
- [ ] T021 [P] Implement `internal/mcprouter/clients.go`, exposing the predicate reused by T018 for the FR-008 skip.

**Checkpoint**: `BuildPlan(ctx, Input, ExistingServers, Options)` works for data dir, database file and export file; all package tests pass with `-race`.

## Phase 3: US2 — Preview first, nothing changed (P1, MVP: read-only command)

**Goal**: `mcpproxy import --from mcp-router [--path] --dry-run [--workspace] [-o json]` prints the plan and changes nothing.
**Independent test**: run against the T004 fixture; config, keyring and telemetry registry byte-identical before/after; JSON equals table content; exit 0.

- [ ] T022 [P] [US2] Test `cmd/mcpproxy/import_cmd_test.go` (dry-run): flag validation (`--from` required, unknown source lists supported ones), auto-detect message naming checked locations when none found (exit 22), dry-run success exit 0 incl. all-unmappable plan, read failure in dry-run → 23/22, `-o json|yaml` structure equals `contracts/cli.md`, `--help-json` includes the command, canaries absent, no config/keyring/telemetry mutation (hash config file; fake keyring counts writes; registry snapshot).
- [ ] T023 [US2] Implement `cmd/mcpproxy/import_cmd.go`: cobra command, flags (`--from --path --dry-run --workspace --yes --secrets-inline`), `BuildPlan`, table and JSON/YAML rendering from `ItemView`, reconnect section; register in `cmd/mcpproxy/main.go` (`rootCmd.AddCommand(importCmd)`).
- [ ] T024 [P] [US2] Add `importOutcomeError` and exit band 20-24 to `cmd/mcpproxy/exit_codes.go`, teach `classifyError` in `cmd/mcpproxy/main.go`, and add `cmd/mcpproxy/exit_codes_import_test.go` table-testing `Classify(plan, outcomes)` for: mixed added+conflict, added+unmappable, all-unmappable, empty source, already-present-only, dry-run, source not found, unreadable, refused. **[opus-review]**
- [ ] T025 [US2] Wire `Classify(plan, outcomes)` into the command (`internal/mcprouter/classify.go`) and make T022/T024 pass.

**Checkpoint**: a read-only importer is shippable on its own (user-visible value: they see exactly what would happen).

## Phase 4: US1 — Move my servers in one command (P1)

**Goal**: apply the plan: servers added quarantined, disabled/hidden-tool state preserved, per-server true outcome.
**Independent test**: quickstart steps 2 on a scratch instance in daemon mode and in file mode.

- [ ] T026 [P] [US1] Test REST/client `disabled_tools`: `internal/httpapi/server_add_disabled_tools_test.go` (create with `disabled_tools` stores it; omitted → unchanged behavior; `enabled_tools` not accepted/ignored as documented) and `internal/cliclient/client_add_test.go` (request marshalling).
- [ ] T027 [US1] Add `DisabledTools []string \`json:"disabled_tools,omitempty"\`` to `AddServerRequest` in `internal/cliclient/client.go` and `internal/httpapi/server.go`; apply to `ServerConfig.DisabledTools` in the add handler; update `docs/api/rest-api.md` and `oas/swagger.yaml` (run the repo's swagger generation/verify command).
- [ ] T028 [P] [US1] Test `internal/mcprouter/apply_test.go` (non-secret paths): fake `Sink`; added servers carry `Quarantined=true`, `Enabled`, `DisabledTools`; definite rejection → `failed`; per-server outcomes returned; read-back fills actual `Enabled/Quarantined` through the `GetServers` projection and `unknown` when the read fails; global quarantine disabled still reports the stored state.
- [ ] T029 [US1] Implement `internal/mcprouter/apply.go` (`Apply`, `Sink`, `ServerOutcome`) and `daemonSink` (uses `cliclient.AddServer` + `GetServers` read-back). **[opus-review]**
- [ ] T030 [P] [US1] Test `internal/mcprouter/filesink_test.go`: import lock (`import.lock`, `O_EXCL`, stale detection by pid/age, released on all exits incl. panic/SIGINT), per-server reload-recheck-append-save, a name that appeared concurrently → `already_present`, failed candidate never persisted by a later save, two concurrent importers serialised, residual-daemon-started-mid-run documented. **[opus-review]**
- [ ] T031 [US1] Implement `fileSink` and the lock in `internal/mcprouter/filesink.go`; share the lock with `daemonSink` (apply phase is serialised in both modes, plan D4).
- [ ] T032 [P] [US1] Test `cmd/mcpproxy/import_daemon_probe_test.go`: tri-state probe (reachable / definitively absent / inaccessible); inaccessible and explicit-endpoint failures stop with an error and never fall back; sink frozen after the first add; no file-mode fallback after an add error.
- [ ] T033 [US1] Implement the probe and sink selection in `cmd/mcpproxy/import_cmd.go` (reuse `daemonEndpoint`/`newDaemonClient` internals without their boolean collapse).
- [ ] T034 [P] [US1] Test `cmd/mcpproxy/import_apply_test.go`: interactive confirmation (`[y/N]` once), `--yes`, non-TTY without `--yes` → refuse (24), servers listed `uncertain` for transport errors, exit statuses (0, 20, 21), reconnect advice printed after a real run, hidden-tool and disabled state present via `upstream list -o json` against an in-process daemon.
- [ ] T035 [US1] Implement apply flow in `cmd/mcpproxy/import_cmd.go`: plan view → confirmation prompt (reuse the TTY check pattern of `confirmation.go`) → `Apply` → summary → exit class.
- [ ] T036 [US1] E2E step: add an import scenario to `scripts/test-api-e2e.sh` (scratch data dir, high port, fixture from `TestFixtureWriteDir`; asserts quarantined, disabled, hidden tools, second run no-op).

**Checkpoint**: MVP complete for non-secret servers.

## Phase 5: US3 — No secret moves silently (P1)

**Independent test**: fixture with API-key env, plain token, encrypted token; verify what is shown, stored and where; canary scan.

- [ ] T037 [P] [US3] Test `internal/mcprouter/secrets_test.go`: reference names `mcpr-import-<kind>-<hex16>` deterministic, fixed length, unique per server+field; existing value remembered and restored on rollback; entry without our prefix never overwritten; mark-before-`Store`; partial `Store` failure (value written, registry update fails) and timeout handled as indeterminate with names listed; keyring unavailable + secrets + no `--secrets-inline` → refuse before any write (exit class f) incl. macOS-gate reason text with the exact `MCPPROXY_KEYRING_WRITE=1` remedy; `--secrets-inline` keeps values inline; inline-credential servers blocked without it; `${keyring:...}` inserted after the source `${` check; sensitive slots carry the reference; encrypted token → server added disabled without header, "needs bearer token"; `MCPR_TOKEN` never read. **[opus-review]**
- [ ] T038 [US3] Implement `internal/mcprouter/secrets.go` (reference naming, backup/restore, compensation, provenance-driven keyring writes) and hook it into `Apply` (store → add → on definite rejection compensate; on indeterminate result keep references and mark `uncertain`; later `GetServers` read upgrades to `added`). **[opus-review]**
- [ ] T039 [P] [US3] Test `internal/mcprouter/apply_uncertain_test.go`: commit-succeeds-response-lost keeps references; timeout then absent stays `uncertain`; timeout then present → `added`; definite 4xx → compensation of that server only and committed servers' references untouched; two importers concurrently on the same server (lock) never delete each other's references.
- [ ] T040 [US3] Plan view and refusal text in `cmd/mcpproxy/import_cmd.go`: per-server secret handling lines (`keyring`/`inline`/`missing`/`blocked_inline_credential`) with names and flags only; confirmation line "Add N servers (X secrets to keyring, Y inline)?"; the exact remedy for the macOS gate.
- [ ] T041 [US3] Canary sweep test `cmd/mcpproxy/import_canary_test.go`: dry run, real run, refused run, failed run, JSON and YAML modes, `--log-level=debug`; assert zero canary bytes in stdout, stderr, log files, config file (only references), telemetry payload.

## Phase 6: US4 — Run it twice, nothing breaks (P1)

- [ ] T042 [P] [US4] Test `internal/mcprouter/idempotency_test.go`: re-run is a no-op and config file byte-identical; new source server added alone; interrupted run completes without duplicates and reuses deterministic references; conflict reports field names only; operator-renamed keyring reference at a sensitive slot still `already_present`; operator-changed enabled/approval state ignored; collision and case/whitespace name rules (FR-016a).
- [ ] T043 [US4] Make T042 pass (comparison in `plan.go`, reuse in `apply.go`); fix any gaps found.

## Phase 7: US8 — Measure the experiment anonymously (P1)

- [ ] T044 [P] [US8] Test `internal/telemetry/import_counter_test.go`: `Service.RecordImportRun` records once per call when enabled; nothing when config opt-out or any env override (use `env_overrides` fixtures); opt-out toggled concurrently under `-race`; disabled → import → enabled emits nothing retroactively; `Drain/Snapshot/Reset/HasPendingCounters` include the counter; frozen-window retry keeps and dedups the delta; payload `import_source_counts` omitted when empty, keys only from the closed enum, integers ≥ 0. **[opus-review]**
- [ ] T045 [US8] Implement `ImportSource` enum, `CounterRegistry.importCounts`, `Service.RecordImportRun` in `internal/telemetry/registry.go` and `internal/telemetry/telemetry.go`; payload field `import_source_counts`.
- [ ] T046 [P] [US8] Test privacy scan: extend `internal/telemetry/payload_privacy_test.go` with positive and negative cases (unknown key, negative number, string value, nested object, key containing a path or server name) for rule `import_source_field_invalid`.
- [ ] T047 [US8] Implement the structural rule in `internal/telemetry/anonymity.go`.
- [ ] T048 [US8] Schema bump to the next version as its own commit: `rg` all current-version (v16 at the time of writing) pins in code, tests, `internal/telemetry/testdata`, docs; update them; extend `internal/telemetry/schema_version_guard_test.go` to the current to next transition; keep historical v14 activation/cohort thresholds; add the "Schema v17 (or next free)" section to `docs/features/telemetry.md` (what is counted: runs, closed enum, never names/paths/counts/servers; opt-outs; undercount when the core is not running).
- [ ] T049 [P] [US8] Test `internal/httpapi/import_report_test.go`: `POST /api/v1/servers/import/report` with `{"source":"mcp-router"}` → 204 and counter incremented when enabled; 204 and no count when opted out (response identical); unknown source → 400; missing auth → 401/403 like other server-add routes (`requireServerOp(auth.ServerOpAdd, ...)`).
- [ ] T050 [US8] Implement the handler and route in `internal/httpapi/server.go`; add `Client.ReportImport(ctx, source)` in `internal/cliclient/client.go`; document in `docs/api/rest-api.md`/`oas/swagger.yaml`.
- [ ] T051 [US8] CLI: after `Apply`, if the daemon sink was used and ≥1 outcome is `added`, call `ReportImport` once (never for dry run, file mode, refused, all-failed, `uncertain`-only). Test in `cmd/mcpproxy/import_telemetry_test.go` with a recording fake daemon.

## Phase 8: US5 — What did not come across, what to reconnect (P2)

- [ ] T052 [P] [US5] Test `cmd/mcpproxy/import_report_test.go`: not-carried-over lines per server; DXT-path dependency warning (commands under `mcp-servers/dxt/`); unresolved placeholders listed; private-address URL warning; categories not imported; reconnect section with `mcpproxy connect <id>` per detected client, Cline one-liner, entry removal note; no client config file modified (hash before/after); JSON `reconnect[]` shape.
- [ ] T053 [US5] Implement report sections in `cmd/mcpproxy/import_cmd.go` using `Plan.NotImported`, `NotCarried`, `Clients`; add the next-step hints (`mcpproxy upstream list`, review queue).

## Phase 9: US6 — Use the exported file (P2)

- [ ] T054 [P] [US6] Test `cmd/mcpproxy/import_export_file_test.go`: `--path` to export, to directory and to database each accepted with input kind stated; random JSON/binary rejected with expectation; empty-command entry "URL was not exported"; export-loss warning (disabled state, tool toggles, tokens, parameter schemas, other workspaces; servers arrive enabled with all tools visible); same secret/quarantine/idempotency rules.
- [ ] T055 [US6] Make T054 pass (mostly wiring; fix gaps in `export.go`/`plan.go`/`import_cmd.go`).

## Phase 10: Docs and acceptance

- [ ] T056 [P] Write `docs/features/mcp-router-import.md` (what is carried over, what is not, secret handling, macOS keyring note with the exact environment variable, after-import checklist per client, undercount note) and add it to the docs allowlist and `website/sidebars.js` per the docs-site pipeline; link check against the docs build.
- [ ] T057 [P] Write `docs/cli/import-command.md` (flags, exit codes 0/20-24, JSON schema from `contracts/cli.md`); add the exit-code band to `docs/cli-management-commands.md`.
- [ ] T058 [P] Add a "Recent Changes" line for spec 120 to `CLAUDE.md` (lean, one paragraph) and the command to the CLI list.
- [ ] T059 Hand-off for FR-026: write `specs/120-mcp-router-import/handoff-website.md` with the final command text, flags, exit behavior, docs URL, and the caveats the migration page must repeat (macOS keyring variable, remote tokens re-entry); send it to the website session via the shared notes folder. The marketing page must link to the docs page and not contradict it.
- [ ] T060 Acceptance run, results recorded in the PR (not committed): SC-001 timed run with one confirmation; SC-002 50-server fixture dry-run vs apply times < 5 s each and plan equality; SC-004 canary matrix green; SC-005 byte-identical config on re-run; SC-006 all quarantined; SC-009 one manual reconnect check per client (Claude Desktop, Cursor, Windsurf, VS Code, Codex).
- [ ] T061 Real-data QA: install MCP Router 0.6.4 (final release) in a scratch home, create servers incl. a remote one with a bearer token and tool toggles, run the importer against the real data directory while the app is running and after quitting; record findings; update `research.md` if the real schema differs.
- [ ] T062 Lint and test gate: `golangci-lint run --config .github/.golangci.yml ./...` and with `--build-tags server`; `go test -race ./internal/mcprouter/... ./internal/telemetry/... ./internal/httpapi/... ./cmd/mcpproxy/...`; the CI `-skip` regex run for `internal/server`/`httpapi`/`storage` under `-tags server`; `./scripts/test-api-e2e.sh`; mutation spot-checks (disable the `os.Root` containment, the `${` check, the quarantine flag, keyring-before-add order, the telemetry gate, one at a time: a test must fail each time).
- [ ] T063 Cross-model review of the implementation PR(s) with opencode `github-copilot/gpt-6.1-sol` (read-only, chunked by package, empty output = UNREVIEWED), round cap 10; independent Sonnet/Opus subagent review for the **[opus-review]** items.

## Phase 11: US7 — Web UI entry (P3, non-gating, droppable; ship last)

Skip this phase entirely if time or review capacity is short; nothing above depends on it.

- [ ] T064 [P] [US7] Test `internal/httpapi/import_mcp_router_test.go`: `GET /api/v1/servers/import/mcp-router` (found/not found, workspaces, count); `POST` preview returns the `ItemView` plan with no values; `POST apply` honours `secrets:"keyring"|"inline"`, `inline_credentials_ok`, per-server selection; quarantine always set; placeholder rejection before any destination reference; same authorization as other server-adding routes; telemetry recorded in-process via `Service.RecordImportRun`; existing `/servers/import*` endpoints unchanged.
- [ ] T065 [US7] Implement the endpoints in `internal/httpapi/import_mcp_router.go` reusing `internal/mcprouter` (daemon-side sink = in-process add path; same lock, outcomes, read-back); update `oas/swagger.yaml` and `docs/api/rest-api.md`.
- [ ] T066 [US7] Frontend: add "MCP Router" as a detected source in `frontend/src/components/ImportServersPanel.vue` (and its entry in `AddMenu.vue` if the detected-sources list lives there), preview table from the plan DTO, secret-handling choices, confirmation, outcome and reconnect list; unit test under `frontend/tests/unit/*.spec.ts`; run the Playwright sweep per `docs/development/web-ui-verification.md`.

---

## Dependencies and order

```text
Setup (T001-T005; T003 gate decides the reader's build mode)
  └─▶ Foundational (T006-T021)
        └─▶ US2 dry-run (T022-T025)   ← shippable read-only MVP
              ├─▶ US1 apply (T026-T036)
              │     ├─▶ US3 secrets (T037-T041)
              │     └─▶ US4 idempotency (T042-T043)   [needs US1 apply for the re-run]
              │           └─▶ US8 telemetry (T044-T051)   [needs US1 apply; independent files]
              ├─▶ US5 report (T052-T053)   [after US2; reconnect data from T021]
              └─▶ US6 export file (T054-T055)   [after US2]
Docs/acceptance (T056-T063) after US1+US3+US4+US8
US7 web (T064-T066) last, droppable
```

US3 must land before the first real (non-dry) release of US1 to users: until secrets are handled, the command is dry-run only (guard with `--dry-run` forced when T037-T041 are not merged; do not release apply without them).

## Parallel examples

- After T005: T006, T008, T010, T012, T014, T016, T017, T019, T020 can be written in parallel (different test files), then their implementations T007, T009, T011, T013, T015, T018, T021.
- After US2: T026/T028/T030/T032 tests in parallel; US5 (T052) and US6 (T054) tests in parallel with US1 implementation.
- Docs T056-T058 in parallel with US8.

## Implementation strategy

1. **Release-able slices.** PR-A: Setup+Foundational+US2 (read-only command, no writes, no secrets, no telemetry). PR-B: US1+US3+US4 (apply, hard-dependent trio). PR-C: US8 telemetry + the next schema version (v17 when written) (its own commit). PR-D: US5+US6+docs. PR-E (optional): US7 web UI. Merge each before starting the next (no stacking).
2. **Gate first.** T003 decides the shape of everything else: if the dependency gate fails, PR-A ships with the export-file reader only.
3. **Security-sensitive tasks** (T006-T009, T018, T024, T029-T031, T037-T039, T044) get the independent review and the mutation checks in T062.
4. **Experiment readout.** Success = ≥ 25 runs counted under `import_source=mcp-router` within 60 days of the release carrying PR-C; known undercounts: daemonless runs, opted-out installs, runs where macOS keyring gating stopped the user (track via support feedback).

## Follow-ups (not in this feature)

- `upstream import` daemon mode drops `Enabled` and `disabled_tools` and swallows add errors (`cmd/mcpproxy/upstream_cmd.go:applyImportedServersDaemonMode`): separate issue.
- If the macOS keyring gate is the main drop-off, let the importer ask the running daemon (opted in by the tray) to store secrets over REST.
- Recreate MCP Router Projects/Workspaces as Spec 108 profiles once those settle.
