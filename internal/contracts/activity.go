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

// ActivitySource indicates how the activity was triggered
type ActivitySource string

const (
	// ActivitySourceMCP indicates the activity was triggered via MCP protocol (AI agent)
	ActivitySourceMCP ActivitySource = "mcp"
	// ActivitySourceCLI indicates the activity was triggered via CLI command
	ActivitySourceCLI ActivitySource = "cli"
	// ActivitySourceAPI indicates the activity was triggered via REST API
	ActivitySourceAPI ActivitySource = "api"
)

// ActivityRecord represents an activity record in API responses
type ActivityRecord struct {
	ID                string                 `json:"id"`                                       // Unique identifier (ULID format)
	Type              ActivityType           `json:"type"`                                     // Type of activity
	Source            ActivitySource         `json:"source,omitempty"`                         // How activity was triggered: "mcp", "cli", "api"
	ServerName        string                 `json:"server_name,omitempty"`                    // Name of upstream MCP server
	ToolName          string                 `json:"tool_name,omitempty"`                      // Name of tool called
	Arguments         map[string]interface{} `json:"arguments,omitempty" swaggertype:"object"` // Tool call arguments
	Response          string                 `json:"response,omitempty"`                       // Tool response (potentially truncated)
	ResponseTruncated bool                   `json:"response_truncated,omitempty"`             // True if response was truncated
	Status            string                 `json:"status"`                                   // Result status: "success", "error", "blocked", "rejected"
	ErrorMessage      string                 `json:"error_message,omitempty"`                  // Error details if status is "error"
	DurationMs        int64                  `json:"duration_ms,omitempty"`                    // Execution duration in milliseconds
	Timestamp         time.Time              `json:"timestamp"`                                // When activity occurred
	SessionID         string                 `json:"session_id,omitempty"`                     // MCP transport session ID (regenerated on every reconnect)
	WorkSessionID     string                 `json:"work_session_id,omitempty"`                // Spec 082: one client, one project, across reconnects
	RequestID         string                 `json:"request_id,omitempty"`                     // HTTP request ID for correlation
	ParentID          string                 `json:"parent_id,omitempty"`                      // Correlation id of the parent call (the code_execution whose sandbox issued this sub-call)
	Metadata          map[string]interface{} `json:"metadata,omitempty" swaggertype:"object"`  // Additional context-specific data

	// Byte sizes measured pre-truncation, mirroring storage.ActivityRecord
	// (Spec 069 A1). They are the only cost signal a bodies-off export carries:
	// with payloads suppressed there is no text left to measure, so a consumer
	// accounting for a record it cannot read has nothing else to go on. They are
	// byte LENGTHS, not token counts — the basis for an explicit estimate, never
	// a measured figure (spec 103, contracts/replay-input.md).
	//
	// Zero means UNKNOWN, not free: legacy records predate the measurement and
	// code-execution sub-calls record both as zero. Hence omitempty — an absent
	// key tells a consumer to fall to exclusion accounting, whereas a present
	// zero would read as a costless call and silently understate the workload.
	RequestBytes  int `json:"request_bytes,omitempty"`  // JSON-serialized request arguments size in bytes
	ResponseBytes int `json:"response_bytes,omitempty"` // Raw upstream response size in bytes before truncation

	// Sensitive data detection fields (Spec 026)
	HasSensitiveData bool     `json:"has_sensitive_data"`        // Whether sensitive data was detected
	DetectionTypes   []string `json:"detection_types,omitempty"` // List of detection types found
	MaxSeverity      string   `json:"max_severity,omitempty"`    // Highest severity level detected (critical, high, medium, low)
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
	EventType  string                 `json:"event_type"`                   // SSE event name
	ActivityID string                 `json:"activity_id"`                  // Reference to ActivityRecord
	Timestamp  int64                  `json:"timestamp"`                    // Unix timestamp
	Payload    map[string]interface{} `json:"payload" swaggertype:"object"` // Event-specific data
}

// ActivitySummaryResponse is the response for GET /api/v1/activity/summary
type ActivitySummaryResponse struct {
	Period       string `json:"period"`        // Time period (1h, 24h, 7d, 30d)
	TotalCount   int    `json:"total_count"`   // Total activity count
	SuccessCount int    `json:"success_count"` // Count of successful activities
	ErrorCount   int    `json:"error_count"`   // Count of error activities
	BlockedCount int    `json:"blocked_count"` // Count of blocked activities
	// RejectedCount is the number of calls shed by a concurrency limiter before
	// they reached an upstream (spec 093). Counted separately from errors: it is
	// proxy backpressure, not an upstream fault, and it is the signal an
	// operator right-sizes max_concurrent_requests against.
	RejectedCount int `json:"rejected_count"`
	// OtherCount is every record whose status is outside the four-value
	// vocabulary above, so that
	//
	//	success + error + blocked + rejected + other == total
	//
	// holds by construction. The status field is a CLOSED vocabulary for tool
	// calls, but the activity log is wider than tool calls: a quarantine change
	// stores its ACTION there ("approved", "auto_approved"), a policy decision
	// stores its DECISION ("allow"). Those rows were counted in the total and in
	// none of the four buckets, so the Activity Log's own status tiles summed to
	// less than the denominator printed beside them — 15+4+0+0 under a "42"
	// (audit finding F2, #1046). The residual now has a name and a tile.
	OtherCount int `json:"other_count"`
	// CallCount is how many of those records are CALLS THE USER MADE, as
	// defined once in storage.CountsAsCall and shared with the usage aggregate
	// behind the Usage tab (audit finding F1, #1046). TotalCount answers "how
	// many rows does the Activity Log have"; CallCount answers "how many calls
	// were there". They are different questions — quarantine auto-approvals,
	// system start, security scans and management chatter are events, not calls
	// — and printing either one under the other's label is how the same instance
	// came to report 51 calls on one screen and 19 on another.
	CallCount int `json:"call_count"`
	// CallErrorCount is the failures within CallCount, so an error RATE computed
	// from this response has one denominator. It is not ErrorCount: a policy
	// block is a failed call but carries status "blocked", and a shed call is an
	// error in neither sense because it never ran.
	CallErrorCount int                 `json:"call_error_count"`
	TopServers     []ActivityTopServer `json:"top_servers,omitempty"` // Top servers by activity count
	TopTools       []ActivityTopTool   `json:"top_tools,omitempty"`   // Top tools by activity count
	StartTime      string              `json:"start_time"`            // Start of the period (RFC3339)
	EndTime        string              `json:"end_time"`              // End of the period (RFC3339)
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
