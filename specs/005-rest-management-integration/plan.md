# Implementation Plan: REST Endpoint Management Service Integration

**Branch**: `005-rest-management-integration` | **Date**: 2025-11-27 | **Spec**: [spec.md](./spec.md)
**Input**: Feature specification from `/specs/005-rest-management-integration/spec.md`

**Note**: This template is filled in by the `/speckit.plan` command. See `.specify/templates/commands/plan.md` for the execution workflow.

## Summary

This feature refactors REST endpoints `/api/v1/servers/{id}/tools` and `/api/v1/servers/{id}/login` to delegate to the management service layer instead of accessing runtime directly. This addresses the architectural violation identified in PR #152 review and ensures compliance with spec 004's unified management service pattern.

**Primary Requirements**:
- Extend management service interface with `GetServerTools()` and `TriggerOAuthLogin()` methods
- Refactor REST handlers in `internal/httpapi/server.go` to call management service
- Ensure configuration gates (`disable_management`, `read_only`) are enforced consistently
- Emit `servers.changed` events for state changes
- Maintain backward compatibility with existing E2E tests

**Technical Approach**:
- Add two new methods to `internal/management/service.go` interface
- Implement methods in service to delegate to existing runtime/upstream manager operations
- Update REST handlers to retrieve management service from controller and call methods
- Add unit tests for new management service methods (target 80% coverage)
- Verify E2E tests pass without modification

## Technical Context

**Language/Version**: Go 1.24.0
**Primary Dependencies**:
- github.com/go-chi/chi/v5 v5.2.3 (HTTP router)
- github.com/mark3labs/mcp-go v0.42.0 (MCP protocol)
- go.uber.org/zap v1.27.0 (structured logging)
- go.etcd.io/bbolt v1.4.1 (embedded database)

**Storage**: BBolt embedded database (`~/.mcpproxy/config.db`) - used by existing runtime, no changes required
**Testing**: Go standard testing, testify for assertions, existing E2E test infrastructure (`./scripts/test-api-e2e.sh`)
**Target Platform**: Cross-platform (macOS, Linux, Windows) - server and desktop application
**Project Type**: Single project (backend server + CLI + optional tray GUI)
**Performance Goals**:
- No regression in existing API response times
- Management service method calls complete in <10ms (in-memory delegation)
- Event emission adds <1ms overhead to operations

**Constraints**:
- Complete backward compatibility with existing REST API contracts
- No breaking changes to CLI commands from PR #152
- Must respect existing configuration gates without adding new config options
- Service layer must remain stateless (delegate to runtime for state)

**Scale/Scope**:
- 2 REST endpoints to refactor
- 2 new management service interface methods
- ~150-200 LOC for management service implementation
- ~100-150 LOC for REST handler updates
- ~200-300 LOC for unit tests
- Support up to 20 configured upstream servers (existing limit)

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

### Principle I: Performance at Scale
✅ **PASS** - This refactoring maintains existing performance:
- Management service methods are thin wrappers delegating to existing runtime operations
- No new blocking operations or I/O introduced
- Event emissions use existing non-blocking event bus
- In-memory delegation adds <1ms overhead (negligible)

### Principle II: Actor-Based Concurrency
✅ **PASS** - Follows existing patterns:
- Management service calls existing runtime/upstream manager (already actor-based)
- No new goroutines, channels, or locks introduced
- Event emissions via established event bus
- Context propagation maintained for cancellation

### Principle III: Configuration-Driven Architecture
✅ **PASS** - Enhances config consistency:
- Management service enforces `disable_management` and `read_only` gates centrally
- No new configuration fields required
- Service layer reads config from runtime (single source of truth)
- Hot-reload support maintained through existing runtime mechanisms

### Principle IV: Security by Default
✅ **PASS** - Security unchanged:
- Existing localhost-only binding preserved
- API key authentication requirements maintained
- No changes to quarantine system or Docker isolation
- Configuration gates prevent unauthorized operations

### Principle V: Test-Driven Development (TDD)
✅ **PASS** - Comprehensive testing planned:
- Unit tests for new management service methods (target 80% coverage per spec SC-007)
- Integration tests verify event emissions (FR-016)
- E2E tests verify backward compatibility (SC-005)
- All tests pass before merge

### Principle VI: Documentation Hygiene
✅ **PASS** - Documentation updates planned:
- CLAUDE.md updated if management service patterns change
- Code comments for new methods explaining delegation
- OpenAPI annotations maintained for REST endpoints
- This plan.md documents architecture decisions

### Architecture Constraints Check

✅ **Separation of Concerns**: Management service acts as application layer between REST (presentation) and runtime (infrastructure)
✅ **Event-Driven Updates**: Service emits events via event bus for SSE propagation
✅ **Domain-Driven Design Layering**: Proper layering maintained (REST → Management → Runtime)
✅ **Upstream Client Modularity**: Not affected (refactoring happens above client layer)

**Pre-Phase 0 Result**: ✅ All gates PASS - proceed to Phase 0 research

## Project Structure

### Documentation (this feature)

```text
specs/005-rest-management-integration/
├── plan.md              # This file (/speckit.plan command output)
├── research.md          # Phase 0 output (/speckit.plan command)
├── data-model.md        # Phase 1 output (/speckit.plan command)
├── quickstart.md        # Phase 1 output (/speckit.plan command)
├── contracts/           # Phase 1 output (/speckit.plan command)
│   └── management-service.yaml  # Extended management service interface
└── tasks.md             # Phase 2 output (/speckit.tasks command - NOT created by /speckit.plan)
```

### Source Code (repository root)

```text
internal/
├── management/
│   ├── service.go           # MODIFY: Add GetServerTools, TriggerOAuthLogin to interface
│   ├── service_impl.go      # MODIFY: Implement new methods
│   └── service_test.go      # MODIFY: Add unit tests for new methods
├── httpapi/
│   ├── server.go            # MODIFY: Update handlers to call management service
│   └── contracts_test.go    # MODIFY: Update mock to include new methods
├── server/
│   └── server.go            # REFERENCE: Existing GetServerTools, TriggerOAuthLogin implementations
└── runtime/
    └── event_bus.go         # REFERENCE: Existing event emission infrastructure

cmd/mcpproxy/
└── [No changes required]   # CLI commands already use REST API from PR #152

scripts/
└── test-api-e2e.sh          # REFERENCE: E2E test suite for validation
```

**Structure Decision**: Single project structure (Option 1) as this is a backend refactoring within the existing mcpproxy codebase. All changes are in `internal/` packages following Go conventions. No frontend or mobile components involved.

## Complexity Tracking

> **Fill ONLY if Constitution Check has violations that must be justified**

N/A - All constitution gates pass. No violations to justify.

## Phase 0: Research & Unknowns

**Status**: No unknowns requiring research

All technical context is known from existing codebase:
- Management service pattern already established in spec 004 implementation
- Runtime and upstream manager interfaces are documented and stable
- Event bus integration is working and well-understood
- REST handler patterns follow existing conventions in `internal/httpapi/server.go`
- Testing infrastructure is established with E2E scripts

**Research Tasks**: None required (skipping research.md generation)

**Proceed directly to Phase 1**: Design artifacts

## Phase 1: Design & Contracts

### Data Model

**Entity**: ManagementService (interface extension)

**New Methods**:
```go
// GetServerTools retrieves all tools for a specific upstream server.
// Delegates to runtime's GetServerTools() which reads from StateView cache.
GetServerTools(ctx context.Context, name string) ([]map[string]interface{}, error)

// TriggerOAuthLogin initiates OAuth authentication flow for a specific server.
// Delegates to upstream manager's StartManualOAuth() to launch browser flow.
TriggerOAuthLogin(ctx context.Context, name string) error
```

**Contracts**: See `contracts/management-service.yaml` (Phase 1 output)

### Integration Points

**Existing → Management Service**:
- `internal/server/server.go:1447` - `GetServerTools()` implementation to wrap
- `internal/server/server.go:136` - `TriggerOAuthLogin()` implementation to wrap
- `internal/runtime/event_bus.go` - Event emission infrastructure

**Management Service → REST API**:
- `internal/httpapi/server.go:1155` - `handleGetServerTools()` handler to update
- `internal/httpapi/server.go:1050` - `handleServerLogin()` handler to update

**Event Flow**:
```
TriggerOAuthLogin() → StartManualOAuth() → OAuth completion →
→ EventEmitter.EmitServersChanged() → SSE /events → Tray UI update
```

### Implementation Strategy

1. **Extend Management Service Interface** (`internal/management/service.go:27-84`)
   - Add `GetServerTools(ctx, name) ([]map[string]interface{}, error)` signature
   - Add `TriggerOAuthLogin(ctx, name) error` signature

2. **Implement Methods** (new file: `internal/management/service_impl.go` or in existing impl)
   - `GetServerTools`: Validate server exists, delegate to `runtime.GetServerTools(name)`
   - `TriggerOAuthLogin`: Check config gates, delegate to `upstreamManager.StartManualOAuth(name, true)`, emit event

3. **Update REST Handlers** (`internal/httpapi/server.go`)
   - `handleGetServerTools`: Call `s.controller.GetManagementService().(ManagementService).GetServerTools(ctx, serverID)`
   - `handleServerLogin`: Call `s.controller.GetManagementService().(ManagementService).TriggerOAuthLogin(ctx, serverID)`

4. **Add Unit Tests** (`internal/management/service_test.go`)
   - Test `GetServerTools` with valid server, invalid server, empty server name
   - Test `TriggerOAuthLogin` with gates enabled, gates disabled, OAuth success, OAuth failure
   - Verify event emissions in tests

5. **Verify E2E Tests** (`./scripts/test-api-e2e.sh`)
   - Run existing tests to confirm backward compatibility
   - No new E2E tests required (behavior unchanged from external perspective)

### Quickstart Guide

See `quickstart.md` (Phase 1 output) for developer onboarding instructions.

## Phase 2: Task Breakdown

**Status**: Deferred to `/speckit.tasks` command

The task breakdown will be generated by the `/speckit.tasks` command after Phase 1 artifacts are complete. Expected tasks will include:
1. Extend management service interface
2. Implement GetServerTools method
3. Implement TriggerOAuthLogin method
4. Update REST handlers
5. Add unit tests
6. Run E2E validation
7. Update documentation

## Post-Phase 1 Constitution Re-Check

*Re-evaluate after design artifacts generated*

**Status**: ✅ COMPLETED

All design artifacts have been generated and reviewed:
- ✅ `data-model.md` - Interface extension with two new methods
- ✅ `contracts/management-service.yaml` - Detailed method contracts and REST mappings
- ✅ `quickstart.md` - Developer onboarding guide

### Constitution Re-Check Results

**Principle I: Performance at Scale** - ✅ PASS
- Design confirms <10ms latency for GetServerTools (in-memory cache read)
- Design confirms <50ms latency for TriggerOAuthLogin (async browser launch)
- No new blocking operations introduced
- Performance goals met per data-model.md specifications

**Principle II: Actor-Based Concurrency** - ✅ PASS
- Management service methods are simple delegators (no new concurrency)
- Existing actor patterns maintained (runtime, upstream manager)
- No new goroutines, channels, or locks in design
- Context propagation maintained throughout call chain

**Principle III: Configuration-Driven Architecture** - ✅ PASS
- Config gates enforced in management service layer (centralized)
- No new configuration fields required
- Hot-reload support maintained (service reads from atomic config value)
- Single source of truth preserved

**Principle IV: Security by Default** - ✅ PASS
- Configuration gates prevent unauthorized operations (403 errors)
- No changes to authentication requirements
- OAuth flow security maintained (existing implementation)
- Error messages sanitized (no sensitive data exposure)

**Principle V: Test-Driven Development** - ✅ PASS
- Unit test patterns defined in quickstart.md (80% coverage target)
- Integration test approach documented (event propagation verification)
- E2E test validation required (backward compatibility check)
- Mock implementations provided in quickstart.md

**Principle VI: Documentation Hygiene** - ✅ PASS
- Complete documentation suite generated (plan, data-model, contracts, quickstart)
- Code examples provided for all methods
- Error handling patterns documented
- API contracts fully specified in YAML format

### Architecture Constraints Re-Check

✅ **Separation of Concerns**: Design maintains clear layering (REST → Management → Runtime)
✅ **Event-Driven Updates**: OAuth completion emits events via existing event bus
✅ **Domain-Driven Design Layering**: Management service properly positioned as application layer
✅ **Upstream Client Modularity**: Not affected by refactoring (operates above client layer)

**Final Result**: ✅ All gates PASS - Design approved for implementation

## Notes

- This refactoring is purely internal - no user-visible behavior changes
- PR #152 CLI commands will automatically benefit once endpoints delegate to management service
- Tray application will automatically get event-driven updates through existing SSE connection
- Configuration gates will be enforced consistently across all interfaces (CLI, REST, MCP)
