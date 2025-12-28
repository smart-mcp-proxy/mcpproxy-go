# Implementation Plan: Intent Declaration with Tool Split

**Branch**: `018-intent-declaration` | **Date**: 2025-12-28 | **Spec**: [spec.md](./spec.md)
**Input**: Feature specification from `/specs/018-intent-declaration/spec.md`

## Summary

Replace the single `call_tool` with three operation-specific variants (`call_tool_read`, `call_tool_write`, `call_tool_destructive`) that enable granular IDE permission control. Implements a two-key security model where agents must declare intent in both tool selection and an `intent` parameter that must match, with validation against server annotations.

## Technical Context

**Language/Version**: Go 1.24 (toolchain go1.24.10)
**Primary Dependencies**: Cobra CLI, Chi router, BBolt (storage), Zap (logging), mark3labs/mcp-go (MCP protocol)
**Storage**: BBolt database (`~/.mcpproxy/config.db`) - ActivityRecord extended with intent metadata
**Testing**: go test, E2E via `./scripts/test-api-e2e.sh`
**Target Platform**: macOS, Linux, Windows (cross-platform CLI + tray)
**Project Type**: Single project (Go monorepo with cmd/ and internal/)
**Performance Goals**: Tool call validation <10ms overhead (per spec SC-007)
**Constraints**: Breaking change (removes legacy call_tool), backwards incompatible
**Scale/Scope**: Handles up to 1,000 tools per constitution

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Status | Notes |
|-----------|--------|-------|
| I. Performance at Scale | ✅ PASS | Intent validation is O(1) lookup, no index changes |
| II. Actor-Based Concurrency | ✅ PASS | No new concurrency patterns; uses existing event bus |
| III. Configuration-Driven Architecture | ✅ PASS | Adds `intent_declaration.strict_server_validation` config option |
| IV. Security by Default | ✅ PASS | Core feature: two-key validation prevents intent spoofing |
| V. Test-Driven Development | ✅ PASS | Unit tests for validation matrix, E2E for tool variants |
| VI. Documentation Hygiene | ✅ PASS | Updates CLAUDE.md, tool descriptions, API docs |

| Architecture Constraint | Status | Notes |
|------------------------|--------|-------|
| Core + Tray Split | ✅ PASS | Changes only in core; tray unaffected |
| Event-Driven Updates | ✅ PASS | Activity events already include metadata field |
| DDD Layering | ✅ PASS | Intent validation in domain layer (server/mcp.go) |
| Upstream Client Modularity | ✅ PASS | No changes to upstream client layers |

**GATE RESULT**: ✅ PASS - No violations requiring justification

## Project Structure

### Documentation (this feature)

```text
specs/018-intent-declaration/
├── plan.md              # This file
├── research.md          # Phase 0 output
├── data-model.md        # Phase 1 output
├── quickstart.md        # Phase 1 output
├── contracts/           # Phase 1 output (OpenAPI additions)
└── tasks.md             # Phase 2 output (/speckit.tasks command)
```

### Source Code (repository root)

```text
cmd/mcpproxy/
├── call_cmd.go          # MODIFY: Add tool-read, tool-write, tool-destructive subcommands
└── activity_cmd.go      # MODIFY: Add --intent-type filter, display intent column

internal/
├── server/
│   └── mcp.go           # MODIFY: Add 3 tool handlers, remove call_tool, add validation
├── contracts/
│   └── types.go         # ADD: IntentDeclaration struct
├── storage/
│   └── activity_models.go  # MODIFY: Document intent in Metadata field
├── config/
│   └── config.go        # MODIFY: Add IntentDeclarationConfig struct
└── httpapi/
    └── server.go        # MODIFY: Add intent_type query param to /api/v1/activity

oas/
└── swagger.yaml         # MODIFY: Add intent_type filter, document intent in responses
```

**Structure Decision**: Single project structure matches existing Go monorepo layout. No new directories needed; intent validation integrates into existing MCP handler layer.

## Complexity Tracking

> No violations requiring justification. Feature uses existing patterns.

| Violation | Why Needed | Simpler Alternative Rejected Because |
|-----------|------------|-------------------------------------|
| N/A | - | - |
