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

## Endpoint

```
http://127.0.0.1:8080/mcp
```

**Note:** The MCP endpoint does not require API key authentication for client compatibility.

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

### call_tool

Execute a tool on an upstream server.

**Input Schema:**
```json
{
  "server": "string (required) - Server name",
  "tool": "string (required) - Tool name",
  "arguments": "object (optional) - Tool arguments"
}
```

**Example:**
```json
{
  "server": "github",
  "tool": "create_issue",
  "arguments": {
    "repo": "owner/repo",
    "title": "Bug report",
    "body": "Description of the issue"
  }
}
```

### upstream_servers

Manage upstream server configurations.

**Actions:**
- `list` - List all servers
- `add` - Add a new server
- `remove` - Remove a server
- `enable` - Enable a server
- `disable` - Disable a server

**Example (list):**
```json
{
  "action": "list"
}
```

### code_execution

Execute JavaScript code to orchestrate multiple tools. Disabled by default.

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
