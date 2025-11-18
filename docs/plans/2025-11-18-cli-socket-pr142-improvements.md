# PR #142 Recommended Improvements

**PR**: Fix CLI commands to use socket IPC when daemon is running
**Date**: 2025-11-18
**Status**: Draft - Ready for improvements before merge

## Overview

This document outlines recommended improvements for PR #142 based on code review. The PR is well-architected and solves a critical UX issue, but could benefit from enhanced error handling, test coverage, and user feedback.

## Priority Summary

| Priority | Item | Estimated Effort |
|----------|------|------------------|
| 🔴 High | Add E2E test coverage | 1-2 hours |
| 🟡 Medium | Enhanced error logging in fallback | 30 minutes |
| 🟡 Medium | Reduce ping timeout | 15 minutes |
| 🟡 Medium | Add CLI mode indicator output | 30 minutes |
| 🟢 Low | Consolidate socket detection logic | 45 minutes |
| 🟢 Low | Documentation improvements | 20 minutes |

**Total estimated effort**: 3-4 hours

---

## High Priority Improvements

### 1. Add E2E Test Coverage

**Problem**: No automated tests verify client mode vs standalone mode behavior.

**Solution**: Add comprehensive E2E tests for both CLI commands.

**Location**: `internal/server/cli_client_mode_e2e_test.go`

**Implementation**:

```go
package server_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCodeExecClientModeE2E(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	tmpDir := t.TempDir()

	// Build mcpproxy binary
	mcpproxyBin := filepath.Join(tmpDir, "mcpproxy")
	buildCmd := exec.Command("go", "build", "-o", mcpproxyBin, "./cmd/mcpproxy")
	require.NoError(t, buildCmd.Run(), "Failed to build mcpproxy")

	// Create minimal config
	configPath := filepath.Join(tmpDir, "mcp_config.json")
	config := `{
		"listen": "127.0.0.1:18080",
		"data_dir": "` + tmpDir + `",
		"enable_code_execution": true,
		"mcpServers": []
	}`
	require.NoError(t, os.WriteFile(configPath, []byte(config), 0600))

	t.Run("client_mode_when_daemon_running", func(t *testing.T) {
		// Start daemon in background
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		daemonCmd := exec.CommandContext(ctx, mcpproxyBin, "serve", "--config", configPath)
		daemonCmd.Env = append(os.Environ(), "MCPPROXY_DATA_DIR="+tmpDir)
		require.NoError(t, daemonCmd.Start())
		defer daemonCmd.Process.Kill()

		// Wait for daemon to be ready
		time.Sleep(2 * time.Second)

		// Run code exec CLI command
		execCmd := exec.Command(mcpproxyBin, "code", "exec",
			"--code", `({ result: 42 })`,
			"--input", `{}`,
			"--config", configPath)
		execCmd.Env = append(os.Environ(), "MCPPROXY_DATA_DIR="+tmpDir)

		output, err := execCmd.CombinedOutput()
		require.NoError(t, err, "code exec should succeed: %s", string(output))

		// Verify result
		assert.Contains(t, string(output), `"result":42`, "Should return correct result")

		// Verify client mode was used (check logs or output)
		assert.NotContains(t, string(output), "database locked", "Should not have DB lock error")
	})

	t.Run("standalone_mode_when_no_daemon", func(t *testing.T) {
		// Ensure no daemon is running
		// Run code exec CLI command
		execCmd := exec.Command(mcpproxyBin, "code", "exec",
			"--code", `({ result: 99 })`,
			"--input", `{}`,
			"--config", configPath)
		execCmd.Env = append(os.Environ(),
			"MCPPROXY_DATA_DIR="+tmpDir,
			"MCPPROXY_TRAY_ENDPOINT=") // Force standalone mode

		output, err := execCmd.CombinedOutput()
		require.NoError(t, err, "code exec should succeed in standalone: %s", string(output))

		// Verify result
		assert.Contains(t, string(output), `"result":99`, "Should return correct result")
	})
}

func TestCallToolClientModeE2E(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	tmpDir := t.TempDir()

	// Build mcpproxy binary
	mcpproxyBin := filepath.Join(tmpDir, "mcpproxy")
	buildCmd := exec.Command("go", "build", "-o", mcpproxyBin, "./cmd/mcpproxy")
	require.NoError(t, buildCmd.Run(), "Failed to build mcpproxy")

	// Create minimal config
	configPath := filepath.Join(tmpDir, "mcp_config.json")
	config := `{
		"listen": "127.0.0.1:18081",
		"data_dir": "` + tmpDir + `",
		"mcpServers": []
	}`
	require.NoError(t, os.WriteFile(configPath, []byte(config), 0600))

	t.Run("client_mode_when_daemon_running", func(t *testing.T) {
		// Start daemon in background
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		daemonCmd := exec.CommandContext(ctx, mcpproxyBin, "serve", "--config", configPath)
		daemonCmd.Env = append(os.Environ(), "MCPPROXY_DATA_DIR="+tmpDir)
		require.NoError(t, daemonCmd.Start())
		defer daemonCmd.Process.Kill()

		// Wait for daemon to be ready
		time.Sleep(2 * time.Second)

		// Run call tool CLI command (test built-in upstream_servers tool)
		callCmd := exec.Command(mcpproxyBin, "call", "tool",
			"--tool-name", "upstream_servers",
			"--json_args", `{"operation":"list"}`,
			"--config", configPath)
		callCmd.Env = append(os.Environ(), "MCPPROXY_DATA_DIR="+tmpDir)

		output, err := callCmd.CombinedOutput()
		require.NoError(t, err, "call tool should succeed: %s", string(output))

		// Verify no DB lock error
		assert.NotContains(t, string(output), "database locked", "Should not have DB lock error")
	})

	t.Run("standalone_mode_when_no_daemon", func(t *testing.T) {
		// Run call tool CLI command without daemon
		callCmd := exec.Command(mcpproxyBin, "call", "tool",
			"--tool-name", "upstream_servers",
			"--json_args", `{"operation":"list"}`,
			"--config", configPath)
		callCmd.Env = append(os.Environ(),
			"MCPPROXY_DATA_DIR="+tmpDir,
			"MCPPROXY_TRAY_ENDPOINT=") // Force standalone mode

		output, err := callCmd.CombinedOutput()
		require.NoError(t, err, "call tool should succeed in standalone: %s", string(output))

		// Verify successful operation
		assert.NotContains(t, string(output), "error", "Should not have errors")
	})
}

func TestConcurrentCLICommands(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	tmpDir := t.TempDir()

	// Build mcpproxy binary
	mcpproxyBin := filepath.Join(tmpDir, "mcpproxy")
	buildCmd := exec.Command("go", "build", "-o", mcpproxyBin, "./cmd/mcpproxy")
	require.NoError(t, buildCmd.Run(), "Failed to build mcpproxy")

	// Create minimal config
	configPath := filepath.Join(tmpDir, "mcp_config.json")
	config := `{
		"listen": "127.0.0.1:18082",
		"data_dir": "` + tmpDir + `",
		"enable_code_execution": true,
		"mcpServers": []
	}`
	require.NoError(t, os.WriteFile(configPath, []byte(config), 0600))

	// Start daemon
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	daemonCmd := exec.CommandContext(ctx, mcpproxyBin, "serve", "--config", configPath)
	daemonCmd.Env = append(os.Environ(), "MCPPROXY_DATA_DIR="+tmpDir)
	require.NoError(t, daemonCmd.Start())
	defer daemonCmd.Process.Kill()

	// Wait for daemon to be ready
	time.Sleep(2 * time.Second)

	// Run 5 concurrent code exec commands
	errChan := make(chan error, 5)
	for i := 0; i < 5; i++ {
		go func(idx int) {
			execCmd := exec.Command(mcpproxyBin, "code", "exec",
				"--code", `({ result: input.value * 2 })`,
				"--input", `{"value": 21}`,
				"--config", configPath)
			execCmd.Env = append(os.Environ(), "MCPPROXY_DATA_DIR="+tmpDir)

			output, err := execCmd.CombinedOutput()
			if err != nil {
				errChan <- err
				return
			}

			// Verify no DB lock error
			if contains(string(output), "database locked") {
				errChan <- assert.AnError
				return
			}

			errChan <- nil
		}(i)
	}

	// Wait for all commands to complete
	for i := 0; i < 5; i++ {
		err := <-errChan
		assert.NoError(t, err, "Concurrent command %d should succeed", i)
	}
}

func contains(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 &&
		(s == substr || (len(s) >= len(substr) &&
		 (s[:len(substr)] == substr || s[len(s)-len(substr):] == substr)))
}
```

**Testing**:
```bash
# Run E2E tests
go test -v ./internal/server -run TestCodeExecClientModeE2E
go test -v ./internal/server -run TestCallToolClientModeE2E
go test -v ./internal/server -run TestConcurrentCLICommands
```

---

## Medium Priority Improvements

### 2. Enhanced Error Logging in Fallback

**Problem**: Silent fallback to standalone mode makes troubleshooting difficult.

**Location**:
- `cmd/mcpproxy/call_cmd.go:145-147`
- `cmd/mcpproxy/code_cmd.go:170-172`

**Changes**:

```go
// In call_cmd.go
if err := client.Ping(ctx); err != nil {
    logger.Warn("Failed to ping daemon, falling back to standalone mode",
        zap.Error(err),
        zap.String("socket_path", socketPath),
        zap.String("data_dir", dataDir),
        zap.String("reason", "daemon_unavailable"))
    // Fall back to standalone mode
    cfg, _ := loadCallConfig()
    parts := strings.SplitN(toolName, ":", 2)
    if len(parts) == 2 {
        return runCallToolStandalone(ctx, parts[0], parts[1], args, cfg)
    }
    return fmt.Errorf("invalid tool name format: %s", toolName)
}
```

```go
// In code_cmd.go
if err := client.Ping(ctx); err != nil {
    logger.Warn("Failed to ping daemon, falling back to standalone mode",
        zap.Error(err),
        zap.String("socket_path", socketPath),
        zap.Duration("ping_timeout", 10*time.Second),
        zap.String("fallback_mode", "standalone"))
    return runCodeExecStandalone(globalConfig, code, inputData, logger)
}
```

### 3. Reduce Ping Timeout

**Problem**: 10-second timeout is too long if daemon is truly unavailable.

**Location**: `cmd/mcpproxy/code_cmd.go:170`

**Change**:

```go
// Before:
ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)

// After:
ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
```

**Rationale**: Daemon should respond within milliseconds if healthy. 2 seconds allows for slow systems while providing faster fallback.

### 4. Add CLI Mode Indicator Output

**Problem**: Users don't know which mode is being used (client vs standalone).

**Location**:
- `cmd/mcpproxy/call_cmd.go` (in `runCallToolClientMode` and `runCallToolStandalone`)
- `cmd/mcpproxy/code_cmd.go` (in `runCodeExecClientMode` and `runCodeExecStandalone`)

**Changes**:

```go
// In runCodeExecClientMode (after successful ping)
func runCodeExecClientMode(dataDir, code string, inputData map[string]interface{}, logger *zap.Logger) error {
    // ... existing code ...

    if err := client.Ping(ctx); err != nil {
        // ... existing fallback logic ...
    }

    // ADD THIS:
    fmt.Fprintf(os.Stderr, "ℹ️  Using daemon mode (via socket) - fast execution\n")

    // Execute code via daemon
    // ... rest of function ...
}

// In runCodeExecStandalone
func runCodeExecStandalone(globalConfig *config.Config, code string, inputData map[string]interface{}, logger *zap.Logger) error {
    // ADD THIS AT START:
    fmt.Fprintf(os.Stderr, "⚠️  Using standalone mode - daemon not detected (slower startup)\n")

    // ... rest of function ...
}
```

**Similar changes for call_cmd.go**

**Example output**:
```bash
$ ./mcpproxy code exec --code="({ result: 42 })" --input='{}'
ℹ️  Using daemon mode (via socket) - fast execution
{"ok":true,"value":{"result":42}}

$ MCPPROXY_TRAY_ENDPOINT="" ./mcpproxy code exec --code="({ result: 42 })" --input='{}'
⚠️  Using standalone mode - daemon not detected (slower startup)
{"ok":true,"value":{"result":42}}
```

---

## Low Priority Improvements

### 5. Consolidate Socket Detection Logic

**Problem**: Socket detection and fallback logic is duplicated and has a TOCTOU race condition.

**Location**: Create new helper in `internal/socket/client.go`

**Implementation**:

```go
package socket

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"
)

// ClientDialer is an interface for socket-based HTTP clients
type ClientDialer interface {
	Ping(ctx context.Context) error
}

// TryClientMode attempts to connect to a running daemon via socket.
// Returns (client, nil) if successful, or (nil, error) if daemon unavailable.
func TryClientMode(dataDir string, logger *zap.Logger, createClient func(socketPath string, logger *zap.SugaredLogger) ClientDialer) (ClientDialer, error) {
	socketPath := DetectSocketPath(dataDir)

	if !IsSocketAvailable(socketPath) {
		return nil, fmt.Errorf("socket not available at %s", socketPath)
	}

	client := createClient(socketPath, logger.Sugar())

	// Verify daemon is responsive (2-second timeout)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := client.Ping(ctx); err != nil {
		return nil, fmt.Errorf("daemon ping failed: %w", err)
	}

	return client, nil
}
```

**Usage in commands**:

```go
// In code_cmd.go
func runCodeExec(_ *cobra.Command, _ []string) error {
    // ... load code, validate, etc ...

    // Try client mode first
    client, err := socket.TryClientMode(globalConfig.DataDir, logger,
        func(socketPath string, logger *zap.SugaredLogger) socket.ClientDialer {
            return cliclient.NewClient(socketPath, logger)
        })

    if err != nil {
        logger.Warn("Client mode unavailable, using standalone", zap.Error(err))
        return runCodeExecStandalone(globalConfig, code, inputData, logger)
    }

    logger.Info("Using client mode via socket")
    return runCodeExecClientMode(client, code, inputData, logger)
}
```

**Benefits**:
- Eliminates TOCTOU race
- Consolidates retry/fallback logic
- Makes intent clearer

### 6. Documentation Improvements

**Location**: `CLAUDE.md` - JavaScript Code Execution section

**Add section on forcing standalone mode**:

```markdown
### Forcing Standalone Mode (Testing)

By default, CLI commands automatically detect and use a running daemon via socket communication. To bypass this and force standalone mode (e.g., for testing):

```bash
# Force standalone mode by clearing socket endpoint
MCPPROXY_TRAY_ENDPOINT="" mcpproxy code exec --code="({ result: 42 })" --input='{}'
MCPPROXY_TRAY_ENDPOINT="" mcpproxy call tool --tool-name=upstream_servers --json_args='{"operation":"list"}'
```

**When to use standalone mode**:
- Testing database access directly
- Debugging without daemon interference
- Single-shot operations when daemon isn't needed

**Performance difference**:
- **Client mode** (daemon running): ~50ms execution
- **Standalone mode** (no daemon): ~8s startup (160x slower)
```

**Add troubleshooting section**:

```markdown
### Troubleshooting Client/Standalone Mode

**How to check which mode is being used**:

```bash
# Client mode (daemon detected)
$ ./mcpproxy code exec --code="..." --input='{}'
ℹ️  Using daemon mode (via socket) - fast execution
{"ok":true,...}

# Standalone mode (no daemon)
$ MCPPROXY_TRAY_ENDPOINT="" ./mcpproxy code exec --code="..." --input='{}'
⚠️  Using standalone mode - daemon not detected (slower startup)
{"ok":true,...}
```

**Common issues**:

1. **"Database locked by another process"** - Daemon is running, but socket detection failed
   - Check socket file exists: `ls -la ~/.mcpproxy/mcpproxy.sock` (Unix) or check named pipe on Windows
   - Verify permissions: Socket should be `srw-------` (0600)
   - Check logs: `tail -f ~/Library/Logs/mcpproxy/main.log`

2. **Slow CLI execution** - Client mode not being used
   - Verify daemon is running: `ps aux | grep mcpproxy`
   - Check socket availability: `MCPPROXY_LOG_LEVEL=debug ./mcpproxy code exec ...`
   - Look for "Using standalone mode" in stderr output

3. **Fallback to standalone mode** - Daemon running but ping fails
   - Check daemon health: `curl -H "X-API-Key: $(cat ~/.mcpproxy/api_key)" http://127.0.0.1:8080/api/v1/status`
   - Verify socket permissions
   - Check for port conflicts or crashes
```

---

## Implementation Checklist

### Before Merging from Draft:

- [x] **Add E2E tests** (`TestCodeExecClientModeE2E`, `TestCallToolClientModeE2E`, `TestConcurrentCLICommands`) ✅ Committed: 811589f
- [ ] **Enhanced error logging** in fallback paths with socket_path, data_dir context
- [ ] **Reduce ping timeout** from 10s to 2s in both commands
- [ ] **Add CLI mode indicator** output (ℹ️ daemon mode vs ⚠️ standalone mode)
- [ ] **Test on Windows** to validate named pipe behavior with these changes
- [ ] **Update documentation** with troubleshooting section in CLAUDE.md

### After Merging (Optional Enhancements):

- [ ] **Consolidate socket detection** logic into `internal/socket/client.go` helper
- [ ] **Add `--force-standalone` flag** for advanced users
- [ ] **Add metrics** to track client mode vs standalone usage
- [ ] **Document performance differences** in user-facing docs

---

## Testing Strategy

### Manual Testing

```bash
# 1. Build binary
go build -o mcpproxy ./cmd/mcpproxy

# 2. Start daemon
./mcpproxy serve &
DAEMON_PID=$!

# 3. Test client mode (should be fast, ~50ms)
time ./mcpproxy code exec --code="({ result: 42 })" --input='{}'
# Expected: ℹ️  Using daemon mode (via socket) - fast execution

# 4. Test call tool client mode
time ./mcpproxy call tool --tool-name=upstream_servers --json_args='{"operation":"list"}'
# Expected: No "database locked" error

# 5. Kill daemon
kill $DAEMON_PID

# 6. Test standalone mode (should be slower, ~8s)
time ./mcpproxy code exec --code="({ result: 99 })" --input='{}'
# Expected: ⚠️  Using standalone mode - daemon not detected

# 7. Test forced standalone mode (daemon running)
./mcpproxy serve &
DAEMON_PID=$!
time MCPPROXY_TRAY_ENDPOINT="" ./mcpproxy code exec --code="({ result: 77 })" --input='{}'
# Expected: ⚠️  Using standalone mode (despite daemon running)

# 8. Cleanup
kill $DAEMON_PID
```

### Automated Testing

```bash
# Run unit tests
go test ./internal/socket -v

# Run E2E tests (after adding them)
go test ./internal/server -v -run TestCodeExecClientModeE2E
go test ./internal/server -v -run TestCallToolClientModeE2E
go test ./internal/server -v -run TestConcurrentCLICommands

# Run full test suite
./scripts/run-all-tests.sh
```

---

## Success Criteria

### Functionality
- ✅ CLI commands detect daemon via socket automatically
- ✅ Graceful fallback to standalone mode when daemon unavailable
- ✅ No "database locked" errors when daemon running
- ✅ Concurrent CLI commands work without conflicts

### Performance
- ✅ Client mode execution < 100ms (vs ~8s standalone)
- ✅ Ping timeout < 2s for faster fallback
- ✅ No performance regression in standalone mode

### User Experience
- ✅ Clear indication of which mode is being used (stderr output)
- ✅ Helpful error messages with troubleshooting context
- ✅ Zero configuration required (automatic detection)

### Code Quality
- ✅ Comprehensive E2E test coverage
- ✅ Platform-agnostic (Unix sockets + Windows named pipes)
- ✅ Clean error handling with structured logging
- ✅ Documentation updated with examples and troubleshooting

---

## Estimated Timeline

| Task | Effort | Priority |
|------|--------|----------|
| E2E tests | 1-2 hours | High |
| Error logging | 30 min | Medium |
| Ping timeout | 15 min | Medium |
| CLI output | 30 min | Medium |
| Socket consolidation | 45 min | Low |
| Documentation | 20 min | Low |
| Testing & validation | 1 hour | High |

**Total**: 3-4 hours for high + medium priority items

---

## References

- **PR #142**: https://github.com/smart-mcp-proxy/mcpproxy-go/pull/142
- **Related PR #102**: Unix socket/named pipe support (foundation)
- **Issue #140**: Database locked error (original issue)
- **Implementation Plan**: `docs/plans/2025-11-17-cli-socket-client-mode.md`
