package core

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/launcher"
)

// defaultLauncherWaitTimeout caps how long we'll wait for a locally-launched
// HTTP/SSE upstream to bind its listener. Tight enough that a misconfigured
// command surfaces as a connect failure within a single retry window;
// configurable per-server via ServerConfig.LauncherWaitTimeout.
const defaultLauncherWaitTimeout = 30 * time.Second

// connectWithLauncher starts the local upstream process described by
// c.config (Command/Args/Env/WorkingDir) and waits for c.config.URL to
// accept a TCP connection. Used by HTTP / SSE / streamable-HTTP transports
// when Command is also set on the server config — letting mcpproxy own the
// process lifecycle of an upstream that exposes its MCP endpoint over the
// network rather than stdio.
//
// Stdio transport never goes through this path — it spawns the child via
// mcp-go's stdio transport internally. See connection_stdio.go.
//
// On success, c.launcherHandle is populated; the caller (Connect) is then
// responsible for the transport-level connect and for arranging
// Disconnect-time cleanup. On failure, the child (if it started) is
// already stopped before this function returns.
func (c *Client) connectWithLauncher(ctx context.Context) error {
	if c.config.Command == "" {
		return nil // launcher not requested
	}

	if err := validateStdioConfig(c.config); err != nil {
		return fmt.Errorf("launcher config invalid: %w", err)
	}
	if c.config.URL == "" {
		return fmt.Errorf("launcher requires url for transport %q", c.transportType)
	}
	if err := validateWorkingDir(c.config.WorkingDir); err != nil {
		return fmt.Errorf("launcher working_dir invalid: %w", err)
	}

	// Pre-flight: detect Docker isolation up-front so we can hold the
	// per-server container lock across the entire spawn sequence
	// (matches connectStdio's behaviour at internal/upstream/core/
	// connection_stdio.go:97). Otherwise concurrent reconnects could
	// race to create overlapping containers for the same upstream.
	willUseDocker := (c.config.Command == cmdDocker || strings.HasSuffix(c.config.Command, "/"+cmdDocker)) && len(c.config.Args) > 0 && c.config.Args[0] == cmdRun
	if !willUseDocker && c.isolationManager != nil {
		willUseDocker = c.isolationManager.ShouldIsolate(c.config)
	}
	if willUseDocker {
		lock := globalContainerLock.Lock(c.config.Name)
		defer lock.Unlock()
		if err := c.ensureNoExistingContainers(ctx); err != nil {
			c.logger.Warn("ensure-no-existing-containers failed; continuing",
				zap.String("server", c.config.Name),
				zap.Error(err))
		}
	}

	cmd, isDocker, cidFile, err := c.buildLauncherCmd(ctx, willUseDocker)
	if err != nil {
		return fmt.Errorf("launcher build cmd: %w", err)
	}

	c.isDockerCommand = isDocker

	sink := newLoggerWriter(c.upstreamLogger, c.logger)
	spec := &launcher.Spec{
		Cmd:     cmd,
		LogSink: sink,
		Name:    c.config.Name,
	}

	handle, err := launcher.Spawn(ctx, spec, c.logger)
	if err != nil {
		if cidFile != "" {
			_ = os.Remove(cidFile)
		}
		return fmt.Errorf("launcher spawn: %w", err)
	}

	// Caller (Client.Connect) already holds c.mu for the duration of
	// Connect, so we set the launcher fields directly here. Read paths
	// (watchLauncher, Disconnect, stopLauncher) use proper locking
	// against this write via the same c.mu / c.mu.RLock.
	//
	// Deliberately NOT setting c.processCmd / c.processGroupID — the
	// launcher.Handle owns the child's lifecycle and the existing
	// stdio-path cleanup helpers would race with it. The launcher
	// already places the child in its own pgroup via applyProcAttrs.
	c.launcherHandle = handle
	c.launcherCIDFile = cidFile

	// Watch the child for unexpected exit during connect/steady-state. If
	// the launched process dies after Connect returns we want to react
	// fast (mark disconnected) instead of waiting for the transport's
	// keepalive to time out.
	go c.watchLauncher(handle)

	waitTimeout := c.config.LauncherWaitTimeout.Duration()
	if waitTimeout <= 0 {
		waitTimeout = defaultLauncherWaitTimeout
	}

	c.logger.Info("waiting for upstream URL to become reachable",
		zap.String("server", c.config.Name),
		zap.String("url", c.config.URL),
		zap.Duration("timeout", waitTimeout))

	if err := launcher.WaitForURL(ctx, c.config.URL, waitTimeout); err != nil {
		// Tear down the child we just started — caller will see a
		// failed Connect and the next attempt should start fresh.
		// Release c.mu around handle.Stop because it blocks until the
		// child is reaped; the `connecting` flag (set by Connect)
		// prevents a concurrent Connect from sneaking in. Reacquire
		// before returning so the caller's deferred Unlock balances.
		c.launcherHandle = nil
		c.launcherCIDFile = ""
		c.mu.Unlock()
		stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		if stopErr := handle.Stop(stopCtx); stopErr != nil {
			c.logger.Warn("failed to stop launcher after wait-for-url failure",
				zap.String("server", c.config.Name),
				zap.Error(stopErr))
		}
		cancel()
		if cidFile != "" {
			_ = os.Remove(cidFile)
		}
		c.mu.Lock()
		return fmt.Errorf("launcher wait-for-url: %w", err)
	}

	c.logger.Info("upstream URL is reachable",
		zap.String("server", c.config.Name),
		zap.String("url", c.config.URL))
	return nil
}

// stopLauncher tears down the spawned child, if any. Safe to call when no
// launcher is active. Called from DisconnectWithContext and from the
// connection-failure cleanup path in Connect.
func (c *Client) stopLauncher(ctx context.Context) {
	c.mu.Lock()
	handle := c.launcherHandle
	cidFile := c.launcherCIDFile
	c.launcherHandle = nil
	c.launcherCIDFile = ""
	c.mu.Unlock()

	if handle == nil {
		return
	}

	if err := handle.Stop(ctx); err != nil {
		c.logger.Warn("error stopping upstream child",
			zap.String("server", c.config.Name),
			zap.Error(err))
	}
	if cidFile != "" {
		_ = os.Remove(cidFile)
	}
}

// watchLauncher reacts to an unexpected child exit by closing the MCP
// client and clearing connected state. The reconnect loop in
// internal/upstream/manager.go can then take over.
func (c *Client) watchLauncher(handle launcher.Handle) {
	<-handle.Done()
	c.mu.RLock()
	stillOurs := c.launcherHandle == handle
	connected := c.connected
	c.mu.RUnlock()
	if !stillOurs {
		// Disconnect already cleaned up — the child exit is expected.
		return
	}
	if !connected {
		// We're still inside Connect; the wait-for-url path will see
		// the failure on its own (TCP dial will eventually fail).
		return
	}
	c.logger.Warn("upstream child exited unexpectedly, disconnecting",
		zap.String("server", c.config.Name))
	if err := c.Disconnect(); err != nil {
		c.logger.Error("disconnect after unexpected child exit failed",
			zap.String("server", c.config.Name),
			zap.Error(err))
	}
}

// buildLauncherCmd produces a *exec.Cmd ready to start, mirroring the
// command-prep that connectStdio does today (env merge, Docker isolation,
// shell-wrap, working-dir, --cidfile threading). The launcher will set
// SysProcAttr for process-group cleanup; we don't pre-set it here. The
// caller is responsible for holding globalContainerLock when willUseDocker
// is true (see connectWithLauncher).
//
// The supplied ctx is intentionally not bound to the returned cmd. Connect
// contexts are typically short-lived (deadline-driven), but the launched
// child must outlive Connect: its lifetime is owned by launcher.Handle
// instead. Stdio's connection_stdio.go uses the same pattern (persistent
// ctx for client.Start).
func (c *Client) buildLauncherCmd(_ context.Context, willUseDocker bool) (*exec.Cmd, bool, string, error) {
	envVars := c.envManager.BuildSecureEnvironment()
	for k, v := range c.config.Env {
		found := false
		for i, ev := range envVars {
			if strings.HasPrefix(ev, k+"=") {
				envVars[i] = k + "=" + v
				found = true
				break
			}
		}
		if !found {
			envVars = append(envVars, k+"="+v)
		}
	}

	args := c.config.Args
	var cidFile string

	if willUseDocker {
		if tmp, err := os.CreateTemp("", "mcpproxy-cid-*.txt"); err == nil {
			cidFile = tmp.Name()
			tmp.Close()
			os.Remove(cidFile)
		} else {
			c.logger.Warn("could not create cidfile",
				zap.String("server", c.config.Name),
				zap.Error(err))
		}
	}

	var finalCommand string
	var finalArgs []string

	if c.isolationManager != nil && c.isolationManager.ShouldIsolate(c.config) {
		var dockerShellWrapped bool
		var dockerDir string
		finalCommand, finalArgs, dockerShellWrapped, dockerDir = c.setupDockerIsolation(c.config.Command, args)
		// Prepend the docker bundle dir to the child PATH so the spawned docker
		// can exec its sibling credential helper / tooling on a registry pull
		// (#715). No-op when docker did not resolve to an absolute path.
		envVars = prependDockerDirToPath(envVars, dockerDir)
		if cidFile != "" {
			if dockerShellWrapped {
				finalArgs = c.insertCidfileIntoShellDockerCommand(finalArgs, cidFile)
			} else {
				finalArgs = c.insertCidfileIntoDockerArgs(finalArgs, cidFile)
			}
		}
	} else if isDirectDockerRun := (c.config.Command == cmdDocker || strings.HasSuffix(c.config.Command, "/"+cmdDocker)) && len(args) > 0 && args[0] == cmdRun; isDirectDockerRun {
		// USER-SUPPLIED `docker run …` upstream launched via the launcher path
		// (config.Command IS `docker`). Reuse the SAME resolve→spawn decision as the
		// isolation path and the stdio path (resolveDockerSpawn) so this entrypoint
		// also direct-execs the resolved ABSOLUTE docker binary instead of
		// shell-wrapping bare `docker`, and gets the docker bundle dir prepended to
		// the child PATH (#715 / MCP-2881). Before this it always shell-wrapped bare
		// `docker` with no PATH augmentation, so a registry pull of an uncached image
		// could fail with `docker-credential-desktop … not found in $PATH`.
		argsToWrap := args
		if len(c.config.Env) > 0 {
			argsToWrap = c.injectEnvVarsIntoDockerArgs(args, c.config.Env)
		}

		var dockerShellWrapped bool
		var dockerDir string
		finalCommand, finalArgs, dockerShellWrapped, dockerDir = c.resolveDockerSpawn(argsToWrap)

		// Prepend the docker bundle dir to the child PATH so the spawned docker can
		// exec its sibling credential helper / tooling on a registry pull (#715).
		// No-op when docker did not resolve to an absolute path.
		envVars = prependDockerDirToPath(envVars, dockerDir)

		// Insert --cidfile via the helper that matches how we spawn: args-based on
		// the direct-exec path, string-based on the login-shell fallback.
		if cidFile != "" {
			if dockerShellWrapped {
				finalArgs = c.insertCidfileIntoShellDockerCommand(finalArgs, cidFile)
			} else {
				finalArgs = c.insertCidfileIntoDockerArgs(finalArgs, cidFile)
			}
		}
	} else {
		// Plain (non-docker) launcher command. Shell-wrap for login-env inheritance.
		finalCommand, finalArgs = c.wrapWithUserShell(c.config.Command, args)
	}

	cmd := exec.Command(finalCommand, finalArgs...)
	cmd.Env = envVars
	if c.config.WorkingDir != "" {
		cmd.Dir = c.config.WorkingDir
	}

	c.logger.Debug("launcher command prepared",
		zap.String("server", c.config.Name),
		zap.String("command", finalCommand),
		zap.Strings("args", finalArgs),
		zap.String("working_dir", c.config.WorkingDir),
		zap.Bool("docker", willUseDocker))

	return cmd, willUseDocker, cidFile, nil
}

// loggerWriter bridges an io.Writer (one Write per line) to the per-server
// zap logger so launcher-pumped output lands in the same place as
// `mcpproxy upstream logs <name>` already shows.
type loggerWriter struct {
	primary  *zap.Logger
	fallback *zap.Logger
}

func newLoggerWriter(primary, fallback *zap.Logger) io.Writer {
	if primary == nil && fallback == nil {
		return io.Discard
	}
	return &loggerWriter{primary: primary, fallback: fallback}
}

func (w *loggerWriter) Write(p []byte) (int, error) {
	line := strings.TrimRight(string(p), "\n")
	if line == "" {
		return len(p), nil
	}
	switch {
	case w.primary != nil:
		w.primary.Info(line)
	case w.fallback != nil:
		w.fallback.Info(line)
	}
	return len(p), nil
}
