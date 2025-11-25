# Quickstart: Management Service & OpenAPI Integration

**Feature**: 004-management-health-refactor
**Audience**: Developers implementing or integrating with the management service
**Date**: 2025-11-23

## Overview

This quickstart guide helps you:
1. Set up the management service in your development environment
2. Add swag annotations to REST handlers
3. Generate and validate OpenAPI specifications
4. Test the unified service layer

## Prerequisites

```bash
# Install swag CLI tool
go install github.com/swaggo/swag/cmd/swag@latest

# Verify installation
swag --version
# Expected: swag version v1.16.0 or later

# Install dependencies
cd /Users/user/repos/mcpproxy-go
go mod download
```

## Step 1: Create Management Service

### 1.1 Define Service Interface

Create `internal/management/service.go`:

```go
package management

import (
    "context"
    "mcpproxy-go/internal/contracts"
)

type Service interface {
    ListServers(ctx context.Context) ([]*contracts.Server, *contracts.ServerStats, error)
    RestartServer(ctx context.Context, name string) error
    RestartAll(ctx context.Context) (int, error)
    Doctor(ctx context.Context) (*contracts.Diagnostics, error)
    // ... other methods
}
```

### 1.2 Implement Service

See `contracts/management-service.go.example` for complete implementation.

Key patterns:
- Check config gates (`disable_management`, `read_only`) before write operations
- Delegate to existing `server.Manager` for actual server operations
- Emit events via `runtime.EventBus` for state changes
- Return consistent error types across all interfaces

### 1.3 Wire Into Runtime

Update `internal/runtime/runtime.go`:

```go
type Runtime struct {
    // ... existing fields ...
    managementService management.Service
}

func NewRuntime(...) *Runtime {
    // ... existing setup ...

    // Create management service
    mgmtService := management.NewService(
        manager,
        config,
        eventBus,
        logReader,
        secretResolver,
        logger,
    )

    return &Runtime{
        // ... existing assignments ...
        managementService: mgmtService,
    }
}

// Add getter for controllers to access
func (r *Runtime) GetManagementService() management.Service {
    return r.managementService
}
```

## Step 2: Update REST Handlers

### 2.1 Delegate to Service

Update `internal/httpapi/server.go`:

```go
func (s *Server) handleRestartServer(w http.ResponseWriter, r *http.Request) {
    name := chi.URLParam(r, "name")

    // OLD: Direct manager call
    // err := s.controller.RestartServer(name)

    // NEW: Delegate to management service
    mgmtService := s.controller.GetManagementService()
    err := mgmtService.RestartServer(r.Context(), name)

    if err != nil {
        s.writeError(w, http.StatusInternalServerError, err)
        return
    }

    s.writeJSON(w, http.StatusOK, map[string]string{
        "message": "Server restarted successfully",
    })
}
```

### 2.2 Add Swag Annotations

Add annotations above handler:

```go
// handleRestartServer restarts a server
// @Summary Restart an upstream server
// @Description Stops and restarts the connection to a specific upstream MCP server
// @Tags servers
// @Accept json
// @Produce json
// @Param name path string true "Server name"
// @Success 200 {object} map[string]string
// @Failure 400 {object} ErrorResponse
// @Failure 403 {object} ErrorResponse
// @Failure 404 {object} ErrorResponse
// @Router /servers/{name}/restart [post]
// @Security ApiKeyAuth || ApiKeyQuery
func (s *Server) handleRestartServer(w http.ResponseWriter, r *http.Request) {
    // ... implementation ...
}
```

See `contracts/swag-annotation-example.go` for complete patterns.

## Step 3: Add New Endpoints

### 3.1 Create Bulk Restart Endpoint

Add to `internal/httpapi/server.go`:

```go
// handleRestartAll restarts all servers
// @Summary Restart all upstream servers
// @Description Sequentially restarts all configured servers
// @Tags servers
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Failure 403 {object} ErrorResponse
// @Router /servers/restart_all [post]
// @Security ApiKeyAuth || ApiKeyQuery
func (s *Server) handleRestartAll(w http.ResponseWriter, r *http.Request) {
    mgmtService := s.controller.GetManagementService()
    count, err := mgmtService.RestartAll(r.Context())

    if err != nil {
        s.writeError(w, http.StatusInternalServerError, err)
        return
    }

    s.writeJSON(w, http.StatusOK, map[string]interface{}{
        "success_count": count,
        "message":       fmt.Sprintf("Restarted %d servers", count),
    })
}
```

Register route in `setupRoutes()`:

```go
r.Route("/api/v1", func(r chi.Router) {
    r.Use(s.apiKeyAuthMiddleware())

    // ... existing routes ...
    r.Post("/servers/restart_all", s.handleRestartAll)
})
```

### 3.2 Create Doctor Endpoint

```go
// handleGetDiagnostics runs health checks
// @Summary Run health diagnostics
// @Description Aggregates health information from all servers
// @Tags diagnostics
// @Produce json
// @Success 200 {object} contracts.Diagnostics
// @Router /doctor [get]
// @Security ApiKeyAuth || ApiKeyQuery
func (s *Server) handleGetDiagnostics(w http.ResponseWriter, r *http.Request) {
    mgmtService := s.controller.GetManagementService()
    diag, err := mgmtService.Doctor(r.Context())

    if err != nil {
        s.writeError(w, http.StatusInternalServerError, err)
        return
    }

    s.writeJSON(w, http.StatusOK, diag)
}
```

Register route:

```go
r.Get("/api/v1/doctor", s.handleGetDiagnostics)
```

## Step 4: Add API Metadata

Update `cmd/mcpproxy/main.go` with top-level annotations:

```go
// @title MCPProxy API
// @version 1.0.0
// @description Smart proxy for Model Context Protocol servers
// @host localhost:8080
// @BasePath /api/v1
// @securityDefinitions.apikey ApiKeyAuth
// @in header
// @name X-API-Key
// @securityDefinitions.apikey ApiKeyQuery
// @in query
// @name apikey
func main() {
    // ... existing code ...
}
```

## Step 5: Generate OpenAPI Spec

### 5.1 Update Makefile

Add swag generation to `Makefile`:

```makefile
.PHONY: swagger build

# Generate OpenAPI spec
swagger:
	@echo "📄 Generating OpenAPI specification..."
	swag init -g cmd/mcpproxy/main.go --output docs --outputTypes yaml
	@echo "✅ OpenAPI spec generated at docs/openapi.yaml"

# Update build target to include swagger
build: swagger frontend-build
	@echo "🔨 Building Go binary..."
	go build -o mcpproxy ./cmd/mcpproxy
	# ... rest of build ...
```

### 5.2 Generate Spec

```bash
make swagger

# Or manually:
swag init -g cmd/mcpproxy/main.go --output docs --outputTypes yaml
```

Expected output:
```
2025/11/23 10:00:00 Generate swagger docs....
2025/11/23 10:00:00 Generate general API Info, search dir:./cmd/mcpproxy
2025/11/23 10:00:01 create docs/docs.go at  docs/docs.go
2025/11/23 10:00:01 create docs/swagger.json at  docs/swagger.json
2025/11/23 10:00:01 create docs/swagger.yaml at  docs/swagger.yaml
```

### 5.3 Validate Spec

```bash
# Install validator (if needed)
npm install -g @apidevtools/swagger-cli

# Validate OpenAPI spec
swagger-cli validate docs/openapi.yaml

# Expected output:
# docs/openapi.yaml is valid
```

## Step 6: Serve Swagger UI

### 6.1 Add Swagger Handler

Create `internal/httpapi/swagger.go`:

```go
package httpapi

import (
    "github.com/go-chi/chi/v5"
    httpSwagger "github.com/swaggo/http-swagger"

    _ "mcpproxy-go/docs" // Import generated docs
)

func (s *Server) mountSwaggerUI(r chi.Router) {
    // Serve Swagger UI at /swagger/
    r.Mount("/swagger", httpSwagger.WrapHandler)
}
```

### 6.2 Register in Router

Update `setupRoutes()` in `internal/httpapi/server.go`:

```go
func (s *Server) setupRoutes() {
    // ... middleware setup ...

    // Mount Swagger UI
    s.mountSwaggerUI(s.router)

    // ... API routes ...
}
```

### 6.3 Test UI

```bash
# Start server
./mcpproxy serve

# Open browser to:
open http://localhost:8080/swagger/index.html
```

## Step 7: Update CLI Commands

### 7.1 Add CLI Client Methods

Update `internal/cliclient/client.go`:

```go
func (c *Client) RestartAll(ctx context.Context) (int, error) {
    resp, err := c.post(ctx, "/api/v1/servers/restart_all", nil)
    if err != nil {
        return 0, err
    }

    var result struct {
        SuccessCount int `json:"success_count"`
    }
    if err := json.Unmarshal(resp, &result); err != nil {
        return 0, err
    }

    return result.SuccessCount, nil
}

func (c *Client) GetDiagnostics(ctx context.Context) (*contracts.Diagnostics, error) {
    resp, err := c.get(ctx, "/api/v1/doctor")
    if err != nil {
        return nil, err
    }

    var diag contracts.Diagnostics
    if err := json.Unmarshal(resp, &diag); err != nil {
        return nil, err
    }

    return &diag, nil
}
```

### 7.2 Update CLI Commands

Update `cmd/mcpproxy/upstream_cmd.go`:

```go
func runUpstreamRestart(cmd *cobra.Command, args []string) error {
    client := cliclient.NewClient(socketPath, logger)

    if upstreamAll {
        count, err := client.RestartAll(ctx)
        if err != nil {
            return err
        }
        fmt.Printf("Restarted %d servers\n", count)
    } else {
        // ... individual restart logic ...
    }
    return nil
}
```

Update `cmd/mcpproxy/doctor_cmd.go`:

```go
func runDoctor(cmd *cobra.Command, args []string) error {
    client := cliclient.NewClient(socketPath, logger)

    diag, err := client.GetDiagnostics(ctx)
    if err != nil {
        return err
    }

    return outputDiagnostics(diag) // Uses existing formatter
}
```

## Step 8: Testing

### 8.1 Unit Tests

Create `internal/management/service_test.go`:

```go
package management

import (
    "context"
    "testing"

    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/mock"
)

func TestRestartServer_ChecksGates(t *testing.T) {
    config := &config.Config{DisableManagement: true}
    service := NewService(nil, config, nil, nil, nil, logger)

    err := service.RestartServer(context.Background(), "test-server")

    assert.Error(t, err)
    assert.Contains(t, err.Error(), "management operations are disabled")
}

func TestRestartAll_ReturnsPartialSuccess(t *testing.T) {
    // Mock setup...
    count, err := service.RestartAll(context.Background())

    assert.NoError(t, err)
    assert.Equal(t, 2, count) // Out of 3 servers
}

// Target: 80% coverage (SC-007)
```

### 8.2 Integration Tests

Add to `internal/httpapi/server_test.go`:

```go
func TestRestartAll_Endpoint(t *testing.T) {
    req := httptest.NewRequest("POST", "/api/v1/servers/restart_all", nil)
    req.Header.Set("X-API-Key", testAPIKey)

    w := httptest.NewRecorder()
    handler.ServeHTTP(w, req)

    assert.Equal(t, http.StatusOK, w.Code)

    var resp map[string]interface{}
    json.Unmarshal(w.Body.Bytes(), &resp)
    assert.Contains(t, resp, "success_count")
}
```

### 8.3 E2E Tests

```bash
# Existing test harness
./scripts/test-api-e2e.sh

# Should verify:
# - CLI restart --all works
# - REST POST /api/v1/servers/restart_all works
# - Both produce identical results
```

## Step 9: Verify Checklist

Before committing, verify:

- [x] Management service implements all interface methods
- [x] All REST handlers delegate to service (no direct manager calls)
- [x] All endpoints have swag annotations
- [x] OpenAPI spec validates successfully
- [x] Swagger UI accessible at /swagger/index.html
- [x] CLI commands work (backward compatibility)
- [x] Unit tests achieve 80% coverage
- [x] E2E tests pass
- [x] Linter passes (`./scripts/run-linter.sh`)

## Troubleshooting

### Swag Generation Fails

**Error**: `cannot find package`

**Solution**: Ensure all imports in annotated files are valid:
```bash
go mod tidy
go build ./cmd/mcpproxy
swag init -g cmd/mcpproxy/main.go --output docs --outputTypes yaml
```

### Swagger UI Shows 404

**Issue**: `/swagger/` returns 404

**Solution**: Check docs import:
```go
import (
    _ "mcpproxy-go/docs" // Must import generated docs
)
```

### API Key Auth Not Working

**Issue**: Swagger UI requests fail with 401

**Solution**: Configure authorization in Swagger UI (click "Authorize" button, enter API key)

## Next Steps

1. **Phase 2**: Run `/speckit.tasks` to generate actionable task list
2. **Implementation**: Follow tasks.md step-by-step
3. **Testing**: Maintain 80% coverage throughout
4. **Documentation**: Update CLAUDE.md and README.md after completion

## Resources

- [Swaggo Documentation](https://github.com/swaggo/swag)
- [OpenAPI 3.0 Specification](https://swagger.io/specification/)
- [Chi Router Guide](https://go-chi.io/)
- [MCPProxy Architecture](../../../CLAUDE.md)
