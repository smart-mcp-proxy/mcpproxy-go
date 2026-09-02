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
	// ActivityTypeToolQuarantineChange represents a tool-level quarantine state change (Spec 032)
	ActivityTypeToolQuarantineChange ActivityType = "tool_quarantine_change"
	// ActivityTypeSecurityScan represents a security scan event (Spec 039)
	ActivityTypeSecurityScan ActivityType = "security_scan"
	// ActivityTypeCredentialBroker represents a per-user credential brokering
	// event: acquisition, refresh, injection, or connect (Spec 074 T10). It
	// carries attribution (UserID, ServerName) and never any token/secret value.
	ActivityTypeCredentialBroker ActivityType = "credential_broker"
	// ActivityTypePreflight represents one executed required-tools preflight
	// (Spec 098 FR-014). The record is written SYNCHRONOUSLY before the
	// preflight is answered — see runtime.ActivityService.RecordPreflight — so a
	// caller that got a verdict can always find the run that produced it.
	//
	// A preflight is set-scoped, not server-scoped: ServerName and ToolName stay
	// empty and the per-tool detail lives in Metadata under the MetadataKeyPreflight*
	// keys. RequestID is the correlation handle (`activity list --request-id`).
	ActivityTypePreflight ActivityType = "preflight"

	// ActivityTypePromptGet represents an upstream prompts/get fetch (Finding F10).
	// Mirrors the tool-call path: ServerName is the upstream server, ToolName is
	// the prompt name, Arguments are the prompt arguments (scanned for sensitive
	// data like tool args), RequestID is the correlation handle.
	ActivityTypePromptGet ActivityType = "prompt_get"
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
	string(ActivityTypeToolQuarantineChange),
	string(ActivityTypeSecurityScan),
	string(ActivityTypeCredentialBroker),
	string(ActivityTypePreflight),
	string(ActivityTypePromptGet),
}

// Activity status vocabulary. Activity status is a CLOSED vocabulary: every
// consumer (filters, summaries, usage aggregation, exports, Web UI badges)
// switches on these values, so a new status has to be threaded through all of
// them — see spec 093 FR-012.
const (
	// ActivityStatusSuccess is a call the upstream answered normally.
	ActivityStatusSuccess = "success"
	// ActivityStatusError is a call that failed (transport, upstream error, or
	// an isError:true answer).
	ActivityStatusError = "error"
	// ActivityStatusBlocked is a call a policy prevented from running.
	ActivityStatusBlocked = "blocked"
	// ActivityStatusRejected is a call shed by a concurrency limiter before it
	// ever reached the upstream (spec 093). Distinct from "error" on purpose:
	// nothing went wrong upstream, the proxy applied backpressure. The record's
	// metadata carries rejection_reason (queue_full | queue_timeout) and
	// rejection_scope (server | global).
	ActivityStatusRejected = "rejected"
)

// ValidActivityStatuses is the closed status vocabulary, for filter validation
// and API documentation.
var ValidActivityStatuses = []string{
	ActivityStatusSuccess,
	ActivityStatusError,
	ActivityStatusBlocked,
	ActivityStatusRejected,
}

// InternalCallToolPrefix marks the internal_tool_call records that MIRROR a
// direct upstream dispatch (call_tool_read / _write / _destructive). Every one
// of them is paired with a tool_call record for the same dispatch, which is why
// both the list filter and the usage aggregate key off this prefix to avoid
// counting one call twice. Internal records WITHOUT it (retrieve_tools,
// describe_tool, code_execution, upstream_servers, quarantine_security …) are
// mcpproxy's own work and have no paired record.
const InternalCallToolPrefix = "call_tool_"

// Metadata keys carried by an ActivityStatusRejected record (spec 093 FR-012).
const (
	// MetadataKeyRejectionReason is "queue_full" or "queue_timeout".
	MetadataKeyRejectionReason = "rejection_reason"
	// MetadataKeyRejectionScope is "server" or "global".
	MetadataKeyRejectionScope = "rejection_scope"
	// MetadataKeyRejectionLimit is the cap that was in force in that scope.
	MetadataKeyRejectionLimit = "rejection_limit"
	// MetadataKeyRejectionRetryAfterMs is the Retry-After hint in milliseconds.
	MetadataKeyRejectionRetryAfterMs = "rejection_retry_after_ms"
)

// Metadata keys carried by an ActivityTypePreflight record (spec 098 FR-014,
// data-model.md "Activity record"). The payload is deliberately small and
// enum-only: reason CODES and counts, never tool descriptions or arguments.
const (
	// MetadataKeyPreflightVerdict is the set-level verdict
	// (ready|degraded_retryable|blocked|unknown_ids).
	MetadataKeyPreflightVerdict = "verdict"
	// MetadataKeyPreflightIDsCount is the number of unique tool ids evaluated.
	MetadataKeyPreflightIDsCount = "ids_count"
	// MetadataKeyPreflightReasons is a {reason_code: count} rollup over the
	// unavailable results — the shape a dashboard or CLI summary reads.
	MetadataKeyPreflightReasons = "reasons"
	// MetadataKeyPreflightPerTool is the ordered per-tool detail:
	// [{id, status, reason?}] using the PreflightPerTool* keys below.
	MetadataKeyPreflightPerTool = "per_tool"
	// MetadataKeyPreflightSurface names the surface that ran the preflight when
	// it is not the REST endpoint — currently only "mcp-check", the in-band
	// describe_tool check mode (spec 099 FR-013). It is OMITTED for the REST
	// surface, whose records predate it and stay byte-identical.
	MetadataKeyPreflightSurface = "surface"

	// MetadataKeyPreflightArguments records the in-band caller's request AS
	// SENT, so the raw requested-id count stays recoverable from the record
	// even though MetadataKeyPreflightIDsCount is the UNIQUE count both
	// surfaces agree on (spec 099 FR-013). It carries the PreflightArgumentsKey*
	// members below: still ids and enum-valued filter names, never descriptions
	// or upstream arguments. OMITTED for the REST surface, whose records
	// predate it and stay byte-identical.
	MetadataKeyPreflightArguments = "arguments"

	// Keys inside MetadataKeyPreflightArguments.
	//
	// PreflightArgumentsKeyToolIDs is the raw tool_ids array: request order,
	// untrimmed, duplicates intact — len() is the raw requested count.
	PreflightArgumentsKeyToolIDs = "tool_ids"
	// PreflightArgumentsKeyFilters lists the annotation filters that were in
	// effect, in the order describe_tool declares them. Absent when none were.
	PreflightArgumentsKeyFilters = "filters"

	// PreflightSurfaceMCPCheck marks a record written by describe_tool check
	// mode. It matches the `surface` value the spec-099 sabotage-matrix rows
	// carry, so a matrix row and an activity record name the surface the same
	// way.
	PreflightSurfaceMCPCheck = "mcp-check"

	// Keys inside one MetadataKeyPreflightPerTool entry.
	PreflightPerToolKeyID     = "id"
	PreflightPerToolKeyStatus = "status"
	PreflightPerToolKeyReason = "reason"
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
	// ActivitySourceInternal indicates the activity was triggered by an internal subsystem (Spec 039)
	ActivitySourceInternal ActivitySource = "internal"
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
	ResponseTruncated bool                   `json:"response_truncated,omitempty"` // Spec 103: RECORDED > DELIVERED — see below
	Status            string                 `json:"status"`                       // Result status: "success", "error", "blocked", "rejected"
	ErrorMessage      string                 `json:"error_message,omitempty"`      // Error details if status is "error"
	DurationMs        int64                  `json:"duration_ms,omitempty"`        // Execution duration in milliseconds
	Timestamp         time.Time              `json:"timestamp"`                    // When activity occurred
	SessionID         string                 `json:"session_id,omitempty"`         // MCP transport session ID (regenerated on every reconnect)
	RequestID         string                 `json:"request_id,omitempty"`         // HTTP request ID for correlation
	Metadata          map[string]interface{} `json:"metadata,omitempty"`           // Additional context-specific data

	// ParentID is the correlation id of the record that CAUSED this one: today
	// the code_execution call whose sandbox issued this sub-call. It equals the
	// parent record's RequestID, so the two directions are one query each:
	//   parent → children:  /api/v1/activity?parent_id=<parent request_id>
	//   child  → parent:    /api/v1/activity?request_id=<child parent_id>
	//
	// First-class rather than metadata for the same reason as WorkSessionID:
	// ActivityFilter.Matches compares struct fields, so a value tucked into
	// Metadata would be stored but not filterable. Empty for every top-level
	// call and for every record written before this field existed.
	ParentID string `json:"parent_id,omitempty"`

	// The two truncation flags point in OPPOSITE directions and are
	// independent — both can be true on one record.
	//
	// ResponseTruncated (above, Spec 103) means the RECORDED response is LARGER
	// than the one the agent received: retrieve_tools stores its full
	// pre-truncation text while the agent consumed the cut version, and a
	// direct call_tool_* dispatch records the upstream result while the agent
	// received it cut to ToolResponseLimit. Consumers act on it by REFUSING to
	// count the record: internal/runtime/usage_aggregate.go excludes its
	// ResponseBytes from delivered traffic, and the token benchmark withholds
	// its response cost rather than tokenizing text nobody paid for.
	//
	// ResponseStorageTruncated means the RECORDED response is SMALLER than the
	// one the agent received: activity_max_response_size cut the text on the
	// way into BBolt so a single multi-megabyte payload cannot outweigh the
	// whole log (issue #1173). ResponseBytes is measured PRE-truncation by the
	// emitter and is therefore still honest, so this flag must never reach the
	// Spec 103 consumers — OR-ing the two would make them discard exactly the
	// oversized records the cap exists to bound, under an inverted
	// justification. What it does mean is that record.Response is no longer the
	// whole text: anything that MEASURES the stored body (as opposed to its
	// recorded byte length) must not treat it as complete.
	ResponseStorageTruncated bool `json:"response_storage_truncated,omitempty"`

	// WorkSessionID groups records into one unit of USER WORK (Spec 082): one
	// client, in one project, under one principal, across reconnects. Unlike
	// SessionID it survives the client re-initializing, which real clients do
	// every few minutes.
	//
	// First-class rather than metadata on purpose: activity filtering compares
	// struct fields (see ActivityFilter.Matches), so a value tucked into
	// Metadata would be stored but not filterable.
	//
	// Empty on records written before Spec 082 — those stay viewable, just
	// unattributed.
	WorkSessionID string `json:"work_session_id,omitempty"`

	// Byte sizes measured pre-truncation (Spec 069 A1). Zero means unknown (legacy records).
	RequestBytes  int `json:"request_bytes,omitempty"`  // JSON-serialized request arguments size in bytes
	ResponseBytes int `json:"response_bytes,omitempty"` // Raw upstream response size in bytes before truncation

	// Multi-user identity fields (server edition). Empty/omitted for personal edition.
	UserID    string `json:"user_id,omitempty"`    // User's unique identifier (set when server auth is active)
	UserEmail string `json:"user_email,omitempty"` // User's email address (set when server auth is active)
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
	SessionID  string    // Filter by MCP transport session
	Status     string    // Filter by status (success/error/blocked/rejected)
	StartTime  time.Time // Activities after this time
	EndTime    time.Time // Activities before this time
	Limit      int       // Max records to return (default 50, max 100)
	Offset     int       // Pagination offset
	IntentType string    // Filter by intent operation type: read, write, destructive (Spec 018)
	RequestID  string    // Filter by HTTP request ID for correlation (Spec 021)

	// ParentID selects the CHILDREN of one parent call (exact match on
	// ActivityRecord.ParentID) — the sub-calls a code_execution script issued.
	ParentID string

	// WorkSessionID filters by a unit of user work (Spec 082) — one client, one
	// project, across reconnects. This is what the UI's "Session" filter means;
	// SessionID above is the raw transport connection.
	WorkSessionID string

	// Sensitive data detection filters (Spec 026)
	SensitiveData *bool  // Filter by sensitive data detection (nil=no filter, true=has detections, false=no detections)
	DetectionType string // Filter by specific detection type (e.g., "aws_access_key", "credit_card")
	Severity      string // Filter by severity level (critical, high, medium, low)

	// Agent token identity filters (Spec 028)
	AgentName string // Filter by agent token name in metadata
	AuthType  string // Filter by auth type: "admin" or "agent"

	// ExcludeCallToolSuccess filters out call_tool_* internal tool calls, which
	// are always paired with a canonical record carrying the same request_id:
	// successful and failed ones with the upstream tool_call record
	// (mcp.go emits both on every dispatched outcome), rejected ones with the
	// concurrency limiter's shed record (spec 093). Pre-dispatch failures
	// (arg validation, server not found) emit ONLY a tool_call record, so no
	// call_tool_* row is ever the sole witness of a call.
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

// Validate validates and normalizes the filter for regular list queries.
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

// ValidateForExport validates and normalizes the filter for export queries.
// Export allows larger limits than regular list queries.
// Default: 10000, Max: 50000.
func (f *ActivityFilter) ValidateForExport() {
	if f.Limit <= 0 {
		f.Limit = 10000
	}
	if f.Limit > 50000 {
		f.Limit = 50000
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

	// Check work-session filter (Spec 082)
	if f.WorkSessionID != "" && record.WorkSessionID != f.WorkSessionID {
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

	// Check parent_id filter: the sub-calls one code_execution issued.
	if f.ParentID != "" && record.ParentID != f.ParentID {
		return false
	}

	// Exclude call_tool_* internal tool calls that are already represented by a
	// canonical tool_call entry, so one dispatch is never counted twice.
	//
	// A SUCCESSFUL or FAILED call_tool_* has the upstream's own tool_call
	// record (mcp.go emits both on every dispatched outcome, sharing one
	// request_id); a REJECTED one has the record the concurrency limiter wrote
	// at the shed (spec 093 FR-012), which every origin produces — the variant
	// handler merely adds a second, MCP-flavoured row on top of it. So every
	// call_tool_* row duplicates a canonical record and is hidden by default.
	if f.ExcludeCallToolSuccess {
		if record.Type == ActivityTypeInternalToolCall &&
			strings.HasPrefix(record.ToolName, InternalCallToolPrefix) {
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

	// Check agent identity filters (Spec 028)
	if f.AgentName != "" {
		recordAgentName := extractAuthMetadataField(record, "_auth_agent_name")
		if recordAgentName != f.AgentName {
			return false
		}
	}
	if f.AuthType != "" {
		recordAuthType := extractAuthMetadataField(record, "_auth_auth_type")
		if recordAuthType != f.AuthType {
			return false
		}
	}

	return true
}

// extractAuthMetadataField extracts an auth metadata field from the activity record's arguments.
// Auth metadata fields are stored with "_auth_" prefix in the Arguments map (Spec 028).
func extractAuthMetadataField(record *ActivityRecord, key string) string {
	if record.Arguments == nil {
		return ""
	}
	if val, ok := record.Arguments[key]; ok {
		if s, ok := val.(string); ok {
			return s
		}
	}
	return ""
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
