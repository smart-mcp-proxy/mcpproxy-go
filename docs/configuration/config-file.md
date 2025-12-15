---
id: config-file
title: Configuration File
sidebar_label: Config File
sidebar_position: 1
description: Complete reference for mcp_config.json
keywords: [config, configuration, mcp_config.json, settings]
---

# Configuration File

MCPProxy uses a JSON configuration file located at `~/.mcpproxy/mcp_config.json`.

## Location

| Platform | Default Location |
|----------|-----------------|
| macOS | `~/.mcpproxy/mcp_config.json` |
| Linux | `~/.mcpproxy/mcp_config.json` |
| Windows | `%USERPROFILE%\.mcpproxy\mcp_config.json` |

## Complete Reference

```json
{
  "listen": "127.0.0.1:8080",
  "data_dir": "~/.mcpproxy",
  "api_key": "your-secret-api-key",
  "enable_socket": true,
  "top_k": 5,
  "tools_limit": 15,
  "tool_response_limit": 20000,
  "enable_code_execution": false,
  "code_execution_timeout_ms": 120000,
  "code_execution_max_tool_calls": 0,
  "code_execution_pool_size": 10,
  "features": {
    "enable_web_ui": true
  },
  "mcpServers": []
}
```

## Options

### Server Settings

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `listen` | string | `127.0.0.1:8080` | Address and port to listen on |
| `data_dir` | string | `~/.mcpproxy` | Directory for data storage |
| `api_key` | string | auto-generated | API key for REST API authentication |
| `enable_socket` | boolean | `true` | Enable Unix socket/named pipe for local communication |

### Feature Flags

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `features.enable_web_ui` | boolean | `true` | Enable the web management interface |

### Tool Discovery Settings

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `top_k` | integer | `5` | Number of top results for tool search |
| `tools_limit` | integer | `15` | Maximum tools to return in a single request |
| `tool_response_limit` | integer | `20000` | Maximum characters in tool response |

### Code Execution Settings

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `enable_code_execution` | boolean | `false` | Enable JavaScript code execution tool |
| `code_execution_timeout_ms` | integer | `120000` | Execution timeout in milliseconds |
| `code_execution_max_tool_calls` | integer | `0` | Maximum tool calls (0 = unlimited) |
| `code_execution_pool_size` | integer | `10` | VM pool size for code execution |

### MCP Servers

See [Upstream Servers](/configuration/upstream-servers) for detailed server configuration.

## Hot Reload

MCPProxy watches the configuration file for changes and automatically reloads when modifications are detected. No restart is required for most configuration changes.

## Environment Variable Overrides

Configuration options can be overridden using environment variables. See [Environment Variables](/configuration/environment-variables) for details.
