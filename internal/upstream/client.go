package upstream

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"go.uber.org/zap"

	"mcpproxy-go/internal/config"
	"mcpproxy-go/internal/hash"
	"mcpproxy-go/internal/logs"
	"mcpproxy-go/internal/oauth"
	"mcpproxy-go/internal/secureenv"
	"mcpproxy-go/internal/transport"
)

const (
	osWindows = "windows"
)

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

	// State manager for connection state
	stateManager *StateManager

	// Connection state protection
	mu sync.RWMutex
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
		stateManager: NewStateManager(),
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

	// Set up state change callback for logging
	c.stateManager.SetStateChangeCallback(c.onStateChange)

	return c, nil
}

// onStateChange handles state transition events
func (c *Client) onStateChange(oldState, newState ConnectionState, info ConnectionInfo) { //nolint:gocritic // info struct is acceptable size
	c.logger.Info("State transition",
		zap.String("from", oldState.String()),
		zap.String("to", newState.String()),
		zap.String("server", c.config.Name))

	if c.upstreamLogger != nil {
		c.upstreamLogger.Info("State transition",
			zap.String("from", oldState.String()),
			zap.String("to", newState.String()))

		if info.LastError != nil {
			c.upstreamLogger.Error("State transition error",
				zap.Error(info.LastError))
		}
	}
}

// Connect establishes a connection to the upstream MCP server
func (c *Client) Connect(ctx context.Context) error {
	// Check if already connecting or connected
	if c.stateManager.IsConnecting() || c.stateManager.IsReady() {
		return fmt.Errorf("connection already in progress or established")
	}

	// Transition to connecting state
	c.stateManager.TransitionTo(StateConnecting)

	// Get connection info for logging
	info := c.stateManager.GetConnectionInfo()

	// Log to both main logger and upstream logger
	c.logger.Info("Connecting to upstream MCP server",
		zap.String("url", c.config.URL),
		zap.String("protocol", c.config.Protocol),
		zap.Int("retry_count", info.RetryCount))

	if c.upstreamLogger != nil {
		c.upstreamLogger.Info("Connecting to upstream server",
			zap.String("url", c.config.URL),
			zap.String("protocol", c.config.Protocol),
			zap.Int("retry_count", info.RetryCount))
	}

	// Determine transport type
	transportType := transport.DetermineTransportType(c.config)

	// Create the appropriate client based on transport type
	var err error
	c.logger.Debug("Creating client based on transport type",
		zap.String("server", c.config.Name),
		zap.String("transport_type", transportType))

	switch transportType {
	case transport.TransportHTTP, transport.TransportStreamableHTTP:
		c.logger.Debug("Using HTTP/Streamable-HTTP transport",
			zap.String("server", c.config.Name))
		c.client, err = c.createHTTPClient()
	case transport.TransportSSE:
		c.logger.Debug("Using SSE transport",
			zap.String("server", c.config.Name))
		c.client, err = c.createSSEClient()
	case transport.TransportStdio:
		c.logger.Debug("Using STDIO transport",
			zap.String("server", c.config.Name))
		c.client, err = c.createStdioClient()
	default:
		c.logger.Error("Unsupported transport type",
			zap.String("server", c.config.Name),
			zap.String("transport_type", transportType))
		err = fmt.Errorf("unsupported transport type: %s", transportType)
	}

	if err != nil {
		c.logger.Error("Failed to create client",
			zap.String("server", c.config.Name),
			zap.String("transport_type", transportType),
			zap.Error(err))
		c.stateManager.SetError(err)
		return fmt.Errorf("failed to create client: %w", err)
	}

	c.logger.Debug("Client created successfully",
		zap.String("server", c.config.Name),
		zap.String("transport_type", transportType))

	// Set connection timeout with exponential backoff consideration
	timeout := c.getConnectionTimeout()
	connectCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Start the client (this may trigger OAuth flow)
	c.logger.Debug("Starting MCP client connection",
		zap.String("server", c.config.Name),
		zap.String("url", c.config.URL),
		zap.String("protocol", c.config.Protocol))

	if err := c.client.Start(connectCtx); err != nil {
		c.logger.Debug("Client.Start() returned error",
			zap.String("server", c.config.Name),
			zap.Error(err),
			zap.String("error_type", fmt.Sprintf("%T", err)))

		// Check if this is an OAuth authorization required error
		if client.IsOAuthAuthorizationRequiredError(err) {
			c.stateManager.TransitionTo(StateAuthenticating)
			c.logger.Info("OAuth authorization required error detected",
				zap.String("server", c.config.Name),
				zap.Error(err))
			c.logger.Info("OAuth authentication required - library will handle Dynamic Client Registration and browser opening",
				zap.String("server", c.config.Name))
			if c.upstreamLogger != nil {
				c.upstreamLogger.Info("OAuth authentication flow starting automatically via mcp-go library")
			}

			// The mcp-go library should handle the OAuth flow automatically:
			// 1. Dynamic Client Registration (DCR)
			// 2. Start local callback server
			// 3. Open browser for user authentication
			// 4. Handle authorization code exchange
			// If we reach here, the OAuth flow failed for some reason
			c.stateManager.SetError(fmt.Errorf("OAuth authentication failed: %w", err))
			return fmt.Errorf("OAuth authentication failed: %w", err)
		}

		// Check if it's a regular HTTP error that might contain OAuth info
		if strings.Contains(err.Error(), "401") || strings.Contains(err.Error(), "Unauthorized") {
			c.logger.Warn("Received 401/Unauthorized but not detected as OAuth error",
				zap.String("server", c.config.Name),
				zap.Error(err))
		}

		// Check if it's a transport-level error
		if strings.Contains(err.Error(), "invalid_token") || strings.Contains(err.Error(), "Missing or invalid access token") {
			c.logger.Warn("Server returned token error - may need OAuth but not properly detected",
				zap.String("server", c.config.Name),
				zap.Error(err))
		}

		c.stateManager.SetError(err)
		c.logger.Error("Failed to start MCP client",
			zap.String("server", c.config.Name),
			zap.Error(err))
		if c.upstreamLogger != nil {
			c.upstreamLogger.Error("Client start failed", zap.Error(err))
		}
		return fmt.Errorf("failed to start MCP client: %w", err)
	}

	c.logger.Debug("Client.Start() completed successfully",
		zap.String("server", c.config.Name))

	// Transition to discovering state for tool discovery
	c.stateManager.TransitionTo(StateDiscovering)

	// Initialize the client
	c.logger.Debug("Initializing MCP client",
		zap.String("server", c.config.Name))

	initRequest := mcp.InitializeRequest{}
	initRequest.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initRequest.Params.ClientInfo = mcp.Implementation{
		Name:    "mcpproxy-go",
		Version: "1.0.0",
	}
	initRequest.Params.Capabilities = mcp.ClientCapabilities{}

	serverInfo, err := c.client.Initialize(connectCtx, initRequest)
	if err != nil {
		c.logger.Debug("Client.Initialize() returned error",
			zap.String("server", c.config.Name),
			zap.Error(err),
			zap.String("error_type", fmt.Sprintf("%T", err)))

		// Check if this is an OAuth authorization required error during Initialize
		if client.IsOAuthAuthorizationRequiredError(err) ||
			strings.Contains(err.Error(), "no valid token available") ||
			strings.Contains(err.Error(), "authorization required") {
			c.stateManager.TransitionTo(StateAuthenticating)
			c.logger.Info("OAuth authorization required - starting complete OAuth flow",
				zap.String("server", c.config.Name),
				zap.String("url", c.config.URL),
				zap.Error(err))

			if c.upstreamLogger != nil {
				c.upstreamLogger.Info("Starting OAuth authentication flow - will open browser automatically")
			}

			// Get OAuth handler from the error
			oauthHandler := client.GetOAuthHandler(err)
			if oauthHandler == nil {
				c.logger.Error("Failed to get OAuth handler from error",
					zap.String("server", c.config.Name))
				return fmt.Errorf("failed to get OAuth handler: %w", err)
			}

			c.logger.Info("OAuth handler obtained - starting Dynamic Client Registration",
				zap.String("server", c.config.Name))

			// Step 1: Dynamic Client Registration
			regErr := oauthHandler.RegisterClient(connectCtx, "mcpproxy-go")
			if regErr != nil {
				c.logger.Error("Dynamic Client Registration failed",
					zap.String("server", c.config.Name),
					zap.Error(regErr))
				return fmt.Errorf("DCR failed: %w", regErr)
			}

			c.logger.Info("Dynamic Client Registration completed",
				zap.String("server", c.config.Name))

			// Step 1.5: Get and validate server metadata
			metadata, metaErr := oauthHandler.GetServerMetadata(connectCtx)
			if metaErr != nil {
				c.logger.Error("Failed to get OAuth server metadata",
					zap.String("server", c.config.Name),
					zap.Error(metaErr))
				return fmt.Errorf("server metadata discovery failed: %w", metaErr)
			}

			c.logger.Info("OAuth server metadata discovered",
				zap.String("server", c.config.Name),
				zap.String("authorization_endpoint", metadata.AuthorizationEndpoint),
				zap.String("token_endpoint", metadata.TokenEndpoint))

			if metadata.AuthorizationEndpoint == "" {
				c.logger.Error("No authorization endpoint in server metadata",
					zap.String("server", c.config.Name))
				return fmt.Errorf("no authorization endpoint found in server metadata")
			}

			// Step 2: Generate PKCE parameters for secure OAuth flow
			codeVerifier, err := client.GenerateCodeVerifier()
			if err != nil {
				c.logger.Error("Failed to generate PKCE code verifier",
					zap.String("server", c.config.Name),
					zap.Error(err))
				return fmt.Errorf("PKCE code verifier generation failed: %w", err)
			}
			codeChallenge := client.GenerateCodeChallenge(codeVerifier)

			// Step 3: Generate state parameter for security
			state, err := client.GenerateState()
			if err != nil {
				c.logger.Error("Failed to generate OAuth state parameter",
					zap.String("server", c.config.Name),
					zap.Error(err))
				return fmt.Errorf("OAuth state generation failed: %w", err)
			}

			c.logger.Debug("Generated OAuth security parameters",
				zap.String("server", c.config.Name),
				zap.String("state", state))

			// Step 4: Get the authorization URL and open browser
			authURL, err := oauthHandler.GetAuthorizationURL(connectCtx, state, codeChallenge)
			if err != nil {
				c.logger.Error("Failed to get OAuth authorization URL",
					zap.String("server", c.config.Name),
					zap.Error(err))
				return fmt.Errorf("authorization URL generation failed: %w", err)
			}

			c.logger.Info("Opening browser for OAuth authentication",
				zap.String("server", c.config.Name),
				zap.String("auth_url", authURL))

			// Import the oauth package to access the callback manager
			callbackManager := oauth.GetGlobalCallbackManager()

			// Get the callback server for this upstream
			callbackServer, exists := callbackManager.GetCallbackServer(c.config.Name)
			if !exists {
				c.logger.Error("OAuth callback server not found",
					zap.String("server", c.config.Name))
				return fmt.Errorf("OAuth callback server not available for %s", c.config.Name)
			}

			c.logger.Debug("Using OAuth callback server",
				zap.String("server", c.config.Name),
				zap.String("redirect_uri", callbackServer.RedirectURI),
				zap.Int("port", callbackServer.Port))

			// Step 5: Open browser for user authentication
			if err := openBrowser(authURL); err != nil {
				c.logger.Error("Failed to open browser for OAuth authentication",
					zap.String("server", c.config.Name),
					zap.String("auth_url", authURL),
					zap.Error(err))
				// Continue anyway - user can manually open the URL
				c.logger.Info("Please open the following URL in your browser to complete OAuth authentication",
					zap.String("server", c.config.Name),
					zap.String("auth_url", authURL))
			} else {
				c.logger.Info("Browser opened successfully for OAuth authentication",
					zap.String("server", c.config.Name))
			}

			// Step 6: Wait for OAuth callback with timeout
			c.logger.Info("OAuth flow initiated - browser opened for user authentication",
				zap.String("server", c.config.Name))

			// Wait for the authorization callback with timeout
			var authParams map[string]string
			select {
			case authParams = <-callbackServer.CallbackChan:
				c.logger.Info("OAuth callback received",
					zap.String("server", c.config.Name),
					zap.Any("params", authParams))
			case <-time.After(5 * time.Minute):
				c.logger.Error("OAuth authorization timeout - user did not complete authentication within 5 minutes",
					zap.String("server", c.config.Name))
				return fmt.Errorf("OAuth authorization timeout for %s", c.config.Name)
			case <-connectCtx.Done():
				c.logger.Error("OAuth authorization cancelled due to context cancellation",
					zap.String("server", c.config.Name))
				return fmt.Errorf("OAuth authorization cancelled for %s", c.config.Name)
			}

			// Step 7: Validate state parameter
			receivedState, hasState := authParams["state"]
			if !hasState || receivedState != state {
				c.logger.Error("OAuth state parameter mismatch",
					zap.String("server", c.config.Name),
					zap.String("expected_state", state),
					zap.String("received_state", receivedState))
				return fmt.Errorf("OAuth state mismatch for %s", c.config.Name)
			}

			// Step 8: Extract authorization code
			authCode, hasCode := authParams["code"]
			if !hasCode || authCode == "" {
				c.logger.Error("OAuth authorization code not received",
					zap.String("server", c.config.Name),
					zap.Any("params", authParams))
				return fmt.Errorf("OAuth authorization code missing for %s", c.config.Name)
			}

			c.logger.Info("OAuth authorization code received, exchanging for access token",
				zap.String("server", c.config.Name))

			// Step 9: Exchange authorization code for access token
			if err := oauthHandler.ProcessAuthorizationResponse(connectCtx, authCode, state, codeVerifier); err != nil {
				c.logger.Error("Failed to exchange OAuth authorization code for token",
					zap.String("server", c.config.Name),
					zap.Error(err))
				return fmt.Errorf("OAuth token exchange failed for %s: %w", c.config.Name, err)
			}

			c.logger.Info("OAuth authentication completed successfully",
				zap.String("server", c.config.Name))

			if c.upstreamLogger != nil {
				c.upstreamLogger.Info("OAuth authentication successful - retrying MCP client initialization")
			}

			// OAuth flow is complete, now retry the initialization
			// Call Initialize again since we now have valid tokens
			c.logger.Info("Retrying MCP client initialization with OAuth tokens",
				zap.String("server", c.config.Name))

			// Create a fresh initialization request
			retryInitRequest := mcp.InitializeRequest{
				Params: struct {
					ProtocolVersion string                 `json:"protocolVersion"`
					Capabilities    mcp.ClientCapabilities `json:"capabilities"`
					ClientInfo      mcp.Implementation     `json:"clientInfo"`
				}{
					ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION,
					ClientInfo: mcp.Implementation{
						Name:    "mcpproxy-go",
						Version: "0.1.0",
					},
				},
			}

			// Retry initialization with OAuth tokens
			retryResult, retryErr := c.client.Initialize(connectCtx, retryInitRequest)
			if retryErr != nil {
				c.logger.Error("OAuth-authenticated initialization failed",
					zap.String("server", c.config.Name),
					zap.Error(retryErr))
				c.stateManager.SetError(retryErr)
				return fmt.Errorf("OAuth-authenticated initialization failed for %s: %w", c.config.Name, retryErr)
			}

			c.logger.Info("OAuth-authenticated initialization succeeded",
				zap.String("server", c.config.Name),
				zap.String("server_name", retryResult.ServerInfo.Name),
				zap.String("server_version", retryResult.ServerInfo.Version))

			// Store server info and update state manager
			c.serverInfo = retryResult
			c.stateManager.SetServerInfo(retryResult.ServerInfo.Name, retryResult.ServerInfo.Version)

			// Transition to ready state
			c.stateManager.TransitionTo(StateReady)

			if c.upstreamLogger != nil {
				c.upstreamLogger.Info("OAuth authentication and initialization successful",
					zap.String("server_name", retryResult.ServerInfo.Name),
					zap.String("server_version", retryResult.ServerInfo.Version),
					zap.String("protocol_version", retryResult.ProtocolVersion))
			}

			// Return successfully - OAuth flow completed and client is ready
			return nil
		}

		// Check for OAuth-related error messages in Initialize step
		if strings.Contains(err.Error(), "no valid token available") ||
			strings.Contains(err.Error(), "authorization required") ||
			strings.Contains(err.Error(), "invalid_token") ||
			strings.Contains(err.Error(), "401") ||
			strings.Contains(err.Error(), "Unauthorized") {
			c.logger.Warn("OAuth-related error detected during Initialize but not recognized by library",
				zap.String("server", c.config.Name),
				zap.Error(err))
			c.logger.Info("This indicates the OAuth flow should have been triggered earlier")
		}

		c.stateManager.SetError(err)
		c.logger.Error("Failed to initialize MCP client",
			zap.String("server", c.config.Name),
			zap.Error(err))
		if c.upstreamLogger != nil {
			c.upstreamLogger.Error("Initialize failed", zap.Error(err))
		}
		c.client.Close()
		return fmt.Errorf("failed to initialize MCP client: %w", err)
	}

	c.logger.Debug("Client.Initialize() completed successfully",
		zap.String("server", c.config.Name))

	// Store server info and update state manager
	c.serverInfo = serverInfo
	c.stateManager.SetServerInfo(serverInfo.ServerInfo.Name, serverInfo.ServerInfo.Version)

	// Transition to ready state
	c.stateManager.TransitionTo(StateReady)

	c.logger.Info("Successfully connected to upstream MCP server",
		zap.String("server_name", serverInfo.ServerInfo.Name),
		zap.String("server_version", serverInfo.ServerInfo.Version))

	if c.upstreamLogger != nil {
		c.upstreamLogger.Info("Connected successfully",
			zap.String("server_name", serverInfo.ServerInfo.Name),
			zap.String("server_version", serverInfo.ServerInfo.Version),
			zap.String("protocol_version", serverInfo.ProtocolVersion))

		// Log initialization JSON if DEBUG level is enabled
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

// createHTTPClient creates an HTTP client with optional OAuth support
func (c *Client) createHTTPClient() (*client.Client, error) {
	c.logger.Debug("Creating HTTP client",
		zap.String("server", c.config.Name),
		zap.String("url", c.config.URL))

	// Create OAuth config if needed
	var oauthConfig *client.OAuthConfig
	if oauth.ShouldUseOAuth(c.config) {
		c.logger.Info("Creating OAuth-enabled client for potential OAuth flow",
			zap.String("server", c.config.Name))

		oauthConfig = oauth.CreateOAuthConfig(c.config)

		if oauthConfig != nil {
			c.logger.Info("OAuth configuration created for dynamic registration",
				zap.String("server", c.config.Name),
				zap.Strings("scopes", oauthConfig.Scopes),
				zap.Bool("pkce_enabled", oauthConfig.PKCEEnabled))
			c.logger.Debug("OAuth config details",
				zap.String("server", c.config.Name),
				zap.String("client_id", oauthConfig.ClientID),
				zap.String("client_secret", oauthConfig.ClientSecret),
				zap.String("redirect_uri", oauthConfig.RedirectURI))
		} else {
			c.logger.Warn("Failed to create OAuth configuration",
				zap.String("server", c.config.Name))
		}
	} else {
		c.logger.Debug("OAuth not required for this server, using regular HTTP client",
			zap.String("server", c.config.Name))
	}

	// Create HTTP transport config
	c.logger.Debug("Creating HTTP transport config",
		zap.String("server", c.config.Name),
		zap.Bool("use_oauth", oauthConfig != nil))
	httpConfig := transport.CreateHTTPTransportConfig(c.config, oauthConfig)

	// Create HTTP client
	c.logger.Debug("Calling transport.CreateHTTPClient",
		zap.String("server", c.config.Name),
		zap.String("url", httpConfig.URL),
		zap.Bool("use_oauth", httpConfig.UseOAuth))

	client, err := transport.CreateHTTPClient(httpConfig)
	if err != nil {
		c.logger.Error("transport.CreateHTTPClient failed",
			zap.String("server", c.config.Name),
			zap.Error(err))
		return nil, err
	}

	c.logger.Debug("transport.CreateHTTPClient succeeded",
		zap.String("server", c.config.Name))
	return client, nil
}

// createSSEClient creates an SSE client with optional OAuth support
func (c *Client) createSSEClient() (*client.Client, error) {
	c.logger.Debug("Creating SSE client",
		zap.String("server", c.config.Name),
		zap.String("url", c.config.URL))

	// Create OAuth config if needed
	var oauthConfig *client.OAuthConfig
	if oauth.ShouldUseOAuth(c.config) {
		c.logger.Info("Creating OAuth-enabled SSE client for potential OAuth flow",
			zap.String("server", c.config.Name))

		oauthConfig = oauth.CreateOAuthConfig(c.config)

		if oauthConfig != nil {
			c.logger.Info("OAuth configuration created for SSE client",
				zap.String("server", c.config.Name),
				zap.Strings("scopes", oauthConfig.Scopes),
				zap.Bool("pkce_enabled", oauthConfig.PKCEEnabled))
		} else {
			c.logger.Warn("Failed to create OAuth configuration for SSE client",
				zap.String("server", c.config.Name))
		}
	} else {
		c.logger.Debug("OAuth not required for SSE client",
			zap.String("server", c.config.Name))
	}

	// Create SSE transport config
	sseConfig := transport.CreateHTTPTransportConfig(c.config, oauthConfig)

	// Create SSE client
	return transport.CreateSSEClient(sseConfig)
}

// createStdioClient creates a stdio client
func (c *Client) createStdioClient() (*client.Client, error) {
	// Create stdio transport config
	stdioConfig := transport.CreateStdioTransportConfig(c.config, c.envManager)

	// Create stdio client
	return transport.CreateStdioClient(stdioConfig)
}

// getConnectionTimeout returns the connection timeout with exponential backoff
func (c *Client) getConnectionTimeout() time.Duration {
	baseTimeout := 30 * time.Second
	info := c.stateManager.GetConnectionInfo()

	if info.RetryCount == 0 {
		return baseTimeout
	}

	// Exponential backoff: min(base * 2^retry, max)
	// Ensure retry count is not negative and within safe range to avoid overflow
	retryCount := info.RetryCount
	if retryCount < 0 {
		retryCount = 0
	}
	if retryCount > 30 { // Cap at 30 to prevent overflow in 64-bit systems
		retryCount = 30
	}
	backoffMultiplier := 1 << uint(retryCount) //nolint:gosec // retryCount is bounds-checked above
	maxTimeout := 5 * time.Minute
	timeout := time.Duration(int64(baseTimeout) * int64(backoffMultiplier))

	if timeout > maxTimeout {
		timeout = maxTimeout
	}

	return timeout
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
	}

	c.stateManager.TransitionTo(StateDisconnected)
	return nil
}

// IsConnected returns whether the client is currently connected
func (c *Client) IsConnected() bool {
	return c.stateManager.IsReady()
}

// IsConnecting returns whether the client is currently connecting
func (c *Client) IsConnecting() bool {
	return c.stateManager.IsConnecting()
}

// GetState returns the current connection state
func (c *Client) GetState() ConnectionState {
	return c.stateManager.GetState()
}

// GetConnectionInfo returns detailed connection information
func (c *Client) GetConnectionInfo() ConnectionInfo {
	return c.stateManager.GetConnectionInfo()
}

// GetConnectionStatus returns detailed connection status information
// This method is kept for backward compatibility but now uses the state manager
func (c *Client) GetConnectionStatus() map[string]interface{} {
	info := c.stateManager.GetConnectionInfo()

	status := map[string]interface{}{
		"connected":       info.State == StateReady,
		"connecting":      c.stateManager.IsConnecting(),
		"retry_count":     info.RetryCount,
		"last_retry_time": info.LastRetryTime,
		"should_retry":    c.stateManager.ShouldRetry(),
	}

	if info.LastError != nil {
		status["last_error"] = info.LastError.Error()
	}

	if info.ServerName != "" {
		status["server_name"] = info.ServerName
	}

	if info.ServerVersion != "" {
		status["server_version"] = info.ServerVersion
	}

	return status
}

// GetServerInfo returns the server information from initialization
func (c *Client) GetServerInfo() *mcp.InitializeResult {
	return c.serverInfo
}

// GetLastError returns the last error encountered
func (c *Client) GetLastError() error {
	info := c.stateManager.GetConnectionInfo()
	return info.LastError
}

// ShouldRetry returns true if the client should retry connecting based on exponential backoff
func (c *Client) ShouldRetry() bool {
	return c.stateManager.ShouldRetry()
}

// ListTools retrieves available tools from the upstream server
func (c *Client) ListTools(ctx context.Context) ([]*config.ToolMetadata, error) {
	if !c.stateManager.IsReady() {
		return nil, fmt.Errorf("client not connected")
	}

	// Check if server supports tools
	if c.serverInfo.Capabilities.Tools == nil {
		c.logger.Debug("Server does not support tools")
		return nil, nil
	}

	toolsRequest := mcp.ListToolsRequest{}
	toolsResult, err := c.client.ListTools(ctx, toolsRequest)
	if err != nil {
		c.stateManager.SetError(err)
		c.logger.Error("ListTools failed", zap.Error(err))
		if c.upstreamLogger != nil {
			c.upstreamLogger.Error("ListTools failed", zap.Error(err))
		}

		// Check if this is a connection error
		if c.isConnectionError(err) {
			c.logger.Warn("Connection appears broken, updating state", zap.Error(err))
			if c.upstreamLogger != nil {
				c.upstreamLogger.Warn("Connection broken detected", zap.Error(err))
			}
		}

		return nil, fmt.Errorf("failed to list tools: %w", err)
	}

	c.logger.Debug("ListTools successful", zap.Int("tools_count", len(toolsResult.Tools)))

	// Convert MCP tools to our metadata format
	var tools []*config.ToolMetadata
	for i := range toolsResult.Tools {
		tool := &toolsResult.Tools[i]
		// Convert tool schema to JSON string for hashing and storage
		var paramsJSON string
		if jsonBytes, err := json.Marshal(tool.InputSchema); err == nil {
			paramsJSON = string(jsonBytes)
		}

		// Generate a hash for change detection
		toolHash := hash.ComputeToolHash(tool.Name, tool.Description, paramsJSON)

		toolMetadata := &config.ToolMetadata{
			Name:        fmt.Sprintf("%s:%s", c.config.Name, tool.Name), // Prefix with server name
			ServerName:  c.config.Name,
			Description: tool.Description,
			ParamsJSON:  paramsJSON,
			Hash:        toolHash,
			Created:     time.Now(),
			Updated:     time.Now(),
		}

		tools = append(tools, toolMetadata)
	}

	return tools, nil
}

// CallTool calls a tool on the upstream server
func (c *Client) CallTool(ctx context.Context, toolName string, args map[string]interface{}) (interface{}, error) {
	if !c.stateManager.IsReady() {
		return nil, fmt.Errorf("client not connected")
	}

	// Check if server supports tools
	if c.serverInfo.Capabilities.Tools == nil {
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

	result, err := c.client.CallTool(ctx, request)
	if err != nil {
		c.stateManager.SetError(err)
		c.logger.Error("CallTool failed", zap.String("tool", toolName), zap.Error(err))
		if c.upstreamLogger != nil {
			c.upstreamLogger.Error("Tool call failed", zap.String("tool", toolName), zap.Error(err))
		}

		// Check if this is a connection error
		if c.isConnectionError(err) {
			c.logger.Warn("Connection appears broken during tool call, updating state",
				zap.String("tool", toolName), zap.Error(err))
			if c.upstreamLogger != nil {
				c.upstreamLogger.Warn("Connection broken during tool call", zap.Error(err))
			}
		}

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
	if !c.stateManager.IsReady() {
		return nil, fmt.Errorf("client not connected")
	}

	// Check if server supports resources
	if c.serverInfo.Capabilities.Resources == nil {
		c.logger.Debug("Server does not support resources")
		return nil, nil
	}

	resourcesRequest := mcp.ListResourcesRequest{}
	resourcesResult, err := c.client.ListResources(ctx, resourcesRequest)
	if err != nil {
		c.stateManager.SetError(err)
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

// isConnectionError checks if an error indicates a broken connection
func (c *Client) isConnectionError(err error) bool {
	return strings.Contains(err.Error(), "connection refused") ||
		strings.Contains(err.Error(), "no such host") ||
		strings.Contains(err.Error(), "timeout")
}

// openBrowser opens the given URL in the default browser
func openBrowser(url string) error {
	var cmd string
	var args []string

	switch runtime.GOOS {
	case osWindows:
		cmd = "cmd"
		args = []string{"/c", "start"}
	case "darwin":
		cmd = "open"
	default: // "linux", "freebsd", "openbsd", "netbsd"
		cmd = "xdg-open"
	}
	args = append(args, url)
	return exec.Command(cmd, args...).Start()
}
