package upstream

import (
	"context"
	"fmt"
	"net"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

// Connectivity watcher (GH #1929).
//
// A remote upstream that failed because the network was gone keeps failing
// while it is gone, and each failure advances its reconnect backoff — overnight
// on a sleeping laptop that ends in "gave up", retried every 30 minutes. When the
// machine wakes and the network is back, nothing used to shorten that wait.
//
// The watcher looks only at servers whose current failure is a network failure
// (managed.Client.NetworkRecoveryEligible) and probes their address with a bare
// TCP dial — no request, no TLS — while they are down. It reconnects a server:
//
//   - when its probes failed and then succeeded (a demonstrated outage ended),
//     with the backoff reset, so the server is back within one probe interval;
//   - after a system wake or a change of the machine's network addresses, once
//     its address answers: one attempt on the current ladder, after which the
//     normal backoff resumes (#1013).
//
// A server whose address answers all along (the upstream itself is broken) is
// never reconnected by the watcher, so the backoff still protects a dead remote.

const (
	reasonNetworkRestored = "network_restored"
	reasonSystemWake      = "system_wake"
	reasonNetworkChange   = "network_change"

	// networkRecoveryEnv turns the watcher off ("off", "false", "0", "no").
	networkRecoveryEnv = "MCPPROXY_NETWORK_RECOVERY"
)

// networkRecoveryConfig is the watcher's tuning. Constants in production
// (defaultNetworkRecoveryConfig); tests shorten them.
type networkRecoveryConfig struct {
	// Interval is the tick, and the probe cadence while a server is down.
	Interval time.Duration
	// SlowInterval is the probe cadence once SlowAfter probes in a row failed,
	// so a remote that is gone for days is not dialed every 10s. It is also the
	// worst-case delay between the network returning and the reconnect.
	SlowInterval time.Duration
	SlowAfter    int
	// ProbeTimeout bounds one probe dial (DNS included).
	ProbeTimeout time.Duration
	// WakeSlack: a tick that arrives this much later than scheduled means the
	// host was asleep.
	WakeSlack time.Duration
	// OutageProbes is how many failed probes in a row prove an outage.
	OutageProbes int
	// MinTriggerSpacing is the least time between two reconnects the watcher
	// starts for one server. A restore inside it is deferred, not dropped.
	MinTriggerSpacing time.Duration
}

func defaultNetworkRecoveryConfig() networkRecoveryConfig {
	return networkRecoveryConfig{
		Interval:          10 * time.Second,
		SlowInterval:      30 * time.Second,
		SlowAfter:         30, // five minutes of failed probes
		ProbeTimeout:      3 * time.Second,
		WakeSlack:         30 * time.Second,
		OutageProbes:      2,
		MinTriggerSpacing: 30 * time.Second,
	}
}

// recoveryTarget is the part of a managed client the watcher uses.
type recoveryTarget interface {
	NetworkRecoveryProbeAddress() (string, bool)
	NetworkRecoveryEligible(now time.Time) bool
	RecoverFromNetworkOutage(resetBackoff bool, reason string) bool
}

type recoveryProbeState struct {
	failures       int       // consecutive failed probes
	lastProbe      time.Time // zero: never probed
	restorePending bool      // an outage ended; reconnect with a reset
	eventPending   string    // a wake or network change happened; one attempt
	lastTrigger    time.Time
}

type networkRecovery struct {
	mu     sync.Mutex
	cfg    networkRecoveryConfig
	logger *zap.Logger

	probe       func(ctx context.Context, addr string, timeout time.Duration) error
	fingerprint func() (string, bool)

	lastTick     time.Time // wall clock of the previous tick
	lastPrint    string
	lastPrintOK  bool
	states       map[recoveryTarget]*recoveryProbeState
	kick         chan struct{}
	triggerCount int // for tests and logs
}

func newNetworkRecovery(cfg networkRecoveryConfig, logger *zap.Logger,
	probe func(ctx context.Context, addr string, timeout time.Duration) error,
	fingerprint func() (string, bool)) *networkRecovery {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &networkRecovery{
		cfg:         cfg,
		logger:      logger,
		probe:       probe,
		fingerprint: fingerprint,
		states:      map[recoveryTarget]*recoveryProbeState{},
		kick:        make(chan struct{}, 1),
	}
}

func networkRecoveryEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(networkRecoveryEnv))) {
	case "off", "false", "0", "no":
		return false
	}
	return true
}

// dialProbe is the production probe: a TCP dial, closed at once.
func dialProbe(ctx context.Context, addr string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	return conn.Close()
}

// networkFingerprint summarizes the machine's network attachment: the up,
// non-loopback interfaces with their IPv4 addresses and whether they hold a
// global IPv6 address. IPv6 values are left out on purpose — privacy addresses
// rotate on their own and would read as a network change every few hours.
func networkFingerprint() (string, bool) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return "", false
	}
	var parts []string
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		var v4 []string
		v6 := false
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			if ip4 := ipnet.IP.To4(); ip4 != nil {
				if !ip4.IsLinkLocalUnicast() {
					v4 = append(v4, ip4.String())
				}
				continue
			}
			if ipnet.IP.IsGlobalUnicast() {
				v6 = true
			}
		}
		if len(v4) == 0 && !v6 {
			continue
		}
		sort.Strings(v4)
		parts = append(parts, fmt.Sprintf("%s=%s;v6=%t", ifc.Name, strings.Join(v4, ","), v6))
	}
	sort.Strings(parts)
	return strings.Join(parts, "|"), true
}

func (r *networkRecovery) interval() time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cfg.Interval
}

// triggers reports how many reconnects the watcher has started.
func (r *networkRecovery) triggers() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.triggerCount
}

// setConfig replaces the tuning and wakes the loop (tests).
func (r *networkRecovery) setConfig(cfg networkRecoveryConfig) {
	r.mu.Lock()
	r.cfg = cfg
	r.mu.Unlock()
	select {
	case r.kick <- struct{}{}:
	default:
	}
}

// run ticks until ctx ends. targets is read fresh every tick.
func (r *networkRecovery) run(ctx context.Context, targets func() []recoveryTarget) {
	for {
		timer := time.NewTimer(r.interval())
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-r.kick:
			timer.Stop()
		case <-timer.C:
		}
		r.tick(ctx, time.Now(), targets())
	}
}

// tick runs one watcher pass at now over the given targets.
func (r *networkRecovery) tick(ctx context.Context, now time.Time, targets []recoveryTarget) {
	r.mu.Lock()
	cfg := r.cfg
	wall := now.Round(0) // the monotonic clock stops while a Mac sleeps; the wall clock does not

	event := ""
	if !r.lastTick.IsZero() && wall.Sub(r.lastTick) > cfg.Interval+cfg.WakeSlack {
		event = reasonSystemWake
		r.logger.Info("System wake detected; servers that failed on the network will be retried once reachable",
			zap.Duration("gap", wall.Sub(r.lastTick)))
	}
	r.lastTick = wall
	if fp, ok := r.fingerprint(); ok {
		if r.lastPrintOK && fp != r.lastPrint && event == "" {
			event = reasonNetworkChange
			r.logger.Info("Network change detected; servers that failed on the network will be retried once reachable")
		}
		r.lastPrint, r.lastPrintOK = fp, true
	}

	// Which targets are waiting on the network, and which of them to probe now.
	present := make(map[recoveryTarget]bool, len(targets))
	type pending struct {
		target recoveryTarget
		addr   string
		state  *recoveryProbeState
	}
	var due []pending
	for _, t := range targets {
		addr, ok := t.NetworkRecoveryProbeAddress()
		if !ok {
			continue
		}
		present[t] = true
		st := r.states[t]
		if st == nil {
			st = &recoveryProbeState{}
			r.states[t] = st
		}
		if !t.NetworkRecoveryEligible(now) {
			// Connected, connecting, or failed for another reason: forget the
			// probe history, keep lastTrigger so the spacing survives a
			// reconnect that fails again.
			st.failures, st.lastProbe, st.restorePending, st.eventPending = 0, time.Time{}, false, ""
			continue
		}
		if event != "" {
			st.eventPending = event
		}
		cadence := cfg.Interval
		if st.failures >= cfg.SlowAfter {
			cadence = cfg.SlowInterval
		}
		if st.lastProbe.IsZero() || now.Sub(st.lastProbe) >= cadence || st.eventPending != "" || st.restorePending {
			due = append(due, pending{target: t, addr: addr, state: st})
		}
	}
	for t := range r.states {
		if !present[t] {
			delete(r.states, t)
		}
	}
	r.mu.Unlock()

	if len(due) == 0 {
		return
	}

	// One probe per distinct address, concurrently. The address list is
	// built before any probe starts; results is read only after wg.Wait.
	var addrs []string
	seen := map[string]bool{}
	for _, p := range due {
		if !seen[p.addr] {
			seen[p.addr] = true
			addrs = append(addrs, p.addr)
		}
	}
	results := make(map[string]error, len(addrs))
	var resMu sync.Mutex
	var wg sync.WaitGroup
	for _, addr := range addrs {
		wg.Add(1)
		go func(addr string) {
			defer wg.Done()
			err := r.probe(ctx, addr, cfg.ProbeTimeout)
			resMu.Lock()
			results[addr] = err
			resMu.Unlock()
		}(addr)
	}
	wg.Wait()

	if ctx.Err() != nil {
		return // shutting down: start no reconnect
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, p := range due {
		st := p.state
		st.lastProbe = now
		if err := results[p.addr]; err != nil {
			st.failures++
			continue
		}
		if st.failures >= cfg.OutageProbes {
			st.restorePending = true
		}
		st.failures = 0
		if !st.restorePending && st.eventPending == "" {
			continue
		}
		if !st.lastTrigger.IsZero() && now.Sub(st.lastTrigger) < cfg.MinTriggerSpacing {
			continue // deferred until the spacing has passed
		}
		reset := st.restorePending
		reason := st.eventPending
		if reset {
			reason = reasonNetworkRestored
		}
		st.restorePending, st.eventPending = false, ""
		if p.target.RecoverFromNetworkOutage(reset, reason) {
			st.lastTrigger = now
			r.triggerCount++
		}
	}
}
