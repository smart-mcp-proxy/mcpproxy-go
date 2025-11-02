package core

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"go.uber.org/zap"
)

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

// killDockerContainerByCommandWithContext finds and kills containers based on the docker command arguments with context timeout
func (c *Client) killDockerContainerByCommandWithContext(ctx context.Context) {
	c.logger.Info("Container ID not available, searching for containers to clean up",
		zap.String("server", c.config.Name))

	if c.upstreamLogger != nil {
		c.upstreamLogger.Info("Searching for containers to clean up by command signature")
	}

	// First try to find containers by name pattern
	if c.killDockerContainersByNamePatternWithContext(ctx) {
		return // Success, we found and cleaned up containers by name
	}

	// Fallback to finding containers by image name
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

// killDockerContainersByNamePatternWithContext finds and kills containers by name pattern
func (c *Client) killDockerContainersByNamePatternWithContext(ctx context.Context) bool {
	// Create sanitized server name for pattern matching
	sanitized := sanitizeServerNameForContainer(c.config.Name)
	namePattern := "mcpproxy-" + sanitized + "-"

	c.logger.Debug("Searching for containers by name pattern",
		zap.String("server", c.config.Name),
		zap.String("name_pattern", namePattern))

	// Get list of containers with name filter
	listCmd := exec.CommandContext(ctx, "docker", "ps", "-a", "--filter", "name="+namePattern, "--format", "{{.ID}}\t{{.Names}}")
	output, err := listCmd.Output()
	if err != nil {
		c.logger.Debug("Failed to list Docker containers by name pattern",
			zap.String("server", c.config.Name),
			zap.String("name_pattern", namePattern),
			zap.Error(err))
		return false
	}

	// Parse output and find matching containers
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	var containersToKill []string

	for _, line := range lines {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) >= 2 {
			containerID := parts[0]
			containerName := parts[1]

			// Check if the container name starts with our pattern
			if strings.HasPrefix(containerName, namePattern) {
				containersToKill = append(containersToKill, containerID)
				c.logger.Info("Found matching container by name pattern",
					zap.String("server", c.config.Name),
					zap.String("container_id", containerID),
					zap.String("container_name", containerName))
			}
		}
	}

	if len(containersToKill) == 0 {
		c.logger.Debug("No matching containers found by name pattern",
			zap.String("server", c.config.Name),
			zap.String("name_pattern", namePattern))
		return false
	}

	// Kill matching containers
	for _, containerID := range containersToKill {
		c.logger.Info("Killing container by name pattern",
			zap.String("server", c.config.Name),
			zap.String("container_id", containerID))

		if c.upstreamLogger != nil {
			c.upstreamLogger.Info("Killing container by name pattern",
				zap.String("container_id", containerID))
		}

		// First try graceful stop
		stopCmd := exec.CommandContext(ctx, "docker", "stop", containerID)
		if err := stopCmd.Run(); err != nil {
			// Force kill if graceful stop fails
			killCmd := exec.CommandContext(ctx, "docker", "kill", containerID)
			if err := killCmd.Run(); err != nil {
				c.logger.Error("Failed to kill container by name pattern",
					zap.String("server", c.config.Name),
					zap.String("container_id", containerID),
					zap.Error(err))
			} else {
				c.logger.Info("Successfully force killed container by name pattern",
					zap.String("server", c.config.Name),
					zap.String("container_id", containerID))
			}
		} else {
			c.logger.Info("Successfully stopped container by name pattern",
				zap.String("server", c.config.Name),
				zap.String("container_id", containerID))
		}
	}

	return true // We found and processed containers
}

// killDockerContainerByNameWithContext kills a specific Docker container by its exact name
func (c *Client) killDockerContainerByNameWithContext(ctx context.Context, containerName string) bool {
	c.logger.Debug("Searching for container by exact name",
		zap.String("server", c.config.Name),
		zap.String("container_name", containerName))

	// Get container ID by exact name match
	listCmd := exec.CommandContext(ctx, "docker", "ps", "-a", "--filter", "name=^"+containerName+"$", "--format", "{{.ID}}")
	output, err := listCmd.Output()
	if err != nil {
		c.logger.Debug("Failed to find Docker container by name",
			zap.String("server", c.config.Name),
			zap.String("container_name", containerName),
			zap.Error(err))
		return false
	}

	containerID := strings.TrimSpace(string(output))
	if containerID == "" {
		c.logger.Debug("No container found with exact name",
			zap.String("server", c.config.Name),
			zap.String("container_name", containerName))
		return false
	}

	c.logger.Info("Found container by name, attempting to kill",
		zap.String("server", c.config.Name),
		zap.String("container_name", containerName),
		zap.String("container_id", containerID))

	if c.upstreamLogger != nil {
		c.upstreamLogger.Info("Killing container by name",
			zap.String("container_name", containerName),
			zap.String("container_id", containerID))
	}

	// First try graceful stop
	stopCmd := exec.CommandContext(ctx, "docker", "stop", containerID)
	if err := stopCmd.Run(); err != nil {
		// Force kill if graceful stop fails
		killCmd := exec.CommandContext(ctx, "docker", "kill", containerID)
		if err := killCmd.Run(); err != nil {
			c.logger.Error("Failed to kill container by name",
				zap.String("server", c.config.Name),
				zap.String("container_name", containerName),
				zap.String("container_id", containerID),
				zap.Error(err))
			return false
		}
		c.logger.Info("Successfully force killed container by name",
			zap.String("server", c.config.Name),
			zap.String("container_name", containerName),
			zap.String("container_id", containerID))
		return true
	}
	c.logger.Info("Successfully stopped container by name",
		zap.String("server", c.config.Name),
		zap.String("container_name", containerName),
		zap.String("container_id", containerID))

	return true
}

// ensureNoExistingContainers removes all existing containers for this server before creating a new one
// This makes container creation idempotent and prevents duplicate container spawning
func (c *Client) ensureNoExistingContainers(ctx context.Context) error {
	sanitized := sanitizeServerNameForContainer(c.config.Name)
	namePattern := "mcpproxy-" + sanitized + "-"

	c.logger.Info("Checking for existing containers before creation",
		zap.String("server", c.config.Name),
		zap.String("name_pattern", namePattern))

	// Find ALL containers matching our server (running or stopped)
	listCmd := exec.CommandContext(ctx, "docker", "ps", "-a",
		"--filter", "name="+namePattern,
		"--format", "{{.ID}}\t{{.Names}}\t{{.Status}}")

	output, err := listCmd.Output()
	if err != nil {
		return fmt.Errorf("failed to list existing containers: %w", err)
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) == 0 || lines[0] == "" {
		c.logger.Debug("No existing containers found - safe to create new one",
			zap.String("server", c.config.Name))
		return nil
	}

	// Found existing containers - clean them up first
	c.logger.Warn("Found existing containers - cleaning up before creating new one",
		zap.String("server", c.config.Name),
		zap.Int("container_count", len(lines)))

	if c.upstreamLogger != nil {
		c.upstreamLogger.Warn("Cleaning up existing containers before creating new one",
			zap.Int("container_count", len(lines)))
	}

	for _, line := range lines {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) >= 2 {
			containerID := parts[0]
			containerName := parts[1]
			status := ""
			if len(parts) >= 3 {
				status = parts[2]
			}

			c.logger.Info("Removing existing container",
				zap.String("server", c.config.Name),
				zap.String("container_id", containerID),
				zap.String("container_name", containerName),
				zap.String("status", status))

			if c.upstreamLogger != nil {
				c.upstreamLogger.Info("Removing existing container",
					zap.String("container_id", containerID),
					zap.String("container_name", containerName))
			}

			// Force remove (works for running and stopped containers)
			rmCmd := exec.CommandContext(ctx, "docker", "rm", "-f", containerID)
			if err := rmCmd.Run(); err != nil {
				c.logger.Error("Failed to remove existing container",
					zap.String("container_id", containerID),
					zap.Error(err))
				// Continue anyway - try to remove others
			} else {
				c.logger.Info("Successfully removed existing container",
					zap.String("container_id", containerID))

				if c.upstreamLogger != nil {
					c.upstreamLogger.Info("Successfully removed existing container",
						zap.String("container_id", containerID))
				}
			}
		}
	}

	return nil
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
