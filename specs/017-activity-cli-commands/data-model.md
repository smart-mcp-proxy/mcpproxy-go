# Data Model: Activity CLI Commands

**Date**: 2025-12-27
**Feature**: 017-activity-cli-commands

## Overview

This document defines the data models for the activity CLI commands. Since the CLI is a thin wrapper around the REST API, most models mirror the API contracts from spec 016.

---

## CLI-Specific Types

### ActivityFilter

Filter options shared across list, watch, export, and summary commands.

```go
// ActivityFilter contains options for filtering activity records
type ActivityFilter struct {
    Type      string    // "tool_call", "policy_decision", "quarantine_change", "server_change"
    Server    string    // Filter by server name
    Tool      string    // Filter by tool name
    Status    string    // "success", "error", "blocked"
    SessionID string    // Filter by MCP session ID
    StartTime time.Time // Filter records after this time
    EndTime   time.Time // Filter records before this time
    Limit     int       // Max records (1-100, default 50)
    Offset    int       // Pagination offset
}

// Validation rules:
// - Type must be one of: tool_call, policy_decision, quarantine_change, server_change (or empty)
// - Status must be one of: success, error, blocked (or empty)
// - Limit must be 1-100 (clamped if out of range)
// - StartTime/EndTime must be valid RFC3339 format when provided
```

### ActivityListResult

Result from list command, used for output formatting.

```go
// ActivityListResult contains paginated activity records
type ActivityListResult struct {
    Activities []ActivityRecord `json:"activities"`
    Total      int              `json:"total"`
    Limit      int              `json:"limit"`
    Offset     int              `json:"offset"`
}

// Table columns for list:
// ID | TYPE | SERVER | TOOL | STATUS | DURATION | TIME
```

### ActivityRecord

Mirrors the API contract from spec 016.

```go
// ActivityRecord represents a single activity entry
type ActivityRecord struct {
    ID                string                 `json:"id"`
    Type              string                 `json:"type"`
    ServerName        string                 `json:"server_name"`
    ToolName          string                 `json:"tool_name"`
    Arguments         map[string]interface{} `json:"arguments,omitempty"`
    Response          string                 `json:"response,omitempty"`
    ResponseTruncated bool                   `json:"response_truncated,omitempty"`
    Status            string                 `json:"status"`
    ErrorMessage      string                 `json:"error_message,omitempty"`
    DurationMs        int64                  `json:"duration_ms"`
    Timestamp         time.Time              `json:"timestamp"`
    SessionID         string                 `json:"session_id,omitempty"`
    RequestID         string                 `json:"request_id,omitempty"`
    Metadata          map[string]interface{} `json:"metadata,omitempty"`
}

// State: No state transitions (records are immutable)
```

### ActivitySummary

Summary statistics for a time period.

```go
// ActivitySummary contains aggregated activity statistics
type ActivitySummary struct {
    Period       string          `json:"period"`        // "1h", "24h", "7d", "30d"
    TotalCount   int             `json:"total_count"`
    SuccessCount int             `json:"success_count"`
    ErrorCount   int             `json:"error_count"`
    BlockedCount int             `json:"blocked_count"`
    SuccessRate  float64         `json:"success_rate"`  // 0.0 - 1.0
    TopServers   []ServerSummary `json:"top_servers"`   // Top 5 by count
    TopTools     []ToolSummary   `json:"top_tools"`     // Top 5 by count
}

type ServerSummary struct {
    Name  string `json:"name"`
    Count int    `json:"count"`
}

type ToolSummary struct {
    Server string `json:"server"`
    Tool   string `json:"tool"`
    Count  int    `json:"count"`
}

// Table format for summary:
// METRIC    | VALUE
// ----------|-------
// Period    | 24h
// Total     | 150
// Success   | 142 (94.7%)
// Errors    | 5 (3.3%)
// Blocked   | 3 (2.0%)
//
// TOP SERVERS
// NAME           | COUNT
// ---------------|------
// github         | 75
// filesystem     | 45
//
// TOP TOOLS
// SERVER:TOOL              | COUNT
// -------------------------|------
// github:create_issue      | 30
// filesystem:read_file     | 25
```

### WatchEvent

Event received from SSE stream during watch.

```go
// WatchEvent represents an activity event from SSE stream
type WatchEvent struct {
    Type      string                 `json:"type"`       // Event type (activity.tool_call.completed, etc.)
    ID        string                 `json:"id"`
    Server    string                 `json:"server"`
    Tool      string                 `json:"tool,omitempty"`
    Status    string                 `json:"status,omitempty"`
    Duration  int64                  `json:"duration_ms,omitempty"`
    Timestamp time.Time              `json:"timestamp"`
    Error     string                 `json:"error,omitempty"`
    Metadata  map[string]interface{} `json:"metadata,omitempty"`
}

// Display format for watch:
// [10:30:45] github:create_issue ✓ 245ms
// [10:30:46] filesystem:write_file ✗ 125ms (permission denied)
// [10:30:47] private-api:get_data ⊘ BLOCKED (policy: no-external)
```

---

## Relationships

```
┌─────────────────┐
│ ActivityFilter  │
└────────┬────────┘
         │ used by
         ▼
┌─────────────────┐     ┌──────────────────┐
│ List Command    │────▶│ ActivityListResult│
└─────────────────┘     └────────┬─────────┘
                                 │ contains[]
                                 ▼
┌─────────────────┐     ┌──────────────────┐
│ Show Command    │────▶│ ActivityRecord   │
└─────────────────┘     └──────────────────┘
                                 ▲
┌─────────────────┐              │
│ Watch Command   │──────────────┤ (via SSE)
└─────────────────┘              │
                        ┌────────┴─────────┐
                        │ WatchEvent       │
                        └──────────────────┘

┌─────────────────┐     ┌──────────────────┐
│ Summary Command │────▶│ ActivitySummary  │
└─────────────────┘     └──────────────────┘

┌─────────────────┐
│ Export Command  │────▶ Streams JSON Lines or CSV directly
└─────────────────┘
```

---

## Validation Rules

| Field | Rule | Error Code |
|-------|------|------------|
| Type | Must be valid activity type or empty | INVALID_TYPE |
| Status | Must be success/error/blocked or empty | INVALID_STATUS |
| Limit | 1-100, values outside clamped | (silent clamp) |
| StartTime | Valid RFC3339 format | INVALID_TIME_FORMAT |
| EndTime | Valid RFC3339 format, >= StartTime | INVALID_TIME_RANGE |
| Activity ID | Valid ULID format | INVALID_ID_FORMAT |

---

## Output Format Mapping

| Format | List | Show | Watch | Summary | Export |
|--------|------|------|-------|---------|--------|
| table | Paginated table | Key-value pairs | Real-time lines | Statistics table | N/A |
| json | Array of records | Single record | NDJSON lines | Object | JSON Lines |
| yaml | Array of records | Single record | N/A | Object | N/A |
| csv | N/A | N/A | N/A | N/A | CSV with headers |
