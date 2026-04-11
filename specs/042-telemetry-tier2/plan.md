# Implementation Plan: Telemetry Tier 2 — Privacy-Respecting Usage Signals

**Branch**: `042-telemetry-tier2` | **Date**: 2026-04-10 | **Spec**: [spec.md](./spec.md)
**Input**: Feature specification from `specs/042-telemetry-tier2/spec.md`

## Summary

Extend the existing `internal/telemetry` package with a thread-safe `CounterRegistry` that aggregates twelve new privacy-respecting signals into the existing daily heartbeat. New signals: surface request counters (mcp/cli/webui/tray/unknown), built-in tool histogram with bucketed upstream-tool total, REST endpoint histogram (Chi route templates), feature-flag adoption snapshot, last startup outcome, upgrade funnel (`previous_version` → `current_version`), error category histogram, annual anonymous-ID rotation, doctor check pass/fail counts, plus three privacy/transparency controls (`DO_NOT_TRACK` and `CI` env handling, `mcpproxy telemetry show-payload` Cobra command, first-run notice). The implementation is purely additive to v1: existing fields, endpoint, opt-out commands, and tests remain unchanged. Counters are in-memory only and reset only on a successful 2xx send. The macOS tray Swift HTTP client gets a one-line `X-MCPProxy-Client: tray/<version>` header addition; CLI and web UI HTTP clients get the equivalent.

## Technical Context

**Language/Version**: Go 1.24 (toolchain go1.24.10) — primary; Swift 5.9 — macOS tray header change only
**Primary Dependencies**: `github.com/google/uuid` (existing), `github.com/go-chi/chi/v5` (existing, for `RoutePattern()`), `github.com/spf13/cobra` (existing, new subcommand), `go.uber.org/zap` (existing), stdlib `sync/atomic`, `sync`, `os`
**Storage**: Config file `~/.mcpproxy/mcp_config.json` only — counters live in memory and are never persisted between restarts (privacy constraint). No BBolt buckets, no new files.
**Testing**: `go test ./internal/telemetry/... -race` for unit; `./scripts/test-api-e2e.sh` for E2E; existing `internal/server/e2e_test.go` smoke verifies builds.
**Target Platform**: All platforms mcpproxy supports (linux/darwin/windows × amd64/arm64). Personal and server editions both must build clean.
**Project Type**: single project — Go module with internal packages
**Performance Goals**: Zero observable overhead from telemetry on hot paths. Counter increment is one atomic add or a single short-locked map write. Heartbeat render and send happens once per 24 hours in a goroutine. Payload size <8 KB.
**Constraints**:
- No raw URLs, no upstream tool names, no error messages, no path strings in payload.
- All counter mutations safe under `-race`.
- No outbound network calls when `DO_NOT_TRACK` or `CI` is set.
- Heartbeat goroutine MUST NOT block startup or shutdown beyond the existing 10s HTTP timeout.
- Personal edition (`go build ./cmd/mcpproxy`) and server edition (`go build -tags server`) must both compile.

**Scale/Scope**: ~1000 unique installs anticipated. ~7 built-in tool counter keys. ~30 REST endpoint counter keys (templated). 11 error category constants. 5 surface enum values. Single payload <8 KB.

## Constitution Check

*Gate evaluated against `.specify/memory/constitution.md` v1.1.0.*

| Principle | Status | Notes |
|---|---|---|
| **I. Performance at Scale** | ✅ PASS | Counter increments are O(1) atomic ops or short-locked map writes. Heartbeat work runs in a goroutine. No new BM25 / index work. No memory leaks: counters reset on flush. |
| **II. Actor-Based Concurrency** | ⚠️ JUSTIFIED | Counter registry uses `sync.RWMutex` (for the few maps that can't be pure atomics) plus `atomic.Int64` for the surface counters. Constitution says locks must be "proven necessary". Justification: a counter registry with ~7 maps is not a hot enough path to need a goroutine + channel actor (would add inbox latency on every tool call), and `RWMutex` gives lock-free reads on render. Documented in Complexity Tracking. |
| **III. Configuration-Driven Architecture** | ✅ PASS | New fields (`anonymous_id_created_at`, `last_reported_version`, `last_startup_outcome`, `telemetry_notice_shown`) go in the existing `telemetry` config struct. No tray-side state. |
| **IV. Security by Default** | ✅ PASS | Adds `DO_NOT_TRACK` and `CI` env vars (privacy-strengthening). Adds `show-payload` transparency command. Does not weaken any existing security control. Privacy constraints in spec are stricter than v1. |
| **V. Test-Driven Development** | ✅ PASS | Every sub-task in `tasks.md` will follow red-green: failing test → impl → green. Race detection mandatory on all counter tests. |
| **VI. Documentation Hygiene** | ✅ PASS | `docs/features/telemetry.md`, `CLAUDE.md` (Telemetry CLI section), and the heartbeat schema doc all updated as part of the implementation tasks. |

**Verdict**: PASS (with one justified exception in Complexity Tracking).

## Project Structure

### Documentation (this feature)

```text
specs/042-telemetry-tier2/
├── plan.md              # This file
├── spec.md              # Feature specification
├── research.md          # Phase 0: technical research and decisions
├── data-model.md        # Phase 1: entities, schemas, state transitions
├── quickstart.md        # Phase 1: how to build, run, verify
├── contracts/           # Phase 1: payload schema as contract
│   └── heartbeat-v2.schema.json
├── checklists/
│   └── requirements.md  # Spec quality checklist (already passes)
└── tasks.md             # Phase 2 output (NOT created here — /speckit.tasks creates it)
```

### Source Code (repository root)

```text
internal/
├── telemetry/
│   ├── telemetry.go              # MODIFY: extend Service, payload schema, lifecycle
│   ├── telemetry_test.go         # MODIFY: cover new payload fields
│   ├── feedback.go               # UNCHANGED
│   ├── registry.go               # NEW: CounterRegistry (counters + Snapshot/Reset)
│   ├── registry_test.go          # NEW: race-safe counter unit tests
│   ├── error_categories.go       # NEW: ErrorCategory enum + RecordError function
│   ├── error_categories_test.go  # NEW: enum validation, unknown-category drop
│   ├── feature_flags.go          # NEW: FeatureFlagSnapshot from *config.Config
│   ├── feature_flags_test.go     # NEW
│   ├── id_rotation.go            # NEW: annual anonymous-ID rotation logic
│   ├── id_rotation_test.go       # NEW
│   ├── upgrade_funnel.go         # NEW: previous→current version tracking
│   ├── upgrade_funnel_test.go    # NEW
│   ├── env_overrides.go          # NEW: DO_NOT_TRACK / CI env handling
│   └── env_overrides_test.go     # NEW
│
├── httpapi/
│   ├── middleware.go             # MODIFY: add SurfaceClassifier middleware + REST endpoint counter
│   └── middleware_test.go        # MODIFY/NEW: surface and endpoint classification cases
│
├── server/
│   └── mcp.go                    # MODIFY: increment built-in tool counter and upstream counter
│
├── runtime/
│   └── lifecycle.go              # MODIFY: record last_startup_outcome, wire telemetry to runtime
│
├── doctor/
│   └── doctor.go                 # MODIFY: feed structured results into CounterRegistry after run
│
├── config/
│   └── config.go                 # MODIFY: add new fields to telemetry struct
│
└── oauth/
    ├── coordinator.go            # MODIFY: call RecordError on refresh failure
    └── ...

cmd/mcpproxy/
├── telemetry_cmd.go              # MODIFY: add `show-payload` subcommand; first-run notice
└── serve.go                      # MODIFY: env override check; record startup outcome on exit

cmd/mcpproxy-tray/
└── (existing) — uses cliclient with header set centrally; no changes here

internal/cliclient/
├── client.go                     # MODIFY: set X-MCPProxy-Client: cli/<version> header
└── client_test.go                # MODIFY: assert header present

frontend/
└── src/
    └── api/
        └── client.ts             # MODIFY: include X-MCPProxy-Client: webui/<version> in fetch

native/macos/MCPProxy/
└── (Swift HTTP client file)      # MODIFY: add header to all REST requests

docs/features/telemetry.md        # MODIFY: document new fields, env vars, show-payload, notice
CLAUDE.md                         # MODIFY: telemetry CLI section
```

**Structure Decision**: This is a single Go module project. New code clusters in `internal/telemetry/` (where the existing v1 service lives) plus targeted edits to the integration points (`httpapi/middleware.go`, `server/mcp.go`, `runtime/lifecycle.go`, `doctor/doctor.go`, `cmd/mcpproxy/telemetry_cmd.go`, `cmd/mcpproxy/serve.go`, `internal/cliclient/client.go`, `frontend/src/api/client.ts`, and one Swift file in `native/macos/MCPProxy/`). No new top-level packages. No new storage.

## Phase 0: Research (consolidated)

All technical unknowns are resolved in [research.md](./research.md). Summary of decisions:

1. **Counter primitive choice** — `atomic.Int64` for fixed-cardinality counters (surfaces, upstream tool count); `sync.RWMutex` + `map[string]int64` for variable-cardinality counters (built-in tools, REST endpoints, error categories, doctor checks). Justification: atomics for hot paths, locked map only for the 5–30 element maps that change rarely.
2. **Surface classification location** — Chi middleware on the `/api/v1` router, executed before the route handler so that the classification happens regardless of which handler runs. The `/mcp` paths are handled by a separate mux and increment `mcp` directly when an MCP request hits `handleToolCall`.
3. **REST endpoint template extraction** — `chi.RouteContext(r.Context()).RoutePattern()` returns the template string. Must be called *after* the router has matched (i.e., in a deferred middleware that wraps the response writer). For `UNMATCHED` (no route matched at all), Chi returns an empty pattern.
4. **Status class derivation** — wrap `http.ResponseWriter` to capture the status code; convert to class with integer division (`status / 100 * 100` → "2xx" etc.).
5. **Built-in tool detection** — `internal/server/mcp.go` already routes built-in tools through specific handlers; we add `registry.RecordBuiltinTool(name)` at the entry of each handler. For upstream-proxied tool calls (which go through `handleCallToolVariant`), we add `registry.RecordUpstreamTool()` once per call. Both increments happen before any error returns so they always fire.
6. **Doctor integration** — `internal/doctor/doctor.go` already returns structured `CheckResult` slices. We add a single helper `registry.RecordDoctorRun(results []CheckResult)` called from the doctor command and the `/api/v1/doctor` REST handler if it exists.
7. **Env var precedence** — checked once at telemetry service construction in `NewService(cfg, logger)`; the result becomes `service.enabled`. No re-check during runtime — the env vars are not hot-reloadable, matching how the existing config flag works.
8. **First-run notice** — printed by `cmd/mcpproxy/serve.go` immediately after config load, before the telemetry service starts. Persists `telemetry_notice_shown=true` via the existing config save mechanism. Output goes to `os.Stderr` directly (not via zap) so it appears in plain text.
9. **Annual ID rotation** — checked at `Snapshot()` time. If `now.Sub(createdAt) > 365 * 24h && !createdAt.IsZero() && createdAt.Before(now)`, regenerate the ID. The rotation persists immediately to config (synchronously, since snapshots happen rarely).
10. **Upgrade funnel persistence write timing** — write `last_reported_version` to config inside `Service.send()` only after `resp.StatusCode/100 == 2`.
11. **Schema versioning** — heartbeat payload gets a `schema_version: 2` field so the receiver can distinguish v1 from v2 payloads. v1 payloads don't have this field, so absence implies v1.
12. **Swift tray header addition** — modify the file in `native/macos/MCPProxy/` that owns the URLSession HTTP client (research will identify the exact file). Single line change to set the request header on every outgoing REST call.

## Phase 1: Design Artifacts

### Data Model

See [data-model.md](./data-model.md). Key entities:
- **HeartbeatPayloadV2**: extends v1 with 11 new fields. Backward-compatible (purely additive).
- **CounterRegistry**: holds 4 atomic counters (surfaces × 5 + upstream tool total) and 4 RWMutex-protected maps (built-in tools, REST endpoints, error categories, doctor checks).
- **ErrorCategory**: typed string enum, 11 initial constants. Unknown values silently dropped.
- **TelemetryConfig** (extended): 4 new persisted fields.

### Contracts

See [contracts/heartbeat-v2.schema.json](./contracts/heartbeat-v2.schema.json) for the JSON schema. The contract covers:
- All v1 fields (preserved)
- All Tier 2 additions
- Constraints: bucket enum values, surface enum values, status class enum values, error category enum values
- A `forbidden_substrings_test` is implicit: tests will assert that no payload renders ever contains any of `localhost`, `/Users/`, `/home/`, `Bearer `, etc.

### Quickstart

See [quickstart.md](./quickstart.md) for how to build the worktree, run the unit suite, exercise `show-payload`, and verify the new fields end-to-end.

### Agent Context Update

After Phase 1 completes, run `.specify/scripts/bash/update-agent-context.sh claude` to refresh `CLAUDE.md` Active Technologies section with the spec 042 entry.

## Complexity Tracking

| Violation | Why Needed | Simpler Alternative Rejected Because |
|---|---|---|
| `sync.RWMutex` in `registry.go` (Constitution Principle II prefers actors/channels) | The counter registry has 4 maps with cardinality 5–30 each. The increment path is called from every HTTP request, every MCP tool call, and every error site — potentially thousands of times per second under load. Wrapping every increment in a channel send would add inbox latency, increase goroutine count, and be harder to test for races than a short-locked map write. RWMutex permits lock-free reads at render time (once per day), and the write critical section is one map lookup + increment. | An actor goroutine would: (a) require a select on a buffered channel for every increment, with backpressure handling if the inbox is full; (b) require a separate goroutine that the telemetry service must shut down on stop; (c) make rendering harder because the actor would need to serialize a snapshot back through another channel. Mutex is simpler, faster, and the contention surface is tiny (no I/O under lock). |

## Phase 2: Tasks

**NOT generated by this command.** Run `/speckit.tasks` next to produce `tasks.md`.

## Files Generated by This Plan

- `specs/042-telemetry-tier2/plan.md` (this file)
- `specs/042-telemetry-tier2/research.md`
- `specs/042-telemetry-tier2/data-model.md`
- `specs/042-telemetry-tier2/contracts/heartbeat-v2.schema.json`
- `specs/042-telemetry-tier2/quickstart.md`
