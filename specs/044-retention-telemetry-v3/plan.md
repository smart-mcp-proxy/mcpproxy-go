# Implementation Plan: Retention Telemetry Hygiene & Activation Instrumentation

**Branch**: `044-retention-telemetry-v3` | **Date**: 2026-04-24 | **Spec**: [spec.md](./spec.md)
**Input**: Feature specification from `/specs/044-retention-telemetry-v3/spec.md`

## Summary

Extend the existing anonymous telemetry payload (already at `SchemaVersion = 3` from spec 042 Tier 2) with five additional fields — `env_kind`, `launch_source`, `autostart_enabled`, `activation`, `env_markers` — that give the dashboard ground-truth environment classification and an activation funnel. Add a BBolt-backed activation bucket, hook the MCP `initialize` handler to record `clientInfo.name`, hook the builtin `retrieve_tools` to bump an activation counter, and set the macOS tray login-item ON by default on first launch. The macOS DMG installer gets a post-install script that launches the tray once with `MCPPROXY_LAUNCHED_BY=installer` so first-heartbeat attribution is accurate. All new fields are covered by unit tests; every new field is validated by a payload-builder self-check that scans the serialized JSON for user-path prefixes, usernames, and env-var-value leaks. Windows tray + installer final-step are explicitly deferred to a follow-up spec.

## Technical Context

**Language/Version**: Go 1.24 (toolchain go1.24.10), Swift 5.9+ (macOS tray only), Bash (DMG post-install script)
**Primary Dependencies**: `go.etcd.io/bbolt` (existing), `go.uber.org/zap` (existing), `github.com/mark3labs/mcp-go` (existing MCP protocol lib), `github.com/google/uuid` (existing). macOS: `ServiceManagement.framework` (SMAppService, macOS 13+), existing `native/macos/MCPProxy` module. No new external dependencies.
**Storage**: BBolt (`~/.mcpproxy/config.db`) — new `activation` bucket alongside existing buckets; no migration required because absence of bucket means "fresh install, all flags false".
**Testing**: `go test -race` for unit tests; `./scripts/test-api-e2e.sh` for HTTP integration; XCTest (or manual via `mcpproxy-ui-test` MCP tools) for Swift tray; `vitest` does not apply (no JS in scope).
**Target Platform**: macOS 13+ (tray + installer changes), Linux + Windows (client telemetry fields only, tray/installer deferred on Windows).
**Project Type**: Single Go project + native macOS tray sub-project. Aligns with existing repo layout — no structural change.
**Performance Goals**: env_kind detection runs once at startup, target < 50ms total for all syscalls; activation-bucket read/write < 5ms per heartbeat; no impact on BM25 search or steady-state MCP routing.
**Constraints**: Anonymity invariant is non-negotiable (see FR-011); BBolt writes must be transactional and tolerate crash mid-write; login-item API failures must not block tray startup.
**Scale/Scope**: Single-user install; new BBolt bucket stores at most ~24 monotonic booleans + 16-entry client set + 2 rolling counters = < 1KB per install.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Status | Notes |
|-----------|--------|-------|
| I. Performance at Scale | PASS | env_kind detection cached; BBolt ops are O(1); no new search/indexing paths. |
| II. Actor-Based Concurrency | PASS | Activation service is single-goroutine-owned (similar to existing telemetry service); callers send events via existing event bus or telemetry counter API. No new locks introduced. |
| III. Configuration-Driven Architecture | PASS | No new config knobs required — existing `MCPPROXY_TELEMETRY=false` and `telemetry disable` are the authoritative opt-outs. Login-item state is read from the OS, not persisted in mcp_config.json. Tray continues to be UI-only (reads state from core socket, does not own it). |
| IV. Security by Default | PASS | Anonymity preserved; env_markers are booleans only; payload builder self-check scans for user-path prefixes; no new network surface. |
| V. TDD | PASS | Each FR has a corresponding unit test required in tasks.md; v2 payload builder kept callable for regression tests (FR-012). |
| VI. Documentation Hygiene | PASS | CLAUDE.md telemetry section will be updated; docs/features/telemetry.md will document new fields and opt-out behavior. |

No violations. Complexity Tracking section is empty.

## Project Structure

### Documentation (this feature)

```text
specs/044-retention-telemetry-v3/
├── spec.md              # Feature specification (complete)
├── plan.md              # This file
├── research.md          # Phase 0 output
├── data-model.md        # Phase 1 output
├── quickstart.md        # Phase 1 output
├── contracts/           # Phase 1 output
│   └── heartbeat-v3.json  # JSON schema for v3 payload
├── checklists/
│   └── requirements.md  # Spec-quality checklist (complete)
└── tasks.md             # Phase 2 output (/speckit.tasks)
```

### Source Code (repository root)

```text
internal/telemetry/
├── telemetry.go                  # existing — extend HeartbeatPayload with 5 new fields
├── payload_v2_test.go            # existing — verify v2 builder still emits v2 fields for regression
├── payload_privacy_test.go       # existing — extend with new anonymity scanner
├── env_kind.go                   # NEW — ordered decision tree, cached
├── env_kind_test.go              # NEW — one test per branch (interactive/ci/cloud_ide/container/headless/unknown)
├── launch_source.go              # NEW — launch source detection
├── launch_source_test.go         # NEW
├── activation.go                 # NEW — BBolt bucket, monotonic flags, counters, token estimator
├── activation_test.go            # NEW
├── autostart.go                  # NEW — OS-specific login-item state readers (macOS via socket; Linux = nil)
├── autostart_test.go             # NEW
├── payload_v3.go                 # NEW — new payload builder that layers new fields onto v2 struct
└── payload_v3_test.go            # NEW — golden-payload tests + anonymity scanner

internal/server/
├── mcp.go                        # existing — hook initialize handler to record clientInfo.name
├── mcp_builtin_retrieve_tools.go # existing (or equivalent) — bump retrieve_tools_calls_24h

internal/httpapi/
├── server.go                     # existing — extend /api/v1/status with activation snapshot (read-only)

cmd/mcpproxy/
├── telemetry_cmd.go              # existing — extend `telemetry status` to show activation state

native/macos/MCPProxy/
├── Sources/MCPProxy/AutoStart.swift        # NEW — SMAppService wrapper
├── Sources/MCPProxy/FirstRunDialog.swift   # NEW — "Launch at login" checkbox (checked by default)
├── Sources/MCPProxy/SocketRoutes.swift     # existing (or equivalent) — expose /autostart endpoint

packaging/
├── macos/postinstall.sh          # NEW — DMG post-install: set MCPPROXY_LAUNCHED_BY=installer, launch tray once

docs/features/
├── telemetry.md                  # existing — document new fields + opt-out
```

**Structure Decision**: Single Go project with an existing macOS sub-project (`native/macos/MCPProxy`). All new Go code lives under `internal/telemetry/`. The MCP hook (`internal/server/mcp.go`), the HTTP status endpoint (`internal/httpapi/server.go`), the CLI (`cmd/mcpproxy/telemetry_cmd.go`), and the macOS tray (`native/macos/MCPProxy/`) each receive one small, surgical edit. No new top-level directories; no structural changes.

## Phase 0 — Research

See `research.md` for full notes. Key decisions pre-resolved from the design doc:

1. **env_kind decision tree**: ordered list per design §4.2. Runs once at startup, cached. File probe (`/.dockerenv`, `/run/.containerenv`) uses `os.Stat` with a timeout guard.
2. **launch_source**: `MCPPROXY_LAUNCHED_BY=installer` takes precedence; next is the tray socket handshake flag that core already receives; then parent-process heuristics (launchd on macOS, explorer.exe on Windows); then TTY check → `cli` or `unknown`.
3. **autostart_enabled on macOS**: the tray (which runs in a GUI session and has access to `SMAppService.mainApp.status`) reports state via a socket endpoint; core reads it once per heartbeat. Core running without tray → `null` (unknown). Linux → always `null`.
4. **Activation BBolt bucket shape**: `activation` bucket with well-known keys: `first_connected_server_ever`, `first_mcp_client_ever`, `first_retrieve_tools_call_ever` (boolean, 1 byte); `mcp_clients_seen_ever` (JSON-encoded `[]string`, capped at 16); `retrieve_tools_calls_24h` (uint64 counter with timestamp header for 24h decay); `estimated_tokens_saved_24h` (uint64 counter, bucketed at emit time).
5. **MCP client fingerprint sanitization**: any `clientInfo.name` containing `/`, `\`, `..`, absolute-path prefixes, or longer than 64 chars is recorded as `"unknown"`. Whitelist of known clients: `claude-code`, `cursor`, `windsurf`, `codex-cli`, `gemini-cli`, `vscode`, `continue`. Non-whitelisted but syntactically safe names are kept verbatim (since MCP spec requires them to be enum-like).
6. **Token estimator constant**: `avg_tokens_per_tool_schema = 150` (empirically measured from existing tool index). Source: BM25 indexed schemas (`internal/index/`). Bucketing applied at emit time, never stored raw beyond 24h window.
7. **Payload-builder self-check**: runs `json.Marshal` then regex-scans for `/Users/`, `/home/`, `C:\\Users\\`, `@`, and any string that contains >2 `/` path separators. Found match → payload fails validation and a counter increments (never blocks app startup; telemetry skips that heartbeat).

## Phase 1 — Design & Contracts

### Data model (`data-model.md`)

Entities:

- **EnvKind** (enum) — fixed 6 values.
- **LaunchSource** (enum) — fixed 5 values.
- **ActivationState** — struct with monotonic flags, client set (bounded), counters.
- **EnvMarkers** — boolean-only struct with 5 fields.
- **ActivationBucket** — BBolt bucket schema: key → value type mapping.

### Contracts (`contracts/heartbeat-v3.json`)

JSON Schema for the v3 heartbeat payload. Extends the existing v3 shape (Docker-isolation fields from spec 042) with the five new fields. Used by the worker's vitest suite (sibling repo) as the source of truth and by the Go `payload_v3_test.go` for shape verification.

### Quickstart (`quickstart.md`)

Step-by-step: check out branch → `make build` → run `mcpproxy serve` → curl `/api/v1/status` and assert new fields → set `GITHUB_ACTIONS=true` and restart → assert `env_kind=ci` → touch `/.dockerenv` mock and restart → assert `env_kind=container` → run `scripts/test-api-e2e.sh` → assert v3 fixture passes.

### Agent context update

Run `.specify/scripts/bash/update-agent-context.sh claude` to register `env_kind`, `launch_source`, `autostart_enabled`, `activation`, `env_markers`, and the new BBolt bucket as active technologies for this feature.

### Constitution re-check (post-design)

| Principle | Status | Notes |
|-----------|--------|-------|
| I. Performance at Scale | PASS | env_kind detection cached; activation bucket reads amortized over heartbeat interval (24h). |
| II. Actor-Based Concurrency | PASS | Activation service owned by a single goroutine; existing event bus used for retrieve_tools increment. No new mutexes. |
| III. Configuration-Driven Architecture | PASS | No new config knobs. Opt-out flows through existing telemetry opt-out. |
| IV. Security by Default | PASS | Anonymity scanner added to payload builder; zero env-var values transmitted; quarantine model unchanged. |
| V. TDD | PASS | tasks.md will enumerate one failing test per FR before implementation. |
| VI. Documentation Hygiene | PASS | CLAUDE.md + docs/features/telemetry.md updates scheduled in tasks.md. |

No new violations. Complexity Tracking remains empty.

## Complexity Tracking

*No Constitution Check violations. No complexity deviations to justify.*
