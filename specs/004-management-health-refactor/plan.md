# Implementation Plan: Management Service Refactoring & OpenAPI Generation

**Branch**: `004-management-health-refactor` | **Date**: 2025-11-23 | **Spec**: [spec.md](./spec.md)
**Input**: Feature specification from `/specs/004-management-health-refactor/spec.md`

## Summary

This feature refactors the upstream server lifecycle management and health diagnostics into a unified service layer to eliminate code duplication across CLI, REST API, and MCP tool interfaces. It also adds automated OpenAPI 3.x specification generation using swaggo/swag, ensuring API documentation stays synchronized with the codebase for every release.

**Primary Requirements**:
- Create `internal/management/service.go` providing a single `ManagementService` interface
- All CLI commands, REST endpoints, and MCP tools delegate to this service
- Add comprehensive `Doctor()` method for health diagnostics aggregation
- Generate OpenAPI spec from swag annotations integrated into `make build`
- Maintain complete backward compatibility with existing CLI commands and flags

**Technical Approach**:
- Extract duplicated management logic from CLI/REST/MCP handlers into service layer
- Service enforces `disable_management` and `read_only` config gates centrally
- Emit events via existing `internal/runtime/event_bus.go` for state changes
- Add swag annotations to all REST handlers in `internal/httpapi/`
- Integrate `swag init` into Makefile build process

## Technical Context

**Language/Version**: Go 1.24.0
**Primary Dependencies**:
- github.com/go-chi/chi/v5 v5.2.3 (HTTP router)
- github.com/spf13/cobra v1.9.1 (CLI framework)
- github.com/mark3labs/mcp-go v0.42.0 (MCP protocol)
- go.uber.org/zap v1.27.0 (structured logging)
- go.etcd.io/bbolt v1.4.1 (embedded database)
- **NEW**: github.com/swaggo/swag (OpenAPI generation)
- **NEW**: github.com/swaggo/http-swagger (Swagger UI integration)

**Storage**: BBolt embedded database (`~/.mcpproxy/config.db`) for server configurations, quarantine status, and tool statistics
**Testing**: Go standard testing, testify for assertions, existing E2E test infrastructure
**Target Platform**: Cross-platform (macOS, Linux, Windows) - server and desktop application
**Project Type**: Hybrid (backend server + CLI + optional tray GUI)
**Performance Goals**:
- Doctor diagnostics complete in <3s for 20 servers
- OpenAPI generation in <5s during build
- No regression in existing API response times

**Constraints**:
- Complete backward compatibility with existing CLI interface
- No breaking changes to REST API endpoints
- Service layer must honor existing config gates (`disable_management`, `read_only`)
- OpenAPI spec must validate against OpenAPI 3.x schema

**Scale/Scope**:
- Support up to 20 configured upstream servers (typical deployment)
- ~30 REST endpoints to annotate for OpenAPI
- ~10 management operations to consolidate into service
- ~1500 LOC for service layer + tests

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

### Principle I: Performance at Scale
✅ **PASS** - This refactoring maintains existing performance characteristics:
- Doctor diagnostics designed for <3s completion (FR-002 success criteria)
- OpenAPI generation is build-time only, no runtime impact
- Service layer uses existing manager/event bus, no new blocking operations
- BM25 search and tool indexing unchanged

### Principle II: Actor-Based Concurrency
✅ **PASS** - Follows existing patterns:
- Management service calls existing `internal/server/manager.go` (actor-based)
- No new goroutines or channels introduced (uses existing runtime patterns)
- Event emissions via established event bus
- Context propagation maintained for cancellation

### Principle III: Configuration-Driven Architecture
✅ **PASS** - Enhances config consistency:
- Service layer enforces `disable_management` and `read_only` centrally (FR-002)
- No new config fields required (spec confirms this in Dependencies section)
- Optional `enable_swagger_ui` flag aligns with config-driven approach
- Tray/CLI continue reading from core config via REST API

### Principle IV: Security by Default
✅ **PASS** - Security unchanged:
- Existing localhost-only binding preserved
- API key authentication requirements maintained (FR-018)
- No changes to quarantine system or Docker isolation
- OpenAPI spec documents security schemes (FR-029)

### Principle V: Test-Driven Development (TDD)
✅ **PASS** - Comprehensive testing planned:
- Unit tests for management service (SC-007: 80% coverage target)
- Integration tests for REST/CLI/MCP parity (SC-001)
- E2E tests for doctor diagnostics (User Story 2 acceptance scenarios)
- OpenAPI validation tests (SC-003)

### Principle VI: Documentation Hygiene
✅ **PASS** - Documentation is core deliverable:
- OpenAPI spec auto-generated from annotations (primary feature goal)
- CLAUDE.md updates planned (SC-008)
- Architecture diagrams in plan showing service layer flow
- Swag annotations document all endpoints inline

### Architecture Constraints

✅ **Separation of Concerns: Core + Tray Split** - Preserved:
- Core server gains management service (no GUI dependencies)
- Tray continues using REST API (no direct service access)
- Headless mode unaffected

✅ **Event-Driven Updates** - Enhanced:
- Management service emits events for state changes (FR-003)
- Existing SSE integration forwards events to tray/web UI
- No polling introduced

✅ **Domain-Driven Design (DDD) Layering** - Improved:
- **Domain**: Management business logic in `internal/management/`
- **Application**: Runtime orchestration (existing, calls service)
- **Infrastructure**: HTTP/storage/logging (existing)
- **Presentation**: REST handlers delegate to service (refactored)

✅ **Upstream Client Modularity** - Unchanged:
- Management service calls existing managed client layer
- 3-layer client design preserved

### Development Workflow

✅ **Pre-Commit Quality Gates** - Standard compliance:
- All tests pass before merge
- Linting required (`./scripts/run-linter.sh`)
- E2E API tests included

✅ **Error Handling Standards** - Follows Go idioms:
- Service returns `error` values
- Errors wrapped with context
- Structured logging via zap

✅ **Git Commit Discipline** - Spec mandates:
- Conventional commits format in spec
- Issue refs use `Related #` (not auto-closing)
- No AI co-authorship attribution

✅ **Branch Strategy** - Aligned:
- Feature branch `004-management-health-refactor`
- Merges to `next` for validation, then `main`

**Constitution Compliance**: ✅ **ALL GATES PASS** - No violations, no complexity justification needed.

## Project Structure

### Documentation (this feature)

```text
specs/004-management-health-refactor/
├── spec.md              # Feature specification (completed)
├── plan.md              # This file (in progress)
├── research.md          # Phase 0 output (next)
├── data-model.md        # Phase 1 output
├── quickstart.md        # Phase 1 output
├── contracts/           # Phase 1 output (OpenAPI examples, service contracts)
│   ├── management-service.go.example     # Service interface example
│   ├── diagnostics.json                  # Diagnostics response schema
│   ├── swag-annotation-example.go        # Swag annotation patterns
│   └── openapi-partial.yaml              # Expected OpenAPI structure
└── checklists/
    └── requirements.md  # Spec validation (completed)
```

### Source Code (repository root)

```text
mcpproxy-go/                              # Existing structure
├── cmd/
│   ├── mcpproxy/                         # Core server CLI
│   │   ├── main.go                       # [MODIFY] Add swag annotations
│   │   ├── upstream_cmd.go               # [MODIFY] Delegate to service
│   │   └── doctor_cmd.go                 # [MODIFY] Delegate to service
│   └── mcpproxy-tray/                    # Tray application (unchanged)
│
├── internal/
│   ├── management/                       # [NEW] Management service layer
│   │   ├── service.go                    # Service interface + implementation
│   │   ├── service_test.go               # Unit tests (80% coverage)
│   │   ├── diagnostics.go                # Doctor logic
│   │   └── diagnostics_test.go           # Diagnostics tests
│   │
│   ├── contracts/                        # [MODIFY] Add diagnostics types
│   │   ├── diagnostics.go                # [NEW] Diagnostics, AuthStatus types
│   │   └── server.go                     # [EXISTING] Server, ServerStats, LogEntry
│   │
│   ├── httpapi/                          # [MODIFY] Add swag annotations
│   │   ├── server.go                     # [MODIFY] Delegate to management service
│   │   └── swagger.go                    # [NEW] Swagger UI handler
│   │
│   ├── cliclient/                        # [MODIFY] Add new endpoints
│   │   └── client.go                     # [MODIFY] Add Doctor, bulk operation methods
│   │
│   ├── server/                           # [MODIFY] MCP tool handlers
│   │   └── mcp.go                        # [MODIFY] Delegate upstream_servers to service
│   │
│   ├── runtime/                          # [EXISTING] Uses new service
│   │   ├── runtime.go                    # [MODIFY] Inject management service
│   │   └── event_bus.go                  # [EXISTING] Receives service events
│   │
│   └── [other packages unchanged]
│
├── docs/                                 # [NEW] Generated documentation
│   └── swagger.yaml                      # [GENERATED] OpenAPI 3.x spec (in oas/)
│
├── Makefile                              # [MODIFY] Add swag generation
├── go.mod                                # [MODIFY] Add swag dependencies
└── README.md                             # [MODIFY] Document new endpoints
```

**Structure Decision**: This is a backend refactoring with CLI interface. We're enhancing the existing single-project structure by adding a new `internal/management/` package. The frontend (Vue.js web UI) exists but is unchanged by this feature. Core server architecture follows the established pattern:

- **Domain logic**: `internal/management/` (new service layer)
- **Application layer**: `internal/runtime/` (orchestrates services)
- **Infrastructure**: `internal/httpapi/`, `internal/storage/`
- **Presentation**: `cmd/mcpproxy/` (CLI), `internal/httpapi/` (REST)

## Complexity Tracking

> **No constitution violations - this section is empty.**

All complexity introduced by this feature aligns with constitution principles:
- ✅ New service layer reduces complexity by consolidating duplicate logic
- ✅ No new concurrency patterns (uses existing event bus)
- ✅ No new storage abstractions (BBolt usage unchanged)
- ✅ Swag annotations add documentation, not runtime complexity
