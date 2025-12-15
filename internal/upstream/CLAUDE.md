# Upstream Client Architecture

## 3-Layer Design

| Layer | Directory | Purpose |
|-------|-----------|---------|
| **Core** | `core/` | Stateless, transport-agnostic MCP client |
| **Managed** | `managed/` | Production client with state, retries, pooling |
| **CLI** | `cli/` | Debug client with enhanced logging |

## Core Layer (`core/`)

Basic MCP protocol client:
- Stateless operations
- Transport-agnostic (stdio, HTTP, SSE)
- Protocol message handling

## Managed Layer (`managed/`)

Production client features:
- Connection state machine
- Retry logic with exponential backoff
- Connection pooling
- Health monitoring

**Connection States**:
```
Disconnected → Connecting → Authenticating → Ready
```

## CLI Layer (`cli/`)

Debug client for manual testing:
- Enhanced logging
- Single-operation mode
- Verbose output

```bash
./mcpproxy tools list --server=name --log-level=trace
```

## Docker Isolation

Configured per-server in managed layer:
- `isolation.enabled` - Enable container isolation
- `isolation.image` - Custom Docker image
- `isolation.memory_limit` - Memory cap
- `isolation.cpu_limit` - CPU cap

## Key Patterns

- Background connection attempts
- Separate contexts (app vs server lifecycle)
- Hash-based tool change detection
- Automatic reconnection on failure
