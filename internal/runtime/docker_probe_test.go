package runtime

import (
	"testing"
	"time"
)

// TestIsDockerAvailable_CachesResult verifies that a second call within the
// TTL does not re-probe — we assert this indirectly by checking that the
// cached timestamp is not updated.
func TestIsDockerAvailable_CachesResult(t *testing.T) {
	rt := newTestRuntime(t)

	// Prime the cache with a known (result, timestamp) pair so we don't
	// depend on whether Docker is actually installed on the test host.
	rt.dockerProbeMu.Lock()
	rt.dockerProbeResult = true
	rt.dockerProbedAt = time.Now()
	rt.dockerProbeKnown = true
	firstProbe := rt.dockerProbedAt
	rt.dockerProbeMu.Unlock()

	// Call within the positive TTL — must reuse the cached value and NOT
	// update the probe timestamp.
	got := rt.IsDockerAvailable()
	if !got {
		t.Fatalf("expected cached true, got false")
	}

	rt.dockerProbeMu.Lock()
	if !rt.dockerProbedAt.Equal(firstProbe) {
		t.Fatalf("expected dockerProbedAt to be unchanged (cache hit), was updated from %v to %v",
			firstProbe, rt.dockerProbedAt)
	}
	rt.dockerProbeMu.Unlock()
}

// TestIsDockerAvailable_NegativeTTLShorter verifies that a cached `false`
// result expires after 5m (negative TTL) — so users who launch Docker
// after mcpproxy starts see the flip within minutes, not only after restart.
func TestIsDockerAvailable_NegativeTTLShorter(t *testing.T) {
	rt := newTestRuntime(t)

	// Prime with a negative result 6 minutes old — past negative TTL (5m)
	// but well within positive TTL (15m).
	rt.dockerProbeMu.Lock()
	rt.dockerProbeResult = false
	rt.dockerProbedAt = time.Now().Add(-6 * time.Minute)
	rt.dockerProbeKnown = true
	staleProbe := rt.dockerProbedAt
	rt.dockerProbeMu.Unlock()

	// This call should re-probe (regardless of what the real result is);
	// we assert via the timestamp being refreshed.
	_ = rt.IsDockerAvailable()

	rt.dockerProbeMu.Lock()
	defer rt.dockerProbeMu.Unlock()
	if !rt.dockerProbedAt.After(staleProbe) {
		t.Fatalf("expected probe to re-run past the 5m negative TTL, timestamp still %v", staleProbe)
	}
}

// TestIsDockerAvailable_PositiveTTLHonored verifies that a cached `true`
// result within 15m is reused (no re-probe) to avoid spending 2s on
// `docker info` on every telemetry heartbeat.
func TestIsDockerAvailable_PositiveTTLHonored(t *testing.T) {
	rt := newTestRuntime(t)

	// 10 minutes old — well within positive TTL (15m).
	rt.dockerProbeMu.Lock()
	rt.dockerProbeResult = true
	rt.dockerProbedAt = time.Now().Add(-10 * time.Minute)
	rt.dockerProbeKnown = true
	cachedProbe := rt.dockerProbedAt
	rt.dockerProbeMu.Unlock()

	got := rt.IsDockerAvailable()
	if !got {
		t.Fatalf("expected cached true, got false")
	}

	rt.dockerProbeMu.Lock()
	defer rt.dockerProbeMu.Unlock()
	if !rt.dockerProbedAt.Equal(cachedProbe) {
		t.Fatalf("expected cache hit within positive TTL; timestamp changed from %v to %v",
			cachedProbe, rt.dockerProbedAt)
	}
}
