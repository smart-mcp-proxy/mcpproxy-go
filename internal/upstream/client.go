package upstream

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"runtime"
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
	mcptransport "github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	"go.uber.org/zap"
)

const (
	authTypeOAuth   = "OAuth"
	authTypeHeaders = "headers"
	authTypeNoAuth  = "no-auth"
	authTypeStdio   = "stdio"

	// Operating system constants for browser opening
	osWindows = "windows"
	osDarwin  = "darwin"
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
	c.logger.Debug("Connect method called",
		zap.String("server", c.config.Name),
		zap.String("url", c.config.URL),
		zap.String("command", c.config.Command),
		zap.String("protocol", c.config.Protocol),
		zap.Bool("enabled", c.config.Enabled),
		zap.Bool("quarantined", c.config.Quarantined),
		zap.String("current_state", c.stateManager.GetState().String()),
		zap.Bool("is_connecting", c.stateManager.IsConnecting()),
		zap.Bool("is_ready", c.stateManager.IsReady()))

	// Check if already connecting or connected
	if c.stateManager.IsConnecting() || c.stateManager.IsReady() {
		errMsg := fmt.Sprintf("connection already in progress or established (state: %s)", c.stateManager.GetState().String())
		c.logger.Debug("Connect aborted - already in progress or connected",
			zap.String("server", c.config.Name),
			zap.String("current_state", c.stateManager.GetState().String()))
		return errors.New(errMsg)
	}

	c.logger.Info("Starting connection to upstream MCP server",
		zap.String("server", c.config.Name),
		zap.String("url", c.config.URL),
		zap.String("command", c.config.Command),
		zap.String("protocol", c.config.Protocol))

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

	// Implement authentication strategy based on configuration
	if transportType == transport.TransportStdio {
		// Stdio doesn't support auth fallback, use original logic
		return c.connectStdio(ctx, transportType)
	}

	// For HTTP/SSE transports, implement headers-first with OAuth fallback
	if len(c.config.Headers) > 0 {
		c.logger.Info("Headers configured - trying headers authentication first",
			zap.String("server", c.config.Name),
			zap.Int("header_count", len(c.config.Headers)))

		if err := c.attemptHeadersAuth(ctx, transportType); err != nil {
			if c.isAuthenticationError(err) {
				c.logger.Info("Headers authentication failed with auth error - trying OAuth fallback",
					zap.String("server", c.config.Name),
					zap.Error(err))
				return c.attemptOAuthAuth(ctx, transportType)
			}
			return err
		}
		return nil
	}

	// No headers configured - try no-auth first, then OAuth fallback
	c.logger.Info("No headers configured - trying connection without authentication first",
		zap.String("server", c.config.Name))

	if err := c.attemptNoAuth(ctx, transportType); err != nil {
		if c.isAuthenticationError(err) {
			c.logger.Info("No-auth connection failed with auth error - trying OAuth fallback",
				zap.String("server", c.config.Name),
				zap.Error(err))
			return c.attemptOAuthAuth(ctx, transportType)
		}
		return err
	}
	return nil
}

// isAuthenticationError checks if an error indicates authentication is required
func (c *Client) isAuthenticationError(err error) bool {
	if err == nil {
		return false
	}

	errStr := err.Error()
	authIndicators := []string{
		"401", "Unauthorized", "unauthorized",
		"403", "Forbidden", "forbidden",
		"authorization required", "authentication required",
		"invalid_token", "missing token", "no valid token",
		"access_denied", "WWW-Authenticate",
	}

	for _, indicator := range authIndicators {
		if strings.Contains(errStr, indicator) {
			c.logger.Debug("Authentication error detected",
				zap.String("server", c.config.Name),
				zap.String("indicator", indicator),
				zap.Error(err))
			return true
		}
	}

	return false
}

// attemptHeadersAuth attempts connection using only headers (no OAuth)
func (c *Client) attemptHeadersAuth(ctx context.Context, transportType string) error {
	c.logger.Debug("Attempting headers-only authentication",
		zap.String("server", c.config.Name),
		zap.String("transport_type", transportType))

	// Create client with headers but no OAuth
	client, err := c.createClientWithAuth(transportType, false) // false = no OAuth
	if err != nil {
		return fmt.Errorf("failed to create headers-only client: %w", err)
	}

	c.client = client
	return c.completeConnection(ctx, transportType, authTypeHeaders)
}

// attemptNoAuth attempts connection without any authentication
func (c *Client) attemptNoAuth(ctx context.Context, transportType string) error {
	c.logger.Debug("Attempting connection without authentication",
		zap.String("server", c.config.Name),
		zap.String("transport_type", transportType))

	// Temporarily clear headers for no-auth attempt
	originalHeaders := c.config.Headers
	c.config.Headers = nil
	defer func() { c.config.Headers = originalHeaders }()

	// Create client without OAuth or headers
	client, err := c.createClientWithAuth(transportType, false) // false = no OAuth
	if err != nil {
		return fmt.Errorf("failed to create no-auth client: %w", err)
	}

	c.client = client
	return c.completeConnection(ctx, transportType, authTypeNoAuth)
}

// attemptOAuthAuth attempts connection using OAuth
func (c *Client) attemptOAuthAuth(ctx context.Context, transportType string) error {
	c.logger.Debug("Attempting OAuth authentication",
		zap.String("server", c.config.Name),
		zap.String("transport_type", transportType))

	// Create client with OAuth enabled
	client, err := c.createClientWithAuth(transportType, true) // true = use OAuth
	if err != nil {
		return fmt.Errorf("failed to create OAuth client: %w", err)
	}

	c.client = client
	return c.completeConnection(ctx, transportType, authTypeOAuth)
}

// createClientWithAuth creates a client with the specified auth mode
func (c *Client) createClientWithAuth(transportType string, useOAuth bool) (*client.Client, error) {
	switch transportType {
	case transport.TransportHTTP, transport.TransportStreamableHTTP:
		return c.createHTTPClientWithAuth(useOAuth)
	case transport.TransportSSE:
		return c.createSSEClientWithAuth(useOAuth)
	default:
		return nil, fmt.Errorf("unsupported transport type for auth: %s", transportType)
	}
}

// createHTTPClientWithAuth creates an HTTP client with specific auth settings
func (c *Client) createHTTPClientWithAuth(useOAuth bool) (*client.Client, error) {
	c.logger.Debug("Creating HTTP client with auth settings",
		zap.String("server", c.config.Name),
		zap.String("url", c.config.URL),
		zap.Bool("use_oauth", useOAuth))

	var oauthConfig *client.OAuthConfig
	if useOAuth {
		oauthConfig = oauth.CreateOAuthConfig(c.config)
		if oauthConfig != nil {
			c.logger.Debug("OAuth configuration created",
				zap.String("server", c.config.Name),
				zap.Strings("scopes", oauthConfig.Scopes))
		}
	}

	httpConfig := transport.CreateHTTPTransportConfig(c.config, oauthConfig)
	return transport.CreateHTTPClient(httpConfig)
}

// createSSEClientWithAuth creates an SSE client with specific auth settings
func (c *Client) createSSEClientWithAuth(useOAuth bool) (*client.Client, error) {
	c.logger.Debug("Creating SSE client with auth settings",
		zap.String("server", c.config.Name),
		zap.String("url", c.config.URL),
		zap.Bool("use_oauth", useOAuth))

	var oauthConfig *client.OAuthConfig
	if useOAuth {
		oauthConfig = oauth.CreateOAuthConfig(c.config)
		if oauthConfig != nil {
			c.logger.Debug("OAuth configuration created for SSE",
				zap.String("server", c.config.Name),
				zap.Strings("scopes", oauthConfig.Scopes))
		}
	}

	sseConfig := transport.CreateHTTPTransportConfig(c.config, oauthConfig)
	return transport.CreateSSEClient(sseConfig)
}

// safeClientStart wraps client.Start() with panic recovery to prevent crashes
// when connecting to non-MCP endpoints
func (c *Client) safeClientStart(ctx context.Context, authType string) (err error) {
	defer func() {
		if r := recover(); r != nil {
			c.logger.Error("Panic during client start - likely not an MCP server",
				zap.String("server", c.config.Name),
				zap.String("url", c.config.URL),
				zap.String("auth_type", authType),
				zap.Any("panic", r))

			if c.upstreamLogger != nil {
				c.upstreamLogger.Error("Panic during client start",
					zap.String("auth_type", authType),
					zap.Any("panic", r))
			}

			// Convert panic to a descriptive error
			err = fmt.Errorf("failed to start MCP client - endpoint may not be an MCP server or may have crashed the client library. URL: %s, panic: %v", c.config.URL, r)
		}
	}()

	if err := c.client.Start(ctx); err != nil {
		c.logger.Debug("Client.Start() failed",
			zap.String("server", c.config.Name),
			zap.String("auth_type", authType),
			zap.Error(err))
		return err
	}

	return nil
}

// safeClientInitialize wraps client.Initialize() with panic recovery to prevent crashes
// when connecting to non-MCP endpoints
func (c *Client) safeClientInitialize(ctx context.Context, initRequest *mcp.InitializeRequest, authType string) (serverInfo *mcp.InitializeResult, err error) {
	defer func() {
		if r := recover(); r != nil {
			c.logger.Error("Panic during client initialization - likely not an MCP server",
				zap.String("server", c.config.Name),
				zap.String("url", c.config.URL),
				zap.String("auth_type", authType),
				zap.Any("panic", r))

			if c.upstreamLogger != nil {
				c.upstreamLogger.Error("Panic during client initialization",
					zap.String("auth_type", authType),
					zap.Any("panic", r))
			}

			// Convert panic to a descriptive error
			err = fmt.Errorf("failed to initialize MCP client - endpoint may not be an MCP server or may have returned invalid response. URL: %s, panic: %v", c.config.URL, r)
			serverInfo = nil
		}
	}()

	result, err := c.client.Initialize(ctx, *initRequest)
	if err != nil {
		c.logger.Debug("Client.Initialize() failed",
			zap.String("server", c.config.Name),
			zap.String("auth_type", authType),
			zap.Error(err))
		return nil, err
	}

	return result, nil
}

// completeConnection completes the connection process after client creation
func (c *Client) completeConnection(ctx context.Context, transportType, authType string) error {
	c.logger.Debug("Completing connection",
		zap.String("server", c.config.Name),
		zap.String("transport_type", transportType),
		zap.String("auth_type", authType))

	// Set connection timeout
	timeout := c.getConnectionTimeout()
	connectCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Start the client with panic recovery
	c.logger.Debug("Starting MCP client connection",
		zap.String("server", c.config.Name),
		zap.String("auth_type", authType))

	if err := c.safeClientStart(connectCtx, authType); err != nil {
		// For OAuth clients, handle OAuth flow errors
		if authType == authTypeOAuth && c.isOAuthRelatedError(err) {
			return c.handleOAuthFlow(connectCtx, err)
		}

		c.stateManager.SetError(err)
		if c.upstreamLogger != nil {
			c.upstreamLogger.Error("Client start failed",
				zap.String("auth_type", authType),
				zap.Error(err))
		}
		return fmt.Errorf("failed to start MCP client with %s auth: %w", authType, err)
	}

	c.logger.Debug("Client.Start() succeeded",
		zap.String("server", c.config.Name),
		zap.String("auth_type", authType))

	// For stdio transports, start stderr monitoring
	if transportType == transport.TransportStdio {
		if stderr, hasStderr := client.GetStderr(c.client); hasStderr {
			c.startStderrMonitoring(stderr)
		}
	}

	// Transition to discovering state
	c.stateManager.TransitionTo(StateDiscovering)

	// Initialize the client with panic recovery
	c.logger.Debug("Initializing MCP client",
		zap.String("server", c.config.Name),
		zap.String("auth_type", authType))

	initRequest := mcp.InitializeRequest{}
	initRequest.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initRequest.Params.ClientInfo = mcp.Implementation{
		Name:    "mcpproxy-go",
		Version: "1.0.0",
	}
	initRequest.Params.Capabilities = mcp.ClientCapabilities{}

	serverInfo, err := c.safeClientInitialize(connectCtx, &initRequest, authType)
	if err != nil {
		// For OAuth clients, handle OAuth initialization errors
		if authType == authTypeOAuth && (client.IsOAuthAuthorizationRequiredError(err) ||
			c.isOAuthRelatedError(err)) {
			return c.handleOAuthFlow(connectCtx, err)
		}

		c.stateManager.SetError(err)
		c.logger.Error("Failed to initialize MCP client",
			zap.String("server", c.config.Name),
			zap.String("auth_type", authType),
			zap.Error(err))
		if c.upstreamLogger != nil {
			c.upstreamLogger.Error("Initialize failed",
				zap.String("auth_type", authType),
				zap.Error(err))
		}
		c.client.Close()
		return fmt.Errorf("failed to initialize MCP client with %s auth: %w", authType, err)
	}

	c.logger.Debug("Client.Initialize() succeeded",
		zap.String("server", c.config.Name),
		zap.String("auth_type", authType))

	// Store server info and transition to ready state
	c.serverInfo = serverInfo
	c.stateManager.SetServerInfo(serverInfo.ServerInfo.Name, serverInfo.ServerInfo.Version)
	c.stateManager.TransitionTo(StateReady)

	c.logger.Info("Successfully connected to upstream MCP server",
		zap.String("server_name", serverInfo.ServerInfo.Name),
		zap.String("server_version", serverInfo.ServerInfo.Version),
		zap.String("auth_type", authType))

	if c.upstreamLogger != nil {
		c.upstreamLogger.Info("Connected successfully",
			zap.String("server_name", serverInfo.ServerInfo.Name),
			zap.String("server_version", serverInfo.ServerInfo.Version),
			zap.String("auth_type", authType),
			zap.String("protocol_version", serverInfo.ProtocolVersion))
	}

	return nil
}

// connectStdio handles stdio connections (original logic)
func (c *Client) connectStdio(ctx context.Context, transportType string) error {
	c.logger.Debug("Creating STDIO client",
		zap.String("server", c.config.Name))

	// Create stdio transport config
	stdioConfig := transport.CreateStdioTransportConfig(c.config, c.envManager)

	// Create stdio client with stderr access
	result, err := transport.CreateStdioClient(stdioConfig)
	if err != nil {
		return fmt.Errorf("failed to create stdio client: %w", err)
	}

	c.logger.Debug("Created stdio client",
		zap.String("server", c.config.Name))
	if c.upstreamLogger != nil {
		c.upstreamLogger.Debug("Created stdio client")
	}

	c.client = result.Client
	return c.completeConnection(ctx, transportType, authTypeStdio)
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
	toolsResult, err := c.safeListTools(ctx, toolsRequest)
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

// safeListTools wraps client.ListTools() with panic recovery to prevent crashes
// when listing tools fails unexpectedly
func (c *Client) safeListTools(ctx context.Context, request mcp.ListToolsRequest) (result *mcp.ListToolsResult, err error) {
	defer func() {
		if r := recover(); r != nil {
			c.logger.Error("Panic during list tools",
				zap.String("server", c.config.Name),
				zap.String("url", c.config.URL),
				zap.Any("panic", r))

			if c.upstreamLogger != nil {
				c.upstreamLogger.Error("Panic during list tools",
					zap.Any("panic", r))
			}

			// Convert panic to a descriptive error
			err = fmt.Errorf("list tools crashed - server may not be properly responding to tool listing. URL: %s, panic: %v", c.config.URL, r)
			result = nil
		}
	}()

	return c.client.ListTools(ctx, request)
}

// safeCallTool wraps client.CallTool() with panic recovery to prevent crashes
// when tool calls fail unexpectedly
func (c *Client) safeCallTool(ctx context.Context, request mcp.CallToolRequest) (result *mcp.CallToolResult, err error) {
	defer func() {
		if r := recover(); r != nil {
			c.logger.Error("Panic during tool call",
				zap.String("server", c.config.Name),
				zap.String("url", c.config.URL),
				zap.String("tool", request.Params.Name),
				zap.Any("panic", r))

			if c.upstreamLogger != nil {
				c.upstreamLogger.Error("Panic during tool call",
					zap.String("tool", request.Params.Name),
					zap.Any("panic", r))
			}

			// Convert panic to a descriptive error
			err = fmt.Errorf("tool call crashed - server may not be properly responding to tool calls. Tool: %s, URL: %s, panic: %v", request.Params.Name, c.config.URL, r)
			result = nil
		}
	}()

	return c.client.CallTool(ctx, request)
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

	result, err := c.safeCallTool(ctx, request)
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
	resourcesResult, err := c.safeListResources(ctx, resourcesRequest)
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

// safeListResources wraps client.ListResources() with panic recovery to prevent crashes
// when listing resources fails unexpectedly
func (c *Client) safeListResources(ctx context.Context, request mcp.ListResourcesRequest) (result *mcp.ListResourcesResult, err error) {
	defer func() {
		if r := recover(); r != nil {
			c.logger.Error("Panic during list resources",
				zap.String("server", c.config.Name),
				zap.String("url", c.config.URL),
				zap.Any("panic", r))

			if c.upstreamLogger != nil {
				c.upstreamLogger.Error("Panic during list resources",
					zap.Any("panic", r))
			}

			// Convert panic to a descriptive error
			err = fmt.Errorf("list resources crashed - server may not be properly responding to resource listing. URL: %s, panic: %v", c.config.URL, r)
			result = nil
		}
	}()

	return c.client.ListResources(ctx, request)
}

// isConnectionError checks if an error is related to connection issues
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
func (c *Client) handleOAuthFlow(ctx context.Context, originalErr error) error {
	c.logger.Info("Starting OAuth authentication flow",
		zap.String("server", c.config.Name),
		zap.Error(originalErr))

	// Transition to authenticating state
	c.stateManager.TransitionTo(StateAuthenticating)

	// Check if this is a standard OAuth authorization required error
	if client.IsOAuthAuthorizationRequiredError(originalErr) {
		c.logger.Info("Standard OAuth authorization required - starting OAuth flow",
			zap.String("server", c.config.Name))
		if c.upstreamLogger != nil {
			c.upstreamLogger.Info("OAuth authentication flow starting via mcp-go library")
		}

		// Get the OAuth handler from the error
		oauthHandler := client.GetOAuthHandler(originalErr)
		if oauthHandler == nil {
			err := fmt.Errorf("OAuth handler not available in authorization error")
			c.stateManager.SetError(err)
			return err
		}

		// Get the callback server for this upstream connection
		callbackServer, exists := oauth.GetGlobalCallbackManager().GetCallbackServer(c.config.Name)
		if !exists {
			err := fmt.Errorf("OAuth callback server not found for server %s", c.config.Name)
			c.stateManager.SetError(err)
			return err
		}

		// Perform the OAuth authorization flow
		if err := c.performOAuthAuthorization(ctx, oauthHandler, callbackServer); err != nil {
			c.stateManager.SetError(fmt.Errorf("OAuth authorization failed: %w", err))
			return fmt.Errorf("OAuth authorization failed: %w", err)
		}

		c.logger.Info("OAuth authentication completed successfully - retrying connection with token",
			zap.String("server", c.config.Name))

		// Step 9: After OAuth completes, retry Start() and Initialize() with the same client that now has the token
		if err := c.retryOAuthConnection(ctx); err != nil {
			c.stateManager.SetError(fmt.Errorf("OAuth connection retry failed: %w", err))
			return fmt.Errorf("OAuth connection retry failed: %w", err)
		}

		c.logger.Info("OAuth connection retry successful",
			zap.String("server", c.config.Name))
		return nil
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

// retryOAuthConnection retries the connection with the existing OAuth client that now has the token
func (c *Client) retryOAuthConnection(ctx context.Context) error {
	c.logger.Info("Retrying connection with OAuth token",
		zap.String("server", c.config.Name))

	// Set connection timeout for retry
	timeout := c.getConnectionTimeout()
	connectCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Step 1: Retry Start() - the OAuth client should now have the token
	c.logger.Debug("Retrying MCP client start with OAuth token",
		zap.String("server", c.config.Name))

	if err := c.safeClientStart(connectCtx, authTypeOAuth); err != nil {
		c.logger.Error("OAuth client start retry failed",
			zap.String("server", c.config.Name),
			zap.Error(err))
		return fmt.Errorf("failed to start OAuth client after authentication: %w", err)
	}

	c.logger.Debug("OAuth client start retry succeeded",
		zap.String("server", c.config.Name))

	// Step 2: Transition to discovering state
	c.stateManager.TransitionTo(StateDiscovering)

	// Step 3: Retry Initialize()
	c.logger.Debug("Retrying MCP client initialization with OAuth token",
		zap.String("server", c.config.Name))

	initRequest := mcp.InitializeRequest{}
	initRequest.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initRequest.Params.ClientInfo = mcp.Implementation{
		Name:    "mcpproxy-go",
		Version: "1.0.0",
	}
	initRequest.Params.Capabilities = mcp.ClientCapabilities{}

	serverInfo, err := c.safeClientInitialize(connectCtx, &initRequest, authTypeOAuth)
	if err != nil {
		c.logger.Error("OAuth client initialization retry failed",
			zap.String("server", c.config.Name),
			zap.Error(err))
		return fmt.Errorf("failed to initialize OAuth client after authentication: %w", err)
	}

	c.logger.Debug("OAuth client initialization retry succeeded",
		zap.String("server", c.config.Name))

	// Step 4: Store server info and transition to ready state
	c.serverInfo = serverInfo
	c.stateManager.SetServerInfo(serverInfo.ServerInfo.Name, serverInfo.ServerInfo.Version)
	c.stateManager.TransitionTo(StateReady)

	c.logger.Info("Successfully connected to upstream MCP server with OAuth",
		zap.String("server_name", serverInfo.ServerInfo.Name),
		zap.String("server_version", serverInfo.ServerInfo.Version),
		zap.String("auth_type", authTypeOAuth))

	if c.upstreamLogger != nil {
		c.upstreamLogger.Info("Connected successfully with OAuth",
			zap.String("server_name", serverInfo.ServerInfo.Name),
			zap.String("server_version", serverInfo.ServerInfo.Version),
			zap.String("auth_type", authTypeOAuth),
			zap.String("protocol_version", serverInfo.ProtocolVersion))
	}

	return nil
}

// performOAuthAuthorization performs the complete OAuth authorization flow
func (c *Client) performOAuthAuthorization(ctx context.Context, oauthHandler *mcptransport.OAuthHandler, callbackServer *oauth.CallbackServer) error {
	c.logger.Info("Starting OAuth authorization flow",
		zap.String("server", c.config.Name),
		zap.String("redirect_uri", callbackServer.RedirectURI))

	// Step 1: Dynamic Client Registration
	c.logger.Info("Performing Dynamic Client Registration",
		zap.String("server", c.config.Name))

	if err := oauthHandler.RegisterClient(ctx, "mcpproxy-go"); err != nil {
		c.logger.Error("Dynamic Client Registration failed",
			zap.String("server", c.config.Name),
			zap.Error(err))
		return fmt.Errorf("Dynamic Client Registration failed: %w", err)
	}

	c.logger.Info("Dynamic Client Registration successful",
		zap.String("server", c.config.Name))

	// Step 2: Generate PKCE parameters
	codeVerifier, err := client.GenerateCodeVerifier()
	if err != nil {
		return fmt.Errorf("failed to generate PKCE code verifier: %w", err)
	}
	codeChallenge := client.GenerateCodeChallenge(codeVerifier)

	// Step 3: Generate state parameter for CSRF protection
	state, err := client.GenerateState()
	if err != nil {
		return fmt.Errorf("failed to generate state parameter: %w", err)
	}

	// Step 4: Get authorization URL
	authURL, err := oauthHandler.GetAuthorizationURL(ctx, state, codeChallenge)
	if err != nil {
		return fmt.Errorf("failed to get authorization URL: %w", err)
	}

	c.logger.Info("Opening browser for OAuth authentication",
		zap.String("server", c.config.Name),
		zap.String("auth_url", authURL))

	// Step 5: Open browser to authorization URL
	if err := c.openBrowser(authURL); err != nil {
		c.logger.Warn("Failed to open browser automatically",
			zap.String("server", c.config.Name),
			zap.Error(err))
		c.logger.Info("Please open the following URL in your browser manually",
			zap.String("server", c.config.Name),
			zap.String("auth_url", authURL))
	}

	// Step 6: Wait for authorization callback
	c.logger.Info("Waiting for OAuth authorization callback",
		zap.String("server", c.config.Name))

	select {
	case authParams := <-callbackServer.CallbackChan:
		c.logger.Info("OAuth callback received",
			zap.String("server", c.config.Name),
			zap.Any("params", authParams))

		// Step 7: Validate state parameter to prevent CSRF attacks
		if authParams["state"] != state {
			return fmt.Errorf("OAuth state mismatch - possible CSRF attack. Expected: %s, Got: %s",
				state, authParams["state"])
		}

		// Check for error in callback
		if errParam := authParams["error"]; errParam != "" {
			errorDesc := authParams["error_description"]
			if errorDesc == "" {
				errorDesc = "No description provided"
			}
			return fmt.Errorf("OAuth authorization failed: %s - %s", errParam, errorDesc)
		}

		// Get authorization code
		code := authParams["code"]
		if code == "" {
			return fmt.Errorf("no authorization code received in OAuth callback")
		}

		// Step 8: Exchange authorization code for access token
		c.logger.Info("Exchanging authorization code for access token",
			zap.String("server", c.config.Name))

		if err := oauthHandler.ProcessAuthorizationResponse(ctx, code, state, codeVerifier); err != nil {
			return fmt.Errorf("failed to exchange authorization code for token: %w", err)
		}

		c.logger.Info("OAuth token exchange successful",
			zap.String("server", c.config.Name))
		return nil

	case <-time.After(5 * time.Minute):
		return fmt.Errorf("OAuth authorization timeout - user did not complete authorization within 5 minutes")

	case <-ctx.Done():
		return fmt.Errorf("OAuth authorization cancelled: %w", ctx.Err())
	}
}

// openBrowser opens the default browser to the specified URL
func (c *Client) openBrowser(url string) error {
	var cmd string
	var args []string

	switch runtime.GOOS {
	case osWindows:
		cmd = "cmd"
		args = []string{"/c", "start"}
	case osDarwin:
		cmd = "open"
	default: // "linux", "freebsd", "openbsd", "netbsd"
		cmd = "xdg-open"
	}
	args = append(args, url)

	if err := exec.Command(cmd, args...).Start(); err != nil {
		return fmt.Errorf("failed to open browser: %w", err)
	}

	return nil
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
