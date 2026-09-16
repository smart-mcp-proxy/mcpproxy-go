package core

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/shellwrap"
	"go.uber.org/zap"
)

// newDockerCmd creates an exec.Cmd for Docker. The docker binary itself is
// resolved once via shellwrap.ResolveDockerPath (cached process-wide) so
// that hot paths like checkDockerContainerHealth / GetConnectionDiagnostics
// — which fire every few seconds — do not re-spawn a login shell to
// re-read .zshrc/.bashrc on every invocation.
//
// If docker cannot be resolved directly (rare: uncommon install layout) we
// fall back to wrapping with the user's login shell to preserve the
// original PR's "Docker works when launched from Launchpad" fix.
func (c *Client) newDockerCmd(ctx context.Context, args ...string) *exec.Cmd {
	if dockerBin, err := shellwrap.ResolveDockerPath(c.logger); err == nil && dockerBin != "" {
		cmd := exec.CommandContext(ctx, dockerBin, args...)
		if c.envManager != nil {
			cmd.Env = c.envManager.BuildSecureEnvironment()
		}
		return cmd
	}

	// Fallback: login-shell wrap so the child process picks up the full
	// interactive PATH the user sees in Terminal.
	shell, shellArgs := c.wrapWithUserShell("docker", args)
	cmd := exec.CommandContext(ctx, shell, shellArgs...)
	if c.envManager != nil {
		cmd.Env = c.envManager.BuildSecureEnvironment()
	}
	return cmd
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
							containerOwnerField(c.config.Name),
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

	c.logger.Warn("Failed to read container ID from cidfile, attempting recovery via container name",
		zap.String("server", c.config.Name),
		zap.String("cid_file", cidFile),
		zap.String("container_name", c.containerName))

	if c.upstreamLogger != nil {
		c.upstreamLogger.Warn("cidfile read timeout - attempting name lookup recovery")
	}

	// Fallback: Find container by name
	if c.containerName != "" {
		listCmd := c.newDockerCmd(ctx, "ps",
			"--filter", "label="+containerOwnerLabel+"="+c.config.Name,
			"--filter", fmt.Sprintf("name=^%s$", c.containerName),
			"--format", "{{.ID}}")

		if output, err := listCmd.Output(); err == nil {
			foundID := strings.TrimSpace(string(output))
			if foundID != "" {
				c.mu.Lock()
				c.containerID = foundID
				c.mu.Unlock()

				c.logger.Info("Successfully recovered container ID via name lookup",
					zap.String("server", c.config.Name),
					zap.String("container_id", foundID[:12]),
					zap.String("full_container_id", foundID),
					zap.String("container_name", c.containerName))

				if c.upstreamLogger != nil {
					c.upstreamLogger.Info("Container ID recovered via name lookup",
						zap.String("container_id", foundID),
						containerOwnerField(c.config.Name))
				}

				// Clean up the cidfile since we got the ID
				os.Remove(cidFile)
				return
			}
		}
	}

	c.logger.Error("Failed to recover container ID - container will be orphaned on disconnect",
		zap.String("server", c.config.Name),
		zap.String("container_name", c.containerName))

	if c.upstreamLogger != nil {
		c.upstreamLogger.Error("Failed to recover container ID - may be orphaned")
	}
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
			zap.String("container_id", containerID),
			containerOwnerField(c.config.Name))
	}

	// First try graceful stop (SIGTERM)
	c.logger.Debug("Attempting graceful stop",
		zap.String("server", c.config.Name),
		zap.String("container_id", containerID[:12]))

	stopCmd := c.newDockerCmd(ctx, "stop", containerID)
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

		killCmd := c.newDockerCmd(ctx, "kill", containerID)
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
			zap.Strings("args", logSafeArgs(c.config.Args)))
		return
	}

	c.logger.Debug("Searching for owned containers by image name",
		zap.String("server", c.config.Name),
		zap.String("image_name", imageName))

	// Spec 105 FR-007 / D9: the image-name fallback lists only containers this
	// server canonically owns (label + name regex) and then matches the image,
	// so a foreign container that merely shares the image is neither killed
	// nor written into this server's log.
	owned, err := c.listOwnedContainers(ctx, false)
	if err != nil {
		c.logger.Error("Failed to list Docker containers for cleanup",
			zap.String("server", c.config.Name),
			zap.Error(err))
		return
	}

	var containersToKill []ownedContainer
	for _, container := range owned {
		// Check if this container matches our image
		if container.Image == imageName {
			containersToKill = append(containersToKill, container)
			c.logger.Info("Found matching owned container for cleanup",
				zap.String("server", c.config.Name),
				zap.String("container_id", container.ID),
				zap.String("container_name", container.Name),
				containerOwnerField(container.Owner),
				zap.String("image", container.Image))
		}
	}

	if len(containersToKill) == 0 {
		c.logger.Debug("No matching owned containers found for cleanup",
			zap.String("server", c.config.Name),
			zap.String("image_name", imageName))
		return
	}

	// Kill matching containers
	for _, container := range containersToKill {
		c.stopOwnedContainer(ctx, container, "image")
	}
}

// killDockerContainersByNamePatternWithContext finds and kills the containers
// this server canonically owns (Spec 105 FR-007 / D9: label
// com.mcpproxy.server=<raw name> AND name ^mcpproxy-<sanitised>-[a-z0-9]{4}$).
// Pre-105 this was a `name=mcpproxy-<sanitised>-` substring filter, which
// also matched — and killed, and logged — `a-b`'s containers for server `a`.
func (c *Client) killDockerContainersByNamePatternWithContext(ctx context.Context) bool {
	namePattern := ownedContainerNamePattern(c.config.Name)

	c.logger.Debug("Searching for owned containers by name pattern",
		zap.String("server", c.config.Name),
		zap.String("name_pattern", namePattern))

	owned, err := c.listOwnedContainers(ctx, true)
	if err != nil {
		c.logger.Debug("Failed to list Docker containers by name pattern",
			zap.String("server", c.config.Name),
			zap.String("name_pattern", namePattern),
			zap.Error(err))
		return false
	}

	for _, container := range owned {
		c.logger.Info("Found owned container by name pattern",
			zap.String("server", c.config.Name),
			zap.String("container_id", container.ID),
			zap.String("container_name", container.Name),
			containerOwnerField(container.Owner))
	}

	if len(owned) == 0 {
		c.logger.Debug("No owned containers found by name pattern",
			zap.String("server", c.config.Name),
			zap.String("name_pattern", namePattern))
		return false
	}

	// Kill owned containers
	for _, container := range owned {
		c.stopOwnedContainer(ctx, container, "name pattern")
	}

	return true // We found and processed containers
}

// killDockerContainerByNameWithContext kills a specific Docker container by its exact name
func (c *Client) killDockerContainerByNameWithContext(ctx context.Context, containerName string) bool {
	c.logger.Debug("Searching for container by exact name",
		zap.String("server", c.config.Name),
		zap.String("container_name", containerName))

	// Get container ID by exact name match. The name is the one this server
	// generated for its own container (setupDockerIsolation), and the label
	// filter keeps a same-named foreign container out of the result
	// (Spec 105 FR-007 / D9).
	listCmd := c.newDockerCmd(ctx, "ps", "-a",
		"--filter", "label="+containerOwnerLabel+"="+c.config.Name,
		"--filter", "name=^"+containerName+"$",
		"--format", "{{.ID}}")
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
			zap.String("container_id", containerID),
			containerOwnerField(c.config.Name))
	}

	// First try graceful stop
	stopCmd := c.newDockerCmd(ctx, "stop", containerID)
	if err := stopCmd.Run(); err != nil {
		// Force kill if graceful stop fails
		killCmd := c.newDockerCmd(ctx, "kill", containerID)
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

// ensureNoExistingContainers removes all existing containers this server
// canonically owns before creating a new one. This makes container creation
// idempotent and prevents duplicate container spawning. Ownership is label
// com.mcpproxy.server=<raw name> AND name ^mcpproxy-<sanitised>-[a-z0-9]{4}$
// (Spec 105 FR-007 / D9): a foreign container whose name merely shares the
// prefix — `a-b`'s or `a/b`'s for server `a` — is neither removed nor named
// in this server's log.
func (c *Client) ensureNoExistingContainers(ctx context.Context) error {
	namePattern := ownedContainerNamePattern(c.config.Name)

	c.logger.Info("Checking for existing owned containers before creation",
		zap.String("server", c.config.Name),
		zap.String("name_pattern", namePattern))

	// Find ALL containers owned by this server (running or stopped)
	owned, err := c.listOwnedContainers(ctx, true)
	if err != nil {
		return fmt.Errorf("failed to list existing containers: %w", err)
	}

	if len(owned) == 0 {
		c.logger.Debug("No existing owned containers found - safe to create new one",
			zap.String("server", c.config.Name))
		return nil
	}

	// Found existing containers - clean them up first
	c.logger.Warn("Found existing owned containers - cleaning up before creating new one",
		zap.String("server", c.config.Name),
		zap.Int("container_count", len(owned)))

	if c.upstreamLogger != nil {
		c.upstreamLogger.Warn("Cleaning up existing containers before creating new one",
			zap.Int("container_count", len(owned)))
	}

	for _, container := range owned {
		c.logger.Info("Removing existing container",
			zap.String("server", c.config.Name),
			zap.String("container_id", container.ID),
			zap.String("container_name", container.Name),
			containerOwnerField(container.Owner),
			zap.String("status", container.Status))

		if c.upstreamLogger != nil {
			c.upstreamLogger.Info("Removing existing container",
				zap.String("container_id", container.ID),
				zap.String("container_name", container.Name),
				containerOwnerField(container.Owner))
		}

		// Force remove (works for running and stopped containers)
		rmCmd := c.newDockerCmd(ctx, "rm", "-f", container.ID)
		if err := rmCmd.Run(); err != nil {
			c.logger.Error("Failed to remove existing container",
				zap.String("container_id", container.ID),
				containerOwnerField(container.Owner),
				zap.Error(err))
			// Continue anyway - try to remove others
		} else {
			c.logger.Info("Successfully removed existing container",
				zap.String("container_id", container.ID),
				containerOwnerField(container.Owner))

			if c.upstreamLogger != nil {
				c.upstreamLogger.Info("Successfully removed existing container",
					zap.String("container_id", container.ID),
					containerOwnerField(container.Owner))
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

	cmd := c.newDockerCmd(ctx, "version", "--format", "{{.Server.Version}}")
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
