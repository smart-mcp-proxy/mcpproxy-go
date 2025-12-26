# Implementation Plan: Server Management CLI

**Branch**: `015-server-management-cli` | **Date**: 2025-12-26 | **Spec**: [spec.md](spec.md)
**Input**: Feature specification from `/specs/015-server-management-cli/spec.md`

## Summary

Implement CLI commands for adding and removing MCP servers with Claude Code-style syntax. The system provides:
1. `mcpproxy upstream add <name> <url>` for HTTP servers
2. `mcpproxy upstream add <name> -- <command> [args...]` for stdio servers
3. `mcpproxy upstream remove <name>` with confirmation prompts
4. `mcpproxy upstream add-json <name> '<json>'` for complex configurations
5. Idempotent operations via `--if-not-exists` and `--if-exists` flags

Current state: Server add/remove is already implemented in the MCP `upstream_servers` tool. This spec exposes that functionality via CLI commands with proper flag parsing, validation, and output formatting.

## Technical Context

**Language/Version**: Go 1.24 (toolchain go1.24.10)
**Primary Dependencies**: Cobra CLI framework, existing storage/config packages
**Storage**: ~/.mcpproxy/mcp_config.json (config file), BoltDB (persistence)
**Testing**: go test, ./scripts/test-api-e2e.sh
**Target Platform**: macOS (darwin), Linux, Windows
**Project Type**: CLI application (single binary)
**Performance Goals**: Add/remove completes in <1s including config persistence
**Constraints**: Backward compatible with existing config format, quarantine by default
**Scale/Scope**: 3 new CLI commands, HTTP API endpoints, cliclient methods

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Status | Notes |
|-----------|--------|-------|
| I. Performance at Scale | ✅ PASS | Single server operations, O(1) |
| II. Actor-Based Concurrency | ✅ PASS | Uses existing management service layer |
| III. Configuration-Driven | ✅ PASS | Config file + quarantine by default |
| IV. Security by Default | ✅ PASS | New servers quarantined, validation for inputs |
| V. TDD | ✅ PASS | Unit tests for parsing, E2E tests for commands |
| VI. Documentation Hygiene | ✅ PASS | Update docs/ with add/remove examples |
| Separation of Concerns | ✅ PASS | CLI → cliclient → HTTP API → management service |
| Event-Driven Updates | ✅ PASS | EmitServersChanged on add/remove |
| DDD Layering | ✅ PASS | CLI is presentation, management is domain |
| Upstream Client Modularity | ✅ PASS | Uses existing upstream manager |

**Gate Result**: ✅ PASS - No violations, proceed to implementation

## Project Structure

### Documentation (this feature)

```text
specs/015-server-management-cli/
├── plan.md              # This file
├── spec.md              # Feature specification
└── checklists/          # Quality checklists
```

### Source Code (repository root)

```text
cmd/mcpproxy/                  # MODIFY: Add new commands
├── upstream_cmd.go            # Add 'add', 'add-json', 'remove' subcommands

internal/cliclient/            # MODIFY: Add client methods
└── client.go                  # AddServer, RemoveServer methods

internal/httpapi/              # MODIFY: Add REST endpoints
├── handlers.go                # POST /servers, DELETE /servers/{name}
└── routes.go                  # Register new routes

docs/                          # UPDATE: Documentation
└── cli-management-commands.md # Add examples for add/remove
```

**Structure Decision**: Extend existing files. No new packages needed - CLI commands go in existing upstream_cmd.go, HTTP endpoints in existing httpapi handlers.

## Complexity Tracking

> No violations - all gates passed. No complexity justification needed.

## Implementation Approach

### Phase 1: CLI Command Structure

1. Add `upstreamAddCmd` with flag parsing:
   - Positional: `<name>` (required), `<url>` (optional for HTTP)
   - `--` separator for stdio command
   - `--transport http|stdio` (auto-detect if not specified)
   - `--env KEY=value` (repeatable)
   - `--header "Name: value"` (repeatable)
   - `--working-dir <path>`
   - `--if-not-exists`

2. Add `upstreamAddJSONCmd`:
   - Positional: `<name>`, `<json>`
   - Validate JSON structure

3. Add `upstreamRemoveCmd`:
   - Positional: `<name>`
   - `--yes` to skip confirmation
   - `--if-exists` for idempotent removal

### Phase 2: HTTP API Integration

1. Add HTTP endpoints for daemon mode:
   - `POST /api/v1/servers` - Add server
   - `DELETE /api/v1/servers/{name}` - Remove server

2. Add cliclient methods:
   - `AddServer(ctx, config)` - POST to /servers
   - `RemoveServer(ctx, name, force)` - DELETE /servers/{name}

### Phase 3: Validation & Output

1. Server name validation (alphanumeric, hyphens, underscores, 1-64 chars)
2. URL validation for HTTP servers
3. ENV format validation (KEY=value)
4. Output using spec 014 formatters

## Dependencies

- **Spec 014**: CLI Output Formatting (for consistent output) - ✅ Merged
- **Existing Components**:
  - `internal/management/service.go`: Management service
  - `internal/storage/manager.go`: AddUpstream, RemoveUpstream
  - `internal/upstream/manager.go`: AddServer, RemoveServer
  - `cmd/mcpproxy/upstream_cmd.go`: Existing upstream commands

## Next Steps

Proceed with implementation in this order:
1. T001-T010: CLI command parsing and validation
2. T011-T015: HTTP API endpoints
3. T016-T020: cliclient methods
4. T021-T025: Integration and testing
