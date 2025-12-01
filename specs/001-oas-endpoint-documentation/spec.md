# Feature Specification: Complete OpenAPI Documentation for REST API Endpoints

**Feature Branch**: `001-oas-endpoint-documentation`
**Created**: 2025-11-28
**Status**: Draft
**Input**: User description: "Required to add into OAS all existed endpoints see doc @docs/oas-missing-endpoints-plan.md. Also in swagger some endpoints marked as required auth some not. Actually most of endpoints required API Key need to double check this attribute"

## User Scenarios & Testing *(mandatory)*

### User Story 1 - API Consumer Discovers All Available Endpoints (Priority: P1)

A developer or third-party integrator opens Swagger UI to explore MCPProxy's REST API capabilities. They need to see complete, accurate documentation for all implemented endpoints to understand what operations are available and how to authenticate.

**Why this priority**: Without complete endpoint documentation, API consumers cannot discover or use 19 implemented endpoints (configuration management, secrets, tool-calls, sessions, registries, code execution, SSE events). This blocks API adoption and forces users to read source code.

**Independent Test**: Can be fully tested by opening Swagger UI at `http://localhost:8080/swagger/` and verifying all 19 missing endpoints appear in the documentation. Delivers immediate value by making undocumented features discoverable.

**Acceptance Scenarios**:

1. **Given** a developer opens Swagger UI, **When** they browse the API documentation, **Then** they see all 19 currently undocumented endpoints listed with descriptions, parameters, request/response schemas, and authentication requirements
2. **Given** an API consumer wants to manage configuration, **When** they view the `/api/v1/config` endpoint group in Swagger UI, **Then** they see GET, POST validate, and POST apply operations with complete schema definitions
3. **Given** a third-party integration needs secrets management, **When** they explore the `/api/v1/secrets/*` endpoints, **Then** they find 5 documented operations (list refs, get config, migrate, set, delete) with authentication marked as required

---

### User Story 2 - Developer Understands Authentication Requirements (Priority: P1)

An API consumer reviews endpoint documentation to understand which endpoints require authentication and which do not. They need accurate security annotations to avoid authentication errors and understand the security model.

**Why this priority**: Current OAS documentation has inconsistent authentication markers - some endpoints show authentication required when they don't need it, others are missing authentication markers when they do require API keys. This creates confusion and failed API calls.

**Independent Test**: Can be tested by reviewing the OAS security schemes and verifying each endpoint's security requirements match the actual middleware implementation in `internal/httpapi/server.go:135-184`. Delivers value by preventing authentication errors.

**Acceptance Scenarios**:

1. **Given** a developer views an `/api/v1/*` endpoint in Swagger UI, **When** the endpoint is protected by API key middleware, **Then** the documentation displays a lock icon and lists "API Key Authentication" as required
2. **Given** a developer views a health check endpoint (`/healthz`, `/readyz`, `/livez`, `/ready`), **When** these endpoints bypass authentication, **Then** the documentation shows no authentication requirement
3. **Given** a developer views the `/events` SSE endpoint, **When** this endpoint requires API key authentication via header or query parameter, **Then** the documentation lists both authentication methods (`X-API-Key` header and `?apikey=` query parameter)
4. **Given** a developer views the `/swagger/*` endpoint, **When** Swagger UI itself is unprotected, **Then** the documentation clearly indicates no authentication is required for documentation browsing

---

### User Story 3 - System Maintainer Prevents Documentation Drift (Priority: P2)

A development team maintains MCPProxy and adds new REST endpoints over time. They need automated validation to ensure new endpoints are documented in the OAS spec before merging, preventing the same documentation drift that created the current 19-endpoint gap.

**Why this priority**: Manual documentation maintenance is error-prone. Without automated checks, new endpoints will continue to be implemented without OAS annotations, recreating the current problem.

**Independent Test**: Can be tested by running the OAS coverage verification script and CI check against a pull request that adds a new endpoint without OAS documentation. The test should fail and block the merge. Delivers value by preventing future documentation gaps.

**Acceptance Scenarios**:

1. **Given** a developer adds a new REST endpoint to `internal/httpapi/server.go`, **When** they run the OAS verification script locally, **Then** the script detects the missing endpoint and reports it before commit
2. **Given** a pull request adds a new API endpoint, **When** the CI pipeline runs, **Then** the OAS coverage check fails if the endpoint lacks OpenAPI annotations
3. **Given** all implemented endpoints have OAS documentation, **When** the verification script runs, **Then** it reports 100% coverage and exits with success code

---

### Edge Cases

- What happens when an endpoint uses both `/api/v1/servers/{id}/*` and `/api/v1/servers/{name}/*` patterns? (Documentation should clarify that `{id}` and `{name}` are equivalent path parameters for server identification)
- How does the system handle OAS documentation for SSE streaming endpoints that don't return JSON? (Should use appropriate `text/event-stream` content type and describe event payload structure)
- What happens when endpoints support multiple authentication methods (header vs query parameter for SSE)? (Documentation should list all supported methods with usage examples)
- How should the OAS document endpoints that behave differently based on connection source (Unix socket vs TCP)? (Should document the TCP behavior with authentication, noting that Unix socket connections bypass auth automatically)

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: OpenAPI specification MUST document all 19 currently undocumented REST endpoints across configuration management (3), secrets management (5), tool call history (3), session management (2), registry browsing (2), code execution (1), SSE events (2), and per-server tool calls (1)
- **FR-002**: Each endpoint documentation MUST include HTTP method, path, summary description, request body schema (if applicable), response schemas (success and error cases), and path/query parameters
- **FR-003**: OpenAPI security schemes MUST accurately reflect the authentication middleware implementation: API key required for `/api/v1/*` endpoints when configured, no authentication for health endpoints (`/healthz`, `/readyz`, `/livez`, `/ready`), and SSE endpoints support both header and query parameter authentication
- **FR-004**: Documentation MUST clearly indicate which endpoints require API key authentication with visible lock icons in Swagger UI and security requirement annotations in the OAS YAML
- **FR-005**: System MUST provide an automated verification script that compares implemented routes in `internal/httpapi/server.go` against documented paths in `oas/swagger.yaml` and reports missing endpoint coverage
- **FR-006**: CI pipeline MUST include OAS coverage validation that fails builds when new endpoints are added without corresponding OpenAPI documentation
- **FR-007**: OAS specification MUST define reusable schema components for request/response bodies to avoid duplication across endpoints
- **FR-008**: Documentation MUST include request/response examples for complex endpoints (configuration apply, secrets migration, tool call replay, code execution)
- **FR-009**: SSE endpoint documentation (`/events`) MUST specify `text/event-stream` content type and describe event payload structure for real-time updates
- **FR-010**: Authentication documentation MUST explain the security model: Unix socket/named pipe connections bypass authentication (OS-level security), TCP connections require API key when configured, and empty API key disables authentication

### Key Entities *(include if feature involves data)*

- **OpenAPI Endpoint**: Represents a documented REST API operation with HTTP method, path, parameters, request/response schemas, and security requirements
- **Security Scheme**: Defines authentication methods (API key via header or query parameter) and which endpoints require them
- **Request/Response Schema**: Reusable component definitions for endpoint payloads (configuration objects, secret references, tool call records, session data, registry listings)
- **OAS Coverage Report**: Output from verification script showing total routes, documented routes, undocumented routes, and coverage percentage

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Swagger UI displays documentation for 100% of implemented REST endpoints (currently 19 undocumented + existing documented endpoints)
- **SC-002**: OAS verification script runs in under 5 seconds and reports zero missing endpoints after implementation
- **SC-003**: API consumers can identify authentication requirements for any endpoint by viewing Swagger UI without reading source code
- **SC-004**: CI pipeline prevents merging pull requests that add REST endpoints without OpenAPI documentation
- **SC-005**: Developers can generate accurate API client libraries from the OAS specification using standard code generation tools (openapi-generator, swagger-codegen)
- **SC-006**: 100% of endpoints requiring API key authentication display lock icons in Swagger UI
- **SC-007**: Documentation includes working curl examples with proper authentication for at least 80% of documented endpoints

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
docs: add OpenAPI documentation for secrets management endpoints

Related #[issue-number]

Added complete OAS annotations for 5 secrets management endpoints:
- GET /api/v1/secrets/refs - List secret references
- GET /api/v1/secrets/config - Get config secrets with resolution status
- POST /api/v1/secrets/migrate - Migrate between storage backends
- POST /api/v1/secrets - Set secret value
- DELETE /api/v1/secrets/{name} - Delete secret

## Changes
- Added OAS 3.1 annotations to handler functions in internal/httpapi/server.go:1474-1658
- Defined SecretReference, SecretConfig, MigrateRequest schema components
- Marked all endpoints as requiring API key authentication
- Added request/response examples for migrate operation

## Testing
- Verified all endpoints appear in Swagger UI with lock icons
- Tested "Try it out" functionality with valid API key
- Confirmed OAS verification script reports zero missing endpoints
```
