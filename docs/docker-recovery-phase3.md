# Docker Recovery - Phase 3: Nice-to-Have Features

## Overview
This PR implements the remaining enhancements from the Docker recovery improvement plan. These are non-critical features that improve observability, user experience, and operational excellence.

**Previous PR**: #120 (Critical fixes - MERGED)
**This PR**: Phase 3 enhancements

## Features to Implement

### Issue #5: Persistent Retry State Across Restarts (4 hours)
**Priority**: Medium | **Effort**: 4h

**Problem**: When mcpproxy restarts, Docker recovery state is lost. If Docker is still down, the system doesn't remember it was in recovery mode.

**Solution**:
- Persist recovery state to BBolt database
- Store: last recovery attempt time, failure count, Docker availability status
- On startup, check persisted state and resume recovery if needed
- Clear state after successful recovery

**Files to modify**:
- `internal/storage/manager.go` - Add recovery state schema
- `cmd/mcpproxy-tray/internal/state/machine.go` - Load/save state
- `cmd/mcpproxy-tray/main.go` - Resume recovery on startup

**Implementation**:
```go
// storage schema
type DockerRecoveryState struct {
    LastAttempt      time.Time
    FailureCount     int
    DockerAvailable  bool
    RecoveryMode     bool
}

// On startup
if state := loadDockerRecoveryState(); state.RecoveryMode {
    resumeDockerRecovery(state)
}
```

**Testing**:
- Start with Docker down
- Restart mcpproxy during recovery
- Verify recovery resumes automatically

---

### Issue #6: User Notification Improvements (2 hours)
**Priority**: Medium | **Effort**: 2h

**Problem**: Users aren't clearly notified about Docker recovery progress and outcomes.

**Solution**:
- Add system notification when Docker recovery starts
- Add notification when recovery succeeds
- Add notification when recovery fails after max retries
- Show estimated time until next retry

**Files to modify**:
- `cmd/mcpproxy-tray/main.go` - Add notification calls
- `internal/tray/notifications.go` - Add recovery notifications

**Implementation**:
```go
// When Docker recovery starts
showNotification("Docker Recovery", "Docker engine detected offline. Reconnecting servers...")

// When recovery succeeds
showNotification("Recovery Complete", "Successfully reconnected X servers")

// When recovery fails
showNotification("Recovery Failed", "Unable to reconnect servers. Check Docker status.")
```

**Testing**:
- Verify notifications appear at appropriate times
- Test on macOS, Windows, Linux

---

### Issue #7: Health Check Configuration (3 hours)
**Priority**: Low | **Effort**: 3h

**Problem**: Docker health check intervals and timeouts are hardcoded. Power users may want to customize these.

**Solution**:
- Add configuration options for health check behavior
- Allow customization of polling intervals
- Allow customization of max retries and timeouts

**Files to modify**:
- `internal/config/config.go` - Add docker_recovery section
- `cmd/mcpproxy-tray/main.go` - Use config values
- `docs/configuration.md` - Document options

**Configuration schema**:
```json
{
  "docker_recovery": {
    "enabled": true,
    "health_check": {
      "initial_delay": "2s",
      "intervals": ["2s", "5s", "10s", "30s", "60s"],
      "max_retries": 10,
      "timeout": "5s"
    }
  }
}
```

**Testing**:
- Test with custom intervals
- Test with disabled recovery
- Verify defaults work when not configured

---

### Issue #8: Metrics and Monitoring (6 hours)
**Priority**: Low | **Effort**: 6h

**Problem**: No visibility into Docker recovery statistics and success rates over time.

**Solution**:
- Track Docker recovery metrics
- Expose via `/api/v1/metrics` endpoint
- Store historical data in BBolt

**Metrics to track**:
- Total recovery attempts
- Successful vs failed recoveries
- Average recovery time
- Last recovery timestamp
- Per-server recovery success rate

**Files to modify**:
- `internal/storage/manager.go` - Add metrics schema
- `internal/httpapi/server.go` - Add `/api/v1/metrics` endpoint
- `cmd/mcpproxy-tray/main.go` - Record metrics during recovery

**API response**:
```json
{
  "docker_recovery": {
    "total_attempts": 42,
    "successful": 38,
    "failed": 4,
    "success_rate": 0.904,
    "avg_recovery_time_seconds": 12.5,
    "last_recovery": "2025-11-02T10:30:00Z",
    "per_server_stats": {
      "everything-server": {
        "attempts": 5,
        "successes": 5,
        "avg_time_seconds": 8.2
      }
    }
  }
}
```

**Testing**:
- Trigger multiple recovery cycles
- Verify metrics are accurate
- Test metrics endpoint

---

### Issue #10: Documentation Updates (2 hours)
**Priority**: Medium | **Effort**: 2h

**Problem**: Users don't know about Docker recovery features and how to configure them.

**Solution**:
- Update README.md with Docker recovery section
- Create troubleshooting guide
- Document configuration options

**Files to modify**:
- `README.md` - Add Docker recovery section
- `docs/troubleshooting.md` - Add Docker recovery guide
- `docs/configuration.md` - Document recovery settings

**Content to add**:
```markdown
## Docker Recovery

MCPProxy automatically detects when Docker engine becomes unavailable and
implements intelligent recovery:

- **Automatic Detection**: Monitors Docker health every 2-60 seconds
- **Exponential Backoff**: Reduces polling frequency for efficiency
- **Graceful Reconnection**: Reconnects all Docker-based servers
- **Container Cleanup**: Removes orphaned containers on shutdown

### Troubleshooting

If servers don't reconnect after Docker recovery:
1. Check Docker is running: `docker ps`
2. Check mcpproxy logs: `~/.mcpproxy/logs/main.log`
3. Verify container labels: `docker ps -a --filter label=com.mcpproxy.managed`
4. Force reconnect via tray: System Tray → Force Reconnect

### Configuration

Customize recovery behavior in `mcp_config.json`:
...
```

**Testing**:
- Review documentation for accuracy
- Test all documented commands
- Verify configuration examples work

---

## Implementation Order

**Recommended order** (total: 17 hours):

1. **Issue #6** (2h) - User notifications (immediate user value)
2. **Issue #10** (2h) - Documentation (helps users understand features)
3. **Issue #5** (4h) - Persistent state (improves reliability)
4. **Issue #7** (3h) - Configuration (enables customization)
5. **Issue #8** (6h) - Metrics (advanced monitoring)

## Testing Strategy

1. **Manual Testing**:
   - Stop Docker engine
   - Start mcpproxy
   - Verify recovery process
   - Check notifications
   - Restart mcpproxy during recovery
   - Verify state persistence

2. **Automated Testing**:
   - Add E2E test for recovery flow
   - Mock Docker health checks
   - Test configuration loading
   - Test metrics API

3. **CI/CD**:
   - All existing tests must pass
   - New features should not break backward compatibility
   - Test on all platforms (macOS, Linux, Windows)

## Success Criteria

- ✅ All features implemented and tested
- ✅ Documentation complete and accurate
- ✅ CI/CD pipeline green
- ✅ No breaking changes
- ✅ Backward compatible with existing configs

## Notes

- These features are **optional enhancements**
- Core Docker recovery functionality already works (from PR #120)
- Can be implemented incrementally in smaller PRs if desired
- Each issue can be a separate commit for easy review
