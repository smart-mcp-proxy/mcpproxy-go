# Implementation Plan: Tray Glance v2 — Grouped Calls, Intent Reasons, Failure-Only Marks, Idle Clients

**Branch**: `090-tray-glance-v2` | **Date**: 2026-07-31 | **Spec**: [spec.md](./spec.md)
**Input**: Feature specification from `/specs/090-tray-glance-v2/spec.md`

## Summary

Iterate on the merged tray glance section (PR #930): collapse consecutive same-tool activity records into ×N rows, render caller intent reasons as menu-row subtitles (macOS 14.4+), mark only failures and surface policy blocks, move the 24h histogram to the top of the block, and replace the perpetually-empty "Clients" section with presence states (active / idle / seen) over all retained sessions. Three small additive backend changes carry the data: a contextual-metadata whitelist in the `exclude_payloads` projection, `request_id` on policy-decision events/records, and last-activity ordering in the sessions listing.

## Technical Context

**Language/Version**: Swift 5.9 (AppKit, SwiftUI for histogram only) + Go 1.25 (core)
**Primary Dependencies**: AppKit `NSMenuItem` (incl. `subtitle`, macOS 14.4+), fixture-driven XCTest; Go: chi HTTP API, bbolt storage, zap
**Storage**: bbolt (`activity_records`, `sessions` buckets) — no schema changes; additive record field (`request_id` on policy decisions)
**Testing**: `swift test` (native/macos/MCPProxy, XCTest), `go test ./internal/...`, fixture replay from `specs/090-tray-glance-v2/fixtures/activity-replay.jsonl`
**Target Platform**: macOS 13+ (deployment floor unchanged); reason subtitles render on macOS 14.4+ only (documented degradation below)
**Project Type**: Native macOS app + Go backend (two build systems, one repo)
**Performance Goals**: glance poll payload stays ~tens of KB (whitelist keeps the 28× projection saving); menu updates in place without re-layout storms
**Constraints**: zero network requests on menu open (spec 048 invariant); no structural menu mutation while open (MenuRebuildGuard); GlanceSelection/GlanceFormatting stay pure and AppKit-free; tray reads no config files
**Scale/Scope**: 100-record activity page, 100-session page, 5 activity rows + 5 client rows

## Constitution Check

*GATE: evaluated pre-Phase-0 and re-checked post-design — PASS, no violations.*

- **I. Performance at Scale**: The metadata whitelist preserves the lightweight projection (~30KB/poll vs ~848KB full). Sessions ordering change is an O(retained=100) scan + sort, bounded. PASS.
- **II. Actor-Based Concurrency**: No new goroutines; changes ride existing ActivityService/event-bus paths. Swift side stays @MainActor for menu code, pure functions elsewhere. PASS.
- **III. Configuration-Driven Architecture**: No new config; thresholds are fixed presentation policy per spec. Tray keeps zero own state (all data via REST/SSE). PASS.
- **IV. Security by Default**: The whitelist adds only small contextual fields (intent reason/operation type, policy decision/reason, client name) — never arguments/response bodies. Policy `request_id` is an opaque correlation id. PASS.
- **V. TDD**: Every behavior lands test-first: pure pipeline tests (Swift), projection/ordering/request-id tests (Go), fixture replay tests, menu-open counting test. PASS.
- **VI. Documentation Hygiene**: docs/features/activity-log.md gains the projection whitelist note; CLAUDE.md untouched (no commands/architecture change). PASS.
- **Core+Tray split / Event-driven**: All data flows through existing REST + SSE surfaces; tray renders only. PASS.

## Project Structure

### Documentation (this feature)

```text
specs/090-tray-glance-v2/
├── spec.md
├── plan.md              # This file
├── research.md          # Phase 0
├── data-model.md        # Phase 1
├── quickstart.md        # Phase 1
├── contracts/           # Phase 1 (API deltas)
├── fixtures/activity-replay.jsonl
├── verification/manual-protocol.md
└── tasks.md             # Phase 2 (/speckit.tasks)
```

### Source Code (repository root)

```text
# Go core (additive deltas)
internal/httpapi/activity.go            # exclude_payloads → contextual-metadata whitelist
internal/httpapi/activity_test.go       # projection tests
internal/runtime/event_bus.go           # EmitActivityPolicyDecision gains requestID param
internal/runtime/activity_service.go    # persist request_id on policy records
internal/server/mcp.go                  # pass requestID at the policy-rejection call sites
internal/storage/manager.go             # GetRecentSessions: order by last_activity pre-truncation
internal/storage/manager_test.go        # ordering tests

# Swift tray (native/macos/MCPProxy/MCPProxy)
Menu/Glance/GlanceSelection.swift       # qualify + collapse + NEW: outcome-class grouping pipeline
Menu/Glance/GlanceFormatting.swift      # reason extraction, ×N suffix, budgets
Menu/Glance/GlanceSection.swift         # subtitle rendering (14.4+ gate), failure-only icons,
                                        #   presence rows, block reorder, group-identity updates
Menu/Glance/GlanceEvent.swift           # SSE adapter: extract intent + adapt policy_decision events
Menu/Glance/GlancePresence.swift        # NEW: pure client-presence classification/dedup
State/AppState.swift                    # feed changes: policy type filter, 100-session unfiltered poll,
                                        #   summary counts by state
API/APIClient.swift                     # glanceActivity type param + sessions limit/filter change
API/Models.swift                        # ActivityEntry.reason accessor (metadata.intent.reason / metadata.reason)

# Swift tests (native/macos/MCPProxy/MCPProxyTests)
GlanceSelectionTests.swift, GlanceSelectionCollapseTests.swift   # extended: pipeline order, grouping
GlanceGroupingTests.swift               # NEW: run grouping, worst outcome, ×N, group identity
GlancePresenceTests.swift               # NEW: classification, dedup, boundaries, malformed timestamps
GlanceFixtureReplayTests.swift          # NEW: SC-001/SC-004 chronological replay over the fixture
GlanceFormattingTests.swift             # extended: reason budget, truncation precedence
AppStateGlanceTests.swift               # extended: summary counts, menu-open zero-request delta
```

**Structure Decision**: Extend the existing glance module in place; one new pure Swift file per new pure concern (grouping stays in GlanceSelection; presence gets GlancePresence.swift). Backend deltas ride existing files — no new packages.

## Complexity Tracking

No constitution violations; table intentionally empty.
