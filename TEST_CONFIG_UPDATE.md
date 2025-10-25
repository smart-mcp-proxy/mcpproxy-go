# Secrets Management Fixes - Verification Guide

## Summary

Fixed keyring secret resolution issue where `${keyring:...}` placeholders were not being replaced with actual values in Docker containers and stdio servers.

## Key Changes

1. **Event-Based Server Restart**: When secrets are added/updated/deleted, affected servers automatically restart
2. **Improved Error Logging**: CRITICAL errors with helpful instructions when secrets can't be resolved  
3. **New Event Type**: `secrets.changed` event emitted on secret modifications
4. **Comprehensive Tests**: E2E tests for secret resolution

## Quick Test

```bash
# 1. Build
make build

# 2. Start mcpproxy (will show CRITICAL error for missing secret)
./mcpproxy serve

# 3. Add secret via curl
curl -X POST http://localhost:8080/api/v1/secrets/ \
  -H "Content-Type: application/json" \
  -d '{"name": "my_secret", "value": "test123", "type": "keyring"}'

# 4. Server with ${keyring:my_secret} will automatically restart and resolve it
```

## Verification Steps

1. Configure server with `${keyring:secret_name}` in env vars
2. Start mcpproxy - see CRITICAL error in logs
3. Add secret via Web UI or API
4. Server automatically restarts
5. Secret is now resolved (no more placeholder)
