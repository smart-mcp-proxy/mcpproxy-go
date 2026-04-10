# Feature Specification: Telemetry Tier 2 — Privacy-Respecting Usage Signals

**Feature Branch**: `042-telemetry-tier2`
**Created**: 2026-04-10
**Status**: Draft
**Input**: User description: "Expand the existing anonymous telemetry system into a Tier 2 version that adds privacy-respecting usage signals for product roadmap decisions."

## Overview

mcpproxy already ships an anonymous telemetry v1 — a once-per-day heartbeat with version, OS, and high-level counts. This is too coarse to drive product decisions. Tier 2 expands the heartbeat with twelve additional signals, all designed under hard privacy constraints (no names, no paths, no messages, no per-event timestamps), so that the maintainers can answer concrete questions like *"which client surface should we invest in?"* and *"are users hitting startup failures?"* without ever collecting personally identifiable or competitively sensitive information.

The defining constraint is that **counters live in memory and are flushed once per day**. There is no event stream, no retry queue, no per-call timing. A user with `tcpdump` running can see exactly one HTTPS POST every 24 hours, and the `mcpproxy telemetry show-payload` command lets them inspect the exact JSON before it is sent.

## User Scenarios & Testing *(mandatory)*

### User Story 1 — Maintainers can compare client surface usage (Priority: P1)

A maintainer wants to know whether to invest engineering time in the macOS tray, the web UI, the CLI, or the MCP protocol surface. After Tier 2 ships, they can query their telemetry backend and see daily counts of how many active installs interacted with each surface and the relative request volume per surface.

**Why this priority**: This is the single most-asked product question and it directly shapes the roadmap. Without it the team is guessing about where users actually spend their time.

**Independent Test**: Start `mcpproxy serve`, then exercise each surface (call an MCP tool, run a CLI command that hits the daemon, open the web UI, click in the tray). Run `mcpproxy telemetry show-payload` and verify the JSON contains a non-zero counter for each surface that was used and zero for surfaces that were not.

**Acceptance Scenarios**:

1. **Given** a fresh `mcpproxy serve` process with no requests yet, **When** the user runs `mcpproxy telemetry show-payload`, **Then** the payload contains a `surface_requests` object with all four keys (`mcp`, `cli`, `webui`, `tray`) set to 0 and an `unknown` key also set to 0.
2. **Given** a running daemon, **When** the macOS tray issues a request including `X-MCPProxy-Client: tray/<version>`, **Then** the next `show-payload` reflects an incremented `tray` counter.
3. **Given** a running daemon, **When** an HTTP client calls `/api/v1/status` with no `X-MCPProxy-Client` header, **Then** the request is counted under the `unknown` surface key.
4. **Given** a running daemon, **When** any request hits the `/mcp` endpoint, **Then** it is counted under `mcp` regardless of any header value.

---

### User Story 2 — Maintainers can see which built-in MCP tools matter (Priority: P1)

A maintainer wants to know whether `code_execution` is actually used in the wild, whether users prefer `call_tool_read` over the consolidated `retrieve_tools`, and whether anyone is hitting `quarantine_security` interactively. After Tier 2, the daily heartbeat includes a histogram of built-in tool call counts. Upstream tool calls are reported only as a bucketed total (`upstream_tool_call_count_bucket`) to protect user privacy.

**Why this priority**: Built-in tools are mcpproxy's product surface area. Knowing usage rates is essential for deprecation, polish, and prioritization decisions.

**Independent Test**: From an MCP client, call `retrieve_tools` three times and `code_execution` once, then call any upstream tool fifteen times. Run `mcpproxy telemetry show-payload`. The payload shows `builtin_tool_calls.retrieve_tools = 3`, `builtin_tool_calls.code_execution = 1`, no entry naming the upstream tool, and `upstream_tool_call_count_bucket = "11-100"`.

**Acceptance Scenarios**:

1. **Given** a running daemon, **When** an MCP client calls `retrieve_tools`, **Then** `builtin_tool_calls.retrieve_tools` increments by exactly 1.
2. **Given** an MCP client that calls an upstream tool such as `github:create_issue`, **When** the heartbeat is rendered, **Then** the payload contains no string `github`, `create_issue`, or any combination thereof.
3. **Given** zero upstream tool calls in a flush window, **When** the payload is rendered, **Then** `upstream_tool_call_count_bucket = "0"`.
4. **Given** 250 upstream tool calls in a flush window, **When** the payload is rendered, **Then** `upstream_tool_call_count_bucket = "101-1000"`.

---

### User Story 3 — Maintainers can see REST API endpoint adoption (Priority: P2)

A maintainer wants a histogram of REST endpoint usage to know which endpoints to optimize, which to deprecate, and where to invest documentation effort. Endpoint paths are reported as Chi route templates (e.g., `POST /api/v1/servers/{name}/enable`), never raw URLs. Status codes are reported as classes (`2xx`, `4xx`, `5xx`).

**Why this priority**: Direct input into API stability and documentation prioritization. Slightly lower than tool calls because fewer users hit the REST API directly.

**Independent Test**: Make three requests: `GET /api/v1/servers`, `POST /api/v1/servers/my-secret-server/enable`, and `GET /api/v1/nonexistent`. Run `show-payload` and confirm the payload contains exactly the templated paths and class codes, with no occurrence of the literal string `my-secret-server`.

**Acceptance Scenarios**:

1. **Given** a request to `GET /api/v1/servers` returning 200, **When** the payload is rendered, **Then** `rest_endpoint_calls["GET /api/v1/servers"]["2xx"] = 1`.
2. **Given** a request to `POST /api/v1/servers/anything/enable` returning 200, **When** the payload is rendered, **Then** the literal string `anything` does not appear anywhere in the payload and the templated key `POST /api/v1/servers/{name}/enable` is incremented.
3. **Given** a request to an unknown URL returning 404 (no Chi route matched), **When** the payload is rendered, **Then** the request is counted under a single fallback key `UNMATCHED` and the raw path is not recorded.

---

### User Story 4 — Maintainers can see feature-flag adoption (Priority: P2)

A maintainer wants to know what fraction of installs have enabled `code_execution`, `enable_socket`, `require_mcp_auth`, etc. After Tier 2, every heartbeat snapshots a fixed set of config booleans plus a list of OAuth provider *types* (no client IDs or URLs).

**Why this priority**: Tells the team which features are real and which are theoretical. Drives sunset and investment decisions.

**Independent Test**: Set `enable_web_ui: false`, `code_execution_enabled: true` in config and configure two OAuth providers (one Google, one GitHub). Run `show-payload`. The payload contains `feature_flags.enable_web_ui = false`, `feature_flags.code_execution_enabled = true`, and `feature_flags.oauth_provider_types = ["google", "github"]` (deterministic order).

**Acceptance Scenarios**:

1. **Given** the config sets `code_execution_enabled: true`, **When** the payload is rendered, **Then** `feature_flags.code_execution_enabled = true`.
2. **Given** the config has no OAuth providers configured, **When** the payload is rendered, **Then** `feature_flags.oauth_provider_types = []`.
3. **Given** the config configures a custom OIDC provider, **When** the payload is rendered, **Then** the entry is reported as `generic` and no client ID, URL, or tenant is included.

---

### User Story 5 — Maintainers can see startup outcome distribution (Priority: P2)

A maintainer wants to know whether installs are starting cleanly or hitting port conflicts, locked databases, or config errors. After Tier 2, the heartbeat includes a `last_startup_outcome` enum recorded by the lifecycle code at the moment startup either succeeds or aborts.

**Why this priority**: Reliability is silent until something breaks. This signal turns silent failures into a measurable metric.

**Independent Test**: Start mcpproxy successfully and verify `last_startup_outcome = "success"` in `show-payload`. Stop, then start with a port already in use; verify `last_startup_outcome = "port_conflict"` is recorded for the next heartbeat.

**Acceptance Scenarios**:

1. **Given** mcpproxy starts cleanly, **When** the payload is rendered, **Then** `last_startup_outcome = "success"`.
2. **Given** mcpproxy fails to bind its listener because the port is in use, **When** the next process starts and writes the recorded outcome, **Then** `last_startup_outcome = "port_conflict"`.
3. **Given** a corrupt config file causes startup to abort, **When** the next process starts, **Then** `last_startup_outcome = "config_error"`.

---

### User Story 6 — Maintainers can see error category distribution (Priority: P2)

A maintainer wants to understand where errors cluster: OAuth refresh failures, upstream connection timeouts, Docker pull failures, tool quarantine blocks, etc. Tier 2 introduces an `error_category_counts` map keyed by a fixed enum of category codes. Code that hits these error paths increments the counter; the heartbeat reports the totals. Error messages are never recorded.

**Why this priority**: Same as startup outcomes — turns silent reliability into a measurable signal — but at finer granularity.

**Independent Test**: Force an OAuth refresh failure (e.g. expired token), then run `show-payload`. The payload contains `error_category_counts.oauth_refresh_failed >= 1` and no error messages anywhere.

**Acceptance Scenarios**:

1. **Given** an OAuth token refresh fails, **When** the payload is rendered, **Then** `error_category_counts.oauth_refresh_failed` is incremented.
2. **Given** code attempts to record an error category not in the fixed enum, **When** the recording function is called, **Then** the increment is silently dropped (the unknown category is not added to the map).
3. **Given** any error path is hit, **When** the payload is rendered, **Then** no human-readable error message string appears anywhere in the payload.

---

### User Story 7 — Maintainers can see version upgrade behavior (Priority: P3)

A maintainer wants to know whether users actually upgrade or sit on old versions. Tier 2 persists `last_reported_version` in the config file. Each heartbeat reports both `previous_version` (what was persisted) and `current_version` (the running binary). After a successful send, the persisted value is updated.

**Why this priority**: Useful for release-cadence and LTS-pocket detection but lower marginal value than the live-usage signals.

**Independent Test**: Start v0.21.0, observe a heartbeat with `previous_version = ""` (first run after install), `current_version = "v0.21.0"`. Upgrade to v0.22.0, restart, observe `previous_version = "v0.21.0"`, `current_version = "v0.22.0"`.

**Acceptance Scenarios**:

1. **Given** a fresh install with no `last_reported_version` in config, **When** the first heartbeat is rendered, **Then** `previous_version = ""` and `current_version = <build version>`.
2. **Given** a heartbeat that sent successfully, **When** the same process renders a second heartbeat 24 hours later, **Then** `previous_version = current_version` (because the persisted value was updated).
3. **Given** a heartbeat that failed to send, **When** the process renders the next heartbeat, **Then** `previous_version` is unchanged from the prior render.

---

### User Story 8 — Anonymous IDs rotate annually (Priority: P3)

A user wants reassurance that their anonymous ID does not create a permanent identifier. After Tier 2, the anonymous ID is regenerated automatically every 365 days. The previous ID is not preserved, so longitudinal correlation across years is impossible.

**Why this priority**: Directly addresses a privacy critique that maintainers have already heard. Low engineering cost.

**Independent Test**: Set `anonymous_id_created_at` in config to 366 days ago, run `mcpproxy telemetry show-payload`, observe that the anonymous ID has changed from its previous value.

**Acceptance Scenarios**:

1. **Given** `anonymous_id_created_at` is 366 days old, **When** a heartbeat is generated, **Then** a new UUID is generated and `anonymous_id_created_at` is updated to "now".
2. **Given** `anonymous_id_created_at` is 30 days old, **When** a heartbeat is generated, **Then** the existing UUID is preserved unchanged.
3. **Given** `anonymous_id_created_at` is missing from config (legacy install), **When** a heartbeat is generated, **Then** the field is initialized to "now" and the existing UUID is preserved.

---

### User Story 9 — Maintainers can see doctor check pass/fail rates (Priority: P3)

A maintainer wants to know which `mcpproxy doctor` checks fail in the wild. Tier 2 aggregates doctor results into a `doctor_checks` map (per check name: pass count + fail count) since the last flush. Doctor is invoked on demand, so this counter is sparse — many heartbeats will have an empty `doctor_checks` map.

**Why this priority**: Useful health signal, but doctor is rarely invoked, so the data volume is low. Lower priority than always-on counters.

**Independent Test**: Run `mcpproxy doctor` once. Run `show-payload`. The payload contains a `doctor_checks` map where each checked item has either a pass or fail count of 1.

**Acceptance Scenarios**:

1. **Given** doctor is never run since startup, **When** the payload is rendered, **Then** `doctor_checks = {}`.
2. **Given** doctor is run twice and both runs pass the `db_writable` check, **When** the payload is rendered, **Then** `doctor_checks.db_writable.pass = 2` and `doctor_checks.db_writable.fail = 0`.
3. **Given** the heartbeat flushes successfully, **When** the next render is requested, **Then** `doctor_checks` is reset to `{}`.

---

### User Story 10 — Privacy-conscious users have transparency tools (Priority: P3)

A privacy-conscious user wants to inspect what mcpproxy would send to the telemetry endpoint, and disable telemetry without editing config files manually. Tier 2 adds:
- `mcpproxy telemetry show-payload` — prints the exact JSON that would next be sent, without making any network call.
- `DO_NOT_TRACK=1` and `CI=true` environment variable handling — both fully disable telemetry without any config edit.
- A one-time first-run notice printed on stderr the first time `mcpproxy serve` runs without a `telemetry_notice_shown=true` flag in config.

**Why this priority**: High user-trust value, low engineering cost. Defuses backlash before it starts.

**Independent Test**: With `DO_NOT_TRACK=1` set, start `mcpproxy serve` with verbose logging — observe a log line indicating telemetry is disabled and verify no outbound HTTP request is made to the telemetry endpoint over 24 hours (or via mocked transport in tests). Then run `mcpproxy telemetry show-payload` and observe the JSON payload still renders (it does not require telemetry to be enabled).

**Acceptance Scenarios**:

1. **Given** `DO_NOT_TRACK=1` is set, **When** mcpproxy starts, **Then** the telemetry service does not start its heartbeat goroutine and no outbound POST is made.
2. **Given** `CI=true` is set with no other overrides, **When** mcpproxy starts, **Then** telemetry is disabled.
3. **Given** the precedence chain `DO_NOT_TRACK=1 CI=true MCPPROXY_TELEMETRY=true`, **When** mcpproxy starts, **Then** telemetry is disabled (DO_NOT_TRACK wins).
4. **Given** the config file has no `telemetry_notice_shown` field, **When** `mcpproxy serve` starts for the first time, **Then** a notice is printed to stderr and the field is persisted as `true`.
5. **Given** the config has `telemetry_notice_shown: true`, **When** `mcpproxy serve` starts, **Then** no notice is printed.
6. **Given** any state, **When** the user runs `mcpproxy telemetry show-payload`, **Then** the command prints pretty JSON to stdout and makes no network call.

---

### Edge Cases

- **Counter increment vs. flush race**: If a counter is being incremented while a flush is in progress, the implementation must use atomic operations or a lock so neither the increment nor the flush is lost. Tests verify this with `-race`.
- **Heartbeat send failure mid-flight**: If the network call fails after counters have been read but before the response confirms success, counters are NOT zeroed and `last_reported_version` is NOT updated. The next attempt will retry the same data.
- **Process killed before flush**: All in-memory counter state is lost. This is acceptable per the privacy constraint that we never persist counters to disk between restarts.
- **Clock skew**: If `anonymous_id_created_at` is in the future (clock rolled back), the rotation check treats it as "not yet expired" and does not regenerate.
- **Doctor check name with special characters**: The doctor check name is used as a map key; the implementation must ensure no user-supplied input ever becomes a check name. Check names are a fixed enum from `internal/doctor`.
- **Two mcpproxy processes running concurrently against the same config file**: Each process maintains its own in-memory counters; both will send their own heartbeats with the same anonymous ID. This is acceptable (rare and self-correcting).
- **REST endpoint with no Chi route match**: A request to `GET /not-a-real-path` returning 404 is recorded under the literal templated key `UNMATCHED` so as not to leak the path.
- **Telemetry disabled but `show-payload` is invoked**: The command must still render the payload locally without requiring telemetry to be enabled and without making any network call.

## Requirements *(mandatory)*

### Functional Requirements

#### Surface tracking (User Story 1)

- **FR-001**: System MUST classify every incoming request to the HTTP server as one of `mcp`, `cli`, `webui`, `tray`, or `unknown` based on the rule: `/mcp` paths are always `mcp`; otherwise, the value of the `X-MCPProxy-Client` header determines the surface; missing/unrecognized header is `unknown`.
- **FR-002**: System MUST maintain in-memory counters per surface and increment them on every classified request.
- **FR-003**: The CLI HTTP client used by `mcpproxy` Cobra subcommands MUST send `X-MCPProxy-Client: cli/<version>` on every request to the daemon.
- **FR-004**: The macOS tray's HTTP client MUST send `X-MCPProxy-Client: tray/<version>` on every request to the core.
- **FR-005**: The web UI's `fetch()` calls MUST include `X-MCPProxy-Client: webui/<version>` on every request.
- **FR-006**: The header parser MUST extract only the prefix before `/` (e.g., `tray`, `cli`, `webui`) and discard the version suffix when classifying. Unknown prefixes map to `unknown`.

#### Built-in tool histogram (User Story 2)

- **FR-007**: System MUST maintain an in-memory `map[string]int64` of built-in tool call counts, keyed by the built-in tool name from the fixed enum: `retrieve_tools`, `call_tool_read`, `call_tool_write`, `call_tool_destructive`, `upstream_servers`, `quarantine_security`, `code_execution`.
- **FR-008**: When an MCP tool call hits one of these built-in tools, the corresponding counter MUST be incremented exactly once per call regardless of success or failure.
- **FR-009**: When an MCP tool call hits a tool that is not in the built-in enum (i.e., an upstream-proxied tool), an `upstream_tool_call_count` integer MUST be incremented. The integer is bucketed at flush time into `upstream_tool_call_count_bucket` using buckets `"0"`, `"1-10"`, `"11-100"`, `"101-1000"`, `"1000+"`.
- **FR-010**: The names of upstream tools MUST NOT appear anywhere in the heartbeat payload.

#### REST endpoint histogram (User Story 3)

- **FR-011**: System MUST record every REST API request as an entry in a `map[string]map[string]int64` keyed by `"<METHOD> <chi-route-template>"` and then by status-code class (`"2xx"`, `"3xx"`, `"4xx"`, `"5xx"`).
- **FR-012**: The route template MUST be obtained from the Chi router context (e.g., `chi.RouteContext(r.Context()).RoutePattern()`); raw URLs MUST NOT be recorded.
- **FR-013**: Requests to paths that do not match any registered route MUST be recorded under the literal key `"UNMATCHED"` and the raw path MUST NOT be recorded.
- **FR-014**: Path-parameter values (e.g., a server name in `POST /api/v1/servers/{name}/enable`) MUST NOT appear anywhere in the payload.

#### Feature-flag matrix (User Story 4)

- **FR-015**: On heartbeat render, System MUST snapshot the current values of these config fields into a `feature_flags` object: `enable_web_ui`, `enable_socket`, `require_mcp_auth`, `enable_code_execution`, `quarantine_enabled`, `sensitive_data_detection.enabled`.
- **FR-016**: System MUST include `feature_flags.oauth_provider_types`: a sorted, deduplicated list of provider types (`google`, `github`, `microsoft`, `generic`) derived from the OAuth-configured upstream servers. No client IDs, URLs, or tenant identifiers are included. `generic` is the catch-all for non-recognized providers.

#### Startup outcome (User Story 5)

- **FR-017**: System MUST persist in the config file a field `last_startup_outcome` (string), one of: `success`, `port_conflict`, `db_locked`, `config_error`, `permission_error`, `other_error`, or empty.
- **FR-018**: After `mcpproxy serve` startup completes (either successfully or by aborting), System MUST write the corresponding outcome value to the config file. Mapping: clean start → `success`; exit code 2 → `port_conflict`; exit code 3 → `db_locked`; exit code 4 → `config_error`; exit code 5 → `permission_error`; any other failure → `other_error`.
- **FR-019**: On heartbeat render, System MUST include the persisted `last_startup_outcome` as a top-level field in the payload.

#### Error category counts (User Story 6)

- **FR-020**: System MUST expose a function `RecordError(category ErrorCategory)` where `ErrorCategory` is a defined Go enum. Initial enum values: `oauth_refresh_failed`, `oauth_token_expired`, `upstream_connect_timeout`, `upstream_connect_refused`, `upstream_handshake_failed`, `tool_quarantine_blocked`, `docker_pull_failed`, `docker_run_failed`, `index_rebuild_failed`, `config_reload_failed`, `socket_bind_failed`.
- **FR-021**: System MUST maintain an in-memory `map[ErrorCategory]int64` of counts and increment on each call to `RecordError`. Calls with categories not in the enum MUST be silently dropped (the unknown category is never added to the map).
- **FR-022**: On heartbeat render, the map MUST be serialized as `error_category_counts: {<enum_string>: <count>}`. Categories with zero counts MUST be omitted.
- **FR-023**: At least three existing error sites in the codebase (OAuth refresh, upstream connect timeout, tool quarantine block) MUST be wired to call `RecordError`.

#### Upgrade funnel (User Story 7)

- **FR-024**: System MUST persist in the config file a field `last_reported_version` (string).
- **FR-025**: On each heartbeat render, the payload MUST include `previous_version = <persisted value>` and `current_version = <build version>`.
- **FR-026**: On successful heartbeat send (HTTP 2xx response), the persisted `last_reported_version` MUST be updated to the current version.
- **FR-027**: On failed heartbeat send, the persisted `last_reported_version` MUST NOT be updated.

#### Anonymous-ID rotation (User Story 8)

- **FR-028**: System MUST persist in the config file a field `anonymous_id_created_at` (RFC3339 timestamp).
- **FR-029**: At each heartbeat render, if `anonymous_id_created_at` is older than 365 days, System MUST regenerate `anonymous_id` (new UUIDv4) and reset `anonymous_id_created_at` to now. The previous ID MUST NOT be retained.
- **FR-030**: For legacy installs that have an `anonymous_id` but no `anonymous_id_created_at`, System MUST initialize `anonymous_id_created_at` to "now" without rotating the ID.
- **FR-031**: If `anonymous_id_created_at` is in the future (clock skew), the rotation check MUST NOT fire (treat as not expired).

#### Doctor checks (User Story 9)

- **FR-032**: System MUST maintain an in-memory `map[string]struct{Pass, Fail int64}` keyed by doctor check name.
- **FR-033**: After each `mcpproxy doctor` invocation, System MUST aggregate the structured check results into the map (incrementing `Pass` or `Fail` per check).
- **FR-034**: On heartbeat render, the map MUST be serialized as `doctor_checks: {<check_name>: {"pass": N, "fail": M}}`.
- **FR-035**: After successful flush, the doctor check map MUST be zeroed.

#### Privacy controls (User Story 10)

- **FR-036**: System MUST disable telemetry entirely if `DO_NOT_TRACK` env var is set to any non-empty value (`1`, `true`, `yes` all count). No heartbeat goroutine, no outbound network calls.
- **FR-037**: System MUST disable telemetry entirely if `CI` env var is set to `true` or `1`.
- **FR-038**: Precedence for the enabled/disabled decision MUST be: `DO_NOT_TRACK` set → disabled; else `CI=true` → disabled; else `MCPPROXY_TELEMETRY=false` → disabled; else config file `telemetry.enabled = false` → disabled; else default enabled.
- **FR-039**: System MUST add a Cobra subcommand `mcpproxy telemetry show-payload` that renders and prints the current payload as pretty JSON to stdout, without any network call. The command MUST work even when telemetry is disabled.
- **FR-040**: On the first `mcpproxy serve` run after install (detected by absence of a `telemetry_notice_shown` field set to `true` in config), System MUST print a one-time notice to stderr explaining that anonymous telemetry is enabled, listing the privacy doc URL, and showing the disable command. After printing, System MUST persist `telemetry_notice_shown=true` in the config file. Subsequent runs MUST NOT print the notice.

#### General

- **FR-041**: All counter mutations and the flush operation MUST be safe under concurrent access; race detector tests MUST pass.
- **FR-042**: The heartbeat goroutine MUST start asynchronously after process startup completes and MUST NOT block startup.
- **FR-043**: All counter resets MUST occur only after a successful HTTP 2xx response from the telemetry endpoint. On failure, counters are preserved for the next attempt.
- **FR-044**: The personal edition (`go build ./cmd/mcpproxy`) and server edition (`go build -tags server ./cmd/mcpproxy`) MUST both build cleanly with all changes.

### Key Entities

- **HeartbeatPayloadV2**: extends the existing v1 payload with these new fields: `surface_requests`, `builtin_tool_calls`, `upstream_tool_call_count_bucket`, `rest_endpoint_calls`, `feature_flags`, `last_startup_outcome`, `previous_version`, `error_category_counts`, `doctor_checks`, `anonymous_id_created_at`. The existing v1 fields (anonymous_id, version, edition, os, arch, go_version, server_count, connected_server_count, tool_count, uptime_hours, routing_mode, quarantine_enabled, timestamp) are preserved.
- **CounterRegistry**: in-memory aggregate of all counter maps and atomics. Has `Snapshot()` (build payload), `Reset()` (zero all counters), and `RecordX()` methods for each counter category. Single instance owned by the telemetry service. Thread-safe.
- **ErrorCategory**: Go string type with constants enumerating valid categories. Only values in the enum may be recorded.
- **TelemetryConfig**: extended config struct with new persisted fields: `anonymous_id_created_at`, `last_reported_version`, `last_startup_outcome`, `telemetry_notice_shown`. Existing fields (`enabled`, `endpoint`, `anonymous_id`) preserved.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A maintainer can answer "what fraction of weekly active installs used the macOS tray surface this week?" using only telemetry backend queries. (Measured: query the surface_requests counter in heartbeats sent in the past 7 days.)
- **SC-002**: A maintainer can produce a top-10 list of REST API endpoints by request volume from the past 30 days of heartbeats, with all paths shown as Chi templates and zero raw URLs. (Measured: backend query.)
- **SC-003**: A user can verify exactly what telemetry data their install would send by running one command with no network access. (Measured: `mcpproxy telemetry show-payload` succeeds offline and prints valid JSON.)
- **SC-004**: A user can disable telemetry with zero config edits using a single environment variable. (Measured: `DO_NOT_TRACK=1 mcpproxy serve` produces zero outbound HTTPS connections to the telemetry endpoint over a 25-hour observation window.)
- **SC-005**: A privacy auditor inspecting any heartbeat payload can confirm the absence of: server names, upstream tool names, file paths, hostnames, IP addresses, error messages, command-line arguments, environment variable values, and any user-provided strings. (Measured: documented test fixture passes a string-search assertion against the rendered payload using a list of forbidden substrings.)
- **SC-006**: The total byte size of a fully populated heartbeat payload (every counter bucket non-zero, every flag set, every doctor check present) is under 8 KB. (Measured: serialized JSON length test.)
- **SC-007**: A new install hitting `mcpproxy serve` for the first time sees the telemetry notice on stderr exactly once across multiple subsequent runs. (Measured: integration test that runs `serve --once` twice and asserts notice appears in run 1 but not run 2.)
- **SC-008**: After a successful heartbeat send, all in-memory counters are zero (verified by running `show-payload` immediately after a simulated successful flush).
- **SC-009**: All existing telemetry tests continue to pass. The new test suite has zero `-race` violations.
- **SC-010**: `go build ./cmd/mcpproxy` and `go build -tags server ./cmd/mcpproxy` both succeed with no errors or warnings.

## Assumptions

These are reasonable defaults chosen to avoid blocking on clarification. Each can be revisited.

1. **Header convention is `<surface>/<version>` not just `<surface>`** because some downstream telemetry analysis may want to correlate by client version. Only the `<surface>` prefix is used for surface classification; the version is recorded only as part of the existing `version` heartbeat field, not duplicated per request.
2. **CLI `cli/<version>` value is set by the CLI HTTP client wrapper** and applies to every Cobra subcommand that talks to the daemon. There is no way for a user-supplied script to spoof the surface other than setting the header manually, which is acceptable for anonymous aggregates.
3. **Web UI version is the build version of mcpproxy** (the same version embedded at build time), since the web UI is shipped from the same binary.
4. **Doctor check names are a fixed enum** sourced from `internal/doctor`, never user-supplied. If new doctor checks are added later, their names automatically appear in the next heartbeat without needing telemetry-spec updates.
5. **Bucket boundaries for `upstream_tool_call_count_bucket` are `0`, `1-10`, `11-100`, `101-1000`, `1000+`**. These are log-scale buckets following Homebrew's convention, sufficient to distinguish casual from heavy users without revealing exact counts.
6. **First-run notice text** is a fixed string defined in code, contains a link to `https://mcpproxy.app/telemetry` (to be updated when the docs page exists), and prints to stderr (not stdout) so it does not interfere with `serve` log output piped to other tools.
7. **`telemetry_notice_shown` flag lives in the existing `telemetry` config struct**, not in a new top-level config field, to keep all telemetry-related state in one place.
8. **`last_reported_version` is updated only after a 2xx HTTP response**, not after the request is sent. This ensures that intermittent send failures do not silently lose the upgrade signal.
9. **Annual ID rotation timestamp is checked at heartbeat render time, not lazily**, so that the rotation happens predictably once per year without requiring a separate timer goroutine.
10. **Doctor check counters reset on flush**, not on doctor invocation. This means that if doctor is run twice and the heartbeat sends in between, the first run's data has already been flushed and only the second run's data is in the next payload.
11. **REST endpoint `UNMATCHED` fallback** is a single bucket with no further classification, so we cannot tell whether a 404 was for `/foo` or `/bar`. This is acceptable; raw paths leak.
12. **Status code class `"1xx"` is omitted** because mcpproxy does not currently produce 1xx responses; the four buckets (`2xx`, `3xx`, `4xx`, `5xx`) cover the API surface.
13. **Existing v1 heartbeat fields are preserved unchanged**. Tier 2 is purely additive at the JSON schema level. The receiver at telemetry.mcpproxy.app will need a separate update to ingest the new fields, but is out of scope for this spec.
14. **Error category enum is extensible**: adding a new constant in code does not require a spec change. The enum starts with the eleven categories in FR-020 and can grow as call sites are wired up.
15. **Native macOS tray Swift code** *is* in scope for setting the `X-MCPProxy-Client: tray/<version>` header on its REST API requests, since the tray is mcpproxy-owned code shipped from this repo. Tests for the Swift change are limited to verifying the header is sent (no Swift unit test framework expansion).

## Out of Scope

- Backend ingester changes at telemetry.mcpproxy.app (server-side schema migration is a separate effort).
- Activity-log integration (telemetry and activity log remain separate systems with different privacy guarantees).
- Persisting in-memory counters to disk between restarts (acceptable trade-off for privacy).
- Tier 3 signals: install→retention buckets, time-to-first-tool-call, upstream transport mix breakdown, full OAuth provider distribution beyond simple type list.
- Any change to the existing `mcpproxy telemetry status / enable / disable` subcommands beyond adding `show-payload`.
- Frontend telemetry sent directly from the web UI to a third-party analytics endpoint. The web UI's only telemetry contribution is the `X-MCPProxy-Client` header on its existing API requests.

## Commit Message Conventions *(mandatory)*

When committing changes for this feature, follow these guidelines:

### Issue References
- Use `Related #042` to link without auto-closing.

### Co-Authorship
- Do NOT include `Co-Authored-By: Claude <noreply@anthropic.com>`.
- Do NOT include "Generated with Claude Code".

### Example Commit Message

```
feat(042): add surface tracking middleware for telemetry

Related #042

Adds X-MCPProxy-Client header parsing to the REST API middleware.
Surface counts are aggregated in CounterRegistry and flushed to the
daily heartbeat. Built-in tool counters and REST endpoint histogram
share the same registry.

## Changes
- internal/telemetry/registry.go: new CounterRegistry with atomic counters
- internal/httpapi/middleware.go: surface classification middleware
- internal/telemetry/telemetry.go: HeartbeatPayloadV2 fields wired

## Testing
- internal/telemetry/registry_test.go: race-safe counter unit tests
- internal/httpapi/middleware_test.go: header classification cases
```
