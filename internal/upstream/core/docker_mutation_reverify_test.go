package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest/observer"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Codex round 6 (PR E), docker findings 1–3 (Spec 105 FR-007 / research D9,
// moment-of-mutation rule): the core's image-fallback, name-pattern and
// pre-creation cleanups established ownership by a LISTING and consumed it
// on a LATER stop / rm -f, and stopOwnedContainer's kill after a failed stop
// was a second mutation with no read at all. Another Docker client can
// rename or relabel a container — or replace it under an id that extends
// the listed one — between the listing and the command. Every mutation now
// re-reads that one container's full id, name and label immediately before
// the command (ContainerMutator, the one verify-then-mutate implementation
// the manager sweeps share): a container that no longer satisfies the
// predicate is left alone and the refusal recorded without its id or name;
// one that still does is mutated, and every record naming it carries
// container_owner from that mutation-time read.

// ownFixtureNamed is one container under id as name/owner label say.
func ownFixtureNamed(id, name, owner string) []fakeContainer {
	labels := map[string]string{"com.mcpproxy.managed": "true"}
	if owner != "" {
		labels[ownerLabel] = owner
	}
	return []fakeContainer{{ID: id, Name: name, Image: "mcp/example", Status: "Up 2 minutes", Labels: labels}}
}

// assertNoRecordNames asserts no record in either logger carries any of
// needles in its message or a field value.
func assertNoRecordNames(t *testing.T, mainLogs, upLogs *observer.ObservedLogs, needles ...string) {
	t.Helper()
	for _, needle := range needles {
		assert.Empty(t, recordsMentioning(upLogs, needle), "%q written into the per-server log", needle)
		assert.Empty(t, recordsMentioning(mainLogs, needle), "%q written into main log", needle)
	}
}

// assertEveryRecordNamingCarriesOwner asserts every record (both loggers)
// whose field values name one of needles carries container_owner == owner,
// and that at least one such record exists.
func assertEveryRecordNamingCarriesOwner(t *testing.T, mainLogs, upLogs *observer.ObservedLogs, owner string, needles ...string) {
	t.Helper()
	named := 0
	for _, logs := range []*observer.ObservedLogs{mainLogs, upLogs} {
		for _, entry := range logs.All() {
			fields := entry.ContextMap()
			if !fieldsName(fields, needles...) {
				continue
			}
			named++
			assert.Equal(t, owner, fields["container_owner"],
				"record %q must carry the owner read at mutation time: %v", entry.Message, fields)
		}
	}
	assert.NotZero(t, named, "the mutation is recorded with its subject")
}

// fieldsName reports whether any string field value carries one of needles.
func fieldsName(fields map[string]interface{}, needles ...string) bool {
	for _, v := range fields {
		s, ok := v.(string)
		if !ok {
			continue
		}
		for _, needle := range needles {
			if strings.Contains(s, needle) {
				return true
			}
		}
	}
	return false
}

// extendedOwnID is a different container whose full id has the listed
// ownContainerID as a prefix: `docker ps --filter id=` is a prefix match,
// so only an exact full-id comparison tells the two apart.
const extendedOwnID = ownContainerID + "ffffffffffffffffffffffffffffffffffffffffffffffffffff"

func TestDockerMutations_ReverifyOwnershipAtMutationTime(t *testing.T) {
	paths := []struct {
		name string
		run  func(t *testing.T, c *Client)
		verb string
	}{
		{name: "name pattern", verb: "stop",
			run: func(_ *testing.T, c *Client) { c.killDockerContainersByNamePatternWithContext(context.Background()) }},
		{name: "image fallback", verb: "stop",
			run: func(_ *testing.T, c *Client) {
				c.killDockerContainersByImageWithContext(context.Background(), "mcp/example")
			}},
		{name: "exact name", verb: "stop",
			run: func(_ *testing.T, c *Client) {
				c.killDockerContainerByNameWithContext(context.Background(), ownContainerName)
			}},
		{name: "pre-creation rm", verb: "rm -f",
			run: func(t *testing.T, c *Client) { require.NoError(t, c.ensureNoExistingContainers(context.Background())) }},
	}
	arms := []struct {
		name      string
		after     []fakeContainer
		wantOwner string // "" — the container must be left alone
	}{
		{name: "relabelled foreign between listing and mutation", after: ownFixtureNamed(ownContainerID, ownContainerName, "a-b")},
		{name: "renamed to a co-tenant's shape", after: ownFixtureNamed(ownContainerID, foreignContainerName, "a-b")},
		{name: "replaced by a container whose id extends the listed one", after: ownFixtureNamed(extendedOwnID, ownContainerName, "a")},
		{name: "unchanged", after: ownFixtureNamed(ownContainerID, ownContainerName, "a"), wantOwner: "a"},
	}
	for _, path := range paths {
		for _, arm := range arms {
			t.Run(path.name+"/"+arm.name, func(t *testing.T) {
				fd := installFakeDocker(t, ownFixtureNamed(ownContainerID, ownContainerName, "a"))
				// The listing sees the original state, everything after it the changed one.
				fd.swapFixtureAfterPs(t, 1, arm.after)
				c, mainLogs, upLogs := newOwnershipClient("a", &config.ServerConfig{
					Command: "docker", Args: []string{"run", "-i", "--rm", "mcp/example"},
				})

				path.run(t, c)

				for _, line := range fd.invocations(t) {
					if strings.HasPrefix(line, "ps") {
						assert.Contains(t, line, "--no-trunc", "every ownership read must return the FULL id: %q", line)
					}
				}
				mutations := append(fd.mutationsOf(t, ownContainerID), fd.mutationsOf(t, extendedOwnID)...)
				if arm.wantOwner == "" {
					assert.Empty(t, mutations, "a container whose ownership changed was mutated; invocations:\n%s",
						strings.Join(fd.invocations(t), "\n"))
					// The ids are never named; the name only when it is not the
					// tracked one (the exact-name path's own lookup record names
					// what it was asked for — this server's knowledge, not a read).
					needles := []string{ownContainerID, extendedOwnID}
					if arm.after[0].Name != ownContainerName {
						needles = append(needles, arm.after[0].Name)
					}
					assertNoRecordNames(t, mainLogs, upLogs, needles...)
					assert.NotEmpty(t, recordsMentioning(mainLogs, "no longer canonically owned"), "the refusal is recorded")
					return
				}
				assert.Equal(t, []string{path.verb + " " + ownContainerID}, mutations, "an owned container is still cleaned up")
				assertEveryRecordNamingCarriesOwner(t, mainLogs, upLogs, "a", ownContainerID)
			})
		}
	}
}

// Pre-creation cleanup re-verifies per ROW, not per listing: with two owned
// containers listed, the second is relabelled to the colliding co-tenant
// (`a/b` and `a-b` both name mcpproxy-a-b-*, so only the label separates
// them) while the first is being removed. The first is removed; the second
// is left alone and never named.
func TestDockerCleanup_PreCreationReverifiesEachRow_SlashVsDashCollision(t *testing.T) {
	const firstID = "0a0a0a0a0a0a"
	const firstName = "mcpproxy-a-b-wxyz"
	const secondID = "0b0b0b0b0b0b"
	const secondName = "mcpproxy-a-b-q2w3"
	row := func(id, name, owner string) fakeContainer {
		return fakeContainer{ID: id, Name: name, Image: "mcp/example", Status: "Up", Labels: map[string]string{ownerLabel: owner}}
	}
	fd := installFakeDocker(t, []fakeContainer{row(firstID, firstName, "a/b"), row(secondID, secondName, "a/b")})
	// ps 1: the listing; ps 2: the first row's re-read. The second row is
	// a-b's by the time its own re-read happens.
	fd.swapFixtureAfterPs(t, 2, []fakeContainer{row(firstID, firstName, "a/b"), row(secondID, secondName, "a-b")})
	c, mainLogs, upLogs := newOwnershipClient("a/b", nil)

	require.NoError(t, c.ensureNoExistingContainers(context.Background()))

	assert.Equal(t, []string{"rm -f " + firstID}, fd.mutationsOf(t, firstID), "the unchanged first row is removed")
	assert.Empty(t, fd.mutationsOf(t, secondID), "the row relabelled to a-b was removed by a/b; invocations:\n%s",
		strings.Join(fd.invocations(t), "\n"))
	assertNoRecordNames(t, mainLogs, upLogs, secondID, secondName)
	assertEveryRecordNamingCarriesOwner(t, mainLogs, upLogs, "a/b", firstID)
	assert.NotEmpty(t, recordsMentioning(mainLogs, "no longer canonically owned"), "the refusal is recorded")
}

// The kill after a failed `docker stop` is a second mutation: ownership is
// re-read again before it. Exact-name path: ps 1 is the name lookup, ps 2
// the stop's re-read, ps 3 the kill's.
func TestDockerStopEscalation_ReverifiesBeforeKill(t *testing.T) {
	arms := []struct {
		name      string
		after     []fakeContainer
		wantOwner string
	}{
		{name: "relabelled between stop and kill", after: ownFixtureNamed(ownContainerID, ownContainerName, "a-b")},
		{name: "replaced by a container whose id extends the listed one", after: ownFixtureNamed(extendedOwnID, ownContainerName, "a")},
		{name: "unchanged", after: ownFixtureNamed(ownContainerID, ownContainerName, "a"), wantOwner: "a"},
	}
	killMessages := []string{"Owned container force killed", "Successfully force killed owned container", "Failed to kill owned container"}
	for _, arm := range arms {
		t.Run(arm.name, func(t *testing.T) {
			fd := installFakeDocker(t, ownFixtureNamed(ownContainerID, ownContainerName, "a"))
			fd.failVerbs(t, "stop")
			fd.swapFixtureAfterPs(t, 2, arm.after)
			c, mainLogs, upLogs := newOwnershipClient("a", nil)

			stopped := c.killDockerContainerByNameWithContext(context.Background(), ownContainerName)

			require.Contains(t, fd.mutationsOf(t, ownContainerID), "stop "+ownContainerID, "premise: the stop ran with ownership held")
			kills := 0
			for _, line := range fd.invocations(t) {
				if strings.HasPrefix(line, "kill ") {
					kills++
				}
			}
			if arm.wantOwner == "" {
				assert.False(t, stopped)
				assert.Zero(t, kills, "a container whose ownership changed after the stop was killed; invocations:\n%s",
					strings.Join(fd.invocations(t), "\n"))
				assertNoRecordNames(t, mainLogs, upLogs, extendedOwnID, "a-b")
				for _, msg := range killMessages {
					assert.Empty(t, mainLogs.FilterMessage(msg).All(), "%q recorded for a refused kill", msg)
					assert.Empty(t, upLogs.FilterMessage(msg).All(), "%q recorded for a refused kill", msg)
				}
				assert.NotEmpty(t, recordsMentioning(mainLogs, "no longer canonically owned"), "the refusal is recorded")
				// The stop's own records named the container with the owner read for the stop.
				assertEveryRecordNamingCarriesOwner(t, mainLogs, upLogs, "a", ownContainerID)
				return
			}
			assert.True(t, stopped)
			assert.Equal(t, []string{"stop " + ownContainerID, "kill " + ownContainerID}, fd.mutationsOf(t, ownContainerID))
			require.Len(t, upLogs.FilterMessage("Owned container force killed").All(), 1)
			assertEveryRecordNamingCarriesOwner(t, mainLogs, upLogs, "a", ownContainerID)
		})
	}
}

// Codex round 6, docker finding 4: on its wait timeout the docker-logs
// monitor read the cidfile itself and recorded that id — plus an executable
// `docker logs` command — with no ownership read, so a direct `docker run
// --name custom` server or a tampered cidfile named a foreign container as
// this server's. The monitor takes the id only from the tracked state
// trackCidfileContainer verified: none → a record naming no id and no
// command; a verified one → id and container_owner from that verification.
func TestMonitorDockerLogs_NamesOnlyAVerifiedContainer(t *testing.T) {
	shorten := func(t *testing.T) {
		t.Helper()
		prev := dockerLogsWaitTimeout
		// Longer than the monitor's 100 ms poll tick, so a tracked id is
		// seen before the timeout fires (production: 10 s).
		dockerLogsWaitTimeout = 300 * time.Millisecond
		t.Cleanup(func() { dockerLogsWaitTimeout = prev })
	}
	const startedMsg = "Docker container started - logs available via 'docker logs' command"
	const foreignFullID = foreignContainerID + "0000000000000000000000000000000000000000000000000000"

	t.Run("unverified cidfile on timeout", func(t *testing.T) {
		shorten(t)
		installFakeDocker(t, ownAndForeignFixture())
		c, mainLogs, upLogs := newOwnershipClient("a", nil)
		cidFile := filepath.Join(t.TempDir(), "cid")
		require.NoError(t, os.WriteFile(cidFile, []byte(foreignFullID+"\n"), 0o600))

		done := make(chan struct{})
		go func() {
			defer close(done)
			c.monitorDockerLogsWithContext(context.Background(), cidFile)
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("monitor did not return after the wait timeout")
		}

		assertNoRecordNames(t, mainLogs, upLogs, foreignFullID, foreignContainerID, "docker logs")
		assert.Empty(t, mainLogs.FilterMessage(startedMsg).All(), "an unverified container was announced as started")
		assert.NotEmpty(t, recordsMentioning(mainLogs, "verified"), "the timeout records that no container was verified")
	})

	t.Run("verified tracked container", func(t *testing.T) {
		shorten(t)
		installFakeDocker(t, ownAndForeignFixture())
		c, mainLogs, _ := newOwnershipClient("a", nil)
		c.trackCidfileContainer(context.Background(), ownContainerID, 0)
		require.Equal(t, ownContainerID, c.containerID, "premise: the tracked id is the verified one")

		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() {
			defer close(done)
			c.monitorDockerLogsWithContext(ctx, filepath.Join(t.TempDir(), "never-read"))
		}()
		require.Eventually(t, func() bool { return len(mainLogs.FilterMessage(startedMsg).All()) == 1 }, 5*time.Second, 10*time.Millisecond)
		cancel()
		<-done

		started := mainLogs.FilterMessage(startedMsg).All()[0].ContextMap()
		assert.Equal(t, shortContainerID(ownContainerID), started["container_id"])
		assert.Equal(t, "a", started["container_owner"], "the id is named with the owner from its verification")
		assert.Equal(t, "docker logs -f "+shortContainerID(ownContainerID), started["command"])
		for _, entry := range mainLogs.All() {
			fields := entry.ContextMap()
			if fieldsName(fields, ownContainerID, shortContainerID(ownContainerID)) {
				assert.Equal(t, "a", fields["container_owner"], "record %q names the container without its owner: %v", entry.Message, fields)
			}
		}
	})
}
