package storage

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// ServerIdentity represents a unique server identity based on stable configuration
type ServerIdentity struct {
	ID          string            `json:"id"`           // SHA256 hash of stable attributes
	ServerName  string            `json:"server_name"`  // Human-readable name
	Fingerprint string            `json:"fingerprint"`  // Short hash (first 12 chars) for display
	Attributes  ServerAttributes  `json:"attributes"`   // Stable configuration attributes
	FirstSeen   time.Time         `json:"first_seen"`   // When first encountered
	LastSeen    time.Time         `json:"last_seen"`    // When last active
	ConfigPaths []string          `json:"config_paths"` // All configs that have included this server
	Metadata    map[string]string `json:"metadata"`     // Additional metadata
}

// ServerAttributes represents the stable attributes that define a server's identity
type ServerAttributes struct {
	Name       string            `json:"name"`        // Server name (required)
	Protocol   string            `json:"protocol"`    // http, stdio, etc.
	URL        string            `json:"url"`         // For HTTP servers
	Command    string            `json:"command"`     // For stdio servers
	Args       []string          `json:"args"`        // For stdio servers
	WorkingDir string            `json:"working_dir"` // Working directory
	Env        map[string]string `json:"env"`         // Environment variables (sorted)
	Headers    map[string]string `json:"headers"`     // HTTP headers (sorted)
	OAuth      *OAuthAttributes  `json:"oauth"`       // OAuth configuration (if present)
}

// OAuthAttributes represents stable OAuth configuration attributes
type OAuthAttributes struct {
	ClientID     string   `json:"client_id"`
	ClientSecret string   `json:"client_secret"` // Note: This might contain secrets
	RedirectURI  string   `json:"redirect_uri"`
	Scopes       []string `json:"scopes"`
	PKCEEnabled  bool     `json:"pkce_enabled"`
}

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

// ToolCallRecord represents a tool call with server context
type ToolCallRecord struct {
	ID               string                 `json:"id"`                          // UUID
	ServerID         string                 `json:"server_id"`                   // Server identity
	ServerName       string                 `json:"server_name"`                 // For quick reference
	ToolName         string                 `json:"tool_name"`                   // Original tool name (without server prefix)
	Arguments        map[string]interface{} `json:"arguments"`                   // Tool arguments
	Response         interface{}            `json:"response"`                    // Tool response
	Error            string                 `json:"error"`                       // Error if failed
	Duration         int64                  `json:"duration"`                    // Duration in nanoseconds
	Timestamp        time.Time              `json:"timestamp"`                   // When the call was made
	ConfigPath       string                 `json:"config_path"`                 // Which config was active
	RequestID        string                 `json:"request_id"`                  // For correlation
	Metrics          *TokenMetrics          `json:"metrics,omitempty"`           // Token usage metrics (nil for older records)
	ParentCallID     string                 `json:"parent_call_id,omitempty"`    // Links nested calls to parent code_execution
	ExecutionType    string                 `json:"execution_type,omitempty"`    // "direct" or "code_execution"
	MCPSessionID     string                    `json:"mcp_session_id,omitempty"`    // MCP session identifier
	MCPClientName    string                    `json:"mcp_client_name,omitempty"`   // MCP client name from InitializeRequest
	MCPClientVersion string                    `json:"mcp_client_version,omitempty"` // MCP client version
	Annotations      *config.ToolAnnotations   `json:"annotations,omitempty"`       // Tool behavior hints snapshot
}

// DiagnosticRecord represents a diagnostic event for a server
type DiagnosticRecord struct {
	ServerID   string                 `json:"server_id"`
	ServerName string                 `json:"server_name"`
	Type       string                 `json:"type"`     // error, warning, info
	Category   string                 `json:"category"` // oauth, connection, etc.
	Message    string                 `json:"message"`
	Details    map[string]interface{} `json:"details"`
	Timestamp  time.Time              `json:"timestamp"`
	ConfigPath string                 `json:"config_path"`
	Resolved   bool                   `json:"resolved"`
	ResolvedAt *time.Time             `json:"resolved_at,omitempty"`
}

// ServerStatistics represents statistical data for a server
type ServerStatistics struct {
	ServerID            string     `json:"server_id"`
	ServerName          string     `json:"server_name"`
	TotalCalls          int        `json:"total_calls"`
	SuccessfulCalls     int        `json:"successful_calls"`
	ErrorCalls          int        `json:"error_calls"`
	AverageResponseTime int64      `json:"avg_response_time"` // nanoseconds
	LastCallTime        *time.Time `json:"last_call_time,omitempty"`
	UpdatedAt           time.Time  `json:"updated_at"`
}

// GenerateServerID creates a unique, stable identity for a server
func GenerateServerID(server *config.ServerConfig) string {
	attrs := extractServerAttributes(server)
	return generateIDFromAttributes(attrs)
}

// GenerateServerIDFromAttributes creates ID from attributes directly
func GenerateServerIDFromAttributes(attrs ServerAttributes) string {
	return generateIDFromAttributes(attrs)
}

// extractServerAttributes extracts stable attributes from config
func extractServerAttributes(server *config.ServerConfig) ServerAttributes {
	attrs := ServerAttributes{
		Name:       server.Name,
		Protocol:   server.Protocol,
		URL:        server.URL,
		Command:    server.Command,
		Args:       make([]string, len(server.Args)),
		WorkingDir: server.WorkingDir,
		Env:        make(map[string]string),
		Headers:    make(map[string]string),
	}

	// Copy args
	copy(attrs.Args, server.Args)

	// Sort environment variables for consistency
	if server.Env != nil {
		for k, v := range server.Env {
			attrs.Env[k] = v
		}
	}

	// Sort headers for consistency
	if server.Headers != nil {
		for k, v := range server.Headers {
			attrs.Headers[k] = v
		}
	}

	// Convert OAuth config if present
	if server.OAuth != nil {
		attrs.OAuth = &OAuthAttributes{
			ClientID:     server.OAuth.ClientID,
			ClientSecret: server.OAuth.ClientSecret,
			RedirectURI:  server.OAuth.RedirectURI,
			Scopes:       make([]string, len(server.OAuth.Scopes)),
			PKCEEnabled:  server.OAuth.PKCEEnabled,
		}
		copy(attrs.OAuth.Scopes, server.OAuth.Scopes)
	}

	return attrs
}

// generateIDFromAttributes creates SHA256 hash from normalized attributes
func generateIDFromAttributes(attrs ServerAttributes) string {
	// Normalize for consistent hashing
	normalized := normalizeAttributes(attrs)

	data, err := json.Marshal(normalized)
	if err != nil {
		// Fallback to simple name-based hash if marshaling fails
		return hashString(attrs.Name + attrs.Protocol + attrs.URL + attrs.Command)
	}

	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

// normalizeAttributes ensures consistent ordering for hashing
func normalizeAttributes(attrs ServerAttributes) ServerAttributes {
	// Create a deep copy to avoid modifying the original
	normalized := ServerAttributes{
		Name:       attrs.Name,
		Protocol:   attrs.Protocol,
		URL:        attrs.URL,
		Command:    attrs.Command,
		WorkingDir: attrs.WorkingDir,
	}

	// Copy Args without sorting (order matters!)
	if attrs.Args != nil {
		normalized.Args = make([]string, len(attrs.Args))
		copy(normalized.Args, attrs.Args)
	}

	// Copy Env map
	if attrs.Env != nil {
		normalized.Env = make(map[string]string, len(attrs.Env))
		for k, v := range attrs.Env {
			normalized.Env[k] = v
		}
	}

	// Copy Headers map
	if attrs.Headers != nil {
		normalized.Headers = make(map[string]string, len(attrs.Headers))
		for k, v := range attrs.Headers {
			normalized.Headers[k] = v
		}
	}

	// Deep copy OAuth with sorted scopes (order doesn't affect OAuth functionality)
	if attrs.OAuth != nil {
		normalized.OAuth = &OAuthAttributes{
			ClientID:     attrs.OAuth.ClientID,
			ClientSecret: attrs.OAuth.ClientSecret,
			RedirectURI:  attrs.OAuth.RedirectURI,
			PKCEEnabled:  attrs.OAuth.PKCEEnabled,
		}

		if attrs.OAuth.Scopes != nil {
			normalized.OAuth.Scopes = make([]string, len(attrs.OAuth.Scopes))
			copy(normalized.OAuth.Scopes, attrs.OAuth.Scopes)
			sort.Strings(normalized.OAuth.Scopes)
		}
	}

	// Maps are already sorted by json.Marshal, but we ensure consistency

	return normalized
}

// hashString creates a simple SHA256 hash of a string
func hashString(s string) string {
	hash := sha256.Sum256([]byte(s))
	return hex.EncodeToString(hash[:])
}

// NewServerIdentity creates a new ServerIdentity from config
func NewServerIdentity(server *config.ServerConfig, configPath string) *ServerIdentity {
	attrs := extractServerAttributes(server)
	id := generateIDFromAttributes(attrs)

	now := time.Now()

	return &ServerIdentity{
		ID:          id,
		ServerName:  server.Name,
		Fingerprint: id[:12], // First 12 chars for display
		Attributes:  attrs,
		FirstSeen:   now,
		LastSeen:    now,
		ConfigPaths: []string{configPath},
		Metadata:    make(map[string]string),
	}
}

// UpdateLastSeen updates the last seen timestamp and adds config path if new
func (si *ServerIdentity) UpdateLastSeen(configPath string) {
	si.LastSeen = time.Now()

	// Add config path if not already present
	for _, path := range si.ConfigPaths {
		if path == configPath {
			return
		}
	}
	si.ConfigPaths = append(si.ConfigPaths, configPath)
}

// GetShortID returns a shortened version of the ID for display
func (si *ServerIdentity) GetShortID() string {
	return si.Fingerprint
}

// IsStale returns true if the server hasn't been seen for a long time
func (si *ServerIdentity) IsStale(threshold time.Duration) bool {
	return time.Since(si.LastSeen) > threshold
}
