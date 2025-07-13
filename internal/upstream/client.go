package upstream

import (
	"context"
	"encoding/json"
	"fmt"
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
	switch transportType {
	case transport.TransportHTTP, transport.TransportStreamableHTTP:
		c.client, err = c.createHTTPClient()
	case transport.TransportSSE:
		c.client, err = c.createSSEClient()
	case transport.TransportStdio:
		c.client, err = c.createStdioClient()
	default:
		err = fmt.Errorf("unsupported transport type: %s", transportType)
	}

	if err != nil {
		c.stateManager.SetError(err)
		return fmt.Errorf("failed to create client: %w", err)
	}

	// Set connection timeout with exponential backoff consideration
	timeout := c.getConnectionTimeout()
	connectCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Start the client (this may trigger OAuth flow)
	if err := c.client.Start(connectCtx); err != nil {
		// Check if this is an OAuth authorization required error
		if client.IsOAuthAuthorizationRequiredError(err) {
			c.stateManager.TransitionTo(StateAuthenticating)
			c.logger.Info("OAuth authentication required, starting authorization flow")
			if c.upstreamLogger != nil {
				c.upstreamLogger.Info("OAuth authentication required")
			}

			// Handle OAuth flow here (for now just log and error)
			c.stateManager.SetError(fmt.Errorf("OAuth flow not yet implemented: %w", err))
			return fmt.Errorf("OAuth authentication required but flow not implemented: %w", err)
		}

		c.stateManager.SetError(err)
		c.logger.Error("Failed to start MCP client", zap.Error(err))
		if c.upstreamLogger != nil {
			c.upstreamLogger.Error("Client start failed", zap.Error(err))
		}
		return fmt.Errorf("failed to start MCP client: %w", err)
	}

	// Transition to discovering state for tool discovery
	c.stateManager.TransitionTo(StateDiscovering)

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
		c.stateManager.SetError(err)
		c.logger.Error("Failed to initialize MCP client", zap.Error(err))
		if c.upstreamLogger != nil {
			c.upstreamLogger.Error("Initialize failed", zap.Error(err))
		}
		c.client.Close()
		return fmt.Errorf("failed to initialize MCP client: %w", err)
	}

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
	// Create OAuth config if needed
	var oauthConfig *client.OAuthConfig
	if oauth.ShouldUseOAuth(c.config) {
		envConfig := oauth.GetOAuthConfigFromEnv(c.config.Name)
		mergedConfig := oauth.MergeOAuthConfig(c.config, envConfig)
		oauthConfig = oauth.CreateOAuthConfig(c.config, mergedConfig)
	}

	// Create HTTP transport config
	httpConfig := transport.CreateHTTPTransportConfig(c.config, oauthConfig)

	// Create HTTP client
	return transport.CreateHTTPClient(httpConfig)
}

// createSSEClient creates an SSE client with optional OAuth support
func (c *Client) createSSEClient() (*client.Client, error) {
	// Create OAuth config if needed
	var oauthConfig *client.OAuthConfig
	if oauth.ShouldUseOAuth(c.config) {
		envConfig := oauth.GetOAuthConfigFromEnv(c.config.Name)
		mergedConfig := oauth.MergeOAuthConfig(c.config, envConfig)
		oauthConfig = oauth.CreateOAuthConfig(c.config, mergedConfig)
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
	errStr := err.Error()
	return strings.Contains(errStr, "broken pipe") ||
		strings.Contains(errStr, "connection reset") ||
		strings.Contains(errStr, "EOF") ||
		strings.Contains(errStr, "connection refused") ||
		strings.Contains(errStr, "transport error")
}
