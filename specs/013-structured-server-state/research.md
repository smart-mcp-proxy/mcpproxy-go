# Research: Structured Server State

**Feature**: 013-structured-server-state
**Date**: 2025-12-13
**Updated**: 2025-12-16

## Overview

This document captures research findings for implementing structured server state objects.

**Update (2025-12-16)**: The Unified Health Status (#192) has been merged to main. This research is updated to reflect the current codebase state.

## Research Tasks

### 1. Existing State Management Patterns

**Question**: How does the codebase currently handle server state?

**Findings**:
- `internal/upstream/types/types.go` defines `ConnectionInfo` and `StateManager`
- `ConnectionInfo` already contains: State, LastError, RetryCount, LastRetryTime, ServerName, ServerVersion, LastOAuthAttempt, OAuthRetryCount, IsOAuthError
- `StateManager` provides thread-safe state transitions with callbacks
- State changes emit events via `StateChangeNotifier` in `internal/upstream/notifications.go`

**Decision**: Leverage existing `ConnectionInfo` as the source for `ConnectionState`. Add `OAuthState` as new struct.

**Rationale**: Minimizes duplication; `ConnectionInfo` already has rich retry and timing data.

**Alternatives Considered**:
- Create entirely new state tracking: Rejected (duplicates existing work)
- Expose `ConnectionInfo` directly: Rejected (internal type with Go-specific fields like `error`)

---

### 2. Health Calculation Pattern ✅ IMPLEMENTED

**Question**: How is health currently calculated?

**Findings**:
- `internal/health/calculator.go` implements `CalculateHealth(input HealthCalculatorInput, cfg *HealthCalculatorConfig)`
- Input struct contains flat fields extracted from server state
- Calculation follows priority: admin state > connection state > OAuth state > healthy
- Returns `*contracts.HealthStatus` with Level, AdminState, Summary, Detail, Action

**Status**: ✅ IMPLEMENTED in #192. The health calculator is fully functional.

**Remaining Work**: Optionally update `HealthCalculatorInput` to accept structured state objects as source (deferred - current flat field approach works).

---

### 3. API Serialization Pattern (Partial)

**Question**: How are server objects serialized for API responses?

**Findings**:
- `internal/contracts/types.go` defines `Server` struct with JSON tags
- `internal/upstream/manager.go:GetAllServersWithStatus()` builds server maps
- Flat fields are populated from `ConnectionInfo` and OAuth state
- ✅ `Health *HealthStatus` field exists on `contracts.Server` (line 48)

**Status**: Health field is implemented. OAuthState and ConnectionState fields are TODO.

**Decision**: Add `OAuthState` and `ConnectionState` fields to `contracts.Server` with `omitempty` JSON tags.

**Rationale**: Consistent with existing pattern; allows gradual adoption.

---

### 4. Doctor() Aggregation Pattern

**Question**: How should Doctor() aggregate from Health?

**Findings**:
- Current `Doctor()` in `internal/management/diagnostics.go` iterates servers and checks each field
- Returns `contracts.Diagnostics` with UpstreamErrors, OAuthRequired, etc.
- Categories map to Health.Action: login→OAuthRequired, restart→UpstreamErrors

**Decision**: Doctor() iterates servers, reads `Health.Action` and `Health.Level`, and categorizes into existing Diagnostics buckets.

**Rationale**: Preserves CLI output format; single source of truth.

**Alternatives Considered**:
- Change Diagnostics format: Rejected (breaking change for CLI consumers)
- Keep separate detection logic: Rejected (defeats purpose of refactor)

---

### 5. Frontend Type Patterns (Partial)

**Question**: How should TypeScript types mirror Go structs?

**Findings**:
- `frontend/src/types/api.ts` defines interfaces matching Go structs
- ✅ `HealthStatus` interface exists (lines 9-15)
- ✅ `Server` interface includes `health?: HealthStatus` (line 39)
- Optional fields use `?:` syntax

**Status**: HealthStatus interface is implemented. OAuthState and ConnectionState interfaces are TODO.

**Decision**: Add `OAuthState` and `ConnectionState` interfaces matching Go structs exactly.

**Rationale**: Maintains 1:1 mapping for API contract clarity.

---

## Summary

| Area | Decision | Status | Key File(s) |
|------|----------|--------|-------------|
| State Source | Use existing ConnectionInfo + new OAuthState | TODO | `internal/upstream/types/types.go` |
| Health Calculation | Keep logic, change input source | ✅ DONE | `internal/health/calculator.go` |
| API Serialization | Add new fields with omitempty | Partial | `internal/contracts/types.go` |
| Doctor Aggregation | Map Health.Action to Diagnostics categories | TODO | `internal/management/diagnostics.go` |
| Frontend Types | Add interfaces matching Go structs | Partial | `frontend/src/types/api.ts` |

Health calculation and HealthStatus types are complete. Remaining work is structured state objects and Doctor() refactoring.
