# Implementation Plan: OAuth Token Refresh Reliability

**Branch**: `023-oauth-state-persistence` | **Date**: 2026-01-12 | **Spec**: [spec.md](./spec.md)
**Input**: Feature specification from `/specs/023-oauth-state-persistence/spec.md`

## Summary

Fix OAuth token refresh so servers survive restarts and proactively refresh before expiration. The current implementation has misleading logging (reports success when `Start()` returns nil, not actual refresh), swallowed errors from mcp-go, and no startup recovery mechanism. This plan implements proper token refresh with exponential backoff retry, health status surfacing, and Prometheus metrics.

## Technical Context

**Language/Version**: Go 1.24 (toolchain go1.24.10)
**Primary Dependencies**: mcp-go v0.43.1 (OAuth client), BBolt (storage), Prometheus (metrics), Zap (logging)
**Storage**: BBolt database (`~/.mcpproxy/config.db`) - `oauth_tokens` bucket with `OAuthTokenRecord` model
**Testing**: `go test ./internal/...`, `./scripts/test-api-e2e.sh`, `./scripts/run-all-tests.sh`
**Target Platform**: macOS, Linux, Windows (desktop application)
**Project Type**: Single Go module with core server + tray application
**Performance Goals**: Token refresh within 30 seconds of startup; proactive refresh at 80% lifetime; health status update within 5 seconds of failure
**Constraints**: Rate limit refreshes to 1 per 10 seconds per server; exponential backoff capped at 5 minutes; localhost-only by default
**Scale/Scope**: Support multiple OAuth servers; tokens expire in ~2 hours; refresh tokens valid 24+ hours

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Status | Evidence/Notes |
|-----------|--------|----------------|
| I. Performance at Scale | ✅ PASS | Token refresh is per-server, async background operation; does not block API requests; uses existing scheduler pattern |
| II. Actor-Based Concurrency | ✅ PASS | Existing RefreshManager uses goroutine + timer pattern; will extend with exponential backoff; no new locks required |
| III. Configuration-Driven Architecture | ✅ PASS | Refresh threshold (80%) already configurable; no new config fields required; existing patterns sufficient |
| IV. Security by Default | ✅ PASS | No new security surface; tokens stored in existing encrypted BBolt bucket; rate limiting prevents abuse |
| V. Test-Driven Development (TDD) | ✅ PASS | Will add unit tests for RefreshManager changes, integration tests for health status, E2E tests for restart recovery |
| VI. Documentation Hygiene | ✅ PASS | Will update CLAUDE.md if architecture changes; logging documentation already exists |

**Architecture Constraints:**

| Constraint | Status | Evidence/Notes |
|------------|--------|----------------|
| Core + Tray Split | ✅ PASS | Changes are in core server only; tray consumes health status via existing SSE |
| Event-Driven Updates | ✅ PASS | RefreshManager already emits events; health status propagates via existing event bus |
| DDD Layering | ✅ PASS | Changes to Domain (oauth/), Infrastructure (storage/), Presentation (health status via httpapi/) |
| Upstream Client Modularity | ✅ PASS | Changes primarily in oauth/ layer; connection.go logging fix is surgical |

**Gate Result**: ✅ PASS - No violations. Proceed to Phase 0.

### Post-Design Re-evaluation (Phase 1 Complete)

| Principle | Status | Post-Design Evidence |
|-----------|--------|----------------------|
| I. Performance at Scale | ✅ PASS | RefreshSchedule uses timers, not polling; metrics add ~0 overhead |
| II. Actor-Based Concurrency | ✅ PASS | Extended RefreshSchedule struct; no new goroutines or locks |
| III. Configuration-Driven Architecture | ✅ PASS | No new config fields; constants in code are appropriate for fixed behavior |
| IV. Security by Default | ✅ PASS | No changes to security model; rate limiting added |
| V. Test-Driven Development (TDD) | ✅ PASS | Test plan in quickstart.md; unit + integration tests specified |
| VI. Documentation Hygiene | ✅ PASS | CLAUDE.md updated via agent context script |

**Post-Design Gate Result**: ✅ PASS - Design aligns with constitution. Ready for `/speckit.tasks`.

## Project Structure

### Documentation (this feature)

```text
specs/023-oauth-state-persistence/
├── plan.md              # This file
├── research.md          # Phase 0 output
├── data-model.md        # Phase 1 output
├── quickstart.md        # Phase 1 output
├── contracts/           # Phase 1 output (N/A - no new REST endpoints)
└── tasks.md             # Phase 2 output (/speckit.tasks command)
```

### Source Code (repository root)

```text
internal/
├── oauth/
│   ├── refresh_manager.go      # MODIFY: Add exponential backoff retry, startup refresh
│   ├── persistent_token_store.go  # MODIFY: Improve GetToken() error handling
│   └── logging.go              # MODIFY: Fix misleading success logging
├── upstream/
│   └── core/
│       └── connection.go       # MODIFY: Fix misleading "OAuth token refresh successful" logs
├── health/
│   └── calculator.go           # MODIFY: Add refresh retry state to health calculation
├── observability/
│   └── metrics.go              # MODIFY: Add OAuth refresh metrics
└── storage/
    └── models.go               # NO CHANGE: Existing OAuthTokenRecord sufficient

tests/
├── integration/                # ADD: Token refresh integration tests
└── unit/                       # ADD: RefreshManager unit tests
```

**Structure Decision**: Single Go module structure. All changes are modifications to existing files in `internal/` packages. No new packages or architectural changes required.

## Complexity Tracking

> No constitution violations to justify.

| Violation | Why Needed | Simpler Alternative Rejected Because |
|-----------|------------|-------------------------------------|
| N/A | N/A | N/A |
