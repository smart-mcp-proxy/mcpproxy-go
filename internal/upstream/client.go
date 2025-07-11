package upstream

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/pkg/browser"
	"go.uber.org/zap"
	"golang.org/x/oauth2"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"

	"mcpproxy-go/internal/config"
	"mcpproxy-go/internal/hash"
	"mcpproxy-go/internal/logs"
	"mcpproxy-go/internal/prompt"
	"mcpproxy-go/internal/secureenv"
	"mcpproxy-go/internal/storage"
)

const (
	transportHTTP           = "http"
	transportStreamableHTTP = "streamable-http"
	transportSSE            = "sse"
	transportStdio          = "stdio"
	osWindows               = "windows"
	defaultClientName       = "mcpproxy-go"

	// ListTools cache duration – limits how frequently we hit the upstream
	// server with tools/list requests when we are already connected.
	// A short 30-second TTL keeps the UI responsive while preventing spam.
	listToolsCacheDuration = 30 * time.Second

	// OAuth flow types - removed unused local constants, using config package constants instead
)

// Deployment types
const (
	DeploymentLocal DeploymentType = iota + 1
	DeploymentRemote
	DeploymentHeadless
)

type DeploymentType int

func (d DeploymentType) String() string {
	switch d {
	case DeploymentLocal:
		return "local"
	case DeploymentRemote:
		return "remote"
	case DeploymentHeadless:
		return "headless"
	default:
		return "unknown"
	}
}

// Client represents an MCP client connection to an upstream server
type Client struct {
	id     string
	config *config.ServerConfig
	client *client.Client
	logger *zap.Logger

	// Upstream server specific logger for debugging
	upstreamLogger *zap.Logger

	// Server information received during initialization
	serverInfo *mcp.InitializeResult

	// Secure environment manager for filtering environment variables
	envManager *secureenv.Manager

	// User prompter for interactive OAuth setup
	prompter prompt.UserPrompter

	// Storage manager for OAuth token persistence
	storageManager *storage.Manager

	// Global configuration for accessing tracing settings
	globalConfig *config.Config

	// Connection state (protected by mutex)
	mu            sync.RWMutex
	connected     bool
	lastError     error
	retryCount    int
	lastRetryTime time.Time
	connecting    bool

	// Connection request deduplication
	connectionRequestID string // Tracks current connection attempt

	// ListTools request deduplication
	listToolsInProgress bool
	listToolsRequestID  string
	listToolsResult     []*config.ToolMetadata
	listToolsError      error
	listToolsWaiters    []chan struct{} // Channels to notify waiting requests

	// ListTools circuit breaker
	listToolsFailureCount int
	listToolsLastFailure  time.Time
	listToolsCircuitOpen  bool

	// OAuth state management
	oauthPending    bool
	oauthError      error
	oauthFlowActive bool // Guard to prevent concurrent OAuth flows
	cachedTools     []*config.ToolMetadata
	toolCacheExpiry time.Time
	connectionState string // "disconnected", "connecting", "connected", "oauth_pending", "failed"

	// Deployment detection cache
	deploymentType    DeploymentType // DeploymentLocal, DeploymentRemote, DeploymentHeadless
	detectedPublicURL string

	// OAuth token storage
	oauthToken *oauth2.Token
}

// Tool represents a tool from an upstream server
type Tool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"inputSchema"`
}

// NewClient creates a new MCP client for connecting to an upstream server
func NewClient(id string, serverConfig *config.ServerConfig, logger *zap.Logger, logConfig *config.LogConfig, globalConfig *config.Config, storageManager *storage.Manager) (*Client, error) {
	c := &Client{
		id:             id,
		config:         serverConfig,
		storageManager: storageManager,
		globalConfig:   globalConfig,
		logger: logger.With(
			zap.String("upstream_id", id),
			zap.String("upstream_name", serverConfig.Name),
		),
		prompter: prompt.NewConsolePrompter(),
	}

	// Create secure environment manager
	var envConfig *secureenv.EnvConfig
	if globalConfig != nil && globalConfig.Environment != nil {
		envConfig = globalConfig.Environment
	} else {
		envConfig = secureenv.DefaultEnvConfig()
	}

	// Add server-specific environment variables to the custom vars
	if len(serverConfig.Env) > 0 {
		// Create a copy of the environment config with server-specific variables
		serverEnvConfig := *envConfig
		if serverEnvConfig.CustomVars == nil {
			serverEnvConfig.CustomVars = make(map[string]string)
		} else {
			// Create a copy of the custom vars map
			customVars := make(map[string]string)
			for k, v := range serverEnvConfig.CustomVars {
				customVars[k] = v
			}
			serverEnvConfig.CustomVars = customVars
		}

		// Add server-specific environment variables
		for k, v := range serverConfig.Env {
			serverEnvConfig.CustomVars[k] = v
		}

		envConfig = &serverEnvConfig
	}

	c.envManager = secureenv.NewManager(envConfig)

	// Create upstream server logger if logging config is provided
	if logConfig != nil {
		upstreamLogger, err := logs.CreateUpstreamServerLogger(logConfig, serverConfig.Name)
		if err != nil {
			logger.Warn("Failed to create upstream server logger",
				zap.String("server", serverConfig.Name),
				zap.Error(err))
		} else {
			c.upstreamLogger = upstreamLogger
		}
	}

	return c, nil
}

// SetPrompter sets a custom prompter for testing
func (c *Client) SetPrompter(prompter prompt.UserPrompter) {
	c.prompter = prompter
}

// wrapWithTracingIfEnabled wraps a transport with tracing if tracing is enabled
func (c *Client) wrapWithTracingIfEnabled(transport transport.Interface) transport.Interface {
	if c.globalConfig != nil && c.globalConfig.EnableTracing {
		return NewTracingTransport(transport, c.logger, c.config.Name, true)
	}
	return transport
}

// PKCE helper functions for OAuth 2.0 PKCE extension (RFC 7636)

// generateCodeVerifier generates a cryptographically secure code verifier
// as specified in RFC 7636 Section 4.1
func generateCodeVerifier() (string, error) {
	// Generate 32 random bytes (256 bits)
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("failed to generate random bytes: %w", err)
	}

	// Base64url-encode the bytes
	return base64.RawURLEncoding.EncodeToString(bytes), nil
}

// generateCodeChallenge generates a code challenge from a code verifier
// using SHA256 method as specified in RFC 7636 Section 4.2
func generateCodeChallenge(codeVerifier string) string {
	hash := sha256.Sum256([]byte(codeVerifier))
	return base64.RawURLEncoding.EncodeToString(hash[:])
}

// PKCEParams represents PKCE parameters for OAuth flow
type PKCEParams struct {
	CodeVerifier  string
	CodeChallenge string
	Method        string // "S256" for SHA256
}

// generatePKCEParams generates PKCE parameters for OAuth flow
func (c *Client) generatePKCEParams() (*PKCEParams, error) {
	codeVerifier, err := generateCodeVerifier()
	if err != nil {
		return nil, fmt.Errorf("failed to generate code verifier: %w", err)
	}

	codeChallenge := generateCodeChallenge(codeVerifier)

	return &PKCEParams{
		CodeVerifier:  codeVerifier,
		CodeChallenge: codeChallenge,
		Method:        "S256",
	}, nil
}

// shouldUsePKCE determines if PKCE should be used for the OAuth flow
func (c *Client) shouldUsePKCE() bool {
	oauth := c.getOAuthConfig()

	// Use PKCE if explicitly enabled
	if oauth.UsePKCE {
		return true
	}

	// Always use PKCE for public clients (no client secret)
	if oauth.ClientSecret == "" {
		return true
	}

	// Use PKCE for authorization code flow in public deployments
	if oauth.FlowType == config.OAuthFlowAuthorizationCode {
		return true
	}

	// For confidential clients with client secret and other flows, PKCE is optional
	// but we still default to using it for enhanced security
	return oauth.UsePKCE // Will be false if not explicitly set
}

// Dynamic Client Registration (DCR) helper functions (RFC 7591)

// performDynamicClientRegistration attempts to register a client with the OAuth provider
func (c *Client) performDynamicClientRegistration(ctx context.Context) error {
	if c.config.OAuth == nil || c.config.OAuth.DynamicClientRegistration == nil || !c.config.OAuth.DynamicClientRegistration.Enabled {
		c.logger.Debug("DCR not enabled or configured, skipping.")
		return nil
	}
	oauth := c.config.OAuth

	// Skip if client is already registered
	if oauth.ClientID != "" {
		c.logger.Debug("Client already has client_id, skipping DCR")
		return nil
	}

	// Get registration endpoint
	registrationEndpoint := oauth.RegistrationEndpoint
	if registrationEndpoint == "" {
		c.logger.Debug("No registration endpoint available for DCR")
		return nil
	}

	c.logger.Info("Attempting Dynamic Client Registration", zap.String("endpoint", registrationEndpoint))

	// Build registration request
	regReq := c.buildRegistrationRequest()

	// Make registration request
	regResp, err := c.registerClient(ctx, registrationEndpoint, regReq)
	if err != nil {
		c.logger.Error("DCR request failed", zap.Error(err))
		return fmt.Errorf("failed to register client dynamically: %w", err)
	}

	c.logger.Info("Dynamic Client Registration successful",
		zap.String("client_id", regResp.ClientID))

	// Update OAuth configuration with registered client credentials
	oauth.ClientID = regResp.ClientID
	oauth.ClientSecret = regResp.ClientSecret

	c.logger.Info("Dynamic Client Registration successful",
		zap.String("client_id", oauth.ClientID),
		zap.Bool("has_client_secret", oauth.ClientSecret != ""))

	return nil
}

// performDCRWithExactRedirectURI performs DCR with a specific redirect URI (for two-phase OAuth)
func (c *Client) performDCRWithExactRedirectURI(ctx context.Context, exactRedirectURI string) error {
	if c.config.OAuth == nil {
		return fmt.Errorf("OAuth configuration not initialized")
	}

	oauth := c.config.OAuth

	// Get registration endpoint
	registrationEndpoint := oauth.RegistrationEndpoint
	if registrationEndpoint == "" {
		c.logger.Debug("No registration endpoint available for DCR")
		return nil
	}

	c.logger.Info("Attempting Dynamic Client Registration with exact redirect URI",
		zap.String("endpoint", registrationEndpoint),
		zap.String("redirect_uri", exactRedirectURI))

	// Build registration request with exact redirect URI
	regReq := c.buildRegistrationRequestWithExactURI(exactRedirectURI)

	// Make registration request
	regResp, err := c.registerClient(ctx, registrationEndpoint, regReq)
	if err != nil {
		c.logger.Error("DCR request with exact redirect URI failed", zap.Error(err))
		return fmt.Errorf("failed to register client with exact redirect URI: %w", err)
	}

	c.logger.Info("Dynamic Client Registration with exact redirect URI successful",
		zap.String("client_id", regResp.ClientID),
		zap.String("redirect_uri", exactRedirectURI))

	// Update OAuth configuration with registered client credentials
	oauth.ClientID = regResp.ClientID
	oauth.ClientSecret = regResp.ClientSecret

	return nil
}

// buildRegistrationRequestWithExactURI creates a client registration request with exact redirect URI
func (c *Client) buildRegistrationRequestWithExactURI(exactRedirectURI string) *config.ClientRegistrationRequest {
	oauth := c.getOAuthConfig()
	dcr := oauth.DynamicClientRegistration

	// Determine grant types based on flow type
	grantTypes := []string{"authorization_code", "refresh_token"}
	if dcr != nil && dcr.GrantTypes != nil {
		grantTypes = dcr.GrantTypes
	}

	// Determine response types
	responseTypes := []string{"code"}
	if dcr != nil && dcr.ResponseTypes != nil {
		responseTypes = dcr.ResponseTypes
	}

	// Build application type
	applicationType := "native"
	deploymentType := c.detectDeploymentType()
	if deploymentType == DeploymentRemote {
		applicationType = "web"
	}

	// Build client metadata
	clientName := "mcpproxy-go"
	if dcr != nil && dcr.ClientName != "" {
		clientName = dcr.ClientName
	}

	clientURI := "https://github.com/smart-mcp-proxy/mcpproxy-go"
	if dcr != nil && dcr.ClientURI != "" {
		clientURI = dcr.ClientURI
	}

	contacts := []string{"support@mcpproxy.com"}
	if dcr != nil && dcr.Contacts != nil {
		contacts = dcr.Contacts
	}

	var logoURI, tosURI, policyURI string
	if dcr != nil {
		logoURI = dcr.LogoURI
		tosURI = dcr.TosURI
		policyURI = dcr.PolicyURI
	}

	return &config.ClientRegistrationRequest{
		ClientName:              clientName,
		ClientURI:               clientURI,
		LogoURI:                 logoURI,
		TosURI:                  tosURI,
		PolicyURI:               policyURI,
		Contacts:                contacts,
		RedirectURIs:            []string{exactRedirectURI}, // Use exact redirect URI
		GrantTypes:              grantTypes,
		ResponseTypes:           responseTypes,
		TokenEndpointAuthMethod: "none", // For public clients
		ApplicationType:         applicationType,
		SubjectType:             "public",
		SoftwareID:              "mcpproxy-go",
		SoftwareVersion:         "1.0.0",
	}
}

// buildRegistrationRequest creates a client registration request
func (c *Client) buildRegistrationRequest() *config.ClientRegistrationRequest {
	oauth := c.getOAuthConfig()
	dcr := oauth.DynamicClientRegistration

	// Get redirect URIs based on deployment type
	redirectURIs := c.getRedirectURIs()

	// Determine grant types based on flow type
	grantTypes := []string{"authorization_code", "refresh_token"}
	if dcr.GrantTypes != nil {
		grantTypes = dcr.GrantTypes
	}

	// Determine response types
	responseTypes := []string{"code"}
	if dcr.ResponseTypes != nil {
		responseTypes = dcr.ResponseTypes
	}

	// Build application type
	applicationType := "native"
	deploymentType := c.detectDeploymentType()
	if deploymentType == DeploymentRemote {
		applicationType = "web"
	}

	// Build client metadata
	clientName := "mcpproxy"
	if dcr.ClientName != "" {
		clientName = dcr.ClientName
	}

	clientURI := "https://github.com/user/mcpproxy-go"
	if dcr.ClientURI != "" {
		clientURI = dcr.ClientURI
	}

	contacts := []string{"admin@example.com"}
	if dcr.Contacts != nil {
		contacts = dcr.Contacts
	}

	return &config.ClientRegistrationRequest{
		ClientName:              clientName,
		ClientURI:               clientURI,
		LogoURI:                 dcr.LogoURI,
		TosURI:                  dcr.TosURI,
		PolicyURI:               dcr.PolicyURI,
		Contacts:                contacts,
		RedirectURIs:            redirectURIs,
		GrantTypes:              grantTypes,
		ResponseTypes:           responseTypes,
		TokenEndpointAuthMethod: "none", // For public clients
		ApplicationType:         applicationType,
		SubjectType:             "public",
		SoftwareID:              "mcpproxy-go",
		SoftwareVersion:         "1.0.0",
	}
}

// registerClient makes the actual DCR request to the registration endpoint
func (c *Client) registerClient(ctx context.Context, registrationEndpoint string, regReq *config.ClientRegistrationRequest) (*config.ClientRegistrationResponse, error) {
	// Marshal request body
	reqBody, err := json.Marshal(regReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal DCR request: %w", err)
	}
	c.logger.Debug("DCR request body", zap.String("body", string(reqBody)))

	// Create HTTP request
	req, err := http.NewRequestWithContext(ctx, "POST", registrationEndpoint, bytes.NewBuffer(reqBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create registration request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	// Make request
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make registration request: %w", err)
	}
	defer resp.Body.Close()

	// Check status code
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("DCR request failed with status: %d", resp.StatusCode)
	}

	// Parse response
	var regResp config.ClientRegistrationResponse
	if err := json.NewDecoder(resp.Body).Decode(&regResp); err != nil {
		return nil, fmt.Errorf("failed to decode registration response: %w", err)
	}

	c.logger.Debug("DCR response", zap.Int("status", resp.StatusCode), zap.String("body", string(reqBody)))

	return &regResp, nil
}

// detectDeploymentType automatically detects the deployment type based on environment
func (c *Client) detectDeploymentType() DeploymentType {
	if c.deploymentType != 0 {
		c.logger.Debug("Using cached deployment type", zap.String("type", c.deploymentType.String()))
		return c.deploymentType // Use cached result
	}

	// Check if running in headless environment
	if os.Getenv("DISPLAY") == "" && runtime.GOOS == "linux" {
		c.logger.Info("Detected headless deployment", zap.String("os", runtime.GOOS), zap.String("display", os.Getenv("DISPLAY")))
		c.deploymentType = DeploymentHeadless
		return DeploymentHeadless
	}

	// Check if mcpproxy is configured with public URL
	if c.config.PublicURL != "" {
		c.logger.Info("Detected remote deployment", zap.String("public_url", c.config.PublicURL))
		c.deploymentType = DeploymentRemote
		c.detectedPublicURL = c.config.PublicURL
		return DeploymentRemote
	}

	// Check if mcpproxy is listening on non-localhost interfaces
	// This would require access to global config, for now assume local
	c.logger.Info("Detected local deployment", zap.String("os", runtime.GOOS))
	c.deploymentType = DeploymentLocal
	return DeploymentLocal
}

// getConnectionState returns the current connection state
//
//nolint:unused // Utility function for debugging/future use
func (c *Client) getConnectionState() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.connectionState
}

// setConnectionState updates the connection state
func (c *Client) setConnectionState(state string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.connectionState = state
}

// isOAuthPending returns true if OAuth is pending for this client
//
//nolint:unused // Utility function for debugging/future use
func (c *Client) isOAuthPending() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.oauthPending
}

// setOAuthPending sets the OAuth pending state
func (c *Client) setOAuthPending(pending bool, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.oauthPending = pending
	c.oauthError = err
	if pending {
		c.connectionState = config.ConnectionStateOAuthPending
	}
}

// getCachedTools returns cached tools if available and not expired
func (c *Client) getCachedTools() []*config.ToolMetadata {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.cachedTools == nil || time.Now().After(c.toolCacheExpiry) {
		return nil
	}

	return c.cachedTools
}

// setCachedTools stores tools in cache with expiry
func (c *Client) setCachedTools(tools []*config.ToolMetadata, expiry time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.cachedTools = tools
	c.toolCacheExpiry = time.Now().Add(expiry)
}

// clearCachedTools clears the tool cache
//
//nolint:unused // Utility function for debugging/future use
func (c *Client) clearCachedTools() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.cachedTools = nil
	c.toolCacheExpiry = time.Time{}
}

// getOAuthConfig returns the OAuth configuration with defaults
func (c *Client) getOAuthConfig() *config.OAuthConfig {
	if c.config.OAuth == nil {
		c.config.OAuth = config.DefaultOAuthConfig()
	}
	return c.config.OAuth
}

// createAutoOAuthConfig creates an OAuth configuration optimized for auto-detection scenarios
func (c *Client) createAutoOAuthConfig() *config.OAuthConfig {
	oauthConfig := config.DefaultOAuthConfig()

	// Enable lazy auth by default for auto-detected OAuth scenarios
	oauthConfig.LazyAuth = true

	// Configure for known server types
	if strings.Contains(c.config.URL, "mcp.cloudflare.com") || strings.Contains(c.config.URL, "builds.mcp.cloudflare.com") {
		// Cloudflare-specific configuration
		oauthConfig.AutoDiscovery.Enabled = true
		oauthConfig.AutoDiscovery.EnableDCR = true
		oauthConfig.DynamicClientRegistration.Enabled = true
		oauthConfig.DynamicClientRegistration.ClientName = defaultClientName
		oauthConfig.DynamicClientRegistration.ClientURI = "https://github.com/smart-mcp-proxy/mcpproxy-go"
		oauthConfig.DynamicClientRegistration.Contacts = []string{"support@mcpproxy.com"}

		// For Cloudflare, prefer authorization code flow for better UX
		deploymentType := c.detectDeploymentType()
		if deploymentType == DeploymentLocal {
			oauthConfig.FlowType = config.OAuthFlowAuthorizationCode
		} else {
			oauthConfig.FlowType = config.OAuthFlowDeviceCode
		}

		c.logger.Info("Auto-configured OAuth for Cloudflare server")
	} else {
		// Generic server configuration
		oauthConfig.AutoDiscovery.Enabled = true
		oauthConfig.AutoDiscovery.EnableDCR = true
		oauthConfig.FlowType = config.OAuthFlowAuto

		c.logger.Info("Auto-configured OAuth for generic server")
	}

	return oauthConfig
}

// generateAuthenticationURL generates a direct authentication URL for local deployments
func (c *Client) generateAuthenticationURL() string {
	if c.config.OAuth == nil {
		return ""
	}

	oauth := c.config.OAuth

	// For authorization code flow, generate authorization URL
	if oauth.FlowType == config.OAuthFlowAuthorizationCode || oauth.FlowType == config.OAuthFlowAuto {
		if oauth.AuthorizationEndpoint == "" {
			c.logger.Debug("Cannot generate auth URL: AuthorizationEndpoint is empty")
			return ""
		}

		// Build basic authorization URL
		authURL := fmt.Sprintf("%s?response_type=code&client_id=%s",
			oauth.AuthorizationEndpoint,
			oauth.ClientID)

		// Add redirect URI if available
		redirectURIs := c.getRedirectURIs()
		if len(redirectURIs) > 0 {
			authURL += "&redirect_uri=" + redirectURIs[0]
		}

		// Add scopes if available
		if len(oauth.Scopes) > 0 {
			authURL += "&scope=" + strings.Join(oauth.Scopes, " ")
		}

		return authURL
	}

	// For device code flow, return device endpoint
	if oauth.FlowType == config.OAuthFlowDeviceCode {
		return oauth.DeviceEndpoint
	}

	return ""
}

// triggerOAuthFlowAsync triggers the OAuth flow asynchronously for better UX
func (c *Client) triggerOAuthFlowAsync(ctx context.Context) {
	c.logger.Info("Triggering automatic OAuth flow for local deployment")

	// Check if OAuth flow is already active
	c.mu.Lock()
	if c.oauthFlowActive {
		c.logger.Debug("OAuth flow already active, skipping duplicate trigger")
		c.mu.Unlock()
		return
	}
	c.oauthFlowActive = true
	c.mu.Unlock()

	// Ensure we reset the flag when done
	defer func() {
		c.mu.Lock()
		c.oauthFlowActive = false
		c.mu.Unlock()
	}()

	// Wait a brief moment to allow the connection to stabilize
	time.Sleep(500 * time.Millisecond)

	// Check if OAuth is still pending
	c.mu.RLock()
	stillPending := c.oauthPending
	c.mu.RUnlock()

	if !stillPending {
		c.logger.Debug("OAuth no longer pending, skipping automatic flow")
		return
	}

	// Use background context to prevent cancellation from connection timeouts
	oauthCtx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	c.logger.Debug("Starting OAuth flow with background context")

	// Trigger OAuth flow
	if err := c.handleOAuthFlow(oauthCtx); err != nil {
		c.logger.Error("Automatic OAuth flow failed", zap.Error(err))
		c.mu.Lock()
		c.oauthError = err
		c.mu.Unlock()
		return
	}

	c.logger.Info("Automatic OAuth flow completed successfully")

	// Clear OAuth pending state before attempting to reconnect
	c.setOAuthPending(false, nil)

	// Force reconnection after successful OAuth - bypass shouldAttemptConnection()
	// since we know OAuth just completed and we need to establish a fresh connection
	c.logger.Info("Forcing reconnection after successful OAuth")
	if err := c.Connect(ctx); err != nil {
		c.logger.Error("Failed to reconnect after automatic OAuth", zap.Error(err))
		c.mu.Lock()
		c.oauthError = err
		c.mu.Unlock()
	} else {
		c.logger.Info("Successfully reconnected after OAuth flow")
	}
}

// shouldUseLazyAuth returns true if lazy OAuth should be used
func (c *Client) shouldUseLazyAuth() bool {
	oauth := c.getOAuthConfig()
	return oauth.LazyAuth
}

// getRedirectURIs returns appropriate redirect URIs based on deployment type
func (c *Client) getRedirectURIs() []string {
	oauth := c.getOAuthConfig()

	// Use configured redirect URIs if available
	if len(oauth.RedirectURIs) > 0 {
		return oauth.RedirectURIs
	}

	if oauth.RedirectURI != "" {
		return []string{oauth.RedirectURI}
	}

	// Generate appropriate redirect URIs based on deployment type
	deploymentType := c.detectDeploymentType()
	switch deploymentType {
	case DeploymentLocal:
		// RFC 8252 Section 7.3: Register base loopback URIs without specific ports
		// Authorization servers MUST allow any port at request time for loopback IPs
		return []string{
			"http://127.0.0.1/oauth/callback",
			"http://localhost/oauth/callback",
		}
	case DeploymentRemote:
		if c.detectedPublicURL != "" {
			return []string{c.detectedPublicURL + "/oauth/callback"}
		}
		return []string{"https://example.com/oauth/callback"} // Fallback
	case DeploymentHeadless:
		return []string{"urn:ietf:wg:oauth:2.0:oob"} // Out-of-band for headless
	default:
		return []string{"http://127.0.0.1:8080/oauth/callback"}
	}
}

// selectOAuthFlow selects the appropriate OAuth flow based on deployment type
func (c *Client) selectOAuthFlow() string {
	oauth := c.getOAuthConfig()

	// Use configured flow if not auto (and not empty)
	if oauth.FlowType != config.OAuthFlowAuto && oauth.FlowType != "" {
		return oauth.FlowType
	}

	// Auto-select based on deployment type
	deploymentType := c.detectDeploymentType()
	switch deploymentType {
	case DeploymentHeadless:
		return config.OAuthFlowDeviceCode
	case DeploymentRemote:
		if oauth.PreferDeviceFlow {
			return config.OAuthFlowDeviceCode
		}
		return config.OAuthFlowAuthorizationCode
	case DeploymentLocal:
		return config.OAuthFlowAuthorizationCode
	default:
		return config.OAuthFlowDeviceCode
	}
}

// Connect establishes a connection to the upstream MCP server
func (c *Client) Connect(ctx context.Context) error {
	// Use connection deduplication system
	requestID, shouldProceed := c.startConnectionAttempt()
	if !shouldProceed {
		c.logger.Debug("Connection attempt skipped - already connected, connecting, or OAuth pending")
		return nil // Not an error - just nothing to do
	}

	// Declare variables that will be used in error handling
	var command string
	var cmdArgs []string
	var envVars []string

	// Track success and error for connection attempt cleanup
	var connectionSuccess bool
	var connectionError error

	// Ensure we finish the connection attempt regardless of outcome
	defer func() {
		// If we exited early because OAuth is pending, treat the attempt as
		// "successful" for back-off purposes so the manager does not schedule an
		// immediate retry that races with the post-OAuth reconnect. This avoids the
		// extra Connect that gets cancelled and logged as a transport error.
		c.mu.RLock()
		oauthPendingNow := c.oauthPending
		c.mu.RUnlock()
		if oauthPendingNow && connectionError == nil {
			connectionSuccess = true
		}

		c.finishConnectionAttempt(requestID, connectionSuccess, connectionError)
	}()

	c.mu.RLock()
	retryCount := c.retryCount
	c.mu.RUnlock()

	// Log to both main logger and upstream logger
	c.logger.Info("Connecting to upstream MCP server",
		zap.String("url", c.config.URL),
		zap.String("protocol", c.config.Protocol),
		zap.Int("retry_count", retryCount))

	if c.upstreamLogger != nil {
		c.upstreamLogger.Info("Connecting to upstream server",
			zap.String("url", c.config.URL),
			zap.String("protocol", c.config.Protocol),
			zap.Int("retry_count", retryCount))
	}

	// Debug: Check for stored OAuth tokens before connection attempt
	hasStoredToken := false
	var tokenInfo string
	if c.config.OAuth != nil && c.config.OAuth.TokenStorage != nil {
		hasStoredToken = true
		tokenInfo = fmt.Sprintf("type=%s, expires_at=%v, has_refresh=%t",
			c.config.OAuth.TokenStorage.TokenType,
			c.config.OAuth.TokenStorage.ExpiresAt,
			c.config.OAuth.TokenStorage.RefreshToken != "")
	}

	c.logger.Debug("Connection attempt starting",
		zap.String("upstream_id", c.id),
		zap.String("upstream_name", c.config.Name),
		zap.Bool("has_stored_oauth_token", hasStoredToken),
		zap.String("stored_token_info", tokenInfo))

	// Load OAuth tokens from storage if not already loaded
	if !hasStoredToken {
		if err := c.loadOAuthTokensFromStorage(ctx); err != nil {
			c.logger.Warn("Failed to load OAuth tokens from storage", zap.Error(err))
		} else {
			// Update token info after loading from storage
			if c.config.OAuth != nil && c.config.OAuth.TokenStorage != nil {
				hasStoredToken = true
				tokenInfo = fmt.Sprintf("type=%s, expires_at=%v, has_refresh=%t",
					c.config.OAuth.TokenStorage.TokenType,
					c.config.OAuth.TokenStorage.ExpiresAt,
					c.config.OAuth.TokenStorage.RefreshToken != "")
				c.logger.Debug("Updated token info after loading from storage",
					zap.String("upstream_id", c.id),
					zap.String("upstream_name", c.config.Name),
					zap.Bool("has_stored_oauth_token", hasStoredToken),
					zap.String("stored_token_info", tokenInfo))
			}
		}
	}

	transportType := c.determineTransportType()

	switch transportType {
	case transportHTTP, transportStreamableHTTP:
		// Merge static headers with OAuth Authorization header if a valid token is available
		allHeaders := make(map[string]string)
		for k, v := range c.config.Headers {
			allHeaders[k] = v
		}

		shouldUseOAuth := false
		var tokenExpired bool
		if c.config.OAuth != nil && c.config.OAuth.TokenStorage != nil {
			tokenExpired = time.Now().After(c.config.OAuth.TokenStorage.ExpiresAt)
			if !tokenExpired {
				shouldUseOAuth = true
				allHeaders["Authorization"] = fmt.Sprintf("Bearer %s", c.config.OAuth.TokenStorage.AccessToken)
			} else {
				c.logger.Warn("OAuth token expired during HTTP client creation",
					zap.String("upstream_id", c.id),
					zap.String("upstream_name", c.config.Name),
					zap.Time("expires_at", c.config.OAuth.TokenStorage.ExpiresAt))
			}
		}

		c.logger.Debug("Creating HTTP client",
			zap.String("upstream_id", c.id),
			zap.String("upstream_name", c.config.Name),
			zap.Bool("should_use_oauth", shouldUseOAuth),
			zap.Bool("token_expired", tokenExpired),
			zap.Int("total_headers", len(allHeaders)),
			zap.Bool("has_authorization", allHeaders["Authorization"] != ""))

		httpTransport, err := transport.NewStreamableHTTP(c.config.URL, transport.WithHTTPHeaders(allHeaders))
		if err != nil {
			// Check if this is an OAuth authorization required error during HTTP connection
			if c.isOAuthAuthorizationRequired(err) {
				c.logger.Info("OAuth authorization required during HTTP connection")
				if c.upstreamLogger != nil {
					c.upstreamLogger.Info("OAuth authorization required during HTTP connection")
				}

				// Auto-initialize OAuth configuration if not present
				if c.config.OAuth == nil {
					c.logger.Info("Auto-initializing OAuth configuration for HTTP")
					c.config.OAuth = c.createAutoOAuthConfig()
				} else {
					c.logger.Debug("OAuth configuration already exists, preserving discovered endpoints")
				}

				// Set OAuth pending state
				c.setOAuthPending(true, err)

				// For local deployments, automatically trigger OAuth flow
				deploymentType := c.detectDeploymentType()
				c.logger.Info("Checking deployment type for OAuth flow trigger",
					zap.String("deployment_type", deploymentType.String()),
					zap.Bool("is_local", deploymentType == DeploymentLocal))
				if deploymentType == DeploymentLocal {
					go c.triggerOAuthFlowAsync(ctx)
				} else {
					c.logger.Info("OAuth flow not triggered - not local deployment",
						zap.String("deployment_type", deploymentType.String()))
				}

				// Return nil to indicate "success" with OAuth pending
				return nil
			}

			connectionError = fmt.Errorf("failed to create HTTP transport: %w", err)
			return connectionError
		}

		c.client = client.NewClient(c.wrapWithTracingIfEnabled(httpTransport))
	case transportSSE:
		// Debug: Check if OAuth tokens should be applied to SSE client
		shouldUseOAuth := false
		authHeaders := make(map[string]string)
		var tokenExpired bool
		if c.config.OAuth != nil && c.config.OAuth.TokenStorage != nil {
			tokenExpired = time.Now().After(c.config.OAuth.TokenStorage.ExpiresAt)
			if !tokenExpired {
				shouldUseOAuth = true
				authHeaders["Authorization"] = fmt.Sprintf("Bearer %s", c.config.OAuth.TokenStorage.AccessToken)
			} else {
				c.logger.Warn("OAuth token expired during SSE client creation",
					zap.String("upstream_id", c.id),
					zap.String("upstream_name", c.config.Name),
					zap.Time("expires_at", c.config.OAuth.TokenStorage.ExpiresAt),
					zap.Duration("expired_since", time.Now().Sub(c.config.OAuth.TokenStorage.ExpiresAt)))
			}
		}

		c.logger.Debug("Creating SSE client",
			zap.String("upstream_id", c.id),
			zap.String("upstream_name", c.config.Name),
			zap.Bool("should_use_oauth", shouldUseOAuth),
			zap.Bool("token_expired", tokenExpired),
			zap.Bool("has_config_headers", len(c.config.Headers) > 0))

		// Create SSE client with headers if provided
		if len(c.config.Headers) > 0 {
			// Merge config headers with OAuth headers
			allHeaders := make(map[string]string)
			for k, v := range c.config.Headers {
				allHeaders[k] = v
			}
			for k, v := range authHeaders {
				allHeaders[k] = v
			}

			c.logger.Debug("SSE client headers applied",
				zap.String("upstream_id", c.id),
				zap.String("upstream_name", c.config.Name),
				zap.Int("total_headers", len(allHeaders)),
				zap.Bool("has_authorization", allHeaders["Authorization"] != ""))

			c.logger.Debug("Creating SSE client with authentication headers",
				zap.String("upstream_id", c.id),
				zap.String("upstream_name", c.config.Name),
				zap.String("url", c.config.URL))

			sseTransport, err := transport.NewSSE(c.config.URL,
				transport.WithHeaders(allHeaders))
			if err != nil {
				// Check if this is an OAuth authorization required error during SSE connection
				if c.isOAuthAuthorizationRequired(err) {
					c.logger.Info("OAuth authorization required during SSE connection")
					if c.upstreamLogger != nil {
						c.upstreamLogger.Info("OAuth authorization required during SSE connection")
					}

					// Auto-initialize OAuth configuration if not present
					if c.config.OAuth == nil {
						c.logger.Info("Auto-initializing OAuth configuration for SSE")
						c.config.OAuth = c.createAutoOAuthConfig()
					} else {
						c.logger.Debug("OAuth configuration already exists, preserving discovered endpoints")
					}

					// Set OAuth pending state
					c.setOAuthPending(true, err)

					// For local deployments, automatically trigger OAuth flow
					deploymentType := c.detectDeploymentType()
					if deploymentType == DeploymentLocal {
						go c.triggerOAuthFlowAsync(ctx)
					}

					// Return nil to indicate "success" with OAuth pending
					return nil
				}

				connectionError = fmt.Errorf("failed to create SSE transport: %w", err)
				return connectionError
			}
			c.client = client.NewClient(c.wrapWithTracingIfEnabled(sseTransport))
		} else {
			// Apply OAuth headers even if no config headers
			if shouldUseOAuth {
				c.logger.Debug("Applying OAuth headers to SSE client (no config headers)",
					zap.String("upstream_id", c.id),
					zap.String("upstream_name", c.config.Name),
					zap.Bool("has_authorization", authHeaders["Authorization"] != ""))
				sseTransport, err := transport.NewSSE(c.config.URL,
					transport.WithHeaders(authHeaders))
				if err != nil {
					// Check if this is an OAuth authorization required error during SSE connection
					if c.isOAuthAuthorizationRequired(err) {
						c.logger.Info("OAuth authorization required during SSE connection")
						if c.upstreamLogger != nil {
							c.upstreamLogger.Info("OAuth authorization required during SSE connection")
						}

						// Auto-initialize OAuth configuration if not present
						if c.config.OAuth == nil {
							c.logger.Info("Auto-initializing OAuth configuration for SSE")
							c.config.OAuth = c.createAutoOAuthConfig()
						} else {
							c.logger.Debug("OAuth configuration already exists, preserving discovered endpoints")
						}

						// Set OAuth pending state
						c.setOAuthPending(true, err)

						// For local deployments, automatically trigger OAuth flow
						deploymentType := c.detectDeploymentType()
						if deploymentType == DeploymentLocal {
							go c.triggerOAuthFlowAsync(ctx)
						}

						// Return nil to indicate "success" with OAuth pending
						return nil
					}

					c.mu.Lock()
					c.lastError = err
					c.retryCount++
					c.lastRetryTime = time.Now()
					c.mu.Unlock()
					return fmt.Errorf("failed to create SSE transport: %w", err)
				}
				c.client = client.NewClient(c.wrapWithTracingIfEnabled(sseTransport))
			} else {
				sseTransport, err := transport.NewSSE(c.config.URL)
				if err != nil {
					// Check if this is an OAuth authorization required error during SSE connection
					if c.isOAuthAuthorizationRequired(err) {
						c.logger.Info("OAuth authorization required during SSE connection")
						if c.upstreamLogger != nil {
							c.upstreamLogger.Info("OAuth authorization required during SSE connection")
						}

						// Auto-initialize OAuth configuration if not present
						if c.config.OAuth == nil {
							c.logger.Info("Auto-initializing OAuth configuration for SSE")
							c.config.OAuth = c.createAutoOAuthConfig()
						} else {
							c.logger.Debug("OAuth configuration already exists, preserving discovered endpoints")
						}

						// Set OAuth pending state
						c.setOAuthPending(true, err)

						// For local deployments, automatically trigger OAuth flow
						deploymentType := c.detectDeploymentType()
						if deploymentType == DeploymentLocal {
							go c.triggerOAuthFlowAsync(ctx)
						}

						// Return nil to indicate "success" with OAuth pending
						return nil
					}

					c.mu.Lock()
					c.lastError = err
					c.retryCount++
					c.lastRetryTime = time.Now()
					c.mu.Unlock()
					return fmt.Errorf("failed to create SSE transport: %w", err)
				}
				c.client = client.NewClient(c.wrapWithTracingIfEnabled(sseTransport))
			}
		}
	case transportStdio:
		var originalCommand string
		var originalArgs []string

		// Check if command is specified separately (preferred)
		if c.config.Command != "" {
			originalCommand = c.config.Command
			originalArgs = c.config.Args
		} else {
			// Fallback to parsing from URL
			args := c.parseCommand(c.config.URL)
			if len(args) == 0 {
				connectionError = fmt.Errorf("invalid stdio command: %s", c.config.URL)
				return connectionError
			}
			originalCommand = args[0]
			originalArgs = args[1:]
		}

		if originalCommand == "" {
			connectionError = fmt.Errorf("no command specified for stdio transport")
			return connectionError
		}

		// Use secure environment manager to build filtered environment variables
		envVars = c.envManager.BuildSecureEnvironment()

		// Wrap command in a shell to ensure user's PATH is respected, especially in GUI apps
		command, cmdArgs = c.wrapCommandInShell(originalCommand, originalArgs)

		if c.upstreamLogger != nil {
			c.upstreamLogger.Debug("Process starting",
				zap.String("full_command", fmt.Sprintf("%s %s", command, strings.Join(cmdArgs, " "))))
		}

		stdioTransport := transport.NewStdio(command, envVars, cmdArgs...)
		c.client = client.NewClient(c.wrapWithTracingIfEnabled(stdioTransport))
	default:
		connectionError = fmt.Errorf("unsupported transport type: %s", transportType)
		return connectionError
	}

	// Set connection timeout with exponential backoff consideration
	timeout := c.getConnectionTimeout()
	connectCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Log the timeout being used for debugging
	c.logger.Debug("Using connection timeout",
		zap.Duration("timeout", timeout),
		zap.Bool("custom_timeout", c.config.Timeout != nil),
		zap.Int("retry_count", retryCount))

	// Start the client
	if err := c.client.Start(connectCtx); err != nil {
		// Check if this is an OAuth authorization required error during client start
		if c.isOAuthAuthorizationRequired(err) {
			c.logger.Info("OAuth authorization required during client start")
			if c.upstreamLogger != nil {
				c.upstreamLogger.Info("OAuth authorization required during client start")
			}

			// Auto-initialize OAuth configuration if not present
			if c.config.OAuth == nil {
				c.logger.Info("Auto-initializing OAuth configuration during start")
				c.config.OAuth = c.createAutoOAuthConfig()
			}

			// Set OAuth pending state
			c.setOAuthPending(true, err)

			// For local deployments, automatically trigger OAuth flow
			deploymentType := c.detectDeploymentType()
			if deploymentType == DeploymentLocal {
				go c.triggerOAuthFlowAsync(ctx)
			}

			// Return nil to indicate "success" with OAuth pending
			return nil
		}

		c.logger.Error("Failed to start MCP client",
			zap.Error(err),
			zap.String("command", command),
			zap.Strings("args", cmdArgs))

		if c.upstreamLogger != nil {
			c.upstreamLogger.Error("Client start failed", zap.Error(err))
		}

		connectionError = fmt.Errorf("failed to start MCP client: %w", err)
		return connectionError
	}

	// Initialize the client
	initRequest := mcp.InitializeRequest{}
	initRequest.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initRequest.Params.ClientInfo = mcp.Implementation{
		Name:    "mcpproxy-go",
		Version: "1.0.0",
	}
	initRequest.Params.Capabilities = mcp.ClientCapabilities{}

	serverInfo, err := c.client.Initialize(connectCtx, initRequest)
	if err != nil {
		// Check if this is an OAuth authorization required error
		if c.isOAuthAuthorizationRequired(err) {
			c.logger.Info("OAuth authorization required")
			if c.upstreamLogger != nil {
				c.upstreamLogger.Info("OAuth authorization required")
			}

			// Auto-initialize OAuth configuration if not present
			if c.config.OAuth == nil {
				c.logger.Info("Auto-initializing OAuth configuration")
				c.config.OAuth = c.createAutoOAuthConfig()
			}

			// Check if lazy OAuth is enabled
			if c.shouldUseLazyAuth() {
				c.logger.Info("OAuth pending - entering lazy OAuth state")
				if c.upstreamLogger != nil {
					c.upstreamLogger.Info("Entering OAuth pending state")
				}

				// Set OAuth pending state
				c.setOAuthPending(true, err)

				// For local deployments, automatically open browser for better UX
				deploymentType := c.detectDeploymentType()
				c.logger.Info("Checking deployment type for OAuth flow trigger (lazy OAuth)",
					zap.String("deployment_type", deploymentType.String()),
					zap.Bool("is_local", deploymentType == DeploymentLocal))
				if deploymentType == DeploymentLocal {
					go c.triggerOAuthFlowAsync(connectCtx)
				} else {
					c.logger.Info("OAuth flow not triggered - not local deployment (lazy OAuth)",
						zap.String("deployment_type", deploymentType.String()))
				}

				// Close client connection since we can't use it yet
				c.client.Close()

				// Return nil to indicate "success" with OAuth pending
				return nil
			}

			// Immediate OAuth flow (non-lazy)
			c.logger.Info("Initiating immediate OAuth flow")
			if c.upstreamLogger != nil {
				c.upstreamLogger.Info("Starting immediate OAuth flow")
			}

			// Handle OAuth flow
			if oauthErr := c.handleOAuthFlow(connectCtx); oauthErr != nil {
				c.mu.Lock()
				c.lastError = oauthErr
				c.retryCount++
				c.lastRetryTime = time.Now()
				c.mu.Unlock()

				c.logger.Error("OAuth flow failed", zap.Error(oauthErr))
				if c.upstreamLogger != nil {
					c.upstreamLogger.Error("OAuth flow failed", zap.Error(oauthErr))
				}

				c.client.Close()
				return fmt.Errorf("OAuth flow failed: %w", oauthErr)
			}

			// Retry initialization after OAuth flow
			serverInfo, err = c.client.Initialize(connectCtx, initRequest)
			if err != nil {
				c.mu.Lock()
				c.lastError = err
				c.retryCount++
				c.lastRetryTime = time.Now()
				c.mu.Unlock()

				c.logger.Error("Failed to initialize MCP client after OAuth", zap.Error(err))
				if c.upstreamLogger != nil {
					c.upstreamLogger.Error("Initialize failed after OAuth", zap.Error(err))
				}

				c.client.Close()
				return fmt.Errorf("failed to initialize MCP client after OAuth: %w", err)
			}
		} else {
			c.mu.Lock()
			c.lastError = err
			c.retryCount++
			c.lastRetryTime = time.Now()
			c.mu.Unlock()

			// Log to both main and server logs for critical errors
			c.logger.Error("Failed to initialize MCP client", zap.Error(err))
			if c.upstreamLogger != nil {
				c.upstreamLogger.Error("Initialize failed", zap.Error(err))
			}

			c.client.Close()
			c.setConnectionState(config.ConnectionStateFailed)
			return fmt.Errorf("failed to initialize MCP client: %w", err)
		}
	}

	c.serverInfo = serverInfo

	// Mark connection as successful (will be handled by defer)
	connectionSuccess = true

	// Set connection state
	c.setConnectionState(config.ConnectionStateConnected)

	c.logger.Info("Successfully connected to upstream MCP server",
		zap.String("server_name", serverInfo.ServerInfo.Name),
		zap.String("server_version", serverInfo.ServerInfo.Version))

	// Add debug transport info if DEBUG level is enabled
	if c.logger.Core().Enabled(zap.DebugLevel) {
		c.logger.Debug("MCP connection details",
			zap.String("protocol_version", serverInfo.ProtocolVersion),
			zap.String("command", c.config.Command),
			zap.Strings("args", c.config.Args),
			zap.String("transport", c.determineTransportType()))
	}

	if c.upstreamLogger != nil {
		c.upstreamLogger.Info("Connected successfully",
			zap.String("server_name", serverInfo.ServerInfo.Name),
			zap.String("server_version", serverInfo.ServerInfo.Version),
			zap.String("protocol_version", serverInfo.ProtocolVersion))

		// Only log initialization JSON if DEBUG level is enabled
		if c.logger.Core().Enabled(zap.DebugLevel) {
			c.upstreamLogger.Debug("[Client→Server] initialize")
			if initBytes, err := json.Marshal(initRequest); err == nil {
				c.upstreamLogger.Debug(string(initBytes))
			}
			c.upstreamLogger.Debug("[Server→Client] initialize response")
			if respBytes, err := json.Marshal(serverInfo); err == nil {
				c.upstreamLogger.Debug(string(respBytes))
			}
		}
	}

	return nil
}

// getConnectionTimeout returns the connection timeout with exponential backoff
func (c *Client) getConnectionTimeout() time.Duration {
	// Use custom timeout if specified, otherwise default to 30 seconds
	baseTimeout := 30 * time.Second
	if c.config.Timeout != nil {
		baseTimeout = c.config.Timeout.ToDuration()
	}

	c.mu.RLock()
	retryCount := c.retryCount
	c.mu.RUnlock()

	if retryCount == 0 {
		return baseTimeout
	}

	// Exponential backoff: min(base * 2^retry, max)
	backoffMultiplier := math.Pow(2, float64(retryCount))
	maxTimeout := 5 * time.Minute
	timeout := time.Duration(float64(baseTimeout) * backoffMultiplier)

	if timeout > maxTimeout {
		timeout = maxTimeout
	}

	return timeout
}

// wrapCommandInShell wraps the original command in a shell to ensure PATH is loaded.
func (c *Client) wrapCommandInShell(command string, args []string) (shellCmd string, shellArgs []string) {
	fullCmd := command
	if len(args) > 0 {
		quotedArgs := make([]string, len(args))
		for i, arg := range args {
			// Basic quoting for arguments with spaces
			if strings.Contains(arg, " ") {
				quotedArgs[i] = fmt.Sprintf("%q", arg)
			} else {
				quotedArgs[i] = arg
			}
		}
		fullCmd = fmt.Sprintf("%s %s", command, strings.Join(quotedArgs, " "))
	}

	if runtime.GOOS == osWindows {
		return "cmd.exe", []string{"/c", fullCmd}
	}

	// For Unix-like systems, use a login shell to load profile scripts
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	return shell, []string{"-l", "-c", fullCmd}
}

// ShouldRetry returns true if the client should retry connecting based on exponential backoff
func (c *Client) ShouldRetry() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.shouldRetryLocked()
}

// shouldRetryLocked is the implementation of ShouldRetry that assumes the mutex is already held
func (c *Client) shouldRetryLocked() bool {
	if c.connected || c.connecting {
		return false
	}

	// Don't retry if OAuth is pending - wait for user to complete the flow
	if c.oauthPending {
		return false
	}

	if c.retryCount == 0 {
		return true
	}

	// Calculate next retry time using exponential backoff
	backoffDuration := time.Duration(math.Pow(2, float64(c.retryCount-1))) * time.Second
	maxBackoff := 5 * time.Minute
	if backoffDuration > maxBackoff {
		backoffDuration = maxBackoff
	}

	return time.Since(c.lastRetryTime) >= backoffDuration
}

// GetConnectionStatus returns detailed connection status information
func (c *Client) GetConnectionStatus() map[string]interface{} {
	c.mu.RLock()
	defer c.mu.RUnlock()

	shouldRetry := c.shouldRetryLocked()

	status := map[string]interface{}{
		"connected":       c.connected,
		"connecting":      c.connecting,
		"retry_count":     c.retryCount,
		"last_retry_time": c.lastRetryTime,
		"should_retry":    shouldRetry,
	}

	if c.lastError != nil {
		status["last_error"] = c.lastError.Error()
	}

	if c.serverInfo != nil {
		status["server_name"] = c.serverInfo.ServerInfo.Name
		status["server_version"] = c.serverInfo.ServerInfo.Version
	}

	// Add OAuth-related status information
	if c.oauthPending {
		status["oauth_pending"] = true
		status["oauth_status"] = "authentication_required"
		status["oauth_message"] = "OAuth authentication required. Click the authentication link to complete the process."

		// Add authentication URLs if OAuth is configured
		if c.config.OAuth != nil {
			oauthInfo := map[string]interface{}{
				"flow_type": c.config.OAuth.FlowType,
			}

			// Generate authentication URL for local deployments
			deploymentType := c.detectDeploymentType()
			if deploymentType == DeploymentLocal {
				// For local deployments, generate a direct authentication URL
				authURL := c.generateAuthenticationURL()
				if authURL != "" {
					oauthInfo["auth_url"] = authURL
					oauthInfo["auth_instructions"] = "Click the authentication URL to complete OAuth setup in your browser."
				}
			} else {
				// For remote/headless deployments, provide device code instructions
				oauthInfo["auth_instructions"] = "OAuth authentication required. Use the upstream_servers tool to initiate the authentication flow."
			}

			status["oauth_info"] = oauthInfo
		}
	} else if c.config.OAuth != nil {
		status["oauth_configured"] = true
		status["oauth_status"] = "configured"
	}

	if c.oauthError != nil {
		status["oauth_error"] = c.oauthError.Error()
	}

	// Add cached tools info if available
	cachedTools := c.getCachedTools()
	if cachedTools != nil {
		status["cached_tools_count"] = len(cachedTools)
		status["cached_tools_available"] = true
	}

	// Add connection state
	status["connection_state"] = c.getConnectionState()

	return status
}

// determineTransportType determines the transport type based on URL and config
func (c *Client) determineTransportType() string {
	if c.config.Protocol != "" && c.config.Protocol != "auto" {
		return c.config.Protocol
	}

	// Auto-detect based on command first (highest priority)
	if c.config.Command != "" {
		return transportStdio
	}

	// Auto-detect based on URL
	if strings.HasPrefix(c.config.URL, "http://") || strings.HasPrefix(c.config.URL, "https://") {
		// Default to streamable-http for HTTP URLs unless explicitly set
		return transportStreamableHTTP
	}

	// Assume stdio for command-like URLs or when command is specified
	return transportStdio
}

// parseCommand parses a command string into command and arguments
func (c *Client) parseCommand(cmd string) []string {
	var result []string
	var current string
	var inQuote bool
	var quoteChar rune

	for _, r := range cmd {
		switch {
		case r == ' ' && !inQuote:
			if current != "" {
				result = append(result, current)
				current = ""
			}
		case (r == '"' || r == '\''):
			if inQuote && r == quoteChar {
				inQuote = false
				quoteChar = 0
			} else if !inQuote {
				inQuote = true
				quoteChar = r
			} else {
				current += string(r)
			}
		default:
			current += string(r)
		}
	}

	if current != "" {
		result = append(result, current)
	}

	return result
}

// Disconnect closes the connection to the upstream server
func (c *Client) Disconnect() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.client != nil {
		c.logger.Info("Disconnecting from upstream MCP server")
		if c.upstreamLogger != nil {
			c.upstreamLogger.Info("Disconnecting client")
		}

		c.client.Close()
		c.connected = false
	}
	return nil
}

// IsConnected returns whether the client is currently connected
func (c *Client) IsConnected() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.connected
}

// IsConnecting returns whether the client is currently connecting
func (c *Client) IsConnecting() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.connecting
}

// GetServerInfo returns the server information from initialization
func (c *Client) GetServerInfo() *mcp.InitializeResult {
	return c.serverInfo
}

// GetLastError returns the last error encountered
func (c *Client) GetLastError() error {
	return c.lastError
}

// ListTools retrieves available tools from the upstream server
func (c *Client) ListTools(ctx context.Context) ([]*config.ToolMetadata, error) {
	// Fast path: if we recently cached the tools list, return it immediately
	if cached := c.getCachedTools(); cached != nil {
		c.logger.Debug("ListTools returning cached tools", zap.Int("count", len(cached)))
		return cached, nil
	}

	// Use request deduplication to prevent multiple concurrent requests
	requestID, shouldProceed, waiter := c.startListToolsRequest()

	if !shouldProceed {
		// Another request is in progress, wait for it to complete
		select {
		case <-waiter:
			// Request completed, return the cached result
			return c.getListToolsResult()
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	// We're the primary request, proceed with the actual ListTools call
	var finalResult []*config.ToolMetadata
	var finalError error

	defer func() {
		// Update circuit breaker state based on result
		c.mu.Lock()
		if finalError != nil {
			c.listToolsFailureCount++
			c.listToolsLastFailure = time.Now()
			// Open circuit after 3 consecutive failures
			if c.listToolsFailureCount >= 3 {
				c.listToolsCircuitOpen = true
				c.logger.Warn("ListTools circuit breaker opened due to consecutive failures",
					zap.String("upstream_id", c.id),
					zap.String("upstream_name", c.config.Name),
					zap.Int("failure_count", c.listToolsFailureCount))
			}
		} else {
			// Reset on success
			c.listToolsFailureCount = 0
			c.listToolsCircuitOpen = false
		}
		c.mu.Unlock()

		c.finishListToolsRequest(requestID, finalResult, finalError)
	}()

	// Check circuit breaker
	c.mu.RLock()
	circuitOpen := c.listToolsCircuitOpen
	lastFailure := c.listToolsLastFailure
	failureCount := c.listToolsFailureCount
	c.mu.RUnlock()

	if circuitOpen {
		// Check if enough time has passed to try again (exponential backoff)
		backoffDuration := time.Duration(math.Pow(2, float64(failureCount-3))) * time.Minute
		if backoffDuration > 10*time.Minute {
			backoffDuration = 10 * time.Minute
		}

		if time.Since(lastFailure) < backoffDuration {
			c.logger.Debug("ListTools circuit breaker is open, skipping request",
				zap.String("upstream_id", c.id),
				zap.String("upstream_name", c.config.Name),
				zap.Duration("backoff_remaining", backoffDuration-time.Since(lastFailure)))
			finalError = fmt.Errorf("ListTools circuit breaker is open due to repeated failures")
			return nil, finalError
		}

		c.logger.Debug("ListTools circuit breaker trying half-open state",
			zap.String("upstream_id", c.id),
			zap.String("upstream_name", c.config.Name))
	}

	c.mu.RLock()
	connected := c.connected
	client := c.client
	oauthPending := c.oauthPending
	c.mu.RUnlock()

	// Debug: Check OAuth token state before ListTools
	hasStoredToken := false
	var tokenInfo string
	var tokenExpired bool
	if c.config.OAuth != nil && c.config.OAuth.TokenStorage != nil {
		hasStoredToken = true
		tokenExpired = time.Now().After(c.config.OAuth.TokenStorage.ExpiresAt)
		tokenInfo = fmt.Sprintf("type=%s, expires_at=%v, expired=%t",
			c.config.OAuth.TokenStorage.TokenType,
			c.config.OAuth.TokenStorage.ExpiresAt,
			tokenExpired)
	}

	c.logger.Debug("ListTools request starting",
		zap.String("upstream_id", c.id),
		zap.String("upstream_name", c.config.Name),
		zap.Bool("connected", connected),
		zap.Bool("oauth_pending", oauthPending),
		zap.Bool("has_stored_token", hasStoredToken),
		zap.Bool("token_expired", tokenExpired),
		zap.String("token_info", tokenInfo))

	// If OAuth is pending, return cached tools if available
	if oauthPending {
		c.logger.Debug("OAuth pending, checking for cached tools")
		cachedTools := c.getCachedTools()
		if cachedTools != nil {
			c.logger.Debug("Returning cached tools", zap.Int("count", len(cachedTools)))
			// Mark tools as OAuth pending
			for _, tool := range cachedTools {
				tool.Status = config.ConnectionStateOAuthPending
			}
			finalResult = cachedTools
			return cachedTools, nil
		}
		c.logger.Debug("No cached tools available for OAuth pending server")
		finalResult = nil
		return nil, nil
	}

	if !connected || client == nil {
		finalError = fmt.Errorf("client not connected")
		return nil, finalError
	}

	// Try to refresh token if needed before making the request
	if err := c.refreshTokenIfNeeded(ctx); err != nil {
		c.logger.Warn("Token refresh failed before ListTools",
			zap.String("upstream_id", c.id),
			zap.String("upstream_name", c.config.Name),
			zap.Error(err))
		// Continue anyway, as the request might still work
	}

	// Check if server supports tools
	c.mu.RLock()
	serverInfo := c.serverInfo
	c.mu.RUnlock()

	// If initialization never completed we might have a connected flag without
	// serverInfo populated. Avoid nil-pointer panics and signal caller to retry
	// after a successful Connect.
	if serverInfo == nil {
		finalError = fmt.Errorf("server not initialized")
		return nil, finalError
	}

	if serverInfo.Capabilities.Tools == nil {
		c.logger.Debug("Server does not support tools")
		finalResult = nil
		return nil, nil
	}

	toolsRequest := mcp.ListToolsRequest{}

	c.logger.Debug("Making ListTools MCP request",
		zap.String("upstream_id", c.id),
		zap.String("upstream_name", c.config.Name),
		zap.String("request_type", "tools/list"))

	toolsResult, err := client.ListTools(ctx, toolsRequest)
	if err != nil {
		c.logger.Debug("ListTools MCP request failed",
			zap.String("upstream_id", c.id),
			zap.String("upstream_name", c.config.Name),
			zap.Error(err),
			zap.String("error_type", fmt.Sprintf("%T", err)),
			zap.String("error_details", err.Error()))

		// Check if this is an OAuth authorization required error
		if c.isOAuthAuthorizationRequired(err) {
			c.logger.Info("OAuth authorization required during ListTools")
			if c.upstreamLogger != nil {
				c.upstreamLogger.Info("OAuth authorization required during ListTools")
			}

			// Auto-initialize OAuth configuration if not present
			if c.config.OAuth == nil {
				c.logger.Info("Auto-initializing OAuth configuration for ListTools")
				c.config.OAuth = c.createAutoOAuthConfig()
			}

			// Set OAuth pending state
			c.setOAuthPending(true, err)

			// For local deployments, automatically trigger OAuth flow
			deploymentType := c.detectDeploymentType()
			if deploymentType == DeploymentLocal {
				go c.triggerOAuthFlowAsync(ctx)
			}

			// Return nil to indicate tools are not yet available due to OAuth pending
			finalResult = nil
			return nil, nil
		}

		c.mu.Lock()
		c.lastError = err

		// Log to both main and server logs for critical errors
		c.logger.Error("ListTools failed", zap.Error(err))
		if c.upstreamLogger != nil {
			c.upstreamLogger.Error("ListTools failed", zap.Error(err))
		}

		// Check if this is a connection error that indicates the connection is broken
		errStr := err.Error()
		if strings.Contains(errStr, "broken pipe") ||
			strings.Contains(errStr, "connection reset") ||
			strings.Contains(errStr, "EOF") ||
			strings.Contains(errStr, "connection refused") ||
			strings.Contains(errStr, "transport error") {

			// Log pipe errors to both main and server logs
			c.logger.Warn("Connection appears broken, updating state", zap.Error(err))
			if c.upstreamLogger != nil {
				c.upstreamLogger.Warn("Connection broken detected", zap.Error(err))
			}

			c.connected = false
		}
		c.mu.Unlock()

		finalError = fmt.Errorf("failed to list tools: %w", err)
		return nil, finalError
	}

	c.logger.Debug("ListTools successful", zap.Int("tools_count", len(toolsResult.Tools)))

	// Convert MCP tools to our metadata format
	var tools []*config.ToolMetadata
	for i := range toolsResult.Tools {
		tool := &toolsResult.Tools[i]
		// Compute hash of tool definition
		toolHash := hash.ComputeToolHash(c.config.Name, tool.Name, tool.InputSchema)

		metadata := &config.ToolMetadata{
			Name:        fmt.Sprintf("%s:%s", c.config.Name, tool.Name),
			ServerName:  c.config.Name,
			Description: tool.Description,
			Hash:        toolHash,
			ParamsJSON:  "",          // Will be filled from InputSchema if needed
			Status:      "available", // Tools are available when connected
		}

		// Convert InputSchema to JSON string if present
		if schemaBytes, err := tool.InputSchema.MarshalJSON(); err == nil {
			metadata.ParamsJSON = string(schemaBytes)
		}

		tools = append(tools, metadata)
	}

	// Cache tools for a short period to avoid excessive polling
	c.setCachedTools(tools, listToolsCacheDuration)

	c.logger.Debug("Listed tools from upstream server", zap.Int("count", len(tools)))
	finalResult = tools
	return tools, nil
}

// CallTool calls a specific tool on the upstream server
func (c *Client) CallTool(ctx context.Context, toolName string, args map[string]interface{}) (interface{}, error) {
	c.mu.RLock()
	connected := c.connected
	client := c.client
	serverInfo := c.serverInfo
	oauthPending := c.oauthPending
	c.mu.RUnlock()

	// Debug: Check OAuth token state before CallTool
	hasStoredToken := false
	var tokenInfo string
	var tokenExpired bool
	if c.config.OAuth != nil && c.config.OAuth.TokenStorage != nil {
		hasStoredToken = true
		tokenExpired = time.Now().After(c.config.OAuth.TokenStorage.ExpiresAt)
		tokenInfo = fmt.Sprintf("type=%s, expires_at=%v, expired=%t",
			c.config.OAuth.TokenStorage.TokenType,
			c.config.OAuth.TokenStorage.ExpiresAt,
			tokenExpired)
	}

	c.logger.Debug("CallTool request starting",
		zap.String("upstream_id", c.id),
		zap.String("upstream_name", c.config.Name),
		zap.String("tool_name", toolName),
		zap.Bool("connected", connected),
		zap.Bool("oauth_pending", oauthPending),
		zap.Bool("has_stored_token", hasStoredToken),
		zap.Bool("token_expired", tokenExpired),
		zap.String("token_info", tokenInfo))

	// If OAuth is pending, trigger the OAuth flow now
	if oauthPending {
		c.logger.Info("OAuth pending, triggering OAuth flow for tool call",
			zap.String("tool_name", toolName))
		if c.upstreamLogger != nil {
			c.upstreamLogger.Info("Triggering OAuth flow for tool call",
				zap.String("tool", toolName))
		}

		// Perform OAuth flow
		if err := c.handleOAuthFlow(ctx); err != nil {
			c.logger.Error("OAuth flow failed during tool call", zap.Error(err))
			return nil, fmt.Errorf("OAuth flow failed: %w", err)
		}

		// Reconnect after OAuth
		if err := c.Connect(ctx); err != nil {
			c.logger.Error("Failed to reconnect after OAuth", zap.Error(err))
			return nil, fmt.Errorf("failed to reconnect after OAuth: %w", err)
		}

		// Update connection state
		c.mu.RLock()
		connected = c.connected
		client = c.client
		serverInfo = c.serverInfo
		c.mu.RUnlock()
	}

	if !connected || client == nil {
		return nil, fmt.Errorf("client not connected")
	}

	// Try to refresh token if needed before making the request
	if err := c.refreshTokenIfNeeded(ctx); err != nil {
		c.logger.Warn("Token refresh failed before CallTool",
			zap.String("upstream_id", c.id),
			zap.String("upstream_name", c.config.Name),
			zap.String("tool_name", toolName),
			zap.Error(err))
		// Continue anyway, as the request might still work
	}

	// Check if server supports tools
	if serverInfo.Capabilities.Tools == nil {
		return nil, fmt.Errorf("server does not support tools")
	}

	// Prepare the tool call request
	request := mcp.CallToolRequest{}
	request.Params.Name = toolName
	if args != nil {
		request.Params.Arguments = args
	}

	c.logger.Debug("Calling tool on upstream server",
		zap.String("tool_name", toolName),
		zap.Any("args", args))

	// Log detailed transport debug information if DEBUG level is enabled
	if c.upstreamLogger != nil {
		c.upstreamLogger.Debug("[Client→Server] tools/call",
			zap.String("tool", toolName))

		// Only log full request/response JSON if DEBUG level is enabled
		if c.logger.Core().Enabled(zap.DebugLevel) {
			if reqBytes, err := json.Marshal(request); err == nil {
				c.upstreamLogger.Debug(string(reqBytes))
			}
		}
	}

	c.logger.Debug("Making CallTool MCP request",
		zap.String("upstream_id", c.id),
		zap.String("upstream_name", c.config.Name),
		zap.String("tool_name", toolName),
		zap.String("request_type", "tools/call"))

	result, err := client.CallTool(ctx, request)
	if err != nil {
		c.logger.Debug("CallTool MCP request failed",
			zap.String("upstream_id", c.id),
			zap.String("upstream_name", c.config.Name),
			zap.String("tool_name", toolName),
			zap.Error(err),
			zap.String("error_type", fmt.Sprintf("%T", err)))
		c.mu.Lock()
		c.lastError = err

		// Log to both main and server logs for critical errors
		c.logger.Error("CallTool failed", zap.String("tool", toolName), zap.Error(err))
		if c.upstreamLogger != nil {
			c.upstreamLogger.Error("Tool call failed", zap.String("tool", toolName), zap.Error(err))
		}

		// Check if this is a connection error that indicates the connection is broken
		errStr := err.Error()
		if strings.Contains(errStr, "broken pipe") ||
			strings.Contains(errStr, "connection reset") ||
			strings.Contains(errStr, "EOF") ||
			strings.Contains(errStr, "connection refused") ||
			strings.Contains(errStr, "transport error") {

			// Log pipe errors to both main and server logs
			c.logger.Warn("Connection appears broken during tool call, updating state",
				zap.String("tool", toolName), zap.Error(err))
			if c.upstreamLogger != nil {
				c.upstreamLogger.Warn("Connection broken during tool call", zap.Error(err))
			}

			c.connected = false
		}
		c.mu.Unlock()

		return nil, fmt.Errorf("failed to call tool %s: %w", toolName, err)
	}

	c.logger.Debug("CallTool successful", zap.String("tool", toolName))

	// Log successful response to upstream logger
	if c.upstreamLogger != nil {
		c.upstreamLogger.Debug("[Server→Client] tools/call response")

		// Only log full response JSON if DEBUG level is enabled
		if c.logger.Core().Enabled(zap.DebugLevel) {
			if respBytes, err := json.Marshal(result); err == nil {
				c.upstreamLogger.Debug(string(respBytes))
			}
		}
	}

	// Extract content from result
	if len(result.Content) > 0 {
		// Return the content array directly
		return result.Content, nil
	}

	// If there's an error in the result, return it
	if result.IsError {
		return nil, fmt.Errorf("tool call failed: error indicated in result")
	}

	return result, nil
}

// ListResources retrieves available resources from the upstream server (if supported)
func (c *Client) ListResources(ctx context.Context) ([]interface{}, error) {
	c.mu.RLock()
	connected := c.connected
	client := c.client
	serverInfo := c.serverInfo
	c.mu.RUnlock()

	if !connected || client == nil {
		return nil, fmt.Errorf("client not connected")
	}

	// Check if server supports resources
	if serverInfo.Capabilities.Resources == nil {
		c.logger.Debug("Server does not support resources")
		return nil, nil
	}

	resourcesRequest := mcp.ListResourcesRequest{}
	resourcesResult, err := client.ListResources(ctx, resourcesRequest)
	if err != nil {
		c.mu.Lock()
		c.lastError = err
		c.mu.Unlock()
		return nil, fmt.Errorf("failed to list resources: %w", err)
	}

	// Convert to generic interface slice
	var resources []interface{}
	for _, resource := range resourcesResult.Resources {
		resources = append(resources, resource)
	}

	c.logger.Debug("Listed resources from upstream server", zap.Int("count", len(resources)))
	return resources, nil
}

// refreshTokenIfNeeded checks if the OAuth token is expired and refreshes it if possible
func (c *Client) refreshTokenIfNeeded(ctx context.Context) error {
	if c.config.OAuth == nil || c.config.OAuth.TokenStorage == nil {
		return nil // No OAuth configured
	}

	// Check if token is expired or about to expire (within 5 minutes)
	if time.Now().Add(5 * time.Minute).Before(c.config.OAuth.TokenStorage.ExpiresAt) {
		return nil // Token is still valid
	}

	c.logger.Info("OAuth token expired or expiring soon, attempting refresh",
		zap.String("upstream_id", c.id),
		zap.String("upstream_name", c.config.Name),
		zap.Time("expires_at", c.config.OAuth.TokenStorage.ExpiresAt),
		zap.Bool("has_refresh_token", c.config.OAuth.TokenStorage.RefreshToken != ""))

	// Check if we have a refresh token
	if c.config.OAuth.TokenStorage.RefreshToken == "" {
		c.logger.Warn("No refresh token available, cannot refresh",
			zap.String("upstream_id", c.id),
			zap.String("upstream_name", c.config.Name))
		return fmt.Errorf("token expired and no refresh token available")
	}

	// Use oauth2.Config to refresh the token
	if c.config.OAuth.TokenEndpoint == "" {
		return fmt.Errorf("no token endpoint configured for refresh")
	}

	conf := &oauth2.Config{
		ClientID:     c.config.OAuth.ClientID,
		ClientSecret: c.config.OAuth.ClientSecret,
		Endpoint: oauth2.Endpoint{
			TokenURL: c.config.OAuth.TokenEndpoint,
		},
	}

	// Convert stored token to oauth2.Token
	oldToken := &oauth2.Token{
		AccessToken:  c.config.OAuth.TokenStorage.AccessToken,
		RefreshToken: c.config.OAuth.TokenStorage.RefreshToken,
		TokenType:    c.config.OAuth.TokenStorage.TokenType,
		Expiry:       c.config.OAuth.TokenStorage.ExpiresAt,
	}

	// Refresh the token
	tokenSource := conf.TokenSource(ctx, oldToken)
	newToken, err := tokenSource.Token()
	if err != nil {
		c.logger.Error("Failed to refresh OAuth token",
			zap.String("upstream_id", c.id),
			zap.String("upstream_name", c.config.Name),
			zap.Error(err))
		return fmt.Errorf("failed to refresh token: %w", err)
	}

	// Update stored token
	c.config.OAuth.TokenStorage = &config.TokenStorage{
		AccessToken:  newToken.AccessToken,
		RefreshToken: newToken.RefreshToken,
		ExpiresAt:    newToken.Expiry,
		TokenType:    newToken.TokenType,
	}

	// Also update the oauth2.Token field
	c.mu.Lock()
	c.oauthToken = newToken
	c.mu.Unlock()

	c.logger.Info("OAuth token refreshed successfully",
		zap.String("upstream_id", c.id),
		zap.String("upstream_name", c.config.Name),
		zap.Time("new_expires_at", newToken.Expiry),
		zap.String("new_expires_in", newToken.Expiry.Sub(time.Now()).String()))

	// Save refreshed tokens to storage for persistence
	if err := c.saveOAuthTokensToStorage(c.config.OAuth.TokenStorage); err != nil {
		c.logger.Warn("Failed to persist refreshed OAuth tokens to storage", zap.Error(err))
		// Continue anyway - tokens are still in memory
	} else {
		c.logger.Debug("Refreshed OAuth tokens persisted to storage successfully")
	}

	// CRITICAL: Force SSE client reconnection with new tokens
	// The existing SSE client was created with the old expired token headers
	if c.client != nil {
		c.logger.Info("Forcing SSE client reconnection due to token refresh",
			zap.String("upstream_id", c.id),
			zap.String("upstream_name", c.config.Name))

		// Disconnect current client
		c.client.Close()
		c.mu.Lock()
		c.connected = false
		c.client = nil
		c.mu.Unlock()

		// This will trigger a new connection attempt with fresh tokens
		c.logger.Debug("SSE client disconnected, will need fresh connection with new OAuth tokens")
	}

	return nil
}

// isOAuthAuthorizationRequired checks if the error indicates OAuth authorization is required
func (c *Client) isOAuthAuthorizationRequired(err error) bool {
	if err == nil {
		return false
	}

	// Check current OAuth state
	hasStoredToken := false
	var tokenValidInfo string
	if c.config.OAuth != nil && c.config.OAuth.TokenStorage != nil {
		hasStoredToken = true
		tokenValidInfo = fmt.Sprintf("type=%s, expires_at=%v",
			c.config.OAuth.TokenStorage.TokenType,
			c.config.OAuth.TokenStorage.ExpiresAt)
	}

	// Check for OAuth authorization required error from mcp-go library
	isRequired := false
	if strings.Contains(err.Error(), "authorization required") ||
		strings.Contains(err.Error(), "no valid token available") ||
		strings.Contains(err.Error(), "unauthorized") {
		isRequired = true
	}

	// Check for HTTP 401 unauthorized errors
	if strings.Contains(err.Error(), "401") {
		isRequired = true
	}

	// Check for timeout errors on known OAuth-enabled servers
	// Some servers (like Cloudflare AutoRAG) don't respond to MCP requests until OAuth is completed
	if strings.Contains(err.Error(), "context deadline exceeded") ||
		strings.Contains(err.Error(), "timeout") {
		// Only treat timeouts as OAuth requirement for known OAuth servers and when no token is available
		if !hasStoredToken {
			// Check if this is a known OAuth-enabled server
			if strings.Contains(c.config.URL, "mcp.cloudflare.com") ||
				strings.Contains(c.config.URL, "builds.mcp.cloudflare.com") ||
				strings.Contains(c.config.URL, "autorag.mcp.cloudflare.com") {
				isRequired = true
			}
			// If OAuth config exists but no token, likely OAuth-enabled
			if c.config.OAuth != nil && c.config.OAuth.AuthorizationEndpoint != "" {
				isRequired = true
			}
		}
	}

	c.logger.Debug("OAuth requirement check",
		zap.String("upstream_id", c.id),
		zap.String("upstream_name", c.config.Name),
		zap.Error(err),
		zap.Bool("oauth_required", isRequired),
		zap.Bool("has_stored_token", hasStoredToken),
		zap.String("token_info", tokenValidInfo))

	return isRequired
}

// handleOAuthFlow handles the OAuth authorization flow with auto-discovery
func (c *Client) handleOAuthFlow(ctx context.Context) error {
	c.logger.Debug("Starting handleOAuthFlow")
	// Initialize OAuth configuration if not present with auto-discovery enabled by default
	if c.config.OAuth == nil {
		c.logger.Debug("OAuth config is nil, initializing")
		c.config.OAuth = &config.OAuthConfig{
			AutoDiscovery: &config.OAuthAutoDiscovery{
				Enabled:           true,
				PromptForClientID: false, // Default to false - only prompt when actually needed
				AutoDeviceFlow:    true,
			},
		}
	}

	// Perform auto-discovery (enabled by default)
	c.logger.Debug("Performing OAuth auto-discovery")
	if err := c.performOAuthAutoDiscovery(ctx); err != nil {
		c.logger.Warn("OAuth auto-discovery failed, continuing with manual configuration", zap.Error(err))
		if c.upstreamLogger != nil {
			c.upstreamLogger.Warn("OAuth auto-discovery failed", zap.Error(err))
		}
	}

	// Perform dynamic client registration if enabled
	c.logger.Debug("Performing dynamic client registration")
	if err := c.performDynamicClientRegistration(ctx); err != nil {
		c.logger.Warn("Dynamic Client Registration failed, continuing with manual configuration", zap.Error(err))
		if c.upstreamLogger != nil {
			c.upstreamLogger.Warn("Dynamic Client Registration failed", zap.Error(err))
		}
	}

	// Prompt for client ID now if it's missing (when OAuth flow actually starts)
	if c.config.OAuth.ClientID == "" {
		// For authorization code flow, we can try without client ID (public client)
		// Only prompt for client ID if we're using device code flow or if explicitly required
		if c.config.OAuth.FlowType == config.OAuthFlowDeviceCode ||
			(c.config.OAuth.FlowType == "" && c.config.OAuth.AutoDiscovery != nil && c.config.OAuth.AutoDiscovery.PromptForClientID) {
			if err := c.promptForClientID(); err != nil {
				return fmt.Errorf("failed to get client ID: %w", err)
			}
		} else {
			// Check if this is a known server that requires client registration
			if strings.Contains(c.config.URL, "mcp.cloudflare.com") || strings.Contains(c.config.URL, "builds.mcp.cloudflare.com") {
				// For Cloudflare servers, try to use Dynamic Client Registration first
				if c.config.OAuth.DynamicClientRegistration != nil && c.config.OAuth.DynamicClientRegistration.Enabled {
					c.logger.Info("Attempting Dynamic Client Registration for Cloudflare server")
					if err := c.performDynamicClientRegistration(ctx); err != nil {
						c.logger.Warn("Dynamic Client Registration failed for Cloudflare server", zap.Error(err))
						return fmt.Errorf("Cloudflare MCP servers require OAuth client registration. Dynamic Client Registration failed: %w.\n"+
							"Please visit the Cloudflare dashboard to register an OAuth client manually:\n"+
							"1. Visit the Cloudflare dashboard\n"+
							"2. Register an OAuth client for MCP access\n"+
							"3. Add the client_id to your server configuration\n"+
							"4. Set the redirect URI to: http://127.0.0.1:*/oauth/callback\n"+
							"For more info, see: https://developers.cloudflare.com/agents/model-context-protocol/authorization/", err)
					}
				} else {
					return fmt.Errorf("Cloudflare MCP servers require OAuth client registration.\n" +
						"Your server configuration has been automatically initialized with OAuth settings.\n" +
						"To complete setup, please:\n" +
						"1. Visit the Cloudflare dashboard\n" +
						"2. Register an OAuth client for MCP access\n" +
						"3. Add the client_id to your server configuration\n" +
						"4. Set the redirect URI to: http://127.0.0.1:*/oauth/callback\n" +
						"For more info, see: https://developers.cloudflare.com/agents/model-context-protocol/authorization/")
				}
			} else {
				c.logger.Info("Proceeding with OAuth flow without client ID (public client)")
			}
		}
	}

	// Determine which OAuth flow to use based on deployment type
	flowType := c.selectOAuthFlow()

	c.logger.Info("Selected OAuth flow type",
		zap.String("flow_type", flowType),
		zap.String("deployment_type", c.detectDeploymentType().String()))
	if c.upstreamLogger != nil {
		c.upstreamLogger.Info("Selected OAuth flow type",
			zap.String("flow_type", flowType),
			zap.String("deployment_type", c.detectDeploymentType().String()))
	}

	switch flowType {
	case config.OAuthFlowAuthorizationCode:
		return c.handleAuthorizationCodeFlow(ctx)
	case config.OAuthFlowDeviceCode:
		return c.handleDeviceCodeFlow(ctx)
	default:
		return fmt.Errorf("unsupported OAuth flow type: %s", flowType)
	}
}

// performOAuthAutoDiscovery attempts to auto-discover OAuth configuration
func (c *Client) performOAuthAutoDiscovery(ctx context.Context) error {
	// Initialize OAuth config if nil
	if c.config.OAuth == nil {
		c.config.OAuth = &config.OAuthConfig{}
	}

	// Check if auto-discovery is enabled
	if c.config.OAuth.AutoDiscovery == nil {
		// Default to enabled for auto-discovery
		c.config.OAuth.AutoDiscovery = &config.OAuthAutoDiscovery{
			Enabled:           true,
			PromptForClientID: false, // Default to false - only prompt when OAuth flow actually starts
			AutoDeviceFlow:    true,
		}
	}

	if !c.config.OAuth.AutoDiscovery.Enabled {
		return nil
	}

	// Only perform discovery for HTTP/HTTPS protocols
	if c.config.Protocol != transportHTTP && c.config.Protocol != transportSSE && c.config.Protocol != transportStreamableHTTP {
		return fmt.Errorf("OAuth auto-discovery only supported for HTTP protocols")
	}

	c.logger.Info("Starting OAuth auto-discovery", zap.String("server_url", c.config.URL))
	if c.upstreamLogger != nil {
		c.upstreamLogger.Info("Starting OAuth auto-discovery")
	}

	// Create OAuth handler for discovery
	oauthConfig := transport.OAuthConfig{
		ClientID:     c.config.OAuth.ClientID,
		ClientSecret: c.config.OAuth.ClientSecret,
		Scopes:       c.config.OAuth.Scopes,
	}

	// Set custom metadata URL if provided
	if c.config.OAuth.AutoDiscovery.MetadataURL != "" {
		oauthConfig.AuthServerMetadataURL = c.config.OAuth.AutoDiscovery.MetadataURL
	}

	oauthHandler := transport.NewOAuthHandler(oauthConfig)

	// Extract base URL from server URL
	serverURL, err := url.Parse(c.config.URL)
	if err != nil {
		return fmt.Errorf("failed to parse server URL: %w", err)
	}

	baseURL := fmt.Sprintf("%s://%s", serverURL.Scheme, serverURL.Host)
	c.logger.Debug("Setting OAuth discovery base URL",
		zap.String("server_url", c.config.URL),
		zap.String("base_url", baseURL))
	oauthHandler.SetBaseURL(baseURL)

	// Discover OAuth server metadata
	c.logger.Debug("Starting OAuth metadata discovery", zap.String("base_url", baseURL))
	metadata, err := oauthHandler.GetServerMetadata(ctx)
	if err != nil {
		return fmt.Errorf("failed to discover OAuth metadata: %w", err)
	}

	c.logger.Debug("Raw OAuth metadata discovered", zap.Any("metadata", metadata))
	c.logger.Info("OAuth metadata discovery request details",
		zap.String("base_url_used", baseURL),
		zap.String("discovered_auth_endpoint", metadata.AuthorizationEndpoint),
		zap.String("discovered_token_endpoint", metadata.TokenEndpoint),
		zap.String("discovered_registration_endpoint", metadata.RegistrationEndpoint))

	c.logger.Info("OAuth metadata discovered successfully",
		zap.String("authorization_endpoint", metadata.AuthorizationEndpoint),
		zap.String("token_endpoint", metadata.TokenEndpoint))

	if c.upstreamLogger != nil {
		c.upstreamLogger.Info("OAuth metadata discovered",
			zap.String("authorization_endpoint", metadata.AuthorizationEndpoint),
			zap.String("token_endpoint", metadata.TokenEndpoint))
	}

	// Update OAuth configuration with discovered endpoints (only if not manually configured)
	if c.config.OAuth.AuthorizationEndpoint == "" {
		// HOTFIX: Cloudflare-specific endpoint correction
		authEndpoint := metadata.AuthorizationEndpoint
		if strings.Contains(c.config.URL, "mcp.cloudflare.com") {
			// Cloudflare returns /authorize but actually uses /oauth/authorize
			if strings.HasSuffix(authEndpoint, "/authorize") && !strings.HasSuffix(authEndpoint, "/oauth/authorize") {
				authEndpoint = strings.Replace(authEndpoint, "/authorize", "/oauth/authorize", 1)
				c.logger.Info("Applied Cloudflare OAuth endpoint fix",
					zap.String("discovered", metadata.AuthorizationEndpoint),
					zap.String("corrected", authEndpoint))
			}
		}

		c.config.OAuth.AuthorizationEndpoint = authEndpoint
		c.logger.Debug("Updated AuthorizationEndpoint from auto-discovery", zap.String("endpoint", authEndpoint))
	}
	if c.config.OAuth.TokenEndpoint == "" {
		c.config.OAuth.TokenEndpoint = metadata.TokenEndpoint
		c.logger.Debug("Updated TokenEndpoint from auto-discovery", zap.String("endpoint", metadata.TokenEndpoint))
	}

	// Set registration endpoint if available for DCR
	if metadata.RegistrationEndpoint != "" {
		c.config.OAuth.RegistrationEndpoint = metadata.RegistrationEndpoint
		c.logger.Debug("Found registration endpoint for DCR", zap.String("registration_endpoint", metadata.RegistrationEndpoint))
	}

	// Set device endpoint if available (GitHub, Google, etc.)
	if metadata.AuthorizationEndpoint != "" {
		// Try common device endpoint patterns
		deviceEndpoint := c.inferDeviceEndpoint(metadata.AuthorizationEndpoint)
		if deviceEndpoint != "" {
			c.config.OAuth.DeviceEndpoint = deviceEndpoint
		}
	}

	// Auto-select appropriate flow if enabled and no flow is specified
	if c.config.OAuth.AutoDiscovery.AutoDeviceFlow && c.config.OAuth.FlowType == "" {
		// Use the new flexible flow selection based on deployment type
		selectedFlow := c.selectOAuthFlow()
		c.config.OAuth.FlowType = selectedFlow
		c.logger.Info("Auto-selected OAuth flow",
			zap.String("flow_type", selectedFlow),
			zap.String("deployment_type", c.detectDeploymentType().String()))
	}

	// Note: Client ID prompting moved to handleOAuthFlow() where it's actually needed

	// Update suggested scopes from metadata if available
	if len(metadata.ScopesSupported) > 0 && len(c.config.OAuth.Scopes) == 0 {
		c.config.OAuth.Scopes = c.selectDefaultScopes(metadata.ScopesSupported)
	}

	return nil
}

// inferDeviceEndpoint tries to infer the device endpoint from the authorization endpoint
func (c *Client) inferDeviceEndpoint(authEndpoint string) string {
	authURL, err := url.Parse(authEndpoint)
	if err != nil {
		return ""
	}

	// Common device endpoint patterns
	devicePatterns := map[string]string{
		"github.com":                "https://github.com/login/device/code",
		"googleapis.com":            "https://oauth2.googleapis.com/device/code",
		"accounts.google.com":       "https://oauth2.googleapis.com/device/code",
		"login.microsoftonline.com": "https://login.microsoftonline.com/common/oauth2/v2.0/devicecode",
		"microsoft.com":             "https://login.microsoftonline.com/common/oauth2/v2.0/devicecode",
	}

	for domain, deviceEndpoint := range devicePatterns {
		if strings.Contains(authURL.Host, domain) {
			return deviceEndpoint
		}
	}

	// Generic pattern - try /device/code or /oauth/device
	baseURL := fmt.Sprintf("%s://%s", authURL.Scheme, authURL.Host)

	// Try common device endpoint paths
	devicePaths := []string{
		"/device/code",
		"/oauth/device/code",
		"/oauth/device",
		"/device",
	}

	for _, path := range devicePaths {
		deviceEndpoint := baseURL + path
		if c.testEndpointExists(deviceEndpoint) {
			return deviceEndpoint
		}
	}

	return ""
}

// testEndpointExists tests if an endpoint exists (returns 200 or 405)
func (c *Client) testEndpointExists(endpoint string) bool {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Head(endpoint)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	// Accept 200 OK or 405 Method Not Allowed (HEAD might not be supported)
	return resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusMethodNotAllowed
}

// promptForClientID prompts the user for OAuth client ID
func (c *Client) promptForClientID() error {
	if c.prompter == nil {
		return fmt.Errorf("no prompter available for client ID input")
	}

	message := fmt.Sprintf("OAuth client ID required for server '%s'.\nPlease enter your OAuth client ID: ", c.config.Name)
	clientID, err := c.prompter.PromptString(message)
	if err != nil {
		return fmt.Errorf("failed to prompt for client ID: %w", err)
	}

	if clientID == "" {
		return fmt.Errorf("client ID cannot be empty")
	}

	c.config.OAuth.ClientID = clientID
	c.logger.Info("Client ID provided interactively", zap.String("client_id", clientID))

	return nil
}

// selectDefaultScopes selects reasonable default scopes from supported scopes
func (c *Client) selectDefaultScopes(supportedScopes []string) []string {
	// Return empty slice for empty input
	if len(supportedScopes) == 0 {
		return []string{}
	}

	// Common useful scopes in order of preference
	preferredScopes := []string{"read", "write", "user", "repo", "openid", "profile", "email"}

	var selectedScopes []string
	for _, preferred := range preferredScopes {
		for _, supported := range supportedScopes {
			if strings.EqualFold(preferred, supported) {
				selectedScopes = append(selectedScopes, supported)
				break
			}
		}
	}

	// If no preferred scopes found, use the first few supported scopes
	if len(selectedScopes) == 0 && len(supportedScopes) > 0 {
		maxScopes := 3
		if len(supportedScopes) < maxScopes {
			maxScopes = len(supportedScopes)
		}
		selectedScopes = supportedScopes[:maxScopes]
	}

	// Ensure we return empty slice instead of nil
	if selectedScopes == nil {
		return []string{}
	}

	return selectedScopes
}

// handleAuthorizationCodeFlow handles the OAuth Authorization Code flow
func (c *Client) handleAuthorizationCodeFlow(ctx context.Context) error {
	c.logger.Info("Starting OAuth Authorization Code flow")
	if c.upstreamLogger != nil {
		c.upstreamLogger.Info("Starting OAuth Authorization Code flow")
	}

	oauthCfg := c.config.OAuth
	if oauthCfg.AuthorizationEndpoint == "" || oauthCfg.TokenEndpoint == "" {
		return fmt.Errorf("authorization or token endpoint not configured for OAuth")
	}

	// Generate PKCE parameters if required
	var pkceParams *PKCEParams
	if c.shouldUsePKCE() {
		c.logger.Info("Using PKCE for OAuth flow")
		var err error
		pkceParams, err = c.generatePKCEParams()
		if err != nil {
			return fmt.Errorf("failed to generate PKCE parameters: %w", err)
		}
	}

	// Use a channel to receive the result from the callback handler
	resultChan := make(chan error, 1)
	var token *oauth2.Token

	// RFC 8252 compliant: Use random/ephemeral ports for OAuth callback
	// This prevents port conflicts and follows security best practices
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("failed to start local server for oauth callback: %w", err)
	}
	defer listener.Close()

	port := listener.Addr().(*net.TCPAddr).Port
	redirectURL := fmt.Sprintf("http://127.0.0.1:%d/oauth/callback", port)

	// Two-Phase Solution: Perform DCR with exact redirect URI for Cloudflare compatibility
	// Always re-register for local deployments to ensure exact redirect URI matching
	if c.detectDeploymentType() == DeploymentLocal && oauthCfg.RegistrationEndpoint != "" {
		c.logger.Info("Performing DCR with exact redirect URI for Cloudflare compatibility",
			zap.String("redirect_uri", redirectURL),
			zap.String("previous_client_id", oauthCfg.ClientID))
		if err := c.performDCRWithExactRedirectURI(ctx, redirectURL); err != nil {
			c.logger.Warn("DCR with exact redirect URI failed", zap.Error(err))
		} else {
			c.logger.Info("Successfully re-registered client with exact redirect URI",
				zap.String("redirect_uri", redirectURL),
				zap.String("new_client_id", oauthCfg.ClientID))
		}
	}

	deploymentType := c.detectDeploymentType()
	if deploymentType == DeploymentLocal {
		c.logger.Info("Using random port for local OAuth flow (RFC 8252 compliant)",
			zap.String("redirect_uri", redirectURL),
			zap.Int("port", port))
	} else {
		// For remote deployments, use configured redirect URIs if available
		oauth := c.getOAuthConfig()
		if oauth.RedirectURI != "" || len(oauth.RedirectURIs) > 0 {
			redirectURIs := c.getRedirectURIs()
			if len(redirectURIs) > 0 {
				redirectURL = redirectURIs[0]
				c.logger.Info("Using configured redirect URI for remote deployment",
					zap.String("redirect_uri", redirectURL))
			}
		}

		// Start server on dynamic port for remote deployments
		listener, err = net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return fmt.Errorf("failed to start local server for oauth callback: %w", err)
		}
		port = listener.Addr().(*net.TCPAddr).Port
		if redirectURL == "" {
			redirectURL = fmt.Sprintf("http://127.0.0.1:%d/oauth/callback", port)
		}
	}
	defer listener.Close()

	// Create a state token for CSRF protection
	stateBytes := make([]byte, 32)
	if _, err := rand.Read(stateBytes); err != nil {
		return fmt.Errorf("failed to generate state token: %w", err)
	}
	state := base64.URLEncoding.EncodeToString(stateBytes)

	// Debug the OAuth configuration before building the URL
	c.logger.Debug("Building OAuth config for authorization code flow",
		zap.String("authorization_endpoint", oauthCfg.AuthorizationEndpoint),
		zap.String("token_endpoint", oauthCfg.TokenEndpoint),
		zap.String("client_id", oauthCfg.ClientID),
		zap.String("redirect_url", redirectURL))

	conf := &oauth2.Config{
		ClientID:     oauthCfg.ClientID,
		ClientSecret: oauthCfg.ClientSecret,
		Scopes:       oauthCfg.Scopes,
		Endpoint: oauth2.Endpoint{
			AuthURL:  oauthCfg.AuthorizationEndpoint,
			TokenURL: oauthCfg.TokenEndpoint,
		},
		RedirectURL: redirectURL,
	}

	// For public clients (no client ID), use PKCE for security
	var authURLOptions []oauth2.AuthCodeOption
	authURLOptions = append(authURLOptions, oauth2.AccessTypeOffline)

	// Add PKCE parameters if enabled
	if pkceParams != nil {
		authURLOptions = append(authURLOptions,
			oauth2.SetAuthURLParam("code_challenge", pkceParams.CodeChallenge),
			oauth2.SetAuthURLParam("code_challenge_method", pkceParams.Method))
	}

	// If no client ID is provided, this might be a public client
	if oauthCfg.ClientID == "" {
		c.logger.Info("Using public OAuth client (no client ID)")
		// For public clients, we might need to use different parameters
		// Some servers expect the client_id to be omitted entirely
	}

	authURL := conf.AuthCodeURL(state, authURLOptions...)

	// Debug the final authorization URL
	c.logger.Debug("Generated authorization URL",
		zap.String("auth_url", authURL),
		zap.String("oauth_config_auth_url", conf.Endpoint.AuthURL),
		zap.String("oauth_config_token_url", conf.Endpoint.TokenURL))

	// Open browser for user to authorize
	c.logger.Info("Opening browser for OAuth authorization", zap.String("url", authURL))
	if err := browser.OpenURL(authURL); err != nil {
		c.logger.Error("Failed to open browser, please navigate to the URL manually", zap.Error(err))
	}

	// Implement a simple HTTP server to handle the callback
	mux := http.NewServeMux()
	server := &http.Server{
		Addr:              listener.Addr().String(),
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second, // Protect against slowloris attacks
	}
	defer func() {
		if err := server.Shutdown(ctx); err != nil {
			c.logger.Warn("Failed to shutdown callback server", zap.Error(err))
		}
	}()

	mux.HandleFunc("/oauth/callback", func(w http.ResponseWriter, r *http.Request) {
		// Check for error response from provider
		if errMsg := r.URL.Query().Get("error"); errMsg != "" {
			errDesc := r.URL.Query().Get("error_description")
			http.Error(w, fmt.Sprintf("OAuth error: %s - %s", errMsg, errDesc), http.StatusBadRequest)
			resultChan <- fmt.Errorf("oauth provider error: %s", errMsg)
			return
		}

		// Verify state
		if r.URL.Query().Get("state") != state {
			http.Error(w, "Invalid state token", http.StatusBadRequest)
			resultChan <- fmt.Errorf("invalid state token")
			return
		}

		code := r.URL.Query().Get("code")
		if code == "" {
			http.Error(w, "Missing authorization code", http.StatusBadRequest)
			resultChan <- fmt.Errorf("missing authorization code")
			return
		}

		// Prepare token exchange parameters
		var tokenExchangeOptions []oauth2.AuthCodeOption
		if pkceParams != nil {
			tokenExchangeOptions = append(tokenExchangeOptions,
				oauth2.SetAuthURLParam("code_verifier", pkceParams.CodeVerifier))
		}

		// Exchange code for token
		tok, err := conf.Exchange(ctx, code, tokenExchangeOptions...)
		if err != nil {
			http.Error(w, "Failed to exchange token", http.StatusInternalServerError)
			resultChan <- fmt.Errorf("failed to exchange token: %w", err)
			return
		}
		token = tok

		// Respond to user and close server
		fmt.Fprintf(w, "Authorization successful! You can close this window.")
		resultChan <- nil
	})

	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			c.logger.Error("Callback server error", zap.Error(err))
			resultChan <- fmt.Errorf("callback server error: %w", err)
		}
	}()

	// Wait for result from callback handler or context cancellation
	c.logger.Debug("Waiting for OAuth callback or context cancellation",
		zap.String("callback_url", redirectURL))

	select {
	case err := <-resultChan:
		if err != nil {
			c.logger.Error("OAuth callback received error", zap.Error(err))
			return err
		}
		c.logger.Debug("OAuth callback completed successfully")
	case <-ctx.Done():
		c.logger.Error("OAuth flow context canceled",
			zap.Error(ctx.Err()),
			zap.String("callback_url", redirectURL))
		return ctx.Err()
	}

	// Store token
	oauthCfg.TokenStorage = &config.TokenStorage{
		AccessToken:  token.AccessToken,
		RefreshToken: token.RefreshToken,
		ExpiresAt:    token.Expiry,
		TokenType:    token.TokenType,
	}

	// Also store the oauth2.Token for automatic refresh capabilities
	c.mu.Lock()
	c.oauthToken = token
	c.mu.Unlock()

	c.logger.Info("OAuth tokens stored successfully",
		zap.String("upstream_id", c.id),
		zap.String("upstream_name", c.config.Name),
		zap.Bool("has_access_token", token.AccessToken != ""),
		zap.Bool("has_refresh_token", token.RefreshToken != ""),
		zap.String("token_type", token.TokenType),
		zap.String("expires_in", token.Expiry.Sub(time.Now()).String()))

	// Save tokens to storage for persistence across restarts
	if err := c.saveOAuthTokensToStorage(oauthCfg.TokenStorage); err != nil {
		c.logger.Warn("Failed to persist OAuth tokens to storage", zap.Error(err))
		// Continue anyway - tokens are still in memory
	} else {
		c.logger.Debug("OAuth tokens persisted to storage successfully")
	}

	c.logger.Info("OAuth Authorization Code flow completed successfully")
	return nil
}

// handleDeviceCodeFlow handles the OAuth Device Code flow for headless/remote scenarios
func (c *Client) handleDeviceCodeFlow(ctx context.Context) error {
	c.logger.Info("Starting OAuth Device Code flow")

	oauth := c.config.OAuth
	if oauth.DeviceEndpoint == "" {
		return fmt.Errorf("device endpoint not configured for OAuth Device Code flow")
	}

	// Get device code from authorization server
	deviceResp, err := c.requestDeviceCode(ctx, oauth)
	if err != nil {
		return fmt.Errorf("failed to request device code: %w", err)
	}

	// Display authorization URL and user code to user
	c.logger.Info("OAuth Device Code flow started",
		zap.String("authorization_url", deviceResp.VerificationURI),
		zap.String("user_code", deviceResp.UserCode),
		zap.Duration("expires_in", deviceResp.ExpiresIn))

	// Show notification to user via tray if enabled
	if oauth.DeviceFlow != nil && oauth.DeviceFlow.EnableNotification {
		c.showDeviceCodeNotification(deviceResp)
	}

	// Poll for token
	token, err := c.pollForToken(ctx, oauth, deviceResp)
	if err != nil {
		return fmt.Errorf("failed to get token: %w", err)
	}

	// Store token
	oauth.TokenStorage = &config.TokenStorage{
		AccessToken:  token.AccessToken,
		RefreshToken: token.RefreshToken,
		ExpiresAt:    time.Now().Add(token.ExpiresIn),
		TokenType:    token.TokenType,
	}

	c.logger.Info("OAuth Device Code flow completed successfully")
	return nil
}

// DeviceCodeResponse represents the response from device code request
type DeviceCodeResponse struct {
	DeviceCode              string        `json:"device_code"`
	UserCode                string        `json:"user_code"`
	VerificationURI         string        `json:"verification_uri"`
	VerificationURIComplete string        `json:"verification_uri_complete,omitempty"`
	ExpiresIn               time.Duration `json:"expires_in"`
	Interval                time.Duration `json:"interval"`
}

// TokenResponse represents the OAuth token response
type TokenResponse struct {
	AccessToken  string        `json:"access_token"`
	RefreshToken string        `json:"refresh_token,omitempty"`
	TokenType    string        `json:"token_type"`
	ExpiresIn    time.Duration `json:"expires_in"`
	Scope        string        `json:"scope,omitempty"`
}

// requestDeviceCode requests a device code from the authorization server
func (c *Client) requestDeviceCode(ctx context.Context, oauth *config.OAuthConfig) (*DeviceCodeResponse, error) {
	// Prepare request data
	data := url.Values{}
	data.Set("client_id", oauth.ClientID)
	if len(oauth.Scopes) > 0 {
		data.Set("scope", strings.Join(oauth.Scopes, " "))
	}

	// Create HTTP request
	req, err := http.NewRequestWithContext(ctx, "POST", oauth.DeviceEndpoint, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("failed to create device code request: %w", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	// Send request
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send device code request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("device code request failed with status %d", resp.StatusCode)
	}

	// Parse response
	var rawResp map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&rawResp); err != nil {
		return nil, fmt.Errorf("failed to decode device code response: %w", err)
	}

	deviceResp := &DeviceCodeResponse{
		DeviceCode:      getStringValue(rawResp, "device_code"),
		UserCode:        getStringValue(rawResp, "user_code"),
		VerificationURI: getStringValue(rawResp, "verification_uri"),
		ExpiresIn:       getDurationValue(rawResp, "expires_in", 600*time.Second),
		Interval:        getDurationValue(rawResp, "interval", 5*time.Second),
	}

	if complete, ok := rawResp["verification_uri_complete"].(string); ok {
		deviceResp.VerificationURIComplete = complete
	}

	return deviceResp, nil
}

// pollForToken polls the token endpoint until the user authorizes the device
func (c *Client) pollForToken(ctx context.Context, oauth *config.OAuthConfig, deviceResp *DeviceCodeResponse) (*TokenResponse, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	interval := deviceResp.Interval
	deadline := time.Now().Add(deviceResp.ExpiresIn)

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(interval):
			// Poll for token
			data := url.Values{}
			data.Set("grant_type", "urn:ietf:params:oauth:grant-type:device_code")
			data.Set("device_code", deviceResp.DeviceCode)
			data.Set("client_id", oauth.ClientID)

			req, err := http.NewRequestWithContext(ctx, "POST", oauth.TokenEndpoint, strings.NewReader(data.Encode()))
			if err != nil {
				return nil, fmt.Errorf("failed to create token request: %w", err)
			}

			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.Header.Set("Accept", "application/json")

			resp, err := client.Do(req)
			if err != nil {
				c.logger.Debug("Token poll request failed", zap.Error(err))
				continue
			}

			var rawResp map[string]interface{}
			if err := json.NewDecoder(resp.Body).Decode(&rawResp); err != nil {
				resp.Body.Close()
				c.logger.Debug("Failed to decode token response", zap.Error(err))
				continue
			}
			resp.Body.Close()

			if resp.StatusCode == http.StatusOK {
				// Success - got token
				token := &TokenResponse{
					AccessToken:  getStringValue(rawResp, "access_token"),
					RefreshToken: getStringValue(rawResp, "refresh_token"),
					TokenType:    getStringValue(rawResp, "token_type"),
					ExpiresIn:    getDurationValue(rawResp, "expires_in", 3600*time.Second),
				}
				if scope, ok := rawResp["scope"].(string); ok {
					token.Scope = scope
				}
				return token, nil
			} else if resp.StatusCode == http.StatusBadRequest {
				// Check for specific error codes
				if errorCode, ok := rawResp["error"].(string); ok {
					switch errorCode {
					case "authorization_pending":
						// User hasn't authorized yet, continue polling
						continue
					case "slow_down":
						// Server requests slower polling
						interval *= 2
						continue
					case "expired_token":
						return nil, fmt.Errorf("device code expired")
					case "access_denied":
						return nil, fmt.Errorf("user denied authorization")
					default:
						return nil, fmt.Errorf("OAuth error: %s", errorCode)
					}
				}
			}
		}
	}

	return nil, fmt.Errorf("device code expired")
}

// showDeviceCodeNotification shows device code notification to user via various channels
func (c *Client) showDeviceCodeNotification(deviceResp *DeviceCodeResponse) {
	oauth := c.getOAuthConfig()
	notificationMethods := oauth.NotificationMethods

	// Default to tray and log if no methods specified
	if len(notificationMethods) == 0 {
		notificationMethods = []string{config.NotificationMethodTray, config.NotificationMethodLog}
	}

	// Create notification data
	notification := &config.OAuthNotification{
		ServerName:      c.config.Name,
		VerificationURI: deviceResp.VerificationURI,
		UserCode:        deviceResp.UserCode,
		ExpiresIn:       deviceResp.ExpiresIn,
		Timestamp:       time.Now(),
		FlowType:        "device_code",
	}

	// Send notifications via configured channels
	for _, method := range notificationMethods {
		switch method {
		case config.NotificationMethodTray:
			c.sendTrayNotification(notification)
		case config.NotificationMethodLog:
			c.sendLogNotification(notification)
		case config.NotificationMethodWebhook:
			c.sendWebhookNotification(notification)
		case config.NotificationMethodEmail:
			c.sendEmailNotification(notification)
		default:
			c.logger.Warn("Unknown notification method", zap.String("method", method))
		}
	}
}

// sendTrayNotification sends notification via system tray
func (c *Client) sendTrayNotification(notification *config.OAuthNotification) {
	// This would integrate with the tray system
	// For now, just log the notification
	c.logger.Info("Tray notification (placeholder)",
		zap.String("server", notification.ServerName),
		zap.String("user_code", notification.UserCode),
		zap.String("verification_uri", notification.VerificationURI))
}

// sendLogNotification sends notification via log output
func (c *Client) sendLogNotification(notification *config.OAuthNotification) {
	c.logger.Info("OAuth Device Code Authorization Required",
		zap.String("server", notification.ServerName),
		zap.String("verification_uri", notification.VerificationURI),
		zap.String("user_code", notification.UserCode),
		zap.Duration("expires_in", notification.ExpiresIn))

	// Also log to upstream logger
	if c.upstreamLogger != nil {
		c.upstreamLogger.Info("OAuth Device Code Authorization Required",
			zap.String("verification_uri", notification.VerificationURI),
			zap.String("user_code", notification.UserCode),
			zap.Duration("expires_in", notification.ExpiresIn))
	}
}

// sendWebhookNotification sends notification via webhook
func (c *Client) sendWebhookNotification(notification *config.OAuthNotification) {
	oauth := c.getOAuthConfig()
	webhookURL := oauth.WebhookURL

	if webhookURL == "" {
		c.logger.Warn("Webhook notification requested but no webhook URL configured")
		return
	}

	// Send webhook notification in a goroutine to avoid blocking
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		// Prepare webhook payload
		payload := map[string]interface{}{
			"type":             "oauth_device_code",
			"server_name":      notification.ServerName,
			"verification_uri": notification.VerificationURI,
			"user_code":        notification.UserCode,
			"expires_in":       notification.ExpiresIn.Seconds(),
			"timestamp":        notification.Timestamp.Unix(),
			"flow_type":        notification.FlowType,
		}

		jsonPayload, err := json.Marshal(payload)
		if err != nil {
			c.logger.Error("Failed to marshal webhook payload", zap.Error(err))
			return
		}

		// Send webhook request
		req, err := http.NewRequestWithContext(ctx, "POST", webhookURL, strings.NewReader(string(jsonPayload)))
		if err != nil {
			c.logger.Error("Failed to create webhook request", zap.Error(err))
			return
		}

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "mcpproxy-go/1.0.0")

		client := &http.Client{}
		resp, err := client.Do(req)
		if err != nil {
			c.logger.Error("Failed to send webhook notification", zap.Error(err))
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			c.logger.Debug("Webhook notification sent successfully", zap.Int("status_code", resp.StatusCode))
		} else {
			c.logger.Warn("Webhook notification failed", zap.Int("status_code", resp.StatusCode))
		}
	}()
}

// sendEmailNotification sends notification via email
func (c *Client) sendEmailNotification(notification *config.OAuthNotification) {
	oauth := c.getOAuthConfig()
	emailConfig := oauth.EmailNotification

	if emailConfig == "" {
		c.logger.Warn("Email notification requested but no email configuration provided")
		return
	}

	// For now, just log the email notification
	// In a full implementation, this would integrate with an email service
	c.logger.Info("Email notification (placeholder)",
		zap.String("email_config", emailConfig),
		zap.String("server", notification.ServerName),
		zap.String("user_code", notification.UserCode),
		zap.String("verification_uri", notification.VerificationURI))
}

// sendOAuthNotification sends a generic OAuth notification via all configured channels
//
//nolint:unused // Utility function for debugging/future use
func (c *Client) sendOAuthNotification(notificationType string, data map[string]interface{}) {
	oauth := c.getOAuthConfig()
	notificationMethods := oauth.NotificationMethods

	// Default to log if no methods specified
	if len(notificationMethods) == 0 {
		notificationMethods = []string{config.NotificationMethodLog}
	}

	// Create notification
	notification := &config.OAuthNotification{
		ServerName: c.config.Name,
		Timestamp:  time.Now(),
		FlowType:   notificationType,
	}

	// Extract common fields from data
	if uri, ok := data["verification_uri"].(string); ok {
		notification.VerificationURI = uri
	}
	if code, ok := data["user_code"].(string); ok {
		notification.UserCode = code
	}
	if expires, ok := data["expires_in"].(time.Duration); ok {
		notification.ExpiresIn = expires
	}

	// Send via configured channels
	for _, method := range notificationMethods {
		switch method {
		case config.NotificationMethodTray:
			c.sendTrayNotification(notification)
		case config.NotificationMethodLog:
			c.logger.Info("OAuth notification",
				zap.String("type", notificationType),
				zap.String("server", notification.ServerName),
				zap.Any("data", data))
		case config.NotificationMethodWebhook:
			c.sendWebhookNotification(notification)
		case config.NotificationMethodEmail:
			c.sendEmailNotification(notification)
		}
	}
}

// getStringValue safely gets a string value from a map
func getStringValue(m map[string]interface{}, key string) string {
	if val, ok := m[key].(string); ok {
		return val
	}
	return ""
}

// getDurationValue safely gets a duration value from a map (expects seconds as number)
func getDurationValue(m map[string]interface{}, key string, defaultValue time.Duration) time.Duration {
	if val, ok := m[key].(float64); ok {
		return time.Duration(val) * time.Second
	}
	if val, ok := m[key].(int); ok {
		return time.Duration(val) * time.Second
	}
	return defaultValue
}

// loadOAuthTokensFromStorage loads stored OAuth tokens for this server during startup
func (c *Client) loadOAuthTokensFromStorage(ctx context.Context) error {
	if c.storageManager == nil {
		return nil // No storage available
	}

	tokens, err := c.storageManager.LoadOAuthTokens(c.config.Name)
	if err != nil {
		c.logger.Debug("Failed to load OAuth tokens from storage", zap.Error(err))
		return nil // Don't treat this as a fatal error
	}

	if tokens == nil {
		c.logger.Debug("No OAuth tokens found in storage for server",
			zap.String("server_name", c.config.Name))
		return nil
	}

	// Check if tokens are expired
	if time.Now().After(tokens.ExpiresAt) {
		c.logger.Info("Stored OAuth tokens are expired, will need re-authentication",
			zap.String("server_name", c.config.Name),
			zap.Time("expired_at", tokens.ExpiresAt),
			zap.Duration("expired_since", time.Now().Sub(tokens.ExpiresAt)))

		// Don't load expired tokens but don't delete them either (they might have refresh tokens)
		// Set up OAuth config structure for potential refresh
		if c.config.OAuth == nil {
			c.config.OAuth = &config.OAuthConfig{}
		}
		c.config.OAuth.TokenStorage = tokens

		return nil
	}

	// Load valid tokens
	if c.config.OAuth == nil {
		c.config.OAuth = &config.OAuthConfig{}
	}
	c.config.OAuth.TokenStorage = tokens

	// Also create oauth2.Token for refresh capabilities
	c.mu.Lock()
	c.oauthToken = &oauth2.Token{
		AccessToken:  tokens.AccessToken,
		RefreshToken: tokens.RefreshToken,
		TokenType:    tokens.TokenType,
		Expiry:       tokens.ExpiresAt,
	}
	c.mu.Unlock()

	c.logger.Info("Loaded valid OAuth tokens from storage",
		zap.String("server_name", c.config.Name),
		zap.String("token_type", tokens.TokenType),
		zap.Time("expires_at", tokens.ExpiresAt),
		zap.Duration("expires_in", tokens.ExpiresAt.Sub(time.Now())))

	return nil
}

// saveOAuthTokensToStorage saves OAuth tokens to storage for persistence across restarts
func (c *Client) saveOAuthTokensToStorage(tokens *config.TokenStorage) error {
	if c.storageManager == nil {
		c.logger.Warn("No storage manager available, OAuth tokens will not persist across restarts")
		return nil
	}

	if tokens == nil {
		c.logger.Debug("No tokens to save to storage")
		return nil
	}

	err := c.storageManager.SaveOAuthTokens(c.config.Name, tokens)
	if err != nil {
		c.logger.Error("Failed to save OAuth tokens to storage",
			zap.String("server_name", c.config.Name),
			zap.Error(err))
		return err
	}

	c.logger.Debug("Successfully saved OAuth tokens to storage",
		zap.String("server_name", c.config.Name),
		zap.String("token_type", tokens.TokenType),
		zap.Time("expires_at", tokens.ExpiresAt))

	return nil
}

// clearOAuthTokensFromStorage removes OAuth tokens from storage (e.g., on logout)
func (c *Client) clearOAuthTokensFromStorage() error {
	if c.storageManager == nil {
		return nil
	}

	err := c.storageManager.SaveOAuthTokens(c.config.Name, nil)
	if err != nil {
		c.logger.Error("Failed to clear OAuth tokens from storage",
			zap.String("server_name", c.config.Name),
			zap.Error(err))
		return err
	}

	c.logger.Debug("Cleared OAuth tokens from storage",
		zap.String("server_name", c.config.Name))

	return nil
}

// shouldAttemptConnection checks if a connection attempt should proceed
// Returns false if already connected, connecting, or OAuth is pending without user intervention
func (c *Client) shouldAttemptConnection() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	// Don't attempt if already connected or connecting
	if c.connected || c.connecting {
		return false
	}

	// Don't attempt if OAuth is pending - wait for user to complete the flow
	// This prevents background retries from interfering with OAuth flow
	if c.oauthPending {
		return false
	}

	return true
}

// startConnectionAttempt marks the start of a connection attempt with deduplication
// Returns a unique request ID and whether this attempt should proceed
func (c *Client) startConnectionAttempt() (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Check if already connected, connecting, or OAuth pending
	if c.connected || c.connecting || c.oauthPending {
		return "", false
	}

	// Generate unique request ID for this attempt
	requestID := fmt.Sprintf("%s-%d", c.id, time.Now().UnixNano())
	c.connectionRequestID = requestID
	c.connecting = true

	return requestID, true
}

// finishConnectionAttempt marks the end of a connection attempt
func (c *Client) finishConnectionAttempt(requestID string, success bool, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Only update state if this is the current connection attempt
	if c.connectionRequestID != requestID {
		c.logger.Debug("Ignoring stale connection attempt finish",
			zap.String("request_id", requestID),
			zap.String("current_request_id", c.connectionRequestID))
		return
	}

	c.connecting = false
	c.connectionRequestID = ""

	if success {
		c.connected = true
		c.lastError = nil
		c.retryCount = 0
		c.oauthPending = false
		c.oauthError = nil
	} else {
		c.connected = false
		c.lastError = err
		c.retryCount++
		c.lastRetryTime = time.Now()
	}
}

// startListToolsRequest starts a ListTools request with deduplication
// Returns a request ID, whether this request should proceed, and a channel to wait on if another request is in progress
func (c *Client) startListToolsRequest() (string, bool, <-chan struct{}) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// If a request is already in progress, add ourselves to the waiters
	if c.listToolsInProgress {
		waiter := make(chan struct{})
		c.listToolsWaiters = append(c.listToolsWaiters, waiter)
		return "", false, waiter
	}

	// Start a new request
	requestID := fmt.Sprintf("listtools-%s-%d", c.id, time.Now().UnixNano())
	c.listToolsInProgress = true
	c.listToolsRequestID = requestID
	c.listToolsResult = nil
	c.listToolsError = nil

	return requestID, true, nil
}

// finishListToolsRequest finishes a ListTools request and notifies waiters
func (c *Client) finishListToolsRequest(requestID string, result []*config.ToolMetadata, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Only update if this is the current request
	if c.listToolsRequestID != requestID {
		return
	}

	c.listToolsInProgress = false
	c.listToolsRequestID = ""
	c.listToolsResult = result
	c.listToolsError = err

	// Notify all waiters
	for _, waiter := range c.listToolsWaiters {
		close(waiter)
	}
	c.listToolsWaiters = nil
}

// getListToolsResult returns the cached result from the last ListTools request
func (c *Client) getListToolsResult() ([]*config.ToolMetadata, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.listToolsResult, c.listToolsError
}
