package config

import (
	"encoding/json"
	"fmt"
	"mcpproxy-go/internal/secureenv"
	"time"
)

const (
	defaultPort = ":8080"
)

// Duration is a custom type that wraps time.Duration for JSON marshaling
type Duration time.Duration

// MarshalJSON implements json.Marshaler
func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

// UnmarshalJSON implements json.Unmarshaler
func (d *Duration) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	
	duration, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	
	*d = Duration(duration)
	return nil
}

// String returns the string representation of the duration
func (d Duration) String() string {
	return time.Duration(d).String()
}

// ToDuration converts Duration to time.Duration
func (d Duration) ToDuration() time.Duration {
	return time.Duration(d)
}

// OAuth flow types
const (
	OAuthFlowAuthorizationCode = "authorization_code"
	OAuthFlowDeviceCode        = "device_code"
	OAuthFlowAuto              = "auto"
)

// Deployment types
const (
	DeploymentTypeLocal    = "local"
	DeploymentTypeRemote   = "remote"
	DeploymentTypeHeadless = "headless"
	DeploymentTypeAuto     = "auto"
)

// OAuth connection states
const (
	ConnectionStateDisconnected = "disconnected"
	ConnectionStateConnecting   = "connecting"
	ConnectionStateConnected    = "connected"
	ConnectionStateOAuthPending = "oauth_pending"
	ConnectionStateFailed       = "failed"
)

// Notification methods
const (
	NotificationMethodTray    = "tray"
	NotificationMethodLog     = "log"
	NotificationMethodWebhook = "webhook"
	NotificationMethodEmail   = "email"
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

	// Deployment configuration
	PublicURL      string `json:"public_url,omitempty" mapstructure:"public-url"`           // For remote deployments
	DeploymentType string `json:"deployment_type,omitempty" mapstructure:"deployment-type"` // "local", "remote", "headless", "auto"
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

	// Connection timeout (e.g., "30s", "2m", "120s"). If not specified, defaults to 30s
	Timeout *Duration `json:"timeout,omitempty" mapstructure:"timeout"`

	// OAuth configuration
	OAuth *OAuthConfig `json:"oauth,omitempty" mapstructure:"oauth"`

	// Deployment-specific settings
	PublicURL      string `json:"public_url,omitempty" mapstructure:"public-url"`           // Override global public URL
	DeploymentType string `json:"deployment_type,omitempty" mapstructure:"deployment-type"` // Override global deployment type
}

// OAuthConfig represents OAuth configuration for upstream servers
type OAuthConfig struct {
	// OAuth flow type - "authorization_code", "device_code", or "auto"
	FlowType string `json:"flow_type,omitempty" mapstructure:"flow-type"`

	// OAuth endpoints (auto-discovered if not provided)
	AuthorizationEndpoint string `json:"authorization_endpoint,omitempty" mapstructure:"authorization-endpoint"`
	TokenEndpoint         string `json:"token_endpoint,omitempty" mapstructure:"token-endpoint"`
	DeviceEndpoint        string `json:"device_endpoint,omitempty" mapstructure:"device-endpoint"`
	RegistrationEndpoint  string `json:"registration_endpoint,omitempty" mapstructure:"registration-endpoint"`

	// Client credentials (for pre-registered clients)
	ClientID     string `json:"client_id,omitempty" mapstructure:"client-id"`
	ClientSecret string `json:"client_secret,omitempty" mapstructure:"client-secret"`

	// OAuth scopes
	Scopes []string `json:"scopes,omitempty" mapstructure:"scopes"`

	// PKCE settings
	UsePKCE bool `json:"use_pkce,omitempty" mapstructure:"use-pkce"` // Force PKCE usage

	// Redirect URI configuration
	RedirectURI  string   `json:"redirect_uri,omitempty" mapstructure:"redirect-uri"`   // Fixed redirect URI
	RedirectURIs []string `json:"redirect_uris,omitempty" mapstructure:"redirect-uris"` // Multiple redirect URIs

	// Flow preferences
	PreferDeviceFlow bool `json:"prefer_device_flow,omitempty" mapstructure:"prefer-device-flow"` // Force device code flow
	LazyAuth         bool `json:"lazy_auth,omitempty" mapstructure:"lazy-auth"`                   // Enable lazy OAuth (default: true)

	// Auto-discovery settings
	AutoDiscovery *OAuthAutoDiscovery `json:"auto_discovery,omitempty" mapstructure:"auto-discovery"`

	// Dynamic Client Registration settings
	DynamicClientRegistration *DCRConfig `json:"dynamic_client_registration,omitempty" mapstructure:"dynamic-client-registration"`

	// Token storage
	TokenStorage *TokenStorage `json:"token_storage,omitempty" mapstructure:"token-storage"`

	// Device flow specific settings
	DeviceFlow *DeviceFlowConfig `json:"device_flow,omitempty" mapstructure:"device-flow"`

	// Notification settings
	NotificationMethods []string `json:"notification_methods,omitempty" mapstructure:"notification-methods"` // ["tray", "log", "webhook", "email"]
	WebhookURL          string   `json:"webhook_url,omitempty" mapstructure:"webhook-url"`
	EmailNotification   string   `json:"email_notification,omitempty" mapstructure:"email-notification"`
}

// OAuthAutoDiscovery represents OAuth auto-discovery settings
type OAuthAutoDiscovery struct {
	// Enable automatic OAuth metadata discovery
	Enabled bool `json:"enabled,omitempty" mapstructure:"enabled"`

	// Custom metadata URL (if different from standard .well-known endpoints)
	MetadataURL string `json:"metadata_url,omitempty" mapstructure:"metadata-url"`

	// Prompt for client_id if not provided
	PromptForClientID bool `json:"prompt_for_client_id,omitempty" mapstructure:"prompt-for-client-id"`

	// Auto-select device flow for headless scenarios
	AutoDeviceFlow bool `json:"auto_device_flow,omitempty" mapstructure:"auto-device-flow"`

	// Enable Dynamic Client Registration
	EnableDCR bool `json:"enable_dcr,omitempty" mapstructure:"enable-dcr"`
}

// DCRConfig represents Dynamic Client Registration configuration
type DCRConfig struct {
	// Enable DCR
	Enabled bool `json:"enabled,omitempty" mapstructure:"enabled"`

	// Client metadata for registration
	ClientName    string   `json:"client_name,omitempty" mapstructure:"client-name"`
	ClientURI     string   `json:"client_uri,omitempty" mapstructure:"client-uri"`
	LogoURI       string   `json:"logo_uri,omitempty" mapstructure:"logo-uri"`
	TosURI        string   `json:"tos_uri,omitempty" mapstructure:"tos-uri"`
	PolicyURI     string   `json:"policy_uri,omitempty" mapstructure:"policy-uri"`
	Contacts      []string `json:"contacts,omitempty" mapstructure:"contacts"`
	GrantTypes    []string `json:"grant_types,omitempty" mapstructure:"grant-types"`
	ResponseTypes []string `json:"response_types,omitempty" mapstructure:"response-types"`
}

// TokenStorage represents stored OAuth tokens
type TokenStorage struct {
	AccessToken  string    `json:"access_token,omitempty" mapstructure:"access-token"`
	RefreshToken string    `json:"refresh_token,omitempty" mapstructure:"refresh-token"`
	ExpiresAt    time.Time `json:"expires_at,omitempty" mapstructure:"expires-at"`
	TokenType    string    `json:"token_type,omitempty" mapstructure:"token-type"`
	Scope        string    `json:"scope,omitempty" mapstructure:"scope"`
}

// DeviceFlowConfig represents device flow specific configuration
type DeviceFlowConfig struct {
	// Poll interval for device flow (default: 5 seconds)
	PollInterval time.Duration `json:"poll_interval,omitempty" mapstructure:"poll-interval"`

	// Device code expiration (default: 600 seconds)
	CodeExpiration time.Duration `json:"code_expiration,omitempty" mapstructure:"code-expiration"`

	// Enable notification to user (tray notification, etc.)
	EnableNotification bool `json:"enable_notification,omitempty" mapstructure:"enable-notification"`

	// Notification methods and endpoints
	NotificationMethods []string `json:"notification_methods,omitempty" mapstructure:"notification-methods"`
	WebhookURL          string   `json:"webhook_url,omitempty" mapstructure:"webhook-url"`
	EmailNotification   string   `json:"email_notification,omitempty" mapstructure:"email-notification"`
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
	Status      string    `json:"status,omitempty"` // "available", "oauth_pending", "failed"
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

// ToolStats represents tool usage statistics
type ToolStats struct {
	TotalTools int             `json:"total_tools"`
	TopTools   []ToolStatEntry `json:"top_tools"`
}

// ToolStatEntry represents a single tool usage statistic
type ToolStatEntry struct {
	ToolName string `json:"tool_name"`
	Count    uint64 `json:"count"`
}

// OAuth-related types for notifications and caching

// OAuthNotification represents an OAuth notification message
type OAuthNotification struct {
	ServerName      string        `json:"server_name"`
	VerificationURI string        `json:"verification_uri"`
	UserCode        string        `json:"user_code"`
	ExpiresIn       time.Duration `json:"expires_in"`
	Timestamp       time.Time     `json:"timestamp"`
	FlowType        string        `json:"flow_type"`
}

// ToolCache represents cached tool metadata
type ToolCache struct {
	Tools     []*ToolMetadata `json:"tools"`
	Timestamp time.Time       `json:"timestamp"`
	ExpiresAt time.Time       `json:"expires_at"`
	ServerID  string          `json:"server_id"`
}

// ClientRegistrationRequest represents a Dynamic Client Registration request
type ClientRegistrationRequest struct {
	ClientName               string   `json:"client_name"`
	ClientURI                string   `json:"client_uri,omitempty"`
	LogoURI                  string   `json:"logo_uri,omitempty"`
	TosURI                   string   `json:"tos_uri,omitempty"`
	PolicyURI                string   `json:"policy_uri,omitempty"`
	Contacts                 []string `json:"contacts,omitempty"`
	RedirectURIs             []string `json:"redirect_uris"`
	GrantTypes               []string `json:"grant_types"`
	ResponseTypes            []string `json:"response_types"`
	TokenEndpointAuthMethod  string   `json:"token_endpoint_auth_method"`
	Scope                    string   `json:"scope,omitempty"`
	ApplicationType          string   `json:"application_type,omitempty"`
	SubjectType              string   `json:"subject_type,omitempty"`
	IDTokenSignedResponseAlg string   `json:"id_token_signed_response_alg,omitempty"`
	JWKSUri                  string   `json:"jwks_uri,omitempty"`
	SoftwareID               string   `json:"software_id,omitempty"`
	SoftwareVersion          string   `json:"software_version,omitempty"`
}

// ClientRegistrationResponse represents a Dynamic Client Registration response
type ClientRegistrationResponse struct {
	ClientID                 string   `json:"client_id"`
	ClientSecret             string   `json:"client_secret,omitempty"`
	ClientSecretExpiresAt    int64    `json:"client_secret_expires_at,omitempty"`
	RegistrationAccessToken  string   `json:"registration_access_token,omitempty"`
	RegistrationClientURI    string   `json:"registration_client_uri,omitempty"`
	ClientName               string   `json:"client_name,omitempty"`
	ClientURI                string   `json:"client_uri,omitempty"`
	LogoURI                  string   `json:"logo_uri,omitempty"`
	TosURI                   string   `json:"tos_uri,omitempty"`
	PolicyURI                string   `json:"policy_uri,omitempty"`
	Contacts                 []string `json:"contacts,omitempty"`
	RedirectURIs             []string `json:"redirect_uris"`
	GrantTypes               []string `json:"grant_types"`
	ResponseTypes            []string `json:"response_types"`
	TokenEndpointAuthMethod  string   `json:"token_endpoint_auth_method"`
	Scope                    string   `json:"scope,omitempty"`
	ApplicationType          string   `json:"application_type,omitempty"`
	SubjectType              string   `json:"subject_type,omitempty"`
	IDTokenSignedResponseAlg string   `json:"id_token_signed_response_alg,omitempty"`
	JWKSUri                  string   `json:"jwks_uri,omitempty"`
	SoftwareID               string   `json:"software_id,omitempty"`
	SoftwareVersion          string   `json:"software_version,omitempty"`
}

// OAuthServerMetadata represents OAuth server metadata from .well-known endpoints
type OAuthServerMetadata struct {
	Issuer                                    string   `json:"issuer"`
	AuthorizationEndpoint                     string   `json:"authorization_endpoint"`
	TokenEndpoint                             string   `json:"token_endpoint"`
	DeviceEndpoint                            string   `json:"device_authorization_endpoint,omitempty"`
	RegistrationEndpoint                      string   `json:"registration_endpoint,omitempty"`
	JWKSUri                                   string   `json:"jwks_uri,omitempty"`
	ResponseTypesSupported                    []string `json:"response_types_supported,omitempty"`
	SubjectTypesSupported                     []string `json:"subject_types_supported,omitempty"`
	IDTokenSigningAlgValuesSupported          []string `json:"id_token_signing_alg_values_supported,omitempty"`
	ScopesSupported                           []string `json:"scopes_supported,omitempty"`
	TokenEndpointAuthMethodsSupported         []string `json:"token_endpoint_auth_methods_supported,omitempty"`
	ClaimsSupported                           []string `json:"claims_supported,omitempty"`
	CodeChallengeMethodsSupported             []string `json:"code_challenge_methods_supported,omitempty"`
	GrantTypesSupported                       []string `json:"grant_types_supported,omitempty"`
	RevocationEndpoint                        string   `json:"revocation_endpoint,omitempty"`
	RevocationEndpointAuthMethodsSupported    []string `json:"revocation_endpoint_auth_methods_supported,omitempty"`
	IntrospectionEndpoint                     string   `json:"introspection_endpoint,omitempty"`
	IntrospectionEndpointAuthMethodsSupported []string `json:"introspection_endpoint_auth_methods_supported,omitempty"`
	PkceRequired                              bool     `json:"require_pushed_authorization_requests,omitempty"`
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

		// Deployment defaults
		DeploymentType: DeploymentTypeAuto, // Auto-detect deployment type
		PublicURL:      "",                 // Will be auto-detected if needed
	}
}

// DefaultOAuthConfig returns a default OAuth configuration
func DefaultOAuthConfig() *OAuthConfig {
	return &OAuthConfig{
		FlowType:         OAuthFlowAuto, // Auto-select flow type
		UsePKCE:          true,          // Always use PKCE for security
		PreferDeviceFlow: false,         // Don't force device flow
		LazyAuth:         false,         // Disable lazy OAuth by default
		AutoDiscovery: &OAuthAutoDiscovery{
			Enabled:           true,  // Enable auto-discovery
			PromptForClientID: false, // Don't prompt by default
			AutoDeviceFlow:    true,  // Auto-select device flow for headless
			EnableDCR:         true,  // Enable DCR by default
		},
		DynamicClientRegistration: &DCRConfig{
			Enabled:       true,                                            // Enable DCR
			ClientName:    "mcpproxy",                                      // Default client name
			ClientURI:     "https://github.com/your-username/mcpproxy-go",  // Default client URI
			GrantTypes:    []string{"authorization_code", "refresh_token"}, // Standard grant types
			ResponseTypes: []string{"code"},                                // Standard response types
			Contacts:      []string{"admin@example.com"},                   // Default contact
		},
		DeviceFlow: &DeviceFlowConfig{
			PollInterval:        5 * time.Second,                                         // 5 seconds
			CodeExpiration:      10 * time.Minute,                                        // 10 minutes
			EnableNotification:  true,                                                    // Enable notifications
			NotificationMethods: []string{NotificationMethodTray, NotificationMethodLog}, // Default methods
		},
		NotificationMethods: []string{NotificationMethodTray, NotificationMethodLog}, // Default notification methods
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

	// Validate server configurations
	for _, server := range c.Servers {
		if err := c.validateServerConfig(server); err != nil {
			return fmt.Errorf("invalid server config for '%s': %w", server.Name, err)
		}
	}

	// Ensure Environment config is not nil
	if c.Environment == nil {
		c.Environment = secureenv.DefaultEnvConfig()
	}

	return nil
}

// validateServerConfig validates a single server configuration
func (c *Config) validateServerConfig(server *ServerConfig) error {
	// Validate timeout if specified
	if server.Timeout != nil {
		timeout := server.Timeout.ToDuration()
		
		// Minimum timeout: 5 seconds
		if timeout < 5*time.Second {
			return fmt.Errorf("timeout must be at least 5 seconds, got %v", timeout)
		}
		
		// Maximum timeout: 10 minutes
		if timeout > 10*time.Minute {
			return fmt.Errorf("timeout must be at most 10 minutes, got %v", timeout)
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
