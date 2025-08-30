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

	// Tools caching disabled - will make direct calls to upstream server each time
	c.logger.Debug("Tools caching disabled - will make direct calls to upstream server",
		zap.String("server", c.config.Name),
		zap.String("transport", c.transportType))

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

	// Check if this will be a Docker command (either explicit or through isolation)
	willUseDocker := (c.config.Command == "docker" || strings.HasSuffix(c.config.Command, "/docker")) && len(args) > 0 && args[0] == "run"
	if !willUseDocker && c.isolationManager != nil {
		willUseDocker = c.isolationManager.ShouldIsolate(c.config)
	}

	if willUseDocker {
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

			c.logger.Debug("Container ID file setup complete",
				zap.String("server", c.config.Name),
				zap.String("cid_file", cidFile))
		} else {
			c.logger.Error("Failed to create container ID file",
				zap.String("server", c.config.Name),
				zap.Error(err))
		}
	}

	// Determine final command and args based on isolation settings
	var finalCommand string
	var finalArgs []string

	// Check if Docker isolation should be used
	if c.isolationManager != nil && c.isolationManager.ShouldIsolate(c.config) {
		c.logger.Info("Docker isolation enabled for server",
			zap.String("server", c.config.Name),
			zap.String("original_command", c.config.Command))

		// Use Docker isolation
		finalCommand, finalArgs = c.setupDockerIsolation(c.config.Command, args)
		c.isDockerCommand = true

		// Add cidfile to Docker args if we have one
		if cidFile != "" {
			finalArgs = c.insertCidfileIntoDockerArgs(finalArgs, cidFile)
		}
	} else {
		// Use shell wrapping for environment inheritance
		// This fixes issues when mcpproxy is launched via Launchd and doesn't inherit
		// user's shell environment (like PATH customizations from .bashrc, .zshrc, etc.)
		finalCommand, finalArgs = c.wrapWithUserShell(c.config.Command, args)
		c.isDockerCommand = false

		// Handle explicit docker commands
		if (c.config.Command == "docker" || strings.HasSuffix(c.config.Command, "/docker")) && len(args) > 0 && args[0] == "run" {
			c.isDockerCommand = true
			if cidFile != "" {
				// For shell-wrapped Docker commands, we need to modify the shell command string
				finalArgs = c.insertCidfileIntoShellDockerCommand(finalArgs, cidFile)
			}
		}
	}

	// Upstream transport (same as demo)
	stdioTransport := uptransport.NewStdio(finalCommand, envVars, finalArgs...)
	c.client = client.NewClient(stdioTransport)

	// Log final stdio configuration for debugging
	c.logger.Debug("Initialized stdio transport",
		zap.String("server", c.config.Name),
		zap.String("final_command", finalCommand),
		zap.Strings("final_args", finalArgs),
		zap.String("original_command", c.config.Command),
		zap.Strings("original_args", args),
		zap.Bool("docker_isolation", c.isDockerCommand))

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
		// Use the same monitoring context as other goroutines
		go c.monitorDockerLogsWithContext(c.stderrMonitoringCtx, cidFile)
		// Also read container ID for cleanup purposes
		go c.readContainerIDWithContext(c.stderrMonitoringCtx, cidFile)
	}

	return nil
}

// setupDockerIsolation sets up Docker isolation for a stdio command
func (c *Client) setupDockerIsolation(command string, args []string) (dockerCommand string, dockerArgs []string) {
	// Detect the runtime type from the command
	runtimeType := c.isolationManager.DetectRuntimeType(command)
	c.logger.Debug("Detected runtime type for Docker isolation",
		zap.String("server", c.config.Name),
		zap.String("command", command),
		zap.String("runtime_type", runtimeType))

	// Build Docker run arguments
	dockerRunArgs, err := c.isolationManager.BuildDockerArgs(c.config, runtimeType)
	if err != nil {
		c.logger.Error("Failed to build Docker args, falling back to shell wrapping",
			zap.String("server", c.config.Name),
			zap.Error(err))
		return c.wrapWithUserShell(command, args)
	}

	// Transform the command for container execution
	containerCommand, containerArgs := c.isolationManager.TransformCommandForContainer(command, args, runtimeType)

	// Combine Docker run args with the container command
	finalArgs := make([]string, 0, len(dockerRunArgs)+1+len(containerArgs))
	finalArgs = append(finalArgs, dockerRunArgs...)
	finalArgs = append(finalArgs, containerCommand)
	finalArgs = append(finalArgs, containerArgs...)

	c.logger.Info("Docker isolation setup completed",
		zap.String("server", c.config.Name),
		zap.String("runtime_type", runtimeType),
		zap.String("container_command", containerCommand),
		zap.Strings("container_args", containerArgs),
		zap.Strings("docker_run_args", dockerRunArgs))

	// Log to server-specific log as well
	if c.upstreamLogger != nil {
		c.upstreamLogger.Info("Docker isolation configured",
			zap.String("runtime_type", runtimeType),
			zap.String("container_command", containerCommand))
	}

	return "docker", finalArgs
}

// insertCidfileIntoDockerArgs inserts --cidfile option into Docker run arguments
func (c *Client) insertCidfileIntoDockerArgs(dockerArgs []string, cidFile string) []string {
	// Find the position after "run"
	runIndex := -1
	for i, arg := range dockerArgs {
		if arg == "run" {
			runIndex = i
			break
		}
	}

	if runIndex == -1 {
		// No "run" found, just append at the end (shouldn't happen in normal cases)
		c.logger.Warn("No 'run' command found in Docker args, appending cidfile at end",
			zap.String("server", c.config.Name),
			zap.Strings("docker_args", dockerArgs))
		return append(dockerArgs, "--cidfile", cidFile)
	}

	// Insert --cidfile after "run" and before other arguments
	newArgs := make([]string, 0, len(dockerArgs)+2)
	newArgs = append(newArgs, dockerArgs[:runIndex+1]...) // Include "run"
	newArgs = append(newArgs, "--cidfile", cidFile)       // Add cidfile
	newArgs = append(newArgs, dockerArgs[runIndex+1:]...) // Add remaining args

	// Ensure -i (interactive) is present for stdio transport
	interactivePresent := false
	for _, arg := range newArgs {
		if arg == "-i" || strings.HasPrefix(arg, "--interactive") {
			interactivePresent = true
			break
		}
	}

	if !interactivePresent {
		// Insert -i after "run" and cidfile args
		finalArgs := make([]string, 0, len(newArgs)+1)
		finalArgs = append(finalArgs, newArgs[:runIndex+3]...) // Include "run", "--cidfile", cidFile
		finalArgs = append(finalArgs, "-i")                    // Add interactive flag
		finalArgs = append(finalArgs, newArgs[runIndex+3:]...) // Add remaining args

		c.logger.Debug("Added -i flag to Docker args for stdio transport",
			zap.String("server", c.config.Name))

		if c.upstreamLogger != nil {
			c.upstreamLogger.Debug("Added -i flag for stdio transport")
		}

		return finalArgs
	}

	return newArgs
}

// insertCidfileIntoShellDockerCommand inserts --cidfile into a shell-wrapped Docker command
func (c *Client) insertCidfileIntoShellDockerCommand(shellArgs []string, cidFile string) []string {
	// Shell args typically look like: ["-l", "-c", "docker run -i --rm mcp/duckduckgo"]
	if len(shellArgs) < 3 || shellArgs[len(shellArgs)-3] != "-c" {
		// If it's not the expected format, fall back to appending
		c.logger.Warn("Unexpected shell command format for Docker cidfile insertion",
			zap.String("server", c.config.Name),
			zap.Strings("shell_args", shellArgs))
		return append(shellArgs, "--cidfile", cidFile)
	}

	// Get the Docker command string (last argument)
	dockerCmd := shellArgs[len(shellArgs)-1]

	// Insert --cidfile into the Docker command string
	// Look for "docker run" and insert --cidfile right after
	if strings.Contains(dockerCmd, "docker run") {
		// Replace "docker run" with "docker run --cidfile /path/to/file"
		dockerCmdWithCid := strings.Replace(dockerCmd, "docker run", fmt.Sprintf("docker run --cidfile %s", cidFile), 1)

		// Create new args with the modified command
		newArgs := make([]string, len(shellArgs))
		copy(newArgs, shellArgs)
		newArgs[len(newArgs)-1] = dockerCmdWithCid

		c.logger.Debug("Inserted cidfile into shell-wrapped Docker command",
			zap.String("server", c.config.Name),
			zap.String("original_cmd", dockerCmd),
			zap.String("modified_cmd", dockerCmdWithCid))

		return newArgs
	}

	// If we can't find "docker run", fall back to appending
	c.logger.Warn("Could not find 'docker run' in shell command for cidfile insertion",
		zap.String("server", c.config.Name),
		zap.String("docker_cmd", dockerCmd))
	return append(shellArgs, "--cidfile", cidFile)
}

// wrapWithUserShell wraps a command with the user's login shell to inherit full environment
func (c *Client) wrapWithUserShell(command string, args []string) (shellCommand string, shellArgs []string) {
	// Get the user's default shell
	shell, _ := c.envManager.GetSystemEnvVar("SHELL")
	if shell == "" {
		// Fallback to common shells based on OS
		if strings.Contains(strings.ToLower(command), "windows") {
			shell = "cmd"
		} else {
			shell = "/bin/bash" // Default fallback
		}
	}

	// Build the command string that will be executed by the shell
	// We need to properly escape the command and arguments for shell execution
	var commandParts []string
	commandParts = append(commandParts, shellescape(command))
	for _, arg := range args {
		commandParts = append(commandParts, shellescape(arg))
	}
	commandString := strings.Join(commandParts, " ")

	// Log what we're doing for debugging
	c.logger.Debug("Wrapping command with user shell for full environment inheritance",
		zap.String("server", c.config.Name),
		zap.String("original_command", command),
		zap.Strings("original_args", args),
		zap.String("shell", shell),
		zap.String("wrapped_command", commandString))

	// Return shell with -l (login) flag to load user's full environment
	// The -c flag executes the command string
	return shell, []string{"-l", "-c", commandString}
}

// shellescape escapes a string for safe shell execution
func shellescape(s string) string {
	if s == "" {
		return "''"
	}

	// If string contains no special characters, return as-is
	if !strings.ContainsAny(s, " \t\n\r\"'\\$`;&|<>(){}[]?*~") {
		return s
	}

	// Use single quotes and escape any single quotes in the string
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
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
	return c.DisconnectWithContext(context.Background())
}

// DisconnectWithContext closes the connection with context timeout
func (c *Client) DisconnectWithContext(_ context.Context) error {
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

		// Create a fresh context for Docker cleanup with its own timeout
		// This ensures cleanup can complete even if the main context expires
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()

		if c.containerID != "" {
			c.killDockerContainerWithContext(cleanupCtx)
			c.logger.Debug("Docker container cleanup completed",
				zap.String("server", c.config.Name))
		} else {
			c.logger.Debug("No container ID available, using fallback cleanup method",
				zap.String("server", c.config.Name))
			// Fallback: try to find and kill any containers started by this command
			c.killDockerContainerByCommandWithContext(cleanupCtx)
			c.logger.Debug("Docker fallback cleanup completed",
				zap.String("server", c.config.Name))
		}
	} else {
		c.logger.Debug("Non-Docker command disconnecting, no container cleanup needed",
			zap.String("server", c.config.Name))
	}

	c.logger.Debug("Closing MCP client connection",
		zap.String("server", c.config.Name))
	c.client.Close()
	c.logger.Debug("MCP client connection closed",
		zap.String("server", c.config.Name))

	c.client = nil
	c.serverInfo = nil
	c.connected = false

	// Clear cached tools on disconnect
	c.cachedTools = nil

	c.logger.Debug("Disconnect completed successfully",
		zap.String("server", c.config.Name))
	return nil
}
