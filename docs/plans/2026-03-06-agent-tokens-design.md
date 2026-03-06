# Agent Tokens — Scoped Access for Autonomous Agents

**Date:** 2026-03-06
**Status:** Design
**Author:** Algis Dumbris
**Depends on:** Nothing (works with current personal mode)
**Blocks:** MCPProxy for Teams (extends this with per-user agent tokens)

---

## 1. Problem Statement

Autonomous AI agents (OpenClaw, Devin, custom agents, CI pipelines) need programmatic access to MCPProxy but cannot perform interactive OAuth. Today, the only option is sharing the single global API key — which gives full access to all upstream servers and all permission tiers.

Users need to create **scoped credentials** for their agents: limited to specific upstream servers, restricted permission tiers, and with automatic expiry.

---

## 2. Design Overview

Users create **agent tokens** — scoped API credentials that provide a subset of MCPProxy's capabilities. Each token is:

- **Server-scoped**: only sees/calls tools from specified upstream servers
- **Permission-scoped**: restricted to specific tool call tiers (read / write / destructive)
- **Time-limited**: automatic expiry, no infinite tokens
- **Auditable**: activity log records which agent token made each request
- **Revocable**: user can revoke any token instantly

### Token Hierarchy

```
MCPProxy Instance
├── Global API Key (full access, admin — current behavior)
│
└── Agent Tokens (scoped access)
    ├── "openClaw-coding"
    │   ├── Servers: [github, filesystem]
    │   ├── Permissions: read + write
    │   └── Expires: 2026-04-05
    │
    ├── "research-bot"
    │   ├── Servers: [brave-search]
    │   ├── Permissions: read only
    │   └── Expires: 2026-03-13
    │
    └── "ci-deploy"
        ├── Servers: [github, sentry, linear]
        ├── Permissions: read + write + destructive
        └── Expires: 2026-06-06
```

### Agent Connection

An autonomous agent connects with just a URL and token:

```bash
# Agent environment / config
MCP_PROXY_URL=http://localhost:8080/mcp
MCP_PROXY_TOKEN=mcp_agt_a8f3c2d1e4b5...
```

The agent authenticates via the same mechanisms as the global API key:
- `Authorization: Bearer mcp_agt_...` header
- `X-API-Key: mcp_agt_...` header

---

## 3. Token Format

```
mcp_agt_<32-bytes-hex>
```

- Prefix `mcp_agt_` makes tokens identifiable (vs global API key)
- 32 bytes (256 bits) of cryptographic randomness via `crypto/rand`
- Stored as bcrypt hash in BBolt (original shown once at creation)

---

## 4. Scoping Behavior

### Server Scoping

When an agent token has `allowed_servers: ["github", "filesystem"]`:

| MCP Tool | Behavior |
|----------|----------|
| `retrieve_tools` | Returns tools only from `github` and `filesystem` servers |
| `call_tool_read("github:list_repos", ...)` | Allowed |
| `call_tool_read("jira:list_issues", ...)` | Rejected: `403 Server not in scope` |
| `upstream_servers` (list) | Returns only `github` and `filesystem` |
| `upstream_servers` (add/remove) | Rejected: agent tokens cannot modify servers |

Special value `allowed_servers: ["*"]` means all current servers (but new quarantined servers are still excluded).

### Permission Scoping

| Permission Config | `call_tool_read` | `call_tool_write` | `call_tool_destructive` |
|-------------------|------------------|-------------------|------------------------|
| `["read"]` | Allowed | Rejected | Rejected |
| `["read", "write"]` | Allowed | Allowed | Rejected |
| `["read", "write", "destructive"]` | Allowed | Allowed | Allowed |

Permission `read` is always included — you cannot create a write-only token.

### Administrative Tool Scoping

Agent tokens cannot:
- Modify upstream servers (`upstream_servers` add/remove/update)
- Manage quarantine (`quarantine_security`)
- Create or revoke other tokens
- Access REST API admin endpoints (`/api/v1/config`, `/api/v1/servers/*/enable`)

Agent tokens can:
- `retrieve_tools` (filtered to allowed servers)
- `call_tool_read/write/destructive` (filtered by server + permission scope)
- `code_execution` (if enabled; tool calls within are also scoped)
- `read_cache` (for paginated results from their own requests)

---

## 5. Management Interfaces

### CLI

```bash
# Create token
mcpproxy token create \
  --name "openClaw-coding" \
  --servers github,filesystem \
  --permissions read,write \
  --expires 30d

# Output:
# Token created: openClaw-coding
# Token: mcp_agt_a8f3c2d1e4b5f6a7... (shown once — save it now)
# Servers: github, filesystem
# Permissions: read, write
# Expires: 2026-04-05T12:00:00Z

# List tokens
mcpproxy token list
# NAME              SERVERS              PERMISSIONS        EXPIRES         LAST USED
# openClaw-coding   github,filesystem    read,write         2026-04-05      2 hours ago
# research-bot      brave-search         read               2026-03-13      never
# ci-deploy         github,sentry        read,write,destr.  2026-06-06      5 min ago

# Revoke token
mcpproxy token revoke openClaw-coding

# Regenerate token (new secret, same config)
mcpproxy token regenerate openClaw-coding
```

### REST API

```
# Create
POST /api/v1/tokens
Authorization: X-API-Key <global-api-key>
{
  "name": "openClaw-coding",
  "allowed_servers": ["github", "filesystem"],
  "permissions": ["read", "write"],
  "expires_in": "720h"
}
→ 201 {
    "name": "openClaw-coding",
    "token": "mcp_agt_a8f3...",
    "allowed_servers": ["github", "filesystem"],
    "permissions": ["read", "write"],
    "expires_at": "2026-04-05T12:00:00Z"
  }

# List (token values never returned)
GET /api/v1/tokens
→ 200 [
    {
      "name": "openClaw-coding",
      "allowed_servers": ["github", "filesystem"],
      "permissions": ["read", "write"],
      "expires_at": "2026-04-05T12:00:00Z",
      "last_used_at": "2026-03-06T10:30:00Z",
      "created_at": "2026-03-06T12:00:00Z"
    }
  ]

# Revoke
DELETE /api/v1/tokens/openClaw-coding
→ 204

# Regenerate
POST /api/v1/tokens/openClaw-coding/regenerate
→ 200 {"token": "mcp_agt_new_value..."}
```

### Web UI

Agent Tokens tab in the dashboard:
- List all tokens with status, servers, last used
- Create token dialog: name, server checkboxes, permission radio, expiry picker
- Revoke button per token
- Token shown once in a copyable modal after creation

---

## 6. Storage

### BBolt Schema

```
config.db
└── agent_tokens/
    └── <token-name>/
        → {
            "name": "openClaw-coding",
            "token_hash": "$2a$10$...",        // bcrypt hash
            "token_prefix": "mcp_agt_a8f3",    // first 12 chars for identification
            "allowed_servers": ["github", "filesystem"],
            "permissions": ["read", "write"],
            "expires_at": "2026-04-05T12:00:00Z",
            "created_at": "2026-03-06T12:00:00Z",
            "last_used_at": "2026-03-06T10:30:00Z",
            "revoked": false
          }
```

### Token Lookup

On each request, MCPProxy needs to identify which token is being used. Since tokens are bcrypt-hashed, we cannot do a simple lookup. Strategy:

1. **Prefix index**: store first 8 bytes of each token as a plaintext prefix
2. On request, extract prefix → find candidate token(s) → bcrypt verify
3. With typical agent token counts (<100), this is fast enough

Alternative: store tokens as HMAC-SHA256 (with a server-side key) instead of bcrypt for O(1) lookup. This is secure as long as the HMAC key is protected (OS keyring). Recommended for performance at scale.

---

## 7. Authentication Flow

```go
func (s *Server) authenticateRequest(r *http.Request) (AuthContext, error) {
    token := extractToken(r) // from Authorization or X-API-Key header

    // 1. Check global API key first
    if token == s.config.APIKey {
        return AuthContext{Type: "admin", Scope: fullScope()}, nil
    }

    // 2. Check agent tokens
    if strings.HasPrefix(token, "mcp_agt_") {
        agentToken, err := s.tokenStore.ValidateAgentToken(token)
        if err != nil {
            return AuthContext{}, err // expired, revoked, or invalid
        }
        return AuthContext{
            Type:           "agent",
            AgentName:      agentToken.Name,
            AllowedServers: agentToken.AllowedServers,
            Permissions:    agentToken.Permissions,
        }, nil
    }

    // 3. No valid auth
    return AuthContext{}, ErrUnauthorized
}
```

---

## 8. Activity Log Integration

All requests made with agent tokens include the agent identity in the activity record:

```json
{
  "id": 12345,
  "type": "tool_call",
  "tool": "github:create_issue",
  "server": "github",
  "auth": {
    "type": "agent_token",
    "agent_name": "openClaw-coding",
    "token_prefix": "mcp_agt_a8f3"
  },
  "timestamp": "2026-03-06T10:30:00Z"
}
```

CLI filtering:
```bash
mcpproxy activity list --agent openClaw-coding
mcpproxy activity list --auth-type agent_token
```

---

## 9. Integration Points

| Layer | File | Change |
|-------|------|--------|
| **Config types** | `internal/config/config.go` | `AgentToken` struct |
| **Token store** | `internal/storage/agent_tokens.go` (new) | CRUD + validation + HMAC lookup |
| **Auth middleware** | `internal/httpapi/server.go` | Extend `apiKeyAuthMiddleware` to check agent tokens |
| **MCP scoping** | `internal/server/mcp.go` | Filter `retrieve_tools` results by allowed servers; reject out-of-scope `call_tool_*` |
| **CLI commands** | `cmd/mcpproxy/token.go` (new) | `token create/list/revoke/regenerate` |
| **REST API** | `internal/httpapi/tokens.go` (new) | Token CRUD endpoints |
| **Activity log** | `internal/runtime/activity_service.go` | Add agent identity to activity records |
| **Web UI** | `frontend/src/views/AgentTokens.vue` (new) | Token management UI |

---

## 10. Security Considerations

| Concern | Mitigation |
|---------|-----------|
| Token leakage | Shown once at creation. Stored as HMAC hash. Prefix for identification only. |
| Scope escalation | Server and permission scopes enforced on every request, not just at creation. |
| Expired token use | Checked on every request. Expiry is mandatory (max 365 days). |
| Brute force | Rate limiting on auth failures. Token entropy (256 bits) makes guessing infeasible. |
| Token in logs | Activity log stores `token_prefix` (first 12 chars), never full token. |
| Revocation lag | Immediate — revocation flag checked on every request, no caching. |

---

## 11. Backward Compatibility

- Global API key continues to work exactly as before
- Agent tokens are an additive feature — no behavior changes for existing users
- MCP endpoint remains unprotected when no auth is configured (personal mode default)
- Agent token auth is checked alongside existing API key auth, not replacing it

---

## 12. Relation to MCPProxy for Teams

Agent tokens are designed to compose with the Teams feature:

| Mode | Agent Token Behavior |
|------|---------------------|
| **Personal** | User creates tokens scoped to their upstream servers |
| **Team** | Each authenticated user creates tokens scoped to their workspace (team + personal servers) |

In Teams mode, agent tokens inherit the creating user's identity — activity logs show both the user and the agent.

---

## 13. Example: OpenClaw Integration

```bash
# 1. Create a scoped token for OpenClaw
mcpproxy token create \
  --name "openclaw-dev" \
  --servers github,filesystem,brave-search \
  --permissions read,write \
  --expires 30d

# 2. Configure OpenClaw
export OPENCLAW_MCP_URL="http://localhost:8080/mcp"
export OPENCLAW_MCP_TOKEN="mcp_agt_a8f3c2d1e4b5..."

# 3. OpenClaw can now:
#    - Search and call tools from github, filesystem, brave-search
#    - Use call_tool_read and call_tool_write
#    - Cannot use call_tool_destructive
#    - Cannot access jira, confluence, or any other upstream server
#    - Cannot modify mcpproxy configuration
```
