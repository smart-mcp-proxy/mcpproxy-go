package oauthserver

import "time"

// DeviceCodeStatus represents the state of a device code.
type DeviceCodeStatus int

const (
	// Pending - awaiting user action
	Pending DeviceCodeStatus = iota
	// Approved - user approved the request
	Approved
	// Denied - user denied the request
	Denied
	// Expired - code has expired
	Expired
)

// Client represents a registered OAuth client.
type Client struct {
	ClientID      string
	ClientSecret  string // Empty for public clients
	RedirectURIs  []string
	GrantTypes    []string
	ResponseTypes []string
	Scopes        []string
	ClientName    string
	IsPublic      bool
	CreatedAt     time.Time
}

// AuthorizationCode represents an ephemeral code issued during authorization flow.
type AuthorizationCode struct {
	Code                string
	ClientID            string
	RedirectURI         string
	Scopes              []string
	CodeChallenge       string
	CodeChallengeMethod string
	Resource            string // RFC 8707 resource indicator
	State               string
	Subject             string // Username who authorized
	ExpiresAt           time.Time
	Used                bool
}

// IsExpired checks if the authorization code has expired.
func (ac *AuthorizationCode) IsExpired() bool {
	return time.Now().After(ac.ExpiresAt)
}

// DeviceCode represents a device authorization code for device flow.
type DeviceCode struct {
	DeviceCode              string
	UserCode                string
	ClientID                string
	Scopes                  []string
	Resource                string // RFC 8707 resource indicator
	VerificationURI         string
	VerificationURIComplete string
	ExpiresAt               time.Time
	Interval                int
	Status                  DeviceCodeStatus
	ApprovedScopes          []string // Scopes approved by user (if approved)
	Subject                 string   // Username who approved (if approved)
}

// IsExpired checks if the device code has expired.
func (dc *DeviceCode) IsExpired() bool {
	return time.Now().After(dc.ExpiresAt)
}

// TokenResponse represents a successful token response.
type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token,omitempty"`
	Scope        string `json:"scope,omitempty"`
}

// TokenErrorResponse represents a token error response.
type TokenErrorResponse struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description,omitempty"`
	ErrorURI         string `json:"error_uri,omitempty"`
}

// DiscoveryMetadata represents OAuth 2.0 Authorization Server Metadata (RFC 8414).
type DiscoveryMetadata struct {
	Issuer                            string   `json:"issuer"`
	AuthorizationEndpoint             string   `json:"authorization_endpoint"`
	TokenEndpoint                     string   `json:"token_endpoint"`
	JWKSURI                           string   `json:"jwks_uri"`
	RegistrationEndpoint              string   `json:"registration_endpoint,omitempty"`
	DeviceAuthorizationEndpoint       string   `json:"device_authorization_endpoint,omitempty"`
	ScopesSupported                   []string `json:"scopes_supported"`
	ResponseTypesSupported            []string `json:"response_types_supported"`
	GrantTypesSupported               []string `json:"grant_types_supported"`
	CodeChallengeMethodsSupported     []string `json:"code_challenge_methods_supported"`
	TokenEndpointAuthMethodsSupported []string `json:"token_endpoint_auth_methods_supported"`
}

// DeviceAuthorizationResponse represents the response from the device authorization endpoint.
type DeviceAuthorizationResponse struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete,omitempty"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval,omitempty"`
}

// ClientRegistrationRequest represents a DCR request.
type ClientRegistrationRequest struct {
	RedirectURIs              []string `json:"redirect_uris"`
	GrantTypes                []string `json:"grant_types,omitempty"`
	ResponseTypes             []string `json:"response_types,omitempty"`
	ClientName                string   `json:"client_name,omitempty"`
	Scope                     string   `json:"scope,omitempty"`
	TokenEndpointAuthMethod   string   `json:"token_endpoint_auth_method,omitempty"`
}

// ClientRegistrationResponse represents a successful DCR response.
type ClientRegistrationResponse struct {
	ClientID                string   `json:"client_id"`
	ClientSecret            string   `json:"client_secret,omitempty"`
	ClientIDIssuedAt        int64    `json:"client_id_issued_at,omitempty"`
	ClientSecretExpiresAt   int64    `json:"client_secret_expires_at,omitempty"`
	RedirectURIs            []string `json:"redirect_uris"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
	ClientName              string   `json:"client_name,omitempty"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
}

// OAuthError represents a generic OAuth error.
type OAuthError struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description,omitempty"`
}

// RefreshTokenData stores refresh token information.
type RefreshTokenData struct {
	Token     string
	ClientID  string
	Subject   string
	Scopes    []string
	Resource  string
	ExpiresAt time.Time
}

// IsExpired checks if the refresh token has expired.
func (rt *RefreshTokenData) IsExpired() bool {
	return time.Now().After(rt.ExpiresAt)
}
