---
id: upstream-servers
title: Upstream Servers
sidebar_label: Upstream Servers
sidebar_position: 2
description: Configure MCP servers to connect through MCPProxy
keywords: [upstream, servers, mcp, stdio, http, oauth]
---

# Upstream Servers

MCPProxy can connect to multiple MCP servers simultaneously, providing unified access through a single endpoint.

## Server Types

MCPProxy supports three types of upstream connections:

### stdio Servers

Local servers that communicate via standard input/output:

```json
{
  "name": "filesystem",
  "command": "npx",
  "args": ["-y", "@modelcontextprotocol/server-filesystem", "/home/user/projects"],
  "protocol": "stdio",
  "enabled": true
}
```

### HTTP Servers

Remote servers accessible via HTTP/HTTPS:

```json
{
  "name": "remote-server",
  "url": "https://api.example.com/mcp",
  "protocol": "http",
  "enabled": true
}
```

### OAuth Servers

Servers requiring OAuth 2.1 authentication:

```json
{
  "name": "github-server",
  "url": "https://api.github.com/mcp",
  "protocol": "http",
  "oauth": {
    "client_id": "your-client-id",
    "scopes": ["repo", "user"]
  },
  "enabled": true
}
```

## Configuration Options

| Option | Type | Required | Description |
|--------|------|----------|-------------|
| `name` | string | Yes | Unique identifier for the server |
| `command` | string | For stdio | Command to execute |
| `args` | array | No | Command arguments |
| `url` | string | For HTTP | Server URL |
| `protocol` | string | Yes | `stdio` or `http` |
| `enabled` | boolean | No | Whether server is active (default: true) |
| `working_dir` | string | No | Working directory for stdio servers |
| `env` | object | No | Environment variables to pass |
| `oauth` | object | No | OAuth configuration |

## Docker Isolation

For enhanced security, stdio servers can run in Docker containers:

```json
{
  "name": "isolated-server",
  "command": "npx",
  "args": ["-y", "some-mcp-server"],
  "protocol": "stdio",
  "isolation": {
    "type": "docker",
    "image": "node:20",
    "memory_limit": "512m",
    "cpu_limit": "1.0"
  }
}
```

See [Docker Isolation](/features/docker-isolation) for complete documentation.

## Quarantine System

New servers added via AI clients are automatically quarantined for security review. See [Security Quarantine](/features/security-quarantine) for details.
