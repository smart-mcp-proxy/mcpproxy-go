# Feature Specification: Activity-Log Size-Based Retention Cap

**Feature Branch**: `073-activity-size-retention`
**Created**: 2026-06-04
**Status**: Draft
**Input**: Bound the local database's unbounded growth from the activity log by adding a total-size retention cap, complementing the existing age- and count-based caps.

## Why (Problem)

The local activity log (a record of every tool call, policy decision, and quarantine change) is retained only by **age** (default 90 days) and **record count** (default 100,000). Because each record can carry a large request/response payload (truncated at 64 KB), the log can grow to **hundreds of megabytes while staying well under both caps** — on a real instance it reached ~438 MB across ~93,000 records, the single largest contributor to a ~1 GB local database. Operators have no way to put a predictable ceiling on how much disk the activity log consumes.

## User Scenarios & Testing

### User Story 1 — Operator bounds activity-log disk use (Priority: P1)

An operator running mcpproxy long-term wants assurance that the activity log will never silently consume more than a chosen amount of disk, regardless of how chatty their tools are.

**Why this priority**: This is the core value — a predictable disk ceiling. Without it, the database grows unbounded between the (generous) age/count limits.

**Acceptance Scenarios**:

1. **Given** a configured size cap of 256 MB and an activity log currently at ~440 MB, **When** the periodic retention cleanup runs, **Then** the oldest activity records are removed until the log's data is at or below 256 MB, and the most recent records are preserved.
2. **Given** a size cap and an activity log already under it, **When** retention runs, **Then** no records are removed by the size cap.
3. **Given** the size cap is left at its default, **When** mcpproxy starts and runs retention, **Then** the cap is enforced without any configuration change required.

### User Story 2 — Operator tunes or disables the cap (Priority: P2)

An operator wants to raise, lower, or disable the size cap to fit their environment (e.g., a compliance setup that keeps more history on a large disk).

**Acceptance Scenarios**:

1. **Given** the operator sets the size cap to a larger value, **When** retention runs, **Then** more history is retained up to the new bound.
2. **Given** the operator disables the size cap (sets it to 0), **When** retention runs, **Then** only the existing age and count caps apply and the size cap removes nothing.

### User Story 3 — Newest activity is always retained (Priority: P1)

When the cap forces deletions, an operator inspecting recent activity (debugging a failed tool call) must still find the most recent records.

**Acceptance Scenarios**:

1. **Given** the cap deletes to fit the budget, **When** the operator lists recent activity, **Then** the newest records are present and only the oldest were removed.

### Edge Cases

- A single record larger than the entire budget: the log is reduced to at most the newest record(s); the cap always keeps the newest record rather than emptying the log.
- Size cap stricter than the count/age caps, or vice versa: all caps apply; the most aggressive one wins for a given record.
- Cap set to 0 or negative: treated as "disabled" — no size-based deletion.
- Empty activity log: retention is a no-op.

## Requirements

### Functional Requirements

- **FR-001**: The system MUST support a configurable maximum total size for the activity log, expressed in megabytes.
- **FR-002**: The size cap MUST have a sensible default that bounds the log without configuration (the database must not silently exceed a predictable size from activity records alone).
- **FR-003**: During the existing periodic retention cleanup (and at startup), the system MUST remove the **oldest** activity records until the activity log's data is at or below the configured size budget.
- **FR-004**: The size cap MUST run alongside the existing age and count caps; enabling it MUST NOT disable or weaken them.
- **FR-005**: The system MUST always preserve the newest activity records; size-based deletion removes oldest-first.
- **FR-006**: Setting the cap to 0 (or negative) MUST disable size-based deletion (age/count caps still apply).
- **FR-007**: The system MUST log how many records the size cap removed when it removes any (operator visibility).
- **FR-008**: Enforcing the cap MUST NOT block or noticeably delay normal request handling (it runs on the existing background retention cadence).

### Key Entities

- **Activity record**: one logged event (tool call, policy decision, quarantine change) with a timestamp-ordered key and a payload of variable size.
- **Activity log**: the ordered collection of activity records subject to retention.
- **Retention policy**: the combined set of caps applied to the activity log — age, count, and (new) total size.

## Success Criteria

- **SC-001**: After retention runs with a size cap of N MB on a log larger than N MB, the activity log's data is at or below N MB.
- **SC-002**: The newest activity records are always retained after size-based pruning (0 cases of newest-record loss in tests).
- **SC-003**: With the default configuration and no operator action, the activity log cannot grow the local database past a predictable, documented bound.
- **SC-004**: Disabling the cap (0) results in zero size-based deletions; the prior age/count behavior is unchanged.
- **SC-005**: Enforcing the cap on a large log completes within the background retention cycle and does not interrupt request handling.

## Assumptions

- Activity record keys are timestamp-ordered (ULID-based), so "oldest" is well-defined and oldest-first deletion is efficient.
- A default cap of **256 MB** is a reasonable balance between history depth and disk use; it is documented and operator-tunable.
- "Size" refers to the activity log's stored data size (sum of record bytes), which is what the operator-facing budget governs; reclaiming freed disk pages back to the OS (file compaction) is separate and out of scope.
- The existing periodic retention loop is the correct and only place to enforce the cap (no new background service).

## Out of Scope (Future Extensions)

- A size cap for security scan reports/jobs (already count-capped per server) — a separate follow-up.
- BBolt file compaction to return freed pages to the OS after deletions — a separate follow-up (deletions free pages for reuse but do not shrink the file).
- Per-server or per-type activity quotas.
