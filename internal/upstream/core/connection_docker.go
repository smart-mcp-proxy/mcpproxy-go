package core

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/shellwrap"
	"go.uber.org/zap"
)

// resolveDockerBinary resolves the absolute path to the `docker` binary. It is
// indirected through a package var (rather than calling shellwrap.ResolveDockerPath
// directly) so tests can stub resolution without a real Docker install. Mirrors
// newDockerCmd's resolution so spawn and runtime-cleanup use the same binary.
var resolveDockerBinary = shellwrap.ResolveDockerPath

// setupDockerIsolation configures Docker isolation for the MCP server process.
// Returns the docker command, arguments, and whether the returned command was
// wrapped in the user's login shell. shellWrapped governs how the caller
// inserts --cidfile: a direct-exec spawn (shellWrapped == false) uses the
// args-based insertCidfileIntoDockerArgs, while the login-shell fallback
// (shellWrapped == true) uses the string-based insertCidfileIntoShellDockerCommand.
func (c *Client) setupDockerIsolation(command string, args []string) (dockerCommand string, dockerArgs []string, shellWrapped bool) {
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
		shellCmd, shellArgs := c.wrapWithUserShell(command, args)
		return shellCmd, shellArgs, true
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

	// CRITICAL FIX (#696 / MCP-2744 / MCP-2753): resolve `docker` to an ABSOLUTE
	// path and exec it DIRECTLY — no login-shell wrap. Docker Desktop installed
	// the default way on macOS (without the optional, admin-gated "install CLI
	// tools" step) leaves the docker CLI only inside the app bundle at
	// /Applications/Docker.app/Contents/Resources/bin/docker, which is NOT on
	// any standard PATH dir nor on the (often unreliable) login-shell PATH a
	// LaunchAgent captures.
	//
	// #697 resolved the absolute path but still routed the spawn through
	// `$SHELL -l -c "<docker> run …"`. There the absolute path is only a token
	// inside the -c string: the login shell re-derives $PATH from rc files and
	// can drop the bundle dir, so the bug persisted. Exec'ing the absolute path
	// directly (mirroring newDockerCmd's exec.CommandContext(dockerBin, …))
	// bypasses PATH entirely. The spawn env already carries the enhanced PATH
	// and the hydrated DOCKER_* daemon vars (BuildSecureEnvironment with
	// EnhancePath=true, after the startup login-shell hydration in MCP-2751), so
	// no login shell is needed for env inheritance either.
	//
	// We only direct-exec a VERIFIED absolute executable. ResolveDockerPath's
	// last resort runs `command -v docker` in the login shell, which can emit a
	// shell function name, an alias, or a bare command name rather than a real
	// binary path; direct-exec'ing that would fail with "no such file". For any
	// non-absolute / non-executable result we fall back to a login-shell wrap of
	// bare "docker", giving the interactive PATH a last-resort chance to find it.
	if resolved, resErr := resolveDockerBinary(c.logger); resErr == nil && isDirectExecutable(resolved) {
		return resolved, finalArgs, false
	} else if resErr != nil {
		c.logger.Warn("Could not resolve docker to an absolute path; falling back to bare 'docker' via login shell (isolated server may fail if docker is not on the spawn PATH)",
			zap.String("server", c.config.Name),
			zap.Error(resErr))
	} else if resolved != "" {
		c.logger.Warn("Resolved docker value is not a verified absolute executable; falling back to bare 'docker' via login shell",
			zap.String("server", c.config.Name),
			zap.String("resolved", resolved))
	}
	shellCmd, shellArgs := c.wrapWithUserShell(cmdDocker, finalArgs)
	return shellCmd, shellArgs, true
}

// isDirectExecutable reports whether path is safe to hand to exec directly
// (without a login-shell wrap): it must be an ABSOLUTE path to an existing,
// non-directory file that is executable. This is the Codex round-3 guard for
// MCP-2753 — a resolved value such as a shell function/alias name or a bare
// command name (which `command -v docker` can emit) is rejected so it is
// shell-wrapped instead of failing a direct exec.
func isDirectExecutable(path string) bool {
	if path == "" || !filepath.IsAbs(path) {
		return false
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	if runtime.GOOS == "windows" {
		// Windows file mode bits don't convey the executable flag; an absolute
		// path to a regular file is the strongest portable signal here.
		return true
	}
	return info.Mode().Perm()&0o111 != 0
}

// insertCidfileIntoDockerArgs inserts "--cidfile <file>" immediately after the
// "run" token in a direct-exec docker argument slice (no shell wrap). It is the
// args-based sibling of insertCidfileIntoShellDockerCommand used on the
// direct-exec spawn path (MCP-2753). Operating on argv (rather than a shell
// command string) makes it platform-agnostic and sidesteps the -c vs /c shell
// quirk #697 had to patch.
func (c *Client) insertCidfileIntoDockerArgs(args []string, cidFile string) []string {
	if len(args) == 0 || args[0] != cmdRun {
		c.logger.Warn("Could not find 'run' as the first docker arg for cidfile insertion - container ID tracking may be limited",
			zap.String("server", c.config.Name),
			zap.Strings("args", args))
		return args
	}

	newArgs := make([]string, 0, len(args)+2)
	newArgs = append(newArgs, args[0]) // "run"
	newArgs = append(newArgs, "--cidfile", cidFile)
	newArgs = append(newArgs, args[1:]...)

	c.logger.Debug("Inserted cidfile into direct-exec docker args",
		zap.String("server", c.config.Name),
		zap.String("cid_file", cidFile))
	return newArgs
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
	// Shell args look like:
	//   Unix/bash:     ["-l", "-c", "docker run …"]  → second-to-last is "-c"
	//   Windows cmd:   ["/c",       "docker run …"]  → second-to-last is "/c"
	// Accept both flags so cidfile insertion works on all platforms.
	if len(shellArgs) < 2 {
		c.logger.Error("Unexpected shell command format for Docker cidfile insertion - cannot track container ID",
			zap.String("server", c.config.Name),
			zap.Strings("shell_args", shellArgs),
			zap.String("expected_format", "[shell, -c, docker_command] or [-l, -c, docker_command]"))
		return shellArgs
	}
	secondToLast := shellArgs[len(shellArgs)-2]
	if secondToLast != "-c" && secondToLast != "/c" {
		c.logger.Error("Unexpected shell command format for Docker cidfile insertion - cannot track container ID",
			zap.String("server", c.config.Name),
			zap.Strings("shell_args", shellArgs),
			zap.String("expected_format", "[shell, -c, docker_command] or [-l, -c, docker_command]"))
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
