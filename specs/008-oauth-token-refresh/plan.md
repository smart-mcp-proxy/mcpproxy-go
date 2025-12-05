# Implementation Plan: OAuth Token Refresh Bug Fixes and Logging Improvements

**Branch**: `008-oauth-token-refresh` | **Date**: 2025-12-04 | **Spec**: [spec.md](./spec.md)
**Input**: Feature specification from `/specs/008-oauth-token-refresh/spec.md`

## Summary

Fix three documented OAuth bugs (token refresh on reconnection, race conditions, browser rate limiting) and add comprehensive logging with correlation IDs. The implementation enhances existing OAuth infrastructure in `internal/oauth/` and `internal/upstream/` packages to properly load persisted tokens, coordinate concurrent OAuth flows, and provide traceable debug logging for troubleshooting.

## Technical Context

**Language/Version**: Go 1.24.0
**Primary Dependencies**: mcp-go (OAuth transport), zap (logging), BBolt (token persistence), google/uuid (correlation IDs)
**Storage**: BBolt embedded database (`~/.mcpproxy/config.db`) - `oauth_tokens` and `oauth_completion` buckets
**Testing**: go test, Playwright E2E tests, OAuth test server (`tests/oauthserver/`)
**Target Platform**: macOS, Linux, Windows (desktop)
**Project Type**: Single project (Go CLI + daemon)
**Performance Goals**: Token refresh <500ms, OAuth flow completion <30s
**Constraints**: No breaking changes to existing OAuth config, backward compatible
**Scale/Scope**: Fix 3 bugs, add correlation ID logging, enhance debug output

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Status | Notes |
|-----------|--------|-------|
| I. Performance at Scale | ✅ PASS | Token refresh is background operation, no blocking |
| II. Actor-Based Concurrency | ✅ PASS | OAuth flow coordination uses channels/mutex per constitution |
| III. Configuration-Driven | ✅ PASS | No config changes required, behavior improvements only |
| IV. Security by Default | ✅ PASS | Token redaction in logs, secure token handling |
| V. Test-Driven Development | ✅ PASS | Comprehensive testing via OAuth test server required |
| VI. Documentation Hygiene | ✅ PASS | Will update CLAUDE.md if architecture changes |
| Core + Tray Split | ✅ PASS | Changes are in core only, tray unaffected |
| Event-Driven Updates | ✅ PASS | Uses existing event bus for OAuth state changes |
| DDD Layering | ✅ PASS | OAuth logic stays in infrastructure/domain layers |
| Upstream Client Modularity | ✅ PASS | Follows 3-layer design in `internal/upstream/` |

**Pre-Commit Gates**: Required tests before merge
- `./scripts/run-linter.sh`
- `go test ./internal/... -v`
- `./scripts/test-api-e2e.sh`
- `./scripts/run-oauth-e2e.sh`

## Project Structure

### Documentation (this feature)

```text
specs/008-oauth-token-refresh/
├── plan.md              # This file
├── research.md          # Phase 0: Implementation research
├── data-model.md        # Phase 1: Data models for OAuth flow
├── quickstart.md        # Phase 1: Quick start guide
├── contracts/           # Phase 1: API changes (if any)
└── tasks.md             # Phase 2: Implementation tasks
```

### Source Code (repository root)

```text
internal/
├── oauth/
│   ├── config.go                 # OAuth config creation - MODIFY (load persisted tokens)
│   ├── discovery.go              # RFC 9728/8414 discovery - ENHANCE (correlation ID logging)
│   ├── persistent_token_store.go # Token persistence - MODIFY (refresh token flow)
│   ├── correlation.go            # NEW: Correlation ID generation and context
│   └── logging.go                # NEW: Enhanced OAuth logging utilities
├── upstream/
│   ├── core/
│   │   └── connection.go         # OAuth flow handling - MODIFY (flow coordination, logging)
│   └── managed/
│       └── client.go             # State management - MODIFY (token refresh handling)
└── storage/
    └── bbolt.go                  # Token persistence - NO CHANGES (already supports refresh)

tests/
├── oauthserver/                  # Existing test server
│   └── cmd/server/               # Test server with TTL flags
└── e2e/
    └── oauth.spec.ts             # Playwright E2E tests - ENHANCE

docs/
└── oauth_mcpproxy_bug.md         # Bug documentation - UPDATE (mark as fixed)
```

**Structure Decision**: Existing single project structure. All changes in `internal/oauth/` and `internal/upstream/` packages following the established 3-layer upstream client design.

## Complexity Tracking

> No constitution violations. All changes follow existing patterns.

| Aspect | Approach | Rationale |
|--------|----------|-----------|
| Correlation IDs | Context-based propagation | Standard Go pattern, no new abstractions |
| Flow coordination | Per-server mutex map | Existing pattern in codebase, minimal change |
| Debug logging | zap structured fields | Consistent with existing logging approach |

## Post-Design Constitution Check

*Re-verified after Phase 1 design completion.*

| Principle | Status | Verification |
|-----------|--------|--------------|
| I. Performance at Scale | ✅ PASS | Token refresh uses background goroutine, no blocking |
| II. Actor-Based Concurrency | ✅ PASS | OAuthFlowCoordinator uses per-server mutex pattern |
| III. Configuration-Driven | ✅ PASS | No config changes; correlation IDs automatic |
| IV. Security by Default | ✅ PASS | Token redaction in logging.go; no secrets exposed |
| V. Test-Driven Development | ✅ PASS | OAuth test server with TTL flags enables TDD |
| VI. Documentation Hygiene | ✅ PASS | quickstart.md, data-model.md created |
| Core + Tray Split | ✅ PASS | All changes in core (`internal/`); tray unchanged |
| Event-Driven Updates | ✅ PASS | SSE events for OAuth state changes defined |
| DDD Layering | ✅ PASS | OAuth in infrastructure; coordination in domain |
| Upstream Client Modularity | ✅ PASS | Enhances existing 3-layer design |

**Design Artifacts Generated**:
- `research.md` - Bug analysis and implementation decisions
- `data-model.md` - Entity definitions and data flow
- `contracts/api-changes.md` - API enhancements (no breaking changes)
- `quickstart.md` - Testing guide

**Ready for**: `/speckit.tasks` to generate implementation tasks
