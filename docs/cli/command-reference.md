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
mcpproxy upstream list
```

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

## Tool Commands

### tools list

List available tools:

```bash
mcpproxy tools list [flags]
```

| Flag | Description |
|------|-------------|
| `--server` | Filter by server name |

### call tool

Execute a tool:

```bash
mcpproxy call tool <server:tool> [--input='{"key":"value"}']
```

## Code Execution

### code exec

Execute JavaScript code:

```bash
mcpproxy code exec --code="({ result: input.value * 2 })" --input='{"value": 21}'
```

See [Code Execution](/features/code-execution) for detailed documentation.

## Authentication

### auth login

Authenticate with an OAuth server:

```bash
mcpproxy auth login --server=<server-name>
```

### auth status

Check authentication status:

```bash
mcpproxy auth status
```

## Secrets Management

### secrets set

Store a secret in the system keyring:

```bash
mcpproxy secrets set <key> <value>
```

### secrets get

Retrieve a secret:

```bash
mcpproxy secrets get <key>
```

### secrets delete

Delete a secret:

```bash
mcpproxy secrets delete <key>
```

## Certificate Management

### trust-cert

Install a trusted certificate:

```bash
mcpproxy trust-cert <certificate-path>
```
