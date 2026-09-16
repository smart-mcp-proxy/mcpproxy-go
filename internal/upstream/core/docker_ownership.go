package core

import (
	"context"
	"regexp"
	"strings"

	"go.uber.org/zap"
)

// Spec 105 FR-007 (gap FR007-G5, research D9): canonical container ownership.
//
// Every container mcpproxy creates is named `mcpproxy-<sanitised>-<4 chars>`
// (generateContainerName) and labelled `com.mcpproxy.server=<raw name>`
// (formatContainerLabels). The name alone is NOT ownership evidence: `a/b`
// and `a-b` both sanitise to `a-b`, and `a`'s old prefix filter
// (`name=mcpproxy-a-`) is a substring match that also lists `a-b`'s
// containers. Pre-105 every cleanup path removed those foreign containers
// and wrote their ids and names into `a`'s per-server log, which
// `upstream_servers tail_log` serves to an `a`-scoped agent.
//
// A container is owned by server S iff BOTH hold:
//   - its com.mcpproxy.server label equals S's raw name exactly, and
//   - its name matches ^mcpproxy-<sanitised(S)>-[a-z0-9]{4}$ (the regex guards
//     against a foreign process re-using the label).
//
// Docker applies both filters server-side (`--filter label=` is an exact
// match, `--filter name=` a regexp match) and ownsContainer re-checks them in
// Go, so no container that fails either is ever mutated or logged. Pre-label
// containers and user-`--name` containers are left alone: they never were ours
// by this rule. That holds on EVERY stop/kill/rm path, including the two that
// start from a single known container rather than a listing (codex round 1):
// the id read from the cidfile of this server's own `docker run` — a
// user-configured direct `docker run --name custom` gets a cidfile but no
// label and no canonical name — and the exact tracked name. Both look the
// container up (lookupOwnedContainerByID / lookupOwnedContainerByName) and
// apply ownsContainer before acting and before writing a record. Every
// housekeeping record that names a container carries `container_owner` — the
// label value READ BACK from Docker, never the requesting server's name — so
// the attributed log reader (internal/logs, D8 rule 3) can prove the subject
// belongs to the requested server; that field is the only
// administrator-visible change (SC-005).

// containerOwnerLabel is the Docker label carrying the RAW server name of the
// mcpproxy server a container was created for (formatContainerLabels).
const containerOwnerLabel = "com.mcpproxy.server"

// ownedContainerSuffixPattern is the random suffix generateRandomSuffix
// produces: four lowercase alphanumerics.
const ownedContainerSuffixPattern = "[a-z0-9]{4}"

// ownedContainerNamePattern returns the anchored regexp every container
// owned by serverName must match by name.
func ownedContainerNamePattern(serverName string) string {
	return "^mcpproxy-" + regexp.QuoteMeta(sanitizeServerNameForContainer(serverName)) + "-" + ownedContainerSuffixPattern + "$"
}

// ownsContainer is the Go-side ownership predicate: label AND canonical name.
func ownsContainer(serverName, containerName, ownerLabel string) bool {
	if ownerLabel != serverName {
		return false
	}
	matched, err := regexp.MatchString(ownedContainerNamePattern(serverName), containerName)
	return err == nil && matched
}

// ownedContainer is one `docker ps` row that passed the ownership predicate.
type ownedContainer struct {
	ID     string
	Name   string
	Status string
	Image  string
	Owner  string // the com.mcpproxy.server label value (== the server's raw name)
}

// ownedContainerFormat is the `docker ps --format` template the ownership
// listing reads: one tab-separated row per container, label value last so an
// empty label leaves the column empty rather than shifting the others.
const ownedContainerFormat = "{{.ID}}\t{{.Names}}\t{{.Status}}\t{{.Image}}\t{{.Label \"" + containerOwnerLabel + "\"}}"

// listOwnedContainers lists the containers canonically owned by this server.
// includeStopped adds `-a` (stopped containers too). Rows that fail the
// Go-side predicate are dropped before anything is logged or mutated.
func (c *Client) listOwnedContainers(ctx context.Context, includeStopped bool) ([]ownedContainer, error) {
	return c.listOwnedContainersFiltered(ctx, includeStopped,
		"label="+containerOwnerLabel+"="+c.config.Name,
		"name="+ownedContainerNamePattern(c.config.Name))
}

// lookupOwnedContainerByID resolves one container id (a full cidfile id or a
// short id) to an owned container. ok is false when Docker knows no such
// container or it fails ownsContainer — a user-`--name` container, a
// pre-label one, a foreign one — in which case the caller leaves it alone.
// The returned ID is the id the caller passed, so stop/kill/rm and the
// records name the id that was tracked.
func (c *Client) lookupOwnedContainerByID(ctx context.Context, id string) (ownedContainer, bool, error) {
	// `--filter id=` is a prefix match on the full id.
	rows, err := c.listOwnedContainersFiltered(ctx, true, "id="+id)
	if err != nil {
		return ownedContainer{}, false, err
	}
	for _, row := range rows {
		if strings.HasPrefix(id, row.ID) || strings.HasPrefix(row.ID, id) {
			row.ID = id
			return row, true, nil
		}
	}
	return ownedContainer{}, false, nil
}

// lookupOwnedContainerByName resolves one exact container name to an owned
// container. ok is false when no container of that name is canonically owned
// by this server: a foreign `--label com.mcpproxy.server=<name> --name
// custom` container matches the label filter but not the name half of
// ownsContainer and is left alone.
func (c *Client) lookupOwnedContainerByName(ctx context.Context, name string) (ownedContainer, bool, error) {
	rows, err := c.listOwnedContainersFiltered(ctx, true,
		"label="+containerOwnerLabel+"="+c.config.Name,
		"name=^"+regexp.QuoteMeta(name)+"$")
	if err != nil {
		return ownedContainer{}, false, err
	}
	for _, row := range rows {
		if row.Name == name {
			return row, true, nil
		}
	}
	return ownedContainer{}, false, nil
}

// listOwnedContainersFiltered runs `docker ps [-a] --filter <f>...` with the
// ownership --format and returns only the rows that pass ownsContainer with
// the label value Docker reported. Every lookup goes through here so no path
// can act on, or log, a container the predicate did not admit.
func (c *Client) listOwnedContainersFiltered(ctx context.Context, includeStopped bool, filters ...string) ([]ownedContainer, error) {
	args := []string{"ps"}
	if includeStopped {
		args = append(args, "-a")
	}
	for _, filter := range filters {
		args = append(args, "--filter", filter)
	}
	args = append(args, "--format", ownedContainerFormat)

	output, err := c.newDockerCmd(ctx, args...).Output()
	if err != nil {
		return nil, err
	}

	var owned []ownedContainer
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 5 {
			continue
		}
		row := ownedContainer{ID: parts[0], Name: parts[1], Status: parts[2], Image: parts[3], Owner: parts[4]}
		if !ownsContainer(c.config.Name, row.Name, row.Owner) {
			continue
		}
		owned = append(owned, row)
	}
	return owned, nil
}

// containerOwnerField is the housekeeping-record field that lets the
// attributed log reader (D8 rule 3) verify the record's subject: the value of
// the container's com.mcpproxy.server label as Docker reported it
// (ownedContainer.Owner). It is never derived from the requesting server's
// name: a container identified by the cidfile of this server's own `docker
// run` is inspected first, since a direct `docker run --name custom` upstream
// gets a cidfile but no label.
func containerOwnerField(owner string) zap.Field {
	return zap.String("container_owner", owner)
}

// stopOwnedContainer stops (then force-kills) one owned container and
// records the outcome in both loggers. cleanupPath names the path that found
// the container ("name pattern", "image", "cidfile", "exact name") for the
// records. It reports whether the container was stopped or killed.
func (c *Client) stopOwnedContainer(ctx context.Context, container ownedContainer, cleanupPath string) bool {
	c.logger.Info("Killing owned container",
		zap.String("server", c.config.Name),
		zap.String("cleanup_path", cleanupPath),
		zap.String("container_id", container.ID),
		zap.String("container_name", container.Name),
		containerOwnerField(container.Owner))

	if c.upstreamLogger != nil {
		c.upstreamLogger.Info("Killing owned container",
			zap.String("cleanup_path", cleanupPath),
			zap.String("container_id", container.ID),
			zap.String("container_name", container.Name),
			containerOwnerField(container.Owner))
	}

	// First try graceful stop
	stopCmd := c.newDockerCmd(ctx, "stop", container.ID)
	if err := stopCmd.Run(); err != nil {
		// Force kill if graceful stop fails
		killCmd := c.newDockerCmd(ctx, "kill", container.ID)
		if err := killCmd.Run(); err != nil {
			c.logger.Error("Failed to kill owned container",
				zap.String("server", c.config.Name),
				zap.String("cleanup_path", cleanupPath),
				zap.String("container_id", container.ID),
				containerOwnerField(container.Owner),
				zap.Error(err))
			if c.upstreamLogger != nil {
				c.upstreamLogger.Error("Failed to kill owned container",
					zap.String("cleanup_path", cleanupPath),
					zap.String("container_id", container.ID),
					containerOwnerField(container.Owner),
					zap.Error(err))
			}
			return false
		}
		c.logger.Info("Successfully force killed owned container",
			zap.String("server", c.config.Name),
			zap.String("cleanup_path", cleanupPath),
			zap.String("container_id", container.ID),
			containerOwnerField(container.Owner))
		if c.upstreamLogger != nil {
			c.upstreamLogger.Info("Owned container force killed",
				zap.String("cleanup_path", cleanupPath),
				zap.String("container_id", container.ID),
				containerOwnerField(container.Owner))
		}
		return true
	}
	c.logger.Info("Successfully stopped owned container",
		zap.String("server", c.config.Name),
		zap.String("cleanup_path", cleanupPath),
		zap.String("container_id", container.ID),
		containerOwnerField(container.Owner))
	if c.upstreamLogger != nil {
		c.upstreamLogger.Info("Owned container stopped gracefully",
			zap.String("cleanup_path", cleanupPath),
			zap.String("container_id", container.ID),
			containerOwnerField(container.Owner))
	}
	return true
}

// ContainerOwnedByAny is the whole-manager predicate: a container (its name
// and its com.mcpproxy.server label as Docker reported them) is canonically
// owned by one of serverNames — the configured servers — under the same
// label-AND-name rule ownsContainer applies per server. The manager's
// shutdown and emergency sweeps select containers by the shared
// com.mcpproxy.managed / com.mcpproxy.instance labels, which any foreign
// container can copy; only the rows this admits may be stopped, removed or
// named (codex round 3).
func ContainerOwnedByAny(serverNames []string, containerName, ownerLabel string) bool {
	for _, serverName := range serverNames {
		if ownsContainer(serverName, containerName, ownerLabel) {
			return true
		}
	}
	return false
}

// ForceRemoveTrackedContainerIfOwned is the manager's emergency path for a
// client whose Disconnect hung: `docker rm -f` the container tracked as
// containerID, but only after re-establishing canonical ownership NOW — the
// predicate killDockerContainerWithContext applies at the moment of the
// mutation — so a container renamed, relabelled or reused under that id since
// it was tracked is left alone (codex round 3). owned reports whether the
// predicate admitted the container (removal was attempted); err is the docker
// error when removal ran and failed, or the lookup error. Records carry
// container_owner from the label read back; an unowned container is never
// named in the per-server log.
func (c *Client) ForceRemoveTrackedContainerIfOwned(ctx context.Context, containerID string) (owned bool, err error) {
	if containerID == "" {
		return false, nil
	}
	container, ok, err := c.lookupOwnedContainerByID(ctx, containerID)
	switch {
	case err != nil:
		c.logger.Warn("Could not verify ownership of the tracked container for force removal - leaving it alone",
			zap.String("server", c.config.Name),
			zap.String("container_id", shortContainerID(containerID)),
			zap.Error(err))
		if c.upstreamLogger != nil {
			c.upstreamLogger.Warn("Could not verify ownership of the tracked container for force removal - leaving it alone", zap.Error(err))
		}
		return false, err
	case !ok:
		c.logger.Info("Tracked container is not canonically owned by this server - not force removed",
			zap.String("server", c.config.Name),
			zap.String("container_id", shortContainerID(containerID)))
		if c.upstreamLogger != nil {
			c.upstreamLogger.Info("Tracked container is not canonically owned by this server - not force removed")
		}
		return false, nil
	}

	c.logger.Warn("Force removing owned container",
		zap.String("server", c.config.Name),
		zap.String("cleanup_path", "force"),
		zap.String("container_id", container.ID),
		zap.String("container_name", container.Name),
		containerOwnerField(container.Owner))
	if c.upstreamLogger != nil {
		c.upstreamLogger.Warn("Force removing owned container",
			zap.String("cleanup_path", "force"),
			zap.String("container_id", container.ID),
			zap.String("container_name", container.Name),
			containerOwnerField(container.Owner))
	}
	if err := c.newDockerCmd(ctx, "rm", "-f", container.ID).Run(); err != nil {
		c.logger.Error("Failed to force remove owned container",
			zap.String("server", c.config.Name),
			zap.String("cleanup_path", "force"),
			zap.String("container_id", container.ID),
			containerOwnerField(container.Owner),
			zap.Error(err))
		if c.upstreamLogger != nil {
			c.upstreamLogger.Error("Failed to force remove owned container",
				zap.String("cleanup_path", "force"),
				zap.String("container_id", container.ID),
				containerOwnerField(container.Owner),
				zap.Error(err))
		}
		return true, err
	}
	c.logger.Info("Owned container force removed",
		zap.String("server", c.config.Name),
		zap.String("cleanup_path", "force"),
		zap.String("container_id", container.ID),
		containerOwnerField(container.Owner))
	if c.upstreamLogger != nil {
		c.upstreamLogger.Info("Owned container force removed",
			zap.String("cleanup_path", "force"),
			zap.String("container_id", container.ID),
			containerOwnerField(container.Owner))
	}
	return true, nil
}
