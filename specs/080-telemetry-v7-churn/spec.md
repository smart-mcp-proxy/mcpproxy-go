# Feature Specification: Telemetry v7 — honest activation funnel + churn instrumentation

**Feature Branch**: `080-telemetry-v7-churn`
**Created**: 2026-07-06
**Status**: Draft
**Input**: User description: "Our activation funnel is measured with a lying wizard metric and we cannot see churn at all. Fix the wizard connect-step telemetry so external connections stop being counted as skips, add the observability the funnel analysis showed is missing (wizard shown, Web UI opened, install age, rolling active days), and make the last heartbeat before an install goes silent carry a cause-of-death snapshot (clean vs crash shutdown, last error code). Bump the heartbeat schema to v7 and document it. Keep every existing privacy invariant: no PII, no per-server identity, counters not timelines, opt-out preserved."

## Overview

A funnel analysis of live telemetry (measured 2026-07-06, 60-day human cohort, n=1082 — CI/cloud-IDE/container environments and `-dev` builds excluded) shows that MCPProxy does not have a connection problem; it has a **return** problem. **96.4%** of installs get an MCP client to actually handshake (`activation.first_mcp_client_ever`), **41.7%** issue a first `retrieve_tools` call — but only **17.7%** ever return on a second distinct day, only **16.4%** make a real upstream tool call, and only **9.1%** are still active after 14 days. Across all time (n=1459), **75.5%** of installs have exactly one day of heartbeats ever (Linux 87.4%, macOS 35.5%, Windows 60.9%). The cliff is day-2 return, not first connection.

Worse, the metric the team was steering by is wrong. The long-standing claim that "72% skip the connect step" is **debunked**: the wizard's `dismiss()` handler (`frontend/src/components/OnboardingWizard.vue:1258-1275`) stamps `connect_step_status='skipped'` on any step the user never advanced through inside the wizard — even when the user already connected via `ConnectModal.vue`, the CLI (`cmd/mcpproxy/connect_cmd.go`), or manual config edits, none of which touch `OnboardingState` (`internal/storage/models.go:52`). Cross-checking the data: connect-step "skippers" whose `activation.first_mcp_client_ever` is false number **exactly zero**. The genuine never-connected skip rate is **0%**. The metric measured wizard usage, not connection failure.

The telemetry client already carries substantial churn-relevant signal: `error_category_counts` (11 fixed categories, `internal/telemetry/error_categories.go`), `diagnostics.error_code_counts_24h` with stable `MCPX_*` codes, `last_startup_outcome` (`internal/telemetry/telemetry.go:85`), `doctor_checks`, and the `server_count` vs `connected_server_count` gap. But it has four blind spots that make the day-2 cliff undiagnosable: (1) `wizard_engaged` is only ever `1` or absent (`internal/telemetry/telemetry.go`, `WizardEngaged bool` with `omitempty`), so "wizard shown but ignored" is unobservable even though storage records `FirstShownAt` (`internal/storage/models.go:59`); (2) "Web UI opened" is only inferable from `surface_requests.webui` set by SPA API calls carrying the `X-MCPProxy-Client` header (`internal/httpapi/middleware.go:18`) — there is no counter on actually serving the embedded UI; (3) there is no install-age or rolling-activity field, so retention must be reconstructed server-side across churning `anonymous_id`s (composite-identity uncertainty is ±37%: 1459 ids vs 2007 composite identities); (4) all error counts are 24h-windowed with no shutdown/session-end signal, so the final heartbeat before an install goes silent says nothing about *why* it died.

This feature makes the activation funnel **honest, observable, self-contained, and churn-diagnostic**. It corrects the wizard connect-step semantics with a new `completed_external` status, adds four additive heartbeat fields (`wizard_shown`, `web_ui_opened`, `days_since_install`, `active_days_30d`), adds a pre-churn snapshot (`previous_shutdown`, `last_error_code`) so the last heartbeat before silence doubles as a cause-of-death record, and bumps the payload schema to v7 with documentation. It does NOT build the server-side churn pipeline (worker `churn_events` materialization and the dashboard Churn page are cross-repo dependencies, see Out of Scope), does NOT add any timeline, per-server, or free-text data to the payload, and does NOT change when or whether heartbeats are sent (opt-out and the opt-out beacon are untouched).

### Context (current behavior, verified)

- **The wizard skip metric lies by construction.** `dismiss()` in `frontend/src/components/OnboardingWizard.vue:1258-1275` marks every untouched step `skipped`; connections made outside the wizard (ConnectModal, `mcpproxy connect`, manual config) never write `OnboardingState`, so externally-connected users are indistinguishable from genuine skippers. Measured: 0 skippers with `first_mcp_client_ever=false`.
- **"Shown but not engaged" is unobservable in telemetry.** Storage distinguishes it (`FirstShownAt` set, `Engaged=false`, `internal/storage/models.go:59`), but the heartbeat only carries `WizardEngaged bool` with `omitempty` — the field is `1` or absent, never `0`.
- **Web UI opens are only proxied by API traffic.** `surface_requests.webui` (v5+ payloads) increments on SPA API calls via the `X-MCPProxy-Client` header (`internal/httpapi/middleware.go:18-25`); serving `index.html` itself is uncounted, so a user who opens the UI and bounces before any API call is invisible.
- **Schema is at v6 with machine_id defined but unreleased.** `SchemaVersion = 6` (`internal/telemetry/telemetry.go:48`); `machine_id` (`internal/telemetry/machine_id.go`, HMAC-SHA256, non-reversible) has 0 rows in production. Identity churn makes server-side retention joins ±37% uncertain — which is why the client should self-report install age and rolling activity.
- **A startup-outcome signal already exists; a shutdown-outcome signal does not.** `last_startup_outcome` is populated from config at heartbeat build (`internal/telemetry/telemetry.go:581`); nothing records whether the *previous* process exited cleanly or crashed.
- **Privacy posture (must be preserved).** Payload is anonymous, fixed-enum, counter-based: no server names/URLs, no user strings, no timelines; the anonymity scanner (`internal/telemetry/anonymity.go`, `payload_privacy_test.go`) asserts this on the serialized form; opt-out suppresses the whole heartbeat.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Wizard connect step tells the truth about external connections (Priority: P1)

A user installs MCPProxy, connects Claude Code via `mcpproxy connect` (or the ConnectModal, or by editing the client config by hand), then later opens the Web UI, sees the onboarding wizard, and dismisses it because they are already set up. Today their telemetry says they *skipped* connecting. After this feature, dismissing the wizard checks whether the install is in fact connected — and records the connect step as `completed_external`, so the funnel separates "connected outside the wizard" from "genuinely never connected".

**Why this priority**: This is the correction of the metric the product roadmap was being steered by. Every downstream funnel number (and the ux-audit epic's framing) depends on the connect step being honest; US2–US3 add new signal, but this story stops an existing signal from lying. It has no dependency on the other stories.

**Independent Test**: On a fresh install, connect a client via CLI only (never touching the wizard's connect step), open the Web UI, dismiss the wizard, and assert the persisted `OnboardingState.ConnectStepStatus` is `completed_external` and the next heartbeat's `wizard_connect_step` is `completed_external`. Negative case: on a fresh install with zero connected clients and `first_mcp_client_ever=false`, dismiss the wizard and assert the status remains `skipped`.

**Acceptance Scenarios**:

1. **Given** an install where a client was connected via `mcpproxy connect` and the wizard's connect step was never advanced, **When** the user dismisses the wizard, **Then** the connect step is recorded as `completed_external` (not `skipped`), and the next heartbeat carries `wizard_connect_step: "completed_external"`.
2. **Given** an install with no client config entries but where an MCP client has already handshaked (`activation.first_mcp_client_ever` is true, e.g. manual config pointing at the proxy), **When** the user dismisses the wizard with the connect step untouched, **Then** the connect step is recorded as `completed_external`.
3. **Given** a fresh install with `connected_client_count == 0` and `first_mcp_client_ever == false`, **When** the user dismisses the wizard with the connect step untouched, **Then** the connect step is recorded as `skipped` exactly as today.
4. **Given** a user who completed the connect step *inside* the wizard, **When** the wizard is dismissed, **Then** the status remains `completed`; **When** the user later disconnects all clients, **Then** the recorded status does not regress.

---

### User Story 2 - The funnel's blind spots become observable (Priority: P1)

A maintainer reading the telemetry dashboard can now answer four questions that are unanswerable today: Was the wizard ever shown to installs that never engaged with it? Did the user ever actually open the Web UI (not just have the SPA fire API calls)? How old is each install, in days? How many distinct days in the last 30 was this install actually running? The last two make day-N retention computable from a single heartbeat, without cross-heartbeat identity joins that the ±37% id-churn uncertainty poisons.

**Why this priority**: The verified analysis shows the real cliff is day-2 return (17.7%) and the retrieve_tools→real-call gap (41.7% → 16.4%). Choosing between hypothesis H2 (no daily habit) and H1/H4 (breakage) requires exactly these fields. Shares P1 with US1 because both are pure measurement fixes that everything downstream (churn worker, dashboard) consumes.

**Independent Test**: Run a proxy instance, render the wizard once without engaging, serve the embedded Web UI index twice, and inspect the heartbeat payload (e.g. via `mcpproxy telemetry show-payload`): `wizard_shown` is true while `wizard_engaged` is absent/false, `web_ui_opened >= 2`, `days_since_install == 0`, `active_days_30d == 1`. Negative case: on an install where the wizard never rendered and the UI was never served, `wizard_shown` is false/absent and `web_ui_opened` is absent.

**Acceptance Scenarios**:

1. **Given** an install where the wizard rendered at least once (`FirstShownAt` set) but the user never completed or skipped it, **When** the next heartbeat is built, **Then** it carries `wizard_shown: true` with `wizard_engaged` absent/false — the previously unobservable "shown but ignored" state.
2. **Given** a running proxy, **When** the embedded Web UI index page is served N times, **Then** the lifetime `web_ui_opened` counter reflects those serves independently of any SPA API traffic; **When** the UI is opened and closed before any API call fires, **Then** the open is still counted.
3. **Given** an install first seen K days ago (per a persisted first-install stamp, not the churnable `anonymous_id_created_at`), **When** a heartbeat is built, **Then** `days_since_install == K` as a plain integer day count with no timestamp transmitted.
4. **Given** an install that ran on 3 distinct UTC days within the trailing 30 days, **When** a heartbeat is built, **Then** `active_days_30d == 3`; **When** a fourth run happens 31+ days after the first, **Then** the first day ages out of the count.
5. **Given** telemetry is disabled (config, env, or CLI opt-out), **When** any of these events occur, **Then** no heartbeat is sent — local persistence MAY still occur, but nothing is transmitted (existing opt-out semantics unchanged).

---

### User Story 3 - The last heartbeat doubles as a cause-of-death snapshot (Priority: P2)

An install goes silent — the 75.5% one-day-ever majority. Today the final heartbeat says nothing about why. After this feature, every heartbeat carries `previous_shutdown` ("clean" | "crash" | "unknown") — derived from a persisted flag written on graceful shutdown and read at next start — and `last_error_code`, the most recent stable `MCPX_*` diagnostic code observed. When the churn pipeline (cross-repo) later identifies a churned install, its final heartbeat already distinguishes "crashed and never came back" (H1/H4 breakage) from "exited cleanly and never returned" (H2 no habit).

**Why this priority**: P2 because it is the churn-*diagnosis* layer on top of the churn-*measurement* layer (US2). It is independently testable and shippable, but its analytical value is realized only once the funnel fields land and the cross-repo worker consumes them.

**Independent Test**: Start the proxy, stop it gracefully (SIGTERM), restart, and assert the heartbeat carries `previous_shutdown: "clean"`. Then start the proxy, kill it hard (SIGKILL), restart, and assert `previous_shutdown: "crash"`. Trigger a categorized error (e.g. an upstream connect refused) and assert `last_error_code` carries the corresponding `MCPX_*` code and nothing else (no message text, no server name).

**Acceptance Scenarios**:

1. **Given** the previous process instance shut down gracefully (signal-handled shutdown path completed), **When** the next instance builds a heartbeat, **Then** it carries `previous_shutdown: "clean"`.
2. **Given** the previous instance was killed without running the shutdown path (SIGKILL, panic, power loss), **When** the next instance builds a heartbeat, **Then** it carries `previous_shutdown: "crash"`.
3. **Given** a first-ever run (no prior-instance marker exists), **When** the heartbeat is built, **Then** `previous_shutdown` is `"unknown"` or absent — a fresh install is never misreported as a crash.
4. **Given** diagnostics recorded `MCPX_*` codes during the session, **When** a heartbeat is built, **Then** `last_error_code` is the most recently observed stable code drawn only from the existing fixed code set; **Given** no error was ever recorded, **Then** the field is absent.

---

### User Story 4 - Schema v7 is bumped, documented, and backward-compatible (Priority: P3)

A telemetry consumer (ingest worker, dashboard, or a privacy-conscious user reading `mcpproxy telemetry show-payload`) sees `schema_version: 7` and can look up exactly which fields v7 added, their enums, and their privacy rationale in `docs/features/telemetry.md` — following the same additive-versioning discipline as v3–v6 (`internal/telemetry/telemetry.go:23-48`).

**Why this priority**: P3 hardening/contract work — it has no user-visible behavior of its own, but the version bump and docs are the contract that lets the cross-repo worker and dashboard adopt the new fields safely, and lets v6-and-earlier consumers keep ignoring them.

**Independent Test**: Assert `SchemaVersion == 7` and that a serialized payload from an install exercising all new fields round-trips through the existing anonymity scanner with zero violations. Verify `docs/features/telemetry.md` documents every new field, its type, its enum values, and its retention/privacy note. Negative case: a payload with all new fields empty/zero serializes without the fields (omitempty discipline), i.e. is shape-compatible with a v6 payload except for the version number.

**Acceptance Scenarios**:

1. **Given** the v7 client, **When** any heartbeat is built, **Then** `schema_version` is 7 and every pre-v7 field is unchanged in name, type, and semantics.
2. **Given** a v6-or-earlier consumer (the ingest worker stores `payload_json` wholesale and ignores unknown fields), **When** it receives a v7 payload, **Then** ingestion succeeds with no rejects.
3. **Given** the serialized v7 payload with all new fields populated, **When** the anonymity scanner runs, **Then** it passes: new fields are only booleans, non-negative integers, or values from documented fixed enums.

---

### Edge Cases

- **Wizard dismissed while the connect check cannot be evaluated** (e.g. client-detection service errors): → fall back to today's behavior (`skipped`); never block or delay dismissal on the check, and never guess `completed_external` without positive evidence.
- **`completed_external` reaches an old consumer**: `wizard_connect_step` is already a string enum; consumers that switch on `completed|skipped` must treat unknown values as "other/engaged", and `docs/features/telemetry.md` MUST call out the widened enum.
- **Status already recorded before this feature ships** (historical `skipped` rows): → never rewritten retroactively; the correction applies from upgrade onward. Dashboards segment by version/schema.
- **Clock skew or backwards clock for `days_since_install` / `active_days_30d`**: → values clamp at 0 and the rolling window tolerates out-of-order days (a day is a set member, not a delta); a negative day count is never transmitted.
- **30-day activity storage**: persisted as a compact per-day structure (e.g. bitmap keyed by day ordinal) in BBolt; the *payload* carries only the integer count — transmitting the bitmap or any per-day breakdown would violate the counters-not-timelines invariant.
- **Crash flag vs multiple instances**: the DB lock already enforces a single core instance per data dir (exit code 3), so the clean-shutdown marker has a single writer; a second instance failing to acquire the lock MUST NOT clobber the marker.
- **`web_ui_opened` inflation** (health checkers, prefetchers repeatedly fetching `/`): acceptable — it is a coarse lifetime counter, not a session metric; the field is documented as "index serves", and only serves of the embedded UI entrypoint count (asset and API requests do not).
- **Fresh install crash-loop**: if the very first run crashes before ever writing state, the next run reports `previous_shutdown: "crash"` only if the prior run got far enough to arm the marker; arming MUST happen early in startup so crash loops are visible.
- **Opt-out mid-lifecycle**: disabling telemetry stops all transmission immediately (existing gate); local counters/markers MAY continue to persist so that re-enabling does not fabricate a fresh-install picture. The existing single opt-out beacon behavior is unchanged.

## Requirements *(mandatory)*

### Functional Requirements

**Honest wizard connect metric (US1)**

- **FR-001**: The onboarding connect-step status enum MUST be extended with a fourth value, `completed_external`, alongside the existing `""`, `completed`, `skipped` (`internal/storage/models.go:52`, `OnboardingState.ConnectStepStatus`).
- **FR-002**: When the wizard is dismissed with the connect step untouched, the system MUST record the connect step as `completed_external` instead of `skipped` if, at dismissal time, at least one supported client is connected (`connected_client_count > 0`) OR an MCP client has ever handshaked (`activation.first_mcp_client_ever` is true).
- **FR-002a**: If neither condition can be positively established (evaluation error, data unavailable), the system MUST fall back to `skipped` and MUST NOT block or delay wizard dismissal.
- **FR-003**: The heartbeat field `wizard_connect_step` MUST surface `completed_external` as a distinct enum value; already-persisted statuses MUST NOT be rewritten retroactively.
- **FR-004**: A status of `completed` or `completed_external`, once recorded, MUST NOT regress if clients are later disconnected (matching the existing non-regression posture of `WizardEngaged`).

**Funnel observability (US2)**

- **FR-005**: The heartbeat MUST carry a `wizard_shown` boolean, true once the wizard has rendered at least once for this install (derivable from the existing `OnboardingState.FirstShownAt`, `internal/storage/models.go:59`), making "shown but not engaged" (`wizard_shown=true`, `wizard_engaged` false/absent) observable.
- **FR-006**: The system MUST maintain a persistent lifetime counter, surfaced as `web_ui_opened`, incremented when the embedded Web UI entrypoint (index document) is actually served — independent of the `X-MCPProxy-Client`-header-based `surface_requests.webui` counting (`internal/httpapi/middleware.go:18`), which remains unchanged. Asset, API, and non-UI requests MUST NOT increment it.
- **FR-007**: The system MUST persist a first-install day stamp (independent of `anonymous_id`, which churns in ephemeral environments) and surface `days_since_install` as a non-negative integer count of whole days. No install timestamp is added to the payload.
- **FR-008**: The system MUST track the set of distinct UTC days with process activity within a trailing 30-day window (compact per-day persistence, e.g. bitmap) and surface only the integer count as `active_days_30d` (1–30). The per-day structure MUST NOT be transmitted.
- **FR-009**: All new fields MUST use `omitempty`-style serialization so zero-valued payloads remain shape-compatible with v6, and MUST populate from BBolt-backed state with the same nil-safety as `Activation` (omitted when the store is not wired, e.g. short-lived CLI commands).

**Pre-churn snapshot (US3)**

- **FR-010**: The system MUST persist a shutdown marker: armed early during startup, resolved to "clean" by the graceful-shutdown path. At next startup, the prior marker's state determines `previous_shutdown`: `clean` (marker resolved), `crash` (marker armed but unresolved), or `unknown`/absent (no prior marker, e.g. first run).
- **FR-011**: Every heartbeat MUST carry `previous_shutdown` for the *previous* process instance; the value MUST remain stable across all heartbeats of the current instance.
- **FR-012**: The system MUST surface `last_error_code`: the most recently observed stable `MCPX_*` diagnostic code (same fixed code set as `diagnostics.error_code_counts_24h`), persisted across restarts so the post-crash heartbeat can carry the pre-crash code. Only the enum code is stored and transmitted — never message text, stack traces, server names, or paths. Absent when no error was ever recorded.
- **FR-013**: A first-ever run MUST NOT be reported as a crash (FR-010's `unknown` case), and a second process instance that fails to acquire the DB lock MUST NOT overwrite the running instance's shutdown marker.

**Schema v7, privacy & compatibility (US4)**

- **FR-014**: `SchemaVersion` (`internal/telemetry/telemetry.go:48`) MUST be bumped to 7, with the version-history comment extended describing every v7 addition, following the v3–v6 additive pattern.
- **FR-015**: All v7 additions MUST be additive and forward-compatible: no pre-v7 field changes name, type, or semantics; v6-and-earlier consumers ingest v7 payloads unmodified.
- **FR-016**: All new fields MUST satisfy the existing anonymity posture — booleans, non-negative integers, or documented fixed enums only; no timestamps, no per-server identity, no free text — and the anonymity/privacy scanner assertions MUST be extended to cover them.
- **FR-017**: All new signals MUST ride the existing opt-out gate: when telemetry is disabled (config, `MCPPROXY_TELEMETRY=false`, or CLI), nothing is transmitted; the existing opt-out beacon behavior is unchanged.
- **FR-018**: `docs/features/telemetry.md` MUST document every v7 field: name, type, enum values, when it is set, and its privacy rationale, including the widened `wizard_connect_step` enum and guidance for consumers on unknown enum values.
- **FR-019**: `mcpproxy telemetry show-payload` MUST render the new fields so users can inspect exactly what would be sent.
- **FR-020**: All new behavior MUST be covered by automated tests, including: the `completed_external` decision matrix (connected / handshaked-only / neither / evaluation-error fallback), non-regression of recorded statuses, `wizard_shown` independent of engagement, `web_ui_opened` counting index serves but not asset/API requests, `days_since_install` clamping and day-boundary math, `active_days_30d` window aging and out-of-order days, clean vs crash vs first-run shutdown detection, `last_error_code` enum-only persistence across restart, schema-version bump, omitempty shape-compatibility, and anonymity-scanner passes on fully-populated payloads.

### Key Entities *(include if feature involves data)*

- **OnboardingState (extended)**: existing BBolt record (`internal/storage/models.go:52`); `ConnectStepStatus` enum widened to `"" | completed | completed_external | skipped`. No new record type.
- **First-install stamp**: a persisted day-granularity marker of first run for this data dir, independent of `anonymous_id`; source for `days_since_install`.
- **Activity-day window**: compact persisted set of distinct active UTC days over a trailing 30-day window; only its cardinality (`active_days_30d`) leaves the machine.
- **Shutdown marker**: persisted armed/resolved flag pair encoding whether the previous instance exited via the graceful path; source for `previous_shutdown`.
- **Last-error record**: single persisted `MCPX_*` enum code (most recent), extending the existing diagnostics-counter contract without adding new code values.
- **HeartbeatPayload v7 (extended contract)**: existing payload (`internal/telemetry/telemetry.go:52`) plus `wizard_shown`, `web_ui_opened`, `days_since_install`, `active_days_30d`, `previous_shutdown`, `last_error_code`, and the widened `wizard_connect_step` enum; `schema_version: 7`.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: The reported wizard connect-step "skipped" rate drops from the inflated legacy figure toward the genuine never-connected rate — measured **0%** in the 2026-07-06 cross-check (0 skippers with `first_mcp_client_ever=false`, 60d cohort n=1082) — because externally-connected dismissals report `completed_external`; the split is visible in the dashboard within one release cycle.
- **SC-002**: Day-2 return (baseline **17.7%**, 2026-07-06, 60d human cohort) and 14-day retention (baseline **9.1%**) become computable from a *single* heartbeat via `days_since_install` + `active_days_30d`, eliminating the ±37% identity-join uncertainty (1459 anonymous ids vs 2007 composite identities) for retention questions.
- **SC-003**: For installs that go silent after upgrade to v7, 100% of their final heartbeats carry a `previous_shutdown` value and (when any error occurred) a `last_error_code`, enabling the H1/H4 (breakage) vs H2 (no habit) hypothesis split on the one-day-ever majority (**75.5%** all-time baseline, n=1459).
- **SC-004**: 100% of wizard dismissals with an already-connected install record `completed_external`, and 0 dismissals with no connection evidence record anything other than `skipped`, verified by test.
- **SC-005**: `wizard_shown` is true with `wizard_engaged` false/absent for a shown-but-ignored install, and `web_ui_opened` counts index serves while asset/API requests count 0, verified by test.
- **SC-006**: Clean shutdown → `"clean"`, hard kill → `"crash"`, first run → `"unknown"`/absent, in 100% of restart sequences, verified by test.
- **SC-007**: A fully-populated v7 payload passes the anonymity scanner with zero violations, and no new field ever contains a timestamp, per-server identity, or free text, verified by test.
- **SC-008**: All pre-v7 payload fields are byte-identical in name, type, and semantics; existing telemetry tests (`payload_v2_test.go`, `payload_privacy_test.go`, `payload_phase_h_test.go`) continue to pass, and a v7 payload with all new fields zero serializes shape-compatible with v6 except `schema_version`.

## Assumptions

- The 2026-07-06 funnel measurements (Cloudflare D1, human filter: `env_kind` not in ci/ci_inferred/cloud_ide*/container and released `v%` versions) are the authoritative baselines for SC-001–SC-003.
- The ingest worker stores `payload_json` wholesale and never rejects unknown fields or higher schema versions (established for v6/machine_id), so v7 can ship client-first with worker/dash adoption trailing.
- `activation.first_mcp_client_ever` and `connected_client_count` are already computed at heartbeat/wizard-dismiss time (Spec 044/046 infrastructure) and are acceptable evidence of an external connection; no new client-detection mechanism is needed.
- The single-writer guarantee for BBolt (DB lock, exit code 3 on contention) makes a simple armed/resolved shutdown marker sound without cross-process coordination.
- Day-granularity (UTC) is sufficient for retention analytics; sub-day session tracking is intentionally not pursued (counters, not timelines).
- `machine_id` (schema v6) shipping end-to-end is a separate, already-tracked effort (roadmap epic `telemetry-identity`); this spec's `days_since_install`/`active_days_30d` deliberately work *without* stable cross-heartbeat identity.

## Out of Scope

- **Cross-repo: churn-event materialization** — the `mcpproxy-telemetry` worker's nightly job producing `churn_events` rows (identity = `machine_id` fallback `anonymous_id`; criteria: active ≥2 days then silent >14d; capturing last_version, days_active, wizard state, clients_seen, last error snapshot, crash flag) is a documented downstream dependency, not an FR of this spec.
- **Cross-repo: dashboard Churn page** — the `mcpproxy-dash` page with the four hypothesis signatures (H1 breakage, H2 no habit, H3 version rot, H4 silent failure) consumes v7 fields but ships separately.
- **Shipping `machine_id` end-to-end** (worker column extraction, dashboard `identityExpr`) — owned by the existing `telemetry-identity` epic in `roadmap.yaml`.
- **Retroactive correction of historical data** — previously-recorded `skipped` statuses and pre-v7 heartbeats are not rewritten; analysis segments by schema version.
- **Session/timeline telemetry** — per-session durations, event timestamps, shutdown-time beacons, or any per-day breakdown on the wire; only aggregate counters and enums are added (the local 30-day structure is in scope per FR-008; transmitting it is not).
- **New error taxonomies** — `last_error_code` reuses the existing stable `MCPX_*` code set; adding categories or codes is separate work.
- **Wizard UX changes** — this spec changes what dismissal *records*, not how the wizard looks or flows (that belongs to the `ux-audit` epic).

## Commit Message Conventions *(mandatory)*

When committing changes for this feature, follow these guidelines:

### Issue References
- ✅ **Use**: `Related #NNN` - Links the commit to the issue without auto-closing
- ❌ **Do NOT use**: `Fixes #`, `Closes #`, `Resolves #`

### Co-Authorship
- ❌ **Do NOT include** `Co-Authored-By: Claude` or "Generated with Claude Code" trailers.

### Example Commit Message
```
feat(telemetry): honest wizard connect metric + v7 churn instrumentation

Related #NNN

Widens the wizard connect-step enum with completed_external so external
connections (CLI/ConnectModal/manual config) stop being counted as skips,
adds wizard_shown, web_ui_opened, days_since_install, and active_days_30d
for a self-contained retention funnel, and records previous_shutdown +
last_error_code so the final heartbeat before churn carries a
cause-of-death snapshot. Bumps heartbeat schema to v7 (additive).

## Changes
- OnboardingState connect-step enum + dismissal-time external-connection check
- New BBolt-backed counters/markers: web_ui_opened, first-install stamp,
  30-day activity window, shutdown marker, last_error_code
- HeartbeatPayload v7 fields + schema bump + docs/features/telemetry.md

## Testing
- Decision-matrix tests for completed_external incl. fallback + non-regression
- Day-window aging, clock-clamp, and index-serve counting tests
- Clean/crash/first-run shutdown detection across restart sequences
- Anonymity-scanner and v6 shape-compatibility assertions
```
