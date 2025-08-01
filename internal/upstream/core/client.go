package core

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"

	"mcpproxy-go/internal/config"
	"mcpproxy-go/internal/logs"
	"mcpproxy-go/internal/secureenv"
	"mcpproxy-go/internal/transport"
	"mcpproxy-go/internal/upstream/types"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"go.uber.org/zap"
)

// CoreClient implements basic MCP client functionality without state management
type CoreClient struct {
	id     string
	config *config.ServerConfig
	logger *zap.Logger

	// Upstream server specific logger for debugging
	upstreamLogger *zap.Logger

	// MCP client and server info
	client     *client.Client
	serverInfo *mcp.InitializeResult

	// Environment manager for stdio transport
	envManager *secureenv.Manager

	// Connection state protection
	mu        sync.RWMutex
	connected bool

	// Transport type and stderr access (for stdio)
	transportType string
	stderr        io.Reader
}

// NewCoreClient creates a new core MCP client
func NewCoreClient(id string, serverConfig *config.ServerConfig, logger *zap.Logger, logConfig *config.LogConfig, globalConfig *config.Config) (*CoreClient, error) {
	return NewCoreClientWithOptions(id, serverConfig, logger, logConfig, globalConfig, false)
}

// NewCoreClientWithOptions creates a new core MCP client with additional options
func NewCoreClientWithOptions(id string, serverConfig *config.ServerConfig, logger *zap.Logger, logConfig *config.LogConfig, globalConfig *config.Config, cliDebugMode bool) (*CoreClient, error) {
	c := &CoreClient{
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

	// Add server-specific environment variables
	if len(serverConfig.Env) > 0 {
		serverEnvConfig := *envConfig
		if serverEnvConfig.CustomVars == nil {
			serverEnvConfig.CustomVars = make(map[string]string)
		} else {
			customVars := make(map[string]string)
			for k, v := range serverEnvConfig.CustomVars {
				customVars[k] = v
			}
			serverEnvConfig.CustomVars = customVars
		}

		for k, v := range serverConfig.Env {
			serverEnvConfig.CustomVars[k] = v
		}
		envConfig = &serverEnvConfig
	}

	c.envManager = secureenv.NewManager(envConfig)

	// Create upstream server logger if provided
	if logConfig != nil {
		var upstreamLogger *zap.Logger
		var err error

		// Use CLI logger for debugging or regular logger for daemon mode
		if cliDebugMode {
			upstreamLogger, err = logs.CreateCLIUpstreamServerLogger(logConfig, serverConfig.Name)
		} else {
			upstreamLogger, err = logs.CreateUpstreamServerLogger(logConfig, serverConfig.Name)
		}

		if err != nil {
			logger.Warn("Failed to create upstream server logger",
				zap.String("server", serverConfig.Name),
				zap.Bool("cli_debug_mode", cliDebugMode),
				zap.Error(err))
		} else {
			c.upstreamLogger = upstreamLogger
			if logConfig.Level == "trace" && cliDebugMode {
				c.upstreamLogger.Debug("TRACE LEVEL ENABLED - All JSON-RPC frames will be logged to console",
					zap.String("server", serverConfig.Name))
			}
		}
	}

	return c, nil
}

// Connect establishes connection to the upstream server
func (c *CoreClient) Connect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.connected {
		return fmt.Errorf("client already connected")
	}

	c.logger.Info("Connecting to upstream MCP server",
		zap.String("server", c.config.Name),
		zap.String("url", c.config.URL),
		zap.String("command", c.config.Command),
		zap.String("protocol", c.config.Protocol))

	// Determine transport type
	c.transportType = transport.DetermineTransportType(c.config)

	// Create and connect client based on transport type
	var err error
	switch c.transportType {
	case transport.TransportStdio:
		err = c.connectStdio(ctx)
	case transport.TransportHTTP, transport.TransportStreamableHTTP, transport.TransportSSE:
		err = c.connectHTTP(ctx)
	default:
		return fmt.Errorf("unsupported transport type: %s", c.transportType)
	}

	if err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}

	// Initialize the MCP connection
	if err := c.initialize(ctx); err != nil {
		c.client.Close()
		c.client = nil
		return fmt.Errorf("failed to initialize: %w", err)
	}

	c.connected = true
	c.logger.Info("Successfully connected to upstream MCP server",
		zap.String("server", c.config.Name),
		zap.String("transport", c.transportType))

	return nil
}

// connectStdio establishes stdio transport connection
func (c *CoreClient) connectStdio(ctx context.Context) error {
	if c.config.Command == "" {
		return fmt.Errorf("no command specified for stdio transport")
	}

	stdioConfig := &transport.StdioTransportConfig{
		Command:    c.config.Command,
		Args:       c.config.Args,
		Env:        c.config.Env,
		EnvManager: c.envManager,
	}

	result, err := transport.CreateStdioClient(stdioConfig)
	if err != nil {
		return fmt.Errorf("failed to create stdio client: %w", err)
	}

	c.client = result.Client
	c.stderr = result.Stderr

	// Start the client
	if err := c.client.Start(ctx); err != nil {
		return fmt.Errorf("failed to start stdio client: %w", err)
	}

	return nil
}

// connectHTTP establishes HTTP/SSE transport connection with auth fallback
func (c *CoreClient) connectHTTP(ctx context.Context) error {
	// Try authentication strategies in order: headers -> no-auth -> OAuth
	authStrategies := []func(context.Context) error{
		c.tryHeadersAuth,
		c.tryNoAuth,
		c.tryOAuthAuth,
	}

	var lastErr error
	for i, authFunc := range authStrategies {
		if err := authFunc(ctx); err != nil {
			lastErr = err
			c.logger.Debug("Auth strategy failed",
				zap.Int("strategy_index", i),
				zap.Error(err))

			// For configuration errors (like no headers), always try next strategy
			if c.isConfigError(err) {
				continue
			}

			// If it's not an auth error, don't try fallback
			if !c.isAuthError(err) {
				return err
			}
			continue
		}
		return nil
	}

	return fmt.Errorf("all authentication strategies failed, last error: %w", lastErr)
}

// tryHeadersAuth attempts authentication using configured headers
func (c *CoreClient) tryHeadersAuth(ctx context.Context) error {
	if len(c.config.Headers) == 0 {
		return fmt.Errorf("no headers configured")
	}

	httpConfig := transport.CreateHTTPTransportConfig(c.config, nil)
	httpClient, err := transport.CreateHTTPClient(httpConfig)
	if err != nil {
		return fmt.Errorf("failed to create HTTP client with headers: %w", err)
	}

	c.client = httpClient
	return c.client.Start(ctx)
}

// tryNoAuth attempts connection without authentication
func (c *CoreClient) tryNoAuth(ctx context.Context) error {
	// Create config without headers
	configNoAuth := *c.config
	configNoAuth.Headers = nil

	httpConfig := transport.CreateHTTPTransportConfig(&configNoAuth, nil)
	httpClient, err := transport.CreateHTTPClient(httpConfig)
	if err != nil {
		return fmt.Errorf("failed to create HTTP client without auth: %w", err)
	}

	c.client = httpClient
	return c.client.Start(ctx)
}

// tryOAuthAuth attempts OAuth authentication
func (c *CoreClient) tryOAuthAuth(_ context.Context) error {
	// This will be implemented in the auth module
	// For now, return error
	return fmt.Errorf("OAuth authentication not yet implemented in core client")
}

// isAuthError checks if error indicates authentication failure
func (c *CoreClient) isAuthError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return containsAny(errStr, []string{
		"401", "Unauthorized",
		"403", "Forbidden",
		"invalid_token", "token",
		"authentication", "auth",
	})
}

// isConfigError checks if error indicates a configuration issue that should trigger fallback
func (c *CoreClient) isConfigError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return containsAny(errStr, []string{
		"no headers configured",
		"no command specified",
		"OAuth authentication not yet implemented",
	})
}

// initialize performs MCP initialization handshake
func (c *CoreClient) initialize(ctx context.Context) error {
	initRequest := mcp.InitializeRequest{}
	initRequest.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initRequest.Params.ClientInfo = mcp.Implementation{
		Name:    "mcpproxy-go",
		Version: "1.0.0",
	}
	initRequest.Params.Capabilities = mcp.ClientCapabilities{}

	// Log request for trace debugging - use main logger for CLI debug mode
	if reqBytes, err := json.MarshalIndent(initRequest, "", "  "); err == nil {
		c.logger.Debug("🔍 JSON-RPC INITIALIZE REQUEST",
			zap.String("method", "initialize"),
			zap.String("formatted_json", string(reqBytes)))
	}

	serverInfo, err := c.client.Initialize(ctx, initRequest)
	if err != nil {
		return fmt.Errorf("MCP initialize failed: %w", err)
	}

	// Log response for trace debugging - use main logger for CLI debug mode
	if respBytes, err := json.MarshalIndent(serverInfo, "", "  "); err == nil {
		c.logger.Debug("🔍 JSON-RPC INITIALIZE RESPONSE",
			zap.String("method", "initialize"),
			zap.String("formatted_json", string(respBytes)))
	}

	c.serverInfo = serverInfo
	c.logger.Info("MCP initialization successful",
		zap.String("server_name", serverInfo.ServerInfo.Name),
		zap.String("server_version", serverInfo.ServerInfo.Version))

	return nil
}

// Disconnect closes the connection
func (c *CoreClient) Disconnect() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.connected || c.client == nil {
		return nil
	}

	c.logger.Info("Disconnecting from upstream MCP server")
	c.client.Close()
	c.client = nil
	c.serverInfo = nil
	c.connected = false

	return nil
}

// IsConnected returns whether the client is currently connected
func (c *CoreClient) IsConnected() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.connected
}

// ListTools retrieves available tools from the upstream server
func (c *CoreClient) ListTools(ctx context.Context) ([]*config.ToolMetadata, error) {
	c.mu.RLock()
	client := c.client
	serverInfo := c.serverInfo
	c.mu.RUnlock()

	if !c.IsConnected() || client == nil {
		return nil, fmt.Errorf("client not connected")
	}

	// Check if server supports tools
	if serverInfo.Capabilities.Tools == nil {
		c.logger.Debug("Server does not support tools")
		return nil, nil
	}

	toolsRequest := mcp.ListToolsRequest{}

	// Log request for trace debugging - use main logger for CLI debug mode
	if reqBytes, err := json.MarshalIndent(toolsRequest, "", "  "); err == nil {
		c.logger.Debug("🔍 JSON-RPC LISTTOOLS REQUEST",
			zap.String("method", "tools/list"),
			zap.String("formatted_json", string(reqBytes)))
	}

	toolsResult, err := client.ListTools(ctx, toolsRequest)
	if err != nil {
		return nil, fmt.Errorf("ListTools failed: %w", err)
	}

	// Log response for trace debugging - use main logger for CLI debug mode
	if respBytes, err := json.MarshalIndent(toolsResult, "", "  "); err == nil {
		c.logger.Debug("🔍 JSON-RPC LISTTOOLS RESPONSE",
			zap.String("method", "tools/list"),
			zap.String("formatted_json", string(respBytes)))
	}

	// Convert to ToolMetadata
	var tools []*config.ToolMetadata
	for i := range toolsResult.Tools {
		tool := &toolsResult.Tools[i]
		// Convert input schema to JSON string
		var paramsJSON string
		if schemaBytes, err := json.Marshal(tool.InputSchema); err == nil {
			paramsJSON = string(schemaBytes)
		}

		toolMeta := &config.ToolMetadata{
			ServerName:  c.config.Name,
			Name:        tool.Name,
			Description: tool.Description,
			ParamsJSON:  paramsJSON,
		}
		tools = append(tools, toolMeta)
	}

	c.logger.Info("Successfully listed tools",
		zap.String("server", c.config.Name),
		zap.Int("tool_count", len(tools)))

	return tools, nil
}

// CallTool executes a tool on the upstream server
func (c *CoreClient) CallTool(ctx context.Context, toolName string, args map[string]interface{}) (*mcp.CallToolResult, error) {
	c.mu.RLock()
	client := c.client
	c.mu.RUnlock()

	if !c.IsConnected() || client == nil {
		return nil, fmt.Errorf("client not connected")
	}

	request := mcp.CallToolRequest{}
	request.Params.Name = toolName
	request.Params.Arguments = args

	// Log request for trace debugging
	if c.upstreamLogger != nil {
		if reqBytes, err := json.MarshalIndent(request, "", "  "); err == nil {
			c.upstreamLogger.Debug("TRACE JSON-RPC CALLTOOL REQUEST",
				zap.String("method", "tools/call"),
				zap.String("tool", toolName),
				zap.String("formatted_json", string(reqBytes)))
		}
	}

	result, err := client.CallTool(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("CallTool failed for '%s': %w", toolName, err)
	}

	// Log response for trace debugging
	if c.upstreamLogger != nil {
		if respBytes, err := json.MarshalIndent(result, "", "  "); err == nil {
			c.upstreamLogger.Debug("TRACE JSON-RPC CALLTOOL RESPONSE",
				zap.String("method", "tools/call"),
				zap.String("tool", toolName),
				zap.String("formatted_json", string(respBytes)))
		}
	}

	return result, nil
}

// GetConnectionInfo returns basic connection information
func (c *CoreClient) GetConnectionInfo() types.ConnectionInfo {
	c.mu.RLock()
	defer c.mu.RUnlock()

	state := types.StateDisconnected
	if c.connected {
		state = types.StateReady
	}

	return types.ConnectionInfo{
		State:      state,
		ServerName: c.getServerName(),
	}
}

// GetServerInfo returns server information from initialization
func (c *CoreClient) GetServerInfo() *mcp.InitializeResult {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.serverInfo
}

// GetTransportType returns the transport type being used
func (c *CoreClient) GetTransportType() string {
	return c.transportType
}

// GetStderr returns stderr reader for stdio transport
func (c *CoreClient) GetStderr() io.Reader {
	return c.stderr
}

// GetEnvManager returns the environment manager for testing purposes
func (c *CoreClient) GetEnvManager() interface{} {
	return c.envManager
}

// Helper methods

func (c *CoreClient) getServerName() string {
	if c.serverInfo != nil {
		return c.serverInfo.ServerInfo.Name
	}
	return c.config.Name
}

func containsAny(str string, substrs []string) bool {
	for _, substr := range substrs {
		if substr != "" && len(str) >= len(substr) {
			for i := 0; i <= len(str)-len(substr); i++ {
				if str[i:i+len(substr)] == substr {
					return true
				}
			}
		}
	}
	return false
}
