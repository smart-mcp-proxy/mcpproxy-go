# Implementation Plan: Review-Bound Approvals, Durable Baselines and Ordered Inventories

**Branch**: `117-review-bound-approvals` (design) → implementation branches `117-pr1-durable-baseline`, `117-pr2-review-binding`, `117-pr3-capture-order` | **Date**: 2026-10-11 | **Spec**: [spec.md](spec.md)
**Input**: issues #1565, #1566, #1567; PR #1572 (tray client of the #1566 contract)

## Summary

Three PRs, one issue each. PR-1 and PR-3 are independent. PR-2 builds on PR-1 (it needs the durable marker for its empty-review guarantee, design review r1 #3):

1. **PR-1 (#1567) durable baseline.**
   - A `tool_baseline_decisions` bucket records that a server's tool baseline was decided. Trusted discovery and every server-approval commit write it.
   - `serverHasBaseline` consults it, OR-ed with today's record inference.
   - It is removed only with the server's approval records.
2. **PR-2 (#1566) review binding.**
   - The review payload exposes a per-tool review fingerprint (`current_hash`: the definition plus the four safety hints).
   - `security/approve` and `tools/approve` accept `expected_hashes` and validate it under the per-server approval lock. A mismatch returns 409 "out of date" and writes nothing.
   - A server approval runs validate → mark → commit → promote → storage flip in one critical section, with no promote-all after the unquarantine.
   - The Web review screen and the CLI send the binding; the tray (#1572) already does.
3. **PR-3 (#1565) capture order.**
   - The leader of each upstream `tools/list` stamps a process-global capture sequence.
   - A pass applies its inventory (records, deletes, eviction, index, last-good snapshot, StateView) in one critical section under the per-server lock, and only if its capture is not older than the last applied one.
   - The quarantine purge moves under the lock, and the manager retires replaced clients.
   - A capture whose record writes fail marks the server review-dirty: reachability transitions and dispatch of its tools are refused until a complete newer capture (FR-018).

## Technical Context

**Language/Version**: Go 1.26 (backend), TypeScript 5.9 / Vue 3.5 (Web UI)
**Primary Dependencies**: existing only: bbolt, zap, chi, mark3labs/mcp-go v1.0.0, Vue/Pinia. **No new dependencies.**
**Storage**: BBolt `config.db`. One new bucket (`tool_baseline_decisions`, PR-1). No change to the record schema and no migration.
**Testing**:
- `go test -race` in `internal/runtime`, `internal/storage`, `internal/httpapi`, `internal/security/scanner`, `internal/upstream/...` and `cmd/mcpproxy`.
- The internal/server race lane under `-tags server` with the CI skip regex (CLAUDE.md).
- vitest `frontend/tests/unit/*.spec.ts`.
- The Playwright review sweep (`docs/development/web-ui-verification.md`).
- Live harness `qa070` (quickstart.md).

**Target Platform**: both editions (identical behaviour)
**Performance Goals**: SC-005 (≤10% longer discovery pass for a 320-tool server under PR-3). Approval latency is unchanged (one extra list read under the lock).
**Constraints**:
- Additive wire changes only; unbound clients keep working.
- The out-of-date message contains "out of date" and never "dangerous" (tray parsing).
- Lock discipline per contracts/runtime-ordering.md.

## Constitution Check

| Principle | Status | Note |
|---|---|---|
| I. Performance at scale | ✅ | Index writes move under a **per-server** lock (never global). The fingerprint costs one sha256 per record per review read |
| II. Actor-based concurrency | ✅ | Extends the existing per-server lock (#1571) with sequence state. No new goroutines |
| III. Configuration-driven | ✅ | No new config keys. The baseline fact is stored outside the user-editable config on purpose (research D7) |
| IV. Security by default | ✅ | All three PRs fail closed: a stale review approves nothing, an unreadable marker counts as decided, a stale or retired capture writes nothing |
| V. TDD | ✅ | tasks.md pairs every impl task with a failing-first test, behavioural (not a compile error; memory `verify-test-bites-build-failure`) |
| VI. Documentation hygiene | ✅ | rest-api.md, review-commands.md, security-quarantine.md, swagger; CLAUDE.md Recent Changes line per PR |
| Core + tray split | ✅ | The tray is unchanged here. PR #1572 is the client half and is already compatible |
| Event-driven updates | ✅ | Existing `review.changed` / `servers.changed` events; emitted after unlock |

No violations, so Complexity Tracking is empty.

## Project Structure

### Documentation (this feature)

```text
specs/117-review-bound-approvals/
├── spec.md
├── plan.md
├── research.md
├── data-model.md
├── quickstart.md
├── contracts/
│   ├── rest-approval-binding.md
│   └── runtime-ordering.md
├── checklists/requirements.md
└── tasks.md
```

### Source Code (files touched, by PR)

```text
PR-1 (#1567)
internal/storage/models.go                     # ToolBaselineDecisionsBucket const
internal/toolreview/baseline.go                # NEW leaf pkg: BaselineDecision value type
internal/storage/tool_baseline_decision.go     # NEW: Mark/Get + tx-scoped delete helper
internal/storage/bbolt.go                      # DeleteServerToolApprovals, PruneToolApprovalsNotIn also drop markers
internal/storage/manager.go                    # Manager wrappers
internal/runtime/tool_quarantine.go            # serverHasBaseline OR marker; inferred mark before the loop, discovery mark after it (FR-022, FR-027a)
internal/runtime/lifecycle.go                  # FR-027 abort path; sweep StateView batch omits aborted servers (FR-027a)
internal/runtime/tool_approval_lock.go         # CommitServerApproval (PR-1 form: lock, mark, commit)
internal/security/scanner/service.go           # ServerApprovalCommitter capability, both commit branches
internal/server/server.go                      # adapter implements ServerApprovalCommitter
frontend/src/components/ReviewScreen.vue       # blind dialog copy (FR-026)
cmd/mcpproxy/review_cmd.go                     # blind prompt copy (FR-026)
docs/features/security-quarantine.md

PR-2 (#1566)
internal/runtime/review_fingerprint.go         # NEW
internal/runtime/review.go                     # ReviewTool.CurrentHash
internal/toolreview/errors.go                  # NEW leaf pkg: StaleToolReviewError (no storage/scanner import cycle)
internal/runtime/tool_quarantine.go            # ApproveToolsReviewed, ApproveAllToolsReviewed, staleReviewedRecords
internal/runtime/tool_approval_lock.go         # CommitServerApproval, FinishServerApproval
internal/runtime/lifecycle.go                  # QuarantineServer split: finishUnquarantine helper (no promote) reused by FinishServerApproval
internal/security/scanner/service.go           # ApproveServerReviewed; ReviewedServerApprover iface
internal/server/server.go                      # adapter implements ReviewedServerApprover
internal/httpapi/security_scanner.go           # expected_hashes; 409 mapping
internal/httpapi/server.go                     # handleApproveTools expected_hashes; 409 mapping
internal/httpapi/review_errors.go              # NEW: writeStaleReview helper
internal/httpapi/controller iface              # ApproveToolsReviewed/ApproveAllToolsReviewed on the controller interface
cmd/mcpproxy/review_cmd.go                     # binding + 409 handling
frontend/src/services/api.ts, types/api.ts, components/ReviewScreen.vue, utils/reviewPresentation.ts
docs/api/rest-api.md, docs/cli/review-commands.md, docs/features/security-quarantine.md, oas/swagger.yaml

PR-3 (#1565)
internal/upstream/managed/capture.go           # NEW: InventoryCapture, seq counter
internal/upstream/managed/client.go            # ListToolsCaptured, leader stamping, Retire, CaptureIsCurrent
internal/upstream/manager.go                   # DiscoverToolsReportCaptured; Retire on replace/remove
internal/runtime/tool_approval_lock.go         # toolApprovalServerLock{mu, applied, published}; lockToolApprovalsForCapture
internal/runtime/lifecycle.go                  # applyInventory (locked phase + tail); sweep per-server publish; QuarantineServer purge under lock; lastGoodTools seq
internal/runtime/runtime.go                    # lastGoodTools type
internal/runtime/tool_quarantine.go            # checkToolApprovalsLocked (body without lock); reindexFromLastGoodSnapshot uses seq
internal/runtime/supervisor/supervisor.go      # drop retained Tools for disabled/quarantined servers in reconcile + StateView (design review r9 #3)
internal/toolreview/errors.go                  # ErrReviewNotRecorded (FR-018)
internal/httpapi/*                             # 503 mapping for ErrReviewNotRecorded
internal/runtime/tool_approval_lock.go         # (also) reviewDirtyAt atomic mirror + ToolReviewDirty accessor (FR-018 dispatch refusal)
internal/server/tool_gate.go                   # reviewDirty field (orthogonal to class) in evaluateExactToolGate; dispatch-only callable() false while dirty (spec review r1 #1)
internal/server/mcp.go, mcp_code_execution.go  # dispatch refusal builders: ErrReviewNotRecorded text, zero upstream calls
internal/server/mcp_direct_callability.go      # decision.reviewDirty, NOT folded into callable (listing/call-time filter/describe); new directRefusalKind (spec review r2 #1)
internal/server/mcp_routing.go                 # direct dispatch block answers the dirty refusal
internal/server/access_chain.go                # explainer reports the dirty verdict
internal/server/server.go                      # Server.UpdateServer PATCH quarantined/enabled via persistServerIneligibleLocked (T309)
scripts/check-quarantine-writers.sh            # CI guard (T309)
```

## Phasing and merge order

| Order | PR | Size (est.) | Risk | Reviewer (CLAUDE.md routing) |
|---|---|---|---|---|
| 1 | PR-1 #1567 | S (~300 LOC + tests) | low: additive bucket, OR-ed predicate | opencode Sol 6.1 (default reviewer, memory `default-reviewer-opencode-sol61`) |
| 2 | PR-2 #1566 | M (~900 LOC + tests) | medium: security approval path restructured | opencode Sol 6.1; codex Sol for the commit section if Sol keeps missing (security) |
| 3 | PR-3 #1565 | L (~1,200 LOC + tests) | high: lock scope widened across index/StateView writes | opencode Sol 6.1 + an Opus subagent review (concurrency) |

Each PR:
- is cut from the then-current `origin/main` (memory `merge-early-dont-stack`);
- carries its own failing-first tests;
- includes a live harness check (quickstart.md L1/L2/L3);
- includes docs and a CLAUDE.md "Recent Changes" line;
- leaves unbound clients working.

PR-2 is cut after PR-1 merges. PR-3 can be cut at any point; if it lands before PR-2, PR-2 must call its storage flip through the locked section that PR-3 also uses for the quarantine purge (T309), which is a mechanical rebase.

## Risks and mitigations

| Risk | Mitigation |
|---|---|
| Annotation flapping servers (`tool_quarantine.go:27-31`) cause repeated 409s on review-bound approvals | The fingerprint covers only four boolean hints. Two reloads bound the cost. The 409 names the tools. Telemetry-free log at Info with the tool names |
| Deadlock from the wider lock scope (PR-3) | Lock-order contract plus a test: a `-race` stress test with concurrent approvals, sweeps, quarantine toggles and reindex, using goroutine-dump assertions on timeout (`tool_quarantine_concurrency_test.go` pattern) |
| Supervisor publish under the approval lock | T305 asserts the publish path is non-reentrant. If it is not, publish moves after unlock behind a per-server `published` CAS (fallback design noted in research D10) |
| Blind-approval UX change (A6/O1) | Copy change (FR-026). Release note. Decision recorded as O1 for the lead |
| Old trays approving blind | Accepted, with later tools held (safer). Documented in the compatibility matrix |
