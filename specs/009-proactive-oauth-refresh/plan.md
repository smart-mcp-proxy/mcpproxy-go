# Implementation Plan: Proactive OAuth Token Refresh & UX Improvements

**Branch**: `009-proactive-oauth-refresh` | **Date**: 2025-12-07 | **Spec**: [spec.md](./spec.md)
**Input**: Feature specification from `/specs/009-proactive-oauth-refresh/spec.md`

## Summary

Implement proactive OAuth token refresh to prevent tool call failures, add CLI/REST logout functionality, and fix Web UI login button visibility for connected servers with expired tokens. The core change is a background token refresh manager that monitors all OAuth servers and refreshes tokens at 80% of their lifetime before expiration.

## Technical Context

**Language/Version**: Go 1.24.0
**Primary Dependencies**: mcp-go (v0.43.1), zap (logging), chi (HTTP router), BBolt (storage), Vue 3 + TypeScript (frontend)
**Storage**: BBolt embedded database (`~/.mcpproxy/config.db`) - `oauth_tokens` bucket
**Testing**: go test (unit/integration), Playwright (E2E for Web UI), bash scripts (API E2E)
**Target Platform**: macOS, Linux, Windows - CLI + system tray
**Project Type**: Web application (Go backend + Vue 3 frontend)
**Performance Goals**: Token refresh within 5 seconds of 80% lifetime, logout under 3 seconds
**Constraints**: Per-server mutex to prevent race conditions, no blocking on main thread
**Scale/Scope**: Supports 100+ OAuth servers with independent refresh schedules

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Status | Notes |
|-----------|--------|-------|
| I. Performance at Scale | PASS | Background refresh doesn't block API requests |
| II. Actor-Based Concurrency | PASS | Token refresh manager uses channels for coordination |
| III. Configuration-Driven | PASS | Refresh threshold configurable via config file |
| IV. Security by Default | PASS | Tokens stored securely in BBolt, cleared on logout |
| V. TDD | PASS | Unit tests for refresh logic, E2E tests specified |
| VI. Documentation Hygiene | PASS | CLAUDE.md update required for new CLI command |

**Architecture Constraints**:
- Core + Tray Split: PASS - Logout REST endpoint in core, tray uses it via API
- Event-Driven Updates: PASS - SSE events for token refresh/logout
- DDD Layering: PASS - Refresh manager in `internal/oauth/`, REST in `internal/httpapi/`
- Upstream Client Modularity: PASS - Leverages existing 3-layer design

## Project Structure

### Documentation (this feature)

```text
specs/009-proactive-oauth-refresh/
├── plan.md              # This file
├── research.md          # Phase 0 output
├── data-model.md        # Phase 1 output
├── quickstart.md        # Phase 1 output
├── contracts/           # Phase 1 output (OpenAPI additions)
└── tasks.md             # Phase 2 output
```

### Source Code (repository root)

```text
# Backend (Go)
internal/
├── oauth/
│   ├── persistent_token_store.go  # Existing - token persistence
│   ├── refresh_manager.go         # NEW - background refresh coordinator
│   └── refresh_manager_test.go    # NEW - unit tests
├── httpapi/
│   └── server.go                  # MODIFY - add /logout endpoint
├── runtime/
│   └── runtime.go                 # MODIFY - integrate refresh manager
├── management/
│   └── service.go                 # MODIFY - add OAuthLogout method
└── cliclient/
    └── client.go                  # MODIFY - add TriggerOAuthLogout

cmd/
└── mcpproxy/
    └── auth_cmd.go                # MODIFY - add logout subcommand

# Frontend (Vue 3 + TypeScript)
frontend/src/
├── components/
│   └── ServerCard.vue             # MODIFY - Login/Logout buttons, auth badge
├── services/
│   └── api.ts                     # MODIFY - add triggerOAuthLogout
└── stores/
    └── servers.ts                 # MODIFY - add triggerOAuthLogout action

# Tests
tests/
├── oauthserver/                   # Existing OAuth test server
├── e2e/
│   └── playwright/
│       └── oauth-ux.spec.ts       # NEW - Playwright tests for login/logout UI
scripts/
└── test-oauth-refresh-e2e.sh      # NEW - E2E test for proactive refresh
```

**Structure Decision**: Web application structure - Go backend in `internal/` + `cmd/`, Vue 3 frontend in `frontend/src/`. Tests use existing OAuth test server from spec 007.

## Complexity Tracking

No constitution violations - all changes follow established patterns.

