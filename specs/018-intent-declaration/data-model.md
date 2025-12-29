# Data Model: Intent Declaration with Tool Split

**Feature**: 018-intent-declaration | **Date**: 2025-12-28

## Entity Relationship Diagram

```
┌─────────────────────────────────────────────────────────────────┐
│                        MCP Tool Call Flow                        │
└─────────────────────────────────────────────────────────────────┘

┌───────────────┐     ┌──────────────────┐     ┌─────────────────┐
│   MCP Client  │────▶│  Tool Variant    │────▶│ Intent Object   │
│  (AI Agent)   │     │  (call_tool_*)   │     │                 │
└───────────────┘     └──────────────────┘     └─────────────────┘
                              │                        │
                              ▼                        ▼
                      ┌──────────────────┐     ┌─────────────────┐
                      │ Validation Layer │◀────│ Server          │
                      │ (Two-Key Match)  │     │ Annotations     │
                      └──────────────────┘     └─────────────────┘
                              │
                              ▼
                      ┌──────────────────┐
                      │ Upstream Server  │
                      │ Tool Execution   │
                      └──────────────────┘
                              │
                              ▼
                      ┌──────────────────┐
                      │ Activity Record  │
                      │ (with Intent)    │
                      └──────────────────┘
```

---

## Core Entities

### 1. IntentDeclaration

Agent-provided intent metadata for tool calls.

```go
// Location: internal/contracts/types.go

// IntentDeclaration represents the agent's declared intent for a tool call.
// This enables the two-key security model where intent must be declared both
// in tool selection (call_tool_read/write/destructive) and in this parameter.
type IntentDeclaration struct {
    // OperationType is REQUIRED and must match the tool variant used.
    // Valid values: "read", "write", "destructive"
    OperationType string `json:"operation_type"`

    // DataSensitivity is optional classification of data being accessed/modified.
    // Valid values: "public", "internal", "private", "unknown"
    // Default: "unknown" if not provided
    DataSensitivity string `json:"data_sensitivity,omitempty"`

    // Reason is optional human-readable explanation for the operation.
    // Max length: 1000 characters
    Reason string `json:"reason,omitempty"`
}
```

**Validation Rules**:
| Field | Required | Validation |
|-------|----------|------------|
| operation_type | Yes | Must be "read", "write", or "destructive" |
| data_sensitivity | No | If provided, must be "public", "internal", "private", or "unknown" |
| reason | No | Max 1000 characters |

**State Transitions**: N/A (immutable after creation)

---

### 2. ToolCallRequest (Extended)

Request structure for call_tool_* variants.

```go
// Location: internal/server/mcp.go (handler parameters)

// ToolCallRequest represents parameters for call_tool_read/write/destructive
type ToolCallRequest struct {
    // Name is REQUIRED. Format: "server:tool" (e.g., "github:create_issue")
    Name string `json:"name"`

    // ArgsJSON is optional JSON string containing tool arguments.
    // Either args_json (string) or args (object) format is accepted.
    ArgsJSON string `json:"args_json,omitempty"`

    // Args is optional object containing tool arguments (legacy format).
    Args map[string]interface{} `json:"args,omitempty"`

    // Intent is REQUIRED. Must match the tool variant being called.
    Intent IntentDeclaration `json:"intent"`
}
```

**Validation Rules**:
| Field | Required | Validation |
|-------|----------|------------|
| name | Yes | Non-empty, contains ":" for external tools |
| args_json | No | Valid JSON if provided |
| args | No | Mutually exclusive with args_json |
| intent | Yes | Valid IntentDeclaration with matching operation_type |

---

### 3. ToolWithAnnotations

Enhanced tool response from retrieve_tools.

```go
// Location: internal/server/mcp.go (response construction)

// ToolWithAnnotations represents a tool with server-provided hints
// and MCPProxy recommendations for which call_tool variant to use.
type ToolWithAnnotations struct {
    // Name is the fully-qualified tool name (server:tool format)
    Name string `json:"name"`

    // Description is the tool's description
    Description string `json:"description"`

    // InputSchema is the JSON schema for tool arguments
    InputSchema map[string]interface{} `json:"inputSchema"`

    // Score is the BM25 relevance score (0.0-1.0)
    Score float64 `json:"score"`

    // Server is the upstream server name
    Server string `json:"server"`

    // Annotations contains server-provided hints about tool behavior
    Annotations *ToolAnnotations `json:"annotations,omitempty"`

    // CallWith is MCPProxy's recommendation for which tool variant to use
    // Values: "call_tool_read", "call_tool_write", "call_tool_destructive"
    CallWith string `json:"call_with"`
}
```

---

### 4. ToolAnnotations (Existing, Referenced)

Server-provided hints about tool behavior.

```go
// Location: internal/config/config.go (existing)

type ToolAnnotations struct {
    Title           string `json:"title,omitempty"`
    ReadOnlyHint    *bool  `json:"readOnlyHint,omitempty"`
    DestructiveHint *bool  `json:"destructiveHint,omitempty"`
    IdempotentHint  *bool  `json:"idempotentHint,omitempty"`
    OpenWorldHint   *bool  `json:"openWorldHint,omitempty"`
}
```

**CallWith Derivation Logic**:
```go
func DeriveCallWith(annotations *ToolAnnotations) string {
    if annotations != nil {
        if annotations.DestructiveHint != nil && *annotations.DestructiveHint {
            return "call_tool_destructive"
        }
        if annotations.ReadOnlyHint != nil && *annotations.ReadOnlyHint {
            return "call_tool_read"
        }
    }
    return "call_tool_write" // Safe default
}
```

---

### 5. ActivityRecord (Extended)

Activity record with intent metadata.

```go
// Location: internal/storage/activity_models.go (existing, extended)

type ActivityRecord struct {
    // ... existing fields ...

    // Metadata contains extensible data including intent
    // For tool calls, includes:
    //   "intent": IntentDeclaration object
    //   "tool_variant": "call_tool_read|write|destructive"
    Metadata map[string]interface{} `json:"metadata,omitempty"`
}
```

**Intent Storage Format**:
```json
{
  "metadata": {
    "intent": {
      "operation_type": "destructive",
      "data_sensitivity": "private",
      "reason": "User requested cleanup"
    },
    "tool_variant": "call_tool_destructive"
  }
}
```

---

### 6. IntentDeclarationConfig

Configuration for intent validation behavior.

```go
// Location: internal/config/config.go (new section)

// IntentDeclarationConfig controls intent validation behavior
type IntentDeclarationConfig struct {
    // StrictServerValidation controls whether server annotation mismatches
    // cause rejection (true) or just warnings (false).
    // Default: true (reject mismatches)
    StrictServerValidation bool `json:"strict_server_validation"`
}
```

**Config File Format**:
```json
{
  "intent_declaration": {
    "strict_server_validation": true
  }
}
```

---

## Validation Error Types

```go
// Location: internal/server/mcp.go (error handling)

// IntentValidationError represents intent validation failures
type IntentValidationError struct {
    Code    string // "INTENT_MISMATCH", "MISSING_INTENT", "SERVER_MISMATCH"
    Message string // Human-readable error
    Details map[string]interface{} // Additional context
}
```

**Error Codes**:
| Code | Description | Example Message |
|------|-------------|-----------------|
| INTENT_MISMATCH | Tool variant doesn't match intent.operation_type | `Intent mismatch: tool is call_tool_read but intent declares write` |
| MISSING_INTENT | Intent parameter not provided | `intent parameter is required for call_tool_read` |
| MISSING_OPERATION_TYPE | intent.operation_type not provided | `intent.operation_type is required` |
| INVALID_OPERATION_TYPE | Unknown operation_type value | `Invalid intent.operation_type 'unknown': must be read, write, or destructive` |
| SERVER_MISMATCH | Server annotation conflicts with intent | `Tool 'github:delete_repo' is marked destructive by server, use call_tool_destructive` |
| INVALID_SENSITIVITY | Unknown data_sensitivity value | `Invalid intent.data_sensitivity 'secret': must be public, internal, private, or unknown` |
| REASON_TOO_LONG | Reason exceeds max length | `intent.reason exceeds maximum length of 1000 characters` |

---

## Enum Values

### OperationType
```go
const (
    OperationTypeRead        = "read"
    OperationTypeWrite       = "write"
    OperationTypeDestructive = "destructive"
)

var ValidOperationTypes = []string{
    OperationTypeRead,
    OperationTypeWrite,
    OperationTypeDestructive,
}
```

### DataSensitivity
```go
const (
    DataSensitivityPublic   = "public"
    DataSensitivityInternal = "internal"
    DataSensitivityPrivate  = "private"
    DataSensitivityUnknown  = "unknown"
)

var ValidDataSensitivities = []string{
    DataSensitivityPublic,
    DataSensitivityInternal,
    DataSensitivityPrivate,
    DataSensitivityUnknown,
}
```

### ToolVariant
```go
const (
    ToolVariantRead        = "call_tool_read"
    ToolVariantWrite       = "call_tool_write"
    ToolVariantDestructive = "call_tool_destructive"
)
```

---

## Database Impact

### No Schema Changes Required

The ActivityRecord already has a `Metadata map[string]interface{}` field that can store intent data without schema migration.

### Query Patterns

**Filter by intent_type** (activity list):
```go
// Pseudo-code for filtering
for _, record := range records {
    if metadata, ok := record.Metadata["intent"].(map[string]interface{}); ok {
        if opType, ok := metadata["operation_type"].(string); ok {
            if opType == filterIntentType {
                // Include in results
            }
        }
    }
}
```

**Note**: For high-volume filtering, consider adding a top-level `IntentType` field to ActivityRecord in a future optimization.
