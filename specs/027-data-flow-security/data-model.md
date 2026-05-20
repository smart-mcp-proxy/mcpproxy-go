# Data Model: Data Flow Security with Agent Hook Integration

**Feature**: 027-data-flow-security
**Date**: 2026-02-04

## Entity Diagram

```
┌─────────────────────────┐         ┌──────────────────────────┐
│    FlowSession          │         │    ClassificationResult   │
│─────────────────────────│         │──────────────────────────│
│ ID: string (hookSessID) │         │ Classification: enum     │
│ StartTime: time         │         │ Confidence: float64      │
│ LastActivity: time      │         │ Method: string           │
│ LinkedMCPSessions: []   │◄────────│ CanExfiltrate: bool      │
│ Origins: map[hash]Origin│         │ CanReadData: bool        │
│ Flows: []FlowEdge       │         └──────────────────────────┘
│ Alerts: []FlowAlert     │                    ▲
└─────────┬───────────────┘                    │
          │ contains                  classifies│
          ▼                                    │
┌─────────────────────────┐         ┌──────────────────────────┐
│    DataOrigin           │         │    ServerClassifier       │
│─────────────────────────│         │──────────────────────────│
│ ContentHash: string     │         │ InternalPatterns: []str  │
│ ToolName: string        │         │ ExternalPatterns: []str  │
│ ServerName: string      │         │ HybridPatterns: []str    │
│ Classification: enum    │         │ ConfigOverrides: map     │
│ HasSensitiveData: bool  │         └──────────────────────────┘
│ SensitiveTypes: []str   │
│ Timestamp: time         │
└─────────────────────────┘
          │ referenced by
          ▼
┌─────────────────────────┐         ┌──────────────────────────┐
│    FlowEdge             │         │    FlowPolicy            │
│─────────────────────────│         │──────────────────────────│
│ ID: string              │         │ IntToExt: PolicyAction   │
│ FromOrigin: *DataOrigin │         │ SensitiveExt: PolicyAct  │
│ ToToolName: string      │         │ RequireJustify: bool     │
│ ToServerName: string    │         │ SuspiciousEndpoints: []  │
│ ToClassification: enum  │         │ ToolOverrides: map       │
│ FlowType: enum          │◄────────│                          │
│ RiskLevel: enum         │ decides │                          │
│ ContentHash: string     │         └──────────────────────────┘
│ Timestamp: time         │
└─────────────────────────┘

┌─────────────────────────┐
│  PendingCorrelation     │
│─────────────────────────│
│ HookSessionID: string   │
│ ArgsHash: string        │
│ ToolName: string        │
│ Timestamp: time         │
│ TTL: duration           │
└─────────────────────────┘
```

## Entities

### FlowSession

A per-agent-session container tracking all data origins and flow edges within a single Claude Code (or other agent) session.

| Field | Type | Description | Constraints |
|-------|------|-------------|-------------|
| ID | string | Hook session ID from agent | Primary key, immutable |
| StartTime | time.Time | When the session started | Set on creation |
| LastActivity | time.Time | Last tool call timestamp | Updated on every event |
| LinkedMCPSessions | []string | Correlated MCP session IDs | Appended via Mechanism A |
| Origins | map[string]*DataOrigin | Content hash → origin info | Max 10,000 entries (configurable) |
| Flows | []*FlowEdge | Detected data movements | Append-only |

**Lifecycle**: Created on first hook event for a session ID. Expired after `session_timeout_minutes` of inactivity (default: 30). In-memory only (not persisted to BBolt).

### DataOrigin

A record of where data was produced — which tool call generated this content.

| Field | Type | Description | Constraints |
|-------|------|-------------|-------------|
| ContentHash | string | SHA256 truncated to 128 bits (hex) | 32 hex chars |
| ToolCallID | string | Unique ID for the originating tool call | Optional |
| ToolName | string | Tool that produced this data | e.g., "Read", "github:get_file" |
| ServerName | string | MCP server name (empty for internal tools) | Optional |
| Classification | Classification | internal/external/hybrid/unknown | From classifier |
| HasSensitiveData | bool | Whether sensitive data was detected | From Spec 026 detector |
| SensitiveTypes | []string | Types of sensitive data found | e.g., ["api_token", "private_key"] |
| Timestamp | time.Time | When the data was produced | Set on creation |

### FlowEdge

A detected data movement between tools — data from one tool appearing in another tool's arguments.

| Field | Type | Description | Constraints |
|-------|------|-------------|-------------|
| ID | string | Unique edge identifier | ULID format |
| FromOrigin | *DataOrigin | Source of the data | Required |
| ToToolCallID | string | Destination tool call ID | Optional |
| ToToolName | string | Destination tool name | Required |
| ToServerName | string | Destination MCP server (empty for internal) | Optional |
| ToClassification | Classification | Classification of destination | From classifier |
| FlowType | FlowType | Direction classification | See FlowType enum |
| RiskLevel | RiskLevel | Assessed risk | See RiskLevel enum |
| ContentHash | string | Hash of the matching content | 32 hex chars |
| Timestamp | time.Time | When the flow was detected | Set on detection |

### ClassificationResult

The outcome of classifying a server or tool.

| Field | Type | Description | Constraints |
|-------|------|-------------|-------------|
| Classification | Classification | internal/external/hybrid/unknown | Enum |
| Confidence | float64 | How confident the classification is | 0.0 to 1.0 |
| Method | string | How the classification was determined | "heuristic", "config", "annotation" |
| Reason | string | Human-readable explanation | For logging |
| CanExfiltrate | bool | Whether this tool can send data externally | Derived from classification |
| CanReadData | bool | Whether this tool can access private data | Derived from classification |

### PendingCorrelation

A temporary entry for linking hook sessions to MCP sessions via argument hash matching.

| Field | Type | Description | Constraints |
|-------|------|-------------|-------------|
| HookSessionID | string | Claude Code hook session ID | Required |
| ArgsHash | string | SHA256 of tool name + arguments | 32 hex chars |
| ToolName | string | Inner tool name (e.g., "github:get_file") | Extracted from tool_input.name |
| Timestamp | time.Time | When the pending entry was created | For TTL expiry |
| TTL | time.Duration | Time-to-live before expiry | Default: 5 seconds |

**Lifecycle**: Created when hook evaluate receives a PreToolUse for `mcp__mcpproxy__*` tools. Consumed (deleted) when a matching MCP call arrives. Expired (deleted) after TTL.

### FlowPolicy

Configuration for how the system responds to different flow patterns.

| Field | Type | Description | Constraints |
|-------|------|-------------|-------------|
| InternalToExternal | PolicyAction | Action for internal→external flows | "allow", "warn", "ask", "deny" |
| SensitiveDataExternal | PolicyAction | Action when sensitive data flows externally | Default: "deny" |
| RequireJustification | bool | Whether justification is required | Default: true |
| SuspiciousEndpoints | []string | Known testing/exfiltration endpoints | Always denied |
| ToolOverrides | map[string]PolicyAction | Per-tool action overrides | Tool name → action |

## Enumerations

### Classification
```
internal  — Data sources, private systems (databases, file systems, code repos)
external  — Communication channels, public APIs (Slack, email, webhooks)
hybrid    — Can be either depending on usage (Bash, cloud databases)
unknown   — Unclassified; treated according to default_unknown config
```

### FlowType
```
internal→internal  — Safe: data stays within trusted boundary
external→external  — Safe: no private data involved
external→internal  — Safe: data ingestion
internal→external  — CRITICAL: potential exfiltration
```

### RiskLevel
```
none     — No concern (safe flow types)
low      — Log only
medium   — internal→external without sensitive data
high     — internal→external without justification
critical — internal→external with sensitive data, or suspicious endpoint
```

### PolicyAction
```
allow  — Allow the call, log only
warn   — Allow the call, log warning
ask    — Return "ask" to agent hook (user confirmation needed)
deny   — Block the call
```

## Configuration Entities

### FlowTrackingConfig

```json
{
  "security": {
    "flow_tracking": {
      "enabled": true,
      "session_timeout_minutes": 30,
      "max_origins_per_session": 10000,
      "hash_min_length": 20,
      "max_response_hash_bytes": 65536
    },
    "classification": {
      "default_unknown": "internal",
      "server_overrides": {}
    },
    "flow_policy": {
      "internal_to_external": "ask",
      "sensitive_data_external": "deny",
      "require_justification": true,
      "suspicious_endpoints": [],
      "tool_overrides": {}
    },
    "hooks": {
      "enabled": true,
      "fail_open": true,
      "correlation_ttl_seconds": 5
    }
  }
}
```

## Activity Log Extension

The existing `ActivityRecord` is extended with a new type:

### ActivityType: "hook_evaluation"

Stored in `ActivityRecord.Metadata`:

```json
{
  "hook_evaluation": {
    "event": "PreToolUse",
    "agent_type": "claude-code",
    "hook_session_id": "cc-session-abc",
    "coverage_mode": "full",
    "classification": {
      "classification": "external",
      "confidence": 0.9,
      "method": "heuristic"
    },
    "flow_analysis": {
      "flows_detected": 1,
      "flow_type": "internal→external",
      "risk_level": "critical",
      "has_sensitive_data": true,
      "sensitive_types": ["api_token"]
    },
    "policy_decision": "deny",
    "policy_reason": "Sensitive data (api_token) flowing from internal source to external destination"
  }
}
```

### FlowSummary (Activity Log Record)

Written to the unified activity log when a flow session expires. Provides aggregate flow intelligence without persisting full in-memory state.

| Field | Type | Description | Constraints |
|-------|------|-------------|-------------|
| SessionID | string | Hook or MCP session ID | Required |
| CoverageMode | string | "proxy_only" or "full" | Required |
| DurationMinutes | int | Session duration | Computed |
| TotalOrigins | int | Number of data origins tracked | From session |
| TotalFlows | int | Number of flow edges detected | From session |
| FlowTypeDistribution | map[string]int | Count per flow type | e.g., {"internal→external": 1} |
| RiskLevelDistribution | map[string]int | Count per risk level | e.g., {"critical": 1, "none": 5} |
| LinkedMCPSessions | []string | Correlated MCP session IDs | From session |
| ToolsUsed | []string | Unique tools observed | Deduped list |
| HasSensitiveFlows | bool | Any critical risk flows? | Derived |

**Lifecycle**: Written as `ActivityRecord` of type `flow_summary` when a FlowSession expires (30min inactivity) or on daemon shutdown.

## Relationships

```
FlowSession 1──* DataOrigin     (session contains many origins)
FlowSession 1──* FlowEdge       (session contains many flow edges)
FlowEdge    *──1 DataOrigin      (each edge references one origin)
FlowSession 1──* MCP Session     (linked via PendingCorrelation)
FlowSession 1──1 FlowSummary     (summary written on expiry)
FlowPolicy  1──* FlowEdge        (policy evaluates each edge)
Classifier  1──* Classification  (classifier produces results)
```
