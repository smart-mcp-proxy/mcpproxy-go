# Implementation Plan: CLI Output Formatting System

**Branch**: `014-cli-output-formatting` | **Date**: 2025-12-26 | **Spec**: [spec.md](spec.md)
**Input**: Feature specification from `/specs/014-cli-output-formatting/spec.md`

**Note**: This template is filled in by the `/speckit.plan` command. See `.specify/templates/commands/plan.md` for the execution workflow.

## Summary

Implement a unified output formatting system for mcpproxy CLI commands. The system provides:
1. Global `-o/--output` flag with table, json, yaml formats
2. `--json` convenience alias for `-o json`
3. `--help-json` for machine-readable command discovery
4. Structured error output with recovery hints
5. `MCPPROXY_OUTPUT` environment variable for default format

Current state: Commands like `upstream list`, `tools list`, `doctor` already have ad-hoc `-o json` implementations with duplicated formatting logic. This spec consolidates them into a reusable `internal/cli/output/` package.

## Technical Context

**Language/Version**: Go 1.24 (toolchain go1.24.10)
**Primary Dependencies**: Cobra CLI framework, encoding/json, gopkg.in/yaml.v3
**Storage**: N/A (CLI output only)
**Testing**: go test, ./scripts/test-api-e2e.sh, mcp-eval scenarios
**Target Platform**: macOS (darwin), Linux, Windows
**Project Type**: CLI application (single binary)
**Performance Goals**: --help-json returns in <100ms, formatting adds <10ms overhead
**Constraints**: No external dependencies beyond existing yaml.v3, maintain backward compatibility
**Scale/Scope**: ~15 CLI commands to update, 4 formatter implementations

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Status | Notes |
|-----------|--------|-------|
| I. Performance at Scale | ✅ PASS | Formatting is O(n) where n = output rows, negligible overhead |
| II. Actor-Based Concurrency | ✅ PASS | Formatters are stateless, no locks required |
| III. Configuration-Driven | ✅ PASS | MCPPROXY_OUTPUT env var for default format |
| IV. Security by Default | ✅ PASS | No security implications for output formatting |
| V. TDD | ✅ PASS | Unit tests for formatters, E2E tests for commands |
| VI. Documentation Hygiene | ✅ PASS | Update docs/ with CLI output guide |
| Separation of Concerns | ✅ PASS | `internal/cli/output/` is presentation layer |
| Event-Driven Updates | N/A | Output formatting is synchronous, not event-driven |
| DDD Layering | ✅ PASS | Output formatters are presentation layer |
| Upstream Client Modularity | N/A | Not applicable to CLI output |

**Gate Result**: ✅ PASS - No violations, proceed to Phase 0

## Project Structure

### Documentation (this feature)

```text
specs/[###-feature]/
├── plan.md              # This file (/speckit.plan command output)
├── research.md          # Phase 0 output (/speckit.plan command)
├── data-model.md        # Phase 1 output (/speckit.plan command)
├── quickstart.md        # Phase 1 output (/speckit.plan command)
├── contracts/           # Phase 1 output (/speckit.plan command)
└── tasks.md             # Phase 2 output (/speckit.tasks command - NOT created by /speckit.plan)
```

### Source Code (repository root)

```text
internal/cli/output/           # NEW: Output formatting package
├── formatter.go               # OutputFormatter interface + factory
├── table.go                   # TableFormatter implementation
├── json.go                    # JSONFormatter implementation
├── yaml.go                    # YAMLFormatter implementation
├── error.go                   # StructuredError type + ErrorFormatter
├── help.go                    # HelpInfo type + HelpFormatter for --help-json
└── formatter_test.go          # Unit tests for all formatters

cmd/mcpproxy/                  # MODIFY: Update existing commands
├── main.go                    # Add global --output flag, --json alias
├── upstream_cmd.go            # Migrate to use OutputFormatter
├── tools_cmd.go               # Migrate to use OutputFormatter
├── doctor_cmd.go              # Migrate to use OutputFormatter
├── call_cmd.go                # Migrate to use OutputFormatter
├── auth_cmd.go                # Migrate to use OutputFormatter
└── secrets_cmd.go             # Migrate to use OutputFormatter

docs/                          # UPDATE: Documentation
└── cli-output-formatting.md   # NEW: CLI output guide for agents
```

**Structure Decision**: Single project structure. New `internal/cli/output/` package follows existing pattern of `internal/` subdirectories. All formatting logic centralized there, commands delegate to formatters.

## Complexity Tracking

> No violations - all gates passed. No complexity justification needed.

## Phase 0 Artifacts

- [research.md](research.md) - Technical decisions and alternatives

## Phase 1 Artifacts

- [data-model.md](data-model.md) - Core types and interfaces
- [contracts/cli-output-schema.json](contracts/cli-output-schema.json) - JSON schemas for output
- [quickstart.md](quickstart.md) - Implementation guide

## Next Steps

Run `/speckit.tasks` to generate the implementation task list.
