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

	// OIDC tamper knobs (Spec 107 research D2, one per T037 network/integration
	// case). Each takes effect only when Options.OIDC is on and produces exactly
	// the defect its name states; everything else about the flow stays valid so
	// a verifier's refusal is attributable to that one check.
	IDTokenBadSignature   bool // id_token signed by a key that is NOT in the JWKS (kid still names a published key)
	IDTokenWrongIssuer    bool // id_token `iss` is another origin
	IDTokenWrongAudience  bool // id_token `aud` is another client_id
	IDTokenMultiAudNoAzp  bool // id_token `aud` has two members and no `azp`
	IDTokenAzpNonString   bool // id_token `azp` is present but not a string (a number)
	IDTokenExpired        bool // id_token `exp` one hour in the past
	IDTokenNbfFuture      bool // id_token `nbf` one hour in the future
	IDTokenNoNonce        bool // id_token omits `nonce`
	IDTokenAlgNone        bool // id_token is an unsigned `alg: none` JWT
	IDTokenHS256          bool // id_token is HMAC-signed (HS256) with the client secret
	IDTokenUnknownKid     bool // id_token header `kid` names a key that is not in the JWKS
	IDTokenKeyAlgMismatch bool // id_token header says ES256 (signed by the EC key) but `kid` names the RSA key
	EmailVerifiedFalse    bool // `email_verified: false` in the id_token and userinfo

	// Groups-claim knobs (apply to the id_token and to /userinfo alike).
	GroupsNonArray         bool // groups claim is a string, not an array
	GroupsOverageMarker    bool // groups claim absent; Entra `_claim_names`/`_claim_sources` overage marker present
	GroupsAbsentEverywhere bool // groups claim absent from the id_token and from /userinfo

	// Userinfo knobs. Each also strips the groups claim from the id_token so a
	// verifier that only consults /userinfo when the token lacks groups is
	// forced onto the endpoint.
	UserinfoSubMismatch bool // /userinfo answers with a different `sub`
	UserinfoRedirect    bool // /userinfo answers 302 to another origin
	UserinfoNonJSON     bool // /userinfo answers 200 with a non-JSON body
	UserinfoUnavailable bool // /userinfo answers 503

	// Provider-metadata / token-endpoint knobs (FR-020 hostile discovery).
	DiscoveryHTTPTokenEndpoint bool // discovery advertises a plain-http, non-loopback token_endpoint
	TokenEndpointRedirect      bool // /token answers 302 to another origin instead of a token response
}

// userinfoConsulted reports whether a knob needs the id_token to lack the
// groups claim so that the verifier is driven onto /userinfo.
func (e ErrorMode) userinfoConsulted() bool {
	return e.UserinfoSubMismatch || e.UserinfoRedirect || e.UserinfoNonJSON || e.UserinfoUnavailable
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

	// MCPPerMethodAuth makes /mcp authorise per JSON-RPC method the way
	// Google's Gmail MCP endpoint does (GH #1271): initialize, ping,
	// notifications/* and tools/list answer anonymously, tools/call (and every
	// other request) still requires a valid bearer token. Default (false):
	// every request to /mcp requires a token.
	MCPPerMethodAuth bool

	// Test credentials
	ValidUsers map[string]string // Default: {"testuser": "testpass"}

	// OIDC turns the fake into an OpenID Provider (Spec 107, research D2):
	// discovery advertises userinfo_endpoint and
	// id_token_signing_alg_values_supported, the auth-code token response
	// carries an RS256 id_token signed by the KeyRing, /userinfo answers for a
	// valid access token, the JWKS also publishes an EC (P-256) key, and the
	// authorize request's nonce is echoed in the id_token. Default: off, in
	// which case none of this exists and the server behaves as before.
	OIDC bool

	// UserClaims holds the identity claims per ValidUsers entry (keyed by
	// username): sub, email, email_verified, name and the groups claim (under
	// GroupsClaim, or "groups"). Missing entries and missing keys are derived:
	// sub from the username, email = username (or username@example.com),
	// email_verified = true, name = the local part, groups = [].
	UserClaims map[string]map[string]any

	// ClientRedirectURIs are appended to the redirect URIs of both
	// pre-registered test clients (e.g. mcpproxy's /api/v1/auth/callback).
	ClientRedirectURIs []string

	// GroupsClaim is the name of the groups claim in the id_token and
	// /userinfo. Default: "groups".
	GroupsClaim string

	// Pre-registered clients (in addition to auto-generated test client)
	Clients []ClientConfig
}

// ClientConfig defines a pre-registered OAuth client.
type ClientConfig struct {
	ClientID      string
	ClientSecret  string // Empty for public clients
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
	if o.OIDC {
		if o.GroupsClaim == "" {
			o.GroupsClaim = "groups"
		}
		for _, sc := range []string{"openid", "profile", "email", "groups"} {
			if !containsString(o.SupportedScopes, sc) {
				o.SupportedScopes = append(o.SupportedScopes, sc)
			}
		}
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

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
