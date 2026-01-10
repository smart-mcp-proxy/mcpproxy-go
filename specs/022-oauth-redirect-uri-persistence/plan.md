# Implementation Plan: OAuth Redirect URI Port Persistence

**Branch**: `022-oauth-redirect-uri-persistence` | **Date**: 2025-01-09 | **Spec**: [spec.md](./spec.md)
**Input**: Feature specification from `/specs/022-oauth-redirect-uri-persistence/spec.md`

## Summary

When OAuth tokens expire and re-authentication is needed, mcpproxy allocates a new dynamic port for the callback server, causing "Invalid redirect URI" errors because the port doesn't match what was registered during DCR. This implementation stores the callback port used during successful DCR and attempts to reuse it for subsequent authentications, with automatic re-DCR fallback if the port is unavailable.

## Technical Context

**Language/Version**: Go 1.24 (toolchain go1.24.10)
**Primary Dependencies**: BBolt (storage), Chi router (HTTP), Zap (logging), mark3labs/mcp-go (MCP protocol)
**Storage**: BBolt database (`~/.mcpproxy/config.db`) - OAuthTokenRecord extended with callback port
**Testing**: go test, E2E scripts (`./scripts/test-api-e2e.sh`, `./scripts/run-oauth-e2e.sh`)
**Target Platform**: macOS, Linux, Windows desktop
**Project Type**: Single Go application with multiple binaries (core + tray)
**Performance Goals**: OAuth flow startup <100ms (port binding is negligible)
**Constraints**: Must maintain backward compatibility with existing stored credentials
**Scale/Scope**: Per-server OAuth credentials (~10-50 servers typical)

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Status | Notes |
|-----------|--------|-------|
| I. Performance at Scale | PASS | Port binding is O(1), no index impact |
| II. Actor-Based Concurrency | PASS | CallbackServerManager already uses mutex for server map |
| III. Configuration-Driven Architecture | PASS | OAuth state stored in BBolt, not config file |
| IV. Security by Default | PASS | No new security surface, port is localhost-only |
| V. Test-Driven Development | PASS | Unit + E2E tests planned |
| VI. Documentation Hygiene | PASS | CLAUDE.md update not needed (internal change) |

### Architecture Constraints

| Constraint | Status | Notes |
|------------|--------|-------|
| Core + Tray Split | PASS | Change is in core server only |
| Event-Driven Updates | N/A | No new events needed |
| DDD Layering | PASS | Storage in infrastructure, OAuth in application |
| Upstream Client Modularity | PASS | Change spans core client and OAuth config |

## Project Structure

### Documentation (this feature)

```text
specs/022-oauth-redirect-uri-persistence/
├── spec.md              # Feature specification
├── plan.md              # This file
├── research.md          # Phase 0 research output
├── data-model.md        # Phase 1 data model
└── tasks.md             # Phase 2 implementation tasks
```

### Source Code (repository root)

```text
internal/
├── storage/
│   ├── models.go           # OAuthTokenRecord - add CallbackPort, RedirectURI fields
│   ├── bbolt.go            # UpdateOAuthClientCredentials, GetOAuthClientCredentials
│   ├── manager.go          # Storage manager wrappers (if any)
│   └── bbolt_test.go       # Unit tests for port persistence
├── oauth/
│   ├── config.go           # StartCallbackServer - add preferredPort parameter
│   ├── config_test.go      # Unit tests for preferred port binding
│   └── persistent_token_store.go  # May need updates for port handling
└── upstream/
    └── core/
        └── connection.go   # DCR credential persistence with port
```

**Structure Decision**: Single project structure following existing patterns. All changes are within `internal/` packages, following Go conventions.

## Complexity Tracking

> No violations - implementation is a straightforward extension of existing patterns.

| Item | Rationale |
|------|-----------|
| New fields on OAuthTokenRecord | Required for port persistence, same pattern as ClientID/ClientSecret |
| preferredPort parameter | Backward compatible - 0 means use existing dynamic allocation |
