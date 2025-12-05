# Quick Start: Testing OAuth Token Refresh Fixes

**Feature**: 008-oauth-token-refresh
**Date**: 2025-12-04

## Prerequisites

- Go 1.24.0+
- Node.js (for Playwright tests)
- Built mcpproxy binary

```bash
# Build mcpproxy
CGO_ENABLED=0 go build -o mcpproxy ./cmd/mcpproxy
```

## Quick Test: Token Refresh

### 1. Start OAuth Test Server (30-second token TTL)

```bash
# Terminal 1: Start OAuth test server with short-lived tokens
go run ./tests/oauthserver/cmd/server -port 9000 -access-token-ttl=30s
```

### 2. Configure mcpproxy

Add to `~/.mcpproxy/mcp_config.json`:

```json
{
  "mcpServers": [
    {
      "name": "oauth-test-server",
      "url": "http://127.0.0.1:9000/mcp",
      "protocol": "streamable-http",
      "enabled": true
    }
  ]
}
```

### 3. Start mcpproxy with Debug Logging

```bash
# Terminal 2: Start mcpproxy with debug logging
./mcpproxy serve --log-level=debug
```

### 4. Trigger OAuth Login

```bash
# Terminal 3: Login (opens browser)
./mcpproxy auth login --server=oauth-test-server
```

Login with test credentials: `testuser` / `testpass`

### 5. Verify Token Refresh

Wait 30+ seconds for token expiration, then:

```bash
# List servers - should show "connected" status
./mcpproxy upstream list

# Check tools - should work without re-authentication
./mcpproxy tools list --server=oauth-test-server
```

### 6. Check Logs for Correlation IDs

```bash
# Filter logs for OAuth correlation
grep "correlation_id" ~/Library/Logs/mcpproxy/main.log | tail -20

# Filter for specific correlation ID
grep "550e8400-e29b-41d4" ~/Library/Logs/mcpproxy/main.log
```

## Quick Test: Race Condition Fix

### 1. Trigger Rapid Reconnections

```bash
# Restart server multiple times quickly
for i in 1 2 3 4 5; do
  ./mcpproxy upstream restart oauth-test-server &
done
wait
```

### 2. Verify Single OAuth Flow

Check logs - should see only ONE OAuth flow executing:

```bash
grep "OAuth flow" ~/Library/Logs/mcpproxy/main.log | tail -20
```

**Expected**: No "clearing stale state" warnings, single correlation_id per restart.

## Quick Test: Playwright E2E

### 1. Start All Components

```bash
# Terminal 1: OAuth server
go run ./tests/oauthserver/cmd/server -port 9000 -access-token-ttl=1h

# Terminal 2: mcpproxy
MCPPROXY_API_KEY="test-key" ./mcpproxy serve --listen=127.0.0.1:8085

# Terminal 3: Run Playwright tests
OAUTH_SERVER_URL="http://127.0.0.1:9000" \
OAUTH_CLIENT_ID="test-client" \
MCPPROXY_URL="http://127.0.0.1:8085" \
MCPPROXY_API_KEY="test-key" \
npx playwright test tests/e2e/oauth.spec.ts
```

## Quick Test: Web UI Verification

### 1. Open Web UI

```bash
# Get current API key
cat ~/.mcpproxy/mcp_config.json | jq -r '.api_key'

# Open Web UI (substitute your API key)
open "http://127.0.0.1:8080/ui/?apikey=YOUR_API_KEY"
```

### 2. Verify OAuth Status

- Navigate to Servers section
- Check `oauth-test-server` shows "connected" status
- Verify OAuth indicator shows authenticated

## Debug Commands

### Check OAuth Token Status

```bash
./mcpproxy auth status --server=oauth-test-server
```

### View Server Logs

```bash
./mcpproxy upstream logs oauth-test-server --tail=50
```

### Monitor Logs in Real-time

```bash
./mcpproxy upstream logs oauth-test-server --follow
```

### Full Diagnostics

```bash
./mcpproxy doctor
```

## Expected Outcomes

After implementing this feature:

| Scenario | Before | After |
|----------|--------|-------|
| Token expires | Server disconnects | Auto-refresh, stays connected |
| Restart mcpproxy | Re-authentication required | Uses persisted token |
| Concurrent reconnects | Race condition, failures | Single coordinated flow |
| Debug logs | Limited OAuth info | Full correlation + HTTP details |

## Troubleshooting

### Token Refresh Not Working

1. Check refresh token exists:
   ```bash
   ./mcpproxy auth status --server=oauth-test-server
   ```

2. Enable trace logging:
   ```bash
   ./mcpproxy serve --log-level=trace
   ```

3. Check token endpoint response in logs.

### OAuth Flow Stuck

1. Clear OAuth state:
   ```bash
   ./mcpproxy auth logout --server=oauth-test-server
   ```

2. Restart mcpproxy and try again.

### Browser Not Opening

Check environment:
```bash
echo $HEADLESS    # Should NOT be 'true' for manual testing
echo $NO_BROWSER  # Should NOT be 'true' for manual testing
```
