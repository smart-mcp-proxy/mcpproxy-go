package core

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"reflect"
	"runtime"
	"strings"
	"time"

	"mcpproxy-go/internal/oauth"
	"mcpproxy-go/internal/transport"

	"github.com/mark3labs/mcp-go/client"
	uptransport "github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	"go.uber.org/zap"
)

const (
	osLinux   = "linux"
	osDarwin  = "darwin"
	osWindows = "windows"

	dockerCleanupTimeout = 30 * time.Second

	// Transport types
	transportHTTP           = "http"
	transportHTTPStreamable = "streamable-http"
	transportSSE            = "sse"
)

// Context key types
type contextKey string

const (
	manualOAuthKey contextKey = "manual_oauth"
)

// OAuthParameterError represents a missing or invalid OAuth parameter
type OAuthParameterError struct {
	Parameter   string
	Location    string // "authorization_url" or "token_request"
	Message     string
	OriginalErr error
}

func (e *OAuthParameterError) Error() string {
	return fmt.Sprintf("OAuth provider requires '%s' parameter: %s", e.Parameter, e.Message)
}

func (e *OAuthParameterError) Unwrap() error {
	return e.OriginalErr
}

// ErrOAuthPending represents a deferred OAuth authentication requirement.
// This error indicates that OAuth is required but has been intentionally deferred
// (e.g., for user action via tray UI or CLI) rather than being a connection failure.
type ErrOAuthPending struct {
	ServerName string
	ServerURL  string
	Message    string
}

func (e *ErrOAuthPending) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("OAuth authentication required for %s: %s", e.ServerName, e.Message)
	}
	return fmt.Sprintf("OAuth authentication required for %s - use 'mcpproxy auth login --server=%s' or tray menu", e.ServerName, e.ServerName)
}

// IsOAuthPending checks if an error is an ErrOAuthPending
func IsOAuthPending(err error) bool {
	_, ok := err.(*ErrOAuthPending)
	return ok
}

// parseOAuthError extracts structured error information from OAuth provider responses
func parseOAuthError(err error, responseBody []byte) error {
	// Try to parse as FastAPI validation error (Runlayer format)
	var fapiErr struct {
		Detail []struct {
			Type  string   `json:"type"`
			Loc   []string `json:"loc"`
			Msg   string   `json:"msg"`
			Input any      `json:"input"`
		} `json:"detail"`
	}

	if json.Unmarshal(responseBody, &fapiErr) == nil && len(fapiErr.Detail) > 0 {
		for _, detail := range fapiErr.Detail {
			if detail.Type == "missing" && len(detail.Loc) >= 2 {
				if detail.Loc[0] == "query" {
					paramName := detail.Loc[1]
					return &OAuthParameterError{
						Parameter:   paramName,
						Location:    "authorization_url",
						Message:     detail.Msg,
						OriginalErr: err,
					}
				}
			}
		}
	}

	// Try to parse as RFC 6749 OAuth error response
	var oauthErr struct {
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
		ErrorURI         string `json:"error_uri"`
	}

	if json.Unmarshal(responseBody, &oauthErr) == nil && oauthErr.Error != "" {
		return fmt.Errorf("OAuth error: %s - %s", oauthErr.Error, oauthErr.ErrorDescription)
	}

	// Fallback to original error
	return err
}

// Connect establishes connection to the upstream server
func (c *Client) Connect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// CRITICAL FIX: Check for concurrent connection attempts to prevent duplicate containers
	if c.connecting {
		c.logger.Debug("Connection already in progress, rejecting concurrent attempt",
			zap.String("server", c.config.Name))
		return fmt.Errorf("connection already in progress")
	}

	// Allow reconnection if OAuth was recently completed (bypass "already connected" check)
	if c.connected && !c.wasOAuthRecentlyCompleted() {
		c.logger.Debug("Client already connected and OAuth not recent",
			zap.String("server", c.config.Name),
			zap.Bool("connected", c.connected))
		return fmt.Errorf("client already connected")
	}

	// Set connecting flag to prevent concurrent attempts
	c.connecting = true
	defer func() {
		c.connecting = false
	}()

	// Reset connection state for fresh connection attempt
	if c.connected {
		c.logger.Info("🔄 Reconnecting after OAuth completion",
			zap.String("server", c.config.Name))
		c.connected = false
		if c.client != nil {
			c.client.Close()
			c.client = nil
		}
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
	case transportHTTP, transportHTTPStreamable:
		c.logger.Debug("🌐 Using HTTP transport")
		err = c.connectHTTP(ctx)
	case transportSSE:
		c.logger.Debug("📡 Using SSE transport")
		err = c.connectSSE(ctx)
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

		// CRITICAL FIX: Cleanup Docker containers when any connection type fails
		// This prevents container accumulation when connections fail after Docker setup
		if c.isDockerCommand {
			c.logger.Warn("Connection failed for Docker command - cleaning up container",
				zap.String("server", c.config.Name),
				zap.String("transport", c.transportType),
				zap.String("container_name", c.containerName),
				zap.String("container_id", c.containerID),
				zap.Error(err))

			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), dockerCleanupTimeout)
			defer cleanupCancel()

			// Try to cleanup using container name first, then ID, then pattern matching
			if c.containerName != "" {
				c.logger.Debug("Attempting container cleanup by name after connection failure")
				if success := c.killDockerContainerByNameWithContext(cleanupCtx, c.containerName); success {
					c.logger.Info("Successfully cleaned up container by name after connection failure")
				}
			} else if c.containerID != "" {
				c.logger.Debug("Attempting container cleanup by ID after connection failure")
				c.killDockerContainerWithContext(cleanupCtx)
			} else {
				c.logger.Debug("Attempting container cleanup by pattern matching after connection failure")
				c.killDockerContainerByCommandWithContext(cleanupCtx)
			}
		}

		// CRITICAL FIX: Also cleanup process groups to prevent zombie processes on connection failure
		if c.processGroupID > 0 {
			c.logger.Warn("Connection failed - cleaning up process group to prevent zombie processes",
				zap.String("server", c.config.Name),
				zap.Int("pgid", c.processGroupID))

			if err := killProcessGroup(c.processGroupID, c.logger, c.config.Name); err != nil {
				c.logger.Error("Failed to clean up process group after connection failure",
					zap.String("server", c.config.Name),
					zap.Int("pgid", c.processGroupID),
					zap.Error(err))
			}
			c.processGroupID = 0
		}

		return fmt.Errorf("failed to connect: %w", err)
	}

	// CRITICAL FIX: Authentication strategies now handle initialize() testing
	// This eliminates the duplicate initialize() call that was causing OAuth strategy
	// to never be reached when no-auth succeeded at Start() but failed at initialize()
	// All authentication strategies (tryNoAuth, tryHeadersAuth, tryOAuthAuth) now test
	// both client.Start() AND c.initialize() to ensure OAuth errors are properly detected

	c.connected = true

	// If we had an OAuth flow in progress and connection succeeded, mark OAuth as complete
	if c.isOAuthInProgress() {
		c.logger.Info("✅ OAuth flow completed successfully - connection established with token",
			zap.String("server", c.config.Name))
		c.markOAuthComplete()
	}

	c.logger.Info("Successfully connected to upstream MCP server",
		zap.String("server", c.config.Name),
		zap.String("transport", c.transportType))

	// Tools caching disabled - will make direct calls to upstream server each time
	c.logger.Debug("Tools caching disabled - will make direct calls to upstream server",
		zap.String("server", c.config.Name),
		zap.String("transport", c.transportType))

	// Log successful connection to server-specific log
	if c.upstreamLogger != nil {
		if c.serverInfo != nil && c.serverInfo.ServerInfo.Name != "" {
			c.upstreamLogger.Info("Successfully connected and initialized",
				zap.String("transport", c.transportType),
				zap.String("server_name", c.serverInfo.ServerInfo.Name),
				zap.String("server_version", c.serverInfo.ServerInfo.Version),
				zap.String("protocol_version", c.serverInfo.ProtocolVersion))
		} else {
			c.upstreamLogger.Info("Successfully connected",
				zap.String("transport", c.transportType),
				zap.String("note", "serverInfo not yet available"))
		}
	}

	return nil
}

// connectStdio establishes stdio transport connection
func (c *Client) connectStdio(ctx context.Context) error {
	if c.config.Command == "" {
		return fmt.Errorf("no command specified for stdio transport")
	}

	// Validate working directory if specified
	if err := validateWorkingDir(c.config.WorkingDir); err != nil {
		// Log warning to both main logger and server-specific logger
		c.logger.Error("Invalid working directory for stdio server",
			zap.String("server", c.config.Name),
			zap.String("working_dir", c.config.WorkingDir),
			zap.Error(err))

		if c.upstreamLogger != nil {
			c.upstreamLogger.Error("Server startup failed due to invalid working directory",
				zap.String("working_dir", c.config.WorkingDir),
				zap.Error(err))
		}

		return fmt.Errorf("invalid working directory for server %s: %w", c.config.Name, err)
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
	willUseDocker := (c.config.Command == cmdDocker || strings.HasSuffix(c.config.Command, "/"+cmdDocker)) && len(args) > 0 && args[0] == cmdRun
	if !willUseDocker && c.isolationManager != nil {
		willUseDocker = c.isolationManager.ShouldIsolate(c.config)
	}

	if willUseDocker {
		// CRITICAL: Acquire per-server lock to prevent concurrent container creation
		// This prevents race conditions when multiple goroutines try to reconnect the same server
		lock := globalContainerLock.Lock(c.config.Name)
		defer lock.Unlock()

		c.logger.Debug("Docker command detected, setting up container ID tracking",
			zap.String("server", c.config.Name),
			zap.String("command", c.config.Command),
			zap.Strings("original_args", args))

		// CRITICAL: Clean up any existing containers first to prevent duplicates
		// This makes container creation idempotent and safe to call multiple times
		if err := c.ensureNoExistingContainers(ctx); err != nil {
			c.logger.Error("Failed to ensure no existing containers",
				zap.String("server", c.config.Name),
				zap.Error(err))
			// Continue anyway - we'll try to create the container
		}

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

		// Use Docker isolation (now shell-wrapped for PATH inheritance)
		finalCommand, finalArgs = c.setupDockerIsolation(c.config.Command, args)
		c.isDockerCommand = true

		// Add cidfile to shell-wrapped Docker command if we have one
		if cidFile != "" {
			finalArgs = c.insertCidfileIntoShellDockerCommand(finalArgs, cidFile)
		}
	} else {
		// For direct docker commands, inject env vars as -e flags before shell wrapping
		argsToWrap := args
		isDirectDockerRun := (c.config.Command == cmdDocker || strings.HasSuffix(c.config.Command, "/"+cmdDocker)) && len(args) > 0 && args[0] == cmdRun
		if isDirectDockerRun && len(c.config.Env) > 0 {
			argsToWrap = c.injectEnvVarsIntoDockerArgs(args, c.config.Env)
			c.logger.Debug("Injected env vars into direct docker command",
				zap.String("server", c.config.Name),
				zap.Int("env_count", len(c.config.Env)),
				zap.Strings("modified_args", argsToWrap))
		}

		// Use shell wrapping for environment inheritance
		// This fixes issues when mcpproxy is launched via Launchd and doesn't inherit
		// user's shell environment (like PATH customizations from .bashrc, .zshrc, etc.)
		finalCommand, finalArgs = c.wrapWithUserShell(c.config.Command, argsToWrap)
		c.isDockerCommand = false

		// Handle explicit docker commands
		if isDirectDockerRun {
			c.isDockerCommand = true
			if cidFile != "" {
				// For shell-wrapped Docker commands, we need to modify the shell command string
				finalArgs = c.insertCidfileIntoShellDockerCommand(finalArgs, cidFile)
			}
		}
	}

	// Upstream transport with working directory support and process group management
	var stdioTransport *uptransport.Stdio
	if c.config.WorkingDir != "" {
		// CRITICAL FIX: Use enhanced CommandFunc with process group management for proper cleanup
		commandFunc := createEnhancedWorkingDirCommandFunc(c, c.config.WorkingDir, c.logger)
		stdioTransport = uptransport.NewStdioWithOptions(finalCommand, envVars, finalArgs,
			uptransport.WithCommandFunc(commandFunc))
	} else {
		// CRITICAL FIX: Use enhanced CommandFunc even without working directory to ensure process groups
		commandFunc := createEnhancedWorkingDirCommandFunc(c, "", c.logger)
		stdioTransport = uptransport.NewStdioWithOptions(finalCommand, envVars, finalArgs,
			uptransport.WithCommandFunc(commandFunc))
	}

	c.client = client.NewClient(stdioTransport)

	// Log final stdio configuration for debugging
	c.logger.Debug("Initialized stdio transport",
		zap.String("server", c.config.Name),
		zap.String("final_command", finalCommand),
		zap.Strings("final_args", finalArgs),
		zap.String("original_command", c.config.Command),
		zap.Strings("original_args", args),
		zap.String("working_dir", c.config.WorkingDir),
		zap.Bool("docker_isolation", c.isDockerCommand))

	// Start stdio transport with a persistent background context so the child
	// process keeps running even if the connect context is short-lived.
	persistentCtx := context.Background()
	if err := c.client.Start(persistentCtx); err != nil {
		return fmt.Errorf("failed to start stdio client: %w", err)
	}

	// CRITICAL FIX: Enable stderr monitoring IMMEDIATELY after starting the process
	// This ensures we capture startup errors (like missing API keys) even if
	// initialization fails with timeout. Previously, stderr monitoring started
	// after successful initialization, so early errors were never logged.
	c.stderr = stdioTransport.Stderr()
	if c.stderr != nil {
		c.StartStderrMonitoring()
		c.logger.Debug("Started early stderr monitoring to capture startup errors",
			zap.String("server", c.config.Name))
	}

	// IMPORTANT: Perform MCP initialize() handshake for stdio transports as well,
	// so c.serverInfo is populated and tool discovery/search can proceed.
	// Use the caller's context with timeout to avoid hanging.
	if err := c.initialize(ctx); err != nil {
		// CRITICAL FIX: Cleanup Docker containers when initialization fails
		// This prevents container accumulation when servers timeout during startup
		if c.isDockerCommand {
			c.logger.Warn("Initialization failed for Docker command - cleaning up container",
				zap.String("server", c.config.Name),
				zap.String("container_name", c.containerName),
				zap.String("container_id", c.containerID),
				zap.Error(err))

			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), dockerCleanupTimeout)
			defer cleanupCancel()

			// Try to cleanup using container name first, then ID, then pattern matching
			if c.containerName != "" {
				c.logger.Debug("Attempting container cleanup by name after init failure")
				if success := c.killDockerContainerByNameWithContext(cleanupCtx, c.containerName); success {
					c.logger.Info("Successfully cleaned up container by name after initialization failure")
				}
			} else if c.containerID != "" {
				c.logger.Debug("Attempting container cleanup by ID after init failure")
				c.killDockerContainerWithContext(cleanupCtx)
			} else {
				c.logger.Debug("Attempting container cleanup by pattern matching after init failure")
				c.killDockerContainerByCommandWithContext(cleanupCtx)
			}
		}

		// CRITICAL FIX: Also cleanup process groups to prevent zombie processes on initialization failure
		if c.processGroupID > 0 {
			c.logger.Warn("Initialization failed - cleaning up process group to prevent zombie processes",
				zap.String("server", c.config.Name),
				zap.Int("pgid", c.processGroupID))

			if err := killProcessGroup(c.processGroupID, c.logger, c.config.Name); err != nil {
				c.logger.Error("Failed to clean up process group after initialization failure",
					zap.String("server", c.config.Name),
					zap.Int("pgid", c.processGroupID),
					zap.Error(err))
			}
			c.processGroupID = 0
		}
		return fmt.Errorf("MCP initialize failed for stdio transport: %w", err)
	}

	// CRITICAL FIX: Extract underlying process from mcp-go transport for lifecycle management
	if c.processCmd != nil && c.processCmd.Process != nil {
		c.logger.Info("Successfully captured process from stdio transport for lifecycle management",
			zap.String("server", c.config.Name),
			zap.Int("pid", c.processCmd.Process.Pid))

		if c.processGroupID <= 0 {
			c.processGroupID = extractProcessGroupID(c.processCmd, c.logger, c.config.Name)
		}
		if c.processGroupID > 0 {
			c.logger.Info("Process group ID tracked for cleanup",
				zap.String("server", c.config.Name),
				zap.Int("pgid", c.processGroupID))
		}
	} else {
		// Try to access the process via reflection as a fallback
		c.logger.Debug("Attempting to extract process from stdio transport via reflection",
			zap.String("server", c.config.Name),
			zap.String("transport_type", fmt.Sprintf("%T", stdioTransport)))

		transportValue := reflect.ValueOf(stdioTransport)
		if transportValue.Kind() == reflect.Ptr {
			transportValue = transportValue.Elem()
		}

		if transportValue.IsValid() {
			for _, fieldName := range []string{"cmd", "process", "proc", "Cmd", "Process", "Proc"} {
				field := transportValue.FieldByName(fieldName)
				if field.IsValid() && field.CanInterface() {
					if cmd, ok := field.Interface().(*exec.Cmd); ok && cmd != nil {
						c.processCmd = cmd
						c.logger.Info("Successfully extracted process from stdio transport for lifecycle management",
							zap.String("server", c.config.Name),
							zap.Int("pid", c.processCmd.Process.Pid))

						c.processGroupID = extractProcessGroupID(cmd, c.logger, c.config.Name)
						if c.processGroupID > 0 {
							c.logger.Info("Process group ID tracked for cleanup",
								zap.String("server", c.config.Name),
								zap.Int("pgid", c.processGroupID))
						}
						break
					}
				}
			}
		}

		if c.processCmd == nil {
			c.logger.Warn("Could not extract process from stdio transport - will use alternative process tracking",
				zap.String("server", c.config.Name),
				zap.String("transport_type", fmt.Sprintf("%T", stdioTransport)))

			// For Docker commands, we can still monitor via container ID and docker ps
			if c.isDockerCommand {
				c.logger.Info("Docker command detected - will monitor via container health checks",
					zap.String("server", c.config.Name))
			}
		}
	}

	// Note: stderr monitoring was already started earlier (right after Start())
	// to capture startup errors before initialization completes

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

	// Extract container name from Docker args for tracking
	c.containerName = c.extractContainerNameFromArgs(dockerRunArgs)

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
		zap.String("container_name", c.containerName),
		zap.String("container_command", containerCommand),
		zap.Strings("container_args", containerArgs),
		zap.Strings("docker_run_args", dockerRunArgs))

	// Log to server-specific log as well
	if c.upstreamLogger != nil {
		c.upstreamLogger.Info("Docker isolation configured",
			zap.String("runtime_type", runtimeType),
			zap.String("container_name", c.containerName),
			zap.String("container_command", containerCommand))
	}

	// CRITICAL FIX: Wrap Docker command with user shell to inherit proper PATH
	// This fixes issues when mcpproxy is launched via Launchpad/GUI where PATH doesn't include Docker
	return c.wrapWithUserShell(cmdDocker, finalArgs)
}

// injectEnvVarsIntoDockerArgs injects environment variables as -e flags into Docker run args
// The flags are inserted after "run" but before the image name
// Example: ["run", "-i", "--rm", "image"] -> ["run", "-e", "KEY=VAL", "-i", "--rm", "image"]
func (c *Client) injectEnvVarsIntoDockerArgs(args []string, envVars map[string]string) []string {
	if len(args) < 2 || args[0] != cmdRun {
		return args
	}

	// Build new args with env vars injected after "run"
	newArgs := make([]string, 0, len(args)+len(envVars)*2)
	newArgs = append(newArgs, args[0]) // "run"

	// Add -e flags for each env var
	for key, value := range envVars {
		newArgs = append(newArgs, "-e", fmt.Sprintf("%s=%s", key, value))
	}

	// Add remaining args (flags and image name)
	newArgs = append(newArgs, args[1:]...)

	return newArgs
}

// insertCidfileIntoShellDockerCommand inserts --cidfile into a shell-wrapped Docker command
func (c *Client) insertCidfileIntoShellDockerCommand(shellArgs []string, cidFile string) []string {
	// Shell args typically look like: ["-l", "-c", "docker run -i --rm mcp/duckduckgo"]
	// Fix: Check for correct shell format - args can be 2 or 3 elements
	if len(shellArgs) < 2 || shellArgs[len(shellArgs)-2] != "-c" {
		// If it's not the expected format, log error and fall back
		c.logger.Error("Unexpected shell command format for Docker cidfile insertion - cannot track container ID",
			zap.String("server", c.config.Name),
			zap.Strings("shell_args", shellArgs),
			zap.String("expected_format", "[shell, -c, docker_command] or [-l, -c, docker_command]"))
		// Don't append cidfile to shell args as it won't work
		return shellArgs
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

// extractContainerNameFromArgs extracts the container name from Docker run arguments
func (c *Client) extractContainerNameFromArgs(dockerArgs []string) string {
	// Look for --name flag in the arguments
	for i, arg := range dockerArgs {
		if arg == "--name" && i+1 < len(dockerArgs) {
			containerName := dockerArgs[i+1]
			c.logger.Debug("Extracted container name from Docker args",
				zap.String("server", c.config.Name),
				zap.String("container_name", containerName))
			return containerName
		}
	}

	c.logger.Warn("Could not extract container name from Docker args - cleanup may be limited",
		zap.String("server", c.config.Name),
		zap.Strings("docker_args", dockerArgs))
	return ""
}

// wrapWithUserShell wraps a command with the user's login shell to inherit full environment
func (c *Client) wrapWithUserShell(command string, args []string) (shellCommand string, shellArgs []string) {
	// Get the user's default shell
	shell, _ := c.envManager.GetSystemEnvVar("SHELL")
	if shell == "" {
		// Fallback to common shells based on OS
		if runtime.GOOS == osWindows {
			shell = "cmd"
		} else {
			shell = pathBinBash // Default fallback
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

	// Return shell with appropriate flags for the OS
	if runtime.GOOS == osWindows {
		// Windows cmd.exe uses /c flag to execute command string
		return shell, []string{"/c", commandString}
	}
	// Unix shells use -l (login) flag to load user's full environment
	// The -c flag executes the command string
	return shell, []string{"-l", "-c", commandString}
}

// shellescape escapes a string for safe shell execution
func shellescape(s string) string {
	if s == "" {
		if runtime.GOOS == osWindows {
			return `""`
		}
		return "''"
	}

	// If string contains no special characters, return as-is
	if runtime.GOOS == osWindows {
		// Windows cmd.exe special characters
		if !strings.ContainsAny(s, " \t\n\r\"&|<>()^%") {
			return s
		}
		// For Windows, use double quotes and escape internal double quotes
		return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
	}
	// Unix shell special characters
	if !strings.ContainsAny(s, " \t\n\r\"'\\$`;&|<>(){}[]?*~") {
		return s
	}
	// Use single quotes and escape any single quotes in the string
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}

// hasCommand checks if a command is available in PATH
func hasCommand(command string) bool {
	_, err := exec.LookPath(command)
	return err == nil
}

// validateWorkingDir checks if the working directory exists and is accessible
// Returns error if directory doesn't exist or isn't accessible
func validateWorkingDir(workingDir string) error {
	if workingDir == "" {
		// Empty working directory is valid (uses current directory)
		return nil
	}

	fi, err := os.Stat(workingDir)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("working directory does not exist: %s", workingDir)
		}
		return fmt.Errorf("cannot access working directory %s: %w", workingDir, err)
	}

	if !fi.IsDir() {
		return fmt.Errorf("working directory path is not a directory: %s", workingDir)
	}

	return nil
}

// createWorkingDirCommandFunc creates a custom CommandFunc that sets the working directory
func createWorkingDirCommandFunc(workingDir string) uptransport.CommandFunc {
	return func(ctx context.Context, command string, env []string, args []string) (*exec.Cmd, error) {
		cmd := exec.CommandContext(ctx, command, args...)
		cmd.Env = env

		// Set working directory if specified
		if workingDir != "" {
			cmd.Dir = workingDir
		}

		return cmd, nil
	}
}

// createEnhancedWorkingDirCommandFunc creates a custom CommandFunc with process group management
func createEnhancedWorkingDirCommandFunc(client *Client, workingDir string, logger *zap.Logger) uptransport.CommandFunc {
	return createProcessGroupCommandFunc(client, workingDir, logger)
}

// connectHTTP establishes HTTP transport connection with auth fallback
func (c *Client) connectHTTP(ctx context.Context) error {
	// Try authentication strategies in order: headers -> no-auth -> OAuth
	authStrategies := []func(context.Context) error{
		c.tryHeadersAuth,
		c.tryNoAuth,
		c.tryOAuthAuth,
	}

	var lastErr error
	for i, authFunc := range authStrategies {
		strategyName := []string{"headers", "no-auth", "OAuth"}[i]
		c.logger.Debug("🔐 Trying authentication strategy",
			zap.Int("strategy_index", i),
			zap.String("strategy", strategyName))

		if err := authFunc(ctx); err != nil {
			lastErr = err
			c.logger.Debug("🚫 Auth strategy failed",
				zap.Int("strategy_index", i),
				zap.String("strategy", strategyName),
				zap.Error(err))

			// For configuration errors (like no headers), always try next strategy
			if c.isConfigError(err) {
				continue
			}

			// For OAuth errors, continue to OAuth strategy
			if c.isOAuthError(err) {
				continue
			}

			// If it's not an auth error, don't try fallback
			if !c.isAuthError(err) {
				return err
			}
			continue
		}
		c.logger.Info("✅ Authentication successful",
			zap.Int("strategy_index", i),
			zap.String("strategy", strategyName))
		return nil
	}

	return fmt.Errorf("all authentication strategies failed, last error: %w", lastErr)
}

// connectSSE establishes SSE transport connection with auth fallback
func (c *Client) connectSSE(ctx context.Context) error {
	// Try authentication strategies in order: headers -> no-auth -> OAuth
	authStrategies := []func(context.Context) error{
		c.trySSEHeadersAuth,
		c.trySSENoAuth,
		c.trySSEOAuthAuth,
	}

	var lastErr error
	for i, authFunc := range authStrategies {
		strategyName := []string{"headers", "no-auth", "OAuth"}[i]
		c.logger.Debug("🔐 Trying SSE authentication strategy",
			zap.Int("strategy_index", i),
			zap.String("strategy", strategyName))

		if err := authFunc(ctx); err != nil {
			lastErr = err
			c.logger.Debug("🚫 SSE auth strategy failed",
				zap.Int("strategy_index", i),
				zap.String("strategy", strategyName),
				zap.Error(err))

			// For configuration errors (like no headers), always try next strategy
			if c.isConfigError(err) {
				continue
			}

			// For OAuth errors, continue to OAuth strategy
			if c.isOAuthError(err) {
				continue
			}

			// If it's not an auth error, don't try fallback
			if !c.isAuthError(err) {
				return err
			}
			continue
		}
		c.logger.Info("✅ SSE Authentication successful",
			zap.Int("strategy_index", i),
			zap.String("strategy", strategyName))
		return nil
	}

	return fmt.Errorf("all SSE authentication strategies failed, last error: %w", lastErr)
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

	// Start the client
	if err := c.client.Start(ctx); err != nil {
		return err
	}

	// CRITICAL FIX: Test initialize() to detect OAuth errors during auth strategy phase
	// This ensures OAuth strategy will be tried if headers-auth fails during MCP initialization
	if err := c.initialize(ctx); err != nil {
		return fmt.Errorf("MCP initialize failed during headers-auth strategy: %w", err)
	}

	return nil
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

	// Start the client
	if err := c.client.Start(ctx); err != nil {
		return err
	}

	// CRITICAL FIX: Test initialize() to detect OAuth errors during auth strategy phase
	// This ensures OAuth strategy will be tried if no-auth fails during MCP initialization
	if err := c.initialize(ctx); err != nil {
		return fmt.Errorf("MCP initialize failed during no-auth strategy: %w", err)
	}

	return nil
}

// tryOAuthAuth attempts OAuth authentication
func (c *Client) tryOAuthAuth(ctx context.Context) error {
	// Use the global OAuth flow coordinator to prevent race conditions
	coordinator := oauth.GetGlobalCoordinator()

	// Try to start a new OAuth flow for this server
	flowCtx, err := coordinator.StartFlow(c.config.Name)
	if err != nil {
		if err == oauth.ErrFlowInProgress {
			// Another flow is already in progress for this server
			// Wait for it to complete instead of starting a new one
			c.logger.Info("⏳ OAuth flow already in progress for this server, waiting for completion",
				zap.String("server", c.config.Name))

			waitErr := coordinator.WaitForFlow(ctx, c.config.Name, oauth.DefaultFlowTimeout)
			if waitErr != nil {
				return fmt.Errorf("waiting for OAuth flow failed: %w", waitErr)
			}

			// Flow completed, try to connect with the new tokens
			c.logger.Info("✅ OAuth flow completed by another goroutine, retrying connection",
				zap.String("server", c.config.Name))
			return nil // The caller will retry the connection
		}
		return fmt.Errorf("failed to start OAuth flow: %w", err)
	}

	// We own this OAuth flow, make sure to end it when done
	// Use named return to capture final error state
	var oauthErr error
	defer func() {
		success := oauthErr == nil
		coordinator.EndFlow(c.config.Name, success, oauthErr)
	}()

	// Update the flow context with the one from the coordinator
	ctx = oauth.WithFlowContext(ctx, flowCtx)
	logger := oauth.CorrelationLoggerWithFlow(ctx, c.logger)

	oauth.LogOAuthFlowStart(logger, c.config.Name, flowCtx.CorrelationID)

	logger.Debug("🚨 OAUTH AUTH FUNCTION CALLED - START")

	// Check if OAuth was recently completed by another client (e.g., tray OAuth, CLI)
	// If so, we should skip the browser flow and try to use the existing tokens
	// This handles cross-process OAuth completion (e.g., CLI completed OAuth, daemon needs to reconnect)
	tokenManager := oauth.GetTokenStoreManager()
	skipBrowserFlow := false

	// First check for valid persisted token directly (handles cross-process OAuth)
	hasTokenPrecheck, hasRefreshPrecheck, tokenExpiredPrecheck := oauth.HasPersistedToken(c.config.Name, c.config.URL, c.storage)
	if hasTokenPrecheck && !tokenExpiredPrecheck {
		logger.Info("🔄 Valid OAuth token found in persistent storage - will skip browser flow if OAuth error occurs",
			zap.String("server", c.config.Name),
			zap.Bool("has_refresh_token", hasRefreshPrecheck))
		skipBrowserFlow = true
	} else if tokenManager.HasRecentOAuthCompletion(c.config.Name) {
		// Also check in-memory completion flag (same-process OAuth)
		logger.Info("🔄 OAuth was recently completed in this process but token may be stale",
			zap.String("server", c.config.Name),
			zap.Bool("has_token", hasTokenPrecheck),
			zap.Bool("token_expired", tokenExpiredPrecheck))
	}

	logger.Debug("🔐 Attempting OAuth authentication",
		zap.String("url", c.config.URL))

	// Mark OAuth as in progress (local state, coordinator handles cross-goroutine coordination)
	c.markOAuthInProgress()

	// Check if tokens already exist for this server (both in-memory and persisted)
	hasInMemoryTokens := tokenManager.HasTokenStore(c.config.Name)
	hasPersistedToken, hasRefreshToken, isExpired := oauth.HasPersistedToken(c.config.Name, c.config.URL, c.storage)
	logger.Info("🔍 HTTP OAuth strategy token status",
		zap.Bool("has_in_memory_token_store", hasInMemoryTokens),
		zap.Bool("has_persisted_token", hasPersistedToken),
		zap.Bool("has_refresh_token", hasRefreshToken),
		zap.Bool("token_expired", isExpired),
		zap.String("strategy", "HTTP OAuth"))

	// If we have a persisted token with refresh_token but it's expired,
	// mcp-go should automatically try to refresh it when we call Start().
	// Log this scenario for debugging.
	if hasPersistedToken && hasRefreshToken && isExpired {
		logger.Info("🔄 Token expired but refresh_token available - mcp-go will attempt automatic refresh",
			zap.String("strategy", "HTTP OAuth"))
	}

	logger.Debug("🔧 Creating OAuth config")

	// Create OAuth config using the oauth package
	oauthConfig := oauth.CreateOAuthConfig(c.config, c.storage)

	c.logger.Error("🚨 oauth.CreateOAuthConfig RETURNED",
		zap.Bool("config_nil", oauthConfig == nil))

	if oauthConfig == nil {
		c.logger.Error("🚨 OAUTH CONFIG IS NIL - RETURNING ERROR")
		oauthErr = fmt.Errorf("failed to create OAuth config")
		return oauthErr
	}

	c.logger.Info("🌟 Starting OAuth authentication flow",
		zap.String("server", c.config.Name),
		zap.String("redirect_uri", oauthConfig.RedirectURI),
		zap.Strings("scopes", oauthConfig.Scopes),
		zap.Bool("pkce_enabled", oauthConfig.PKCEEnabled))

	// Create HTTP transport config with OAuth
	c.logger.Debug("🛠️ Creating HTTP transport config for OAuth")
	httpConfig := transport.CreateHTTPTransportConfig(c.config, oauthConfig)

	c.logger.Debug("🔨 Calling transport.CreateHTTPClient with OAuth config")
	httpClient, err := transport.CreateHTTPClient(httpConfig)
	if err != nil {
		c.logger.Error("💥 Failed to create OAuth HTTP client in transport layer",
			zap.Error(err))
		return fmt.Errorf("failed to create OAuth HTTP client: %w", err)
	}

	c.logger.Debug("✅ HTTP client created, storing in c.client")

	c.logger.Debug("🔗 OAuth HTTP client created, starting connection")
	c.client = httpClient

	// Add detailed logging before starting the OAuth client
	c.logger.Info("🚀 Starting OAuth client - this should trigger browser opening",
		zap.String("server", c.config.Name),
		zap.String("callback_uri", oauthConfig.RedirectURI))

	// Add debug logging to check environment and system capabilities
	c.logger.Debug("🔍 OAuth environment diagnostics",
		zap.String("DISPLAY", os.Getenv("DISPLAY")),
		zap.String("PATH", os.Getenv("PATH")),
		zap.String("GOOS", runtime.GOOS),
		zap.Bool("has_open_command", hasCommand("open")),
		zap.Bool("has_xdg_open", hasCommand("xdg-open")),
		zap.String("BROWSER", os.Getenv("BROWSER")),
		zap.String("XDG_SESSION_TYPE", os.Getenv("XDG_SESSION_TYPE")),
		zap.String("WAYLAND_DISPLAY", os.Getenv("WAYLAND_DISPLAY")),
		zap.Bool("CI", os.Getenv("CI") != ""),
		zap.Bool("HEADLESS", os.Getenv("HEADLESS") != ""),
		zap.Bool("NO_BROWSER", os.Getenv("NO_BROWSER") != ""),
		zap.String("SSH_CLIENT", os.Getenv("SSH_CLIENT")),
		zap.String("SSH_TTY", os.Getenv("SSH_TTY")))

	// Check for conditions that might prevent browser opening
	browserBlockingConditions := []string{}
	if os.Getenv("CI") != "" {
		browserBlockingConditions = append(browserBlockingConditions, "CI=true")
	}
	if os.Getenv("HEADLESS") != "" {
		browserBlockingConditions = append(browserBlockingConditions, "HEADLESS=true")
	}
	if os.Getenv("NO_BROWSER") != "" {
		browserBlockingConditions = append(browserBlockingConditions, "NO_BROWSER=true")
	}
	if os.Getenv("SSH_CLIENT") != "" || os.Getenv("SSH_TTY") != "" {
		browserBlockingConditions = append(browserBlockingConditions, "SSH_session")
	}
	if runtime.GOOS == osLinux && os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
		browserBlockingConditions = append(browserBlockingConditions, "no_GUI_on_linux")
	}

	if len(browserBlockingConditions) > 0 {
		c.logger.Warn("⚠️ Detected conditions that may prevent browser opening",
			zap.String("server", c.config.Name),
			zap.Strings("blocking_conditions", browserBlockingConditions),
			zap.String("recommendation", "You may need to manually open the OAuth URL when prompted"))

		// For remote scenarios, log additional guidance
		if os.Getenv("SSH_CLIENT") != "" || os.Getenv("SSH_TTY") != "" {
			c.logger.Info("📡 Remote SSH session detected - extended OAuth timeout enabled",
				zap.String("server", c.config.Name),
				zap.Duration("timeout", 120*time.Second),
				zap.String("note", "OAuth URL will be displayed for manual opening"))
		}
	}

	// Start the OAuth client and handle OAuth authorization errors properly
	c.logger.Info("🚀 Starting OAuth client - using proper mcp-go OAuth error handling",
		zap.String("server", c.config.Name),
		zap.Duration("callback_timeout", 120*time.Second))

	// Token refresh retry configuration
	refreshConfig := oauth.DefaultRefreshConfig()
	var lastErr error

	// If we have a refresh token, retry Start() with exponential backoff
	// to give mcp-go's automatic token refresh a chance to succeed
	if hasRefreshToken {
		backoff := refreshConfig.InitialBackoff
		for attempt := 1; attempt <= refreshConfig.MaxAttempts; attempt++ {
			oauth.LogTokenRefreshAttempt(c.logger, attempt, refreshConfig.MaxAttempts)

			err = c.client.Start(ctx)
			if err == nil {
				oauth.LogTokenRefreshSuccess(c.logger, time.Duration(attempt)*backoff)
				lastErr = nil
				break
			}

			// If not an OAuth error, don't retry for token refresh
			if !client.IsOAuthAuthorizationRequiredError(err) {
				c.logger.Error("❌ OAuth client start failed with non-OAuth error",
					zap.String("server", c.config.Name),
					zap.Error(err))
				oauthErr = fmt.Errorf("OAuth client start failed: %w", err)
				return oauthErr
			}

			oauth.LogTokenRefreshFailure(c.logger, attempt, err)
			lastErr = err

			// Don't sleep on the last attempt
			if attempt < refreshConfig.MaxAttempts {
				c.logger.Debug("⏳ Waiting before retry",
					zap.String("server", c.config.Name),
					zap.Duration("backoff", backoff),
					zap.Int("attempt", attempt))
				time.Sleep(backoff)
				backoff = min(backoff*2, refreshConfig.MaxBackoff)
			}
		}
	} else {
		// No refresh token, single attempt
		err = c.client.Start(ctx)
		lastErr = err
	}

	// If we still have an error after retries, proceed with browser OAuth flow
	if lastErr != nil {
		// Check if this is an OAuth authorization error that we need to handle manually
		if client.IsOAuthAuthorizationRequiredError(lastErr) {
			// CRITICAL FIX: If we have a valid persisted token (e.g., from CLI OAuth),
			// skip browser flow and return retriable error. The token exists but
			// the mcp-go client needs to pick it up on retry.
			if skipBrowserFlow {
				c.logger.Info("🔄 OAuth authorization error but valid token exists - skipping browser flow",
					zap.String("server", c.config.Name),
					zap.String("tip", "Token should be used on next connection attempt"))

				// Clear in-progress state so retry can proceed
				c.clearOAuthState()

				// Return a retriable error - the managed client will retry and pick up the token
				return fmt.Errorf("OAuth token exists in storage, retry connection to use it: %w", lastErr)
			}

			c.logger.Info("🎯 OAuth authorization required after refresh attempts - starting manual OAuth flow",
				zap.String("server", c.config.Name),
				zap.Bool("had_refresh_token", hasRefreshToken))

			// Handle OAuth authorization manually using the example pattern
			if handleErr := c.handleOAuthAuthorization(ctx, lastErr, oauthConfig); handleErr != nil {
				c.clearOAuthState() // Clear state on OAuth failure
				oauthErr = fmt.Errorf("OAuth authorization failed: %w", handleErr)
				return oauthErr
			}

			// Retry starting the client after OAuth is complete
			c.logger.Info("🔄 Retrying client start after OAuth authorization",
				zap.String("server", c.config.Name))

			err = c.client.Start(ctx)
			if err != nil {
				c.logger.Error("❌ OAuth client start failed after authorization",
					zap.String("server", c.config.Name),
					zap.Error(err))
				oauthErr = fmt.Errorf("OAuth client start failed after authorization: %w", err)
				return oauthErr
			}

			c.logger.Info("✅ OAuth client start successful after authorization",
				zap.String("server", c.config.Name))
		} else {
			c.logger.Error("❌ OAuth client start failed with non-OAuth error",
				zap.String("server", c.config.Name),
				zap.Error(lastErr))
			oauthErr = fmt.Errorf("OAuth client start failed: %w", lastErr)
			return oauthErr
		}
	}

	c.logger.Info("✅ OAuth client started successfully",
		zap.String("server", c.config.Name))

	c.logger.Info("✅ OAuth setup complete - using proper mcp-go OAuth error handling pattern",
		zap.String("server", c.config.Name))

	// CRITICAL FIX: Test initialize() to verify connection and set serverInfo
	// This ensures consistency with other auth strategies and sets c.serverInfo for ListTools
	c.logger.Debug("🔍 Starting MCP initialization after OAuth setup",
		zap.String("server", c.config.Name))

	if err := c.initialize(ctx); err != nil {
		c.logger.Error("❌ MCP initialization failed after OAuth setup",
			zap.String("server", c.config.Name),
			zap.Error(err))

		// Check if this is an OAuth authorization error that we need to handle manually
		if client.IsOAuthAuthorizationRequiredError(err) {
			c.logger.Info("🎯 OAuth authorization required during MCP init - deferring OAuth for background processing",
				zap.String("server", c.config.Name))

			// For daemon mode, defer OAuth to prevent UI blocking
			// The connection will be retried by the managed client retry logic
			// which will eventually complete OAuth in the background
			if c.isDeferOAuthForTray() {
				c.logger.Info("⏳ Deferring OAuth to prevent UI blocking - will retry in background",
					zap.String("server", c.config.Name))

				// Log a user-friendly message about OAuth login options
				c.logger.Info("💡 OAuth login available via Web UI, system tray menu, or CLI command",
					zap.String("server", c.config.Name))

				return &ErrOAuthPending{
					ServerName: c.config.Name,
					ServerURL:  c.config.URL,
					Message:    "login available via Web UI, system tray menu, or 'mcpproxy auth login' CLI command",
				}
			}

			// CRITICAL FIX: If we have a valid persisted token, skip browser flow
			if skipBrowserFlow {
				c.logger.Info("🔄 OAuth authorization error during MCP init but valid token exists - skipping browser flow",
					zap.String("server", c.config.Name),
					zap.String("tip", "Token should be used on next connection attempt"))

				// Clear in-progress state so retry can proceed
				c.clearOAuthState()

				// Return a retriable error - the managed client will retry and pick up the token
				return fmt.Errorf("OAuth token exists in storage, retry connection to use it: %w", err)
			}

			// Clear OAuth state before starting manual flow to prevent "already in progress" errors
			c.clearOAuthState()

			// Handle OAuth authorization manually using the example pattern
			if handleErr := c.handleOAuthAuthorization(ctx, err, oauthConfig); handleErr != nil {
				c.clearOAuthState() // Clear state on OAuth failure
				oauthErr = fmt.Errorf("OAuth authorization during MCP init failed: %w", handleErr)
				return oauthErr
			}

			// Retry MCP initialization after OAuth is complete
			c.logger.Info("🔄 Retrying MCP initialization after OAuth authorization",
				zap.String("server", c.config.Name))

			if retryErr := c.initialize(ctx); retryErr != nil {
				c.logger.Error("❌ MCP initialization failed after OAuth authorization",
					zap.String("server", c.config.Name),
					zap.Error(retryErr))
				oauthErr = fmt.Errorf("MCP initialize failed after OAuth authorization: %w", retryErr)
				return oauthErr
			}

			c.logger.Info("✅ MCP initialization successful after OAuth authorization",
				zap.String("server", c.config.Name))
		} else {
			oauthErr = fmt.Errorf("MCP initialize failed during OAuth strategy: %w", err)
			return oauthErr
		}
	}

	c.logger.Info("✅ MCP initialization completed successfully after OAuth",
		zap.String("server", c.config.Name))

	return nil
}

// trySSEHeadersAuth attempts SSE authentication using configured headers
func (c *Client) trySSEHeadersAuth(ctx context.Context) error {
	if len(c.config.Headers) == 0 {
		return fmt.Errorf("no headers configured")
	}

	httpConfig := transport.CreateHTTPTransportConfig(c.config, nil)
	sseClient, err := transport.CreateSSEClient(httpConfig)
	if err != nil {
		return fmt.Errorf("failed to create SSE client with headers: %w", err)
	}

	c.client = sseClient

	// Register connection lost handler for SSE transport to detect GOAWAY/disconnects
	c.client.OnConnectionLost(func(err error) {
		c.logger.Warn("⚠️ SSE connection lost detected",
			zap.String("server", c.config.Name),
			zap.Error(err),
			zap.String("transport", "sse"),
			zap.String("note", "Connection dropped by server or network - will attempt reconnection"))
	})

	// Start the client with persistent context so SSE stream keeps running
	// even if the connect context is short-lived (same as stdio transport).
	// SSE stream runs in a background goroutine and needs context to stay alive.
	persistentCtx := context.Background()
	if err := c.client.Start(persistentCtx); err != nil {
		return err
	}

	// CRITICAL FIX: Test initialize() to detect OAuth errors during auth strategy phase
	// This ensures OAuth strategy will be tried if SSE headers-auth fails during MCP initialization
	// Use caller's context for initialize() to respect timeouts
	if err := c.initialize(ctx); err != nil {
		return fmt.Errorf("MCP initialize failed during SSE headers-auth strategy: %w", err)
	}

	return nil
}

// trySSENoAuth attempts SSE connection without authentication
func (c *Client) trySSENoAuth(ctx context.Context) error {
	// Create config without headers
	configNoAuth := *c.config
	configNoAuth.Headers = nil

	httpConfig := transport.CreateHTTPTransportConfig(&configNoAuth, nil)
	sseClient, err := transport.CreateSSEClient(httpConfig)
	if err != nil {
		return fmt.Errorf("failed to create SSE client without auth: %w", err)
	}

	c.client = sseClient

	// Register connection lost handler for SSE transport to detect GOAWAY/disconnects
	c.client.OnConnectionLost(func(err error) {
		c.logger.Warn("⚠️ SSE connection lost detected",
			zap.String("server", c.config.Name),
			zap.Error(err),
			zap.String("transport", "sse"),
			zap.String("note", "Connection dropped by server or network - will attempt reconnection"))
	})

	// Start the client with persistent context so SSE stream keeps running
	// even if the connect context is short-lived (same as stdio transport).
	// SSE stream runs in a background goroutine and needs context to stay alive.
	persistentCtx := context.Background()
	if err := c.client.Start(persistentCtx); err != nil {
		return err
	}

	// CRITICAL FIX: Test initialize() to detect OAuth errors during auth strategy phase
	// This ensures OAuth strategy will be tried if SSE no-auth fails during MCP initialization
	// Use caller's context for initialize() to respect timeouts
	if err := c.initialize(ctx); err != nil {
		return fmt.Errorf("MCP initialize failed during SSE no-auth strategy: %w", err)
	}

	return nil
}

// trySSEOAuthAuth attempts SSE OAuth authentication
func (c *Client) trySSEOAuthAuth(ctx context.Context) error {
	// Use the global OAuth flow coordinator to prevent race conditions
	coordinator := oauth.GetGlobalCoordinator()

	// Try to start a new OAuth flow for this server
	flowCtx, err := coordinator.StartFlow(c.config.Name)
	if err != nil {
		if err == oauth.ErrFlowInProgress {
			// Another flow is already in progress for this server
			// Wait for it to complete instead of starting a new one
			c.logger.Info("⏳ SSE OAuth flow already in progress for this server, waiting for completion",
				zap.String("server", c.config.Name))

			waitErr := coordinator.WaitForFlow(ctx, c.config.Name, oauth.DefaultFlowTimeout)
			if waitErr != nil {
				return fmt.Errorf("waiting for SSE OAuth flow failed: %w", waitErr)
			}

			// Flow completed, try to connect with the new tokens
			c.logger.Info("✅ SSE OAuth flow completed by another goroutine, retrying connection",
				zap.String("server", c.config.Name))
			return nil // The caller will retry the connection
		}
		return fmt.Errorf("failed to start SSE OAuth flow: %w", err)
	}

	// We own this OAuth flow, make sure to end it when done
	// Use named return to capture final error state
	var oauthErr error
	defer func() {
		success := oauthErr == nil
		coordinator.EndFlow(c.config.Name, success, oauthErr)
	}()

	// Update the flow context with the one from the coordinator
	ctx = oauth.WithFlowContext(ctx, flowCtx)
	logger := oauth.CorrelationLoggerWithFlow(ctx, c.logger)

	oauth.LogOAuthFlowStart(logger, c.config.Name, flowCtx.CorrelationID)

	// Check if OAuth was recently completed by another client (e.g., tray OAuth, CLI)
	// This handles cross-process OAuth completion (e.g., CLI completed OAuth, daemon needs to reconnect)
	tokenManager := oauth.GetTokenStoreManager()
	skipBrowserFlow := false

	// First check for valid persisted token directly (handles cross-process OAuth)
	hasTokenPrecheck, hasRefreshPrecheck, tokenExpiredPrecheck := oauth.HasPersistedToken(c.config.Name, c.config.URL, c.storage)
	if hasTokenPrecheck && !tokenExpiredPrecheck {
		logger.Info("🔄 Valid OAuth token found in persistent storage - will skip browser flow if OAuth error occurs",
			zap.String("server", c.config.Name),
			zap.Bool("has_refresh_token", hasRefreshPrecheck))
		skipBrowserFlow = true
	} else if tokenManager.HasRecentOAuthCompletion(c.config.Name) {
		// Also check in-memory completion flag (same-process OAuth)
		logger.Info("🔄 SSE OAuth was recently completed in this process but token may be stale",
			zap.String("server", c.config.Name),
			zap.Bool("has_token", hasTokenPrecheck),
			zap.Bool("token_expired", tokenExpiredPrecheck))
	}

	logger.Debug("🔐 Attempting SSE OAuth authentication",
		zap.String("url", c.config.URL))

	// Mark OAuth as in progress
	c.markOAuthInProgress()

	// Check if tokens already exist for this server (both in-memory and persisted)
	hasInMemoryTokens := tokenManager.HasTokenStore(c.config.Name)
	hasPersistedToken, hasRefreshToken, isExpired := oauth.HasPersistedToken(c.config.Name, c.config.URL, c.storage)
	logger.Info("🔍 SSE OAuth strategy token status",
		zap.Bool("has_in_memory_token_store", hasInMemoryTokens),
		zap.Bool("has_persisted_token", hasPersistedToken),
		zap.Bool("has_refresh_token", hasRefreshToken),
		zap.Bool("token_expired", isExpired),
		zap.String("strategy", "SSE OAuth"))

	// If we have a persisted token with refresh_token but it's expired,
	// mcp-go should automatically try to refresh it when we call Start().
	// Log this scenario for debugging.
	if hasPersistedToken && hasRefreshToken && isExpired {
		logger.Info("🔄 Token expired but refresh_token available - mcp-go will attempt automatic refresh",
			zap.String("strategy", "SSE OAuth"))
	}

	// Create OAuth config using the oauth package
	oauthConfig := oauth.CreateOAuthConfig(c.config, c.storage)
	if oauthConfig == nil {
		oauthErr = fmt.Errorf("failed to create OAuth config")
		return oauthErr
	}

	c.logger.Info("🌟 Starting SSE OAuth authentication flow",
		zap.String("server", c.config.Name),
		zap.String("redirect_uri", oauthConfig.RedirectURI),
		zap.Strings("scopes", oauthConfig.Scopes),
		zap.Bool("pkce_enabled", oauthConfig.PKCEEnabled))

	// Create SSE transport config with OAuth
	c.logger.Debug("🛠️ Creating SSE transport config for OAuth")
	httpConfig := transport.CreateHTTPTransportConfig(c.config, oauthConfig)

	c.logger.Debug("🔨 Calling transport.CreateSSEClient with OAuth config")
	sseClient, err := transport.CreateSSEClient(httpConfig)
	if err != nil {
		c.logger.Error("💥 Failed to create OAuth SSE client in transport layer",
			zap.Error(err))
		return fmt.Errorf("failed to create OAuth SSE client: %w", err)
	}

	c.logger.Debug("✅ SSE client created, storing in c.client")

	c.logger.Debug("🔗 OAuth SSE client created, starting connection")
	c.client = sseClient

	// Register connection lost handler for SSE transport to detect GOAWAY/disconnects
	c.client.OnConnectionLost(func(err error) {
		c.logger.Warn("⚠️ SSE OAuth connection lost detected",
			zap.String("server", c.config.Name),
			zap.Error(err),
			zap.String("transport", "sse-oauth"),
			zap.String("note", "Connection dropped by server or network - will attempt reconnection"))
	})

	// Add detailed logging before starting the OAuth client
	c.logger.Info("🚀 Starting OAuth SSE client - this should trigger browser opening",
		zap.String("server", c.config.Name),
		zap.String("callback_uri", oauthConfig.RedirectURI))

	// Add debug logging to check environment and system capabilities
	c.logger.Debug("🔍 SSE OAuth environment diagnostics",
		zap.String("DISPLAY", os.Getenv("DISPLAY")),
		zap.String("PATH", os.Getenv("PATH")),
		zap.String("GOOS", runtime.GOOS),
		zap.Bool("has_open_command", hasCommand("open")),
		zap.Bool("has_xdg_open", hasCommand("xdg-open")),
		zap.String("BROWSER", os.Getenv("BROWSER")),
		zap.String("XDG_SESSION_TYPE", os.Getenv("XDG_SESSION_TYPE")),
		zap.String("WAYLAND_DISPLAY", os.Getenv("WAYLAND_DISPLAY")),
		zap.Bool("CI", os.Getenv("CI") != ""),
		zap.Bool("HEADLESS", os.Getenv("HEADLESS") != ""))

	// Detect conditions that might prevent browser opening
	var browserBlockingConditions []string
	if os.Getenv("CI") != "" {
		browserBlockingConditions = append(browserBlockingConditions, "CI=true")
	}
	if os.Getenv("HEADLESS") != "" {
		browserBlockingConditions = append(browserBlockingConditions, "HEADLESS=true")
	}
	if os.Getenv("NO_BROWSER") != "" {
		browserBlockingConditions = append(browserBlockingConditions, "NO_BROWSER=true")
	}
	if os.Getenv("SSH_CLIENT") != "" || os.Getenv("SSH_TTY") != "" {
		browserBlockingConditions = append(browserBlockingConditions, "SSH_session")
	}
	if runtime.GOOS == osLinux && os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
		browserBlockingConditions = append(browserBlockingConditions, "no_GUI_on_linux")
	}

	if len(browserBlockingConditions) > 0 {
		c.logger.Warn("⚠️ Detected conditions that may prevent browser opening for SSE OAuth",
			zap.String("server", c.config.Name),
			zap.Strings("blocking_conditions", browserBlockingConditions),
			zap.String("recommendation", "You may need to manually open the OAuth URL when prompted"))

		// For remote scenarios, log additional guidance
		if os.Getenv("SSH_CLIENT") != "" || os.Getenv("SSH_TTY") != "" {
			c.logger.Info("📡 Remote SSH session detected - extended OAuth timeout enabled",
				zap.String("server", c.config.Name),
				zap.Duration("timeout", 120*time.Second),
				zap.String("note", "OAuth URL will be displayed for manual opening"))
		}
	}

	// Start the OAuth client and handle OAuth authorization errors properly
	c.logger.Info("🚀 Starting SSE OAuth client - using proper mcp-go OAuth error handling",
		zap.String("server", c.config.Name),
		zap.Duration("callback_timeout", 120*time.Second))

	// Start the client with persistent context so SSE stream keeps running
	// even if the connect context is short-lived (same as stdio transport).
	// SSE stream runs in a background goroutine and needs context to stay alive.
	persistentCtx := context.Background()
	c.logger.Debug("🔍 Starting SSE OAuth client with persistent context",
		zap.String("server", c.config.Name))

	// Token refresh retry configuration
	refreshConfig := oauth.DefaultRefreshConfig()
	var lastErr error

	// If we have a refresh token, retry Start() with exponential backoff
	// to give mcp-go's automatic token refresh a chance to succeed
	if hasRefreshToken {
		backoff := refreshConfig.InitialBackoff
		for attempt := 1; attempt <= refreshConfig.MaxAttempts; attempt++ {
			oauth.LogTokenRefreshAttempt(c.logger, attempt, refreshConfig.MaxAttempts)

			err = c.client.Start(persistentCtx)
			if err == nil {
				oauth.LogTokenRefreshSuccess(c.logger, time.Duration(attempt)*backoff)
				lastErr = nil
				break
			}

			// If not an OAuth error, don't retry for token refresh
			if !client.IsOAuthAuthorizationRequiredError(err) {
				c.logger.Error("❌ SSE OAuth client start failed with non-OAuth error",
					zap.String("server", c.config.Name),
					zap.Error(err))
				oauthErr = fmt.Errorf("SSE OAuth client start failed: %w", err)
				return oauthErr
			}

			oauth.LogTokenRefreshFailure(c.logger, attempt, err)
			lastErr = err

			// Don't sleep on the last attempt
			if attempt < refreshConfig.MaxAttempts {
				c.logger.Debug("⏳ Waiting before retry",
					zap.String("server", c.config.Name),
					zap.Duration("backoff", backoff),
					zap.Int("attempt", attempt))
				time.Sleep(backoff)
				backoff = min(backoff*2, refreshConfig.MaxBackoff)
			}
		}
	} else {
		// No refresh token, single attempt
		err = c.client.Start(persistentCtx)
		lastErr = err
	}

	// If we still have an error after retries, proceed with browser OAuth flow
	if lastErr != nil {
		// Check if this is an OAuth authorization error that we need to handle manually
		if client.IsOAuthAuthorizationRequiredError(lastErr) {
			// CRITICAL FIX: If we have a valid persisted token (e.g., from CLI OAuth),
			// skip browser flow and return retriable error. The token exists but
			// the mcp-go client needs to pick it up on retry.
			if skipBrowserFlow {
				c.logger.Info("🔄 SSE OAuth authorization error but valid token exists - skipping browser flow",
					zap.String("server", c.config.Name),
					zap.String("tip", "Token should be used on next connection attempt"))

				// Clear in-progress state so retry can proceed
				c.clearOAuthState()

				// Return a retriable error - the managed client will retry and pick up the token
				return fmt.Errorf("OAuth token exists in storage, retry connection to use it: %w", lastErr)
			}

			c.logger.Info("🎯 SSE OAuth authorization required after refresh attempts - starting manual OAuth flow",
				zap.String("server", c.config.Name),
				zap.Bool("had_refresh_token", hasRefreshToken))

			// Handle OAuth authorization manually using the example pattern
			if handleErr := c.handleOAuthAuthorization(ctx, lastErr, oauthConfig); handleErr != nil {
				c.clearOAuthState() // Clear state on OAuth failure
				oauthErr = fmt.Errorf("SSE OAuth authorization failed: %w", handleErr)
				return oauthErr
			}

			// Create a fresh context for the retry to avoid cancellation issues
			c.logger.Info("🔄 Retrying SSE client start after OAuth authorization with fresh context",
				zap.String("server", c.config.Name))

			retryCtx := context.Background() // Use fresh context to avoid cancellation
			err = c.client.Start(retryCtx)
			if err != nil {
				c.logger.Error("❌ SSE OAuth client start failed after authorization",
					zap.String("server", c.config.Name),
					zap.Error(err))
				oauthErr = fmt.Errorf("SSE OAuth client start failed after authorization: %w", err)
				return oauthErr
			}
		} else {
			c.logger.Error("❌ SSE OAuth client start failed with non-OAuth error",
				zap.String("server", c.config.Name),
				zap.Error(lastErr))
			oauthErr = fmt.Errorf("SSE OAuth client start failed: %w", lastErr)
			return oauthErr
		}
	}

	c.logger.Info("✅ SSE OAuth client started successfully",
		zap.String("server", c.config.Name))

	c.logger.Info("✅ SSE OAuth setup complete - using proper mcp-go OAuth error handling pattern",
		zap.String("server", c.config.Name))

	// CRITICAL FIX: Test initialize() to verify connection and set serverInfo
	// This ensures consistency with other auth strategies and sets c.serverInfo for ListTools
	if err := c.initialize(ctx); err != nil {
		oauthErr = fmt.Errorf("MCP initialize failed during SSE OAuth strategy: %w", err)
		return oauthErr
	}

	return nil
}

// isOAuthError checks if the error is OAuth-related (actual authentication failure)
func (c *Client) isOAuthError(err error) bool {
	if err == nil {
		return false
	}

	errStr := err.Error()
	oauthErrors := []string{
		"invalid_token",
		"invalid_grant",
		"access_denied",
		"unauthorized",
		"401", // HTTP 401 Unauthorized
		"Missing or invalid access token",
		"OAuth authentication failed",
		"oauth timeout",
		"oauth error",
	}

	for _, oauthErr := range oauthErrors {
		if containsString(errStr, oauthErr) {
			return true
		}
	}

	return false
}

// isAuthError checks if error indicates authentication failure (non-OAuth)
func (c *Client) isAuthError(err error) bool {
	if err == nil {
		return false
	}

	// Don't catch OAuth errors here - they should be handled by isOAuthError() first
	if c.isOAuthError(err) {
		return false
	}

	errStr := err.Error()
	return containsAny(errStr, []string{
		"403", "Forbidden",
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

		// CRITICAL FIX: Additional cleanup for direct initialize() calls
		// This handles cases where initialize() is called independently
		if c.isDockerCommand {
			c.logger.Debug("Direct initialization failed for Docker command - cleanup may be handled by caller",
				zap.String("server", c.config.Name),
				zap.String("container_name", c.containerName),
				zap.String("container_id", c.containerID),
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

	// CRITICAL FIX: Always perform cleanup, even if not fully connected
	// Servers in "Connecting" state may have Docker containers running
	wasConnected := c.connected

	c.logger.Info("Disconnecting from upstream MCP server",
		zap.Bool("was_connected", wasConnected))

	// Log disconnection to server-specific log
	if c.upstreamLogger != nil {
		c.upstreamLogger.Info("Disconnecting from server",
			zap.Bool("was_connected", wasConnected))
	}

	// Stop stderr monitoring before closing client
	c.StopStderrMonitoring()

	// Stop process monitoring before closing client
	c.StopProcessMonitoring()

	// For Docker containers, kill the container before closing the client
	// IMPORTANT: Do this even if not fully connected, as containers may be running
	if c.isDockerCommand {
		c.logger.Debug("Disconnecting Docker command, attempting container cleanup",
			zap.String("server", c.config.Name),
			zap.Bool("has_container_id", c.containerID != ""))

		// Create a fresh context for Docker cleanup with its own timeout
		// This ensures cleanup can complete even if the main context expires
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), dockerCleanupTimeout)
		defer cleanupCancel()

		if c.containerID != "" {
			c.logger.Debug("Cleaning up Docker container by ID",
				zap.String("server", c.config.Name),
				zap.String("container_id", c.containerID))
			c.killDockerContainerWithContext(cleanupCtx)
			c.logger.Debug("Docker container cleanup by ID completed",
				zap.String("server", c.config.Name))
		} else if c.containerName != "" {
			c.logger.Debug("Cleaning up Docker container by name",
				zap.String("server", c.config.Name),
				zap.String("container_name", c.containerName))
			c.killDockerContainerByNameWithContext(cleanupCtx, c.containerName)
			c.logger.Debug("Docker container cleanup by name completed",
				zap.String("server", c.config.Name))
		} else {
			c.logger.Debug("No container ID or name available, using pattern-based cleanup method",
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

	// CRITICAL FIX: Clean up process group to prevent zombie processes
	if c.processGroupID > 0 {
		c.logger.Info("Cleaning up process group to prevent zombie processes",
			zap.String("server", c.config.Name),
			zap.Int("pgid", c.processGroupID))

		if err := killProcessGroup(c.processGroupID, c.logger, c.config.Name); err != nil {
			c.logger.Error("Failed to clean up process group",
				zap.String("server", c.config.Name),
				zap.Int("pgid", c.processGroupID),
				zap.Error(err))
		} else {
			c.logger.Info("Process group cleanup completed successfully",
				zap.String("server", c.config.Name),
				zap.Int("pgid", c.processGroupID))
		}

		// Reset process group ID after cleanup
		c.processGroupID = 0
	}

	// Close MCP client if it exists
	if c.client != nil {
		c.logger.Debug("Closing MCP client connection",
			zap.String("server", c.config.Name))

		closeDone := make(chan struct{})
		go func() {
			c.client.Close()
			close(closeDone)
		}()

		select {
		case <-closeDone:
			c.logger.Debug("MCP client connection closed",
				zap.String("server", c.config.Name))
		case <-time.After(2 * time.Second):
			c.logger.Warn("MCP client close timed out, forcing shutdown",
				zap.String("server", c.config.Name))

			// Attempt process group cleanup if available
			if c.processGroupID > 0 {
				_ = killProcessGroup(c.processGroupID, c.logger, c.config.Name)
			}

			if c.processCmd != nil && c.processCmd.Process != nil {
				if err := c.processCmd.Process.Kill(); err != nil {
					c.logger.Warn("Failed to kill stdio process after close timeout",
						zap.String("server", c.config.Name),
						zap.Error(err))
				} else {
					c.logger.Info("Killed stdio process after close timeout",
						zap.String("server", c.config.Name))
				}
			}
		}
	} else {
		c.logger.Debug("No MCP client to close (may be in connecting state)",
			zap.String("server", c.config.Name))
	}

	c.client = nil
	c.serverInfo = nil
	c.connected = false

	// Clear cached tools on disconnect
	c.cachedTools = nil

	c.logger.Debug("Disconnect completed successfully",
		zap.String("server", c.config.Name))
	return nil
}

// handleOAuthAuthorization handles the manual OAuth flow following the mcp-go example pattern
func (c *Client) handleOAuthAuthorization(ctx context.Context, authErr error, oauthConfig *client.OAuthConfig) error {
	// Check if OAuth is already in progress to prevent duplicate flows (CRITICAL FIX for Phase 1)
	if c.isOAuthInProgress() {
		c.logger.Warn("⚠️ OAuth authorization already in progress, skipping duplicate attempt",
			zap.String("server", c.config.Name))
		return fmt.Errorf("OAuth authorization already in progress for %s", c.config.Name)
	}

	// Mark OAuth as in progress to prevent concurrent attempts
	c.markOAuthInProgress()
	defer func() {
		// Clear OAuth progress state on exit (success or failure)
		c.oauthMu.Lock()
		c.oauthInProgress = false
		c.oauthMu.Unlock()
	}()

	c.logger.Info("🔐 Starting manual OAuth authorization flow",
		zap.String("server", c.config.Name))

	// Get the OAuth handler from the error (as shown in the example)
	oauthHandler := client.GetOAuthHandler(authErr)
	if oauthHandler == nil {
		return fmt.Errorf("failed to get OAuth handler from error")
	}

	c.logger.Info("✅ OAuth handler obtained from error",
		zap.String("server", c.config.Name))

	// Generate PKCE code verifier and challenge
	codeVerifier, err := client.GenerateCodeVerifier()
	if err != nil {
		return fmt.Errorf("failed to generate code verifier: %w", err)
	}
	codeChallenge := client.GenerateCodeChallenge(codeVerifier)

	// Generate state parameter
	state, err := client.GenerateState()
	if err != nil {
		return fmt.Errorf("failed to generate state: %w", err)
	}

	c.logger.Info("🔑 Generated PKCE and state parameters",
		zap.String("server", c.config.Name),
		zap.String("state", state))

	// Check if OAuth credentials are available (either from config or persisted DCR)
	// oauthConfig.ClientID may contain persisted DCR credentials loaded by CreateOAuthConfig()
	hasStaticCredentials := c.config.OAuth != nil && c.config.OAuth.ClientID != ""
	hasPersistedCredentials := oauthConfig.ClientID != ""

	// Determine OAuth mode and attempt registration if needed
	var oauthMode string
	if hasStaticCredentials {
		// Skip DCR when static credentials are provided in config
		oauthMode = "static credentials"
		c.logger.Info("⏩ Skipping Dynamic Client Registration (static credentials provided)",
			zap.String("server", c.config.Name),
			zap.String("client_id", c.config.OAuth.ClientID))
	} else if hasPersistedCredentials {
		// Skip DCR when we have persisted DCR credentials from a previous OAuth flow
		oauthMode = "persisted DCR credentials"
		c.logger.Info("⏩ Skipping Dynamic Client Registration (using persisted DCR credentials)",
			zap.String("server", c.config.Name),
			zap.String("client_id", oauthConfig.ClientID))
	} else {
		// Attempt DCR for servers without static credentials
		c.logger.Info("📋 Attempting Dynamic Client Registration (optional)",
			zap.String("server", c.config.Name))
		var regErr error
		func() {
			defer func() {
				if r := recover(); r != nil {
					c.logger.Warn("OAuth RegisterClient panicked - server metadata missing or malformed",
						zap.String("server", c.config.Name),
						zap.Any("panic", r))
					regErr = fmt.Errorf("server does not support dynamic client registration: metadata missing")
				}
			}()
			regErr = oauthHandler.RegisterClient(ctx, "mcpproxy-go")
		}()

		if regErr != nil {
			// DCR failed - proceed with public client OAuth (PKCE without client_id)
			oauthMode = "public client (PKCE)"
			c.logger.Warn("⚠️ Dynamic Client Registration not supported - using public client OAuth with PKCE",
				zap.String("server", c.config.Name),
				zap.Error(regErr))
			c.logger.Info("💡 Proceeding with public client authentication (no client_id required)",
				zap.String("server", c.config.Name),
				zap.String("mode", "OAuth 2.1 public client with PKCE"),
				zap.Strings("scopes", oauthConfig.Scopes))
		} else {
			oauthMode = "dynamic client registration"

			// Persist DCR credentials for token refresh
			clientID := oauthHandler.GetClientID()
			clientSecret := oauthHandler.GetClientSecret()
			if c.storage != nil && clientID != "" {
				serverKey := oauth.GenerateServerKey(c.config.Name, c.config.URL)
				if err := c.storage.UpdateOAuthClientCredentials(serverKey, clientID, clientSecret); err != nil {
					c.logger.Warn("Failed to persist DCR credentials - token refresh may fail later",
						zap.String("server", c.config.Name),
						zap.Error(err))
				} else {
					c.logger.Info("✅ DCR credentials persisted for token refresh",
						zap.String("server", c.config.Name),
						zap.String("client_id", clientID))
				}
			}

			c.logger.Info("✅ Dynamic Client Registration successful",
				zap.String("server", c.config.Name),
				zap.String("client_id", clientID))
		}
	}

	// Continue with OAuth flow regardless of DCR result
	// Public client OAuth (RFC 8252) with PKCE doesn't require client_id
	// If server doesn't support this, it will reject the authorization request

	c.logger.Info("🌟 Starting OAuth authentication flow",
		zap.String("server", c.config.Name),
		zap.Strings("scopes", oauthConfig.Scopes),
		zap.Bool("pkce_enabled", true),
		zap.String("mode", oauthMode))

	// Get the authorization URL
	// Works with: static credentials, DCR, or public client OAuth (empty client_id + PKCE)
	var authURL string
	var authURLErr error
	func() {
		defer func() {
			if r := recover(); r != nil {
				c.logger.Error("GetAuthorizationURL panicked",
					zap.String("server", c.config.Name),
					zap.Any("panic", r))
				authURLErr = fmt.Errorf("failed to get authorization URL: internal error (panic recovered)")
			}
		}()
		authURL, authURLErr = oauthHandler.GetAuthorizationURL(ctx, state, codeChallenge)
	}()

	if authURLErr != nil {
		return fmt.Errorf("failed to get authorization URL: %w", authURLErr)
	}

	// Append extra OAuth parameters to authorization URL (RFC 8707 resource, etc.)
	if c.config.OAuth != nil && len(c.config.OAuth.ExtraParams) > 0 {
		parsedURL, err := url.Parse(authURL)
		if err == nil {
			query := parsedURL.Query()
			for key, value := range c.config.OAuth.ExtraParams {
				query.Set(key, value)
				c.logger.Debug("Added extra OAuth parameter to authorization URL",
					zap.String("server", c.config.Name),
					zap.String("key", key),
					zap.String("value", value))
			}
			parsedURL.RawQuery = query.Encode()
			authURL = parsedURL.String()
			c.logger.Info("✅ Appended extra OAuth parameters to authorization URL",
				zap.String("server", c.config.Name),
				zap.Int("extra_params_count", len(c.config.OAuth.ExtraParams)))
		} else {
			c.logger.Warn("Failed to parse authorization URL for extra params",
				zap.String("server", c.config.Name),
				zap.Error(err))
		}
	}

	// Always log the computed authorization URL so users can copy/paste if auto-launch fails.
	c.logger.Info("OAuth authorization URL ready",
		zap.String("server", c.config.Name),
		zap.String("auth_url", authURL))
	fmt.Printf("OAuth login URL for %s:\n%s\n", c.config.Name, authURL)

	// Check if this is a manual OAuth flow using the proper context key
	isManualFlow := c.isManualOAuthFlow(ctx)

	// Rate limit browser opening to prevent spam (CRITICAL FIX for Phase 1)
	// Skip rate limiting for manual OAuth flows
	browserRateLimit := 5 * time.Minute
	c.oauthMu.RLock()
	timeSinceLastBrowser := time.Since(c.lastOAuthTimestamp)
	c.oauthMu.RUnlock()

	if !isManualFlow && timeSinceLastBrowser < browserRateLimit {
		c.logger.Warn("⏱️ Browser opening rate limited - OAuth attempt too soon after previous attempt",
			zap.String("server", c.config.Name),
			zap.Duration("time_since_last", timeSinceLastBrowser),
			zap.Duration("rate_limit", browserRateLimit),
			zap.String("auth_url", authURL))

		fmt.Printf("OAuth authorization required for %s, but browser opening is rate limited.\n", c.config.Name)
		fmt.Printf("Please open the following URL manually in your browser: %s\n", authURL)
	} else {
		if isManualFlow {
			c.logger.Info("🎯 Manual OAuth flow detected - bypassing rate limiting",
				zap.String("server", c.config.Name),
				zap.Duration("time_since_last", timeSinceLastBrowser))
		}

		// Open the browser to the authorization URL
		c.logger.Info("🌐 Opening browser for OAuth authorization",
			zap.String("server", c.config.Name),
			zap.String("auth_url", authURL))

		if err := c.openBrowser(authURL); err != nil {
			c.logger.Warn("Failed to open browser automatically, please open manually",
				zap.String("server", c.config.Name),
				zap.String("url", authURL),
				zap.Error(err))
			fmt.Printf("Please open the following URL in your browser: %s\n", authURL)
		}

		// Update the timestamp to track browser opening for rate limiting
		c.oauthMu.Lock()
		c.lastOAuthTimestamp = time.Now()
		c.oauthMu.Unlock()
	}

	// Wait for the callback using our callback server coordination system
	waitStartTime := time.Now()
	c.logger.Info("⏳ Waiting for OAuth authorization callback...",
		zap.String("server", c.config.Name),
		zap.Duration("timeout", 120*time.Second),
		zap.Time("wait_start", waitStartTime))

	// Get our callback server that was started in OAuth config creation
	callbackServer, exists := oauth.GetCallbackServer(c.config.Name)
	if !exists {
		return fmt.Errorf("callback server not found for %s", c.config.Name)
	}

	// Wait for the authorization code with extended timeout for remote/systemd scenarios
	select {
	case params := <-callbackServer.CallbackChan:
		waitDuration := time.Since(waitStartTime)
		c.logger.Info("🎯 OAuth callback received",
			zap.String("server", c.config.Name),
			zap.Duration("wait_duration", waitDuration),
			zap.String("note", fmt.Sprintf("User completed authorization in %.1f seconds", waitDuration.Seconds())))

		// Verify state parameter
		if params["state"] != state {
			return fmt.Errorf("state mismatch: expected %s, got %s", state, params["state"])
		}

		// Get authorization code
		code := params["code"]
		if code == "" {
			if params["error"] != "" {
				return fmt.Errorf("OAuth authorization failed: %s - %s", params["error"], params["error_description"])
			}
			return fmt.Errorf("no authorization code received")
		}

		// Exchange the authorization code for a token
		c.logger.Info("🔄 Exchanging authorization code for token",
			zap.String("server", c.config.Name),
			zap.String("code", code[:10]+"..."))

		err = oauthHandler.ProcessAuthorizationResponse(ctx, code, state, codeVerifier)
		if err != nil {
			c.logger.Error("❌ Failed to process authorization response",
				zap.String("server", c.config.Name),
				zap.Error(err))
			return fmt.Errorf("failed to process authorization response: %w", err)
		}

		c.logger.Info("✅ OAuth authorization successful - token obtained and processed",
			zap.String("server", c.config.Name))

		// Mark OAuth as complete to prevent retry loops
		c.markOAuthComplete()

		// Record OAuth completion in global token manager for other clients
		tokenManager := oauth.GetTokenStoreManager()
		tokenManager.MarkOAuthCompleted(c.config.Name)

		return nil

	case <-time.After(120 * time.Second):
		c.logger.Warn("⏱️ OAuth authorization timeout - user did not complete authorization within 120 seconds",
			zap.String("server", c.config.Name),
			zap.String("note", "Extended timeout for remote/systemd scenarios where manual browser opening may be needed"))
		return fmt.Errorf("OAuth authorization timeout - user did not complete authorization within 120 seconds (extended for remote access)")
	case <-ctx.Done():
		return ctx.Err()
	}
}

// isOAuthInProgress checks if OAuth is in progress
func (c *Client) isOAuthInProgress() bool {
	c.oauthMu.RLock()
	defer c.oauthMu.RUnlock()
	return c.oauthInProgress
}

// markOAuthInProgress marks OAuth as in progress
func (c *Client) markOAuthInProgress() {
	c.oauthMu.Lock()
	defer c.oauthMu.Unlock()
	c.oauthInProgress = true
	c.lastOAuthTimestamp = time.Now()
}

// markOAuthComplete marks OAuth as complete and cleans up callback server
func (c *Client) markOAuthComplete() {
	c.oauthMu.Lock()
	defer c.oauthMu.Unlock()

	c.oauthInProgress = false
	c.oauthCompleted = true
	c.lastOAuthTimestamp = time.Now()

	c.logger.Info("✅ OAuth marked as complete",
		zap.String("server", c.config.Name),
		zap.Time("completion_time", c.lastOAuthTimestamp))

	// Notify global token manager so the running process (daemon) can trigger
	// an immediate reconnect. Also persist a DB event when possible so other
	// processes can detect completion without polling.
	tm := oauth.GetTokenStoreManager()
	if c.storage != nil {
		if err := tm.MarkOAuthCompletedWithDB(c.config.Name, c.storage); err != nil {
			c.logger.Warn("Failed to persist OAuth completion event to DB; using in-memory notification",
				zap.String("server", c.config.Name),
				zap.Error(err))
			tm.MarkOAuthCompleted(c.config.Name)
		} else {
			c.logger.Info("📢 OAuth completion recorded to DB for cross-process notification",
				zap.String("server", c.config.Name))
		}
	} else {
		tm.MarkOAuthCompleted(c.config.Name)
		c.logger.Info("📢 OAuth completion recorded in-memory (no DB available)",
			zap.String("server", c.config.Name))
	}

	// Clean up the callback server to free the port
	if manager := oauth.GetGlobalCallbackManager(); manager != nil {
		if err := manager.StopCallbackServer(c.config.Name); err != nil {
			c.logger.Warn("Failed to stop OAuth callback server",
				zap.String("server", c.config.Name),
				zap.Error(err))
		}
	}
}

// wasOAuthRecentlyCompleted checks if OAuth was completed recently to prevent retry loops
func (c *Client) wasOAuthRecentlyCompleted() bool {
	c.oauthMu.RLock()
	defer c.oauthMu.RUnlock()

	// Consider OAuth "recently completed" if it finished within the last 10 seconds
	return c.oauthCompleted && time.Since(c.lastOAuthTimestamp) < 10*time.Second
}

// ClearOAuthState clears OAuth state (public API for manual OAuth flows)
func (c *Client) ClearOAuthState() {
	c.clearOAuthState()
}

// ForceOAuthFlow forces an OAuth authentication flow, bypassing rate limiting (for manual auth)
func (c *Client) ForceOAuthFlow(ctx context.Context) error {
	c.logger.Info("🔐 Starting forced OAuth authentication flow",
		zap.String("server", c.config.Name))

	// Fast‑fail if OAuth is clearly not applicable for this server
	if !oauth.ShouldUseOAuth(c.config) {
		return fmt.Errorf("OAuth is not supported or not applicable for server '%s'", c.config.Name)
	}

	// Clear any existing OAuth state
	c.clearOAuthState()

	// Ensure transport type is determined if not already set
	if c.transportType == "" {
		c.transportType = transport.DetermineTransportType(c.config)
		c.logger.Info("Transport type determined for OAuth flow",
			zap.String("server", c.config.Name),
			zap.String("transport_type", c.transportType))
	}

	// Mark context as manual OAuth flow to bypass rate limiting
	manualCtx := context.WithValue(ctx, manualOAuthKey, true)

	// Try to create an OAuth-enabled client that will trigger the OAuth flow
	switch c.transportType {
	case transportHTTP, transportHTTPStreamable:
		return c.forceHTTPOAuthFlow(manualCtx)
	case transportSSE:
		return c.forceSSEOAuthFlow(manualCtx)
	default:
		return fmt.Errorf("OAuth not supported for transport type: %s", c.transportType)
	}
}

// forceHTTPOAuthFlow forces OAuth flow for HTTP transport
func (c *Client) forceHTTPOAuthFlow(ctx context.Context) error {
	// Create OAuth config
	oauthConfig := oauth.CreateOAuthConfig(c.config, c.storage)
	if oauthConfig == nil {
		return fmt.Errorf("failed to create OAuth config - server may not support OAuth")
	}

	c.logger.Info("🌐 Starting manual HTTP OAuth flow...",
		zap.String("server", c.config.Name))

	// Create HTTP transport config with OAuth
	httpConfig := transport.CreateHTTPTransportConfig(c.config, oauthConfig)

	// Create OAuth-enabled HTTP client using transport layer
	httpClient, err := transport.CreateHTTPClient(httpConfig)
	if err != nil {
		return fmt.Errorf("failed to create OAuth HTTP client: %w", err)
	}

	// Store the client temporarily
	c.client = httpClient

	c.logger.Info("🚀 Starting OAuth HTTP client and triggering initialization to force authorization...")

	// Start the client first
	err = c.client.Start(ctx)
	if err != nil {
		return fmt.Errorf("failed to start OAuth client: %w", err)
	}

	// Now try to initialize - this will trigger OAuth authorization requirement
	c.logger.Info("🎯 Attempting initialize to trigger OAuth authorization requirement...")
	err = c.initialize(ctx)
	if err != nil {
		// Check if this is an OAuth authorization error that we need to handle manually
		if client.IsOAuthAuthorizationRequiredError(err) {
			c.logger.Info("✅ OAuth authorization requirement triggered - starting manual OAuth flow")

			// Handle OAuth authorization manually using the example pattern
			if oauthErr := c.handleOAuthAuthorization(ctx, err, oauthConfig); oauthErr != nil {
				return fmt.Errorf("OAuth authorization failed: %w", oauthErr)
			}

			// Retry initialization after OAuth is complete
			c.logger.Info("🔄 Retrying initialization after OAuth authorization")
			err = c.initialize(ctx)
			if err != nil {
				return fmt.Errorf("initialization failed after OAuth authorization: %w", err)
			}
		} else {
			return fmt.Errorf("initialization failed with non-OAuth error: %w", err)
		}
	}

	c.logger.Info("✅ Manual HTTP OAuth authentication completed successfully")
	return nil
}

// forceSSEOAuthFlow forces OAuth flow for SSE transport
func (c *Client) forceSSEOAuthFlow(ctx context.Context) error {
	// Create OAuth config
	oauthConfig := oauth.CreateOAuthConfig(c.config, c.storage)
	if oauthConfig == nil {
		return fmt.Errorf("failed to create OAuth config - server may not support OAuth")
	}

	c.logger.Info("🌐 Starting manual SSE OAuth flow...",
		zap.String("server", c.config.Name))

	// Create SSE transport config with OAuth
	httpConfig := transport.CreateHTTPTransportConfig(c.config, oauthConfig)

	// Create OAuth-enabled SSE client using transport layer
	sseClient, err := transport.CreateSSEClient(httpConfig)
	if err != nil {
		return fmt.Errorf("failed to create OAuth SSE client: %w", err)
	}

	// Store the client temporarily
	c.client = sseClient

	c.logger.Info("🚀 Starting OAuth SSE client and triggering authorization...")

	// Start the client first - this may fail with authorization required for SSE
	err = c.client.Start(ctx)
	if err != nil {
		// Check if this is an OAuth authorization error from Start()
		if c.isOAuthError(err) || strings.Contains(err.Error(), "authorization required") || strings.Contains(err.Error(), "no valid token") {
			c.logger.Info("✅ OAuth authorization required from SSE Start() - triggering manual OAuth flow")

			// Handle OAuth authorization manually
			if oauthErr := c.handleOAuthAuthorization(ctx, err, oauthConfig); oauthErr != nil {
				return fmt.Errorf("OAuth authorization failed: %w", oauthErr)
			}

			// Retry starting the client after OAuth is complete
			c.logger.Info("🔄 Retrying SSE client start after OAuth authorization")
			err = c.client.Start(ctx)
			if err != nil {
				return fmt.Errorf("SSE client start failed after OAuth authorization: %w", err)
			}
		} else {
			return fmt.Errorf("failed to start OAuth client: %w", err)
		}
	}

	// Now try to initialize to ensure connection is working
	c.logger.Info("🎯 Attempting initialize to verify connection...")
	err = c.initialize(ctx)
	if err != nil {
		// Check if this is an OAuth authorization error that we need to handle manually
		if client.IsOAuthAuthorizationRequiredError(err) || c.isOAuthError(err) {
			c.logger.Info("✅ OAuth authorization requirement from initialize - starting manual OAuth flow")

			// Handle OAuth authorization manually using the example pattern
			if oauthErr := c.handleOAuthAuthorization(ctx, err, oauthConfig); oauthErr != nil {
				return fmt.Errorf("OAuth authorization failed: %w", oauthErr)
			}

			// Retry initialization after OAuth is complete
			c.logger.Info("🔄 Retrying initialization after OAuth authorization")
			err = c.initialize(ctx)
			if err != nil {
				return fmt.Errorf("initialization failed after OAuth authorization: %w", err)
			}
		} else {
			return fmt.Errorf("initialization failed with non-OAuth error: %w", err)
		}
	}

	c.logger.Info("✅ Manual SSE OAuth authentication completed successfully")
	return nil
}

// isManualOAuthFlow checks if this is a manual OAuth flow
func (c *Client) isManualOAuthFlow(ctx context.Context) bool {
	// Check if context has manual OAuth marker
	if ctx != nil {
		if value := ctx.Value(manualOAuthKey); value != nil {
			if manual, ok := value.(bool); ok && manual {
				return true
			}
		}
	}
	return false
}

// clearOAuthState clears OAuth state (for cleaning up stale state)
func (c *Client) clearOAuthState() {
	c.oauthMu.Lock()
	defer c.oauthMu.Unlock()

	c.logger.Info("🧹 Clearing OAuth state",
		zap.String("server", c.config.Name),
		zap.Bool("was_in_progress", c.oauthInProgress),
		zap.Bool("was_completed", c.oauthCompleted))

	c.oauthInProgress = false
	c.oauthCompleted = false
	c.lastOAuthTimestamp = time.Time{}
}

// openBrowser attempts to open the OAuth URL in the default browser
func (c *Client) openBrowser(authURL string) error {
	var cmd string
	var args []string

	switch runtime.GOOS {
	case osWindows:
		cmd = "rundll32"
		args = []string{"url.dll,FileProtocolHandler", authURL}
	case osDarwin:
		cmd = "open"
		args = []string{authURL}
	case osLinux:
		// Always attempt xdg-open, but warn when no GUI/session indicators are found.
		if !c.hasGUIEnvironment() {
			c.logger.Warn("No GUI session detected - attempting to launch browser anyway. If nothing appears, copy/paste the URL manually.",
				zap.String("server", c.config.Name))
		}

		if _, err := exec.LookPath("xdg-open"); err != nil {
			return fmt.Errorf("xdg-open not found in PATH: %w", err)
		}

		cmd = "xdg-open"
		args = []string{authURL}
	default:
		return fmt.Errorf("unsupported operating system: %s", runtime.GOOS)
	}

	execCmd := exec.Command(cmd, args...)
	return execCmd.Start()
}

// hasGUIEnvironment checks if a GUI environment is available on Linux
func (c *Client) hasGUIEnvironment() bool {
	// Check for common environment variables that indicate GUI
	envVars := []string{"DISPLAY", "WAYLAND_DISPLAY", "XDG_SESSION_TYPE"}

	for _, envVar := range envVars {
		if value := os.Getenv(envVar); value != "" {
			return true
		}
	}

	return false
}

// isDeferOAuthForTray checks if OAuth should be deferred to prevent tray UI blocking
func (c *Client) isDeferOAuthForTray() bool {
	// Check if we're in tray mode by looking for tray-specific environment or configuration
	// During initial server startup, we should defer OAuth to prevent blocking the tray UI

	tokenManager := oauth.GetTokenStoreManager()
	if tokenManager == nil {
		return false
	}

	// If OAuth has been recently attempted (within last 5 minutes), don't defer
	// This allows manual retry flows to work
	if tokenManager.HasRecentOAuthCompletion(c.config.Name) {
		c.logger.Debug("OAuth recently attempted - allowing manual flow",
			zap.String("server", c.config.Name))
		return false
	}

	// Check if this is an automatic retry vs manual trigger
	// Defer only during automatic connection attempts to prevent UI blocking
	// Manual OAuth flows (triggered via tray menu) should proceed immediately

	c.logger.Debug("Deferring OAuth during automatic connection attempt",
		zap.String("server", c.config.Name))
	return true
}
