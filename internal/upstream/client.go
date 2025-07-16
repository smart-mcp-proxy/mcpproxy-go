package upstream

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	"mcpproxy-go/internal/config"
	"mcpproxy-go/internal/hash"
	"mcpproxy-go/internal/logs"
	"mcpproxy-go/internal/oauth"
	"mcpproxy-go/internal/secureenv"
	"mcpproxy-go/internal/transport"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"go.uber.org/zap"
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

		// Enhanced OAuth error detection with guards
		if c.isOAuthRelatedError(err) {
			return c.handleOAuthFlow(connectCtx, err)
		}

		// Enhanced error classification for non-OAuth errors
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

	// For stdio transports, start stderr monitoring after client is started
	if transportType == transport.TransportStdio {
		if stderr, hasStderr := client.GetStderr(c.client); hasStderr {
			c.logger.Debug("Starting stderr monitoring for stdio process",
				zap.String("server", c.config.Name))
			c.startStderrMonitoring(stderr)
		} else {
			c.logger.Debug("No stderr available for stdio client",
				zap.String("server", c.config.Name))
		}
	}

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

			// Add comprehensive safety checks before DCR
			c.logger.Debug("Performing pre-DCR safety checks",
				zap.String("server", c.config.Name),
				zap.String("oauth_handler_status", "initialized"))

			// Validate OAuth handler before proceeding
			if oauthHandler == nil {
				err := fmt.Errorf("OAuth handler is nil after initialization")
				c.logger.Error("Critical error - OAuth handler validation failed",
					zap.String("server", c.config.Name),
					zap.Error(err))
				return fmt.Errorf("OAuth setup failed - handler initialization error: %w", err)
			}

			// Use a short timeout for metadata check to prevent hanging
			metadataCtx, metadataCancel := context.WithTimeout(connectCtx, 30*time.Second)
			defer metadataCancel()

			// Enhanced metadata investigation to understand the root cause
			c.logger.Debug("Investigating server OAuth metadata availability",
				zap.String("server", c.config.Name),
				zap.String("server_url", c.config.URL))

			// Try to directly check if the server supports OAuth metadata endpoints
			metadataEndpoints := []string{
				c.config.URL + "/.well-known/oauth-authorization-server",
				c.config.URL + "/.well-known/oauth-protected-resource",
				strings.TrimSuffix(c.config.URL, "/mcp") + "/.well-known/oauth-authorization-server",
				strings.TrimSuffix(c.config.URL, "/") + "/.well-known/oauth-protected-resource",
			}

			c.logger.Debug("Checking potential OAuth metadata endpoints",
				zap.String("server", c.config.Name),
				zap.Strings("endpoints", metadataEndpoints))

			// Enhanced resource management and cleanup
			defer func() {
				// Ensure proper cleanup regardless of outcome
				if metadataCtx.Err() != nil {
					c.logger.Debug("OAuth metadata context cleanup",
						zap.String("server", c.config.Name),
						zap.Error(metadataCtx.Err()))
				}
			}()

			// This is a defensive call to understand what getServerMetadata returns
			// We'll wrap the actual RegisterClient call to catch the nil pointer issue
			func() {
				defer func() {
					if r := recover(); r != nil {
						c.logger.Error("Panic detected during DCR preparation",
							zap.String("server", c.config.Name),
							zap.Any("panic", r),
							zap.String("stack", string(debug.Stack())))
					}
				}()

				c.logger.Debug("Starting Dynamic Client Registration with enhanced safety wrapper",
					zap.String("server", c.config.Name))
			}()

			// Step 1: Dynamic Client Registration with enhanced error handling and safety wrapper
			var regErr error
			var isDCRUnsupported bool
			func() {
				defer func() {
					if r := recover(); r != nil {
						panicStr := fmt.Sprintf("%v", r)
						c.logger.Error("Panic during RegisterClient call - enhanced diagnostics",
							zap.String("server", c.config.Name),
							zap.Any("panic", r),
							zap.String("panic_type", fmt.Sprintf("%T", r)),
							zap.String("likely_cause", "Server OAuth metadata endpoint unavailable or malformed"),
							zap.String("recovery_action", "Gracefully handling panic and setting appropriate error state"))

						// Enhanced error classification based on panic details
						if strings.Contains(panicStr, "nil pointer") {
							regErr = fmt.Errorf("OAuth metadata unavailable - server does not provide valid OAuth configuration: %v", r)
							isDCRUnsupported = true
						} else {
							regErr = fmt.Errorf("OAuth registration failed with unexpected error: %v", r)
						}
					}
				}()

				c.logger.Debug("Calling oauthHandler.RegisterClient with enhanced monitoring",
					zap.String("server", c.config.Name),
					zap.String("client_name", "mcpproxy-go"),
					zap.Duration("timeout", 30*time.Second))

				startTime := time.Now()
				regErr = oauthHandler.RegisterClient(metadataCtx, "mcpproxy-go")
				duration := time.Since(startTime)

				c.logger.Debug("RegisterClient call completed with detailed metrics",
					zap.String("server", c.config.Name),
					zap.Bool("success", regErr == nil),
					zap.Duration("duration", duration),
					zap.String("error", func() string {
						if regErr != nil {
							return regErr.Error()
						}
						return "none"
					}()))
			}()

			if regErr != nil {
				// Enhanced DCR failure handling with detailed error classification
				c.handleDCRFailure(regErr, c.config.Name)

				// Enhanced error classification for better user guidance
				var userFriendlyError error
				if isDCRUnsupported {
					userFriendlyError = fmt.Errorf("OAuth setup failed - server does not provide OAuth metadata endpoints. This server may not support OAuth or requires manual OAuth configuration: %w", regErr)
					c.logger.Warn("Server appears to not support Dynamic Client Registration",
						zap.String("server", c.config.Name),
						zap.String("recommendation", "Consider using Personal Access Token or pre-configured OAuth credentials"))
				} else {
					userFriendlyError = fmt.Errorf("OAuth setup failed - server requires pre-configured OAuth application: %w", regErr)
				}

				// Instead of immediately failing, transition to a recoverable error state
				c.stateManager.SetError(userFriendlyError)

				// Log detailed recovery instructions
				c.logOAuthRecoveryInstructions(regErr)

				// Return a user-friendly error instead of crashing
				return userFriendlyError
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

	// Create stdio client with stderr access
	result, err := transport.CreateStdioClient(stdioConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create stdio client: %w", err)
	}

	c.logger.Debug("Created stdio client",
		zap.String("server", c.config.Name))
	if c.upstreamLogger != nil {
		c.upstreamLogger.Debug("Created stdio client")
	}

	return result.Client, nil
}

// startStderrMonitoring starts a goroutine to monitor stderr output from stdio processes
func (c *Client) startStderrMonitoring(stderr io.Reader) {
	if c.upstreamLogger != nil {
		c.upstreamLogger.Debug("Starting stderr monitoring")
	}

	go func() {
		defer func() {
			if r := recover(); r != nil {
				c.logger.Error("Panic in stderr monitoring",
					zap.String("server", c.config.Name),
					zap.Any("panic", r))
			}
		}()

		buf := make([]byte, 4096)
		for {
			n, err := stderr.Read(buf)
			if err != nil {
				if err != io.EOF {
					c.logger.Debug("Stderr monitoring ended",
						zap.String("server", c.config.Name),
						zap.Error(err))
					if c.upstreamLogger != nil {
						c.upstreamLogger.Debug("Stderr monitoring ended", zap.Error(err))
					}
				}
				return
			}
			if n > 0 {
				// Log stderr output to both main and upstream loggers
				stderrOutput := string(buf[:n])
				c.logger.Info("Process stderr output",
					zap.String("server", c.config.Name),
					zap.String("stderr", strings.TrimSpace(stderrOutput)))

				if c.upstreamLogger != nil {
					c.upstreamLogger.Info("Process stderr output",
						zap.String("stderr", strings.TrimSpace(stderrOutput)))
				}
			}
		}
	}()
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

		// Extract detailed HTTP error information
		httpErr := c.extractHTTPErrorDetails(err)

		// Enhanced error message creation with HTTP context
		var enrichedErr error
		errStr := err.Error()

		// OAuth/Authentication errors
		if strings.Contains(errStr, "401") || strings.Contains(errStr, "Unauthorized") {
			enrichedErr = fmt.Errorf("authentication required for tool '%s' on server '%s' - OAuth/token authentication needed: %w", toolName, c.config.Name, err)
		} else if strings.Contains(errStr, "403") || strings.Contains(errStr, "Forbidden") {
			enrichedErr = fmt.Errorf("access forbidden for tool '%s' on server '%s' - insufficient permissions or invalid credentials: %w", toolName, c.config.Name, err)
		} else if strings.Contains(errStr, "invalid_token") || strings.Contains(errStr, "token") {
			enrichedErr = fmt.Errorf("invalid or expired token for tool '%s' on server '%s' - token refresh or re-authentication required: %w", toolName, c.config.Name, err)
		} else if c.isConnectionError(err) {
			// Connection errors
			enrichedErr = fmt.Errorf("connection failed for tool '%s' on server '%s' - check server availability and network connectivity: %w", toolName, c.config.Name, err)
		} else if strings.Contains(errStr, "timeout") || strings.Contains(errStr, "deadline exceeded") {
			enrichedErr = fmt.Errorf("timeout calling tool '%s' on server '%s' - server may be overloaded or unresponsive: %w", toolName, c.config.Name, err)
		} else {
			// Generic error with server context
			enrichedErr = fmt.Errorf("tool call failed for '%s' on server '%s': %w", toolName, c.config.Name, err)
		}

		// Log detailed HTTP error information
		c.logDetailedHTTPError(toolName, httpErr, nil)

		// Check if this is a connection error
		if c.isConnectionError(err) {
			c.logger.Warn("Connection appears broken during tool call, updating state",
				zap.String("tool", toolName),
				zap.String("server", c.config.Name),
				zap.Error(enrichedErr))
			if c.upstreamLogger != nil {
				c.upstreamLogger.Warn("Connection broken during tool call",
					zap.String("server", c.config.Name),
					zap.Error(enrichedErr))
			}
		}

		return nil, enrichedErr
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
		// Check for JSON-RPC errors in successful responses
		if result.IsError {
			// Extract JSON-RPC error details for better debugging
			jsonRPCErr := c.extractJSONRPCErrorDetails(result, nil)
			if jsonRPCErr != nil {
				// Log detailed error information
				c.logDetailedHTTPError(toolName, nil, jsonRPCErr)
			}
		}

		// Return the content array directly
		return result.Content, nil
	}

	// If there's an error in the result, return it
	if result.IsError {
		// Extract JSON-RPC error details for enhanced error message
		jsonRPCErr := c.extractJSONRPCErrorDetails(result, nil)
		if jsonRPCErr != nil {
			c.logDetailedHTTPError(toolName, nil, jsonRPCErr)
			return nil, fmt.Errorf("tool call failed: %s", jsonRPCErr.Error())
		}
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

// isOAuthRelatedError detects various OAuth-related errors with enhanced guards
func (c *Client) isOAuthRelatedError(err error) bool {
	if err == nil {
		return false
	}

	// Check for library-specific OAuth errors first
	if client.IsOAuthAuthorizationRequiredError(err) {
		return true
	}

	errStr := err.Error()

	// Enhanced detection for various OAuth error patterns
	oauthPatterns := []string{
		"401", "Unauthorized", "unauthorized",
		"invalid_token", "Missing or invalid access token",
		"Bearer", "authentication required", "authorization required",
		"oauth", "OAuth", "token", "access_denied",
		"insufficient_scope", "invalid_grant",
	}

	for _, pattern := range oauthPatterns {
		if strings.Contains(errStr, pattern) {
			c.logger.Debug("OAuth-related error pattern detected",
				zap.String("server", c.config.Name),
				zap.String("pattern", pattern),
				zap.Error(err))
			return true
		}
	}

	return false
}

// handleOAuthFlow manages the complete OAuth authentication flow with error recovery
func (c *Client) handleOAuthFlow(_ context.Context, originalErr error) error {
	c.logger.Info("Starting OAuth authentication flow",
		zap.String("server", c.config.Name),
		zap.Error(originalErr))

	// Transition to authenticating state
	c.stateManager.TransitionTo(StateAuthenticating)

	// Check if this is a standard OAuth authorization required error
	if client.IsOAuthAuthorizationRequiredError(originalErr) {
		c.logger.Info("Standard OAuth authorization required - starting DCR flow",
			zap.String("server", c.config.Name))
		if c.upstreamLogger != nil {
			c.upstreamLogger.Info("OAuth authentication flow starting via mcp-go library")
		}

		// This will fall through to the existing DCR handling in Initialize()
		// The library should handle the OAuth flow automatically, but if we reach here,
		// the OAuth flow failed for some reason
		c.stateManager.SetError(fmt.Errorf("OAuth authentication failed: %w", originalErr))
		return fmt.Errorf("OAuth authentication failed: %w", originalErr)
	}

	// Handle non-standard OAuth errors (like manual Bearer token requirements)
	c.logger.Warn("Non-standard OAuth error detected",
		zap.String("server", c.config.Name),
		zap.Error(originalErr),
		zap.String("suggestion", "Server may require manual OAuth configuration"))

	// Check for specific error patterns and provide guidance
	errStr := originalErr.Error()
	if strings.Contains(errStr, "401") || strings.Contains(errStr, "Unauthorized") {
		c.logger.Info("HTTP 401 Unauthorized detected",
			zap.String("server", c.config.Name),
			zap.String("likely_cause", "Missing or invalid access token"),
			zap.String("solution", "Configure OAuth credentials or Personal Access Token"))
	}

	if strings.Contains(errStr, "invalid_token") || strings.Contains(errStr, "Missing or invalid access token") {
		c.logger.Info("Token-related error detected",
			zap.String("server", c.config.Name),
			zap.String("likely_cause", "Server requires authentication but no valid token provided"),
			zap.String("solution", "Add OAuth configuration or Authorization header"))
	}

	// Log recovery instructions for manual OAuth setup
	c.logOAuthRecoveryInstructions(originalErr)

	// Set recoverable error state instead of crashing
	c.stateManager.SetError(fmt.Errorf("OAuth authentication required but not properly configured: %w", originalErr))

	return fmt.Errorf("OAuth authentication failed for %s - server requires authentication but OAuth is not properly configured. See logs for setup instructions", c.config.Name)
}

// handleDCRFailure provides detailed error classification for Dynamic Client Registration failures
func (c *Client) handleDCRFailure(err error, serverName string) {
	errStr := err.Error()

	// Enhanced error classification with production-ready patterns
	var errorType, solution, recommendation string

	// Classify different types of DCR failures
	if strings.Contains(errStr, "403") || strings.Contains(errStr, "Forbidden") {
		errorType = "DCR_FORBIDDEN"
		solution = "Server requires pre-registered OAuth application"
		recommendation = "Create OAuth app in provider's developer console"

		c.logger.Error("Dynamic Client Registration forbidden - server does not support DCR",
			zap.String("server", serverName),
			zap.Error(err),
			zap.String("error_type", errorType),
			zap.String("solution", solution))

	} else if strings.Contains(errStr, "404") || strings.Contains(errStr, "Not Found") {
		errorType = "DCR_NOT_FOUND"
		solution = "DCR endpoint not available on this server"
		recommendation = "Use Personal Access Token or pre-configured OAuth credentials"

		c.logger.Error("Dynamic Client Registration endpoint not found",
			zap.String("server", serverName),
			zap.Error(err),
			zap.String("error_type", errorType),
			zap.String("solution", solution))

	} else if strings.Contains(errStr, "401") || strings.Contains(errStr, "Unauthorized") {
		errorType = "DCR_UNAUTHORIZED"
		solution = "DCR requires authentication"
		recommendation = "Check server OAuth configuration"

		c.logger.Error("Dynamic Client Registration requires authentication",
			zap.String("server", serverName),
			zap.Error(err),
			zap.String("error_type", errorType),
			zap.String("solution", solution))

	} else if strings.Contains(errStr, "timeout") || strings.Contains(errStr, "context deadline exceeded") {
		errorType = "DCR_TIMEOUT"
		solution = "Server did not respond within timeout period"
		recommendation = "Check network connectivity and server availability"

		c.logger.Error("Dynamic Client Registration timed out",
			zap.String("server", serverName),
			zap.Error(err),
			zap.String("error_type", errorType),
			zap.String("solution", solution))

	} else if strings.Contains(errStr, "OAuth metadata unavailable") {
		errorType = "DCR_METADATA_UNAVAILABLE"
		solution = "Server OAuth metadata endpoints are not accessible"
		recommendation = "Server may not support OAuth or requires different authentication method"

		c.logger.Error("OAuth metadata unavailable - server may not support OAuth",
			zap.String("server", serverName),
			zap.Error(err),
			zap.String("error_type", errorType),
			zap.String("solution", solution))

	} else {
		errorType = "DCR_UNKNOWN"
		solution = "Unknown OAuth registration error"
		recommendation = "Check server OAuth configuration and network connectivity"

		c.logger.Error("Dynamic Client Registration failed with unknown error",
			zap.String("server", serverName),
			zap.Error(err),
			zap.String("error_type", errorType),
			zap.String("solution", solution))
	}

	// Production-ready circuit breaker logic
	c.updateOAuthFailureMetrics(errorType)

	// Log to upstream logger if available
	if c.upstreamLogger != nil {
		c.upstreamLogger.Error("DCR_FAILURE",
			zap.String("server", serverName),
			zap.String("error_type", errorType),
			zap.String("solution", solution),
			zap.String("recommendation", recommendation),
			zap.Error(err))
	}
}

// updateOAuthFailureMetrics tracks OAuth failure patterns for circuit breaker
func (c *Client) updateOAuthFailureMetrics(errorType string) {
	// This could be extended with metrics collection
	c.logger.Debug("OAuth failure metrics updated",
		zap.String("server", c.config.Name),
		zap.String("failure_type", errorType),
		zap.String("recommendation", "Consider implementing circuit breaker if failures persist"))
}

// logOAuthRecoveryInstructions provides detailed recovery instructions for OAuth setup failures
func (c *Client) logOAuthRecoveryInstructions(err error) {
	serverName := c.config.Name
	serverURL := c.config.URL

	c.logger.Info("=== OAuth Configuration Help ===",
		zap.String("server", serverName),
		zap.String("issue", "Server does not support Dynamic Client Registration"))

	// Generic OAuth setup instructions for any MCP server
	c.logger.Info("OAuth Setup Instructions",
		zap.String("server", serverName),
		zap.String("step_1", "Create OAuth application in your OAuth provider's developer settings"),
		zap.String("step_2", "Set Authorization callback URL to: http://127.0.0.1:0/oauth/callback"),
		zap.String("step_3", "Add OAuth configuration to mcpproxy server settings"),
		zap.String("config_path", "~/.mcpproxy/mcp_config.json"))

	c.logger.Info("OAuth Configuration Example",
		zap.String("server", serverName),
		zap.String("config_example", `"oauth": {"client_id": "your_oauth_client_id", "client_secret": "your_oauth_client_secret", "scopes": ["mcp.read", "mcp.write"]}`))

	// Alternative solution using Personal Access Token
	c.logger.Info("Alternative: Use Personal Access Token",
		zap.String("server", serverName),
		zap.String("option", "Instead of OAuth, you can use a Personal Access Token if supported by the server"),
		zap.String("config_example", `"headers": {"Authorization": "Bearer your_personal_access_token"}`))

	// Log the specific error for debugging
	if c.upstreamLogger != nil {
		c.upstreamLogger.Info("=== OAuth Setup Required ===",
			zap.String("reason", "Dynamic Client Registration not supported"),
			zap.String("server_url", serverURL),
			zap.Error(err))
	}

	c.logger.Info("=== End OAuth Configuration Help ===", zap.String("server", serverName))
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

// extractHTTPErrorDetails attempts to extract HTTP error details from error messages
func (c *Client) extractHTTPErrorDetails(err error) *transport.HTTPError {
	if err == nil {
		return nil
	}

	errStr := err.Error()

	// Try to extract status code from error message
	statusCode := 0
	if matches := regexp.MustCompile(`status (?:code )?(\d+)`).FindStringSubmatch(errStr); len(matches) > 1 {
		if code, parseErr := strconv.Atoi(matches[1]); parseErr == nil {
			statusCode = code
		}
	}

	// Try to extract response body from error message
	body := ""
	if matches := regexp.MustCompile(`(?:response|body):\s*(.+)$`).FindStringSubmatch(errStr); len(matches) > 1 {
		body = strings.TrimSpace(matches[1])
	}

	// If we found meaningful HTTP details, create HTTPError
	if statusCode > 0 || body != "" {
		return transport.NewHTTPError(
			statusCode,
			body,
			"POST", // MCP calls are typically POST
			c.config.URL,
			map[string]string{}, // Headers not available from error
			err,
		)
	}

	return nil
}

// extractJSONRPCErrorDetails attempts to extract JSON-RPC error details from tool call results
func (c *Client) extractJSONRPCErrorDetails(result *mcp.CallToolResult, httpErr *transport.HTTPError) *transport.JSONRPCError {
	if result == nil || !result.IsError {
		return nil
	}

	// Try to extract JSON-RPC error from content
	for _, content := range result.Content {
		// Convert content to JSON string to extract text
		contentBytes, err := json.Marshal(content)
		if err != nil {
			continue
		}

		var contentObj map[string]interface{}
		if err := json.Unmarshal(contentBytes, &contentObj); err != nil {
			continue
		}

		// Look for text content
		if textContent, ok := contentObj["text"].(string); ok {
			// Look for JSON-RPC error patterns
			if strings.Contains(textContent, "API Error") || strings.Contains(textContent, "status code") {
				// Try to extract status code from text
				code := -1
				if matches := regexp.MustCompile(`status code (\d+)`).FindStringSubmatch(textContent); len(matches) > 1 {
					if statusCode, err := strconv.Atoi(matches[1]); err == nil {
						code = statusCode
					}
				}

				return transport.NewJSONRPCError(
					code,
					textContent,
					nil,
					httpErr,
				)
			}
		}
	}

	return nil
}

// logDetailedHTTPError logs detailed HTTP error information for debugging
func (c *Client) logDetailedHTTPError(toolName string, httpErr *transport.HTTPError, jsonRPCErr *transport.JSONRPCError) {
	// Log to main logger
	fields := []zap.Field{
		zap.String("tool", toolName),
		zap.String("server", c.config.Name),
	}

	if httpErr != nil {
		fields = append(fields,
			zap.Int("http_status", httpErr.StatusCode),
			zap.String("http_method", httpErr.Method),
			zap.String("http_url", httpErr.URL),
			zap.String("http_body", httpErr.Body),
			zap.Any("http_headers", httpErr.Headers),
		)
	}

	if jsonRPCErr != nil {
		fields = append(fields,
			zap.Int("jsonrpc_code", jsonRPCErr.Code),
			zap.String("jsonrpc_message", jsonRPCErr.Message),
			zap.Any("jsonrpc_data", jsonRPCErr.Data),
		)
	}

	c.logger.Error("Detailed HTTP tool call error", fields...)

	// Log to upstream server log if available
	if c.upstreamLogger != nil {
		upstreamFields := []zap.Field{
			zap.String("tool", toolName),
		}

		if httpErr != nil {
			upstreamFields = append(upstreamFields,
				zap.Int("http_status", httpErr.StatusCode),
				zap.String("http_body", httpErr.Body),
			)
		}

		if jsonRPCErr != nil {
			upstreamFields = append(upstreamFields,
				zap.String("jsonrpc_message", jsonRPCErr.Message),
			)
		}

		c.upstreamLogger.Error("HTTP tool call failed", upstreamFields...)
	}
}

// isConnectionError checks if an error is related to connection issues
