package contracts

import (
	"time"
)

// ActivityType represents the type of activity being recorded
type ActivityType string

const (
	// ActivityTypeToolCall represents a tool execution event
	ActivityTypeToolCall ActivityType = "tool_call"
	// ActivityTypePolicyDecision represents a policy blocking a tool call
	ActivityTypePolicyDecision ActivityType = "policy_decision"
	// ActivityTypeQuarantineChange represents a server quarantine state change
	ActivityTypeQuarantineChange ActivityType = "quarantine_change"
	// ActivityTypeServerChange represents a server configuration change
	ActivityTypeServerChange ActivityType = "server_change"
)

// ActivityRecord represents an activity record in API responses
type ActivityRecord struct {
	ID                string                 `json:"id"`                           // Unique identifier (ULID format)
	Type              ActivityType           `json:"type"`                         // Type of activity
	ServerName        string                 `json:"server_name,omitempty"`        // Name of upstream MCP server
	ToolName          string                 `json:"tool_name,omitempty"`          // Name of tool called
	Arguments         map[string]interface{} `json:"arguments,omitempty"`          // Tool call arguments
	Response          string                 `json:"response,omitempty"`           // Tool response (potentially truncated)
	ResponseTruncated bool                   `json:"response_truncated,omitempty"` // True if response was truncated
	Status            string                 `json:"status"`                       // Result status: "success", "error", "blocked"
	ErrorMessage      string                 `json:"error_message,omitempty"`      // Error details if status is "error"
	DurationMs        int64                  `json:"duration_ms,omitempty"`        // Execution duration in milliseconds
	Timestamp         time.Time              `json:"timestamp"`                    // When activity occurred
	SessionID         string                 `json:"session_id,omitempty"`         // MCP session ID for correlation
	RequestID         string                 `json:"request_id,omitempty"`         // HTTP request ID for correlation
	Metadata          map[string]interface{} `json:"metadata,omitempty"`           // Additional context-specific data
}

// ActivityListResponse is the response for GET /api/v1/activity
type ActivityListResponse struct {
	Activities []ActivityRecord `json:"activities"`
	Total      int              `json:"total"`
	Limit      int              `json:"limit"`
	Offset     int              `json:"offset"`
}

// ActivityDetailResponse is the response for GET /api/v1/activity/{id}
type ActivityDetailResponse struct {
	Activity ActivityRecord `json:"activity"`
}

// ActivityExportFormat represents the format for exporting activities
type ActivityExportFormat string

const (
	// ActivityExportFormatJSON exports as JSON Lines (JSONL)
	ActivityExportFormatJSON ActivityExportFormat = "json"
	// ActivityExportFormatCSV exports as CSV
	ActivityExportFormatCSV ActivityExportFormat = "csv"
)

// ActivitySSEEvent represents an activity event for SSE streaming
type ActivitySSEEvent struct {
	EventType  string                 `json:"event_type"`  // SSE event name
	ActivityID string                 `json:"activity_id"` // Reference to ActivityRecord
	Timestamp  int64                  `json:"timestamp"`   // Unix timestamp
	Payload    map[string]interface{} `json:"payload"`     // Event-specific data
}

// ActivitySummaryResponse is the response for GET /api/v1/activity/summary
type ActivitySummaryResponse struct {
	Period       string              `json:"period"`                  // Time period (1h, 24h, 7d, 30d)
	TotalCount   int                 `json:"total_count"`             // Total activity count
	SuccessCount int                 `json:"success_count"`           // Count of successful activities
	ErrorCount   int                 `json:"error_count"`             // Count of error activities
	BlockedCount int                 `json:"blocked_count"`           // Count of blocked activities
	TopServers   []ActivityTopServer `json:"top_servers,omitempty"`   // Top servers by activity count
	TopTools     []ActivityTopTool   `json:"top_tools,omitempty"`     // Top tools by activity count
	StartTime    string              `json:"start_time"`              // Start of the period (RFC3339)
	EndTime      string              `json:"end_time"`                // End of the period (RFC3339)
}

// ActivityTopServer represents a server's activity count in the summary
type ActivityTopServer struct {
	Name  string `json:"name"`  // Server name
	Count int    `json:"count"` // Activity count
}

// ActivityTopTool represents a tool's activity count in the summary
type ActivityTopTool struct {
	Server string `json:"server"` // Server name
	Tool   string `json:"tool"`   // Tool name
	Count  int    `json:"count"`  // Activity count
}
