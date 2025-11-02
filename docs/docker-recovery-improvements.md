# Docker Resume Recovery - Critical Analysis & Improvement Recommendations

## Executive Summary

The current Docker resume recovery implementation (feature/docker-recovery branch) provides a solid foundation for detecting and recovering from Docker Desktop pause/resume events. However, critical analysis reveals **11 significant gaps and improvement opportunities** that could enhance reliability, user experience, and system observability.

**🚨 CRITICAL:** Issue #11 (Duplicate Container Spawning) is the **most dangerous** problem - the system can spawn multiple containers for the same server during recovery, leading to resource exhaustion, port conflicts, and orphaned containers.

**Severity Levels:**
- 🔴 **Critical** - Can cause data loss, incorrect behavior, or poor user experience
- 🟡 **Important** - Impacts reliability or performance
- 🟢 **Nice-to-have** - Improves observability or maintainability

---

## Current Implementation Review

### ✅ What Works Well

1. **Pre-launch Docker health probe** - Prevents repeated startup failures
2. **Polling-based recovery** - Detects when Docker becomes available again
3. **Explicit error state** - UX clearly shows Docker unavailability
4. **Force reconnect API** - Clean separation between tray and runtime
5. **Container cleanup timeout increase** - 30s prevents race conditions
6. **Safe config cloning** - Avoids mutating shared state

### ❌ Critical Gaps Identified

---

## 🔴 Critical Issue #1: Reconnects ALL Servers Instead of Docker-Only

**Problem:**
`ForceReconnectAll()` reconnects **every disconnected server**, regardless of whether they use Docker isolation.

**Evidence:**
```go
// internal/upstream/manager.go:890-897
for id, client := range clientMap {
    if client.IsConnected() {
        continue  // Skip connected
    }
    // ❌ No check for IsDockerCommand() - reconnects HTTP, SSE, stdio servers too!
    cfg := cloneServerConfig(client.GetConfig())
    // ... recreate client
}
```

**Impact:**
- Wastes resources reconnecting HTTP/SSE/stdio servers that weren't affected
- Unnecessary downtime for unaffected servers
- Confusing logs showing reconnections for non-Docker servers

**Recommended Fix:**
```go
for id, client := range clientMap {
    if client.IsConnected() {
        continue
    }

    // ✅ ADD: Filter for Docker-based servers only
    if !client.IsDockerCommand() {
        m.logger.Debug("Skipping force reconnect for non-Docker server",
            zap.String("server", id),
            zap.String("reason", reason))
        continue
    }

    // Only reconnect Docker-isolated servers
    cfg := cloneServerConfig(client.GetConfig())
    // ...
}
```

**Files to modify:**
- `internal/upstream/manager.go:890-897` (add Docker filter)

---

## 🔴 Critical Issue #2: No Container Health Verification

**Problem:**
When Docker is paused, existing container sockets remain open but processes inside are frozen. When Docker resumes:
1. Tray detects Docker is available
2. Calls `ForceReconnectAll()`
3. Manager skips servers where `IsConnected() == true`
4. **But those containers are dead/paused!**

**Evidence:**
```go
// internal/upstream/manager.go:895-897
if client.IsConnected() {
    continue  // ❌ Connection alive ≠ container healthy!
}
```

**Impact:**
- Servers appear "connected" but are non-functional
- Tool calls timeout or hang indefinitely
- Users must manually restart servers

**Recommended Fix:**

Add container health verification before skipping reconnection:

```go
if client.IsConnected() {
    // ✅ For Docker servers, verify container is actually healthy
    if client.IsDockerCommand() {
        if !m.verifyDockerContainerHealthy(client) {
            m.logger.Warn("Docker container unhealthy despite active connection",
                zap.String("server", id),
                zap.String("container_id", client.GetContainerID()))
            // Force reconnect even though connection appears active
        } else {
            continue // Container is healthy, skip
        }
    } else {
        continue // Non-Docker server, connection is sufficient
    }
}
```

Add helper method:
```go
func (m *Manager) verifyDockerContainerHealthy(client *managed.Client) bool {
    containerID := client.GetContainerID()
    if containerID == "" {
        return false
    }

    // Quick health check: docker ps --filter id=<containerID> --format {{.Status}}
    ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
    defer cancel()

    cmd := exec.CommandContext(ctx, "docker", "ps",
        "--filter", fmt.Sprintf("id=%s", containerID),
        "--format", "{{.Status}}")

    output, err := cmd.Output()
    if err != nil || len(output) == 0 {
        return false // Container not running
    }

    status := strings.ToLower(string(output))
    return strings.Contains(status, "up") || strings.Contains(status, "running")
}
```

**Files to modify:**
- `internal/upstream/manager.go` (add health check logic)
- `internal/upstream/managed/client.go` (add `GetContainerID()` accessor if missing)

---

## 🟡 Important Issue #3: Fixed 5-Second Polling is Suboptimal

**Problem:**
Docker health polling uses a fixed 5-second interval:
- **Too frequent** when Docker Desktop is off (wastes CPU/battery on laptops)
- **Too slow** when Docker just resumed (users wait 5s before recovery starts)

**Evidence:**
```go
// cmd/mcpproxy-tray/main.go:1378
ticker := time.NewTicker(5 * time.Second) // ❌ Fixed interval
```

**Recommended Fix:**

Implement **exponential backoff with jitter**:

```go
func (cpl *CoreProcessLauncher) handleDockerUnavailable(ctx context.Context) {
    // Initial fast retry (Docker just paused), then slow down
    intervals := []time.Duration{
        2 * time.Second,   // Immediate retry
        5 * time.Second,   // Quick retry
        10 * time.Second,  // Normal retry
        30 * time.Second,  // Slow retry
        60 * time.Second,  // Very slow retry
    }

    attempt := 0
    for {
        select {
        case <-retryCtx.Done():
            return
        case <-time.After(intervals[min(attempt, len(intervals)-1)]):
            if err := cpl.ensureDockerAvailable(retryCtx); err == nil {
                cpl.logger.Info("Docker available after recovery",
                    zap.Int("attempts", attempt+1),
                    zap.Duration("total_wait", time.Since(startTime)))
                // Recovery logic...
                return
            }
            attempt++
        }
    }
}
```

**Benefits:**
- Faster recovery when Docker quickly resumes (2s vs 5s)
- Lower CPU usage when Docker is off for extended periods
- Better battery life on laptops

---

## 🔴 Critical Issue #4: No "Recovering" State in Tray UI

**Problem:**
Tray shows `error_docker` state, but doesn't differentiate between:
1. "Docker is currently unavailable" (user needs to resume Docker)
2. "Docker just came back, reconnecting servers..." (recovery in progress)

**Impact:**
- Users don't know if recovery is happening
- Appears broken even when working correctly
- No visibility into recovery progress

**Recommended Fix:**

**Step 1:** Add new state in `cmd/mcpproxy-tray/internal/state/states.go`:
```go
const (
    // ... existing states ...
    StateCoreErrorDocker State = "core_error_docker"

    // ✅ ADD: New state for Docker recovery in progress
    StateCoreRecoveringDocker State = "core_recovering_docker"
)
```

**Step 2:** Add corresponding tray connection state in `internal/tray/connection_state.go`:
```go
const (
    // ... existing states ...
    ConnectionStateErrorDocker ConnectionState = "error_docker"

    // ✅ ADD: New state for recovery
    ConnectionStateRecoveringDocker ConnectionState = "recovering_docker"
)
```

**Step 3:** Update state mapping in `cmd/mcpproxy-tray/main.go:1019`:
```go
case state.StateCoreRecoveringDocker:
    trayState = tray.ConnectionStateRecoveringDocker
```

**Step 4:** Transition to recovering state when Docker comes back:
```go
// cmd/mcpproxy-tray/main.go:1385-1390
if err := cpl.ensureDockerAvailable(retryCtx); err == nil {
    cpl.logger.Info("Docker engine available - starting recovery")
    cpl.setDockerReconnectPending(true)
    cpl.cancelDockerRetry()

    // ✅ Transition to recovering state (not retry)
    cpl.stateMachine.SendEvent(state.EventDockerRecovered)
    return
}
```

**Step 5:** Update tray menu to show recovery status:
```go
// internal/tray/menu.go
case ConnectionStateRecoveringDocker:
    return "🔄 Recovering from Docker outage..."
```

**Files to modify:**
- `cmd/mcpproxy-tray/internal/state/states.go` (add new state + event)
- `internal/tray/connection_state.go` (add ConnectionStateRecoveringDocker)
- `cmd/mcpproxy-tray/main.go` (add state mapping + event handling)
- `internal/tray/menu.go` (add menu text for recovering state)

---

## 🟡 Important Issue #5: No Observability/Metrics

**Problem:**
No tracking of:
- How often Docker outages occur
- How long recovery takes
- Success/failure rate of reconnections
- Which servers failed to reconnect

**Recommended Fix:**

Add metrics struct:

```go
// cmd/mcpproxy-tray/main.go
type DockerRecoveryMetrics struct {
    OutageCount        int
    LastOutageTime     time.Time
    LastRecoveryTime   time.Time
    RecoveryDuration   time.Duration
    SuccessfulRecoveries int
    FailedRecoveries   int
}

func (cpl *CoreProcessLauncher) recordDockerOutage() {
    cpl.dockerMetrics.OutageCount++
    cpl.dockerMetrics.LastOutageTime = time.Now()
    cpl.logger.Info("Docker outage recorded",
        zap.Int("total_outages", cpl.dockerMetrics.OutageCount))
}

func (cpl *CoreProcessLauncher) recordDockerRecovery(success bool, duration time.Duration) {
    cpl.dockerMetrics.LastRecoveryTime = time.Now()
    cpl.dockerMetrics.RecoveryDuration = duration
    if success {
        cpl.dockerMetrics.SuccessfulRecoveries++
    } else {
        cpl.dockerMetrics.FailedRecoveries++
    }

    cpl.logger.Info("Docker recovery completed",
        zap.Bool("success", success),
        zap.Duration("duration", duration),
        zap.Int("success_count", cpl.dockerMetrics.SuccessfulRecoveries),
        zap.Int("failure_count", cpl.dockerMetrics.FailedRecoveries))
}
```

---

## 🟡 Important Issue #6: Force Reconnect Retries Too Aggressively

**Problem:**
Force reconnect API call has only 3 attempts with 2s delays:

```go
// cmd/mcpproxy-tray/main.go:1488-1495
const maxAttempts = 3
for attempt := 1; attempt <= maxAttempts; attempt++ {
    if err := cpl.apiClient.ForceReconnectAllServers(reason); err != nil {
        time.Sleep(2 * time.Second) // ❌ Linear backoff
        continue
    }
}
```

**Issues:**
- Linear 2s delay is arbitrary
- Only 3 attempts may not be enough if core is still starting upstream connections
- No jitter (thundering herd if multiple servers)

**Recommended Fix:**

Use exponential backoff with jitter:

```go
func (cpl *CoreProcessLauncher) triggerForceReconnect(reason string) {
    if cpl.apiClient == nil {
        return
    }

    backoff := []time.Duration{
        1 * time.Second,   // Fast first retry
        3 * time.Second,   // Medium retry
        5 * time.Second,   // Slow retry
        10 * time.Second,  // Very slow retry
    }

    for attempt := 0; attempt < len(backoff); attempt++ {
        if err := cpl.apiClient.ForceReconnectAllServers(reason); err != nil {
            cpl.logger.Warn("Failed to trigger upstream reconnection",
                zap.String("reason", reason),
                zap.Int("attempt", attempt+1),
                zap.Error(err))

            if attempt < len(backoff)-1 {
                // Add jitter ±20%
                jitter := time.Duration(float64(backoff[attempt]) * 0.2 * (rand.Float64()*2 - 1))
                time.Sleep(backoff[attempt] + jitter)
            }
            continue
        }

        cpl.logger.Info("Triggered upstream reconnection successfully",
            zap.String("reason", reason),
            zap.Int("attempt", attempt+1))
        return
    }
}
```

---

## 🟢 Nice-to-have Issue #7: Better Error Message Differentiation

**Problem:**
`ensureDockerAvailable()` distinguishes between "paused" and "unavailable" but state machine doesn't preserve this distinction.

**Recommended Fix:**

Add two separate Docker error states:
- `StateCoreErrorDockerPaused` - Docker Desktop manually paused
- `StateCoreErrorDockerDown` - Docker daemon not running

Update tray UI accordingly:
- Paused: "⏸️ Docker Desktop is paused - click Resume in Docker menu"
- Down: "⬇️ Docker Desktop is not running - start Docker Desktop"

---

## 🟢 Nice-to-have Issue #8: Configurable Timeouts

**Problem:**
Hardcoded timeouts may not suit all systems:
- 30s Docker cleanup timeout (line 22 in docs)
- 3s Docker info check (main.go:1431)
- 60s tool indexing interval (lifecycle.go:84)

**Recommended Fix:**

Add configuration options:
```json
{
  "docker_recovery": {
    "health_check_timeout": "3s",
    "cleanup_timeout": "30s",
    "polling_intervals": [2, 5, 10, 30, 60],
    "max_reconnect_attempts": 4
  }
}
```

---

## 🟡 Important Issue #9: No Partial Failure Handling

**Problem:**
If `ForceReconnectAll()` fails for some servers but succeeds for others, there's no granular status reporting.

**Recommended Fix:**

Return structured result from `ForceReconnectAll()`:

```go
type ReconnectResult struct {
    TotalServers      int
    AttemptedServers  int
    SuccessfulServers []string
    FailedServers     map[string]error
}

func (m *Manager) ForceReconnectAll(reason string) *ReconnectResult {
    result := &ReconnectResult{
        SuccessfulServers: []string{},
        FailedServers:     make(map[string]error),
    }

    // ... reconnection logic ...

    for id, client := range clientMap {
        result.TotalServers++

        if !shouldReconnect(client) {
            continue
        }

        result.AttemptedServers++

        if err := reconnectClient(id, client); err != nil {
            result.FailedServers[id] = err
        } else {
            result.SuccessfulServers = append(result.SuccessfulServers, id)
        }
    }

    return result
}
```

---

## 🟢 Nice-to-have Issue #10: No User Notification

**Problem:**
When recovery completes, users don't get explicit feedback that servers are operational again.

**Recommended Fix:**

Add system notification (macOS/Windows):
```go
// After successful recovery
notification := &tray.Notification{
    Title:   "Docker Recovery Complete",
    Message: fmt.Sprintf("%d servers reconnected successfully", len(successfulServers)),
    Icon:    tray.IconSuccess,
}
cpl.trayApp.ShowNotification(notification)
```

---

## 🔴 Critical Issue #11: Duplicate Container Spawning

**Problem:**
The system can spawn **duplicate containers** for the same server when the supervisor fails to detect container liveness. This is the **most dangerous issue** as it leads to:
- Resource exhaustion (multiple containers consuming memory/CPU)
- Port binding conflicts
- Data corruption if containers share state
- Confused system state (which container is "current"?)
- Orphaned containers that never get cleaned up

**Root Causes:**

1. **Race Condition During ForceReconnectAll()**
   ```go
   // internal/upstream/manager.go:890-903
   for id, client := range clientMap {
       if client.IsConnected() {
           continue  // ❌ No container health check!
       }

       // ❌ Multiple goroutines can enter here simultaneously
       cfg := cloneServerConfig(client.GetConfig())
       // ... recreate client (spawns new container)
   }
   ```

   **Scenario:**
   - Docker paused, 3 servers disconnected
   - `ForceReconnectAll()` called
   - All 3 clients reconnect in parallel goroutines
   - If same client gets reconnected twice → **2 containers spawned**

2. **Lost Container ID = Orphaned Containers**
   ```go
   // internal/upstream/core/docker.go:20-67
   for attempt := 0; attempt < 100; attempt++ { // Wait up to 10 seconds
       cidBytes, err := os.ReadFile(cidFile)
       if err == nil {
           c.containerID = containerID  // ✅ Tracked
           return
       }
   }
   // ❌ If cidfile read fails → c.containerID stays empty → orphan!
   ```

   **Scenario:**
   - Container spawned with slow image pull (takes 15s)
   - cidfile read timeout after 10s
   - `c.containerID` remains empty
   - Next reconnect spawns NEW container
   - Old container never cleaned up → **orphan**

3. **Random Container Names Don't Prevent Duplicates**
   ```go
   // internal/upstream/core/isolation.go:403-408
   func generateContainerName(serverName string) string {
       sanitized := sanitizeServerNameForContainer(serverName)
       // Generate 4-character random suffix
       return fmt.Sprintf("mcpproxy-%s-%s", sanitized, randomSuffix)
   }
   ```

   **Result:**
   - Server "github" → `mcpproxy-github-a1b2`
   - Reconnect → `mcpproxy-github-c3d4`
   - **Both containers exist simultaneously!**

4. **No Pre-Creation Check**
   ```go
   // internal/upstream/core/connection.go:260-328
   // ❌ Directly spawns container, no check for existing containers!
   dockerRunArgs, err := c.isolationManager.BuildDockerArgs(c.config, runtimeType)
   ```

   **Missing:**
   - `docker ps --filter name=mcpproxy-github-*` check
   - Cleanup of stale containers before creating new one

5. **Supervisor Liveness Detection Failures**

   **Scenario A: Slow Container Startup**
   - Container starts but takes 30s to respond (image pull + init)
   - Supervisor checks health after 20s → timeout
   - Supervisor thinks container dead → spawns new one
   - **Result: 2 containers, one still starting**

   **Scenario B: Network Partition**
   - Docker API unreachable due to network issue
   - Supervisor can't verify container health
   - Assumes container dead → spawns new one
   - Network recovers → **2 containers running**

   **Scenario C: Transient Docker API Errors**
   - Docker API returns 500 Internal Server Error
   - Supervisor retries immediately
   - Each retry spawns new container
   - **Result: Multiple containers**

**Evidence from Codebase:**

```go
// internal/upstream/core/docker.go:274-360
func (c *Client) killDockerContainersByNamePatternWithContext(ctx context.Context) bool {
    namePattern := "mcpproxy-" + sanitized + "-"

    // This finds ALL containers matching pattern
    listCmd := exec.CommandContext(ctx, "docker", "ps", "-a",
        "--filter", "name="+namePattern, "--format", "{{.ID}}\t{{.Names}}")

    // ⚠️ The fact that this returns MULTIPLE containers proves duplicates can exist!
    for _, containerID := range containersToKill {
        // Kill each one...
    }
}
```

The cleanup logic **already handles multiple containers** because it knows duplicates happen!

---

### **Comprehensive Solution: 5-Layer Defense**

#### **Layer 1: Idempotent Container Creation** 🔴 Critical

**Before creating any container, clean up ALL existing containers for that server:**

```go
// internal/upstream/core/connection.go (add before BuildDockerArgs)

func (c *Client) ensureNoExistingContainers(ctx context.Context) error {
    sanitized := sanitizeServerNameForContainer(c.config.Name)
    namePattern := "mcpproxy-" + sanitized + "-"

    c.logger.Info("Checking for existing containers before creation",
        zap.String("server", c.config.Name),
        zap.String("name_pattern", namePattern))

    // Find ALL containers matching our server (running or stopped)
    listCmd := exec.CommandContext(ctx, "docker", "ps", "-a",
        "--filter", "name="+namePattern,
        "--format", "{{.ID}}\t{{.Names}}\t{{.Status}}")

    output, err := listCmd.Output()
    if err != nil {
        return fmt.Errorf("failed to list existing containers: %w", err)
    }

    lines := strings.Split(strings.TrimSpace(string(output)), "\n")
    if len(lines) == 0 || lines[0] == "" {
        c.logger.Debug("No existing containers found - safe to create new one",
            zap.String("server", c.config.Name))
        return nil
    }

    // Found existing containers - clean them up first
    c.logger.Warn("Found existing containers - cleaning up before creating new one",
        zap.String("server", c.config.Name),
        zap.Int("container_count", len(lines)))

    for _, line := range lines {
        if line == "" {
            continue
        }
        parts := strings.SplitN(line, "\t", 3)
        if len(parts) >= 2 {
            containerID := parts[0]
            containerName := parts[1]
            status := ""
            if len(parts) >= 3 {
                status = parts[2]
            }

            c.logger.Info("Removing existing container",
                zap.String("server", c.config.Name),
                zap.String("container_id", containerID),
                zap.String("container_name", containerName),
                zap.String("status", status))

            // Force remove (works for running and stopped containers)
            rmCmd := exec.CommandContext(ctx, "docker", "rm", "-f", containerID)
            if err := rmCmd.Run(); err != nil {
                c.logger.Error("Failed to remove existing container",
                    zap.String("container_id", containerID),
                    zap.Error(err))
                // Continue anyway - try to remove others
            } else {
                c.logger.Info("Successfully removed existing container",
                    zap.String("container_id", containerID))
            }
        }
    }

    return nil
}
```

**Usage:**
```go
// internal/upstream/core/connection.go:260 (before BuildDockerArgs)
if willUseDocker {
    // ✅ CRITICAL: Clean up any existing containers first
    if err := c.ensureNoExistingContainers(connectCtx); err != nil {
        c.logger.Error("Failed to ensure no existing containers",
            zap.String("server", c.config.Name),
            zap.Error(err))
        // Continue anyway - we'll try to create
    }

    // Now safe to create new container
    dockerRunArgs, err := c.isolationManager.BuildDockerArgs(c.config, runtimeType)
    // ...
}
```

**Benefits:**
- ✅ Idempotent: Can call multiple times safely
- ✅ Prevents duplicates at creation time
- ✅ Cleans up orphaned containers automatically
- ✅ Works even if containerID was lost

---

#### **Layer 2: Container Labels for Ownership Tracking** 🟡 Important

**Label all containers with mcpproxy instance ID and server name:**

```go
// internal/upstream/core/isolation.go:262 (add after --name)

// Add labels for ownership tracking and cleanup
instanceID := getInstanceID() // Global instance UUID
labels := []string{
    "--label", "com.mcpproxy.managed=true",
    "--label", "com.mcpproxy.instance=" + instanceID,
    "--label", "com.mcpproxy.server=" + serverConfig.Name,
    "--label", "com.mcpproxy.created=" + time.Now().UTC().Format(time.RFC3339),
}
args = append(args, labels...)
```

**Add instance ID tracking:**
```go
// internal/upstream/core/instance.go (new file)

var (
    instanceID     string
    instanceIDOnce sync.Once
)

func getInstanceID() string {
    instanceIDOnce.Do(func() {
        // Try to load from file first
        if id, err := loadInstanceID(); err == nil {
            instanceID = id
            return
        }

        // Generate new instance ID
        instanceID = generateUUID()
        saveInstanceID(instanceID)
    })
    return instanceID
}

func loadInstanceID() (string, error) {
    data, err := os.ReadFile(filepath.Join(os.TempDir(), "mcpproxy-instance-id"))
    if err != nil {
        return "", err
    }
    return strings.TrimSpace(string(data)), nil
}

func saveInstanceID(id string) {
    os.WriteFile(filepath.Join(os.TempDir(), "mcpproxy-instance-id"), []byte(id), 0644)
}
```

**Improved cleanup using labels:**
```go
func (c *Client) cleanupOwnedContainers(ctx context.Context) error {
    instanceID := getInstanceID()
    serverName := c.config.Name

    // Find containers owned by THIS instance for THIS server
    listCmd := exec.CommandContext(ctx, "docker", "ps", "-a",
        "--filter", fmt.Sprintf("label=com.mcpproxy.instance=%s", instanceID),
        "--filter", fmt.Sprintf("label=com.mcpproxy.server=%s", serverName),
        "--format", "{{.ID}}\t{{.Label \"com.mcpproxy.created\"}}")

    // ... cleanup logic
}
```

**Benefits:**
- ✅ Accurate ownership tracking
- ✅ Can clean up containers from crashed instances
- ✅ Prevents conflicts with other mcpproxy instances
- ✅ Enables better debugging (docker ps --filter label=com.mcpproxy.managed=true)

---

#### **Layer 3: Distributed Lock for Container Creation** 🟡 Important

**Prevent race conditions during concurrent reconnection attempts:**

```go
// internal/upstream/core/container_lock.go (new file)

type ContainerLock struct {
    locks sync.Map // serverName -> *sync.Mutex
}

func (cl *ContainerLock) Lock(serverName string) *sync.Mutex {
    mutex, _ := cl.locks.LoadOrStore(serverName, &sync.Mutex{})
    m := mutex.(*sync.Mutex)
    m.Lock()
    return m
}

var globalContainerLock = &ContainerLock{}

// Usage in connection.go:
func (c *Client) connectStdio(connectCtx context.Context) error {
    if willUseDocker {
        // ✅ Acquire lock for this server
        lock := globalContainerLock.Lock(c.config.Name)
        defer lock.Unlock()

        // Now only ONE goroutine can create container for this server
        if err := c.ensureNoExistingContainers(connectCtx); err != nil {
            // ...
        }

        dockerRunArgs, err := c.isolationManager.BuildDockerArgs(c.config, runtimeType)
        // ...
    }
}
```

**Benefits:**
- ✅ Prevents concurrent container creation for same server
- ✅ Eliminates race condition in ForceReconnectAll()
- ✅ Simple implementation using sync.Mutex

---

#### **Layer 4: Enhanced Container Health Verification** 🔴 Critical

**Add comprehensive health check before skipping reconnection:**

```go
// internal/upstream/manager.go (enhance ForceReconnectAll)

func (m *Manager) verifyContainerHealthy(client *managed.Client) (bool, error) {
    containerID := client.GetContainerID()
    if containerID == "" {
        return false, fmt.Errorf("no container ID")
    }

    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()

    // Check 1: Container exists and is running
    inspectCmd := exec.CommandContext(ctx, "docker", "inspect",
        "--format", "{{.State.Running}},{{.State.Status}},{{.State.Health.Status}}",
        containerID)

    output, err := inspectCmd.Output()
    if err != nil {
        return false, fmt.Errorf("container not found: %w", err)
    }

    parts := strings.Split(strings.TrimSpace(string(output)), ",")
    if len(parts) < 2 {
        return false, fmt.Errorf("unexpected inspect output")
    }

    running := parts[0] == "true"
    status := parts[1]

    if !running {
        return false, fmt.Errorf("container not running (status: %s)", status)
    }

    // Check 2: Container is responsive (simple ping)
    // This catches cases where Docker is paused but sockets remain open
    pingCtx, pingCancel := context.WithTimeout(ctx, 2*time.Second)
    defer pingCancel()

    if err := client.Ping(pingCtx); err != nil {
        return false, fmt.Errorf("container not responsive: %w", err)
    }

    return true, nil
}

// Update ForceReconnectAll:
func (m *Manager) ForceReconnectAll(reason string) {
    for id, client := range clientMap {
        if client.IsConnected() {
            // ✅ For Docker servers, verify container health
            if client.IsDockerCommand() {
                healthy, err := m.verifyContainerHealthy(client)
                if !healthy {
                    m.logger.Warn("Container unhealthy despite active connection",
                        zap.String("server", id),
                        zap.Error(err))
                    // Force reconnect
                } else {
                    continue // Container healthy, skip
                }
            } else {
                continue // Non-Docker, connection check sufficient
            }
        }

        // Reconnect logic...
    }
}
```

**Benefits:**
- ✅ Detects frozen containers (Docker paused)
- ✅ Verifies actual responsiveness, not just connection state
- ✅ Prevents skipping reconnection for dead containers

---

#### **Layer 5: Graceful Degradation on cidfile Timeout** 🟡 Important

**If cidfile read fails, use container name to track container ID:**

```go
// internal/upstream/core/docker.go:67 (after cidfile timeout)

c.logger.Warn("Failed to read container ID from cidfile, attempting recovery via container name",
    zap.String("server", c.config.Name),
    zap.String("container_name", c.containerName))

// ✅ Fallback: Find container by name
if c.containerName != "" {
    listCmd := exec.CommandContext(ctx, "docker", "ps",
        "--filter", fmt.Sprintf("name=^%s$", c.containerName),
        "--format", "{{.ID}}")

    if output, err := listCmd.Output(); err == nil {
        foundID := strings.TrimSpace(string(output))
        if foundID != "" {
            c.mu.Lock()
            c.containerID = foundID
            c.mu.Unlock()

            c.logger.Info("Successfully recovered container ID via name lookup",
                zap.String("server", c.config.Name),
                zap.String("container_id", foundID),
                zap.String("container_name", c.containerName))
            return
        }
    }
}

c.logger.Error("Failed to recover container ID - container will be orphaned on disconnect",
    zap.String("server", c.config.Name),
    zap.String("container_name", c.containerName))
```

**Benefits:**
- ✅ Prevents orphaned containers
- ✅ Graceful fallback when cidfile fails
- ✅ Uses container name as secondary tracking mechanism

---

### **Impact Assessment**

**Without these fixes:**
- 🔴 **High probability** of duplicate containers during Docker recovery
- 🔴 **Resource exhaustion** on servers with frequent Docker issues
- 🔴 **Port conflicts** if containers bind to specific ports
- 🔴 **Orphaned containers** accumulate over time

**With these fixes:**
- ✅ **Idempotent creation** prevents duplicates
- ✅ **Container labels** enable reliable cleanup
- ✅ **Distributed locks** prevent race conditions
- ✅ **Health verification** catches stale containers
- ✅ **Graceful degradation** prevents orphans

---

### **Testing Plan for Duplicate Prevention**

1. **Concurrent Reconnection Test**
   ```bash
   # Start 5 Docker servers
   # Pause Docker Desktop
   # Trigger ForceReconnectAll() 3 times rapidly
   # Resume Docker Desktop
   # Expected: Each server has EXACTLY 1 container
   # Actual before fix: Each server has 2-3 containers
   ```

2. **cidfile Timeout Test**
   ```bash
   # Use very large Docker image (slow pull: 20s)
   # Set cidfile timeout to 5s
   # Trigger reconnection
   # Expected: Container ID recovered via name lookup
   # Verify: Container cleaned up on disconnect
   ```

3. **Orphan Container Test**
   ```bash
   # Start server, kill mcpproxy (SIGKILL)
   # Restart mcpproxy
   # Expected: Old container cleaned up before new one created
   # Verify: docker ps shows only 1 container per server
   ```

4. **Network Partition Test**
   ```bash
   # Start server
   # Block Docker API (iptables or firewall)
   # Trigger health check
   # Unblock Docker API
   # Expected: Health check detects unreachable container
   # Verify: New container created, old one removed
   ```

5. **Label-based Cleanup Test**
   ```bash
   # Create containers with instance ID labels
   # Crash mcpproxy (kill -9)
   # Start new mcpproxy instance (different instance ID)
   # Expected: Old containers cleaned up despite different instance ID
   # Verify: No orphaned containers remain
   ```

---

## Summary of Recommended Changes

### High Priority (Critical & Important)

| # | Issue | Files to Modify | Effort | Impact |
|---|-------|----------------|--------|--------|
| 1 | Filter Docker-only reconnections | `internal/upstream/manager.go` | Small | High |
| 2 | Add container health verification | `internal/upstream/manager.go` | Medium | High |
| 3 | Exponential backoff polling | `cmd/mcpproxy-tray/main.go` | Small | Medium |
| 4 | Add "Recovering" state | Multiple tray files | Medium | High |
| 6 | Better force reconnect retry logic | `cmd/mcpproxy-tray/main.go` | Small | Medium |
| 9 | Partial failure handling | `internal/upstream/manager.go` | Medium | Medium |
| **11** | **Duplicate container spawning** | **`internal/upstream/core/connection.go`, `manager.go`** | **Large** | **Critical** |

### Medium Priority (Nice-to-have)

| # | Issue | Files to Modify | Effort | Impact |
|---|-------|----------------|--------|--------|
| 5 | Add observability/metrics | `cmd/mcpproxy-tray/main.go` | Medium | Low |
| 7 | Better error differentiation | State machine files | Medium | Low |
| 8 | Configurable timeouts | Config + multiple files | Large | Low |
| 10 | User notifications | Tray app | Small | Low |

---

## Testing Recommendations

After implementing improvements, test the following scenarios:

1. **Basic pause/resume**
   - Pause Docker Desktop → Tray shows error
   - Resume Docker Desktop → Tray shows "recovering" → transitions to "connected"
   - Verify only Docker servers reconnected (not HTTP/SSE servers)

2. **Container health verification**
   - Pause Docker while server is connected
   - Resume Docker
   - Verify stale container connections are detected and recreated

3. **Exponential backoff**
   - Pause Docker → Verify polling starts at 2s, increases to 60s
   - Resume Docker quickly → Verify fast recovery (within 5s)
   - Leave Docker off for 5 minutes → Verify polling backs off to 60s intervals

4. **Partial failures**
   - Configure 3 Docker servers + 1 HTTP server
   - Break 1 Docker server (bad image name)
   - Pause/resume Docker
   - Verify status shows 2/3 Docker servers reconnected, HTTP server unaffected

5. **Metrics verification**
   - Check logs for outage count, recovery duration, success/failure rates
   - Verify metrics persist across tray restarts

---

## Implementation Plan

**Phase 1: Critical Fixes (Week 1)** - 17 hours
1. Issue #11: Duplicate container spawning (8 hours)
   - Layer 1: Idempotent creation (3 hours)
   - Layer 3: Distributed locks (2 hours)
   - Layer 4: Container health verification (3 hours)
2. Issue #1: Docker-only filtering (2 hours)
3. Issue #2: Container health verification (4 hours)
4. Issue #4: Add "Recovering" state (3 hours)

**Phase 2: Reliability Improvements (Week 2)** - 11 hours
1. Issue #11 (continued): Advanced container management (4 hours)
   - Layer 2: Container labels (2 hours)
   - Layer 5: cidfile timeout fallback (2 hours)
2. Issue #3: Exponential backoff (2 hours)
3. Issue #6: Better retry logic (2 hours)
4. Issue #9: Partial failure handling (3 hours)

**Phase 3: Observability & Polish (Week 3)** - 8 hours
1. Issue #5: Metrics/observability (4 hours)
2. Issue #7: Error differentiation (2 hours)
3. Issue #10: User notifications (2 hours)

**Phase 4: Configuration (Optional)** - 4 hours
1. Issue #8: Configurable timeouts (4 hours)

**Total effort estimate: 40 hours** (up from 28-32 hours due to Issue #11)

---

## References

- Original implementation: `docs/docker-resume-recovery.md`
- Tray state machine: `cmd/mcpproxy-tray/internal/state/`
- Upstream manager: `internal/upstream/manager.go`
- Docker isolation: `internal/upstream/core/isolation.go`
