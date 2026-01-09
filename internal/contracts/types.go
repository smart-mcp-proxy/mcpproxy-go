// Package contracts defines typed data transfer objects for API communication
package contracts

import (
	"time"
)

// APIResponse is the standard wrapper for all API responses
type APIResponse struct {
	Success   bool        `json:"success"`
	Data      interface{} `json:"data,omitempty" swaggertype:"object"`
	Error     string      `json:"error,omitempty"`
	RequestID string      `json:"request_id,omitempty"`
}

// Server represents an upstream MCP server configuration and status
type Server struct {
	ID                string            `json:"id"`
	Name              string            `json:"name"`
	URL               string            `json:"url,omitempty"`
	Protocol          string            `json:"protocol"`
	Command           string            `json:"command,omitempty"`
	Args              []string          `json:"args,omitempty"`
	WorkingDir        string            `json:"working_dir,omitempty"`
	Env               map[string]string `json:"env,omitempty"`
	Headers           map[string]string `json:"headers,omitempty"`
	OAuth             *OAuthConfig      `json:"oauth,omitempty"`
	Enabled           bool              `json:"enabled"`
	Quarantined       bool              `json:"quarantined"`
	Connected         bool              `json:"connected"`
	Connecting        bool              `json:"connecting"`
	Status            string            `json:"status"`
	LastError         string            `json:"last_error,omitempty"`
	ConnectedAt       *time.Time        `json:"connected_at,omitempty"`
	LastReconnectAt   *time.Time        `json:"last_reconnect_at,omitempty"`
	ReconnectCount    int               `json:"reconnect_count"`
	ToolCount         int               `json:"tool_count"`
	Created           time.Time         `json:"created"`
	Updated           time.Time         `json:"updated"`
	Isolation         *IsolationConfig  `json:"isolation,omitempty"`
	Authenticated     bool              `json:"authenticated"`                  // OAuth authentication status
	OAuthStatus       string            `json:"oauth_status,omitempty"`         // OAuth status: "authenticated", "expired", "error", "none"
	TokenExpiresAt    *time.Time        `json:"token_expires_at,omitempty"`     // When the OAuth token expires (ISO 8601)
	ToolListTokenSize int               `json:"tool_list_token_size,omitempty"` // Token size for this server's tools
	ShouldRetry       bool              `json:"should_retry,omitempty"`
	RetryCount        int               `json:"retry_count,omitempty"`
	LastRetryTime     *time.Time        `json:"last_retry_time,omitempty"`
	UserLoggedOut     bool              `json:"user_logged_out,omitempty"` // True if user explicitly logged out (prevents auto-reconnection)
	Health            *HealthStatus     `json:"health,omitempty"`          // Unified health status calculated by the backend
}

// OAuthConfig represents OAuth configuration for a server
type OAuthConfig struct {
	AuthURL        string            `json:"auth_url"`
	TokenURL       string            `json:"token_url"`
	ClientID       string            `json:"client_id"`
	Scopes         []string          `json:"scopes,omitempty"`
	ExtraParams    map[string]string `json:"extra_params,omitempty"`
	RedirectPort   int               `json:"redirect_port,omitempty"`
	PKCEEnabled    bool              `json:"pkce_enabled,omitempty"`
	TokenExpiresAt *time.Time        `json:"token_expires_at,omitempty"` // When the OAuth token expires
	TokenValid     bool              `json:"token_valid,omitempty"`      // Whether token is currently valid
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

// ToolAnnotation represents MCP tool behavior hints
type ToolAnnotation struct {
	Title           string `json:"title,omitempty"`
	ReadOnlyHint    *bool  `json:"readOnlyHint,omitempty"`
	DestructiveHint *bool  `json:"destructiveHint,omitempty"`
	IdempotentHint  *bool  `json:"idempotentHint,omitempty"`
	OpenWorldHint   *bool  `json:"openWorldHint,omitempty"`
}

// MCPSession represents a client session with MCPProxy
type MCPSession struct {
	ID            string     `json:"id"`
	ClientName    string     `json:"client_name,omitempty"`
	ClientVersion string     `json:"client_version,omitempty"`
	Status        string     `json:"status"`
	StartTime     time.Time  `json:"start_time"`
	EndTime       *time.Time `json:"end_time,omitempty"`
	LastActivity  time.Time  `json:"last_activity"`
	ToolCallCount int        `json:"tool_call_count"`
	TotalTokens   int        `json:"total_tokens"`
	// MCP Client Capabilities
	HasRoots     bool     `json:"has_roots,omitempty"`
	HasSampling  bool     `json:"has_sampling,omitempty"`
	Experimental []string `json:"experimental,omitempty"`
}

// Tool represents an MCP tool with its metadata
type Tool struct {
	Name        string                 `json:"name"`
	ServerName  string                 `json:"server_name"`
	Description string                 `json:"description"`
	Schema      map[string]interface{} `json:"schema,omitempty" swaggertype:"object"`
	Usage       int                    `json:"usage"`
	LastUsed    *time.Time             `json:"last_used,omitempty"`
	Annotations *ToolAnnotation        `json:"annotations,omitempty"`
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
	TotalServers       int                 `json:"total_servers"`
	ConnectedServers   int                 `json:"connected_servers"`
	QuarantinedServers int                 `json:"quarantined_servers"`
	TotalTools         int                 `json:"total_tools"`
	DockerContainers   int                 `json:"docker_containers"`
	TokenMetrics       *ServerTokenMetrics `json:"token_metrics,omitempty"`
}

// ServerTokenMetrics represents token usage and savings metrics
type ServerTokenMetrics struct {
	TotalServerToolListSize int            `json:"total_server_tool_list_size"` // All upstream tools combined (tokens)
	AverageQueryResultSize  int            `json:"average_query_result_size"`   // Typical retrieve_tools output (tokens)
	SavedTokens             int            `json:"saved_tokens"`                // Difference
	SavedTokensPercentage   float64        `json:"saved_tokens_percentage"`     // Percentage saved
	PerServerToolListSizes  map[string]int `json:"per_server_tool_list_sizes"`  // Token size per server
}

// LogEntry represents a single log entry
type LogEntry struct {
	Timestamp time.Time              `json:"timestamp"`
	Level     string                 `json:"level"`
	Message   string                 `json:"message"`
	Server    string                 `json:"server,omitempty"`
	Fields    map[string]interface{} `json:"fields,omitempty" swaggertype:"object"`
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
	Args     map[string]interface{} `json:"args" swaggertype:"object"`
}

// ToolCallResponse represents the response from a tool call
type ToolCallResponse struct {
	ToolName   string      `json:"tool_name"`
	ServerName string      `json:"server_name"`
	Result     interface{} `json:"result" swaggertype:"object"`
	Error      string      `json:"error,omitempty"`
	Duration   string      `json:"duration"`
	Timestamp  time.Time   `json:"timestamp"`
}

// Event represents a system event for SSE streaming
type Event struct {
	Type      string                 `json:"type"`
	Data      interface{}            `json:"data" swaggertype:"object"`
	Server    string                 `json:"server,omitempty"`
	Timestamp time.Time              `json:"timestamp"`
	Metadata  map[string]interface{} `json:"metadata,omitempty" swaggertype:"object"`
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
	Type      string                 `json:"type"`                                            // error, warning, info
	Category  string                 `json:"category"`                                        // oauth, connection, secrets, config
	Server    string                 `json:"server,omitempty"`                                // Associated server (if any)
	Title     string                 `json:"title"`                                           // Short title
	Message   string                 `json:"message"`                                         // Detailed message
	Timestamp time.Time              `json:"timestamp"`                                       // When detected
	Severity  string                 `json:"severity"`                                        // critical, high, medium, low
	Metadata  map[string]interface{} `json:"metadata,omitempty" swaggertype:"object"`         // Additional context
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
	UpstreamErrors  []DiagnosticIssue `json:"upstream_errors"`
	OAuthRequired   []string          `json:"oauth_required"` // Server names
	MissingSecrets  []MissingSecret   `json:"missing_secrets"`
	RuntimeWarnings []DiagnosticIssue `json:"runtime_warnings"`
	TotalIssues     int               `json:"total_issues"`
	LastUpdated     time.Time         `json:"last_updated"`
}

// Diagnostics represents aggregated health information from all MCPProxy components.
// This is the new unified diagnostics format for the management service.
type Diagnostics struct {
	TotalIssues     int                 `json:"total_issues"`
	UpstreamErrors  []UpstreamError     `json:"upstream_errors"`
	OAuthRequired   []OAuthRequirement  `json:"oauth_required"`
	OAuthIssues     []OAuthIssue        `json:"oauth_issues"`          // OAuth parameter mismatches
	MissingSecrets  []MissingSecretInfo `json:"missing_secrets"`       // Renamed to avoid conflict
	RuntimeWarnings []string            `json:"runtime_warnings"`
	DockerStatus    *DockerStatus       `json:"docker_status,omitempty"`
	Timestamp       time.Time           `json:"timestamp"`
}

// UpstreamError represents a connection or runtime error from an upstream MCP server.
type UpstreamError struct {
	ServerName   string    `json:"server_name"`
	ErrorMessage string    `json:"error_message"`
	Timestamp    time.Time `json:"timestamp"`
}

// OAuthRequirement represents an OAuth authentication requirement for a server.
type OAuthRequirement struct {
	ServerName string     `json:"server_name"`
	State      string     `json:"state"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	Message    string     `json:"message"`
}

// OAuthIssue represents an OAuth configuration issue (e.g., missing parameters).
type OAuthIssue struct {
	ServerName       string   `json:"server_name"`
	Issue            string   `json:"issue"`
	Error            string   `json:"error"`
	MissingParams    []string `json:"missing_params,omitempty"`
	Resolution       string   `json:"resolution"`
	DocumentationURL string   `json:"documentation_url,omitempty"`
}

// MissingSecretInfo represents a secret referenced in configuration but not found.
// This is used by the new Diagnostics type to avoid field name conflicts.
type MissingSecretInfo struct {
	SecretName string   `json:"secret_name"`
	UsedBy     []string `json:"used_by"`
}

// DockerStatus represents the availability of Docker daemon for stdio server isolation.
type DockerStatus struct {
	Available bool   `json:"available"`
	Version   string `json:"version,omitempty"`
	Error     string `json:"error,omitempty"`
}

// AuthStatus represents detailed OAuth authentication status for a single server.
type AuthStatus struct {
	ServerName string     `json:"server_name"`
	State      string     `json:"state"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	TokenType  string     `json:"token_type,omitempty"`
	Scopes     []string   `json:"scopes,omitempty"`
	Message    string     `json:"message"`
}

// OAuth Authentication State Constants
const (
	AuthStateAuthenticated   = "authenticated"
	AuthStateUnauthenticated = "unauthenticated"
	AuthStateExpired         = "expired"
	AuthStateRefreshing      = "refreshing"
)

// Tool Call History types

// TokenMetrics represents token usage statistics for a tool call
type TokenMetrics struct {
	InputTokens     int     `json:"input_tokens"`               // Tokens in the request
	OutputTokens    int     `json:"output_tokens"`              // Tokens in the response
	TotalTokens     int     `json:"total_tokens"`               // Total tokens (input + output)
	Model           string  `json:"model"`                      // Model used for tokenization
	Encoding        string  `json:"encoding"`                   // Encoding used (e.g., cl100k_base)
	EstimatedCost   float64 `json:"estimated_cost,omitempty"`   // Optional cost estimate
	TruncatedTokens int     `json:"truncated_tokens,omitempty"` // Tokens removed by truncation
	WasTruncated    bool    `json:"was_truncated"`              // Whether response was truncated
}

// ToolCallRecord represents a single recorded tool call with full context
type ToolCallRecord struct {
	ID               string                 `json:"id"`                                                   // Unique identifier
	ServerID         string                 `json:"server_id"`                                            // Server identity hash
	ServerName       string                 `json:"server_name"`                                          // Human-readable server name
	ToolName         string                 `json:"tool_name"`                                            // Tool name (without server prefix)
	Arguments        map[string]interface{} `json:"arguments" swaggertype:"object"`                       // Tool arguments
	Response         interface{}            `json:"response,omitempty" swaggertype:"object"`              // Tool response (success only)
	Error            string                 `json:"error,omitempty"`                                      // Error message (failure only)
	Duration         int64                  `json:"duration"`                                             // Duration in nanoseconds
	Timestamp        time.Time              `json:"timestamp"`                                            // When the call was made
	ConfigPath       string                 `json:"config_path"`                                          // Active config file path
	RequestID        string                 `json:"request_id,omitempty"`                                 // Request correlation ID
	Metrics          *TokenMetrics          `json:"metrics,omitempty"`                                    // Token usage metrics (nil for older records)
	ParentCallID     string                 `json:"parent_call_id,omitempty"`                             // Links nested calls to parent code_execution
	ExecutionType    string                 `json:"execution_type,omitempty"`                             // "direct" or "code_execution"
	MCPSessionID     string                 `json:"mcp_session_id,omitempty"`                             // MCP session identifier
	MCPClientName    string                 `json:"mcp_client_name,omitempty"`                            // MCP client name from InitializeRequest
	MCPClientVersion string                 `json:"mcp_client_version,omitempty"`                         // MCP client version
	Annotations      *ToolAnnotation        `json:"annotations,omitempty"`                                // Tool behavior hints snapshot
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

// GetSessionsResponse is the response for GET /api/v1/sessions
type GetSessionsResponse struct {
	Sessions []MCPSession `json:"sessions"`
	Total    int          `json:"total"`
	Limit    int          `json:"limit"`
	Offset   int          `json:"offset"`
}

// GetSessionDetailResponse is the response for GET /api/v1/sessions/{id}
type GetSessionDetailResponse struct {
	Session MCPSession `json:"session"`
}

// Configuration management types

// ValidationError represents a single configuration validation error
type ValidationError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// ConfigApplyResult represents the result of applying a configuration change
type ConfigApplyResult struct {
	Success            bool              `json:"success"`
	AppliedImmediately bool              `json:"applied_immediately"`
	RequiresRestart    bool              `json:"requires_restart"`
	RestartReason      string            `json:"restart_reason,omitempty"`
	ValidationErrors   []ValidationError `json:"validation_errors,omitempty"`
	ChangedFields      []string          `json:"changed_fields,omitempty"`
}

// GetConfigResponse is the response for GET /api/v1/config
type GetConfigResponse struct {
	Config     interface{} `json:"config" swaggertype:"object"`      // The configuration object
	ConfigPath string      `json:"config_path"`                      // Path to config file
}

// ValidateConfigRequest is the request for POST /api/v1/config/validate
type ValidateConfigRequest struct {
	Config interface{} `json:"config" swaggertype:"object"` // The configuration to validate
}

// ValidateConfigResponse is the response for POST /api/v1/config/validate
type ValidateConfigResponse struct {
	Valid  bool              `json:"valid"`
	Errors []ValidationError `json:"errors,omitempty"`
}

// ApplyConfigRequest is the request for POST /api/v1/config/apply
type ApplyConfigRequest struct {
	Config interface{} `json:"config" swaggertype:"object"` // The new configuration to apply
}

// Tool call replay types

// ReplayToolCallRequest is the request for POST /api/v1/tool-calls/{id}/replay
type ReplayToolCallRequest struct {
	Arguments map[string]interface{} `json:"arguments" swaggertype:"object"` // Modified arguments for replay
}

// ReplayToolCallResponse is the response for POST /api/v1/tool-calls/{id}/replay
type ReplayToolCallResponse struct {
	Success      bool           `json:"success"`
	NewCallID    string         `json:"new_call_id"`     // ID of the newly created call
	NewToolCall  ToolCallRecord `json:"new_tool_call"`   // The new tool call record
	ReplayedFrom string         `json:"replayed_from"`   // Original call ID
	Error        string         `json:"error,omitempty"` // Error if replay failed
}

// Registry browsing types (Phase 7)

// Registry represents an MCP server registry
type Registry struct {
	ID          string      `json:"id"`
	Name        string      `json:"name"`
	Description string      `json:"description"`
	URL         string      `json:"url"`
	ServersURL  string      `json:"servers_url,omitempty"`
	Tags        []string    `json:"tags,omitempty"`
	Protocol    string      `json:"protocol,omitempty"`
	Count       interface{} `json:"count,omitempty" swaggertype:"primitive,string"` // number or string
}

// RepositoryInfo represents detected repository type information
type RepositoryInfo struct {
	NPM *NPMPackageInfo `json:"npm,omitempty"`
	// Future: PyPI, Docker Hub, etc.
}

// NPMPackageInfo represents NPM package information
type NPMPackageInfo struct {
	Exists     bool   `json:"exists"`
	InstallCmd string `json:"install_cmd"`
}

// RepositoryServer represents an MCP server from a registry
type RepositoryServer struct {
	ID             string          `json:"id"`
	Name           string          `json:"name"`
	Description    string          `json:"description"`
	URL            string          `json:"url,omitempty"`             // MCP endpoint for remote servers only
	SourceCodeURL  string          `json:"source_code_url,omitempty"` // Source repository URL
	InstallCmd     string          `json:"install_cmd,omitempty"`     // Installation command
	ConnectURL     string          `json:"connect_url,omitempty"`     // Alternative connection URL
	UpdatedAt      string          `json:"updated_at,omitempty"`
	CreatedAt      string          `json:"created_at,omitempty"`
	Registry       string          `json:"registry,omitempty"`        // Which registry this came from
	RepositoryInfo *RepositoryInfo `json:"repository_info,omitempty"` // Detected package info
}

// GetRegistriesResponse is the response for GET /api/v1/registries
type GetRegistriesResponse struct {
	Registries []Registry `json:"registries"`
	Total      int        `json:"total"`
}

// SearchRegistryServersResponse is the response for GET /api/v1/registries/{id}/servers
type SearchRegistryServersResponse struct {
	RegistryID string             `json:"registry_id"`
	Servers    []RepositoryServer `json:"servers"`
	Total      int                `json:"total"`
	Query      string             `json:"query,omitempty"`
	Tag        string             `json:"tag,omitempty"`
}

// SuccessResponse is the standard success response wrapper for API endpoints.
type SuccessResponse struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data" swaggertype:"object"`
}

// ErrorResponse is the standard error response for API endpoints.
// All error responses include a request_id for log correlation.
type ErrorResponse struct {
	Success   bool   `json:"success"`
	Error     string `json:"error"`
	RequestID string `json:"request_id,omitempty"`
}

// HealthStatus represents the unified health status of an upstream MCP server.
// Calculated once in the backend and rendered identically by all interfaces.
type HealthStatus struct {
	// Level indicates the health level: "healthy", "degraded", or "unhealthy"
	Level string `json:"level"`

	// AdminState indicates the admin state: "enabled", "disabled", or "quarantined"
	AdminState string `json:"admin_state"`

	// Summary is a human-readable status message (e.g., "Connected (5 tools)")
	Summary string `json:"summary"`

	// Detail is an optional longer explanation of the status
	Detail string `json:"detail,omitempty"`

	// Action is the suggested fix action: "login", "restart", "enable", "approve", "view_logs", "set_secret", "configure", or "" (none)
	Action string `json:"action,omitempty"`
}

// UpdateInfo represents version update check information
type UpdateInfo struct {
	Available     bool       `json:"available"`                // Whether an update is available
	LatestVersion string     `json:"latest_version,omitempty"` // Latest version available (e.g., "v1.2.3")
	ReleaseURL    string     `json:"release_url,omitempty"`    // URL to the release page
	CheckedAt     *time.Time `json:"checked_at,omitempty"`     // When the update check was performed
	IsPrerelease  bool       `json:"is_prerelease,omitempty"`  // Whether the latest version is a prerelease
	CheckError    string     `json:"check_error,omitempty"`    // Error message if update check failed
}

// InfoEndpoints represents the available API endpoints
type InfoEndpoints struct {
	HTTP   string `json:"http"`   // HTTP endpoint address (e.g., "127.0.0.1:8080")
	Socket string `json:"socket"` // Unix socket path (empty if disabled)
}

// InfoResponse is the response for GET /api/v1/info
type InfoResponse struct {
	Version    string        `json:"version"`     // Current MCPProxy version
	WebUIURL   string        `json:"web_ui_url"`  // URL to access the web control panel
	ListenAddr string        `json:"listen_addr"` // Listen address (e.g., "127.0.0.1:8080")
	Endpoints  InfoEndpoints `json:"endpoints"`   // Available API endpoints
	Update     *UpdateInfo   `json:"update,omitempty"` // Update information (if available)
}
