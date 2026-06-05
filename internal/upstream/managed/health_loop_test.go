package managed

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// TestPlanHealthCheckCycle covers the pure decision the loop makes each
// iteration: a positive interval probes on that cadence; a non-positive
// interval (disabled) skips the probe and waits the fallback re-check window so
// a later hot-reload can re-enable it (spec 074, FR-006/FR-012).
func TestPlanHealthCheckCycle(t *testing.T) {
	const recheck = 30 * time.Second

	probe, wait := planHealthCheckCycle(45*time.Second, recheck)
	assert.True(t, probe, "positive interval must probe")
	assert.Equal(t, 45*time.Second, wait)

	probe, wait = planHealthCheckCycle(0, recheck)
	assert.False(t, probe, "0s must disable probing")
	assert.Equal(t, recheck, wait, "disabled cycle waits the re-check window")

	probe, wait = planHealthCheckCycle(-1, recheck)
	assert.False(t, probe, "negative interval must disable probing")
	assert.Equal(t, recheck, wait)
}

// TestBackgroundHealthCheck_DisabledSkipsProbe asserts that a resolved interval
// of 0s means the loop never issues a liveness probe (SC-003).
func TestBackgroundHealthCheck_DisabledSkipsProbe(t *testing.T) {
	mc := newTestClientForHealth(t)
	mc.stopMonitoring = make(chan struct{})
	fake := &fakeProber{}
	mc.healthProbe = fake
	mc.globalConfig = &config.Config{}

	cfg := mc.GetConfig()
	zero := config.Duration(0)
	cfg.HealthCheckInterval = &zero // per-server disabled
	mc.SetConfig(cfg)

	done := make(chan struct{})
	go func() { mc.backgroundHealthCheck(); close(done) }()
	time.Sleep(150 * time.Millisecond)
	close(mc.stopMonitoring)
	<-done

	assert.Equal(t, 0, fake.pingCalls, "disabled health check must never probe")
}

// TestBackgroundHealthCheck_EnabledProbesOnInterval asserts the resettable
// timer re-resolves the per-server interval and probes on that cadence.
func TestBackgroundHealthCheck_EnabledProbesOnInterval(t *testing.T) {
	mc := newTestClientForHealth(t)
	mc.stopMonitoring = make(chan struct{})
	fake := &fakeProber{}
	mc.healthProbe = fake
	mc.globalConfig = &config.Config{}

	cfg := mc.GetConfig()
	d := config.Duration(40 * time.Millisecond) // sub-validation value is fine for the loop mechanics
	cfg.HealthCheckInterval = &d
	mc.SetConfig(cfg)

	done := make(chan struct{})
	go func() { mc.backgroundHealthCheck(); close(done) }()
	time.Sleep(300 * time.Millisecond)
	close(mc.stopMonitoring)
	<-done

	assert.GreaterOrEqual(t, fake.pingCalls, 1, "enabled health check must probe on its interval")
}
