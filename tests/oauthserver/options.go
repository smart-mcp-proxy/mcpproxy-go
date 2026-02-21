// Package oauthserver provides a test OAuth 2.1 server for mcpproxy E2E testing.
// It implements RFC 6749, 7636, 7591, 8414, 8628, 8707.
package oauthserver

import "time"

// DetectionMode controls how OAuth is advertised to clients.
type DetectionMode int

const (
	// Discovery serves /.well-known/oauth-authorization-server
	Discovery DetectionMode = iota

	// WWWAuthenticate returns 401 with WWW-Authenticate header on /protected
	WWWAuthenticate

	// Explicit provides no discovery; client must configure endpoints manually
	Explicit

	// Both serves discovery AND returns WWW-Authenticate
	Both
)

// ErrorMode configures error injection for testing error handling.
type ErrorMode struct {
	// Token endpoint errors
	TokenInvalidClient    bool          // Return `invalid_client` on token requests
	TokenInvalidGrant     bool          // Return `invalid_grant` on token requests
	TokenInvalidScope     bool          // Return `invalid_scope` on token requests
	TokenServerError      bool          // Return HTTP 500 on token requests
	TokenSlowResponse     time.Duration // Delay before token response
	TokenUnsupportedGrant bool          // Return `unsupported_grant_type`

	// Authorization endpoint errors
	AuthAccessDenied   bool // Return `error=access_denied` on authorize
	AuthInvalidRequest bool // Return `error=invalid_request` on authorize

	// DCR endpoint errors
	DCRInvalidRedirectURI bool // Reject registration with bad redirect
	DCRInvalidScope       bool // Reject registration with bad scope

	// Device code errors
	DeviceSlowPoll bool // Return `slow_down` on device polling
	DeviceExpired  bool // Return `expired_token` on device polling

	// MCP endpoint rate limiting (for resource auto-detection testing)
	MCPRateLimitCount      int  // Return 429 this many times before real response
	MCPRateLimitRetryAfter int  // Retry-After header value (seconds, 0 = omit header)
	MCPRateLimitUseResetAt bool // Use JSON body with reset_at instead of Retry-After header
}

// Options configures the OAuth test server behavior.
type Options struct {
	// Flow toggles (all true by default when using defaults)
	EnableAuthCode          bool
	EnableDeviceCode        bool
	EnableDCR               bool
	EnableClientCredentials bool
	EnableRefreshToken      bool

	// Token lifetimes
	AccessTokenExpiry  time.Duration // Default: 1 hour
	RefreshTokenExpiry time.Duration // Default: 24 hours
	AuthCodeExpiry     time.Duration // Default: 10 minutes
	DeviceCodeExpiry   time.Duration // Default: 5 minutes
	DeviceCodeInterval int           // Default: 5 seconds

	// Scopes
	DefaultScopes   []string // Default: ["read"]
	SupportedScopes []string // Default: ["read", "write", "admin"]

	// Security
	RequirePKCE              bool // Default: true
	RequireResourceIndicator bool // RFC 8707: Require resource parameter (default: false)

	// Compatibility modes
	RunlayerMode bool // Mimic Runlayer's strict validation with Pydantic-style 422 errors

	// Error injection
	ErrorMode ErrorMode

	// Detection mode
	DetectionMode DetectionMode // Default: Discovery

	// Test credentials
	ValidUsers map[string]string // Default: {"testuser": "testpass"}

	// Pre-registered clients (in addition to auto-generated test client)
	Clients []ClientConfig
}

// ClientConfig defines a pre-registered OAuth client.
type ClientConfig struct {
	ClientID      string
	ClientSecret  string   // Empty for public clients
	RedirectURIs  []string
	GrantTypes    []string // Default: ["authorization_code", "refresh_token"]
	ResponseTypes []string // Default: ["code"]
	Scopes        []string // Default: options.SupportedScopes
	ClientName    string
}

// DefaultOptions returns Options with sensible defaults for testing.
func DefaultOptions() Options {
	return Options{
		EnableAuthCode:          true,
		EnableDeviceCode:        true,
		EnableDCR:               true,
		EnableClientCredentials: true,
		EnableRefreshToken:      true,
		AccessTokenExpiry:       time.Hour,
		RefreshTokenExpiry:      24 * time.Hour,
		AuthCodeExpiry:          10 * time.Minute,
		DeviceCodeExpiry:        5 * time.Minute,
		DeviceCodeInterval:      5,
		DefaultScopes:           []string{"read"},
		SupportedScopes:         []string{"read", "write", "admin"},
		RequirePKCE:             true,
		DetectionMode:           Discovery,
		ValidUsers:              map[string]string{"testuser": "testpass"},
	}
}

// applyDefaults fills in zero values with defaults.
func (o *Options) applyDefaults() {
	defaults := DefaultOptions()

	if o.AccessTokenExpiry == 0 {
		o.AccessTokenExpiry = defaults.AccessTokenExpiry
	}
	if o.RefreshTokenExpiry == 0 {
		o.RefreshTokenExpiry = defaults.RefreshTokenExpiry
	}
	if o.AuthCodeExpiry == 0 {
		o.AuthCodeExpiry = defaults.AuthCodeExpiry
	}
	if o.DeviceCodeExpiry == 0 {
		o.DeviceCodeExpiry = defaults.DeviceCodeExpiry
	}
	if o.DeviceCodeInterval == 0 {
		o.DeviceCodeInterval = defaults.DeviceCodeInterval
	}
	if len(o.DefaultScopes) == 0 {
		o.DefaultScopes = defaults.DefaultScopes
	}
	if len(o.SupportedScopes) == 0 {
		o.SupportedScopes = defaults.SupportedScopes
	}
	if len(o.ValidUsers) == 0 {
		o.ValidUsers = defaults.ValidUsers
	}

	// Enable all flows by default if none explicitly set
	// (This is a heuristic: if all are false, enable defaults)
	if !o.EnableAuthCode && !o.EnableDeviceCode && !o.EnableDCR &&
		!o.EnableClientCredentials && !o.EnableRefreshToken {
		o.EnableAuthCode = defaults.EnableAuthCode
		o.EnableDeviceCode = defaults.EnableDeviceCode
		o.EnableDCR = defaults.EnableDCR
		o.EnableClientCredentials = defaults.EnableClientCredentials
		o.EnableRefreshToken = defaults.EnableRefreshToken
		o.RequirePKCE = defaults.RequirePKCE
	}
}
