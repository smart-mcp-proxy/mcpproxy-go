package storage

import (
	"encoding/json"
	"time"
)

// ActivityRecordsBucket is the BBolt bucket name for activity records
const ActivityRecordsBucket = "activity_records"

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

// ActivityRecord represents a single activity log entry stored in BBolt
type ActivityRecord struct {
	ID                string                 `json:"id"`                           // Unique identifier (ULID format)
	Type              ActivityType           `json:"type"`                         // Type of activity
	Source            ActivitySource         `json:"source,omitempty"`             // How activity was triggered: "mcp", "cli", "api"
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

// MarshalBinary implements encoding.BinaryMarshaler for BBolt storage
func (a *ActivityRecord) MarshalBinary() ([]byte, error) {
	return json.Marshal(a)
}

// UnmarshalBinary implements encoding.BinaryUnmarshaler for BBolt storage
func (a *ActivityRecord) UnmarshalBinary(data []byte) error {
	return json.Unmarshal(data, a)
}

// ActivityFilter represents query parameters for filtering activity records
type ActivityFilter struct {
	Type       string    // Filter by activity type
	Server     string    // Filter by server name
	Tool       string    // Filter by tool name
	SessionID  string    // Filter by MCP session
	Status     string    // Filter by status (success/error/blocked)
	StartTime  time.Time // Activities after this time
	EndTime    time.Time // Activities before this time
	Limit      int       // Max records to return (default 50, max 100)
	Offset     int       // Pagination offset
	IntentType string    // Filter by intent operation type: read, write, destructive (Spec 018)
}

// DefaultActivityFilter returns an ActivityFilter with sensible defaults
func DefaultActivityFilter() ActivityFilter {
	return ActivityFilter{
		Limit:  50,
		Offset: 0,
	}
}

// Validate validates and normalizes the filter
func (f *ActivityFilter) Validate() {
	if f.Limit <= 0 {
		f.Limit = 50
	}
	if f.Limit > 100 {
		f.Limit = 100
	}
	if f.Offset < 0 {
		f.Offset = 0
	}
}

// Matches checks if an activity record matches the filter criteria
func (f *ActivityFilter) Matches(record *ActivityRecord) bool {
	// Check type filter
	if f.Type != "" && string(record.Type) != f.Type {
		return false
	}

	// Check server filter
	if f.Server != "" && record.ServerName != f.Server {
		return false
	}

	// Check tool filter
	if f.Tool != "" && record.ToolName != f.Tool {
		return false
	}

	// Check session filter
	if f.SessionID != "" && record.SessionID != f.SessionID {
		return false
	}

	// Check status filter
	if f.Status != "" && record.Status != f.Status {
		return false
	}

	// Check time range
	if !f.StartTime.IsZero() && record.Timestamp.Before(f.StartTime) {
		return false
	}
	if !f.EndTime.IsZero() && record.Timestamp.After(f.EndTime) {
		return false
	}

	// Check intent_type filter (Spec 018)
	if f.IntentType != "" {
		recordIntentType := extractIntentType(record)
		if recordIntentType != f.IntentType {
			return false
		}
	}

	return true
}

// extractIntentType extracts the operation type from activity metadata.
// It checks both intent.operation_type and derives from tool_variant as fallback.
func extractIntentType(record *ActivityRecord) string {
	if record.Metadata == nil {
		return ""
	}

	// Try to get intent.operation_type first
	if intent, ok := record.Metadata["intent"].(map[string]interface{}); ok {
		if opType, ok := intent["operation_type"].(string); ok {
			return opType
		}
	}

	// Fall back to deriving from tool_variant
	if toolVariant, ok := record.Metadata["tool_variant"].(string); ok {
		switch toolVariant {
		case "call_tool_read":
			return "read"
		case "call_tool_write":
			return "write"
		case "call_tool_destructive":
			return "destructive"
		}
	}

	return ""
}
