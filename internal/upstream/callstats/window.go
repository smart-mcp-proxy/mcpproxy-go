// Package callstats keeps a rolling, in-memory record of per-server tool-call
// outcomes so the health calculator can report a server whose calls mostly
// fail as degraded even though its connection looks fine (Spec 113-d).
//
// The window is bucketed (30 buckets of 10 s = 5 min) so Record and Snapshot
// are O(buckets) with no per-call allocation. Nothing here is persisted.
package callstats

import (
	"sync"
	"time"
)

const (
	// BucketSize is the width of one counter bucket.
	BucketSize = 10 * time.Second
	// NumBuckets is the number of buckets kept.
	NumBuckets = 30
	// WindowSize is the rolling window length (NumBuckets * BucketSize).
	WindowSize = NumBuckets * BucketSize
)

// Kind is the dominant class of a failed call.
type Kind uint8

const (
	// KindNone means no failure (or no data).
	KindNone Kind = iota
	// KindNetwork is a connection-level failure (refused, reset, closed, EOF).
	KindNetwork
	// KindTimeout is a timeout or deadline after the call was dispatched.
	KindTimeout
	// KindHTTP is an HTTP status failure from the upstream.
	KindHTTP
	// KindJSONRPC is a JSON-RPC protocol error from the upstream.
	KindJSONRPC
	// KindSession is a terminated MCP session.
	KindSession
	// KindOther is any other counted failure.
	KindOther

	numKinds
)

// String returns a short human-readable label used in health detail text.
func (k Kind) String() string {
	switch k {
	case KindNetwork:
		return "network"
	case KindTimeout:
		return "timeout"
	case KindHTTP:
		return "HTTP"
	case KindJSONRPC:
		return "JSON-RPC"
	case KindSession:
		return "session"
	case KindOther:
		return "other"
	default:
		return ""
	}
}

type bucket struct {
	slot     int64 // BucketSize-aligned unix slot this bucket currently holds
	calls    int
	failures int
	kinds    [numKinds]int
}

// Window is a rolling per-server outcome counter. The zero value is ready to
// use and safe for concurrent use.
type Window struct {
	mu      sync.Mutex
	buckets [NumBuckets]bucket
}

func slotOf(t time.Time) int64 { return t.Unix() / int64(BucketSize/time.Second) }

// Record adds one outcome. Uncounted outcomes (counted == false) are ignored:
// they are in neither the numerator nor the denominator.
func (w *Window) Record(now time.Time, counted, failed bool, kind Kind) {
	if !counted {
		return
	}
	slot := slotOf(now)
	w.mu.Lock()
	defer w.mu.Unlock()
	b := &w.buckets[((slot%NumBuckets)+NumBuckets)%NumBuckets]
	if b.slot != slot {
		*b = bucket{slot: slot}
	}
	b.calls++
	if failed {
		b.failures++
		if kind >= numKinds {
			kind = KindOther
		}
		b.kinds[kind]++
	}
}

// Snapshot returns the counted calls, failures and the most frequent failure
// kind inside the window ending at now.
func (w *Window) Snapshot(now time.Time) (calls, failures int, dominant Kind) {
	cur := slotOf(now)
	var kinds [numKinds]int
	w.mu.Lock()
	for i := range w.buckets {
		b := &w.buckets[i]
		if b.calls == 0 || b.slot > cur || b.slot <= cur-NumBuckets {
			continue
		}
		calls += b.calls
		failures += b.failures
		for k := range kinds {
			kinds[k] += b.kinds[k]
		}
	}
	w.mu.Unlock()
	best := 0
	for k := KindNetwork; k < numKinds; k++ {
		if kinds[k] > best {
			best = kinds[k]
			dominant = k
		}
	}
	return calls, failures, dominant
}

// Registry holds one Window per server name.
type Registry struct {
	mu      sync.RWMutex
	windows map[string]*Window
	now     func() time.Time // injectable clock for tests
}

// NewRegistry returns an empty registry using the wall clock.
func NewRegistry() *Registry {
	return &Registry{windows: make(map[string]*Window), now: time.Now}
}

// Record adds an outcome for server.
func (r *Registry) Record(server string, counted, failed bool, kind Kind) {
	if !counted {
		return
	}
	r.mu.RLock()
	w := r.windows[server]
	r.mu.RUnlock()
	if w == nil {
		r.mu.Lock()
		if w = r.windows[server]; w == nil {
			w = &Window{}
			r.windows[server] = w
		}
		r.mu.Unlock()
	}
	w.Record(r.clock(), counted, failed, kind)
}

// Snapshot returns the current window counts for server (zeros if unknown).
func (r *Registry) Snapshot(server string) (calls, failures int, dominant Kind) {
	r.mu.RLock()
	w := r.windows[server]
	r.mu.RUnlock()
	if w == nil {
		return 0, 0, KindNone
	}
	return w.Snapshot(r.clock())
}

func (r *Registry) clock() time.Time {
	r.mu.RLock()
	now := r.now
	r.mu.RUnlock()
	return now()
}

// Drop discards a server's window (server removed or renamed).
func (r *Registry) Drop(server string) {
	r.mu.Lock()
	delete(r.windows, server)
	r.mu.Unlock()
}

// SetClock replaces the registry clock. Intended for tests.
func (r *Registry) SetClock(now func() time.Time) {
	r.mu.Lock()
	r.now = now
	r.mu.Unlock()
}
