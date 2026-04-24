# Feature Specification: Diagnostics & Error Taxonomy

**Feature Branch**: `044-diagnostics-taxonomy` (work performed on `feat/diagnostics-taxonomy`)
**Created**: 2026-04-24
**Status**: Draft
**Input**: User description: "Stable error-code catalog (MCPX_<DOMAIN>_<SPECIFIC>) for every recoverable failure in upstream/oauth/docker/config/network paths, surfaced in tray, web UI, and CLI with user_message, fix_steps, and docs_url. No auto-remediation; fixes gated on explicit user action. Telemetry v3 adds diagnostics counters."

## Clarifications

### Session 2026-04-24

- Q: What rate limit applies to the fix endpoint per server? → A: 1 request per second per (server, code) pair with a short burst allowance; exceeding it returns 429 with Retry-After.
- Q: What makes a fix "destructive" (and therefore dry-run by default)? → A: Any fix that modifies config files, deletes or rotates stored credentials/tokens, restarts a process/container, mutates upstream server state, or touches the file system outside mcpproxy's own logs/cache. Read-only probes (ping, DNS lookup, filesystem check) are non-destructive.
- Q: How are unresolved design-doc §11 open questions handled? → A: Deferred to the `/speckit.plan` phase. The snap-apparmor Docker fix and concurrent OAuth re-auth handling are tracked as plan-phase decisions, not blockers to the spec.

## Background

Of 306 real-user installs with at least one configured upstream server, only 238 (78%) have any connected server. Roughly 22% of configured servers never connect. Current error reporting is cryptic (e.g. `oauth_refresh_failed`, `docker_status fail`) and the CLI `doctor` output is pass/fail per category with no user-facing fix guidance. There is no stable error-code catalog, and users cannot easily self-serve a fix.

## User Scenarios & Testing *(mandatory)*

### User Story 1 — Diagnose a failing server with a stable error code (Priority: P1)

A user configures an upstream MCP server but the server fails to connect. The user opens the mcpproxy web UI (or runs the CLI), sees a stable, human-readable error code such as `MCPX_STDIO_SPAWN_ENOENT`, a one-sentence explanation of what went wrong, a concrete list of fix steps, and a link to detailed documentation. No cryptic free-form error strings are shown as the only information source.

**Why this priority**: Without a stable code + explanation, every other piece of value in this feature is impossible — the surfacing, the fix buttons, the telemetry, and the docs all key off of the code. This is the MVP.

**Independent Test**: Configure a deliberately-broken stdio server (e.g. `command: /nonexistent`). Call the diagnostics REST endpoint for that server. Assert the response contains a non-empty stable code, a user_message, at least one fix_step, and a docs_url. Passes without any fix UI or tray badge.

**Acceptance Scenarios**:

1. **Given** a user has configured an upstream server with a non-existent stdio command, **When** the user queries that server's diagnostics via REST or CLI, **Then** the response includes `error_code = "MCPX_STDIO_SPAWN_ENOENT"`, a human-readable user_message, an ordered list of fix_steps, and a docs_url pointing to `docs/errors/MCPX_STDIO_SPAWN_ENOENT.md`.
2. **Given** a user has configured an upstream HTTP server with an OAuth provider whose refresh token has expired, **When** the user queries that server's diagnostics, **Then** the response includes `error_code = "MCPX_OAUTH_REFRESH_EXPIRED"` with fix steps that reference the re-login action.
3. **Given** a server is healthy and connected, **When** the user queries its diagnostics, **Then** the response includes an empty or null `error_code` and no fix_steps, and existing non-error diagnostics fields remain populated.

---

### User Story 2 — Surface failures in the web UI and tray (Priority: P2)

A user does not need to run any CLI to notice a failure. The macOS tray icon shows a badge indicating N servers failing, and a menu section labelled "Fix issues (N)" lists them. Clicking a menu item opens the web UI to that server's detail page, where an ErrorPanel renders the code, user message, and fix buttons. Clicking a fix button triggers the fix path (dry-run by default for anything destructive).

**Why this priority**: P2 because the catalog (P1) provides the data; surfacing multiplies its value but could be shipped a release later.

**Independent Test**: With a broken server configured, open the web UI for that server and assert ErrorPanel renders the code + fix buttons. Take a macOS tray screenshot and assert the badge is present and the "Fix issues" menu group lists the broken server. Click a fix button and verify the dry-run response is displayed.

**Acceptance Scenarios**:

1. **Given** any server has an active diagnostic with severity=error, **When** the user looks at the macOS tray, **Then** a red indicator is visible and a "Fix issues (N)" menu group lists failing servers by name.
2. **Given** a server's ErrorPanel is rendered in the web UI, **When** the user clicks a non-destructive fix button, **Then** the fix is executed and a success/failure toast is shown.
3. **Given** a server's ErrorPanel is rendered, **When** the user clicks a destructive fix button, **Then** the dry-run preview is shown by default with a confirmation affordance before any state mutation.

---

### User Story 3 — Fix a failure from the CLI with dry-run safety (Priority: P2)

A power user wants to diagnose and fix a server from the terminal. `mcpproxy doctor --server <name>` prints the error code and fix steps; `mcpproxy doctor fix <CODE> --server <name>` runs the associated fixer. Destructive fixes default to `--dry-run` and require an explicit flag to execute. `mcpproxy doctor list-codes` prints the complete catalog for discovery and for AI-agent consumption.

**Why this priority**: P2 — CLI parity with the UI is essential for ops, scripting, and headless environments.

**Independent Test**: Run `mcpproxy doctor fix MCPX_STDIO_SPAWN_ENOENT --server broken-server` and assert the dry-run preview is printed without mutation. Re-run with explicit execute flag and assert the expected side effect (or appropriate "nothing to do" message) occurs.

**Acceptance Scenarios**:

1. **Given** a failing server, **When** the user runs `mcpproxy doctor --server <name>`, **Then** the output includes the stable error code, user_message, and fix_steps.
2. **Given** a fix exists for a given code, **When** the user runs `mcpproxy doctor fix <CODE> --server <name>` without any additional flags, **Then** the command defaults to dry-run for any destructive fix and shows the preview only.
3. **Given** the user runs `mcpproxy doctor list-codes`, **When** the command completes, **Then** the output enumerates every registered code with its severity, message, and docs URL.

---

### User Story 4 — Telemetry on errors and fixes (Priority: P3)

mcpproxy reports anonymous telemetry about which error codes are occurring and how often users click fix buttons. This lets maintainers prioritize future fixes and measure the `fix_succeeded / fix_attempted` ratio.

**Why this priority**: P3 — valuable for the product team but not user-visible; ships alongside the v3 telemetry schema bump already planned in spec 042.

**Independent Test**: Trigger multiple error codes, click fix buttons, then inspect the next telemetry heartbeat payload. Assert it contains a `diagnostics` object with `error_code_counts_24h`, `fix_attempted_24h`, `fix_succeeded_24h`, and `unique_codes_ever`, all bounded and correctly counted.

**Acceptance Scenarios**:

1. **Given** at least one server has reported an error code in the last 24h, **When** a telemetry heartbeat is emitted, **Then** the payload includes a `diagnostics` object with `error_code_counts_24h` capped at the top 20 codes.
2. **Given** a user has clicked fix buttons or run `doctor fix`, **When** a telemetry heartbeat is emitted, **Then** the payload reports `fix_attempted_24h` and `fix_succeeded_24h` counts.
3. **Given** telemetry is disabled, **When** any error or fix occurs, **Then** no diagnostics telemetry data is transmitted.

---

### Edge Cases

- **Unknown raw error**: If the classifier cannot map a raw error to a known code, the system emits a generic fallback code (e.g. `MCPX_UNKNOWN`) with user_message that asks the user to file a bug report, rather than silently dropping the error.
- **Concurrent fix attempts**: If two clients trigger the same fix for the same server simultaneously, the system rate-limits or serializes the request so no duplicate state changes occur.
- **Rate-limited fix endpoint**: The fix endpoint is rate-limited per-server to protect against accidental rapid-fire clicks.
- **Stale error_code**: When a server transitions from failing to healthy, the diagnostic surface clears the code on the next snapshot; no stale red badge persists.
- **Docs 404**: If a docs URL is missing at release time, CI fails the build rather than allowing a broken link to ship.
- **Destructive fix default**: A fix that mutates config, deletes tokens, or restarts a container defaults to dry-run; the user must explicitly opt in to execute.
- **Code rename**: Once a code is registered and shipped, it is never renamed; deprecation marks it hidden and points at the replacement code.

## Requirements *(mandatory)*

### Functional Requirements

#### Catalog

- **FR-001**: System MUST provide a registry of stable error codes, each following the pattern `MCPX_<DOMAIN>_<SPECIFIC>` where `<DOMAIN>` is one of `OAUTH`, `STDIO`, `HTTP`, `DOCKER`, `CONFIG`, `QUARANTINE`, `NETWORK`, or `UNKNOWN`.
- **FR-002**: Every registered code MUST include a non-empty user-facing message, at least one fix step, a severity (info/warn/error), and a docs URL.
- **FR-003**: The registry MUST be exhaustively tested — a unit test MUST fail the build if any registered code is missing a message, a fix step, or a docs URL.
- **FR-004**: Once shipped, a code name MUST NOT be renamed; deprecation is the only allowed transition, and deprecated codes MUST point at a replacement code.

#### Classification

- **FR-005**: System MUST classify every terminal (non-recoverable-automatically) error in the upstream, OAuth, Docker, config, and network paths into exactly one error code.
- **FR-006**: If no specific classification applies, System MUST emit a documented fallback code rather than a silent or unclassified error.

#### Surfacing — REST API

- **FR-007**: System MUST extend the existing per-server diagnostics REST response to include an additive `error_code`, `user_message`, `fix_steps`, and `docs_url`. Existing consumers MUST continue to function with no field removals or renames.
- **FR-008**: System MUST expose a new REST endpoint that accepts a fix invocation by error code and server name, supports a dry-run mode (default for destructive fixes), records the attempt in the activity log, and is rate-limited to at most 1 request per second per `(server, code)` tuple (with short burst tolerance); requests exceeding the limit MUST return HTTP 429 with a `Retry-After` header.
- **FR-008a**: A fix is considered "destructive" if it: modifies a config file, deletes or rotates a stored credential/token, restarts an upstream process or container, mutates upstream server state, or writes outside mcpproxy's own logs/cache directories. Read-only probes (DNS lookup, TCP ping, filesystem check) are non-destructive.

#### Surfacing — Web UI

- **FR-009**: Web UI MUST render an ErrorPanel on the per-server detail view whenever that server has an active error code, showing the code, user_message, fix_steps as actionable buttons/links, and a docs link.
- **FR-010**: Fix buttons for destructive actions MUST surface the dry-run preview before executing and require explicit user confirmation.

#### Surfacing — macOS Tray

- **FR-011**: macOS tray MUST show a visual indicator when any server has a diagnostic of severity=error, and a lesser indicator when the worst active diagnostic is severity=warn.
- **FR-012**: macOS tray MUST list failing servers in a "Fix issues (N)" menu group, with each entry routing the user to the corresponding web UI diagnostics panel.

#### Surfacing — CLI

- **FR-013**: CLI MUST provide `mcpproxy doctor --server <name>` that prints the active error code, user_message, and fix_steps for that server.
- **FR-014**: CLI MUST provide `mcpproxy doctor fix <CODE> --server <name>` that executes the registered fixer, defaulting to dry-run for any destructive fix.
- **FR-015**: CLI MUST provide `mcpproxy doctor list-codes` that prints the entire catalog with code, severity, short message, and docs URL in human and JSON formats.

#### Docs

- **FR-016**: Every registered code MUST have a documentation page at `docs/errors/<CODE>.md` that explains cause, symptoms, and fix steps.
- **FR-017**: CI MUST fail if any registered code lacks a matching docs file, or if any docs file references a non-existent code.

#### Telemetry (v3)

- **FR-018**: Anonymous telemetry MUST include a `diagnostics` object in the v3 payload with: `error_code_counts_24h` (map capped at top 20 codes by count), `fix_attempted_24h`, `fix_succeeded_24h`, and `unique_codes_ever`.
- **FR-019**: When telemetry is disabled (config or env var), no diagnostics telemetry data MUST be transmitted.
- **FR-020**: Diagnostics telemetry counters MUST live in memory only and MUST NOT be persisted across restarts (consistent with spec 042 privacy rules).

#### Safety & Ground Rules

- **FR-021**: System MUST NOT invoke any fixer automatically — fixers only run in response to an explicit user action (button click or CLI invocation).
- **FR-022**: Destructive fixes (per FR-008a) MUST default to dry-run and require an explicit user action (button confirmation in UI, `--execute` flag in CLI) to execute.
- **FR-023**: Every fix attempt (dry-run or executed) MUST be recorded in the existing activity log with enough metadata to correlate with logs (server name, code, outcome, user/agent identity where applicable).

### Key Entities

- **DiagnosticCode**: A stable identifier string (e.g. `MCPX_OAUTH_REFRESH_EXPIRED`). Immutable once shipped.
- **CatalogEntry**: A registered entry composed of code, severity, user_message, ordered fix_steps, docs_url, and optional deprecation metadata.
- **FixStep**: One of three action types — a command to run, an in-app button to trigger a fixer, or a link to external documentation. Each step has a human label.
- **DiagnosticError**: A per-server runtime record composed of code, severity, the original cause, the server id, and a timestamp. Stored transiently on the server state snapshot.
- **FixAttempt**: An audit record in the activity log composed of code, server, mode (dry-run or execute), outcome (success/failure), and reason on failure.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: 100% of terminal connection errors produced by the upstream, OAuth, Docker, and config subsystems map to a non-empty, registered error code. Verified by unit tests plus E2E coverage.
- **SC-002**: Every registered code has a user_message, at least one fix step, and a valid docs page — enforced by CI (unit test plus docs link-check).
- **SC-003**: Per-server diagnostics responses return in under 50 ms at the 95th percentile under normal load.
- **SC-004**: Over the first 30 days after launch, the ratio `connected_server_count / server_count` among real-user installs rises relative to the pre-launch baseline (measured via telemetry).
- **SC-005**: Among users who click a fix button, at least 50% of fix attempts result in a successful outcome (`fix_succeeded_24h / fix_attempted_24h ≥ 0.5`) once telemetry is live.
- **SC-006**: Support/GitHub issues tagged as "cryptic error" or "can't connect, don't know why" decline by at least 30% within 60 days of launch.
- **SC-007**: No fix action ever executes without an explicit user click or CLI invocation — zero auto-remediation events logged.

## Assumptions

- The existing per-server diagnostics endpoint is extended additively; no existing field is renamed or removed in this feature.
- The v3 telemetry schema bump lands either in this feature or in the concurrent spec 042 work; if spec 042 has not shipped the v3 client when this feature is ready for telemetry integration, the diagnostics telemetry work is deferred behind the v3 client landing.
- No new persistent storage is introduced; diagnostic state lives on the in-memory server snapshot and in the existing activity log.
- The initial catalog is populated from a codebase grep of every terminal error in the upstream, OAuth, server, and Docker subsystems — not pre-enumerated in this spec.
- Fix buttons for auth flows (e.g. re-login) reuse the existing OAuth flow coordinator; no new OAuth flow logic is introduced.
- The macOS tray routes users to the web UI for fix actions rather than duplicating fix logic in Swift.

## Dependencies

- Activity log subsystem (spec 016 / 024) — used to audit fix attempts.
- v3 telemetry schema (spec 042) — extended with the `diagnostics` sub-object.
- Existing OAuth flow coordinator — used by OAuth re-auth fix.
- Existing per-server stateview snapshot — extended with per-server DiagnosticError.
- Existing per-server diagnostics REST endpoint — extended additively.

## Out of Scope

- Fully automated auto-remediation at startup or on a timer.
- Replacing existing structured logging. The taxonomy layers on top of it.
- Changing MCP protocol behaviour.
- Enumerating every possible failure mode; scope is the recurring categories in existing telemetry plus those discovered during the error-inventory phase.

## Commit Message Conventions *(mandatory)*

### Issue References
- Use: `Related #[issue-number]`
- Do NOT use: `Fixes`, `Closes`, `Resolves` (they auto-close issues on merge)

### Co-Authorship
- Do NOT include `Co-Authored-By: Claude ...`
- Do NOT include any "Generated with" attribution line.

### Example Commit Message
```
feat(diagnostics): add STDIO domain classifier + REST surfacing

Related #NNN

Adds the internal/diagnostics classifier for stdio spawn/exit/handshake
failures, wires DiagnosticError into the per-server snapshot, and
extends the diagnostics REST endpoint with error_code/user_message/
fix_steps/docs_url.

## Changes
- internal/diagnostics/{codes,catalog,classifier}.go
- internal/upstream/manager.go — wrap stdio spawn errors
- internal/runtime/stateview — include DiagnosticError
- internal/httpapi — extend diagnostics response

## Testing
- Unit tests for classifier + catalog completeness
- E2E: broken stdio server returns MCPX_STDIO_SPAWN_ENOENT
```
