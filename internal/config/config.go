package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"mcpproxy-go/internal/secureenv"
	"os"
	"time"
)

const (
	defaultPort = "127.0.0.1:8080" // Localhost-only binding by default for security
)

// Duration is a wrapper around time.Duration that can be marshaled to/from JSON
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
	Listen            string          `json:"listen" mapstructure:"listen"`
	TrayEndpoint      string          `json:"tray_endpoint,omitempty" mapstructure:"tray-endpoint"`       // Tray endpoint override (unix:// or npipe://)
	EnableSocket      bool            `json:"enable_socket" mapstructure:"enable-socket"`                 // Enable Unix socket/named pipe for local IPC (default: true)
	DataDir           string          `json:"data_dir" mapstructure:"data-dir"`
	EnableTray        bool            `json:"enable_tray" mapstructure:"tray"`
	DebugSearch       bool            `json:"debug_search" mapstructure:"debug-search"`
	Servers           []*ServerConfig `json:"mcpServers" mapstructure:"servers"`
	TopK              int             `json:"top_k" mapstructure:"top-k"`
	ToolsLimit        int             `json:"tools_limit" mapstructure:"tools-limit"`
	ToolResponseLimit int             `json:"tool_response_limit" mapstructure:"tool-response-limit"`
	CallToolTimeout   Duration        `json:"call_tool_timeout" mapstructure:"call-tool-timeout"`

	// Environment configuration for secure variable filtering
	Environment *secureenv.EnvConfig `json:"environment,omitempty" mapstructure:"environment"`

	// Logging configuration
	Logging *LogConfig `json:"logging,omitempty" mapstructure:"logging"`

	// Security settings
	APIKey            string `json:"api_key,omitempty" mapstructure:"api-key"` // API key for REST API authentication
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

	// Registries configuration for MCP server discovery
	Registries []RegistryEntry `json:"registries,omitempty" mapstructure:"registries"`

	// Feature flags for modular functionality
	Features *FeatureFlags `json:"features,omitempty" mapstructure:"features"`

	// TLS configuration
	TLS *TLSConfig `json:"tls,omitempty" mapstructure:"tls"`

	// Tokenizer configuration for token counting
	Tokenizer *TokenizerConfig `json:"tokenizer,omitempty" mapstructure:"tokenizer"`
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
	Name        string            `json:"name,omitempty" mapstructure:"name"`
	URL         string            `json:"url,omitempty" mapstructure:"url"`
	Protocol    string            `json:"protocol,omitempty" mapstructure:"protocol"` // stdio, http, sse, streamable-http, auto
	Command     string            `json:"command,omitempty" mapstructure:"command"`
	Args        []string          `json:"args,omitempty" mapstructure:"args"`
	WorkingDir  string            `json:"working_dir,omitempty" mapstructure:"working_dir"` // Working directory for stdio servers
	Env         map[string]string `json:"env,omitempty" mapstructure:"env"`
	Headers     map[string]string `json:"headers,omitempty" mapstructure:"headers"` // For HTTP servers
	OAuth       *OAuthConfig      `json:"oauth,omitempty" mapstructure:"oauth"`     // OAuth configuration
	Enabled     bool              `json:"enabled" mapstructure:"enabled"`
	Quarantined bool              `json:"quarantined" mapstructure:"quarantined"` // Security quarantine status
	Created     time.Time         `json:"created" mapstructure:"created"`
	Updated     time.Time         `json:"updated,omitempty" mapstructure:"updated"`
	Isolation   *IsolationConfig  `json:"isolation,omitempty" mapstructure:"isolation"` // Per-server isolation settings
}

// OAuthConfig represents OAuth configuration for a server
type OAuthConfig struct {
	ClientID     string   `json:"client_id,omitempty" mapstructure:"client_id"`
	ClientSecret string   `json:"client_secret,omitempty" mapstructure:"client_secret"`
	RedirectURI  string   `json:"redirect_uri,omitempty" mapstructure:"redirect_uri"`
	Scopes       []string `json:"scopes,omitempty" mapstructure:"scopes"`
	PKCEEnabled  bool     `json:"pkce_enabled,omitempty" mapstructure:"pkce_enabled"`
}

// DockerIsolationConfig represents global Docker isolation settings
type DockerIsolationConfig struct {
	Enabled       bool              `json:"enabled" mapstructure:"enabled"`                       // Global enable/disable for Docker isolation
	DefaultImages map[string]string `json:"default_images" mapstructure:"default_images"`         // Map of runtime type to Docker image
	Registry      string            `json:"registry,omitempty" mapstructure:"registry"`           // Custom registry (defaults to docker.io)
	NetworkMode   string            `json:"network_mode,omitempty" mapstructure:"network_mode"`   // Docker network mode (default: bridge)
	MemoryLimit   string            `json:"memory_limit,omitempty" mapstructure:"memory_limit"`   // Memory limit for containers
	CPULimit      string            `json:"cpu_limit,omitempty" mapstructure:"cpu_limit"`         // CPU limit for containers
	Timeout       Duration          `json:"timeout,omitempty" mapstructure:"timeout"`             // Container startup timeout
	ExtraArgs     []string          `json:"extra_args,omitempty" mapstructure:"extra_args"`       // Additional docker run arguments
	LogDriver     string            `json:"log_driver,omitempty" mapstructure:"log_driver"`       // Docker log driver (default: json-file)
	LogMaxSize    string            `json:"log_max_size,omitempty" mapstructure:"log_max_size"`   // Maximum size of log files (default: 100m)
	LogMaxFiles   string            `json:"log_max_files,omitempty" mapstructure:"log_max_files"` // Maximum number of log files (default: 3)
}

// IsolationConfig represents per-server isolation settings
type IsolationConfig struct {
	Enabled     bool     `json:"enabled" mapstructure:"enabled"`                       // Enable Docker isolation for this server
	Image       string   `json:"image,omitempty" mapstructure:"image"`                 // Custom Docker image (overrides default)
	NetworkMode string   `json:"network_mode,omitempty" mapstructure:"network_mode"`   // Custom network mode for this server
	ExtraArgs   []string `json:"extra_args,omitempty" mapstructure:"extra_args"`       // Additional docker run arguments for this server
	WorkingDir  string   `json:"working_dir,omitempty" mapstructure:"working_dir"`     // Custom working directory in container
	LogDriver   string   `json:"log_driver,omitempty" mapstructure:"log_driver"`       // Docker log driver override for this server
	LogMaxSize  string   `json:"log_max_size,omitempty" mapstructure:"log_max_size"`   // Maximum size of log files override
	LogMaxFiles string   `json:"log_max_files,omitempty" mapstructure:"log_max_files"` // Maximum number of log files override
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
	Count       interface{} `json:"count,omitempty"` // number or string
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

// DefaultDockerIsolationConfig returns default Docker isolation configuration
func DefaultDockerIsolationConfig() *DockerIsolationConfig {
	return &DockerIsolationConfig{
		Enabled: false, // Disabled by default for backward compatibility
		DefaultImages: map[string]string{
			// Python environments - using full images for Git and build tool support
			"python":  "python:3.11",
			"python3": "python:3.11",
			"uvx":     "python:3.11", // Full image needed for git+https:// installs
			"pip":     "python:3.11",
			"pipx":    "python:3.11",

			// Node.js environments - using full images for Git and native module support
			"node": "node:20",
			"npm":  "node:20",
			"npx":  "node:20", // Full image needed for git dependencies and native modules
			"yarn": "node:20",

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
		Listen:            defaultPort,
		EnableSocket:      true, // Enable Unix socket/named pipe by default for local IPC
		DataDir:           "", // Will be set to ~/.mcpproxy by loader
		EnableTray:        true,
		DebugSearch:       false,
		Servers:           []*ServerConfig{},
		TopK:              5,
		ToolsLimit:        15,
		ToolResponseLimit: 20000,                     // Default 20000 characters
		CallToolTimeout:   Duration(2 * time.Minute), // Default 2 minutes for tool calls

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

// EnsureAPIKey ensures the API key is set, generating one if needed
// Returns the API key, whether it was auto-generated, and the source
func (c *Config) EnsureAPIKey() (apiKey string, wasGenerated bool, source APIKeySource) {
	// Check environment variable for API key first - this overrides config file
	// Use LookupEnv to distinguish between "not set" and "set to empty string"
	if envAPIKey, exists := os.LookupEnv("MCPPROXY_API_KEY"); exists {
		c.APIKey = envAPIKey // Allow empty string to explicitly disable authentication
		return c.APIKey, false, APIKeySourceEnvironment
	}

	// If API key was explicitly set in config (including empty string), respect it
	if c.apiKeyExplicitlySet {
		return c.APIKey, false, APIKeySourceConfig // User-provided or explicitly disabled
	}

	// Generate a new API key only if not explicitly set
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

	// Validate TopK range
	if c.TopK < 1 || c.TopK > 100 {
		errors = append(errors, ValidationError{
			Field:   "top_k",
			Message: "must be between 1 and 100",
		})
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

		// Validate OAuth configuration if present
		if server.OAuth != nil {
			oauthPrefix := fieldPrefix + ".oauth"
			if server.OAuth.ClientID == "" {
				errors = append(errors, ValidationError{
					Field:   oauthPrefix + ".client_id",
					Message: "client_id is required when oauth is configured",
				})
			}
			// Note: ClientSecret can be a secret reference, so we don't validate it as empty
		}
	}

	// Validate DataDir exists (if specified and not empty)
	if c.DataDir != "" {
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
	if c.TopK <= 0 {
		c.TopK = 5
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

	// Ensure Features config is not nil and validate dependencies
	if c.Features == nil {
		flags := DefaultFeatureFlags()
		c.Features = &flags
	} else {
		if err := c.Features.ValidateFeatureFlags(); err != nil {
			return fmt.Errorf("feature flag validation failed: %w", err)
		}
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
