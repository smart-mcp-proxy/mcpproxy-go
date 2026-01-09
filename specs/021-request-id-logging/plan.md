# Implementation Plan: Request ID Logging

**Branch**: `021-request-id-logging` | **Date**: 2026-01-07 | **Spec**: [spec.md](./spec.md)
**Input**: Feature specification from `/specs/021-request-id-logging/spec.md`

## Summary

Add request-scoped logging with `X-Request-Id` header support across all clients (CLI, tray, Web UI). **Clients are NOT required to provide a request ID** - if the `X-Request-Id` header is missing, mcpproxy core automatically generates a random UUID v4 and uses it for the request. The generated ID is returned in the response header and included in error payloads. A new `--request-id` flag enables log retrieval by ID.

## Technical Context

**Language/Version**: Go 1.24 (toolchain go1.24.10)
**Primary Dependencies**: Cobra (CLI), Chi router (HTTP), Zap (logging), google/uuid (ID generation)
**Storage**: BBolt database (`~/.mcpproxy/config.db`) - activity log extended with request_id field
**Testing**: go test, scripts/test-api-e2e.sh, scripts/run-all-tests.sh
**Target Platform**: macOS, Linux, Windows (cross-platform CLI)
**Project Type**: Single project with CLI and daemon components
**Performance Goals**: Request ID generation <1ms latency; log filtering <100ms
**Constraints**: No breaking changes to existing API responses; backward compatible
**Scale/Scope**: Handles existing request volume; request IDs are transient (no persistence)

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Status | Notes |
|-----------|--------|-------|
| I. Performance at Scale | PASS | UUID generation ~100ns; minimal log storage increase (36 chars) |
| II. Actor-Based Concurrency | PASS | Request context propagated via Go context.Context; no shared state |
| III. Configuration-Driven Architecture | PASS | No new config needed; uses existing logging infrastructure |
| IV. Security by Default | PASS | Request IDs validated; no secrets embedded; safe to display |
| V. Test-Driven Development | PASS | Will add unit tests for middleware, E2E for response headers |
| VI. Documentation Hygiene | PASS | Will update CLAUDE.md with error handling docs |

**Architecture Constraints:**

| Constraint | Status | Notes |
|------------|--------|-------|
| Core + Tray Split | PASS | Middleware in core; tray/Web UI receive same response format |
| Event-Driven Updates | N/A | No event changes; request ID is per-request context |
| DDD Layering | PASS | Middleware in server layer; error response in httpapi |
| Upstream Client Modularity | N/A | Request ID applies to REST API, not MCP protocol |

## Project Structure

### Documentation (this feature)

```text
specs/021-request-id-logging/
├── plan.md              # This file
├── research.md          # Phase 0 output (complete)
├── data-model.md        # Phase 1 output (complete)
├── quickstart.md        # Phase 1 output
├── contracts/           # Phase 1 output
│   └── request-id-api.yaml
└── tasks.md             # Phase 2 output (/speckit.tasks command)
```

### Source Code (repository root)

```text
cmd/mcpproxy/
├── root.go              # MODIFY: Add request ID handling to error output
├── logs_cmd.go          # MODIFY: Add --request-id flag

internal/
├── httpapi/
│   └── middleware.go    # ADD: RequestID middleware
│   └── errors.go        # MODIFY: Include request_id in all error responses
├── server/
│   └── server.go        # MODIFY: Register RequestID middleware first
└── logs/
    └── query.go         # MODIFY: Add request_id filter to log queries

tests/
└── internal/
    └── server/e2e_test.go  # MODIFY: E2E tests for X-Request-Id header
```

**Structure Decision**: Single project structure; changes span HTTP middleware (`internal/httpapi`), CLI (`cmd/mcpproxy`), and tests.

## Implementation Scope

### In Scope

| Component | Change |
|-----------|--------|
| HTTP Middleware | New `RequestIDMiddleware` - generate/validate ID, set header |
| Error Responses | All error JSON includes `request_id` field |
| CLI Error Display | Print Request ID + log suggestion on errors |
| Log Retrieval | `--request-id` flag for logs command; API query param |
| E2E Tests | Verify `X-Request-Id` header in responses |

### Out of Scope

- Tray/Web UI implementation (they receive response, implement their own UX)
- Distributed tracing (spans, parent IDs)
- Request ID in MCP protocol messages
- Request ID persistence across daemon restarts

## Multi-Client Behavior

All clients receive the same `X-Request-Id` header and error response structure:

| Client | On Error | Log Retrieval |
|--------|----------|---------------|
| CLI | Print request_id + suggestion to stderr | `mcpproxy logs --request-id <id>` |
| Tray | Display in notification (future) | Link to logs endpoint |
| Web UI | Display in error modal (future) | Inline logs or link |

CLI is the only client modified in this feature. Tray and Web UI will implement their handling in future work.

## Complexity Tracking

> No constitution violations requiring justification.

| Aspect | Decision | Rationale |
|--------|----------|-----------|
| ID Generation | UUID v4 in middleware | Standard format, fast (~100ns), no coordination |
| Header Name | X-Request-Id | Industry standard (AWS, Heroku, nginx) |
| Log Integration | Zap context field | Single place to set; all downstream logs get ID |

---

## Post-Design Constitution Re-evaluation

*Re-checked after Phase 1 design completion.*

| Principle | Status | Post-Design Notes |
|-----------|--------|-------------------|
| I. Performance at Scale | PASS | UUID generation ~100ns; minimal impact on request latency |
| II. Actor-Based Concurrency | PASS | Request context via context.Context; no shared state or locks |
| III. Configuration-Driven Architecture | PASS | No new configuration; uses existing infrastructure |
| IV. Security by Default | PASS | IDs validated (alphanumeric, 256 char limit); safe to display |
| V. Test-Driven Development | PASS | E2E tests for header, unit tests for middleware |
| VI. Documentation Hygiene | PASS | quickstart.md created; will update CLAUDE.md |

**Architecture Constraints (Post-Design):**

| Constraint | Status | Post-Design Notes |
|------------|--------|-------------------|
| Core + Tray Split | PASS | All changes in core daemon. Tray/Web UI receive same response format. |
| Event-Driven Updates | N/A | Request ID is per-request; no event infrastructure needed. |
| DDD Layering | PASS | Middleware in server layer; error helpers in httpapi. |
| Upstream Client Modularity | N/A | Applies to REST API only; MCP protocol unchanged. |

**Conclusion**: All constitution principles pass. Design is ready for implementation.

---

## Generated Artifacts

| Artifact | Status | Description |
|----------|--------|-------------|
| `research.md` | Complete | 10 design decisions with rationale |
| `data-model.md` | Complete | RequestContext, ErrorResponse, LogQueryParams entities |
| `contracts/request-id-api.yaml` | Complete | OpenAPI 3.0 contract for request ID headers |
| `quickstart.md` | Complete | Usage examples and troubleshooting |

---

## Next Steps

Run `/speckit.tasks` to generate the implementation task list.
