# MCP Server

## Purpose

HTTP server and MCP protocol implementation.

## Key Files

| File | Purpose |
|------|---------|
| `server.go` | HTTP server management, delegates to runtime |
| `mcp.go` | MCP protocol implementation, tool routing |

## Built-in MCP Tools

| Tool | Purpose |
|------|---------|
| `retrieve_tools` | BM25 search across upstream tools |
| `call_tool` | Proxy tool calls to upstream servers |
| `code_execution` | JavaScript orchestration (disabled by default) |
| `upstream_servers` | CRUD for server management |

## Tool Name Format

Format: `<serverName>:<originalToolName>`

Example: `github:create_issue`

Tools are automatically prefixed to prevent conflicts.

## MCP Endpoints

- `POST /mcp` - Main MCP protocol endpoint
- `GET /mcp/` - MCP discovery (SSE-compatible)

**Note**: MCP endpoints remain unprotected for client compatibility.

## Server-Runtime Delegation

Server delegates most operations to runtime:
- Configuration management → `runtime.Config()`
- Server connections → `runtime.GetManager()`
- Tool search → `runtime.SearchTools()`
- Events → `runtime.EventBus()`

## E2E Tests

```bash
go test ./internal/server -run TestBinary -v   # Binary E2E
go test ./internal/server -run TestMCP -v      # MCP protocol
```

Test file: `e2e_test.go`
