package upstream

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// fakeRecoveryTarget stands in for a managed client.
type fakeRecoveryTarget struct {
	mu       sync.Mutex
	addr     string
	eligible bool
	resets   []bool
	reasons  []string
}

func (f *fakeRecoveryTarget) NetworkRecoveryProbeAddress() (string, bool) {
	return f.addr, f.addr != ""
}
func (f *fakeRecoveryTarget) NetworkRecoveryEligible(time.Time) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.eligible
}
func (f *fakeRecoveryTarget) RecoverFromNetworkOutage(reset bool, reason string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.eligible {
		return false
	}
	f.resets = append(f.resets, reset)
	f.reasons = append(f.reasons, reason)
	return true
}
func (f *fakeRecoveryTarget) triggers() ([]bool, []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]bool(nil), f.resets...), append([]string(nil), f.reasons...)
}

// fakeNetwork answers probes per address and counts them.
type fakeNetwork struct {
	mu     sync.Mutex
	up     map[string]bool
	probes map[string]int
	fp     string
	fpOK   bool
}

func newFakeNetwork() *fakeNetwork {
	return &fakeNetwork{up: map[string]bool{}, probes: map[string]int{}, fp: "en0=192.168.1.20;v6=true", fpOK: true}
}
func (n *fakeNetwork) set(addr string, up bool) { n.mu.Lock(); n.up[addr] = up; n.mu.Unlock() }
func (n *fakeNetwork) probe(_ context.Context, addr string, _ time.Duration) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.probes[addr]++
	if n.up[addr] {
		return nil
	}
	return errors.New("dial tcp: connect: connection refused")
}
func (n *fakeNetwork) fingerprint() (string, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.fp, n.fpOK
}
func (n *fakeNetwork) setFingerprint(fp string) { n.mu.Lock(); n.fp = fp; n.mu.Unlock() }
func (n *fakeNetwork) probeCount(addr string) int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.probes[addr]
}

func testRecoveryConfig() networkRecoveryConfig {
	return networkRecoveryConfig{
		Interval:          10 * time.Second,
		SlowInterval:      30 * time.Second,
		SlowAfter:         30,
		ProbeTimeout:      time.Second,
		WakeSlack:         30 * time.Second,
		OutageProbes:      2,
		MinTriggerSpacing: 30 * time.Second,
	}
}

type recoveryHarness struct {
	r   *networkRecovery
	net *fakeNetwork
	now time.Time
}

func newRecoveryHarness(cfg networkRecoveryConfig) *recoveryHarness {
	n := newFakeNetwork()
	return &recoveryHarness{
		r:   newNetworkRecovery(cfg, zap.NewNop(), n.probe, n.fingerprint),
		net: n,
		now: time.Date(2026, 11, 18, 4, 0, 0, 0, time.UTC),
	}
}

// step advances the fake clock by d and runs one tick over targets.
func (h *recoveryHarness) step(d time.Duration, targets ...recoveryTarget) {
	h.now = h.now.Add(d)
	h.r.tick(context.Background(), h.now, targets)
}

// GH #1929: probes fail while the network is gone, and the first successful
// probe after a demonstrated outage reconnects the server with its backoff
// reset — once.
func TestNetworkRecovery_OutageThenRestoreReconnectsOnceWithReset(t *testing.T) {
	h := newRecoveryHarness(testRecoveryConfig())
	target := &fakeRecoveryTarget{addr: "mcp.example.com:443", eligible: true}

	for i := 0; i < 6; i++ { // a minute of outage
		h.step(10*time.Second, target)
	}
	resets, _ := target.triggers()
	assert.Empty(t, resets, "nothing is triggered while the address is unreachable")
	assert.Equal(t, 6, h.net.probeCount(target.addr), "one probe per tick while the server is down")

	h.net.set(target.addr, true)
	h.step(10*time.Second, target)
	resets, reasons := target.triggers()
	require.Equal(t, []bool{true}, resets, "the restore resets the backoff and reconnects")
	assert.Equal(t, []string{reasonNetworkRestored}, reasons)

	for i := 0; i < 5; i++ { // still eligible (say the attempt failed for another reason), network steady
		h.step(10*time.Second, target)
	}
	resets, _ = target.triggers()
	assert.Len(t, resets, 1, "a steady network triggers nothing more")
}

// A single failed probe is a blip, not an outage: the normal ladder handles it.
func TestNetworkRecovery_SingleFailedProbeIsNotAnOutage(t *testing.T) {
	h := newRecoveryHarness(testRecoveryConfig())
	target := &fakeRecoveryTarget{addr: "mcp.example.com:443", eligible: true}

	h.step(10*time.Second, target) // fails once
	h.net.set(target.addr, true)
	h.step(10*time.Second, target)
	resets, _ := target.triggers()
	assert.Empty(t, resets)
}

// A server whose address answers all along (the upstream itself is broken) is
// never reconnected by the watcher: no outage was observed, so its backoff is
// left alone. This is what keeps the watcher from defeating #1013.
func TestNetworkRecovery_ReachableButFailingServerIsLeftToTheBackoff(t *testing.T) {
	h := newRecoveryHarness(testRecoveryConfig())
	target := &fakeRecoveryTarget{addr: "mcp.example.com:443", eligible: true}
	h.net.set(target.addr, true)

	for i := 0; i < 360; i++ { // an hour
		h.step(10*time.Second, target)
	}
	resets, _ := target.triggers()
	assert.Empty(t, resets)
}

func TestNetworkRecovery_IneligibleServersAreNeitherProbedNorReconnected(t *testing.T) {
	h := newRecoveryHarness(testRecoveryConfig())
	healthy := &fakeRecoveryTarget{addr: "a.example.com:443", eligible: false}
	stdio := &fakeRecoveryTarget{addr: "", eligible: true}

	for i := 0; i < 4; i++ {
		h.step(10*time.Second, healthy, stdio)
	}
	h.net.set(healthy.addr, true)
	h.step(10*time.Second, healthy, stdio)
	h.step(2*time.Minute, healthy, stdio) // a wake

	assert.Zero(t, h.net.probeCount(healthy.addr))
	r1, _ := healthy.triggers()
	r2, _ := stdio.triggers()
	assert.Empty(t, r1)
	assert.Empty(t, r2)
}

// A tick that arrives long after it was due means the host slept. A server
// failed on the network gets ONE attempt on its current ladder once its
// address answers — the issue's "one attempt per wake, then the normal backoff
// resumes".
func TestNetworkRecovery_WakeGivesOneAttemptWithoutReset(t *testing.T) {
	h := newRecoveryHarness(testRecoveryConfig())
	target := &fakeRecoveryTarget{addr: "mcp.example.com:443", eligible: true}
	h.net.set(target.addr, true)

	h.step(10*time.Second, target)
	h.step(10*time.Second, target)
	h.step(7*time.Hour, target) // slept overnight; the network is already back on wake

	resets, reasons := target.triggers()
	require.Equal(t, []bool{false}, resets)
	assert.Equal(t, []string{reasonSystemWake}, reasons)

	h.step(10*time.Second, target)
	resets, _ = target.triggers()
	assert.Len(t, resets, 1, "one attempt per wake")
}

// A wake with the network still down keeps the attempt pending; the outage the
// probes then observe ends in a reset when the network returns.
func TestNetworkRecovery_WakeWithoutNetworkWaitsForTheNetwork(t *testing.T) {
	h := newRecoveryHarness(testRecoveryConfig())
	target := &fakeRecoveryTarget{addr: "mcp.example.com:443", eligible: true}

	h.step(10*time.Second, target)
	h.step(7*time.Hour, target) // wake, no network yet
	h.step(10*time.Second, target)
	resets, _ := target.triggers()
	assert.Empty(t, resets)

	h.net.set(target.addr, true)
	h.step(10*time.Second, target)
	resets, _ = target.triggers()
	assert.Equal(t, []bool{true}, resets, "the outage the probes saw ends in a reset")
}

func TestNetworkRecovery_NetworkChangeGivesOneAttempt(t *testing.T) {
	h := newRecoveryHarness(testRecoveryConfig())
	target := &fakeRecoveryTarget{addr: "mcp.example.com:443", eligible: true}
	h.net.set(target.addr, true)

	h.step(10*time.Second, target)
	h.net.setFingerprint("en0=10.0.0.5;v6=false") // joined another network
	h.step(10*time.Second, target)
	resets, reasons := target.triggers()
	require.Equal(t, []bool{false}, resets)
	assert.Equal(t, []string{reasonNetworkChange}, reasons)

	// An unreadable interface list is not a change.
	h.net.mu.Lock()
	h.net.fpOK = false
	h.net.mu.Unlock()
	h.step(10*time.Second, target)
	h.net.mu.Lock()
	h.net.fpOK = true
	h.net.mu.Unlock()
	h.step(10*time.Second, target)
	resets, _ = target.triggers()
	assert.Len(t, resets, 1)
}

// Two restores close together: the second is deferred until the per-server
// spacing has passed, never dropped, so the server still recovers.
func TestNetworkRecovery_RestoresWithinTheSpacingAreDeferredNotDropped(t *testing.T) {
	cfg := testRecoveryConfig()
	cfg.MinTriggerSpacing = time.Minute
	h := newRecoveryHarness(cfg)
	target := &fakeRecoveryTarget{addr: "mcp.example.com:443", eligible: true}

	h.step(10*time.Second, target)
	h.step(10*time.Second, target)
	h.net.set(target.addr, true)
	h.step(10*time.Second, target) // t=30: restore #1 → trigger
	h.net.set(target.addr, false)
	h.step(10*time.Second, target)
	h.step(10*time.Second, target)
	h.net.set(target.addr, true)
	h.step(10*time.Second, target) // t=60: restore #2, 30s after #1 → deferred
	resets, _ := target.triggers()
	require.Len(t, resets, 1)

	h.step(10*time.Second, target) // t=70
	h.step(10*time.Second, target) // t=80
	resets, _ = target.triggers()
	require.Len(t, resets, 1)
	h.step(10*time.Second, target) // t=90: spacing passed
	resets, _ = target.triggers()
	assert.Equal(t, []bool{true, true}, resets, "the deferred restore fires once the spacing passed")
}

// Repeated network changes: every restore recovers the server, nothing fires
// while the network is down, and the trigger count equals the restore count.
func TestNetworkRecovery_RepeatedOutagesEachRecoverOnce(t *testing.T) {
	h := newRecoveryHarness(testRecoveryConfig())
	a := &fakeRecoveryTarget{addr: "mcp.atlassian.com:443", eligible: true}
	b := &fakeRecoveryTarget{addr: "api.githubcopilot.com:443", eligible: true}

	const cycles = 5
	for c := 0; c < cycles; c++ {
		h.net.set(a.addr, false)
		h.net.set(b.addr, false)
		for i := 0; i < 4; i++ {
			h.step(10*time.Second, a, b)
		}
		ra, _ := a.triggers()
		require.Len(t, ra, c, "cycle %d: nothing fires during the outage", c)
		h.net.set(a.addr, true)
		h.net.set(b.addr, true)
		h.step(10*time.Second, a, b)
		ra, _ = a.triggers()
		rb, _ := b.triggers()
		require.Len(t, ra, c+1, "cycle %d: the restore reconnects a", c)
		require.Len(t, rb, c+1, "cycle %d: the restore reconnects b", c)
	}
}

// A long outage slows the probe cadence (a dead remote is not probed every 10s
// for days), and the slow cadence still bounds recovery well under a minute.
func TestNetworkRecovery_LongOutageSlowsTheProbeCadence(t *testing.T) {
	cfg := testRecoveryConfig()
	h := newRecoveryHarness(cfg)
	target := &fakeRecoveryTarget{addr: "mcp.example.com:443", eligible: true}

	for i := 0; i < 360; i++ { // an hour of 10s ticks
		h.step(10*time.Second, target)
	}
	probes := h.net.probeCount(target.addr)
	// 30 probes at 10s (5 min), then one per 30s for the remaining 55 min.
	assert.InDelta(t, cfg.SlowAfter+55*2, probes, 3, "probes in an hour of outage: %d", probes)

	h.net.set(target.addr, true)
	restoredAt := h.now
	for i := 0; i < 6; i++ {
		h.step(10*time.Second, target)
		if r, _ := target.triggers(); len(r) > 0 {
			break
		}
	}
	resets, _ := target.triggers()
	require.Equal(t, []bool{true}, resets)
	assert.LessOrEqual(t, h.now.Sub(restoredAt), cfg.SlowInterval, "recovered within one slow probe interval")
}

// Servers on the same address share one probe per tick.
func TestNetworkRecovery_ProbesAreSharedPerAddress(t *testing.T) {
	h := newRecoveryHarness(testRecoveryConfig())
	a := &fakeRecoveryTarget{addr: "proxy.corp:3128", eligible: true}
	b := &fakeRecoveryTarget{addr: "proxy.corp:3128", eligible: true}
	h.step(10*time.Second, a, b)
	assert.Equal(t, 1, h.net.probeCount("proxy.corp:3128"))
}

func TestNetworkRecovery_DisabledByEnvironment(t *testing.T) {
	for _, v := range []string{"off", "false", "0", "no", "OFF"} {
		t.Setenv(networkRecoveryEnv, v)
		assert.False(t, networkRecoveryEnabled(), v)
	}
	for _, v := range []string{"", "on", "1", "true"} {
		t.Setenv(networkRecoveryEnv, v)
		assert.True(t, networkRecoveryEnabled(), v)
	}
}

func TestNetworkFingerprint_IsStableAcrossCalls(t *testing.T) {
	a, okA := networkFingerprint()
	b, okB := networkFingerprint()
	assert.Equal(t, okA, okB)
	assert.Equal(t, a, b)
}
