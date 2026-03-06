# Tasks: Agent Tokens

**Input**: Design documents from `/specs/028-agent-tokens/`
**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/

**Tests**: Included per constitution (TDD required).

**Organization**: Tasks are grouped by user story to enable independent implementation and testing.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies)
- **[Story]**: Which user story this task belongs to (e.g., US1, US2, US3)
- Include exact file paths in descriptions

---

## Phase 1: Setup

**Purpose**: Create new packages and project structure for agent tokens feature

- [ ] T001 Create `internal/auth/` package directory and package declaration in `internal/auth/doc.go`
- [ ] T002 [P] Create `internal/auth/agent_token.go` with AgentToken struct, HMAC hashing functions (NewAgentToken, HashToken, ValidateToken), token generation (GenerateToken with `mcp_agt_` prefix and 32-byte crypto/rand), and expiry/revocation checks per `data-model.md`
- [ ] T003 [P] Create `internal/auth/context.go` with AuthContext struct (type, agent_name, token_prefix, allowed_servers, permissions), context key, `WithAuthContext(ctx, authCtx)` setter, `AuthContextFromContext(ctx)` getter, `IsAdmin()`, `CanAccessServer(name)`, `HasPermission(perm)` helper methods
- [ ] T004 [P] Create `internal/auth/agent_token_test.go` with tests for: token generation format validation (`mcp_agt_` prefix, 64 hex chars), HMAC hash/verify round-trip, expiry detection, revocation detection, permission checking (`HasPermission`), server scope checking (`CanAccessServer`), wildcard server scope (`["*"]`)
- [ ] T005 [P] Create `internal/auth/context_test.go` with tests for: context set/get round-trip, nil context returns nil AuthContext, IsAdmin for admin vs agent, CanAccessServer with explicit list and wildcard, HasPermission for each tier combination

**Checkpoint**: Core types and their tests exist. Tests should PASS since they test standalone types.

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: BBolt storage layer and HMAC key management — MUST complete before any user story

- [ ] T006 Create `internal/storage/agent_tokens.go` with BBolt CRUD: `CreateAgentToken(token AgentToken) error`, `GetAgentTokenByName(name string) (*AgentToken, error)`, `GetAgentTokenByHash(hash string) (*AgentToken, error)`, `ListAgentTokens() ([]AgentToken, error)`, `RevokeAgentToken(name string) error`, `RegenerateAgentToken(name string, newHash, newPrefix string) error`, `UpdateLastUsed(name string, t time.Time) error`, `GetTokenCount() (int, error)`. Uses two BBolt buckets: `agent_tokens` (hash → JSON) and `agent_token_names` (name → hash) per `data-model.md`
- [ ] T007 [P] Create `internal/storage/agent_tokens_test.go` with tests for: create and retrieve by name, create and retrieve by hash, duplicate name rejection, list all tokens, revoke token, regenerate token (old hash removed, new hash works), update last used timestamp, token count, max 100 token enforcement
- [ ] T008 Implement HMAC key management in `internal/auth/agent_token.go`: `GetOrCreateHMACKey()` function that tries OS keyring first (`go-keyring` package, service "mcpproxy", key "agent-token-hmac"), falls back to file `~/.mcpproxy/.token_key` with 0600 permissions, generates 32-byte random key on first use per `research.md` Decision 3
- [ ] T009 [P] Add HMAC key tests in `internal/auth/agent_token_test.go`: test key generation, test key persistence (generate once, retrieve same key), test HMAC determinism (same input + key = same hash)

**Checkpoint**: Foundation ready — storage and crypto infrastructure complete. User story implementation can begin.

---

## Phase 3: User Story 1+2 — Create Token & Scope Enforcement (Priority: P1) MVP

**Goal**: Users create scoped agent tokens via CLI. Agents use tokens to access MCP with server and permission scoping enforced.

**Independent Test**: Create a token with `mcpproxy token create`, connect as agent, verify `retrieve_tools` returns only scoped servers, verify `call_tool_read` to out-of-scope server is rejected.

### Tests for User Story 1+2

- [ ] T010 [P] [US1] Create `internal/httpapi/tokens_test.go` with tests for auth middleware agent token path: valid agent token authenticates, expired token rejected (401), revoked token rejected (401), invalid token rejected (401), global API key still works, agent token on admin endpoint rejected (403)
- [ ] T011 [P] [US2] Add scope enforcement tests in `internal/server/mcp_scope_test.go`: retrieve_tools with scoped token returns only allowed servers, call_tool_read to allowed server succeeds, call_tool_read to disallowed server returns 403, call_tool_write with read-only token returns 403, call_tool_destructive with read+write token returns 403, wildcard `["*"]` allows all non-quarantined servers, admin context has no restrictions

### Implementation for User Story 1+2

- [ ] T012 [US1] Extend `internal/httpapi/server.go` auth middleware: in `apiKeyAuthMiddleware()` after the existing API key check (line ~195), add agent token detection — if token starts with `mcp_agt_`, compute HMAC hash, look up in storage, validate expiry/revocation, set `AuthContext` on request context via `auth.WithAuthContext()`. Keep global API key path unchanged.
- [ ] T013 [US1] Add `internal/httpapi/server.go` helper: `extractBearerToken(r *http.Request) string` to support `Authorization: Bearer mcp_agt_...` in addition to existing `X-API-Key` header and `?apikey=` query parameter
- [ ] T014 [US2] Modify `internal/server/mcp.go` `handleRetrieveTools` (line ~767): after `p.index.Search()` returns results, extract `AuthContext` from context, if agent type filter results slice to only include tools where server name is in `AuthContext.AllowedServers` (or skip filter if `AllowedServers` contains `"*"` and server is not quarantined)
- [ ] T015 [US2] Modify `internal/server/mcp.go` `handleCallToolVariant` (line ~1012): after serverName is parsed (line ~1033-1038), extract `AuthContext` from context. If agent type: (1) check `AuthContext.CanAccessServer(serverName)` — reject with "server not in scope" error if false, (2) check `AuthContext.HasPermission(toolVariant)` — reject with "insufficient permissions" error if false. Map tool variants: `call_tool_read` → "read", `call_tool_write` → "write", `call_tool_destructive` → "destructive"
- [ ] T016 [US2] Modify `internal/server/mcp.go` to block administrative MCP tools for agent tokens: in `upstream_servers` handler, check `AuthContext.IsAdmin()` — reject add/remove/update operations with "agent tokens cannot manage servers" error. Allow list operation but filter to allowed servers only.
- [ ] T017 [US1] Create `cmd/mcpproxy/token_cmd.go` with `token` parent command and `token create` subcommand: flags `--name` (required), `--servers` (required, comma-separated), `--permissions` (required, comma-separated), `--expires` (default "30d"). Command connects to running mcpproxy via REST API `POST /api/v1/tokens`, displays token secret once with prominent "save it now" warning.

**Checkpoint**: Core agent tokens work end-to-end. Token creation via CLI + scope enforcement in MCP handlers. This is the MVP.

---

## Phase 4: User Story 3 — REST API Management (Priority: P1)

**Goal**: Full CRUD for agent tokens via REST API per `contracts/agent-tokens-api.yaml`

**Independent Test**: Use curl to create, list, get, revoke, and regenerate tokens via `/api/v1/tokens` endpoints.

### Tests for User Story 3

- [ ] T018 [P] [US3] Add REST API handler tests in `internal/httpapi/tokens_test.go`: POST /api/v1/tokens creates token (201 with secret), POST with duplicate name returns 409, POST with invalid server returns 400, POST with missing fields returns 400, GET /api/v1/tokens returns list without secrets, GET /api/v1/tokens/{name} returns single token info, DELETE /api/v1/tokens/{name} revokes token (204), DELETE non-existent returns 404, POST /api/v1/tokens/{name}/regenerate returns new secret (200), agent token cannot access token management endpoints (403)

### Implementation for User Story 3

- [ ] T019 [US3] Create `internal/httpapi/tokens.go` with handlers: `handleCreateToken` (POST /api/v1/tokens), `handleListTokens` (GET /api/v1/tokens), `handleGetToken` (GET /api/v1/tokens/{name}), `handleRevokeToken` (DELETE /api/v1/tokens/{name}), `handleRegenerateToken` (POST /api/v1/tokens/{name}/regenerate). Validate inputs per `contracts/agent-tokens-api.yaml` schema. Return proper HTTP status codes. Only allow global API key (not agent tokens) via AuthContext check.
- [ ] T020 [US3] Register token routes in `internal/httpapi/server.go` `setupRoutes()`: add token management routes inside the `/api/v1` route group (after activity routes ~line 418). Add middleware to reject agent tokens on these routes.
- [ ] T021 [US3] Add input validation helpers in `internal/httpapi/tokens.go`: validate name format (1-64 chars, `^[a-zA-Z0-9][a-zA-Z0-9_-]*$`), validate server names against current config, validate permissions (must include "read"), validate expiry duration (parse "30d", "720h" etc., max 365 days)
- [ ] T022 [US3] Wire storage layer: inject `storage.Manager` into httpapi.Server (if not already available), call storage CRUD methods from handlers, handle concurrent token creation with BBolt transaction guarantees

**Checkpoint**: Full REST API management works. Tokens can be created, listed, revoked, regenerated via HTTP.

---

## Phase 5: User Story 4 — CLI Management (Priority: P2)

**Goal**: Full token lifecycle management via `mcpproxy token` CLI commands

**Independent Test**: Run `mcpproxy token list`, `mcpproxy token revoke <name>`, `mcpproxy token regenerate <name>` and verify output.

### Tests for User Story 4

- [ ] T023 [P] [US4] Add CLI command tests in `cmd/mcpproxy/token_cmd_test.go`: test `token list` output format (table and JSON), test `token revoke` success and not-found error, test `token regenerate` displays new secret, test `token create` with all flags

### Implementation for User Story 4

- [ ] T024 [US4] Add `token list` subcommand in `cmd/mcpproxy/token_cmd.go`: calls `GET /api/v1/tokens`, displays table with columns Name, Servers, Permissions, Expires, Last Used. Support `-o json` and `-o yaml` via existing `internal/cli/output/` formatters.
- [ ] T025 [P] [US4] Add `token revoke` subcommand in `cmd/mcpproxy/token_cmd.go`: takes token name as argument, calls `DELETE /api/v1/tokens/{name}`, displays confirmation message
- [ ] T026 [P] [US4] Add `token regenerate` subcommand in `cmd/mcpproxy/token_cmd.go`: takes token name as argument, calls `POST /api/v1/tokens/{name}/regenerate`, displays new secret once with "save it now" warning
- [ ] T027 [US4] Register `tokenCmd` in `cmd/mcpproxy/main.go` root command alongside existing upstream, activity, status commands

**Checkpoint**: Full CLI token management works. All 4 commands (create, list, revoke, regenerate) functional.

---

## Phase 6: User Story 5 — Activity Log with Agent Identity (Priority: P2)

**Goal**: Activity log entries include agent identity, filterable by agent name and auth type

**Independent Test**: Make requests with an agent token, run `mcpproxy activity list --agent <name>`, verify entries show agent identity.

### Tests for User Story 5

- [ ] T028 [P] [US5] Add activity logging tests in `internal/runtime/activity_agent_test.go`: test that tool calls with agent token include auth metadata (auth_type, agent_name, token_prefix) in ActivityRecord.Metadata, test that tool calls with global API key include auth_type "admin", test activity filtering by agent name

### Implementation for User Story 5

- [ ] T029 [US5] Modify `internal/server/mcp.go` activity logging calls: in `handleCallToolVariant` and `handleRetrieveTools`, extract AuthContext from context, add `auth_type`, `agent_name`, and `token_prefix` to the metadata map passed to `emitActivityInternalToolCall` and `emitActivityPolicyDecision`
- [ ] T030 [US5] Extend activity list filtering in `internal/httpapi/server.go` `handleListActivity`: add query parameters `agent` (filter by agent_name in metadata) and `auth_type` (filter by auth_type in metadata). Apply filters when querying storage.
- [ ] T031 [US5] Add CLI flags to `cmd/mcpproxy/activity_cmd.go`: add `--agent` and `--auth-type` flags to `activity list` command, pass to REST API as query parameters
- [ ] T032 [US5] Update `internal/storage/agent_tokens.go` `UpdateLastUsed`: call this in the auth middleware after successful agent token validation to track last usage timestamp

**Checkpoint**: Activity log fully tracks agent identity. Filtering works via CLI and REST API.

---

## Phase 7: User Story 6 — Web UI Token Management (Priority: P3)

**Goal**: Web UI tab for managing agent tokens with create dialog, list, and revoke

**Independent Test**: Navigate to Agent Tokens tab in browser, create a token via dialog, verify it appears in the list, revoke it.

### Tests for User Story 6

- [ ] T033 [P] [US6] Add E2E browser test in `tests/e2e/agent-tokens.spec.ts` (or equivalent): navigate to tokens page, verify empty state, create token via dialog, verify token appears in list, verify secret shown in modal, revoke token, verify removed from list

### Implementation for User Story 6

- [ ] T034 [P] [US6] Create `frontend/src/services/tokenApi.ts` with API client functions: `listTokens()`, `createToken(req)`, `revokeToken(name)`, `regenerateToken(name)` calling REST API endpoints from `contracts/agent-tokens-api.yaml`
- [ ] T035 [US6] Create `frontend/src/views/AgentTokens.vue` with: token list table (name, servers as badges, permissions as badges, expiry, last used, revoke button), "Create Token" button, empty state message, auto-refresh via SSE events
- [ ] T036 [US6] Create `frontend/src/components/CreateTokenDialog.vue` with: name input, server checkboxes (loaded from GET /api/v1/servers), permission radio group (read / read+write / all), expiry picker (preset buttons: 7d, 30d, 90d, custom), create button, secret display modal with copy-to-clipboard
- [ ] T037 [US6] Add Agent Tokens route and navigation: register `/tokens` route in Vue Router, add "Agent Tokens" nav item in sidebar/header alongside existing Servers, Activity tabs

**Checkpoint**: Full Web UI token management. Users can create, view, and revoke tokens visually.

---

## Phase 8: Polish & Cross-Cutting Concerns

**Purpose**: Documentation, security hardening, final integration

- [ ] T038 [P] Update `CLAUDE.md` with agent token CLI commands, REST API endpoints, and token format documentation
- [ ] T039 [P] Update `oas/swagger.yaml` with agent token endpoints from `contracts/agent-tokens-api.yaml`
- [ ] T040 Run `./scripts/run-linter.sh` and fix any lint errors across all new files
- [ ] T041 Run `go test -race ./internal/auth/... ./internal/storage/... ./internal/httpapi/...` and fix any race conditions
- [ ] T042 Run `./scripts/test-api-e2e.sh` and verify agent token flows work end-to-end
- [ ] T043 Run quickstart.md validation: follow the quickstart steps manually and verify they work

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: No dependencies — can start immediately
- **Foundational (Phase 2)**: Depends on Phase 1 (T001) — BLOCKS all user stories
- **US1+US2 (Phase 3)**: Depends on Phase 2 — core MVP
- **US3 (Phase 4)**: Depends on Phase 2 — can run in parallel with Phase 3 (different files)
- **US4 (Phase 5)**: Depends on Phase 4 (CLI calls REST API)
- **US5 (Phase 6)**: Depends on Phase 3 (needs auth middleware and MCP scoping)
- **US6 (Phase 7)**: Depends on Phase 4 (frontend calls REST API)
- **Polish (Phase 8)**: Depends on all desired phases being complete

### User Story Dependencies

- **US1+US2 (P1)**: Core — depends only on Foundational
- **US3 (P1)**: REST API — depends only on Foundational, can parallel with US1+US2
- **US4 (P2)**: CLI — depends on US3 (calls REST API endpoints)
- **US5 (P2)**: Activity — depends on US1+US2 (needs AuthContext in MCP handlers)
- **US6 (P3)**: Web UI — depends on US3 (frontend calls REST API)

### Within Each User Story

- Tests MUST be written and FAIL before implementation
- Models before services
- Services before endpoints
- Core implementation before integration

### Parallel Opportunities

- **Phase 1**: T002, T003, T004, T005 all in parallel (different files)
- **Phase 2**: T007, T009 in parallel with T006, T008
- **Phase 3**: T010, T011 in parallel (tests); then T014, T015, T016 partially parallel
- **Phase 4**: T018 (tests) parallel; T019-T022 mostly sequential
- **Phase 5**: T023-T026 partially parallel (different subcommands)
- **Phase 6**: T028, T029-T032 mostly sequential
- **Phase 7**: T033, T034 in parallel; T035-T037 sequential
- **Phase 3 and Phase 4 can run fully in parallel** (different files, no dependencies)

---

## Parallel Example: Phase 1 Setup

```bash
# Launch all setup tasks in parallel (different files):
Task: "Create internal/auth/agent_token.go with AgentToken struct and HMAC functions"
Task: "Create internal/auth/context.go with AuthContext struct and context helpers"
Task: "Create internal/auth/agent_token_test.go with token generation and hashing tests"
Task: "Create internal/auth/context_test.go with context propagation tests"
```

## Parallel Example: Phase 3+4 (MVP)

```bash
# After foundational phase, launch US1+US2 and US3 in parallel:
# Agent 1: Auth middleware + MCP scope enforcement (Phase 3)
# Agent 2: REST API handlers (Phase 4)
```

---

## Implementation Strategy

### MVP First (Phase 1 + 2 + 3)

1. Complete Phase 1: Setup — types and tests
2. Complete Phase 2: Foundational — storage layer
3. Complete Phase 3: US1+US2 — token creation + scope enforcement
4. **STOP and VALIDATE**: Create a token, use it, verify scoping works
5. This is a deployable MVP

### Incremental Delivery

1. Setup + Foundational → Foundation ready
2. US1+US2 → Scoped tokens work in MCP → MVP
3. US3 → REST API management → Programmatic access
4. US4 → CLI management → Full developer workflow
5. US5 → Activity logging → Audit trail
6. US6 → Web UI → Visual management
7. Each phase adds value without breaking previous phases

### Parallel Agent Strategy

With subagent-driven development:

1. Main agent completes Phase 1 + 2
2. Once Foundational is done:
   - **Agent A (worktree)**: Phase 3 (US1+US2 auth middleware + MCP scoping)
   - **Agent B (worktree)**: Phase 4 (US3 REST API handlers)
3. After Phase 3+4 merge:
   - **Agent C (worktree)**: Phase 5 (US4 CLI)
   - **Agent D (worktree)**: Phase 6 (US5 Activity logging)
4. After Phase 5+6 merge:
   - **Agent E (worktree)**: Phase 7 (US6 Web UI)
5. Main agent: Phase 8 (Polish)

---

## Notes

- [P] tasks = different files, no dependencies
- [Story] label maps task to specific user story for traceability
- Each user story is independently completable and testable
- Tests MUST fail before implementing (TDD per constitution)
- Commit after each task or logical group
- Stop at any checkpoint to validate story independently
- Total tasks: 43
- US1+US2: 8 tasks | US3: 5 tasks | US4: 5 tasks | US5: 5 tasks | US6: 5 tasks | Setup: 5 | Foundation: 4 | Polish: 6
