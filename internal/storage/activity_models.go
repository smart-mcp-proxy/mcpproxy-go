package storage

import (
	"encoding/json"
	"strings"
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
	// ActivityTypeSystemStart represents MCPProxy server startup (Spec 024)
	ActivityTypeSystemStart ActivityType = "system_start"
	// ActivityTypeSystemStop represents MCPProxy server shutdown (Spec 024)
	ActivityTypeSystemStop ActivityType = "system_stop"
	// ActivityTypeInternalToolCall represents internal MCP tool calls like retrieve_tools, call_tool_* (Spec 024)
	ActivityTypeInternalToolCall ActivityType = "internal_tool_call"
	// ActivityTypeConfigChange represents configuration changes like server add/remove/update (Spec 024)
	ActivityTypeConfigChange ActivityType = "config_change"
)

// ValidActivityTypes is the list of all valid activity types for filtering (Spec 024)
var ValidActivityTypes = []string{
	string(ActivityTypeToolCall),
	string(ActivityTypePolicyDecision),
	string(ActivityTypeQuarantineChange),
	string(ActivityTypeServerChange),
	string(ActivityTypeSystemStart),
	string(ActivityTypeSystemStop),
	string(ActivityTypeInternalToolCall),
	string(ActivityTypeConfigChange),
}

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
	Types      []string  // Filter by activity types (Spec 024: supports multiple types with OR logic)
	Server     string    // Filter by server name
	Tool       string    // Filter by tool name
	SessionID  string    // Filter by MCP session
	Status     string    // Filter by status (success/error/blocked)
	StartTime  time.Time // Activities after this time
	EndTime    time.Time // Activities before this time
	Limit      int       // Max records to return (default 50, max 100)
	Offset     int       // Pagination offset
	IntentType string    // Filter by intent operation type: read, write, destructive (Spec 018)
	RequestID  string    // Filter by HTTP request ID for correlation (Spec 021)

	// Sensitive data detection filters (Spec 026)
	SensitiveData *bool  // Filter by sensitive data detection (nil=no filter, true=has detections, false=no detections)
	DetectionType string // Filter by specific detection type (e.g., "aws_access_key", "credit_card")
	Severity      string // Filter by severity level (critical, high, medium, low)

	// ExcludeCallToolSuccess filters out successful call_tool_* internal tool calls.
	// These appear as duplicates since the actual upstream tool call is also logged.
	// Failed call_tool_* calls are still shown (no corresponding tool_call entry).
	// Default: true (to avoid duplicate entries in UI/CLI)
	ExcludeCallToolSuccess bool
}

// DefaultActivityFilter returns an ActivityFilter with sensible defaults
func DefaultActivityFilter() ActivityFilter {
	return ActivityFilter{
		Limit:                  50,
		Offset:                 0,
		ExcludeCallToolSuccess: true, // Exclude successful call_tool_* to avoid duplicates
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
	// Check types filter (Spec 024: OR logic for multiple types)
	if len(f.Types) > 0 {
		typeMatches := false
		for _, t := range f.Types {
			if string(record.Type) == t {
				typeMatches = true
				break
			}
		}
		if !typeMatches {
			return false
		}
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

	// Check request_id filter (Spec 021)
	if f.RequestID != "" && record.RequestID != f.RequestID {
		return false
	}

	// Exclude successful call_tool_* internal tool calls to avoid duplicates
	// These have a corresponding tool_call entry that shows the actual upstream call.
	// Failed call_tool_* calls are shown since they have no corresponding tool_call.
	if f.ExcludeCallToolSuccess {
		if record.Type == ActivityTypeInternalToolCall &&
			record.Status == "success" &&
			strings.HasPrefix(record.ToolName, "call_tool_") {
			return false
		}
	}

	// Check sensitive data detection filters (Spec 026)
	if f.SensitiveData != nil || f.DetectionType != "" || f.Severity != "" {
		detected, detectionTypes, maxSeverity := extractSensitiveDataInfo(record)

		// Filter by has_sensitive_data
		if f.SensitiveData != nil {
			if *f.SensitiveData && !detected {
				return false
			}
			if !*f.SensitiveData && detected {
				return false
			}
		}

		// Filter by detection type
		if f.DetectionType != "" {
			found := false
			for _, dt := range detectionTypes {
				if dt == f.DetectionType {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}

		// Filter by severity
		if f.Severity != "" {
			if maxSeverity != f.Severity {
				return false
			}
		}
	}

	return true
}

// extractSensitiveDataInfo extracts sensitive data detection info from activity metadata.
// Returns (detected bool, detectionTypes []string, maxSeverity string).
func extractSensitiveDataInfo(record *ActivityRecord) (bool, []string, string) {
	if record.Metadata == nil {
		return false, nil, ""
	}

	detection, ok := record.Metadata["sensitive_data_detection"].(map[string]interface{})
	if !ok {
		return false, nil, ""
	}

	detected, _ := detection["detected"].(bool)
	if !detected {
		return false, nil, ""
	}

	// Extract detection types
	var detectionTypes []string
	typeSet := make(map[string]struct{})

	if detections, ok := detection["detections"].([]interface{}); ok {
		for _, d := range detections {
			if det, ok := d.(map[string]interface{}); ok {
				if dtype, ok := det["type"].(string); ok {
					if _, exists := typeSet[dtype]; !exists {
						typeSet[dtype] = struct{}{}
						detectionTypes = append(detectionTypes, dtype)
					}
				}
			}
		}
	}

	// Calculate max severity
	maxSeverity := calculateMaxSeverity(detection)

	return detected, detectionTypes, maxSeverity
}

// calculateMaxSeverity determines the highest severity from detection results.
// Severity order: critical > high > medium > low
func calculateMaxSeverity(detection map[string]interface{}) string {
	severityOrder := map[string]int{
		"critical": 4,
		"high":     3,
		"medium":   2,
		"low":      1,
	}

	maxLevel := 0
	maxSeverity := ""

	if detections, ok := detection["detections"].([]interface{}); ok {
		for _, d := range detections {
			if det, ok := d.(map[string]interface{}); ok {
				if sev, ok := det["severity"].(string); ok {
					if level, exists := severityOrder[sev]; exists && level > maxLevel {
						maxLevel = level
						maxSeverity = sev
					}
				}
			}
		}
	}

	return maxSeverity
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
