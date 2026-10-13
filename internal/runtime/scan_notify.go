package runtime

import (
	"sync"
	"time"
)

// scanNotifyDebouncer collapses the per-scanner security-scan lifecycle storm
// into a single debounced "settled" event per server per scan (Spec 077 US4,
// MCP-2207). Prior partial fixes (#659, MCP-2223) trimmed individual event
// classes but the reconnect-storm × per-scanner multiplication remained: every
// reconnect re-ran the full scan_started/progress/completed lifecycle for every
// enabled scanner, flooding SSE subscribers.
//
// The model here is deliberately terminal-triggered: only terminal signals
// (scan completed / scan failed) arm the per-server debounce timer; the noisy
// started/progress signals are dropped entirely. Each terminal signal for a
// server (re)arms a short timer; when the timer finally fires quietly, exactly
// one EventTypeSecurityScanSettled is published carrying the last-known terminal
// state. A reconnect storm across N servers therefore yields at most N settled
// events (one per server), satisfying FR-015/SC-006.
//
// Concurrency: a per-server generation counter guards against the classic
// AfterFunc race where a timer that has already fired blocks on the mutex while
// a fresh signal re-arms — only the flush whose generation still matches the
// latest signal is allowed to publish, so a superseded timer is a no-op.
type scanNotifyDebouncer struct {
	rt       *Runtime
	interval time.Duration

	mu      sync.Mutex
	pending map[string]*pendingScan
}

// pendingScan holds the debounced terminal state for one server between the
// last terminal signal and the settled publish.
type pendingScan struct {
	gen     uint64
	timer   *time.Timer
	status  string         // "completed" | "failed"
	summary map[string]int // findings-by-severity from the last completed scan
	errMsg  string         // last error, when status == "failed"
}

func newScanNotifyDebouncer(rt *Runtime, interval time.Duration) *scanNotifyDebouncer {
	return &scanNotifyDebouncer{
		rt:       rt,
		interval: interval,
		pending:  make(map[string]*pendingScan),
	}
}

// noteTerminal records a terminal scan signal for a server and (re)arms the
// debounce timer. Non-nil summaries and non-empty statuses/errors overwrite the
// prior state last-write-wins, so the eventual settled event reflects the most
// recent scan in a storm.
func (d *scanNotifyDebouncer) noteTerminal(server, status string, summary map[string]int, errMsg string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	p := d.pending[server]
	if p == nil {
		p = &pendingScan{}
		d.pending[server] = p
	}
	if status != "" {
		p.status = status
	}
	if summary != nil {
		p.summary = summary
	}
	if errMsg != "" {
		p.errMsg = errMsg
	}

	p.gen++
	gen := p.gen
	if p.timer != nil {
		p.timer.Stop()
	}
	p.timer = time.AfterFunc(d.interval, func() { d.flush(server, gen) })
}

// flush publishes the single settled event for a server, but only if no newer
// terminal signal has superseded the timer that scheduled this flush.
func (d *scanNotifyDebouncer) flush(server string, gen uint64) {
	d.mu.Lock()
	p := d.pending[server]
	if p == nil || p.gen != gen {
		// Superseded by a newer signal (or already flushed) — do nothing.
		d.mu.Unlock()
		return
	}
	delete(d.pending, server)
	status := p.status
	summary := p.summary
	errMsg := p.errMsg
	d.mu.Unlock()

	d.rt.publishScanSettled(server, status, summary, errMsg)
}
