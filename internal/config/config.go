package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secureenv"
)

const (
	defaultPort = "127.0.0.1:8080" // Localhost-only binding by default for security

	// Routing mode constants (Spec 031)
	RoutingModeRetrieveTools = "retrieve_tools" // Default: BM25 search via retrieve_tools + call_tool_read/write/destructive
	RoutingModeDirect        = "direct"         // All upstream tools exposed directly with serverName__toolName naming
	RoutingModeCodeExecution = "code_execution" // JS orchestration via code_execution tool with tool catalog
)

// Duration is a wrapper around time.Duration that can be marshaled to/from JSON.
// When serialized to JSON, it is represented as a string (e.g., "30s", "5m").
// @swaggertype string
type Duration time.Duration

// MarshalJSON implements json.Marshaler interface
func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

// UnmarshalJSON implements json.Unmarshaler interface
func (d *Duration) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}

	parsed, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration format: %w", err)
	}

	*d = Duration(parsed)
	return nil
}

// Duration returns the underlying time.Duration
func (d Duration) Duration() time.Duration {
	return time.Duration(d)
}

// Config represents the main configuration structure
type Config struct {
	Listen       string `json:"listen" mapstructure:"listen"`
	TrayEndpoint string `json:"tray_endpoint,omitempty" mapstructure:"tray-endpoint"` // Tray endpoint override (unix:// or npipe://)
	EnableSocket bool   `json:"enable_socket" mapstructure:"enable-socket"`           // Enable Unix socket/named pipe for local IPC (default: true)
	DataDir      string `json:"data_dir" mapstructure:"data-dir"`
	// Deprecated: EnableTray is unused and has no runtime effect. Kept for backward compatibility.
	EnableTray  bool            `json:"enable_tray,omitempty" mapstructure:"tray"`
	DebugSearch bool            `json:"debug_search" mapstructure:"debug-search"`
	Servers     []*ServerConfig `json:"mcpServers" mapstructure:"servers"`
	// Deprecated: TopK is superseded by ToolsLimit and has no runtime effect. Kept for backward compatibility.
	TopK               int      `json:"top_k,omitempty" mapstructure:"top-k"`
	ToolsLimit         int      `json:"tools_limit" mapstructure:"tools-limit"`
	ToolResponseLimit  int      `json:"tool_response_limit" mapstructure:"tool-response-limit"`
	CallToolTimeout    Duration `json:"call_tool_timeout" mapstructure:"call-tool-timeout" swaggertype:"string"`
	MaxResultSizeChars int      `json:"max_result_size_chars,omitempty" mapstructure:"max-result-size-chars"` // Advertised on every tool as `_meta.anthropic/maxResultSizeChars`; raises Claude Code's inline-response ceiling from 50k to up to 500k chars. Set to 0 to disable.

	// Environment configuration for secure variable filtering
	Environment *secureenv.EnvConfig `json:"environment,omitempty" mapstructure:"environment"`

	// Logging configuration
	Logging *LogConfig `json:"logging,omitempty" mapstructure:"logging"`

	// Security settings
	APIKey            string `json:"api_key,omitempty" mapstructure:"api-key"`         // API key for REST API authentication
	RequireMCPAuth    bool   `json:"require_mcp_auth" mapstructure:"require-mcp-auth"` // Require authentication on /mcp endpoint (default: false)
	ReadOnlyMode      bool   `json:"read_only_mode" mapstructure:"read-only-mode"`
	DisableManagement bool   `json:"disable_management" mapstructure:"disable-management"`
	AllowServerAdd    bool   `json:"allow_server_add" mapstructure:"allow-server-add"`
	AllowServerRemove bool   `json:"allow_server_remove" mapstructure:"allow-server-remove"`

	// Internal field to track if API key was explicitly set in config
	apiKeyExplicitlySet bool `json:"-"`

	// Prompts settings
	EnablePrompts bool `json:"enable_prompts" mapstructure:"enable-prompts"`

	// Repository detection settings
	CheckServerRepo bool `json:"check_server_repo" mapstructure:"check-server-repo"`

	// Docker isolation settings
	DockerIsolation *DockerIsolationConfig `json:"docker_isolation,omitempty" mapstructure:"docker-isolation"`

	// Docker recovery settings
	DockerRecovery *DockerRecoveryConfig `json:"docker_recovery,omitempty" mapstructure:"docker-recovery"`

	// Registries configuration for MCP server discovery
	Registries []RegistryEntry `json:"registries,omitempty" mapstructure:"registries"`

	// Deprecated: Features flags are unused and have no runtime effect. Kept for backward compatibility.
	Features *FeatureFlags `json:"features,omitempty" mapstructure:"features"`

	// TLS configuration
	TLS *TLSConfig `json:"tls,omitempty" mapstructure:"tls"`

	// Tokenizer configuration for token counting
	Tokenizer *TokenizerConfig `json:"tokenizer,omitempty" mapstructure:"tokenizer"`

	// Code execution settings
	EnableCodeExecution       bool `json:"enable_code_execution" mapstructure:"enable-code-execution"`                           // Enable JavaScript code execution tool (default: false)
	CodeExecutionTimeoutMs    int  `json:"code_execution_timeout_ms,omitempty" mapstructure:"code-execution-timeout-ms"`         // Timeout in milliseconds (default: 120000, max: 600000)
	CodeExecutionMaxToolCalls int  `json:"code_execution_max_tool_calls,omitempty" mapstructure:"code-execution-max-tool-calls"` // Max tool calls per execution (0 = unlimited, default: 0)
	CodeExecutionPoolSize     int  `json:"code_execution_pool_size,omitempty" mapstructure:"code-execution-pool-size"`           // JavaScript runtime pool size (default: 10)

	// ToolResponseSessionRiskWarning controls whether the prose `warning` field
	// is included in the `session_risk` object returned by `retrieve_tools`.
	// The structured fields (level, lethal_trifecta, has_open_world_tools, etc.)
	// are always included. Default: false (quiet for LLM clients) — see issue #406.
	// Most tools lack annotations, so the MCP-spec defaults treat them as fully
	// permissive across all three risk axes, which makes the prose warning fire
	// on almost every call and wastes tokens.
	ToolResponseSessionRiskWarning bool `json:"tool_response_session_risk_warning,omitempty" mapstructure:"tool-response-session-risk-warning"`

	// Health status settings
	OAuthExpiryWarningHours float64 `json:"oauth_expiry_warning_hours,omitempty" mapstructure:"oauth-expiry-warning-hours"` // Hours before token expiry to show degraded status (default: 1.0)

	// Activity logging settings (RFC-003)
	ActivityRetentionDays      int `json:"activity_retention_days,omitempty" mapstructure:"activity-retention-days"`             // Max age before pruning (default: 90)
	ActivityMaxRecords         int `json:"activity_max_records,omitempty" mapstructure:"activity-max-records"`                   // Max records before pruning (default: 100000)
	ActivityMaxResponseSize    int `json:"activity_max_response_size,omitempty" mapstructure:"activity-max-response-size"`       // Response truncation limit in bytes (default: 65536)
	ActivityCleanupIntervalMin int `json:"activity_cleanup_interval_min,omitempty" mapstructure:"activity-cleanup-interval-min"` // Background cleanup interval in minutes (default: 60)

	// Intent declaration settings (Spec 018)
	IntentDeclaration *IntentDeclarationConfig `json:"intent_declaration,omitempty" mapstructure:"intent-declaration"`

	// Sensitive data detection settings (Spec 026)
	SensitiveDataDetection *SensitiveDataDetectionConfig `json:"sensitive_data_detection,omitempty" mapstructure:"sensitive-data-detection"`

	// Telemetry settings (Spec 036)
	Telemetry *TelemetryConfig `json:"telemetry,omitempty" mapstructure:"telemetry"`

	// Routing mode (Spec 031): how MCP tools are exposed to clients
	// Valid values: "retrieve_tools" (default), "direct", "code_execution"
	RoutingMode string `json:"routing_mode,omitempty" mapstructure:"routing-mode"`

	// QuarantineEnabled controls whether quarantine is active. It gates two
	// things together:
	//   1. Server-level auto-quarantine for newly added servers (issue #370).
	//      When true, servers added via the upstream_servers MCP tool or the
	//      REST API default to quarantined=true; when false, they default to
	//      quarantined=false. Explicit per-request values always win.
	//   2. Tool-level quarantine (Spec 032): per-tool SHA-256 approval of
	//      tool descriptions/schemas.
	// When nil (default), quarantine is enabled (secure by default). Set to
	// explicit false to opt out of both. Per-server SkipQuarantine still
	// applies for the tool-level check on individual servers.
	QuarantineEnabled *bool `json:"quarantine_enabled,omitempty" mapstructure:"quarantine-enabled"`

	// Security scanner settings (Spec 039)
	Security *SecurityConfig `json:"security,omitempty" mapstructure:"security"`

	// RevealSecretHeaders, when true, disables the redaction of sensitive
	// header values (Authorization, X-API-Key, Cookie, …) in responses from
	// the `upstream_servers` MCP tool and the `/api/v1/servers` REST API.
	// Default false — sensitive header values are surfaced as
	// `***REDACTED***` so an MCP agent cannot read Bearer tokens / API keys
	// out of another upstream's config. Set to true if a downstream tool
	// needs the raw values (e.g. a UI that round-trips PUT replaces).
	RevealSecretHeaders bool `json:"reveal_secret_headers,omitempty" mapstructure:"reveal-secret-headers"`

	// Server edition multi-user configuration (only meaningful with -tags server)
	Teams *TeamsConfig `json:"teams,omitempty" mapstructure:"teams" swaggerignore:"true"`
}

// TLSConfig represents TLS configuration
type TLSConfig struct {
	Enabled           bool   `json:"enabled" mapstructure:"enabled"`                         // Enable HTTPS
	RequireClientCert bool   `json:"require_client_cert" mapstructure:"require_client_cert"` // Enable mTLS
	CertsDir          string `json:"certs_dir,omitempty" mapstructure:"certs_dir"`           // Directory for certificates
	HSTS              bool   `json:"hsts" mapstructure:"hsts"`                               // Enable HTTP Strict Transport Security
}

// TokenizerConfig represents tokenizer configuration for token counting
type TokenizerConfig struct {
	Enabled      bool   `json:"enabled" mapstructure:"enabled"`             // Enable token counting
	DefaultModel string `json:"default_model" mapstructure:"default_model"` // Default model for tokenization (e.g., "gpt-4")
	Encoding     string `json:"encoding" mapstructure:"encoding"`           // Default encoding (e.g., "cl100k_base")
}

// LogConfig represents logging configuration
type LogConfig struct {
	Level         string `json:"level" mapstructure:"level"`
	EnableFile    bool   `json:"enable_file" mapstructure:"enable-file"`
	EnableConsole bool   `json:"enable_console" mapstructure:"enable-console"`
	Filename      string `json:"filename" mapstructure:"filename"`
	LogDir        string `json:"log_dir,omitempty" mapstructure:"log-dir"` // Custom log directory
	MaxSize       int    `json:"max_size" mapstructure:"max-size"`         // MB
	MaxBackups    int    `json:"max_backups" mapstructure:"max-backups"`   // number of backup files
	MaxAge        int    `json:"max_age" mapstructure:"max-age"`           // days
	Compress      bool   `json:"compress" mapstructure:"compress"`
	JSONFormat    bool   `json:"json_format" mapstructure:"json-format"`
}

// ServerConfig represents upstream MCP server configuration
type ServerConfig struct {
	Name           string            `json:"name,omitempty" mapstructure:"name"`
	URL            string            `json:"url,omitempty" mapstructure:"url"`
	Protocol       string            `json:"protocol,omitempty" mapstructure:"protocol"` // stdio, http, sse, streamable-http, auto
	Command        string            `json:"command,omitempty" mapstructure:"command"`
	Args           []string          `json:"args,omitempty" mapstructure:"args"`
	WorkingDir     string            `json:"working_dir,omitempty" mapstructure:"working_dir"` // Working directory for stdio servers
	Env            map[string]string `json:"env,omitempty" mapstructure:"env"`
	Headers        map[string]string `json:"headers,omitempty" mapstructure:"headers"` // For HTTP servers
	OAuth          *OAuthConfig      `json:"oauth" mapstructure:"oauth"`               // OAuth configuration (keep even when empty to signal OAuth requirement)
	Enabled        bool              `json:"enabled" mapstructure:"enabled"`
	Quarantined    bool              `json:"quarantined" mapstructure:"quarantined"`                   // Security quarantine status
	SkipQuarantine bool              `json:"skip_quarantine,omitempty" mapstructure:"skip-quarantine"` // Skip tool-level quarantine for this server
	Shared         bool              `json:"shared,omitempty" mapstructure:"shared"`                   // Server edition: shared with all users
	Created        time.Time         `json:"created" mapstructure:"created"`
	Updated        time.Time         `json:"updated,omitempty" mapstructure:"updated"`
	Isolation      *IsolationConfig  `json:"isolation,omitempty" mapstructure:"isolation"`               // Per-server isolation settings
	ReconnectOnUse bool              `json:"reconnect_on_use,omitempty" mapstructure:"reconnect-on-use"` // Attempt reconnection when a tool call targets a disconnected server
}

// OAuthConfig represents OAuth configuration for a server
type OAuthConfig struct {
	ClientID     string            `json:"client_id,omitempty" mapstructure:"client_id"`
	ClientSecret string            `json:"client_secret,omitempty" mapstructure:"client_secret"`
	RedirectURI  string            `json:"redirect_uri,omitempty" mapstructure:"redirect_uri"`
	Scopes       []string          `json:"scopes,omitempty" mapstructure:"scopes"`
	PKCEEnabled  bool              `json:"pkce_enabled,omitempty" mapstructure:"pkce_enabled"`
	ExtraParams  map[string]string `json:"extra_params,omitempty" mapstructure:"extra_params"` // Additional OAuth parameters (e.g., RFC 8707 resource)
}

// DockerIsolationConfig represents global Docker isolation settings
type DockerIsolationConfig struct {
	Enabled           bool              `json:"enabled" mapstructure:"enabled"`                                // Global enable/disable for Docker isolation
	EnableCacheVolume bool              `json:"enable_cache_volume" mapstructure:"enable_cache_volume"`        // Mount shared cache volumes for faster restarts (default: true)
	DefaultImages     map[string]string `json:"default_images" mapstructure:"default_images"`                  // Map of runtime type to Docker image
	Registry          string            `json:"registry,omitempty" mapstructure:"registry"`                    // Custom registry (defaults to docker.io)
	NetworkMode       string            `json:"network_mode,omitempty" mapstructure:"network_mode"`            // Docker network mode (default: bridge)
	MemoryLimit       string            `json:"memory_limit,omitempty" mapstructure:"memory_limit"`            // Memory limit for containers
	CPULimit          string            `json:"cpu_limit,omitempty" mapstructure:"cpu_limit"`                  // CPU limit for containers
	Timeout           Duration          `json:"timeout,omitempty" mapstructure:"timeout" swaggertype:"string"` // Container startup timeout
	ExtraArgs         []string          `json:"extra_args,omitempty" mapstructure:"extra_args"`                // Additional docker run arguments
	LogDriver         string            `json:"log_driver,omitempty" mapstructure:"log_driver"`                // Docker log driver (default: json-file)
	LogMaxSize        string            `json:"log_max_size,omitempty" mapstructure:"log_max_size"`            // Maximum size of log files (default: 100m)
	LogMaxFiles       string            `json:"log_max_files,omitempty" mapstructure:"log_max_files"`          // Maximum number of log files (default: 3)
}

// IsolationConfig represents per-server isolation settings
type IsolationConfig struct {
	Enabled     *bool    `json:"enabled,omitempty" mapstructure:"enabled"`             // Enable Docker isolation for this server (nil = inherit global)
	Image       string   `json:"image,omitempty" mapstructure:"image"`                 // Custom Docker image (overrides default)
	NetworkMode string   `json:"network_mode,omitempty" mapstructure:"network_mode"`   // Custom network mode for this server
	ExtraArgs   []string `json:"extra_args,omitempty" mapstructure:"extra_args"`       // Additional docker run arguments for this server
	WorkingDir  string   `json:"working_dir,omitempty" mapstructure:"working_dir"`     // Custom working directory in container
	LogDriver   string   `json:"log_driver,omitempty" mapstructure:"log_driver"`       // Docker log driver override for this server
	LogMaxSize  string   `json:"log_max_size,omitempty" mapstructure:"log_max_size"`   // Maximum size of log files override
	LogMaxFiles string   `json:"log_max_files,omitempty" mapstructure:"log_max_files"` // Maximum number of log files override
}

// IsEnabled returns true if isolation is explicitly enabled, false otherwise.
// Returns false if Enabled is nil (not set).
func (ic *IsolationConfig) IsEnabled() bool {
	if ic == nil || ic.Enabled == nil {
		return false
	}
	return *ic.Enabled
}

// BoolPtr returns a pointer to the given bool value.
// Useful for setting *bool fields in struct literals.
func BoolPtr(b bool) *bool {
	return &b
}

// DockerRecoveryConfig represents Docker recovery settings for the tray application
type DockerRecoveryConfig struct {
	Enabled         bool       `json:"enabled" mapstructure:"enabled"`                                          // Enable Docker recovery monitoring (default: true)
	CheckIntervals  []Duration `json:"check_intervals,omitempty" mapstructure:"intervals" swaggerignore:"true"` // Custom health check intervals (exponential backoff)
	MaxRetries      int        `json:"max_retries,omitempty" mapstructure:"max_retries"`                        // Maximum retry attempts (0 = unlimited)
	NotifyOnStart   bool       `json:"notify_on_start" mapstructure:"notify_on_start"`                          // Show notification when recovery starts (default: true)
	NotifyOnSuccess bool       `json:"notify_on_success" mapstructure:"notify_on_success"`                      // Show notification on successful recovery (default: true)
	NotifyOnFailure bool       `json:"notify_on_failure" mapstructure:"notify_on_failure"`                      // Show notification on recovery failure (default: true)
	NotifyOnRetry   bool       `json:"notify_on_retry" mapstructure:"notify_on_retry"`                          // Show notification on each retry (default: false)
	PersistentState bool       `json:"persistent_state" mapstructure:"persistent_state"`                        // Save recovery state across restarts (default: true)
}

// DefaultCheckIntervals returns the default Docker recovery check intervals
func DefaultCheckIntervals() []time.Duration {
	return []time.Duration{
		2 * time.Second,  // Immediate retry (Docker just paused)
		5 * time.Second,  // Quick retry
		10 * time.Second, // Normal retry
		30 * time.Second, // Slow retry
		60 * time.Second, // Very slow retry (max backoff)
	}
}

// GetCheckIntervals returns the configured check intervals as time.Duration slice, or defaults if not set
func (d *DockerRecoveryConfig) GetCheckIntervals() []time.Duration {
	if d == nil || len(d.CheckIntervals) == 0 {
		return DefaultCheckIntervals()
	}

	intervals := make([]time.Duration, len(d.CheckIntervals))
	for i, dur := range d.CheckIntervals {
		intervals[i] = dur.Duration()
	}
	return intervals
}

// IsEnabled returns whether Docker recovery is enabled (default: true)
func (d *DockerRecoveryConfig) IsEnabled() bool {
	if d == nil {
		return true // Enabled by default
	}
	return d.Enabled
}

// ShouldNotifyOnStart returns whether to notify when recovery starts (default: true)
func (d *DockerRecoveryConfig) ShouldNotifyOnStart() bool {
	if d == nil {
		return true
	}
	return d.NotifyOnStart
}

// ShouldNotifyOnSuccess returns whether to notify on successful recovery (default: true)
func (d *DockerRecoveryConfig) ShouldNotifyOnSuccess() bool {
	if d == nil {
		return true
	}
	return d.NotifyOnSuccess
}

// ShouldNotifyOnFailure returns whether to notify on recovery failure (default: true)
func (d *DockerRecoveryConfig) ShouldNotifyOnFailure() bool {
	if d == nil {
		return true
	}
	return d.NotifyOnFailure
}

// ShouldNotifyOnRetry returns whether to notify on each retry (default: false)
func (d *DockerRecoveryConfig) ShouldNotifyOnRetry() bool {
	if d == nil {
		return false
	}
	return d.NotifyOnRetry
}

// ShouldPersistState returns whether to persist recovery state across restarts (default: true)
func (d *DockerRecoveryConfig) ShouldPersistState() bool {
	if d == nil {
		return true
	}
	return d.PersistentState
}

// GetMaxRetries returns the maximum number of retries (0 = unlimited)
func (d *DockerRecoveryConfig) GetMaxRetries() int {
	if d == nil {
		return 0 // Unlimited by default
	}
	return d.MaxRetries
}

// SensitiveDataDetectionConfig represents sensitive data detection settings (Spec 026)
type SensitiveDataDetectionConfig struct {
	Enabled           bool            `json:"enabled" mapstructure:"enabled"`                                 // Enable sensitive data detection (default: true)
	ScanRequests      bool            `json:"scan_requests" mapstructure:"scan-requests"`                     // Scan tool call arguments (default: true)
	ScanResponses     bool            `json:"scan_responses" mapstructure:"scan-responses"`                   // Scan tool responses (default: true)
	MaxPayloadSizeKB  int             `json:"max_payload_size_kb" mapstructure:"max-payload-size-kb"`         // Max size to scan before truncating (default: 1024)
	EntropyThreshold  float64         `json:"entropy_threshold" mapstructure:"entropy-threshold"`             // Shannon entropy threshold for high-entropy detection (default: 4.5)
	Categories        map[string]bool `json:"categories,omitempty" mapstructure:"categories"`                 // Enable/disable specific detection categories
	CustomPatterns    []CustomPattern `json:"custom_patterns,omitempty" mapstructure:"custom-patterns"`       // User-defined detection patterns
	SensitiveKeywords []string        `json:"sensitive_keywords,omitempty" mapstructure:"sensitive-keywords"` // Keywords to flag
}

// CustomPattern represents a user-defined detection pattern
type CustomPattern struct {
	Name     string   `json:"name" mapstructure:"name"`                   // Unique identifier for this pattern
	Regex    string   `json:"regex,omitempty" mapstructure:"regex"`       // Regex pattern (mutually exclusive with Keywords)
	Keywords []string `json:"keywords,omitempty" mapstructure:"keywords"` // Keywords to match (mutually exclusive with Regex)
	Severity string   `json:"severity" mapstructure:"severity"`           // Risk level: critical, high, medium, low
	Category string   `json:"category,omitempty" mapstructure:"category"` // Category (defaults to "custom")
}

// DefaultSensitiveDataDetectionConfig returns the default configuration for sensitive data detection
func DefaultSensitiveDataDetectionConfig() *SensitiveDataDetectionConfig {
	return &SensitiveDataDetectionConfig{
		Enabled:          true,
		ScanRequests:     true,
		ScanResponses:    true,
		MaxPayloadSizeKB: 1024,
		EntropyThreshold: 4.5,
		Categories: map[string]bool{
			"cloud_credentials":   true,
			"private_key":         true,
			"api_token":           true,
			"auth_token":          true,
			"sensitive_file":      true,
			"database_credential": true,
			"high_entropy":        true,
			"credit_card":         true,
		},
	}
}

// IsEnabled returns true if sensitive data detection is enabled (default: true)
func (c *SensitiveDataDetectionConfig) IsEnabled() bool {
	if c == nil {
		return true // Enabled by default
	}
	return c.Enabled
}

// IsCategoryEnabled returns true if the specified category is enabled
func (c *SensitiveDataDetectionConfig) IsCategoryEnabled(category string) bool {
	if c == nil || c.Categories == nil {
		return true // All categories enabled by default
	}
	enabled, exists := c.Categories[category]
	if !exists {
		return true // Categories not in the map are enabled by default
	}
	return enabled
}

// GetMaxPayloadSize returns the max payload size in bytes
func (c *SensitiveDataDetectionConfig) GetMaxPayloadSize() int {
	if c == nil || c.MaxPayloadSizeKB <= 0 {
		return 1024 * 1024 // 1MB default
	}
	return c.MaxPayloadSizeKB * 1024
}

// GetEntropyThreshold returns the entropy threshold (default: 4.5)
func (c *SensitiveDataDetectionConfig) GetEntropyThreshold() float64 {
	if c == nil || c.EntropyThreshold <= 0 {
		return 4.5
	}
	return c.EntropyThreshold
}

// RegistryEntry represents a registry in the configuration
type RegistryEntry struct {
	ID          string      `json:"id"`
	Name        string      `json:"name"`
	Description string      `json:"description"`
	URL         string      `json:"url"`
	ServersURL  string      `json:"servers_url,omitempty"`
	Tags        []string    `json:"tags,omitempty"`
	Protocol    string      `json:"protocol,omitempty"`
	Count       interface{} `json:"count,omitempty" swaggertype:"primitive,string"` // number or string
}

// CursorMCPConfig represents the structure for Cursor IDE MCP configuration
type CursorMCPConfig struct {
	MCPServers map[string]CursorServerConfig `json:"mcpServers"`
}

// CursorServerConfig represents a single server configuration in Cursor format
type CursorServerConfig struct {
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

// ConvertFromCursorFormat converts Cursor IDE format to our internal format
func ConvertFromCursorFormat(cursorConfig *CursorMCPConfig) []*ServerConfig {
	var servers []*ServerConfig

	for name, serverConfig := range cursorConfig.MCPServers {
		server := &ServerConfig{
			Name:    name,
			Enabled: true,
			Created: time.Now(),
		}

		if serverConfig.Command != "" {
			server.Command = serverConfig.Command
			server.Args = serverConfig.Args
			server.Env = serverConfig.Env
			server.Protocol = "stdio"
		} else if serverConfig.URL != "" {
			server.URL = serverConfig.URL
			server.Headers = serverConfig.Headers
			server.Protocol = "http"
		}

		servers = append(servers, server)
	}

	return servers
}

// ToolMetadata represents tool information stored in the index
type ToolMetadata struct {
	Name        string           `json:"name"`
	ServerName  string           `json:"server_name"`
	Description string           `json:"description"`
	ParamsJSON  string           `json:"params_json"`
	Hash        string           `json:"hash"`
	Created     time.Time        `json:"created"`
	Updated     time.Time        `json:"updated"`
	Annotations *ToolAnnotations `json:"annotations,omitempty"`
}

// ToolAnnotations represents MCP tool behavior hints
type ToolAnnotations struct {
	Title           string `json:"title,omitempty"`
	ReadOnlyHint    *bool  `json:"readOnlyHint,omitempty"`
	DestructiveHint *bool  `json:"destructiveHint,omitempty"`
	IdempotentHint  *bool  `json:"idempotentHint,omitempty"`
	OpenWorldHint   *bool  `json:"openWorldHint,omitempty"`
}

// IntentDeclarationConfig controls intent validation behavior for tool calls
type IntentDeclarationConfig struct {
	// StrictServerValidation controls whether server annotation mismatches
	// cause rejection (true) or just warnings (false).
	// Default: true (reject mismatches)
	StrictServerValidation bool `json:"strict_server_validation" mapstructure:"strict-server-validation"`
}

// DefaultIntentDeclarationConfig returns the default intent declaration configuration
func DefaultIntentDeclarationConfig() *IntentDeclarationConfig {
	return &IntentDeclarationConfig{
		StrictServerValidation: true, // Security by default
	}
}

// IsStrictServerValidation returns whether strict server validation is enabled
func (c *IntentDeclarationConfig) IsStrictServerValidation() bool {
	if c == nil {
		return true // Default to strict for security
	}
	return c.StrictServerValidation
}

// ToolRegistration represents a tool registration
type ToolRegistration struct {
	Name         string                 `json:"name"`
	Description  string                 `json:"description"`
	InputSchema  map[string]interface{} `json:"input_schema"`
	ServerName   string                 `json:"server_name"`
	OriginalName string                 `json:"original_name"`
}

// SearchResult represents a search result with score
type SearchResult struct {
	Tool  *ToolMetadata `json:"tool"`
	Score float64       `json:"score"`
}

// ToolStats represents tool statistics
type ToolStats struct {
	TotalTools int             `json:"total_tools"`
	TopTools   []ToolStatEntry `json:"top_tools"`
}

// ToolStatEntry represents a single tool stat entry
type ToolStatEntry struct {
	ToolName string `json:"tool_name"`
	Count    uint64 `json:"count"`
}

// DefaultDockerIsolationConfig returns default Docker isolation configuration
func DefaultDockerIsolationConfig() *DockerIsolationConfig {
	return &DockerIsolationConfig{
		Enabled:           false, // Disabled by default for backward compatibility
		EnableCacheVolume: true,  // Cache volumes speed up container restarts dramatically
		DefaultImages: map[string]string{
			// Python environments - single uv image for all Python commands (includes python, pip, uvx)
			"python":  "ghcr.io/astral-sh/uv:python3.13-bookworm-slim",
			"python3": "ghcr.io/astral-sh/uv:python3.13-bookworm-slim",
			"uvx":     "ghcr.io/astral-sh/uv:python3.13-bookworm-slim",
			"pip":     "ghcr.io/astral-sh/uv:python3.13-bookworm-slim",
			"pipx":    "ghcr.io/astral-sh/uv:python3.13-bookworm-slim",

			// Node.js environments - full image for git deps and native modules (LTS until Apr 2028)
			"node": "node:22",
			"npm":  "node:22",
			"npx":  "node:22",
			"yarn": "node:22",

			// Go binaries
			"go": "golang:1.21-alpine",

			// Rust binaries
			"cargo": "rust:1.75-slim",
			"rustc": "rust:1.75-slim",

			// Generic binary execution
			"binary": "alpine:3.18",

			// Shell/script execution
			"sh":   "alpine:3.18",
			"bash": "alpine:3.18",

			// Ruby
			"ruby": "ruby:3.2-alpine",
			"gem":  "ruby:3.2-alpine",

			// PHP
			"php":      "php:8.2-cli-alpine",
			"composer": "php:8.2-cli-alpine",
		},
		Registry:    "docker.io",                // Default Docker Hub registry
		NetworkMode: "bridge",                   // Default Docker network mode
		MemoryLimit: "512m",                     // Default memory limit
		CPULimit:    "1.0",                      // Default CPU limit (1 core)
		Timeout:     Duration(30 * time.Second), // 30 second startup timeout
		ExtraArgs:   []string{},                 // No extra args by default
		LogDriver:   "",                         // Use Docker system default (empty = no override)
		LogMaxSize:  "100m",                     // Default maximum log file size (only used if json-file driver is set)
		LogMaxFiles: "3",                        // Default maximum number of log files (only used if json-file driver is set)
	}
}

// DefaultConfig returns a default configuration
func DefaultConfig() *Config {
	return &Config{
		Listen:             defaultPort,
		EnableSocket:       true, // Enable Unix socket/named pipe by default for local IPC
		DataDir:            "",   // Will be set to ~/.mcpproxy by loader
		DebugSearch:        false,
		Servers:            []*ServerConfig{},
		ToolsLimit:         15,
		ToolResponseLimit:  20000,                     // Default 20000 characters
		CallToolTimeout:    Duration(2 * time.Minute), // Default 2 minutes for tool calls
		MaxResultSizeChars: 500000,                    // Claude Code's inline-response hard max

		// Default secure environment configuration
		Environment: secureenv.DefaultEnvConfig(),

		// Default logging configuration
		Logging: &LogConfig{
			Level:         "info",
			EnableFile:    false, // Changed: Console by default
			EnableConsole: true,
			Filename:      "main.log",
			MaxSize:       10, // 10MB
			MaxBackups:    5,  // 5 backup files
			MaxAge:        30, // 30 days
			Compress:      true,
			JSONFormat:    false, // Use console format for readability
		},

		// Security defaults - permissive by default for compatibility
		RequireMCPAuth:    false, // MCP endpoint is unprotected by default for backward compatibility
		ReadOnlyMode:      false,
		DisableManagement: false,
		AllowServerAdd:    true,
		AllowServerRemove: true,

		// Prompts enabled by default
		EnablePrompts: true,

		// Repository detection enabled by default
		CheckServerRepo: true,

		// Default Docker isolation settings
		DockerIsolation: DefaultDockerIsolationConfig(),

		// Default sensitive data detection settings (enabled by default for security)
		SensitiveDataDetection: DefaultSensitiveDataDetectionConfig(),

		// Default registries for MCP server discovery
		Registries: []RegistryEntry{
			{
				ID:          "pulse",
				Name:        "Pulse MCP",
				Description: "Browse and discover MCP use-cases, servers, clients, and news",
				URL:         "https://www.pulsemcp.com/",
				ServersURL:  "https://api.pulsemcp.com/v0beta/servers",
				Tags:        []string{"verified"},
				Protocol:    "custom/pulse",
			},
			{
				ID:          "docker-mcp-catalog",
				Name:        "Docker MCP Catalog",
				Description: "A collection of secure, high-quality MCP servers as docker images",
				URL:         "https://hub.docker.com/catalogs/mcp",
				ServersURL:  "https://hub.docker.com/v2/repositories/mcp/",
				Tags:        []string{"verified"},
				Protocol:    "custom/docker",
			},
			{
				ID:          "fleur",
				Name:        "Fleur",
				Description: "Fleur is the app store for Claude",
				URL:         "https://www.fleurmcp.com/",
				ServersURL:  "https://raw.githubusercontent.com/fleuristes/app-registry/refs/heads/main/apps.json",
				Tags:        []string{"verified"},
				Protocol:    "custom/fleur",
			},
			{
				ID:          "azure-mcp-demo",
				Name:        "Azure MCP Registry Demo",
				Description: "A reference implementation of MCP registry using Azure API Center",
				URL:         "https://demo.registry.azure-mcp.net/",
				ServersURL:  "https://demo.registry.azure-mcp.net/v0/servers",
				Tags:        []string{"verified", "demo", "azure", "reference"},
				Protocol:    "mcp/v0",
			},
			{
				ID:          "remote-mcp-servers",
				Name:        "Remote MCP Servers",
				Description: "Community-maintained list of remote Model Context Protocol servers",
				URL:         "https://remote-mcp-servers.com/",
				ServersURL:  "https://remote-mcp-servers.com/api/servers",
				Tags:        []string{"verified", "community", "remote"},
				Protocol:    "custom/remote",
			},
		},

		// Default feature flags
		Features: func() *FeatureFlags {
			flags := DefaultFeatureFlags()
			return &flags
		}(),

		// Default TLS configuration - disabled by default for easier setup
		TLS: &TLSConfig{
			Enabled:           false, // HTTPS disabled by default, can be enabled via config or env var
			RequireClientCert: false, // mTLS disabled by default
			CertsDir:          "",    // Will default to ${data_dir}/certs
			HSTS:              true,  // HSTS enabled by default
		},

		// Default tokenizer configuration
		Tokenizer: &TokenizerConfig{
			Enabled:      true,          // Token counting enabled by default
			DefaultModel: "gpt-4",       // Default to GPT-4 tokenization
			Encoding:     "cl100k_base", // Default encoding (GPT-4, GPT-3.5)
		},

		// Code execution defaults - disabled by default for security
		EnableCodeExecution:       false,  // Must be explicitly enabled
		CodeExecutionTimeoutMs:    120000, // 2 minutes (120,000ms)
		CodeExecutionMaxToolCalls: 0,      // Unlimited by default (0 = no limit)
		CodeExecutionPoolSize:     10,     // 10 JavaScript runtime instances

		// Session risk warning prose disabled by default to reduce token overhead
		// and LLM distraction in trusted setups (issue #406). Structured risk
		// fields are still emitted; only the prose `warning` is gated.
		ToolResponseSessionRiskWarning: false,

		// Activity logging defaults (RFC-003)
		ActivityRetentionDays:      90,     // 90 days retention
		ActivityMaxRecords:         100000, // 100K records max
		ActivityMaxResponseSize:    65536,  // 64KB response truncation
		ActivityCleanupIntervalMin: 60,     // 1 hour cleanup interval

		// Intent declaration defaults (Spec 018) - strict validation by default for security
		IntentDeclaration: DefaultIntentDeclarationConfig(),
	}
}

// generateAPIKey creates a cryptographically secure random API key
func generateAPIKey() string {
	bytes := make([]byte, 32) // 32 bytes = 256 bits
	if _, err := rand.Read(bytes); err != nil {
		// Fallback to less secure method if crypto/rand fails
		return fmt.Sprintf("mcpproxy_%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(bytes)
}

// APIKeySource represents where the API key came from
type APIKeySource int

const (
	APIKeySourceEnvironment APIKeySource = iota
	APIKeySourceConfig
	APIKeySourceGenerated
)

// String returns a human-readable representation of the API key source
func (s APIKeySource) String() string {
	switch s {
	case APIKeySourceEnvironment:
		return "environment variable"
	case APIKeySourceConfig:
		return "configuration file"
	case APIKeySourceGenerated:
		return "auto-generated"
	default:
		return "unknown"
	}
}

// IsQuarantineEnabled returns whether quarantine (both server-level
// auto-quarantine and tool-level approval) is enabled. Defaults to true
// (secure by default) when not explicitly set.
func (c *Config) IsQuarantineEnabled() bool {
	if c.QuarantineEnabled == nil {
		return true
	}
	return *c.QuarantineEnabled
}

// DefaultQuarantineForNewServer returns the default value for the
// Quarantined field when a new server is added via an API that does not
// explicitly specify it (MCP upstream_servers tool, REST /api/v1/servers).
// Secure by default: true unless the operator has disabled quarantine
// globally via quarantine_enabled=false.
func (c *Config) DefaultQuarantineForNewServer() bool {
	return c.IsQuarantineEnabled()
}

// IsQuarantineSkipped returns whether this server should skip tool-level quarantine.
func (sc *ServerConfig) IsQuarantineSkipped() bool {
	return sc.SkipQuarantine
}

// EnsureAPIKey ensures the API key is set, generating one if needed
// Returns the API key, whether it was auto-generated, and the source
// SECURITY: Empty API keys are never allowed - always auto-generates if empty or missing
func (c *Config) EnsureAPIKey() (apiKey string, wasGenerated bool, source APIKeySource) {
	// Check environment variable for API key first - this overrides config file
	// Use LookupEnv to distinguish between "not set" and "set to empty string"
	if envAPIKey, exists := os.LookupEnv("MCPPROXY_API_KEY"); exists && envAPIKey != "" {
		c.APIKey = envAPIKey
		return c.APIKey, false, APIKeySourceEnvironment
	}

	// If API key was explicitly set in config and is non-empty, use it
	if c.apiKeyExplicitlySet && c.APIKey != "" {
		return c.APIKey, false, APIKeySourceConfig
	}

	// Generate a new API key if missing or empty (never allow empty for security)
	c.APIKey = generateAPIKey()
	c.apiKeyExplicitlySet = true
	return c.APIKey, true, APIKeySourceGenerated
}

// ValidationError represents a configuration validation error
type ValidationError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// Error implements the error interface
func (v ValidationError) Error() string {
	return fmt.Sprintf("%s: %s", v.Field, v.Message)
}

// ValidateDetailed performs detailed validation and returns all errors
func (c *Config) ValidateDetailed() []ValidationError {
	var errors []ValidationError

	// Validate listen address format
	if c.Listen != "" {
		// Check for valid format (host:port or :port)
		if !isValidListenAddr(c.Listen) {
			errors = append(errors, ValidationError{
				Field:   "listen",
				Message: "invalid listen address format (expected host:port or :port)",
			})
		}
	}

	// Validate ToolsLimit range
	if c.ToolsLimit < 1 || c.ToolsLimit > 1000 {
		errors = append(errors, ValidationError{
			Field:   "tools_limit",
			Message: "must be between 1 and 1000",
		})
	}

	// Validate ToolResponseLimit
	if c.ToolResponseLimit < 0 {
		errors = append(errors, ValidationError{
			Field:   "tool_response_limit",
			Message: "cannot be negative",
		})
	}

	// Validate timeout
	if c.CallToolTimeout.Duration() <= 0 {
		errors = append(errors, ValidationError{
			Field:   "call_tool_timeout",
			Message: "must be a positive duration",
		})
	}

	// Validate code execution configuration (0 means use default)
	if c.CodeExecutionTimeoutMs != 0 && (c.CodeExecutionTimeoutMs < 1 || c.CodeExecutionTimeoutMs > 600000) {
		errors = append(errors, ValidationError{
			Field:   "code_execution_timeout_ms",
			Message: "must be between 1 and 600000 milliseconds (or 0 for default)",
		})
	}

	if c.CodeExecutionMaxToolCalls < 0 {
		errors = append(errors, ValidationError{
			Field:   "code_execution_max_tool_calls",
			Message: "cannot be negative (0 means unlimited)",
		})
	}

	if c.CodeExecutionPoolSize != 0 && (c.CodeExecutionPoolSize < 1 || c.CodeExecutionPoolSize > 100) {
		errors = append(errors, ValidationError{
			Field:   "code_execution_pool_size",
			Message: "must be between 1 and 100 (or 0 for default)",
		})
	}

	// Validate routing mode (Spec 031)
	if c.RoutingMode != "" {
		validRoutingModes := map[string]bool{
			RoutingModeRetrieveTools: true,
			RoutingModeDirect:        true,
			RoutingModeCodeExecution: true,
		}
		if !validRoutingModes[c.RoutingMode] {
			errors = append(errors, ValidationError{
				Field:   "routing_mode",
				Message: fmt.Sprintf("invalid routing mode: %s (must be retrieve_tools, direct, or code_execution)", c.RoutingMode),
			})
		}
	}

	// Validate server configurations
	serverNames := make(map[string]bool)
	for i, server := range c.Servers {
		fieldPrefix := fmt.Sprintf("mcpServers[%d]", i)

		// Validate server name
		if server.Name == "" {
			errors = append(errors, ValidationError{
				Field:   fieldPrefix + ".name",
				Message: "server name is required",
			})
		} else if serverNames[server.Name] {
			errors = append(errors, ValidationError{
				Field:   fieldPrefix + ".name",
				Message: fmt.Sprintf("duplicate server name: %s", server.Name),
			})
		} else {
			serverNames[server.Name] = true
		}

		// Validate protocol
		validProtocols := map[string]bool{"stdio": true, "http": true, "sse": true, "streamable-http": true, "auto": true}
		if server.Protocol != "" && !validProtocols[server.Protocol] {
			errors = append(errors, ValidationError{
				Field:   fieldPrefix + ".protocol",
				Message: fmt.Sprintf("invalid protocol: %s (must be stdio, http, sse, streamable-http, or auto)", server.Protocol),
			})
		}

		// Validate stdio server requirements
		if server.Protocol == "stdio" || (server.Protocol == "" && server.Command != "") {
			if server.Command == "" {
				errors = append(errors, ValidationError{
					Field:   fieldPrefix + ".command",
					Message: "command is required for stdio protocol",
				})
			}
			// Validate working directory exists if specified
			if server.WorkingDir != "" {
				if _, err := os.Stat(server.WorkingDir); os.IsNotExist(err) {
					errors = append(errors, ValidationError{
						Field:   fieldPrefix + ".working_dir",
						Message: fmt.Sprintf("directory does not exist: %s", server.WorkingDir),
					})
				}
			}
		}

		// Validate HTTP server requirements
		if server.Protocol == "http" || server.Protocol == "sse" || server.Protocol == "streamable-http" {
			if server.URL == "" {
				errors = append(errors, ValidationError{
					Field:   fieldPrefix + ".url",
					Message: fmt.Sprintf("url is required for %s protocol", server.Protocol),
				})
			}
		}

		// Note: OAuth configuration is optional. client_id is optional (uses Dynamic Client Registration RFC 7591 if empty).
		// ClientSecret can be a secret reference, so we don't validate it as empty.
	}

	// Validate DataDir exists (if specified and not empty).
	// Skip validation if the path still contains unresolved ${...} refs —
	// it will be resolved at a later point or the user will fix the env var.
	if c.DataDir != "" && !strings.Contains(c.DataDir, "${") {
		if _, err := os.Stat(c.DataDir); os.IsNotExist(err) {
			errors = append(errors, ValidationError{
				Field:   "data_dir",
				Message: fmt.Sprintf("directory does not exist: %s", c.DataDir),
			})
		}
	}

	// Validate TLS configuration
	if c.TLS != nil && c.TLS.Enabled {
		if c.TLS.CertsDir != "" {
			if _, err := os.Stat(c.TLS.CertsDir); os.IsNotExist(err) {
				errors = append(errors, ValidationError{
					Field:   "tls.certs_dir",
					Message: fmt.Sprintf("directory does not exist: %s", c.TLS.CertsDir),
				})
			}
		}
	}

	// Validate logging configuration
	if c.Logging != nil {
		validLevels := map[string]bool{"trace": true, "debug": true, "info": true, "warn": true, "error": true}
		if c.Logging.Level != "" && !validLevels[c.Logging.Level] {
			errors = append(errors, ValidationError{
				Field:   "logging.level",
				Message: fmt.Sprintf("invalid log level: %s (must be trace, debug, info, warn, or error)", c.Logging.Level),
			})
		}
	}

	return errors
}

// isValidListenAddr checks if the listen address format is valid
func isValidListenAddr(addr string) bool {
	// Allow :port format
	if addr != "" && addr[0] == ':' {
		return true
	}
	// Allow host:port format (simple check)
	return addr != "" && (addr[0] != ':' || len(addr) > 1)
}

// Validate validates the configuration (backward compatible)
func (c *Config) Validate() error {
	// Apply defaults FIRST (non-validation logic)
	if c.Listen == "" {
		c.Listen = defaultPort
	}
	if c.ToolsLimit <= 0 {
		c.ToolsLimit = 15
	}
	if c.ToolResponseLimit < 0 {
		c.ToolResponseLimit = 0 // 0 means disabled
	}
	if c.CallToolTimeout.Duration() <= 0 {
		c.CallToolTimeout = Duration(2 * time.Minute) // Default to 2 minutes
	}
	// Apply code execution defaults
	if c.CodeExecutionTimeoutMs <= 0 {
		c.CodeExecutionTimeoutMs = 120000 // 2 minutes (120,000ms)
	}
	if c.CodeExecutionPoolSize <= 0 {
		c.CodeExecutionPoolSize = 10 // 10 JavaScript runtime instances
	}
	// CodeExecutionMaxToolCalls defaults to 0 (unlimited), which is valid

	// Apply routing mode default (Spec 031)
	if c.RoutingMode == "" {
		c.RoutingMode = RoutingModeRetrieveTools
	}

	// Then perform detailed validation
	errors := c.ValidateDetailed()
	if len(errors) > 0 {
		// Return first error for backward compatibility
		return fmt.Errorf("%s", errors[0].Error())
	}

	// Handle API key generation if not configured
	// Empty string means authentication disabled, nil means auto-generate
	if c.APIKey == "" {
		// Check environment variable for API key
		// Use LookupEnv to distinguish between "not set" and "set to empty string"
		if envAPIKey, exists := os.LookupEnv("MCPPROXY_API_KEY"); exists {
			c.APIKey = envAPIKey // Allow empty string to explicitly disable authentication
		}
	}

	// Ensure Environment config is not nil
	if c.Environment == nil {
		c.Environment = secureenv.DefaultEnvConfig()
	}

	// Ensure DockerIsolation config is not nil
	if c.DockerIsolation == nil {
		c.DockerIsolation = DefaultDockerIsolationConfig()
	}

	// Ensure TLS config is not nil
	if c.TLS == nil {
		c.TLS = &TLSConfig{
			Enabled:           false, // HTTPS disabled by default, can be enabled via config or env var
			RequireClientCert: false, // mTLS disabled by default
			CertsDir:          "",    // Will default to ${data_dir}/certs
			HSTS:              true,  // HSTS enabled by default
		}
	}

	// Ensure Tokenizer config is not nil
	if c.Tokenizer == nil {
		c.Tokenizer = &TokenizerConfig{
			Enabled:      true,          // Token counting enabled by default
			DefaultModel: "gpt-4",       // Default to GPT-4 tokenization
			Encoding:     "cl100k_base", // Default encoding (GPT-4, GPT-3.5)
		}
	}

	// Ensure IntentDeclaration config is not nil
	if c.IntentDeclaration == nil {
		c.IntentDeclaration = DefaultIntentDeclarationConfig()
	}

	return nil
}

// MarshalJSON implements json.Marshaler interface
func (c *Config) MarshalJSON() ([]byte, error) {
	type Alias Config
	return json.Marshal((*Alias)(c))
}

// UnmarshalJSON implements json.Unmarshaler interface
func (c *Config) UnmarshalJSON(data []byte) error {
	type Alias Config
	aux := &struct {
		*Alias
	}{
		Alias: (*Alias)(c),
	}
	return json.Unmarshal(data, aux)
}

// OAuthConfigChanged checks if OAuth configuration has changed between two configs.
// Returns true if any OAuth field differs (ClientID, Scopes, ExtraParams, etc.)
func OAuthConfigChanged(old, new *OAuthConfig) bool {
	// Both nil - no change
	if old == nil && new == nil {
		return false
	}

	// One nil, one not - changed
	if (old == nil) != (new == nil) {
		return true
	}

	// Compare all fields
	if old.ClientID != new.ClientID ||
		old.ClientSecret != new.ClientSecret ||
		old.RedirectURI != new.RedirectURI ||
		old.PKCEEnabled != new.PKCEEnabled {
		return true
	}

	// Compare scopes (order matters for OAuth)
	if len(old.Scopes) != len(new.Scopes) {
		return true
	}
	for i := range old.Scopes {
		if old.Scopes[i] != new.Scopes[i] {
			return true
		}
	}

	// Compare extra params
	if len(old.ExtraParams) != len(new.ExtraParams) {
		return true
	}
	for key, oldVal := range old.ExtraParams {
		newVal, exists := new.ExtraParams[key]
		if !exists || oldVal != newVal {
			return true
		}
	}

	return false
}

// TelemetryConfig controls anonymous usage telemetry (Spec 036, extended in Spec 042).
type TelemetryConfig struct {
	Enabled     *bool  `json:"enabled,omitempty" mapstructure:"enabled"`           // Default: true (opt-out)
	AnonymousID string `json:"anonymous_id,omitempty" mapstructure:"anonymous-id"` // Auto-generated UUIDv4
	Endpoint    string `json:"endpoint,omitempty" mapstructure:"endpoint"`         // Override for testing

	// Spec 042 (Tier 2) additions — all default-zero, all backwards-compatible.
	AnonymousIDCreatedAt string `json:"anonymous_id_created_at,omitempty" mapstructure:"anonymous-id-created-at"` // RFC3339; for annual rotation
	LastReportedVersion  string `json:"last_reported_version,omitempty" mapstructure:"last-reported-version"`     // Upgrade funnel
	LastStartupOutcome   string `json:"last_startup_outcome,omitempty" mapstructure:"last-startup-outcome"`       // success|port_conflict|db_locked|...
	NoticeShown          bool   `json:"notice_shown,omitempty" mapstructure:"notice-shown"`                       // First-run notice flag
}

// IsTelemetryEnabled returns whether telemetry is enabled.
// Respects MCPPROXY_TELEMETRY=false env var override and defaults to true.
func (c *Config) IsTelemetryEnabled() bool {
	if os.Getenv("MCPPROXY_TELEMETRY") == "false" {
		return false
	}
	if c.Telemetry == nil {
		return true // default enabled
	}
	if c.Telemetry.Enabled == nil {
		return true
	}
	return *c.Telemetry.Enabled
}

// GetTelemetryEndpoint returns the telemetry endpoint URL.
func (c *Config) GetTelemetryEndpoint() string {
	if c.Telemetry != nil && c.Telemetry.Endpoint != "" {
		return c.Telemetry.Endpoint
	}
	return "https://telemetry.mcpproxy.app/v1"
}

// GetAnonymousID returns the anonymous telemetry ID if set.
func (c *Config) GetAnonymousID() string {
	if c.Telemetry != nil && c.Telemetry.AnonymousID != "" {
		return c.Telemetry.AnonymousID
	}
	return ""
}

// SecurityConfig represents security scanner configuration (Spec 039)
type SecurityConfig struct {
	AutoScanQuarantined     bool     `json:"auto_scan_quarantined" mapstructure:"auto-scan-quarantined"`
	ScanTimeoutDefault      Duration `json:"scan_timeout_default,omitempty" mapstructure:"scan-timeout-default" swaggertype:"string"`
	IntegrityCheckInterval  Duration `json:"integrity_check_interval,omitempty" mapstructure:"integrity-check-interval" swaggertype:"string"`
	IntegrityCheckOnRestart bool     `json:"integrity_check_on_restart" mapstructure:"integrity-check-on-restart"`
	ScannerRegistryURL      string   `json:"scanner_registry_url,omitempty" mapstructure:"scanner-registry-url"`
	RuntimeReadOnly         bool     `json:"runtime_read_only" mapstructure:"runtime-read-only"`
	RuntimeTmpfsSize        string   `json:"runtime_tmpfs_size,omitempty" mapstructure:"runtime-tmpfs-size"`

	// ScannerDisableNoNewPrivileges, when true, omits the
	// `--security-opt no-new-privileges` flag from scanner container runs.
	//
	// Background: snap-installed Docker on Ubuntu confines dockerd under the
	// `snap.docker.dockerd` AppArmor profile. When runc tries to transition
	// the container into the inner `docker-default` profile to exec the
	// entrypoint, AppArmor refuses the transition because NO_NEW_PRIVS
	// forbids privilege/profile changes on exec — the result is EPERM
	// ("operation not permitted") and every scanner fails immediately.
	//
	// Set this to true ONLY on hosts hitting that incompatibility. Scanner
	// containers still run with read-only rootfs, tmpfs /tmp, no-network by
	// default, and read-only source mounts, so the marginal isolation loss
	// is small. The preferred fix remains replacing snap docker with a
	// distro-packaged docker.
	ScannerDisableNoNewPrivileges bool `json:"scanner_disable_no_new_privileges,omitempty" mapstructure:"scanner-disable-no-new-privileges"`
}
