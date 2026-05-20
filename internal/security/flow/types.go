// Package flow implements data flow security for detecting exfiltration patterns.
// It tracks data movement across tool calls and enforces policies to prevent
// the "lethal trifecta" — agents with access to private data, untrusted content,
// and external communication channels.
package flow

import (
	"sync"
	"time"
)

// --- Enumerations ---

// Classification represents the data flow role of a server or tool.
type Classification string

const (
	ClassInternal Classification = "internal" // Data sources, private systems
	ClassExternal Classification = "external" // Communication channels, public APIs
	ClassHybrid   Classification = "hybrid"   // Can be either (e.g., Bash)
	ClassUnknown  Classification = "unknown"  // Unclassified
)

// FlowType represents the direction of data movement.
type FlowType string

const (
	FlowInternalToInternal FlowType = "internal_to_internal" // Safe
	FlowExternalToExternal FlowType = "external_to_external" // Safe
	FlowExternalToInternal FlowType = "external_to_internal" // Safe (ingestion)
	FlowInternalToExternal FlowType = "internal_to_external" // CRITICAL (exfiltration)
)

// RiskLevel represents the assessed risk of a data flow.
type RiskLevel string

const (
	RiskNone     RiskLevel = "none"     // Safe flow types
	RiskLow      RiskLevel = "low"      // Log only
	RiskMedium   RiskLevel = "medium"   // internal→external, no sensitive data
	RiskHigh     RiskLevel = "high"     // internal→external, no justification
	RiskCritical RiskLevel = "critical" // internal→external with sensitive data
)

// PolicyAction represents a policy enforcement decision.
type PolicyAction string

const (
	PolicyAllow PolicyAction = "allow" // Allow, log only
	PolicyWarn  PolicyAction = "warn"  // Allow, log warning
	PolicyAsk   PolicyAction = "ask"   // Return "ask" (user confirmation)
	PolicyDeny  PolicyAction = "deny"  // Block the call
)

// CoverageMode represents the current security coverage level.
type CoverageMode string

const (
	CoverageModeProxyOnly CoverageMode = "proxy_only" // MCP proxy tracking only
	CoverageModeFull      CoverageMode = "full"        // Proxy + hook-enhanced tracking
)

// --- Core Types ---

// FlowSession tracks all data origins and flow edges within a single agent session.
type FlowSession struct {
	ID                string                 // Hook session ID or MCP session ID
	StartTime         time.Time              // When the session started
	LastActivity      time.Time              // Last tool call timestamp
	LinkedMCPSessions []string               // Correlated MCP session IDs
	Origins           map[string]*DataOrigin // Content hash → origin info
	Flows             []*FlowEdge           // Detected data movements (append-only)
	ToolsUsed         map[string]bool        // Unique tools observed
	mu                sync.RWMutex           // Per-session lock
}

// DataOrigin records where data was produced — which tool call generated it.
type DataOrigin struct {
	ContentHash      string         // SHA256 truncated to 128 bits (32 hex chars)
	ToolCallID       string         // Unique ID for the originating tool call (optional)
	ToolName         string         // Tool that produced this data (e.g., "Read", "github:get_file")
	ServerName       string         // MCP server name (empty for internal tools)
	Classification   Classification // internal/external/hybrid/unknown
	HasSensitiveData bool           // Whether sensitive data was detected (from Spec 026)
	SensitiveTypes   []string       // Types of sensitive data (e.g., ["api_token", "private_key"])
	Timestamp        time.Time      // When the data was produced
}

// FlowEdge represents a detected data movement between tools.
type FlowEdge struct {
	ID               string         // ULID format unique edge identifier
	FromOrigin       *DataOrigin    // Source of the data
	ToToolCallID     string         // Destination tool call ID (optional)
	ToToolName       string         // Destination tool name
	ToServerName     string         // Destination MCP server (empty for internal)
	ToClassification Classification // Classification of destination
	FlowType         FlowType       // Direction classification
	RiskLevel        RiskLevel      // Assessed risk
	ContentHash      string         // Hash of the matching content (32 hex chars)
	Timestamp        time.Time      // When the flow was detected
}

// ClassificationResult is the outcome of classifying a server or tool.
type ClassificationResult struct {
	Classification Classification // internal/external/hybrid/unknown
	Confidence     float64        // 0.0 to 1.0
	Method         string         // "heuristic", "config", "builtin", or "annotation"
	Reason         string         // Human-readable explanation
	CanExfiltrate  bool           // Whether this tool can send data externally
	CanReadData    bool           // Whether this tool can access private data
}

// PendingCorrelation is a temporary entry for linking hook sessions to MCP sessions.
type PendingCorrelation struct {
	HookSessionID string        // Agent hook session ID
	ArgsHash      string        // SHA256 of tool name + arguments (32 hex chars)
	ToolName      string        // Inner tool name (e.g., "github:get_file")
	Timestamp     time.Time     // When the pending entry was created
	TTL           time.Duration // Time-to-live before expiry (default: 5s)
}

// FlowSummary contains aggregate statistics for a completed flow session.
// Written to the activity log when a session expires.
type FlowSummary struct {
	SessionID            string         `json:"session_id"`
	CoverageMode         string         `json:"coverage_mode"` // "proxy_only" or "full"
	DurationMinutes      int            `json:"duration_minutes"`
	TotalOrigins         int            `json:"total_origins"`
	TotalFlows           int            `json:"total_flows"`
	FlowTypeDistribution map[string]int `json:"flow_type_distribution"`
	RiskLevelDistribution map[string]int `json:"risk_level_distribution"`
	LinkedMCPSessions    []string       `json:"linked_mcp_sessions,omitempty"`
	ToolsUsed            []string       `json:"tools_used"`
	HasSensitiveFlows    bool           `json:"has_sensitive_flows"`
}

// --- API Types ---

// HookEvaluateRequest is the HTTP request body for POST /api/v1/hooks/evaluate.
type HookEvaluateRequest struct {
	Event        string         `json:"event"`                    // "PreToolUse" or "PostToolUse"
	SessionID    string         `json:"session_id"`               // Agent session identifier
	ToolName     string         `json:"tool_name"`                // Tool being called
	ToolInput    map[string]any `json:"tool_input"`               // Tool input arguments
	ToolResponse string         `json:"tool_response,omitempty"`  // Response (PostToolUse only)
}

// HookEvaluateResponse is the HTTP response body for POST /api/v1/hooks/evaluate.
type HookEvaluateResponse struct {
	Decision   PolicyAction `json:"decision"`              // allow/warn/ask/deny
	Reason     string       `json:"reason,omitempty"`      // Explanation
	RiskLevel  RiskLevel    `json:"risk_level,omitempty"`  // Assessed risk
	FlowType   FlowType     `json:"flow_type,omitempty"`   // Detected flow direction
	ActivityID string       `json:"activity_id,omitempty"` // Activity log record ID
}

// --- Severity Ordering ---

// policyActionSeverity returns a numeric severity for ordering policy actions.
// Higher value = more severe action.
func policyActionSeverity(a PolicyAction) int {
	switch a {
	case PolicyAllow:
		return 0
	case PolicyWarn:
		return 1
	case PolicyAsk:
		return 2
	case PolicyDeny:
		return 3
	default:
		return 0
	}
}
