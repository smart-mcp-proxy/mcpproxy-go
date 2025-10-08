# MCPProxy Architecture Documentation

This document describes the modular architecture of mcpproxy-go and the boundaries between different components.

## Core Architecture Principles

MCPProxy follows a **modular, interface-driven architecture** with clear separation of concerns:

1. **Core Runtime**: Central orchestration and lifecycle management
2. **Interface Contracts**: Type-safe communication via `internal/contracts`
3. **Feature Modularity**: Optional features controlled via feature flags
4. **Dependency Injection**: Components receive dependencies through interfaces

## Module Boundaries

### 1. Runtime Module (`internal/runtime/`)

**Purpose**: Central orchestration and lifecycle management

**Responsibilities**:
- Server lifecycle management (start, stop, restart)
- Background connection management with retries
- Tool discovery and indexing coordination
- Event bus for cross-component communication
- Configuration synchronization

**Interfaces**:
```go
type RuntimeManager interface {
    Start(ctx context.Context) error
    Stop() error
    StatusChannel() <-chan interface{}
    EventsChannel() <-chan Event
}
```

**Dependencies**: Storage, Index, AppContext adapters

### 2. HTTP API Module (`internal/httpapi/`)

**Purpose**: REST API and Server-Sent Events endpoints

**Responsibilities**:
- RESTful API endpoints (`/api/v1/*`)
- Server-Sent Events (`/events`)
- Request/response handling with typed contracts
- HTTP middleware integration

**Interfaces**:
```go
type ServerController interface {
    IsRunning() bool
    GetAllServers() ([]map[string]interface{}, error)
    EnableServer(serverName string, enabled bool) error
    // ... other server operations
}
```

**Dependencies**: ServerController (runtime), Observability (optional)

### 3. Observability Module (`internal/observability/`)

**Purpose**: Health checks, metrics, and distributed tracing

**Responsibilities**:
- Health endpoints (`/healthz`, `/readyz`)
- Prometheus metrics collection (`/metrics`)
- OpenTelemetry distributed tracing
- Component health checking

**Interfaces**:
```go
type HealthManager interface {
    HealthzHandler() http.HandlerFunc
    ReadyzHandler() http.HandlerFunc
    IsHealthy() bool
    IsReady() bool
}

type MetricsManager interface {
    Handler() http.Handler
    HTTPMiddleware() func(http.Handler) http.Handler
    RecordToolCall(server, tool, status string, duration time.Duration)
}
```

**Dependencies**: Optional - can be nil for reduced footprint

### 4. Storage Module (`internal/storage/`)

**Purpose**: Persistent data storage with async operations

**Responsibilities**:
- BoltDB database operations
- Tool statistics and metadata storage
- Server configuration persistence
- Async operation queuing to prevent deadlocks

**Interfaces**:
```go
type StorageManager interface {
    StoreToolCall(serverName, toolName string) error
    GetToolStats() (map[string]interface{}, error)
    Close() error
}
```

**Key Pattern**: Single-writer goroutine with operation queues

### 5. Index Module (`internal/index/`)

**Purpose**: Full-text search using Bleve

**Responsibilities**:
- BM25 search index management
- Tool indexing and updates
- Search query processing

**Interfaces**:
```go
type IndexManager interface {
    Index(tools []ToolMetadata) error
    Search(query string, limit int) ([]SearchResult, error)
    Close() error
}
```

### 6. Cache Module (`internal/cache/`)

**Purpose**: Response caching layer

**Responsibilities**:
- Tool response caching
- TTL-based cache expiration
- Cache statistics

### 7. Upstream Module (`internal/upstream/`)

**Purpose**: MCP client implementations

**Architecture**: 3-layer design
- `core/`: Basic MCP client (stateless, transport-agnostic)
- `managed/`: Production client (state management, retry logic)
- `cli/`: Debug client (enhanced logging, single operations)

### 8. Contracts Module (`internal/contracts/`)

**Purpose**: Type-safe data structures and conversion utilities

**Responsibilities**:
- Typed DTOs replacing `map[string]interface{}`
- Type conversion utilities
- TypeScript type generation

### 9. Web UI Module (`web/`)

**Purpose**: Embedded Vue.js frontend

**Responsibilities**:
- Frontend asset serving via `go:embed`
- Static file handling
- UI route management

### 10. Tray Module (`cmd/mcpproxy-tray/`)

**Purpose**: Cross-platform system tray application

**Responsibilities**:
- Native system tray integration
- Menu management and user interactions
- Communication with main mcpproxy via HTTP API

**Separation**: Build-tagged for platform-specific implementations

## Feature Flag System

Features can be selectively enabled/disabled via configuration:

```json
{
  "features": {
    "enable_observability": true,
    "enable_health_checks": true,
    "enable_metrics": true,
    "enable_tracing": false,
    "enable_docker_isolation": false,
    "enable_web_ui": true,
    "enable_tray": true
  }
}
```

### Feature Dependencies

```
Runtime (always enabled)
├── EventBus (required for SSE)
│   └── SSE (required for real-time updates)
├── Observability (optional)
│   ├── HealthChecks (requires observability)
│   ├── Metrics (requires observability)
│   └── Tracing (requires observability)
└── Storage (required for persistence)
    ├── Search (optional)
    └── Caching (optional)
```

## Communication Patterns

### 1. Event-Driven Architecture

Components communicate via the runtime event bus:

```go
type Event struct {
    Type      EventType
    Payload   interface{}
    Timestamp time.Time
}

// Event types
const (
    ServerStateChanged EventType = "server.state.changed"
    ToolIndexUpdated   EventType = "tool.index.updated"
    ConfigReloaded     EventType = "config.reloaded"
)
```

### 2. Interface-Based Dependency Injection

Components receive dependencies through well-defined interfaces:

```go
// Example: HTTP server receives dependencies
func NewServer(
    controller ServerController,
    logger *zap.SugaredLogger,
    observability *observability.Manager, // Optional
) *Server
```

### 3. Graceful Degradation

Components handle missing optional dependencies gracefully:

```go
if s.observability != nil {
    if health := s.observability.Health(); health != nil {
        s.router.Get("/healthz", health.HealthzHandler())
    }
}
```

## Testing Strategy

### 1. Interface Mocking

Each interface has mock implementations for testing:

```go
type MockServerController struct{}
func (m *MockServerController) IsRunning() bool { return true }
// ... other mock methods
```

### 2. Contract Testing

Golden file tests ensure API stability:

```go
func TestAPIContractCompliance(t *testing.T) {
    // Tests API responses against golden files
}
```

### 3. Feature Flag Testing

Tests verify feature flag dependencies and validation:

```go
func TestFeatureFlagValidation(t *testing.T) {
    // Tests feature flag dependency rules
}
```

## Security Boundaries

### 1. Docker Isolation

MCP servers can run in isolated Docker containers with:
- Resource limits (CPU, memory)
- Network isolation
- Read-only filesystems
- Dropped capabilities

### 2. OAuth Token Security

Secure token storage with multiple backends:
- OS keyring (primary)
- Age-encrypted files (fallback)
- Proper token refresh with exponential backoff

### 3. Quarantine System

New servers are automatically quarantined to prevent:
- Tool Poisoning Attacks (TPA)
- Malicious tool descriptions
- Data exfiltration attempts

## Deployment Patterns

### 1. Monolithic Deployment

Single binary with all features enabled (default):
```bash
./mcpproxy serve --config=config.json
```

### 2. Minimal Deployment

Reduced footprint with selective features:
```json
{
  "features": {
    "enable_observability": false,
    "enable_tracing": false,
    "enable_docker_isolation": false,
    "enable_web_ui": false
  }
}
```

### 3. Observability-First Deployment

Full monitoring and tracing enabled:
```json
{
  "features": {
    "enable_observability": true,
    "enable_health_checks": true,
    "enable_metrics": true,
    "enable_tracing": true
  }
}
```

## Future Extensibility

The architecture supports future enhancements:

1. **Plugin System**: New modules can be added via interface implementations
2. **Transport Abstraction**: Support for gRPC, WebSocket, etc.
3. **Storage Backends**: Additional storage implementations (PostgreSQL, Redis, etc.)
4. **Authentication Providers**: OIDC, SAML, etc.
5. **Monitoring Integrations**: Datadog, New Relic, etc.

## Performance Considerations

### 1. Async Operations

BoltDB operations use async queues to prevent deadlocks:
- Single writer goroutine
- Operation batching
- Context-based cancellation

### 2. Connection Pooling

HTTP clients use connection pooling and keepalives:
- Configurable timeouts
- Circuit breakers for upstream services
- Exponential backoff with jitter

### 3. Memory Management

- Bounded caches with LRU eviction
- Streaming for large responses
- Connection limits for upstream servers

This architecture provides a solid foundation for scaling mcpproxy while maintaining modularity and testability.