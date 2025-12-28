# Research: Activity CLI Commands

**Date**: 2025-12-27
**Feature**: 017-activity-cli-commands

## Overview

This document captures research decisions for implementing activity CLI commands. Since this is primarily a CLI wrapper around the existing activity REST API (spec 016), research focuses on CLI-specific patterns.

---

## 1. SSE Client for Watch Command

### Decision
Use `bufio.Scanner` with custom split function for SSE parsing.

### Rationale
- Standard library approach, no external dependencies
- Works with existing `/events` endpoint
- Simple line-by-line parsing matches SSE format
- Handles reconnection via wrapper loop

### Alternatives Considered

| Alternative | Why Rejected |
|-------------|--------------|
| r3labs/sse | External dependency, overkill for simple use case |
| Custom HTTP client | bufio.Scanner is simpler and sufficient |
| Polling /activity endpoint | Higher latency, more API calls, not real-time |

### Implementation Pattern

```go
func watchActivityStream(ctx context.Context, client *http.Client, baseURL string, filter ActivityFilter) error {
    // Build SSE URL with filter params
    u, _ := url.Parse(baseURL + "/events")
    q := u.Query()
    if filter.Type != "" {
        q.Set("type", filter.Type)
    }
    if filter.Server != "" {
        q.Set("server", filter.Server)
    }
    u.RawQuery = q.Encode()

    req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
    resp, err := client.Do(req)
    if err != nil {
        return fmt.Errorf("connect to event stream: %w", err)
    }
    defer resp.Body.Close()

    scanner := bufio.NewScanner(resp.Body)
    var eventType, eventData string

    for scanner.Scan() {
        line := scanner.Text()
        switch {
        case strings.HasPrefix(line, "event: "):
            eventType = strings.TrimPrefix(line, "event: ")
        case strings.HasPrefix(line, "data: "):
            eventData = strings.TrimPrefix(line, "data: ")
        case line == "":
            // Empty line = event complete
            if strings.HasPrefix(eventType, "activity.") {
                displayActivityEvent(eventType, eventData)
            }
            eventType, eventData = "", ""
        }
    }
    return scanner.Err()
}
```

---

## 2. Timestamp Display Format

### Decision
Use relative time for recent activities, absolute for older ones.

### Rationale
- Matches `gh` and `kubectl` patterns
- "2 minutes ago" is more readable than "2025-12-27T10:30:00Z"
- Absolute time available via `--time-format` flag or JSON output

### Format Rules

| Age | Display Format | Example |
|-----|---------------|---------|
| < 1 min | "just now" | just now |
| < 1 hour | "X minutes ago" | 5 minutes ago |
| < 24 hours | "X hours ago" | 3 hours ago |
| < 7 days | "X days ago" | 2 days ago |
| >= 7 days | "MMM DD" | Dec 20 |
| >= 1 year | "MMM DD, YYYY" | Dec 20, 2024 |

### Implementation

```go
func formatRelativeTime(t time.Time) string {
    now := time.Now()
    diff := now.Sub(t)

    switch {
    case diff < time.Minute:
        return "just now"
    case diff < time.Hour:
        mins := int(diff.Minutes())
        if mins == 1 {
            return "1 minute ago"
        }
        return fmt.Sprintf("%d minutes ago", mins)
    case diff < 24*time.Hour:
        hours := int(diff.Hours())
        if hours == 1 {
            return "1 hour ago"
        }
        return fmt.Sprintf("%d hours ago", hours)
    case diff < 7*24*time.Hour:
        days := int(diff.Hours() / 24)
        if days == 1 {
            return "1 day ago"
        }
        return fmt.Sprintf("%d days ago", days)
    case t.Year() == now.Year():
        return t.Format("Jan 02")
    default:
        return t.Format("Jan 02, 2006")
    }
}
```

---

## 3. Summary Aggregation

### Decision
Server-side aggregation via new `/api/v1/activity/summary` endpoint.

### Rationale
- Client-side aggregation would require fetching all records (up to 100k)
- Server has access to database for efficient COUNT/GROUP BY
- Spec 016 backend already has storage layer for queries

### Alternatives Considered

| Alternative | Why Rejected |
|-------------|--------------|
| Client-side aggregation | Too slow for large datasets, memory issues |
| Pre-computed summaries | Adds complexity, real-time queries are fast enough |
| Sampling | Inaccurate for small datasets |

### Required Backend Addition

The summary endpoint needs to be added to spec 016's activity.go:

```go
// GET /api/v1/activity/summary
// Query params: period (1h, 24h, 7d, 30d), by (server, tool, status)
type ActivitySummaryResponse struct {
    Period      string                 `json:"period"`
    TotalCount  int                    `json:"total_count"`
    SuccessCount int                   `json:"success_count"`
    ErrorCount  int                    `json:"error_count"`
    BlockedCount int                   `json:"blocked_count"`
    SuccessRate float64                `json:"success_rate"`
    TopServers  []ServerSummary        `json:"top_servers"`
    TopTools    []ToolSummary          `json:"top_tools"`
    ByGrouping  map[string]int         `json:"by_grouping,omitempty"`
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
```

### Note
This requires extending spec 016 with a summary endpoint. If not available, the summary command can fall back to fetching the last N records and computing locally (with a warning about incomplete data).

---

## 4. Export Streaming

### Decision
Use existing `/api/v1/activity/export` endpoint with streaming output.

### Rationale
- Endpoint already supports JSON Lines and CSV formats
- Streaming avoids loading all records in memory
- CLI just pipes response body to file

### Implementation Pattern

```go
func runActivityExport(cmd *cobra.Command, args []string) error {
    // Build export URL
    u, _ := url.Parse(baseURL + "/api/v1/activity/export")
    q := u.Query()
    q.Set("format", exportFormat) // "json" or "csv"
    // Add filter params...
    u.RawQuery = q.Encode()

    resp, err := client.Get(u.String())
    if err != nil {
        return err
    }
    defer resp.Body.Close()

    // Determine output destination
    var w io.Writer = os.Stdout
    if outputPath != "" {
        f, err := os.Create(outputPath)
        if err != nil {
            return fmt.Errorf("create output file: %w", err)
        }
        defer f.Close()
        w = f
    }

    // Stream response to output
    _, err = io.Copy(w, resp.Body)
    return err
}
```

---

## 5. Filter Flag Naming

### Decision
Use consistent flag names across all activity commands.

### Rationale
- Matches REST API query parameters
- Users learn once, apply everywhere
- JSON output uses same field names

### Flag Mapping

| Flag | Short | API Param | Description |
|------|-------|-----------|-------------|
| `--type` | `-t` | `type` | Activity type filter |
| `--server` | `-s` | `server` | Server name filter |
| `--tool` | | `tool` | Tool name filter |
| `--status` | | `status` | Status filter (success, error, blocked) |
| `--session` | | `session_id` | Session ID filter |
| `--start-time` | | `start_time` | Start time (RFC3339) |
| `--end-time` | | `end_time` | End time (RFC3339) |
| `--limit` | `-n` | `limit` | Max records |
| `--offset` | | `offset` | Pagination offset |

---

## 6. Watch Command Event Filtering

### Decision
Filter events client-side from the general `/events` SSE stream.

### Rationale
- The `/events` endpoint streams all events (servers, config, activity)
- Client filters for `activity.*` event types
- Additional filtering by server/type done on event data

### Event Types to Display

| SSE Event Type | Display |
|---------------|---------|
| `activity.tool_call.started` | Show with "started" indicator |
| `activity.tool_call.completed` | Show with duration and status |
| `activity.policy_decision` | Show with "BLOCKED" indicator |
| `activity.quarantine_change` | Show quarantine status change |
| `activity.server_change` | Skip (not activity per se) |

---

## 7. Reconnection Strategy for Watch

### Decision
Automatic reconnection with exponential backoff.

### Rationale
- Long-running watch commands need resilience
- Network blips shouldn't require manual restart
- Matches behavior of `kubectl logs -f` and similar tools

### Implementation

```go
func watchWithReconnect(ctx context.Context, baseURL string, filter ActivityFilter) error {
    backoff := 1 * time.Second
    maxBackoff := 30 * time.Second

    for {
        err := watchActivityStream(ctx, baseURL, filter)

        select {
        case <-ctx.Done():
            return ctx.Err()
        default:
        }

        if err != nil {
            fmt.Fprintf(os.Stderr, "Connection lost: %v. Reconnecting in %v...\n", err, backoff)
            time.Sleep(backoff)
            backoff = min(backoff*2, maxBackoff)
            continue
        }

        // Reset backoff on successful connection
        backoff = 1 * time.Second
    }
}
```

---

## Conclusion

All research items resolved. Key decisions:

1. **SSE**: Use bufio.Scanner (standard library)
2. **Time format**: Relative for recent, absolute for older
3. **Summary**: Requires server-side endpoint (extend spec 016)
4. **Export**: Stream directly from API to file
5. **Flags**: Consistent naming matching API params
6. **Watch filtering**: Client-side from general event stream
7. **Reconnection**: Exponential backoff for resilience
