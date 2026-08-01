# Research — Tray Glance v2 (Spec 090)

All unknowns were resolved against the actual codebase during spec review (4 Codex
review rounds). Decisions and rationale:

## D1. Reason display mechanism: `NSMenuItem.subtitle`, gated to macOS 14.4+

- **Decision**: Render the reason via the standard `NSMenuItem.subtitle` property
  (`if #available(macOS 14.4, *)`); below 14.4 the row is single-line and the
  reason lives in tooltip + accessibility label. Deployment floor stays macOS 13
  (`Package.swift` platforms `.macOS(.v13)`).
- **Rationale**: subtitle is the only *documented* two-line mechanism on a plain
  `NSMenuItem`; it preserves keyboard navigation and VoiceOver (custom
  view-backed rows lose both — see the GlanceSection header comment). A newline
  in `attributedTitle` is undocumented behavior with unreliable row sizing
  (Codex round-2 P1).
- **Alternatives**: attributed-title newline (undocumented, rejected);
  view-backed rows (breaks accessibility invariant, rejected); raising the
  deployment floor (product decision out of scope, rejected).

## D2. Contextual metadata in the glance poll: whitelist inside `exclude_payloads`

- **Decision**: `internal/httpapi/activity.go` — when `exclude_payloads=true`,
  instead of dropping `metadata` entirely, keep a small whitelist:
  `intent.reason`, `intent.operation_type`, `decision`, `reason`,
  `client_name`, `client_version`. Continue dropping `arguments`, `response`,
  and everything else in metadata (incl. toon output, error payload bodies).
- **Rationale**: today's projection strips ALL metadata (the 28× size saving:
  ~848KB → ~30KB per 100-record poll, per the APIClient comment). The
  whitelisted fields are short strings (reason p90 = 82 chars), keeping the
  payload in the tens of KB. No new endpoint, no new query param — the
  projection simply gets less lossy for context fields.
- **Alternatives**: dropping `exclude_payloads` (28× payload regression every
  30s, rejected); a new `fields=` param (more API surface for one caller,
  rejected).

## D3. Policy-decision identity: `request_id` end to end

- **Decision**: Thread `requestID` through EVERY policy-decision emitter — 16
  invocations across `internal/server`: 13 in `mcp.go`, plus
  `mcp_routing.go:193` (direct routing) and `output_sanitisation.go:108`
  (sanitisation/validation helpers, whose signatures gain a requestID
  parameter since their callers already hold one) — into
  `Runtime.EmitActivityPolicyDecision` (`event_bus.go:497`, payload field) and
  the persisted record (`activity_service.go` policy subscriber). Blocks that
  fire before today's minting point (e.g. `mcp.go:1781`, `mcp.go:1913`) hoist
  minting above the earliest policy gate, so the blocked SSE event and the
  persisted record share one non-empty id. Tests cover: pre-intent-validation
  blocks, direct-routing blocks, output-sanitisation blocks/redactions,
  output-schema decisions, and SSE/persisted id parity. Legacy records without
  `request_id` are never collapsed (spec FR-015), so no migration.
- **Rationale**: the glance reconciles live SSE rows with polled rows by
  `request_id` (`GlanceSelection.recordKey`); without it every blocked row
  would double-render (provisional + persisted).
- **Alternatives**: synthesizing identity client-side from
  (timestamp, server, tool) — collision-prone and still divergent between SSE
  and poll (rejected).

## D4. Sessions ordering: sort by `last_activity` before truncation

- **Decision**: `internal/storage/manager.go GetRecentSessions` collects ALL
  retained sessions (cap 100 by `enforceSessionRetention`), sorts by
  `LastActivity` desc, then truncates to `limit`. The runtime's post-hoc
  page re-sort (`runtime.go:1344`) becomes a no-op but stays harmless.
- **Rationale**: today the bucket cursor walks start-time-prefixed keys and
  truncates FIRST, so a long-running but recently-active session can vanish
  from the page (Codex round-1 P1). Retention caps the scan at 100 records —
  bounded work.
- **Alternatives**: new `sort=` query param (unneeded generality); secondary
  index bucket (overkill for ≤100 records).

## D5. Grouping location and shape: pure functions in `GlanceSelection`

- **Decision**: Pipeline stays in `GlanceSelection` as pure, ordered steps
  (spec FR-001): `qualifies` → `collapseByRequestID` → NEW
  `groupConsecutive(byKey:)` with key (server, tool, outcomeClass) → prefix(5).
  Output is a new pure value type `GlanceRun` (records, count, newest, oldest,
  worstStatus, newestReason). Group identity for in-place updates =
  `recordKey(oldest)` (stable while a run extends at the head; spec FR-024).
- **Rationale**: preserves the existing testability seam (all pure, AppKit-free)
  and the GlanceSection contract (rows keyed by a stable identity so
  `updateInPlace` distinguishes "same run, new count" from "different run").

## D6. Presence classification: new pure `GlancePresence`

- **Decision**: New AppKit-free `GlancePresence` enum namespace:
  dedupe(sessions, by: name+version, keep most-recent last_activity) →
  classify(now:) into `.active` (<5m) / `.idle` (5m…30m inclusive) /
  `.seen` (>30m, ≤24h) / excluded (>24h or unparseable timestamps; missing
  `last_activity` falls back to start time). Summary counts computed over the
  full deduped set; rows capped at 5 by most-recent activity.
- **Rationale**: mirrors the GlanceSelection pattern — policy as pure functions,
  rendering in GlanceSection.

## D7. Fixture replay tests: `#filePath`-relative access

- **Decision**: `GlanceFixtureReplayTests` locates
  `specs/090-tray-glance-v2/fixtures/activity-replay.jsonl` by walking up from
  `#filePath` to the repo root. Replay = reverse file order (fixture is stored
  newest-first), sliding latest-100 window per step (spec SC-004).
- **Rationale**: avoids duplicating an 860KB fixture into test resources and
  keeps one source of truth. The `#filePath` walk is independent of the
  working directory (CI runs `swift test` from `native/macos/MCPProxy`, not
  the repo root — irrelevant to a source-path-anchored walk).
- **Alternatives**: Package resource bundling (duplicates the fixture and
  bloats the package; rejected).

## D8. Menu-open zero-request test

- **Decision**: `AppController.menuWillOpen` currently has no dependency on
  `GlanceDataSource` — it calls `rebuildMenu()` directly, so counting the
  existing seam through menu-open is vacuous. Introduce a genuine seam: the
  controller's data source becomes injectable (production default:
  `APIClient`), and every network-capable path the menu-open sequence could
  reach goes through it. `MenuOpenNetworkTests` then constructs the controller
  with a counting stub, drives the REAL `menuWillOpen` → `rebuildMenu` →
  `updateInPlace` sequence on a real `NSMenu`, and asserts a zero request
  delta (spec FR-022/SC-006).
- **Rationale**: Codex plan-review P1 — without the injection point the test
  can never fail, regardless of future regressions.

## D9. Blocked-event SSE adaptation

- **Decision**: `GlanceEvent` learns `activity.policy_decision` (event name from
  `internal/runtime/events.go`): map `decision blocked/block → status
  "blocked"`, carry `reason` into the entry's metadata-reason slot, use the new
  `request_id` for provisional identity (`"<request_id>:policy_decision"`);
  also extract `intent` from the two completed-event payloads (today
  discarded, `metadata: nil`). Crucially, the production SSE dispatch switch in
  `Core/CoreProcessManager.swift` (~line 1349) routes only the two completion
  event names today — it gains the policy event name, with a routing test
  proving a policy SSE event reaches the glance feed before any poll.
- **Rationale**: FR-006/FR-008 live-row parity with polled rows; adapting
  without routing would be dead code (Codex plan-review P1).
