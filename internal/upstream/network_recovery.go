package upstream

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/managed"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/types"
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
//
// Reconnects are budgeted per server. The first FreeTriggers reconnects after
// the server was last seen connected are immediate (MinTriggerSpacing apart)
// and may reset the backoff; past that — a remote that is only intermittently
// reachable, a network that keeps flapping — no reset is granted and the
// spacing doubles up to MaxTriggerSpacing (the 30-minute give-up probe), so the
// watcher can never restart the short retry ladder over and over (#1013). A
// successful connection restores the budget.

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
	// FreeTriggers is how many reconnects may follow one another without the
	// server being seen connected before the budget kicks in.
	FreeTriggers int
	// MaxTriggerSpacing caps the doubling spacing once the budget is spent.
	MaxTriggerSpacing time.Duration
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
		FreeTriggers:      3,
		MaxTriggerSpacing: types.GaveUpProbeInterval,
	}
}

// recoveryTarget is the part of a managed client the watcher uses.
type recoveryTarget interface {
	NetworkRecoveryProbe() (managed.NetworkProbe, bool)
	NetworkRecoveryEligible(now time.Time) bool
	IsConnected() bool
	ConnectSuccessCount() uint64
	BeginNetworkRecovery(resetBackoff bool, reason string) bool
	ReconnectAfterNetworkRecovery(ctx context.Context)
}

type recoveryProbeState struct {
	failures       int       // consecutive failed probes
	lastProbe      time.Time // zero: never probed
	restorePending bool      // an outage ended; reconnect with a reset
	eventPending   string    // a wake or network change happened; one attempt
	lastTrigger    time.Time // wall clock
	unproductive   int       // reconnects started since the server last connected
	successes      uint64    // ConnectSuccessCount when last looked at
}

// triggerSpacing is the least time before the next reconnect for a server
// that already had `unproductive` reconnects without connecting.
func (cfg networkRecoveryConfig) triggerSpacing(unproductive int) time.Duration {
	if unproductive < cfg.FreeTriggers {
		return cfg.MinTriggerSpacing
	}
	shift := unproductive - cfg.FreeTriggers + 1
	if shift > 16 {
		shift = 16
	}
	d := cfg.MinTriggerSpacing << uint(shift)
	if cfg.MaxTriggerSpacing > 0 && (d > cfg.MaxTriggerSpacing || d <= 0) {
		d = cfg.MaxTriggerSpacing
	}
	return d
}

type networkRecovery struct {
	mu     sync.Mutex
	cfg    networkRecoveryConfig
	logger *zap.Logger

	probe       func(ctx context.Context, p managed.NetworkProbe, timeout time.Duration) error
	fingerprint func() (string, bool)

	lastTick     time.Time // wall clock of the previous tick
	lastPrint    string
	lastPrintOK  bool
	states       map[recoveryTarget]*recoveryProbeState
	kick         chan struct{}
	triggerCount int            // for tests and logs
	reconnects   sync.WaitGroup // reconnects the watcher started; run waits for them
}

func newNetworkRecovery(cfg networkRecoveryConfig, logger *zap.Logger,
	probe func(ctx context.Context, p managed.NetworkProbe, timeout time.Duration) error,
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

// dialProbe is the production probe: a TCP dial, closed at once, and through
// an HTTP proxy a CONNECT to the upstream that must be answered 2xx. Nothing
// else is sent: no TLS, no request to the upstream, no credentials of its own.
func dialProbe(ctx context.Context, p managed.NetworkProbe, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", p.Addr)
	if err != nil {
		return err
	}
	defer conn.Close()
	if p.ConnectTo == "" {
		return nil
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	req := &http.Request{
		Method: http.MethodConnect,
		URL:    &url.URL{Opaque: p.ConnectTo},
		Host:   p.ConnectTo,
		Header: make(http.Header),
	}
	if p.ProxyAuth != "" {
		req.Header.Set("Proxy-Authorization", p.ProxyAuth)
	}
	if err := req.Write(conn); err != nil {
		return err
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), req)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("proxy answered CONNECT %s with %s", p.ConnectTo, resp.Status)
	}
	return nil
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
			r.reconnects.Wait() // bounded by ctx: the reconnects abort on it
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
		probe  managed.NetworkProbe
		state  *recoveryProbeState
	}
	var due []pending
	for _, t := range targets {
		probe, ok := t.NetworkRecoveryProbe()
		if !ok {
			continue
		}
		present[t] = true
		st := r.states[t]
		if st == nil {
			st = &recoveryProbeState{}
			r.states[t] = st
		}
		// A connection since the last tick — even one that failed again before
		// this tick — means the reconnects paid off: the budget is restored.
		if n := t.ConnectSuccessCount(); t.IsConnected() || n != st.successes {
			st.unproductive = 0
			st.successes = n
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
			due = append(due, pending{target: t, probe: probe, state: st})
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
	var probes []managed.NetworkProbe
	seen := map[string]bool{}
	for _, p := range due {
		if !seen[p.probe.Key()] {
			seen[p.probe.Key()] = true
			probes = append(probes, p.probe)
		}
	}
	results := make(map[string]error, len(probes))
	var resMu sync.Mutex
	var wg sync.WaitGroup
	for _, probe := range probes {
		wg.Add(1)
		go func(probe managed.NetworkProbe) {
			defer wg.Done()
			err := r.probe(ctx, probe, cfg.ProbeTimeout)
			resMu.Lock()
			results[probe.Key()] = err
			resMu.Unlock()
		}(probe)
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
		if err := results[p.probe.Key()]; err != nil {
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
		// Spacing runs on the wall clock: hours asleep count, so a budget spent
		// on dark wakes overnight does not defer the morning's recovery.
		if !st.lastTrigger.IsZero() && wall.Sub(st.lastTrigger) < cfg.triggerSpacing(st.unproductive) {
			continue // deferred until the spacing has passed
		}
		reason := st.eventPending
		if st.restorePending {
			reason = reasonNetworkRestored
		}
		// A reset only while the budget lasts: past it the attempt is made on
		// the server's current ladder, which keeps climbing toward give-up.
		reset := st.restorePending && st.unproductive < cfg.FreeTriggers
		st.restorePending, st.eventPending = false, ""
		if p.target.BeginNetworkRecovery(reset, reason) {
			st.lastTrigger = wall
			st.unproductive++
			r.triggerCount++
			r.reconnects.Add(1)
			go func(t recoveryTarget) {
				defer r.reconnects.Done()
				t.ReconnectAfterNetworkRecovery(ctx)
			}(p.target)
		}
	}
}
