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
	c.readContainerIDWithContext(context.Background(), cidFile)
}

// readContainerIDWithContext reads the container ID from cidfile for tracking with context cancellation
func (c *Client) readContainerIDWithContext(ctx context.Context, cidFile string) {
	c.logger.Debug("Starting container ID tracking",
		zap.String("server", c.config.Name),
		zap.String("cid_file", cidFile))

	// Wait for container to start and write CID file - longer timeout for image pulls
	for attempt := 0; attempt < 100; attempt++ { // Wait up to 10 seconds
		select {
		case <-ctx.Done():
			c.logger.Debug("Container ID tracking canceled",
				zap.String("server", c.config.Name),
				zap.String("cid_file", cidFile))
			return
		default:
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
						zap.String("container_id", containerID[:12]), // Show short ID
						zap.String("full_container_id", containerID),
						zap.Int("attempt", attempt))

					if c.upstreamLogger != nil {
						c.upstreamLogger.Info("Container ID captured",
							zap.String("container_id", containerID),
							zap.Int("attempt", attempt))
					}

					// Clean up the cidfile now that we have the ID
					os.Remove(cidFile)
					return
				}
			} else if attempt%10 == 0 { // Log every 1 second
				c.logger.Debug("Waiting for container ID file",
					zap.String("server", c.config.Name),
					zap.String("cid_file", cidFile),
					zap.Int("attempt", attempt),
					zap.Error(err))
			}
		}
	}

	c.logger.Warn("Failed to read Docker container ID from cidfile after 10 seconds",
		zap.String("server", c.config.Name),
		zap.String("cid_file", cidFile))
}

// killDockerContainer kills the Docker container if one is running
func (c *Client) killDockerContainer() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c.killDockerContainerWithContext(ctx)
}

// killDockerContainerWithContext kills the Docker container if one is running with context timeout
// NOTE: This function expects the caller to already hold the mutex lock
func (c *Client) killDockerContainerWithContext(ctx context.Context) {
	c.logger.Debug("Starting Docker container kill process",
		zap.String("server", c.config.Name))

	// Don't lock here - caller already holds the lock
	containerID := c.containerID

	if containerID == "" {
		c.logger.Debug("No container ID available for cleanup",
			zap.String("server", c.config.Name))
		return
	}

	c.logger.Info("Killing Docker container during disconnect",
		zap.String("server", c.config.Name),
		zap.String("container_id", containerID[:12]),
		zap.String("full_container_id", containerID))

	if c.upstreamLogger != nil {
		c.upstreamLogger.Info("Killing Docker container",
			zap.String("container_id", containerID))
	}

	// First try graceful stop (SIGTERM)
	c.logger.Debug("Attempting graceful stop",
		zap.String("server", c.config.Name),
		zap.String("container_id", containerID[:12]))

	stopCmd := exec.CommandContext(ctx, "docker", "stop", containerID)
	c.logger.Debug("Executing docker stop command",
		zap.String("server", c.config.Name),
		zap.String("container_id", containerID[:12]))

	if err := stopCmd.Run(); err != nil {
		c.logger.Warn("Failed to stop Docker container gracefully, trying force kill",
			zap.String("server", c.config.Name),
			zap.String("container_id", containerID[:12]),
			zap.Error(err))

		// Force kill (SIGKILL)
		c.logger.Debug("Attempting force kill",
			zap.String("server", c.config.Name),
			zap.String("container_id", containerID[:12]))

		killCmd := exec.CommandContext(ctx, "docker", "kill", containerID)
		c.logger.Debug("Executing docker kill command",
			zap.String("server", c.config.Name),
			zap.String("container_id", containerID[:12]))

		if err := killCmd.Run(); err != nil {
			c.logger.Error("Failed to force kill Docker container",
				zap.String("server", c.config.Name),
				zap.String("container_id", containerID[:12]),
				zap.Error(err))

			if c.upstreamLogger != nil {
				c.upstreamLogger.Error("Failed to kill container", zap.Error(err))
			}
		} else {
			c.logger.Info("Docker container force killed successfully",
				zap.String("server", c.config.Name),
				zap.String("container_id", containerID[:12]))

			if c.upstreamLogger != nil {
				c.upstreamLogger.Info("Container force killed successfully")
			}
		}
	} else {
		c.logger.Info("Docker container stopped gracefully",
			zap.String("server", c.config.Name),
			zap.String("container_id", containerID[:12]))

		if c.upstreamLogger != nil {
			c.upstreamLogger.Info("Container stopped gracefully")
		}
	}

	c.logger.Debug("Docker stop/kill commands completed, clearing container ID",
		zap.String("server", c.config.Name),
		zap.String("container_id", containerID[:12]))

	// Clear the container ID after cleanup attempt
	// Note: Caller already holds the mutex lock
	c.containerID = ""

	c.logger.Debug("Container cleanup process finished",
		zap.String("server", c.config.Name))
}

// killDockerContainerByCommand finds and kills containers based on the docker command arguments
func (c *Client) killDockerContainerByCommand() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c.killDockerContainerByCommandWithContext(ctx)
}

// killDockerContainerByCommandWithContext finds and kills containers based on the docker command arguments with context timeout
func (c *Client) killDockerContainerByCommandWithContext(ctx context.Context) {
	c.logger.Info("Container ID not available, searching for containers to clean up",
		zap.String("server", c.config.Name))

	if c.upstreamLogger != nil {
		c.upstreamLogger.Info("Searching for containers to clean up by command signature")
	}

	// Try to find containers that match our image name instead of command pattern
	var imageName string
	if len(c.config.Args) > 2 {
		// Look for the image name in args (typically last argument for docker run)
		for i := len(c.config.Args) - 1; i >= 0; i-- {
			arg := c.config.Args[i]
			// Skip flags that start with -
			if !strings.HasPrefix(arg, "-") && !strings.Contains(arg, "=") {
				imageName = arg
				break
			}
		}
	}

	if imageName == "" {
		c.logger.Warn("No image name found in docker command args",
			zap.String("server", c.config.Name),
			zap.Strings("args", c.config.Args))
		return
	}

	c.logger.Debug("Searching for containers by image name",
		zap.String("server", c.config.Name),
		zap.String("image_name", imageName))

	// Get list of running containers with image and created time
	listCmd := exec.CommandContext(ctx, "docker", "ps", "--format", "{{.ID}}\t{{.Image}}\t{{.CreatedAt}}")
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
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) >= 2 {
			containerID := parts[0]
			image := parts[1]

			// Check if this container matches our image
			if image == imageName {
				containersToKill = append(containersToKill, containerID)
				c.logger.Info("Found matching container for cleanup",
					zap.String("server", c.config.Name),
					zap.String("container_id", containerID),
					zap.String("image", image))
			}
		}
	}

	if len(containersToKill) == 0 {
		c.logger.Debug("No matching containers found for cleanup",
			zap.String("server", c.config.Name),
			zap.String("image_name", imageName))
		return
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
