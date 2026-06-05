package runtime

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestPlanToolDiscoveryCycle covers the pure decision the background indexing
// loop makes each iteration: a positive interval sweeps on that cadence; a
// non-positive interval (disabled) skips the periodic sweep and waits the
// fallback re-check window so a hot-reload can re-enable it (spec 074, FR-006).
func TestPlanToolDiscoveryCycle(t *testing.T) {
	const recheck = 5 * time.Minute

	sweep, wait := planToolDiscoveryCycle(10*time.Minute, recheck)
	assert.True(t, sweep, "positive interval must sweep")
	assert.Equal(t, 10*time.Minute, wait)

	sweep, wait = planToolDiscoveryCycle(0, recheck)
	assert.False(t, sweep, "0s must disable the periodic sweep")
	assert.Equal(t, recheck, wait, "disabled cycle waits the re-check window")

	sweep, wait = planToolDiscoveryCycle(-1, recheck)
	assert.False(t, sweep, "negative interval must disable the periodic sweep")
	assert.Equal(t, recheck, wait)
}
