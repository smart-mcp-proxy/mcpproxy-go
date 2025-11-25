# Shared Management & Health Service Refactor Plan

Goal: converge upstream management and diagnostics so CLI, REST, and MCP tools all call the same code paths, honoring config gates (`disable_management`, `read_only`) and producing consistent outputs.

## Objectives
- Single service layer for upstream lifecycle, logs, and diagnostics.
- REST endpoints, MCP tools, and CLI commands call this layer (no duplicated logic).
- Consistent JSON schemas and CLI outputs; unified gating and auditing.
- Clear extension points for auth status/login and log follow/stream.

## Proposed Service Interfaces (sketch)
Create `internal/management/service.go` (or `internal/runtime/management/service.go`):
```go
type ManagementService interface {
    ListServers(ctx context.Context) ([]*contracts.Server, *contracts.ServerStats, error)
    GetServerLogs(ctx context.Context, name string, tail int) ([]contracts.LogEntry, error)
    Enable(ctx context.Context, name string) error
    Disable(ctx context.Context, name string) error
    Restart(ctx context.Context, name string) error
    RestartAll(ctx context.Context) error
    EnableAll(ctx context.Context) (int, error)   // returns count
    DisableAll(ctx context.Context) (int, error)
    Doctor(ctx context.Context) (*contracts.Diagnostics, error)
    AuthStatus(ctx context.Context, name string) (*contracts.AuthStatus, error)
    AuthLogin(ctx context.Context, name string) error
}
```
Implementation wires into existing upstream manager (`internal/server/manager.go`), log reader, secret resolver, OAuth helpers, and Docker recovery state.

## REST Surface Alignment
- Add/ensure endpoints call the service:
  - `GET /api/v1/servers` → `ListServers`
  - `GET /api/v1/servers/{id}/logs` → `GetServerLogs`
  - `POST /api/v1/servers/{id}/enable|disable|restart` → respective service calls
  - `POST /api/v1/servers/restart_all|enable_all|disable_all` → bulk helpers
  - `GET /api/v1/doctor` → `Doctor`
  - (Optional) `GET /api/v1/servers/{id}/auth` and `POST /api/v1/servers/{id}/login` → `AuthStatus`/`AuthLogin`
- Keep existing contracts structs; add `contracts.Diagnostics` if missing.

## MCP Tool Parity
- `upstream_servers` handler should delegate to the service for list/log/toggle/restart (add a `restart` op).
- Add a `doctor` MCP tool that returns the same diagnostics structure as REST/CLI.
- If auth operations are exposed to MCP, route through the same service and enforce gating.

## CLI Wiring
- `cmd/mcpproxy/upstream_cmd.go`: in daemon mode, continue using `internal/cliclient`, but have `cliclient` hit the new REST endpoints; standalone fallback can call the service directly (optional).
- `cmd/mcpproxy/doctor_cmd.go`: call `Doctor` via REST client; output JSON/pretty using the shared schema.
- `internal/cliclient/client.go`: add methods for `Doctor`, bulk actions, and restart with the new endpoints.

## Gating & Safety
- Centralize `read_only` and `disable_management` checks inside the service; REST/MCP/CLI do not re-implement guards.
- Bulk actions should validate `--all` versus named server inside CLI before hitting the service (already done for flags), and service should still block disallowed operations.

## Data & Types
- Reuse `contracts` types: `Server`, `ServerStats`, `LogEntry`.
- Add `contracts.Diagnostics` with fields already used in doctor command: `total_issues`, `upstream_errors`, `oauth_required`, `missing_secrets`, `runtime_warnings`, `docker_status`.
- Add `contracts.AuthStatus` if not present (fields: `server`, `state`, `expires_at`, `message`).

## File Touch List
- New: `internal/management/service.go` (+ tests).
- Update: `internal/server/manager.go` (expose methods the service uses), `internal/httpapi/server.go` (route endpoints to service), `internal/server/mcp.go` (delegate tool handlers), `internal/cliclient/client.go` (new methods/endpoints), `cmd/mcpproxy/upstream_cmd.go` and `cmd/mcpproxy/doctor_cmd.go` (no logic changes; rely on REST updates).
- Contracts: `internal/contracts/*.go` add diagnostics/auth structs if missing.
- Tests: add unit tests for service, adjust REST/MCP/CLI tests to assert common behavior.

## Step-by-Step Plan
1) Define `ManagementService` interface and concrete implementation backed by existing runtime/manager/logs/diagnostics code.
2) Add/align diagnostics struct in `contracts`; ensure doctor uses it end-to-end.
3) Wire REST handlers to the service (servers, logs, enable/disable/restart/bulk, doctor, auth).
4) Update MCP tool handlers to call the service (`upstream_servers`, new `doctor`, optional auth tool).
5) Extend `internal/cliclient` with new endpoints and update CLI commands to consume them (minimal CLI code churn).
6) Tests: unit tests for service; update HTTP API tests for endpoints; MCP tool tests for new ops; smoke CLI tests for daemon mode to ensure parity.
7) Docs: update `docs/cli-management-commands.md` and `CLAUDE.md` with the surface mapping (CLI ↔ REST ↔ MCP).
