# MCPProxy Shutdown Bug Analysis

## Overview
This document describes two critical shutdown bugs discovered in mcpproxy-go and the fixes applied.

---

## Bug #1: Core Process Hangs During SIGTERM Shutdown (FIXED)

### Symptom
When sending SIGTERM to the core `mcpproxy serve` process, it would hang indefinitely and never exit, leaving Docker containers orphaned.

### Test Case
```bash
./mcpproxy serve &
CORE_PID=$!
sleep 8  # Let servers start connecting
kill -TERM $CORE_PID
sleep 15  # Wait for graceful shutdown

# Expected: Core exits, all containers cleaned up
# Actual (before fix): Core still running after 15+ seconds
```

### Root Cause

**Deadlock in Parallel Disconnect Logic**

File: `internal/upstream/manager.go` - `ShutdownAll()` method

The parallel disconnect implementation had a critical deadlock:

1. **Lock Contention**: Test servers in "Connecting" state hold write locks while waiting for Docker containers to start (can take 10+ seconds)

2. **Blocking Goroutines**: Disconnect goroutines call `client.Disconnect()` which needs to acquire a write lock to update state

3. **Infinite Wait**: Main thread waits on `wg.Wait()` for all disconnect goroutines to complete

4. **Deadlock**: Disconnect goroutines wait for write locks that connecting servers hold, `wg.Wait()` blocks forever, container cleanup phase never runs

**Timeline of Events:**
```
T+0s:  SIGTERM received
T+0s:  Shutdown begins, 18 servers to disconnect
T+0s:  Launch 18 goroutines to disconnect in parallel
T+0s:  4 servers stuck in "Connecting" state with write locks held
T+0s:  14 servers disconnect successfully
T+0s:  4 disconnect goroutines block on Disconnect() waiting for write locks
T+∞:   wg.Wait() blocks forever, cleanup never runs
```

**Example from logs:**
```
12:19:09.691 | INFO  | Disconnecting all upstream servers (in parallel) | count=18
12:19:09.703 | DEBUG | Successfully disconnected server | id=gcore-mcp-server
12:19:09.703 | DEBUG | Successfully disconnected server | id=defillama2
12:19:09.703 | DEBUG | Successfully disconnected server | id=cloudflare-docs-sse
12:19:09.703 | DEBUG | Successfully disconnected server | id=everything-server
# ... 14 servers disconnect successfully ...
# ... logs stop here, 4 servers never disconnect ...
# ... no "All upstream servers disconnected successfully" log ...
# ... no "Cleaning up all mcpproxy-managed Docker containers" log ...
```

### The Fix

**File**: `internal/upstream/manager.go` (lines 419-444)

**Strategy**: Add a 5-second timeout on the disconnect phase. If goroutines are stuck, force-proceed to container cleanup which can handle stuck containers.

**Code Changes**:

```go
// BEFORE (lines 410-422):
done := make(chan struct{})
go func() {
    wg.Wait()
    close(done)
}()

select {
case <-done:
    m.logger.Info("All upstream servers disconnected successfully")
case <-ctx.Done():
    m.logger.Warn("Shutdown context cancelled, some servers may not have disconnected cleanly")
}

// Additional cleanup: Find and stop ALL mcpproxy-managed containers
m.cleanupAllManagedContainers(ctx)
```

```go
// AFTER (lines 419-450):
// Wait for all disconnections to complete (with timeout)
// Use a shorter timeout (5 seconds) for the disconnect phase
// If servers are stuck in "Connecting" state, their disconnection will hang
// In that case, we'll force-proceed to container cleanup which can handle it
done := make(chan struct{})
go func() {
    wg.Wait()
    close(done)
}()

disconnectTimeout := 5 * time.Second
disconnectTimer := time.NewTimer(disconnectTimeout)
defer disconnectTimer.Stop()

select {
case <-done:
    m.logger.Info("All upstream servers disconnected successfully")
case <-disconnectTimer.C:
    m.logger.Warn("Disconnect phase timed out after 5 seconds, forcing cleanup")
    m.logger.Warn("Some servers may not have disconnected cleanly (likely stuck in Connecting state)")
    // Don't wait for stuck goroutines - proceed with container cleanup anyway
case <-ctx.Done():
    m.logger.Warn("Shutdown context cancelled, forcing cleanup",
        zap.Error(ctx.Err()))
    // Don't wait for stuck goroutines - proceed with container cleanup anyway
}

// Additional cleanup: Find and stop ALL mcpproxy-managed containers
// This catches any orphaned containers from previous crashes AND any containers
// from servers that were stuck in "Connecting" state and couldn't disconnect
m.logger.Info("Starting Docker container cleanup phase")
m.cleanupAllManagedContainers(ctx)
```

**Key Changes**:
1. Added `disconnectTimeout := 5 * time.Second` and `time.NewTimer()`
2. Added `case <-disconnectTimer.C:` to select statement
3. Force-proceed to `cleanupAllManagedContainers()` after 5 seconds
4. Enhanced logging to show when timeout occurs

**Why This Works**:
- `cleanupAllManagedContainers()` uses `docker ps --filter "label=com.mcpproxy.managed=true"` to find containers
- It doesn't rely on managed client state or locks
- It force-stops containers that failed to disconnect gracefully
- Total shutdown time: ~9 seconds (5s disconnect timeout + 4s container cleanup)

### Test Results

**Before Fix**:
```
✗ Core still running after 15 seconds
✗ 4-7 containers orphaned
✗ Requires SIGKILL to terminate
```

**After Fix**:
```
✓ Core exits in 9 seconds
✓ All 7 containers cleaned up (100%)
✓ Graceful shutdown with proper cleanup
```

**Test Output**:
```
=== RESULTS ===
Shutdown time: 9s
Containers before: 7
Containers after: 0

✅ SUCCESS: All tests passed!
   - Core responded to SIGTERM
   - Core exited within 9s (under 15s limit)
   - All 7 containers cleaned up
```

### Files Modified
- `internal/upstream/manager.go` (lines 419-450)

### Commit Message
```
fix: Add 5-second timeout to shutdown disconnect phase to prevent deadlock

When servers are stuck in "Connecting" state with Docker containers starting,
their disconnect goroutines would block waiting for write locks. This caused
wg.Wait() to hang forever and the container cleanup phase never ran.

Now we timeout after 5 seconds and force-proceed to cleanupAllManagedContainers()
which can handle stuck containers using docker ps with label filters.

Fixes #XXX
```

---

## Bug #2: Tray Quit Doesn't Stop Core Process (UNFIXED)

### Symptom
When quitting the tray application via "Quit" menu item, the tray exits cleanly but the core `mcpproxy serve` process continues running with all Docker containers still active.

### Test Case
```bash
# Start tray application (which auto-starts core)
./mcpproxy-tray &

# Wait for core to be ready
sleep 10

# Click "Quit" in system tray menu
# Tray exits successfully

# Check processes
ps aux | grep mcpproxy
# Expected: No mcpproxy processes
# Actual: Core process (PID 35836) still running with 3 Docker containers
```

### Evidence from Logs

**Tray logs show clean shutdown:**
```json
{"level":"info","timestamp":"2025-11-03T12:38:39.200","message":"Quit item clicked, shutting down"}
{"level":"info","timestamp":"2025-11-03T12:38:39.200","message":"Tray shutdown requested"}
{"level":"info","timestamp":"2025-11-03T12:38:39.201","message":"Shutting down launcher..."}
{"level":"info","timestamp":"2025-11-03T12:38:39.201","message":"Core process launcher shutting down"}
{"level":"info","timestamp":"2025-11-03T12:38:39.201","message":"Disabling menu synchronization"}
{"level":"info","timestamp":"2025-11-03T12:38:39.201","message":"State transition","from":"connected","to":"shutting_down"}
{"level":"info","timestamp":"2025-11-03T12:38:39.205","message":"Tray application exiting"}
```

**But core process remains running:**
```bash
user  35836  0.9  /Users/user/repos/mcpproxy-go/mcpproxy serve
user  35857  0.1  docker run ... mcpproxy-cloudflare-docs-sse-smpp
user  35878  0.1  docker run ... mcpproxy-everything-server-5308
user  35880  0.1  docker run ... mcpproxy-defillama2-7eb8
```

### Root Cause Analysis

**Missing processMonitor Reference During Shutdown**

The tray's `handleShutdown()` function (line 1658-1717 in `main.go`) has proper logic to stop the core:

```go
// Line 1686-1714
if cpl.processMonitor != nil {
    cpl.logger.Info("Shutting down core process - waiting for termination...")
    // ... shutdown logic ...
} else {
    // SILENT SKIP - NO LOG, NO SHUTDOWN!
}
```

**The Problem**: When `processMonitor` is `nil`, the entire shutdown block is silently skipped.

**Evidence from User Logs**:
```json
{"timestamp":"2025-11-03T12:38:39.201","message":"Core process launcher shutting down"}
{"timestamp":"2025-11-03T12:38:39.201","message":"Disabling menu synchronization"}
// MISSING: "Stopping SSE connection" (line 1673)
// MISSING: "Stopping health monitor" (line 1681)
// MISSING: "Shutting down core process" (line 1687)
// MISSING: "Core shutdown complete" (line 1716)
{"timestamp":"2025-11-03T12:38:39.205","message":"Tray application exiting"}
```

**Why is processMonitor nil?**

Possible scenarios:
1. **Auto-detection mode**: Tray connects to existing core without launching it (processMonitor never created)
2. **Environment variable**: `MCPPROXY_TRAY_SKIP_CORE=1` skips core launch
3. **Custom core URL**: `MCPPROXY_CORE_URL` set, tray connects without launching
4. **Race condition**: processMonitor not initialized before shutdown called

**Files to Investigate**:
- `cmd/mcpproxy-tray/main.go` (lines 221-228) - NewCoreProcessLauncher initialization
- `cmd/mcpproxy-tray/main.go` (lines 1380-1450) - launchAndManageCore() where processMonitor is set
- Check if processMonitor is set before state reaches "connected"

### Recommended Fix

**Option 1: Add nil Check with Warning Log** (Minimal Change)

```go
// File: cmd/mcpproxy-tray/main.go, line 1686
func (cpl *CoreProcessLauncher) handleShutdown() {
    cpl.logger.Info("Core process launcher shutting down")

    // ... existing code lines 1661-1683 ...

    // Finally, kill the core process and WAIT for it to terminate
    if cpl.processMonitor != nil {
        cpl.logger.Info("Shutting down core process - waiting for termination...")
        // ... existing shutdown logic ...
    } else {
        // NEW: Log warning instead of silent skip
        cpl.logger.Warn("Process monitor is nil - cannot stop core process")
        cpl.logger.Warn("Core process may continue running after tray exits")

        // NEW: Attempt to find and kill core by PID file or port
        cpl.emergencyKillCore()
    }

    cpl.logger.Info("Core shutdown complete")
}

// NEW: Emergency shutdown when processMonitor is nil
func (cpl *CoreProcessLauncher) emergencyKillCore() {
    cpl.logger.Info("Attempting emergency core shutdown via PID discovery")

    // Try to find core process by looking for "mcpproxy serve" in process list
    cmd := exec.Command("pgrep", "-f", "mcpproxy serve")
    output, err := cmd.Output()
    if err != nil {
        cpl.logger.Warn("Could not find core process via pgrep", zap.Error(err))
        return
    }

    pidStr := strings.TrimSpace(string(output))
    if pidStr == "" {
        cpl.logger.Info("No mcpproxy serve process found")
        return
    }

    pid, err := strconv.Atoi(pidStr)
    if err != nil {
        cpl.logger.Warn("Invalid PID from pgrep", zap.String("output", pidStr))
        return
    }

    cpl.logger.Info("Found core process, sending SIGTERM", zap.Int("pid", pid))

    // Send SIGTERM to process group
    if err := syscall.Kill(-pid, syscall.SIGTERM); err != nil {
        cpl.logger.Warn("Failed to send SIGTERM to core", zap.Error(err))
        return
    }

    // Wait up to 15 seconds for graceful shutdown
    cpl.logger.Info("Waiting for core to exit...")
    for i := 0; i < 15; i++ {
        time.Sleep(1 * time.Second)

        // Check if process still exists
        if err := syscall.Kill(pid, 0); err != nil {
            if errors.Is(err, syscall.ESRCH) {
                cpl.logger.Info("Core process exited", zap.Int("after_seconds", i+1))
                return
            }
        }
    }

    // Force kill after timeout
    cpl.logger.Warn("Core did not exit gracefully, sending SIGKILL", zap.Int("pid", pid))
    syscall.Kill(-pid, syscall.SIGKILL)
    time.Sleep(1 * time.Second)
}
```

**Option 2: Always Track Core Process** (Better Long-term Solution)

Ensure `processMonitor` is always set when core is running, even in auto-detection mode:

```go
// When tray detects existing core:
func (cpl *CoreProcessLauncher) attachToExistingCore(pid int) {
    cpl.logger.Info("Attaching to existing core process", zap.Int("pid", pid))

    // Create a process monitor for the existing process
    // This allows us to track and terminate it on shutdown
    existingCmd := &exec.Cmd{
        Process: &os.Process{
            Pid: pid,
        },
    }

    cpl.processMonitor = monitor.NewFromExisting(existingCmd, cpl.logger)
}
```

**Option 3: Fix SSE Blocking** (If SSE is the issue)

Add timeout to `StopSSE()` call:

```go
// Line 1673-1674
cpl.logger.Info("Stopping SSE connection")
done := make(chan struct{})
go func() {
    cpl.apiClient.StopSSE()
    close(done)
}()

select {
case <-done:
    cpl.logger.Info("SSE stopped successfully")
case <-time.After(5 * time.Second):
    cpl.logger.Warn("SSE stop timed out, continuing with shutdown")
}
```

### Impact
- **Severity**: HIGH
- Users must manually kill core process and Docker containers after quitting tray
- Orphaned containers continue consuming resources
- Database remains locked, preventing new instances from starting

### Status
🔴 **UNFIXED** - Requires investigation and implementation

### Next Steps
1. Examine tray shutdown code in `cmd/mcpproxy-tray/main.go`
2. Identify where core process reference is stored
3. Add SIGTERM sending logic with timeout
4. Add SIGKILL fallback if graceful shutdown fails
5. Verify Docker container cleanup
6. Test with comprehensive test case

---

## Testing

### Test Script (Bug #1 - FIXED)
```bash
#!/bin/bash
# File: /tmp/test-shutdown-final.sh

./mcpproxy serve &
CORE_PID=$!
sleep 8

CONTAINERS_BEFORE=$(docker ps --filter "label=com.mcpproxy.managed=true" --format "{{.Names}}" | wc -l)
echo "Containers before: $CONTAINERS_BEFORE"

kill -TERM $CORE_PID

for i in {1..15}; do
    if ! kill -0 $CORE_PID 2>/dev/null; then
        echo "✓ Core exited after $i seconds"
        break
    fi
    sleep 1
done

CONTAINERS_AFTER=$(docker ps --filter "label=com.mcpproxy.managed=true" --format "{{.Names}}" | wc -l)
echo "Containers after: $CONTAINERS_AFTER"

if [ "$CONTAINERS_AFTER" -eq 0 ]; then
    echo "✅ SUCCESS"
else
    echo "❌ FAIL"
fi
```

### Test Script (Bug #2 - TODO)
```bash
#!/bin/bash
# File: /tmp/test-tray-quit.sh

./mcpproxy-tray &
TRAY_PID=$!
sleep 10

# Trigger quit via API or kill tray
kill -TERM $TRAY_PID

sleep 2

# Check if core is still running
CORE_RUNNING=$(ps aux | grep "mcpproxy serve" | grep -v grep | wc -l)
CONTAINERS_RUNNING=$(docker ps --filter "label=com.mcpproxy.managed=true" --format "{{.Names}}" | wc -l)

if [ "$CORE_RUNNING" -eq 0 ] && [ "$CONTAINERS_RUNNING" -eq 0 ]; then
    echo "✅ SUCCESS: Tray quit cleaned up everything"
else
    echo "❌ FAIL: Core=$CORE_RUNNING, Containers=$CONTAINERS_RUNNING still running"
fi
```

---

---

## Summary

### Bug #1 (FIXED ✅)
- **Issue**: Core hangs on SIGTERM, never exits, leaves Docker containers orphaned
- **Root Cause**: Deadlock in parallel disconnect - servers in "Connecting" state block disconnect goroutines
- **Fix**: Added 5-second timeout to force-proceed to container cleanup
- **Result**: Shutdown completes in 9 seconds with 100% container cleanup
- **Files Modified**: `internal/upstream/manager.go` (lines 419-450)

### Bug #2 (PARTIALLY FIXED ⚠️)
- **Issue**: Tray quit doesn't stop core process
- **Original Root Cause**: `processMonitor` is nil when handleShutdown() runs
- **Fix Applied**: Added ownership tracking, PID discovery via `/api/v1/status`, emergency shutdown path
- **New Issue Discovered**: `cmd.Wait()` race condition causes tray to hang
  - **Root Cause**: Both `monitor()` goroutine (line 286) and `Stop()` method (line 216) call `cmd.Wait()`
  - **Problem**: `cmd.Wait()` can only be called once per process
  - **Result**: Stop() hangs forever waiting on line 220 because monitor() already consumed the Wait()
  - **Core Behavior**: Core exits successfully after 9 seconds ✅
  - **Container Cleanup**: 6/7 containers cleaned up, 1 orphaned ❌
  - **Tray Behavior**: Tray hangs indefinitely, never exits ❌
- **Files to Fix**: `cmd/mcpproxy-tray/internal/monitor/process.go` (lines 196-233, 281-328)

## Related Issues
- Issue #XXX: Core process hangs on SIGTERM (FIXED)
- Issue #XXX: Tray quit doesn't stop core process (OPEN)

## References
- `internal/upstream/manager.go` - Upstream server lifecycle management
- `internal/upstream/core/connection.go` - Core client disconnect logic
- `cmd/mcpproxy-tray/main.go` - Tray application shutdown (lines 170-206, 1658-1717)
- `cmd/mcpproxy-tray/internal/monitor/process.go` - Process monitor Stop() method (lines 196-233)
- `CLAUDE.md` - Project architecture documentation

## Bug #2.1: cmd.Wait() Race Condition (NEW)

### Symptom
After applying the Bug #2 fix (ownership tracking + PID discovery), the tray now successfully terminates the core process, but the tray itself hangs indefinitely and never exits.

### Test Results (2025-11-03)
```
Containers before: 7
Core PID: 44886
Tray PID: 44869

Tray sends SIGTERM → Core exits in 9 seconds ✅
6/7 containers cleaned up ✅
1/7 container orphaned ❌
Tray hangs for 20+ seconds, never exits ❌
```

### Root Cause

**File**: `cmd/mcpproxy-tray/internal/monitor/process.go`

The `ProcessMonitor` struct has two goroutines trying to call `cmd.Wait()`:

1. **monitor() goroutine** (line 286): Started when process launches, calls `cmd.Wait()` to detect exit
2. **Stop() method** (line 216): Called during shutdown, also calls `cmd.Wait()` to wait for graceful exit

**The Problem**: `cmd.Wait()` can only be called **once** on a process. The second call blocks forever.

**Timeline**:
```
13:45:20.251 | Stop() called, sends SIGTERM to PID 44886
13:45:20.251 | Stop() calls cmd.Wait() on line 216 (goroutine waiting)
13:45:20.259 | monitor() goroutine calls cmd.Wait() on line 286 (consumes the Wait)
13:45:20.259 | monitor() logs "Process exited with error"
13:45:20.251+ | Stop() still waiting on line 220... HANGS FOREVER
```

### Evidence from Logs
```json
{"timestamp":"2025-11-03T13:45:20.251","message":"Stopping process","pid":44886}
{"timestamp":"2025-11-03T13:45:20.259","message":"Process exited with error","pid":44886}
// MISSING: "Process stopped gracefully" log (line 221 never reached)
```

### Recommended Fix

**Option 1: Use Event Channel Instead of cmd.Wait()**

Modify `Stop()` to wait for the monitor's event channel instead of calling `cmd.Wait()`:

```go
func (pm *ProcessMonitor) Stop() error {
	pm.mu.Lock()
	pid := pm.pid
	pm.mu.Unlock()

	if pid <= 0 {
		return fmt.Errorf("no process to stop")
	}

	pm.logger.Infow("Stopping process", "pid", pid)

	// Send SIGTERM to process group
	if err := syscall.Kill(-pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		pm.logger.Warn("Failed to send SIGTERM", "pid", pid, "error", err)
	}

	// Wait for monitor goroutine to detect exit via event channel
	timeout := time.NewTimer(45 * time.Second)
	defer timeout.Stop()

	for {
		select {
		case event, ok := <-pm.eventCh:
			if !ok {
				// Channel closed, monitor exited
				pm.logger.Infow("Process stopped (monitor exited)", "pid", pid)
				return nil
			}
			if event.Type == ProcessEventExit {
				pm.logger.Infow("Process stopped gracefully", "pid", pid, "exit_code", event.ExitCode)
				return event.Error
			}
		case <-timeout.C:
			pm.logger.Warn("Process did not stop gracefully after 45s, sending SIGKILL", "pid", pid)
			if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
				pm.logger.Error("Failed to send SIGKILL", "pid", pid, "error", err)
			}
			// Continue waiting for monitor to detect the kill
		}
	}
}
```

**Option 2: Add Done Channel to ProcessMonitor**

```go
type ProcessMonitor struct {
	// ... existing fields ...
	doneCh chan struct{} // Closed when monitor() exits
}

func (pm *ProcessMonitor) monitor() {
	defer close(pm.eventCh)
	defer close(pm.doneCh) // NEW

	err := pm.cmd.Wait()
	// ... rest of monitor logic ...
}

func (pm *ProcessMonitor) Stop() error {
	// ... send SIGTERM ...

	select {
	case <-pm.doneCh:
		pm.logger.Info("Process stopped successfully")
		return pm.GetExitInfo().Error
	case <-time.After(45 * time.Second):
		pm.logger.Warn("Timeout waiting for process to stop")
		syscall.Kill(-pid, syscall.SIGKILL)
		<-pm.doneCh // Wait for monitor to finish
		return fmt.Errorf("process force killed")
	}
}
```

### Secondary Issue: Goroutine Leak

The logs show API calls continuing 6 seconds after shutdown:
```
13:45:26.502 | Failed to fetch upstream servers
13:45:32.507 | Failed to fetch upstream servers
```

This suggests the tray's synchronization loop or SSE reconnect logic is still running. Need to verify:
- SSE stop timeout is working (5 seconds per user's description)
- Synchronization loop respects context cancellation
- No goroutines are started without proper cleanup

## Testing Recommendations

1. **Test direct core shutdown**: `kill -TERM $(pgrep "mcpproxy serve")` - PASS ✅
2. **Test tray shutdown**: Start tray, quit via menu, verify core exits - PARTIAL ⚠️
   - Core exits: YES ✅
   - Containers cleaned: 6/7 ✅
   - Tray exits: NO ❌ (hangs due to cmd.Wait() race)
3. **Test auto-detection mode**: Start core first, then tray, then quit tray - UNTESTED
4. **Test skip-core mode**: `MCPPROXY_TRAY_SKIP_CORE=1 ./mcpproxy-tray` then quit - UNTESTED

---

*Document created: 2025-11-03*
*Bug #1 fixed: 2025-11-03*
*Bug #2 partially fixed: 2025-11-03*
*Bug #2.1 discovered: 2025-11-03*
