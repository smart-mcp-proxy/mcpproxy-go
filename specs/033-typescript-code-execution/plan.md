# Implementation Plan: TypeScript Code Execution Support

**Branch**: `033-typescript-code-execution` | **Date**: 2026-03-10 | **Spec**: [spec.md](./spec.md)
**Input**: Feature specification from `/specs/033-typescript-code-execution/spec.md`

## Summary

Add TypeScript language support to MCPProxy's code_execution feature by integrating esbuild's Go API for fast type-stripping transpilation. When users specify `language: "typescript"`, the submitted code is transpiled to JavaScript before execution in the existing goja sandbox. This adds a `language` parameter to the MCP tool schema, REST API, and CLI command while maintaining full backward compatibility with existing JavaScript code execution.

## Technical Context

**Language/Version**: Go 1.24 (toolchain go1.24.10)
**Primary Dependencies**: `github.com/dop251/goja` (existing JS sandbox), `github.com/evanw/esbuild` (new - TypeScript transpilation), `github.com/mark3labs/mcp-go` (MCP protocol), `github.com/spf13/cobra` (CLI)
**Storage**: N/A (no new storage requirements)
**Testing**: `go test -race` (unit and integration tests)
**Target Platform**: macOS, Linux, Windows (cross-platform Go binary)
**Project Type**: Single Go project with modular internal packages
**Performance Goals**: TypeScript transpilation overhead < 5ms for code under 10KB
**Constraints**: Transpiled output must be ES5.1+ compatible for goja sandbox; full backward compatibility with existing JavaScript execution
**Scale/Scope**: Single feature addition touching 6-8 files across 4 packages

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Status | Notes |
|-----------|--------|-------|
| I. Performance at Scale | PASS | esbuild Go API transpiles in <1ms; well under 5ms target. No impact on tool indexing or search. |
| II. Actor-Based Concurrency | PASS | Transpilation is synchronous and stateless - runs within existing execution goroutine. No new locks or shared state needed. |
| III. Configuration-Driven Architecture | PASS | No new config options needed. TypeScript support is always available when code_execution is enabled. The `language` parameter is per-request, not per-config. |
| IV. Security by Default | PASS | esbuild only strips types - no code generation beyond what TypeScript syntax requires (enums, namespaces). Output executes in the same goja sandbox with all existing restrictions. |
| V. Test-Driven Development (TDD) | PASS | Comprehensive unit tests planned for transpilation layer, integration tests for MCP tool and REST API. |
| VI. Documentation Hygiene | PASS | Updates planned for CLAUDE.md, code_execution docs, and API reference. |
| Separation of Concerns | PASS | Transpilation layer is isolated in `internal/jsruntime/typescript.go`. No impact on core/tray split. |
| Event-Driven Updates | N/A | No state changes or events involved. |
| DDD Layering | PASS | Transpilation logic stays in domain layer (`internal/jsruntime`), API changes in presentation layer (`internal/httpapi`, `internal/server`). |
| Upstream Client Modularity | N/A | No changes to upstream client layers. |

**Gate result**: PASS - no violations.

## Project Structure

### Documentation (this feature)

```text
specs/033-typescript-code-execution/
├── plan.md              # This file
├── research.md          # Phase 0 output
├── data-model.md        # Phase 1 output
├── quickstart.md        # Phase 1 output
├── contracts/           # Phase 1 output
└── tasks.md             # Phase 2 output (created by /speckit.tasks)
```

### Source Code (repository root)

```text
internal/jsruntime/
├── runtime.go           # MODIFY - Add language parameter to Execute(), call transpiler
├── errors.go            # MODIFY - Add ErrorCodeTranspileError error code
├── typescript.go        # NEW - TypeScript transpilation via esbuild
├── typescript_test.go   # NEW - Unit tests for transpilation
├── runtime_test.go      # MODIFY - Add TypeScript execution tests
├── pool.go              # NO CHANGE
└── pool_test.go         # NO CHANGE

internal/server/
├── mcp.go               # MODIFY - Add 'language' parameter to code_execution tool schema
├── mcp_code_execution.go # MODIFY - Parse and pass language parameter
└── mcp_code_execution_test.go # MODIFY - Add TypeScript test cases

internal/httpapi/
├── code_exec.go         # MODIFY - Add 'language' field to CodeExecRequest
└── code_exec_test.go    # MODIFY - Add TypeScript test cases

cmd/mcpproxy/
└── code_cmd.go          # MODIFY - Add --language flag

docs/code_execution/
├── overview.md          # MODIFY - Document TypeScript support
└── api-reference.md     # MODIFY - Document language parameter
```

**Structure Decision**: This is a focused feature addition to existing packages. No new packages or directories needed. The transpilation layer (`typescript.go`) is a new file in the existing `internal/jsruntime/` package, keeping related code together.

## Complexity Tracking

No constitution violations to justify. The feature adds a single new dependency (esbuild) and a thin transpilation layer with no new abstractions, patterns, or architectural changes.
