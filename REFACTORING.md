# REFACTORING.md — mcpproxy-go Comprehensive Refactor Plan

> **Goal**: Safely refactor `mcpproxy-go` to a **core + tray** split with a **v1 REST API + SSE**, embedded **Web UI**, hardened **OAuth/storage**, and robust **tests/observability** — while preserving the **current hotfix/release workflow** on `main` and running **prerelease** builds from `next`.

---

## Status Overview

**Current Status**: Phases 0-4 are **largely completed** with some ongoing work. The runtime has been extracted, APIs are implemented, tray is separated, and major deadlock issues have been resolved.

### ✅ COMPLETED PHASES

#### Phase 0 ✅ — Prep & Guard Rails
- [x] Snapshot current behaviour and test baselines established
- [x] Web smoke scenario created with Playwright tests
- [x] Tray API usage confirmed (still has legacy `/api` calls to clean up)

#### Phase 1 ✅ — Runtime Skeleton (Pure Extraction)
- [x] `internal/runtime` package created with core lifecycle management
- [x] Server delegates to runtime while maintaining API compatibility
- [x] Background initialization, connection management, and tool indexing extracted

#### Phase 3 ✅ — Event Bus & Config Sync
- [x] Runtime event bus implemented for status updates, server mutations, config reloads
- [x] SSE stream wired to runtime events
- [x] Tray menus refresh via `servers.changed` SSE without fsnotify

#### Phase 4 ✅ — Legacy `/api` Removal (MOSTLY DONE)
- [x] Legacy `/api` stack removed; tray now relies on `/api/v1` + SSE bus
- [x] **✅ RESOLVED**: BoltDB deadlock issue fixed with async storage operations
- [x] `TestBinaryAPIEndpoints` timeouts resolved via queue-based AsyncManager
- [x] Tests now pass consistently

#### Phase 5 ✅ — Observability Module
- [x] Create `internal/observability` package
- [x] Implement `/healthz`, `/readyz`, `/metrics` endpoints
- [x] Add Prometheus metrics and optional OpenTelemetry tracing
- [x] Component health checkers for database, index, upstream servers
- [x] HTTP middleware integration for metrics and tracing

#### Phase 2 ✅ — Shared Contracts Package
- [x] `internal/appctx` with interfaces created
- [x] Full typed DTOs replacing `map[string]interface{}` payloads
- [x] `internal/contracts/types.go` with comprehensive data structures
- [x] `internal/contracts/converters.go` for type conversion utilities
- [x] TypeScript type generation for frontend via `cmd/generate-types`
- [x] Generated types available at `web/frontend/src/types/contracts.ts`

#### Phase 6 ✅ — Web UI & Contract Tests
- [x] Web UI embedded via `go:embed` at `/ui/`
- [x] Frontend built with Vite + TypeScript
- [x] Basic Playwright smoke tests implemented
- [x] Full contract tests with golden responses via `internal/httpapi/contracts_test.go`
- [x] Comprehensive API coverage for all major endpoints
- [x] Golden file validation for API contract stability

#### Phase 7 ✅ — Follow-up Hardening
- [x] Expand runtime interfaces for future extensibility
- [x] Feature flags for module isolation via `internal/config/features.go`
- [x] Document module boundaries in `ARCHITECTURE.md`
- [x] Feature flag validation and dependency checking
- [x] Graceful degradation patterns documented

### 🚧 IN PROGRESS / PARTIALLY COMPLETED

### 📋 TODO PHASES

*All core refactoring phases (0-7) are now complete! Remaining items are future enhancements:*

---

## Detailed Implementation Status

### Core Architecture ✅ DONE

**What's Working:**
- Runtime extraction complete with proper lifecycle management
- Event bus system operational for real-time updates
- BoltDB async storage pattern preventing deadlocks
- API endpoints functional with proper separation
- SSE streaming for live updates
- Web UI serving from embedded filesystem

**Key Files Implemented:**
- `internal/runtime/` - Core runtime management
- `internal/runtime/lifecycle.go` - Background operations and config sync
- `internal/storage/async_ops.go` - Queue-based storage operations
- `internal/httpapi/` - REST API with chi router
- `web/handler.go` - Web UI serving with go:embed
- `cmd/mcpproxy-tray/` - Separated tray application

### Release Infrastructure 📋 TODO

Based on the original REFACTORING.md plan, these phases need to be implemented:

#### P0 — Branching Model & Protections
- [ ] Create proper `main`/`next` branch strategy
- [ ] GitHub Environments for production vs staging
- [ ] Hotfix workflow documentation

#### P1 — Split CI/CD: Stable vs Prerelease
- [ ] `.github/workflows/release.yml` for stable releases from `main`
- [ ] `.github/workflows/prerelease.yml` for prereleases from `next`
- [ ] Proper DMG notarization workflows

#### P2 — Auto-Updater Safety
- [ ] Prevent prerelease auto-updates in production
- [ ] `MCPPROXY_ALLOW_PRERELEASE_UPDATES` flag
- [ ] Asset selection unit tests

### Security & Resilience 📋 TODO

#### P8 — OAuth Token Store (Keychain + age fallback)
- [x] Basic OAuth implementation exists
- [ ] **TODO**: Keyring integration with fallback to age-encrypted files
- [ ] **TODO**: Proper token refresh with exponential backoff

#### P9 — Circuit Breakers, Backoff, and Rate Limits
- [ ] Per-server circuit breakers for upstream calls
- [ ] Exponential backoff with jitter on retries
- [ ] Rate limiting with metrics exposure

#### P10 — Health/Ready + Prometheus + OpenTelemetry
- [ ] Health endpoints (`/healthz`, `/readyz`)
- [ ] Prometheus metrics via `/metrics`
- [ ] OpenTelemetry tracing for upstream calls

#### P11 — OpenAPI + Golden Tests
- [ ] Swagger documentation generation
- [ ] Golden test files for API compatibility
- [ ] API documentation at `/ui/swagger/`

### Packaging & Distribution 📋 TODO

#### P12 — Docker Isolation Hardening
- [x] Basic Docker isolation exists
- [ ] **TODO**: CPU/memory quotas, read-only FS, dropped capabilities
- [ ] **TODO**: Optional gVisor/Firecracker backends

#### P13 — macOS Packaging
- [x] Basic DMG creation exists
- [ ] **TODO**: Proper Tray.app bundle packaging
- [ ] **TODO**: Enhanced codesigning and notarization

---

## Next Priority Actions

### Immediate (Next 1-2 PRs)
1. **Complete Phase 2**: Replace remaining `map[string]interface{}` with typed contracts
2. **Complete Phase 5**: Add observability endpoints (`/healthz`, `/readyz`, `/metrics`)
3. **Clean up Phase 4**: Remove any remaining legacy `/api` references in tray client

### Short Term (Next 4-6 PRs)
1. **Implement P8**: Secure OAuth token storage with keyring
2. **Implement P9**: Circuit breakers and rate limiting
3. **Implement P11**: OpenAPI documentation and golden tests

### Medium Term (Next 8-10 PRs)
1. **Implement P0-P2**: Release infrastructure and branching
2. **Implement P12**: Enhanced Docker isolation
3. **Implement P13**: Professional macOS packaging

---

## Verification Commands

### Current Working Features
```bash
# Build both binaries
CGO_ENABLED=0 go build -o mcpproxy ./cmd/mcpproxy
GOOS=darwin CGO_ENABLED=1 go build -o mcpproxy-tray ./cmd/mcpproxy-tray

# Test core functionality
go test ./internal/runtime ./internal/server
./scripts/run-e2e-tests.sh
./scripts/test-api-e2e.sh

# Test API endpoints
./mcpproxy serve &
curl -s :8080/api/v1/servers | jq .
curl -N :8080/events | head -10

# Test Web UI
open http://localhost:8080/ui/

# Test Playwright smoke
scripts/run-web-smoke.sh
```

### Known Issues
- Some contract types still use `map[string]interface{}`
- Missing observability endpoints
- OAuth token storage not fully hardened
- Release infrastructure needs proper setup

---

## Working Principles

1. **One phase per PR** unless explicitly approved for combination
2. **Verify before proceeding** - all tests must pass before moving to next phase
3. **Maintain backward compatibility** during transitions
4. **Document decisions** in commit messages and PR descriptions
5. **Feature flags** for major changes to allow safe rollback

---

## Success Criteria (Final Goals)

- [x] Core builds CGO-off; Tray builds CGO-on (darwin)
- [x] `/api/v1/*` endpoints + `/events` functional
- [x] Web UI embedded & operational
- [ ] Tokens secured via keyring/age; no plaintext
- [ ] Circuit breakers and rate limits active
- [ ] `/metrics` and health endpoints exposed
- [ ] OpenAPI documentation generated
- [ ] Golden tests lock API compatibility
- [ ] DMG properly signed, notarized, and stapled
- [ ] Releases split stable/prerelease with proper workflows

---

*This document replaces both REFACTORING.md and REFACTORING_CODEX.md as the single source of truth for the refactoring plan.*