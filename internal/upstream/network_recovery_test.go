package upstream

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/managed"
)

// fakeRecoveryTarget stands in for a managed client. A reconnect connects it
// unless failReconnect is set (an upstream that stays broken).
type fakeRecoveryTarget struct {
	mu            sync.Mutex
	addr          string
	eligible      bool
	connected     bool
	failReconnect bool
	// connectsThenFails: the reconnect connects, and the connection is lost
	// again before the watcher's next tick.
	connectsThenFails bool
	successes         uint64
	resets            []bool
	reasons           []string
}

func (f *fakeRecoveryTarget) NetworkRecoveryProbe() (managed.NetworkProbe, bool) {
	return managed.NetworkProbe{Addr: f.addr}, f.addr != ""
}
func (f *fakeRecoveryTarget) NetworkRecoveryEligible(time.Time) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.eligible
}
func (f *fakeRecoveryTarget) IsConnected() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.connected
}
func (f *fakeRecoveryTarget) BeginNetworkRecovery(reset bool, reason string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.eligible {
		return false
	}
	f.resets = append(f.resets, reset)
	f.reasons = append(f.reasons, reason)
	return true
}
func (f *fakeRecoveryTarget) ConnectSuccessCount() uint64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.successes
}
func (f *fakeRecoveryTarget) ReconnectAfterNetworkRecovery(context.Context) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case f.connectsThenFails:
		f.successes++ // connected, then lost again before the next tick
	case !f.failReconnect:
		f.successes++
		f.eligible, f.connected = false, true
	}
}

// fail marks the target as failed on the network again (the next outage).
func (f *fakeRecoveryTarget) fail() {
	f.mu.Lock()
	f.eligible, f.connected = true, false
	f.mu.Unlock()
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
func (n *fakeNetwork) probe(_ context.Context, p managed.NetworkProbe, _ time.Duration) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	addr := p.Addr
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
		FreeTriggers:      3,
		MaxTriggerSpacing: 30 * time.Minute,
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

// step advances the fake clock by d, runs one tick over targets and waits
// for the reconnects it started.
func (h *recoveryHarness) step(d time.Duration, targets ...recoveryTarget) {
	h.now = h.now.Add(d)
	h.r.tick(context.Background(), h.now, targets)
	h.r.reconnects.Wait()
}

// GH #1929: probes fail while the network is gone, and the first successful
// probe after a demonstrated outage reconnects the server with its backoff
// reset — once.
func TestNetworkRecovery_OutageThenRestoreReconnectsOnceWithReset(t *testing.T) {
	h := newRecoveryHarness(testRecoveryConfig())
	target := &fakeRecoveryTarget{addr: "mcp.example.com:443", eligible: true, failReconnect: true}

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
	target := &fakeRecoveryTarget{addr: "mcp.example.com:443", eligible: true, failReconnect: true}
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
	target := &fakeRecoveryTarget{addr: "mcp.example.com:443", eligible: true, failReconnect: true}
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
	target := &fakeRecoveryTarget{addr: "mcp.example.com:443", eligible: true, failReconnect: true}

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
// Each reconnect succeeds, so the budget never runs out.
func TestNetworkRecovery_RepeatedOutagesEachRecoverOnce(t *testing.T) {
	h := newRecoveryHarness(testRecoveryConfig())
	a := &fakeRecoveryTarget{addr: "mcp.atlassian.com:443", eligible: true}
	b := &fakeRecoveryTarget{addr: "api.githubcopilot.com:443", eligible: true}

	const cycles = 8
	for c := 0; c < cycles; c++ {
		a.fail()
		b.fail()
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
		assert.True(t, ra[c], "cycle %d: a connection in between restores the reset budget", c)
		h.step(10*time.Second, a, b) // connected for a while
	}
}

// Opus r1 MAJOR 2: a remote that is reachable only intermittently, and whose
// reconnects never succeed, must not get its backoff reset over and over. After
// FreeTriggers reconnects without a connection no reset is granted and the
// spacing doubles up to the give-up probe interval.
func TestNetworkRecovery_IntermittentRemoteExhaustsTheBudget(t *testing.T) {
	cfg := testRecoveryConfig()
	h := newRecoveryHarness(cfg)
	target := &fakeRecoveryTarget{addr: "flaky.example.com:443", eligible: true, failReconnect: true}

	var times []time.Time
	for i := 0; h.now.Before(time.Date(2026, 11, 18, 6, 0, 0, 0, time.UTC)); i++ { // two hours
		h.net.set(target.addr, i%3 == 2) // down, down, up, ...
		before, _ := target.triggers()
		h.step(10*time.Second, target)
		if after, _ := target.triggers(); len(after) > len(before) {
			times = append(times, h.now)
		}
	}
	resets, _ := target.triggers()
	require.NotEmpty(t, resets)
	nResets := 0
	for _, r := range resets {
		if r {
			nResets++
		}
	}
	assert.Equal(t, cfg.FreeTriggers, nResets, "only the free reconnects reset the backoff: %v", resets)
	assert.LessOrEqual(t, len(resets), 12, "two hours of flapping start a bounded number of reconnects: %d", len(resets))
	for i := cfg.FreeTriggers + 1; i < len(times); i++ {
		assert.GreaterOrEqual(t, times[i].Sub(times[i-1]), times[i-1].Sub(times[i-2])-10*time.Second,
			"the spacing does not shrink while no reconnect succeeds")
	}
	assert.LessOrEqual(t, cfg.triggerSpacing(100), cfg.MaxTriggerSpacing)
	assert.Equal(t, cfg.MaxTriggerSpacing, cfg.triggerSpacing(100))
}

// A flapping link changes the fingerprint on every tick; the attempts it
// grants are bounded by the same budget and never reset the backoff.
func TestNetworkRecovery_FlappingNetworkIsBudgeted(t *testing.T) {
	h := newRecoveryHarness(testRecoveryConfig())
	target := &fakeRecoveryTarget{addr: "mcp.example.com:443", eligible: true, failReconnect: true}
	h.net.set(target.addr, true)

	for i := 0; i < 720; i++ { // two hours of 10s ticks
		h.net.setFingerprint(fmt.Sprintf("en0=10.0.0.%d;v6=false", i%2))
		h.step(10*time.Second, target)
	}
	resets, reasons := target.triggers()
	assert.LessOrEqual(t, len(resets), 12, "bounded: %d", len(resets))
	for i, r := range resets {
		assert.False(t, r, "a network change never resets the backoff")
		assert.Equal(t, reasonNetworkChange, reasons[i])
	}
}

// Once a reconnect succeeds the budget is restored: the next real outage is
// recovered at once, with a reset.
func TestNetworkRecovery_AConnectionRestoresTheBudget(t *testing.T) {
	cfg := testRecoveryConfig()
	h := newRecoveryHarness(cfg)
	target := &fakeRecoveryTarget{addr: "mcp.example.com:443", eligible: true, failReconnect: true}

	for i := 0; i < 60; i++ { // spend the budget on a flaky remote
		h.net.set(target.addr, i%3 == 2)
		h.step(10*time.Second, target)
	}
	spent, _ := target.triggers()
	require.Greater(t, len(spent), cfg.FreeTriggers)

	target.mu.Lock()
	target.failReconnect, target.eligible, target.connected = false, false, true // it connected
	target.mu.Unlock()
	h.step(10*time.Second, target)

	target.fail()
	h.net.set(target.addr, false)
	h.step(10*time.Second, target)
	h.step(10*time.Second, target)
	h.net.set(target.addr, true)
	h.step(10*time.Second, target)
	resets, _ := target.triggers()
	require.Len(t, resets, len(spent)+1, "the restore after a connection fires at once")
	assert.True(t, resets[len(resets)-1], "and resets the backoff again")
}

// A long outage slows the probe cadence (a dead remote is not probed every 10s
// for days), and the slow cadence still bounds recovery well under a minute.
func TestNetworkRecovery_LongOutageSlowsTheProbeCadence(t *testing.T) {
	cfg := testRecoveryConfig()
	h := newRecoveryHarness(cfg)
	target := &fakeRecoveryTarget{addr: "mcp.example.com:443", eligible: true, failReconnect: true}

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

// Through an HTTP proxy the probe asks the proxy to CONNECT to the upstream and
// succeeds only on a 2xx: a proxy that is up while the network behind it is
// down reads as an outage, not as reachable.
func TestDialProbe_ConnectThroughProxy(t *testing.T) {
	upstreamUp := true
	var gotAuth, gotTarget string
	var mu sync.Mutex
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				req, err := http.ReadRequest(bufio.NewReader(c))
				if err != nil {
					return
				}
				mu.Lock()
				gotAuth, gotTarget = req.Header.Get("Proxy-Authorization"), req.Host
				up := upstreamUp
				mu.Unlock()
				if req.Method == http.MethodConnect && up {
					_, _ = c.Write([]byte("HTTP/1.1 200 Connection established\r\n\r\n"))
					return
				}
				_, _ = c.Write([]byte("HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\n\r\n"))
			}(c)
		}
	}()

	probe := managed.NetworkProbe{Addr: ln.Addr().String(), ConnectTo: "mcp.example.com:443", ProxyAuth: "Basic YWxpY2U6czNjcmV0"}
	require.NoError(t, dialProbe(context.Background(), probe, 2*time.Second))
	mu.Lock()
	assert.Equal(t, "mcp.example.com:443", gotTarget)
	assert.Equal(t, "Basic YWxpY2U6czNjcmV0", gotAuth)
	upstreamUp = false
	mu.Unlock()
	assert.Error(t, dialProbe(context.Background(), probe, 2*time.Second), "a 502 from the proxy is an unreachable upstream")

	plain := managed.NetworkProbe{Addr: ln.Addr().String()}
	assert.NoError(t, dialProbe(context.Background(), plain, 2*time.Second), "without ConnectTo a TCP dial is enough")
	_ = ln.Close()
	assert.Error(t, dialProbe(context.Background(), plain, 2*time.Second))
}

// Opus r2 MINOR A: a reconnect that connected and lost the connection again
// before the next tick (a dark wake cut short) still restores the budget, so a
// night of such wakes cannot hold back the morning's recovery.
func TestNetworkRecovery_AMissedConnectionStillRestoresTheBudget(t *testing.T) {
	cfg := testRecoveryConfig()
	h := newRecoveryHarness(cfg)
	target := &fakeRecoveryTarget{addr: "mcp.example.com:443", eligible: true, connectsThenFails: true}

	for i := 0; i < 90; i++ { // 15 minutes of outages and restores
		h.net.set(target.addr, i%3 == 2)
		h.step(10*time.Second, target)
	}
	resets, _ := target.triggers()
	require.Greater(t, len(resets), cfg.FreeTriggers)
	for i, r := range resets {
		assert.True(t, r, "trigger %d: every reconnect connected, so every restore may reset", i)
	}
}

// Opus r3 MINOR: a wall clock stepped backwards must not defer recovery by the
// size of the jump.
func TestNetworkRecovery_BackwardsClockDoesNotDeferRecovery(t *testing.T) {
	h := newRecoveryHarness(testRecoveryConfig())
	target := &fakeRecoveryTarget{addr: "mcp.example.com:443", eligible: true, failReconnect: true}

	h.step(10*time.Second, target)
	h.step(10*time.Second, target)
	h.net.set(target.addr, true)
	h.step(10*time.Second, target) // trigger #1
	h.net.set(target.addr, false)
	h.step(10*time.Second, target)
	h.step(-2*time.Hour, target) // the clock is set back two hours
	h.net.set(target.addr, true)
	h.step(10*time.Second, target)
	resets, _ := target.triggers()
	assert.Len(t, resets, 2, "the restore after the clock moved back is not deferred")
}
