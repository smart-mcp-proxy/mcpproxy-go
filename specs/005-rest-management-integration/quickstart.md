# Quickstart Guide: REST Endpoint Management Service Integration

**Feature**: 005-rest-management-integration
**Created**: 2025-11-27
**Related**: [spec.md](./spec.md) | [plan.md](./plan.md) | [data-model.md](./data-model.md)

## Overview

This guide helps developers understand and implement the REST endpoint refactoring to use the management service layer. The refactoring ensures architectural compliance with spec 004's unified management pattern.

## What's Being Changed

**Before** (current state):
```
REST Handler → Runtime (bypasses management service)
```

**After** (target state):
```
REST Handler → Management Service → Runtime
```

**Why**: Ensures consistent behavior, configuration gate enforcement, and event emissions across all interfaces (CLI, REST, MCP).

## 5-Minute Quick Start

### 1. Understand the Current Code

The two REST endpoints currently bypass the management service:

**File**: `internal/httpapi/server.go:1155`
```go
func (s *Server) handleGetServerTools(w http.ResponseWriter, r *http.Request) {
    serverID := chi.URLParam(r, "id")

    // CURRENT: Direct runtime access (bypasses management service)
    tools, err := s.controller.GetServerTools(serverID)

    // ... rest of handler
}
```

**File**: `internal/httpapi/server.go:1050`
```go
func (s *Server) handleServerLogin(w http.ResponseWriter, r *http.Request) {
    serverID := chi.URLParam(r, "id")

    // CURRENT: Direct runtime access (bypasses management service)
    if err := s.controller.TriggerOAuthLogin(serverID); err != nil {
        // ... error handling
    }

    // ... rest of handler
}
```

### 2. Add Methods to Management Service Interface

**File**: `internal/management/service.go:27-84`

Add these two methods to the `Service` interface:

```go
type Service interface {
    // ... existing methods ...

    // GetServerTools retrieves all tools for a specific upstream server.
    // Delegates to runtime's GetServerTools() which reads from StateView cache.
    GetServerTools(ctx context.Context, name string) ([]map[string]interface{}, error)

    // TriggerOAuthLogin initiates OAuth authentication flow for a server.
    // Enforces configuration gates and emits events on completion.
    TriggerOAuthLogin(ctx context.Context, name string) error
}
```

### 3. Implement the Methods

**File**: `internal/management/service_impl.go` (or in existing implementation file)

```go
func (s *serviceImpl) GetServerTools(ctx context.Context, name string) ([]map[string]interface{}, error) {
    // Validate input
    if name == "" {
        return nil, fmt.Errorf("server name required")
    }

    // Delegate to runtime (existing implementation)
    tools, err := s.runtime.GetServerTools(name)
    if err != nil {
        return nil, fmt.Errorf("failed to get tools: %w", err)
    }

    return tools, nil
}

func (s *serviceImpl) TriggerOAuthLogin(ctx context.Context, name string) error {
    // Validate input
    if name == "" {
        return fmt.Errorf("server name required")
    }

    // Check configuration gates
    cfg := s.config.Load().(*config.Config)
    if cfg.DisableManagement {
        return fmt.Errorf("operation blocked: management disabled")
    }
    if cfg.ReadOnly {
        return fmt.Errorf("operation blocked: read-only mode")
    }

    // Delegate to upstream manager (existing implementation)
    if err := s.runtime.TriggerOAuthLogin(name); err != nil {
        return fmt.Errorf("failed to start OAuth: %w", err)
    }

    // Event will be emitted by upstream manager on OAuth completion
    // (existing behavior - no changes needed)

    return nil
}
```

### 4. Update REST Handlers

**File**: `internal/httpapi/server.go:1155`

```go
func (s *Server) handleGetServerTools(w http.ResponseWriter, r *http.Request) {
    serverID := chi.URLParam(r, "id")
    if serverID == "" {
        s.writeError(w, http.StatusBadRequest, "Server ID required")
        return
    }

    // NEW: Call management service instead of controller
    mgmtSvc := s.controller.GetManagementService().(ManagementService)
    tools, err := mgmtSvc.GetServerTools(r.Context(), serverID)
    if err != nil {
        s.logger.Error("Failed to get server tools", "server", serverID, "error", err)

        // Map errors to HTTP status codes
        if strings.Contains(err.Error(), "not found") {
            s.writeError(w, http.StatusNotFound, fmt.Sprintf("Server not found: %s", serverID))
            return
        }
        s.writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to get tools: %v", err))
        return
    }

    // ... rest unchanged (convert and return response)
}
```

**File**: `internal/httpapi/server.go:1050`

```go
func (s *Server) handleServerLogin(w http.ResponseWriter, r *http.Request) {
    serverID := chi.URLParam(r, "id")
    if serverID == "" {
        s.writeError(w, http.StatusBadRequest, "Server ID required")
        return
    }

    // NEW: Call management service instead of controller
    mgmtSvc := s.controller.GetManagementService().(ManagementService)
    if err := mgmtSvc.TriggerOAuthLogin(r.Context(), serverID); err != nil {
        s.logger.Error("Failed to trigger OAuth login", "server", serverID, "error", err)

        // Map errors to HTTP status codes
        if strings.Contains(err.Error(), "management disabled") || strings.Contains(err.Error(), "read-only") {
            s.writeError(w, http.StatusForbidden, err.Error())
            return
        }
        if strings.Contains(err.Error(), "not found") {
            s.writeError(w, http.StatusNotFound, fmt.Sprintf("Server not found: %s", serverID))
            return
        }
        s.writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to trigger login: %v", err))
        return
    }

    // ... rest unchanged (return success response)
}
```

### 5. Add Unit Tests

**File**: `internal/management/service_test.go`

```go
func TestServiceImpl_GetServerTools(t *testing.T) {
    tests := []struct {
        name        string
        serverName  string
        mockTools   []map[string]interface{}
        mockError   error
        wantErr     bool
        errContains string
    }{
        {
            name:       "valid server returns tools",
            serverName: "test-server",
            mockTools: []map[string]interface{}{
                {"name": "test_tool", "description": "A test tool"},
            },
            wantErr: false,
        },
        {
            name:        "empty server name returns error",
            serverName:  "",
            wantErr:     true,
            errContains: "server name required",
        },
        {
            name:        "server not found returns error",
            serverName:  "nonexistent",
            mockError:   fmt.Errorf("server not found: nonexistent"),
            wantErr:     true,
            errContains: "failed to get tools",
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            // Setup mocks
            mockRuntime := &MockRuntime{
                tools: tt.mockTools,
                err:   tt.mockError,
            }
            svc := &serviceImpl{runtime: mockRuntime}

            // Execute
            tools, err := svc.GetServerTools(context.Background(), tt.serverName)

            // Assert
            if tt.wantErr {
                require.Error(t, err)
                assert.Contains(t, err.Error(), tt.errContains)
            } else {
                require.NoError(t, err)
                assert.Equal(t, tt.mockTools, tools)
            }
        })
    }
}

func TestServiceImpl_TriggerOAuthLogin(t *testing.T) {
    tests := []struct {
        name              string
        serverName        string
        disableManagement bool
        readOnly          bool
        mockError         error
        wantErr           bool
        errContains       string
        expectedHTTPStatus int
    }{
        {
            name:       "valid server triggers OAuth",
            serverName: "test-server",
            wantErr:    false,
        },
        {
            name:        "empty server name returns error",
            serverName:  "",
            wantErr:     true,
            errContains: "server name required",
        },
        {
            name:              "disable_management blocks operation",
            serverName:        "test-server",
            disableManagement: true,
            wantErr:           true,
            errContains:       "management disabled",
            expectedHTTPStatus: 403,
        },
        {
            name:              "read_only blocks operation",
            serverName:        "test-server",
            readOnly:          true,
            wantErr:           true,
            errContains:       "read-only mode",
            expectedHTTPStatus: 403,
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            // Setup mocks with config
            cfg := &config.Config{
                DisableManagement: tt.disableManagement,
                ReadOnly:          tt.readOnly,
            }
            mockRuntime := &MockRuntime{err: tt.mockError}
            svc := &serviceImpl{
                runtime: mockRuntime,
                config:  atomic.Value{},
            }
            svc.config.Store(cfg)

            // Execute
            err := svc.TriggerOAuthLogin(context.Background(), tt.serverName)

            // Assert
            if tt.wantErr {
                require.Error(t, err)
                assert.Contains(t, err.Error(), tt.errContains)
            } else {
                require.NoError(t, err)
            }
        })
    }
}
```

### 6. Run Tests

```bash
# Run unit tests for management service
go test ./internal/management/... -v

# Run E2E API tests to verify backward compatibility
./scripts/test-api-e2e.sh

# Test CLI commands (from PR #152)
mcpproxy serve &  # Start daemon
mcpproxy tools list --server=test-server
mcpproxy auth login --server=test-server
```

## Key Concepts

### Management Service Pattern

The management service acts as a **centralized application layer** between presentation (REST/CLI/MCP) and infrastructure (runtime/database):

```
┌─────────────────────────────────────┐
│   Presentation Layer                │
│   - REST handlers                   │
│   - CLI commands                    │
│   - MCP tools                       │
└──────────────┬──────────────────────┘
               │
               ▼
┌─────────────────────────────────────┐
│   Application Layer                 │
│   - ManagementService               │
│   - Config gate enforcement         │
│   - Event emissions                 │
└──────────────┬──────────────────────┘
               │
               ▼
┌─────────────────────────────────────┐
│   Infrastructure Layer              │
│   - Runtime (server lifecycle)      │
│   - Upstream Manager (connections)  │
│   - Event Bus (notifications)       │
└─────────────────────────────────────┘
```

### Configuration Gates

The management service enforces two configuration gates:

1. **`disable_management`**: Blocks ALL write operations (enable, disable, restart, OAuth)
2. **`read_only`**: Blocks configuration changes (more restrictive than disable_management)

These gates are checked **once** in the management service, not duplicated across REST/CLI/MCP.

### Event Emissions

When `TriggerOAuthLogin()` completes successfully:

1. Upstream manager updates server authentication state
2. Upstream manager emits `servers.changed` event
3. Event bus broadcasts to all subscribers
4. SSE endpoint `/events` sends event to connected clients
5. Tray UI and Web UI refresh automatically

## Common Patterns

### Error Handling Pattern

```go
// In management service implementation
if err := s.runtime.SomeOperation(name); err != nil {
    // Wrap error with context
    return fmt.Errorf("failed to do something: %w", err)
}

// In REST handler
if err := mgmtSvc.SomeMethod(ctx, serverID); err != nil {
    // Map error to HTTP status
    if strings.Contains(err.Error(), "not found") {
        s.writeError(w, http.StatusNotFound, err.Error())
        return
    }
    if strings.Contains(err.Error(), "blocked") {
        s.writeError(w, http.StatusForbidden, err.Error())
        return
    }
    s.writeError(w, http.StatusInternalServerError, err.Error())
    return
}
```

### Config Gate Pattern

```go
// In management service implementation
cfg := s.config.Load().(*config.Config)
if cfg.DisableManagement {
    return fmt.Errorf("operation blocked: management disabled")
}
if cfg.ReadOnly {
    return fmt.Errorf("operation blocked: read-only mode")
}
```

### Delegation Pattern

```go
// Management service delegates to existing implementations
func (s *serviceImpl) GetServerTools(ctx context.Context, name string) ([]map[string]interface{}, error) {
    // Validation only - no business logic
    if name == "" {
        return nil, fmt.Errorf("server name required")
    }

    // Delegate to existing runtime method
    return s.runtime.GetServerTools(name)
}
```

## Testing Strategy

### Unit Tests (Target: 80% Coverage)

Test each method with:
- ✅ Valid inputs (success case)
- ✅ Empty/nil inputs (validation errors)
- ✅ Config gates enabled (403 errors)
- ✅ Server not found (404 errors)
- ✅ Runtime errors (500 errors)

### Integration Tests

- ✅ REST handlers call management service methods
- ✅ Events emitted and propagated to SSE endpoint
- ✅ CLI commands work via socket connection

### E2E Tests

- ✅ Existing `./scripts/test-api-e2e.sh` passes without modification
- ✅ No behavioral changes from user perspective
- ✅ Backward compatibility maintained

## Troubleshooting

### Issue: "interface conversion: interface {} is nil, not management.Service"

**Cause**: Management service not initialized in runtime

**Fix**: Verify `runtime.SetManagementService()` is called during server initialization

### Issue: "operation blocked: management disabled"

**Cause**: `disable_management=true` in config

**Fix**: Either remove config gate or accept that write operations are blocked (this is expected behavior)

### Issue: Tests failing with "server not found"

**Cause**: Mock runtime doesn't have server in state

**Fix**: Add server to mock state in test setup

## Next Steps

After completing this refactoring:

1. **Run `/speckit.tasks`** to generate detailed task breakdown
2. **Implement tasks sequentially** (interface → implementation → tests → handlers)
3. **Verify E2E tests pass** before submitting PR
4. **Update CLAUDE.md** if management service patterns change

## References

- [Spec 004: Management Service Architecture](../../004-management-health-refactor/spec.md)
- [PR #152: CLI Socket Support](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/152)
- [Management Service Interface](./contracts/management-service.yaml)
- [Data Model](./data-model.md)
- [Implementation Plan](./plan.md)
