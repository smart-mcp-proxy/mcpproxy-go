---
id: mcp-protocol
title: MCP Protocol
sidebar_label: MCP Protocol
sidebar_position: 2
description: MCPProxy's MCP protocol implementation and built-in tools
keywords: [mcp, protocol, tools, proxy]
---

# MCP Protocol

MCPProxy implements the Model Context Protocol (MCP) specification, providing AI clients with unified access to multiple upstream MCP servers.

## Endpoints

### Default Endpoint

```
http://127.0.0.1:8080/mcp
```

Serves the routing mode configured in `routing_mode` (default: `retrieve_tools`).

### Routing Mode Endpoints

Each routing mode has a dedicated endpoint that is always available regardless of the configured default:

| Endpoint | Mode | Description |
|----------|------|-------------|
| `/mcp` | *(configured)* | Default routing mode from config |
| `/mcp/call` | `retrieve_tools` | BM25 search via `retrieve_tools` + `call_tool` variants |
| `/mcp/all` | `direct` | All upstream tools as `serverName__toolName` |
| `/mcp/code` | `code_execution` | JavaScript orchestration via `code_execution` tool |

See [Routing Modes](../features/routing-modes.md) for details on each mode.

**Note:** MCP endpoints do not require API key authentication for client compatibility.

## Built-in Tools

MCPProxy provides several built-in tools for managing and interacting with upstream servers.

### retrieve_tools

Search for tools across all connected servers using BM25 keyword search.

**Input Schema:**
```json
{
  "query": "string (required) - Search keywords",
  "limit": "number (optional) - Maximum results, default 15"
}
```

**Example:**
```json
{
  "query": "create github issue",
  "limit": 5
}
```

### call_tool_read / call_tool_write / call_tool_destructive

Execute an upstream tool through the variant that matches what it does. All three share one input schema; the variant itself declares the operation type.

- `call_tool_read` - **read-only** tools that query data without modifying state.
- `call_tool_write` - **state-modifying** tools that create or update resources.
- `call_tool_destructive` - **destructive** tools that delete or permanently modify resources.

**Input Schema (flat):**
```json
{
  "name": "string (required) - Tool name in server:tool format",
  "args": "object (optional, preferred) - Tool arguments as a native JSON object",
  "args_json": "string (optional, legacy) - Tool arguments as a JSON string; wins over args if both are sent",
  "intent_data_sensitivity": "string (optional) - public|internal|private|unknown",
  "intent_reason": "string (optional, max 1000 chars) - Explanation for the audit trail"
}
```

The operation type is **inferred from the variant you call** (`read`, `write` or `destructive`) and recorded in the activity log. It cannot be set by the client. The older nested `intent: {operation_type, data_sensitivity, reason}` object is no longer part of the schema; it is still accepted as a compatibility fallback for `reason` and `data_sensitivity` when the flat fields are absent (the flat fields win, and the object is not forwarded to the upstream tool).

**Example (read):**
```json
{
  "name": "github:list_repos",
  "args": {"org": "myorg"}
}
```

**Example (write, with intent):**
```json
{
  "name": "github:create_issue",
  "args": {"repo": "owner/repo", "title": "Bug report", "body": "Description"},
  "intent_reason": "Creating bug report per user request"
}
```

**Example (destructive, with intent, legacy `args_json`):**
```json
{
  "name": "github:delete_repo",
  "args_json": "{\"repo\": \"test-repo\"}",
  "intent_data_sensitivity": "private",
  "intent_reason": "User confirmed deletion"
}
```

**Validation:** `call_tool_read` and `call_tool_write` are rejected if the server marks the tool as `destructiveHint: true`; `call_tool_destructive` is the most permissive and is allowed regardless of server annotations.

**Policy blocks are errors:** a call to a tool on a quarantined server, or to a tool that is pending approval or changed since approval, returns a result with `isError: true`. The text body is still JSON (`status` is `QUARANTINED_SERVER_BLOCKED` or `TOOL_QUARANTINED`, with `reason`, `message` and `action`), and the upstream tool is never invoked. Over REST (`POST /api/v1/tools/call`) and `mcpproxy tools call` the same block is answered with HTTP `403`.

:::tip Choosing the Right Tool Variant
Use `retrieve_tools` to discover tools - each result includes a `call_with` field recommending the appropriate variant based on server annotations.
:::

See [Intent Declaration](/features/intent-declaration) for complete documentation on the two-key security model.

### upstream_servers

Manage upstream server configurations with smart patching support.

**Operations:**
- `list` - List all servers with health status
- `add` - Add a new server (quarantined by default)
- `remove` - Remove a server
- `update` - Update server (smart merge)
- `patch` - Patch server (smart merge)
- `tail_log` - View server logs

**Smart Patching (update/patch):**

Uses deep merge semantics - only specify fields you want to change:

| Field Type | Behavior | Example |
|------------|----------|---------|
| Scalars | Replace | `enabled: true` |
| Maps (env, headers) | Merge | New keys added, existing updated |
| Arrays (args) | Replace entirely | All args replaced |
| Objects (isolation, oauth) | Deep merge | Only specified fields change |
| Explicit `null` | Remove field | `isolation_json: "null"` removes isolation |

**Example (patch - toggle enabled):**
```json
{
  "operation": "patch",
  "name": "my-server",
  "enabled": true
}
```
Only `enabled` changes - all other fields (env, isolation, oauth) are preserved.

**Example (add with isolation):**
```json
{
  "operation": "add",
  "name": "isolated-server",
  "command": "npx",
  "args_json": "[\"-y\", \"some-mcp-server\"]",
  "isolation_json": "{\"enabled\": true, \"image\": \"node:20\"}"
}
```

**Example (patch isolation image only):**
```json
{
  "operation": "patch",
  "name": "isolated-server",
  "isolation_json": "{\"image\": \"node:22\"}"
}
```
Only `isolation.image` changes - `isolation.enabled` and other fields preserved.

:::caution Concurrent Edits
MCPProxy does not support three-way merge with conflict detection. Avoid making concurrent edits to the same server configuration from multiple sources (MCP tool, Web UI, CLI) simultaneously. If concurrent edits occur, the last write wins and earlier changes may be lost.

**Best practices:**
- Make configuration changes from a single interface at a time
- Use the `list` operation to verify current state before patching
- Consider using the Web UI for interactive editing sessions
:::

### code_execution

Execute JavaScript/TypeScript to orchestrate multiple tools. Enabled by default since v0.66.0 (`enable_code_execution: true`); set it to `false` in the config file to remove the tool from `tools/list`. A named profile can only narrow this: with a `max_tier` of `read` or `write`, a profile that does not set `code_execution` has it off (see [Profiles](/features/profiles) and [Configuration](/configuration/config-file)).

See [Code Execution](/features/code-execution) for complete documentation.

## Tool Name Format

Tools from upstream servers are prefixed with the server name:

```
<serverName>:<originalToolName>
```

Examples:
- `github:create_issue`
- `filesystem:read_file`
- `sqlite:query`

This prevents naming conflicts when multiple servers provide similar tools.

## Quarantine Behavior

When a tool is called on a quarantined server:
1. The tool call is **not executed**
2. A security analysis is returned instead
3. User must approve the server via Web UI or config

This protects against Tool Poisoning Attacks (TPA).

## Connection States

Upstream servers can be in these states:

| State | Description |
|-------|-------------|
| Disconnected | Not connected to server |
| Connecting | Attempting to connect |
| Authenticating | OAuth flow in progress |
| Ready | Connected and operational |
| Error | Connection failed |

## Tool Change Notifications

MCPProxy subscribes to the MCP `notifications/tools/list_changed` notification from upstream servers. This enables automatic tool re-indexing when servers add, remove, or modify their available tools.

### How It Works

1. **Server Capability**: Servers that support tool change notifications advertise `capabilities.tools.listChanged: true` during initialization
2. **Notification Handler**: MCPProxy registers a notification handler after connecting to each server
3. **Automatic Re-indexing**: When a notification is received, MCPProxy triggers `DiscoverAndIndexToolsForServer()` within seconds
4. **Fallback Behavior**: Servers without notification support continue to use the 5-minute background polling cycle

### Supported Connection Types

Tool change notifications are supported across all connection types:
- **stdio**: Local process servers
- **HTTP/SSE**: Remote HTTP-based servers
- **Streamable HTTP**: Modern HTTP transport

### Resilience Features

- **Deduplication**: Rapid successive notifications are deduplicated to prevent redundant discovery
- **Timeout Protection**: Discovery operations have a 30-second timeout
- **Graceful Degradation**: Errors during discovery are logged but don't crash the proxy

### Logs

When notifications are received, MCPProxy logs:

| Level | Message |
|-------|---------|
| INFO | "Received tools/list_changed notification from upstream server" |
| DEBUG | "Server supports tool change notifications - registered handler" |
| DEBUG | "Tool discovery triggered by notification" |
| WARN | "Received tools notification from server that did not advertise listChanged capability" |

## Error Handling

MCP errors follow the JSON-RPC 2.0 error format:

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "error": {
    "code": -32600,
    "message": "Invalid Request",
    "data": {}
  }
}
```
