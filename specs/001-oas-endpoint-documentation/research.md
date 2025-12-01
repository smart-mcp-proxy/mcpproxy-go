# Research: OpenAPI Documentation with swaggo/swag

**Feature**: Complete OpenAPI Documentation for REST API Endpoints
**Date**: 2025-11-28

## Research Questions

### 1. swaggo/swag v2.0.0-rc4 Best Practices for OpenAPI 3.1

**Question**: What are the annotation patterns and best practices for documenting REST endpoints using swaggo/swag v2.0.0-rc4 with OpenAPI 3.1 support?

**Findings**:

swaggo/swag uses specially formatted Go comments above handler functions to generate OpenAPI documentation. Key patterns identified from existing code review (`oas/docs.go` and `internal/httpapi/server.go`):

**Annotation Structure**:
```go
// HandlerName godoc
// @Summary      Short description
// @Description  Detailed multi-line description
// @Tags         category-name
// @Accept       json
// @Produce      json
// @Param        param_name  query/path/body  type  required  "description"
// @Success      200  {object}  contracts.ResponseType  "Success message"
// @Failure      400  {object}  contracts.ErrorResponse  "Error message"
// @Failure      401  {object}  contracts.ErrorResponse  "Unauthorized"
// @Security     ApiKeyAuth
// @Security     ApiKeyQuery
// @Router       /api/v1/endpoint [get/post/put/delete]
func (s *Server) HandlerName(w http.ResponseWriter, r *http.Request) {
    // Implementation
}
```

**Security Scheme Documentation**:
From existing OAS file (`oas/docs.go`), MCPProxy uses:
- `ApiKeyAuth`: API key in header (`X-API-Key`)
- `ApiKeyQuery`: API key in query parameter (`?apikey=`)

Endpoints requiring authentication should include:
```go
// @Security     ApiKeyAuth
// @Security     ApiKeyQuery
```

Endpoints that DON'T require authentication (health checks, swagger UI) should omit `@Security` annotations entirely.

**Schema Components**:
Define reusable request/response types in `internal/contracts/` package with struct tags:
```go
type ConfigResponse struct {
    Config   *config.Config `json:"config"`
    Valid    bool           `json:"valid"`
    Errors   []string       `json:"errors,omitempty"`
}
```

swag automatically discovers these types and generates schema components.

**SSE (Server-Sent Events) Documentation**:
For streaming endpoints, specify content type:
```go
// @Produce  text/event-stream
// @Success  200  {string}  string  "SSE stream of server events"
```

**Decision**: Use inline swag comments above existing handlers in `internal/httpapi/server.go`. No separate YAML files needed—swag generates `oas/swagger.yaml` from Go comments during `make swagger`.

**Rationale**: Keeps documentation close to code (reduces drift), leverages existing Makefile integration, and follows established pattern in the codebase.

---

### 2. Authentication Security Scheme Consistency

**Question**: How should we accurately document which endpoints require API key authentication and which don't, based on the middleware implementation?

**Findings**:

From code review of `internal/httpapi/server.go:135-184` (API key middleware):

**Authentication Logic**:
1. **Tray connections** (Unix socket/named pipe): Skip API key validation (lines 138-146)
   - Authenticated via OS-level permissions (UID/SID matching)
   - Tagged with `transport.ConnectionSourceTray`

2. **Empty API key config**: Authentication disabled (lines 165-168)
   - Allows all requests through without validation
   - For testing/development scenarios

3. **TCP connections with API key configured**: Require validation (lines 171-177)
   - Check `X-API-Key` header or `?apikey=` query parameter (lines 188-200)
   - Return 401 Unauthorized if missing/invalid

**Endpoint Categories**:

**Require Authentication** (`@Security` annotations needed):
- All `/api/v1/*` endpoints (mounted with API key middleware)
- `/events` SSE endpoint (supports both header and query parameter)

**No Authentication Required** (omit `@Security` annotations):
- `/healthz`, `/readyz`, `/livez`, `/ready` (health checks, mounted outside middleware)
- `/swagger/*` (Swagger UI static files, mounted outside middleware)

**Existing OAS Issues**:
Review of current `oas/swagger.yaml` shows some endpoints incorrectly marked:
- Some health endpoints may have security annotations (should be removed)
- Missing dual security options for SSE endpoint (both header and query param)

**Decision**:
1. Add `@Security ApiKeyAuth` and `@Security ApiKeyQuery` to all `/api/v1/*` endpoints
2. Add both security options to `/events` endpoint
3. Remove security annotations from health endpoints if present
4. Document in endpoint descriptions that Unix socket connections bypass authentication automatically

**Rationale**: Accurately reflects the actual middleware implementation and helps API consumers understand when they need to provide authentication.

---

### 3. OAS Coverage Verification Script Design

**Question**: What approach should we use for the automated OAS coverage verification script to detect missing endpoint documentation?

**Findings**:

**Verification Strategy Options**:

**Option A: Route Extraction from Go Code**
```bash
# Extract routes from chi router registrations
grep -E 'r\.(Get|Post|Put|Patch|Delete)\(' internal/httpapi/server.go | \
  sed -E 's/.*\.(Get|Post|Put|Patch|Delete)\("([^"]+)".*/\1 \2/' | \
  sort -u
```
**Pros**: Directly parses route registrations
**Cons**: Fragile regex, may miss dynamic routes or nested routers

**Option B: Documented Endpoint Extraction from OAS**
```bash
# Extract documented paths from swagger.yaml
grep -E '^  /' oas/swagger.yaml | sed 's/://g' | sort -u
```
**Pros**: Simple, reliable
**Cons**: Doesn't capture HTTP methods separately

**Option C: Hybrid Approach (Recommended)**
1. Extract route registrations from `internal/httpapi/server.go` with method + path
2. Extract documented endpoints from `oas/swagger.yaml` with method + path
3. Compare sets and report missing endpoints
4. Exclude known exceptions (health endpoints, static file handlers)

**Implementation Sketch**:
```bash
#!/bin/bash
# scripts/verify-oas-coverage.sh

# Extract routes from server.go
ROUTES=$(grep -E 'r\.(Get|Post|Put|Delete|Patch|Head)\(' internal/httpapi/server.go | \
  sed -E 's/.*r\.(Get|Post|Put|Delete|Patch|Head)\("([^"]+)".*/\U\1 \2/' | \
  grep -v '/ui' | grep -v '/swagger' | grep -v '{' | \
  sort -u)

# Extract documented paths from OAS
OAS_PATHS=$(grep -E '^  /' oas/swagger.yaml | sed 's/://g' | sort -u)

# Compare and report missing
comm -23 <(echo "$ROUTES") <(echo "$OAS_PATHS") > missing.txt

if [ -s missing.txt ]; then
  echo "❌ Missing OAS documentation for:"
  cat missing.txt
  exit 1
else
  echo "✅ All REST endpoints documented in OAS"
  exit 0
fi
```

**Decision**: Implement Option C (hybrid approach) with route extraction from `internal/httpapi/server.go` and documented endpoint extraction from `oas/swagger.yaml`.

**Rationale**: Provides automated detection of undocumented endpoints while being simple enough to maintain. Can be extended to handle dynamic routes if needed.

---

### 4. OpenAPI 3.1 Schema Components Organization

**Question**: How should we organize request/response schema components to avoid duplication and maintain clarity?

**Findings**:

**Existing Schema Organization**:
From `oas/swagger.yaml` review, schemas are organized under `components.schemas` with naming pattern:
- `contracts.{TypeName}`: Domain objects (servers, tools, diagnostics)
- `main.{TypeName}`: Endpoint-specific request/response types

**Best Practices Identified**:

1. **Use Existing Contract Types**: Many required schemas already exist in `internal/contracts/`:
   - `contracts.Server`, `contracts.Tool`, `contracts.LogEntry`
   - `contracts.ErrorResponse`, `contracts.SuccessResponse`
   - These are automatically discovered by swag

2. **Create New Contract Types for Missing Endpoints**:
   - Configuration management: `contracts.GetConfigResponse`, `contracts.ValidateConfigResponse`, `contracts.ConfigApplyResult`
   - Secrets management: `contracts.SecretReference`, `contracts.SecretConfig`, `contracts.MigrateSecretsRequest`
   - Tool call history: `contracts.ToolCallRecord`, `contracts.GetToolCallsResponse`, `contracts.ReplayToolCallRequest`
   - Sessions: `contracts.MCPSession`, `contracts.GetSessionsResponse`
   - Registries: `contracts.Registry`, `contracts.RegistryServer`

3. **Schema Reuse Pattern**:
```go
// Reuse common response wrapper
type GetConfigResponse struct {
    Success bool           `json:"success"`
    Data    *config.Config `json:"data"`
}

// Reuse common error response
// Already exists: contracts.ErrorResponse
```

**Decision**:
1. Review `internal/contracts/` to identify which schema types already exist
2. Add new contract types for missing endpoint responses
3. Use consistent naming: `{Action}{Resource}Response` (e.g., `GetConfigResponse`, `ValidateConfigResponse`)
4. Define contracts in `internal/contracts/` package so swag auto-discovers them

**Rationale**: Maintains existing schema organization pattern, enables reuse across endpoints, and keeps all contract types in a single discoverable location.

---

## Summary of Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| **Annotation Method** | Inline swag comments in Go handlers | Keeps docs close to code, prevents drift |
| **Security Documentation** | `@Security ApiKeyAuth` + `@Security ApiKeyQuery` for `/api/v1/*` and `/events`, omit for health endpoints | Accurately reflects middleware implementation |
| **OAS Verification** | Bash script comparing route registrations vs documented paths | Simple, maintainable, detects missing docs |
| **Schema Organization** | New contract types in `internal/contracts/` following `{Action}{Resource}Response` naming | Consistent with existing patterns, enables reuse |
| **Generation Tool** | Continue using swaggo/swag v2.0.0-rc4 with existing Makefile targets | No tooling changes needed, works with OpenAPI 3.1 |

## Alternatives Considered

### OpenAPI Generator vs swaggo/swag
- **Rejected**: OpenAPI Generator (code-first, external tool) would require rewriting existing OAS infrastructure
- **swaggo/swag already integrated**: Makefile targets exist, CI uses `make swagger-verify`, team familiar with annotation syntax

### Separate YAML Files vs Inline Annotations
- **Rejected**: Maintaining separate YAML files increases drift risk (docs separate from code)
- **Inline annotations preferred**: Documentation lives next to handler code, updated together during changes

### Manual OAS Verification vs Automated Script
- **Rejected**: Manual verification (developer remembers to document) is error-prone and created current 19-endpoint gap
- **Automated script preferred**: Prevents future drift, can be enforced in CI, zero human oversight required

## References

- swaggo/swag documentation: https://github.com/swaggo/swag
- OpenAPI 3.1 Specification: https://spec.openapis.org/oas/v3.1.0
- Existing MCPProxy OAS implementation: `/oas/docs.go`, `/oas/swagger.yaml`
- API key middleware: `/internal/httpapi/server.go:135-184`
- Existing contract types: `/internal/contracts/`
