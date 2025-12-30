# Data Model: Activity Log Web UI

**Branch**: `019-activity-webui` | **Date**: 2025-12-29

## Overview

This document defines the TypeScript interfaces and data models for the Activity Log Web UI feature. These interfaces mirror the backend contracts and provide type safety for the frontend implementation.

## Core Entities

### ActivityRecord

Represents a single activity entry from the backend.

```typescript
// Activity type enumeration
export type ActivityType =
  | 'tool_call'        // Tool execution event
  | 'policy_decision'  // Policy blocking a tool call
  | 'quarantine_change' // Server quarantine state change
  | 'server_change'    // Server configuration change

// Activity source enumeration
export type ActivitySource =
  | 'mcp'   // Triggered via MCP protocol (AI agent)
  | 'cli'   // Triggered via CLI command
  | 'api'   // Triggered via REST API

// Activity status enumeration
export type ActivityStatus = 'success' | 'error' | 'blocked'

// Main activity record interface
export interface ActivityRecord {
  id: string                          // Unique identifier (ULID format)
  type: ActivityType                  // Type of activity
  source?: ActivitySource             // How activity was triggered
  server_name?: string                // Name of upstream MCP server
  tool_name?: string                  // Name of tool called
  arguments?: Record<string, any>     // Tool call arguments
  response?: string                   // Tool response (potentially truncated)
  response_truncated?: boolean        // True if response was truncated
  status: ActivityStatus              // Result status
  error_message?: string              // Error details if status is "error"
  duration_ms?: number                // Execution duration in milliseconds
  timestamp: string                   // When activity occurred (ISO 8601)
  session_id?: string                 // MCP session ID for correlation
  request_id?: string                 // HTTP request ID for correlation
  metadata?: Record<string, any>      // Additional context-specific data
}
```

### API Response Types

```typescript
// Response for GET /api/v1/activity
export interface ActivityListResponse {
  activities: ActivityRecord[]
  total: number                       // Total matching records (for pagination)
  limit: number                       // Records per page
  offset: number                      // Current offset
}

// Response for GET /api/v1/activity/{id}
export interface ActivityDetailResponse {
  activity: ActivityRecord
}

// Response for GET /api/v1/activity/summary
export interface ActivitySummaryResponse {
  period: string                      // Time period (1h, 24h, 7d, 30d)
  total_count: number                 // Total activity count
  success_count: number               // Count of successful activities
  error_count: number                 // Count of error activities
  blocked_count: number               // Count of blocked activities
  top_servers?: ActivityTopServer[]   // Top servers by activity count
  top_tools?: ActivityTopTool[]       // Top tools by activity count
  start_time: string                  // Start of the period (RFC3339)
  end_time: string                    // End of the period (RFC3339)
}

export interface ActivityTopServer {
  name: string                        // Server name
  count: number                       // Activity count
}

export interface ActivityTopTool {
  server: string                      // Server name
  tool: string                        // Tool name
  count: number                       // Activity count
}
```

### Filter Types

```typescript
// Filter parameters for activity queries
export interface ActivityFilter {
  type?: ActivityType                 // Filter by activity type
  server?: string                     // Filter by server name
  tool?: string                       // Filter by tool name
  session_id?: string                 // Filter by MCP session ID
  status?: ActivityStatus             // Filter by status
  intent_type?: 'read' | 'write' | 'destructive' // Filter by intent operation type
  start_time?: string                 // Filter activities after this time (RFC3339)
  end_time?: string                   // Filter activities before this time (RFC3339)
  limit?: number                      // Maximum records to return (1-100)
  offset?: number                     // Pagination offset
}

// Export parameters
export interface ActivityExportParams extends ActivityFilter {
  format: 'json' | 'csv'              // Export format
  include_bodies?: boolean            // Include request/response bodies
}
```

### SSE Event Types

```typescript
// SSE event types for real-time updates
export type ActivitySSEEventType =
  | 'activity.tool_call.started'
  | 'activity.tool_call.completed'
  | 'activity.policy_decision'
  | 'activity.quarantine_change'

// SSE event payload for tool call started
export interface ActivityToolCallStartedPayload {
  server_name: string
  tool_name: string
  session_id?: string
  request_id?: string
  arguments?: Record<string, any>
}

// SSE event payload for tool call completed
export interface ActivityToolCallCompletedPayload {
  server_name: string
  tool_name: string
  session_id?: string
  request_id?: string
  status: ActivityStatus
  error_message?: string
  duration_ms: number
  response?: string
  response_truncated?: boolean
}

// SSE event payload for policy decision
export interface ActivityPolicyDecisionPayload {
  server_name: string
  tool_name: string
  session_id?: string
  decision: 'blocked'
  reason: string
}

// SSE event payload for quarantine change
export interface ActivityQuarantineChangePayload {
  server_name: string
  quarantined: boolean
  reason: string
}
```

## Component State Types

### Activity Page State

```typescript
// State for Activity.vue component
export interface ActivityPageState {
  // Data
  activities: ActivityRecord[]
  selectedActivity: ActivityRecord | null

  // Filters
  filter: ActivityFilter

  // Pagination
  currentPage: number
  pageSize: number
  totalRecords: number

  // UI State
  loading: boolean
  error: string | null
  autoRefresh: boolean
  detailPanelOpen: boolean

  // Filter options (populated from data)
  availableServers: string[]
  availableTypes: ActivityType[]
  availableStatuses: ActivityStatus[]
}
```

### Dashboard Widget State

```typescript
// State for ActivityWidget.vue component
export interface ActivityWidgetState {
  summary: ActivitySummaryResponse | null
  recentActivities: ActivityRecord[]
  loading: boolean
  error: string | null
}
```

## Relationships

```
ActivityRecord
├── type: ActivityType (enum)
├── source: ActivitySource (enum)
├── status: ActivityStatus (enum)
└── metadata: Record<string, any>
    └── (for tool_call) intent: IntentDeclaration
        ├── operation_type: 'read' | 'write' | 'destructive'
        ├── data_sensitivity: string
        └── reason: string

ActivityListResponse
└── activities: ActivityRecord[]

ActivitySummaryResponse
├── top_servers: ActivityTopServer[]
└── top_tools: ActivityTopTool[]
```

## Validation Rules

### ActivityFilter

| Field | Rule |
|-------|------|
| limit | 1-100, default 50 |
| offset | >= 0, default 0 |
| start_time | Valid RFC3339 datetime or empty |
| end_time | Valid RFC3339 datetime or empty |
| type | One of ActivityType values or empty |
| status | One of ActivityStatus values or empty |

### Pagination

| Field | Rule |
|-------|------|
| pageSize | 10, 25, 50, 100 (selectable) |
| currentPage | >= 1 |
| totalPages | ceil(totalRecords / pageSize) |

## State Transitions

### Activity Status Flow

```
[Started] ─────────────> [Completed: success]
    │
    └──────────────────> [Completed: error]

[Policy Check] ────────> [Blocked]
```

### SSE Update Flow

```
SSE Event Received
    │
    ├─ tool_call.started ──> Add pending activity to list
    │
    ├─ tool_call.completed ─> Update existing activity with result
    │
    ├─ policy_decision ────> Add blocked activity to list
    │
    └─ quarantine_change ──> Refresh server list (handled by servers.changed)
```
