package core

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"go.uber.org/zap"
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

			// Log to main logger
			c.logger.Info("stderr output",
				zap.String("server", c.config.Name),
				zap.String("message", line))

			// Log to server-specific logger if available
			if c.upstreamLogger != nil {
				c.upstreamLogger.Info("stderr", zap.String("message", line))
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

// monitorDockerLogsWithContext monitors Docker container logs using `docker logs` with context cancellation
func (c *Client) monitorDockerLogsWithContext(ctx context.Context, cidFile string) {
	waitTicker := time.NewTicker(100 * time.Millisecond)
	defer waitTicker.Stop()

	waitTimeout := time.NewTimer(10 * time.Second)
	defer waitTimeout.Stop()

	var containerID string

waitLoop:
	for {
		select {
		case <-ctx.Done():
			c.logger.Debug("Docker logs monitoring canceled before container ID available",
				zap.String("server", c.config.Name),
				zap.String("cid_file", cidFile))
			return
		case <-waitTimeout.C:
			// Fall back to reading the cid file one time in case tracking goroutine failed
			if data, err := os.ReadFile(cidFile); err == nil {
				containerID = strings.TrimSpace(string(data))
				if containerID != "" {
					break waitLoop
				}
			}
			c.logger.Debug("Docker logs monitoring timed out waiting for container ID",
				zap.String("server", c.config.Name),
				zap.String("cid_file", cidFile))
			return
		case <-waitTicker.C:
			c.mu.RLock()
			containerID = c.containerID
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
		zap.String("command", fmt.Sprintf("docker logs -f %s", containerID[:12])))

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
		zap.String("container_id", shortContainerID(containerID)))
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

	diagnostics := map[string]interface{}{
		"connected":       c.connected,
		"transport_type":  c.transportType,
		"server_name":     c.config.Name,
		"command":         c.config.Command,
		"args":            c.config.Args,
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
		diagnostics["docker_args"] = c.config.Args
		diagnostics["container_id"] = c.containerID

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

		// Check if container is still running
		if c.containerID != "" {
			inspectCmd := c.newDockerCmd(ctx, "inspect", "--format", "{{.State.Running}}", c.containerID)
			if output, err := inspectCmd.Output(); err == nil {
				diagnostics["container_running"] = strings.TrimSpace(string(output)) == "true"
			}
		}
	}

	return diagnostics
}
