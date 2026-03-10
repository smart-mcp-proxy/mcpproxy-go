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

The default mode uses BM25 full-text search to help AI agents discover relevant tools without exposing the entire tool catalog. This approach — sometimes called "lazy tool loading" or "tool search" — is used by Anthropic's own MCP implementation and is the recommended pattern for large tool sets.

**Endpoint:** `/mcp/call`

**Tools exposed:**
- `retrieve_tools` — Search for tools by natural language query
- `call_tool_read` — Execute read-only tool calls
- `call_tool_write` — Execute write tool calls
- `call_tool_destructive` — Execute destructive tool calls
- `read_cache` — Access paginated responses

**How it works:**
1. AI agent calls `retrieve_tools` with a natural language query
2. MCPProxy returns matching tools ranked by BM25 relevance, with `call_with` recommendations
3. AI agent calls the appropriate `call_tool_*` variant with the exact tool name from results

**Pros:**
- Massive token savings: only matched tools are sent to the model, not the full catalog
- Scales to hundreds of tools across many servers without context window bloat
- Intent-based permission control (read/write/destructive variants) enables granular IDE approval flows
- Activity logging captures operation type and intent metadata for auditing

**Cons:**
- Two-step workflow (search then call) adds one round-trip compared to direct mode
- BM25 search quality depends on tool descriptions — poorly described tools may not surface
- The AI agent must learn the retrieve-then-call pattern (most modern models handle this well)

**When to use:**
- You have many upstream servers with dozens or hundreds of tools
- Token usage is a concern (common with paid API usage)
- You want intent-based permission control in IDE auto-approve settings
- Production deployments where audit trails matter

### direct

Direct mode exposes every upstream tool directly to the AI agent. Each tool appears in the standard MCP `tools/list` with a `serverName__toolName` name. This is the simplest mode and the closest to how individual MCP servers work natively.

**Endpoint:** `/mcp/all`

**Tools exposed:**
- Every tool from every connected, enabled, non-quarantined upstream server
- Named as `serverName__toolName` (e.g., `github__create_issue`, `filesystem__read_file`)

**How it works:**
1. AI agent sees all available tools in `tools/list`
2. AI agent calls tools directly by their `serverName__toolName` name
3. MCPProxy routes the call to the correct upstream server

**Tool naming:**
- Separator is `__` (double underscore) to avoid conflicts with single underscores in tool names
- The split happens on the FIRST `__` only, so tool names containing `__` are preserved
- Descriptions are prefixed with `[serverName]` for clarity

**Auth enforcement:**
- Agent tokens with server restrictions are enforced (access denied if token lacks server access)
- Permission levels are derived from tool annotations (read-only, destructive, etc.)

**Pros:**
- Zero learning curve: tools work exactly like native MCP tools
- Single round-trip: no search step needed, call any tool directly
- Maximum compatibility: works with any MCP client without special handling
- Tool annotations (readOnlyHint, destructiveHint) are preserved from upstream

**Cons:**
- High token cost: all tool definitions are sent in every request context
- Does not scale well beyond ~50 tools (context window fills up, model accuracy degrades)
- No intent-based permission tiers (the model just calls tools)
- All tools visible upfront increases attack surface for prompt injection

**When to use:**
- Small setups with fewer than 50 total tools
- Quick prototyping and testing
- AI clients that don't support the retrieve-then-call pattern
- CI/CD agents that know exactly which tools they need

### code_execution

Code execution mode is designed for multi-step orchestration workflows. Instead of making separate tool calls for each step, the AI agent writes JavaScript or TypeScript code that chains multiple tool calls together in a single request. This is inspired by patterns from OpenAI's code interpreter and similar "tool-as-code" approaches.

**Endpoint:** `/mcp/code`

**Tools exposed:**
- `code_execution` — Execute JavaScript/TypeScript that orchestrates upstream tools (includes a catalog of all available tools in the description)
- `retrieve_tools` — Search for tools (instructs to use `code_execution`, not `call_tool_*`)

**How it works:**
1. AI agent sees the `code_execution` tool with a listing of all available upstream tools
2. AI agent writes JavaScript/TypeScript code that calls `call_tool(serverName, toolName, args)`
3. MCPProxy executes the code in a sandboxed ES2020+ VM, routing tool calls to upstream servers
4. Results are returned as a single response

**Pros:**
- Minimal round-trips: complex multi-step workflows execute in one request
- Full programming power: conditionals, loops, error handling, data transformation
- TypeScript support with type safety (auto-transpiled via esbuild)
- Sandboxed execution: no filesystem or network access, timeout enforcement
- Tool catalog in description means no separate search step needed

**Cons:**
- Requires the AI model to write correct JavaScript/TypeScript code
- Debugging is harder: errors come from inside the sandbox, not from MCP tool calls
- Higher latency per request (VM startup + multiple sequential tool calls)
- Must be explicitly enabled (`"enable_code_execution": true`)
- Not all AI models are equally good at writing code for tool orchestration

**When to use:**
- Workflows that chain 2+ tool calls with data dependencies between them
- Batch operations (e.g., "for each repo, check CI status and create issue if failing")
- Complex conditional logic that would require many round-trips in other modes
- Data transformation pipelines (fetch from one tool, transform, send to another)

**Note:** Code execution must be enabled in config (`"enable_code_execution": true`). If disabled, the `code_execution` tool appears but returns an error message directing the user to enable it.

## Choosing the Right Mode

| Factor | retrieve_tools | direct | code_execution |
|--------|---------------|--------|----------------|
| **Token cost** | Low (only matched tools) | High (all tools) | Medium (catalog in description) |
| **Round-trips per task** | 2 (search + call) | 1 (direct call) | 1 (code handles all) |
| **Max practical tools** | 500+ | ~50 | 500+ |
| **Setup complexity** | None (default) | None | Requires enablement |
| **Model requirements** | Any modern LLM | Any LLM | Code-capable LLM |
| **Audit granularity** | High (intent metadata) | Medium (annotations) | Medium (code logged) |
| **IDE auto-approve** | Per-variant rules | Per-tool rules | Single rule |

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
