# Tasks: Activity-Log Size-Based Retention Cap

**Feature**: `073-activity-size-retention` | **Spec**: [spec.md](./spec.md) | **Plan**: [plan.md](./plan.md)
**Approach**: Test-driven (tests written before implementation).

## Phase 1: Setup

- [ ] T001 Confirm branch `073-activity-size-retention` is checked out and `go build ./...` is green before starting.

## Phase 2: Foundational (config field)

- [ ] T002 Add `ActivityMaxSizeMB int` field with `json:"activity_max_size_mb,omitempty"` and `mapstructure:"activity-max-size-mb"` to the config struct in `internal/config/config.go`, next to the existing `Activity*` fields.
- [ ] T003 Set the default `ActivityMaxSizeMB: 256` in `DefaultConfig()` in `internal/config/config.go` (alongside `ActivityMaxRecords: 100000`).

## Phase 3: User Story 1 — Bound activity-log disk use (P1) [MVP]

**Goal**: A size cap removes oldest records until the log is within budget.
**Independent test**: seed a bucket over budget, prune to size, assert remaining bytes ≤ budget and oldest were removed.

- [ ] T004 [P] [US1] Write failing test `TestPruneActivitiesToSize_RemovesOldestUntilUnderBudget` in `internal/storage/activity_size_test.go`: save N activity records with known payload sizes (oldest→newest timestamps), call `PruneActivitiesToSize(budget)`, assert summed remaining bytes ≤ budget and the removed records are the oldest.
- [ ] T005 [US1] Implement `func (m *Manager) PruneActivitiesToSize(maxBytes int64) (int, error)` in `internal/storage/activity.go`: in one `Update` txn, forward-cursor the `activity_records` bucket computing total `len(k)+len(v)`; if total ≤ maxBytes return 0; else delete oldest-first, subtracting each from total, stopping when total ≤ maxBytes; never delete the final (newest) remaining key. Return count deleted. Guard `maxBytes <= 0` → no-op returns (0, nil).
- [ ] T006 [P] [US1] Write test `TestPruneActivitiesToSize_AlreadyUnderBudget_NoOp` in `internal/storage/activity_size_test.go`: log under budget → returns 0, all records intact.

## Phase 4: User Story 3 — Newest activity always retained (P1)

**Goal**: deletion never removes the newest record, even if a single record exceeds the budget.
**Independent test**: one record larger than the budget → that newest record survives.

- [ ] T007 [P] [US3] Write test `TestPruneActivitiesToSize_KeepsNewestEvenIfOverBudget` in `internal/storage/activity_size_test.go`: seed records where the newest single record exceeds `maxBytes`; assert the newest record is retained and older ones removed (log not emptied).

## Phase 5: User Story 2 — Tune / disable the cap (P2)

**Goal**: cap of 0 disables size pruning; larger cap retains more.
**Independent test**: `PruneActivitiesToSize(0)` deletes nothing.

- [ ] T008 [P] [US2] Write test `TestPruneActivitiesToSize_DisabledWhenZeroOrNegative` in `internal/storage/activity_size_test.go`: `PruneActivitiesToSize(0)` and `(-1)` return (0, nil) with all records intact.

## Phase 6: Retention wiring (runtime)

- [ ] T009 [US1] Plumb the size budget into `ActivityService`: add a `maxSizeBytes int64` field set from `ActivityMaxSizeMB * 1024 * 1024` where the service is constructed, in `internal/runtime/activity_service.go` (find the constructor that already reads `maxAge`/`maxRecords`).
- [ ] T010 [US1] In `runRetentionCleanup` (`internal/runtime/activity_service.go`), after the age and count prunes, call `PruneActivitiesToSize(s.maxSizeBytes)` when `s.maxSizeBytes > 0`; on success with deleted > 0 log `Info("Pruned activity records to size budget", deleted, max_size_mb)`; on error log `Error`.
- [ ] T011 [P] [US1] Write/extend a runtime test in `internal/runtime/activity_service_test.go` (`TestActivityRetention_SizeCapRemovesOldest`): construct the service with a small `maxSizeBytes`, seed an oversized log, run `runRetentionCleanup`, assert the log is within budget and newest records remain.

## Phase 7: Polish & Docs

- [ ] T012 [P] Document `activity_max_size_mb` (default 256, 0 = disabled, runs with age/count caps) in `docs/configuration.md` and the activity/retention docs.
- [ ] T013 Run the full local verification: `go test ./internal/storage/ ./internal/runtime/ ./internal/config/` and `go build ./cmd/mcpproxy`; ensure green.
- [ ] T014 Run `gofmt`/`goimports` and `./scripts/run-linter.sh` on changed files.

## Dependencies & Order

- Phase 2 (config field) blocks T009 (wiring reads the field).
- T005 (implementation) unblocks all storage tests passing; tests T004/T006/T007/T008 are written first (TDD) and fail until T005.
- Phase 6 (wiring) depends on T005 + Phase 2.
- T013/T014 last.

## Parallel Opportunities

- T004, T006, T007, T008 are all in the same new test file — write together, but they only pass after T005.
- T012 (docs) is independent and can be done anytime after the config field exists.

## MVP

User Story 1 (T002–T006 + T009–T011) delivers the core: a default-on size cap that bounds the activity log. US2/US3 are small additions to the same method.
