package core

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"strings"

	"github.com/mark3labs/mcp-go/client"
	uptransport "github.com/mark3labs/mcp-go/client/transport"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/shellwrap"
	"go.uber.org/zap"
)

// packageRunnerNoArgs lists commands that behave as "runner for a package
// named on the command line" — invoking them with no args prints help and
// exits, which manifests downstream as an opaque "context deadline exceeded"
// on MCP initialize. We fail fast instead.
var packageRunnerNoArgs = map[string]string{
	"uvx":  "the Python package to run (e.g. [\"obsidian-mcp\"])",
	"npx":  "the npm package to run (e.g. [\"-y\", \"some-mcp-server\"])",
	"pipx": "the subcommand and package (e.g. [\"run\", \"obsidian-mcp\"])",
}

// validateStdioConfig runs cheap pre-flight checks on a stdio server
// configuration before launching a subprocess. Separated out so it can be
// unit-tested without exercising the full connection path.
func validateStdioConfig(cfg *config.ServerConfig) error {
	if cfg.Command == "" {
		return fmt.Errorf("no command specified for stdio transport")
	}
	if len(cfg.Args) == 0 {
		if hint, ok := packageRunnerNoArgs[cfg.Command]; ok {
			return fmt.Errorf("server %q: command %q has no args — %s is required", cfg.Name, cfg.Command, hint)
		}
	}
	return nil
}

// connectStdio establishes a stdio transport connection to an MCP server
func (c *Client) connectStdio(ctx context.Context) error {
	if err := validateStdioConfig(c.config); err != nil {
		return err
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
		// Do not re-prefix with another "MCP initialize failed" — the
		// inner error from initialize() already carries a human-readable
		// message. Attach just the transport-level context (command that
		// was launched and whether Docker isolation was in effect) so
		// users can tell from one log line whether to look at the host
		// command or the Docker layer.
		return fmt.Errorf("stdio transport (command=%q, docker_isolation=%t): %w",
			c.config.Command, c.isDockerCommand, err)
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

	// Register notification handler for tools/list_changed
	c.registerNotificationHandler()

	return nil
}

// wrapWithUserShell wraps a command with the user's login shell to inherit
// the full interactive environment. This is a thin wrapper around
// shellwrap.WrapWithUserShell — the canonical implementation now lives in
// internal/shellwrap so the security scanner can share it.
//
// The client-scoped debug log (with server name) is retained here for
// continuity with historical log output.
func (c *Client) wrapWithUserShell(command string, args []string) (shellCommand string, shellArgs []string) {
	shellCommand, shellArgs = shellwrap.WrapWithUserShell(c.logger, command, args)
	c.logger.Debug("Wrapping command with user shell for full environment inheritance",
		zap.String("server", c.config.Name),
		zap.String("original_command", command),
		zap.Strings("original_args", args),
		zap.String("shell", shellCommand))
	return shellCommand, shellArgs
}

// shellescape is retained as a package-local alias for backward
// compatibility with existing tests (internal/upstream/core/shell_test.go).
// It delegates to shellwrap.Shellescape.
func shellescape(s string) string {
	return shellwrap.Shellescape(s)
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
