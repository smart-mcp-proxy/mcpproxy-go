---
id: routing-modes
title: Routing Modes
sidebar_label: Routing Modes
sidebar_position: 2
description: Configure how MCPProxy exposes upstream tools to AI agents
keywords: [routing, modes, direct, code_execution, retrieve_tools, mcp]
---

# Routing Modes

MCPProxy supports three routing modes that control how upstream MCP tools are exposed to AI agents. Each mode offers different tradeoffs between token efficiency, flexibility, and simplicity.

## Overview

| Mode | Default MCP Endpoint | Tool Exposure | Best For |
|------|---------------------|---------------|----------|
| `retrieve_tools` | `/mcp` | BM25 search via `retrieve_tools` + `call_tool_read/write/destructive` | Large tool sets (50+ tools), token-sensitive workloads |
| `direct` | `/mcp` | All tools exposed as `serverName__toolName` | Small tool sets, simple setups, maximum compatibility |
| `code_execution` | `/mcp` | `code_execution` + `retrieve_tools` for discovery | Multi-step orchestration, reduced round-trips |

## Configuration

Set the routing mode in your config file (`~/.mcpproxy/mcp_config.json`):

```json
{
  "routing_mode": "retrieve_tools"
}
```

| Field | Type | Default | Values |
|-------|------|---------|--------|
| `routing_mode` | string | `"retrieve_tools"` | `retrieve_tools`, `direct`, `code_execution` |

The configured routing mode determines which tools are served on the default `/mcp` endpoint. All three modes are always available on dedicated endpoints regardless of the config setting.

## Dedicated Endpoints

Each routing mode has a dedicated MCP endpoint that is always available:

| Endpoint | Mode | Description |
|----------|------|-------------|
| `/mcp/call` | `retrieve_tools` | BM25 search via `retrieve_tools` + `call_tool` variants |
| `/mcp/all` | `direct` | All upstream tools with `serverName__toolName` naming |
| `/mcp/code` | `code_execution` | JavaScript orchestration via `code_execution` tool |
| `/mcp` | *(configured)* | Serves whichever mode is set in `routing_mode` |

This means you can point different AI clients at different endpoints. For example, Claude Desktop at `/mcp` (retrieve_tools mode for token savings) and a CI/CD agent at `/mcp/all` (direct mode for simplicity).

## Mode Details

### retrieve_tools (Default)

The default mode uses BM25 full-text search to help AI agents discover relevant tools without exposing the entire tool catalog. This is the most token-efficient mode.

**Tools exposed:**
- `retrieve_tools` -- Search for tools by natural language query
- `call_tool_read` -- Execute read-only tool calls
- `call_tool_write` -- Execute write tool calls
- `call_tool_destructive` -- Execute destructive tool calls
- `upstream_servers` -- Manage upstream servers (if management enabled)
- `code_execution` -- JavaScript orchestration (if enabled)

**How it works:**
1. AI agent calls `retrieve_tools` with a natural language query
2. MCPProxy returns matching tools ranked by BM25 relevance
3. AI agent calls the appropriate `call_tool_*` variant with the tool name

**When to use:**
- You have many upstream servers with dozens or hundreds of tools
- Token usage is a concern (only tool metadata for matched tools is sent)
- You want intent-based permission control (read/write/destructive variants)

### direct

Direct mode exposes every upstream tool directly to the AI agent. Each tool is named `serverName__toolName` (double underscore separator).

**Tools exposed:**
- Every tool from every connected, enabled, non-quarantined upstream server
- Named as `serverName__toolName` (e.g., `github__create_issue`, `filesystem__read_file`)

**How it works:**
1. AI agent sees all available tools in the tools list
2. AI agent calls tools directly by their `serverName__toolName` name
3. MCPProxy routes the call to the correct upstream server

**Tool naming:**
- Separator is `__` (double underscore) to avoid conflicts with single underscores in tool names
- The split happens on the FIRST `__` only, so tool names containing `__` are preserved
- Descriptions are prefixed with `[serverName]` for clarity

**Auth enforcement:**
- Agent tokens with server restrictions are enforced (access denied if token lacks server access)
- Permission levels are derived from tool annotations (read-only, destructive, etc.)

**When to use:**
- You have a small number of upstream servers (fewer than 50 total tools)
- You want maximum simplicity and compatibility
- AI clients that work better with a flat tool list

### code_execution

Code execution mode is designed for multi-step orchestration workflows. It exposes the `code_execution` tool with an enhanced description that includes a catalog of all available upstream tools.

**Tools exposed:**
- `code_execution` -- Execute JavaScript/TypeScript that orchestrates upstream tools
- `retrieve_tools` -- Search for tools (useful for discovery before writing code)

**How it works:**
1. AI agent sees the `code_execution` tool with a listing of all available upstream tools
2. AI agent writes JavaScript/TypeScript code that calls `call_tool(serverName, toolName, args)`
3. MCPProxy executes the code in a sandboxed VM, routing tool calls to upstream servers

**When to use:**
- Workflows that require chaining 2+ tool calls together
- You want to minimize model round-trips
- Complex conditional logic or data transformation between tool calls

**Note:** Code execution must be enabled in config (`"enable_code_execution": true`). If disabled, the `code_execution` tool appears but returns an error message directing the user to enable it.

## Viewing Current Routing Mode

### CLI

```bash
# Status command shows routing mode
mcpproxy status

# Doctor command shows routing mode in Security Features section
mcpproxy doctor
```

### REST API

```bash
# Get routing mode info
curl -H "X-API-Key: your-key" http://127.0.0.1:8080/api/v1/routing

# Response:
{
  "routing_mode": "retrieve_tools",
  "description": "BM25 search via retrieve_tools + call_tool variants (default)",
  "endpoints": {
    "default": "/mcp",
    "direct": "/mcp/all",
    "code_execution": "/mcp/code",
    "retrieve_tools": "/mcp/call"
  },
  "available_modes": ["retrieve_tools", "direct", "code_execution"]
}
```

```bash
# Status endpoint also includes routing_mode
curl -H "X-API-Key: your-key" http://127.0.0.1:8080/api/v1/status
```

## Changing Routing Mode

1. Edit `~/.mcpproxy/mcp_config.json`:
   ```json
   {
     "routing_mode": "direct"
   }
   ```

2. Restart MCPProxy:
   ```bash
   pkill mcpproxy
   mcpproxy serve
   ```

The routing mode is applied at startup and determines which MCP server instance handles the default `/mcp` endpoint. Dedicated endpoints (`/mcp/all`, `/mcp/code`, `/mcp/call`) are always available regardless of the configured mode.

## Tool Refresh

When upstream servers connect, disconnect, or update their tools:

- **Direct mode**: Tool list is automatically rebuilt. The AI client receives a `notifications/tools/list_changed` notification.
- **Code execution mode**: The tool catalog in the `code_execution` description is refreshed.
- **Retrieve tools mode**: The BM25 search index is updated.

## Security Considerations

- **Direct mode** exposes all tools upfront, which increases the initial token cost but simplifies the interaction. Use agent tokens with server restrictions to limit exposure.
- **Retrieve tools mode** is the most secure by default because AI agents only see tools matching their search query.
- **Code execution mode** requires explicit enablement (`enable_code_execution: true`) and runs code in a sandboxed JavaScript VM with no filesystem or network access.
- All modes respect server quarantine, tool-level quarantine, and agent token permissions.
