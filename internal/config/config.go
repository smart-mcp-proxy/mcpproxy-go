package config

import (
	"encoding/json"
	"mcpproxy-go/internal/secureenv"
	"time"
)

const (
	defaultPort = ":8080"
)

// Config represents the main configuration structure
type Config struct {
	Listen            string          `json:"listen" mapstructure:"listen"`
	DataDir           string          `json:"data_dir" mapstructure:"data-dir"`
	EnableTray        bool            `json:"enable_tray" mapstructure:"tray"`
	DebugSearch       bool            `json:"debug_search" mapstructure:"debug-search"`
	Servers           []*ServerConfig `json:"mcpServers" mapstructure:"servers"`
	TopK              int             `json:"top_k" mapstructure:"top-k"`
	ToolsLimit        int             `json:"tools_limit" mapstructure:"tools-limit"`
	ToolResponseLimit int             `json:"tool_response_limit" mapstructure:"tool-response-limit"`

	// Environment configuration for secure variable filtering
	Environment *secureenv.EnvConfig `json:"environment,omitempty" mapstructure:"environment"`

	// Logging configuration
	Logging *LogConfig `json:"logging,omitempty" mapstructure:"logging"`

	// Security settings
	ReadOnlyMode      bool `json:"read_only_mode" mapstructure:"read-only-mode"`
	DisableManagement bool `json:"disable_management" mapstructure:"disable-management"`
	AllowServerAdd    bool `json:"allow_server_add" mapstructure:"allow-server-add"`
	AllowServerRemove bool `json:"allow_server_remove" mapstructure:"allow-server-remove"`

	// Prompts settings
	EnablePrompts bool `json:"enable_prompts" mapstructure:"enable-prompts"`
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
	Name        string            `json:"name,omitempty" mapstructure:"name"`
	URL         string            `json:"url,omitempty" mapstructure:"url"`
	Protocol    string            `json:"protocol,omitempty" mapstructure:"protocol"` // stdio, http, sse, streamable-http, auto
	Command     string            `json:"command,omitempty" mapstructure:"command"`
	Args        []string          `json:"args,omitempty" mapstructure:"args"`
	Env         map[string]string `json:"env,omitempty" mapstructure:"env"`
	Headers     map[string]string `json:"headers,omitempty" mapstructure:"headers"` // For HTTP servers
	Enabled     bool              `json:"enabled" mapstructure:"enabled"`
	Quarantined bool              `json:"quarantined" mapstructure:"quarantined"` // Security quarantine status
	Created     time.Time         `json:"created" mapstructure:"created"`
	Updated     time.Time         `json:"updated,omitempty" mapstructure:"updated"`

	// OAuth configuration
	OAuth *OAuthConfig `json:"oauth,omitempty" mapstructure:"oauth"`
}

// OAuthConfig represents OAuth configuration for upstream servers
type OAuthConfig struct {
	// OAuth flow type - "authorization_code" or "device_code"
	FlowType string `json:"flow_type,omitempty" mapstructure:"flow-type"`

	// OAuth endpoints (auto-discovered if not provided)
	AuthorizationEndpoint string `json:"authorization_endpoint,omitempty" mapstructure:"authorization-endpoint"`
	TokenEndpoint         string `json:"token_endpoint,omitempty" mapstructure:"token-endpoint"`
	DeviceEndpoint        string `json:"device_endpoint,omitempty" mapstructure:"device-endpoint"`

	// Client credentials (for pre-registered clients)
	ClientID     string `json:"client_id,omitempty" mapstructure:"client-id"`
	ClientSecret string `json:"client_secret,omitempty" mapstructure:"client-secret"`

	// OAuth scopes
	Scopes []string `json:"scopes,omitempty" mapstructure:"scopes"`

	// Token storage
	TokenStorage *TokenStorage `json:"token_storage,omitempty" mapstructure:"token-storage"`

	// Device flow specific settings
	DeviceFlow *DeviceFlowConfig `json:"device_flow,omitempty" mapstructure:"device-flow"`
}

// TokenStorage represents stored OAuth tokens
type TokenStorage struct {
	AccessToken  string    `json:"access_token,omitempty" mapstructure:"access-token"`
	RefreshToken string    `json:"refresh_token,omitempty" mapstructure:"refresh-token"`
	ExpiresAt    time.Time `json:"expires_at,omitempty" mapstructure:"expires-at"`
	TokenType    string    `json:"token_type,omitempty" mapstructure:"token-type"`
}

// DeviceFlowConfig represents device flow specific configuration
type DeviceFlowConfig struct {
	// Poll interval for device flow (default: 5 seconds)
	PollInterval time.Duration `json:"poll_interval,omitempty" mapstructure:"poll-interval"`

	// Device code expiration (default: 600 seconds)
	CodeExpiration time.Duration `json:"code_expiration,omitempty" mapstructure:"code-expiration"`

	// Enable notification to user (tray notification, etc.)
	EnableNotification bool `json:"enable_notification,omitempty" mapstructure:"enable-notification"`
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
	Name        string    `json:"name"`
	ServerName  string    `json:"server_name"`
	Description string    `json:"description"`
	ParamsJSON  string    `json:"params_json"`
	Hash        string    `json:"hash"`
	Created     time.Time `json:"created"`
	Updated     time.Time `json:"updated"`
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

// DefaultConfig returns a default configuration
func DefaultConfig() *Config {
	return &Config{
		Listen:            defaultPort,
		DataDir:           "", // Will be set to ~/.mcpproxy by loader
		EnableTray:        true,
		DebugSearch:       false,
		Servers:           []*ServerConfig{},
		TopK:              5,
		ToolsLimit:        15,
		ToolResponseLimit: 20000, // Default 20000 characters

		// Default secure environment configuration
		Environment: secureenv.DefaultEnvConfig(),

		// Default logging configuration
		Logging: &LogConfig{
			Level:         "info",
			EnableFile:    true,
			EnableConsole: true,
			Filename:      "main.log",
			MaxSize:       10, // 10MB
			MaxBackups:    5,  // 5 backup files
			MaxAge:        30, // 30 days
			Compress:      true,
			JSONFormat:    false, // Use console format for readability
		},

		// Security defaults - permissive by default for compatibility
		ReadOnlyMode:      false,
		DisableManagement: false,
		AllowServerAdd:    true,
		AllowServerRemove: true,

		// Prompts enabled by default
		EnablePrompts: true,
	}
}

// Validate validates the configuration
func (c *Config) Validate() error {
	if c.Listen == "" {
		c.Listen = defaultPort
	}
	if c.TopK <= 0 {
		c.TopK = 5
	}
	if c.ToolsLimit <= 0 {
		c.ToolsLimit = 15
	}
	if c.ToolResponseLimit < 0 {
		c.ToolResponseLimit = 0 // 0 means disabled
	}

	// Ensure Environment config is not nil
	if c.Environment == nil {
		c.Environment = secureenv.DefaultEnvConfig()
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
