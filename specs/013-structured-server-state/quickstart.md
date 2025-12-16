# Quickstart: Structured Server State

**Feature**: 013-structured-server-state
**Date**: 2025-12-13

## Overview

This guide provides quick instructions for implementing the structured server state feature.

## Prerequisites

- Go 1.24.0+
- Node.js 18+ (for frontend)
- MCPProxy development environment set up

## Implementation Order

1. **Backend Types** → Add new Go types
2. **Populate State** → Fill structured objects from existing data
3. **Update Health Calculator** → Use structured state as input
4. **Refactor Doctor()** → Aggregate from Health
5. **Frontend Types** → Add TypeScript interfaces
6. **UI Consolidation** → Remove duplicate banner

## Step-by-Step

### 1. Add Go Types (internal/contracts/types.go)

```go
// Add after existing type definitions

type OAuthState struct {
    Status          string     `json:"status"`
    TokenExpiresAt  *time.Time `json:"token_expires_at,omitempty"`
    LastAttempt     *time.Time `json:"last_attempt,omitempty"`
    RetryCount      int        `json:"retry_count"`
    UserLoggedOut   bool       `json:"user_logged_out"`
    HasRefreshToken bool       `json:"has_refresh_token"`
    Error           string     `json:"error,omitempty"`
}

type ConnectionState struct {
    Status      string     `json:"status"`
    ConnectedAt *time.Time `json:"connected_at,omitempty"`
    LastError   string     `json:"last_error,omitempty"`
    RetryCount  int        `json:"retry_count"`
    LastRetryAt *time.Time `json:"last_retry_at,omitempty"`
    ShouldRetry bool       `json:"should_retry"`
}

// Add to Server struct
type Server struct {
    // ... existing fields ...
    OAuthState      *OAuthState      `json:"oauth_state,omitempty"`
    ConnectionState *ConnectionState `json:"connection_state,omitempty"`
}
```

### 2. Populate State (internal/upstream/manager.go)

In `GetAllServersWithStatus()` or equivalent, add:

```go
func buildOAuthState(client *Client) *contracts.OAuthState {
    if !client.HasOAuth() {
        return nil
    }
    info := client.GetConnectionInfo()
    return &contracts.OAuthState{
        Status:          client.GetOAuthStatus(),
        TokenExpiresAt:  client.GetTokenExpiresAt(),
        LastAttempt:     &info.LastOAuthAttempt,
        RetryCount:      info.OAuthRetryCount,
        UserLoggedOut:   client.StateManager.IsUserLoggedOut(),
        HasRefreshToken: client.HasRefreshToken(),
        Error:           getOAuthError(info),
    }
}

func buildConnectionState(client *Client) *contracts.ConnectionState {
    info := client.GetConnectionInfo()
    return &contracts.ConnectionState{
        Status:      info.State.String(),
        ConnectedAt: client.GetConnectedAt(),
        LastError:   getErrorString(info.LastError),
        RetryCount:  info.RetryCount,
        LastRetryAt: &info.LastRetryTime,
        ShouldRetry: client.ShouldRetry(),
    }
}
```

### 3. Update Health Calculator (internal/health/calculator.go)

Update `HealthCalculatorInput` to accept structured state:

```go
type HealthCalculatorInput struct {
    Name        string
    Enabled     bool
    Quarantined bool

    // Use structured state
    OAuth      *contracts.OAuthState
    Connection *contracts.ConnectionState
}

func CalculateHealth(input HealthCalculatorInput, cfg *HealthCalculatorConfig) *contracts.HealthStatus {
    // Extract needed fields from structured state
    state := "disconnected"
    if input.Connection != nil {
        state = input.Connection.Status
    }
    // ... rest of calculation uses structured fields
}
```

### 4. Refactor Doctor() (internal/management/diagnostics.go)

```go
func (s *service) Doctor(ctx context.Context) (*contracts.Diagnostics, error) {
    servers, _ := s.runtime.GetAllServers() // Already includes Health

    diag := &contracts.Diagnostics{Timestamp: time.Now()}

    for _, srv := range servers {
        if srv.Health == nil {
            continue
        }

        switch srv.Health.Action {
        case "login":
            diag.OAuthRequired = append(diag.OAuthRequired, contracts.OAuthRequirement{
                ServerName: srv.Name,
                State:      srv.OAuthState.Status,
                Message:    fmt.Sprintf("Run: mcpproxy auth login --server=%s", srv.Name),
            })
        case "restart":
            diag.UpstreamErrors = append(diag.UpstreamErrors, contracts.UpstreamError{
                ServerName:   srv.Name,
                ErrorMessage: srv.ConnectionState.LastError,
                Timestamp:    time.Now(),
            })
        }
    }

    // System-level checks
    if s.config.DockerIsolation != nil && s.config.DockerIsolation.Enabled {
        diag.DockerStatus = s.checkDockerDaemon()
    }

    diag.TotalIssues = len(diag.UpstreamErrors) + len(diag.OAuthRequired)
    return diag, nil
}
```

### 5. Add TypeScript Types (frontend/src/types/api.ts)

```typescript
export interface OAuthState {
    status: 'authenticated' | 'expired' | 'error' | 'none';
    token_expires_at?: string;
    last_attempt?: string;
    retry_count: number;
    user_logged_out: boolean;
    has_refresh_token: boolean;
    error?: string;
}

export interface ConnectionState {
    status: 'disconnected' | 'connecting' | 'ready' | 'error';
    connected_at?: string;
    last_error?: string;
    retry_count: number;
    last_retry_at?: string;
    should_retry: boolean;
}

// Add to Server interface
export interface Server {
    // ... existing fields ...
    oauth_state?: OAuthState;
    connection_state?: ConnectionState;
}
```

### 6. UI Consolidation (frontend/src/views/Dashboard.vue)

Remove the System Diagnostics banner (lines 3-33) and enhance the existing Servers Needing Attention banner to show aggregated counts.

## Verification

```bash
# Run unit tests
go test ./internal/health/... -v
go test ./internal/management/... -v

# Run E2E tests (verify backwards compat)
./scripts/test-api-e2e.sh

# Run full suite
./scripts/run-all-tests.sh

# Build and test frontend
cd frontend && npm run build && npm run test
```

## Key Files Changed

| File | Change |
|------|--------|
| `internal/contracts/types.go` | Add OAuthState, ConnectionState types |
| `internal/upstream/manager.go` | Populate structured state objects |
| `internal/health/calculator.go` | Update input to use structured state |
| `internal/management/diagnostics.go` | Refactor Doctor() to aggregate from Health |
| `frontend/src/types/api.ts` | Add TypeScript interfaces |
| `frontend/src/views/Dashboard.vue` | Remove duplicate banner |
