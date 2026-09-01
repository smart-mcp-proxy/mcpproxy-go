package limiter

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// deadlineIn returns an absolute queue deadline d from now (FR-004: the queue
// deadline is one absolute deadline shared across tiers, not a per-step timeout).
func deadlineIn(d time.Duration) time.Time { return time.Now().Add(d) }

func TestNilLimiterIsPassthrough(t *testing.T) {
	var l *Limiter
	release, err := l.Acquire(context.Background(), deadlineIn(time.Millisecond))
	if err != nil {
		t.Fatalf("nil limiter Acquire returned error: %v", err)
	}
	if release == nil {
		t.Fatal("nil limiter Acquire returned nil release func")
	}
	release()
	release() // must be safe to call twice
}

func TestZeroMaxIsPassthrough(t *testing.T) {
	l := New(ScopeServer, "srv", Limits{})

	const n = 50
	releases := make([]func(), 0, n)
	for i := 0; i < n; i++ {
		release, err := l.Acquire(context.Background(), deadlineIn(10*time.Millisecond))
		if err != nil {
			t.Fatalf("acquire %d: %v", i, err)
		}
		releases = append(releases, release)
	}
	if got := l.Stats().Running; got != n {
		t.Fatalf("Running = %d, want %d (occupancy is tracked even when unlimited)", got, n)
	}
	for _, r := range releases {
		r()
	}
	if got := l.Stats().Running; got != 0 {
		t.Fatalf("Running after release = %d, want 0", got)
	}
}

func TestMaxConcurrencyIsNeverExceeded(t *testing.T) {
	const max = 3
	l := New(ScopeServer, "srv", Limits{Max: max, QueueSize: 64, QueueTimeout: 5 * time.Second})

	var current, peak int64
	var wg sync.WaitGroup
	deadline := deadlineIn(5 * time.Second)
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			release, err := l.Acquire(context.Background(), deadline)
			if err != nil {
				t.Errorf("acquire: %v", err)
				return
			}
			defer release()
			cur := atomic.AddInt64(&current, 1)
			for {
				old := atomic.LoadInt64(&peak)
				if cur <= old || atomic.CompareAndSwapInt64(&peak, old, cur) {
					break
				}
			}
			time.Sleep(2 * time.Millisecond)
			atomic.AddInt64(&current, -1)
		}()
	}
	wg.Wait()

	if peak > max {
		t.Fatalf("peak concurrency = %d, want <= %d", peak, max)
	}
	if st := l.Stats(); st.Running != 0 || st.Queued != 0 {
		t.Fatalf("stats after drain = %+v, want zero", st)
	}
}

func TestQueueFullRejectsImmediately(t *testing.T) {
	l := New(ScopeServer, "github", Limits{Max: 1, QueueSize: 1, QueueTimeout: 10 * time.Second})

	rel1, err := l.Acquire(context.Background(), deadlineIn(time.Second))
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	defer rel1()

	// Second call occupies the single queue slot.
	queued := make(chan struct{})
	go func() {
		close(queued)
		rel, err := l.Acquire(context.Background(), deadlineIn(5*time.Second))
		if err == nil {
			rel()
		}
	}()
	<-queued
	waitFor(t, time.Second, func() bool { return l.Stats().Queued == 1 })

	// Third call must be rejected instantly (SC-005: < 100ms).
	start := time.Now()
	_, err = l.Acquire(context.Background(), deadlineIn(10*time.Second))
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected queue-full rejection, got nil error")
	}
	if elapsed > 100*time.Millisecond {
		t.Fatalf("queue-full rejection took %v, want < 100ms", elapsed)
	}
	if !errors.Is(err, ErrQueueFull) {
		t.Fatalf("error %v does not match ErrQueueFull", err)
	}
	var le *LimitError
	if !errors.As(err, &le) {
		t.Fatalf("error %v is not a *LimitError", err)
	}
	if le.Scope != ScopeServer || le.Server != "github" || le.Reason != ReasonQueueFull {
		t.Fatalf("LimitError = %+v, want scope=server server=github reason=queue_full", le)
	}
	if le.Limit != 1 {
		t.Fatalf("LimitError.Limit = %d, want 1", le.Limit)
	}
	if le.RetryAfter != 10*time.Second {
		t.Fatalf("LimitError.RetryAfter = %v, want 10s (effective queue_timeout of the shedding scope)", le.RetryAfter)
	}
}

func TestZeroQueueSizeShedsAtTheCap(t *testing.T) {
	l := New(ScopeServer, "srv", Limits{Max: 1, QueueSize: 0, QueueTimeout: time.Second})

	rel, err := l.Acquire(context.Background(), deadlineIn(time.Second))
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	defer rel()

	if _, err := l.Acquire(context.Background(), deadlineIn(time.Second)); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("queue_size 0 must shed at the cap, got %v", err)
	}
}

func TestQueueTimeoutUsesAbsoluteDeadline(t *testing.T) {
	l := New(ScopeGlobal, "", Limits{Max: 1, QueueSize: 4, QueueTimeout: 80 * time.Millisecond})

	rel, err := l.Acquire(context.Background(), deadlineIn(time.Second))
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	defer rel()

	start := time.Now()
	_, err = l.Acquire(context.Background(), start.Add(80*time.Millisecond))
	elapsed := time.Since(start)
	if !errors.Is(err, ErrQueueTimeout) {
		t.Fatalf("error %v does not match ErrQueueTimeout", err)
	}
	if elapsed < 60*time.Millisecond {
		t.Fatalf("timed out after %v, want >= ~80ms (absolute deadline honored)", elapsed)
	}
	var le *LimitError
	if !errors.As(err, &le) {
		t.Fatalf("error %v is not a *LimitError", err)
	}
	if le.Scope != ScopeGlobal {
		t.Fatalf("scope = %s, want global", le.Scope)
	}
	if le.Server != "" {
		t.Fatalf("global LimitError must not name a server, got %q", le.Server)
	}
	if st := l.Stats(); st.Queued != 0 {
		t.Fatalf("queued after timeout = %d, want 0 (slot released)", st.Queued)
	}
}

func TestExpiredDeadlineShedsImmediately(t *testing.T) {
	l := New(ScopeServer, "srv", Limits{Max: 1, QueueSize: 4, QueueTimeout: time.Second})
	rel, err := l.Acquire(context.Background(), deadlineIn(time.Second))
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	defer rel()

	start := time.Now()
	_, err = l.Acquire(context.Background(), time.Now().Add(-time.Second))
	if !errors.Is(err, ErrQueueTimeout) {
		t.Fatalf("error %v does not match ErrQueueTimeout", err)
	}
	if time.Since(start) > 100*time.Millisecond {
		t.Fatalf("expired deadline took %v to shed", time.Since(start))
	}
}

func TestCallerCancelWhileQueuedIsNotShedding(t *testing.T) {
	l := New(ScopeServer, "srv", Limits{Max: 1, QueueSize: 4, QueueTimeout: 10 * time.Second})

	rel, err := l.Acquire(context.Background(), deadlineIn(10*time.Second))
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	defer rel()

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		_, err := l.Acquire(ctx, deadlineIn(10*time.Second))
		errCh <- err
	}()
	waitFor(t, time.Second, func() bool { return l.Stats().Queued == 1 })
	cancel()

	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error %v does not match context.Canceled", err)
		}
		if errors.Is(err, ErrQueueTimeout) || errors.Is(err, ErrQueueFull) {
			t.Fatalf("caller cancellation must not be reported as shedding: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled waiter did not return")
	}
	waitFor(t, time.Second, func() bool { return l.Stats().Queued == 0 })
}

func TestCallerDeadlineWhileQueuedPropagates(t *testing.T) {
	l := New(ScopeServer, "srv", Limits{Max: 1, QueueSize: 4, QueueTimeout: 10 * time.Second})
	rel, err := l.Acquire(context.Background(), deadlineIn(10*time.Second))
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	defer rel()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err = l.Acquire(ctx, deadlineIn(10*time.Second))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error %v does not match context.DeadlineExceeded", err)
	}
}

func TestFIFOOrder(t *testing.T) {
	l := New(ScopeServer, "srv", Limits{Max: 1, QueueSize: 16, QueueTimeout: 10 * time.Second})

	rel, err := l.Acquire(context.Background(), deadlineIn(10*time.Second))
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}

	const n = 6
	var mu sync.Mutex
	order := make([]int, 0, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			r, err := l.Acquire(context.Background(), deadlineIn(10*time.Second))
			if err != nil {
				t.Errorf("waiter %d: %v", idx, err)
				return
			}
			mu.Lock()
			order = append(order, idx)
			mu.Unlock()
			r()
		}(i)
		// Serialize enqueue so arrival order is deterministic.
		want := i + 1
		waitFor(t, time.Second, func() bool { return l.Stats().Queued == want })
	}

	rel()
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	for i, got := range order {
		if got != i {
			t.Fatalf("admission order = %v, want FIFO 0..%d", order, n-1)
		}
	}
}

func TestHotSwapLowerCapDoesNotGrantNewCapacity(t *testing.T) {
	l := New(ScopeServer, "srv", Limits{Max: 4, QueueSize: 8, QueueTimeout: 10 * time.Second})

	releases := make([]func(), 0, 4)
	for i := 0; i < 4; i++ {
		r, err := l.Acquire(context.Background(), deadlineIn(time.Second))
		if err != nil {
			t.Fatalf("acquire %d: %v", i, err)
		}
		releases = append(releases, r)
	}

	// Lower the cap while 4 calls are running: occupancy is shared across
	// generations (FR-021), so no new admission may happen until it drains.
	l.setLimits(Limits{Max: 1, QueueSize: 8, QueueTimeout: 10 * time.Second})

	admitted := make(chan struct{})
	go func() {
		r, err := l.Acquire(context.Background(), deadlineIn(10*time.Second))
		if err == nil {
			close(admitted)
			r()
		}
	}()

	// Release three of four: occupancy 1 == cap, still no admission.
	for i := 0; i < 3; i++ {
		releases[i]()
	}
	select {
	case <-admitted:
		t.Fatal("new call admitted while occupancy still at the lowered cap")
	case <-time.After(150 * time.Millisecond):
	}

	releases[3]()
	select {
	case <-admitted:
	case <-time.After(2 * time.Second):
		t.Fatal("queued call was not admitted after occupancy drained")
	}
}

func TestHotSwapRaiseAdmitsQueuedImmediately(t *testing.T) {
	l := New(ScopeServer, "srv", Limits{Max: 1, QueueSize: 8, QueueTimeout: 10 * time.Second})

	rel, err := l.Acquire(context.Background(), deadlineIn(10*time.Second))
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	defer rel()

	admitted := make(chan struct{}, 2)
	for i := 0; i < 2; i++ {
		go func() {
			r, err := l.Acquire(context.Background(), deadlineIn(10*time.Second))
			if err == nil {
				admitted <- struct{}{}
				r()
			}
		}()
	}
	waitFor(t, time.Second, func() bool { return l.Stats().Queued == 2 })

	l.setLimits(Limits{Max: 3, QueueSize: 8, QueueTimeout: 10 * time.Second})

	for i := 0; i < 2; i++ {
		select {
		case <-admitted:
		case <-time.After(2 * time.Second):
			t.Fatal("raising the cap did not admit queued calls")
		}
	}
}

func TestRetireFailsQueuedAndFutureAcquires(t *testing.T) {
	l := New(ScopeServer, "srv", Limits{Max: 1, QueueSize: 8, QueueTimeout: time.Hour})

	rel, err := l.Acquire(context.Background(), deadlineIn(time.Hour))
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		_, err := l.Acquire(context.Background(), deadlineIn(time.Hour))
		errCh <- err
	}()
	waitFor(t, time.Second, func() bool { return l.Stats().Queued == 1 })

	l.Retire()

	select {
	case err := <-errCh:
		if !errors.Is(err, ErrServerUnavailable) {
			t.Fatalf("queued call after retire: %v, want ErrServerUnavailable", err)
		}
	case <-time.After(time.Second):
		t.Fatal("retire did not promptly fail the queued call (it waited for queue_timeout)")
	}

	// Admit-after-disable race: an acquire on a retired limiter must fail even
	// though capacity looks free once the running call releases.
	rel()
	if _, err := l.Acquire(context.Background(), deadlineIn(time.Second)); !errors.Is(err, ErrServerUnavailable) {
		t.Fatalf("acquire on retired limiter: %v, want ErrServerUnavailable", err)
	}
}

// TestGrantRacingRetireIsObservedByTheWaiter pins the FR-009 interleaving that
// Retire alone cannot cover: a waiter that was GRANTED a slot is already off the
// waiter list, so Retire never sees it. Without the post-wake re-check it would
// wake up, find no retirement flag of its own, and run a call against a server
// that has just been disabled — while holding a slot in the retired instance.
//
// The waiterWoke hook makes the interleaving exact rather than probabilistic:
// the grant has happened, the waiter is parked between the wake and the
// re-check, and retirement lands in that window.
func TestGrantRacingRetireIsObservedByTheWaiter(t *testing.T) {
	l := New(ScopeServer, "srv", Limits{Max: 1, QueueSize: 4, QueueTimeout: time.Hour})

	woke := make(chan struct{})
	proceed := make(chan struct{})
	l.waiterWoke = func() {
		close(woke)
		<-proceed
	}

	rel, err := l.Acquire(context.Background(), deadlineIn(time.Hour))
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		_, aerr := l.Acquire(context.Background(), deadlineIn(time.Hour))
		errCh <- aerr
	}()
	waitFor(t, time.Second, func() bool { return l.Stats().Queued == 1 })

	rel()  // grants the queued waiter and closes its channel
	<-woke // the waiter is now parked between the grant and the re-check
	l.Retire()
	close(proceed)

	select {
	case err := <-errCh:
		if !errors.Is(err, ErrServerUnavailable) {
			t.Fatalf("waiter granted just before retirement: %v, want ErrServerUnavailable", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("waiter never returned after retirement")
	}

	if got := l.Stats().Running; got != 0 {
		t.Fatalf("Running = %d, want 0 — a slot granted before retirement must be handed back", got)
	}
}

func TestReleaseIsIdempotentAndBoundToInstance(t *testing.T) {
	l := New(ScopeServer, "srv", Limits{Max: 1, QueueSize: 1, QueueTimeout: time.Second})
	rel, err := l.Acquire(context.Background(), deadlineIn(time.Second))
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	rel()
	rel()
	rel()
	if got := l.Stats().Running; got != 0 {
		t.Fatalf("Running = %d after repeated release, want 0", got)
	}
	// A fresh acquire must still be possible (no negative occupancy leak).
	rel2, err := l.Acquire(context.Background(), deadlineIn(time.Second))
	if err != nil {
		t.Fatalf("acquire after repeated release: %v", err)
	}
	if got := l.Stats().Running; got != 1 {
		t.Fatalf("Running = %d, want 1", got)
	}
	rel2()
}

func TestConcurrentAcquireReleaseUnderSwap(t *testing.T) {
	l := New(ScopeServer, "srv", Limits{Max: 2, QueueSize: 32, QueueTimeout: 5 * time.Second})

	stop := make(chan struct{})
	var swapWG sync.WaitGroup
	swapWG.Add(1)
	go func() {
		defer swapWG.Done()
		caps := []int{1, 4, 2, 8}
		i := 0
		for {
			select {
			case <-stop:
				return
			default:
			}
			l.setLimits(Limits{Max: caps[i%len(caps)], QueueSize: 32, QueueTimeout: 5 * time.Second})
			i++
			time.Sleep(time.Millisecond)
		}
	}()

	var wg sync.WaitGroup
	for i := 0; i < 60; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := l.Acquire(context.Background(), deadlineIn(5*time.Second))
			if err != nil {
				if !errors.Is(err, ErrQueueFull) && !errors.Is(err, ErrQueueTimeout) {
					t.Errorf("unexpected error: %v", err)
				}
				return
			}
			time.Sleep(time.Millisecond)
			r()
		}()
	}
	wg.Wait()
	close(stop)
	swapWG.Wait()

	if st := l.Stats(); st.Running != 0 || st.Queued != 0 {
		t.Fatalf("stats after drain = %+v, want zero", st)
	}
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("condition not met within %v", timeout)
}

// TestRaisedCapGoesToTheWaiterNotANewcomer pins FIFO across a cap raise.
//
// Publishing a raise is necessarily two steps — store the new generation, then
// re-grant the queue — and the fast path used to admit on free capacity without
// looking at the queue at all. A call arriving in that window therefore took
// the slot the raise had just created, in front of a waiter that had been in
// line for the whole reload.
//
// The window is reproduced exactly: the limits are swapped WITHOUT re-granting
// (which is the state between the two publish steps), and the cap is raised by
// only one, so there is exactly one new slot and it can go to only one of them.
func TestRaisedCapGoesToTheWaiterNotANewcomer(t *testing.T) {
	l := New(ScopeServer, "srv", Limits{Max: 1, QueueSize: 4, QueueTimeout: time.Hour})

	held, err := l.Acquire(context.Background(), deadlineIn(time.Hour))
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	defer held()

	waiterErr := make(chan error, 1)
	waiterAdmitted := make(chan struct{})
	go func() {
		release, aerr := l.Acquire(context.Background(), deadlineIn(time.Hour))
		if aerr == nil {
			close(waiterAdmitted)
			release()
		}
		waiterErr <- aerr
	}()
	waitFor(t, time.Second, func() bool { return l.Stats().Queued == 1 })

	// The raise lands, the re-grant has not run yet: one new slot, one waiter,
	// and a newcomer racing for it. queue_size 0 makes the newcomer's outcome
	// unambiguous — it either takes the slot or is shed.
	raised := Limits{Max: 2, QueueSize: 0, QueueTimeout: time.Hour}
	l.held.Store(&raised)

	_, newcomerErr := l.acquire(context.Background(), raised, deadlineIn(time.Hour))
	if !errors.Is(newcomerErr, ErrQueueFull) {
		t.Fatalf("newcomer took the slot the raise created: err = %v, want ErrQueueFull", newcomerErr)
	}

	select {
	case <-waiterAdmitted:
	case <-time.After(2 * time.Second):
		t.Fatal("the queued call must get the first slot a raise creates (FIFO)")
	}
	if aerr := <-waiterErr; aerr != nil {
		t.Fatalf("queued call: %v", aerr)
	}
}

// TestFastPathDoesNotOvertakeAQueue is the same rule in steady state: while
// anyone is queued, an arriving call joins the back of the line even if its own
// generation would let it run.
//
// Admission order is recorded by each caller stamping a shared counter the
// moment Acquire returns and BEFORE it releases — the same trick as the raise
// test above. It has to be that way round: the second waiter can only be
// granted from inside the first one's release, so a stamp taken before the
// release is a faithful record of the grant order. Reporting order is not:
// both callers finish while the test is still parked, so waiting on their
// result channels would only measure which goroutine got scheduled first.
func TestFastPathDoesNotOvertakeAQueue(t *testing.T) {
	l := New(ScopeServer, "srv", Limits{Max: 1, QueueSize: 4, QueueTimeout: time.Hour})

	held, err := l.Acquire(context.Background(), deadlineIn(time.Hour))
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}

	var admissions atomic.Int64
	type admission struct {
		seq int64
		err error
	}

	first := make(chan admission, 1)
	go func() {
		release, aerr := l.Acquire(context.Background(), deadlineIn(time.Hour))
		seq := admissions.Add(1)
		if aerr == nil {
			release()
		}
		first <- admission{seq: seq, err: aerr}
	}()
	waitFor(t, time.Second, func() bool { return l.Stats().Queued == 1 })

	// A stale generation with a bigger cap must not let this call jump the queue.
	second := make(chan admission, 1)
	go func() {
		release, aerr := l.acquire(context.Background(), Limits{Max: 5, QueueSize: 4, QueueTimeout: time.Hour}, deadlineIn(time.Hour))
		seq := admissions.Add(1)
		if aerr == nil {
			release()
		}
		second <- admission{seq: seq, err: aerr}
	}()
	waitFor(t, time.Second, func() bool { return l.Stats().Queued == 2 })

	if got := l.Stats().Running; got != 1 {
		t.Fatalf("Running = %d, want 1 — a permissive generation must not overtake the queue", got)
	}

	held()
	firstAdmission := awaitAdmission(t, first, "first queued call")
	secondAdmission := awaitAdmission(t, second, "second queued call")
	if firstAdmission.err != nil {
		t.Fatalf("queued call: %v", firstAdmission.err)
	}
	if secondAdmission.err != nil {
		t.Fatalf("second queued call: %v", secondAdmission.err)
	}
	if firstAdmission.seq != 1 || secondAdmission.seq != 2 {
		t.Fatalf("admission order: first was #%d and second was #%d, want #1 then #2 (FIFO violated)",
			firstAdmission.seq, secondAdmission.seq)
	}
}

// awaitAdmission collects one caller's outcome, failing the test if the queue
// never drained. Both result channels are buffered, so the callers finish
// regardless of the order they are collected in.
func awaitAdmission[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(2 * time.Second):
		t.Fatalf("%s did not drain", what)
		var zero T
		return zero
	}
}
