# Data Model: OpenAPI Schema Components

**Feature**: Complete OpenAPI Documentation for REST API Endpoints
**Date**: 2025-11-28

## Overview

This document defines the request/response schema components required for documenting 19 undocumented REST API endpoints. Schemas follow OpenAPI 3.1 specification and leverage swaggo/swag auto-discovery from Go struct types in `internal/contracts/`.

## Schema Organization

### Existing Schemas (Reused)
The following contract types already exist in `internal/contracts/` and will be reused:
- `contracts.ErrorResponse` - Standard error response wrapper
- `contracts.SuccessResponse` - Standard success response wrapper with generic data field
- `config.Config` - Main configuration object (from `internal/config/`)
- `secret.StorageType` - Secret storage backend types (from `internal/secret/`)

### New Schemas Required

The following schema components need to be added to `internal/contracts/` to support undocumented endpoints:

---

## Configuration Management Schemas

### GetConfigResponse
**Purpose**: Response for `GET /api/v1/config`

**Fields**:
| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `success` | boolean | Yes | Operation success status |
| `config` | config.Config | Yes | Current configuration object |

**Validation**: None (read-only response)

---

### ValidateConfigRequest
**Purpose**: Request body for `POST /api/v1/config/validate`

**Fields**:
| Field | Type | Required | Description |
|-------|------|----------|-------------|
| [All config.Config fields] | - | - | Configuration object to validate |

**Validation**:
- Configuration MUST pass all config.Config validation rules
- Invalid JSON returns 400 Bad Request

---

### ValidateConfigResponse
**Purpose**: Response for `POST /api/v1/config/validate`

**Fields**:
| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `success` | boolean | Yes | Whether validation passed |
| `valid` | boolean | Yes | Duplicate of success for clarity |
| `errors` | []string | No | List of validation error messages (empty if valid) |

**State Transitions**: None (stateless validation)

---

### ConfigApplyResult
**Purpose**: Response for `POST /api/v1/config/apply`

**Fields**:
| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `success` | boolean | Yes | Whether configuration was applied successfully |
| `changes_applied` | []string | No | List of configuration changes that were applied |
| `servers_restarted` | []string | No | List of server names that were restarted due to config changes |
| `errors` | []string | No | List of errors encountered during application |

**State Transitions**:
- Configuration file on disk updated
- Runtime configuration reloaded
- Affected servers restarted
- `config.reloaded` event emitted

---

## Secrets Management Schemas

### SecretReference
**Purpose**: Represents a secret reference found in configuration

**Fields**:
| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `secret_name` | string | Yes | Name of the secret (e.g., "GITHUB_TOKEN") |
| `used_by` | []string | Yes | List of server names that reference this secret |
| `is_env_var` | boolean | Yes | Whether secret comes from environment variable |

**Validation**: None (read-only)

---

### GetSecretReferencesResponse
**Purpose**: Response for `GET /api/v1/secrets/refs`

**Fields**:
| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `success` | boolean | Yes | Operation success status |
| `secrets` | []SecretReference | Yes | List of secret references found in config |
| `total_secrets` | integer | Yes | Total number of unique secrets referenced |

**Validation**: None (read-only)

---

### ConfigSecret
**Purpose**: Represents a config-referenced secret with resolution status

**Fields**:
| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `secret_name` | string | Yes | Name of the secret |
| `used_by` | []string | Yes | List of server names using this secret |
| `storage_type` | string | Yes | Storage backend ("keyring", "env", etc.) |
| `is_resolved` | boolean | Yes | Whether secret value can be retrieved |
| `error` | string | No | Error message if resolution failed |

**Validation**: None (read-only)

---

### GetConfigSecretsResponse
**Purpose**: Response for `GET /api/v1/secrets/config`

**Fields**:
| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `success` | boolean | Yes | Operation success status |
| `secrets` | []ConfigSecret | Yes | List of config secrets with resolution status |
| `total_secrets` | integer | Yes | Total number of secrets |
| `unresolved_count` | integer | Yes | Number of secrets that failed to resolve |

**Validation**: None (read-only)

---

### MigrateSecretsRequest
**Purpose**: Request body for `POST /api/v1/secrets/migrate`

**Fields**:
| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `from_storage` | string | Yes | Source storage type ("keyring", "env") |
| `to_storage` | string | Yes | Destination storage type ("keyring", "env") |
| `secret_names` | []string | No | Specific secrets to migrate (empty = all secrets) |

**Validation**:
- `from_storage` and `to_storage` MUST be different
- Storage types MUST be valid values from `secret.StorageType`
- If `secret_names` specified, all names MUST exist in source storage

---

### MigrateSecretsResponse
**Purpose**: Response for `POST /api/v1/secrets/migrate`

**Fields**:
| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `success` | boolean | Yes | Whether all migrations succeeded |
| `migrated_count` | integer | Yes | Number of secrets successfully migrated |
| `failed_count` | integer | Yes | Number of secrets that failed to migrate |
| `errors` | map[string]string | No | Map of secret name to error message for failures |

**State Transitions**:
- Secrets copied from source to destination storage
- Original secrets remain in source (migration is copy, not move)

---

### SetSecretRequest
**Purpose**: Request body for `POST /api/v1/secrets`

**Fields**:
| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `secret_name` | string | Yes | Name of the secret to set |
| `secret_value` | string | Yes | Value to store (will be encrypted in keyring) |
| `storage_type` | string | No | Storage backend (defaults to "keyring") |

**Validation**:
- `secret_name` MUST NOT be empty
- `secret_value` MUST NOT be empty
- `storage_type` MUST be valid if specified

---

## Tool Call History Schemas

### ToolCallRecord
**Purpose**: Represents a single tool call execution record

**Fields**:
| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `id` | string | Yes | Unique identifier for this tool call |
| `session_id` | string | Yes | MCP session ID this call belongs to |
| `server_name` | string | Yes | Upstream server that handled the call |
| `tool_name` | string | Yes | Name of the tool called |
| `arguments` | map[string]interface{} | Yes | Tool call arguments |
| `result` | interface{} | No | Tool call result (null if failed) |
| `error` | string | No | Error message if call failed |
| `started_at` | string (ISO 8601) | Yes | Timestamp when call started |
| `completed_at` | string (ISO 8601) | No | Timestamp when call completed |
| `duration_ms` | integer | No | Execution duration in milliseconds |

**Validation**: None (read-only)

---

### GetToolCallsResponse
**Purpose**: Response for `GET /api/v1/tool-calls`

**Fields**:
| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `success` | boolean | Yes | Operation success status |
| `tool_calls` | []ToolCallRecord | Yes | List of tool call records |
| `total_count` | integer | Yes | Total number of tool calls (before pagination) |
| `limit` | integer | Yes | Pagination limit used |
| `offset` | integer | Yes | Pagination offset used |

**Validation**: None (read-only)

---

### ReplayToolCallRequest
**Purpose**: Request body for `POST /api/v1/tool-calls/{id}/replay`

**Fields**:
| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `arguments` | map[string]interface{} | No | Override arguments (uses original if not provided) |

**Validation**:
- If `arguments` provided, MUST be valid JSON object
- Tool call ID in path MUST exist

---

## Session Management Schemas

### MCPSession
**Purpose**: Represents an MCP protocol session

**Fields**:
| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `session_id` | string | Yes | Unique session identifier |
| `server_name` | string | Yes | Upstream server for this session |
| `started_at` | string (ISO 8601) | Yes | Session start timestamp |
| `last_activity_at` | string (ISO 8601) | Yes | Last tool call or activity timestamp |
| `tool_call_count` | integer | Yes | Number of tool calls in this session |
| `is_active` | boolean | Yes | Whether session is currently active |

**Validation**: None (read-only)

---

### GetSessionsResponse
**Purpose**: Response for `GET /api/v1/sessions`

**Fields**:
| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `success` | boolean | Yes | Operation success status |
| `sessions` | []MCPSession | Yes | List of session records |
| `total_count` | integer | Yes | Total number of sessions |
| `active_count` | integer | Yes | Number of currently active sessions |

**Validation**: None (read-only)

---

## Registry Browsing Schemas

### Registry
**Purpose**: Represents an MCP server registry

**Fields**:
| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `id` | string | Yes | Registry identifier (e.g., "smithery", "official") |
| `name` | string | Yes | Human-readable registry name |
| `description` | string | Yes | Registry description |
| `url` | string | Yes | Registry API base URL |
| `server_count` | integer | Yes | Number of servers in this registry |

**Validation**: None (read-only)

---

### RegistryServer
**Purpose**: Represents an MCP server listing in a registry

**Fields**:
| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `id` | string | Yes | Server identifier in registry |
| `name` | string | Yes | Server name |
| `description` | string | Yes | Server description |
| `tags` | []string | Yes | Category tags for filtering |
| `install_command` | string | Yes | Command to install/configure this server |
| `repository_url` | string | No | Source code repository URL |
| `author` | string | No | Server author/maintainer |

**Validation**: None (read-only)

---

### GetRegistriesResponse
**Purpose**: Response for `GET /api/v1/registries`

**Fields**:
| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `success` | boolean | Yes | Operation success status |
| `registries` | []Registry | Yes | List of available registries |
| `total_count` | integer | Yes | Total number of registries |

**Validation**: None (read-only)

---

### SearchRegistryServersResponse
**Purpose**: Response for `GET /api/v1/registries/{id}/servers`

**Fields**:
| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `success` | boolean | Yes | Operation success status |
| `registry_id` | string | Yes | Registry that was searched |
| `servers` | []RegistryServer | Yes | List of matching servers |
| `total_count` | integer | Yes | Total number of results |
| `query` | string | No | Search query used (if any) |
| `tag` | string | No | Tag filter used (if any) |

**Validation**: None (read-only)

---

## Code Execution Schema

### CodeExecRequest
**Purpose**: Request body for `POST /api/v1/code/exec`

**Fields**:
| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `code` | string | Yes | JavaScript code to execute |
| `input` | map[string]interface{} | No | Input variables for the code |
| `timeout_ms` | integer | No | Execution timeout in milliseconds (defaults to config value) |
| `max_tool_calls` | integer | No | Maximum allowed tool calls (defaults to config value) |

**Validation**:
- `code` MUST NOT be empty
- `code` MUST be valid JavaScript (ES5.1+ syntax)
- `timeout_ms` MUST be <= configured maximum
- `max_tool_calls` MUST be <= configured maximum

---

### CodeExecResponse
**Purpose**: Response for `POST /api/v1/code/exec`

**Fields**:
| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `success` | boolean | Yes | Whether execution succeeded |
| `result` | interface{} | No | Execution result (null if failed) |
| `error` | string | No | Error message if execution failed |
| `tool_calls_made` | integer | Yes | Number of tool calls executed |
| `execution_time_ms` | integer | Yes | Actual execution time in milliseconds |

**Validation**: None (read-only)

---

## SSE Events Schema

### SSEEvent
**Purpose**: Represents a Server-Sent Event payload

**Fields**:
| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `event` | string | Yes | Event type ("servers.changed", "config.reloaded", etc.) |
| `data` | interface{} | Yes | Event-specific payload |
| `timestamp` | string (ISO 8601) | Yes | Event emission timestamp |

**Content Type**: `text/event-stream`

**Event Format** (SSE protocol):
```
event: servers.changed
data: {"server_name": "github-server", "connected": true}

event: config.reloaded
data: {"changes_applied": ["server added: weather-api"]}
```

**Validation**: None (server-generated)

---

## Schema Relationships

```
Configuration Management:
  GetConfigResponse → config.Config
  ValidateConfigRequest → config.Config
  ValidateConfigResponse ← ValidateConfigRequest
  ConfigApplyResult ← config.Config

Secrets Management:
  SecretReference ← config.Config (extracted from)
  ConfigSecret ← SecretReference (resolution status added)
  MigrateSecretsRequest → MigrateSecretsResponse
  SetSecretRequest → secret storage backend

Tool Calls:
  ToolCallRecord ← MCP tool execution
  GetToolCallsResponse → []ToolCallRecord
  ReplayToolCallRequest → ToolCallRecord (creates new record)

Sessions:
  MCPSession ← MCP connection
  GetSessionsResponse → []MCPSession

Registries:
  Registry ← External registry API
  RegistryServer ← Registry
  GetRegistriesResponse → []Registry
  SearchRegistryServersResponse → []RegistryServer

Code Execution:
  CodeExecRequest → JavaScript VM
  CodeExecResponse ← JavaScript VM

SSE:
  SSEEvent ← Runtime event bus
```

---

## Implementation Notes

### Go Struct Definitions
All schemas will be defined as Go structs in `internal/contracts/` with JSON struct tags:

```go
type GetConfigResponse struct {
    Success bool           `json:"success"`
    Config  *config.Config `json:"config"`
}
```

### swaggo/swag Auto-Discovery
swag will automatically discover these types when referenced in `@Success` or `@Failure` annotations:

```go
// @Success 200 {object} contracts.GetConfigResponse
```

### Reusable Error Responses
All endpoints use the existing `contracts.ErrorResponse` for 400/401/403/404/500 errors:

```go
type ErrorResponse struct {
    Success bool   `json:"success"`
    Error   string `json:"error"`
}
```

### Pagination Pattern
Endpoints supporting pagination use `limit` and `offset` query parameters:
- `limit`: Maximum results to return (default and max values defined per endpoint)
- `offset`: Number of results to skip (for paging through large datasets)

Responses include `total_count` for clients to calculate total pages.
