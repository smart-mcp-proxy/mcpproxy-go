package core

import (
	"fmt"
	"strings"

	"go.uber.org/zap"
)

// setupDockerIsolation configures Docker isolation for the MCP server process.
// Returns the docker command and arguments to execute.
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
