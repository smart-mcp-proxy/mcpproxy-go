# Implementation Plan: MCPProxy Repo Restructure (Personal + Teams Foundation)

**Branch**: `029-mcpproxy-teams` | **Date**: 2026-03-08 | **Spec**: [spec.md](spec.md)
**Input**: Feature specification from `/specs/029-mcpproxy-teams/spec.md`

## Summary

Restructure the MCPProxy repository to support two editions (Personal and Teams) built from the same codebase using Go build tags. Personal is the default build; Teams requires `-tags teams`. Add Dockerfile for teams, `native/` directory skeleton for future Swift/C# tray apps, extended Makefile, and edition self-identification.

## Technical Context

**Language/Version**: Go 1.24 (toolchain go1.24.10), TypeScript 5.x / Vue 3.5 (frontend)
**Primary Dependencies**: Cobra (CLI), Chi (HTTP), BBolt (storage), Zap (logging), mcp-go (MCP), Vue 3 + Tailwind + DaisyUI (frontend)
**Storage**: BBolt database (`~/.mcpproxy/config.db`)
**Testing**: `go test`, `./scripts/test-api-e2e.sh`, Playwright (OAuth E2E), Vitest (frontend)
**Target Platform**: macOS (Personal DMG), Windows (Personal MSI), Linux (Personal tar.gz, Teams Docker/deb/tar.gz)
**Project Type**: web (Go backend + Vue frontend, embedded)
**Performance Goals**: No regression from current performance
**Constraints**: Zero behavior change for existing personal mode users (FR-037)
**Scale/Scope**: Foundation only — no teams feature logic, just the skeleton and build infrastructure

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Status | Notes |
|-----------|--------|-------|
| I. Performance at Scale | PASS | No performance-affecting changes; restructure only |
| II. Actor-Based Concurrency | PASS | No concurrency changes |
| III. Configuration-Driven Architecture | PASS | Edition determined at build time, mode at config time |
| IV. Security by Default | PASS | Teams auth middleware added later; foundation is inert |
| V. Test-Driven Development | PASS | Tests for edition detection, build tag verification |
| VI. Documentation Hygiene | PASS | CLAUDE.md updated with new structure |

**Architecture Constraints**:
| Constraint | Status | Notes |
|------------|--------|-------|
| Core + Tray Split | PASS | Tray split preserved; native/ skeleton added |
| Event-Driven Updates | PASS | No event changes |
| DDD Layering | PASS | internal/teams/ follows existing layer patterns |
| Upstream Client Modularity | PASS | No upstream changes |

## Project Structure

### Documentation (this feature)

```text
specs/029-mcpproxy-teams/
├── spec.md              # Feature specification
├── plan.md              # This file
├── research.md          # Phase 0 output
├── data-model.md        # Phase 1 output
├── quickstart.md        # Phase 1 output
├── contracts/           # Phase 1 output
└── tasks.md             # Phase 2 output
```

### Source Code (repository root)

```text
cmd/mcpproxy/
├── main.go                      # Shared entry point (modify: add edition variable)
├── teams_register.go            # NEW: //go:build teams — registers teams features
└── edition.go                   # NEW: default edition = "personal"
    edition_teams.go             # NEW: //go:build teams — overrides to "teams"

internal/
├── teams/                       # NEW: teams-only skeleton
│   ├── doc.go                   # Package doc, //go:build teams
│   ├── registry.go              # Feature registry (init pattern)
│   └── registry_test.go         # Verify registration works
├── httpapi/
│   └── server.go                # Modify: expose edition in /api/v1/status
└── ... (all existing packages unchanged)

frontend/
└── src/
    └── views/teams/             # NEW: empty directory for future teams pages
    └── components/teams/        # NEW: empty directory for future teams components

native/
├── macos/                       # NEW: placeholder for Swift tray app
│   └── README.md
└── windows/                     # NEW: placeholder for C# tray app
    └── README.md

Dockerfile                       # NEW: Teams Docker image
Makefile                         # Modify: add build-teams, build-docker, build-deb targets
.github/workflows/release.yml   # Modify: add teams build matrix entries
```

**Structure Decision**: Existing Go project structure preserved. Teams code isolated in `internal/teams/` with build tags. No `pkg/` migration. Native tray apps in `native/` at repo root.

## Complexity Tracking

No constitution violations. All changes follow existing patterns.
