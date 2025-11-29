# Implementation Plan: Complete OpenAPI Documentation for REST API Endpoints

**Branch**: `001-oas-endpoint-documentation` | **Date**: 2025-11-28 | **Spec**: [spec.md](./spec.md)
**Input**: Feature specification from `/specs/001-oas-endpoint-documentation/spec.md`

**Note**: This template is filled in by the `/speckit.plan` command. See `.specify/templates/commands/plan.md` for the execution workflow.

## Summary

Add complete OpenAPI 3.1 documentation for 19 undocumented REST API endpoints (configuration management, secrets, tool-calls, sessions, registries, code execution, SSE events) and fix inconsistent authentication security markers in the existing OAS specification. Create automated verification script and CI check to prevent future documentation drift.

**Technical Approach**: Annotate existing Go handler functions in `internal/httpapi/server.go` with swaggo/swag comments, define request/response schema components, regenerate `oas/swagger.yaml` using `make swagger`, and add OAS coverage verification to CI pipeline.

## Technical Context

**Language/Version**: Go 1.24.0
**Primary Dependencies**:
- swaggo/swag v2.0.0-rc4 (OpenAPI annotation tool)
- go-chi/chi/v5 (HTTP router)
- Existing OAS infrastructure in `oas/` directory

**Storage**: N/A (documentation-only feature)
**Testing**:
- Manual verification via Swagger UI at `/swagger/`
- Automated OAS coverage verification script
- CI check using `make swagger-verify`

**Target Platform**: Cross-platform (macOS, Linux, Windows) - documentation generation
**Project Type**: Single Go project with backend HTTP API
**Performance Goals**:
- OAS verification script runs in <5 seconds
- Swagger UI renders all endpoints without performance degradation

**Constraints**:
- Must use existing swaggo/swag v2.0.0-rc4 (OpenAPI 3.1 support)
- Cannot modify API behavior (documentation-only changes)
- Must maintain existing Makefile swagger targets

**Scale/Scope**:
- 19 undocumented endpoints to document
- ~23 currently documented endpoints (maintain existing documentation)
- Target 100% REST endpoint coverage

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

### VI. Documentation Hygiene - PASS ✅

**Rule**: After adding a feature or fixing a bug, developers MUST update:
- Tests (unit, integration, E2E as applicable)
- `CLAUDE.md` (if architecture or commands change)
- `README.md` (if user-facing behavior changes)
- Code comments (for complex logic or non-obvious decisions)
- API documentation (if REST endpoints or MCP protocol changes)

**Assessment**: This feature directly addresses Principle VI by completing API documentation for 19 undocumented endpoints. It also adds automated OAS coverage verification to enforce ongoing documentation hygiene. **No violation**.

### V. Test-Driven Development (TDD) - PASS ✅

**Rule**: All features and bug fixes MUST include tests.

**Assessment**: Documentation-only feature. Tests consist of:
1. Manual verification in Swagger UI (user acceptance)
2. Automated OAS coverage script (regression prevention)
3. CI verification using existing `make swagger-verify` target

**No code logic changes** → Unit tests not required. Automated verification provides sufficient coverage. **No violation**.

### IV. Security by Default - PASS ✅

**Rule**: localhost-only binding MUST be the default, API key authentication MUST be enabled by default.

**Assessment**: This feature fixes authentication documentation to accurately reflect existing security implementation (API key required for `/api/v1/*` endpoints, no auth for health endpoints, SSE supports both header and query parameter auth). Does not modify security implementation, only documents it correctly. **No violation**.

### III. Configuration-Driven Architecture - N/A

No configuration changes required for documentation.

### II. Actor-Based Concurrency - N/A

No concurrency patterns affected by documentation.

### I. Performance at Scale - N/A

Documentation does not affect runtime performance. OAS verification script performance goal (<5s) documented in constraints.

**GATE STATUS**: ✅ **PASS** - All applicable principles satisfied. Proceed with Phase 0 research.

## Project Structure

### Documentation (this feature)

```text
specs/001-oas-endpoint-documentation/
├── plan.md              # This file (/speckit.plan command output)
├── research.md          # Phase 0 output (/speckit.plan command)
├── data-model.md        # Phase 1 output (schema definitions)
├── quickstart.md        # Phase 1 output (developer guide)
├── contracts/           # Phase 1 output (endpoint specifications)
│   ├── config-management.yaml
│   ├── secrets-management.yaml
│   ├── tool-calls.yaml
│   ├── sessions.yaml
│   ├── registries.yaml
│   ├── code-execution.yaml
│   └── sse-events.yaml
└── tasks.md             # Phase 2 output (/speckit.tasks command - NOT created by /speckit.plan)
```

### Source Code (repository root)

```text
internal/httpapi/
├── server.go           # Add swag annotations to existing handlers (lines 1474-2350)
└── code_exec.go        # Add swag annotations for code execution endpoint

oas/
├── swagger.yaml        # Regenerated by `make swagger` (auto-updated)
├── docs.go             # Regenerated by `make swagger` (auto-updated)
└── register_compat.go  # Existing file (no changes)

scripts/
└── verify-oas-coverage.sh  # NEW: Automated coverage verification

.github/workflows/
└── verify-oas.yml      # NEW: CI check for OAS coverage (or extend existing workflow)

Makefile                # Existing (no changes to swagger targets)

docs/
└── oas-coverage-report.md  # NEW: Documentation on verification process
```

**Structure Decision**: Single Go project structure maintained. All OAS annotations added as swag comments inline with existing handler functions in `internal/httpapi/`. No new directories required for OAS generation (existing `oas/` directory used). New verification script in `scripts/` and CI workflow in `.github/workflows/` for automation.

## Complexity Tracking

> **Fill ONLY if Constitution Check has violations that must be justified**

**No violations identified** - this section is intentionally left empty per template instructions.
