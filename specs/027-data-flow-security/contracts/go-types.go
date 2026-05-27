// Package flow contains type definitions for data flow security.
// This is a contract file — it defines the public API surface.
// Implementation may vary from these exact definitions.
package flow

import (
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
	FlowInternalToInternal FlowType = "internal→internal" // Safe
	FlowExternalToExternal FlowType = "external→external" // Safe
	FlowExternalToInternal FlowType = "external→internal" // Safe (ingestion)
	FlowInternalToExternal FlowType = "internal→external" // CRITICAL (exfiltration)
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

// --- Core Types ---

// FlowSession tracks all data origins and flow edges within a single agent session.
type FlowSession struct {
	ID               string                   // Hook session ID from agent
	StartTime        time.Time                // When the session started
	LastActivity     time.Time                // Last tool call timestamp
	LinkedMCPSessions []string                // Correlated MCP session IDs
	Origins          map[string]*DataOrigin   // Content hash → origin info
	Flows            []*FlowEdge             // Detected data movements (append-only)
}

// DataOrigin records where data was produced — which tool call generated it.
type DataOrigin struct {
	ContentHash    string         // SHA256 truncated to 128 bits (32 hex chars)
	ToolCallID     string         // Unique ID for the originating tool call (optional)
	ToolName       string         // Tool that produced this data (e.g., "Read", "github:get_file")
	ServerName     string         // MCP server name (empty for internal tools)
	Classification Classification // internal/external/hybrid/unknown
	HasSensitiveData bool         // Whether sensitive data was detected (from Spec 026)
	SensitiveTypes []string       // Types of sensitive data (e.g., ["api_token", "private_key"])
	Timestamp      time.Time      // When the data was produced
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
	Method         string         // "heuristic", "config", or "annotation"
	Reason         string         // Human-readable explanation
	CanExfiltrate  bool           // Whether this tool can send data externally
	CanReadData    bool           // Whether this tool can access private data
}

// PendingCorrelation is a temporary entry for linking hook sessions to MCP sessions.
type PendingCorrelation struct {
	HookSessionID string        // Claude Code hook session ID
	ArgsHash      string        // SHA256 of tool name + arguments (32 hex chars)
	ToolName      string        // Inner tool name (e.g., "github:get_file")
	Timestamp     time.Time     // When the pending entry was created
	TTL           time.Duration // Time-to-live before expiry (default: 5s)
}

// --- API Types ---

// HookEvaluateRequest is the HTTP request body for POST /api/v1/hooks/evaluate.
type HookEvaluateRequest struct {
	Event        string                 `json:"event"`         // "PreToolUse" or "PostToolUse"
	SessionID    string                 `json:"session_id"`    // Agent session identifier
	ToolName     string                 `json:"tool_name"`     // Tool being called
	ToolInput    map[string]interface{} `json:"tool_input"`    // Tool input arguments
	ToolResponse string                 `json:"tool_response"` // Response (PostToolUse only)
}

// HookEvaluateResponse is the HTTP response body for POST /api/v1/hooks/evaluate.
type HookEvaluateResponse struct {
	Decision   PolicyAction `json:"decision"`              // allow/deny/ask
	Reason     string       `json:"reason,omitempty"`      // Explanation
	RiskLevel  RiskLevel    `json:"risk_level,omitempty"`  // Assessed risk
	FlowType   FlowType     `json:"flow_type,omitempty"`   // Detected flow direction
	ActivityID string       `json:"activity_id,omitempty"` // Activity log record ID
}

// --- Configuration Types ---

// FlowTrackingConfig configures the flow tracking subsystem.
type FlowTrackingConfig struct {
	Enabled              bool `json:"enabled"`
	SessionTimeoutMin    int  `json:"session_timeout_minutes"`
	MaxOriginsPerSession int  `json:"max_origins_per_session"`
	HashMinLength        int  `json:"hash_min_length"`
	MaxResponseHashBytes int  `json:"max_response_hash_bytes"`
}

// ClassificationConfig configures server/tool classification.
type ClassificationConfig struct {
	DefaultUnknown  string            `json:"default_unknown"`   // "internal" or "external"
	ServerOverrides map[string]string `json:"server_overrides"`  // server name → classification
}

// FlowPolicyConfig configures policy enforcement.
type FlowPolicyConfig struct {
	InternalToExternal    PolicyAction      `json:"internal_to_external"`
	SensitiveDataExternal PolicyAction      `json:"sensitive_data_external"`
	RequireJustification  bool              `json:"require_justification"`
	SuspiciousEndpoints   []string          `json:"suspicious_endpoints"`
	ToolOverrides         map[string]string `json:"tool_overrides"` // tool name → action
}

// HooksConfig configures agent hook integration.
type HooksConfig struct {
	Enabled            bool `json:"enabled"`
	FailOpen           bool `json:"fail_open"`
	CorrelationTTLSecs int  `json:"correlation_ttl_seconds"`
}

// --- Classifier Interface ---

// ServerClassifier classifies servers and tools.
type ServerClassifier interface {
	// Classify returns the classification for a server or tool name.
	Classify(serverName, toolName string) ClassificationResult
}

// --- Flow Tracker Interface ---

// FlowTracker tracks data origins and detects cross-boundary flows.
type FlowTracker interface {
	// RecordOrigin stores data origin from a PostToolUse event.
	RecordOrigin(sessionID string, origin *DataOrigin)

	// CheckFlow evaluates a PreToolUse event for data flow matches.
	CheckFlow(sessionID string, toolName, serverName string, argsJSON string) ([]*FlowEdge, error)

	// GetSession returns the flow session for a given session ID.
	GetSession(sessionID string) *FlowSession

	// LinkMCPSession links an MCP session to a hook flow session.
	LinkMCPSession(hookSessionID, mcpSessionID string)
}

// --- Policy Evaluator Interface ---

// PolicyEvaluator evaluates flow edges against configured policy.
type PolicyEvaluator interface {
	// Evaluate returns the policy decision for a set of flow edges.
	// mode is "proxy_only" or "hook_enhanced" — PolicyAsk degrades to PolicyWarn in proxy_only mode.
	Evaluate(edges []*FlowEdge, mode string) (PolicyAction, string)
}
