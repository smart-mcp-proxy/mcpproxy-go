# Research: Structured Server State

**Feature**: 013-structured-server-state
**Date**: 2025-12-16

## Overview

Research findings for making Health the single source of truth and having Diagnostics aggregate from it.

## Key Insight

Users care about fixing **servers**, not categories of errors. Health should contain all per-server details; Diagnostics aggregates for system-wide views.

## Research Tasks

### 1. Health Calculator Extension

**Question**: How to detect missing secrets and OAuth config issues in Health?

**Findings**:
- Missing secrets are detected during server startup when resolving `${env:X}` refs
- OAuth config issues surface as errors like "requires 'resource' parameter"
- Current `HealthCalculatorInput` doesn't have fields for these

**Decision**: Add `MissingSecret` and `OAuthConfigErr` fields to `HealthCalculatorInput`. Update priority to check these before connection errors.

**Key Files**:
- `internal/health/calculator.go` - Add new checks
- `internal/health/constants.go` - Add `ActionSetSecret`, `ActionConfigure`
- `internal/upstream/manager.go` - Populate new input fields

---

### 2. Doctor() Refactoring

**Question**: How should Doctor() aggregate from Health?

**Findings**:
- Current Doctor() in `internal/management/diagnostics.go` has independent detection logic
- Duplicates what Health already knows (connection errors, OAuth state)
- Returns categories: UpstreamErrors, OAuthRequired, OAuthIssues, MissingSecrets

**Decision**: Replace independent detection with Health aggregation:
- `action == "restart"` → UpstreamErrors
- `action == "login"` → OAuthRequired
- `action == "configure"` → OAuthIssues
- `action == "set_secret"` → MissingSecrets (grouped by secret name)

**Key File**: `internal/management/diagnostics.go`

---

### 3. Missing Secrets Cross-Cutting

**Question**: How to handle secrets that affect multiple servers?

**Findings**:
- A single secret (e.g., `GITHUB_TOKEN`) can be used by multiple servers
- Current `MissingSecretInfo` has `UsedBy []string` field
- Health is per-server, so multiple servers will have `action: "set_secret"` with same secret name

**Decision**: Diagnostics aggregates by secret name:
```go
secretsMap := make(map[string][]string)  // secret → servers
for _, srv := range servers {
    if srv.Health.Action == "set_secret" {
        secretsMap[srv.Health.Detail] = append(secretsMap[srv.Health.Detail], srv.Name)
    }
}
```

---

### 4. Frontend Navigation

**Question**: How should action buttons navigate?

**Findings**:
- Current buttons trigger in-place actions (restart, login) or are missing
- New actions need navigation: `set_secret` → `/ui/secrets`, `configure` → server config tab
- `ServerCard.vue` already switches on `health.action`

**Decision**: Extend switch statement with new cases:
```typescript
case 'set_secret':
    router.push('/secrets')
    break
case 'configure':
    router.push(`/servers/${server.name}?tab=config`)
    break
```

**Key Files**:
- `frontend/src/components/ServerCard.vue`
- `frontend/src/views/Dashboard.vue`

---

### 5. Dashboard Consolidation

**Question**: How to remove duplicate banners?

**Findings**:
- Dashboard has two banners: "System Diagnostics" and "Servers Needing Attention"
- Both show same issues (connection errors, OAuth needed)
- "Servers Needing Attention" uses Health - keep this one

**Decision**: Remove "System Diagnostics" banner (lines 3-33). Enhance "Servers Needing Attention" with aggregated counts if needed.

**Key File**: `frontend/src/views/Dashboard.vue`

---

## Summary

| Area | Action | Key File(s) |
|------|--------|-------------|
| Health Calculator | Add `set_secret`, `configure` actions | `internal/health/calculator.go` |
| Health Constants | Add new action constants | `internal/health/constants.go` |
| Doctor Refactor | Aggregate from Health.Action | `internal/management/diagnostics.go` |
| Frontend Actions | Navigate to secrets/config pages | `frontend/src/components/ServerCard.vue` |
| Dashboard | Remove duplicate banner | `frontend/src/views/Dashboard.vue` |
