# Quickstart: Structured Server State

**Feature**: 013-structured-server-state
**Date**: 2025-12-16

## Overview

Quick implementation guide for adding structured state objects and consolidating UI.

**Prerequisite**: #192 (Unified Health Status) is already merged - `HealthStatus` and `CalculateHealth()` exist.

## Implementation Order

1. **Backend Types** → Add OAuthState, ConnectionState to contracts
2. **Populate State** → Fill structured objects from existing data
3. **Refactor Doctor()** → Aggregate from Health instead of raw fields
4. **Frontend Types** → Add TypeScript interfaces
5. **UI Consolidation** → Remove duplicate diagnostics banner

## Step-by-Step

### 1. Add Go Types (internal/contracts/types.go)

```go
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

In `GetAllServersWithStatus()` or equivalent:

```go
func buildConnectionState(info *types.ConnectionInfo) *contracts.ConnectionState {
    return &contracts.ConnectionState{
        Status:      info.State.String(),
        ConnectedAt: info.ConnectedAt,
        LastError:   getErrorString(info.LastError),
        RetryCount:  info.RetryCount,
        LastRetryAt: &info.LastRetryTime,
        ShouldRetry: info.ShouldRetry,
    }
}

func buildOAuthState(info *types.ConnectionInfo, token *storage.OAuthToken) *contracts.OAuthState {
    if token == nil {
        return nil
    }
    return &contracts.OAuthState{
        Status:          getOAuthStatus(token),
        TokenExpiresAt:  token.ExpiresAt,
        LastAttempt:     &info.LastOAuthAttempt,
        RetryCount:      info.OAuthRetryCount,
        UserLoggedOut:   info.UserLoggedOut,
        HasRefreshToken: token.RefreshToken != "",
        Error:           getOAuthError(info),
    }
}
```

### 3. Refactor Doctor() (internal/management/diagnostics.go)

```go
func (s *service) Doctor(ctx context.Context) (*contracts.Diagnostics, error) {
    servers, _ := s.runtime.GetAllServersWithHealth()

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
                ErrorMessage: srv.Health.Detail,
                Timestamp:    time.Now(),
            })
        }
    }

    // Keep system-level checks (Docker)
    if s.config.DockerIsolation != nil && s.config.DockerIsolation.Enabled {
        diag.DockerStatus = s.checkDockerDaemon()
    }

    diag.TotalIssues = len(diag.UpstreamErrors) + len(diag.OAuthRequired)
    return diag, nil
}
```

### 4. Add TypeScript Types (frontend/src/types/api.ts)

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

### 5. UI Consolidation (frontend/src/views/Dashboard.vue)

Remove the System Diagnostics section. The existing "Servers Needing Attention" banner (using `server.health`) handles all server health display.

## Verification

```bash
go test ./internal/management/... -v
./scripts/test-api-e2e.sh
./scripts/run-all-tests.sh
cd frontend && npm run build
```

## Key Files

| File | Change |
|------|--------|
| `internal/contracts/types.go` | Add OAuthState, ConnectionState |
| `internal/upstream/manager.go` | Populate structured state |
| `internal/management/diagnostics.go` | Refactor Doctor() |
| `frontend/src/types/api.ts` | Add TypeScript interfaces |
| `frontend/src/views/Dashboard.vue` | Remove diagnostics section |
