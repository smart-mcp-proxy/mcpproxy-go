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

// cidfileReadAttempts × cidfileReadInterval bounds how long the cidfile is
// polled for (10 s by default: image pulls take a while). Variables so tests
// can shorten the wait.
var (
	cidfileReadAttempts = 100
	cidfileReadInterval = 100 * time.Millisecond
)

// readContainerIDWithContext reads the container ID from cidfile for tracking with context cancellation
func (c *Client) readContainerIDWithContext(ctx context.Context, cidFile string) {
	c.logger.Debug("Starting container ID tracking",
		zap.String("server", c.config.Name),
		zap.String("cid_file", cidFile))

	// Wait for container to start and write CID file - longer timeout for image pulls
	for attempt := 0; attempt < cidfileReadAttempts; attempt++ {
		select {
		case <-ctx.Done():
			c.logger.Debug("Container ID tracking canceled",
				zap.String("server", c.config.Name),
				zap.String("cid_file", cidFile))
			return
		default:
			time.Sleep(cidfileReadInterval)

			cidBytes, err := os.ReadFile(cidFile)
			if err == nil {
				containerID := strings.TrimSpace(string(cidBytes))
				if containerID != "" {
					// Clean up the cidfile now that we have the ID
					os.Remove(cidFile)
					c.trackCidfileContainer(ctx, containerID, attempt)
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

	// Fallback: find the container by its exact tracked name. Only a
	// container that passes ownsContainer (label read back AND canonical
	// name) is adopted; a foreign `--label com.mcpproxy.server=<us> --name
	// <tracked>` container is left alone and never named in our log.
	if c.containerName != "" {
		found, ok, err := c.lookupOwnedContainerByName(ctx, c.containerName)
		if err != nil {
			c.logger.Debug("Failed to look up container by name",
				zap.String("server", c.config.Name),
				zap.String("container_name", c.containerName),
				zap.Error(err))
		}
		if ok {
			c.mu.Lock()
			c.containerID = found.ID
			c.mu.Unlock()

			c.logger.Info("Successfully recovered container ID via name lookup",
				zap.String("server", c.config.Name),
				zap.String("container_id", shortContainerID(found.ID)),
				zap.String("full_container_id", found.ID),
				zap.String("container_name", found.Name),
				containerOwnerField(found.Owner))

			if c.upstreamLogger != nil {
				c.upstreamLogger.Info("Container ID recovered via name lookup",
					zap.String("container_id", found.ID),
					zap.String("container_name", found.Name),
					containerOwnerField(found.Owner))
			}

			// Clean up the cidfile since we got the ID
			os.Remove(cidFile)
			return
		}
	}

	c.logger.Error("Failed to recover container ID - container will be orphaned on disconnect",
		zap.String("server", c.config.Name),
		zap.String("container_name", c.containerName))

	if c.upstreamLogger != nil {
		c.upstreamLogger.Error("Failed to recover container ID - may be orphaned")
	}
}

// trackCidfileContainer adopts the id our `docker run` wrote to its cidfile
// — but only after inspecting it: a user-configured direct `docker run
// --name custom` upstream gets a cidfile too, yet carries neither the
// ownership label nor a canonical name, and under D9 such a container is not
// ours to stop or to name in our log (codex round 1). container_owner is the
// label read back, never this server's name.
func (c *Client) trackCidfileContainer(ctx context.Context, containerID string, attempt int) {
	owned, ok, err := c.lookupOwnedContainerByID(ctx, containerID)
	switch {
	case err != nil:
		c.logger.Warn("Could not verify ownership of the container from the cidfile - it will not be managed",
			zap.String("server", c.config.Name),
			zap.String("container_id", shortContainerID(containerID)),
			zap.Error(err))
		if c.upstreamLogger != nil {
			c.upstreamLogger.Warn("Could not verify ownership of the container from the cidfile - it will not be managed",
				zap.Error(err))
		}
		return
	case !ok:
		c.logger.Info("Container from the cidfile is not canonically owned by this server (no com.mcpproxy.server label or non-canonical name) - it will not be stopped on disconnect",
			zap.String("server", c.config.Name),
			zap.String("container_id", shortContainerID(containerID)))
		if c.upstreamLogger != nil {
			c.upstreamLogger.Info("Container from the cidfile is not canonically owned by this server - it will not be stopped on disconnect")
		}
		return
	}

	c.mu.Lock()
	c.containerID = containerID
	c.mu.Unlock()

	c.logger.Info("Docker container ID captured for cleanup",
		zap.String("server", c.config.Name),
		zap.String("container_id", shortContainerID(containerID)),
		zap.String("full_container_id", containerID),
		zap.String("container_name", owned.Name),
		containerOwnerField(owned.Owner),
		zap.Int("attempt", attempt))

	if c.upstreamLogger != nil {
		c.upstreamLogger.Info("Container ID captured",
			zap.String("container_id", containerID),
			zap.String("container_name", owned.Name),
			containerOwnerField(owned.Owner),
			zap.Int("attempt", attempt))
	}
}

// killDockerContainerWithContext stops (then kills) the tracked container
// during disconnect. The id was adopted through trackCidfileContainer or the
// name recovery, but ownership is re-established here, at the moment of the
// mutation — `docker rename` or a foreign container reusing an id prefix
// could have changed the answer — so no path stops a container the
// predicate does not admit now.
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

	// Clear the tracked id whatever happens below: after this call the
	// container is either stopped or deliberately left alone.
	// Note: Caller already holds the mutex lock
	c.containerID = ""

	owned, ok, err := c.lookupOwnedContainerByID(ctx, containerID)
	switch {
	case err != nil:
		c.logger.Warn("Could not verify ownership of the tracked container - leaving it alone",
			zap.String("server", c.config.Name),
			zap.String("container_id", shortContainerID(containerID)),
			zap.Error(err))
		if c.upstreamLogger != nil {
			c.upstreamLogger.Warn("Could not verify ownership of the tracked container - leaving it alone", zap.Error(err))
		}
		return
	case !ok:
		c.logger.Info("Tracked container is not canonically owned by this server - leaving it alone",
			zap.String("server", c.config.Name),
			zap.String("container_id", shortContainerID(containerID)))
		if c.upstreamLogger != nil {
			c.upstreamLogger.Info("Tracked container is not canonically owned by this server - leaving it alone")
		}
		return
	}

	c.logger.Info("Killing Docker container during disconnect",
		zap.String("server", c.config.Name),
		zap.String("container_id", shortContainerID(containerID)),
		zap.String("full_container_id", containerID),
		zap.String("container_name", owned.Name),
		containerOwnerField(owned.Owner))

	c.stopOwnedContainer(ctx, owned, "cidfile")

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

// killDockerContainerByNameWithContext kills the container this server
// tracks by its exact name (the one setupDockerIsolation generated). The
// lookup applies ownsContainer — label read back AND canonical name — so a
// foreign `--label com.mcpproxy.server=<us> --name <tracked>` container is
// neither stopped nor named in our log (Spec 105 FR-007 / D9; codex round 1).
func (c *Client) killDockerContainerByNameWithContext(ctx context.Context, containerName string) bool {
	c.logger.Debug("Searching for owned container by exact name",
		zap.String("server", c.config.Name),
		zap.String("container_name", containerName))

	found, ok, err := c.lookupOwnedContainerByName(ctx, containerName)
	if err != nil {
		c.logger.Debug("Failed to find Docker container by name",
			zap.String("server", c.config.Name),
			zap.String("container_name", containerName),
			zap.Error(err))
		return false
	}
	if !ok {
		c.logger.Debug("No owned container found with exact name",
			zap.String("server", c.config.Name),
			zap.String("container_name", containerName))
		return false
	}

	return c.stopOwnedContainer(ctx, found, "exact name")
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
		// container_owner: the count is of THIS server's owned containers
		// (D8 rule 3 treats a count as a container subject, since the pre-105
		// sweep counted co-owners' containers too). Like every other
		// container record, the owner is the label Docker reported
		// (ownedContainer.Owner — one value for every row, since
		// ownsContainer admits only rows whose label equals this server's
		// raw name), never the requesting server's name (D9, codex round 3).
		c.upstreamLogger.Warn("Cleaning up existing containers before creating new one",
			zap.Int("container_count", len(owned)),
			containerOwnerField(owned[0].Owner))
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
