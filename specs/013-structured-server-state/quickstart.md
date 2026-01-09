# Quickstart: Structured Server State

**Feature**: 013-structured-server-state
**Date**: 2025-12-16

## Overview

Make Health the single source of truth. Add new actions (`set_secret`, `configure`). Refactor Doctor() to aggregate from Health. Update UI to navigate to fix locations.

**Prerequisite**: #192 (Unified Health Status) is merged - `HealthStatus` and `CalculateHealth()` exist.

## Implementation Order

1. **Health Constants** → Add new action constants
2. **Health Calculator** → Detect missing secrets and OAuth config issues
3. **Doctor() Refactor** → Aggregate from Health instead of independent detection
4. **Frontend Actions** → Navigate to fix locations

## Step-by-Step

### 1. Add Constants (internal/health/constants.go)

```go
const (
    // Existing
    ActionNone     = ""
    ActionLogin    = "login"
    ActionRestart  = "restart"
    ActionEnable   = "enable"
    ActionApprove  = "approve"
    ActionViewLogs = "view_logs"

    // New
    ActionSetSecret = "set_secret"
    ActionConfigure = "configure"
)
```

### 2. Extend HealthCalculatorInput (internal/health/calculator.go)

```go
type HealthCalculatorInput struct {
    // Existing fields...
    Name            string
    Enabled         bool
    Quarantined     bool
    State           string
    LastError       string
    OAuthRequired   bool
    OAuthStatus     string
    // ...

    // New fields
    MissingSecret   string  // Secret name if unresolved (e.g., "GITHUB_TOKEN")
    OAuthConfigErr  string  // OAuth config error (e.g., "requires 'resource' parameter")
}
```

### 3. Update CalculateHealth() (internal/health/calculator.go)

Add checks after admin state, before connection state:

```go
func CalculateHealth(input HealthCalculatorInput, cfg *HealthCalculatorConfig) *contracts.HealthStatus {
    // 1. Admin state checks (existing)
    if !input.Enabled { ... }
    if input.Quarantined { ... }

    // 2. NEW: Missing secret check
    if input.MissingSecret != "" {
        return &contracts.HealthStatus{
            Level:      LevelUnhealthy,
            AdminState: StateEnabled,
            Summary:    "Missing secret",
            Detail:     input.MissingSecret,
            Action:     ActionSetSecret,
        }
    }

    // 3. NEW: OAuth config error check
    if input.OAuthConfigErr != "" {
        return &contracts.HealthStatus{
            Level:      LevelUnhealthy,
            AdminState: StateEnabled,
            Summary:    "OAuth configuration error",
            Detail:     input.OAuthConfigErr,
            Action:     ActionConfigure,
        }
    }

    // 4. Connection state checks (existing)
    // 5. OAuth state checks (existing)
    // 6. Healthy (existing)
}
```

### 4. Populate New Input Fields (internal/upstream/manager.go)

When building `HealthCalculatorInput`, detect and populate:

```go
input := health.HealthCalculatorInput{
    // ... existing fields ...

    // Detect missing secrets from connection error
    MissingSecret: extractMissingSecret(connInfo.LastError),

    // Detect OAuth config issues from error message
    OAuthConfigErr: extractOAuthConfigError(connInfo.LastError),
}
```

### 5. Refactor Doctor() (internal/management/diagnostics.go)

Replace independent detection with Health aggregation:

```go
func (s *service) Doctor(ctx context.Context) (*contracts.Diagnostics, error) {
    servers, _ := s.runtime.GetAllServers()
    diag := &contracts.Diagnostics{Timestamp: time.Now()}

    // Aggregate missing secrets by name
    secretsMap := make(map[string][]string)

    for _, srv := range servers {
        if srv.Health == nil {
            continue
        }

        switch srv.Health.Action {
        case health.ActionRestart:
            diag.UpstreamErrors = append(diag.UpstreamErrors, contracts.UpstreamError{
                ServerName:   srv.Name,
                ErrorMessage: srv.Health.Detail,
                Timestamp:    time.Now(),
            })
        case health.ActionLogin:
            diag.OAuthRequired = append(diag.OAuthRequired, contracts.OAuthRequirement{
                ServerName: srv.Name,
                State:      "unauthenticated",
            })
        case health.ActionConfigure:
            diag.OAuthIssues = append(diag.OAuthIssues, contracts.OAuthIssue{
                ServerName: srv.Name,
                Error:      srv.Health.Detail,
            })
        case health.ActionSetSecret:
            secretName := srv.Health.Detail
            secretsMap[secretName] = append(secretsMap[secretName], srv.Name)
        }
    }

    // Convert secrets map to slice
    for secretName, servers := range secretsMap {
        diag.MissingSecrets = append(diag.MissingSecrets, contracts.MissingSecretInfo{
            SecretName: secretName,
            UsedBy:     servers,
        })
    }

    // Keep system-level checks
    if s.config.DockerIsolation != nil && s.config.DockerIsolation.Enabled {
        diag.DockerStatus = s.checkDockerDaemon()
    }

    diag.TotalIssues = len(diag.UpstreamErrors) + len(diag.OAuthRequired) +
        len(diag.OAuthIssues) + len(diag.MissingSecrets)
    return diag, nil
}
```

### 6. Frontend Actions (frontend/src/components/ServerCard.vue)

Add handlers for new actions:

```typescript
// In healthAction computed or handler function
case 'set_secret':
    router.push('/secrets')
    break
case 'configure':
    router.push(`/servers/${server.name}?tab=config`)
    break
```

### 7. Dashboard Consolidation (frontend/src/views/Dashboard.vue)

Remove "System Diagnostics" banner (lines 3-33). The "Servers Needing Attention" banner handles all issues.

### 8. CLI Updates (cmd/mcpproxy/upstream_cmd.go)

Add handlers for new actions in `outputServers()`:

```go
// Format action as CLI command hint
actionHint := "-"
switch healthAction {
case "login":
    actionHint = fmt.Sprintf("auth login --server=%s", name)
case "restart":
    actionHint = fmt.Sprintf("upstream restart %s", name)
case "enable":
    actionHint = fmt.Sprintf("upstream enable %s", name)
case "approve":
    actionHint = "Approve in Web UI"
case "view_logs":
    actionHint = fmt.Sprintf("upstream logs %s", name)
// New actions
case "set_secret":
    actionHint = fmt.Sprintf("Set %s", healthDetail)
case "configure":
    actionHint = "Edit config"
}
```

## Verification

```bash
# Unit tests
go test ./internal/health/... -v
go test ./internal/management/... -v

# E2E tests
./scripts/test-api-e2e.sh

# Frontend
cd frontend && npm run build
```

## Key Files

| File | Change |
|------|--------|
| `internal/health/constants.go` | Add ActionSetSecret, ActionConfigure |
| `internal/health/calculator.go` | Add missing secret/OAuth config checks |
| `internal/upstream/manager.go` | Populate new input fields |
| `internal/management/diagnostics.go` | Aggregate from Health |
| `cmd/mcpproxy/upstream_cmd.go` | Add CLI hints for new actions |
| `frontend/src/components/ServerCard.vue` | Handle new actions |
| `frontend/src/views/Dashboard.vue` | Remove duplicate banner |
