# Implementation Plan: OAuth E2E Testing & Observability

**Branch**: `007-oauth-e2e-testing` | **Date**: 2025-12-02 | **Spec**: [spec.md](./spec.md)
**Input**: Feature specification from `/specs/007-oauth-e2e-testing/spec.md`

## Summary

Implement a comprehensive OAuth E2E test infrastructure for mcpproxy. This includes:

1. **Local OAuth Test Server** (`tests/oauthserver/`): A reusable Go package providing a complete OAuth 2.1 server with configurable flows (auth code + PKCE, device code, DCR, client credentials), error injection, JWKS rotation, and browser-based login UI.

2. **Go Integration Tests**: Test coverage for OAuth detection methods, all grant types, resource indicators (RFC 8707), token refresh, and error handling scenarios.

3. **E2E Script & Playwright Tests**: Orchestration script (`scripts/run-oauth-e2e.sh`) and browser-based tests for the full auth login experience.

4. **Observability Enhancements**: Enriched `auth status`, `auth login` preview output, structured logging, and `doctor` OAuth health checks.

## Technical Context

**Language/Version**: Go 1.24.0 (as per existing project)
**Primary Dependencies**:
- `net/http` (stdlib) for OAuth test server
- `github.com/golang-jwt/jwt/v5` for JWT token generation
- `crypto/rsa` for RSA key pair generation (deterministic for tests)
- `github.com/playwright-community/playwright-go` for browser tests (optional, can use npx playwright)
**Storage**: BBolt (existing `internal/storage/`) for token persistence
**Testing**: `go test` with testify assertions (existing pattern)
**Target Platform**: macOS, Linux, Windows (all platforms mcpproxy supports)
**Project Type**: Single project (Go monorepo)
**Performance Goals**: OAuth E2E suite runs in <5 minutes in CI
**Constraints**: Token expiry times in tests must be short (seconds) to allow refresh testing without long waits
**Scale/Scope**: ~15 new test cases, 1 new package, enhancements to 3 CLI commands

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Status | Notes |
|-----------|--------|-------|
| I. Performance at Scale | PASS | Test infrastructure only; does not affect production performance |
| II. Actor-Based Concurrency | PASS | OAuth test server uses standard `net/http` patterns with goroutines per request |
| III. Configuration-Driven Architecture | PASS | Test harness uses Options struct for configuration; no config file changes |
| IV. Security by Default | PASS | Test server is localhost-only; production OAuth unchanged |
| V. Test-Driven Development | PASS | This feature IS the test infrastructure; increases coverage |
| VI. Documentation Hygiene | PASS | Will update CLAUDE.md with test commands, add MANUAL_TESTING.md section |

**Architecture Constraints**:
- Core + Tray Split: N/A (test infrastructure only)
- Event-Driven Updates: N/A (test infrastructure only)
- DDD Layering: PASS (test package in `tests/` separate from `internal/`)
- Upstream Client Modularity: PASS (tests exercise existing 3-layer design)

## Project Structure

### Documentation (this feature)

```text
specs/007-oauth-e2e-testing/
├── plan.md              # This file
├── research.md          # Phase 0 output
├── data-model.md        # Phase 1 output
├── quickstart.md        # Phase 1 output
├── contracts/           # Phase 1 output
└── tasks.md             # Phase 2 output (from /speckit.tasks)
```

### Source Code (repository root)

```text
tests/
└── oauthserver/
    ├── server.go           # Main test server with Start() entry point
    ├── options.go          # Configuration Options struct
    ├── discovery.go        # /.well-known endpoints
    ├── authorize.go        # /authorize endpoint with login UI
    ├── token.go            # /token endpoint (all grant types)
    ├── dcr.go              # /registration endpoint (DCR)
    ├── device.go           # /device_authorization, /device_verification
    ├── jwks.go             # /jwks.json endpoint with rotation
    ├── jwt.go              # JWT token generation helpers
    ├── protected.go        # Protected resource with WWW-Authenticate
    ├── templates/          # HTML templates for login UI
    │   └── login.html
    └── server_test.go      # Unit tests for test server itself

internal/
├── oauth/
│   └── ... (existing - enhanced logging)
├── upstream/
│   └── ... (existing - enhanced observability)
└── management/
    └── diagnostics.go      # Enhanced doctor OAuth checks

cmd/mcpproxy/
├── auth_cmd.go             # Enhanced auth login/status output
└── doctor_cmd.go           # OAuth health check integration

scripts/
├── run-oauth-e2e.sh        # E2E orchestration script
└── test-api-e2e.sh         # Existing (will call OAuth tests)

e2e/
└── playwright/
    ├── oauth-login.spec.ts # Browser login flow tests
    └── playwright.config.ts
```

**Structure Decision**: Test infrastructure lives in `tests/oauthserver/` (not `internal/`) to keep it clearly separate from production code. Playwright tests live in `e2e/playwright/` following common conventions.

## Complexity Tracking

No constitution violations requiring justification.

## Phase Dependencies

```
Phase 0 (Research) → Phase 1 (Design) → Phase 2 (Tasks)
                                              ↓
                                        /speckit.tasks
```
