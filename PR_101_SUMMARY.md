# PR #101: Fix Keyring Secret Resolution and Reactive Server Restart

## Status
🔄 **CI Running** - Monitoring: https://github.com/smart-mcp-proxy/mcpproxy-go/pull/101

## Problem Fixed

**Keyring secrets were not being resolved in Docker containers and stdio servers.**

When users configured:
```json
{
  "env": {
    "API_KEY": "${keyring:my_secret}"
  }
}
```

The containers received the literal placeholder instead of the actual value:
```bash
$ docker exec container env | grep API_KEY
API_KEY=${keyring:my_secret}  # ❌ Wrong!
```

## Root Cause

**RestartServer() reused the same client instance!**

1. Client created before secrets existed → secret resolution failed
2. Unresolved placeholders stored in `client.config`
3. User added secrets via API
4. RestartServer() called `client.Disconnect()` + `client.Connect()`
5. **Same client reused with old config containing placeholders!**

## Solution

### Critical Fix: Recreate Client on Restart

**File:** `internal/runtime/lifecycle.go:699-742`

```go
// Before (BUG):
client.Disconnect()
client.Connect(ctx)  // ❌ Reuses old client

// After (FIX):
client.Disconnect()
upstreamManager.RemoveServer(name)      // Remove old client
upstreamManager.AddServer(name, config)  // ✅ Create NEW client with fresh resolution!
```

### Event-Based Reactive Updates

**New Event:** `EventTypeSecretsChanged`

Flow:
```
POST /api/v1/secrets/ 
→ Store in keyring
→ runtime.NotifySecretsChanged()
→ Find servers using ${keyring:...}
→ RestartServer() for each
→ New client created with resolved secrets ✅
```

### Improved Logging

```go
// Before:
WARN Failed to resolve secret

// After:
ERROR CRITICAL: Failed to resolve secret in environment variable
      server: everything-server
      env_var: API_KEY
      reference: ${keyring:my_secret}
      help: Use Web UI (http://localhost:8080/ui/) or API to add the secret to keyring
```

## Files Changed

- `internal/runtime/events.go` - Added `EventTypeSecretsChanged`
- `internal/runtime/event_bus.go` - Added `emitSecretsChanged()`  
- `internal/runtime/runtime.go` - Added `NotifySecretsChanged()` (70 lines)
- `internal/runtime/lifecycle.go` - **Fixed RestartServer() to recreate client**
- `internal/httpapi/server.go` - Interface method + event emission
- `internal/server/server.go` - Wrapper implementation
- `internal/upstream/core/client.go` - Enhanced logging
- `internal/runtime/event_bus_secrets_test.go` - Comprehensive tests

## Testing

### ✅ Unit Tests (Local)
```bash
$ go test ./internal/runtime -run "TestEmitSecretsChanged|TestNotifySecretsChanged" -v
PASS
ok      mcpproxy-go/internal/runtime    2.380s
```

### ✅ Linter (Local)
```bash
$ ./scripts/run-linter.sh
0 issues.
```

### ✅ Build (Local)
```bash
$ make build
✅ Build completed!
```

### 🔄 CI Pipeline
All tests queued:
- Unit Tests (ubuntu/macos/windows × 3 Go versions)
- E2E Tests (ubuntu/macos/windows × 3 Go versions)
- Lint
- Build
- Cross-Platform Logging Tests

## How to Test

```bash
# 1. Checkout and build
git fetch origin fix/keyring-secret-resolution-restart
git checkout fix/keyring-secret-resolution-restart
make build

# 2. Start mcpproxy (will show CRITICAL errors for missing secrets)
./mcpproxy serve

# 3. Add secret via API
curl -X POST http://localhost:8080/api/v1/secrets/ \
  -H "X-API-Key: $(jq -r '.api_key' ~/.mcpproxy/mcp_config.json)" \
  -H "Content-Type: application/json" \
  -d '{"name":"my_secret","value":"actual-value-123","type":"keyring"}'

# 4. Server automatically restarts with NEW client
# Check logs: "Creating new client with fresh secret resolution"

# 5. Verify Docker container has resolved value
docker ps | grep your-server
docker exec <container> env | grep MY_SECRET
# Expected: MY_SECRET=actual-value-123 ✅
# Not:      MY_SECRET=${keyring:my_secret} ❌
```

## Impact

- **No Breaking Changes** - This is a bug fix
- **Fixes Critical Issue** - Secrets now work correctly
- **Event System** - Enables reactive updates throughout the system
- **Better DX** - Clear error messages with actionable help

## Next Steps

1. ✅ PR Created
2. 🔄 CI Running
3. ⏳ Awaiting CI results
4. 📋 Review & Merge

---

🤖 Generated with [Claude Code](https://claude.com/claude-code)
