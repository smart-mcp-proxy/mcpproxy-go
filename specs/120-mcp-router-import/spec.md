# Feature Specification: MCP Router Importer (`mcpproxy import --from mcp-router`)

**Feature Branch**: `claude/spec-mcp-router-import-7ae3fb` (spec directory `120-mcp-router-import`)
**Created**: 2026-10-20
**Status**: Draft
**Input**: Growth experiment E4 from the Q4 2026 promotion strategy. MCP Router (a 2,135-star desktop MCP manager) was archived on 2026-09-18; its README says new adoption is not recommended, so its users have no maintained tool. MCPProxy offers a one-command way across.

**Related**: `research.md` in this directory (verified MCP Router storage format, with file paths and commit SHAs). Existing import machinery: `mcpproxy upstream import`, `internal/configimport`, REST `/api/v1/servers/import*`, Web UI `ImportServersPanel`. Spec 109 (catalog-first Add, import preview, FR-064/065 secret handling), Spec 032 / MCP-2931 (quarantine), Spec 108 (profiles), telemetry schema v16.

## Context and Motivation

A person with MCP Router has their servers in its local database and their AI clients pointed at it. MCP Router no longer receives fixes, and the official migration guide only offers a JSON export. We verified that export is lossy: it writes remote servers as broken empty-command entries and drops disabled flags and tool toggles (see `research.md` §3). The database itself holds everything except encrypted bearer tokens.

Today such a user can already run `mcpproxy upstream import <exported-file>`, but only if they know that the export is Claude-Desktop-shaped, and they silently get broken remote entries. Nothing tells them which clients to reconnect. This feature makes the move one discoverable command, loses as little as possible, says plainly what it could not carry over, and keeps MCPProxy's security stance: nothing arrives trusted, and no secret is stored without the person seeing it.

It is also an experiment. Success is measured with a privacy-safe counter: at least 25 successful imports by day 60 after release.

## Scope Boundary

In scope:
- A top-level CLI command `mcpproxy import --from mcp-router` with `--path` and `--dry-run`.
- Reading MCP Router's data directory and database directly, and reading its exported JSON file.
- A summary of what was imported, skipped and could not be mapped, and a list of clients to reconnect.
- A closed-enum telemetry counter `import_source`.
- A Web UI entry point reusing the existing import panel, if it stays small (P3).

Out of scope (documented in the summary instead): MCP Router workflows, hooks, skills, request logs, client tokens, cloud workspaces, Projects (grouping) and Workspaces as first-class MCPProxy objects, decrypting MCP Router's encrypted bearer tokens, `.dxt`/`.mcpb` file import (MCPProxy has no extension runtime), and a general `--from <other client>` catalog beyond reserving the flag shape.

## Clarifications

Self-answered under the zero-interruption policy (no human available); each answer is the safest default consistent with MCPProxy's existing import and secret behavior.

### Session 2026-10-20

- Q: How does a person confirm what will be stored, and how do scripts run it? → A: An interactive run prints the plan and asks one yes/no confirmation. `--yes` skips the prompt. A non-interactive run without `--yes` refuses. `--dry-run` never prompts.
- Q: What is the explicit opt-in for keeping secrets inline when no keyring exists? → A: `--secrets-inline`. It is never implied by `--yes`.
- Q: Which values count as "secret-like"? → A: Whatever the existing import classifier already marks secret-like for env/headers, plus every plain-text bearer token regardless of shape. No new classifier.
- Q: Several local workspaces contain the same server name? → A: The default workspace wins, then others in creation order; losers are listed as collisions. `--workspace <name-or-id>` limits the import to one workspace.
- Q: How is a live (open, WAL-mode) database read without touching it? → A: Copy the database and its journal files to a private temporary directory (mode 0700), read the copy, delete the copy on exit. The live files are never opened for writing. The reader library choice is a plan decision.
- Q: Does `upstream import` also learn this source? → A: No. `upstream import` is unchanged (an export file is byte-compatible with the Claude Desktop shape, so it keeps importing there as before, with its known lossy remote entries). `mcpproxy import --from mcp-router` is the only entry point, so no second path can bypass the secret, confirmation and accounting rules. (Review round 1, finding 8.)
- Q: What happens to an entry that cannot run without a value MCPProxy does not have? → A: Two outcomes. *Unmappable* (not added): empty command, remote server with no URL, unresolved `${...}` placeholder left after resolution. *Needs configuration* (added **disabled** and quarantined): a remote server whose bearer token was encrypted. Both are listed with the reason. (Review round 1, finding 10.)
- Q: Is quarantine unconditional? → A: The importer always creates the server with the quarantined flag set, exactly as `upstream import` does today, independent of trust mode. If the operator has globally disabled quarantine, the existing global semantics apply unchanged and the summary reports the state actually read back from the configuration. (Review round 1, finding 13.)
- Q: Who records the telemetry count, and what about a run without the daemon? → A: The daemon records it when the import is applied through the daemon (the normal case, since the tray starts the core), through the existing counter window and retry mechanism. A daemonless config-file run is **not counted** (documented undercount; the experiment is measured among installs where the core runs). (Review round 1, finding 14.)
- Q: Is the web entry gating? → A: No. Story 7 / FR-025 are non-gating for this release and ship last in the plan, droppable without affecting any other requirement. (Review round 1, finding 19.)
- Q: Are imported servers that MCP Router had disabled also quarantined? → A: Yes. Disabled and quarantined are independent: they arrive disabled and quarantined.
- Q: Daemon running or not? → A: Same as `upstream import`: apply through the running daemon when present, otherwise edit the configuration file.
- Q: What does "success" in the counter mean? → A: A non-dry run that added at least one server (exit status success or partial success). Re-runs that add nothing do not count.
- Q: Exit status for a dry run? → A: Success, regardless of how many servers would fail; the failures are in the output.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Move my servers in one command (Priority: P1)

A MCP Router user installs MCPProxy and runs `mcpproxy import --from mcp-router`. MCPProxy finds MCP Router's data on this computer, shows what it found, and adds the servers, all quarantined, with a summary.

**Why this priority**: This is the experiment. Without it there is nothing to measure.

**Independent Test**: Point `--path` at a fixture data directory built from MCP Router's real schema (local, remote, remote-streamable, disabled, with tool toggles). Run with `--dry-run`, then without. Check the resulting servers and that nothing was changed by the dry run.

**Acceptance Scenarios**:

1. **Given** MCP Router's data directory exists at its default location, **When** the user runs `mcpproxy import --from mcp-router`, **Then** the location is auto-detected, the servers are listed with source name, kind (local process or remote), and status, and each server that can be mapped is added and quarantined.
2. **Given** a stdio server `fs` with `command`, `args` and `env`, **When** it is imported, **Then** the MCPProxy server has the same command, args and env, and shows as quarantined awaiting review like any other newly added server.
3. **Given** a remote server of type `remote` (SSE) and one of type `remote-streamable`, **When** imported, **Then** they become URL servers with the matching transport.
4. **Given** a server MCP Router had disabled, **When** imported, **Then** it is added disabled.
5. **Given** a server with tool toggles where some tools are off, **When** imported, **Then** those tools are hidden on the MCPProxy server and the summary says so.
6. **Given** no MCP Router data is found at the default location, **When** the command runs, **Then** it exits with a clear message naming the paths it looked at and suggesting `--path`; it does not touch configuration.

---

### User Story 2 - Preview first, with nothing changed (Priority: P1)

The user runs `mcpproxy import --from mcp-router --dry-run` to see exactly what would happen.

**Why this priority**: People will not run an importer on their working setup without a safe preview. It is also the first measurable funnel step.

**Independent Test**: Run `--dry-run` against the fixture; assert the configuration, keyring and telemetry import counter are unchanged and the output equals what a real run later reports.

**Acceptance Scenarios**:

1. **Given** any valid source, **When** `--dry-run` is used, **Then** no server is added, no secret is stored, and the import counter is not incremented.
2. **Given** a dry run followed by a real run on the same source and state, **When** compared, **Then** the same servers are listed as to-import, skipped and unmappable.
3. **Given** `-o json`, **When** `--dry-run` runs, **Then** the result is machine-readable with the same content as the table, and contains no secret value.

---

### User Story 3 - No secret moves silently (Priority: P1)

MCP Router stores environment variables in plain text and bearer tokens encrypted by the operating system. The user must see what will be stored and where, and must approve it.

**Why this priority**: The reputation of a security-first proxy depends on it. A silent copy of API keys into a new config file would be a defect.

**Independent Test**: Import a fixture containing an API-key-like env var, a plain-text bearer token and an encrypted bearer token. Verify what is shown, what is stored, and where.

**Acceptance Scenarios**:

1. **Given** servers with env values or tokens, **When** the preview is shown, **Then** each is listed by name with "value present" and "looks like a secret" flags and never with its value.
2. **Given** secret-like values and a working OS keyring, **When** the user confirms the import, **Then** those values are stored in the keyring and the configuration holds only a reference, matching how `upstream add --secret-env` behaves. Non-secret values may stay inline.
3. **Given** secret-like values and no keyring, **When** the user runs a non-dry import, **Then** the command stops before writing anything and says how to proceed (an explicit, named opt-in to keep them inline, or fix the keyring). It never falls back to plaintext on its own.
4. **Given** an encrypted bearer token, **When** the server is imported, **Then** the server is added without that header, flagged "needs bearer token", and the summary tells the user to re-enter it. MCPProxy does not try to decrypt it.
5. **Given** a non-interactive run (no terminal), **When** secret-like values exist and no explicit choice flag was given, **Then** the command refuses rather than guessing.
6. **Given** MCP Router's client tokens (`MCPR_TOKEN`), **When** the import runs, **Then** they are never extracted, printed, logged, stored or sent, and `shared-config.json` is never opened.
7. **Given** a sensitive DXT parameter substituted into an argument, **When** imported, **Then** the argument carries a keyring reference, not the value, even if the parameter has an innocuous name.
8. **Given** a source string containing `${keyring:...}` or `${env:...}`, **When** imported, **Then** the entry is reported unmappable and no destination secret is resolved.

---

### User Story 4 - Run it twice, nothing breaks (Priority: P1)

The user re-runs the command (they added servers in MCP Router since, or the first run was interrupted).

**Why this priority**: Importers are rerun. Duplicates and overwrites destroy trust.

**Independent Test**: Run twice against the same source; then change one source server and run again.

**Acceptance Scenarios**:

1. **Given** a completed import, **When** the same command is re-run, **Then** nothing is added or changed, and every server is reported as already present.
2. **Given** a server of the same name exists in MCPProxy with a different configuration, **When** the import runs, **Then** it is not overwritten; the summary reports a conflict naming the differing fields (not values) and how to resolve it.
3. **Given** a new server was added to MCP Router after the first import, **When** re-run, **Then** only the new server is added.
4. **Given** an interrupted first run, **When** re-run, **Then** it completes the remainder without duplicates.

---

### User Story 5 - Tell me what did not come across, and what to reconnect (Priority: P2)

At the end the user gets an honest list: what was imported, what was skipped and why, what could not be mapped, and which AI clients still talk to MCP Router and how to point them at MCPProxy.

**Why this priority**: Without it the import "works" but the user's clients still call a dead app, and the first thing they see is a broken workflow.

**Independent Test**: Provide fixture client configs containing MCP Router's bridge entry; verify the reconnect list and that no config file is modified.

**Acceptance Scenarios**:

1. **Given** a server field with no MCPProxy equivalent (auto-start, project, workspace, description, DXT input parameters), **When** imported, **Then** it is listed per server under "not carried over".
2. **Given** a local server that depends on files in MCP Router's data directory (DXT-installed), **When** imported, **Then** the summary warns that it still depends on that directory.
3. **Given** a stdio server whose arguments contain unresolved `${param}` placeholders, **When** imported, **Then** placeholders that MCP Router would have filled from `env` or parameter defaults are filled the same way, and any left over are listed as "needs a value".
4. **Given** a Claude Desktop, Cursor, Windsurf, VS Code or Codex config that contains MCP Router's bridge entry, **When** the import finishes, **Then** the summary names each such client and the exact `mcpproxy connect <client>` command, and notes the old `mcp-router` entry should then be removed. The command only reads those files.
5. **Given** the user's client config still contains an entry that points at MCP Router's local endpoint, **When** discovered by the import, **Then** it is never imported as a server.
6. **Given** a Cline installation (no MCPProxy connect target), **When** found, **Then** the summary says so in one line rather than offering a command that does not exist.
7. **Given** MCP Router had workflows, hooks, skills, logs or cloud data, **When** the import ends, **Then** the summary says in one line that these are not imported.

---

### User Story 6 - Use the exported file instead (Priority: P2)

Some users only have the JSON that MCP Router's Export button produced (or are on a machine where the database is not available).

**Why this priority**: It is the officially documented path in MCP Router's end-of-support guide, and it needs no database access.

**Independent Test**: Run `mcpproxy import --from mcp-router --path mcp-servers-2026-09-18.json` on a fixture export.

**Acceptance Scenarios**:

1. **Given** an export file, **When** passed via `--path`, **Then** it is recognised as an MCP Router export and its servers are imported with the same rules.
2. **Given** an export entry with an empty command (how MCP Router exports remote servers), **When** imported, **Then** it is not added as a broken server; it is listed as "remote server: URL was not exported" with the advice to re-run against the data directory, or to add the URL manually.
3. **Given** `--path` points at a directory, a database file, or an export file, **When** the command runs, **Then** each is accepted and the input kind is stated in the output.
4. **Given** `--path` points at something else (a random JSON, a binary), **When** run, **Then** it is rejected with an error that says what was expected.
5. **Given** any export file, **When** imported, **Then** the output states what an export cannot carry (disabled state, tool toggles, bearer tokens, parameter schemas, other workspaces) and that imported servers therefore arrive enabled with all tools visible, after review.

---

### User Story 7 - Import from the Web UI (Priority: P3, non-gating)

A user who prefers the Web UI picks "MCP Router" in the existing Add / Import server flow. It shows the same preview and applies the same rules.

**Why this priority**: Cheap if it reuses the existing import panel and endpoints, but the CLI alone proves the experiment.

**Independent Test**: In the Web UI, open Add server, choose Import, choose MCP Router, observe detected location, preview, and confirm.

**Acceptance Scenarios**:

1. **Given** MCP Router data exists, **When** the user opens the import panel, **Then** MCP Router appears among the detected sources with a count of servers found.
2. **Given** the preview, **When** the user confirms, **Then** the result and summary match the CLI's for the same source, including the secret rules, the quarantine default, and the reconnect list.
3. **Given** a read-only or agent-scoped session, **When** the user attempts it, **Then** the same authorization as other server-adding operations applies.

---

### User Story 8 - Measure the experiment without identifying anyone (Priority: P1)

The maintainer needs to know how many people completed an import from MCP Router, without learning who they are or what they imported.

**Why this priority**: The experiment's success criterion (25 imports by day 60) is meaningless without it.

**Independent Test**: Run a real import and a dry run; inspect the next heartbeat payload.

**Acceptance Scenarios**:

1. **Given** a real import that added at least one server, **When** the next telemetry heartbeat is built, **Then** it carries a count for `import_source` = `mcp-router` and nothing else about the import.
2. **Given** a dry run, a run that committed no server addition (all failed, all already present, refused), or a run without the daemon, **When** the heartbeat is built, **Then** the counter is unchanged.
3. **Given** telemetry is disabled by any existing opt-out, **When** an import runs, **Then** nothing is recorded or sent.
4. **Given** the heartbeat payload, **When** scanned by the existing privacy check, **Then** it passes: only a fixed enum key and a non-negative integer; no server names, counts of servers, paths, URLs, or MCP Router identifiers.
5. **Given** the counter window resets after an accepted send, **When** several imports occurred in the window, **Then** the delta is reported once and not repeated.

---

### Edge Cases

- **MCP Router is still running** and holds its database open. The importer must read a consistent snapshot without modifying or locking the live files (and must tell the user nothing needs to be closed).
- **Older database** without columns added later (no `server_type`, `remote_url`, `tool_permissions`, ...). Missing columns mean defaults, not errors.
- **Corrupt, encrypted-at-rest or unreadable database**: clear error, nothing imported, exit code distinct from "nothing found".
- **Multiple local workspaces**: the default workspace plus others. Servers from all local workspaces are listed with their workspace; the same name in two workspaces is a collision, reported (first wins by default, the rest listed) and selectable with `--workspace`.
- **Names with spaces/Unicode/duplicates after sanitizing**: handled by the existing sanitizer; a collision after sanitizing is reported, never silently merged.
- **Hostile values**: a name, URL, command or argument containing control characters, shell metacharacters, extreme length, or a `file:`/non-http scheme. Treated as data: validated like any added server, never executed or interpreted during import.
- **Remote URL to a private address**: allowed (MCPProxy supports local servers) but shown in the preview.
- **A source server that is MCPProxy itself** (an entry pointing at this instance): skipped as self-reference.
- **Case-only or whitespace-only name differences** with existing servers.
- **The user is not on a supported OS** (Linux): best-effort default path; `--path` always works.
- **Zero servers in source**: a clear "nothing to import" message and a success exit.
- **Both daemon running and not running**: works in both modes, as `upstream import` does.
- **Large source** (hundreds of servers): preview stays readable (grouped, summarised) and completes in seconds.

## Requirements *(mandatory)*

### Functional Requirements

**Command and sources**

- **FR-001**: The system MUST provide `mcpproxy import --from mcp-router [--path <path>] [--dry-run]`. `--from` is required, accepts `mcp-router`, and rejects unknown values with the list of supported sources.
- **FR-002**: Without `--path`, the system MUST auto-detect MCP Router's data location per operating system (macOS, Windows; Linux best effort) and name every location it checked when none is found.
- **FR-003**: `--path` MUST accept (a) MCP Router's data directory, (b) a database file inside it, or (c) an exported server JSON file, and MUST state which kind it recognised.
- **FR-004**: The system MUST read all local workspaces' servers from the data directory, tolerate databases created by any MCP Router release that had the `servers` table (missing columns take defaults), and read the source without modifying it.
- **FR-004a**: Workspace database locations recorded inside the source database are untrusted. The system MUST only open `mcprouter.db` and `workspaces/<id>/database.db` beneath the selected data directory, where `<id>` matches a conservative identifier pattern; the canonical path (symlinks resolved) MUST stay inside that directory. Any other recorded location (absolute, parent-relative, symlink escaping the directory) is skipped with a warning naming the workspace, never followed, and never opened. Paths in server `command`/`args` (for example DXT extension folders) are never opened or probed.
- **FR-005**: The system MUST work against a database that MCP Router currently has open, from a consistent snapshot, without requiring the user to quit MCP Router and without opening the live files for writing. The snapshot lives in a private, permission-restricted location removed on exit. Consistency MUST be verified (the copy passes an integrity check and the source files did not change while copying), retried a bounded number of times, and if it cannot be obtained the command MUST fail safely with advice to close MCP Router or use an export file. A torn snapshot MUST never be read as if it were consistent.
- **FR-005a**: When several local workspaces hold the same server name, the default workspace wins, then others in creation order; the rest are listed as collisions. `--workspace <name-or-id>` MUST limit the import to one workspace.

**Mapping**

- **FR-006**: The system MUST map each source server to an MCPProxy server per this table:

  | MCP Router | MCPProxy |
  |------------|----------|
  | local server: command, args, env | stdio server with the same command, args, env |
  | `${param}` / `${user_config.param}` placeholders in args | resolved exactly as MCP Router resolved them (`research.md` §2: whole-argument match only; `${user_config.x}` takes parameter `x`'s default first, then env `user_config.x`, then parameter `user_config.x`'s default; `${x}` takes a non-empty env `x` first, then parameter `x`'s default); leftovers are unmappable (FR-006b) |
  | remote (SSE) | URL server, SSE transport |
  | remote-streamable | URL server, streamable HTTP transport |
  | remote URL | URL |
  | plain-text bearer token | `Authorization: Bearer` header, subject to the secret rules (FR-009..FR-011) |
  | encrypted bearer token | not imported; "needs bearer token" |
  | disabled | added disabled |
  | tool toggles set to off | those tools hidden on the server |
  | auto-start, project, workspace, description, version, verification status, parameter schema | not carried over; listed |

- **FR-006a**: Placeholder resolution MUST NOT launder secrets into unprotected fields. A value substituted into `args` that comes from a parameter marked sensitive, from a secret-like env var, or from a parameter whose *name* the existing name-based classifier judges secret-like (no value-based detection is added) MUST be stored as a keyring reference (per FR-011) and the argument MUST carry the reference, not the value. Credentials embedded directly in a URL or argument (query token, `--api-key=...`) are listed in the plan as "inline credential" and are imported only with `--secrets-inline`; otherwise that server is reported as needing a decision and is not added.
- **FR-006b**: Source text is data, not MCPProxy secret syntax. After placeholder resolution, any remaining `${...}` sequence (including `${env:...}` and `${keyring:...}`) in a command, argument, env value, URL or header from the source MUST make the entry *unmappable* ("unresolved or reserved placeholder"). Only references generated by the importer itself may carry resolution semantics.
- **FR-007**: A source entry that cannot form a valid server MUST be classified and reported, never added in a broken state. *Unmappable* (not added): empty command, remote server with no URL, invalid URL or name, unresolved or reserved placeholder (FR-006b), inline credential without `--secrets-inline`. *Needs configuration* (added disabled and quarantined): remote server whose bearer token was encrypted.
- **FR-008**: Entries that point at this MCPProxy instance, or that are MCP Router's own client bridge (`@mcp_router/cli connect`) or its local endpoint, MUST be skipped with the reason stated.

**Secrets and trust**

- **FR-009**: The system MUST NEVER print, log, or place in telemetry any secret value. Previews show names and flags only (value present, looks like a secret, empty or placeholder), as the existing import preview does.
- **FR-010**: Before a non-dry run writes anything, the system MUST show what will be stored: for each server, which env vars and headers will be kept, which will go to the OS keyring, and which are missing.
- **FR-011**: Secret-like values (the existing import classifier's judgment, plus every plain-text bearer token) MUST be stored through the same keyring mechanism and reference format as `upstream add --secret-env/--secret-header`. If the keyring is unavailable the run MUST stop before any write, naming the reason, unless the user passes `--secrets-inline` (never implied by `--yes`). An interactive run MUST ask one confirmation after showing the plan; `--yes` skips it; a non-interactive run without `--yes` MUST refuse rather than choose.
- **FR-012**: Every imported server MUST be added through the shared add path with the quarantined flag set and unapproved, independent of trust mode, exactly as `upstream import` does today. If the operator has globally disabled quarantine, existing global semantics apply unchanged and the summary MUST report the state actually read back. The command MUST NOT offer a way to skip quarantine.
- **FR-012a**: The shared apply path MUST carry the enabled flag and the hidden-tools list, in both daemon and config-file modes, and MUST return the true per-server outcome (added, failed with reason). Totals, exit status and telemetry MUST derive from those outcomes, not from attempted adds.
- **FR-013**: The system MUST NOT open `shared-config.json` or any other MCP Router file besides the approved database files, their SQLite sidecar files (`-wal`, `-shm`; same containment, symlink and regular-file checks as the database, FR-004a) and an export file the user named. Client configuration files are opened read-only solely to detect MCP Router's bridge entry; the `MCPR_TOKEN` values found there MUST NOT be extracted, retained, logged, displayed, stored or sent.
- **FR-014**: All source content MUST be treated as untrusted data: validated with the same rules as any added server, never executed, never interpreted as a command, and bounded in size. A malicious database must not be able to make the import run code, write outside MCPProxy's configuration, or read files other than the ones named in this spec.

**Idempotency**

- **FR-015**: Re-running against the same source MUST add nothing and change nothing, reporting each server as already present.
- **FR-016**: A server whose name exists in MCPProxy with a different configuration MUST NOT be overwritten; the summary MUST report the conflict and which fields differ (names only, never values).
- **FR-016a**: Names match after the existing sanitizer, case-insensitively, with surrounding whitespace trimmed. "Same configuration" compares transport, command, args, URL, and the *names* of env vars and headers (never secret values or keyring reference names). It ignores enabled state, quarantine and approval state, and anything the operator edited after import that is outside those fields. Two source servers that sanitize to the same name are a collision, reported, not merged.
- **FR-017**: Each server is committed independently. A failure while adding one server MUST attempt to roll back only the keyring entries written for that server and MUST NOT touch entries referenced by servers already committed. If a rollback is incomplete or indeterminate (the keyring backend can time out or mark itself unavailable), the output MUST name the possibly orphaned references (names, never values). Keyring reference names MUST be deterministic per server and field, so a re-run overwrites and reuses them instead of creating new ones. Unreferenced entries (one or more per failed server) may therefore remain after a backend failure or a hard kill; this is documented and a re-run repairs it. Rollback happens only after a definite rejection. If the outcome of an add is unknown (timeout, lost response), the server is reported as *uncertain*, its keyring entries are kept, it counts as a failure for exit status but earns no telemetry count, and re-running resolves it idempotently. The whole apply phase is serialised across importer processes so concurrent runs cannot delete each other's entries.

**Reporting**

- **FR-018**: The output MUST contain: servers to import, skipped (already present, filtered, self-reference), failed or unmappable (with reason), fields not carried over per server, categories not imported at all (workflows, hooks, skills, logs, cloud), and warnings (DXT path dependency, unresolved placeholders, private-address URLs).
- **FR-019**: The output MUST list AI clients still configured to use MCP Router, found by reading their configuration files read-only, with the exact `mcpproxy connect <client>` command for each client MCPProxy supports and a one-line note for those it does not. Client configuration files MUST NOT be modified by this command.
- **FR-020**: Output MUST support the repository's standard `-o json|yaml` modes and the machine-readable help (`--help-json`), with identical content to the table output.
- **FR-021**: Exit status MUST distinguish: (a) success, including "nothing to import", already-present only, and a `--dry-run` whose planning completed; (b) partial success (at least one server added and at least one failed, conflicted or unmappable); (c) nothing added although something was expected (all failed, unmappable or conflicting); (d) source not found; (e) source unreadable, malformed, or no consistent snapshot; (f) refused by the secret rules (no keyring, non-interactive without `--yes`). A `--dry-run` that cannot read the source fails with (d) or (e). The numeric codes are fixed in the plan, reuse the existing exit-code constants where they fit, and are documented.

**Telemetry**

- **FR-022**: The daemon MUST record, for each non-dry run applied through it that committed at least one server addition (outcome (a) or (b) of FR-021 with at least one real add; a re-run that adds nothing does not count), one count under the closed enum key `import_source` = `mcp-router`. The enum MUST be closed (known sources plus `other`), and a value outside the enum MUST be impossible to emit.
- **FR-023**: The counter MUST carry only a non-negative integer per enum key. It MUST NOT carry server names, number of servers, paths, URLs, usernames, or any MCP Router identifier, MUST honour every existing telemetry opt-out, and MUST pass the existing payload privacy scan.
- **FR-023a**: The eligibility check (every existing opt-out, including environment overrides, evaluated for the process that records the event) MUST run before the event is recorded, not only before sending. Disabled → import → enabled MUST NOT emit the earlier import retroactively.
- **FR-024**: The telemetry change MUST be documented in the telemetry document and carry a schema version bump. The privacy scanner and schema validation MUST recognise the new field structurally: closed key set, non-negative integers only, with negative tests for unknown keys, negative numbers and wrong types.

**Web UI (P3)**

- **FR-025** *(non-gating for the release)*: The Web UI SHOULD offer MCP Router as an import source in the existing import panel, using the same detection, preview, secret rules, quarantine default, idempotency and reconnect list as the CLI. It MUST NOT introduce a second import code path.

**Documentation**

- **FR-026**: The system MUST ship user documentation for the command, including what is and is not carried over and a short "after you import" checklist. The page is linked from, and consistent with, the migration page handled on the marketing website.

### Key Entities

- **Import source**: a recognised origin of servers (`mcp-router` now; the enum is closed and extensible). Has a detected location and an input kind (data directory, database, export file).
- **Source server**: one MCP Router server row, including its workspace, with fields as in `research.md`. Immutable input.
- **Import plan**: the result of reading and mapping, before any write. Lists to-import, skipped, failed and unmappable servers, per-server not-carried-over fields, secret handling per field, and detected clients to reconnect. A dry run prints exactly this.
- **Import outcome**: what a real run did. Same shape as the plan plus what was written.
- **Reconnect advice**: for each detected client, whether it still uses MCP Router and the command to switch.
- **Import counter**: a delta count per source enum value in the telemetry window.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A user with MCP Router installed goes from running the command to having all mappable servers added and quarantined in under 2 minutes of their time (excluding quarantine review), with at most one confirmation prompt.
- **SC-002**: On a fixture database with 50 servers of all kinds, the dry run and the real run complete in under 5 seconds each, and the real run's results equal the dry run's plan.
- **SC-003**: 100% of servers in a fixture covering local, remote and remote-streamable are either imported correctly, added disabled as "needs configuration", or listed unmappable with a specific reason; zero are added enabled in a state that cannot run (for example an empty command or an unresolved placeholder).
- **SC-004**: Zero secret values appear in any output, log, report or telemetry payload across the full test matrix (verified by a scan with seeded canary secrets).
- **SC-005**: A second run against an unchanged source changes nothing (byte-identical configuration), and a run against a changed source adds only the new servers.
- **SC-006**: 100% of imported servers are quarantined under default settings.
- **SC-007**: Experiment target: at least 25 successful import runs counted under `import_source=mcp-router` within 60 days of the release that ships the command. This is run-based, measured among installs where the core is running; installs are approximated by the heartbeat's anonymous id and are not exact.
- **SC-008**: For a fixed fixture matrix, each of command, args, env, URL, transport, enabled state and hidden tools is carried over equivalently for every server whose source contains it, where "equivalent" means after the specified mapping (placeholder resolution, FR-006) and the specified secret-reference transformation (FR-006a, FR-011). The only other exceptions are encrypted bearer tokens and fields an export file lacks; every exception is listed in the output, never silently dropped.
- **SC-009**: Each step of the reconnect advice, when followed on a machine with a client configured through MCP Router, results in that client using MCPProxy (checked once manually per supported client).

## Assumptions

- A-1: MCP Router's on-disk format is frozen: the project is archived and `v0.6.4` is final, so one reader written against `research.md` covers every real database. Versions before the columns were added are handled by defaults.
- A-2: A new top-level `import` command is added rather than extending `upstream import`, for discoverability by someone who knows only "MCP Router is dead". The parsing work is a new format in the existing import library. `upstream import` itself is not changed.
- A-3: Reading the database directly is the main path, because the export loses remote servers. The export file is a supported secondary input.
- A-4: Encrypted bearer tokens are never decrypted, even if technically possible on the same machine; this is a deliberate security decision.
- A-5: Linux is best effort only; MCP Router never shipped a Linux build.
- A-6: Projects and Workspaces are not recreated as MCPProxy profiles in this release; the original grouping is reported so a later feature or the user can recreate it.
- A-7: The Web UI entry is P3 and non-gating. It ships only if it reuses the current import panel and REST endpoints with no new import code path; the plan places it last so it can be dropped alone.
- A-8: The `import_source` counter is a count of daemon-applied successful runs, not distinct installs. Installs can be approximated on the dashboard by `anonymous_id` (not `heartbeat_id`, which rotates every accepted window); that approximation is acknowledged as imprecise.
- A-9: Servers copied by MCP Router from the user's own client configs may duplicate servers MCPProxy already has; name-based skip handles it and the summary says so.
- A-10: `--no-quarantine` is intentionally absent from this command. A user who wants it can approve in the review flow like for any server.
