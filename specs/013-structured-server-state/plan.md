# Implementation Plan: Structured Server State

**Branch**: `013-structured-server-state` | **Date**: 2025-12-16 | **Spec**: [spec.md](./spec.md)
**Depends On**: #192 (Unified Health Status) - merged to main

## Summary

Make Health the single source of truth for per-server issues. Extend Health with new actions (`set_secret`, `configure`). Refactor Doctor() to aggregate from Health. Update UI to navigate to fix locations instead of showing CLI hints.

## Technical Context

**Language/Version**: Go 1.24.0
**Primary Dependencies**: mcp-go, zap, chi, Vue 3/TypeScript
**Testing**: go test, ./scripts/test-api-e2e.sh

## Constitution Check

| Principle | Status | Notes |
|-----------|--------|-------|
| I. Performance at Scale | PASS | Health already calculated; Diagnostics aggregates existing data |
| II. Actor-Based Concurrency | PASS | No new goroutines; uses existing state |
| V. Test-Driven Development | PASS | Update existing tests for new actions |

**Gate Result**: PASS

## Source Code Changes

```text
# Backend (Go)
internal/
├── health/
│   ├── constants.go       # Add ActionSetSecret, ActionConfigure
│   └── calculator.go      # Add missing secret/OAuth config detection
├── management/
│   └── diagnostics.go     # Refactor Doctor() to aggregate from Health
└── upstream/
    └── manager.go         # Populate MissingSecret, OAuthConfigErr in input

# Frontend (Vue)
frontend/src/
├── components/
│   └── ServerCard.vue     # Add set_secret, configure action handlers
└── views/
    └── Dashboard.vue      # Remove duplicate banner, add navigation

# Tests
internal/
├── health/calculator_test.go      # Add tests for new actions
└── management/diagnostics_test.go # Update for aggregation
```

## Implementation Phases

### Phase 1: Health Actions (Backend)

1. Add constants to `internal/health/constants.go`:
   ```go
   ActionSetSecret = "set_secret"
   ActionConfigure = "configure"
   ```

2. Add fields to `HealthCalculatorInput`:
   ```go
   MissingSecret  string  // Secret name if unresolved
   OAuthConfigErr string  // OAuth config error message
   ```

3. Update `CalculateHealth()` priority:
   - After admin state checks
   - Before connection state checks
   - Check `MissingSecret` → return `set_secret` action
   - Check `OAuthConfigErr` → return `configure` action

4. Update `internal/upstream/manager.go` to populate new input fields

### Phase 2: Doctor() Refactoring (Backend)

1. Remove independent detection logic from `Doctor()`
2. Iterate servers, switch on `Health.Action`:
   ```go
   case "restart":  → diag.UpstreamErrors
   case "login":    → diag.OAuthRequired
   case "configure": → diag.OAuthIssues
   case "set_secret": → aggregate by secret name
   ```
3. Keep system-level checks (Docker status)
4. Update tests

### Phase 3: Frontend Updates

1. Add action handlers in `ServerCard.vue`:
   ```typescript
   case 'set_secret':
       router.push('/secrets')
   case 'configure':
       router.push(`/servers/${server.name}?tab=config`)
   ```

2. Update `Dashboard.vue`:
   - Remove "System Diagnostics" banner (lines 3-33)
   - Add action handlers to "Servers Needing Attention" for new actions

3. Update TypeScript types if needed (add new action values)

## Verification

```bash
# Unit tests
go test ./internal/health/... -v
go test ./internal/management/... -v

# E2E tests
./scripts/test-api-e2e.sh

# Frontend
cd frontend && npm run build && npm run test
```
