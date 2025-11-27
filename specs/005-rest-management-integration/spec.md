# Feature Specification: REST Endpoint Management Service Integration

**Feature Branch**: `005-rest-management-integration`
**Created**: 2025-11-27
**Status**: Draft
**Input**: User description: "Refactor /api/v1/servers/* endpoints to use management service (spec 004) related with PR 152"

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Unified Server Management via REST API (Priority: P1)

API consumers and CLI tools need consistent server management behavior when calling REST endpoints. Currently, endpoints like `/api/v1/servers/{id}/tools` and `/api/v1/servers/{id}/login` bypass the management service layer and access runtime directly, violating the architecture defined in spec 004.

**Why this priority**: This is a critical architectural compliance issue. PR #152 added CLI socket support that depends on these endpoints, and they must follow the unified management service pattern to ensure consistent behavior across all interfaces (CLI, REST, MCP).

**Independent Test**: Can be fully tested by calling REST endpoints directly and verifying they delegate to management service methods, emit proper events, and respect configuration gates (`disable_management`, `read_only`).

**Acceptance Scenarios**:

1. **Given** the daemon is running, **When** a client calls `GET /api/v1/servers/{id}/tools`, **Then** the request is delegated to `ManagementService.GetServerTools()` instead of directly accessing runtime
2. **Given** the daemon is running, **When** a client calls `POST /api/v1/servers/{id}/login`, **Then** the request is delegated to `ManagementService.TriggerOAuthLogin()` instead of directly accessing runtime
3. **Given** `disable_management` is enabled in config, **When** a client calls `POST /api/v1/servers/{id}/login`, **Then** the request is blocked with appropriate error message
4. **Given** a server is successfully restarted via REST API, **When** the operation completes, **Then** a `servers.changed` event is emitted to all subscribers

---

### User Story 2 - CLI Socket Commands Use Management Layer (Priority: P2)

CLI commands that use socket communication (from PR #152: `tools list`, `auth login`, `auth status`) need to benefit from the unified management service layer's configuration gates, event emissions, and error handling.

**Why this priority**: This ensures the CLI commands added in PR #152 get the full benefits of the management service architecture, including proper event handling and consistent error messages.

**Independent Test**: Can be tested by running `mcpproxy tools list --server=test-server` and `mcpproxy auth login --server=test-server` with the daemon running, verifying they work correctly and trigger appropriate management service events.

**Acceptance Scenarios**:

1. **Given** the daemon is running and `disable_management` is enabled, **When** a user runs `mcpproxy auth login --server=test-server`, **Then** the operation is blocked with a clear error message explaining management is disabled
2. **Given** the daemon is running, **When** a user runs `mcpproxy tools list --server=test-server`, **Then** the tools are retrieved via the management service and any connection errors are reported consistently
3. **Given** a user triggers OAuth via `mcpproxy auth login`, **When** OAuth completes successfully, **Then** a `servers.changed` event is emitted and the tray UI updates automatically

---

### User Story 3 - Tray Application Server Management (Priority: P3)

Tray application users managing servers through GUI menus need the same consistent behavior when they trigger OAuth login or view server tools as they get from CLI and REST interfaces.

**Why this priority**: The tray already uses the REST API endpoints, so once those are refactored to use management service, the tray automatically benefits. This is a passive benefit rather than new functionality.

**Independent Test**: Can be tested by using tray menu actions to trigger OAuth login and verify the operation goes through management service with proper event emissions.

**Acceptance Scenarios**:

1. **Given** a user clicks "Authenticate Server" in the tray menu, **When** OAuth is triggered, **Then** the operation goes through `ManagementService.TriggerOAuthLogin()` with consistent error handling
2. **Given** `read_only` mode is enabled, **When** a user attempts to restart a server via tray menu, **Then** the operation is blocked at the management service layer with appropriate error message

---

### Edge Cases

- What happens when a REST endpoint is called for a non-existent server? (Management service should return consistent "server not found" error)
- How does the system handle concurrent OAuth login requests to the same server? (Management service should queue or reject duplicate operations)
- What happens if the management service method is called while the server is shutting down? (Should return context-canceled errors gracefully)
- How are errors from management service methods propagated to REST API responses? (Should maintain HTTP status code consistency)

## Requirements *(mandatory)*

### Functional Requirements

**Management Service Interface Extension**:
- **FR-001**: Management service interface MUST add `GetServerTools(ctx context.Context, name string) ([]map[string]interface{}, error)` method
- **FR-002**: Management service interface MUST add `TriggerOAuthLogin(ctx context.Context, name string) error` method
- **FR-003**: `GetServerTools()` method MUST delegate to runtime's `GetServerTools()` implementation to retrieve tool list from StateView
- **FR-004**: `TriggerOAuthLogin()` method MUST delegate to upstream manager's `StartManualOAuth()` to initiate OAuth flow

**REST Endpoint Refactoring**:
- **FR-005**: `GET /api/v1/servers/{id}/tools` handler MUST call `ManagementService.GetServerTools()` instead of `controller.GetServerTools()` directly
- **FR-006**: `POST /api/v1/servers/{id}/login` handler MUST call `ManagementService.TriggerOAuthLogin()` instead of `controller.TriggerOAuthLogin()` directly
- **FR-007**: Both endpoints MUST respect `disable_management` and `read_only` configuration gates enforced by management service
- **FR-008**: All REST endpoints under `/api/v1/servers/*` that perform write operations MUST validate configuration gates before execution

**Event Integration**:
- **FR-009**: `TriggerOAuthLogin()` method MUST emit `servers.changed` event when OAuth flow completes successfully
- **FR-010**: Management service MUST emit events via `EventEmitter` interface for all state-changing operations
- **FR-011**: Event emissions MUST trigger SSE updates to connected tray applications and web UIs

**Error Handling**:
- **FR-012**: Management service methods MUST return consistent error types across all calling interfaces
- **FR-013**: REST handlers MUST map management service errors to appropriate HTTP status codes (400, 404, 500)
- **FR-014**: Configuration gate violations MUST return HTTP 403 Forbidden with descriptive error messages

**Testing Requirements**:
- **FR-015**: All refactored REST endpoints MUST have unit tests verifying management service delegation
- **FR-016**: Integration tests MUST verify event emissions when operations complete
- **FR-017**: E2E tests MUST verify CLI socket commands work correctly after refactoring

### Key Entities

- **ManagementService**: Extended interface providing `GetServerTools()` and `TriggerOAuthLogin()` methods alongside existing lifecycle operations. Enforces configuration gates centrally.

- **REST Handler**: HTTP endpoint handler in `internal/httpapi/server.go` that delegates to management service instead of accessing runtime/controller directly.

- **Event**: Runtime event emitted by management service when operations complete, triggering SSE updates to tray and web UI.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: All REST endpoints under `/api/v1/servers/*` delegate to management service layer, eliminating direct runtime access (verifiable by code review)
- **SC-002**: CLI commands from PR #152 (`tools list`, `auth login`, `auth status`) work correctly after refactoring without behavioral changes (verifiable by running commands)
- **SC-003**: Configuration gates (`disable_management`, `read_only`) are enforced consistently across CLI, REST, and MCP interfaces (verifiable by testing with gates enabled)
- **SC-004**: Event emissions from management service trigger SSE updates to tray application within 1 second of operation completion (verifiable by monitoring SSE stream)
- **SC-005**: All existing E2E API tests pass without modification, confirming backward compatibility (verifiable by running `./scripts/test-api-e2e.sh`)
- **SC-006**: Code duplication is reduced by consolidating REST endpoint logic into management service methods (measurable by LOC comparison)
- **SC-007**: Unit test coverage for management service reaches at least 80% for new methods (measurable by `go test -coverprofile`)

## Assumptions

- The existing management service architecture from spec 004 is already implemented and available in `internal/management/service.go`
- The runtime's `GetServerTools()` and upstream manager's `StartManualOAuth()` methods are working correctly
- The event bus integration is functioning and emitting `servers.changed` events
- PR #152's CLI socket commands are already merged and functioning with the current endpoint implementations
- No breaking changes to REST API response formats are required

## Dependencies

- **Existing Components**:
  - `internal/management/service.go`: Management service interface to be extended with new methods
  - `internal/server/server.go`: Runtime controller providing `GetServerTools()` and `TriggerOAuthLogin()` implementations to be wrapped
  - `internal/httpapi/server.go`: REST handlers to be refactored for delegation
  - `internal/runtime/event_bus.go`: Event system for `servers.changed` emissions

- **Related PRs/Features**:
  - PR #152: CLI socket support that depends on these REST endpoints
  - Spec 004: Management service architecture that defines the delegation pattern

## Out of Scope

- Adding new REST endpoints beyond the two being refactored (`/tools` and `/login`)
- Changing REST API response formats or contracts
- Modifying MCP protocol tool implementations
- Adding new management service features beyond the two required methods
- Refactoring other REST endpoints not related to server management
- Performance optimizations to the existing runtime implementations

## Commit Message Conventions *(mandatory)*

When committing changes for this feature, follow these guidelines:

### Issue References
- ✅ **Use**: `Related #[issue-number]` - Links the commit to the issue without auto-closing
- ❌ **Do NOT use**: `Fixes #[issue-number]`, `Closes #[issue-number]`, `Resolves #[issue-number]` - These auto-close issues on merge

**Rationale**: Issues should only be closed manually after verification and testing in production, not automatically on merge.

### Co-Authorship
- ❌ **Do NOT include**: `Co-Authored-By: Claude <noreply@anthropic.com>`
- ❌ **Do NOT include**: "🤖 Generated with [Claude Code](https://claude.com/claude-code)"

**Rationale**: Commit authorship should reflect the human contributors, not the AI tools used.

### Example Commit Message
```
refactor: delegate REST endpoints to management service

Related #[issue-number]

Refactor GET /api/v1/servers/{id}/tools and POST /api/v1/servers/{id}/login
to use management service layer instead of accessing runtime directly.

## Changes
- Add GetServerTools() method to management service interface
- Add TriggerOAuthLogin() method to management service interface
- Update REST handlers to call management service methods
- Add event emissions for state changes
- Add unit tests for new management service methods

## Testing
- All E2E API tests pass
- CLI socket commands (tools list, auth login) work correctly
- Configuration gates enforced consistently
- Events emitted and SSE updates triggered
```
