# Configuration Reference

Complete reference for MCPProxy configuration file (`mcp_config.json`). This document covers all configuration options, their defaults, and usage examples.

## Table of Contents

1. [Configuration File Location](#configuration-file-location)
2. [Basic Configuration](#basic-configuration)
3. [Server Configuration](#server-configuration)
4. [Security Settings](#security-settings)
5. [Tokenizer Configuration](#tokenizer-configuration)
6. [TLS/HTTPS Configuration](#tlshttps-configuration)
7. [Logging Configuration](#logging-configuration)
8. [Docker Isolation](#docker-isolation)
9. [Docker Recovery](#docker-recovery)
10. [Environment Configuration](#environment-configuration)
11. [Code Execution](#code-execution)
12. [Feature Flags](#feature-flags)
13. [Registries](#registries)
14. [Complete Example](#complete-example)

---

## Configuration File Location

MCPProxy looks for configuration in these locations (in order):

| OS          | Config Location                           |
| ----------- | ----------------------------------------- |
| **macOS**   | `~/.mcpproxy/mcp_config.json`             |
| **Windows** | `%USERPROFILE%\.mcpproxy\mcp_config.json` |
| **Linux**   | `~/.mcpproxy/mcp_config.json`             |

**Note:** At first launch, MCPProxy automatically generates a minimal configuration file if none exists.

---

## Basic Configuration

### Network Binding

```json
{
  "listen": "127.0.0.1:8080"
}
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `listen` | string | `"127.0.0.1:8080"` | Network address to bind to. Use `:8080` for all interfaces, `127.0.0.1:8080` for localhost only (recommended for security) |

**Examples:**
- `"127.0.0.1:8080"` - Localhost only (default, secure)
- `":8080"` - All network interfaces (use with caution)
- `"0.0.0.0:9000"` - All interfaces on port 9000

### Data Directory

```json
{
  "data_dir": "~/.mcpproxy"
}
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `data_dir` | string | `"~/.mcpproxy"` | Directory for database and certificates. Supports `~` expansion for home directory. Logs use OS log directories unless `log_dir` is set |

### Tray Application

```json
{
  "enable_tray": true,
  "enable_socket": true,
  "tray_endpoint": ""
}
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enable_tray` | boolean | `true` | Enable system tray application |
| `enable_socket` | boolean | `true` | Enable Unix socket (macOS/Linux) or named pipe (Windows) for secure local IPC between tray and core |
| `tray_endpoint` | string | `""` | Override socket/pipe path (advanced, usually not needed) |

### Search & Tool Limits

```json
{
  "top_k": 5,
  "tools_limit": 15,
  "tool_response_limit": 20000,
  "call_tool_timeout": "2m"
}
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `top_k` | integer | `5` | Number of top search results to return (1-100) |
| `tools_limit` | integer | `15` | Maximum number of tools to return per request (1-1000) |
| `tool_response_limit` | integer | `20000` | Maximum characters in tool responses (0 = unlimited) |
| `call_tool_timeout` | string | `"2m"` | Timeout for tool calls (e.g., `"30s"`, `"2m"`, `"5m"`). **Note**: When using agents like Codex or Claude as MCP servers, you may need to increase this timeout significantly, even up to 10 minutes (`"10m"`), as these agents may require longer processing times for complex operations |

### Debug & Development

```json
{
  "debug_search": false,
  "enable_prompts": true,
  "check_server_repo": true
}
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `debug_search` | boolean | `false` | Enable debug logging for search operations |
| `enable_prompts` | boolean | `true` | Enable MCP prompts feature for workflow guidance and interactive assistance with common tasks (finding tools, debugging search, setting up servers, troubleshooting connections) |
| `check_server_repo` | boolean | `true` | Enable repository detection for MCP servers (shows install commands) |

---

## Server Configuration

### Basic Server Structure

```json
{
  "mcpServers": [
    {
      "name": "my-server",
      "protocol": "stdio",
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-everything"],
      "working_dir": "/path/to/project",
      "env": {
        "API_KEY": "secret-value"
      },
      "enabled": true,
      "quarantined": false
    }
  ]
}
```

### Server Fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | Yes | Unique server identifier |
| `protocol` | string | No | Transport protocol: `stdio`, `http`, `sse`, `streamable-http`, or `auto` (default: inferred from `command`/`url`) |
| `command` | string | Yes* | Command to execute (required for `stdio` protocol) |
| `args` | array | No | Command arguments |
| `url` | string | Yes* | Server URL (required for `http`/`sse`/`streamable-http` protocols) |
| `headers` | object | No | HTTP headers for HTTP-based protocols |
| `working_dir` | string | No | Working directory for stdio servers (default: current directory) |
| `env` | object | No | Environment variables for stdio servers |
| `oauth` | object | No | OAuth configuration (see [OAuth Configuration](#oauth-configuration)) |
| `isolation` | object | No | Per-server Docker isolation settings (see [Docker Isolation](#docker-isolation)) |
| `enabled` | boolean | No | Enable/disable server (default: `true`) |
| `quarantined` | boolean | No | Security quarantine status (default: `false` for manually added servers, `true` for LLM-added servers) |
| `created` | string | No | ISO 8601 timestamp (auto-generated) |
| `updated` | string | No | ISO 8601 timestamp (auto-updated) |

### Protocol Types

**stdio** - Standard input/output (local processes):
```json
{
  "name": "local-server",
  "protocol": "stdio",
  "command": "python",
  "args": ["-m", "my_mcp_server"],
  "working_dir": "/path/to/project"
}
```

**http** - HTTP transport:
```json
{
  "name": "remote-server",
  "protocol": "http",
  "url": "https://api.example.com/mcp",
  "headers": {
    "Authorization": "Bearer token"
  }
}
```

**sse** - Server-Sent Events:
```json
{
  "name": "sse-server",
  "protocol": "sse",
  "url": "https://api.example.com/mcp/sse"
}
```

**streamable-http** - Streamable HTTP (MCP standard):
```json
{
  "name": "streamable-server",
  "protocol": "streamable-http",
  "url": "https://api.example.com/mcp"
}
```

**auto** - Auto-detect from `command` or `url`:
```json
{
  "name": "auto-server",
  "protocol": "auto",
  "command": "npx",
  "args": ["-y", "my-server"]
}
```

### OAuth Configuration

```json
{
  "oauth": {
    "client_id": "your-client-id",
    "client_secret": "secret-reference",
    "redirect_uri": "http://localhost:8080/oauth/callback",
    "scopes": ["repo", "user"],
    "pkce_enabled": true
  }
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `client_id` | string | No | OAuth client ID (uses Dynamic Client Registration if empty) |
| `client_secret` | string | No | OAuth client secret (can reference secure storage) |
| `redirect_uri` | string | No | OAuth redirect URI (auto-generated if not provided) |
| `scopes` | array | No | OAuth scopes to request |
| `pkce_enabled` | boolean | No | PKCE is always enabled for security; this flag is currently ignored |

See [OAuth Documentation](mcp-go-oauth.md) for complete details.

---

## Security Settings

```json
{
  "api_key": "your-secret-api-key",
  "read_only_mode": false,
  "disable_management": false,
  "allow_server_add": true,
  "allow_server_remove": true
}
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `api_key` | string | Auto-generated | API key for REST API authentication. Required; if empty, one is auto-generated and enforced (logged on startup) |
| `read_only_mode` | boolean | `false` | Prevent all configuration modifications |
| `disable_management` | boolean | `false` | Disable server management operations (restart, enable, disable) |
| `allow_server_add` | boolean | `true` | Allow adding new servers via API/tools |
| `allow_server_remove` | boolean | `true` | Allow removing servers via API/tools |

**Security Notes:**
- **API Key**: Set via `--api-key` flag, `MCPPROXY_API_KEY` environment variable, or config file
- **Empty API Key**: Empty values are replaced with an auto-generated key; authentication is always enforced
- **Auto-Generation**: If no API key is provided, one is generated and logged for easy access
- **Tray Integration**: Tray app automatically manages API keys for core communication

---

## Tokenizer Configuration

The tokenizer provides **local token counting** using the tiktoken library. It does **not** access LLMs or make API calls—it's purely for counting tokens in text locally.

### Basic Configuration

```json
{
  "tokenizer": {
    "enabled": true,
    "default_model": "gpt-4",
    "encoding": "cl100k_base"
  }
}
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | boolean | `true` | Enable/disable token counting |
| `default_model` | string | `"gpt-4"` | Default model name for tokenization (used to determine encoding when model not specified) |
| `encoding` | string | `"cl100k_base"` | Default tiktoken encoding to use |

### How Tokenizer Works

**Important:** The tokenizer does **not** access LLMs. It performs local token counting using the tiktoken algorithm:

1. **Local Processing**: All token counting happens locally using the tiktoken library
2. **No Network Calls**: No API requests or external services are used
3. **Model Mapping**: The `default_model` field is used to look up the appropriate encoding via `GetEncodingForModel()`
4. **Encoding Selection**: If a model isn't recognized, it falls back to the `encoding` field or `cl100k_base`

### Supported Models & Encodings

The tokenizer automatically maps model names to encodings:

**GPT-4o Series** (uses `o200k_base`):
- `gpt-4o`, `gpt-4o-mini`, `gpt-4.1`, `gpt-4.5`, `gpt-4o-2024-05-13`, `gpt-4o-2024-08-06`

**GPT-4 & GPT-3.5 Series** (uses `cl100k_base`):
- `gpt-4`, `gpt-4-turbo`, `gpt-3.5-turbo`, `gpt-3.5-turbo-16k`, `text-embedding-ada-002`, etc.

**Claude Models** (uses `cl100k_base` as approximation):
- `claude-3-5-sonnet`, `claude-3-opus`, `claude-3-sonnet`, `claude-3-haiku`, `claude-2.1`, `claude-2.0`, `claude-instant`
- **Note**: Claude models use `cl100k_base` as an approximation. For accurate counts, use Anthropic's `count_tokens` API.

**Codex Series** (uses `p50k_base`):
- `code-davinci-002`, `code-davinci-001`, `code-cushman-002`, `code-cushman-001`

**Older GPT-3 Series** (uses `r50k_base`):
- `text-davinci-003`, `text-davinci-002`, `davinci`, `curie`, `babbage`, `ada`

### Supported Encodings

| Encoding | Models | Description |
|----------|--------|-------------|
| `o200k_base` | GPT-4o, GPT-4.5 | Latest OpenAI models |
| `cl100k_base` | GPT-4, GPT-3.5, Claude (approx) | Most common encoding |
| `p50k_base` | Codex | Code generation models |
| `r50k_base` | GPT-3 | Legacy models |

### Usage Examples

**For GPT-4:**
```json
{
  "tokenizer": {
    "enabled": true,
    "default_model": "gpt-4",
    "encoding": "cl100k_base"
  }
}
```

**For Claude Models:**
```json
{
  "tokenizer": {
    "enabled": true,
    "default_model": "claude-3-5-sonnet",
    "encoding": "cl100k_base"
  }
}
```

**For GPT-4o:**
```json
{
  "tokenizer": {
    "enabled": true,
    "default_model": "gpt-4o",
    "encoding": "o200k_base"
  }
}
```

**Disable Token Counting:**
```json
{
  "tokenizer": {
    "enabled": false
  }
}
```

### What Tokenizer Is Used For

- **Token Usage Tracking**: Counts tokens in MCP tool calls and responses
- **Token Savings Calculation**: Calculates token savings from caching
- **Metrics & Monitoring**: Provides token metrics for observability
- **Response Truncation**: Helps determine when to truncate large responses

---

## TLS/HTTPS Configuration

```json
{
  "tls": {
    "enabled": false,
    "require_client_cert": false,
    "certs_dir": "",
    "hsts": true
  }
}
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | boolean | `false` | Enable HTTPS/TLS |
| `require_client_cert` | boolean | `false` | Enable mutual TLS (mTLS) for client authentication |
| `certs_dir` | string | `""` | Custom certificate directory (defaults to `${data_dir}/certs`) |
| `hsts` | boolean | `true` | Enable HTTP Strict Transport Security headers |

**Quick Setup:**
1. Trust certificate: `mcpproxy trust-cert`
2. Enable TLS: Set `"enabled": true` or `MCPPROXY_TLS_ENABLED=true`
3. Update client URLs to use `https://`

See [Setup Guide - HTTPS](setup.md#optional-https-setup) for complete details.

---

## Logging Configuration

```json
{
  "logging": {
    "level": "info",
    "enable_file": false,
    "enable_console": true,
    "filename": "main.log",
    "log_dir": "",
    "max_size": 10,
    "max_backups": 5,
    "max_age": 30,
    "compress": true,
    "json_format": false
  }
}
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `level` | string | `"info"` | Log level: `trace`, `debug`, `info`, `warn`, `error` |
| `enable_file` | boolean | `false` | Enable file logging |
| `enable_console` | boolean | `true` | Enable console logging |
| `filename` | string | `"main.log"` | Log filename |
| `log_dir` | string | `""` | Custom log directory (defaults to OS log root; see below) |
| `max_size` | integer | `10` | Maximum log file size in MB before rotation |
| `max_backups` | integer | `5` | Number of backup log files to keep |
| `max_age` | integer | `30` | Maximum age of log files in days |
| `compress` | boolean | `true` | Compress rotated log files |
| `json_format` | boolean | `false` | Use JSON format (useful for log aggregation) |

**Log Locations (defaults):**
- **macOS:** `~/Library/Logs/mcpproxy/main.log`
- **Linux:** `~/.local/state/mcpproxy/logs/main.log` (or `/var/log/mcpproxy` when running as root)
- **Windows:** `%LOCALAPPDATA%\mcpproxy\logs\main.log`
- **Per-server logs:** same directory, `server-{name}.log`
- **Custom:** set `log_dir` to override (supports `~` expansion)

**Behavior notes:**
- `mcpproxy serve` enables file logging by default unless `--log-to-file` is explicitly set to `false`

See [Logging Documentation](logging.md) for complete details.

---

## Docker Isolation

### Global Docker Isolation Settings

```json
{
  "docker_isolation": {
    "enabled": false,
    "default_images": {
      "python": "python:3.11",
      "node": "node:20",
      "npx": "node:20"
    },
    "registry": "docker.io",
    "network_mode": "bridge",
    "memory_limit": "512m",
    "cpu_limit": "1.0",
    "timeout": "30s",
    "extra_args": [],
    "log_driver": "",
    "log_max_size": "100m",
    "log_max_files": "3"
  }
}
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | boolean | `false` | Enable Docker isolation globally |
| `default_images` | object | See below | Map of runtime type to Docker image |
| `registry` | string | `"docker.io"` | Docker registry to use |
| `network_mode` | string | `"bridge"` | Docker network mode |
| `memory_limit` | string | `"512m"` | Memory limit for containers |
| `cpu_limit` | string | `"1.0"` | CPU limit (1 core) |
| `timeout` | string | `"30s"` | Container startup timeout |
| `extra_args` | array | `[]` | Additional `docker run` arguments |
| `log_driver` | string | `""` | Docker log driver (empty = system default) |
| `log_max_size` | string | `"100m"` | Maximum log file size |
| `log_max_files` | string | `"3"` | Maximum number of log files |

### Default Docker Images

```json
{
  "python": "python:3.11",
  "python3": "python:3.11",
  "uvx": "python:3.11",
  "pip": "python:3.11",
  "pipx": "python:3.11",
  "node": "node:20",
  "npm": "node:20",
  "npx": "node:20",
  "yarn": "node:20",
  "go": "golang:1.21-alpine",
  "cargo": "rust:1.75-slim",
  "rustc": "rust:1.75-slim",
  "binary": "alpine:3.18",
  "sh": "alpine:3.18",
  "bash": "alpine:3.18",
  "ruby": "ruby:3.2-alpine",
  "gem": "ruby:3.2-alpine",
  "php": "php:8.2-cli-alpine",
  "composer": "php:8.2-cli-alpine"
}
```

### Per-Server Isolation Settings

```json
{
  "mcpServers": [
    {
      "name": "isolated-server",
      "isolation": {
        "enabled": true,
        "image": "custom-image:latest",
        "network_mode": "none",
        "extra_args": ["--cap-drop=ALL"],
        "working_dir": "/app",
        "log_driver": "json-file",
        "log_max_size": "50m",
        "log_max_files": "2"
      }
    }
  ]
}
```

| Field | Type | Description |
|-------|------|-------------|
| `enabled` | boolean | Enable Docker isolation for this server (overrides global setting) |
| `image` | string | Custom Docker image (overrides default) |
| `network_mode` | string | Custom network mode for this server |
| `extra_args` | array | Additional `docker run` arguments |
| `working_dir` | string | Working directory inside container |
| `log_driver` | string | Log driver override |
| `log_max_size` | string | Log file size override |
| `log_max_files` | string | Log file count override |

See [Docker Isolation Documentation](docker-isolation.md) for complete details.

---

## Docker Recovery

```json
{
  "docker_recovery": {
    "enabled": true,
    "check_intervals": ["2s", "5s", "10s", "30s", "60s"],
    "max_retries": 0,
    "notify_on_start": true,
    "notify_on_success": true,
    "notify_on_failure": true,
    "notify_on_retry": false,
    "persistent_state": true
  }
}
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | boolean | `true` | Enable Docker recovery monitoring |
| `check_intervals` | array | `["2s", "5s", "10s", "30s", "60s"]` | Exponential backoff intervals for health checks |
| `max_retries` | integer | `0` | Maximum retry attempts (0 = unlimited) |
| `notify_on_start` | boolean | `true` | Show notification when recovery starts |
| `notify_on_success` | boolean | `true` | Show notification on successful recovery |
| `notify_on_failure` | boolean | `true` | Show notification on recovery failure |
| `notify_on_retry` | boolean | `false` | Show notification on each retry |
| `persistent_state` | boolean | `true` | Save recovery state across restarts |

See [Docker Recovery Documentation](docker-recovery-phase3.md) for complete details.

---

## Environment Configuration

```json
{
  "environment": {
    "inherit_system_safe": true,
    "allowed_system_vars": [
      "PATH",
      "HOME",
      "TMPDIR",
      "NODE_PATH"
    ],
    "custom_vars": {
      "CUSTOM_VAR": "value"
    },
    "enhance_path": false
  }
}
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `inherit_system_safe` | boolean | `true` | Inherit safe system environment variables |
| `allowed_system_vars` | array | See below | List of system variables to allow |
| `custom_vars` | object | `{}` | Custom environment variables to set |
| `enhance_path` | boolean | `false` | Enable PATH enhancement for Launchd scenarios |

**Default Allowed System Variables:**
- Core: `PATH`, `HOME`, `TMPDIR`, `TEMP`, `TMP`, `SHELL`, `TERM`, `LANG`, `USER`, `USERNAME`
- Windows-specific: `USERPROFILE`, `APPDATA`, `LOCALAPPDATA`, `PROGRAMFILES`, `SYSTEMROOT`, `COMSPEC`
- Unix/XDG: `XDG_CONFIG_HOME`, `XDG_DATA_HOME`, `XDG_CACHE_HOME`, `XDG_RUNTIME_DIR`
- Locale: all `LC_*` variables (e.g., `LC_ALL`, `LC_CTYPE`, …)
- Custom additions: `custom_vars` merged on top

---

## Code Execution

```json
{
  "enable_code_execution": false,
  "code_execution_timeout_ms": 120000,
  "code_execution_max_tool_calls": 0,
  "code_execution_pool_size": 10
}
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enable_code_execution` | boolean | `false` | Enable JavaScript code execution tool (disabled by default for security) |
| `code_execution_timeout_ms` | integer | `120000` | Default timeout in milliseconds (1-600000, max 10 minutes) |
| `code_execution_max_tool_calls` | integer | `0` | Maximum tool calls per execution (0 = unlimited) |
| `code_execution_pool_size` | integer | `10` | Number of JavaScript VM instances in pool (1-100) |

See [Code Execution Documentation](code_execution/overview.md) for complete details.

---

## Feature Flags

```json
{
  "features": {
    "enable_runtime": true,
    "enable_event_bus": true,
    "enable_sse": true,
    "enable_observability": true,
    "enable_health_checks": true,
    "enable_metrics": true,
    "enable_tracing": false,
    "enable_oauth": true,
    "enable_quarantine": true,
    "enable_docker_isolation": false,
    "enable_search": true,
    "enable_caching": true,
    "enable_async_storage": true,
    "enable_web_ui": true,
    "enable_tray": true,
    "enable_debug_logging": false,
    "enable_contract_tests": false
  }
}
```

**Note:** Feature flags are typically managed internally. Most users don't need to modify these settings.

---

## Registries

```json
{
  "registries": [
    {
      "id": "pulse",
      "name": "Pulse MCP",
      "description": "Browse and discover MCP use-cases, servers, clients, and news",
      "url": "https://www.pulsemcp.com/",
      "servers_url": "https://api.pulsemcp.com/v0beta/servers",
      "tags": ["verified"],
      "protocol": "custom/pulse"
    }
  ]
}
```

| Field | Type | Description |
|-------|------|-------------|
| `id` | string | Unique registry identifier |
| `name` | string | Display name |
| `description` | string | Registry description |
| `url` | string | Registry homepage |
| `servers_url` | string | API endpoint for server listings |
| `tags` | array | Registry tags (e.g., `["verified"]`) |
| `protocol` | string | Registry protocol type |
| `count` | number/string | Number of servers in registry (auto-populated) |

**Default Registries:**
- Pulse MCP
- Docker MCP Catalog
- Fleur
- Azure MCP Registry Demo
- Remote MCP Servers

See [Search Servers Documentation](search_servers.md) for complete details.

---

## Complete Example

Here's a complete configuration example with all major sections:

**Note:** Leaving `api_key` empty will cause MCPProxy to generate and enforce a new key on startup.

```json
{
  "listen": "127.0.0.1:8080",
  "data_dir": "~/.mcpproxy",
  "enable_tray": true,
  "enable_socket": true,
  "api_key": "",
  "top_k": 5,
  "tools_limit": 15,
  "tool_response_limit": 20000,
  "call_tool_timeout": "2m",
  "debug_search": false,
  "enable_prompts": true,
  "check_server_repo": true,
  
  "tokenizer": {
    "enabled": true,
    "default_model": "gpt-4",
    "encoding": "cl100k_base"
  },
  
  "tls": {
    "enabled": false,
    "require_client_cert": false,
    "hsts": true
  },
  
  "logging": {
    "level": "info",
    "enable_file": false,
    "enable_console": true,
    "filename": "main.log",
    "max_size": 10,
    "max_backups": 5,
    "max_age": 30,
    "compress": true,
    "json_format": false
  },
  
  "mcpServers": [
    {
      "name": "everything",
      "protocol": "stdio",
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-everything"],
      "enabled": true,
      "quarantined": false
    },
    {
      "name": "github",
      "protocol": "http",
      "url": "https://api.github.com/mcp",
      "oauth": {
        "scopes": ["repo", "user"],
        "pkce_enabled": true
      },
      "enabled": true
    }
  ],
  
  "docker_isolation": {
    "enabled": false
  },
  
  "docker_recovery": {
    "enabled": true,
    "notify_on_start": true,
    "notify_on_success": true,
    "notify_on_failure": true
  },
  
  "environment": {
    "inherit_system_safe": true,
    "allowed_system_vars": ["PATH", "HOME", "TMPDIR"],
    "custom_vars": {},
    "enhance_path": false
  },
  
  "enable_code_execution": false,
  "code_execution_timeout_ms": 120000,
  "code_execution_max_tool_calls": 0,
  "code_execution_pool_size": 10,
  
  "read_only_mode": false,
  "disable_management": false,
  "allow_server_add": true,
  "allow_server_remove": true
}
```

---

## Environment Variables

Many configuration options can be overridden via environment variables:

| Environment Variable | Config Field | Description |
|----------------------|--------------|-------------|
| `MCPPROXY_LISTEN` / `MCPP_LISTEN` | `listen` | Network binding address |
| `MCPPROXY_API_KEY` | `api_key` | API key for authentication (empty values trigger auto-generation; auth remains enabled) |
| `MCPPROXY_TLS_ENABLED` | `tls.enabled` | Enable HTTPS/TLS |
| `MCPPROXY_TLS_REQUIRE_CLIENT_CERT` | `tls.require_client_cert` | Enable mTLS |
| `MCPPROXY_CERTS_DIR` | `tls.certs_dir` | Custom certificates directory |
| `MCPPROXY_DATA` | `data_dir` | Override data directory |
| `MCPPROXY_DISABLE_OAUTH` | - | Disable OAuth for testing |
| `HEADLESS` | - | Run in headless mode |

**Prefix rules:**
- General settings also accept the `MCPP_` prefix (hyphens become underscores), e.g., `MCPP_TOP_K`, `MCPP_TOOLS_LIMIT`, `MCPP_ENABLE_PROMPTS`.
- TLS/listen/data have additional convenience overrides with the `MCPPROXY_` prefix as listed above.

**Priority:** Environment variables > Config file > Defaults

---

## Validation

MCPProxy validates configuration on startup. Common validation errors:

- **Invalid listen address**: Must be `host:port` or `:port` format
- **Invalid top_k**: Must be between 1 and 100
- **Invalid tools_limit**: Must be between 1 and 1000
- **Missing server name**: Each server must have a unique name
- **Invalid protocol**: Must be `stdio`, `http`, `sse`, `streamable-http`, or `auto`
- **Missing command**: stdio servers require `command` field
- **Missing url**: HTTP-based servers require `url` field
- **Invalid timeout**: Must be a valid duration string (e.g., `"30s"`, `"2m"`)

Run `mcpproxy doctor` to check configuration health.

---

## Related Documentation

- [Setup Guide](setup.md) - Initial setup and client configuration
- [OAuth Documentation](mcp-go-oauth.md) - OAuth authentication setup
- [Docker Isolation](docker-isolation.md) - Docker security isolation
- [Logging](logging.md) - Logging configuration and management
- [Code Execution](code_execution/overview.md) - JavaScript code execution
- [Search Servers](search_servers.md) - MCP server discovery
