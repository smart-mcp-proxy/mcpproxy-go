# Research: Structured Server State

**Feature**: 013-structured-server-state
**Date**: 2025-12-16

## Overview

Research findings for implementing structured server state objects. Health calculation is already implemented (#192); this research focuses on remaining work.

## Research Tasks

### 1. State Data Sources

**Question**: Where does connection and OAuth state data come from?

**Findings**:
- `internal/upstream/types/types.go` defines `ConnectionInfo` with: State, LastError, RetryCount, LastRetryTime, ServerName, ServerVersion, LastOAuthAttempt, OAuthRetryCount, IsOAuthError
- `StateManager` provides thread-safe state transitions with callbacks
- OAuth token data is in storage via `Manager.GetOAuthToken()`

**Decision**: Use existing `ConnectionInfo` as source for `ConnectionState`. Build `OAuthState` from storage token data + `ConnectionInfo` OAuth fields.

**Rationale**: Minimizes duplication; `ConnectionInfo` already has rich retry and timing data.

---

### 2. API Serialization

**Question**: How to add structured state to server responses?

**Findings**:
- `internal/contracts/types.go` defines `Server` struct with JSON tags
- `internal/upstream/manager.go:GetAllServersWithStatus()` builds server maps
- `Health *HealthStatus` field exists (line 48) - follows same pattern

**Decision**: Add `OAuthState` and `ConnectionState` fields to `contracts.Server` with `omitempty` JSON tags.

**Key Files**:
- `internal/contracts/types.go` - Add new types
- `internal/upstream/manager.go` - Populate fields in `GetAllServersWithStatus()`

---

### 3. Doctor() Refactoring

**Question**: How should Doctor() aggregate from Health?

**Findings**:
- Current `Doctor()` in `internal/management/diagnostics.go` iterates servers and checks raw fields (lastError, authenticated)
- Returns `contracts.Diagnostics` with UpstreamErrors, OAuthRequired, etc.
- Categories map to Health.Action: login→OAuthRequired, restart→UpstreamErrors

**Decision**: Doctor() should iterate servers, read `server.Health.Action`, and categorize:
- `action == "login"` → OAuthRequired
- `action == "restart"` → UpstreamErrors
- `level == "degraded"` → RuntimeWarnings

**Rationale**: Single source of truth; no duplicate detection logic.

---

### 4. Frontend Types

**Question**: How to add TypeScript interfaces?

**Findings**:
- `frontend/src/types/api.ts` defines interfaces matching Go structs
- `HealthStatus` interface exists (lines 9-15) - follows same pattern
- `Server` interface includes `health?: HealthStatus` (line 39)

**Decision**: Add `OAuthState` and `ConnectionState` interfaces matching Go structs exactly.

**Key File**: `frontend/src/types/api.ts`

---

### 5. Dashboard UI Consolidation

**Question**: How to remove duplicate diagnostics display?

**Findings**:
- `frontend/src/views/Dashboard.vue` has two displays:
  1. "Servers Needing Attention" banner (lines ~35-70) - uses `server.health`
  2. Diagnostics section - uses separate `/api/v1/diagnostics` endpoint
- Action buttons already work in the health banner

**Decision**: Remove diagnostics section entirely. Enhance "Servers Needing Attention" banner to show aggregated counts.

**Key File**: `frontend/src/views/Dashboard.vue`

---

## Summary

| Area | Action | Key File(s) |
|------|--------|-------------|
| State Types | Add OAuthState, ConnectionState | `internal/contracts/types.go` |
| State Population | Populate from ConnectionInfo + storage | `internal/upstream/manager.go` |
| Doctor Refactor | Map Health.Action to categories | `internal/management/diagnostics.go` |
| Frontend Types | Add matching interfaces | `frontend/src/types/api.ts` |
| UI Consolidation | Remove diagnostics section | `frontend/src/views/Dashboard.vue` |
