# Feature Specification: Required-Tools Preflight

**Feature Branch**: `098-tools-preflight`
**Created**: 2026-08-15
**Status**: Draft
**Input**: User description: "Required-tools preflight (issue #969 item 3): a deterministic, side-effect-free availability check for a caller-supplied list of tool IDs, answering per-ID with a machine-readable reason code. Locked decisions from the research report 'Required-Tools Preflight — Research & Alternatives (mcpproxy #969)' (decision log 2026-08-15) and reporter-confirmed shape (issue #969 comment, 2026-08-13)."

**Related**: #969 (item 3). Item 1 (filter_diagnostics) shipped in spec 094 / v0.55.0. This spec is v1 (Phase 1) of the preflight roadmap: shared eligibility evaluator + REST + CLI. Non-goals list the deferred phases.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Cron/CI job gates on required tools before spending model tokens (Priority: P1)

An operator runs a recurring headless automation (cron, launchd, CI) that depends on a small, stable set of tools (e.g. `gh-ops:sync_issues`, `slack:post_message`). Before the agent session starts, the job runs one preflight command. If every required tool is ready, the job proceeds. If not, the job fails deterministically — before any model tokens are spent — with a per-tool, machine-readable reason (quarantined, disabled, unhealthy, unknown ID, …) and a remediation hint, and the wrapper can branch on the exit code alone: retry later, page the operator, or fail the pipeline.

**Why this priority**: This is the reporter's confirmed primary flow ("fail deterministically before an agent session spends tokens on discovery") and the core of issue #969 item 3. Without it, a quarantine flip or upstream failure surfaces as a silent discovery miss and an ambiguous agent failure — the measured cost of that ambiguity in the wild is days-to-weeks of diagnosis.

**Independent Test**: Run `mcpproxy tools preflight <ids…>` against a local proxy with sabotaged fixtures (one tool quarantined, one server disabled, one ID misspelled) and assert the exit code and per-tool reason codes without any other feature present.

**Acceptance Scenarios**:

1. **Given** all required tools are indexed, approved, on enabled healthy servers, **When** the preflight runs, **Then** it exits 0 and reports every tool `ready` — without contacting any upstream server.
2. **Given** one required tool's definition changed after approval (rug-pull guard tripped), **When** the preflight runs, **Then** it exits 11 (blocked), that tool reports `tool_changed` with `retryable: false`, action `approve`, and a remediation pointing at the diff/review flow.
3. **Given** one required server is still connecting/indexing, **When** the preflight runs, **Then** it exits 10 (retryable), the affected tools report `server_initializing` with `retryable: true`, and the job can back off and retry.
4. **Given** a misspelled tool ID, **When** the preflight runs, **Then** it exits 12 (unknown ID), the entry reports `not_found` with a scope-filtered `did_you_mean` suggestion.
5. **Given** a mix of failures, **When** the preflight runs, **Then** the exit code is the worst class present (12 > 11 > 10) and the JSON output lists every per-tool verdict.

---

### User Story 2 - Automation script calls the REST preflight directly (Priority: P1)

A platform engineer's harness (n8n, GitHub Actions, a Python script) calls `POST /api/v1/preflight` with the automation's required tool list (optionally pinned by schema hash, optionally under a named profile) and branches on the structured response. The verdict is data: HTTP status is 200 whenever the check itself executed; the body carries the set-level verdict and per-tool results.

**Why this priority**: Same journey as Story 1 without the CLI dependency — anything that can speak HTTP can gate on required tools. The CLI is a wrapper over this endpoint, so the endpoint is the foundation.

**Independent Test**: `curl -X POST /api/v1/preflight` with a JSON body of tool IDs against a fixture proxy; assert response shape, verdicts, and that the same request under a `profile` reflects that profile's visibility.

**Acceptance Scenarios**:

1. **Given** a valid API key and a list of tool IDs, **When** POSTing to `/api/v1/preflight`, **Then** the response contains `verdict`, `checked_at`, and one result per requested unique ID carrying `id` and `status`; `unavailable` results additionally carry `reason`, `retryable`, `action`, `detail`, `remediation` (ready results omit failure fields).
2. **Given** a `pin_hash` that no longer matches the tool's current schema hash, **When** the preflight runs, **Then** that tool reports `hash_mismatch` (blocked class), even though the tool is otherwise ready.
3. **Given** a `profile` parameter, **When** the preflight runs, **Then** verdicts reflect that profile's server scope and per-profile index — a tool outside the profile reports `not_found`, matching what a profile-pinned session would experience.
4. **Given** `wait_ms` is supplied and a failure is retryable-only, **When** the state becomes ready within the deadline, **Then** the response reports `ready`; **When** the deadline passes first, **Then** the response resolves (never hangs) with the current reasons.
5. **Given** an agent-token caller whose scope excludes a requested server, **When** the preflight runs, **Then** the out-of-scope tool reports plain `not_found` (no existence leak, no cross-scope `did_you_mean`, no hashes), while the same request with the operator API key reports `server_not_in_scope` with the full diagnosis.

---

### User Story 3 - Operator audits preflight activity for the transparency story (Priority: P2)

An operator (or a curious user reviewing what their agents did) opens the activity log and sees every preflight call: when it ran, what was asked, the per-tool verdicts, and the request ID that correlates it with any subsequent tool calls in the same workflow. A failed nightly job is diagnosable next morning from the activity log alone.

**Why this priority**: Transparency is a stated product pillar; a diagnostic feature whose own runs are invisible would undercut it. Also the substrate for later effectiveness metrics (silent-vs-reasoned trend).

**Independent Test**: Run one preflight (REST or CLI), then `mcpproxy activity list` and assert a preflight record exists with the request ID, requested IDs, and verdict summary; open the Web UI activity view and confirm it renders.

**Acceptance Scenarios**:

1. **Given** any preflight call, **When** it completes, **Then** an activity record is written carrying the request ID, the number of requested IDs, the set-level verdict, and per-tool reason codes.
2. **Given** an activity record from a preflight, **When** the operator runs `mcpproxy activity list --request-id <id>`, **Then** the preflight record is returned and browsable alongside other records from the same workflow.
3. **Given** the Web UI activity view, **When** a preflight record is present, **Then** it renders with its verdict without breaking existing activity rendering.

---

### User Story 4 - Agent-driven flows keep working everywhere the proxy is used (Priority: P3)

An agent operating through mcpproxy's MCP surfaces (retrieve_tools mode, code_execution mode, stored server-side scripts) is unaffected by the feature when it doesn't use it: MCP tool schemas are byte-identical, and code-execution scripts can reach the preflight only the same way any REST consumer can. Where an agent needs an in-band check today, the documented interim path is the existing `describe_tool` codes; the dedicated in-band check mode is Phase 2.

**Why this priority**: Guard-rail story — the feature must not tax or destabilize the token-minimized MCP surface that is the product's core value.

**Independent Test**: Diff `tools/list` payloads for all three routing modes between main and the feature branch (must be identical); run a code_execution script and a stored script end-to-end and confirm behavior is unchanged.

**Acceptance Scenarios**:

1. **Given** any MCP client session in any routing mode, **When** listing tools, **Then** the registered tool schemas are byte-identical to the previous release.
2. **Given** a code_execution or stored-script run, **When** it executes upstream tools, **Then** behavior and dispatch gating are unchanged, and dispatch decisions remain consistent with what a preflight of the same tools would report (no visibility/enforcement skew).

---

### Edge Cases

- **Co-occurring states**: a nonexistent tool on a quarantined server reports `server_quarantined` (existence is unknowable there — quarantined servers' tools are never indexed); precedence is fixed and documented (see FR-004).
- **Empty tool list**: rejected with a validation error (400) — an empty preflight is a caller bug, not a trivially-green check.
- **Duplicate IDs**: deduplicated; the response contains one result per unique ID.
- **Malformed ID** (no `server:tool` separator): per-ID `not_found` with a format hint in `detail`, not a request-level error — one bad entry must not mask verdicts for the rest.
- **Batch limits**: at most 100 IDs per request; oversized requests get a 400 with the limit named.
- **Runtime not available** (degraded process state): the served endpoint refuses with 503 rather than emitting reduced-fidelity verdicts (FR-006); storage-only evaluation exists only in unit tests.
- **`wait_ms` under load**: capped at 10 000 ms; the waiting request counts against the concurrency-shed budget (spec 093) so a flood of waiting preflights cannot starve real traffic; polling uses a floor interval so waiting adds no meaningful load.
- **Server edition, multi-user OAuth**: verdicts are operator-view (global connection state). `oauth_required` may not reflect the calling user's own token state; documented, with `as_user` reserved for a future revision (non-goal here).
- **Windows**: the named pipe carries the same admin-context semantics as the Unix socket; the CLI works identically.
- **Never triggers side effects**: a preflight must never initiate connects, reconnects, re-indexing, or any upstream I/O — observational only, even when it finds a dead server.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001 (Evaluator)**: System MUST provide a single shared eligibility evaluator that, given a tool ID and a caller context (auth tier, optional profile, optional policy filters, optional pin hash), returns either `ready` or exactly one failure reason from the closed enum (FR-003). All preflight surfaces MUST use this evaluator.
- **FR-002 (No-skew)**: The evaluator's verdict semantics MUST be consistent with call-time dispatch gating **for the gates the evaluator represents** (existence, scope, quarantine, approval status, tool/user/config disablement): any such state that causes dispatch to refuse a tool MUST map to a non-`ready` preflight verdict, and the existing per-tool gates (dispatch inline checks, describe-gate reasons, locked-tool classification) MUST be reconciled so they cannot disagree with the evaluator. Divergences found during consolidation MUST be resolved in favor of dispatch behavior (dispatch is ground truth) and covered by tests. Explicit carve-out: connection-time failures that only manifest during a live call (network races, per-user OAuth state in the server edition) are outside the no-skew guarantee — preflight is a point-in-time eligibility check, not a call-success guarantee.
- **FR-003 (Reason enum)**: A per-tool result has `status: ready | unavailable`. `ready` is the success status, not a failure reason. For `unavailable`, the closed v1 failure-reason enum is exactly 15 codes, per the normative table below. Each result carries `id` (the requested unique ID), `status`, and for failures `reason`, `retryable` (bool), `action` (existing health-action vocabulary: `login`/`restart`/`enable`/`approve`/`view_logs`/`set_secret`/`configure`/`none`), `detail`, `remediation`. Evolution is additive-only; consumers are instructed to treat unknown codes as non-retryable. `server_saturated` is reserved (not implemented). `server_not_in_scope` is emitted at the operator tier only; the agent-token tier maps that state to `not_found` (see FR-013).

  | Reason | Class | `retryable` | Default `action` | Set verdict | CLI exit |
  |---|---|---|---|---|---|
  | `server_initializing` | retryable | true | none | degraded_retryable | 10 |
  | `server_unhealthy` | retryable | true | best-effort from diagnostics (`restart`/`login`/`view_logs`; default `view_logs`) | degraded_retryable | 10 |
  | `server_disabled` | fix-state-first | false | enable | blocked | 11 |
  | `server_quarantined` | fix-state-first | false | approve | blocked | 11 |
  | `tool_pending_approval` | fix-state-first | false | approve | blocked | 11 |
  | `tool_changed` | fix-state-first | false | approve | blocked | 11 |
  | `tool_blocked_by_user` | fix-state-first | false | enable | blocked | 11 |
  | `oauth_required` | fix-state-first | false | login | blocked | 11 |
  | `hash_mismatch` | fix-state-first | false | configure | blocked | 11 |
  | `server_not_in_scope` (operator tier only) | permanent-config | false | configure | blocked | 11 |
  | `tool_denied_by_config` | permanent-config | false | configure | blocked | 11 |
  | `missing_annotation` | permanent-config | false | configure | blocked | 11 |
  | `policy_filtered` | permanent-config | false | none | blocked | 11 |
  | `not_found` | permanent-config | false | configure | unknown_ids | 12 |
  | `server_not_configured` | permanent-config | false | configure | unknown_ids | 12 |

  Set-level verdict and CLI exit code are the worst class present, ordered `unknown_ids` (12) > `blocked` (11) > `degraded_retryable` (10) > `ready` (0).
- **FR-004 (Precedence)**: When multiple states co-occur for one ID, the first match in this order wins: `server_not_configured` → `server_not_in_scope` (operator tier; token tier reports `not_found` here) → `server_quarantined` → `server_disabled` → `not_found` → `tool_denied_by_config` → `tool_blocked_by_user` → `tool_changed` → `tool_pending_approval` → `hash_mismatch` → `oauth_required` → `server_unhealthy` → `server_initializing` → annotation filters → `ready`. Hash-pin validation runs at its precedence slot: it applies only once the tool is known to exist with a current stored hash; earlier states win over `hash_mismatch`. Annotation-filter classification follows the spec 094 convention: filters are evaluated in the fixed order `read_only_only` → `exclude_destructive` → `exclude_open_world`, the first filter that excludes owns the omission, and within that filter the verdict is `missing_annotation` when the hint is absent and `policy_filtered` when the hint is explicitly unsafe — the two are mutually exclusive per owning filter, so their ordering is deterministic.
- **FR-005 (State sources)**: Verdicts MUST be computed exclusively from local state: index existence, tool-approval records and hashes (spec 032), connection-state snapshot and health classification, config policy (enabled/disabled tools), and annotation classification (spec 094 `excludeReason`) when policy filters are supplied. `server_initializing` is a server-level verdict; the system MUST NOT claim per-tool knowledge ("your tool will appear") during discovery/indexing.
- **FR-006 (Zero upstream I/O)**: A preflight MUST perform zero upstream server calls and MUST NOT mutate proxy runtime state (no connects, reconnects, re-index, config or approval changes). "Side-effect-free" is defined as no upstream I/O and no runtime mutation; the local activity-log write required by FR-014 is explicitly permitted and expected. Any helper with a live-call fallback MUST be excluded or guarded. Asserted by an automated test using an instrumented transport. If the runtime is unavailable when a preflight arrives (degraded process state), the endpoint MUST refuse with 503 (`preflight unavailable`) rather than emit reduced-fidelity verdicts; storage-only evaluation exists only inside unit tests, never on the served surface.
- **FR-007 (PendingAuth)**: The PendingAuth/deferred-OAuth connection state MUST map to `oauth_required` with `retryable: false` and action `login` (waiting does not help without a login).
- **FR-008 (REST surface)**: `POST /api/v1/preflight` MUST accept `{tools: [{id, pin_hash?}…], profile?, policy?{read_only_only, exclude_destructive, exclude_open_world}, wait_ms?}` and return `{verdict, checked_at, waited_ms?, tools: [per-ID results]}` with HTTP 200 whenever the check executed (validation errors: 400; runtime unavailable: 503). Set-level `verdict` per the FR-003 table. Every result carries its requested `id`; results are ordered by first occurrence of each unique ID in the request. Duplicate IDs are deduplicated; duplicates carrying **different** `pin_hash` values are a validation error (400). Requests rejected with 400/503 did not execute a preflight and write no preflight activity record (standard HTTP request handling applies).
- **FR-009 (CLI surface)**: `mcpproxy tools preflight <id>… [--profile P] [--pin id=hash] [--read-only-only] [--exclude-destructive] [--exclude-open-world] [--wait duration] [-o json|yaml|table]` MUST wrap the endpoint with exit codes: 0 all ready · 10 degraded-retryable · 11 blocked (operator action) · 12 unknown ID present; worst class wins. Transport/other CLI failures use the existing general exit code 1. The command MUST follow existing CLI output conventions (`-o`, `MCPPROXY_OUTPUT`, `--help-json`).
- **FR-010 (Profiles)**: With `profile` supplied, evaluation MUST run under that profile's server scope and per-profile index so the verdict matches a profile-pinned session's experience. Without it, evaluation is the unscoped operator view (documented). Unknown profile: 400.
- **FR-011 (Hash pinning)**: With `pin_hash` supplied for an ID, a divergence from the tool's current stored hash MUST report `hash_mismatch`. The pin format MUST embed the hash schema version so proxy-side hash-algorithm bumps are distinguishable from genuine upstream drift; current hashes at the current schema version MUST be discoverable via existing surfaces so pins can be created.
- **FR-012 (wait_ms)**: With `wait_ms > 0` (cap 10 000), when all current failures are retryable-class the system MUST poll local state until every tool is ready or the deadline passes; it terminates early the moment any non-retryable failure appears (waiting cannot help), MUST always resolve with current reasons at the deadline (never hang), MUST use a polling floor (≥250 ms), and the waiting request MUST count against the spec 093 concurrency-shed budget.
- **FR-013 (Disclosure tiers)**: Operator tier (API key / socket / named pipe): full results including hashes and the `server_not_in_scope` diagnosis (emitted when a supplied `profile` — or, in a later phase, a named token scope — excludes an existing server). Agent-token tier: scope-silence — an out-of-scope ID's **entire result** MUST be byte-indistinguishable from an ordinary `not_found` (same `reason`, `retryable`, `action`, `detail`, `remediation` wording; no hashes; no `did_you_mean` crossing the scope boundary). `did_you_mean` is a nearest-name suggestion on `not_found`, computed over the caller-visible index only and never suggesting quarantined-tool names.
- **FR-014 (Activity log)**: Every executed preflight (i.e., any request answered 200) MUST write an activity record carrying the request ID, requested-ID count, set-level verdict, and per-tool reason codes, correlated via X-Request-Id and browsable via `mcpproxy activity list` and the Web UI activity view. Records MUST NOT leak tool names to any telemetry surface (activity log is local-only; telemetry, if any, carries counts and enum codes only).
- **FR-015 (MCP surface untouched)**: No MCP tool schema changes in any routing mode: `tools/list` payloads MUST be byte-identical to the prior release across default, retrieve_tools, and code_execution modes. Verified by a snapshot test.
- **FR-016 (Sabotage test matrix)**: E2E tests MUST deliberately induce each reason state (quarantine flip, tool-definition drift, tool block, config denial, server disable, server kill/disconnect, mid-indexing, missing/explicit annotations under filters, unknown ID, unknown server, hash mismatch, deferred OAuth, out-of-scope under a profile at both disclosure tiers) and assert the exact reason code, `retryable` flag, and action per cell. Adding an enum code without its cell MUST fail review/CI.
- **FR-017 (Docs)**: Documentation MUST be updated: REST API reference (endpoint + schema), CLI reference (command + exit codes), a feature page for preflight (concept, taxonomy table, cron/CI recipes, agent-workflow examples), and expanded usage examples covering token-saving discovery flows and common agent actions with mcpproxy connected, including how preflight composes with code_execution and stored scripts (via REST from the harness; in-band check mode is Phase 2).

### Key Entities

- **Preflight request**: caller-supplied list of tool IDs (`server:tool`), each optionally hash-pinned; optional profile, policy filters, wait budget.
- **Per-tool verdict**: reason code (closed enum), retryable flag, action, human detail, remediation string; optionally hash (operator tier) and `did_you_mean` (on `not_found`).
- **Set-level verdict**: worst-class aggregate (`ready` / `degraded_retryable` / `blocked` / `unknown_ids`) driving the CLI exit code.
- **Activity record (preflight)**: request ID, timestamp, requested-ID count, set verdict, per-tool reasons — local activity log only.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: For every induced failure state in the sabotage matrix, the preflight names the correct reason with the correct retryability — 100% of cells, enforced in CI.
- **SC-002**: A preflight of 10 tools completes in under 50 ms (p95) measured by a committed Go benchmark at the handler level (evaluator + response + activity-record build, excluding HTTP transport) against the repo's fixture corpus on CI-class hardware, and performs zero upstream calls (hard assertion via instrumented transport, not a threshold).
- **SC-003**: A cron wrapper can branch retry-vs-page-vs-fix using only the exit code — no JSON parsing — in all acceptance scenarios of Story 1.
- **SC-004**: MCP client sessions pay zero additional context tokens: `tools/list` payloads are byte-identical across all routing modes.
- **SC-005**: Every preflight run is discoverable in the activity log within the same session by request ID; a scripted "failed nightly job" scenario is diagnosable from the activity log alone (correct root cause named) without server logs.
- **SC-006**: The scripted replay of the deactivated-tool incident class (tool quarantined between runs) reaches a named root cause in ≤1 step after the preflight response, versus never (silent failure) on the baseline.

## Non-Goals (v1)

- In-band MCP surface (`describe_tool` check mode) — Phase 2; interim in-band guidance is the existing `describe_tool` per-ID codes (batch ≤5).
- `readyz` probe endpoint, SSE readiness events, `tools/list_changed` emission.
- Tool lockfile (`mcpproxy tools lock/verify`) and registered automation contracts with change-time warnings.
- Agent-token-carried required-tools contracts; MCP extension (`app.mcpproxy/required-tools`) — later phase; identifier and negotiation shape per the 2026-08-15 SEP verification.
- Per-user verdicts in the server edition (`as_user` reserved); spec 093 queue-saturation verdicts (`server_saturated` reserved).
- Refresh/liveness operations — preflight describes proxy state only; refresh remains a separate explicit operation.

## Assumptions

- The reporter-confirmed contract (issue #969 comment, 2026-08-13) is authoritative for v1 shape: cron/CI-first, REST+CLI, stat-only.
- The three existing per-tool gate implementations are known to disagree in edge cases (quarantine-enabled flag handling; changed-vs-pending collapse); reconciling them is in scope and dispatch behavior is ground truth.
- Activity-log record schema can be extended with a new record kind without breaking existing consumers (CLI + Web UI render unknown kinds generically or are updated in this feature).
- The research artifact and its §11 decision log (2026-08-15) are the design record; this spec implements Phase 1 only.

## Commit Message Conventions *(mandatory)*

### Issue References
- ✅ **Use**: `Related #969`
- ❌ **Do NOT use**: `Fixes #969`, `Closes #969`, `Resolves #969`

### Co-Authorship
- ❌ **Do NOT include**: `Co-Authored-By: Claude <noreply@anthropic.com>`
- ❌ **Do NOT include**: "🤖 Generated with [Claude Code](https://claude.com/claude-code)"
