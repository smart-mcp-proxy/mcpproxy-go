package limiter

import (
	"math"
	"sync"
	"time"
)

// RateSpec is one call budget: PerMinute calls a minute on average, with
// capacity for Burst calls at once. PerMinute <= 0 means no limit. Burst <= 0
// means "as many as PerMinute".
type RateSpec struct {
	PerMinute int
	Burst     int
}

// Enabled reports whether the spec limits anything.
func (s RateSpec) Enabled() bool { return s.PerMinute > 0 }

// capacity is the bucket size in calls.
func (s RateSpec) capacity() float64 {
	if s.Burst > 0 {
		return float64(s.Burst)
	}
	return float64(s.PerMinute)
}

// perSecond is the refill rate in calls per second.
func (s RateSpec) perSecond() float64 { return float64(s.PerMinute) / 60 }

// RateDecision is the outcome of one TokenRates.Allow.
type RateDecision struct {
	// Allowed is true when the call may proceed (it has been charged).
	Allowed bool
	// Spec is the budget that applied. Zero when the caller has none.
	Spec RateSpec
	// RetryAfter is how long until one call is available again. Zero when the
	// call was allowed.
	RetryAfter time.Duration
}

// sweepInterval is how often idle buckets are dropped.
const sweepInterval = time.Minute

// TokenRates is the per-caller call budget (issue #1699): one token bucket per
// key, all sharing a default spec that a caller may override. It is in memory
// only, so a restart gives every caller a full bucket again.
//
// The zero value is ready to use and limits nothing until SetDefault or an
// override says otherwise. It is safe for concurrent use.
type TokenRates struct {
	mu        sync.Mutex
	def       RateSpec
	buckets   map[string]*rateBucket
	now       func() time.Time
	lastSweep time.Time
}

// rateBucket is one key's state. spec is the budget the bucket was last
// charged under, so a changed budget is noticed on the next call.
type rateBucket struct {
	tokens float64
	last   time.Time
	spec   RateSpec
}

// NewTokenRates returns a TokenRates reading the time from now (time.Now when
// nil). A fake clock is how the tests make a minute pass.
func NewTokenRates(now func() time.Time) *TokenRates {
	return &TokenRates{now: now}
}

// SetClock replaces the time source. For tests; call it before any Allow.
func (t *TokenRates) SetClock(now func() time.Time) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.now = now
	t.mu.Unlock()
}

// SetDefault publishes the budget applied to every key that has no override.
// A spec with PerMinute <= 0 turns the default off.
func (t *TokenRates) SetDefault(spec RateSpec) {
	if t == nil {
		return
	}
	if spec.PerMinute < 0 {
		spec = RateSpec{}
	}
	t.mu.Lock()
	t.def = spec
	t.mu.Unlock()
}

// Default returns the current default budget.
func (t *TokenRates) Default() RateSpec {
	if t == nil {
		return RateSpec{}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.def
}

func (t *TokenRates) clock() time.Time {
	if t.now != nil {
		return t.now()
	}
	return time.Now()
}

// Allow charges one call to key. override, when non-nil, replaces the default
// budget for this key (a PerMinute of 0 exempts it). A key with no budget is
// always allowed and holds no state.
//
// A budget that changes between calls (a config reload, a different override)
// is picked up on the next call: the bucket keeps the calls it has left, capped
// to the new size, so editing a limit neither refunds a caller that has used
// it up nor locks one out.
func (t *TokenRates) Allow(key string, override *RateSpec) RateDecision {
	if t == nil {
		return RateDecision{Allowed: true}
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	spec := t.def
	if override != nil {
		spec = *override
	}
	now := t.clock()
	t.sweepLocked(now)

	if !spec.Enabled() {
		delete(t.buckets, key)
		return RateDecision{Allowed: true}
	}

	b := t.buckets[key]
	if b == nil {
		if t.buckets == nil {
			t.buckets = make(map[string]*rateBucket)
		}
		b = &rateBucket{tokens: spec.capacity(), last: now, spec: spec}
		t.buckets[key] = b
	} else {
		// Refill under the budget the bucket was charged with, then adopt the
		// new one.
		b.refill(now)
		b.spec = spec
		if c := spec.capacity(); b.tokens > c {
			b.tokens = c
		}
	}

	if b.tokens >= 1 {
		b.tokens--
		return RateDecision{Allowed: true, Spec: spec}
	}
	wait := time.Duration(math.Ceil((1 - b.tokens) / spec.perSecond() * float64(time.Second)))
	return RateDecision{Spec: spec, RetryAfter: wait}
}

// refill adds the calls earned since the last touch, up to the bucket size. A
// clock that moved backwards earns nothing.
func (b *rateBucket) refill(now time.Time) {
	if elapsed := now.Sub(b.last); elapsed > 0 {
		b.tokens += elapsed.Seconds() * b.spec.perSecond()
		if c := b.spec.capacity(); b.tokens > c {
			b.tokens = c
		}
	}
	b.last = now
}

// sweepLocked drops buckets that have been idle long enough to be full again:
// they behave exactly like a missing one, so only revoked or renamed callers
// are ever forgotten.
func (t *TokenRates) sweepLocked(now time.Time) {
	if now.Sub(t.lastSweep) < sweepInterval {
		return
	}
	t.lastSweep = now
	for key, b := range t.buckets {
		full := time.Duration(b.spec.capacity() / b.spec.perSecond() * float64(time.Second))
		if now.Sub(b.last) >= full {
			delete(t.buckets, key)
		}
	}
}

// Len is the number of live buckets (tests, diagnostics).
func (t *TokenRates) Len() int {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.buckets)
}
