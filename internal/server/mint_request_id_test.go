package server

import (
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// Spec 090: the tray collapses activity rows by request id, so two calls that
// were blocked at the same instant must not mint the same one — otherwise two
// separate attempts appear in the glance as a single row.
func TestMintActivityRequestID_ConcurrentMintsAreUnique(t *testing.T) {
	const goroutines = 32
	const perGoroutine = 200

	var wg sync.WaitGroup
	ids := make([][]string, goroutines)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(slot int) {
			defer wg.Done()
			mine := make([]string, 0, perGoroutine)
			for j := 0; j < perGoroutine; j++ {
				mine = append(mine, mintActivityRequestID("github", "create_issue"))
			}
			ids[slot] = mine
		}(i)
	}
	wg.Wait()

	seen := make(map[string]struct{}, goroutines*perGoroutine)
	for _, batch := range ids {
		for _, id := range batch {
			_, dup := seen[id]
			require.False(t, dup, "minted a duplicate request id: %s", id)
			seen[id] = struct{}{}
		}
	}
	require.Len(t, seen, goroutines*perGoroutine)
}

// The prefix is what operators grep logs by, so uniqueness must not come at the
// cost of the shape the call sites already emit.
func TestMintActivityRequestID_KeepsServerAndToolInTheID(t *testing.T) {
	id := mintActivityRequestID("github", "create_issue")

	require.Contains(t, id, "-github-create_issue")
	require.Regexp(t, `^\d+-`, id, "still starts with the nanosecond stamp")
	require.Greater(t, len(strings.Split(id, "-")), 2)
}
