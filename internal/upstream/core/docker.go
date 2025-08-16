package core

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"time"

	"go.uber.org/zap"
)

// readContainerID reads the container ID from cidfile for tracking
func (c *Client) readContainerID(cidFile string) {
	// Wait for container to start and write CID file - longer timeout for image pulls
	for attempt := 0; attempt < 100; attempt++ { // Wait up to 10 seconds
		time.Sleep(100 * time.Millisecond)

		cidBytes, err := os.ReadFile(cidFile)
		if err == nil {
			containerID := strings.TrimSpace(string(cidBytes))
			if containerID != "" {
				c.mu.Lock()
				c.containerID = containerID
				c.mu.Unlock()

				c.logger.Info("Docker container ID captured for cleanup",
					zap.String("server", c.config.Name),
					zap.String("container_id", containerID[:12])) // Show short ID

				if c.upstreamLogger != nil {
					c.upstreamLogger.Info("Container ID captured",
						zap.String("container_id", containerID))
				}

				// Clean up the cidfile now that we have the ID
				os.Remove(cidFile)
				return
			}
		}
	}

	c.logger.Warn("Failed to read Docker container ID from cidfile after 10 seconds",
		zap.String("server", c.config.Name),
		zap.String("cid_file", cidFile))
}

// killDockerContainer kills the Docker container if one is running
func (c *Client) killDockerContainer() {
	if c.containerID == "" {
		return
	}

	c.logger.Info("Killing Docker container during disconnect",
		zap.String("server", c.config.Name),
		zap.String("container_id", c.containerID[:12]))

	if c.upstreamLogger != nil {
		c.upstreamLogger.Info("Killing Docker container",
			zap.String("container_id", c.containerID))
	}

	// Use a timeout for Docker kill command
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// First try graceful stop (SIGTERM)
	stopCmd := exec.CommandContext(ctx, "docker", "stop", c.containerID)
	if err := stopCmd.Run(); err != nil {
		c.logger.Warn("Failed to stop Docker container gracefully, trying force kill",
			zap.String("server", c.config.Name),
			zap.String("container_id", c.containerID[:12]),
			zap.Error(err))

		// Force kill (SIGKILL)
		killCmd := exec.CommandContext(ctx, "docker", "kill", c.containerID)
		if err := killCmd.Run(); err != nil {
			c.logger.Error("Failed to force kill Docker container",
				zap.String("server", c.config.Name),
				zap.String("container_id", c.containerID[:12]),
				zap.Error(err))

			if c.upstreamLogger != nil {
				c.upstreamLogger.Error("Failed to kill container", zap.Error(err))
			}
		} else {
			c.logger.Info("Docker container force killed successfully",
				zap.String("server", c.config.Name),
				zap.String("container_id", c.containerID[:12]))

			if c.upstreamLogger != nil {
				c.upstreamLogger.Info("Container force killed successfully")
			}
		}
	} else {
		c.logger.Info("Docker container stopped gracefully",
			zap.String("server", c.config.Name),
			zap.String("container_id", c.containerID[:12]))

		if c.upstreamLogger != nil {
			c.upstreamLogger.Info("Container stopped gracefully")
		}
	}
}

// killDockerContainerByCommand finds and kills containers based on the docker command arguments
func (c *Client) killDockerContainerByCommand() {
	c.logger.Info("Container ID not available, searching for containers to clean up",
		zap.String("server", c.config.Name))

	if c.upstreamLogger != nil {
		c.upstreamLogger.Info("Searching for containers to clean up by command signature")
	}

	// Use a timeout for Docker operations
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Try to find containers that match our command signature
	// Look for containers with similar command patterns from our args
	var searchPattern string
	if len(c.config.Args) > 2 {
		// Extract a unique part of the command for searching
		for _, arg := range c.config.Args {
			if strings.Contains(arg, "echo") || strings.Contains(arg, "Container started") {
				searchPattern = "Container started"
				break
			}
		}
	}

	if searchPattern == "" {
		c.logger.Warn("No unique search pattern found in docker command args",
			zap.String("server", c.config.Name),
			zap.Strings("args", c.config.Args))
		return
	}

	// Get list of running containers with full command
	listCmd := exec.CommandContext(ctx, "docker", "ps", "--no-trunc", "--format", "{{.ID}}\t{{.Command}}")
	output, err := listCmd.Output()
	if err != nil {
		c.logger.Error("Failed to list Docker containers for cleanup",
			zap.String("server", c.config.Name),
			zap.Error(err))
		return
	}

	// Parse output and find matching containers
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	var containersToKill []string

	for _, line := range lines {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) == 2 {
			containerID := parts[0]
			command := parts[1]

			// Check if this container matches our command pattern
			if strings.Contains(command, searchPattern) {
				containersToKill = append(containersToKill, containerID)
				c.logger.Info("Found matching container for cleanup",
					zap.String("server", c.config.Name),
					zap.String("container_id", containerID),
					zap.String("command", command))
			}
		}
	}

	// Kill matching containers
	for _, containerID := range containersToKill {
		c.logger.Info("Killing matching Docker container",
			zap.String("server", c.config.Name),
			zap.String("container_id", containerID))

		if c.upstreamLogger != nil {
			c.upstreamLogger.Info("Killing matching container",
				zap.String("container_id", containerID))
		}

		// First try graceful stop
		stopCmd := exec.CommandContext(ctx, "docker", "stop", containerID)
		if err := stopCmd.Run(); err != nil {
			// Force kill if graceful stop fails
			killCmd := exec.CommandContext(ctx, "docker", "kill", containerID)
			if err := killCmd.Run(); err != nil {
				c.logger.Error("Failed to kill matching Docker container",
					zap.String("server", c.config.Name),
					zap.String("container_id", containerID),
					zap.Error(err))
			} else {
				c.logger.Info("Successfully force killed matching Docker container",
					zap.String("server", c.config.Name),
					zap.String("container_id", containerID))
			}
		} else {
			c.logger.Info("Successfully stopped matching Docker container",
				zap.String("server", c.config.Name),
				zap.String("container_id", containerID))
		}
	}
}

// checkDockerContainerHealth checks if Docker containers are still running
func (c *Client) checkDockerContainerHealth() {
	// For Docker commands, we can check if containers are still running
	// This is a simplified check - in production you might want more sophisticated monitoring

	// Try to run a simple docker command to check daemon connectivity
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", "version", "--format", "{{.Server.Version}}")
	if err := cmd.Run(); err != nil {
		c.logger.Warn("Docker daemon appears to be unreachable",
			zap.String("server", c.config.Name),
			zap.Error(err))

		if c.upstreamLogger != nil {
			c.upstreamLogger.Warn("Docker connectivity check failed",
				zap.Error(err))
		}
	}
}
