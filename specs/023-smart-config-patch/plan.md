# Implementation Plan: Smart Config Patching

**Branch**: `023-smart-config-patch` | **Date**: 2026-01-10 | **Spec**: [spec.md](./spec.md)
**Input**: Feature specification from `/specs/023-smart-config-patch/spec.md`
**Related Issues**: #239, #240

## Summary

Configuration update operations currently cause data loss by overwriting entire config objects instead of intelligently merging changes. This implementation adds deep merge semantics to preserve unmodified fields (especially isolation, OAuth, env, headers) during patch/update/quarantine operations.

**Root Cause Identified**: `async_ops.go:saveServerSync()` is missing the `Isolation` field, and the `SaveConfiguration()` flow overwrites config from storage without preserving fields.

## Technical Context

**Language/Version**: Go 1.24 (toolchain go1.24.10)
**Primary Dependencies**: encoding/json, existing config package, BBolt storage
**Storage**: BBolt database (`~/.mcpproxy/config.db`) - existing
**Testing**: go test, ./scripts/test-api-e2e.sh
**Target Platform**: macOS, Linux, Windows (cross-platform CLI + tray app)
**Project Type**: Single Go module with CLI + tray binaries
**Performance Goals**: Merge operations in <1ms, no blocking on config updates
**Constraints**: Must maintain backward compatibility with existing config files
**Scale/Scope**: Affects all config update paths (MCP tool, REST API, CLI, tray)

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Status | Evidence |
|-----------|--------|----------|
| **I. Performance at Scale** | ✅ PASS | Merge operations are O(n) on fields, <1ms expected |
| **II. Actor-Based Concurrency** | ✅ PASS | No new locks; uses existing storage mutex patterns |
| **III. Configuration-Driven Architecture** | ✅ PASS | Preserves config file as source of truth; improves config handling |
| **IV. Security by Default** | ✅ PASS | Quarantine flow preserved; no security regression |
| **V. Test-Driven Development** | ✅ PASS | Unit tests for merge utility, E2E tests for preservation |
| **VI. Documentation Hygiene** | ✅ PASS | CLAUDE.md update if needed; examples.md already created |

**Architecture Constraints**:

| Constraint | Status | Evidence |
|------------|--------|----------|
| Core + Tray Split | ✅ PASS | Fix is in core; tray unaffected |
| Event-Driven Updates | ✅ PASS | No change to event flow |
| DDD Layering | ✅ PASS | Merge utility in config package (domain layer) |
| Upstream Client Modularity | ✅ PASS | No change to upstream client layers |

## Project Structure

### Documentation (this feature)

```text
specs/023-smart-config-patch/
├── spec.md              # Feature specification
├── plan.md              # This file
├── research.md          # Root cause analysis, implementation decisions
├── data-model.md        # Entity definitions, merge semantics
├── quickstart.md        # Usage examples for developers and LLMs
├── examples.md          # Comprehensive merge examples (created earlier)
├── contracts/
│   ├── merge-api.md     # Internal Go API contract
│   └── mcp-tool-schema.json  # MCP tool schema with examples
├── checklists/
│   └── requirements.md  # Spec quality checklist
└── tasks.md             # Implementation tasks (created by /speckit.tasks)
```

### Source Code (repository root)

```text
internal/
├── config/
│   ├── config.go        # Existing ServerConfig, IsolationConfig (unchanged)
│   └── merge.go         # NEW: MergeServerConfig(), ConfigDiff types
├── storage/
│   └── async_ops.go     # FIX: Add Isolation field to saveServerSync()
├── server/
│   └── mcp.go           # UPDATE: Use merge in handlePatchUpstream(), handleUpdateUpstream()
├── runtime/
│   └── lifecycle.go     # REVIEW: SaveConfiguration() flow
└── httpapi/
    └── server.go        # REVIEW: Quarantine handlers (may not need changes)

tests/ (conceptual - tests live alongside code in Go)
├── internal/config/merge_test.go      # NEW: Unit tests for merge utility
├── internal/server/mcp_test.go        # UPDATE: Add patch preservation tests
└── scripts/test-api-e2e.sh            # E2E: Add isolation preservation tests
```

**Structure Decision**: Single Go module structure. New code in `internal/config/merge.go`; fixes in `internal/storage/async_ops.go`. Follows existing DDD layering.

## Complexity Tracking

> No violations. Implementation follows existing patterns.

| Item | Justification |
|------|---------------|
| New merge.go file | Required for centralized merge logic; follows single responsibility |
| ConfigDiff type | Needed for audit trail (FR-006); simple struct |

## Implementation Phases

### Phase 1: Fix Immediate Bug (P1)

1. Add `Isolation` field to `saveServerSync()` in `async_ops.go`
2. Also add `OAuth` field if missing (defensive check)
3. Add unit test to verify all ServerConfig fields are copied

**Files Changed**: `internal/storage/async_ops.go`
**Estimated Effort**: Small

### Phase 2: Implement Merge Utility (P1)

1. Create `internal/config/merge.go`:
   - `MergeServerConfig(base, patch, opts) (*ServerConfig, *ConfigDiff, error)`
   - `MergeMap(dst, src map[string]string) map[string]string`
   - `MergeIsolationConfig(base, patch *IsolationConfig) *IsolationConfig`
   - `MergeOAuthConfig(base, patch *OAuthConfig) *OAuthConfig`
2. Implement merge semantics per data-model.md
3. Add comprehensive unit tests

**Files Changed**: `internal/config/merge.go` (new), `internal/config/merge_test.go` (new)
**Estimated Effort**: Medium

### Phase 3: Update MCP Tool Handlers (P1)

1. Update `handlePatchUpstream()` to use `MergeServerConfig()`
2. Update `handleUpdateUpstream()` to use `MergeServerConfig()`
3. Add response diff for LLM transparency
4. Update tool description with merge semantics

**Files Changed**: `internal/server/mcp.go`
**Estimated Effort**: Medium

### Phase 4: Update REST API & Review Other Paths (P2)

1. Review quarantine handlers - verify they don't need changes (quarantineServerSync already correct)
2. Review enable/disable handlers
3. Update any other config update paths found

**Files Changed**: `internal/httpapi/server.go` (review), `internal/runtime/lifecycle.go` (review)
**Estimated Effort**: Small

### Phase 5: Testing & Documentation (P2)

1. Add E2E tests for isolation preservation:
   - Test unquarantine preserves isolation
   - Test patch preserves isolation
   - Test enable/disable preserves isolation
2. Update CLAUDE.md if API behavior documentation needs changes
3. Update tool descriptions in MCP server

**Files Changed**: E2E test files, potentially CLAUDE.md
**Estimated Effort**: Medium

## Risk Assessment

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| Breaking existing config files | Low | High | Merge is additive; existing fields preserved |
| Missing fields in future | Medium | Medium | Add test that verifies all ServerConfig fields |
| Concurrent modification | Low | Medium | Existing mutex patterns sufficient |
| LLM confusion about merge semantics | Medium | Low | Updated tool description with examples |

## Success Criteria (from Spec)

- [ ] **SC-001**: Unquarantining preserves 100% of original config fields
- [ ] **SC-002**: Patch modifying one field results in exactly one change (+ timestamp)
- [ ] **SC-003**: All existing E2E tests pass
- [ ] **SC-004**: Config round-trip preserves all fields except modified + timestamp
- [ ] **SC-005**: No user reports of lost config data
- [ ] **SC-006**: Config diff logs available for audit

## Dependencies

None - this is a bug fix with no external dependencies.

## Active Technologies (for CLAUDE.md)

- Go 1.24 (toolchain go1.24.10) + encoding/json (merge implementation)
- BBolt database (existing - no changes)
