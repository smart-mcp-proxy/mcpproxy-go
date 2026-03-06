---
id: agent-tokens
title: Agent Tokens
sidebar_label: Agent Tokens
sidebar_position: 10
description: Scoped API credentials for AI agents with server and permission restrictions
keywords: [agent, tokens, authentication, security, scoping, permissions, mcp]
---

# Agent Tokens

Agent tokens provide **scoped, revocable credentials** for AI agents connecting to MCPProxy. Instead of sharing the admin API key with every agent, each agent gets its own token with restricted access to specific servers and permission tiers.

## Why Agent Tokens?

MCPProxy sits between AI agents and upstream MCP servers. Without agent tokens, every connection gets full admin access — any agent can call any tool on any server with no restrictions.

This creates real problems:

- **A CI/CD bot** that only needs to read GitHub issues can also delete repositories
- **A monitoring agent** that checks server status can also modify configurations
- **A compromised agent** has unlimited access to all upstream servers
- **No audit trail** — you can't tell which agent performed which action

Agent tokens solve this with **defense-in-depth scoping**:

```
┌─────────────────────────────────────────┐
│  AI Agent (e.g., deploy-bot)            │
│  Token: mcp_agt_a1b2c3...              │
│  Servers: github, gitlab                │
│  Permissions: read, write               │
└──────────────┬──────────────────────────┘
               │
               ▼
┌─────────────────────────────────────────┐
│  MCPProxy                               │
│                                         │
│  1. retrieve_tools → filters results    │
│     to github + gitlab only             │
│                                         │
│  2. call_tool_write → allowed           │
│  3. call_tool_destructive → BLOCKED     │
│  4. call_tool_read(slack:...) → BLOCKED │
└─────────────────────────────────────────┘
```

## Token Format

Agent tokens use the `mcp_agt_` prefix followed by 64 hex characters:

```
mcp_agt_a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2
```

Tokens are hashed with HMAC-SHA256 before storage — the raw token is shown once at creation and cannot be retrieved again.

## Quick Start

### Create a Token

```bash
mcpproxy token create \
  --name deploy-bot \
  --servers github,gitlab \
  --permissions read,write \
  --expires 30d
```

Output:
```
Agent token created successfully.

  Token: mcp_agt_a1b2c3d4...

  IMPORTANT: Save this token now. It cannot be retrieved again.

  Name:        deploy-bot
  Servers:     github, gitlab
  Permissions: read, write
  Expires:     2026-04-05 14:30
```

### Use the Token

Agents authenticate by passing the token via any standard method:

```bash
# X-API-Key header
curl -H "X-API-Key: mcp_agt_a1b2c3d4..." http://localhost:8080/mcp

# Authorization: Bearer header
curl -H "Authorization: Bearer mcp_agt_a1b2c3d4..." http://localhost:8080/mcp

# Query parameter
curl "http://localhost:8080/mcp?apikey=mcp_agt_a1b2c3d4..."
```

In MCP client configurations:
```json
{
  "mcpServers": {
    "mcpproxy": {
      "url": "http://localhost:8080/mcp",
      "headers": {
        "X-API-Key": "mcp_agt_a1b2c3d4..."
      }
    }
  }
}
```

## Enforcing Authentication on /mcp

By default, the `/mcp` endpoint allows unauthenticated access for backward compatibility with existing MCP clients. This means agent tokens are **optional** — agents that don't provide a token get full admin access.

To make agent tokens **mandatory**, enable `require_mcp_auth`:

```json
{
  "require_mcp_auth": true
}
```

Or via CLI flag:

```bash
mcpproxy serve --require-mcp-auth
```

With this enabled:
- Requests without a token → **401 Unauthorized**
- Requests with an invalid token → **401 Unauthorized**
- Requests with a valid agent token → scoped access
- Requests with the admin API key → full admin access
- Tray/socket connections → always trusted (OS-level auth)

**Recommended setup:** Enable `require_mcp_auth` when deploying MCPProxy in environments where multiple agents connect, or when you want to enforce least-privilege access.

## Permission Tiers

Each token specifies which permission tiers the agent can use:

| Permission | Tool Variants Allowed | Use Case |
|------------|----------------------|----------|
| `read` | `call_tool_read` | Monitoring, querying, status checks |
| `write` | `call_tool_read`, `call_tool_write` | Creating issues, updating records |
| `destructive` | All variants | Deleting resources, admin operations |

Permissions are **cumulative** — `write` implies `read`, and `destructive` implies both. The `read` permission is always required.

```bash
# Read-only monitoring agent
mcpproxy token create --name monitor --servers "*" --permissions read

# CI/CD agent that creates and updates
mcpproxy token create --name ci-agent --servers github --permissions read,write

# Full-access admin agent
mcpproxy token create --name admin-bot --servers "*" --permissions read,write,destructive
```

## Server Scoping

Tokens restrict which upstream servers an agent can access:

```bash
# Only GitHub and GitLab
mcpproxy token create --name deploy-bot --servers github,gitlab --permissions read,write

# All servers (wildcard)
mcpproxy token create --name all-access --servers "*" --permissions read
```

Server scoping is enforced at two levels:
1. **Tool discovery** (`retrieve_tools`) — only returns tools from allowed servers
2. **Tool execution** (`call_tool_*`) — blocks calls to out-of-scope servers

## Managing Tokens

### List All Tokens

```bash
mcpproxy token list
```

```
NAME                 PREFIX         SERVERS                   PERMISSIONS          REVOKED  EXPIRES
deploy-bot           mcp_agt_a1b2   github,gitlab             read,write           no       2026-04-05 14:30
monitor              mcp_agt_c3d4   *                         read                 no       2026-04-05 14:30
old-bot              mcp_agt_e5f6   github                    read                 yes      2026-03-01 10:00
```

### Show Token Details

```bash
mcpproxy token show deploy-bot
```

### Revoke a Token

Immediately invalidates the token:

```bash
mcpproxy token revoke deploy-bot
```

### Regenerate a Token

Invalidates the old secret and generates a new one, keeping the same name and settings:

```bash
mcpproxy token regenerate deploy-bot
```

The new token is displayed once — save it immediately.

### JSON Output

All commands support JSON output for scripting:

```bash
mcpproxy token list -o json
mcpproxy token create --name bot --servers github --permissions read -o json
```

## Activity Logging

Agent token usage is tracked in the activity log. Each tool call records the agent identity:

```bash
# Filter activity by agent
mcpproxy activity list --agent deploy-bot

# Filter by auth type
mcpproxy activity list --auth-type agent
mcpproxy activity list --auth-type admin
```

Activity records include `_auth_type`, `_auth_agent`, and `_auth_token_prefix` metadata fields for audit trails.

## REST API

Agent tokens can also be managed via the REST API (requires admin API key):

| Method | Endpoint | Description |
|--------|----------|-------------|
| `POST` | `/api/v1/tokens` | Create a new agent token |
| `GET` | `/api/v1/tokens` | List all tokens |
| `GET` | `/api/v1/tokens/{name}` | Get token details |
| `DELETE` | `/api/v1/tokens/{name}` | Revoke a token |
| `POST` | `/api/v1/tokens/{name}/regenerate` | Regenerate token secret |

### Create Token via API

```bash
curl -X POST http://localhost:8080/api/v1/tokens \
  -H "X-API-Key: your-admin-key" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "deploy-bot",
    "allowed_servers": ["github", "gitlab"],
    "permissions": ["read", "write"],
    "expires_in": "30d"
  }'
```

## Security Model

- **HMAC-SHA256 hashing** — raw tokens are never stored; only HMAC hashes are persisted
- **Constant-time comparison** — prevents timing attacks during token validation
- **Automatic expiry** — tokens expire after a configurable duration (default: 30 days)
- **Revocation** — tokens can be immediately invalidated
- **Prefix identification** — the `mcp_agt_` prefix distinguishes agent tokens from admin API keys without database lookups
- **Tray bypass** — local tray/socket connections always get admin access (authenticated by OS-level socket permissions)

## Configuration Reference

### Config File

```json
{
  "require_mcp_auth": false,
  "api_key": "your-admin-key"
}
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `require_mcp_auth` | bool | `false` | Require authentication on `/mcp` endpoint |
| `api_key` | string | auto-generated | Admin API key for full access |

### CLI Flags

```bash
mcpproxy serve --require-mcp-auth    # Enforce /mcp authentication
```

### Token Create Flags

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--name` | Yes | — | Unique token name |
| `--servers` | Yes | — | Comma-separated server names or `"*"` |
| `--permissions` | Yes | — | Comma-separated: `read`, `write`, `destructive` |
| `--expires` | No | `30d` | Expiry duration (e.g., `7d`, `90d`, `365d`) |
