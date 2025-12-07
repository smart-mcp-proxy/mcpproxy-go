# Quickstart: Proactive OAuth Token Refresh & UX Improvements

**Feature**: [spec.md](./spec.md) | **Plan**: [plan.md](./plan.md)
**Date**: 2025-12-07

## Overview

This quickstart provides implementation examples for the key components of the proactive OAuth token refresh feature. Use these as reference when implementing the tasks.

---

## 1. Refresh Manager Implementation

### Core Structure

```go
// internal/oauth/refresh_manager.go
package oauth

import (
    "context"
    "sync"
    "time"

    "mcpproxy-go/internal/storage"
    "go.uber.org/zap"
)

const (
    DefaultRefreshThreshold = 0.8  // Refresh at 80% of token lifetime
    DefaultMaxRetries       = 3    // Maximum refresh retry attempts
)

// RefreshManager coordinates proactive OAuth token refresh.
type RefreshManager struct {
    storage     *storage.BoltDB
    coordinator *OAuthFlowCoordinator
    runtime     RefreshRuntimeOperations
    emitter     RefreshEventEmitter
    schedules   map[string]*RefreshSchedule
    threshold   float64
    maxRetries  int
    mu          sync.RWMutex
    logger      *zap.Logger
    ctx         context.Context
    cancel      context.CancelFunc
}

// RefreshSchedule tracks refresh state for a single server.
type RefreshSchedule struct {
    ServerName string
    ServerKey  string
    ExpiresAt  time.Time
    Timer      *time.Timer
    RetryCount int
    LastError  string
}

// NewRefreshManager creates a new refresh manager.
func NewRefreshManager(
    storage *storage.BoltDB,
    coordinator *OAuthFlowCoordinator,
    runtime RefreshRuntimeOperations,
    emitter RefreshEventEmitter,
    logger *zap.Logger,
) *RefreshManager {
    ctx, cancel := context.WithCancel(context.Background())
    return &RefreshManager{
        storage:     storage,
        coordinator: coordinator,
        runtime:     runtime,
        emitter:     emitter,
        schedules:   make(map[string]*RefreshSchedule),
        threshold:   DefaultRefreshThreshold,
        maxRetries:  DefaultMaxRetries,
        logger:      logger.Named("refresh-manager"),
        ctx:         ctx,
        cancel:      cancel,
    }
}

// Start initializes refresh schedules from persisted tokens.
func (m *RefreshManager) Start() error {
    tokens, err := m.storage.ListOAuthTokens()
    if err != nil {
        return err
    }

    for _, token := range tokens {
        if time.Now().Before(token.ExpiresAt) {
            m.scheduleRefresh(token.ServerName, token.ExpiresAt)
        }
    }

    m.logger.Info("Refresh manager started",
        zap.Int("scheduled_tokens", len(m.schedules)))
    return nil
}

// Stop cancels all scheduled refreshes and shuts down.
func (m *RefreshManager) Stop() {
    m.cancel()
    m.mu.Lock()
    defer m.mu.Unlock()

    for _, schedule := range m.schedules {
        if schedule.Timer != nil {
            schedule.Timer.Stop()
        }
    }
    m.schedules = make(map[string]*RefreshSchedule)
    m.logger.Info("Refresh manager stopped")
}

// scheduleRefresh schedules a refresh at 80% of token lifetime.
func (m *RefreshManager) scheduleRefresh(serverName string, expiresAt time.Time) {
    m.mu.Lock()
    defer m.mu.Unlock()

    // Cancel existing schedule if present
    if existing, ok := m.schedules[serverName]; ok && existing.Timer != nil {
        existing.Timer.Stop()
    }

    // Calculate refresh time at threshold percentage of lifetime
    now := time.Now()
    lifetime := expiresAt.Sub(now)
    if lifetime <= 0 {
        m.logger.Warn("Token already expired, not scheduling refresh",
            zap.String("server", serverName))
        return
    }

    refreshDelay := time.Duration(float64(lifetime) * m.threshold)
    refreshAt := now.Add(refreshDelay)

    schedule := &RefreshSchedule{
        ServerName: serverName,
        ExpiresAt:  expiresAt,
        RetryCount: 0,
    }

    schedule.Timer = time.AfterFunc(refreshDelay, func() {
        m.executeRefresh(serverName)
    })

    m.schedules[serverName] = schedule

    m.logger.Info("Scheduled proactive refresh",
        zap.String("server", serverName),
        zap.Time("expires_at", expiresAt),
        zap.Time("refresh_at", refreshAt),
        zap.Duration("delay", refreshDelay))
}

// executeRefresh performs the actual token refresh.
func (m *RefreshManager) executeRefresh(serverName string) {
    m.logger.Info("Executing proactive refresh", zap.String("server", serverName))

    // Check if OAuth flow is already active (manual login)
    if m.coordinator.IsFlowActive(serverName) {
        m.logger.Info("OAuth flow already active, skipping proactive refresh",
            zap.String("server", serverName))
        return
    }

    // Attempt refresh
    err := m.runtime.RefreshOAuthToken(serverName)
    if err != nil {
        m.handleRefreshFailure(serverName, err)
        return
    }

    // Success - get new expiration and reschedule
    token, err := m.storage.GetOAuthToken(serverName)
    if err != nil {
        m.logger.Error("Failed to get refreshed token", zap.Error(err))
        return
    }

    m.mu.Lock()
    if schedule, ok := m.schedules[serverName]; ok {
        schedule.RetryCount = 0
        schedule.LastError = ""
    }
    m.mu.Unlock()

    m.scheduleRefresh(serverName, token.ExpiresAt)
    m.emitter.EmitOAuthTokenRefreshed(serverName, token.ExpiresAt)

    m.logger.Info("Proactive refresh succeeded",
        zap.String("server", serverName),
        zap.Time("new_expires_at", token.ExpiresAt))
}

// handleRefreshFailure handles refresh failure with retry logic.
func (m *RefreshManager) handleRefreshFailure(serverName string, err error) {
    m.mu.Lock()
    schedule, ok := m.schedules[serverName]
    if !ok {
        m.mu.Unlock()
        return
    }

    schedule.RetryCount++
    schedule.LastError = err.Error()
    retryCount := schedule.RetryCount
    m.mu.Unlock()

    m.logger.Warn("Proactive refresh failed",
        zap.String("server", serverName),
        zap.Int("attempt", retryCount),
        zap.Error(err))

    if retryCount >= m.maxRetries {
        // Max retries exceeded - emit failure event
        m.logger.Error("Proactive refresh failed after max retries",
            zap.String("server", serverName),
            zap.Int("max_retries", m.maxRetries))
        m.emitter.EmitOAuthRefreshFailed(serverName, err.Error())
        return
    }

    // Exponential backoff: 1s, 2s, 4s
    delay := time.Duration(1<<uint(retryCount-1)) * time.Second

    m.mu.Lock()
    if schedule, ok := m.schedules[serverName]; ok {
        schedule.Timer = time.AfterFunc(delay, func() {
            m.executeRefresh(serverName)
        })
    }
    m.mu.Unlock()

    m.logger.Info("Scheduling refresh retry",
        zap.String("server", serverName),
        zap.Int("attempt", retryCount+1),
        zap.Duration("delay", delay))
}

// OnTokenSaved is called when a new token is saved (after login/refresh).
func (m *RefreshManager) OnTokenSaved(serverName string, expiresAt time.Time) {
    m.scheduleRefresh(serverName, expiresAt)
}

// OnTokenCleared is called when a token is cleared (logout).
func (m *RefreshManager) OnTokenCleared(serverName string) {
    m.mu.Lock()
    defer m.mu.Unlock()

    if schedule, ok := m.schedules[serverName]; ok {
        if schedule.Timer != nil {
            schedule.Timer.Stop()
        }
        delete(m.schedules, serverName)
    }

    m.logger.Info("Cleared refresh schedule", zap.String("server", serverName))
}
```

---

## 2. Management Service Extension

### TriggerOAuthLogout Implementation

```go
// internal/management/service.go (addition to interface)

// TriggerOAuthLogout clears OAuth token and disconnects a specific server.
TriggerOAuthLogout(ctx context.Context, name string) error

// internal/management/service.go (implementation)

// TriggerOAuthLogout clears OAuth token and disconnects server.
func (s *service) TriggerOAuthLogout(ctx context.Context, name string) error {
    if name == "" {
        return errors.New("server name is required")
    }

    // Check configuration gates
    if err := s.checkWriteGates(); err != nil {
        return err
    }

    // Delegate to runtime
    if err := s.runtime.TriggerOAuthLogout(name); err != nil {
        return err
    }

    // Emit servers.changed event
    s.eventEmitter.EmitServersChanged("oauth_logout", map[string]any{
        "server": name,
    })

    return nil
}
```

### RuntimeOperations Extension

```go
// internal/management/service.go (interface extension)

type RuntimeOperations interface {
    // ... existing methods ...
    TriggerOAuthLogout(serverName string) error
}

// internal/runtime/runtime.go (implementation)

// TriggerOAuthLogout clears OAuth token and disconnects server.
func (r *Runtime) TriggerOAuthLogout(serverName string) error {
    r.logger.Debug("Runtime.TriggerOAuthLogout called", zap.String("server", serverName))

    // Find the server
    server := r.upstreamManager.GetServer(serverName)
    if server == nil {
        return ErrServerNotFound
    }

    // Check if server uses OAuth
    if server.Config.OAuth == nil {
        return ErrServerNotOAuth
    }

    // Clear token from persistent storage
    serverKey := oauth.GenerateServerKey(serverName, server.Config.URL)
    if err := r.storage.DeleteOAuthToken(serverKey); err != nil {
        r.logger.Error("Failed to delete OAuth token", zap.Error(err))
        return err
    }

    // Disconnect the server
    if err := r.upstreamManager.DisconnectServer(serverName); err != nil {
        r.logger.Warn("Failed to disconnect server after logout", zap.Error(err))
        // Don't return error - token is already cleared
    }

    // Notify refresh manager
    if r.refreshManager != nil {
        r.refreshManager.OnTokenCleared(serverName)
    }

    r.logger.Info("OAuth logout completed", zap.String("server", serverName))
    return nil
}
```

---

## 3. CLI Logout Command

```go
// cmd/mcpproxy/auth_cmd.go (addition)

var authLogoutCmd = &cobra.Command{
    Use:   "logout",
    Short: "Logout from an OAuth-enabled MCP server",
    Long: `Clear OAuth credentials and disconnect from an MCP server.
This command clears stored OAuth tokens from persistent storage
and disconnects the server.`,
    RunE: runAuthLogout,
}

func init() {
    authCmd.AddCommand(authLogoutCmd)
    authLogoutCmd.Flags().StringP("server", "s", "", "Name of the server to logout from")
    authLogoutCmd.Flags().Bool("all", false, "Logout from all OAuth-enabled servers")
    authLogoutCmd.MarkFlagRequired("server") // unless --all is provided
}

func runAuthLogout(cmd *cobra.Command, args []string) error {
    serverName, _ := cmd.Flags().GetString("server")
    logoutAll, _ := cmd.Flags().GetBool("all")

    if !logoutAll && serverName == "" {
        return fmt.Errorf("either --server or --all flag is required")
    }

    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()

    // Try daemon connection first
    client, err := cliclient.NewClient(ctx, getSocketPath())
    if err == nil {
        defer client.Close()

        if logoutAll {
            return runLogoutAllViaDaemon(ctx, client)
        }
        return client.TriggerOAuthLogout(ctx, serverName)
    }

    // Fall back to standalone mode
    return runLogoutStandalone(ctx, serverName, logoutAll)
}

func runLogoutStandalone(ctx context.Context, serverName string, logoutAll bool) error {
    // Load storage directly
    storage, err := storage.NewBoltDB(getDataDir())
    if err != nil {
        return fmt.Errorf("failed to open storage: %w", err)
    }
    defer storage.Close()

    if logoutAll {
        tokens, err := storage.ListOAuthTokens()
        if err != nil {
            return err
        }
        for _, token := range tokens {
            if err := storage.DeleteOAuthToken(token.ServerName); err != nil {
                fmt.Printf("Failed to logout from %s: %v\n", token.ServerName, err)
            } else {
                fmt.Printf("Logged out from %s\n", token.ServerName)
            }
        }
        return nil
    }

    // Find server key
    cfg, err := config.Load(getConfigPath())
    if err != nil {
        return err
    }

    var serverURL string
    for _, srv := range cfg.MCPServers {
        if srv.Name == serverName {
            serverURL = srv.URL
            break
        }
    }
    if serverURL == "" {
        return fmt.Errorf("server not found: %s", serverName)
    }

    serverKey := oauth.GenerateServerKey(serverName, serverURL)
    if err := storage.DeleteOAuthToken(serverKey); err != nil {
        return fmt.Errorf("failed to logout: %w", err)
    }

    fmt.Printf("Successfully logged out from %s\n", serverName)
    return nil
}
```

---

## 4. REST API Handler

```go
// internal/httpapi/server.go (addition)

// @Summary      Trigger OAuth logout for server
// @Description  Clear OAuth token and disconnect a specific upstream MCP server
// @Tags         servers
// @Accept       json
// @Produce      json
// @Param        id   path      string  true  "Server ID or name"
// @Success      200  {object}  contracts.ServerActionResponse  "OAuth logout completed successfully"
// @Failure      400  {object}  contracts.ErrorResponse         "Bad request (server does not use OAuth)"
// @Failure      404  {object}  contracts.ErrorResponse         "Server not found"
// @Failure      500  {object}  contracts.ErrorResponse         "Internal server error"
// @Security     ApiKeyAuth
// @Security     ApiKeyQuery
// @Router       /servers/{id}/logout [post]
func (s *Server) handleServerLogout(w http.ResponseWriter, r *http.Request) {
    serverID := chi.URLParam(r, "id")
    if serverID == "" {
        s.writeError(w, http.StatusBadRequest, "server ID required")
        return
    }

    mgmtSvc, ok := s.controller.GetManagementService().(interface {
        TriggerOAuthLogout(ctx context.Context, name string) error
    })
    if !ok {
        s.logger.Error("Management service not available or missing TriggerOAuthLogout method")
        s.writeError(w, http.StatusInternalServerError, "management service unavailable")
        return
    }

    if err := mgmtSvc.TriggerOAuthLogout(r.Context(), serverID); err != nil {
        if errors.Is(err, management.ErrServerNotFound) {
            s.writeError(w, http.StatusNotFound, "server not found")
            return
        }
        if errors.Is(err, management.ErrServerNotOAuth) {
            s.writeError(w, http.StatusBadRequest, "server does not use OAuth")
            return
        }
        s.writeError(w, http.StatusInternalServerError, err.Error())
        return
    }

    s.writeSuccess(w, map[string]interface{}{
        "action": "logout",
        "server": serverID,
    })
}

// Register route (add in setupRoutes)
r.Post("/api/v1/servers/{id}/logout", s.handleServerLogout)
```

---

## 5. Web UI Changes

### ServerCard.vue Login Button Fix

```vue
<!-- frontend/src/components/ServerCard.vue -->

<template>
  <!-- Existing template -->

  <!-- Fix: Show Login when OAuth needed AND (disconnected OR token expired) -->
  <button
    v-if="needsOAuth && (notConnected || oauthExpired)"
    @click="handleLogin"
    class="btn btn-primary"
  >
    Login
  </button>

  <!-- Add Logout button for authenticated servers -->
  <button
    v-if="isAuthenticated"
    @click="handleLogout"
    class="btn btn-secondary"
  >
    Logout
  </button>

  <!-- Auth status badge -->
  <span v-if="oauthExpired" class="badge badge-warning">
    Token Expired
  </span>
  <span v-if="oauthError" class="badge badge-error">
    Auth Error
  </span>
</template>

<script setup lang="ts">
import { computed } from 'vue'

const props = defineProps<{
  server: ServerResponse
}>()

// New computed properties
const oauthExpired = computed(() => {
  return props.server.oauth_status === 'expired'
})

const oauthError = computed(() => {
  return props.server.oauth_status === 'error'
})

const isAuthenticated = computed(() => {
  return props.server.oauth_status === 'authenticated'
})

// Logout handler
const handleLogout = async () => {
  if (!confirm('Are you sure you want to logout from this server?')) {
    return
  }
  await serversStore.triggerOAuthLogout(props.server.name)
}
</script>
```

### API Service Extension

```typescript
// frontend/src/services/api.ts

export async function triggerOAuthLogout(serverName: string): Promise<void> {
  const response = await fetch(`/api/v1/servers/${serverName}/logout`, {
    method: 'POST',
    headers: getHeaders(),
  })
  if (!response.ok) {
    const error = await response.json()
    throw new Error(error.error || 'Logout failed')
  }
}
```

### Store Extension

```typescript
// frontend/src/stores/servers.ts

export const useServersStore = defineStore('servers', {
  // ... existing state and actions ...

  actions: {
    async triggerOAuthLogout(serverName: string) {
      try {
        await api.triggerOAuthLogout(serverName)
        // Refresh servers list to update UI
        await this.fetchServers()
      } catch (error) {
        console.error('OAuth logout failed:', error)
        throw error
      }
    }
  }
})
```

---

## 6. Event Emission

```go
// internal/runtime/events.go (additions)

const (
    EventTypeOAuthTokenRefreshed EventType = "oauth.token_refreshed"
    EventTypeOAuthRefreshFailed  EventType = "oauth.refresh_failed"
)

// internal/runtime/event_bus.go (additions)

// EmitOAuthTokenRefreshed emits an event when OAuth token is proactively refreshed.
func (b *EventBus) EmitOAuthTokenRefreshed(serverName string, expiresAt time.Time) {
    b.Publish(Event{
        Type: EventTypeOAuthTokenRefreshed,
        Data: map[string]any{
            "server_name": serverName,
            "expires_at":  expiresAt.Format(time.RFC3339),
        },
    })
}

// EmitOAuthRefreshFailed emits an event when OAuth token refresh fails.
func (b *EventBus) EmitOAuthRefreshFailed(serverName string, errorMsg string) {
    b.Publish(Event{
        Type: EventTypeOAuthRefreshFailed,
        Data: map[string]any{
            "server_name": serverName,
            "error":       errorMsg,
        },
    })
}
```

---

## 7. Unit Test Examples

### Refresh Manager Tests

```go
// internal/oauth/refresh_manager_test.go

func TestRefreshManager_ScheduleAt80Percent(t *testing.T) {
    manager := setupTestManager(t)
    defer manager.Stop()

    // Token expires in 100 seconds
    expiresAt := time.Now().Add(100 * time.Second)
    manager.OnTokenSaved("test-server", expiresAt)

    // Verify schedule
    manager.mu.RLock()
    schedule, ok := manager.schedules["test-server"]
    manager.mu.RUnlock()

    require.True(t, ok)
    require.NotNil(t, schedule.Timer)
    // Should be scheduled at 80 seconds (80% of 100s)
}

func TestRefreshManager_RetryWithBackoff(t *testing.T) {
    manager := setupTestManager(t)
    defer manager.Stop()

    // Set up mock that fails
    mockRuntime := manager.runtime.(*mockRefreshRuntime)
    mockRuntime.failCount = 2 // Fail first 2 attempts

    manager.executeRefresh("test-server")

    // Should have retried
    assert.Equal(t, 1, mockRuntime.callCount)
    // Wait for retry
    time.Sleep(1500 * time.Millisecond)
    assert.Equal(t, 2, mockRuntime.callCount)
}

func TestRefreshManager_StopOnMaxRetries(t *testing.T) {
    manager := setupTestManager(t)
    defer manager.Stop()

    mockRuntime := manager.runtime.(*mockRefreshRuntime)
    mockRuntime.failCount = 10 // Always fail
    mockEmitter := manager.emitter.(*mockEventEmitter)

    // Execute refresh (will trigger retries)
    manager.executeRefresh("test-server")

    // Wait for all retries
    time.Sleep(8 * time.Second)

    // Should have emitted failure event
    assert.True(t, mockEmitter.refreshFailedCalled)
    assert.Equal(t, "test-server", mockEmitter.lastFailedServer)
}
```

### Logout Tests

```go
// internal/management/service_test.go

func TestTriggerOAuthLogout_ValidServer(t *testing.T) {
    svc := setupTestService(t, config.Config{})

    err := svc.TriggerOAuthLogout(context.Background(), "oauth-server")
    require.NoError(t, err)
}

func TestTriggerOAuthLogout_DisableManagement(t *testing.T) {
    svc := setupTestService(t, config.Config{
        DisableManagement: true,
    })

    err := svc.TriggerOAuthLogout(context.Background(), "oauth-server")
    require.Error(t, err)
    assert.Contains(t, err.Error(), "disabled")
}

func TestTriggerOAuthLogout_NonOAuthServer(t *testing.T) {
    svc := setupTestService(t, config.Config{})

    err := svc.TriggerOAuthLogout(context.Background(), "stdio-server")
    require.Error(t, err)
    assert.Equal(t, ErrServerNotOAuth, err)
}
```

---

## 8. E2E Test Script

```bash
#!/bin/bash
# scripts/test-oauth-refresh-e2e.sh

set -e

echo "=== OAuth Proactive Refresh E2E Test ==="

# Start OAuth test server with short token lifetime
export OAUTH_TOKEN_LIFETIME=30  # 30 seconds
cd tests/oauthserver && go run . &
OAUTH_PID=$!
sleep 2

# Start mcpproxy
./mcpproxy serve --log-level=debug &
PROXY_PID=$!
sleep 3

# Add OAuth server
curl -X POST http://localhost:8080/api/v1/servers \
  -H "X-API-Key: test" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "test-oauth",
    "url": "http://localhost:9000",
    "protocol": "http",
    "oauth": {"scopes": ["mcp"]}
  }'

# Trigger login
./mcpproxy auth login --server=test-oauth

# Wait for 80% of token lifetime (24 seconds)
echo "Waiting for proactive refresh..."
sleep 26

# Check server status - should still be authenticated
STATUS=$(curl -s http://localhost:8080/api/v1/servers/test-oauth | jq -r '.oauth_status')
if [ "$STATUS" != "authenticated" ]; then
  echo "FAIL: Expected authenticated, got $STATUS"
  kill $PROXY_PID $OAUTH_PID
  exit 1
fi

# Verify token was refreshed (check logs)
if grep -q "Proactive refresh succeeded" ~/.mcpproxy/logs/main.log; then
  echo "PASS: Proactive refresh triggered"
else
  echo "FAIL: No proactive refresh in logs"
  kill $PROXY_PID $OAUTH_PID
  exit 1
fi

# Clean up
kill $PROXY_PID $OAUTH_PID
echo "=== Test Passed ==="
```

---

## Implementation Order

1. **RefreshManager** (`internal/oauth/refresh_manager.go`)
   - Core proactive refresh logic
   - Timer management
   - Retry with backoff

2. **RuntimeOperations extension** (`internal/runtime/runtime.go`)
   - `TriggerOAuthLogout()`
   - `RefreshOAuthToken()`
   - Integration with RefreshManager

3. **Management service extension** (`internal/management/service.go`)
   - `TriggerOAuthLogout()` method

4. **REST API handler** (`internal/httpapi/server.go`)
   - `/servers/{id}/logout` endpoint
   - Swagger annotations

5. **CLI command** (`cmd/mcpproxy/auth_cmd.go`)
   - `auth logout` subcommand

6. **Web UI changes** (`frontend/src/components/ServerCard.vue`)
   - Login button visibility fix
   - Logout button
   - Auth status badge

7. **SSE events** (`internal/runtime/events.go`, `event_bus.go`)
   - `oauth.token_refreshed`
   - `oauth.refresh_failed`

8. **Tests**
   - Unit tests for RefreshManager
   - Unit tests for TriggerOAuthLogout
   - E2E test script
   - Playwright tests for Web UI
