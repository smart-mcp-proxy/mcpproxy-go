# RFC-003: Activity Log & Observability

**Status**: Draft
**Created**: 2025-12-19
**Updated**: 2025-12-22
**Related**: RFC-004 (Security & Attack Detection)

---

## Summary

This proposal implements an **Activity Log** system for mcpproxy, providing users complete visibility into what AI agents are doing. This is the foundation for security features defined in RFC-004.

### Naming Convention

Based on industry research (LangSmith, Obot, GitHub Enterprise, OpenTelemetry):

| Aspect | Name | Rationale |
|--------|------|-----------|
| **Feature** | Activity Log | Broader than "tool calls" - includes policy decisions, quarantine events |
| **CLI command** | `mcpproxy activity` | Matches enterprise patterns (GitHub, Azure) |
| **REST endpoint** | `/api/v1/activity` | Aligns with Obot's `/api/mcp-audit-logs` pattern |
| **Technical term** | Traces / Spans | For OpenTelemetry integration |
| **Compliance term** | Audit Trail | For enterprise documentation |

**Reference implementations:**
- **Obot**: `MCPAuditLog` type, `/api/mcp-audit-logs` endpoint
- **LangSmith**: "Traces" with hierarchical "Runs"
- **GitHub**: "Audit logs" with streaming/export

### Related Standards & Proposals

#### MCP Specification Enhancement Proposals (SEPs)

| SEP | Status | Relevance |
|-----|--------|-----------|
| [SEP-1763: Interceptors](https://github.com/modelcontextprotocol/modelcontextprotocol/issues/1763) | Draft | Proposes standardized interceptor framework for audit logging, validation, observability |
| [Discussion #804: Gateway Authorization](https://github.com/modelcontextprotocol/modelcontextprotocol/discussions/804) | Discussion | Proposes gateway as single audit log aggregation point |
| [SEP-1539: Timeout Coordination](https://github.com/modelcontextprotocol/modelcontextprotocol/issues/1539) | Draft | Includes audit logging for security monitoring |

**SEP-1763 Key Features** (align mcpproxy with future MCP standard):
- Interceptor types: validation (info/warn/error), mutation, observability
- Extension points: tool discovery, tool invocation, prompt handling, resource access
- Observability: "auditing, logging, and metrics collection"
- Design: M + N problem (clients implement once, servers expose once)

#### OpenTelemetry GenAI Semantic Conventions

MCPProxy should align with [OpenTelemetry GenAI Semantic Conventions](https://opentelemetry.io/docs/specs/semconv/gen-ai/) for future observability integration:

| Attribute | Type | Description |
|-----------|------|-------------|
| `gen_ai.operation.name` | Required | `execute_tool`, `invoke_agent`, `create_agent` |
| `gen_ai.provider.name` | Required | MCP server name |
| `gen_ai.tool.definitions` | Opt-In | Tool schema definitions |
| `gen_ai.agent.name` | Conditional | Agent identifier |
| `gen_ai.usage.input_tokens` | Recommended | Token usage |
| `gen_ai.request.model` | Recommended | Model identifier |
| `error.type` | Conditional | Error type if operation fails |

**Span Types for Tool Calls:**
- `gen_ai.operation.name = "execute_tool"` with `INTERNAL` span kind
- Parent span: agent invocation or chat completion
- Child spans: individual tool executions

**Future Integration Path:**
```go
// Activity records can export to OpenTelemetry format
type OTelExporter struct {
    tracer trace.Tracer
}

func (e *OTelExporter) ExportActivity(a *ActivityRecord) {
    _, span := e.tracer.Start(ctx, "execute_tool",
        trace.WithAttributes(
            attribute.String("gen_ai.operation.name", "execute_tool"),
            attribute.String("gen_ai.provider.name", a.ServerName),
            attribute.String("gen_ai.tool.name", a.ToolName),
        ),
    )
    defer span.End()
}
```

---

## Current State

MCPProxy already implements:

1. **Tool Call Recording** - Stored in BBolt database per-server
   - Records: ID, arguments, response, error, duration, timestamp, tokens
   - API: `GET /api/v1/tool-calls` with pagination

2. **Session Tracking** - `MCPSession` model
   - Client name/version, start/end times, tool call count

3. **Secret Sanitization** - Pattern-based log masking
   - GitHub tokens, API keys, JWT tokens, Bearer tokens

4. **Security Quarantine** - New servers automatically quarantined
   - Tool Poisoning Attack (TPA) detection

---

## Architecture Overview

```
┌────────────────────────────────────────────────────────────────────┐
│                      ACTIVITY LOG PIPELINE                          │
│                                                                     │
│  ┌──────────────┐    ┌──────────────┐    ┌──────────────┐          │
│  │  MCP Client  │───▶│   MCPProxy   │───▶│  Upstream    │          │
│  │              │    │              │    │  MCP Server  │          │
│  └──────────────┘    └──────┬───────┘    └──────────────┘          │
│                             │                                       │
│                             ▼                                       │
│                    ┌──────────────────┐                             │
│                    │ Activity Recorder│                             │
│                    │                  │                             │
│                    │ • Tool calls     │                             │
│                    │ • Policy events  │                             │
│                    │ • Server changes │                             │
│                    └────────┬─────────┘                             │
│                             │                                       │
│           ┌─────────────────┼─────────────────┐                     │
│           ▼                 ▼                 ▼                     │
│    ┌──────────────┐  ┌──────────────┐  ┌──────────────┐            │
│    │   BBolt DB   │  │  SSE Events  │  │   REST API   │            │
│    │  (storage)   │  │  (real-time) │  │   (query)    │            │
│    └──────────────┘  └──────────────┘  └──────────────┘            │
│                                                                     │
└────────────────────────────────────────────────────────────────────┘
```

---

## Implementation Priority

### Phase 1: Activity Log UX (Foundation)

**Goal**: Give users visibility into all agent activity with live updates.

#### 1.1 Database Schema

```go
// internal/storage/activity.go

// ActivityType defines the type of activity being recorded
type ActivityType string

const (
    ActivityToolCall       ActivityType = "tool_call"
    ActivityPolicyDecision ActivityType = "policy_decision"
    ActivityQuarantine     ActivityType = "quarantine"
    ActivityServerChange   ActivityType = "server_change"
)

// ActivityRecord represents a single activity entry (aligns with Obot's MCPAuditLog)
type ActivityRecord struct {
    ID          string          `json:"id"`
    Type        ActivityType    `json:"type"`
    SessionID   string          `json:"session_id,omitempty"`
    Timestamp   time.Time       `json:"timestamp"`

    // Tool call fields (Type = tool_call)
    ServerName  string          `json:"server_name,omitempty"`
    ToolName    string          `json:"tool_name,omitempty"`
    Arguments   json.RawMessage `json:"arguments,omitempty"`
    Response    json.RawMessage `json:"response,omitempty"`
    DurationMs  int64           `json:"duration_ms,omitempty"`

    // Common fields
    Status      string          `json:"status"`   // pending, success, error, blocked
    Error       string          `json:"error,omitempty"`

    // OpenTelemetry alignment (Phase 1)
    TraceID     string          `json:"trace_id,omitempty"`    // gen_ai trace correlation
    SpanID      string          `json:"span_id,omitempty"`

    // Added in Phase 2
    Intent      *IntentDeclaration `json:"intent,omitempty"`

    // Added in RFC-004 phases
    PIIDetected []string           `json:"pii_detected,omitempty"`
    RiskScore   int                `json:"risk_score,omitempty"`
    Flags       []SecurityFlag     `json:"flags,omitempty"`
}
```

#### 1.2 REST API (aligned with Obot pattern)

```
GET  /api/v1/activity
     ?type=<type>             # tool_call, policy_decision, quarantine
     ?server=<name>           # Filter by server
     ?session=<id>            # Filter by session
     ?status=<status>         # pending, success, error, blocked
     ?start_time=<RFC3339>    # After this time
     ?end_time=<RFC3339>      # Before this time
     ?limit=<n>               # Max records (default 100)
     ?offset=<n>              # Pagination offset

GET  /api/v1/activity/{id}
     # Get single record with full details (request/response bodies)

GET  /api/v1/activity/filter-options/{filter}
     # Get available filter values (like Obot)
     # filter = server_name, tool_name, status, type

GET  /api/v1/activity/export
     ?format=json|csv         # Export format
     ?start_time=<RFC3339>    # Time range
     ?end_time=<RFC3339>

GET  /events
     # SSE stream includes:
     # - activity.tool_call.started
     # - activity.tool_call.completed
     # - activity.policy_decision
     # - activity.quarantine
```

#### 1.3 CLI Commands

```bash
# List recent activity
mcpproxy activity list
  --type <type>         # tool_call, policy_decision, quarantine
  --server <name>       # Filter by server
  --limit <n>           # Number of records
  --json                # JSON output

# Output:
# TIME          TYPE         SERVER      DETAILS              STATUS
# 10:32:15      tool_call    github      search_code          success
# 10:32:14      tool_call    filesystem  read_file            success
# 10:32:10      policy       github      delete_repo BLOCKED  blocked

# Watch live (like tail -f)
mcpproxy activity watch
  --type <type>         # Filter by type
  --server <name>       # Filter by server

# Output (streaming):
# 10:32:20 [tool_call] github:search_code → success (234ms)
# 10:32:21 [tool_call] github:get_file → success (56ms)
# 10:32:22 [policy] slack:post_message → blocked (external_url)

# Show details of a specific activity
mcpproxy activity show <id>
  --json                # Full JSON output

# Output:
# Activity: act_abc123
# ─────────────────────────────────────
# Type:     tool_call
# Time:     2025-12-20 10:32:15
# Server:   github
# Tool:     search_code
# Status:   success
# Duration: 234ms
#
# Arguments:
#   query: "function handleError"
#   repo: "myorg/myrepo"
#
# Response:
#   matches: 3
#   ...

# Summary dashboard
mcpproxy activity summary
  --period <duration>   # Time period: 1h, 24h, 7d (default: 24h)
  --json

# Export activity for compliance
mcpproxy activity export
  --start-time <RFC3339>
  --end-time <RFC3339>
  --format json|csv
  --output activity-audit.json
```

#### 1.4 Web UI

```
/ui/activity
├── Live-updating table (SSE-driven)
├── Filters: type, server, status, time range (like Obot)
├── Filter options API for dynamic dropdowns
├── Click row → detail panel with full request/response
├── Auto-refresh toggle
├── Export to JSON/CSV
└── Pagination with offset/limit
```

**Dashboard Widget:**

```
┌─────────────────────────────────────────────────────────────┐
│ Tool Call Activity                               [View All] │
├─────────────────────────────────────────────────────────────┤
│ 📊 156 total calls today                                   │
│ ✓  153 successful                                          │
│ ⚠️  3 with warnings                                         │
├─────────────────────────────────────────────────────────────┤
│ Recent:                                                     │
│ • github:search_code     2s ago    ✓ success               │
│ • slack:post_message     5s ago    ✓ success               │
│ • postgres:query         12s ago   ✓ success               │
└─────────────────────────────────────────────────────────────┘
```

**Activity Log Page (`/ui/activity`):**

```
┌─────────────────────────────────────────────────────────────┐
│ Activity Log                                                 │
├──────────────────────────────────────────────┬──────────────┤
│ Filters:                                      │ Summary      │
│ [Type ▼] [Server ▼] [Status ▼] [Date Range]  │ 156 total    │
│                                               │ 3 warnings   │
├──────────────────────────────────────────────┴──────────────┤
│ Time     │ Type      │ Server │ Details       │ Status │ Dur │
│──────────│───────────│────────│───────────────│────────│─────│
│ 10:32:15 │ tool_call │ github │ search_code   │ ✓      │245ms│
│ 10:32:12 │ tool_call │ slack  │ post_message  │ ✓      │523ms│
│ 10:32:08 │ policy    │ github │ delete_repo   │ blocked│ -   │
│ 10:31:55 │ tool_call │ http   │ fetch_url     │ ✓      │1.2s │
└─────────────────────────────────────────────────────────────┘
```

#### 1.5 SSE Events (aligned with OpenTelemetry naming)

```go
// Event types for activity log
type ActivityEvent struct {
    Type      string          `json:"type"`      // activity.tool_call.started, etc.
    ID        string          `json:"id"`
    Timestamp time.Time       `json:"timestamp"`

    // OpenTelemetry alignment
    TraceID   string          `json:"trace_id,omitempty"`
    SpanID    string          `json:"span_id,omitempty"`

    // Activity details
    ActivityType string       `json:"activity_type"`  // tool_call, policy_decision
    Server    string          `json:"server,omitempty"`
    Tool      string          `json:"tool,omitempty"`
    Status    string          `json:"status,omitempty"`
    DurationMs int64          `json:"duration_ms,omitempty"`
    Error     string          `json:"error,omitempty"`
}
```

**Event Payloads:**

```json
// Tool call started
{
  "event": "activity.tool_call.started",
  "data": {
    "id": "act_abc123",
    "server": "github",
    "tool": "search_code",
    "timestamp": "2025-12-19T10:30:00Z",
    "trace_id": "abc123def456"
  }
}

// Tool call completed
{
  "event": "activity.tool_call.completed",
  "data": {
    "id": "act_abc123",
    "server": "github",
    "tool": "search_code",
    "timestamp": "2025-12-19T10:30:00.245Z",
    "duration_ms": 245,
    "status": "success"
  }
}

// Policy decision
{
  "event": "activity.policy_decision",
  "data": {
    "id": "act_def456",
    "server": "github",
    "tool": "delete_repository",
    "decision": "blocked",
    "reason": "destructive=deny"
  }
}
```

---

### Phase 2: Intent Declaration

**Goal**: Capture and display agent-declared intent for each tool call.

#### 2.1 Enhanced call_tool Schema

```json
{
  "name": "call_tool",
  "inputSchema": {
    "properties": {
      "server": {"type": "string"},
      "tool": {"type": "string"},
      "arguments": {"type": "object"},
      "intent": {
        "type": "object",
        "description": "Security declaration for this tool call",
        "properties": {
          "operation_type": {
            "enum": ["read", "write", "destructive"]
          },
          "data_sensitivity": {
            "enum": ["public", "internal", "private", "unknown"]
          },
          "reversible": {"type": "boolean"},
          "reason": {"type": "string"}
        }
      }
    }
  }
}
```

#### 2.2 Intent Declaration Type

```go
type IntentDeclaration struct {
    OperationType   string `json:"operation_type"`   // read, write, destructive
    DataSensitivity string `json:"data_sensitivity"` // public, internal, private, unknown
    Reversible      *bool  `json:"reversible,omitempty"`
    Reason          string `json:"reason,omitempty"`
}
```

#### 2.3 Display in CLI

```bash
mcpproxy activity list --show-intent

# Output:
# TIME       SERVER   TOOL              INTENT           STATUS
# 10:32:15   github   delete_repo       ⚠️ destructive   success
# 10:32:14   github   search_code       📖 read          success
# 10:32:10   slack    post_message      ✏️ write         success
```

#### 2.4 Display in Web UI

```
┌─────────────────────────────────────────────────────────────┐
│ Tool Call: github:delete_repository                         │
├─────────────────────────────────────────────────────────────┤
│ Agent Intent:                                                │
│   Operation: 🔴 DESTRUCTIVE                                  │
│   Sensitivity: 🔒 private                                    │
│   Reversible: ❌ No                                          │
│   Reason: "User requested deletion of test repository"       │
└─────────────────────────────────────────────────────────────┘
```

---

## Configuration Options

```json
{
  "activity_log": {
    "enabled": true,
    "retention_days": 90,

    "storage": {
      "type": "bbolt",
      "max_records": 100000
    },

    "real_time": {
      "sse_enabled": true,
      "batch_interval_ms": 100
    },

    "export": {
      "formats": ["json", "csv"],
      "include_bodies": false
    },

    "intent_declaration": {
      "required": false,
      "log_missing_intent": true
    }
  }
}
```

---

## Effort Estimate

| Phase | Scope | Deliverables | Effort |
|-------|-------|--------------|--------|
| **Phase 1** | Activity Log UX | Database schema, REST API, CLI commands, Web UI page, SSE events | 5-7 days |
| **Phase 2** | Intent Declaration | Enhanced call_tool schema, intent capture, display in all UIs | 3-4 days |

---

## Discussion Questions

1. **Intent Required**: Should intent declaration be mandatory?
   - Proposal: Optional by default, configurable per-deployment

---

## References

- [Obot MCPAuditLog Implementation](https://github.com/obot-platform/obot)
- [LangSmith Traces](https://docs.smith.langchain.com/)
- [OpenTelemetry GenAI Semantic Conventions](https://opentelemetry.io/docs/specs/semconv/gen-ai/)
- [MCP SEP-1763: Interceptors](https://github.com/modelcontextprotocol/modelcontextprotocol/issues/1763)
- RFC-004: Security & Attack Detection (companion document)
