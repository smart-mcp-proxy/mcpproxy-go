package core

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
		// Check if this is an auth error that should trigger OAuth retry
		if c.isAuthError(err) {
			c.logger.Debug("🔄 MCP initialization failed with auth error, retrying with OAuth",
				zap.Error(err))

			// Log initialization failure to server-specific log
			if c.upstreamLogger != nil {
				c.upstreamLogger.Warn("MCP initialization failed with auth error, retrying with OAuth",
					zap.Error(err))
			}

			// Close the current client
			c.client.Close()
			c.client = nil

			// Try OAuth authentication
			if oauthErr := c.tryOAuthAuth(ctx); oauthErr != nil {
				return fmt.Errorf("failed to authenticate with OAuth after auth error: %w", oauthErr)
			}

			// Retry MCP initialization with OAuth client
			if initErr := c.initialize(ctx); initErr != nil {
				if c.upstreamLogger != nil {
					c.upstreamLogger.Error("MCP initialization failed even with OAuth",
						zap.Error(initErr))
				}
				c.client.Close()
				c.client = nil
				return fmt.Errorf("failed to initialize even with OAuth: %w", initErr)
			}
		} else {
			// Log initialization failure to server-specific log
			if c.upstreamLogger != nil {
				c.upstreamLogger.Error("MCP initialization failed",
					zap.Error(err))
			}
			c.client.Close()
			c.client = nil
			return fmt.Errorf("failed to initialize: %w", err)
		}
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
	willUseDocker := (c.config.Command == cmdDocker || strings.HasSuffix(c.config.Command, "/"+cmdDocker)) && len(args) > 0 && args[0] == cmdRun
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

		// Use Docker isolation (now shell-wrapped for PATH inheritance)
		finalCommand, finalArgs = c.setupDockerIsolation(c.config.Command, args)
		c.isDockerCommand = true

		// Add cidfile to shell-wrapped Docker command if we have one
		if cidFile != "" {
			finalArgs = c.insertCidfileIntoShellDockerCommand(finalArgs, cidFile)
		}
	} else {
		// Use shell wrapping for environment inheritance
		// This fixes issues when mcpproxy is launched via Launchd and doesn't inherit
		// user's shell environment (like PATH customizations from .bashrc, .zshrc, etc.)
		finalCommand, finalArgs = c.wrapWithUserShell(c.config.Command, args)
		c.isDockerCommand = false

		// Handle explicit docker commands
		if (c.config.Command == cmdDocker || strings.HasSuffix(c.config.Command, "/"+cmdDocker)) && len(args) > 0 && args[0] == cmdRun {
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

	// CRITICAL FIX: Wrap Docker command with user shell to inherit proper PATH
	// This fixes issues when mcpproxy is launched via Launchpad/GUI where PATH doesn't include Docker
	return c.wrapWithUserShell(cmdDocker, finalArgs)
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

// hasCommand checks if a command is available in PATH
func hasCommand(command string) bool {
	_, err := exec.LookPath(command)
	return err == nil
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
func (c *Client) tryOAuthAuth(ctx context.Context) error {
	c.logger.Debug("🔐 Attempting OAuth authentication",
		zap.String("server", c.config.Name),
		zap.String("url", c.config.URL))

	// Create OAuth config using the oauth package
	oauthConfig := oauth.CreateOAuthConfig(c.config)
	if oauthConfig == nil {
		return fmt.Errorf("failed to create OAuth config")
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
	if runtime.GOOS == "linux" && os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
		browserBlockingConditions = append(browserBlockingConditions, "no_GUI_on_linux")
	}

	if len(browserBlockingConditions) > 0 {
		c.logger.Warn("⚠️ Detected conditions that may prevent browser opening",
			zap.String("server", c.config.Name),
			zap.Strings("blocking_conditions", browserBlockingConditions))
	}

	// Start the OAuth client first to set up the flow
	c.logger.Info("🚀 Starting OAuth client - this should trigger mcp-go library authentication flow",
		zap.String("server", c.config.Name),
		zap.String("expectation", "mcp-go library should handle DCR, browser opening, and token exchange"))
	err = c.client.Start(ctx)

	if err != nil {
		c.logger.Error("❌ OAuth client start failed",
			zap.String("server", c.config.Name),
			zap.Error(err),
			zap.String("error_type", fmt.Sprintf("%T", err)))

		// Check if it's specifically a browser opening issue
		if strings.Contains(err.Error(), "browser") || strings.Contains(err.Error(), "open") {
			c.logger.Error("🚫 Browser opening issue detected in mcp-go library",
				zap.String("server", c.config.Name),
				zap.Error(err),
				zap.String("DISPLAY", os.Getenv("DISPLAY")),
				zap.String("XDG_SESSION_TYPE", os.Getenv("XDG_SESSION_TYPE")))

			// Provide manual fallback instructions
			c.provideManualOAuthInstructions(oauthConfig)
		}

		// Check if it's a token availability issue (OAuth flow didn't complete)
		if strings.Contains(err.Error(), "no valid token available") || strings.Contains(err.Error(), "authorization required") {
			c.logger.Error("🚫 OAuth flow did not complete - no valid token available",
				zap.String("server", c.config.Name),
				zap.Error(err))

			// Provide manual fallback instructions
			c.provideManualOAuthInstructions(oauthConfig)
		}

		// Provide helpful troubleshooting information
		c.logger.Error("🔧 OAuth troubleshooting information",
			zap.String("server", c.config.Name),
			zap.String("redirect_uri", oauthConfig.RedirectURI),
			zap.Strings("scopes", oauthConfig.Scopes),
			zap.String("auth_metadata_url", oauthConfig.AuthServerMetadataURL),
			zap.Bool("pkce_enabled", oauthConfig.PKCEEnabled))

		return fmt.Errorf("OAuth client start failed: %w", err)
	}

	c.logger.Info("✅ OAuth client started successfully",
		zap.String("server", c.config.Name))

	// Add a longer delay to allow for Dynamic Client Registration to complete
	c.logger.Debug("⏳ Waiting for OAuth Dynamic Client Registration and browser opening...")
	time.Sleep(5 * time.Second)
	c.logger.Debug("⏰ Done waiting for DCR and browser opening")

	// Check if the client_id is now available after DCR
	c.logger.Debug("🔍 Checking OAuth config after DCR delay",
		zap.String("client_id", oauthConfig.ClientID),
		zap.String("redirect_uri", oauthConfig.RedirectURI),
		zap.Bool("pkce_enabled", oauthConfig.PKCEEnabled))

	c.logger.Debug("🔎 About to check if client_id is empty",
		zap.String("server", c.config.Name),
		zap.String("client_id_value", oauthConfig.ClientID),
		zap.Bool("client_id_is_empty", oauthConfig.ClientID == ""))

	// If client_id is still missing, perform our own DCR
	if oauthConfig.ClientID == "" {
		c.logger.Debug("🎯 Inside client_id empty check - proceeding to OAuth progress check",
			zap.String("server", c.config.Name))
		// Check if OAuth is already in progress to prevent multiple concurrent flows
		inProgress := c.isOAuthInProgress()
		c.logger.Debug("🔍 Checking OAuth progress status",
			zap.String("server", c.config.Name),
			zap.Bool("oauth_in_progress", inProgress))

		if inProgress {
			c.logger.Info("🔄 OAuth flow already in progress - skipping duplicate DCR",
				zap.String("server", c.config.Name))
			return fmt.Errorf("OAuth flow already in progress")
		}

		c.markOAuthInProgress()
		defer c.markOAuthComplete()

		c.logger.Warn("🔧 mcp-go library did not perform DCR - performing manual DCR",
			zap.String("server", c.config.Name),
			zap.String("issue", "client_id required for OAuth but not provided by mcp-go library"))

		// Perform our own Dynamic Client Registration
		clientID, err := c.performDynamicClientRegistration(oauthConfig)
		if err != nil {
			c.logger.Error("❌ Manual DCR failed",
				zap.String("server", c.config.Name),
				zap.Error(err))
		} else {
			c.logger.Info("✅ Manual DCR successful",
				zap.String("server", c.config.Name),
				zap.String("client_id", clientID))
			// Update the OAuth config with the new client_id
			oauthConfig.ClientID = clientID

			// CRITICAL: Recreate the OAuth client with the updated config
			// The existing client was created without client_id, so it can't complete token exchange
			c.logger.Info("🔄 Recreating OAuth client with updated client_id",
				zap.String("server", c.config.Name),
				zap.String("client_id", clientID))

			// Create new HTTP client with updated OAuth config
			httpConfig := &transport.HTTPTransportConfig{
				URL:         c.config.URL,
				OAuthConfig: oauthConfig,
				UseOAuth:    true,
			}

			newClient, err := transport.CreateHTTPClient(httpConfig)
			if err != nil {
				c.logger.Error("❌ Failed to recreate HTTP client with updated OAuth config",
					zap.String("server", c.config.Name),
					zap.Error(err))
			} else {
				c.client = newClient
				c.logger.Info("✅ OAuth client recreated with client_id - ready for token exchange",
					zap.String("server", c.config.Name),
					zap.String("client_id", clientID))

				// Start the OAuth client with proper client_id for token exchange
				c.logger.Info("🔄 Starting OAuth client to enable token exchange and callback handling",
					zap.String("server", c.config.Name))

				err = c.client.Start(ctx)
				if err != nil {
					c.logger.Error("❌ Failed to start OAuth client with client_id",
						zap.String("server", c.config.Name),
						zap.Error(err))
				} else {
					c.logger.Info("✅ OAuth client started successfully - callback handler active",
						zap.String("server", c.config.Name))

					// Open browser for user authentication but DON'T wait for completion
					// The OAuth flow is now asynchronous - user will authorize, callback will be processed
					c.logger.Info("🌐 Opening browser for OAuth authorization - flow is now asynchronous",
						zap.String("server", c.config.Name))
					c.provideManualOAuthInstructions(oauthConfig)

					// DON'T return nil here - let the connection attempt continue
					// The first attempt will likely fail, but subsequent retries should succeed
					// after the user completes OAuth authorization
					c.logger.Info("⏳ OAuth flow initiated - subsequent connection attempts will succeed after user authorization",
						zap.String("server", c.config.Name))
				}
			}
		}
	} else {
		c.logger.Info("✅ client_id already available - skipping DCR",
			zap.String("server", c.config.Name),
			zap.String("client_id", oauthConfig.ClientID))
	}

	c.logger.Debug("🏁 Reached end of OAuth setup function",
		zap.String("server", c.config.Name),
		zap.String("final_client_id", oauthConfig.ClientID))

	// The mcp-go library apparently doesn't open browser automatically, so we need to do it manually
	c.logger.Warn("🚫 mcp-go library did not open browser - providing manual fallback",
		zap.String("server", c.config.Name),
		zap.String("version", "v0.38.0"),
		zap.String("issue", "OAuth client starts successfully but browser opening is not working"))

	// Provide immediate manual instructions and try to open browser
	c.provideManualOAuthInstructions(oauthConfig)

	c.logger.Debug("🔚 OAuth function returning",
		zap.String("server", c.config.Name))

	return nil
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

// manuallyOpenOAuthURL constructs the OAuth authorization URL and opens it in browser
func (c *Client) manuallyOpenOAuthURL(oauthConfig *client.OAuthConfig) error {
	c.logger.Debug("🔍 Attempting to discover OAuth endpoints and open browser manually",
		zap.String("server", c.config.Name),
		zap.String("auth_server_url", oauthConfig.AuthServerMetadataURL))

	// Try to discover OAuth endpoints from the authorization server metadata
	authzEndpoint, err := c.discoverAuthorizationEndpoint(oauthConfig.AuthServerMetadataURL)
	if err != nil {
		c.logger.Warn("⚠️ Failed to discover OAuth authorization endpoint, falling back to instructions",
			zap.String("server", c.config.Name),
			zap.Error(err))

		// Print fallback instructions
		baseURL, parseErr := parseBaseURL(c.config.URL)
		if parseErr != nil {
			baseURL = c.config.URL
		}

		fmt.Printf("\n=== OAuth Authentication Required ===\n")
		fmt.Printf("Server: %s\n", c.config.Name)
		fmt.Printf("The mcp-go library should open a browser automatically.\n")
		fmt.Printf("If it doesn't, you may need to visit the OAuth endpoint manually:\n")
		fmt.Printf("Base URL: %s\n", baseURL)
		fmt.Printf("Redirect URI: %s\n", oauthConfig.RedirectURI)
		fmt.Printf("Scopes: %s\n", strings.Join(oauthConfig.Scopes, " "))
		fmt.Printf("========================================\n\n")

		return err
	}

	// Generate OAuth authorization URL with proper parameters
	authURL, err := c.buildAuthorizationURL(authzEndpoint, oauthConfig)
	if err != nil {
		c.logger.Error("Failed to build authorization URL",
			zap.String("server", c.config.Name),
			zap.Error(err))
		return fmt.Errorf("failed to build authorization URL: %w", err)
	}

	c.logger.Info("🌟 Opening OAuth authorization URL in browser",
		zap.String("server", c.config.Name),
		zap.String("auth_url", authURL),
		zap.String("redirect_uri", oauthConfig.RedirectURI))

	// Try to open browser using platform-specific commands
	if err := c.openBrowser(authURL); err != nil {
		c.logger.Warn("🚫 Could not open browser automatically, please visit the URL manually",
			zap.String("server", c.config.Name),
			zap.String("auth_url", authURL),
			zap.Error(err))

		// Output URL to console for manual access
		fmt.Printf("\n=== OAuth Authentication Required ===\n")
		fmt.Printf("Server: %s\n", c.config.Name)
		fmt.Printf("Please visit this URL to authenticate:\n%s\n", authURL)
		fmt.Printf("=====================================\n\n")

		return err
	}

	c.logger.Info("✅ Browser opened successfully for OAuth authentication",
		zap.String("server", c.config.Name))

	return nil
}

// parseBaseURL extracts the base URL from a full URL
func parseBaseURL(fullURL string) (string, error) {
	u, err := url.Parse(fullURL)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s://%s", u.Scheme, u.Host), nil
}

// OAuthMetadata represents OAuth server metadata from discovery endpoint
type OAuthMetadata struct {
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	RegistrationEndpoint  string `json:"registration_endpoint"`
	JwksURI               string `json:"jwks_uri"`
	Issuer                string `json:"issuer"`
}

// discoverAuthorizationEndpoint discovers OAuth authorization endpoint from metadata URL
func (c *Client) discoverAuthorizationEndpoint(metadataURL string) (string, error) {
	if metadataURL == "" {
		return "", fmt.Errorf("no metadata URL provided")
	}

	c.logger.Debug("🔍 Discovering OAuth authorization endpoint",
		zap.String("metadata_url", metadataURL))

	// Create HTTP client with timeout
	httpClient := &http.Client{
		Timeout: 10 * time.Second,
	}

	resp, err := httpClient.Get(metadataURL)
	if err != nil {
		return "", fmt.Errorf("failed to fetch OAuth metadata: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("OAuth metadata request failed with status %d", resp.StatusCode)
	}

	var metadata OAuthMetadata
	if err := json.NewDecoder(resp.Body).Decode(&metadata); err != nil {
		return "", fmt.Errorf("failed to parse OAuth metadata: %w", err)
	}

	if metadata.AuthorizationEndpoint == "" {
		return "", fmt.Errorf("no authorization_endpoint found in OAuth metadata")
	}

	c.logger.Debug("✅ Successfully discovered OAuth authorization endpoint",
		zap.String("authorization_endpoint", metadata.AuthorizationEndpoint))

	return metadata.AuthorizationEndpoint, nil
}

// generateState generates a random state parameter for OAuth
func generateState() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(b), nil
}

// generateCodeVerifier generates a PKCE code verifier
func generateCodeVerifier() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString(b), nil
}

// generateCodeChallenge generates a PKCE code challenge from verifier
func generateCodeChallenge(verifier string) string {
	h := sha256.Sum256([]byte(verifier))
	return base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString(h[:])
}

// buildAuthorizationURL constructs the OAuth authorization URL with proper parameters
func (c *Client) buildAuthorizationURL(authzEndpoint string, oauthConfig *client.OAuthConfig) (string, error) {
	if authzEndpoint == "" {
		return "", fmt.Errorf("empty authorization endpoint")
	}

	// Parse the authorization endpoint
	u, err := url.Parse(authzEndpoint)
	if err != nil {
		return "", fmt.Errorf("invalid authorization endpoint URL: %w", err)
	}

	// For now, just return the authorization endpoint with basic query parameters
	// The mcp-go library should handle the full OAuth flow including state and PKCE
	// We're just trying to open the browser to the right endpoint

	params := url.Values{}
	params.Set("response_type", "code")

	// Only add client_id if we have one (DCR might not have happened yet)
	if oauthConfig.ClientID != "" {
		params.Set("client_id", oauthConfig.ClientID)
	}

	params.Set("redirect_uri", oauthConfig.RedirectURI)
	params.Set("scope", strings.Join(oauthConfig.Scopes, " "))

	// Let the mcp-go library handle state and PKCE parameters
	// We're just providing a basic authorization URL for manual browser opening

	// Construct final URL
	u.RawQuery = params.Encode()
	authURL := u.String()

	c.logger.Debug("🔗 Built basic OAuth authorization URL for manual browser opening",
		zap.String("auth_url", authURL),
		zap.String("note", "mcp-go library will handle full OAuth flow with proper parameters"))

	return authURL, nil
}

// openBrowser attempts to open the OAuth URL in the default browser
func (c *Client) openBrowser(authURL string) error {
	var cmd string
	var args []string

	c.logger.Debug("🌐 Attempting to open browser",
		zap.String("url", authURL),
		zap.String("os", runtime.GOOS))

	switch runtime.GOOS {
	case "windows":
		cmd = "rundll32"
		args = []string{"url.dll,FileProtocolHandler", authURL}
	case "darwin":
		cmd = "open"
		args = []string{authURL}
	case "linux":
		// Try to detect if we're in a GUI environment
		if !c.hasGUIEnvironment() {
			return fmt.Errorf("no GUI environment detected")
		}
		cmd = "xdg-open"
		args = []string{authURL}
	default:
		return fmt.Errorf("unsupported operating system: %s", runtime.GOOS)
	}

	c.logger.Debug("🔧 Executing browser command",
		zap.String("cmd", cmd),
		zap.Strings("args", args))

	execCmd := exec.Command(cmd, args...)
	if err := execCmd.Start(); err != nil {
		return fmt.Errorf("failed to open browser: %w", err)
	}

	c.logger.Debug("✅ Browser command executed successfully")
	return nil
}

// hasGUIEnvironment checks if a GUI environment is available on Linux
func (c *Client) hasGUIEnvironment() bool {
	// Check for common environment variables that indicate GUI
	envVars := []string{"DISPLAY", "WAYLAND_DISPLAY", "XDG_SESSION_TYPE"}

	for _, envVar := range envVars {
		if value := os.Getenv(envVar); value != "" {
			c.logger.Debug("🖥️ GUI environment detected",
				zap.String("env_var", envVar),
				zap.String("value", value))
			return true
		}
	}

	// Check if xdg-open is available
	if _, err := exec.LookPath("xdg-open"); err == nil {
		c.logger.Debug("🖥️ xdg-open command available")
		return true
	}

	c.logger.Debug("🚫 No GUI environment detected")
	return false
}

// provideManualOAuthInstructions provides manual OAuth instructions to the user
func (c *Client) provideManualOAuthInstructions(oauthConfig *client.OAuthConfig) {
	c.logger.Info("📋 Providing manual OAuth authentication instructions",
		zap.String("server", c.config.Name))

	// Try to discover OAuth endpoints for better instructions
	authzEndpoint, err := c.discoverAuthorizationEndpoint(oauthConfig.AuthServerMetadataURL)
	if err != nil {
		c.logger.Debug("Could not discover OAuth endpoint for manual instructions",
			zap.Error(err))

		// Fallback to base URL
		baseURL, parseErr := parseBaseURL(c.config.URL)
		if parseErr != nil {
			baseURL = c.config.URL
		}
		authzEndpoint = baseURL + "/oauth/authorize"
	}

	fmt.Printf("\n" + strings.Repeat("=", 80) + "\n")
	fmt.Printf("🔐 MANUAL OAUTH AUTHENTICATION REQUIRED\n")
	fmt.Printf(strings.Repeat("=", 80) + "\n")
	fmt.Printf("Server: %s\n", c.config.Name)
	fmt.Printf("URL: %s\n", c.config.URL)
	fmt.Printf("\n")
	fmt.Printf("The automatic OAuth flow failed. Please complete authentication manually:\n")
	fmt.Printf("\n")
	if oauthConfig.ClientID == "" {
		fmt.Printf("⚠️  IMPORTANT: No client_id available yet (Dynamic Client Registration pending)\n")
		fmt.Printf("    The OAuth server may need to register a client first.\n")
		fmt.Printf("\n")
	}
	fmt.Printf("1. Open this URL in your browser:\n")
	fmt.Printf("   %s\n", authzEndpoint)
	fmt.Printf("\n")
	fmt.Printf("2. If asked, use these parameters:\n")
	fmt.Printf("   - Redirect URI: %s\n", oauthConfig.RedirectURI)
	fmt.Printf("   - Scopes: %s\n", strings.Join(oauthConfig.Scopes, " "))
	fmt.Printf("   - Response Type: code\n")
	fmt.Printf("   - PKCE Enabled: %t\n", oauthConfig.PKCEEnabled)
	if oauthConfig.ClientID != "" {
		fmt.Printf("   - Client ID: %s\n", oauthConfig.ClientID)
	} else {
		fmt.Printf("   - Client ID: (not available - DCR required)\n")
	}
	fmt.Printf("\n")
	fmt.Printf("3. Complete the authentication in your browser\n")
	fmt.Printf("\n")
	fmt.Printf("4. If the browser doesn't open automatically, copy the URL above\n")
	fmt.Printf("   and paste it into your browser manually\n")
	fmt.Printf("\n")
	fmt.Printf("OAuth Metadata URL: %s\n", oauthConfig.AuthServerMetadataURL)
	fmt.Printf(strings.Repeat("=", 80) + "\n")
	fmt.Printf("\n")

	// Try to open browser as a last resort with proper OAuth parameters
	if runtime.GOOS == "darwin" && hasCommand("open") {
		c.logger.Info("🌐 Attempting to open browser as fallback",
			zap.String("server", c.config.Name))

		// Build proper OAuth URL with required parameters
		oauthURL, err := c.buildProperOAuthURL(authzEndpoint, oauthConfig)
		if err != nil {
			c.logger.Warn("Could not build proper OAuth URL",
				zap.Error(err))
			// Fallback to basic endpoint
			oauthURL = authzEndpoint
		}

		c.logger.Info("🔗 Opening browser with complete OAuth URL",
			zap.String("oauth_url", oauthURL))

		if err := c.openBrowser(oauthURL); err != nil {
			c.logger.Warn("Could not open browser for manual OAuth",
				zap.Error(err))
		} else {
			fmt.Printf("✅ Browser opened to OAuth authorization page\n\n")
		}
	}
}

// buildProperOAuthURL constructs a complete OAuth authorization URL with all required parameters
func (c *Client) buildProperOAuthURL(authzEndpoint string, oauthConfig *client.OAuthConfig) (string, error) {
	if authzEndpoint == "" {
		return "", fmt.Errorf("empty authorization endpoint")
	}

	// Parse the authorization endpoint
	u, err := url.Parse(authzEndpoint)
	if err != nil {
		return "", fmt.Errorf("invalid authorization endpoint URL: %w", err)
	}

	// Generate OAuth parameters that we need
	state, err := generateState()
	if err != nil {
		return "", fmt.Errorf("failed to generate state: %w", err)
	}

	// Build query parameters
	params := url.Values{}
	params.Set("response_type", "code")
	params.Set("redirect_uri", oauthConfig.RedirectURI)
	params.Set("scope", strings.Join(oauthConfig.Scopes, " "))
	params.Set("state", state)

	// Add client_id if available (it might be empty if DCR hasn't happened yet)
	if oauthConfig.ClientID != "" {
		params.Set("client_id", oauthConfig.ClientID)
		c.logger.Debug("✅ Using client_id from OAuth config",
			zap.String("client_id", oauthConfig.ClientID))
	} else {
		c.logger.Warn("⚠️ No client_id available - Dynamic Client Registration may not have completed",
			zap.String("server", c.config.Name))

		// This URL will likely fail without client_id, but we'll try anyway
		c.logger.Warn("🚫 OAuth URL will be incomplete without client_id",
			zap.String("issue", "DCR must complete first to get client_id"))
	}

	// Add PKCE parameters if enabled
	if oauthConfig.PKCEEnabled {
		codeVerifier, err := generateCodeVerifier()
		if err != nil {
			c.logger.Warn("Failed to generate PKCE code verifier",
				zap.Error(err))
		} else {
			codeChallenge := generateCodeChallenge(codeVerifier)
			params.Set("code_challenge", codeChallenge)
			params.Set("code_challenge_method", "S256")

			c.logger.Debug("🔐 Added PKCE parameters to OAuth URL",
				zap.String("code_challenge", codeChallenge))
		}
	}

	// Construct final URL
	u.RawQuery = params.Encode()
	authURL := u.String()

	c.logger.Debug("🔗 Built complete OAuth authorization URL",
		zap.String("auth_url", authURL),
		zap.String("redirect_uri", oauthConfig.RedirectURI),
		zap.Strings("scopes", oauthConfig.Scopes),
		zap.String("state", state))

	return authURL, nil
}

// DCRRequest represents a Dynamic Client Registration request according to RFC 7591
type DCRRequest struct {
	ClientName              string   `json:"client_name"`
	RedirectURIs            []string `json:"redirect_uris"`
	Scope                   string   `json:"scope"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
}

// DCRResponse represents a Dynamic Client Registration response
type DCRResponse struct {
	ClientID              string `json:"client_id"`
	ClientSecret          string `json:"client_secret,omitempty"`
	ClientIDIssuedAt      int64  `json:"client_id_issued_at,omitempty"`
	ClientSecretExpiresAt int64  `json:"client_secret_expires_at,omitempty"`
}

// performDynamicClientRegistration performs DCR according to RFC 7591
func (c *Client) performDynamicClientRegistration(oauthConfig *client.OAuthConfig) (string, error) {
	c.logger.Info("🔧 Starting Dynamic Client Registration (DCR)",
		zap.String("server", c.config.Name),
		zap.String("auth_metadata_url", oauthConfig.AuthServerMetadataURL))

	// First discover the registration endpoint from OAuth metadata
	registrationEndpoint, err := c.discoverRegistrationEndpoint(oauthConfig.AuthServerMetadataURL)
	if err != nil {
		return "", fmt.Errorf("failed to discover registration endpoint: %w", err)
	}

	c.logger.Debug("✅ Discovered registration endpoint",
		zap.String("registration_endpoint", registrationEndpoint))

	// Prepare DCR request according to RFC 7591
	dcrRequest := DCRRequest{
		ClientName:              "mcpproxy-go",
		RedirectURIs:            []string{oauthConfig.RedirectURI},
		Scope:                   strings.Join(oauthConfig.Scopes, " "),
		GrantTypes:              []string{"authorization_code"},
		ResponseTypes:           []string{"code"},
		TokenEndpointAuthMethod: "none", // PKCE flow doesn't require client secret
	}

	// Marshal request to JSON
	requestBody, err := json.Marshal(dcrRequest)
	if err != nil {
		return "", fmt.Errorf("failed to marshal DCR request: %w", err)
	}

	c.logger.Debug("📤 Sending DCR request",
		zap.String("registration_endpoint", registrationEndpoint),
		zap.String("client_name", dcrRequest.ClientName),
		zap.Strings("redirect_uris", dcrRequest.RedirectURIs))

	// Create HTTP client with timeout
	httpClient := &http.Client{
		Timeout: 10 * time.Second,
	}

	// Send DCR request
	req, err := http.NewRequest("POST", registrationEndpoint, strings.NewReader(string(requestBody)))
	if err != nil {
		return "", fmt.Errorf("failed to create DCR request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to send DCR request: %w", err)
	}
	defer resp.Body.Close()

	// Check response status
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("DCR request failed with status %d: %s", resp.StatusCode, string(body))
	}

	// Parse DCR response
	var dcrResponse DCRResponse
	if err := json.NewDecoder(resp.Body).Decode(&dcrResponse); err != nil {
		return "", fmt.Errorf("failed to parse DCR response: %w", err)
	}

	if dcrResponse.ClientID == "" {
		return "", fmt.Errorf("DCR response missing client_id")
	}

	c.logger.Info("✅ Dynamic Client Registration successful",
		zap.String("server", c.config.Name),
		zap.String("client_id", dcrResponse.ClientID),
		zap.Bool("has_client_secret", dcrResponse.ClientSecret != ""))

	return dcrResponse.ClientID, nil
}

// discoverRegistrationEndpoint discovers the registration endpoint from OAuth metadata
func (c *Client) discoverRegistrationEndpoint(metadataURL string) (string, error) {
	if metadataURL == "" {
		return "", fmt.Errorf("no metadata URL provided")
	}

	c.logger.Debug("🔍 Discovering registration endpoint",
		zap.String("metadata_url", metadataURL))

	// Create HTTP client with timeout
	httpClient := &http.Client{
		Timeout: 10 * time.Second,
	}

	resp, err := httpClient.Get(metadataURL)
	if err != nil {
		return "", fmt.Errorf("failed to fetch OAuth metadata: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("OAuth metadata request failed with status %d", resp.StatusCode)
	}

	var metadata OAuthMetadata
	if err := json.NewDecoder(resp.Body).Decode(&metadata); err != nil {
		return "", fmt.Errorf("failed to parse OAuth metadata: %w", err)
	}

	if metadata.RegistrationEndpoint == "" {
		return "", fmt.Errorf("no registration_endpoint found in OAuth metadata")
	}

	c.logger.Debug("✅ Successfully discovered registration endpoint",
		zap.String("registration_endpoint", metadata.RegistrationEndpoint))

	return metadata.RegistrationEndpoint, nil
}

// OAuth progress tracking to prevent multiple concurrent flows
func (c *Client) isOAuthInProgress() bool {
	c.logger.Debug("🔓 Acquiring OAuth read lock for progress check",
		zap.String("server", c.config.Name))
	c.oauthMu.RLock()
	defer func() {
		c.oauthMu.RUnlock()
		c.logger.Debug("🔓 Released OAuth read lock for progress check",
			zap.String("server", c.config.Name))
	}()

	result := c.oauthInProgress
	c.logger.Debug("🔍 OAuth progress check result",
		zap.String("server", c.config.Name),
		zap.Bool("in_progress", result))
	return result
}

func (c *Client) markOAuthInProgress() {
	c.logger.Debug("🔒 Acquiring OAuth write lock to mark in progress",
		zap.String("server", c.config.Name))
	c.oauthMu.Lock()
	defer func() {
		c.oauthMu.Unlock()
		c.logger.Debug("🔒 Released OAuth write lock after marking in progress",
			zap.String("server", c.config.Name))
	}()
	c.oauthInProgress = true
	c.logger.Debug("🔒 Marked OAuth as in progress",
		zap.String("server", c.config.Name))
}

func (c *Client) markOAuthComplete() {
	c.logger.Debug("🔓 Acquiring OAuth write lock to mark complete",
		zap.String("server", c.config.Name))
	c.oauthMu.Lock()
	defer func() {
		c.oauthMu.Unlock()
		c.logger.Debug("🔓 Released OAuth write lock after marking complete",
			zap.String("server", c.config.Name))
	}()
	c.oauthInProgress = false
	c.logger.Debug("🔓 Marked OAuth as complete",
		zap.String("server", c.config.Name))
}
