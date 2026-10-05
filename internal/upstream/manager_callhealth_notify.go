package upstream

import (
	"sync"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/health"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/callstats"
)

// Spec 113-d FR-068: when the rolling failure rate flips a server between
// "degraded by failure rate" and not, the health level the REST/SSE surfaces
// compute changes without any connection-state event. The existing
// servers.changed producers are all state-driven, so there is no poll-and-diff
// to ride on; this small push path nudges the runtime (which coalesces) at most
// once per callHealthNotifyInterval per server.
const (
	defaultCallHealthNotifyInterval = 10 * time.Second
	// Rechecked once per bucket: the granularity at which the window changes.
	defaultCallHealthRecheckInterval = callstats.BucketSize
)

// callHealthState is the per-server notifier state, guarded by Manager.callHealthMu.
type callHealthState struct {
	degraded    bool
	lastNotify  time.Time
	notifyTimer *time.Timer // pending deferred (debounced) notification
	recheck     *time.Timer // pending window-expiry re-evaluation
}

type callHealthNotifier struct {
	mu       sync.Mutex
	observer func(server string)
	servers  map[string]*callHealthState

	// Zero means the default; tests set them before any call is recorded.
	notifyInterval  time.Duration
	recheckInterval time.Duration
}

func (n *callHealthNotifier) notifyEvery() time.Duration {
	if n.notifyInterval != 0 {
		return n.notifyInterval
	}
	return defaultCallHealthNotifyInterval
}

func (n *callHealthNotifier) recheckEvery() time.Duration {
	if n.recheckInterval != 0 {
		return n.recheckInterval
	}
	return defaultCallHealthRecheckInterval
}

// SetCallHealthObserver registers the callback invoked (debounced, off the
// call path's locks) when a server's failure-rate verdict changes.
func (m *Manager) SetCallHealthObserver(observer func(server string)) {
	m.callHealth.mu.Lock()
	m.callHealth.observer = observer
	m.callHealth.mu.Unlock()
}

// callRateDegraded mirrors the health calculator's rule.
func callRateDegraded(calls, failures int) bool {
	return calls >= health.CallFailureMinSamples &&
		float64(failures)/float64(calls) > health.CallFailureRatio
}

// noteCallHealth re-evaluates server's failure-rate verdict and notifies on a
// flip. While the window holds any counted call a recheck timer stays armed,
// because expiry can flip the verdict in EITHER direction with no triggering
// call: old failures aging out recover a server, and old successes aging out
// can leave a failure-heavy remainder that degrades one.
func (m *Manager) noteCallHealth(server string) {
	n := &m.callHealth
	n.mu.Lock()
	if n.observer == nil {
		n.mu.Unlock()
		return
	}
	calls, failures, _ := m.callStats.Snapshot(server)
	degraded := callRateDegraded(calls, failures)

	if n.servers == nil {
		n.servers = make(map[string]*callHealthState)
	}
	st := n.servers[server]
	if st == nil {
		if calls == 0 {
			n.mu.Unlock()
			return
		}
		st = &callHealthState{}
		n.servers[server] = st
	}
	if calls > 0 && st.recheck == nil {
		st.recheck = time.AfterFunc(n.recheckEvery(), func() {
			n.mu.Lock()
			if n.servers[server] != st {
				n.mu.Unlock()
				return
			}
			st.recheck = nil
			n.mu.Unlock()
			m.noteCallHealth(server)
		})
	}
	flipped := degraded != st.degraded
	st.degraded = degraded
	if calls == 0 && !degraded && st.notifyTimer == nil && !flipped {
		// Window empty and settled: forget the server until its next call.
		stopTimer(st.recheck)
		delete(n.servers, server)
	}
	if !flipped {
		n.mu.Unlock()
		return
	}

	observer := n.observer
	wait := n.notifyEvery() - time.Since(st.lastNotify)
	if wait <= 0 {
		st.lastNotify = time.Now()
		n.mu.Unlock()
		observer(server)
		return
	}
	if st.notifyTimer == nil {
		st.notifyTimer = time.AfterFunc(wait, func() {
			n.mu.Lock()
			// A timer stopped by dropCallHealth/shutdown may already be
			// running; it must not touch or notify for a replacement state.
			if n.servers[server] != st {
				n.mu.Unlock()
				return
			}
			st.notifyTimer = nil
			st.lastNotify = time.Now()
			n.mu.Unlock()
			observer(server)
		})
	}
	n.mu.Unlock()
}

// dropCallHealth forgets a removed server's notifier state and timers.
func (m *Manager) dropCallHealth(server string) {
	n := &m.callHealth
	n.mu.Lock()
	defer n.mu.Unlock()
	if st := n.servers[server]; st != nil {
		stopTimer(st.notifyTimer)
		stopTimer(st.recheck)
		delete(n.servers, server)
	}
}

// stopCallHealthTimers stops every pending timer (shutdown and tests).
func (m *Manager) stopCallHealthTimers() {
	n := &m.callHealth
	n.mu.Lock()
	defer n.mu.Unlock()
	for name, st := range n.servers {
		stopTimer(st.notifyTimer)
		stopTimer(st.recheck)
		delete(n.servers, name)
	}
	n.observer = nil
}

func stopTimer(t *time.Timer) {
	if t != nil {
		t.Stop()
	}
}
