---
id: architecture
title: Architecture
sidebar_label: Architecture
sidebar_position: 1
description: MCPProxy internal architecture and component overview
keywords: [architecture, internals, development]
---

# Architecture

This document describes the internal architecture of MCPProxy for developers.

## Core Components

| Directory | Purpose |
|-----------|---------|
| `cmd/mcpproxy/` | CLI entry point, Cobra commands |
| `cmd/mcpproxy-tray/` | System tray application with state machine |
| `internal/runtime/` | Lifecycle, event bus, background services |
| `internal/server/` | HTTP server, MCP proxy |
| `internal/httpapi/` | REST API endpoints (`/api/v1`) |
| `internal/upstream/` | 3-layer client: core/managed/cli |
| `internal/config/` | Configuration management |
| `internal/index/` | Bleve BM25 search index |
| `internal/storage/` | BBolt database |
| `internal/management/` | Centralized server management |
| `internal/oauth/` | OAuth 2.1 with PKCE |
| `internal/logs/` | Structured logging with per-server files |

## Upstream Client Architecture

The upstream package uses a 3-layer design:

```
┌─────────────────────────────────────────────────────────┐
│                      CLI Layer                          │
│  Enhanced logging, single operations, debug output      │
├─────────────────────────────────────────────────────────┤
│                    Managed Layer                        │
│  State management, retry logic, connection pooling      │
├─────────────────────────────────────────────────────────┤
│                      Core Layer                         │
│  Basic MCP client, stateless, transport-agnostic        │
└─────────────────────────────────────────────────────────┘
```

## Event Bus System

The runtime event bus enables real-time updates:

```go
// Event types
EventServersChanged    // Server status change
EventConfigReloaded    // Config file changed
EventToolsIndexed      // Search index updated
```

Events are consumed by:
- SSE endpoints for Web UI
- Tray application via socket
- Internal state synchronization

## Management Service

The `internal/management/` package provides centralized business logic:

- Used by CLI, REST API, and MCP interfaces
- Eliminates code duplication
- Handles configuration gates and bulk operations

## Tray-Core Communication

Platform-specific IPC:
- **macOS/Linux**: Unix socket at `~/.mcpproxy/mcpproxy.sock`
- **Windows**: Named pipe at `\\.\pipe\mcpproxy-<username>`

Socket connections bypass API key authentication (OS-level security).

## Connection State Machine

```
Disconnected → Connecting → Authenticating → Ready
     ↑              │              │            │
     └──────────────┴──────────────┴────────────┘
                    (on error)
```

## Subprocess Shutdown

When MCPProxy stops, subprocesses are terminated using a two-phase approach:

1. **Graceful Close** (10s): Close MCP connection, wait for process to exit
2. **Force Kill** (9s): If still running, SIGTERM → poll → SIGKILL

| Timeout | Value | Purpose |
|---------|-------|---------|
| MCP Client Close | 10s | Wait for graceful stdin/stdout close |
| SIGTERM → SIGKILL | 9s | Time between graceful and force kill |
| Docker Cleanup | 30s | Container stop/kill timeout |

See [Shutdown Behavior](/operations/shutdown-behavior) for detailed documentation.

For complete architecture details, see [docs/architecture.md](https://github.com/smart-mcp-proxy/mcpproxy-go/blob/main/docs/architecture.md) in the repository.
