# Server Multi-User Authentication (Spec 024)

Server edition supports OAuth-based multi-user authentication with Google, GitHub, or Microsoft identity providers. All server code is behind `//go:build server`; the personal edition is unaffected.

## Server Configuration

```json
{
  "server_edition": {
    "enabled": true,
    "admin_emails": ["admin@company.com"],
    "oauth": {
      "provider": "google",
      "client_id": "xxx.apps.googleusercontent.com",
      "client_secret": "GOCSPX-xxx",
      "tenant_id": "",
      "allowed_domains": ["company.com"]
    },
    "session_ttl": "24h",
    "bearer_token_ttl": "24h",
    "workspace_idle_timeout": "30m",
    "max_user_servers": 20
  }
}
```

## Server API Endpoints

| Endpoint | Auth | Description |
|----------|------|-------------|
| `GET /api/v1/auth/login` | Public | Initiate OAuth login flow |
| `GET /api/v1/auth/callback` | Public | OAuth callback (creates session) |
| `GET /api/v1/auth/me` | Session/JWT | Get current user profile |
| `POST /api/v1/auth/token` | Session | Generate JWT bearer token for MCP |
| `POST /api/v1/auth/logout` | Session | Invalidate session |
| `GET /api/v1/user/servers` | Session/JWT | List user's servers (personal + shared) |
| `POST /api/v1/user/servers` | Session/JWT | Add personal upstream server |
| `GET /api/v1/user/activity` | Session/JWT | User's activity log |
| `GET /api/v1/user/diagnostics` | Session/JWT | Server health for user's servers |
| `GET /api/v1/admin/users` | Admin | List all users |
| `POST /api/v1/admin/users/{id}/disable` | Admin | Disable a user |
| `GET /api/v1/admin/activity` | Admin | All users' activity logs |
| `GET /api/v1/admin/sessions` | Admin | List active sessions |

## Server Architecture

- **Auth flow**: OAuth 2.0 + PKCE → Session cookie (Web UI) + JWT bearer (MCP/API)
- **Server types**: Shared (config file, single connection) + Personal (DB, per-user connections)
- **Isolation**: Users see only shared + own personal servers. Activity logs user-scoped.
- **Admin**: Identified by `admin_emails` config. Sees all activity, manages users.
- **Build tag**: All server code behind `//go:build server`. Personal edition unaffected.

## Key Directories

| Directory | Purpose |
|-----------|---------|
| `cmd/mcpproxy/edition.go` | Default edition = "personal" |
| `cmd/mcpproxy/edition_teams.go` | Build-tagged override for server edition |
| `cmd/mcpproxy/serveredition_register.go` | Server feature registration entry point |
| `internal/serveredition/auth/` | OAuth, sessions, JWT tokens, middleware |
| `internal/serveredition/users/` | User/session models, BBolt store |
| `internal/serveredition/workspace/` | Per-user workspace for personal upstreams |
| `internal/serveredition/multiuser/` | Multi-user router, tool filtering, activity isolation |
| `internal/serveredition/api/` | Server REST API endpoints (user, admin, auth) |

## Server Testing

```bash
go test -tags server ./internal/serveredition/... -v -race  # All server unit + integration tests
go build -tags server ./cmd/mcpproxy                        # Build server edition
go build ./cmd/mcpproxy                                     # Verify personal edition unaffected
```

> Note: server-edition `//go:build server` routes are invisible to `swag` / `verify-oas-coverage.sh` / CI lint (which don't pass `--build-tags server`). Lint locally with the tag and document endpoints here.
