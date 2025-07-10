package upstream

import (
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
)

const (
	transportHTTP           = "http"
	transportStreamableHTTP = "streamable-http"
	transportSSE            = "sse"
	transportStdio          = "stdio"
	osWindows               = "windows"

	// OAuth flow types - removed unused local constants, using config package constants instead
)

// Deployment types
const (
	DeploymentLocal DeploymentType = iota
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

	// Connection state (protected by mutex)
	mu            sync.RWMutex
	connected     bool
	lastError     error
	retryCount    int
	lastRetryTime time.Time
	connecting    bool

	// OAuth state management
	oauthPending    bool
	oauthError      error
	cachedTools     []*config.ToolMetadata
	toolCacheExpiry time.Time
	connectionState string // "disconnected", "connecting", "connected", "oauth_pending", "failed"

	// Deployment detection cache
	deploymentType    DeploymentType // DeploymentLocal, DeploymentRemote, DeploymentHeadless
	detectedPublicURL string
}

// Tool represents a tool from an upstream server
type Tool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"inputSchema"`
}

// NewClient creates a new MCP client for connecting to an upstream server
func NewClient(id string, serverConfig *config.ServerConfig, logger *zap.Logger, logConfig *config.LogConfig, globalConfig *config.Config) (*Client, error) {
	c := &Client{
		id:     id,
		config: serverConfig,
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
	oauth := c.getOAuthConfig()

	// Skip if DCR is not enabled
	if oauth.DynamicClientRegistration == nil || !oauth.DynamicClientRegistration.Enabled {
		return nil
	}

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

	// Prepare registration request
	regReq := c.buildRegistrationRequest()

	// Make registration request
	regResp, err := c.registerClient(ctx, registrationEndpoint, regReq)
	if err != nil {
		return fmt.Errorf("DCR failed: %w", err)
	}

	// Update OAuth configuration with registered client credentials
	oauth.ClientID = regResp.ClientID
	oauth.ClientSecret = regResp.ClientSecret

	c.logger.Info("Dynamic Client Registration successful",
		zap.String("client_id", oauth.ClientID),
		zap.Bool("has_client_secret", oauth.ClientSecret != ""))

	return nil
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
	// Marshal request to JSON
	reqBody, err := json.Marshal(regReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal registration request: %w", err)
	}

	c.logger.Debug("DCR request", zap.String("request", string(reqBody)))

	// Create HTTP request
	req, err := http.NewRequestWithContext(ctx, "POST", registrationEndpoint, strings.NewReader(string(reqBody)))
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

	c.logger.Debug("DCR response", zap.String("client_id", regResp.ClientID))

	return &regResp, nil
}

// detectDeploymentType automatically detects the deployment type based on environment
func (c *Client) detectDeploymentType() DeploymentType {
	if c.deploymentType != 0 {
		return c.deploymentType // Use cached result
	}

	// Check if running in headless environment
	if os.Getenv("DISPLAY") == "" && runtime.GOOS == "linux" {
		c.deploymentType = DeploymentHeadless
		return DeploymentHeadless
	}

	// Check if mcpproxy is configured with public URL
	if c.config.PublicURL != "" {
		c.deploymentType = DeploymentRemote
		c.detectedPublicURL = c.config.PublicURL
		return DeploymentRemote
	}

	// Check if mcpproxy is listening on non-localhost interfaces
	// This would require access to global config, for now assume local
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
		return []string{
			"http://127.0.0.1:8080/oauth/callback",
			"http://localhost:8080/oauth/callback",
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
	c.mu.Lock()
	if c.connecting {
		c.mu.Unlock()
		return fmt.Errorf("connection already in progress")
	}
	c.connecting = true
	c.mu.Unlock()

	// Declare variables that will be used in error handling
	var command string
	var cmdArgs []string
	var envVars []string

	defer func() {
		c.mu.Lock()
		c.connecting = false
		c.mu.Unlock()
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

	transportType := c.determineTransportType()

	switch transportType {
	case transportHTTP, transportStreamableHTTP:
		httpTransport, err := transport.NewStreamableHTTP(c.config.URL)
		if err != nil {
			c.mu.Lock()
			c.lastError = err
			c.retryCount++
			c.lastRetryTime = time.Now()
			c.mu.Unlock()
			return fmt.Errorf("failed to create HTTP transport: %w", err)
		}
		c.client = client.NewClient(httpTransport)
	case transportSSE:
		// For SSE, we need to handle Cloudflare's two-step connection pattern
		// First connect to /sse to get session info, then use that for actual communication
		c.logger.Debug("Creating SSE transport with Cloudflare compatibility",
			zap.String("url", c.config.URL))

		// Create SSE transport with special handling for Cloudflare endpoints
		httpTransport, err := transport.NewStreamableHTTP(c.config.URL)
		if err != nil {
			c.mu.Lock()
			c.lastError = err
			c.retryCount++
			c.lastRetryTime = time.Now()
			c.mu.Unlock()
			return fmt.Errorf("failed to create SSE transport: %w", err)
		}
		c.client = client.NewClient(httpTransport)
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
				c.mu.Lock()
				c.lastError = fmt.Errorf("invalid stdio command: %s", c.config.URL)
				c.retryCount++
				c.lastRetryTime = time.Now()
				c.mu.Unlock()
				return c.lastError
			}
			originalCommand = args[0]
			originalArgs = args[1:]
		}

		if originalCommand == "" {
			c.mu.Lock()
			c.lastError = fmt.Errorf("no command specified for stdio transport")
			c.retryCount++
			c.lastRetryTime = time.Now()
			c.mu.Unlock()
			return c.lastError
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
		c.client = client.NewClient(stdioTransport)
	default:
		c.mu.Lock()
		c.lastError = fmt.Errorf("unsupported transport type: %s", transportType)
		c.retryCount++
		c.lastRetryTime = time.Now()
		c.mu.Unlock()
		return c.lastError
	}

	// Set connection timeout with exponential backoff consideration
	timeout := c.getConnectionTimeout()
	connectCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Start the client
	if err := c.client.Start(connectCtx); err != nil {
		c.mu.Lock()
		c.lastError = err
		c.retryCount++
		c.lastRetryTime = time.Now()
		c.mu.Unlock()

		c.logger.Error("Failed to start MCP client",
			zap.Error(err),
			zap.String("command", command),
			zap.Strings("args", cmdArgs))

		if c.upstreamLogger != nil {
			c.upstreamLogger.Error("Client start failed", zap.Error(err))
		}

		return fmt.Errorf("failed to start MCP client: %w", err)
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

			// Check if lazy OAuth is enabled
			if c.shouldUseLazyAuth() {
				c.logger.Info("OAuth pending - entering lazy OAuth state")
				if c.upstreamLogger != nil {
					c.upstreamLogger.Info("Entering OAuth pending state")
				}

				// Set OAuth pending state
				c.setOAuthPending(true, err)

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
	c.mu.Lock()
	c.connected = true
	c.lastError = nil
	c.retryCount = 0       // Reset retry count on successful connection
	c.oauthPending = false // Clear OAuth pending state
	c.oauthError = nil
	c.mu.Unlock()

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
	baseTimeout := 30 * time.Second

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
	c.mu.RLock()
	connected := c.connected
	client := c.client
	oauthPending := c.oauthPending
	c.mu.RUnlock()

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
			return cachedTools, nil
		}
		c.logger.Debug("No cached tools available for OAuth pending server")
		return nil, nil
	}

	if !connected || client == nil {
		return nil, fmt.Errorf("client not connected")
	}

	// Check if server supports tools
	c.mu.RLock()
	serverInfo := c.serverInfo
	c.mu.RUnlock()

	if serverInfo.Capabilities.Tools == nil {
		c.logger.Debug("Server does not support tools")
		return nil, nil
	}

	toolsRequest := mcp.ListToolsRequest{}
	toolsResult, err := client.ListTools(ctx, toolsRequest)
	if err != nil {
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

		return nil, fmt.Errorf("failed to list tools: %w", err)
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

	// Cache tools for 1 hour in case connection is lost
	c.setCachedTools(tools, 1*time.Hour)

	c.logger.Debug("Listed tools from upstream server", zap.Int("count", len(tools)))
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

	result, err := client.CallTool(ctx, request)
	if err != nil {
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

// isOAuthAuthorizationRequired checks if the error indicates OAuth authorization is required
func (c *Client) isOAuthAuthorizationRequired(err error) bool {
	if err == nil {
		return false
	}

	// Check for OAuth authorization required error from mcp-go library
	if strings.Contains(err.Error(), "authorization required") ||
		strings.Contains(err.Error(), "no valid token available") ||
		strings.Contains(err.Error(), "unauthorized") {
		return true
	}

	// Check for HTTP 401 unauthorized errors
	if strings.Contains(err.Error(), "401") {
		return true
	}

	return false
}

// handleOAuthFlow handles the OAuth authorization flow with auto-discovery
func (c *Client) handleOAuthFlow(ctx context.Context) error {
	// Initialize OAuth configuration if not present with auto-discovery enabled by default
	if c.config.OAuth == nil {
		c.config.OAuth = &config.OAuthConfig{
			AutoDiscovery: &config.OAuthAutoDiscovery{
				Enabled:           true,
				PromptForClientID: false, // Default to false - only prompt when actually needed
				AutoDeviceFlow:    true,
			},
		}
	}

	// Perform auto-discovery (enabled by default)
	if err := c.performOAuthAutoDiscovery(ctx); err != nil {
		c.logger.Warn("OAuth auto-discovery failed, continuing with manual configuration", zap.Error(err))
		if c.upstreamLogger != nil {
			c.upstreamLogger.Warn("OAuth auto-discovery failed", zap.Error(err))
		}
	}

	// Perform dynamic client registration if enabled
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
				return fmt.Errorf("Cloudflare MCP servers require OAuth client registration. Please:\n" +
					"1. Visit the Cloudflare dashboard to register an OAuth client\n" +
					"2. Add the client_id to your server configuration\n" +
					"3. Set the redirect URI to: http://127.0.0.1:*/oauth/callback\n" +
					"For more info, see: https://developers.cloudflare.com/agents/model-context-protocol/authorization/")
			}
			c.logger.Info("Proceeding with OAuth flow without client ID (public client)")
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
	oauthHandler.SetBaseURL(baseURL)

	// Discover OAuth server metadata
	metadata, err := oauthHandler.GetServerMetadata(ctx)
	if err != nil {
		return fmt.Errorf("failed to discover OAuth metadata: %w", err)
	}

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
		c.config.OAuth.AuthorizationEndpoint = metadata.AuthorizationEndpoint
	}
	if c.config.OAuth.TokenEndpoint == "" {
		c.config.OAuth.TokenEndpoint = metadata.TokenEndpoint
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

	// Start a local server to handle the OAuth callback
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("failed to start local server for oauth callback: %w", err)
	}
	defer listener.Close()

	port := listener.Addr().(*net.TCPAddr).Port
	redirectURL := fmt.Sprintf("http://127.0.0.1:%d/oauth/callback", port)

	// Only use configured redirect URIs if they're explicitly set (not auto-generated)
	// For local authorization code flow, we need to use the dynamic port
	oauth := c.getOAuthConfig()
	if oauth.RedirectURI != "" || len(oauth.RedirectURIs) > 0 {
		// User has explicitly configured redirect URIs, use the first one
		redirectURIs := c.getRedirectURIs()
		if len(redirectURIs) > 0 {
			redirectURL = redirectURIs[0]
			c.logger.Info("Using configured redirect URI", zap.String("redirect_uri", redirectURL))
		}
	} else {
		// Use dynamic redirect URL for local OAuth flow
		c.logger.Info("Using dynamic redirect URI for local OAuth flow",
			zap.String("redirect_uri", redirectURL),
			zap.Int("port", port))
	}

	// Create a state token for CSRF protection
	stateBytes := make([]byte, 32)
	if _, err := rand.Read(stateBytes); err != nil {
		return fmt.Errorf("failed to generate state token: %w", err)
	}
	state := base64.URLEncoding.EncodeToString(stateBytes)

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
	select {
	case err := <-resultChan:
		if err != nil {
			return err
		}
	case <-ctx.Done():
		return ctx.Err()
	}

	// Store token
	oauthCfg.TokenStorage = &config.TokenStorage{
		AccessToken:  token.AccessToken,
		RefreshToken: token.RefreshToken,
		ExpiresAt:    token.Expiry,
		TokenType:    token.TokenType,
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
