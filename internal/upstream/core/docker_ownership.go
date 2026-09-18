package core

import (
	"context"
	"os/exec"
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
// A container is owned by server S, on THIS mcpproxy instance, iff ALL hold:
//   - its com.mcpproxy.server label equals S's raw name exactly,
//   - its com.mcpproxy.instance label equals this process's own instance id
//     (core.GetInstanceID(), instance.go) exactly, and
//   - its name matches ^mcpproxy-<sanitised(S)>-[a-z0-9]{4}$ (the regex guards
//     against a foreign process re-using the label).
//
// The instance check matters even though every reader here is scoped to one
// server name: two separate mcpproxy processes (distinct data dirs) can both
// configure a server literally named `a`, and without it either instance's
// cleanup would stop, kill or rm the OTHER instance's live container for
// that name — a container neither created nor is otherwise entitled to touch
// (the vulnerability class this file exists to close, now recurring one
// level up: server-name-scoped but not instance-scoped is exactly as
// canonical as name-scoped but not label-scoped was pre-105).
//
// Docker applies all three filters server-side (`--filter label=` is an
// exact match, `--filter name=` a regexp match) and ownsContainer re-checks
// them in Go, so no container that fails any of them is ever mutated or
// logged. Pre-label containers, containers from another instance, and
// user-`--name` containers are all left alone: they never were ours by this
// rule. That holds on EVERY stop/kill/rm path, including the two that
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
//
// Ownership is a fact about NOW, not about a listing (codex rounds 5 and 6):
// another Docker client can rename or relabel a container — or replace it
// under an id that extends the listed one — between the `docker ps` that
// found it and the stop/kill/rm that acts on it. So every mutation, on every
// path here and in the manager's sweeps, goes through ContainerMutator: it
// re-reads that one container's FULL id, name and label immediately before
// the command, applies the predicate again, refuses (naming no id) when it
// no longer holds, and hands back the row it read so the caller's records
// carry container_id and container_owner from that read alone.

// containerOwnerLabel is the Docker label carrying the RAW server name of the
// mcpproxy server a container was created for (formatContainerLabels).
const containerOwnerLabel = "com.mcpproxy.server"

// containerInstanceLabel is the Docker label carrying this mcpproxy
// PROCESS's instance id (formatContainerLabels, instance.go). Two mcpproxy
// instances on one Docker host can each configure a server with the same
// name — distinct data dirs, distinct config.db, but nothing stops the same
// server name appearing in both — and before this label was checked here,
// ownsContainer admitted either instance's container for that name (codex
// round: FR-007 canonical ownership was server-name-scoped but not
// instance-scoped, so it was neither canonical across instances nor immune
// to two COOPERATING mcpproxy processes colliding on a name). That is the
// gap this label closes.
//
// It does NOT, and cannot, make ownership cryptographically unforgeable
// against a fully Docker-capable adversary (codex round 2): Docker labels
// are plain, uninterpreted, unauthenticated string metadata — anyone who
// can run `docker run --label` can copy this instance's real id verbatim
// (readable off any of its own containers with a plain `docker inspect`,
// no guessing required) onto a container of their own. That actor already
// holds the Docker socket, i.e. is already equivalent to root on this
// host's containers; no label scheme defeats them, and the pre-existing
// com.mcpproxy.server check never claimed to either (ContainerOwnedByAny's
// own doc: "which any foreign container can copy"). FR-007's canonical
// ownership is scoped to distinguishing mcpproxy's OWN legitimate
// containers — between configured servers, and now between live mcpproxy
// instances — not to authenticating labels against a host-level attacker;
// that would need a different mechanism entirely (signed labels, or state
// kept outside Docker's label store) and is out of this fix's scope.
const containerInstanceLabel = "com.mcpproxy.instance"

// ownedContainerSuffixPattern is the random suffix generateRandomSuffix
// produces: four lowercase alphanumerics.
const ownedContainerSuffixPattern = "[a-z0-9]{4}"

// ownedContainerNamePattern returns the anchored regexp every container
// owned by serverName must match by name.
func ownedContainerNamePattern(serverName string) string {
	return "^mcpproxy-" + regexp.QuoteMeta(sanitizeServerNameForContainer(serverName)) + "-" + ownedContainerSuffixPattern + "$"
}

// ownsContainer is the Go-side ownership predicate: label AND canonical name
// AND this process's own instance id. instanceLabel is the
// com.mcpproxy.instance value Docker reported for the row; a container
// created by a different mcpproxy instance (or one with no instance label at
// all — pre-#1300, or forged) fails this exactly like a pre-label container
// fails the server-name half: it is a foreign container, never touched.
func ownsContainer(serverName, containerName, ownerLabel, instanceLabel string) bool {
	if ownerLabel != serverName {
		return false
	}
	if instanceLabel == "" || instanceLabel != getInstanceID() {
		return false
	}
	matched, err := regexp.MatchString(ownedContainerNamePattern(serverName), containerName)
	return err == nil && matched
}

// ownedContainer is one `docker ps` row that passed the ownership predicate.
type ownedContainer struct {
	ID       string
	Name     string
	Status   string
	Image    string
	Owner    string // the com.mcpproxy.server label value (== the server's raw name)
	Instance string // the com.mcpproxy.instance label value
}

// ownedContainerFormat is the `docker ps --format` template the ownership
// listing reads: one tab-separated row per container, label values last so
// an empty label leaves its column empty rather than shifting the others.
const ownedContainerFormat = "{{.ID}}\t{{.Names}}\t{{.Status}}\t{{.Image}}\t{{.Label \"" + containerOwnerLabel + "\"}}\t{{.Label \"" + containerInstanceLabel + "\"}}"

// listOwnedContainers lists the containers canonically owned by this server.
// includeStopped adds `-a` (stopped containers too). Rows that fail the
// Go-side predicate are dropped before anything is logged or mutated.
func (c *Client) listOwnedContainers(ctx context.Context, includeStopped bool) ([]ownedContainer, error) {
	return c.listOwnedContainersFiltered(ctx, includeStopped,
		"label="+containerOwnerLabel+"="+c.config.Name,
		"label="+containerInstanceLabel+"="+getInstanceID(),
		"name="+ownedContainerNamePattern(c.config.Name))
}

// lookupOwnedContainerByID resolves one full container id (the cidfile id)
// to an owned container. ok is false when Docker knows no such container or
// it fails ownsContainer — a user-`--name` container, a pre-label one, a
// foreign one — in which case the caller leaves it alone. `--filter id=` is
// a prefix match, so the row is matched back by its full id exactly.
func (c *Client) lookupOwnedContainerByID(ctx context.Context, id string) (ownedContainer, bool, error) {
	rows, err := c.listOwnedContainersFiltered(ctx, true, "id="+id)
	if err != nil {
		return ownedContainer{}, false, err
	}
	for _, row := range rows {
		if row.ID == id {
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
		"label="+containerInstanceLabel+"="+getInstanceID(),
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

// listOwnedContainersFiltered runs `docker ps [-a] --no-trunc --filter
// <f>...` with the ownership --format and returns only the rows that pass
// ownsContainer with the label value Docker reported. Every lookup goes
// through here so no path can act on, or log, a container the predicate did
// not admit. --no-trunc makes {{.ID}} the full id, which is what every
// mutation is later matched back against exactly.
func (c *Client) listOwnedContainersFiltered(ctx context.Context, includeStopped bool, filters ...string) ([]ownedContainer, error) {
	args := []string{"ps"}
	if includeStopped {
		args = append(args, "-a")
	}
	args = append(args, "--no-trunc")
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
		// EXACTLY 6, never "at least": Docker label VALUES are arbitrary
		// bytes with no tab-escaping, so a label an attacker controls
		// (Owner or Instance, on a container they created themselves) could
		// otherwise smuggle "<real-value>\t<garbage>" past an exact-match
		// comparison — the embedded tab reads as one more field boundary,
		// shifting everything after it, so a `< 6` (at-least) check would
		// still accept parts[4]/parts[5] as exactly the real value with the
		// forged suffix silently absorbed into the row that follows. A
		// genuine row from ownedContainerFormat's 5 literal tabs always
		// splits to exactly 6 fields; any other count is unparseable or
		// tampered and the row is dropped rather than guessed at (codex
		// round, HIGH: FR-007 instance-scoping fix).
		parts := strings.Split(line, "\t")
		if len(parts) != 6 {
			continue
		}
		row := ownedContainer{ID: parts[0], Name: parts[1], Status: parts[2], Image: parts[3], Owner: parts[4], Instance: parts[5]}
		if !ownsContainer(c.config.Name, row.Name, row.Owner, row.Instance) {
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

// dockerContainerLogFields renders the container_name/container_id/
// container_owner fields for a lifecycle housekeeping line (connection
// failure, init failure, disconnect) — but only when containerID is
// non-empty. containerID is assigned exactly by trackCidfileContainer or the
// cidfile-timeout name-recovery fallback (docker.go), both of which verify
// ownership via Docker's label/canonical-name read-back before ever setting
// it, and always pair it with containerOwner (the label value read back)
// under the same lock, clearing both together on cleanup. containerName
// alone is set at spawn time from the GENERATED canonical name, before
// Docker has confirmed anything exists — it is not evidence on its own
// (Spec 105 D8): another Docker client can have relabelled or reused that
// exact name for a colliding server between generation and this log line.
// So an empty containerID here means the record names the server only,
// never a container; these callers must not perform a Docker read of their
// own to firm the name up — they run on failure/disconnect paths where the
// tracked state is all there is to go on.
func dockerContainerLogFields(containerID, containerName, containerOwner string) []zap.Field {
	if containerID == "" {
		return nil
	}
	return []zap.Field{
		zap.String("container_name", containerName),
		zap.String("container_id", containerID),
		containerOwnerField(containerOwner),
	}
}

// ContainerMutation is one of the docker commands that change a container's
// state.
type ContainerMutation string

const (
	ContainerStop   ContainerMutation = "stop"
	ContainerKill   ContainerMutation = "kill"
	ContainerRemove ContainerMutation = "rm" // run as `docker rm -f`
)

// DockerCommand builds one docker invocation: the core client resolves the
// binary through newDockerCmd, the manager execs the bare name.
type DockerCommand func(ctx context.Context, args ...string) *exec.Cmd

// ContainerRow is a container's identity AND running state as Docker
// reported them in one read: full id, name, com.mcpproxy.server label value
// and `docker ps`'s own human status text (e.g. "Up 5 minutes",
// "Exited (0) 2 minutes ago", "Up 5 minutes (Paused)"). Status, not just
// identity, comes from this same read (codex round 16 finding 1): a caller
// that re-read state with a SEPARATE `docker inspect <id>` after Verify
// trusted whatever container held that id at the LATER moment, which another
// Docker client can have relabelled or renamed in between.
type ContainerRow struct {
	ID       string
	Name     string
	Owner    string
	Instance string
	Status   string
}

// Running reports whether the container was up — running or paused, exactly
// `docker inspect`'s State.Running — at the read that produced this row.
// `docker ps --format` has no `.Running` boolean field (verified against a
// live daemon: only `.State`, the short State.Status word, and `.Status`,
// the human text `docker ps` prints in its STATUS column); `.State` is
// State.Status, not State.Running, so a paused container (State.Status
// "paused", State.Running true) would misreport as not running through it.
// Docker's own convention for that STATUS text prefixes "Up" precisely when
// State.Running is true — including while paused ("Up 5 minutes (Paused)")
// — so Status carries the same information State.Running would, without a
// second command.
func (r ContainerRow) Running() bool {
	return strings.HasPrefix(r.Status, "Up")
}

// containerRowFormat is the `docker ps --format` a mutation's re-read uses.
// {{.Status}} rides along with identity so a caller deciding running/healthy
// state never needs a second, separately-timed `docker inspect` (codex round
// 16 finding 1): ownership and state come from the identical read.
const containerRowFormat = "{{.ID}}\t{{.Names}}\t{{.Label \"" + containerOwnerLabel + "\"}}\t{{.Label \"" + containerInstanceLabel + "\"}}\t{{.Status}}"

// MutationResult is what ContainerMutator.Mutate reports. Verified is true
// when ownership held at the re-read and the command ran, in which case
// Container is the row read then and Err the docker error if the command
// failed. Verified false with a nil Err means the container no longer
// satisfies the predicate (or is gone); with a non-nil Err the re-read
// itself failed. Nothing was run in either case.
type MutationResult struct {
	Container ContainerRow
	Verified  bool
	Err       error
}

// ContainerMutator is the one verify-then-mutate implementation every Docker
// mutation goes through — the core client's cleanup paths and the manager's
// sweeps alike (Spec 105 FR-007 / D9, codex round 6). Docker runs the CLI;
// Owns is the ownership predicate over the name and label read back
// (ownsContainer for one server, ContainerOwnedByAny for the manager).
type ContainerMutator struct {
	Docker DockerCommand
	Owns   func(containerName, ownerLabel, instanceLabel string) bool
}

// Mutate re-reads container id immediately before running op on it and runs
// it only if the row read back has exactly that full id and satisfies Owns.
// intent, when set, is called with that row right before the command so the
// caller can record what is about to happen with mutation-time evidence.
func (cm ContainerMutator) Mutate(ctx context.Context, id string, op ContainerMutation, intent func(ContainerRow)) MutationResult {
	row, ok, err := cm.Verify(ctx, id)
	if err != nil {
		return MutationResult{Err: err}
	}
	if !ok {
		return MutationResult{}
	}
	if intent != nil {
		intent(row)
	}
	args := []string{string(op)}
	if op == ContainerRemove {
		args = append(args, "-f")
	}
	args = append(args, row.ID)
	return MutationResult{Container: row, Verified: true, Err: cm.Docker(ctx, args...).Run()}
}

// Verify re-reads container id and reports whether it still satisfies Owns
// right now — the same read+predicate Mutate applies before running a
// command, exposed for a caller that only needs to confirm ownership
// without mutating anything (e.g. a health check re-establishing ownership
// before trusting `docker inspect`, codex round 8). ok is true only when
// the read succeeded and the row's label and name pass Owns; row is the
// meaningful evidence — id, name and owner label as Docker reported them —
// only when ok is true.
func (cm ContainerMutator) Verify(ctx context.Context, id string) (ContainerRow, bool, error) {
	row, ok, err := cm.read(ctx, id)
	if err != nil {
		return ContainerRow{}, false, err
	}
	if !ok || !cm.Owns(row.Name, row.Owner, row.Instance) {
		return ContainerRow{}, false, nil
	}
	return row, true, nil
}

// read runs `docker ps -a --no-trunc --filter id=<id>` and returns the row
// whose full id is exactly id. `--filter id=` is a prefix match, so a
// different container whose id extends a listed one is not it.
func (cm ContainerMutator) read(ctx context.Context, id string) (ContainerRow, bool, error) {
	output, err := cm.Docker(ctx, "ps", "-a", "--no-trunc", "--filter", "id="+id, "--format", containerRowFormat).Output()
	if err != nil {
		return ContainerRow{}, false, err
	}
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		// EXACTLY 5, never "at least" (codex round, HIGH: FR-007
		// instance-scoping fix): containerRowFormat's 4 literal tabs always
		// split a genuine row to exactly 5 fields, Status possibly empty
		// (a pre-label container docker never started, though that never
		// reaches here) but still present as its own field. Label VALUES
		// have no tab-escaping, so a label an attacker controls (Owner or
		// Instance, on a container they created themselves) could otherwise
		// smuggle "<real-value>\t<garbage>" past an exact-match comparison
		// with the embedded tab read as one more field boundary — an
		// unbounded split rejects that row outright (extra fields, wrong
		// count) instead of accepting a plausible-looking prefix.
		parts := strings.Split(line, "\t")
		if len(parts) != 5 || parts[0] != id {
			continue
		}
		return ContainerRow{ID: parts[0], Name: parts[1], Owner: parts[2], Instance: parts[3], Status: parts[4]}, true, nil
	}
	return ContainerRow{}, false, nil
}

// containerMutator is this client's ContainerMutator: its docker resolver
// and its own ownership predicate.
func (c *Client) containerMutator() ContainerMutator {
	return ContainerMutator{
		Docker: c.newDockerCmd,
		Owns: func(containerName, ownerLabel, instanceLabel string) bool {
			return ownsContainer(c.config.Name, containerName, ownerLabel, instanceLabel)
		},
	}
}

// mutateOwnedContainer runs op on container id through the ContainerMutator
// and records a refusal — naming the server only, never the id — in both
// loggers when the container could not be verified or is no longer this
// server's. The caller records the outcome from the row handed back.
func (c *Client) mutateOwnedContainer(ctx context.Context, id string, op ContainerMutation, cleanupPath string, intent func(ContainerRow)) MutationResult {
	res := c.containerMutator().Mutate(ctx, id, op, intent)
	switch {
	case res.Verified:
	case res.Err != nil:
		c.logger.Warn("Could not verify container ownership before mutation - leaving it alone",
			zap.String("server", c.config.Name),
			zap.String("cleanup_path", cleanupPath),
			zap.String("operation", string(op)),
			zap.Error(res.Err))
		if c.upstreamLogger != nil {
			c.upstreamLogger.Warn("Could not verify container ownership before mutation - leaving it alone",
				zap.String("cleanup_path", cleanupPath),
				zap.String("operation", string(op)),
				zap.Error(res.Err))
		}
	default:
		c.logger.Info("Container is no longer canonically owned by this server - leaving it alone",
			zap.String("server", c.config.Name),
			zap.String("cleanup_path", cleanupPath),
			zap.String("operation", string(op)))
		if c.upstreamLogger != nil {
			c.upstreamLogger.Info("Container is no longer canonically owned by this server - leaving it alone",
				zap.String("cleanup_path", cleanupPath),
				zap.String("operation", string(op)))
		}
	}
	return res
}

// stopOwnedContainer stops (then force-kills) the container tracked or
// listed as id and records the outcome in both loggers. cleanupPath names
// the path that found the container ("name pattern", "image", "cidfile",
// "exact name") for the records. Ownership is re-established immediately
// before the stop AND, since it is a second mutation, again before the kill
// (codex round 6); every record that names the container carries the id and
// owner read for that command. It reports whether the container was stopped
// or killed.
func (c *Client) stopOwnedContainer(ctx context.Context, id, cleanupPath string) bool {
	stop := c.mutateOwnedContainer(ctx, id, ContainerStop, cleanupPath, func(container ContainerRow) {
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
	})
	if !stop.Verified {
		return false
	}
	if stop.Err == nil {
		c.logger.Info("Successfully stopped owned container",
			zap.String("server", c.config.Name),
			zap.String("cleanup_path", cleanupPath),
			zap.String("container_id", stop.Container.ID),
			containerOwnerField(stop.Container.Owner))
		if c.upstreamLogger != nil {
			c.upstreamLogger.Info("Owned container stopped gracefully",
				zap.String("cleanup_path", cleanupPath),
				zap.String("container_id", stop.Container.ID),
				containerOwnerField(stop.Container.Owner))
		}
		return true
	}

	// Force kill if graceful stop fails
	kill := c.mutateOwnedContainer(ctx, id, ContainerKill, cleanupPath, nil)
	if !kill.Verified {
		return false
	}
	if kill.Err != nil {
		c.logger.Error("Failed to kill owned container",
			zap.String("server", c.config.Name),
			zap.String("cleanup_path", cleanupPath),
			zap.String("container_id", kill.Container.ID),
			containerOwnerField(kill.Container.Owner),
			zap.Error(kill.Err))
		if c.upstreamLogger != nil {
			c.upstreamLogger.Error("Failed to kill owned container",
				zap.String("cleanup_path", cleanupPath),
				zap.String("container_id", kill.Container.ID),
				containerOwnerField(kill.Container.Owner),
				zap.Error(kill.Err))
		}
		return false
	}
	c.logger.Info("Successfully force killed owned container",
		zap.String("server", c.config.Name),
		zap.String("cleanup_path", cleanupPath),
		zap.String("container_id", kill.Container.ID),
		containerOwnerField(kill.Container.Owner))
	if c.upstreamLogger != nil {
		c.upstreamLogger.Info("Owned container force killed",
			zap.String("cleanup_path", cleanupPath),
			zap.String("container_id", kill.Container.ID),
			containerOwnerField(kill.Container.Owner))
	}
	return true
}

// ContainerOwnedByAny is the whole-manager predicate: a container (its name,
// its com.mcpproxy.server label and its com.mcpproxy.instance label as
// Docker reported them) is canonically owned by one of serverNames — the
// configured servers, on THIS mcpproxy instance — under the same
// label-AND-name-AND-instance rule ownsContainer applies per server. The
// manager's shutdown and emergency sweeps select containers by the shared
// com.mcpproxy.managed / com.mcpproxy.instance labels, which any foreign
// container can copy; only the rows this admits may be stopped, removed or
// named (codex round 3). Requiring the instance label here too (not just in
// the sweep's own Docker filter) closes the gap where a sweep that filtered
// broadly, or a caller re-verifying a single tracked id with no filter at
// all, would otherwise admit another live mcpproxy instance's container for
// a same-named server.
func ContainerOwnedByAny(serverNames []string, containerName, ownerLabel, instanceLabel string) bool {
	for _, serverName := range serverNames {
		if ownsContainer(serverName, containerName, ownerLabel, instanceLabel) {
			return true
		}
	}
	return false
}

// ForceRemoveTrackedContainerIfOwned is the manager's emergency path for a
// client whose Disconnect hung: `docker rm -f` the container tracked as
// containerID, but only after re-establishing canonical ownership NOW — the
// same ContainerMutator every mutation goes through — so a container
// renamed, relabelled or reused under that id since it was tracked is left
// alone (codex round 3). owned reports whether the predicate admitted the
// container (removal was attempted) and owner is then the
// com.mcpproxy.server label read back at that moment — the evidence the
// caller's own records must carry when they name the container (D8, codex
// round 5); err is the docker error when removal ran and failed, or the
// lookup error. Records carry container_owner from the label read back; an
// unowned container is never named in the per-server log.
func (c *Client) ForceRemoveTrackedContainerIfOwned(ctx context.Context, containerID string) (owner string, owned bool, err error) {
	if containerID == "" {
		return "", false, nil
	}
	res := c.mutateOwnedContainer(ctx, containerID, ContainerRemove, "force", func(container ContainerRow) {
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
	})
	if !res.Verified {
		return "", false, res.Err
	}
	if res.Err != nil {
		c.logger.Error("Failed to force remove owned container",
			zap.String("server", c.config.Name),
			zap.String("cleanup_path", "force"),
			zap.String("container_id", res.Container.ID),
			containerOwnerField(res.Container.Owner),
			zap.Error(res.Err))
		if c.upstreamLogger != nil {
			c.upstreamLogger.Error("Failed to force remove owned container",
				zap.String("cleanup_path", "force"),
				zap.String("container_id", res.Container.ID),
				containerOwnerField(res.Container.Owner),
				zap.Error(res.Err))
		}
		return res.Container.Owner, true, res.Err
	}
	c.logger.Info("Owned container force removed",
		zap.String("server", c.config.Name),
		zap.String("cleanup_path", "force"),
		zap.String("container_id", res.Container.ID),
		containerOwnerField(res.Container.Owner))
	if c.upstreamLogger != nil {
		c.upstreamLogger.Info("Owned container force removed",
			zap.String("cleanup_path", "force"),
			zap.String("container_id", res.Container.ID),
			containerOwnerField(res.Container.Owner))
	}
	return res.Container.Owner, true, nil
}
