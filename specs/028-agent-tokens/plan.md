# Implementation Plan: Agent Tokens

**Branch**: `028-agent-tokens` | **Date**: 2026-03-06 | **Spec**: [spec.md](spec.md)
**Input**: Feature specification from `/specs/028-agent-tokens/spec.md`

## Summary

Add scoped agent tokens to MCPProxy that allow autonomous AI agents to access the MCP proxy with restricted server access, permission tiers (read/write/destructive), and automatic expiry. Tokens are managed via CLI, REST API, and Web UI. Activity logs include agent identity. The global API key continues to work unchanged.

## Technical Context

**Language/Version**: Go 1.24 (toolchain go1.24.10)
**Primary Dependencies**: Cobra (CLI), Chi router (HTTP), BBolt (storage), Zap (logging), mcp-go (MCP protocol), crypto/hmac + crypto/sha256 (token hashing)
**Storage**: BBolt database (`~/.mcpproxy/config.db`) — new `agent_tokens` bucket
**Testing**: `go test` (unit), `./scripts/test-api-e2e.sh` (E2E), `./scripts/run-all-tests.sh` (full)
**Target Platform**: macOS, Linux, Windows (desktop)
**Project Type**: Web application (Go backend + Vue 3 frontend)
**Performance Goals**: Token validation <5ms per request, <100 agent tokens per instance
**Constraints**: Zero breaking changes to existing API key auth, backward compatible
**Scale/Scope**: <100 tokens per instance, 14 new files, ~2000 LOC

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Status | Notes |
|-----------|--------|-------|
| I. Performance at Scale | PASS | Token validation uses HMAC-SHA256 with prefix indexing — O(1) lookup. <5ms overhead. |
| II. Actor-Based Concurrency | PASS | Token store uses BBolt transactions (serialized by BBolt). No custom mutexes needed. |
| III. Configuration-Driven | PASS | No new config fields needed — tokens are runtime data stored in BBolt, not config. |
| IV. Security by Default | PASS | Tokens are hashed (HMAC-SHA256), shown once, scoped by server + permission, mandatory expiry. Agent tokens cannot modify servers or manage quarantine. |
| V. TDD | PASS | All components will have unit tests written before implementation. E2E tests for CLI and API. |
| VI. Documentation Hygiene | PASS | CLAUDE.md updated with token commands, REST API docs updated, Web UI docs updated. |

**Architecture Constraints**:
| Constraint | Status | Notes |
|-----------|--------|-------|
| Core + Tray Split | PASS | Agent tokens are core-only. Tray reads via REST API. |
| Event-Driven Updates | PASS | Token CRUD emits events for SSE subscribers. |
| DDD Layering | PASS | Storage layer (BBolt), domain logic (auth context), presentation (REST/CLI/Web). |
| Upstream Client Modularity | PASS | No changes to upstream client layers. Scoping applied at MCP handler level. |

## Project Structure

### Documentation (this feature)

```text
specs/028-agent-tokens/
├── plan.md              # This file
├── research.md          # Phase 0: technology decisions
├── data-model.md        # Phase 1: entity definitions
├── quickstart.md        # Phase 1: developer setup
├── contracts/           # Phase 1: REST API contracts
│   └── agent-tokens-api.yaml
└── tasks.md             # Phase 2: implementation tasks
```

### Source Code (repository root)

```text
# Backend (Go)
internal/
├── auth/                        # NEW: Auth context and token validation
│   ├── context.go               # AuthContext type, middleware helpers
│   ├── context_test.go
│   ├── agent_token.go           # AgentToken model, HMAC hashing, validation
│   └── agent_token_test.go
├── storage/
│   ├── agent_tokens.go          # NEW: BBolt CRUD for agent tokens
│   └── agent_tokens_test.go
├── httpapi/
│   ├── server.go                # MODIFIED: Auth middleware extended
│   ├── tokens.go                # NEW: REST API handlers for token CRUD
│   └── tokens_test.go
├── server/
│   └── mcp.go                   # MODIFIED: Scope enforcement in retrieve_tools, call_tool_*
├── contracts/
│   └── auth.go                  # NEW: AuthContext contract type
└── cli/
    └── output/                  # MODIFIED: Token table formatter

cmd/mcpproxy/
└── token_cmd.go                 # NEW: token create/list/revoke/regenerate commands

# Frontend (Vue 3)
frontend/src/
├── views/
│   └── AgentTokens.vue          # NEW: Token management page
├── components/
│   └── CreateTokenDialog.vue    # NEW: Token creation modal
└── services/
    └── tokenApi.ts              # NEW: REST API client for tokens

# Tests
internal/auth/agent_token_test.go
internal/storage/agent_tokens_test.go
internal/httpapi/tokens_test.go
```

**Structure Decision**: Follows existing Go project layout. New `internal/auth/` package for auth context (clean separation from storage). Token storage in existing `internal/storage/` alongside other BBolt operations. CLI in existing `cmd/mcpproxy/` pattern. Frontend in existing Vue 3 app structure.

## Complexity Tracking

No constitution violations. No complexity justifications needed.
