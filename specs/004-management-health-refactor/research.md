# Research: Management Service Refactoring & OpenAPI Generation

**Feature**: 004-management-health-refactor
**Date**: 2025-11-23
**Status**: Complete

## Overview

This document consolidates research findings for implementing a unified management service layer and automated OpenAPI documentation generation.

## Research Questions

### Q1: OpenAPI Generation Tool Selection for Go with Chi Router

**Decision**: Use swaggo/swag for annotation-based OpenAPI 3.x generation

**Rationale**:
1. **Chi Router Compatibility**: Swag supports chi router via `swaggo/http-swagger` package
2. **Annotation-Based**: Generates specs from Go comments, keeping docs near code
3. **OpenAPI 3.x Support**: Produces OpenAPI 3.0+ specifications (not just Swagger 2.0)
4. **Active Maintenance**: 13k+ GitHub stars, regular updates, strong community
5. **Build Integration**: Simple `swag init` command integrates easily into Makefile
6. **Existing MCPProxy Patterns**: Matches Go comment-based documentation style already used

**Alternatives Considered**:
- **go-swagger**: Older, primarily Swagger 2.0, more complex tooling
- **ogen**: Code-first approach (generates Go from spec), not annotation-based
- **oapi-codegen**: Spec-first (generates code from OpenAPI), doesn't meet "generate from code" requirement
- **Framework wrappers (Fuego, etc.)**: Would require rewriting HTTP handlers, not feasible for refactoring

**Implementation Details**:
```go
// Example main.go annotation
// @title MCPProxy API
// @version 1.0
// @description Smart proxy for Model Context Protocol (MCP) servers
// @contact.name MCPProxy Support
// @contact.url https://github.com/smart-mcp-proxy/mcpproxy-go
// @license.name MIT
// @host localhost:8080
// @BasePath /api/v1
// @securityDefinitions.apikey ApiKeyAuth
// @in header
// @name X-API-Key
// @securityDefinitions.apikey ApiKeyQuery
// @in query
// @name apikey
```

```go
// Example handler annotation
// @Summary List all upstream servers
// @Description Returns all configured MCP servers with status
// @Tags servers
// @Produce json
// @Param apikey query string false "API Key (alternative to header)"
// @Success 200 {array} contracts.Server
// @Failure 401 {object} ErrorResponse
// @Router /servers [get]
// @Security ApiKeyAuth || ApiKeyQuery
func (s *Server) handleGetServers(w http.ResponseWriter, r *http.Request)
```

**Integration Steps**:
1. Install: `go install github.com/swaggo/swag/cmd/swag@latest`
2. Add to `go.mod`: `github.com/swaggo/http-swagger`
3. Annotate `cmd/mcpproxy/main.go` with API metadata
4. Annotate all `internal/httpapi/server.go` handlers
5. Add to Makefile: `swag init -g cmd/mcpproxy/main.go --output docs --outputTypes yaml`
6. Mount Swagger UI: `r.Mount("/swagger", httpSwagger.WrapHandler)`

**References**:
- [swaggo/swag GitHub](https://github.com/swaggo/swag)
- [Using chi with swaggo/swag](https://medium.com/@ganpat.bit/using-chi-with-swaggo-swag-ac8e1c97d10f)
- [swaggo/http-swagger for Chi](https://github.com/swaggo/http-swagger)

### Q2: Management Service Interface Design Pattern

**Decision**: Use Go interface with struct implementation following repository pattern

**Rationale**:
1. **Testability**: Interface enables mock implementations for unit tests
2. **Dependency Injection**: Runtime can inject concrete service into controllers
3. **Existing Patterns**: Matches `ServerController` interface pattern in `internal/httpapi/`
4. **No Breaking Changes**: Existing handlers adapt via composition, not inheritance
5. **Go Idioms**: Interfaces defined by consumers, not implementers (accept interfaces, return structs)

**Pattern**:
```go
// internal/management/service.go
package management

type Service interface {
    // Lifecycle operations
    ListServers(ctx context.Context) ([]*contracts.Server, *contracts.ServerStats, error)
    EnableServer(ctx context.Context, name string, enabled bool) error
    RestartServer(ctx context.Context, name string) error
    RestartAll(ctx context.Context) (int, error)

    // Diagnostics
    Doctor(ctx context.Context) (*contracts.Diagnostics, error)

    // Logs
    GetServerLogs(ctx context.Context, name string, tail int) ([]contracts.LogEntry, error)
}

type service struct {
    manager      *server.Manager         // Existing upstream manager
    config       *config.Config          // Config for gates
    eventBus     *runtime.EventBus       // Event emissions
    logReader    *logs.Reader            // Log retrieval
    secretResolver *secret.Resolver      // Secret checking
    logger       *zap.SugaredLogger
}

func NewService(manager *server.Manager, config *config.Config, eventBus *runtime.EventBus, logReader *logs.Reader, secretResolver *secret.Resolver, logger *zap.SugaredLogger) Service {
    return &service{
        manager: manager,
        config: config,
        eventBus: eventBus,
        logReader: logReader,
        secretResolver: secretResolver,
        logger: logger,
    }
}
```

**Gate Enforcement Pattern**:
```go
func (s *service) EnableServer(ctx context.Context, name string, enabled bool) error {
    // Check gates
    if s.config.DisableManagement {
        return errors.New("management operations disabled in config")
    }
    if s.config.ReadOnly {
        return errors.New("server is in read-only mode")
    }

    // Execute operation via existing manager
    if err := s.manager.EnableServer(name, enabled); err != nil {
        return fmt.Errorf("failed to enable server: %w", err)
    }

    // Emit event
    s.eventBus.Emit(runtime.Event{
        Type: "servers.changed",
        Data: map[string]interface{}{"server": name, "enabled": enabled},
    })

    return nil
}
```

**Alternatives Considered**:
- **Static functions**: No state management, harder to test
- **Singleton**: Global state anti-pattern, breaks testability
- **Abstract class**: Go doesn't have inheritance, N/A

### Q3: Diagnostics Aggregation Strategy

**Decision**: Centralized `Doctor()` method collecting diagnostics from multiple sources

**Rationale**:
1. **Single Responsibility**: Doctor method owns aggregation logic
2. **Extensibility**: Easy to add new diagnostic checks
3. **Performance**: Runs checks concurrently with context timeout
4. **Consistency**: Same data structure for CLI/REST/MCP

**Implementation Pattern**:
```go
type Diagnostics struct {
    TotalIssues      int                   `json:"total_issues"`
    UpstreamErrors   []UpstreamError       `json:"upstream_errors"`
    OAuthRequired    []OAuthRequirement    `json:"oauth_required"`
    MissingSecrets   []MissingSecret       `json:"missing_secrets"`
    RuntimeWarnings  []string              `json:"runtime_warnings"`
    DockerStatus     *DockerStatus         `json:"docker_status,omitempty"`
    Timestamp        time.Time             `json:"timestamp"`
}

func (s *service) Doctor(ctx context.Context) (*contracts.Diagnostics, error) {
    diag := &contracts.Diagnostics{Timestamp: time.Now()}

    // Collect upstream errors (from manager state)
    servers := s.manager.GetAllServers()
    for _, srv := range servers {
        if srv.Error != "" {
            diag.UpstreamErrors = append(diag.UpstreamErrors, contracts.UpstreamError{
                ServerName: srv.Name,
                ErrorMsg:   srv.Error,
                Timestamp:  srv.LastError,
            })
        }
        if srv.RequiresAuth {
            diag.OAuthRequired = append(diag.OAuthRequired, contracts.OAuthRequirement{
                ServerName: srv.Name,
                State:      srv.AuthState,
                Message:    fmt.Sprintf("Run: mcpproxy auth login --server=%s", srv.Name),
            })
        }
    }

    // Check for missing secrets
    secrets := s.secretResolver.ListReferences()
    for _, ref := range secrets {
        if !s.secretResolver.Exists(ref) {
            diag.MissingSecrets = append(diag.MissingSecrets, contracts.MissingSecret{
                SecretName: ref,
                UsedBy:     s.findServersUsingSecret(ref),
            })
        }
    }

    // Docker status (if isolation enabled)
    if dockerEnabled {
        diag.DockerStatus = s.checkDockerDaemon(ctx)
    }

    diag.TotalIssues = len(diag.UpstreamErrors) + len(diag.OAuthRequired) + len(diag.MissingSecrets)
    return diag, nil
}
```

**Alternatives Considered**:
- **Polling-based**: Would violate event-driven architecture, rejected
- **Manager-owned**: Breaks separation of concerns (manager handles connections, not diagnostics)
- **Separate diagnostic service**: Over-engineering for current scope

### Q4: Backward Compatibility Strategy for CLI

**Decision**: Preserve all existing CLI flags and output formats, delegate internally to new service

**Rationale**:
1. **User Trust**: Breaking CLI breaks automation/scripts
2. **Feature Requirement**: Spec explicitly mandates "keep current cmd options, subcommands the same"
3. **Migration Path**: Allows incremental adoption of new REST endpoints
4. **Testing**: Existing E2E tests validate no breaking changes

**Implementation Approach**:
```go
// cmd/mcpproxy/upstream_cmd.go (existing structure preserved)
func runUpstreamRestart(cmd *cobra.Command, args []string) error {
    // Existing flag parsing unchanged
    serverName := upstreamServerName
    if len(args) > 0 {
        serverName = args[0]
    }

    // NEW: Delegate to REST client (which calls service)
    client := cliclient.NewClient(socketPath, logger)

    if upstreamAll {
        count, err := client.RestartAll(ctx)
        if err != nil {
            return err
        }
        fmt.Printf("Restarted %d servers\n", count)
    } else {
        if serverName == "" {
            return errors.New("server name required (use --all for all servers)")
        }
        err := client.RestartServer(ctx, serverName)
        if err != nil {
            return err
        }
        fmt.Printf("Server %s restarted\n", serverName)
    }
    return nil
}
```

**Key Preservation Points**:
- ✅ Flag names: `--all`, `--server`, `--tail`, `--follow`, `--output`, `--log-level`
- ✅ Subcommands: `list`, `logs`, `enable`, `disable`, `restart`
- ✅ Output formats: JSON/pretty (existing formatters reused)
- ✅ Error messages: Same wording for consistency
- ✅ Exit codes: Use existing patterns

### Q5: Event Emission for State Changes

**Decision**: Emit structured events via existing `internal/runtime/event_bus.go`

**Rationale**:
1. **Existing Infrastructure**: Event bus already implemented and working
2. **SSE Integration**: Events auto-forwarded to tray/web UI
3. **Decoupling**: Service doesn't know about HTTP/SSE, just emits events
4. **Consistency**: Matches existing `servers.changed` and `config.reloaded` patterns

**Event Schema**:
```go
// Existing pattern from internal/runtime/event_bus.go
type Event struct {
    Type string                 `json:"type"`
    Data map[string]interface{} `json:"data"`
}

// Service emits these events:
// - servers.changed: {server: "name", operation: "restart|enable|disable"}
// - server.error: {server: "name", error: "message"}
// - diagnostics.updated: {issue_count: 5}
```

**Integration**:
```go
// Service method
func (s *service) RestartServer(ctx context.Context, name string) error {
    // ... gate checks, manager call ...

    s.eventBus.Emit(runtime.Event{
        Type: "servers.changed",
        Data: map[string]interface{}{
            "server":    name,
            "operation": "restart",
            "timestamp": time.Now(),
        },
    })

    return nil
}

// HTTP API (existing SSE endpoint forwards events automatically)
// Tray/Web UI already subscribed to /events, no changes needed
```

### Q6: Bulk Operation Implementation Pattern

**Decision**: Sequential execution with partial failure reporting

**Rationale**:
1. **Safety**: Avoid thundering herd on upstream servers
2. **Transparency**: Report which servers succeeded/failed
3. **Simplicity**: No complex transaction management needed
4. **Event Stream**: Each server operation emits individual event

**Pattern**:
```go
func (s *service) RestartAll(ctx context.Context) (int, error) {
    // Gate checks
    if s.config.DisableManagement || s.config.ReadOnly {
        return 0, errors.New("operation not allowed")
    }

    servers := s.manager.GetAllServers()
    successCount := 0
    var errs []string

    for _, srv := range servers {
        if err := s.RestartServer(ctx, srv.Name); err != nil {
            errs = append(errs, fmt.Sprintf("%s: %v", srv.Name, err))
            s.logger.Warnf("Failed to restart %s: %v", srv.Name, err)
        } else {
            successCount++
        }
    }

    if len(errs) > 0 {
        // Return partial success with error details
        return successCount, fmt.Errorf("some servers failed: %s", strings.Join(errs, ", "))
    }

    return successCount, nil
}
```

**Alternatives Considered**:
- **Concurrent restarts**: Risk of resource exhaustion
- **All-or-nothing**: Too strict, one failure blocks all
- **Transaction rollback**: Over-engineering for server operations

### Q7: Logging Enhancements for Complete Visibility

**Decision**: Add HTTP request logging, OAuth token exchange logging, and Docker stderr streaming

**Rationale**:
1. **HTTP Request Metadata**: Operators need visibility into HTTP MCP server communication for debugging
2. **OAuth Token Exchange**: Critical for troubleshooting authentication failures (DCR, token refresh)
3. **Docker Stderr Streaming**: Unified log access without requiring Docker CLI commands

**Current Coverage** (already implemented):
- ✅ Stdio server stderr output captured and logged
- ✅ OAuth token operations (load/save/clear) logged
- ✅ Process lifecycle events logged
- ✅ Secret sanitization (tokens, API keys) via `internal/logs/sanitizer.go`

**Enhancements Needed**:

**1. HTTP Request Logging** (FR-008b):
```go
// Add to internal/upstream/core/client.go for HTTP transport
func (c *Client) logHTTPRequest(req *http.Request, resp *http.Response, duration time.Duration) {
    // Sanitize authorization headers
    sanitizedHeaders := make(http.Header)
    for k, v := range req.Header {
        if k == "Authorization" {
            sanitizedHeaders[k] = []string{sanitizeAuthHeader(v[0])}
        } else {
            sanitizedHeaders[k] = v
        }
    }

    c.upstreamLogger.Info("HTTP Request",
        zap.String("method", req.Method),
        zap.String("url", req.URL.String()),
        zap.Any("headers", sanitizedHeaders))

    if resp != nil {
        c.upstreamLogger.Info("HTTP Response",
            zap.Int("status", resp.StatusCode),
            zap.Duration("duration", duration))
    }
}
```

**2. OAuth Token Exchange Logging** (FR-008c):
```go
// Add to internal/oauth/client.go (or wherever token exchange happens)
func (o *OAuthClient) ExchangeToken(ctx context.Context, code string) (*Token, error) {
    o.logger.Info("🔄 Token exchange starting",
        zap.String("grant_type", "authorization_code"))

    token, err := o.config.Exchange(ctx, code)
    if err != nil {
        o.logger.Error("❌ Token exchange failed", zap.Error(err))
        return nil, err
    }

    // Redact tokens for logging
    redactedAccess := redactToken(token.AccessToken)
    redactedRefresh := redactToken(token.RefreshToken)

    o.logger.Info("✅ Token exchange succeeded",
        zap.String("access_token", redactedAccess),
        zap.String("refresh_token", redactedRefresh),
        zap.Time("expires_at", token.Expiry))

    return token, nil
}

func redactToken(token string) string {
    if len(token) <= 10 {
        return "***"
    }
    return token[:5] + "***" + token[len(token)-3:]
}
```

**3. Docker Stderr Streaming** (FR-008d):
```go
// Modify internal/upstream/core/monitoring.go monitorDockerLogsWithContext
func (c *Client) monitorDockerLogsWithContext(ctx context.Context, cidFile string) {
    // ... existing container ID reading ...

    // Stream stderr only (user choice: don't flood logs with stdout)
    logOptions := docker.LogsOptions{
        Stderr:      true,
        Stdout:      false, // Changed from true
        Follow:      true,
        Timestamps:  true,
    }

    logReader, err := c.dockerClient.Logs(containerID, logOptions)
    if err != nil {
        c.logger.Warn("Failed to stream Docker logs", zap.Error(err))
        return
    }
    defer logReader.Close()

    scanner := bufio.NewScanner(logReader)
    for scanner.Scan() {
        select {
        case <-ctx.Done():
            return
        default:
            line := scanner.Text()
            // Log to per-server log file
            if c.upstreamLogger != nil {
                c.upstreamLogger.Info("Container stderr", zap.String("message", line))
            }
        }
    }
}
```

**Implementation Locations**:
- HTTP logging: `internal/upstream/core/client.go` (HTTP transport)
- OAuth logging: `internal/oauth/` (token exchange, DCR)
- Docker streaming: `internal/upstream/core/monitoring.go` (update existing function)

**Testing**:
- Unit tests for sanitization functions (ensure tokens are redacted correctly)
- E2E tests verifying log files contain expected entries
- Validate secret sanitizer catches all token formats

## Summary of Decisions

| Topic | Decision | Key Benefit |
|-------|----------|-------------|
| OpenAPI Generation | swaggo/swag | Chi compatible, annotation-based, OpenAPI 3.x |
| Service Interface | Go interface + struct | Testability, dependency injection |
| Diagnostics | Centralized Doctor() | Single source of truth |
| CLI Compatibility | Preserve all flags/commands | No breaking changes |
| Event System | Use existing event bus | SSE integration, decoupling |
| Bulk Operations | Sequential with partial failure | Safety, transparency |
| Logging Enhancements | HTTP metadata + OAuth details + Docker stderr | Complete operational visibility |

## Next Steps

Phase 1 artifacts ready for generation:
1. **data-model.md**: Define Diagnostics, AuthStatus, ServerStats types
2. **contracts/**: Create example service interface, swag annotations
3. **quickstart.md**: Document service usage for developers
4. **Update agent context**: Add swaggo/swag to technology list
