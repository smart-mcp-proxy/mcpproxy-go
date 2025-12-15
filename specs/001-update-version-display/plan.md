# Implementation Plan: Update Check Enhancement & Version Display

**Branch**: `001-update-version-display` | **Date**: 2025-12-15 | **Spec**: [spec.md](./spec.md)
**Input**: Feature specification from `/specs/001-update-version-display/spec.md`

## Summary

Implement centralized version display and update notification across all MCPProxy interfaces (tray menu, Web Control Panel, CLI). The core server will check GitHub releases every 4 hours (+ on startup), cache results in-memory, and expose via REST API. Tray/WebUI/CLI consume this API to display version info and update availability.

## Technical Context

**Language/Version**: Go 1.24 (toolchain go1.24.10)
**Primary Dependencies**:
- fyne.io/systray (tray menu)
- go-chi/chi/v5 (HTTP routing)
- Bleve v2 (search - existing)
- Vue 3 + TypeScript (frontend)
**Storage**: In-memory only for version cache (no persistence per clarification)
**Testing**: go test, ./scripts/test-api-e2e.sh, Playwright (frontend)
**Target Platform**: macOS, Windows, Linux
**Project Type**: Web application (Go backend + Vue frontend + Tray app)
**Performance Goals**: Version check <2s response time, no impact on core server performance
**Constraints**: GitHub API rate limit (60 req/hr unauthenticated), 4-hour check interval
**Scale/Scope**: Single version info entity, 1 new REST endpoint, updates to 3 interfaces

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Status | Notes |
|-----------|--------|-------|
| I. Performance at Scale | PASS | Background check every 4 hours, no blocking operations |
| II. Actor-Based Concurrency | PASS | Update checker as background goroutine with ticker |
| III. Configuration-Driven Architecture | PASS | Respects env vars (MCPPROXY_DISABLE_AUTO_UPDATE, MCPPROXY_ALLOW_PRERELEASE_UPDATES) |
| IV. Security by Default | PASS | No new attack surface, read-only GitHub API |
| V. Test-Driven Development | PASS | FR-020-022 mandate unit + E2E tests |
| VI. Documentation Hygiene | PASS | FR-018-019 mandate docs/ updates |
| Separation of Concerns | PASS | Core owns update check; tray/WebUI consume via API |
| Event-Driven Updates | PASS | Version info exposed via existing /status or new endpoint |

**Gate Result**: PASS - No violations

## Project Structure

### Documentation (this feature)

```text
specs/001-update-version-display/
├── plan.md              # This file
├── research.md          # Phase 0 output
├── data-model.md        # Phase 1 output
├── quickstart.md        # Phase 1 output
├── contracts/           # Phase 1 output
└── tasks.md             # Phase 2 output (/speckit.tasks command)
```

### Source Code (repository root)

```text
# Backend (Go)
internal/
├── httpapi/
│   └── version.go           # NEW: Version endpoint handler
├── runtime/
│   └── update_checker.go    # NEW: Background update checker service
├── tray/
│   └── tray.go              # MODIFY: Add version menu item, update notification
└── server/
    └── routes.go            # MODIFY: Register version endpoint

cmd/mcpproxy/
└── doctor.go                # MODIFY: Add version + update info to doctor output

# Frontend (Vue)
frontend/src/
├── components/
│   └── UpdateBanner.vue     # NEW: Update notification banner
├── types/
│   └── contracts.ts         # MODIFY: Add VersionInfo type (auto-generated)
└── App.vue                  # MODIFY: Display version in header/footer

# Documentation
docs/
└── features/
    └── version-updates.md   # NEW: User-facing documentation

# Tests
internal/
├── httpapi/
│   └── version_test.go      # NEW: Unit tests for version endpoint
└── runtime/
    └── update_checker_test.go # NEW: Unit tests for update checker

scripts/
└── test-api-e2e.sh          # MODIFY: Add version endpoint tests
```

**Structure Decision**: Follows existing project structure. New files in `internal/runtime/` for background service, `internal/httpapi/` for REST endpoint. Tray modifications in existing `internal/tray/tray.go`.

## Complexity Tracking

> No complexity violations - feature follows existing patterns

---

## Post-Design Constitution Re-Check

*Re-evaluated after Phase 1 design completion.*

| Principle | Status | Design Validation |
|-----------|--------|-------------------|
| I. Performance at Scale | PASS | Background ticker (4hr), non-blocking HTTP check, RWMutex for cache |
| II. Actor-Based Concurrency | PASS | Single goroutine owns VersionInfo, communicates via method calls with mutex |
| III. Configuration-Driven Architecture | PASS | Respects MCPPROXY_DISABLE_AUTO_UPDATE, MCPPROXY_ALLOW_PRERELEASE_UPDATES |
| IV. Security by Default | PASS | Read-only GitHub API, no credentials stored, no new endpoints exposed publicly |
| V. Test-Driven Development | PASS | Unit tests for checker + API, E2E tests planned |
| VI. Documentation Hygiene | PASS | docs/features/version-updates.md required, API docs updated |
| Separation of Concerns | PASS | Core: `internal/updatecheck/`, API: `/api/v1/info` extension, UI: consumes API |
| Event-Driven Updates | N/A | Not using events; polling-based (simpler for infrequent checks) |
| 3-Layer Upstream | N/A | Not modifying upstream client layer |

**Post-Design Gate Result**: PASS - Design aligns with constitution principles

---

## Generated Artifacts

| Artifact | Path | Status |
|----------|------|--------|
| Research | `specs/001-update-version-display/research.md` | Complete |
| Data Model | `specs/001-update-version-display/data-model.md` | Complete |
| API Contract (OpenAPI) | `specs/001-update-version-display/contracts/api-extension.yaml` | Complete |
| Go Types | `specs/001-update-version-display/contracts/go-types.go` | Complete |
| TypeScript Types | `specs/001-update-version-display/contracts/typescript-types.ts` | Complete |
| Quickstart Guide | `specs/001-update-version-display/quickstart.md` | Complete |

---

## Next Steps

Run `/speckit.tasks` to generate the implementation task list.
