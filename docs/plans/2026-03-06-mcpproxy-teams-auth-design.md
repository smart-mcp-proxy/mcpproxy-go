# MCPProxy for Teams — Inbound OAuth + Per-User Workspaces

**Date:** 2026-03-06
**Status:** Design
**Author:** Algis Dumbris
**Depends on:** Agent Tokens (2026-03-06-agent-tokens-design.md)

---

## 1. Problem Statement

MCPProxy is deployed at Gcore as a shared team MCP proxy (`mcp.i.gc.onl`). On launch day (2026-03-05), Jira/Confluence access was pulled because:

- A shared service account token gives all users (including external partners) access to restricted Jira pages
- No per-user authentication exists — single API key, single identity
- Security team requires formal OIDC auth for the integration

MCPProxy needs to identify WHO is making each request and use THEIR credentials for upstream services where per-user ACLs matter.

---

## 2. Product Modes

MCPProxy operates in one of two modes, selected by config:

| Mode | Activation | Behavior |
|------|-----------|----------|
| **`personal`** (default) | `"mode": "personal"` or omitted | Current behavior. Single API key. All upstream servers shared. Agent tokens supported. |
| **`team`** | `"mode": "team"` + `auth` block | Multi-user OAuth. `/mcp` requires Bearer token. Per-user workspaces. Per-user agent tokens. |

When `mode` is `personal`, none of the team code paths execute. Backward compatibility is absolute.

---

## 3. Architecture: MCPProxy as OAuth Authorization Server

MCPProxy becomes a **lightweight OAuth 2.1 Authorization Server** that delegates authentication to external Identity Providers (Google, GitHub, Microsoft Entra ID, generic OIDC). This is MCP-spec compliant — the MCP client discovers MCPProxy's auth server via RFC 9728.

### Auth Flow

```
MCP Client                    MCPProxy (AuthZ Server)            IdP (Google/GitHub/Entra)
    │                                │                                    │
    │ GET /.well-known/              │                                    │
    │  oauth-protected-resource      │                                    │
    │───────────────────────────────→│                                    │
    │  {authorization_servers:       │                                    │
    │   ["https://mcp.i.gc.onl"]}   │                                    │
    │←───────────────────────────────│                                    │
    │                                │                                    │
    │ GET /oauth/authorize           │                                    │
    │   ?response_type=code          │                                    │
    │   &client_id=...               │                                    │
    │   &code_challenge=...          │                                    │
    │───────────────────────────────→│                                    │
    │                                │                                    │
    │                                │ 302 → IdP login page               │
    │                                │───────────────────────────────────→│
    │                                │                  (MFA happens here)│
    │                                │                                    │
    │                                │ callback: /oauth/idp-callback      │
    │                                │   ?code=idp_auth_code              │
    │                                │←───────────────────────────────────│
    │                                │                                    │
    │                                │ POST /token (exchange with IdP)    │
    │                                │───────────────────────────────────→│
    │                                │ { id_token, access_token }         │
    │                                │←───────────────────────────────────│
    │                                │                                    │
    │                                │ Extract user identity              │
    │                                │ Check allowed_emails / domains     │
    │                                │ Issue MCPProxy auth code           │
    │                                │                                    │
    │ callback: redirect_uri         │                                    │
    │   ?code=mcpproxy_auth_code     │                                    │
    │←───────────────────────────────│                                    │
    │                                │                                    │
    │ POST /oauth/token              │                                    │
    │   grant_type=authorization_code│                                    │
    │   &code=mcpproxy_auth_code     │                                    │
    │   &code_verifier=...           │                                    │
    │───────────────────────────────→│                                    │
    │                                │                                    │
    │ { access_token, refresh_token }│                                    │
    │←───────────────────────────────│                                    │
    │                                │                                    │
    │ /mcp                           │                                    │
    │   Authorization: Bearer <token>│                                    │
    │───────────────────────────────→│                                    │
    │                                │ Validate token                     │
    │                                │ Load user workspace                │
    │                                │ Route to user's upstreams          │
```

### Key Design Decisions

1. **MCPProxy issues its own tokens** — never passes IdP tokens to MCP clients
2. **MFA is transparent** — handled entirely by the IdP during browser-based login
3. **Provider abstraction** — Google, GitHub, Microsoft, generic OIDC are interchangeable backends
4. **RFC 9728 compliant** — MCP clients auto-discover auth via `/.well-known/oauth-protected-resource`

---

## 4. Identity Providers

### Built-in Providers

| Provider | Use Case | MFA Support |
|----------|----------|-------------|
| **Google** | Families, small teams | Google Workspace enforced |
| **GitHub** | Dev teams, open source | Org-level 2FA policy |
| **Microsoft** | Enterprise with Office 365 | Entra ID Conditional Access |
| **Generic OIDC** | Keycloak, Okta, Auth0 | Depends on IdP config |

### Provider Selection Guide

| Scenario | Provider | Why |
|----------|---------|-----|
| Family / friends sharing tools | **Google** | Everyone has Google. Free. |
| Small dev team / startup | **GitHub** | Developers already have accounts. Org-based access. |
| Enterprise with Microsoft 365 + MFA | **Microsoft** | Existing Entra ID tenant. Conditional Access MFA. |
| Enterprise with Keycloak/Okta (LDAP-backed) | **Generic OIDC** | Federates with AD/LDAP behind the IdP. |

---

## 5. Configuration

### Google (simplest — 5 minute setup)

```json
{
  "mode": "team",
  "auth": {
    "provider": "google",
    "client_id": "xxx.apps.googleusercontent.com",
    "client_secret": "GOCSPX-xxx",
    "admin_emails": ["dad@gmail.com"],
    "allowed_emails": ["mom@gmail.com", "kid@gmail.com"]
  }
}
```

### GitHub (org-based)

```json
{
  "mode": "team",
  "auth": {
    "provider": "github",
    "client_id": "Iv1.xxx",
    "client_secret": "xxx",
    "admin_emails": ["lead@company.com"],
    "allowed_org": "my-startup"
  }
}
```

### Microsoft Entra ID (enterprise with MFA)

```json
{
  "mode": "team",
  "auth": {
    "provider": "microsoft",
    "client_id": "app-uuid",
    "tenant_id": "tenant-uuid",
    "admin_emails": ["algis@gcore.com"],
    "allowed_domains": ["gcore.com"]
  }
}
```

MFA is enforced by Entra ID Conditional Access policies. MCPProxy does not need to know whether MFA is required — it validates the JWT that Entra issues after successful authentication (including MFA satisfaction).

### Generic OIDC (Keycloak, Okta, Auth0)

```json
{
  "mode": "team",
  "auth": {
    "provider": "oidc",
    "issuer_url": "https://keycloak.internal/realms/gcore",
    "client_id": "mcpproxy",
    "client_secret": "xxx",
    "admin_claim": "realm_access.roles",
    "admin_value": "mcp-admin",
    "allowed_domains": ["gcore.com"]
  }
}
```

### Config Schema

```go
type AuthConfig struct {
    Provider       string   `json:"provider"`                  // "google", "github", "microsoft", "oidc"
    ClientID       string   `json:"client_id"`
    ClientSecret   string   `json:"client_secret,omitempty"`   // or env: MCPPROXY_AUTH_CLIENT_SECRET
    TenantID       string   `json:"tenant_id,omitempty"`       // Microsoft only
    IssuerURL      string   `json:"issuer_url,omitempty"`      // Generic OIDC only

    // Access control
    AdminEmails    []string `json:"admin_emails,omitempty"`    // Email-based admin detection
    AdminClaim     string   `json:"admin_claim,omitempty"`     // JWT claim path for role-based admin
    AdminValue     string   `json:"admin_value,omitempty"`     // Value of admin claim

    AllowedEmails  []string `json:"allowed_emails,omitempty"`  // Explicit allowlist
    AllowedDomains []string `json:"allowed_domains,omitempty"` // Domain-based access
    AllowedOrg     string   `json:"allowed_org,omitempty"`     // GitHub org membership

    // Token settings
    TokenTTL       string   `json:"token_ttl,omitempty"`       // Default: "1h"
    RefreshTTL     string   `json:"refresh_ttl,omitempty"`     // Default: "7d"
}
```

---

## 6. Admin Model

Admin determination priority chain:

1. **Email match**: `admin_emails: ["algis@gmail.com"]` — works with any provider
2. **IdP claim match**: `admin_claim` + `admin_value` — for Keycloak/Entra role mapping
3. **Default**: non-admin user

### Permissions

| Capability | Admin | User |
|-----------|-------|------|
| Use team servers | Yes | Yes |
| Add personal upstream servers | Yes | Yes |
| Configure personal server auth | Yes | Yes |
| Create agent tokens (own) | Yes | Yes |
| Manage team-wide servers | Yes | No |
| Manage server templates | Yes | No |
| View all users' activity | Yes | No |
| View own activity | Yes | Yes |

---

## 7. Per-User Workspaces

### Storage: BBolt Per-User Buckets

```
config.db (BBolt)
├── users/
│   ├── alice@gmail.com/
│   │   ├── profile          → {email, name, provider, first_seen, last_seen, role}
│   │   ├── servers          → [{name, url, protocol, auth_type, ...}]
│   │   ├── tokens           → [{service, encrypted_token, created_at}]
│   │   └── agent_tokens     → [{name, hash, servers, permissions, expires}]
│   └── bob@gmail.com/
│       └── ...
├── team/
│   ├── servers              → admin-managed upstream servers
│   └── templates            → server setup templates
└── oauth/
    ├── auth_codes           → pending authorization codes
    ├── access_tokens        → issued access tokens (MCPProxy tokens)
    └── refresh_tokens       → issued refresh tokens
```

### Server Resolution

When a user makes an MCP request:

```
effective_servers = team_servers ∪ user_personal_servers
```

Personal servers override team servers with the same name.

### Per-User Agent Tokens

In team mode, each user creates their own agent tokens:

```
User alice@gcore.com (OAuth login)
├── Agent Token: "alice-openClaw"
│   ├── Servers: [github, filesystem] (subset of alice's workspace)
│   └── Permissions: read + write
│
User bob@gcore.com (OAuth login)
├── Agent Token: "bob-research"
│   ├── Servers: [brave-search]
│   └── Permissions: read
```

Agent tokens inherit the creating user's workspace and upstream credentials. Activity logs show both user and agent identity.

### Token Encryption

Per-user upstream tokens (Jira PATs, etc.) encrypted at rest:
- AES-256-GCM encryption
- Master key from OS keyring
- Per-user salt

---

## 8. Server Templates

### Built-in Templates (shipped in binary)

| Template | Auth Type | URL Pattern |
|----------|-----------|-------------|
| Atlassian Jira | PAT | `https://{domain}.atlassian.net/mcp` |
| Atlassian Confluence | PAT | `https://{domain}.atlassian.net/mcp` |
| GitHub | OAuth | `https://api.github.com/mcp` |
| GitLab | PAT | `https://{domain}/mcp` |
| Sentry | API Token | `https://sentry.io/api/mcp/` |
| Linear | API Key | `https://api.linear.app/mcp` |
| Notion | Integration Token | `https://api.notion.com/mcp` |
| Slack | Bot Token | MCP server (stdio) |
| PostgreSQL | Connection String | MCP server (stdio) |
| Filesystem | Path | MCP server (stdio) |

### Template Schema

```json
{
  "id": "atlassian-jira",
  "display_name": "Jira",
  "description": "Atlassian Jira project management",
  "url_template": "https://{domain}.atlassian.net/mcp",
  "protocol": "http",
  "auth_type": "pat",
  "auth_header_template": "Authorization: Bearer {token}",
  "setup_url": "https://id.atlassian.com/manage-profile/security/api-tokens",
  "variables": [
    {"name": "domain", "label": "Atlassian Domain", "placeholder": "your-company"}
  ]
}
```

Admins can add custom templates in config.

---

## 9. OAuth Authorization Server Endpoints

MCPProxy exposes in `team` mode:

| Endpoint | Method | Purpose |
|----------|--------|---------|
| `/.well-known/oauth-protected-resource` | GET | RFC 9728 resource metadata |
| `/.well-known/oauth-authorization-server` | GET | RFC 8414 auth server metadata |
| `/oauth/authorize` | GET | Authorization endpoint (redirects to IdP) |
| `/oauth/idp-callback` | GET | IdP callback |
| `/oauth/token` | POST | Token endpoint |
| `/oauth/register` | POST | Dynamic client registration (RFC 7591) |

### Token Format

MCPProxy issues opaque tokens (not JWTs) stored in BBolt:

```
Access token:  mcp_at_<32-byte-hex>   (TTL: 1 hour)
Refresh token: mcp_rt_<32-byte-hex>   (TTL: 7 days)
Agent token:   mcp_agt_<32-byte-hex>  (TTL: user-defined, max 365 days)
```

---

## 10. Authentication Resolution

```
Incoming request → extract token
    │
    ├── starts with "mcp_agt_" → Agent token lookup
    │   └── Resolve to user + scope
    │
    ├── starts with "mcp_at_" → MCPProxy access token (team mode OAuth)
    │   └── Resolve to user + full workspace
    │
    ├── matches global API key → Admin (personal mode / team admin)
    │   └── Full access
    │
    └── no match → 401 Unauthorized
```

---

## 11. LDAP Considerations

Direct LDAP is not implemented in MCPProxy. LDAP is supported via IdP federation:

- **Keycloak** federates with AD/LDAP via User Storage SPI
- **Microsoft Entra ID** syncs with on-prem AD via Entra Connect
- **Okta** supports AD agent integration

This keeps MCPProxy's auth simple (only OIDC) while supporting enterprises that use AD/LDAP. If direct LDAP becomes a hard requirement (air-gapped environments), it can be added as a fifth provider type in a future iteration.

---

## 12. Microsoft Entra ID + MFA Details

For Gcore's Microsoft 365 deployment:

### How MFA Works (Transparent to MCPProxy)

1. MCP client initiates OAuth with MCPProxy
2. MCPProxy redirects to Entra ID login
3. User authenticates with email/password
4. Entra ID Conditional Access evaluates: MFA required?
5. If yes: Entra ID prompts for MFA (authenticator app, SMS, etc.)
6. After MFA: Entra ID issues tokens back to MCPProxy
7. MCPProxy extracts identity, issues its own token

MCPProxy never handles MFA directly — it's entirely within Entra ID's browser-based flow.

### Entra ID App Registration

1. Register in [Entra admin center](https://entra.microsoft.com) → App registrations
2. Platform: Web
3. Redirect URI: `https://mcp.i.gc.onl/oauth/idp-callback`
4. API permissions: `openid`, `profile`, `email`
5. Supported account types: "Accounts in this organizational directory only"

### Conditional Access MFA Policy (Admin configures in Entra)

- Target: All users or specific groups
- Conditions: Any cloud app or specifically the MCPProxy app
- Grant: Require multifactor authentication
- MCPProxy config does not reference MFA — it's purely an IdP-side policy

---

## 13. Testing Strategy

### Unit Tests

- Mock OIDC provider (in-process, serves JWKS and issues test tokens)
- Each provider adapter tested independently
- Token issuance, validation, expiry, refresh
- User workspace resolution
- Admin role detection from different claim formats

### Integration Tests with Dex

```yaml
# docker-compose.test.yml
services:
  dex:
    image: dexidp/dex:v2.41.1
    ports: ["5556:5556"]
    volumes: ["./test/dex-config.yaml:/etc/dex/config.yaml"]
```

```yaml
# test/dex-config.yaml
issuer: http://localhost:5556/dex
storage:
  type: memory
staticClients:
  - id: mcpproxy-test
    secret: test-secret
    name: MCPProxy Test
    redirectURIs: ["http://localhost:8080/oauth/idp-callback"]
staticPasswords:
  - email: admin@test.local
    hash: "$2a$10$2b2cU8CPhOTaGrs1HRQuAueS7JTT5ZHsHSzYiFPm1leZck7Mc8T4W"
    username: admin
  - email: user@test.local
    hash: "$2a$10$2b2cU8CPhOTaGrs1HRQuAueS7JTT5ZHsHSzYiFPm1leZck7Mc8T4W"
    username: user
enablePasswordDB: true
```

### E2E Tests (Playwright)

- Full OAuth flow: IdP login → MCPProxy token → MCP request
- Admin vs user permissions
- Personal server management via Web UI
- Agent token creation + scoped access verification

### Manual Testing with Google

Quick validation using the Google quick-start recipe.

---

## 14. Quick-Start Recipes

### 5-Minute Setup with Google

1. [Google Cloud Console](https://console.cloud.google.com) → APIs & Services → Credentials → Create OAuth 2.0 Client ID
2. Authorized redirect URI: `http://localhost:8080/oauth/idp-callback`
3. Add to `~/.mcpproxy/mcp_config.json`:
   ```json
   {
     "mode": "team",
     "auth": {
       "provider": "google",
       "client_id": "YOUR_CLIENT_ID",
       "client_secret": "YOUR_SECRET",
       "admin_emails": ["you@gmail.com"]
     }
   }
   ```
4. `mcpproxy serve` — share the URL with your team.

### GitHub Team (Org-Based)

1. [GitHub Developer Settings](https://github.com/settings/developers) → New OAuth App
2. Callback URL: `http://YOUR_HOST:8080/oauth/idp-callback`
3. Configure with `"provider": "github"` and `"allowed_org": "your-org"`

### Microsoft Entra ID (Enterprise + MFA)

1. [Entra admin center](https://entra.microsoft.com) → App registrations → New
2. Redirect URI: `https://mcp.i.gc.onl/oauth/idp-callback`
3. API permissions: `openid`, `profile`, `email`
4. Configure with `"provider": "microsoft"`, `"tenant_id"`, `"allowed_domains": ["gcore.com"]`
5. MFA enforced via Entra Conditional Access (no MCPProxy config needed)

---

## 15. Integration Points

| Layer | File | Change |
|-------|------|--------|
| **Config** | `internal/config/config.go` | `Mode`, `AuthConfig` structs |
| **OAuth AS** | `internal/auth/` (new) | Authorization server, token issuance |
| **Providers** | `internal/auth/providers/` (new) | Google, GitHub, Microsoft, OIDC |
| **Auth middleware** | `internal/httpapi/server.go` | Token validation for team mode |
| **MCP auth** | `internal/server/server.go` | Token validation on `/mcp`; `/.well-known/*` endpoints |
| **User storage** | `internal/storage/users.go` (new) | Per-user bucket CRUD |
| **Workspace** | `internal/workspace/` (new) | Server resolution, template engine |
| **Web UI** | `frontend/` | Login, personal servers, templates, agent tokens |
| **REST API** | `internal/httpapi/` | User profile, personal server CRUD, token vault |

---

## 16. Security Considerations

| Concern | Mitigation |
|---------|-----------|
| Token theft | Short TTL (1h), refresh bound to client, revocation on logout |
| PKCE bypass | `S256` required, plain rejected |
| CSRF | State parameter + nonce validation |
| Upstream token exposure | AES-256-GCM encryption, OS keyring master key, per-user salt |
| Open redirect | Redirect URIs validated against registered client URIs |
| Admin cannot read user tokens | Per-user encryption keys |
| Privilege escalation | Role checked on every request from IdP claims |

---

## 17. Competitive Positioning

With Agent Tokens + Teams, MCPProxy works at all scales:

| Scale | Feature | Competitors |
|-------|---------|------------|
| **Personal** | API key + agent tokens | Docker MCP GW |
| **Team** | Google/GitHub login, per-user workspaces, agent tokens | None at this simplicity |
| **Enterprise** | OIDC, IdP-driven RBAC, per-user vaults, MFA | Kong, IBM ContextForge |

---

## 18. Implementation Order

1. **Agent Tokens** (personal mode) — no dependencies, high value
2. **OAuth Authorization Server** — core team mode infrastructure
3. **Provider adapters** — Google first (simplest), then GitHub, Microsoft, generic OIDC
4. **Per-user workspaces** — storage, server resolution, template engine
5. **Web UI** — login page, workspace management, agent token UI
6. **Entra ID + MFA** — Microsoft provider with Conditional Access support

---

## 19. Out of Scope (Future)

- Direct LDAP provider
- OPA/Cedar policy engine
- Multi-instance HA (shared external DB)
- SIEM export
- Kubernetes Helm chart
- Per-tool RBAC
