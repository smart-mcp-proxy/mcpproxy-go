# Phase 0 Research: Activity-Log Size-Based Retention

## Decision 1 — How to measure "size"

**Decision**: Sum `len(key) + len(value)` over all entries in the `activity_records` bucket, measured inside a BBolt transaction.

**Rationale**: This is the operator-facing "data size" the budget governs and is deterministic and cheap to compute incrementally during a cursor scan. It is independent of BBolt free-page overhead.

**Alternatives considered**:
- *File size (`config.db`)*: rejected — the file also holds every other bucket and never shrinks on delete (that's compaction, out of scope). Not a usable per-bucket budget.
- *`bucket.Stats()` LeafInuse/BranchInuse*: rejected — observed to badly undercount actual value bytes (it omits large inline values), which is exactly the measurement error that hid this problem originally.

## Decision 2 — Deletion order and termination

**Decision**: Forward-iterate the bucket with a cursor (keys are ULID-prefixed by timestamp → ascending = oldest-first). Compute total bytes first; then delete oldest keys, subtracting each from the running total, until total ≤ budget. Always stop before deleting the last (newest) remaining key.

**Rationale**: ULID keys give a stable chronological order, so oldest-first is a single forward pass. Keeping the newest record satisfies FR-005/US3 even when a single record exceeds the budget.

**Alternatives considered**:
- *Delete by count estimate (budget / avg record size)*: rejected — inaccurate with variable payload sizes; could over- or under-delete.

## Decision 3 — Enforcement point

**Decision**: Call `PruneActivitiesToSize` inside `ActivityService.runRetentionCleanup`, **after** the existing age and count prunes. The existing loop already runs an initial cleanup at startup and then every `ActivityCleanupIntervalMin`.

**Rationale**: Reuses the one background goroutine and the established cadence; acts on the already-trimmed record set so it only removes what age/count didn't. No new service, no hot-path cost (FR-008).

## Decision 4 — Config shape and default

**Decision**: New field `activity_max_size_mb int` (json `activity_max_size_mb`, mapstructure `activity-max-size-mb`), default **256**, `0` (or negative) disables size-based pruning. Mirrors the sibling `activity_max_records`.

**Rationale**: Consistent with existing `activity_*` settings (file + DB config, hot-reloadable). 256 MB bounds the dominant contributor while keeping ample history; operators can raise/lower/disable.

**Open questions**: none.
