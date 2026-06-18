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
	// bypasses PATH entirely.
	//
	// Two preconditions gate the direct-exec:
	//
	//  1. The resolved value must be a VERIFIED absolute executable
	//     (isDirectExecutable). ResolveDockerPath's last resort runs
	//     `command -v docker` in the login shell, which can emit a shell
	//     function name, an alias, or a bare command name rather than a real
	//     binary path; direct-exec'ing that would fail with "no such file".
	//
	//  2. The docker daemon-config env (DOCKER_HOST/DOCKER_CONTEXT) must be
	//     guaranteed in the spawn env WITHOUT the login shell
	//     (dockerDaemonEnvGuaranteed). Dropping the `$SHELL -l` wrap also drops
	//     rc-file env inheritance. On macOS the startup hydration (MCP-2751)
	//     already captured DOCKER_* from the login shell into os.Environ(), and
	//     secureenv forwards them — but that hydration is Darwin-only. On Linux
	//     a rootless/remote daemon whose DOCKER_HOST lives only in .profile would
	//     be lost by direct-exec (the regression #699 kept the shell wrap to
	//     avoid). So on non-Darwin we only direct-exec when DOCKER_HOST/
	//     DOCKER_CONTEXT are already in os.Environ(); otherwise we keep the
	//     login-shell wrap so `docker run` still finds the daemon.
	//
	// When we fall back to the shell wrap we still prefer the resolved absolute
	// path as the wrapped command (it sidesteps the login shell's PATH
	// re-derivation), dropping to bare "docker" only when resolution failed.
	resolved, resErr := resolveDockerBinary(c.logger)
	switch {
	case resErr == nil && isDirectExecutable(resolved) && dockerDaemonEnvGuaranteed():
		// INFO (not Debug): make the spawn decision observable in main.log so a
		// field report like #696 ("docker installed but not on PATH") can be
		// triaged from one line — which docker binary was chosen and that it was
		// exec'd DIRECTLY rather than shell-wrapped with bare `docker`.
		c.logger.Info("Docker spawn: direct-exec of resolved docker binary",
			zap.String("server", c.config.Name),
			zap.String("docker_path", resolved),
			zap.Bool("shell_wrapped", false))
		return resolved, finalArgs, false
	case resErr != nil:
		c.logger.Warn("Could not resolve docker to an absolute path; falling back to bare 'docker' via login shell (isolated server may fail if docker is not on the spawn PATH)",
			zap.String("server", c.config.Name),
			zap.Error(resErr))
	case !isDirectExecutable(resolved):
		c.logger.Warn("Resolved docker value is not a verified absolute executable; falling back to login-shell wrap",
			zap.String("server", c.config.Name),
			zap.String("resolved", resolved))
	default:
		// Verified absolute executable, but the daemon env is not guaranteed
		// without the login shell (non-Darwin, no DOCKER_HOST/DOCKER_CONTEXT in
		// os.Environ()). Keep the shell wrap so rc-file DOCKER_* are inherited.
		c.logger.Debug("docker daemon env not guaranteed in process env; keeping login-shell wrap so rc-file DOCKER_* are inherited",
			zap.String("server", c.config.Name),
			zap.String("resolved", resolved))
	}

	dockerBin := cmdDocker
	if isDirectExecutable(resolved) {
		dockerBin = resolved
	}
	// INFO: the login-shell fallback is the ONLY path that can produce #696's
	// `command not found: docker`, so surface that we took it (and whether we at
	// least wrap the resolved absolute path vs. bare `docker`).
	c.logger.Info("Docker spawn: login-shell wrap fallback",
		zap.String("server", c.config.Name),
		zap.String("docker_command", dockerBin),
		zap.Bool("docker_resolved", isDirectExecutable(resolved)),
		zap.Bool("shell_wrapped", true))
	shellCmd, shellArgs := c.wrapWithUserShell(dockerBin, finalArgs)
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
	if runtime.GOOS == osWindows {
		// Windows file mode bits don't convey the executable flag; an absolute
		// path to a regular file is the strongest portable signal here.
		return true
	}
	return info.Mode().Perm()&0o111 != 0
}

// dockerDaemonEnvGOOS mirrors runtime.GOOS; a package var so tests can exercise
// the Darwin branch on a non-Darwin CI host.
var dockerDaemonEnvGOOS = runtime.GOOS

// dockerDaemonEnvGuaranteed reports whether the docker daemon-config env
// (DOCKER_HOST/DOCKER_CONTEXT) is guaranteed to reach a direct-exec'd docker
// process WITHOUT a login-shell wrap.
//
// On macOS the startup login-shell hydration (shellwrap.HydrateFromLoginShell,
// MCP-2751) captures DOCKER_* into os.Environ() — its gate fires whenever a
// docker var is missing — so the secureenv allow-list forwards them and the
// shell wrap is unnecessary. That hydration is Darwin-only, so on other
// platforms direct-exec is only safe to skip the shell wrap when DOCKER_HOST or
// DOCKER_CONTEXT are ALREADY exported into mcpproxy's own environment. When they
// are configured only in the user's login-shell rc (Codex's non-Darwin rootless
// regression on PR #703), we must keep the `$SHELL -l` wrap so `docker run`
// inherits them.
func dockerDaemonEnvGuaranteed() bool {
	if dockerDaemonEnvGOOS == osDarwin {
		return true
	}
	return os.Getenv("DOCKER_HOST") != "" || os.Getenv("DOCKER_CONTEXT") != ""
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
