# Implementation Plan: Structured Server State

**Branch**: `013-structured-server-state` | **Date**: 2025-12-16 | **Spec**: [spec.md](./spec.md)
**Depends On**: #192 (Unified Health Status) - merged to main

## Summary

Add structured state objects (`OAuthState`, `ConnectionState`) to Server, refactor `Doctor()` to aggregate from `server.Health`, and consolidate Dashboard UI to remove duplicate diagnostics section.

## Technical Context

**Language/Version**: Go 1.24.0
**Primary Dependencies**: mcp-go (MCP protocol), zap (logging), chi (HTTP router), Vue 3/TypeScript (frontend)
**Testing**: go test, ./scripts/test-api-e2e.sh
**Constraints**: Backwards compatibility with existing flat fields

## Constitution Check

| Principle | Status | Notes |
|-----------|--------|-------|
| I. Performance at Scale | PASS | State objects populated on request; no new queries |
| II. Actor-Based Concurrency | PASS | Uses existing state from ConnectionInfo |
| III. Configuration-Driven | PASS | No new configuration required |
| V. Test-Driven Development | PASS | Unit tests for new types; E2E for backwards compat |

**Gate Result**: PASS

## Project Structure

### Documentation

```text
specs/013-structured-server-state/
├── plan.md              # This file
├── spec.md              # Feature specification
├── research.md          # Research findings
├── data-model.md        # Type definitions
└── contracts/           # API changes
```

### Source Code Changes

```text
# Backend (Go)
internal/
├── contracts/
│   └── types.go          # Add OAuthState, ConnectionState types
├── management/
│   └── diagnostics.go    # Refactor Doctor() to use Health
└── upstream/
    └── manager.go        # Populate structured state objects

# Frontend (Vue)
frontend/src/
├── types/api.ts          # Add OAuthState, ConnectionState interfaces
└── views/
    └── Dashboard.vue     # Remove duplicate diagnostics section

# Tests
internal/
└── management/diagnostics_test.go # Update for aggregation
```

## Implementation Phases

### Phase 1: Structured State Types

1. Add `OAuthState` type to `internal/contracts/types.go`
2. Add `ConnectionState` type to `internal/contracts/types.go`
3. Add fields to `Server` struct with `omitempty` tags
4. Add TypeScript interfaces to `frontend/src/types/api.ts`

### Phase 2: State Population

1. Update `GetAllServersWithStatus()` in `internal/upstream/manager.go`
2. Populate `ConnectionState` from `ConnectionInfo`
3. Populate `OAuthState` from storage token data + ConnectionInfo OAuth fields

### Phase 3: Doctor() Refactoring

1. Refactor `Doctor()` in `internal/management/diagnostics.go`
2. Iterate servers, read `server.Health.Action`
3. Map to existing categories: login→OAuthRequired, restart→UpstreamErrors
4. Update tests in `diagnostics_test.go`

### Phase 4: UI Consolidation

1. Remove diagnostics section from `Dashboard.vue`
2. Enhance "Servers Needing Attention" banner with aggregated counts
3. Remove unused `loadDiagnostics()` and related computed properties
