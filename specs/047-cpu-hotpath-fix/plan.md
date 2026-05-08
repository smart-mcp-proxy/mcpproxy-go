# Implementation Plan: CPU Hot-Path Fix

**Branch**: `047-cpu-hotpath-fix` | **Date**: 2026-05-08 | **Spec**: [`spec.md`](./spec.md)
**Input**: [`specs/047-cpu-hotpath-fix/spec.md`](./spec.md)

## Summary

Cut sustained CPU on the steady-state tray-poll path from ~19% to <2% by:

1. **A1** — caching the "no scans found" sentinel in `internal/security/scanner/service.go` so untouched servers stop re-triggering the full scan-job bucket scan in BBolt.
2. **B1** — embedding the server list and stats in the `servers.changed` SSE event payload (`internal/runtime/event_bus.go`) so subscribers (Swift tray, Web UI) consume state directly instead of round-tripping `GET /api/v1/servers`.
3. **B2** — coalescing `servers.changed` bursts in the runtime via a single `pending atomic.Pointer[Event]` + 50 ms drainer goroutine, last-write-wins semantics.

Verified by re-running the same MCPProxy.app + 30-server pprof scenario that produced the original 47% / 60s CPU profile and asserting `bbolt.(*DB).View` cum < 5% and cumulative-cputime delta < 2 s over 60 s wall.

## Technical Context

**Language/Version**: Go 1.24 (toolchain go1.24.10); Swift 5.9 (macOS 13+); TypeScript 5.9 / Vue 3.5 (frontend)
**Primary Dependencies**: `go.etcd.io/bbolt` (existing), `go.uber.org/zap` (existing), `github.com/mark3labs/mcp-go` (existing). No new deps.
**Storage**: BBolt (`~/.mcpproxy/config.db`) — read-only on the hot path; no schema change.
**Testing**: `go test -race`, XCTest (Swift), Vitest + Playwright (frontend).
**Target Platform**: macOS 13+ (Personal edition); Linux/Windows (Personal); Linux server (Server edition).
**Project Type**: single project (multi-target — Go core + Swift tray + Vue frontend, all in one repo).
**Performance Goals**: <2 s of CPU consumed over a 60 s wall window when only the tray is polling. `bbolt.(*DB).View` cum-CPU < 5%. SSE event arrives within 50 ms of state change.
**Constraints**: No BBolt migration. No public API contract change. SSE payload growth ≤ 50 KB per event for ≤ 50-server installs. Backward-compatible with older clients (notify-only fallback).
**Scale/Scope**: 30+ configured upstreams typical; 1.06 GB BBolt DB observed in the wild; 91 k+ activity records.

## Constitution Check

| Principle | Status | Notes |
|---|---|---|
| **I. Performance at Scale** | Reinforced | This entire feature exists to enforce this principle. Removes an O(N) JSON-decode hot path from the steady-state poll. |
| **II. Actor-Based Concurrency** | Aligned | The coalescer is a single owner-goroutine pattern: one drainer owns the publish path; producers communicate via `atomic.Pointer` swap (channel-equivalent for "latest value"). No new shared mutable state behind locks. |
| **III. Configuration-Driven Architecture** | No conflict | No new config keys; behavior is purely a CPU optimization. Hot-reload unaffected. |
| **IV. Security by Default** | No regression | Sensitive header redaction in the SSE payload uses the existing `redactServerHeaders` path. API-key gating on `/events` unchanged. The scanner cache only stores public summary data, not findings. |
| **V. TDD** | Required | Each sub-task lands with a failing test first (see `tasks.md`). |
| **VI. Documentation Hygiene** | Aligned | Spec + plan + tasks + research + verification artifacts committed alongside the code. |

No violations. No entries in Complexity Tracking.

## Project Structure

### Documentation (this feature)

```text
specs/047-cpu-hotpath-fix/
├── spec.md
├── plan.md                ← this file
├── research.md            ← Phase 0
├── data-model.md          ← Phase 1
├── quickstart.md          ← Phase 1
├── contracts/
│   └── sse-events.md      ← Phase 1: SSE event payload contract
├── tasks.md               ← Phase 2 (speckit.tasks)
└── verification/
    ├── cpu_post.pb.gz
    ├── cputime_delta.txt
    └── report.html
```

### Source Code

```text
internal/
├── security/scanner/
│   └── service.go              ← A1: cache nil sentinel; errNoScans
├── runtime/
│   ├── event_bus.go            ← B1+B2: embed payload, coalescer
│   ├── runtime.go              ← B2: hold coalescer state on Runtime
│   └── events.go               ← (unchanged: EventTypeServersChanged constant)
├── storage/
│   └── scanner.go              ← (unchanged in this PR)
└── httpapi/
    └── server.go               ← (unchanged: SSE writer is payload-agnostic)

native/macos/MCPProxy/MCPProxy/
├── Core/CoreProcessManager.swift   ← consume embedded payload, refresh fallback
└── API/Models.swift                ← already has matching Server struct

frontend/src/
├── stores/                          ← consume embedded payload, refetch fallback
└── composables/useEventStream.ts    ← decode embedded payload

specs/047-cpu-hotpath-fix/
└── verification/                    ← post-fix pprof + screenshots
```

**Structure Decision**: Single repo with multi-target sub-trees. Changes are concentrated in three Go files plus their tests, plus one decode-from-payload branch in each of two clients (Swift tray, Vue Web UI).

## Phase 0: Research (research.md)

All decisions were resolved during brainstorming on 2026-05-08. No `NEEDS CLARIFICATION` items remain. The `research.md` document records each decision, rationale, and alternatives considered.

## Phase 1: Design & Contracts

- **`data-model.md`** — declares the in-memory shape of the `serversChangedCoalescer` and the new payload structure for `servers.changed`. No persistent storage changes.
- **`contracts/sse-events.md`** — formal contract for the `servers.changed` SSE event after this change, plus the legacy notify-only fallback contract that older clients will continue to consume.
- **`quickstart.md`** — exact reproduction recipe (build mcpproxy, launch tray, capture pprof, run unit tests, run Playwright sweep).
- **Agent context update** — `.specify/scripts/bash/update-agent-context.sh claude` runs at the end of plan generation to add this feature's stack to the project context.

## Phase 2: Tasks (tasks.md)

Generated by `/speckit.tasks` from this plan. Each implementation step is paired with a preceding test step per Constitution V.

## Risks (mirrored from spec)

See [`spec.md`](./spec.md) → "Risks & Mitigations". No new risks identified during planning.

## Out of Scope (mirrored from spec)

In-memory `(serverName, pass) → latestJobID` index; activity log retention/pagination; bucket re-keying; `encoding/json` replacement; PGO build. All deferred to follow-up specs.

## Complexity Tracking

(empty — no Constitution gate violations)
