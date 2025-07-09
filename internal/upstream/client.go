package upstream

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	"go.uber.org/zap"

	"mcpproxy-go/internal/config"
	"mcpproxy-go/internal/hash"
	"mcpproxy-go/internal/logs"
	"mcpproxy-go/internal/secureenv"
)

const (
	transportHTTP           = "http"
	transportStreamableHTTP = "streamable-http"
	transportStdio          = "stdio"
	osWindows               = "windows"

	// OAuth flow types
	oauthFlowDeviceCode = "device_code"
	oauthFlowAuthCode   = "authorization_code"
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

	// Connection state (protected by mutex)
	mu            sync.RWMutex
	connected     bool
	lastError     error
	retryCount    int
	lastRetryTime time.Time
	connecting    bool
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
	case "sse":
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
			c.logger.Info("OAuth authorization required, initiating OAuth flow")
			if c.upstreamLogger != nil {
				c.upstreamLogger.Info("OAuth authorization required")
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
			return fmt.Errorf("failed to initialize MCP client: %w", err)
		}
	}

	c.serverInfo = serverInfo
	c.mu.Lock()
	c.connected = true
	c.lastError = nil
	c.retryCount = 0 // Reset retry count on successful connection
	c.mu.Unlock()

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
	c.mu.RUnlock()

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
			ParamsJSON:  "", // Will be filled from InputSchema if needed
		}

		// Convert InputSchema to JSON string if present
		if schemaBytes, err := tool.InputSchema.MarshalJSON(); err == nil {
			metadata.ParamsJSON = string(schemaBytes)
		}

		tools = append(tools, metadata)
	}

	c.logger.Debug("Listed tools from upstream server", zap.Int("count", len(tools)))
	return tools, nil
}

// CallTool calls a specific tool on the upstream server
func (c *Client) CallTool(ctx context.Context, toolName string, args map[string]interface{}) (interface{}, error) {
	c.mu.RLock()
	connected := c.connected
	client := c.client
	serverInfo := c.serverInfo
	c.mu.RUnlock()

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

// handleOAuthFlow handles the OAuth authorization flow
func (c *Client) handleOAuthFlow(ctx context.Context) error {
	if c.config.OAuth == nil {
		return fmt.Errorf("OAuth configuration not found")
	}

	// Determine which OAuth flow to use
	flowType := c.config.OAuth.FlowType
	if flowType == "" {
		// Default to device flow for headless/remote scenarios
		flowType = oauthFlowDeviceCode
	}

	switch flowType {
	case oauthFlowAuthCode:
		return c.handleAuthorizationCodeFlow(ctx)
	case oauthFlowDeviceCode:
		return c.handleDeviceCodeFlow(ctx)
	default:
		return fmt.Errorf("unsupported OAuth flow type: %s", flowType)
	}
}

// handleAuthorizationCodeFlow handles the OAuth Authorization Code flow
func (c *Client) handleAuthorizationCodeFlow(_ context.Context) error {
	c.logger.Info("Starting OAuth Authorization Code flow")

	// This flow requires user interaction on the same device
	// For now, return an error indicating this is not supported in headless mode
	return fmt.Errorf("Authorization Code flow requires user interaction on the same device. Use %s flow for remote/headless scenarios", oauthFlowDeviceCode)
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

// showDeviceCodeNotification shows a notification to the user with the device code
func (c *Client) showDeviceCodeNotification(deviceResp *DeviceCodeResponse) {
	// TODO: Implement system tray notification
	// For now, just log the information
	c.logger.Info("Please authorize this device:",
		zap.String("url", deviceResp.VerificationURI),
		zap.String("code", deviceResp.UserCode))
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
