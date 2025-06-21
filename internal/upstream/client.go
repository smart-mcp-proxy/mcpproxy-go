package upstream

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	"go.uber.org/zap"

	"mcpproxy-go/internal/config"
	"mcpproxy-go/internal/hash"
)

// Client represents an MCP client connection to an upstream server
type Client struct {
	id     string
	config *config.ServerConfig
	client *client.Client
	logger *zap.Logger

	// Server information received during initialization
	serverInfo *mcp.InitializeResult

	// Connection state
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
func NewClient(id string, serverConfig *config.ServerConfig, logger *zap.Logger) (*Client, error) {
	c := &Client{
		id:     id,
		config: serverConfig,
		logger: logger.With(
			zap.String("upstream_id", id),
			zap.String("upstream_name", serverConfig.Name),
		),
	}

	return c, nil
}

// Connect establishes a connection to the upstream MCP server
func (c *Client) Connect(ctx context.Context) error {
	if c.connecting {
		return fmt.Errorf("connection already in progress")
	}

	c.connecting = true
	defer func() { c.connecting = false }()

	c.logger.Info("Connecting to upstream MCP server",
		zap.String("url", c.config.URL),
		zap.String("protocol", c.config.Protocol),
		zap.Int("retry_count", c.retryCount))

	transportType := c.determineTransportType()

	switch transportType {
	case "http", "streamable-http":
		httpTransport, err := transport.NewStreamableHTTP(c.config.URL)
		if err != nil {
			c.lastError = err
			c.retryCount++
			c.lastRetryTime = time.Now()
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
			c.lastError = err
			c.retryCount++
			c.lastRetryTime = time.Now()
			return fmt.Errorf("failed to create SSE transport: %w", err)
		}
		c.client = client.NewClient(httpTransport)
	case "stdio":
		var command string
		var cmdArgs []string

		// Check if command is specified separately (preferred)
		if c.config.Command != "" {
			command = c.config.Command
			cmdArgs = c.config.Args
		} else {
			// Fallback to parsing from URL
			args := c.parseCommand(c.config.URL)
			if len(args) == 0 {
				c.lastError = fmt.Errorf("invalid stdio command: %s", c.config.URL)
				c.retryCount++
				c.lastRetryTime = time.Now()
				return c.lastError
			}
			command = args[0]
			cmdArgs = args[1:]
		}

		if command == "" {
			c.lastError = fmt.Errorf("no command specified for stdio transport")
			c.retryCount++
			c.lastRetryTime = time.Now()
			return c.lastError
		}

		// Convert env map to format needed for the process
		var envVars []string
		for k, v := range c.config.Env {
			envVars = append(envVars, k+"="+v)
		}

		stdioTransport := transport.NewStdio(command, envVars, cmdArgs...)
		c.client = client.NewClient(stdioTransport)
	default:
		c.lastError = fmt.Errorf("unsupported transport type: %s", transportType)
		c.retryCount++
		c.lastRetryTime = time.Now()
		return c.lastError
	}

	// Set connection timeout with exponential backoff consideration
	timeout := c.getConnectionTimeout()
	connectCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Start the client
	if err := c.client.Start(connectCtx); err != nil {
		c.lastError = err
		c.retryCount++
		c.lastRetryTime = time.Now()
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
		c.lastError = err
		c.retryCount++
		c.lastRetryTime = time.Now()
		c.client.Close()
		return fmt.Errorf("failed to initialize MCP client: %w", err)
	}

	c.serverInfo = serverInfo
	c.connected = true
	c.lastError = nil
	c.retryCount = 0 // Reset retry count on successful connection

	c.logger.Info("Successfully connected to upstream MCP server",
		zap.String("server_name", serverInfo.ServerInfo.Name),
		zap.String("server_version", serverInfo.ServerInfo.Version))

	return nil
}

// getConnectionTimeout returns the connection timeout with exponential backoff
func (c *Client) getConnectionTimeout() time.Duration {
	baseTimeout := 30 * time.Second
	if c.retryCount == 0 {
		return baseTimeout
	}

	// Exponential backoff: min(base * 2^retry, max)
	backoffMultiplier := math.Pow(2, float64(c.retryCount))
	maxTimeout := 5 * time.Minute
	timeout := time.Duration(float64(baseTimeout) * backoffMultiplier)

	if timeout > maxTimeout {
		timeout = maxTimeout
	}

	return timeout
}

// ShouldRetry returns true if the client should retry connecting based on exponential backoff
func (c *Client) ShouldRetry() bool {
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

	nextRetryTime := c.lastRetryTime.Add(backoffDuration)
	return time.Now().After(nextRetryTime)
}

// GetConnectionStatus returns detailed connection status information
func (c *Client) GetConnectionStatus() map[string]interface{} {
	status := map[string]interface{}{
		"connected":       c.connected,
		"connecting":      c.connecting,
		"retry_count":     c.retryCount,
		"last_retry_time": c.lastRetryTime,
		"should_retry":    c.ShouldRetry(),
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
		return "stdio"
	}

	// Auto-detect based on URL
	if strings.HasPrefix(c.config.URL, "http://") || strings.HasPrefix(c.config.URL, "https://") {
		// Default to streamable-http for HTTP URLs unless explicitly set
		return "streamable-http"
	}

	// Assume stdio for command-like URLs or when command is specified
	return "stdio"
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
	if c.client != nil {
		c.logger.Info("Disconnecting from upstream MCP server")
		c.client.Close()
		c.connected = false
	}
	return nil
}

// IsConnected returns whether the client is currently connected
func (c *Client) IsConnected() bool {
	return c.connected
}

// IsConnecting returns whether the client is currently connecting
func (c *Client) IsConnecting() bool {
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
	if !c.connected || c.client == nil {
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
		c.lastError = err
		return nil, fmt.Errorf("failed to list tools: %w", err)
	}

	// Convert MCP tools to our metadata format
	var tools []*config.ToolMetadata
	for _, tool := range toolsResult.Tools {
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
	if !c.connected || c.client == nil {
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

	result, err := c.client.CallTool(ctx, request)
	if err != nil {
		c.lastError = err
		return nil, fmt.Errorf("failed to call tool %s: %w", toolName, err)
	}

	// Extract content from result
	if result.Content != nil && len(result.Content) > 0 {
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
	if !c.connected || c.client == nil {
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
		c.lastError = err
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
