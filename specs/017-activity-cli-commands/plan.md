# Implementation Plan: Activity CLI Commands

**Branch**: `017-activity-cli-commands` | **Date**: 2025-12-27 | **Spec**: [spec.md](./spec.md)
**Input**: Feature specification from `/specs/017-activity-cli-commands/spec.md`

## Summary

Implement CLI commands for querying and monitoring activity logs: `activity list`, `activity watch`, `activity show`, `activity summary`, and `activity export`. Commands integrate with the existing output formatter (spec 014) and activity REST API (spec 016), following the established `upstream` command patterns.

## Technical Context

**Language/Version**: Go 1.24 (toolchain go1.24.10)
**Primary Dependencies**: Cobra CLI framework, encoding/json, internal/cli/output (spec 014), internal/cliclient
**Storage**: N/A (CLI layer only - uses REST API from spec 016)
**Testing**: go test, E2E tests via scripts/test-api-e2e.sh
**Target Platform**: macOS, Linux, Windows
**Project Type**: Single Go project with cmd/mcpproxy entry point
**Performance Goals**: List command <1s for 100 records, watch latency <200ms (per SC-001, SC-002)
**Constraints**: Must use REST API (no direct storage access per FR-025), daemon required for all commands
**Scale/Scope**: Same as spec 016 - up to 100,000 activity records

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Status | Justification |
|-----------|--------|---------------|
| I. Performance at Scale | PASS | Uses existing indexed API, list limited to 100 records max |
| II. Actor-Based Concurrency | PASS | CLI is single-threaded, daemon handles concurrency |
| III. Configuration-Driven | PASS | Uses daemon config, no CLI-specific state |
| IV. Security by Default | PASS | All API calls use existing auth (API key/socket) |
| V. Test-Driven Development | PASS | Will add unit tests + E2E tests |
| VI. Documentation Hygiene | PASS | Will update CLAUDE.md and docs/cli-management-commands.md |

**Architecture Constraints:**

| Constraint | Status | Justification |
|------------|--------|---------------|
| Core + Tray Split | PASS | CLI is part of core mcpproxy binary |
| Event-Driven Updates | PASS | Watch command uses SSE from /events |
| DDD Layering | PASS | CLI in presentation layer, calls application layer via REST |
| Upstream Client Modularity | N/A | Uses REST API, not upstream MCP client |

## Project Structure

### Documentation (this feature)

```text
specs/017-activity-cli-commands/
├── plan.md              # This file
├── research.md          # Phase 0 output
├── data-model.md        # Phase 1 output
├── quickstart.md        # Phase 1 output
├── contracts/           # Phase 1 output (CLI contract)
└── tasks.md             # Phase 2 output (/speckit.tasks command)
```

### Source Code (repository root)

```text
cmd/mcpproxy/
├── main.go              # Root command (add GetActivityCommand())
├── activity_cmd.go      # NEW: Activity command implementation
├── activity_cmd_test.go # NEW: Unit tests for activity commands
└── upstream_cmd.go      # Reference pattern for daemon commands

internal/
├── cli/output/          # Existing output formatter (spec 014)
│   ├── formatter.go     # OutputFormatter interface
│   └── table.go         # Table formatter
├── cliclient/           # Existing HTTP client for daemon
│   └── client.go        # Add activity API methods
├── httpapi/
│   └── activity.go      # Existing activity REST endpoints (spec 016)
└── contracts/
    └── activity.go      # Existing ActivityRecord types

scripts/
└── test-api-e2e.sh      # Update to include activity command tests
```

**Structure Decision**: Single file `activity_cmd.go` following the established pattern from `upstream_cmd.go`. No new packages needed - uses existing cli/output and cliclient.

## Complexity Tracking

> No violations - implementation follows existing patterns with no new abstractions.

| Item | Decision | Rationale |
|------|----------|-----------|
| Single file | activity_cmd.go | Matches upstream_cmd.go pattern, all 5 subcommands in one file |
| No new packages | Use existing cliclient | REST API already implemented in spec 016 |
| SSE parsing | bufio.Scanner | Standard Go pattern for line-based SSE parsing |
