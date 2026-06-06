package runtime

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestPlanToolDiscoveryCycle covers the pure decision the background indexing
// loop makes each iteration: when at least one server (or the global default)
// has a positive resolved interval, the loop sweeps and ticks at that cadence;
// when every interval is disabled it skips the periodic sweep and waits the
// fallback re-check window so a hot-reload can re-enable it (spec 074,
// FR-006/FR-012). The tick itself is resolved by the upstream manager from its
// thread-safe per-client snapshots (see TestResolveToolDiscoverySweepTick in the
// upstream package) — deliberately not from r.Config().Servers, which would race
// in-place config mutation in the background loop (MCP-1189).
func TestPlanToolDiscoveryCycle(t *testing.T) {
	const recheck = 5 * time.Minute

	sweep, wait := planToolDiscoveryCycle(10*time.Minute, true, recheck)
	assert.True(t, sweep, "an enabled cadence must sweep")
	assert.Equal(t, 10*time.Minute, wait, "loop ticks at the resolved cadence")

	sweep, wait = planToolDiscoveryCycle(0, false, recheck)
	assert.False(t, sweep, "all-disabled must skip the periodic sweep")
	assert.Equal(t, recheck, wait, "disabled cycle waits the re-check window")

	sweep, wait = planToolDiscoveryCycle(30*time.Second, false, recheck)
	assert.False(t, sweep, "anyEnabled=false dominates even with a positive tick")
	assert.Equal(t, recheck, wait)
}
