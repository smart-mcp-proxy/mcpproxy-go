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
mcpproxy call tool <server:tool> [flags]
```

| Flag | Description | Default |
|------|-------------|---------|
| `--input` | JSON input data for the tool | `{}` |
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
