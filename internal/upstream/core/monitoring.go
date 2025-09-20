package core

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"go.uber.org/zap"
)

// StartStderrMonitoring starts monitoring stderr output and logging it
func (c *Client) StartStderrMonitoring() {
	if c.stderr == nil || c.transportType != transportStdio {
		return
	}

	// Create context for stderr monitoring
	c.stderrMonitoringCtx, c.stderrMonitoringCancel = context.WithCancel(context.Background())

	c.stderrMonitoringWG.Add(1)
	go func() {
		defer c.stderrMonitoringWG.Done()
		c.monitorStderr()
	}()

	c.logger.Debug("Started stderr monitoring",
		zap.String("server", c.config.Name))
}

// StopStderrMonitoring stops stderr monitoring
func (c *Client) StopStderrMonitoring() {
	if c.stderrMonitoringCancel != nil {
		c.stderrMonitoringCancel()

		// Use a timeout for the wait to prevent hanging
		done := make(chan struct{})
		go func() {
			c.stderrMonitoringWG.Wait()
			close(done)
		}()

		select {
		case <-done:
			c.logger.Debug("Stopped stderr monitoring",
				zap.String("server", c.config.Name))
		case <-time.After(500 * time.Millisecond):
			c.logger.Warn("Stderr monitoring stop timed out after 500ms, forcing shutdown",
				zap.String("server", c.config.Name))
		}
	}
}

// StartProcessMonitoring starts monitoring the underlying process
func (c *Client) StartProcessMonitoring() {
	// Start monitoring even if processCmd is nil for Docker containers
	if c.processCmd == nil && !c.isDockerCommand {
		return
	}

	// Create context for process monitoring
	c.processMonitorCtx, c.processMonitorCancel = context.WithCancel(context.Background())

	c.processMonitorWG.Add(1)
	go func() {
		defer c.processMonitorWG.Done()
		c.monitorProcess()
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
	if c.processMonitorCancel != nil {
		c.processMonitorCancel()

		// Use a timeout for the wait to prevent hanging
		done := make(chan struct{})
		go func() {
			c.processMonitorWG.Wait()
			close(done)
		}()

		select {
		case <-done:
			c.logger.Debug("Stopped process monitoring",
				zap.String("server", c.config.Name))
		case <-time.After(500 * time.Millisecond):
			c.logger.Warn("Process monitoring stop timed out after 500ms, forcing shutdown",
				zap.String("server", c.config.Name))
		}
	}
}

// monitorProcess monitors the underlying process health
func (c *Client) monitorProcess() {
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
		case <-c.processMonitorCtx.Done():
			return
		case <-ticker.C:
			if isDocker {
				c.checkDockerContainerHealth()
			}
		}
	}
}

// monitorStderr monitors stderr output and logs it to both main and server-specific logs
func (c *Client) monitorStderr() {
	scanner := bufio.NewScanner(c.stderr)
	for scanner.Scan() {
		select {
		case <-c.stderrMonitoringCtx.Done():
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
	// Wait a bit for container to start and CID file to be written
	select {
	case <-ctx.Done():
		c.logger.Debug("Docker logs monitoring canceled before start",
			zap.String("server", c.config.Name),
			zap.String("cid_file", cidFile))
		return
	case <-time.After(500 * time.Millisecond):
	}

	// Read container ID from file
	cidBytes, err := os.ReadFile(cidFile)
	if err != nil {
		c.logger.Debug("Could not read container ID file",
			zap.String("server", c.config.Name),
			zap.String("cid_file", cidFile),
			zap.Error(err))
		return
	}

	containerID := strings.TrimSpace(string(cidBytes))
	if containerID == "" {
		return
	}

	// Clean up the temp file
	defer os.Remove(cidFile)

	c.logger.Info("Starting Docker logs monitoring",
		zap.String("server", c.config.Name),
		zap.String("container_id", containerID[:12])) // Show short ID

	// Start docker logs -f command with context cancellation
	cmd := exec.CommandContext(ctx, "docker", "logs", "-f", "--timestamps", containerID)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		c.logger.Warn("Failed to create docker logs stdout pipe", zap.Error(err))
		return
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		c.logger.Warn("Failed to create docker logs stderr pipe", zap.Error(err))
		return
	}

	if err := cmd.Start(); err != nil {
		c.logger.Warn("Failed to start docker logs command", zap.Error(err))
		return
	}

	// Monitor both stdout and stderr from docker logs with context cancellation
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			select {
			case <-ctx.Done():
				return
			default:
				line := strings.TrimSpace(scanner.Text())
				if line != "" {
					c.logger.Info("container logs (stdout)",
						zap.String("server", c.config.Name),
						zap.String("container_id", containerID[:12]),
						zap.String("message", line))
				}
			}
		}
	}()

	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			select {
			case <-ctx.Done():
				return
			default:
				line := strings.TrimSpace(scanner.Text())
				if line != "" {
					c.logger.Info("container logs (stderr)",
						zap.String("server", c.config.Name),
						zap.String("container_id", containerID[:12]),
						zap.String("message", line))
				}
			}
		}
	}()

	// Wait for docker logs command to finish (when container stops) or context cancellation
	if err := cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			c.logger.Debug("Docker logs command canceled",
				zap.String("server", c.config.Name),
				zap.String("container_id", containerID[:12]))
		} else {
			c.logger.Debug("Docker logs command ended with error",
				zap.String("server", c.config.Name),
				zap.String("container_id", containerID[:12]),
				zap.Error(err))
		}
	} else {
		c.logger.Debug("Docker logs monitoring ended",
			zap.String("server", c.config.Name),
			zap.String("container_id", containerID[:12]))
	}
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

		cmd := exec.CommandContext(ctx, "docker", "version", "--format", "{{.Server.Version}}")
		if err := cmd.Run(); err != nil {
			diagnostics["docker_daemon_reachable"] = false
			diagnostics["docker_daemon_error"] = err.Error()
		} else {
			diagnostics["docker_daemon_reachable"] = true
		}

		// Check if container is still running
		if c.containerID != "" {
			inspectCmd := exec.CommandContext(ctx, "docker", "inspect", "--format", "{{.State.Running}}", c.containerID)
			if output, err := inspectCmd.Output(); err == nil {
				diagnostics["container_running"] = strings.TrimSpace(string(output)) == "true"
			}
		}
	}

	return diagnostics
}
