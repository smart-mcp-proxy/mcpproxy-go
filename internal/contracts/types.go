// Package contracts defines typed data transfer objects for API communication
package contracts

import (
	"time"
)

// APIResponse is the standard wrapper for all API responses
type APIResponse struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
}

// Server represents an upstream MCP server configuration and status
type Server struct {
	ID              string            `json:"id"`
	Name            string            `json:"name"`
	URL             string            `json:"url,omitempty"`
	Protocol        string            `json:"protocol"`
	Command         string            `json:"command,omitempty"`
	Args            []string          `json:"args,omitempty"`
	WorkingDir      string            `json:"working_dir,omitempty"`
	Env             map[string]string `json:"env,omitempty"`
	Headers         map[string]string `json:"headers,omitempty"`
	OAuth           *OAuthConfig      `json:"oauth,omitempty"`
	Enabled         bool              `json:"enabled"`
	Quarantined     bool              `json:"quarantined"`
	Connected       bool              `json:"connected"`
	Status          string            `json:"status"`
	LastError       string            `json:"last_error,omitempty"`
	ConnectedAt     *time.Time        `json:"connected_at,omitempty"`
	LastReconnectAt *time.Time        `json:"last_reconnect_at,omitempty"`
	ReconnectCount  int               `json:"reconnect_count"`
	ToolCount       int               `json:"tool_count"`
	Created         time.Time         `json:"created"`
	Updated         time.Time         `json:"updated"`
	Isolation       *IsolationConfig  `json:"isolation,omitempty"`
	Authenticated   bool              `json:"authenticated"`       // OAuth authentication status
}

// OAuthConfig represents OAuth configuration for a server
type OAuthConfig struct {
	AuthURL      string            `json:"auth_url"`
	TokenURL     string            `json:"token_url"`
	ClientID     string            `json:"client_id"`
	Scopes       []string          `json:"scopes,omitempty"`
	ExtraParams  map[string]string `json:"extra_params,omitempty"`
	RedirectPort int               `json:"redirect_port,omitempty"`
}

// IsolationConfig represents Docker isolation configuration
type IsolationConfig struct {
	Enabled     bool   `json:"enabled"`
	Image       string `json:"image,omitempty"`
	MemoryLimit string `json:"memory_limit,omitempty"`
	CPULimit    string `json:"cpu_limit,omitempty"`
	WorkingDir  string `json:"working_dir,omitempty"`
	Timeout     string `json:"timeout,omitempty"`
}

// Tool represents an MCP tool with its metadata
type Tool struct {
	Name        string                 `json:"name"`
	ServerName  string                 `json:"server_name"`
	Description string                 `json:"description"`
	Schema      map[string]interface{} `json:"schema,omitempty"`
	Usage       int                    `json:"usage"`
	LastUsed    *time.Time             `json:"last_used,omitempty"`
}

// SearchResult represents a search result for tools
type SearchResult struct {
	Tool    Tool    `json:"tool"`
	Score   float64 `json:"score"`
	Snippet string  `json:"snippet,omitempty"`
	Matches int     `json:"matches"`
}

// ServerStats represents aggregated statistics about servers
type ServerStats struct {
	TotalServers       int `json:"total_servers"`
	ConnectedServers   int `json:"connected_servers"`
	QuarantinedServers int `json:"quarantined_servers"`
	TotalTools         int `json:"total_tools"`
	DockerContainers   int `json:"docker_containers"`
}

// LogEntry represents a single log entry
type LogEntry struct {
	Timestamp time.Time              `json:"timestamp"`
	Level     string                 `json:"level"`
	Message   string                 `json:"message"`
	Server    string                 `json:"server,omitempty"`
	Fields    map[string]interface{} `json:"fields,omitempty"`
}

// SystemStatus represents the overall system status
type SystemStatus struct {
	Phase      string        `json:"phase"`
	Message    string        `json:"message"`
	Uptime     time.Duration `json:"uptime"`
	StartedAt  time.Time     `json:"started_at"`
	ConfigPath string        `json:"config_path"`
	LogDir     string        `json:"log_dir"`
	Runtime    RuntimeStatus `json:"runtime"`
	Servers    ServerStats   `json:"servers"`
}

// RuntimeStatus represents runtime-specific status information
type RuntimeStatus struct {
	Version        string    `json:"version"`
	GoVersion      string    `json:"go_version"`
	BuildTime      string    `json:"build_time,omitempty"`
	IndexStatus    string    `json:"index_status"`
	StorageStatus  string    `json:"storage_status"`
	LastConfigLoad time.Time `json:"last_config_load"`
}

// ToolCallRequest represents a request to call a tool
type ToolCallRequest struct {
	ToolName string                 `json:"tool_name"`
	Args     map[string]interface{} `json:"args"`
}

// ToolCallResponse represents the response from a tool call
type ToolCallResponse struct {
	ToolName   string      `json:"tool_name"`
	ServerName string      `json:"server_name"`
	Result     interface{} `json:"result"`
	Error      string      `json:"error,omitempty"`
	Duration   string      `json:"duration"`
	Timestamp  time.Time   `json:"timestamp"`
}

// Event represents a system event for SSE streaming
type Event struct {
	Type      string                 `json:"type"`
	Data      interface{}            `json:"data"`
	Server    string                 `json:"server,omitempty"`
	Timestamp time.Time              `json:"timestamp"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
}

// API Request/Response DTOs

// GetServersResponse is the response for GET /api/v1/servers
type GetServersResponse struct {
	Servers []Server    `json:"servers"`
	Stats   ServerStats `json:"stats"`
}

// GetServerToolsResponse is the response for GET /api/v1/servers/{id}/tools
type GetServerToolsResponse struct {
	ServerName string `json:"server_name"`
	Tools      []Tool `json:"tools"`
	Count      int    `json:"count"`
}

// SearchToolsResponse is the response for GET /api/v1/index/search
type SearchToolsResponse struct {
	Query   string         `json:"query"`
	Results []SearchResult `json:"results"`
	Total   int            `json:"total"`
	Took    string         `json:"took"`
}

// GetServerLogsResponse is the response for GET /api/v1/servers/{id}/logs
type GetServerLogsResponse struct {
	ServerName string     `json:"server_name"`
	Logs       []LogEntry `json:"logs"`
	Count      int        `json:"count"`
}

// ServerActionResponse is the response for server enable/disable/restart actions
type ServerActionResponse struct {
	Server  string `json:"server"`
	Action  string `json:"action"`
	Success bool   `json:"success"`
	Async   bool   `json:"async,omitempty"`
}

// QuarantinedServersResponse is the response for quarantined servers
type QuarantinedServersResponse struct {
	Servers []Server `json:"servers"`
	Count   int      `json:"count"`
}

// Secret management DTOs

// Ref represents a reference to a secret value
type Ref struct {
	Type     string `json:"type"`     // "env", "keyring", etc.
	Name     string `json:"name"`     // The secret name/key
	Original string `json:"original"` // Original reference string like "${env:API_KEY}"
}

// MigrationCandidate represents a potential secret that could be migrated to secure storage
type MigrationCandidate struct {
	Field      string  `json:"field"`      // Field path in configuration
	Value      string  `json:"value"`      // Masked value for display
	Suggested  string  `json:"suggested"`  // Suggested secret reference
	Confidence float64 `json:"confidence"` // Confidence score (0.0 to 1.0)
}

// MigrationAnalysis represents the result of analyzing configuration for potential secrets
type MigrationAnalysis struct {
	Candidates []MigrationCandidate `json:"candidates"`
	TotalFound int                  `json:"total_found"`
}

// GetRefsResponse is the response for GET /api/v1/secrets/refs
type GetRefsResponse struct {
	Refs []Ref `json:"refs"`
}

// GetMigrationAnalysisResponse is the response for POST /api/v1/secrets/migrate
type GetMigrationAnalysisResponse struct {
	Analysis MigrationAnalysis `json:"analysis"`
}

// Diagnostics types

// DiagnosticIssue represents a single diagnostic issue
type DiagnosticIssue struct {
	Type        string                 `json:"type"`         // error, warning, info
	Category    string                 `json:"category"`     // oauth, connection, secrets, config
	Server      string                 `json:"server,omitempty"` // Associated server (if any)
	Title       string                 `json:"title"`        // Short title
	Message     string                 `json:"message"`      // Detailed message
	Timestamp   time.Time              `json:"timestamp"`    // When detected
	Severity    string                 `json:"severity"`     // critical, high, medium, low
	Metadata    map[string]interface{} `json:"metadata,omitempty"` // Additional context
}

// MissingSecret represents an unresolved secret reference
type MissingSecret struct {
	Name      string `json:"name"`      // Variable/secret name
	Reference string `json:"reference"` // Original reference (e.g., "${env:API_KEY}")
	Server    string `json:"server"`    // Which server needs it
	Type      string `json:"type"`      // env, keyring, etc.
}

// DiagnosticsResponse represents the aggregated diagnostics
type DiagnosticsResponse struct {
	UpstreamErrors   []DiagnosticIssue `json:"upstream_errors"`
	OAuthRequired    []string          `json:"oauth_required"`    // Server names
	MissingSecrets   []MissingSecret   `json:"missing_secrets"`
	RuntimeWarnings  []DiagnosticIssue `json:"runtime_warnings"`
	TotalIssues      int               `json:"total_issues"`
	LastUpdated      time.Time         `json:"last_updated"`
}

// Tool Call History types

// ToolCallRecord represents a single recorded tool call with full context
type ToolCallRecord struct {
	ID         string                 `json:"id"`          // Unique identifier
	ServerID   string                 `json:"server_id"`   // Server identity hash
	ServerName string                 `json:"server_name"` // Human-readable server name
	ToolName   string                 `json:"tool_name"`   // Tool name (without server prefix)
	Arguments  map[string]interface{} `json:"arguments"`   // Tool arguments
	Response   interface{}            `json:"response,omitempty"` // Tool response (success only)
	Error      string                 `json:"error,omitempty"`    // Error message (failure only)
	Duration   int64                  `json:"duration"`    // Duration in nanoseconds
	Timestamp  time.Time              `json:"timestamp"`   // When the call was made
	ConfigPath string                 `json:"config_path"` // Active config file path
	RequestID  string                 `json:"request_id,omitempty"` // Request correlation ID
}

// GetToolCallsResponse is the response for GET /api/v1/tool-calls
type GetToolCallsResponse struct {
	ToolCalls []ToolCallRecord `json:"tool_calls"`
	Total     int              `json:"total"`
	Limit     int              `json:"limit"`
	Offset    int              `json:"offset"`
}

// GetToolCallDetailResponse is the response for GET /api/v1/tool-calls/{id}
type GetToolCallDetailResponse struct {
	ToolCall ToolCallRecord `json:"tool_call"`
}

// GetServerToolCallsResponse is the response for GET /api/v1/servers/{name}/tool-calls
type GetServerToolCallsResponse struct {
	ServerName string           `json:"server_name"`
	ToolCalls  []ToolCallRecord `json:"tool_calls"`
	Total      int              `json:"total"`
}
