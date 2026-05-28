# Implementation Plan: Output-Schema Validation for Proxied Tool Calls

**Branch**: `056-output-schema-validation` | **Date**: 2026-05-25 | **Spec**: [spec.md](./spec.md)
**Input**: Feature specification from `/specs/056-output-schema-validation/spec.md`

## Summary

When an upstream tool declares an `outputSchema`, mcpproxy validates the structured portion of every proxied tool-call response against that schema before forwarding it to the agent. A new pure package `internal/outputvalidation` holds a `Validator` with a per-tool compiled-schema cache; the existing single response chokepoint `forwardContentResult` (`internal/server/content_forward.go`) gains an optional validator hook that runs cheap size/depth guards first, then schema validation, never mutating `StructuredContent` on the success path. Violations are turned into a hard block (strict mode) or a forward-with-audit (warn mode, the backward-compatible default). The tool's output schema is captured at discovery (`internal/upstream/core/client.go`) and persisted on `config.ToolMetadata` alongside the existing `ParamsJSON` input schema.

## Technical Context

**Language/Version**: Go 1.24 (toolchain go1.24.10)
**Primary Dependencies**: `github.com/santhosh-tekuri/jsonschema/v6` v6.0.2 (already in module graph as indirect — promote to direct); `github.com/mark3labs/mcp-go` v0.54.0 (existing, provides `Tool.RawOutputSchema`/`OutputSchema` and `CallToolResult.StructuredContent`); `go.uber.org/zap` (existing logging)
**Storage**: No new BBolt buckets. Output schema persisted as a JSON string on the existing `config.ToolMetadata` (in-config / index path), mirroring `ParamsJSON`. Validation config lives in `mcp_config.json` (Constitution III).
**Testing**: `go test` unit tests (new `internal/outputvalidation` package + `content_forward` hook); E2E via `scripts/test-api-e2e.sh` extended with a stub upstream declaring an output schema; `go test -race` on touched packages
**Target Platform**: Linux/macOS/Windows core server; personal + server editions identical (no build tags)
**Project Type**: single (Go backend; no frontend work in Track A)
**Performance Goals**: Validation no-op path (no schema / mode=off) adds negligible per-call overhead (Constitution I — no regression on 1000-tool routing); compiled schemas cached so steady-state validation does not recompile
**Constraints**: Never mutate the forwarded `StructuredContent` on success (FR-A3); guards bound cost before validation (FR-A6); uncompilable schema degrades to no-op, never blocks traffic (FR-A9)
**Scale/Scope**: Hot path runs once per proxied `call_tool_*`; cache keyed by `server:tool` + schema hash, bounded by number of distinct tools (≤ ~1000 per Constitution I)

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Status | Notes |
|-----------|--------|-------|
| I. Performance at Scale | ✅ PASS | Guards run before validation; compiled-schema cache avoids recompilation; no-op fast path for the common (no-schema) case. No new locking on the hot path beyond a `sync.Map` read. |
| II. Actor-Based Concurrency | ✅ PASS | Validator is stateless apart from a `sync.Map` cache (read-mostly). No new goroutines; runs inline on the existing call path. `sync.Map` is the idiomatic choice for a read-mostly cache (benchmark-justified exception per principle II). |
| III. Configuration-Driven Architecture | ✅ PASS | New `output_validation` block in `mcp_config.json` with sensible defaults (mode=warn); hot-reloadable via existing config watcher; no tray-side state. |
| IV. Security by Default | ✅ PASS | This **is** a security feature. Default `warn` mode is the deliberate backward-compatible posture (audit without breaking working agents); operators escalate to `strict`. Failures logged with full transparency via existing activity log. |
| V. Test-Driven Development | ✅ PASS | Red-green-refactor: unit tests for the validator + guards written first; E2E stub-server test for the proxy path; `golangci-lint` clean. |
| VI. Documentation Hygiene | ✅ PASS | New `docs/features/output-schema-validation.md`; config reference + CLAUDE.md note (mind the 40k char gate — keep the CLAUDE.md delta to one line). |

**Gate result**: PASS — no violations, Complexity Tracking not required.

## Project Structure

### Documentation (this feature)

```text
specs/056-output-schema-validation/
├── plan.md              # This file
├── spec.md              # Feature spec
├── research.md          # Phase 0 output
├── data-model.md        # Phase 1 output
├── quickstart.md        # Phase 1 output
├── contracts/           # Phase 1 output (config schema + validator interface)
├── checklists/
│   └── requirements.md
└── tasks.md             # Phase 2 (/speckit.tasks)
```

### Source Code (repository root)

```text
internal/
├── outputvalidation/                 # NEW pure package (no server deps)
│   ├── validator.go                  # Validator: Validate(toolKey, schemaJSON, structured) -> Verdict; compiled-schema sync.Map cache; size/depth guards
│   ├── validator_test.go             # Unit: conforming/violating/missing-schema/uncompilable/guard breaches
│   ├── guards.go                     # byteSize + nestingDepth guards over a decoded value
│   └── guards_test.go
├── config/
│   └── config.go                     # ADD: OutputValidation config struct + defaults; ADD ToolMetadata.OutputSchemaJSON
├── upstream/core/
│   └── client.go                     # ADD: map mcp.Tool.RawOutputSchema/OutputSchema -> ToolMetadata.OutputSchemaJSON (~line 284)
└── server/
    ├── content_forward.go            # ADD: optional validator hook + Verdict handling (block vs tag) without mutating StructuredContent
    ├── content_forward_test.go       # Unit: success passthrough byte-identical; strict block; warn forward+verdict; IsError skip
    └── mcp.go                         # WIRE: look up captured output schema for server:tool; pass validator + mode into forwardContentResult at :1794 / :2166; emitActivityPolicyDecision on failure

e2e/ or scripts/
└── test-api-e2e.sh                   # EXTEND: stub upstream tool with outputSchema; assert strict-block + warn-forward + activity record
```

**Structure Decision**: Single Go project. The validation logic is isolated in a new dependency-free package `internal/outputvalidation` (DDD domain layer per Constitution principle on layering), wired into the existing infrastructure (`content_forward.go` + `mcp.go`) through a narrow interface so the validator stays pure and unit-testable. No frontend changes in Track A.

## Complexity Tracking

> No constitution violations — section intentionally empty.
