# Feature Specification: Retention Telemetry Hygiene & Activation Instrumentation

**Feature Branch**: `044-retention-telemetry-v3`
**Created**: 2026-04-24
**Status**: Draft
**Input**: User description: "Retention telemetry hygiene + activation instrumentation + auto-start defaults. Payload schema v3 for mcpproxy telemetry with env_kind, launch_source, autostart_enabled, activation, env_markers. Anonymity preserved; env_markers booleans only; BBolt-backed activation bucket; login-item ON by default; installer sets MCPPROXY_LAUNCHED_BY=installer; MCP initialize records clientInfo.name; retrieve_tools bumps counter. Design doc at docs/superpowers/specs/2026-04-24-retention-telemetry-hygiene-design.md."

## Clarifications

### Session 2026-04-24

- Scan result: no critical ambiguities. The approved design doc at `docs/superpowers/specs/2026-04-24-retention-telemetry-hygiene-design.md` resolves every major decision (decision trees, enum values, BBolt bucket shape, tray/installer hooks, PII invariants). Remaining scope decisions (Windows tray deferral, heartbeat cadence, dashboard/worker changes) are handled in the Assumptions and Non-Goals sections.
- Q: Should the new activation state be surfaced via `/api/v1/status` and `mcpproxy telemetry status` for local debugging? → A: Yes (read-only). Activation flags, rolling counters, env_kind, launch_source, and autostart_enabled are exposed via the existing status endpoints as a read-only snapshot so developers and the tray can display activation progress without sending telemetry. This matches the existing pattern where `mcpproxy telemetry status` shows the anonymous ID.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Ground-truth CI classification (Priority: P1)

A product owner looking at retention dashboards needs to know whether an install is a real human or an automated environment (CI runner, cloud IDE, container). Today the dashboard classifies post-hoc using version-string heuristics and GitHub Actions IP ranges; both miss real CI (non-GitHub runners, properly-versioned container images) and occasionally mislabel real users. The mcpproxy client should declare its own environment class on every heartbeat so the dashboard can filter on ground truth.

**Why this priority**: Retention is the primary business metric. Without ground-truth classification the retention number is unreliable, which blocks every downstream product decision.

**Independent Test**: Start mcpproxy on a developer laptop, a GitHub Actions runner, and inside a Docker container; verify the payload sent to the telemetry endpoint contains `env_kind` values of `interactive`, `ci`, and `container` respectively, without any environment-variable values leaking into the payload.

**Acceptance Scenarios**:

1. **Given** mcpproxy runs on a macOS developer laptop with no CI env vars, **When** the heartbeat fires, **Then** the payload contains `env_kind=interactive` and `env_markers.has_ci_env=false`.
2. **Given** mcpproxy runs on a GitHub Actions runner (`GITHUB_ACTIONS=true`), **When** the heartbeat fires, **Then** the payload contains `env_kind=ci` and `env_markers.has_ci_env=true`, and no env-var values appear anywhere in the payload.
3. **Given** mcpproxy runs inside a Docker container (`/.dockerenv` present, no CI markers), **When** the heartbeat fires, **Then** the payload contains `env_kind=container` and `env_markers.is_container=true`.
4. **Given** mcpproxy runs on a headless Linux server (no DISPLAY, no TTY, no CI markers), **When** the heartbeat fires, **Then** the payload contains `env_kind=headless`.

---

### User Story 2 - Activation funnel visibility (Priority: P1)

A product owner needs to know, for today's real-human first-runs, what percentage reached each activation milestone: configured a server, connected a server, had an IDE call `/mcp`, called `retrieve_tools`. Without these signals the retention debugging is guesswork.

**Why this priority**: Without activation signals the team cannot tell whether low retention is caused by install friction, config friction, IDE wiring friction, or tool-discovery friction. Each has a different fix.

**Independent Test**: On a fresh install with no prior BBolt database, complete each activation step (add server, connect, wire IDE, call retrieve_tools) and verify each monotonic flag transitions from false to true in the subsequent heartbeat; verify each flag stays true thereafter even across restarts.

**Acceptance Scenarios**:

1. **Given** a brand-new install with no activation history, **When** the user connects their first server successfully, **Then** the next heartbeat carries `activation.first_connected_server_ever=true`.
2. **Given** an install where `first_connected_server_ever=true`, **When** the user disconnects all servers and the process restarts, **Then** the heartbeat still carries `first_connected_server_ever=true` (flag is monotonic).
3. **Given** a running mcpproxy instance, **When** an MCP client performs an `initialize` handshake identifying itself as `claude-code`, **Then** subsequent heartbeats list `claude-code` in `activation.mcp_clients_seen_ever`.
4. **Given** a running mcpproxy instance, **When** the builtin `retrieve_tools` is called 12 times within 24 hours, **Then** the next heartbeat reports `activation.retrieve_tools_calls_24h=12` and `activation.first_retrieve_tools_call_ever=true`.
5. **Given** the process has tracked 250,000 token-equivalents of schema savings in the last 24 hours, **When** the heartbeat fires, **Then** `activation.estimated_tokens_saved_24h_bucket="100k_plus"` (bucketed, never a raw number).

---

### User Story 3 - Auto-start default ON (Priority: P2)

Users who install mcpproxy via the DMG expect the tray icon to start automatically on login (the application is a background utility). Today auto-start-at-login is opt-in and ~39 % of macOS v2 installs never recorded a tray request after first launch, suggesting users forgot to re-enable the tray. Making login-item ON by default (with a clear opt-out) should lift retention.

**Why this priority**: Secondary to the telemetry changes (P1) because it depends on them to measure success, but directly moves the retention metric.

**Independent Test**: Install mcpproxy from a fresh DMG on a clean macOS user account, close the first-run dialog without unchecking the "Launch at login" box, log out and log back in; verify the tray icon appears automatically and that the subsequent heartbeat carries `autostart_enabled=true` and `launch_source=login_item`.

**Acceptance Scenarios**:

1. **Given** a fresh macOS install from DMG, **When** the user accepts the first-run defaults, **Then** a login item is registered with the OS and the next heartbeat reports `autostart_enabled=true`.
2. **Given** a user explicitly unchecks "Launch at login" during first-run, **When** the install completes, **Then** no login item is registered and the heartbeat reports `autostart_enabled=false`.
3. **Given** the installer launches the tray as its final step, **When** the first heartbeat fires within 60 seconds of install, **Then** `launch_source=installer`.
4. **Given** the tray was started by the OS as a login item, **When** the heartbeat fires, **Then** `launch_source=login_item`.

---

### User Story 4 - Privacy & anonymity preservation (Priority: P1)

A privacy-conscious user (and the project's own constitution) requires that no personally identifying information ever leaves the client. The new telemetry fields must add zero PII: detection is boolean-only, the anonymous ID is unchanged, and no env-var values are transmitted.

**Why this priority**: Anonymity is a non-negotiable project invariant. A leak here would violate the project's public telemetry policy and trust.

**Independent Test**: Instrument the payload builder with a validator that fails the test if any string field in `env_markers` is non-boolean, if any env-var value appears anywhere in the payload, if `anonymous_id` format changes, or if any of `username`, `hostname`, `email`, absolute file paths, or machine names appear as values.

**Acceptance Scenarios**:

1. **Given** CI markers like `GITHUB_TOKEN=ghp_abc123` are set in the environment, **When** the heartbeat is built, **Then** the payload contains `env_markers.has_ci_env=true` and nowhere contains the string `ghp_abc123`.
2. **Given** the user's home directory is `/Users/alice`, **When** the heartbeat is built, **Then** the payload does not contain the string `alice` or `/Users/alice` anywhere.
3. **Given** an MCP client identifies with an unrecognized name containing user data like `"claude-code /Users/alice/projects/foo"`, **When** the activation bucket records it, **Then** the recorded value is `"unknown"` (never the raw identifier).
4. **Given** an existing install with a stable `anonymous_id`, **When** the client upgrades from schema v2 to v3, **Then** `anonymous_id` is byte-identical to the v2 payload's value.

---

### Edge Cases

- **Schema v2 readers**: downstream (worker, dashboard) must accept both v2 and v3 payloads. v2 payloads leave the new columns NULL and are still counted for retention.
- **Monotonic flags corrupted**: if the BBolt activation bucket is corrupt or missing, the client must initialize defaults (all flags false) and never crash or block startup.
- **Token estimator overflow**: the 24-hour token-saved estimate could theoretically exceed typical ranges; bucketing caps the top at `100k_plus` so cardinality is bounded.
- **mcp_clients_seen_ever overflow**: capped at 16 entries; the 17th and beyond are dropped (first-in wins, bounded payload size).
- **Unknown client identifier**: any client that does not set `params.clientInfo.name` in the MCP `initialize` handshake is recorded as `"unknown"`.
- **Telemetry disabled**: when `MCPPROXY_TELEMETRY=false` or `mcpproxy telemetry disable` is active, none of the new fields are computed or transmitted (existing opt-out behavior applies).
- **Login-item API fails on macOS**: if registering the login item fails (SMLoginItemSetEnabled returns false, or SMAppService throws), the tray logs a warning, leaves `autostart_enabled=false` in the socket response, and continues to run normally.
- **Launch source disambiguation**: when none of the detection rules match (e.g., detached shell + no env var + no parent-process hint), `launch_source=unknown`.
- **First-run detection**: installer-set env var `MCPPROXY_LAUNCHED_BY=installer` persists only for one heartbeat, then the client clears it so subsequent launches report `launch_source=login_item` or `tray`.
- **Env-kind cache invalidation**: detection runs once per process lifetime; if the environment changes mid-process (e.g., a user sets `CI=true` in a running shell), the cached value is not updated until the next process start (documented behavior).

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The client MUST compute an `env_kind` value from an ordered decision tree (CI markers -> cloud-IDE markers -> container markers -> OS heuristics) exactly once per process start and reuse the cached value for the remainder of the process lifetime.
- **FR-002**: The `env_kind` value MUST be one of the fixed enum values `interactive`, `ci`, `cloud_ide`, `container`, `headless`, or `unknown`; any other value MUST be treated as a programming error and rejected by the payload builder.
- **FR-003**: The client MUST compute a `launch_source` value (`tray`, `login_item`, `cli`, `installer`, or `unknown`) at startup based on the installer-set env var, the tray socket handshake, parent-process heuristics, and TTY presence.
- **FR-004**: When the `MCPPROXY_LAUNCHED_BY=installer` env var is present at startup, the client MUST report `launch_source=installer` on exactly one heartbeat and then clear the signal so subsequent heartbeats report a different, accurate source.
- **FR-005**: The client MUST compute an `autostart_enabled` value as a tri-state (`true`, `false`, or null) -- `true`/`false` on platforms where login-item state is readable (macOS, Windows), `null` on platforms where it is not applicable (Linux today).
- **FR-006**: The client MUST persist monotonic activation flags (`first_connected_server_ever`, `first_mcp_client_ever`, `first_retrieve_tools_call_ever`) in durable local storage so that once they flip true they remain true across restarts, upgrades, and database compaction.
- **FR-007**: The client MUST record observed MCP client names (from `initialize` handshake `params.clientInfo.name`) into a deduplicated set, capped at 16 entries; clients that do not provide a name or provide a name containing path-like characters MUST be recorded as the literal string `unknown`.
- **FR-008**: The client MUST increment a sliding-window counter `retrieve_tools_calls_24h` on every builtin `retrieve_tools` call and decay the counter at the 24-hour heartbeat boundary.
- **FR-009**: The client MUST compute `estimated_tokens_saved_24h_bucket` by summing `tools_not_exposed_to_client * avg_tokens_per_tool_schema` over the last 24 hours and mapping the result to one of the fixed buckets: `"0"`, `"1_100"`, `"100_1k"`, `"1k_10k"`, `"10k_100k"`, `"100k_plus"`. Raw numbers MUST NOT be transmitted.
- **FR-010**: The client MUST emit an `env_markers` object containing only booleans (`has_ci_env`, `has_cloud_ide_env`, `is_container`, `has_tty`, `has_display`); any non-boolean value in this object is a defect and MUST fail the payload-builder validator.
- **FR-011**: The client MUST NOT transmit env-var values, usernames, hostnames, email addresses, absolute file paths, or any other identifier that can correlate to a human. The payload builder MUST include a self-check that scans the serialized payload for known user-path prefixes and rejects the payload if any are found.
- **FR-012**: The client MUST bump `schema_version` from 2 to 3 for payloads containing the new fields. The v2 payload builder MUST remain callable from unit tests to verify backward compatibility.
- **FR-013**: When telemetry is disabled (`MCPPROXY_TELEMETRY=false` or `mcpproxy telemetry disable`), the client MUST NOT compute, persist, or transmit any of the new fields; the activation bucket MAY exist in BBolt but MUST not be populated.
- **FR-014**: The macOS tray MUST, on first launch (no prior login-item state detected), register itself as a login item by default and present a clear opt-out control to the user.
- **FR-015**: The macOS tray MUST expose the current login-item state via the existing tray-to-core socket so the core can populate `autostart_enabled` in each heartbeat without re-querying the OS.
- **FR-016**: The macOS installer (DMG post-install script) MUST launch the tray once with `MCPPROXY_LAUNCHED_BY=installer` set in the child environment so the first heartbeat correctly attributes the install.
- **FR-017**: The `anonymous_id` field MUST be byte-identical in v2 and v3 payloads for the same installation; upgrading from v2 to v3 MUST NOT regenerate, rotate, or modify it.
- **FR-018**: The client MUST expose a read-only snapshot of the new telemetry state (activation flags, rolling counters, env_kind, launch_source, autostart_enabled) via the existing status endpoint (`/api/v1/status`) and the `mcpproxy telemetry status` CLI command so developers and the tray can display activation progress without triggering a telemetry transmission.

### Key Entities

- **Telemetry Payload (v3)**: An anonymous JSON document sent on each heartbeat. Contains `schema_version=3`, `anonymous_id`, existing v2 fields, and the five new fields (`env_kind`, `launch_source`, `autostart_enabled`, `activation`, `env_markers`).
- **Environment Kind**: A single enum value derived once per process from OS signals and env markers. One of `interactive`, `ci`, `cloud_ide`, `container`, `headless`, `unknown`.
- **Launch Source**: A single enum value indicating how the process was started. One of `tray`, `login_item`, `cli`, `installer`, `unknown`.
- **Activation Record**: A durable set of monotonic booleans plus a rolling set of observed client identifiers and a pair of bucketed counters. Lives in a dedicated activation bucket in the local datastore.
- **Env Markers**: A boolean-only object that mirrors the signals used to derive `env_kind`, allowing dashboards to audit classification without re-transmitting env-var values.
- **MCP Client Fingerprint**: A short enum-like string (e.g. `"claude-code"`, `"cursor"`, `"windsurf"`, `"unknown"`) identifying an IDE or agent that has called `/mcp` on this install.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Within 7 days of v0.25 release, at least 95 % of incoming heartbeats carry a non-null `env_kind` value.
- **SC-002**: Within 7 days of v0.25 release, the dashboard's "real-human install" count changes by at most 5 % when flipping from the legacy version-rule heuristic to the new `env_kind` filter, confirming heuristic and ground truth agree.
- **SC-003**: On macOS v0.25+ first heartbeats, at least 90 % report `autostart_enabled=true`.
- **SC-004**: On new macOS v0.25+ installs, at least 50 % of first heartbeats carry `launch_source=installer`.
- **SC-005**: Day-2 retention on macOS v0.25+ is no worse than the v0.24 baseline (78 %); stretch goal is a measurable lift.
- **SC-006**: Over a 7-day window after v0.25 becomes the default download, the telemetry worker rejects zero v3 payloads for validation errors.
- **SC-007**: A test that scans every v3 payload for any of a blacklist of user-path prefixes, usernames, hostnames, and email patterns passes with zero matches across 100 randomized synthetic test runs.
- **SC-008**: The activation funnel dashboard shows non-zero values for each of five stages (first-run -> server configured -> server connected -> IDE connected -> retrieve_tools called) within 7 days of v0.25 release.

## Assumptions

- Downstream telemetry worker and dashboard repositories (`mcpproxy-telemetry`, `mcpproxy-dash`) will be updated in separate, coordinated changes covered by sibling specs, not this spec. This spec is scoped to the mcpproxy-go client only.
- The existing opt-out mechanisms (`MCPPROXY_TELEMETRY=false` env var and `mcpproxy telemetry disable` CLI) remain authoritative; no new opt-out UI is in scope.
- The existing heartbeat cadence (24 h + startup-kick) and endpoint URL are unchanged.
- `anonymous_id` generation, storage, and lifecycle are unchanged. A v2->v3 migration does not touch this field.
- The project's existing BBolt database (`~/.mcpproxy/config.db`) is the durable store; a new dedicated bucket is acceptable within that database and does not require a separate file.
- Windows tray auto-start default changes and Windows installer final-step checkbox are descoped from the initial release and will follow in a subsequent feature; the payload schema already accommodates them so no rework is required.
- The macOS tray is built in Swift (per the existing `native/macos/MCPProxy` module) and login-item state is toggled via the modern `SMAppService` API on macOS 13+.
- Token-saved estimation uses an average-tokens-per-tool-schema constant that is acceptable as a rough estimate; precise accounting is not required because the value is bucketed before transmission.

## Commit Message Conventions *(mandatory)*

When committing changes for this feature, follow these guidelines:

### Issue References
- Use: `Related #[issue-number]` - Links the commit to the issue without auto-closing
- Do NOT use: `Fixes #[issue-number]`, `Closes #[issue-number]`, `Resolves #[issue-number]` - These auto-close issues on merge

### Co-Authorship
- Do NOT include: `Co-Authored-By: Claude <noreply@anthropic.com>`
- Do NOT include: "Generated with Claude Code" footers

### Example Commit Message
```
feat(telemetry): add env_kind detection for ground-truth CI classification

Related #<issue>

Detection runs once at startup following the ordered decision tree
(CI markers -> cloud IDE -> container -> OS heuristics). Cached for
the process lifetime. Anonymity preserved: env_markers are booleans
only and no env-var values are transmitted.

## Changes
- internal/telemetry/env_kind.go: new detection module
- internal/telemetry/env_kind_test.go: unit tests for every branch
- internal/telemetry/payload_v3.go: new payload builder (schema v3)

## Testing
- go test -race ./internal/telemetry/... passes
- scripts/test-api-e2e.sh passes against a v3 heartbeat fixture
```
