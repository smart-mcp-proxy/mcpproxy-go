# Quickstart: Adding OpenAPI Documentation to MCPProxy Endpoints

**Feature**: Complete OpenAPI Documentation for REST API Endpoints
**Audience**: MCPProxy developers adding new REST endpoints or fixing documentation
**Time to Complete**: 10-15 minutes per endpoint

## Prerequisites

- Go 1.24.0 installed
- swaggo/swag v2.0.0-rc4 installed (`go install github.com/swaggo/swag/v2/cmd/swag@v2.0.0-rc4`)
- MCPProxy repository cloned
- Familiarity with OpenAPI 3.1 specification basics

## Quick Start (5 Steps)

### Step 1: Define Request/Response Contracts (2 minutes)

Create Go struct types in `internal/contracts/` for your endpoint's request/response bodies:

```go
// internal/contracts/config.go (example)
package contracts

import "github.com/smart-mcp-proxy/mcpproxy-go/internal/config"

// GetConfigResponse represents the response for GET /api/v1/config
type GetConfigResponse struct {
    Success bool          `json:"success"`
    Config  *config.Config `json:"config"`
}

// ValidateConfigResponse represents the response for POST /api/v1/config/validate
type ValidateConfigResponse struct {
    Success bool     `json:"success"`
    Valid   bool     `json:"valid"`
    Errors  []string `json:"errors,omitempty"`
}
```

**Tips**:
- Use JSON struct tags for field names (`json:"field_name"`)
- Add `omitempty` for optional fields
- Reuse existing types (`config.Config`, `contracts.ErrorResponse`)
- Follow naming pattern: `{Action}{Resource}Response` (e.g., `GetConfigResponse`, `ListSessionsResponse`)

---

### Step 2: Add swag Annotations to Handler (5 minutes)

Add swag comment block above your handler function in `internal/httpapi/server.go`:

```go
// GetConfig godoc
// @Summary      Get current configuration
// @Description  Retrieves the current MCPProxy configuration including all server definitions
// @Tags         config
// @Produce      json
// @Success      200  {object}  contracts.GetConfigResponse  "Configuration retrieved successfully"
// @Failure      401  {object}  contracts.ErrorResponse      "Unauthorized - missing or invalid API key"
// @Failure      500  {object}  contracts.ErrorResponse      "Internal server error"
// @Security     ApiKeyAuth
// @Security     ApiKeyQuery
// @Router       /api/v1/config [get]
func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
    // Existing implementation (no changes needed)
}
```

**Annotation Reference**:

| Annotation | Purpose | Example |
|------------|---------|---------|
| `@Summary` | One-line description | `@Summary Get current configuration` |
| `@Description` | Detailed explanation | `@Description Retrieves the current MCPProxy configuration...` |
| `@Tags` | Category for grouping | `@Tags config` |
| `@Accept` | Request content type | `@Accept json` (for POST/PUT/PATCH) |
| `@Produce` | Response content type | `@Produce json` or `text/event-stream` |
| `@Param` | Path/query/body parameters | `@Param limit query int false "Maximum results"` |
| `@Success` | Success response | `@Success 200 {object} contracts.ResponseType "Description"` |
| `@Failure` | Error response | `@Failure 400 {object} contracts.ErrorResponse "Bad request"` |
| `@Security` | Auth requirement | `@Security ApiKeyAuth` |
| `@Router` | Route path and method | `@Router /api/v1/config [get]` |

**Parameter Syntax**:
```
@Param name location type required "description"
       ↓    ↓        ↓    ↓        ↓
       id   path     string true   "Server ID or name"
       limit query   int    false  "Maximum results (default 50)"
       config body   config.Config true "Configuration to validate"
```

**Security Annotations**:
- All `/api/v1/*` endpoints: Add `@Security ApiKeyAuth` AND `@Security ApiKeyQuery`
- Health endpoints (`/healthz`, etc.): Omit `@Security` annotations entirely
- SSE endpoint (`/events`): Add both security annotations (supports query param auth for browsers)

---

### Step 3: Generate OpenAPI Specification (1 minute)

Run the Makefile target to regenerate the OAS from your annotations:

```bash
make swagger
```

**What This Does**:
1. Scans all Go files in `internal/httpapi/` for swag comments
2. Discovers contract types in `internal/contracts/`
3. Generates `oas/swagger.yaml` (OpenAPI 3.1 spec)
4. Generates `oas/docs.go` (embedded Go representation)

**Expected Output**:
```
✅ OpenAPI 3.1 spec generated: oas/swagger.yaml and oas/docs.go
```

---

### Step 4: Verify in Swagger UI (2 minutes)

Start MCPProxy and open Swagger UI:

```bash
# Build and run
go build -o mcpproxy ./cmd/mcpproxy
./mcpproxy serve

# Open browser
open http://localhost:8080/swagger/
```

**Verification Checklist**:
- [ ] Your endpoint appears in the correct category (Tags)
- [ ] Summary and description are clear
- [ ] All parameters show correct types and required status
- [ ] Success/failure responses include all status codes
- [ ] Lock icon appears next to endpoint name (authentication required)
- [ ] "Try it out" button works with valid API key

**Testing "Try it out"**:
1. Click the endpoint in Swagger UI
2. Click "Try it out"
3. Enter API key in the "ApiKeyAuth" or "apikey" field (get from `~/.mcpproxy/mcp_config.json`)
4. Fill in required parameters
5. Click "Execute"
6. Verify response matches expected schema

---

### Step 5: Run OAS Coverage Verification (1 minute)

Ensure all endpoints are documented:

```bash
./scripts/verify-oas-coverage.sh
```

**Expected Output (All Documented)**:
```
✅ All REST endpoints documented in OAS
```

**If Missing Endpoints Detected**:
```
❌ Missing OAS documentation for:
POST /api/v1/secrets/migrate
GET /api/v1/sessions
```

Fix by repeating Steps 1-3 for the missing endpoints.

---

## Common Patterns

### Pattern 1: Simple GET Endpoint (No Parameters)

```go
// GetStatus godoc
// @Summary      Get server status
// @Description  Returns current server running state and statistics
// @Tags         status
// @Produce      json
// @Success      200  {object}  contracts.StatusResponse
// @Failure      500  {object}  contracts.ErrorResponse
// @Security     ApiKeyAuth
// @Security     ApiKeyQuery
// @Router       /api/v1/status [get]
func (s *Server) handleGetStatus(w http.ResponseWriter, r *http.Request) { ... }
```

---

### Pattern 2: GET with Query Parameters

```go
// ListToolCalls godoc
// @Summary      List tool call history
// @Description  Retrieves paginated tool call history with optional session filtering
// @Tags         tool-calls
// @Produce      json
// @Param        limit      query  int     false  "Maximum results (default 50, max 1000)"
// @Param        offset     query  int     false  "Pagination offset (default 0)"
// @Param        session_id query  string  false  "Filter by session ID"
// @Success      200  {object}  contracts.GetToolCallsResponse
// @Failure      400  {object}  contracts.ErrorResponse  "Invalid query parameters"
// @Failure      401  {object}  contracts.ErrorResponse  "Unauthorized"
// @Failure      500  {object}  contracts.ErrorResponse  "Internal server error"
// @Security     ApiKeyAuth
// @Security     ApiKeyQuery
// @Router       /api/v1/tool-calls [get]
func (s *Server) handleListToolCalls(w http.ResponseWriter, r *http.Request) { ... }
```

---

### Pattern 3: POST with Request Body

```go
// ApplyConfig godoc
// @Summary      Apply configuration changes
// @Description  Validates, applies, and persists configuration changes
// @Tags         config
// @Accept       json
// @Produce      json
// @Param        config  body  config.Config  true  "Configuration to apply"
// @Success      200  {object}  contracts.ConfigApplyResult  "Configuration applied successfully"
// @Failure      400  {object}  contracts.ConfigApplyResult  "Validation failed (check errors field)"
// @Failure      401  {object}  contracts.ErrorResponse      "Unauthorized"
// @Failure      403  {object}  contracts.ErrorResponse      "Management operations disabled"
// @Failure      500  {object}  contracts.ErrorResponse      "Failed to apply configuration"
// @Security     ApiKeyAuth
// @Security     ApiKeyQuery
// @Router       /api/v1/config/apply [post]
func (s *Server) handleApplyConfig(w http.ResponseWriter, r *http.Request) { ... }
```

---

### Pattern 4: Path Parameter Endpoint

```go
// GetServerLogs godoc
// @Summary      Get server logs
// @Description  Retrieves recent log entries for a specific upstream server
// @Tags         servers
// @Produce      json
// @Param        id    path   string  true   "Server ID or name"
// @Param        tail  query  int     false  "Number of log lines (default 100, max 1000)"
// @Success      200  {object}  contracts.GetServerLogsResponse  "Server logs retrieved successfully"
// @Failure      400  {object}  contracts.ErrorResponse          "Bad request (missing server ID)"
// @Failure      401  {object}  contracts.ErrorResponse          "Unauthorized"
// @Failure      404  {object}  contracts.ErrorResponse          "Server not found"
// @Failure      500  {object}  contracts.ErrorResponse          "Internal server error"
// @Security     ApiKeyAuth
// @Security     ApiKeyQuery
// @Router       /api/v1/servers/{id}/logs [get]
func (s *Server) handleGetServerLogs(w http.ResponseWriter, r *http.Request) {
    serverID := chi.URLParam(r, "id") // Extract path parameter
    // ...
}
```

---

### Pattern 5: SSE (Server-Sent Events) Endpoint

```go
// StreamEvents godoc
// @Summary      Server-Sent Events stream
// @Description  Real-time event stream for server status changes and config reloads. Supports API key via query parameter for browser compatibility.
// @Tags         events
// @Produce      text/event-stream
// @Param        apikey  query  string  false  "API key for authentication (alternative to header)"
// @Success      200  {string}  string  "SSE stream of events (event types: servers.changed, config.reloaded)"
// @Failure      401  {object}  contracts.ErrorResponse  "Unauthorized"
// @Security     ApiKeyAuth
// @Security     ApiKeyQuery
// @Router       /events [get]
func (s *Server) handleStreamEvents(w http.ResponseWriter, r *http.Request) { ... }
```

**Note**: SSE uses `@Produce text/event-stream` instead of `json`.

---

## Troubleshooting

### Issue: swag binary not found

**Error**:
```
⚠️  swag binary not found at /Users/user/go/bin/swag
```

**Solution**:
```bash
go install github.com/swaggo/swag/v2/cmd/swag@v2.0.0-rc4
```

---

### Issue: Endpoint doesn't appear in Swagger UI

**Possible Causes**:
1. Missing `godoc` comment before handler function
2. Typo in `@Router` path or method
3. Handler not registered in router (check `internal/httpapi/server.go` route registrations)
4. Forgot to run `make swagger` after adding annotations

**Solution**:
1. Verify swag comment block syntax (no typos in annotations)
2. Ensure `@Router` path exactly matches chi route registration
3. Re-run `make swagger`
4. Hard refresh browser (Cmd+Shift+R on macOS)

---

### Issue: Lock icon not showing (authentication not required)

**Possible Causes**:
1. Missing `@Security ApiKeyAuth` and `@Security ApiKeyQuery` annotations
2. Endpoint mounted outside API key middleware

**Solution**:
For `/api/v1/*` endpoints:
```go
// @Security     ApiKeyAuth
// @Security     ApiKeyQuery
```

For health endpoints (correct - no auth required):
```go
// Do NOT add @Security annotations
```

---

### Issue: OAS coverage script reports missing endpoints

**Error**:
```
❌ Missing OAS documentation for:
POST /api/v1/secrets/migrate
```

**Solution**:
1. Find the handler function for the missing endpoint
2. Add swag comment block (see patterns above)
3. Run `make swagger`
4. Re-run `./scripts/verify-oas-coverage.sh`

---

## Best Practices

### 1. Keep Documentation Close to Code
- Add swag comments directly above handler functions
- Update documentation when modifying endpoints
- Run `make swagger` before committing

### 2. Use Descriptive Summaries
```go
// Good
// @Summary Get server logs with pagination

// Bad
// @Summary Logs
```

### 3. Document All Error Responses
Include all possible HTTP status codes:
```go
// @Failure 400 {object} contracts.ErrorResponse "Bad request"
// @Failure 401 {object} contracts.ErrorResponse "Unauthorized"
// @Failure 403 {object} contracts.ErrorResponse "Forbidden"
// @Failure 404 {object} contracts.ErrorResponse "Not found"
// @Failure 500 {object} contracts.ErrorResponse "Internal server error"
```

### 4. Reuse Contract Types
Don't duplicate schema definitions:
```go
// Good - reuse existing type
// @Success 200 {object} contracts.ErrorResponse

// Bad - inline definition
// @Success 200 {object} object{success=bool,error=string}
```

### 5. Test "Try it out" Functionality
Always verify endpoints work in Swagger UI before committing.

---

## CI Integration

The MCPProxy CI pipeline includes OAS verification:

```yaml
# .github/workflows/verify-oas.yml
- name: Verify OAS Coverage
  run: |
    make swagger-verify
    ./scripts/verify-oas-coverage.sh
```

**What This Checks**:
1. `make swagger-verify`: Regenerates OAS and fails if artifacts are dirty (docs not up to date)
2. `verify-oas-coverage.sh`: Detects undocumented endpoints and fails if any found

**Pre-Commit Checklist**:
- [ ] Added swag annotations to new endpoints
- [ ] Defined contract types in `internal/contracts/`
- [ ] Ran `make swagger` and committed generated files
- [ ] Verified endpoint in Swagger UI
- [ ] Ran `./scripts/verify-oas-coverage.sh` locally (all pass)
- [ ] All tests pass (`./scripts/test-api-e2e.sh`)

---

## Additional Resources

- **swaggo/swag Documentation**: https://github.com/swaggo/swag
- **OpenAPI 3.1 Specification**: https://spec.openapis.org/oas/v3.1.0
- **MCPProxy OAS Examples**: `internal/httpapi/server.go` (existing annotated handlers)
- **Contract Types Reference**: `internal/contracts/` package
- **Swagger UI Local**: http://localhost:8080/swagger/ (when MCPProxy is running)

---

## Summary

**5-Step Process**:
1. Define contract types in `internal/contracts/`
2. Add swag annotations above handler functions
3. Run `make swagger` to regenerate OAS
4. Verify in Swagger UI at `http://localhost:8080/swagger/`
5. Run `./scripts/verify-oas-coverage.sh` to ensure 100% coverage

**Time per Endpoint**: ~10-15 minutes
**Result**: Complete, accurate API documentation that prevents drift and enables automated client generation
