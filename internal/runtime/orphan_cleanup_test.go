package runtime

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A server registered and indexed while the startup orphan cleanup runs must
// not be treated as an orphan. The event fires right after the cleanup's first
// read, whichever list that is.
func TestFindOrphanedIndexServers_ServerAddedDuringCleanupIsKept(t *testing.T) {
	var active, indexed []string
	added := false
	addServer := func() {
		if !added {
			added = true
			active = append(active, "b")
			indexed = append(indexed, "b")
		}
	}
	listActive := func() []string {
		defer addServer()
		return append([]string(nil), active...)
	}
	listIndexed := func() ([]string, error) {
		defer addServer()
		return append([]string(nil), indexed...), nil
	}

	orphans, _, _, err := findOrphanedIndexServers(listIndexed, listActive)
	require.NoError(t, err)
	assert.Empty(t, orphans)
}

func TestFindOrphanedIndexServers_RemovedServerIsOrphan(t *testing.T) {
	orphans, activeCount, indexedCount, err := findOrphanedIndexServers(
		func() ([]string, error) { return []string{"a", "gone"}, nil },
		func() []string { return []string{"a"} },
	)
	require.NoError(t, err)
	assert.Equal(t, []string{"gone"}, orphans)
	assert.Equal(t, 1, activeCount)
	assert.Equal(t, 2, indexedCount)
}
