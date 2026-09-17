package core

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Codex review round 11 (PR E), finding 1: D8 rule 3 treats a container
// COUNT as container-subject evidence, so every record carrying
// container_count must also carry container_owner (the label read back from
// the listed rows) — or, when the count is zero, no container fields at
// all. Three housekeeping sites logged container_count alone: the
// image-name fallback, the name-pattern fallback and the pre-creation
// sweep's main-log record (its upstreamLogger record already paired the two
// per the pre-creation fix at docker.go:423-425).
//
// Each sub-test below drives the site directly against a's own owned
// container and asserts every c.logger (main.log) record carrying
// container_count in this run also carries container_owner="a" — the value
// the fixture's label constrains it to.

func TestDockerCleanup_ImageFallback_CountCarriesOwner(t *testing.T) {
	installFakeDocker(t, []fakeContainer{
		{ID: ownContainerID, Name: ownContainerName, Image: "mcp/example", Status: "Up 2 minutes",
			Labels: map[string]string{ownerLabel: "a"}},
	})
	c, mainLogs, _ := newOwnershipClient("a", nil)

	c.killDockerContainersByImageWithContext(context.Background(), "mcp/example")

	found := false
	for _, entry := range mainLogs.All() {
		fields := entry.ContextMap()
		count, hasCount := fields["container_count"]
		if !hasCount {
			continue
		}
		found = true
		owner, hasOwner := fields["container_owner"]
		assert.True(t, hasOwner, "record %q carries container_count=%v without container_owner", entry.Message, count)
		assert.Equal(t, "a", owner)
	}
	assert.True(t, found, "expected at least one record carrying container_count")
}

func TestDockerCleanup_NamePatternFallback_CountCarriesOwner(t *testing.T) {
	installFakeDocker(t, []fakeContainer{
		{ID: ownContainerID, Name: ownContainerName, Image: "mcp/example", Status: "Up 2 minutes",
			Labels: map[string]string{ownerLabel: "a"}},
	})
	c, mainLogs, _ := newOwnershipClient("a", nil)

	c.killDockerContainersByNamePatternWithContext(context.Background())

	found := false
	for _, entry := range mainLogs.All() {
		fields := entry.ContextMap()
		count, hasCount := fields["container_count"]
		if !hasCount {
			continue
		}
		found = true
		owner, hasOwner := fields["container_owner"]
		assert.True(t, hasOwner, "record %q carries container_count=%v without container_owner", entry.Message, count)
		assert.Equal(t, "a", owner)
	}
	assert.True(t, found, "expected at least one record carrying container_count")
}

func TestDockerCleanup_PreCreationSweep_MainLogCountCarriesOwner(t *testing.T) {
	installFakeDocker(t, []fakeContainer{
		{ID: ownContainerID, Name: ownContainerName, Image: "mcp/example", Status: "Up 2 minutes",
			Labels: map[string]string{ownerLabel: "a"}},
	})
	c, mainLogs, _ := newOwnershipClient("a", nil)

	require.NoError(t, c.ensureNoExistingContainers(context.Background()))

	found := false
	for _, entry := range mainLogs.All() {
		fields := entry.ContextMap()
		count, hasCount := fields["container_count"]
		if !hasCount {
			continue
		}
		found = true
		owner, hasOwner := fields["container_owner"]
		assert.True(t, hasOwner, "main-log record %q carries container_count=%v without container_owner", entry.Message, count)
		assert.Equal(t, "a", owner)
	}
	assert.True(t, found, "expected at least one main-log record carrying container_count")
}

// Codex review round 11, finding 2: the terminal cidfile-recovery failure
// (readContainerIDWithContext, no id from the cidfile AND no owned
// container found by the tracked name) must name only the server in
// main.log — c.containerName is a generated name never read back from
// Docker, and under a suffix collision it can currently belong to a
// different, colliding server. This mirrors the round-9 lifecycle fix,
// where an unverified container_name is omitted rather than logged.
func TestDockerCleanup_CidfileRecoveryFailure_NamesServerOnly(t *testing.T) {
	// No containers at all: the by-name lookup finds nothing, so recovery
	// fails and the terminal error path fires.
	installFakeDocker(t, nil)
	c, mainLogs, _ := newOwnershipClient("a", nil)
	c.containerName = ownContainerName

	shortenCidfilePoll(t)
	cidFile := filepath.Join(t.TempDir(), "never-written")
	c.readContainerIDWithContext(context.Background(), cidFile)

	errorRecords := mainLogs.FilterMessage("Failed to recover container ID - container will be orphaned on disconnect").All()
	require.NotEmpty(t, errorRecords, "expected the terminal recovery-failure record")
	for _, entry := range errorRecords {
		fields := entry.ContextMap()
		_, hasName := fields["container_name"]
		assert.False(t, hasName, "recovery-failure record names an unverified container_name: %v", fields)
	}
}
