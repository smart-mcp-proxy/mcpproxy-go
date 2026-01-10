# Quickstart: Smart Config Patching

**Feature Branch**: `023-smart-config-patch`
**Created**: 2026-01-10

## What Changed

MCPProxy now uses **smart config patching** for all server configuration updates. This means:

1. **Only specified fields are modified** - omitted fields are preserved
2. **Nested objects are deep-merged** - you can update just one field in isolation config
3. **No more data loss** - unquarantine, enable/disable, and patch operations preserve all config

## Quick Examples

### Before (Broken Behavior)

```bash
# Add server with isolation
mcpproxy upstream add-json MyServer '{
  "command": "uvx",
  "args": ["my-mcp"],
  "isolation": {"enabled": true, "image": "python:3.11"}
}'

# Enable via MCP tool - BROKE: isolation was lost!
# Tool call: upstream_servers(operation="patch", name="MyServer", enabled=true)
# Result: isolation config was removed
```

### After (Fixed Behavior)

```bash
# Add server with isolation
mcpproxy upstream add-json MyServer '{
  "command": "uvx",
  "args": ["my-mcp"],
  "isolation": {"enabled": true, "image": "python:3.11"}
}'

# Enable via MCP tool - FIXED: isolation is preserved!
# Tool call: upstream_servers(operation="patch", name="MyServer", enabled=true)
# Result: only enabled field changes, isolation config intact
```

## For AI Agents (LLMs)

### Minimal Patches

Only include the fields you want to change:

```json
// GOOD: Enable server
{"operation": "patch", "name": "my-server", "enabled": true}

// BAD: Including unchanged fields (unnecessary)
{"operation": "patch", "name": "my-server", "enabled": true, "url": "...", "isolation": {...}}
```

### Updating Nested Objects

Deep merge means you only specify the nested fields to change:

```json
// Update only the Docker image
{
  "operation": "patch",
  "name": "my-server",
  "isolation_json": "{\"image\": \"python:3.12\"}"
}
// Result: image updated, all other isolation fields preserved
```

### Removing Fields

Use explicit `null` to remove:

```json
// Remove isolation entirely
{
  "operation": "patch",
  "name": "my-server",
  "isolation_json": "null"
}

// Remove a specific env var
{
  "operation": "patch",
  "name": "my-server",
  "env_json": "{\"DEBUG\": null}"
}
```

### Array Replacement

Arrays are replaced entirely, not merged:

```json
// Replace ALL args (not append)
{
  "operation": "patch",
  "name": "my-server",
  "args_json": "[\"--new-arg\"]"
}
```

## For Developers

### Using the Merge Utility

```go
import "github.com/smart-mcp-proxy/mcpproxy-go/internal/config"

base := &config.ServerConfig{
    Name:    "my-server",
    Enabled: false,
    Isolation: &config.IsolationConfig{
        Enabled: true,
        Image:   "python:3.11",
    },
}

patch := &config.ServerConfig{
    Enabled: true,  // Only change this
}

// Merge with default options
merged, diff, err := config.MergeServerConfig(base, patch, config.DefaultMergeOptions())
if err != nil {
    // Handle error
}

// merged.Enabled = true
// merged.Isolation is preserved!

// diff contains the changes made
log.Printf("Changes: %+v", diff.Modified)
```

### Auditing Changes

```go
opts := config.DefaultMergeOptions()
opts.GenerateDiff = true

merged, diff, _ := config.MergeServerConfig(base, patch, opts)

// Log for audit trail
logger.Info("Config updated",
    zap.String("server", merged.Name),
    zap.Any("modified", diff.Modified),
    zap.Strings("added", diff.Added),
    zap.Strings("removed", diff.Removed))
```

## Merge Semantics Reference

| Field Type | Behavior | Example |
|------------|----------|---------|
| Scalar (string, bool, int) | Replace | `"enabled": true` |
| Map (env, headers) | Deep merge | Add/update keys, null removes |
| Struct (isolation, oauth) | Deep merge | Update nested fields |
| Array (args, extra_args) | Replace | `["new"]` replaces `["old1", "old2"]` |
| Null value | Remove | `"isolation": null` removes field |
| Omitted field | Preserve | Not mentioning isolation keeps it |

## Testing the Fix

```bash
# Run E2E tests
./scripts/test-api-e2e.sh

# Run specific isolation preservation tests
go test ./internal/config/... -v -run TestMergeServerConfig
go test ./internal/server/... -v -run TestPatchPreservesIsolation
```
