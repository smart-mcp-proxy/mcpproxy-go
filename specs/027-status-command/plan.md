# Implementation Plan: Status Command

**Branch**: `027-status-command` | **Date**: 2026-03-02 | **Spec**: [spec.md](spec.md)
**Input**: Feature specification from `/specs/027-status-command/spec.md`

## Summary

Add a `mcpproxy status` CLI command that provides a unified view of MCPProxy state: running/not-running detection, listen address, masked API key, Web UI URL, server counts, and uptime. Supports `--show-key`, `--web-url`, and `--reset-key` flags. Operates in dual mode: live data via socket when daemon is running, config-only when not.

## Technical Context

**Language/Version**: Go 1.24 (toolchain go1.24.10)
**Primary Dependencies**: Cobra (CLI), Chi router (HTTP), Zap (logging), existing cliclient, socket detection, config loader
**Storage**: `~/.mcpproxy/mcp_config.json` (config file), `~/.mcpproxy/config.db` (BBolt - not directly used)
**Testing**: `go test`, `./scripts/test-api-e2e.sh`
**Target Platform**: macOS, Linux, Windows (cross-platform CLI)
**Project Type**: Single Go binary (CLI commands in `cmd/mcpproxy/`)
**Performance Goals**: Command completes in <1s (socket query + formatting)
**Constraints**: Must work without daemon running (config-only mode)
**Scale/Scope**: 1 new CLI command file, 1 new cliclient method, 1 new docs page, sidebar update

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Status | Notes |
|-----------|--------|-------|
| I. Performance at Scale | PASS | Status command is a simple query, no indexing or search |
| II. Actor-Based Concurrency | PASS | No concurrency needed - single CLI request/response |
| III. Configuration-Driven Architecture | PASS | Reads from config file, key reset writes to config with hot-reload |
| IV. Security by Default | PASS | API key masked by default, `--show-key` is opt-in |
| V. Test-Driven Development | PASS | Unit tests for masking/URL/reset, integration tests for dual mode |
| VI. Documentation Hygiene | PASS | New docs page for Docusaurus, CLAUDE.md update |
| Core+Tray Split | PASS | CLI command only, no tray changes |
| Event-Driven Updates | PASS | Key reset relies on existing file watcher hot-reload |
| DDD Layering | PASS | CLI → cliclient → httpapi (presentation layer only) |
| Upstream Client Modularity | N/A | No upstream client changes |

No violations. No complexity tracking needed.

## Project Structure

### Documentation (this feature)

```text
specs/027-status-command/
├── plan.md              # This file
├── research.md          # Phase 0 output
├── spec.md              # Feature specification
├── data-model.md        # Phase 1 output
├── quickstart.md        # Phase 1 output
├── contracts/           # Phase 1 output
│   └── status-response.json
├── checklists/
│   └── requirements.md  # Quality checklist
└── tasks.md             # Phase 2 output (from /speckit.tasks)
```

### Source Code (repository root)

```text
cmd/mcpproxy/
├── status_cmd.go          # NEW: Status command implementation
├── status_cmd_test.go     # NEW: Unit tests for status command
└── main.go                # MODIFY: Register status command

internal/cliclient/
└── client.go              # MODIFY: Add GetStatus() method

docs/cli/
└── status-command.md      # NEW: Docusaurus documentation

website/
└── sidebars.js            # MODIFY: Add status-command to CLI nav
```

**Structure Decision**: Pure CLI feature - all new code in `cmd/mcpproxy/` (command) and `internal/cliclient/` (client method). Follows existing patterns from `upstream_cmd.go`.
