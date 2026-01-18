---
id: command-reference
title: Command Reference
sidebar_label: Command Reference
sidebar_position: 1
description: Complete CLI command reference for MCPProxy
keywords: [cli, commands, terminal, shell]
---

# Command Reference

Complete reference for all MCPProxy CLI commands.

## Global Flags

These flags are available for all commands:

| Flag | Description |
|------|-------------|
| `--config` | Path to configuration file |
| `--log-level` | Log level (debug, info, warn, error) |
| `--data-dir, -d` | Data directory path (default: ~/.mcpproxy) |
| `--log-to-file` | Enable logging to file in standard OS location |
| `--log-dir` | Custom log directory path (overrides standard OS location) |
| `--help` | Show help for command |

## Execution Modes

CLI commands like `tools list`, `call tool`, `code exec`, and `auth login` support two execution modes:

### Daemon Mode (Default)

When `mcpproxy serve` is running, CLI commands automatically connect to it via Unix socket (macOS/Linux) or named pipe (Windows). This provides:

- **Fast execution** - Daemon is already loaded with connections established
- **Shared state** - OAuth tokens, server connections, and search indices are shared
- **Real-time sync** - Changes made via CLI reflect immediately in daemon

**Detection**: CLI checks for socket at `~/.mcpproxy/mcpproxy.sock` (Unix) or `\\.\pipe\mcpproxy-<username>` (Windows).

```bash
# Start daemon
mcpproxy serve &

# These commands use daemon mode automatically
mcpproxy tools list --server=github-server    # Fast - uses daemon
mcpproxy auth login --server=oauth-server     # OAuth tokens shared with daemon
mcpproxy call tool --tool-name=github:search --json_args='{}'  # Uses daemon's connection pool
```

### Standalone Mode (Direct Connection)

When no daemon is detected, CLI commands create direct connections to upstream MCP servers. This is useful for:

- **Debugging** - Full control over connection with verbose logging
- **Isolated testing** - Independent of daemon state
- **Single-use operations** - No need to run persistent daemon

```bash
# Stop daemon to use standalone mode
pkill -f "mcpproxy serve"

# Now commands connect directly to upstream servers
mcpproxy tools list --server=github-server --log-level=debug
mcpproxy tools list --server=github-server --trace-transport  # Full HTTP/SSE tracing
```

:::tip Forcing Standalone Mode
To debug a specific server connection without stopping the daemon:

```bash
# Use a different data directory (creates isolated socket path)
mcpproxy tools list --server=github-server --data-dir=/tmp/debug-session

# Or set empty endpoint to skip socket detection
MCPPROXY_TRAY_ENDPOINT="" mcpproxy tools list --server=github-server
```
:::

### Mode Comparison

| Aspect | Daemon Mode | Standalone Mode |
|--------|-------------|-----------------|
| **Startup** | Fast (< 1s) | Slower (2-5s, initializes components) |
| **OAuth Tokens** | Shared globally | Isolated per command |
| **Server State** | Persistent | Ephemeral |
| **Debugging** | Limited visibility | Full component tracing |
| **Use Case** | Production / Normal use | Debugging / Testing |

### Environment Variables

| Variable | Description |
|----------|-------------|
| `MCPPROXY_TRAY_ENDPOINT` | Override socket path. Set to empty string `""` to force standalone mode |

**Examples:**
```bash
# Custom socket endpoint
MCPPROXY_TRAY_ENDPOINT="unix:///tmp/custom.sock" mcpproxy tools list --server=myserver

# Force standalone mode (skip daemon)
MCPPROXY_TRAY_ENDPOINT="" mcpproxy tools list --server=myserver --log-level=trace
```

:::note auth status requires daemon
The `auth status` command requires a running daemon since it queries the daemon's OAuth state:
```bash
mcpproxy auth status --server=oauth-server
# Error: auth status requires running daemon. Start with: mcpproxy serve
```
:::

## Server Commands

### serve

Start the MCPProxy server:

```bash
mcpproxy serve [flags]
```

| Flag | Description | Default |
|------|-------------|---------|
| `--listen` | Address to listen on | `127.0.0.1:8080` |
| `--api-key` | API key for authentication | auto-generated |
| `--enable-socket` | Enable Unix socket/named pipe | `true` |
| `--tray-endpoint` | Tray endpoint override (unix:///path/socket.sock or npipe:////./pipe/name) | - |
| `--debug-search` | Enable debug search tool | `false` |
| `--tool-response-limit` | Tool response limit in characters (0 = disabled) | `0` |
| `--read-only` | Enable read-only mode | `false` |
| `--disable-management` | Disable management features | `false` |
| `--allow-server-add` | Allow adding new servers | `true` |
| `--allow-server-remove` | Allow removing servers | `true` |
| `--enable-prompts` | Enable prompts for user input | `true` |

### doctor

Run health diagnostics:

```bash
mcpproxy doctor
```

Checks for:
- Upstream server connection errors
- OAuth authentication requirements
- Missing secrets
- Runtime warnings
- Docker isolation status

## Upstream Management

### upstream list

List all configured servers:

```bash
mcpproxy upstream list [flags]
```

| Flag | Description | Default |
|------|-------------|---------|
| `--output, -o` | Output format: table, json | `table` |

### upstream logs

View server logs:

```bash
mcpproxy upstream logs <server-name> [flags]
```

| Flag | Description |
|------|-------------|
| `--tail` | Number of lines to show |
| `--follow` | Follow log output |

### upstream restart

Restart a server:

```bash
mcpproxy upstream restart <server-name>
mcpproxy upstream restart --all
```

### upstream enable/disable

Enable or disable a server:

```bash
mcpproxy upstream enable <server-name>
mcpproxy upstream disable <server-name>
```

## Configuration Import

### import

Import MCP server configurations from other AI tools:

```bash
mcpproxy import [flags]
```

| Flag | Description | Default |
|------|-------------|---------|
| `--path` | Path to configuration file | - |
| `--format` | Force format (claude-desktop, claude-code, cursor, codex, gemini) | auto-detect |
| `--servers` | Comma-separated list of server names to import | all |
| `--preview` | Preview without importing | `false` |

**Supported Formats:**

| Source | Format Flag | Auto-detected |
|--------|-------------|---------------|
| Claude Desktop | `claude-desktop` | Yes |
| Claude Code | `claude-code` | Yes |
| Cursor IDE | `cursor` | Yes |
| Codex CLI | `codex` | Yes (TOML) |
| Gemini CLI | `gemini` | Yes |

**Examples:**

```bash
# Import from Claude Desktop config
mcpproxy import --path ~/Library/Application\ Support/Claude/claude_desktop_config.json

# Import from Claude Code config
mcpproxy import --path ~/.claude.json

# Preview without importing
mcpproxy import --path config.json --preview

# Import with format hint (if auto-detect fails)
mcpproxy import --path config.json --format claude-desktop

# Import only specific servers
mcpproxy import --path config.json --servers "github-server,filesystem"
```

**Canonical Config Paths:**

| Source | macOS | Windows | Linux |
|--------|-------|---------|-------|
| Claude Desktop | `~/Library/Application Support/Claude/claude_desktop_config.json` | `%APPDATA%\Claude\claude_desktop_config.json` | `~/.config/Claude/claude_desktop_config.json` |
| Claude Code | `~/.claude.json` | `~/.claude.json` | `~/.claude.json` |
| Cursor IDE | `~/.cursor/mcp.json` | `~/.cursor/mcp.json` | `~/.cursor/mcp.json` |
| Codex CLI | `~/.codex/config.toml` | `~/.codex/config.toml` | `~/.codex/config.toml` |
| Gemini CLI | `~/.gemini/settings.json` | `~/.gemini/settings.json` | `~/.gemini/settings.json` |

:::note Imported servers are quarantined
For security, all imported servers are quarantined by default. Review and approve them before enabling.
:::

See [Configuration Import](/features/config-import) for Web UI and REST API documentation.

## Server Discovery

### search-servers

Search MCP registries for available servers:

```bash
mcpproxy search-servers [flags]
```

| Flag | Description |
|------|-------------|
| `-r, --registry` | Registry ID or name to search (exact match) |
| `-s, --search` | Search term for server name/description |
| `-t, --tag` | Filter servers by tag/category |
| `-l, --limit` | Maximum results (default: 10, max: 50) |
| `--list-registries` | List all known registries |

## Tool Commands

### tools list

List available tools:

```bash
mcpproxy tools list [flags]
```

| Flag | Description | Default |
|------|-------------|---------|
| `--server` | Filter by server name | - |
| `--timeout, -t` | Connection timeout | `30s` |
| `--output, -o` | Output format: table, json, yaml | `table` |
| `--trace-transport` | Enable detailed HTTP/SSE frame-by-frame tracing | `false` |

### call tool

Execute a tool:

```bash
mcpproxy call tool --tool-name=<server:tool> [flags]
```

| Flag | Description | Default |
|------|-------------|---------|
| `--tool-name` | Tool name in format `server:tool` or built-in tool name | - |
| `--json_args, -j` | JSON arguments for the tool | `{}` |
| `--output, -o` | Output format: pretty, json | `pretty` |

**Examples:**
```bash
# Call a built-in tool
mcpproxy call tool --tool-name=upstream_servers --json_args='{"operation":"list"}'

# Call an upstream server tool
mcpproxy call tool --tool-name=github:list_repos --json_args='{"owner":"myorg"}'
```

### Intent-Based Tool Variants

For granular permission control, use intent-based tool variants:

```bash
# Read-only operations (safe, no side effects)
mcpproxy call tool-read --tool-name=github:list_repos --json_args='{}'

# Write operations (creates/modifies state)
mcpproxy call tool-write --tool-name=github:create_issue --json_args='{"title":"Bug"}'

# Destructive operations (deletes/removes state)
mcpproxy call tool-destructive --tool-name=github:delete_repo --json_args='{"repo":"test"}'
```

| Flag | Description | Default |
|------|-------------|---------|
| `--tool-name` | Tool name in format `server:tool` | - |
| `--json_args, -j` | JSON arguments for the tool | `{}` |
| `--reason` | Human-readable reason for the operation | - |
| `--sensitivity` | Data sensitivity: public, internal, private, unknown | - |
| `--output, -o` | Output format: pretty, json | `pretty` |

## Code Execution

### code exec

Execute JavaScript code:

```bash
mcpproxy code exec [flags]
```

| Flag | Description | Default |
|------|-------------|---------|
| `--code` | JavaScript code to execute | - |
| `--file` | Path to JavaScript file (alternative to --code) | - |
| `--input` | JSON input data | `{}` |
| `--input-file` | Path to JSON file containing input data | - |
| `--max-tool-calls` | Maximum tool calls (0 = unlimited) | `0` |
| `--allowed-servers` | Comma-separated list of allowed servers | - |

**Example:**
```bash
mcpproxy code exec --code="({ result: input.value * 2 })" --input='{"value": 21}'
```

See [Code Execution](/features/code-execution) for detailed documentation.

## Authentication

### auth login

Authenticate with an OAuth server:

```bash
mcpproxy auth login [flags]
```

| Flag | Description | Default |
|------|-------------|---------|
| `--server` | Server name to authenticate with (required) | - |
| `--timeout` | Authentication timeout | `5m` |

### auth status

Check authentication status:

```bash
mcpproxy auth status [flags]
```

| Flag | Description |
|------|-------------|
| `--server, -s` | Server name to check status for |
| `--all` | Show status for all servers |

### auth logout

Clear OAuth token and disconnect from a server:

```bash
mcpproxy auth logout [flags]
```

| Flag | Description | Default |
|------|-------------|---------|
| `-s, --server` | Server name to logout from (required) | - |
| `--timeout` | Logout timeout | `30s` |

## Secrets Management

### secrets set

Store a secret in the system keyring:

```bash
mcpproxy secrets set <key> <value> [flags]
```

| Flag | Description |
|------|-------------|
| `--type` | Secret type (api-key, oauth-token, password) |
| `--from-env` | Read value from environment variable |
| `--from-stdin` | Read value from stdin |

**Examples:**
```bash
mcpproxy secrets set github-token "ghp_abc123" --type=oauth-token
mcpproxy secrets set api-key --from-env=MY_API_KEY
echo "secret-value" | mcpproxy secrets set db-password --from-stdin
```

### secrets get

Retrieve a secret:

```bash
mcpproxy secrets get <key> [flags]
```

| Flag | Description |
|------|-------------|
| `--type` | Secret type filter |
| `--masked` | Show masked value (first/last 4 chars) |

### secrets del

Delete a secret:

```bash
mcpproxy secrets del <key> [flags]
```

| Flag | Description |
|------|-------------|
| `--type` | Secret type filter |

### secrets list

List all stored secrets:

```bash
mcpproxy secrets list [flags]
```

| Flag | Description |
|------|-------------|
| `--json` | Output in JSON format |
| `--all` | Show all secret metadata |

### secrets migrate

Migrate secrets between storage backends:

```bash
mcpproxy secrets migrate [flags]
```

| Flag | Description | Default |
|------|-------------|---------|
| `--dry-run` | Show what would be migrated without executing | `false` |
| `--auto-approve` | Skip confirmation prompts | `false` |
| `--from` | Source storage backend | - |
| `--to` | Target storage backend | - |

## Certificate Management

### trust-cert

Install a trusted certificate:

```bash
mcpproxy trust-cert <certificate-path> [flags]
```

| Flag | Description | Default |
|------|-------------|---------|
| `--force` | Install certificate without confirmation | `false` |
| `--keychain` | Target keychain: 'system' or 'login' | `system` |

**Example:**
```bash
mcpproxy trust-cert /path/to/cert.pem --keychain=system
```
