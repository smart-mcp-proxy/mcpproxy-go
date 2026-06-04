# Implementation Plan: Activity-Log Size-Based Retention Cap

**Branch**: `073-activity-size-retention` | **Date**: 2026-06-04 | **Spec**: [spec.md](./spec.md)
**Input**: Feature specification from `/specs/073-activity-size-retention/spec.md`

## Summary

Add a configurable **total-size cap** for the activity log so the local BBolt database (`config.db`) cannot grow unbounded between the existing age and count caps. Implement a new storage method `PruneActivitiesToSize(maxBytes)` that deletes oldest-first until the `activity_records` bucket's stored bytes are within budget, wire it into the existing periodic retention cleanup (`ActivityService.runRetentionCleanup`) alongside the age/count prunes, and expose a new config field `activity_max_size_mb` (default 256, 0 = disabled). Test-driven; verified with `go test`.

## Technical Context

**Language/Version**: Go 1.24 (toolchain go1.24.10)
**Primary Dependencies**: `go.etcd.io/bbolt` (store), `go.uber.org/zap` (logging) — all existing; no new deps
**Storage**: BBolt `config.db`, bucket `activity_records` (key = ULID/timestamp-ordered via `activityKey`)
**Testing**: `go test` (stdlib + testify), existing `internal/storage` and `internal/runtime` suites
**Target Platform**: All editions (personal + server); backend only — no frontend/macOS changes
**Project Type**: single (Go backend)
**Performance Goals**: Pruning runs on the existing background retention cadence (default hourly); must not block request handling. Oldest-first deletion via a forward bucket cursor is O(deleted).
**Constraints**: Must preserve existing age/count behavior; must always keep the newest record; 0/negative cap = disabled. No new background service.
**Scale/Scope**: Targets logs of ~100k records / hundreds of MB; single new storage method + one config field + one call site + tests.

## Constitution Check

*GATE: must pass before and after design.*

- **I. Performance at Scale**: PASS — pruning is on the background retention loop, not the hot path; cursor-based oldest-first delete is linear in deletions only. No added per-request cost.
- **II. Actor-Based Concurrency**: PASS — reuses the existing single retention goroutine + `Manager` mutex around the BBolt write txn; no new shared state.
- **III. Configuration-Driven Architecture**: PASS — new behavior is a config field with a safe default and a disable value (0), persisted in both file and DB config like the sibling `activity_*` fields.
- **IV. Security by Default**: PASS — default cap is ON (bounds disk by default); never deletes the newest record; no change to what is logged.
- **V. Test-Driven Development**: PASS — storage method and retention wiring are covered by tests written before implementation (see tasks.md).
- **VI. Documentation Hygiene**: PASS — new config field documented in `docs/configuration.md` + the activity/feature docs; spec/plan/tasks under `specs/073-*`.

No violations → Complexity Tracking not required.

## Project Structure

### Documentation (this feature)

```text
specs/073-activity-size-retention/
├── spec.md              # Feature spec
├── plan.md              # This file
├── research.md          # Phase 0 — key/size mechanics decisions
├── data-model.md        # Phase 1 — config field + entity notes
├── quickstart.md        # Phase 1 — how to configure + verify
└── tasks.md             # Phase 2 (/speckit.tasks)
```

### Source Code (repository root)

```text
internal/
├── config/
│   └── config.go                  # + ActivityMaxSizeMB field (json/mapstructure) + default 256
├── storage/
│   ├── activity.go                # + PruneActivitiesToSize(maxBytes int64) (int, error)
│   └── activity_size_test.go      # NEW — TDD for the prune method (size, oldest-first, keep-newest, disabled)
└── runtime/
    ├── activity_service.go        # plumb maxSizeBytes from config; call in runRetentionCleanup
    └── activity_service_test.go   # + retention wiring test (size cap removes oldest, keeps newest)

docs/
├── configuration.md               # document activity_max_size_mb
└── features/...                   # activity/retention note
```

**Structure Decision**: Single Go backend. Changes are confined to three existing files plus one new test file and docs. No new packages, services, or schema/bucket changes — the cap is a new prune pass over the existing `activity_records` bucket.

## Phase 0 — Research (decisions)

See `research.md`. Key decisions:
1. **Size measurement** — sum of `len(key)+len(value)` over the bucket (the operator-facing "data size" budget), measured inside the same read/write txn. Not the file size (file shrink = compaction, out of scope).
2. **Oldest-first deletion** — forward-iterate the bucket with a cursor (keys are ULID/timestamp-ordered ascending) deleting until running total of *remaining* bytes ≤ budget; always retain the newest record (never delete the last/most-recent key).
3. **Where to enforce** — inside `runRetentionCleanup`, after age+count prunes, so it acts on the already-trimmed set. Also runs at startup via the existing initial cleanup call.
4. **Config shape** — `activity_max_size_mb int` mirroring `activity_max_records` (json + mapstructure tags, default in `DefaultConfig`, 0 disables).

## Phase 1 — Design (artifacts)

- `data-model.md` — the new config field and the (unchanged) activity record/bucket; no schema migration.
- `quickstart.md` — configure `activity_max_size_mb`, observe pruning in logs, verify with `go test`.
- No API contracts: this is internal retention behavior with no new REST/MCP surface. (The existing `/api/v1/activity` read endpoints are unaffected.)
