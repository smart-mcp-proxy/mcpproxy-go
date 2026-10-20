# Feature Specification: Automatic rescan after a tool-definition change

**Feature Branch**: `118-auto-rescan-on-change`
**Status**: Implemented in one PR
**Input**: Maintainer UI feedback 2026-10-20: after a tool definition hash changes the review screen shows
"Scan out of date: N tool definitions changed after the last scan … Rescan", and the held tool has no scan
verdict, because the automatic scan only runs on server admission and in a one-shot startup sweep, and the
synchronous per-tool TPA scan only runs under `trust_mode: scan`.

## Invariant (RT-02, decision 2026-10-11) — NOT changed by this feature

A changed tool contract is **always held for review** in `scan` and `manual` trust modes. A clean verdict
**never** approves a change. This feature adds **evidence** (a per-tool verdict) and **freshness** (a current
server scan) only. No approval decision, status transition or gate is altered.

## User Stories

### US1 — A changed or newly pending tool carries a scan verdict (P1)
As an operator reviewing a held tool in `manual` or `scan` trust mode, I see the in-process TPA scan verdict
for the tool's *current* definition without clicking anything.

- **FR-001** When discovery marks a tool `changed` (first approved→changed transition) or refreshes a
  still-changed record to a newer definition, the in-process scan of the new definition runs in `manual` mode
  as it already does in `scan` mode, and its result is recorded on the approval record (`held_reason`,
  `held_verdict`, `held_signals`).
- **FR-002** A newly added `pending` tool in `manual` mode is scanned the same way, so the review list shows a
  per-tool verdict instead of `not_scanned`/unknown. A pending record whose definition moves, or that has no
  verdict yet, is rescanned on the next discovery pass.
- **FR-003** A **clean** verdict is recorded as `held_reason: awaiting_review`, `held_verdict: clean`, no
  signals. It means "the scan found nothing; the hold is the review requirement itself" and is distinct from
  `scan_findings` (threat) and `scan_coverage` (scan could not complete, precaution). The record's status is
  unchanged: still `changed`/`pending`, still blocked.
- **FR-004** `awaiting_review` does not widen the review screens' default selection: a tool carrying any
  `held_reason`, `awaiting_review` included, starts unchecked, as before the verdict was recorded. The
  in-process verdict is defeatable by paraphrase (RT-02) and must not pre-check anything.
- **FR-005** `trust_mode: auto` is untouched (changes auto-approve; nothing is held).

### US2 — The server scan is current again after a change (P1)
- **FR-006** When a server's tool inventory hash set changes after admission (tool changed, or added after the
  baseline), a debounced informational server rescan runs so the review screen's scan coverage returns to
  `current` instead of `stale`. Changes arriving within the debounce window coalesce into one rescan per server;
  a change that lands while a rescan is queued or running schedules one more.
- **FR-007** It reuses the informational scan machinery: per-server claim, serialization behind the same run
  mutex, and the `maxInformationalScanAttempts` failure cap. It is informational: no gating, no approvals.
- **FR-008** Servers the scan-mode admission gate owns are skipped while quarantined, as for admission scans.
  Disabled servers and `trust_mode: auto` servers are skipped. Removing a server cancels its pending rescan.

### US3 — Switches (P1)
- **FR-009** Both behaviours (FR-001/002 in manual mode, FR-006) are gated on `security.auto_baseline_scan`
  AND a new finer switch `security.auto_rescan_on_change` (`*bool`, default on, env
  `MCPPROXY_AUTO_RESCAN_ON_CHANGE`, env wins on every path; hot-reloadable). With either off, nothing runs
  automatically (manual scans keep working). The `trust_mode: scan` gate's own admission-time scan of new tools
  is a gate, not an automatic scan, and is unaffected by either switch.

## Out of scope
Changing any approval rule; deep (Docker) scanners; the `auto` trust mode.

## Acceptance
A `manual`-mode server whose tool description changes shows, with no click, a per-tool verdict on the review
screen and a server scan line reading `current`; with `auto_rescan_on_change: false` nothing runs.
