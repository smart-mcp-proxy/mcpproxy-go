---
id: quick-start
title: Quick Start
sidebar_label: Quick Start
sidebar_position: 2
description: Get MCPProxy running in 5 minutes
keywords: [quickstart, getting started, first run, configuration]
---

# Quick Start

This guide will get MCPProxy running in 5 minutes.

## Prerequisites

- MCPProxy installed (see [Installation](/getting-started/installation))
- An MCP-compatible AI client (Claude Desktop, etc.)

## 1. Start the Server

Start MCPProxy in your terminal:

```bash
mcpproxy serve
```

You should see output like:

```
MCPProxy server started
Listening on http://127.0.0.1:8080
Web UI available at http://127.0.0.1:8080/ui/
```

## 2. Open the Web UI

Open your browser to [http://127.0.0.1:8080/ui/](http://127.0.0.1:8080/ui/) to access the management dashboard.

## 3. Add Your First MCP Server

You can add MCP servers in two ways:

### Via Web UI

1. Click "Add Server" in the Web UI
2. Enter the server details
3. Click "Save"

### Via Configuration File

Edit `~/.mcpproxy/mcp_config.json`:

```json
{
  "mcpServers": [
    {
      "name": "filesystem",
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-filesystem", "/path/to/directory"],
      "protocol": "stdio",
      "enabled": true
    }
  ]
}
```

## 4. Connect Your AI Client

Configure your AI client to connect to MCPProxy:

**MCP Endpoint:** `http://127.0.0.1:8080/mcp`

For Claude Desktop, add to your configuration:

```json
{
  "mcpServers": {
    "mcpproxy": {
      "url": "http://127.0.0.1:8080/mcp"
    }
  }
}
```

## 5. Verify Connection

In your AI client, ask it to list available tools. You should see tools from all your configured MCP servers.

## Next Steps

- Learn about [configuration options](/configuration/config-file)
- Set up [upstream servers](/configuration/upstream-servers)
- Explore [CLI commands](/cli/command-reference)
