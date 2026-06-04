# Phase 1 Data Model

No schema/bucket changes. One new configuration field; the activity record and bucket are unchanged.

## Config field (new)

| Field | Type | JSON / mapstructure | Default | Meaning |
|-------|------|---------------------|---------|---------|
| `ActivityMaxSizeMB` | `int` | `activity_max_size_mb` / `activity-max-size-mb` | `256` | Max total stored size of the activity log in MB. `0` (or negative) disables size-based pruning. Applied alongside `ActivityRetentionDays` and `ActivityMaxRecords`. |

Lives next to the existing activity settings in `internal/config/config.go`:
`ActivityRetentionDays`, `ActivityMaxRecords`, `ActivityMaxResponseSize`, `ActivityCleanupIntervalMin`.

## Entities (unchanged)

- **ActivityRecord** (`internal/storage/activity_models.go`): `ID` (ULID), `Timestamp`, type, payload fields. Stored in bucket `activity_records` under key `activityKey(timestamp, id)` — timestamp-ordered ascending.
- **Activity log**: the `activity_records` bucket.

## Derived value

- **Bucket data size** = Σ `len(key)+len(value)` over `activity_records`. Computed transiently during pruning; not persisted.

## Retention policy (combined)

Applied in order each cleanup cycle:
1. Age cap — `PruneOldActivities(maxAge)` (existing)
2. Count cap — `PruneExcessActivities(maxRecords, 0.9)` (existing)
3. **Size cap — `PruneActivitiesToSize(maxBytes)` (new)** — `maxBytes = ActivityMaxSizeMB * 1024 * 1024`; skipped when `≤ 0`.
