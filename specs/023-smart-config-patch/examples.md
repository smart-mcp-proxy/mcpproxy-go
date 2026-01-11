# Smart Config Patching: Examples and MCP Tool Descriptions

This document provides comprehensive examples of the Deep Merge + Strategic Merge Patch approach, including MCP tool descriptions optimized for LLM understanding.

## Table of Contents

1. [Current vs. Fixed Behavior Comparison](#current-vs-fixed-behavior-comparison)
2. [Merge Semantics Deep Dive](#merge-semantics-deep-dive)
3. [MCP Tool Description for LLMs](#mcp-tool-description-for-llms)
4. [Example Patch Operations](#example-patch-operations)
5. [Edge Cases and Expected Behavior](#edge-cases-and-expected-behavior)

---

## Current vs. Fixed Behavior Comparison

### Example 1: Unquarantine Operation

**Initial Config:**
```json
{
  "name": "ElevenLabs",
  "command": "uvx",
  "args": ["elevenlabs-mcp"],
  "env": {"ELEVENLABS_API_KEY": "${keyring:elevenlabs_api_key}"},
  "enabled": true,
  "quarantined": true,
  "isolation": {
    "enabled": true,
    "image": "python:3.11",
    "network_mode": "bridge",
    "extra_args": ["-v", "/Users/user/Downloads/elevenlabs:/root/Desktop:rw"],
    "working_dir": "/root"
  }
}
```

**Operation:** Unquarantine via Web UI

| Aspect | CURRENT (Broken) | FIXED (Smart Patch) |
|--------|------------------|---------------------|
| Result | `isolation` block removed entirely | Only `quarantined` changed to `false` |
| Data Loss | Yes - Docker volume mappings, image, network_mode lost | No data loss |
| User Impact | Server runs without Docker isolation | Server continues with full isolation |

**FIXED Result:**
```json
{
  "name": "ElevenLabs",
  "command": "uvx",
  "args": ["elevenlabs-mcp"],
  "env": {"ELEVENLABS_API_KEY": "${keyring:elevenlabs_api_key}"},
  "enabled": true,
  "quarantined": false,  // <-- ONLY this changed
  "isolation": {
    "enabled": true,
    "image": "python:3.11",
    "network_mode": "bridge",
    "extra_args": ["-v", "/Users/user/Downloads/elevenlabs:/root/Desktop:rw"],
    "working_dir": "/root"
  }
}
```

---

### Example 2: MCP Tool Patch - Enable Server

**Initial Config:**
```json
{
  "name": "github-server",
  "url": "https://api.github.com/mcp",
  "protocol": "http",
  "enabled": false,
  "headers": {"Authorization": "Bearer ${keyring:github_token}"},
  "oauth": {
    "client_id": "Iv1.abc123",
    "scopes": ["repo", "user"]
  }
}
```

**MCP Tool Call:**
```json
{
  "operation": "patch",
  "name": "github-server",
  "enabled": true
}
```

| Aspect | CURRENT (Broken) | FIXED (Smart Patch) |
|--------|------------------|---------------------|
| Result | `oauth` and `headers` may be lost | Only `enabled` changed |
| Data Loss | OAuth client_id, scopes, auth headers | None |

**FIXED Result:**
```json
{
  "name": "github-server",
  "url": "https://api.github.com/mcp",
  "protocol": "http",
  "enabled": true,  // <-- ONLY this changed
  "headers": {"Authorization": "Bearer ${keyring:github_token}"},
  "oauth": {
    "client_id": "Iv1.abc123",
    "scopes": ["repo", "user"]
  }
}
```

---

## Merge Semantics Deep Dive

### Field Type Merge Rules

| Field Type | Merge Behavior | Example |
|------------|----------------|---------|
| **Scalar** (string, bool, number) | Replace | `"enabled": false` → `"enabled": true` |
| **Object** (map, struct) | Deep merge recursively | `{"image": "python:3.12"}` merges into existing isolation |
| **Array** (list) | Replace entirely | `["arg1"]` replaces existing `["old1", "old2"]` |
| **Null** | Remove field | `"isolation": null` removes isolation block |
| **Omitted** | Preserve existing | Not providing `isolation` keeps it unchanged |

### Deep Merge Example

**Original:**
```json
{
  "isolation": {
    "enabled": true,
    "image": "python:3.11",
    "network_mode": "bridge",
    "extra_args": ["-v", "/path1:/mount1"],
    "working_dir": "/root"
  }
}
```

**Patch:**
```json
{
  "isolation": {
    "image": "python:3.12",
    "extra_args": ["-v", "/path2:/mount2"]
  }
}
```

**Result (Deep Merged):**
```json
{
  "isolation": {
    "enabled": true,           // Preserved - not in patch
    "image": "python:3.12",    // Updated from patch
    "network_mode": "bridge",  // Preserved - not in patch
    "extra_args": ["-v", "/path2:/mount2"],  // Replaced (array)
    "working_dir": "/root"     // Preserved - not in patch
  }
}
```

### Array Replacement Rationale

Arrays are replaced entirely, not merged element-wise, because:

1. **No merge key**: Unlike Kubernetes pods (which use `name` as merge key for containers), our arrays don't have identity fields
2. **Ordering matters**: For `args` and `extra_args`, order is significant
3. **Intent ambiguity**: If user provides `["new-arg"]`, do they want to append, prepend, or replace? Replacement is unambiguous
4. **Industry standard**: RFC 7396 (JSON Merge Patch) and most merge implementations use array replacement

---

## MCP Tool Description for LLMs

### Current Tool Description (Insufficient)

```json
{
  "name": "upstream_servers",
  "description": "Manage upstream MCP servers",
  "inputSchema": {
    "operation": "string: list, add, remove, patch, update",
    "name": "string: server name (required for patch/update)",
    "enabled": "boolean: enable/disable server",
    "url": "string: server URL",
    ...
  }
}
```

### IMPROVED Tool Description (For Smart Patching)

```json
{
  "name": "upstream_servers",
  "description": "Manage upstream MCP servers with smart configuration patching. IMPORTANT: Patch operations use deep merge semantics - only specify fields you want to change. Omitted fields are preserved, not removed.",
  "inputSchema": {
    "type": "object",
    "properties": {
      "operation": {
        "type": "string",
        "enum": ["list", "add", "remove", "patch", "update"],
        "description": "Operation to perform. 'patch' uses smart merge - only modifies specified fields while preserving all others."
      },
      "name": {
        "type": "string",
        "description": "Server name. Required for patch/update/remove operations."
      },
      "enabled": {
        "type": "boolean",
        "description": "Enable or disable the server. When patching, omit this field to preserve current state."
      },
      "quarantined": {
        "type": "boolean",
        "description": "Quarantine status. When patching, omit to preserve current state."
      },
      "url": {
        "type": "string",
        "description": "Server URL for HTTP/SSE servers. Omit to preserve existing URL."
      },
      "command": {
        "type": "string",
        "description": "Command for stdio servers. Omit to preserve existing command."
      },
      "args_json": {
        "type": "string",
        "description": "JSON array of command arguments. REPLACES existing args entirely if provided. Omit to preserve existing args."
      },
      "env_json": {
        "type": "string",
        "description": "JSON object of environment variables. MERGES with existing env vars. Use null value to remove a specific var."
      },
      "headers_json": {
        "type": "string",
        "description": "JSON object of HTTP headers. MERGES with existing headers. Use null value to remove a specific header."
      },
      "isolation_json": {
        "type": "string",
        "description": "JSON object for Docker isolation config. MERGES with existing isolation settings. Fields: enabled, image, network_mode, extra_args (replaces), working_dir. Use null to remove isolation entirely."
      },
      "oauth_json": {
        "type": "string",
        "description": "JSON object for OAuth config. MERGES with existing OAuth settings. Use null to remove OAuth entirely."
      }
    }
  }
}
```

---

## Example Patch Operations

### 1. Enable a Server (Minimal Patch)

**LLM Intent:** "Enable the github-server"

**Correct Patch:**
```json
{
  "operation": "patch",
  "name": "github-server",
  "enabled": true
}
```

**Result:** Only `enabled` changes. All other config (isolation, oauth, env, headers) preserved.

---

### 2. Update Docker Image Only

**LLM Intent:** "Update ElevenLabs server to use Python 3.12"

**Correct Patch:**
```json
{
  "operation": "patch",
  "name": "ElevenLabs",
  "isolation_json": "{\"image\": \"python:3.12\"}"
}
```

**Result:**
- `isolation.image` → `"python:3.12"`
- `isolation.enabled`, `isolation.network_mode`, `isolation.extra_args`, `isolation.working_dir` → preserved

---

### 3. Add Environment Variable (Merge)

**LLM Intent:** "Add DEBUG=true env var to my-server"

**Correct Patch:**
```json
{
  "operation": "patch",
  "name": "my-server",
  "env_json": "{\"DEBUG\": \"true\"}"
}
```

**Before:**
```json
{"env": {"API_KEY": "xxx", "TIMEOUT": "30"}}
```

**After (Merged):**
```json
{"env": {"API_KEY": "xxx", "TIMEOUT": "30", "DEBUG": "true"}}
```

---

### 4. Remove Environment Variable

**LLM Intent:** "Remove the DEBUG env var from my-server"

**Correct Patch:**
```json
{
  "operation": "patch",
  "name": "my-server",
  "env_json": "{\"DEBUG\": null}"
}
```

**Before:**
```json
{"env": {"API_KEY": "xxx", "DEBUG": "true"}}
```

**After:**
```json
{"env": {"API_KEY": "xxx"}}
```

---

### 5. Remove Isolation Entirely

**LLM Intent:** "Disable Docker isolation for my-server"

**Correct Patch:**
```json
{
  "operation": "patch",
  "name": "my-server",
  "isolation_json": "null"
}
```

**Result:** `isolation` field removed entirely from server config.

---

### 6. Update Multiple Fields

**LLM Intent:** "Change my-server's URL to https://new.api.com and enable it"

**Correct Patch:**
```json
{
  "operation": "patch",
  "name": "my-server",
  "url": "https://new.api.com",
  "enabled": true
}
```

**Result:**
- `url` → `"https://new.api.com"`
- `enabled` → `true`
- All other fields (isolation, oauth, env, headers, args) → preserved

---

### 7. Replace Command Args

**LLM Intent:** "Change my-server args to just ['--verbose']"

**Correct Patch:**
```json
{
  "operation": "patch",
  "name": "my-server",
  "args_json": "[\"--verbose\"]"
}
```

**Before:**
```json
{"args": ["--config", "/path/to/config.json", "--quiet"]}
```

**After (Replaced, not merged):**
```json
{"args": ["--verbose"]}
```

---

## Edge Cases and Expected Behavior

### Case 1: Patching Non-Existent Field (Add)

**Before:**
```json
{
  "name": "simple-server",
  "command": "npx",
  "args": ["my-mcp"]
}
```

**Patch:**
```json
{
  "operation": "patch",
  "name": "simple-server",
  "isolation_json": "{\"enabled\": true, \"image\": \"node:20\"}"
}
```

**After:**
```json
{
  "name": "simple-server",
  "command": "npx",
  "args": ["my-mcp"],
  "isolation": {
    "enabled": true,
    "image": "node:20"
  }
}
```

---

### Case 2: Invalid JSON in Patch

**Patch:**
```json
{
  "operation": "patch",
  "name": "my-server",
  "env_json": "{invalid json}"
}
```

**Expected Result:**
```json
{
  "error": "Invalid env_json format: invalid character 'i' looking for beginning of object key string",
  "success": false
}
```

---

### Case 3: Server Not Found

**Patch:**
```json
{
  "operation": "patch",
  "name": "nonexistent-server",
  "enabled": true
}
```

**Expected Result:**
```json
{
  "error": "Server 'nonexistent-server' not found",
  "success": false
}
```

---

### Case 4: Empty Patch (No Changes)

**Patch:**
```json
{
  "operation": "patch",
  "name": "my-server"
}
```

**Expected Result:** Server config unchanged, but `updated` timestamp refreshed.

---

## Response Format for LLMs

### Successful Patch Response

```json
{
  "id": "github-server",
  "name": "github-server",
  "updated": true,
  "enabled": true,
  "changes": {
    "enabled": {"old": false, "new": true}
  },
  "preserved_fields": ["url", "protocol", "headers", "oauth", "isolation"],
  "connection_status": "connected",
  "connection_message": "Server connected successfully"
}
```

### Response with Diff (For Auditability)

```json
{
  "id": "my-server",
  "name": "my-server",
  "updated": true,
  "diff": {
    "modified": {
      "enabled": {"from": false, "to": true},
      "isolation.image": {"from": "python:3.11", "to": "python:3.12"}
    },
    "added": {},
    "removed": {}
  },
  "preserved_fields": ["url", "command", "args", "env", "oauth", "headers", "isolation.enabled", "isolation.network_mode", "isolation.extra_args", "isolation.working_dir"]
}
```

---

## Summary: LLM Guidelines

### DO:

1. **Only specify fields you want to change** - omitted fields are preserved
2. **Use `null` to explicitly remove fields** - `"isolation_json": "null"` removes isolation
3. **Expect arrays to be replaced entirely** - `args_json` replaces, doesn't append
4. **Trust deep merge for objects** - `env_json` and `headers_json` merge with existing

### DON'T:

1. **Don't include unchanged fields** - they're preserved automatically
2. **Don't reconstruct the entire config** - just patch what changed
3. **Don't assume empty string removes fields** - use explicit `null`
4. **Don't expect array merging** - provide complete array if updating args

### Quick Reference:

| To Do This... | Use This Patch |
|---------------|----------------|
| Enable server | `{"operation": "patch", "name": "X", "enabled": true}` |
| Change one env var | `{"env_json": "{\"KEY\": \"value\"}"}` |
| Remove env var | `{"env_json": "{\"KEY\": null}"}` |
| Update isolation image | `{"isolation_json": "{\"image\": \"new:tag\"}"}` |
| Remove isolation entirely | `{"isolation_json": "null"}` |
| Replace all args | `{"args_json": "[\"new\", \"args\"]"}` |
