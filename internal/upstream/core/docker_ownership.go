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
// by this rule. Every housekeeping record that names a container carries
// `container_owner` — the label value — so the attributed log reader
// (internal/logs, D8 rule 3) can prove the subject belongs to the requested
// server; that field is the only administrator-visible change (SC-005).

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
	args := []string{"ps"}
	if includeStopped {
		args = append(args, "-a")
	}
	args = append(args,
		"--filter", "label="+containerOwnerLabel+"="+c.config.Name,
		"--filter", "name="+ownedContainerNamePattern(c.config.Name),
		"--format", ownedContainerFormat)

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
// the container's com.mcpproxy.server label. For a container identified by
// the cidfile of this server's own `docker run`, the launching server is the
// owner by construction.
func containerOwnerField(owner string) zap.Field {
	return zap.String("container_owner", owner)
}

// stopOwnedContainer stops (then force-kills) one owned container and
// records the outcome in both loggers. cleanupPath names the path that found
// the container ("name pattern", "image") for the records.
func (c *Client) stopOwnedContainer(ctx context.Context, container ownedContainer, cleanupPath string) {
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
			return
		}
		c.logger.Info("Successfully force killed owned container",
			zap.String("server", c.config.Name),
			zap.String("cleanup_path", cleanupPath),
			zap.String("container_id", container.ID),
			containerOwnerField(container.Owner))
		return
	}
	c.logger.Info("Successfully stopped owned container",
		zap.String("server", c.config.Name),
		zap.String("cleanup_path", cleanupPath),
		zap.String("container_id", container.ID),
		containerOwnerField(container.Owner))
}
