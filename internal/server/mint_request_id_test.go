package server

import (
	"strconv"
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

// Uniqueness cannot rest on the clock: whether two mints land in the same
// nanosecond is a property of the machine, not of the code, so the race test
// above can pass on a build that has no disambiguator at all. This pins the
// disambiguator itself — a numeric final component that advances on every mint,
// which is deterministic on any hardware.
func TestMintActivityRequestID_EndsInAnAdvancingCounter(t *testing.T) {
	var previous uint64
	for i := 0; i < 5; i++ {
		id := mintActivityRequestID("github", "create_issue")
		seq := counterSuffix(t, id)

		if i > 0 {
			require.Greater(t, seq, previous, "the counter must advance on every mint: %s", id)
		}
		previous = seq
	}
}

// The counter is process-wide, so ids minted for unrelated targets cannot
// repeat either — two different tools blocked in the same nanosecond are still
// two rows.
func TestMintActivityRequestID_CounterIsSharedAcrossTargets(t *testing.T) {
	first := counterSuffix(t, mintActivityRequestID("github", "create_issue"))
	second := counterSuffix(t, mintActivityRequestID("slack", "post_message"))

	require.Greater(t, second, first, "one counter, not one per target")
}

// counterSuffix returns the trailing numeric component of a minted id, failing
// the test when there is not one — which is precisely the defect these tests
// exist to catch.
func counterSuffix(t *testing.T, id string) uint64 {
	t.Helper()

	parts := strings.Split(id, "-")
	require.Greater(t, len(parts), 1, "id has no components to take a suffix from: %s", id)

	seq, err := strconv.ParseUint(parts[len(parts)-1], 10, 64)
	require.NoErrorf(t, err, "id must end in a numeric counter, got %q", id)
	return seq
}
