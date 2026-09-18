package core

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/logs"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"
)

const (
	// maxRecentStderrLines bounds the per-client ring buffer of recent
	// stderr output. Kept small because this is meant for the last few
	// lines before a failure — not a log archive.
	maxRecentStderrLines = 20
	// maxStderrLineLen truncates individual lines stored in the ring
	// buffer to protect memory against a misbehaving child that spews
	// huge single lines (e.g. a base64-encoded traceback).
	maxStderrLineLen = 512
)

// StartStderrMonitoring starts monitoring stderr output and logging it
func (c *Client) StartStderrMonitoring() {
	c.monitoringMu.Lock()
	defer c.monitoringMu.Unlock()

	if c.stderr == nil || c.transportType != transportStdio {
		return
	}

	// Capture the stderr reader as a local under monitoringMu. connectStdio
	// reassigns c.stderr on every (re)connect (connection_stdio.go:217); passing
	// the reader as a goroutine arg keeps monitorStderr from reading the shared
	// field, so a later reconnect's write never races a lingering monitor's read
	// (the connectStdio↔monitorStderr data race, MCP-816).
	stderr := c.stderr

	// Create context for stderr monitoring. The monitor goroutine receives the
	// context, stderr reader, and its done channel as locals so an abandoned
	// (timed-out) goroutine never reads the shared fields a later Start may
	// overwrite.
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	c.stderrMonitoringCtx, c.stderrMonitoringCancel = ctx, cancel
	c.stderrMonitoringDone = done

	go func() {
		defer close(done)
		c.monitorStderr(ctx, stderr)
	}()

	c.logger.Debug("Started stderr monitoring",
		zap.String("server", c.config.Name))
}

// StopStderrMonitoring stops stderr monitoring
func (c *Client) StopStderrMonitoring() {
	c.monitoringMu.Lock()
	defer c.monitoringMu.Unlock()

	if c.stderrMonitoringCancel == nil {
		return
	}

	c.stderrMonitoringCancel()
	done := c.stderrMonitoringDone
	c.stderrMonitoringCancel = nil
	c.stderrMonitoringDone = nil
	if done == nil {
		return
	}

	// Wait for the monitor goroutine directly under monitoringMu (no detached
	// waiter that could outlive the lock). On timeout the goroutine is abandoned;
	// it closes its own done channel and touches only its captured ctx, so it
	// cannot race a subsequent Start.
	select {
	case <-done:
		c.logger.Debug("Stopped stderr monitoring",
			zap.String("server", c.config.Name))
	case <-time.After(500 * time.Millisecond):
		c.logger.Warn("Stderr monitoring stop timed out after 500ms, forcing shutdown",
			zap.String("server", c.config.Name))
	}
}

// StartProcessMonitoring starts monitoring the underlying process
func (c *Client) StartProcessMonitoring() {
	c.monitoringMu.Lock()
	defer c.monitoringMu.Unlock()

	// Start monitoring even if processCmd is nil for Docker containers
	if c.processCmd == nil && !c.isDockerCommand {
		return
	}

	// Create context for process monitoring (ctx + done passed as locals; see
	// StartStderrMonitoring for the abandoned-goroutine rationale).
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	c.processMonitorCtx, c.processMonitorCancel = ctx, cancel
	c.processMonitorDone = done

	go func() {
		defer close(done)
		c.monitorProcess(ctx)
	}()

	if c.processCmd != nil {
		c.logger.Debug("Started process monitoring",
			zap.String("server", c.config.Name),
			zap.String("command", c.processCmd.Path),
			zap.Int("pid", c.processCmd.Process.Pid))
	} else {
		c.logger.Debug("Started Docker container monitoring",
			zap.String("server", c.config.Name),
			zap.String("command", c.config.Command))
	}
}

// StopProcessMonitoring stops process monitoring
func (c *Client) StopProcessMonitoring() {
	c.monitoringMu.Lock()
	defer c.monitoringMu.Unlock()

	if c.processMonitorCancel == nil {
		return
	}

	c.processMonitorCancel()
	done := c.processMonitorDone
	c.processMonitorCancel = nil
	c.processMonitorDone = nil
	if done == nil {
		return
	}

	select {
	case <-done:
		c.logger.Debug("Stopped process monitoring",
			zap.String("server", c.config.Name))
	case <-time.After(500 * time.Millisecond):
		c.logger.Warn("Process monitoring stop timed out after 500ms, forcing shutdown",
			zap.String("server", c.config.Name))
	}
}

// monitorProcess monitors the underlying process health
func (c *Client) monitorProcess(ctx context.Context) {
	// Only return early if we have neither processCmd nor Docker command
	if c.processCmd == nil && !c.isDockerCommand {
		return
	}

	// Check if this is a Docker command
	isDocker := strings.Contains(c.config.Command, "docker")

	ticker := time.NewTicker(5 * time.Second) // Check every 5 seconds
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if isDocker {
				c.checkDockerContainerHealth()
			}
		}
	}
}

// monitorStderr monitors stderr output and logs it to both main and server-specific logs.
// The stderr reader is passed as an argument (captured under monitoringMu by the
// caller) rather than read from c.stderr, so a concurrent connectStdio reassigning
// the shared field cannot race this goroutine's read (MCP-816).
func (c *Client) monitorStderr(ctx context.Context, stderr io.Reader) {
	scanner := bufio.NewScanner(stderr)
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return
		default:
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}

			// #1158 (review round 2, live check): this is the child's own
			// stderr, and it lands in THREE places from here — main.log, the
			// per-server log that GET /api/v1/servers/{id}/logs serves, and the
			// recent-stderr ring buffer that connectStdio splices into the
			// "did not respond to MCP initialize" error, which is itself
			// logged and returned over REST. A live run proved it: a server
			// whose startup banner named its own token published that token
			// eight times. Scrubbing the line once here covers all three,
			// which is the same shape as the launcher's loggerWriter fix.
			line = oauth.ScrubUpstreamText(line)

			// Log to main logger
			c.logger.Info("stderr output",
				zap.String("server", c.config.Name),
				zap.String("message", line))

			// Log to server-specific logger if available. The child's text is
			// a field value stamped child_output=true (Spec 105 FR-007, D8
			// rules 1 and 3): a docker CLI failure on the isolation path names
			// a colliding container — another server's — and the attributed
			// reader withholds child-output records that mention a container.
			if c.upstreamLogger != nil {
				c.upstreamLogger.Info("stderr", zap.String("message", line), logs.ChildOutputField())
			}

			c.recordRecentStderr(line)
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

// dockerLogsWaitTimeout bounds how long monitorDockerLogsWithContext waits
// for the container id to be tracked. A variable so tests can shorten it.
var dockerLogsWaitTimeout = 10 * time.Second

// monitorDockerLogsWithContext monitors Docker container logs using `docker
// logs` with context cancellation. The container it names is only ever the
// one trackCidfileContainer verified (id and owner read back from Docker):
// it never reads the cidfile itself, since a cidfile can name a container
// that is not this server's (Spec 105 D9, codex round 6).
func (c *Client) monitorDockerLogsWithContext(ctx context.Context, cidFile string) {
	waitTicker := time.NewTicker(100 * time.Millisecond)
	defer waitTicker.Stop()

	waitTimeout := time.NewTimer(dockerLogsWaitTimeout)
	defer waitTimeout.Stop()

	var containerID, containerOwner string

waitLoop:
	for {
		select {
		case <-ctx.Done():
			c.logger.Debug("Docker logs monitoring canceled before container ID available",
				zap.String("server", c.config.Name),
				zap.String("cid_file", cidFile))
			return
		case <-waitTimeout.C:
			c.logger.Debug("Docker logs monitoring timed out before a verified container ID was tracked",
				zap.String("server", c.config.Name),
				zap.String("cid_file", cidFile))
			return
		case <-waitTicker.C:
			c.mu.RLock()
			containerID, containerOwner = c.containerID, c.containerOwner
			c.mu.RUnlock()
			if containerID != "" {
				break waitLoop
			}
		}
	}

	// Docker container log streaming disabled by default to prevent log flooding and performance issues.
	// Container logs are already captured by Docker's logging driver (json-file with rotation).
	// Use `docker logs <container_id>` to view container logs when debugging.
	c.logger.Debug("Docker container started - logs available via 'docker logs' command",
		zap.String("server", c.config.Name),
		zap.String("container_id", shortContainerID(containerID)),
		containerOwnerField(containerOwner),
		zap.String("command", fmt.Sprintf("docker logs -f %s", shortContainerID(containerID))))

	// Note: We intentionally do NOT stream container logs to mcpproxy logs because:
	// 1. It causes massive log file bloat (multiple GB per day with active containers)
	// 2. It floods tray application logs and Web UI event streams
	// 3. Docker's built-in logging driver already handles log rotation and persistence
	// 4. Users can access container logs directly via `docker logs <container_id>`
	//
	// If log streaming is needed for debugging, enable it with a feature flag in config.

	// Simply wait for context cancellation - no log streaming
	<-ctx.Done()
	c.logger.Debug("Docker logs monitoring ended",
		zap.String("server", c.config.Name),
		zap.String("container_id", shortContainerID(containerID)),
		containerOwnerField(containerOwner))
}

// recordRecentStderr appends a stderr line to the bounded ring buffer.
func (c *Client) recordRecentStderr(line string) {
	if line == "" {
		return
	}
	if len(line) > maxStderrLineLen {
		line = line[:maxStderrLineLen] + "…"
	}
	c.recentStderrMu.Lock()
	defer c.recentStderrMu.Unlock()
	c.recentStderr = append(c.recentStderr, line)
	if overflow := len(c.recentStderr) - maxRecentStderrLines; overflow > 0 {
		c.recentStderr = append([]string(nil), c.recentStderr[overflow:]...)
	}
}

// RecentStderrSnapshot returns a copy of the recent stderr lines captured
// from the child process. Returns nil if nothing has been captured yet.
func (c *Client) RecentStderrSnapshot() []string {
	c.recentStderrMu.Lock()
	defer c.recentStderrMu.Unlock()
	if len(c.recentStderr) == 0 {
		return nil
	}
	out := make([]string, len(c.recentStderr))
	copy(out, c.recentStderr)
	return out
}

// formatRecentStderr returns a human-readable, indented block of recent
// stderr lines suitable for embedding in an error message. Empty when no
// stderr has been captured.
//
// Two readability transforms are applied (#696): a "command not found"
// actionable hint is led when the child failed to resolve a binary (notably
// docker), and runs of identical consecutive lines are collapsed into a single
// "… (repeated N×)" entry so a process that prints the same error on each of
// its ~20 connection retries produces one readable line instead of a wall.
func (c *Client) formatRecentStderr() string {
	lines := c.RecentStderrSnapshot()
	if len(lines) == 0 {
		return ""
	}
	var b strings.Builder
	if hint := commandNotFoundHint(lines); hint != "" {
		b.WriteString(hint)
		b.WriteByte('\n')
	}
	for _, l := range collapseRepeatedLines(lines) {
		b.WriteString("  | ")
		b.WriteString(l)
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}

// collapseRepeatedLines collapses runs of identical consecutive lines into a
// single "<line> (repeated N×)" entry. Non-repeated lines pass through verbatim.
func collapseRepeatedLines(lines []string) []string {
	if len(lines) == 0 {
		return nil
	}
	out := make([]string, 0, len(lines))
	for i := 0; i < len(lines); {
		j := i + 1
		for j < len(lines) && lines[j] == lines[i] {
			j++
		}
		if n := j - i; n > 1 {
			out = append(out, fmt.Sprintf("%s (repeated %d×)", lines[i], n))
		} else {
			out = append(out, lines[i])
		}
		i = j
	}
	return out
}

var (
	// cmdNotFoundZshRe matches zsh's form: "zsh:1: command not found: docker".
	cmdNotFoundZshRe = regexp.MustCompile(`command not found: (\S+)`)
	// cmdNotFoundBashRe matches bash/sh's form: "bash: docker: command not found".
	cmdNotFoundBashRe = regexp.MustCompile(`([^\s:]+): command not found`)
)

// extractMissingCommand returns the name of a binary the shell could not find
// in a stderr line, or "" if the line is not a "command not found" error.
func extractMissingCommand(line string) string {
	if m := cmdNotFoundZshRe.FindStringSubmatch(line); m != nil {
		return strings.Trim(m[1], `"'`)
	}
	if m := cmdNotFoundBashRe.FindStringSubmatch(line); m != nil {
		return strings.Trim(m[1], `"'`)
	}
	return ""
}

// commandNotFoundHint scans captured stderr for a shell "command not found"
// error and returns a single actionable message, or "" if none is present. The
// docker-specific case (#696) points the user at the app-bundle binary that
// Docker Desktop ships even when the optional CLI-tools step was skipped.
func commandNotFoundHint(lines []string) string {
	for _, l := range lines {
		cmd := extractMissingCommand(l)
		if cmd == "" {
			continue
		}
		if cmd == "docker" {
			return "Docker CLI not found on PATH. Install Docker Desktop CLI tools, or it is bundled at /Applications/Docker.app/Contents/Resources/bin/docker — restart the affected servers."
		}
		return fmt.Sprintf("Command %q not found on the spawn PATH. Ensure it is installed and on PATH, then restart the affected servers.", cmd)
	}
	return ""
}

func shortContainerID(id string) string {
	if len(id) <= 12 {
		return id
	}
	return id[:12]
}

// CheckConnectionHealth performs a health check on the connection
func (c *Client) CheckConnectionHealth(ctx context.Context) error {
	if !c.IsConnected() {
		return fmt.Errorf("client not connected")
	}

	// For stdio connections, try a simple ping-like operation
	if c.transportType == transportStdio {
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

	// Issue #1148, round 8: this map is a server-payload shape (command + argv)
	// and nothing about it is private to this package, so it takes the shared
	// LIVE rule like every other door — before it acquires a caller rather than
	// after.
	diagnostics := map[string]interface{}{
		"connected":       c.connected,
		"transport_type":  c.transportType,
		"server_name":     c.config.Name,
		"command":         oauth.LiveRedaction.Leaf("command", c.config.Command),
		"args":            oauth.LiveRedaction.Argv(c.config.Args),
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
	if c.isDockerCommand {
		diagnostics["is_docker"] = true
		diagnostics["docker_args"] = oauth.LiveRedaction.Argv(c.config.Args)

		// Check Docker daemon connectivity
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		cmd := c.newDockerCmd(ctx, "version", "--format", "{{.Server.Version}}")
		if err := cmd.Run(); err != nil {
			diagnostics["docker_daemon_reachable"] = false
			diagnostics["docker_daemon_error"] = err.Error()
		} else {
			diagnostics["docker_daemon_reachable"] = true
		}

		// Spec 105 D8/D9: `docker inspect <id>` resolves purely by id, so
		// publishing and inspecting the tracked id directly let a container
		// another Docker client relabelled or renamed after tracking still
		// report as this server's and running — the same stale-ownership
		// failure verifyDockerContainerHealthy fixed for the manager's
		// health path (codex round 8). Re-verify through the same
		// ContainerMutator.Verify read+predicate before publishing anything:
		// once the predicate no longer holds (or the re-read itself fails),
		// the diagnostics name no container id and report it not running;
		// only a container ownership confirms right now is published, with
		// the container_owner read back at that same moment.
		//
		// Running state comes from that SAME read, never a follow-up
		// `docker inspect` (codex round 16 finding 1): a second, separately
		// timed command by id alone would report whatever container holds
		// that id AT THAT LATER MOMENT — which ownership may no longer
		// belong to — while the diagnostics kept attributing it to this
		// server. ContainerRow.Running derives it from the ps row Verify
		// already read.
		if c.containerID != "" {
			row, ok, err := c.containerMutator().Verify(ctx, c.containerID)
			if err != nil || !ok {
				diagnostics["container_running"] = false
			} else {
				diagnostics["container_id"] = row.ID
				diagnostics["container_owner"] = row.Owner
				diagnostics["container_running"] = row.Running()
			}
		}
	}

	return diagnostics
}
