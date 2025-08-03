package core

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"mcpproxy-go/internal/config"
	"mcpproxy-go/internal/logs"
	"mcpproxy-go/internal/secureenv"
	"mcpproxy-go/internal/transport"
	"mcpproxy-go/internal/upstream/types"

	uptransport "github.com/mark3labs/mcp-go/client/transport"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"go.uber.org/zap"
)

// Client implements basic MCP client functionality without state management
type Client struct {
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

	// Cached tools list from successful immediate call
	cachedTools []mcp.Tool

	// Stderr monitoring
	stderrMonitoringCtx    context.Context
	stderrMonitoringCancel context.CancelFunc
	stderrMonitoringWG     sync.WaitGroup

	// Process monitoring (for stdio transport)
	processCmd           *exec.Cmd
	processMonitorCtx    context.Context
	processMonitorCancel context.CancelFunc
	processMonitorWG     sync.WaitGroup
}

// NewClient creates a new core MCP client
func NewClient(id string, serverConfig *config.ServerConfig, logger *zap.Logger, logConfig *config.LogConfig, globalConfig *config.Config) (*Client, error) {
	return NewClientWithOptions(id, serverConfig, logger, logConfig, globalConfig, false)
}

// NewClientWithOptions creates a new core MCP client with additional options
func NewClientWithOptions(id string, serverConfig *config.ServerConfig, logger *zap.Logger, logConfig *config.LogConfig, globalConfig *config.Config, cliDebugMode bool) (*Client, error) {
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
func (c *Client) Connect(ctx context.Context) error {
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

	// Log to server-specific log file as well
	if c.upstreamLogger != nil {
		c.upstreamLogger.Info("Starting connection attempt",
			zap.String("transport", c.transportType),
			zap.String("url", c.config.URL),
			zap.String("command", c.config.Command),
			zap.String("protocol", c.config.Protocol))
	}

	// Debug: Show transport type determination
	c.logger.Debug("🔍 Transport Type Determination",
		zap.String("server", c.config.Name),
		zap.String("command", c.config.Command),
		zap.String("url", c.config.URL),
		zap.String("protocol", c.config.Protocol),
		zap.String("determined_transport", c.transportType))

	// Create and connect client based on transport type
	var err error
	switch c.transportType {
	case transport.TransportStdio:
		c.logger.Debug("📡 Using STDIO transport")
		err = c.connectStdio(ctx)
	case transport.TransportHTTP, transport.TransportStreamableHTTP, transport.TransportSSE:
		c.logger.Debug("🌐 Using HTTP/SSE transport")
		err = c.connectHTTP(ctx)
	default:
		return fmt.Errorf("unsupported transport type: %s", c.transportType)
	}

	if err != nil {
		// Log connection failure to server-specific log
		if c.upstreamLogger != nil {
			c.upstreamLogger.Error("Connection failed",
				zap.String("transport", c.transportType),
				zap.Error(err))
		}
		return fmt.Errorf("failed to connect: %w", err)
	}

	// Initialize the MCP connection
	if err := c.initialize(ctx); err != nil {
		// Log initialization failure to server-specific log
		if c.upstreamLogger != nil {
			c.upstreamLogger.Error("MCP initialization failed",
				zap.Error(err))
		}
		c.client.Close()
		c.client = nil
		return fmt.Errorf("failed to initialize: %w", err)
	}

	c.connected = true
	c.logger.Info("Successfully connected to upstream MCP server",
		zap.String("server", c.config.Name),
		zap.String("transport", c.transportType))

	// Workaround: Get tools list immediately after init when transport works
	c.logger.Debug("Attempting to cache tools immediately after initialization",
		zap.String("server", c.config.Name),
		zap.String("transport", c.transportType))

	// Try to get tools with retry for servers that need time to initialize
	var toolsResult *mcp.ListToolsResult
	var listErr error
	maxRetries := 3
	for attempt := 1; attempt <= maxRetries; attempt++ {
		listReq := mcp.ListToolsRequest{}
		toolsResult, listErr = c.client.ListTools(ctx, listReq)

		if listErr != nil {
			c.logger.Warn("Failed to get tools during initialization attempt",
				zap.String("server", c.config.Name),
				zap.String("transport", c.transportType),
				zap.Int("attempt", attempt),
				zap.Int("max_retries", maxRetries),
				zap.Error(listErr))

			// Don't retry on connection errors
			break
		}

		if len(toolsResult.Tools) > 0 {
			// Got tools, break out of retry loop
			break
		}

		// Empty tools list - retry with small delay for HTTP servers
		if attempt < maxRetries {
			c.logger.Debug("Empty tools list, retrying after delay",
				zap.String("server", c.config.Name),
				zap.String("transport", c.transportType),
				zap.Int("attempt", attempt),
				zap.Int("max_retries", maxRetries))
			time.Sleep(time.Duration(attempt) * 100 * time.Millisecond) // 100ms, 200ms, 300ms
		}
	}

	if listErr != nil {
		c.logger.Warn("Failed to cache tools during initialization after retries",
			zap.String("server", c.config.Name),
			zap.String("transport", c.transportType),
			zap.Error(listErr))
		// Log to server-specific log for debugging
		if c.upstreamLogger != nil {
			c.upstreamLogger.Warn("Failed to cache tools during initialization",
				zap.Error(listErr))
		}
	} else if len(toolsResult.Tools) == 0 {
		c.logger.Warn("Server returned empty tools list after all retry attempts",
			zap.String("server", c.config.Name),
			zap.String("transport", c.transportType),
			zap.Int("attempts", maxRetries))
		// Log to server-specific log for debugging
		if c.upstreamLogger != nil {
			c.upstreamLogger.Warn("Server returned empty tools list after all retry attempts")
		}
	} else {
		// Cache tools for the duration of this connection (until disconnect)
		// Note: We already hold a lock from Connect(), so set directly
		c.cachedTools = make([]mcp.Tool, len(toolsResult.Tools))
		copy(c.cachedTools, toolsResult.Tools)

		c.logger.Info("Tools list cached for connection duration",
			zap.String("server", c.config.Name),
			zap.String("transport", c.transportType),
			zap.Int("tool_count", len(toolsResult.Tools)))

		// Log to server-specific log for debugging
		if c.upstreamLogger != nil {
			c.upstreamLogger.Info("Tools list cached during connection initialization",
				zap.Int("tool_count", len(toolsResult.Tools)))
		}
	}

	// Log successful connection to server-specific log
	if c.upstreamLogger != nil {
		c.upstreamLogger.Info("Successfully connected and initialized",
			zap.String("transport", c.transportType),
			zap.String("server_name", c.serverInfo.ServerInfo.Name),
			zap.String("server_version", c.serverInfo.ServerInfo.Version),
			zap.String("protocol_version", c.serverInfo.ProtocolVersion))
	}

	return nil
}

// connectStdio establishes stdio transport connection
func (c *Client) connectStdio(ctx context.Context) error {
	if c.config.Command == "" {
		return fmt.Errorf("no command specified for stdio transport")
	}

	// Build environment variables (same as demo - full system environment)
	envVars := os.Environ() // Start with full system environment
	for k, v := range c.config.Env {
		envVars = append(envVars, fmt.Sprintf("%s=%s", k, v))
	}

	// For Docker commands, add --cidfile to capture container ID for debugging
	args := c.config.Args
	var cidFile string
	if c.config.Command == "docker" && len(args) > 0 && args[0] == "run" {
		// Create temp file for container ID
		tmpFile, err := os.CreateTemp("", "mcpproxy-cid-*.txt")
		if err == nil {
			cidFile = tmpFile.Name()
			tmpFile.Close()
			// Remove the file first to avoid Docker's "file exists" error
			os.Remove(cidFile)
			// Insert --cidfile after "run"
			newArgs := make([]string, 0, len(args)+2)
			newArgs = append(newArgs, args[0], "--cidfile", cidFile) // "run" + cidfile
			newArgs = append(newArgs, args[1:]...)
			args = newArgs
		}
	}

	// Upstream transport (same as demo)
	stdioTransport := uptransport.NewStdio(c.config.Command, envVars, args...)
	c.client = client.NewClient(stdioTransport)

	if err := c.client.Start(ctx); err != nil {
		return fmt.Errorf("failed to start stdio client: %w", err)
	}

	// Enable stderr monitoring for Docker containers
	c.stderr = stdioTransport.Stderr()
	if c.stderr != nil {
		c.StartStderrMonitoring()
	}

	// Enable Docker logs monitoring if we have a container ID
	if cidFile != "" {
		go c.monitorDockerLogs(cidFile)
	}

	return nil
}

// connectHTTP establishes HTTP/SSE transport connection with auth fallback
func (c *Client) connectHTTP(ctx context.Context) error {
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
func (c *Client) tryHeadersAuth(ctx context.Context) error {
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
func (c *Client) tryNoAuth(ctx context.Context) error {
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
func (c *Client) tryOAuthAuth(_ context.Context) error {
	// This will be implemented in the auth module
	// For now, return error
	return fmt.Errorf("OAuth authentication not yet implemented in core client")
}

// isAuthError checks if error indicates authentication failure
func (c *Client) isAuthError(err error) bool {
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
func (c *Client) isConfigError(err error) bool {
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
func (c *Client) initialize(ctx context.Context) error {
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
		// Log initialization failure to server-specific log
		if c.upstreamLogger != nil {
			c.upstreamLogger.Error("MCP initialize JSON-RPC call failed",
				zap.Error(err))
		}
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

	// Log initialization success to server-specific log
	if c.upstreamLogger != nil {
		c.upstreamLogger.Info("MCP initialization completed successfully",
			zap.String("server_name", serverInfo.ServerInfo.Name),
			zap.String("server_version", serverInfo.ServerInfo.Version),
			zap.String("protocol_version", serverInfo.ProtocolVersion))
	}

	return nil
}

// Disconnect closes the connection
func (c *Client) Disconnect() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.connected || c.client == nil {
		return nil
	}

	c.logger.Info("Disconnecting from upstream MCP server")

	// Log disconnection to server-specific log
	if c.upstreamLogger != nil {
		c.upstreamLogger.Info("Disconnecting from server")
	}

	// Stop stderr monitoring before closing client
	c.StopStderrMonitoring()

	// Stop process monitoring before closing client
	c.StopProcessMonitoring()

	c.client.Close()
	c.client = nil
	c.serverInfo = nil
	c.connected = false

	// Clear cached tools on disconnect
	c.cachedTools = nil

	return nil
}

// IsConnected returns whether the client is currently connected
func (c *Client) IsConnected() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.connected
}

// ListTools retrieves available tools from the upstream server
func (c *Client) ListTools(ctx context.Context) ([]*config.ToolMetadata, error) {
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

	// SOLUTION: Use cached tools from successful immediate call
	c.mu.RLock()
	cachedTools := c.cachedTools
	c.mu.RUnlock()

	if len(cachedTools) > 0 {
		c.logger.Info("🎯 Using cached tools list",
			zap.Int("cached_tool_count", len(cachedTools)))

		// Convert cached tools to our format
		tools := []*config.ToolMetadata{}
		for _, tool := range cachedTools {
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

		return tools, nil
	}

	// Fallback if no cached tools (shouldn't happen)
	c.logger.Warn("No cached tools available, falling back to direct call")
	return nil, fmt.Errorf("No cached tools available and transport is broken for subsequent calls")
}

// CallTool executes a tool on the upstream server
func (c *Client) CallTool(ctx context.Context, toolName string, args map[string]interface{}) (*mcp.CallToolResult, error) {
	c.mu.RLock()
	client := c.client
	c.mu.RUnlock()

	if !c.IsConnected() || client == nil {
		return nil, fmt.Errorf("client not connected")
	}

	request := mcp.CallToolRequest{}
	request.Params.Name = toolName
	request.Params.Arguments = args

	// Log to server-specific log
	if c.upstreamLogger != nil {
		c.upstreamLogger.Info("Starting CallTool operation",
			zap.String("tool_name", toolName))
	}

	// Log request for trace debugging
	if c.upstreamLogger != nil {
		if reqBytes, err := json.MarshalIndent(request, "", "  "); err == nil {
			c.upstreamLogger.Debug("JSON-RPC CallTool Request",
				zap.String("method", "tools/call"),
				zap.String("tool", toolName),
				zap.String("formatted_json", string(reqBytes)))
		}
	}

	result, err := client.CallTool(ctx, request)
	if err != nil {
		// Log CallTool failure to server-specific log
		if c.upstreamLogger != nil {
			c.upstreamLogger.Error("CallTool operation failed",
				zap.String("tool_name", toolName),
				zap.Error(err))
		}
		return nil, fmt.Errorf("CallTool failed for '%s': %w", toolName, err)
	}

	// Log successful CallTool to server-specific log
	if c.upstreamLogger != nil {
		c.upstreamLogger.Info("CallTool operation completed successfully",
			zap.String("tool_name", toolName))
	}

	// Log response for trace debugging
	if c.upstreamLogger != nil {
		if respBytes, err := json.MarshalIndent(result, "", "  "); err == nil {
			c.upstreamLogger.Debug("JSON-RPC CallTool Response",
				zap.String("method", "tools/call"),
				zap.String("tool", toolName),
				zap.String("formatted_json", string(respBytes)))
		}
	}

	return result, nil
}

// GetConnectionInfo returns basic connection information
func (c *Client) GetConnectionInfo() types.ConnectionInfo {
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
func (c *Client) GetServerInfo() *mcp.InitializeResult {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.serverInfo
}

// GetTransportType returns the transport type being used
func (c *Client) GetTransportType() string {
	return c.transportType
}

// GetStderr returns stderr reader for stdio transport
func (c *Client) GetStderr() io.Reader {
	return c.stderr
}

// StartStderrMonitoring starts monitoring stderr output and logging it
func (c *Client) StartStderrMonitoring() {
	if c.stderr == nil || c.transportType != transport.TransportStdio {
		return
	}

	// Create context for stderr monitoring
	c.stderrMonitoringCtx, c.stderrMonitoringCancel = context.WithCancel(context.Background())

	c.stderrMonitoringWG.Add(1)
	go func() {
		defer c.stderrMonitoringWG.Done()
		c.monitorStderr()
	}()

	c.logger.Debug("Started stderr monitoring",
		zap.String("server", c.config.Name))
}

// StopStderrMonitoring stops stderr monitoring
func (c *Client) StopStderrMonitoring() {
	if c.stderrMonitoringCancel != nil {
		c.stderrMonitoringCancel()
		c.stderrMonitoringWG.Wait()
		c.logger.Debug("Stopped stderr monitoring",
			zap.String("server", c.config.Name))
	}
}

// StartProcessMonitoring starts monitoring the underlying process
func (c *Client) StartProcessMonitoring() {
	if c.processCmd == nil {
		return
	}

	// Create context for process monitoring
	c.processMonitorCtx, c.processMonitorCancel = context.WithCancel(context.Background())

	c.processMonitorWG.Add(1)
	go func() {
		defer c.processMonitorWG.Done()
		c.monitorProcess()
	}()

	c.logger.Debug("Started process monitoring",
		zap.String("server", c.config.Name),
		zap.String("command", c.processCmd.Path))
}

// StopProcessMonitoring stops process monitoring
func (c *Client) StopProcessMonitoring() {
	if c.processMonitorCancel != nil {
		c.processMonitorCancel()
		c.processMonitorWG.Wait()
		c.logger.Debug("Stopped process monitoring",
			zap.String("server", c.config.Name))
	}
}

// monitorProcess monitors the underlying process health
func (c *Client) monitorProcess() {
	if c.processCmd == nil {
		return
	}

	// Check if this is a Docker command
	isDocker := strings.Contains(c.config.Command, "docker")

	ticker := time.NewTicker(5 * time.Second) // Check every 5 seconds
	defer ticker.Stop()

	for {
		select {
		case <-c.processMonitorCtx.Done():
			return
		case <-ticker.C:
			if isDocker {
				c.checkDockerContainerHealth()
			}
		}
	}
}

// checkDockerContainerHealth checks if Docker containers are still running
func (c *Client) checkDockerContainerHealth() {
	// For Docker commands, we can check if containers are still running
	// This is a simplified check - in production you might want more sophisticated monitoring

	// Try to run a simple docker command to check daemon connectivity
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", "version", "--format", "{{.Server.Version}}")
	if err := cmd.Run(); err != nil {
		c.logger.Warn("Docker daemon appears to be unreachable",
			zap.String("server", c.config.Name),
			zap.Error(err))

		if c.upstreamLogger != nil {
			c.upstreamLogger.Warn("Docker connectivity check failed",
				zap.Error(err))
		}
	}
}

// monitorStderr monitors stderr output and logs it to both main and server-specific logs
func (c *Client) monitorStderr() {
	scanner := bufio.NewScanner(c.stderr)
	for scanner.Scan() {
		select {
		case <-c.stderrMonitoringCtx.Done():
			return
		default:
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}

			// Log to main logger
			c.logger.Info("stderr output",
				zap.String("server", c.config.Name),
				zap.String("message", line))

			// Log to server-specific logger if available
			if c.upstreamLogger != nil {
				c.upstreamLogger.Info("stderr", zap.String("message", line))
			}
		}
	}

	// Check for scanner errors - this is crucial for detecting pipe issues
	if err := scanner.Err(); err != nil {
		// Distinguish between different error types
		if strings.Contains(err.Error(), "broken pipe") || strings.Contains(err.Error(), "closed pipe") {
			c.logger.Error("Stdin/stdout pipe closed - container may have died",
				zap.String("server", c.config.Name),
				zap.Error(err))
		} else {
			c.logger.Warn("Error reading stderr",
				zap.String("server", c.config.Name),
				zap.Error(err))
		}

		if c.upstreamLogger != nil {
			c.upstreamLogger.Warn("stderr read error", zap.Error(err))
		}
	} else {
		// If scanner ended without error, the pipe was likely closed gracefully
		c.logger.Info("Stderr stream ended",
			zap.String("server", c.config.Name))

		if c.upstreamLogger != nil {
			c.upstreamLogger.Info("stderr stream closed")
		}
	}
}

// monitorDockerLogs monitors Docker container logs using `docker logs`
func (c *Client) monitorDockerLogs(cidFile string) {
	// Wait a bit for container to start and CID file to be written
	time.Sleep(500 * time.Millisecond)

	// Read container ID from file
	cidBytes, err := os.ReadFile(cidFile)
	if err != nil {
		c.logger.Debug("Could not read container ID file",
			zap.String("server", c.config.Name),
			zap.String("cid_file", cidFile),
			zap.Error(err))
		return
	}

	containerID := strings.TrimSpace(string(cidBytes))
	if containerID == "" {
		return
	}

	// Clean up the temp file
	defer os.Remove(cidFile)

	c.logger.Info("Starting Docker logs monitoring",
		zap.String("server", c.config.Name),
		zap.String("container_id", containerID[:12])) // Show short ID

	// Start docker logs -f command
	cmd := exec.Command("docker", "logs", "-f", "--timestamps", containerID)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		c.logger.Warn("Failed to create docker logs stdout pipe", zap.Error(err))
		return
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		c.logger.Warn("Failed to create docker logs stderr pipe", zap.Error(err))
		return
	}

	if err := cmd.Start(); err != nil {
		c.logger.Warn("Failed to start docker logs command", zap.Error(err))
		return
	}

	// Monitor both stdout and stderr from docker logs
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line != "" {
				c.logger.Info("container logs (stdout)",
					zap.String("server", c.config.Name),
					zap.String("container_id", containerID[:12]),
					zap.String("message", line))
			}
		}
	}()

	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line != "" {
				c.logger.Info("container logs (stderr)",
					zap.String("server", c.config.Name),
					zap.String("container_id", containerID[:12]),
					zap.String("message", line))
			}
		}
	}()

	// Wait for docker logs command to finish (when container stops)
	if err := cmd.Wait(); err != nil {
		c.logger.Debug("Docker logs command ended with error", zap.Error(err))
	}
	c.logger.Debug("Docker logs monitoring ended",
		zap.String("server", c.config.Name),
		zap.String("container_id", containerID[:12]))
}

// CheckConnectionHealth performs a health check on the connection
func (c *Client) CheckConnectionHealth(ctx context.Context) error {
	if !c.IsConnected() {
		return fmt.Errorf("client not connected")
	}

	// For stdio connections, try a simple ping-like operation
	if c.transportType == transport.TransportStdio {
		// Use a short timeout for health check
		checkCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()

		// Try to list tools as a health check
		_, err := c.ListTools(checkCtx)
		if err != nil {
			if strings.Contains(err.Error(), "context deadline exceeded") {
				return fmt.Errorf("connection health check timed out - container may be unresponsive")
			} else if strings.Contains(err.Error(), "broken pipe") || strings.Contains(err.Error(), "connection refused") {
				return fmt.Errorf("connection pipe broken - container may have died")
			}
			return fmt.Errorf("connection health check failed: %w", err)
		}
	}

	return nil
}

// GetConnectionDiagnostics returns detailed diagnostic information about the connection
func (c *Client) GetConnectionDiagnostics() map[string]interface{} {
	c.mu.RLock()
	defer c.mu.RUnlock()

	diagnostics := map[string]interface{}{
		"connected":       c.connected,
		"transport_type":  c.transportType,
		"server_name":     c.config.Name,
		"command":         c.config.Command,
		"args":            c.config.Args,
		"has_stderr":      c.stderr != nil,
		"has_process_cmd": c.processCmd != nil,
	}

	if c.serverInfo != nil {
		diagnostics["server_info"] = map[string]interface{}{
			"name":             c.serverInfo.ServerInfo.Name,
			"version":          c.serverInfo.ServerInfo.Version,
			"protocol_version": c.serverInfo.ProtocolVersion,
		}
	}

	// Add Docker-specific diagnostics
	if strings.Contains(c.config.Command, "docker") {
		diagnostics["is_docker"] = true
		diagnostics["docker_args"] = c.config.Args

		// Check Docker daemon connectivity
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		cmd := exec.CommandContext(ctx, "docker", "version", "--format", "{{.Server.Version}}")
		if err := cmd.Run(); err != nil {
			diagnostics["docker_daemon_reachable"] = false
			diagnostics["docker_daemon_error"] = err.Error()
		} else {
			diagnostics["docker_daemon_reachable"] = true
		}
	}

	return diagnostics
}

// GetEnvManager returns the environment manager for testing purposes
func (c *Client) GetEnvManager() interface{} {
	return c.envManager
}

// Helper methods

func (c *Client) getServerName() string {
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
