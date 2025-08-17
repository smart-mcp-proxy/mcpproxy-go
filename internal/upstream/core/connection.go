package core

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"time"

	"mcpproxy-go/internal/transport"

	"github.com/mark3labs/mcp-go/client"
	uptransport "github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	"go.uber.org/zap"
)

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
	case transportStdio:
		c.logger.Debug("📡 Using STDIO transport")
		err = c.connectStdio(ctx)
	case "http", "streamable-http", "sse":
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
func (c *Client) connectStdio(_ context.Context) error {
	if c.config.Command == "" {
		return fmt.Errorf("no command specified for stdio transport")
	}

	// Build environment variables using secure environment manager
	// This ensures PATH includes proper discovery even when launched via Launchd
	envVars := c.envManager.BuildSecureEnvironment()

	// Add server-specific environment variables (these are already included via envManager,
	// but this ensures any additional runtime variables are included)
	for k, v := range c.config.Env {
		found := false
		for i, envVar := range envVars {
			if strings.HasPrefix(envVar, k+"=") {
				envVars[i] = fmt.Sprintf("%s=%s", k, v) // Override existing
				found = true
				break
			}
		}
		if !found {
			envVars = append(envVars, fmt.Sprintf("%s=%s", k, v)) // Add new
		}
	}

	// For Docker commands, add --cidfile to capture container ID for proper cleanup
	args := c.config.Args
	var cidFile string
	c.isDockerCommand = (c.config.Command == "docker" || strings.HasSuffix(c.config.Command, "/docker")) && len(args) > 0 && args[0] == "run"
	if c.isDockerCommand {
		c.logger.Debug("Docker command detected, setting up container ID tracking",
			zap.String("server", c.config.Name),
			zap.String("command", c.config.Command),
			zap.Strings("original_args", args))

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

			c.logger.Debug("Container ID file setup complete",
				zap.String("server", c.config.Name),
				zap.String("cid_file", cidFile),
				zap.Strings("modified_args", args))
		} else {
			c.logger.Error("Failed to create container ID file",
				zap.String("server", c.config.Name),
				zap.Error(err))
		}

		// Ensure interactive mode to keep STDIN open for MCP stdio transport
		interactivePresent := false
		for _, a := range args {
			if a == "-i" || strings.HasPrefix(a, "--interactive") {
				interactivePresent = true
				break
			}
		}
		if !interactivePresent {
			fixedArgs := make([]string, 0, len(args)+1)
			fixedArgs = append(fixedArgs, args[0], "-i")
			fixedArgs = append(fixedArgs, args[1:]...)
			args = fixedArgs
			c.logger.Warn("Docker run without -i detected; adding -i to keep STDIN open for stdio transport",
				zap.String("server", c.config.Name),
				zap.Strings("final_args", args))
			if c.upstreamLogger != nil {
				c.upstreamLogger.Warn("Docker run without -i detected; adding -i to keep STDIN open")
			}
		}
	}

	// Upstream transport (same as demo)
	stdioTransport := uptransport.NewStdio(c.config.Command, envVars, args...)
	c.client = client.NewClient(stdioTransport)

	// Log final stdio configuration for debugging
	c.logger.Debug("Initialized stdio transport",
		zap.String("server", c.config.Name),
		zap.String("command", c.config.Command),
		zap.Strings("args", args))

	// CRITICAL FIX: Use persistent context for stdio transport to prevent premature process termination
	// The initialization context might be short-lived, but the stdio process needs to stay alive
	persistentCtx := context.Background()
	if err := c.client.Start(persistentCtx); err != nil {
		return fmt.Errorf("failed to start stdio client: %w", err)
	}

	// CRITICAL FIX: Extract underlying process from mcp-go transport for lifecycle management
	// Try to access the process via reflection
	c.logger.Debug("Attempting to extract process from stdio transport for lifecycle management",
		zap.String("server", c.config.Name),
		zap.String("transport_type", fmt.Sprintf("%T", stdioTransport)))

	// Use reflection to access the process field from the transport
	transportValue := reflect.ValueOf(stdioTransport)
	if transportValue.Kind() == reflect.Ptr {
		transportValue = transportValue.Elem()
	}

	// Try to find a process field (common names: cmd, process, proc)
	var processField reflect.Value
	if transportValue.IsValid() {
		for _, fieldName := range []string{"cmd", "process", "proc", "Cmd", "Process", "Proc"} {
			field := transportValue.FieldByName(fieldName)
			if field.IsValid() && field.CanInterface() {
				if cmd, ok := field.Interface().(*exec.Cmd); ok && cmd != nil {
					processField = field
					break
				}
			}
		}
	}

	if processField.IsValid() {
		if cmd, ok := processField.Interface().(*exec.Cmd); ok && cmd != nil {
			c.processCmd = cmd
			c.logger.Info("Successfully extracted process from stdio transport for lifecycle management",
				zap.String("server", c.config.Name),
				zap.Int("pid", c.processCmd.Process.Pid))
		}
	} else {
		c.logger.Warn("Could not extract process from stdio transport - will use alternative process tracking",
			zap.String("server", c.config.Name),
			zap.String("transport_type", fmt.Sprintf("%T", stdioTransport)))

		// For Docker commands, we can still monitor via container ID and docker ps
		if c.isDockerCommand {
			c.logger.Info("Docker command detected - will monitor via container health checks",
				zap.String("server", c.config.Name))
		}
	}

	// Enable stderr monitoring for Docker containers
	c.stderr = stdioTransport.Stderr()
	if c.stderr != nil {
		c.StartStderrMonitoring()
	}

	// Start process monitoring if we have the process reference OR it's a Docker command
	if c.processCmd != nil {
		c.logger.Debug("Starting process monitoring with extracted process reference",
			zap.String("server", c.config.Name))
		c.StartProcessMonitoring()
	} else if c.isDockerCommand {
		c.logger.Debug("Starting Docker container health monitoring without process reference",
			zap.String("server", c.config.Name))
		c.StartProcessMonitoring() // This will handle Docker-specific monitoring
	}

	// Enable Docker logs monitoring and track container ID if we have a container ID file
	if cidFile != "" {
		go c.monitorDockerLogs(cidFile)
		// Also read container ID for cleanup purposes
		go c.readContainerID(cidFile)
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

	// For Docker containers, kill the container before closing the client
	if c.isDockerCommand {
		c.logger.Debug("Disconnecting Docker command, attempting container cleanup",
			zap.String("server", c.config.Name),
			zap.Bool("has_container_id", c.containerID != ""))

		if c.containerID != "" {
			c.killDockerContainer()
		} else {
			c.logger.Debug("No container ID available, using fallback cleanup method",
				zap.String("server", c.config.Name))
			// Fallback: try to find and kill any containers started by this command
			c.killDockerContainerByCommand()
		}
	} else {
		c.logger.Debug("Non-Docker command disconnecting, no container cleanup needed",
			zap.String("server", c.config.Name))
	}

	c.client.Close()
	c.client = nil
	c.serverInfo = nil
	c.connected = false

	// Clear cached tools on disconnect
	c.cachedTools = nil

	return nil
}
