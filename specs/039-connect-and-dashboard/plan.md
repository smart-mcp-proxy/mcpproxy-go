# Implementation Plan: Connect Clients & Dashboard Visual Redesign

**Spec**: 039-connect-and-dashboard
**Created**: 2026-03-28

## Architecture Decisions

1. **New package `internal/connect/`** for client config manipulation logic
2. **New API handlers in `internal/httpapi/connect.go`** for REST endpoints
3. **New CLI command `cmd/mcpproxy/connect.go`** for CLI interface
4. **Dashboard.vue rewrite** — full replacement of the dashboard component
5. **ConnectModal.vue** — new modal component for the connect UI
6. **No new storage** — all state derived from filesystem checks at request time

## Implementation Tasks

### Task 1: Backend — Connect Package (`internal/connect/`)
**Branch**: Works in `039-connect-and-dashboard` worktree
**Files**:
- `internal/connect/connect.go` — main Connect/Disconnect/Status logic
- `internal/connect/clients.go` — client definitions (paths, formats, entry templates)
- `internal/connect/backup.go` — backup and atomic write utilities
- `internal/connect/connect_test.go` — unit tests

**Implementation**:
1. Define `ClientDef` struct: ID, Name, configPath(), supported, format (json/toml), serverKey
2. Define all 7 clients with their paths per OS
3. `Status()` — check each client config, return whether mcpproxy entry exists
4. `Connect(clientID, serverName, listenURL, force)` — backup + modify + verify
5. `Disconnect(clientID, serverName)` — backup + remove entry + verify
6. Handle JSON (most clients) and TOML (Codex) separately
7. Handle VS Code's `servers` key vs others' `mcpServers` key

**Tests**: Unit tests with temp directories, mock config files

### Task 2: Backend — REST API Endpoints (`internal/httpapi/connect.go`)
**Files**:
- `internal/httpapi/connect.go` — HTTP handlers
- Route registration in `internal/httpapi/server.go`

**Endpoints**:
- `GET /api/v1/connect` — list all clients with status
- `POST /api/v1/connect/{client}` — connect MCPProxy to client
- `DELETE /api/v1/connect/{client}` — disconnect MCPProxy from client

### Task 3: Backend — CLI Command (`cmd/mcpproxy/connect.go`)
**Files**:
- `cmd/mcpproxy/connect.go` — Cobra command

**Commands**:
- `mcpproxy connect <client>` — connect to specific client
- `mcpproxy connect --list` — show status table
- `mcpproxy connect --all` — connect all supported
- `mcpproxy disconnect <client>` — remove from client

### Task 4: Frontend — Dashboard Visual Redesign
**Files**:
- `frontend/src/views/Dashboard.vue` — complete rewrite
- `frontend/src/components/ConnectModal.vue` — new connect UI modal
- `frontend/src/services/api.ts` — add connect API methods

**Layout**: Three-column hub visualization per spec wireframe

### Task 5: Integration Testing & Verification
- Build backend, run go tests
- Build frontend, verify in browser
- Test connect API with curl
- Visual verification with mcpproxy-ui-test

## Execution Strategy

Tasks 1-3 (backend) are sequential (each builds on prior).
Task 4 (frontend) can run in parallel with Tasks 1-3 after API contract is defined.
Task 5 runs after all others complete.

**Worktree plan**: Single feature worktree `039-connect-and-dashboard` since frontend depends on backend API.
