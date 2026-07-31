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

- **Decision**: Thread `requestID` through `emitActivityPolicyDecision`
  (`internal/server/mcp.go:640`, 9 call sites) →
  `Runtime.EmitActivityPolicyDecision` (`event_bus.go:497`, add payload field) →
  persisted record (`activity_service.go` policy subscriber). Call sites where
  the block fires before a request id is minted generate it at (or hoist
  minting above) the earliest policy gate, so the blocked SSE event and the
  persisted record share one id. Legacy records without `request_id` are never
  collapsed (spec FR-015), so no migration.
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
  keeps one source of truth. `swift test` runs from the repo checkout, so the
  path is stable; CI runs the same way (`.github/workflows` invoke swift tests
  from the repo).
- **Alternatives**: Package resource bundling (duplicates the fixture and
  bloats the package; rejected).

## D8. Menu-open zero-request test

- **Decision**: Extend the counting-stub seam (`GlanceDataSource`) with a test
  that snapshots request counters, drives the real menu-open path
  (`menuWillOpen`/rebuild on a constructed menu), and asserts a zero delta
  (spec FR-022/SC-006). The existing test only counted the three fetches.
- **Rationale**: Codex round-1 P2 — the previous seam never exercised menu
  opening, so the invariant was asserted but not tested.

## D9. Blocked-event SSE adaptation

- **Decision**: `GlanceEvent` learns `activity.policy_decision` (event name from
  `internal/runtime/events.go`): map `decision blocked/block → status
  "blocked"`, carry `reason` into the entry's metadata-reason slot, use the new
  `request_id` for provisional identity; also extract `intent` from the two
  completed-event payloads (today discarded, `metadata: nil`).
- **Rationale**: FR-006/FR-008 live-row parity with polled rows.
