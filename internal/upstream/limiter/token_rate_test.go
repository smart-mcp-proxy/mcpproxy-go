package limiter

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeClock is the injectable time source: a minute passes when the test says
// so, never because the test is slow.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

func newRates(t *testing.T, def RateSpec) (*TokenRates, *fakeClock) {
	t.Helper()
	clk := newFakeClock()
	r := NewTokenRates(clk.Now)
	r.SetDefault(def)
	return r, clk
}

func TestTokenRates_OffByDefault(t *testing.T) {
	var r TokenRates // the zero value must be usable and limit nothing
	for i := 0; i < 1000; i++ {
		require.True(t, r.Allow("tok", nil).Allowed)
	}
	assert.Equal(t, 0, r.Len(), "an unlimited key holds no state")

	var nilRates *TokenRates
	assert.True(t, nilRates.Allow("tok", nil).Allowed, "a nil TokenRates admits everything")
}

func TestTokenRates_BurstThenRefused(t *testing.T) {
	r, _ := newRates(t, RateSpec{PerMinute: 60, Burst: 5})

	for i := 0; i < 5; i++ {
		require.True(t, r.Allow("tok", nil).Allowed, "call %d is inside the burst", i+1)
	}
	d := r.Allow("tok", nil)
	require.False(t, d.Allowed, "the sixth call is over the burst")
	assert.Equal(t, RateSpec{PerMinute: 60, Burst: 5}, d.Spec)
	assert.Equal(t, time.Second, d.RetryAfter, "60/min refills one call a second")
}

func TestTokenRates_BurstDefaultsToPerMinute(t *testing.T) {
	r, _ := newRates(t, RateSpec{PerMinute: 3})

	for i := 0; i < 3; i++ {
		require.True(t, r.Allow("tok", nil).Allowed)
	}
	d := r.Allow("tok", nil)
	require.False(t, d.Allowed)
	assert.Equal(t, 20*time.Second, d.RetryAfter, "3/min refills one call every 20s")
}

func TestTokenRates_RefillsOverTime(t *testing.T) {
	r, clk := newRates(t, RateSpec{PerMinute: 60, Burst: 2})

	require.True(t, r.Allow("tok", nil).Allowed)
	require.True(t, r.Allow("tok", nil).Allowed)
	require.False(t, r.Allow("tok", nil).Allowed)

	clk.Advance(500 * time.Millisecond)
	d := r.Allow("tok", nil)
	require.False(t, d.Allowed, "half a second earns half a call")
	assert.Equal(t, 500*time.Millisecond, d.RetryAfter, "the hint is what is still missing, not a full interval")

	clk.Advance(500 * time.Millisecond)
	require.True(t, r.Allow("tok", nil).Allowed, "a full second earned one call")
	require.False(t, r.Allow("tok", nil).Allowed)

	clk.Advance(time.Hour)
	require.True(t, r.Allow("tok", nil).Allowed)
	require.True(t, r.Allow("tok", nil).Allowed)
	require.False(t, r.Allow("tok", nil).Allowed, "a long idle spell refills to the burst, not beyond it")
}

func TestTokenRates_RetryAfterIsNeverZeroWhenRefused(t *testing.T) {
	r, clk := newRates(t, RateSpec{PerMinute: 6000, Burst: 1})

	require.True(t, r.Allow("tok", nil).Allowed)
	clk.Advance(time.Nanosecond)
	d := r.Allow("tok", nil)
	require.False(t, d.Allowed)
	assert.Greater(t, d.RetryAfter, time.Duration(0))
}

func TestTokenRates_KeysAreIsolated(t *testing.T) {
	r, _ := newRates(t, RateSpec{PerMinute: 60, Burst: 2})

	require.True(t, r.Allow("a", nil).Allowed)
	require.True(t, r.Allow("a", nil).Allowed)
	require.False(t, r.Allow("a", nil).Allowed, "a is spent")

	require.True(t, r.Allow("b", nil).Allowed, "b has its own bucket")
	require.True(t, r.Allow("b", nil).Allowed)
	require.False(t, r.Allow("b", nil).Allowed)
}

func TestTokenRates_OverrideReplacesDefault(t *testing.T) {
	r, _ := newRates(t, RateSpec{PerMinute: 60, Burst: 1})

	tight := RateSpec{PerMinute: 60, Burst: 3}
	for i := 0; i < 3; i++ {
		require.True(t, r.Allow("tight", &tight).Allowed, "the override's burst applies")
	}
	require.False(t, r.Allow("tight", &tight).Allowed)

	require.True(t, r.Allow("plain", nil).Allowed)
	require.False(t, r.Allow("plain", nil).Allowed, "a key without an override keeps the default")
}

func TestTokenRates_ZeroOverrideExemptsFromDefault(t *testing.T) {
	r, _ := newRates(t, RateSpec{PerMinute: 60, Burst: 1})

	exempt := RateSpec{}
	for i := 0; i < 100; i++ {
		require.True(t, r.Allow("exempt", &exempt).Allowed)
	}
}

func TestTokenRates_OverrideWorksWithoutDefault(t *testing.T) {
	r, _ := newRates(t, RateSpec{})

	own := RateSpec{PerMinute: 60, Burst: 1}
	require.True(t, r.Allow("tok", &own).Allowed)
	require.False(t, r.Allow("tok", &own).Allowed)
	require.True(t, r.Allow("other", nil).Allowed, "no default, no override: unlimited")
}

func TestTokenRates_ChangedBudgetKeepsWhatTheBucketHas(t *testing.T) {
	r, clk := newRates(t, RateSpec{PerMinute: 60, Burst: 10})

	for i := 0; i < 10; i++ {
		require.True(t, r.Allow("tok", nil).Allowed)
	}
	require.False(t, r.Allow("tok", nil).Allowed)

	// Raising the limit does not refund a spent bucket...
	r.SetDefault(RateSpec{PerMinute: 600, Burst: 100})
	require.False(t, r.Allow("tok", nil).Allowed, "an edit is not a refill")

	// ...but the faster rate applies from now on: 100ms earns one call at 600/min.
	clk.Advance(100 * time.Millisecond)
	require.True(t, r.Allow("tok", nil).Allowed)

	// Lowering it clamps a full bucket to the new size.
	clk.Advance(time.Hour)
	r.SetDefault(RateSpec{PerMinute: 60, Burst: 2})
	require.True(t, r.Allow("tok", nil).Allowed)
	require.True(t, r.Allow("tok", nil).Allowed)
	require.False(t, r.Allow("tok", nil).Allowed, "the old, larger bucket does not outlive the edit")
}

func TestTokenRates_TurningTheDefaultOffAdmitsEveryone(t *testing.T) {
	r, _ := newRates(t, RateSpec{PerMinute: 60, Burst: 1})

	require.True(t, r.Allow("tok", nil).Allowed)
	require.False(t, r.Allow("tok", nil).Allowed)

	r.SetDefault(RateSpec{})
	require.True(t, r.Allow("tok", nil).Allowed)
	assert.Equal(t, 0, r.Len(), "an unlimited key's bucket is dropped")

	r.SetDefault(RateSpec{PerMinute: -5})
	assert.False(t, r.Default().Enabled(), "a negative rate is off, not a panic")
}

func TestTokenRates_ClockGoingBackwardsEarnsNothing(t *testing.T) {
	r, clk := newRates(t, RateSpec{PerMinute: 60, Burst: 1})

	require.True(t, r.Allow("tok", nil).Allowed)
	clk.Advance(-time.Hour)
	require.False(t, r.Allow("tok", nil).Allowed)
	clk.Advance(time.Hour + time.Second)
	require.True(t, r.Allow("tok", nil).Allowed)
}

func TestTokenRates_IdleBucketsAreForgotten(t *testing.T) {
	r, clk := newRates(t, RateSpec{PerMinute: 60, Burst: 2})

	for _, k := range []string{"a", "b", "c"} {
		require.True(t, r.Allow(k, nil).Allowed)
	}
	require.Equal(t, 3, r.Len())

	// Two calls at 60/min is two seconds to refill; well past the sweep interval.
	clk.Advance(5 * time.Minute)
	require.True(t, r.Allow("d", nil).Allowed)
	assert.Equal(t, 1, r.Len(), "revoked or renamed tokens do not accumulate")
}

func TestTokenRates_ConcurrentCallersNeverExceedTheBurst(t *testing.T) {
	r, _ := newRates(t, RateSpec{PerMinute: 60, Burst: 50})

	var wg sync.WaitGroup
	var mu sync.Mutex
	allowed := 0
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if r.Allow("tok", nil).Allowed {
				mu.Lock()
				allowed++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	assert.Equal(t, 50, allowed)
}

func TestRegistry_TokenRates(t *testing.T) {
	var nilReg *Registry
	assert.Nil(t, nilReg.TokenRates())

	reg := NewRegistry()
	require.NotNil(t, reg.TokenRates())
	assert.Same(t, reg.TokenRates(), reg.TokenRates(), "one budget per registry")
	assert.True(t, reg.TokenRates().Allow("tok", nil).Allowed, "a fresh registry limits nothing")
}

func TestLimitError_RateLimited(t *testing.T) {
	err := error(&LimitError{Scope: ScopeToken, Reason: ReasonRateLimited, Limit: 60, Burst: 10, RetryAfter: 1500 * time.Millisecond})

	var le *LimitError
	require.True(t, errors.As(err, &le))
	assert.True(t, le.Shed())
	assert.True(t, errors.Is(err, ErrRateLimited))
	assert.False(t, errors.Is(err, ErrQueueFull))
	assert.False(t, errors.Is(err, ErrServerUnavailable))

	msg := le.UserMessage()
	assert.Contains(t, msg, "60 tool calls per minute")
	assert.Contains(t, msg, "bursts of up to 10")
	assert.Contains(t, msg, "Retry in 2 seconds.", "a fractional wait rounds up, like the Retry-After header")
	assert.NotContains(t, msg, "busy", "a rate limit is not saturation, and names no server")
	assert.Equal(t, msg, le.Error(), "the typed error reads the same as its user message")

	one := &LimitError{Scope: ScopeToken, Reason: ReasonRateLimited, Limit: 60, RetryAfter: 10 * time.Millisecond}
	assert.Contains(t, one.UserMessage(), "Retry in 1 second.")
	assert.NotContains(t, one.UserMessage(), "bursts", "a burst equal to the rate is not worth a clause")
}

func TestLimitError_ShedVocabulary(t *testing.T) {
	for reason, want := range map[Reason]bool{
		ReasonQueueFull:         true,
		ReasonQueueTimeout:      true,
		ReasonRateLimited:       true,
		ReasonServerUnavailable: false,
	} {
		assert.Equal(t, want, (&LimitError{Reason: reason}).Shed(), string(reason))
	}
	assert.False(t, (*LimitError)(nil).Shed())
}
