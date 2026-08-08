package limiter

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRegistryZeroConfigIsNoOp(t *testing.T) {
	r := NewRegistry()

	if r.Server("anything") != nil {
		t.Fatal("unconfigured server must have no limiter instance")
	}
	if r.Global() != nil {
		t.Fatal("unconfigured global scope must have no limiter instance")
	}

	release, err := r.Acquire(context.Background(), "anything")
	if err != nil {
		t.Fatalf("zero-config Acquire: %v", err)
	}
	if release == nil {
		t.Fatal("zero-config Acquire returned nil release")
	}
	release()
}

func TestRegistryApplyBuildsAndUpdatesScopes(t *testing.T) {
	r := NewRegistry()
	r.Apply(Limits{Max: 10, QueueSize: 5, QueueTimeout: time.Second},
		map[string]Limits{"a": {Max: 1, QueueSize: 1, QueueTimeout: 2 * time.Second}})

	if r.Global() == nil {
		t.Fatal("global limiter not created")
	}
	a := r.Server("a")
	if a == nil {
		t.Fatal("server limiter not created")
	}
	if got := a.Limits(); got.Max != 1 || got.QueueSize != 1 || got.QueueTimeout != 2*time.Second {
		t.Fatalf("server limits = %+v", got)
	}

	// Re-apply with different values: the SAME instance must be updated so
	// occupancy is shared across generations (FR-021).
	rel, err := a.Acquire(context.Background(), deadlineIn(time.Second))
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	r.Apply(Limits{Max: 20}, map[string]Limits{"a": {Max: 3, QueueSize: 2, QueueTimeout: time.Second}})
	if r.Server("a") != a {
		t.Fatal("hot-reload replaced the limiter instance; occupancy would be lost")
	}
	if got := a.Stats().Running; got != 1 {
		t.Fatalf("Running after hot-reload = %d, want 1 (occupancy shared across generations)", got)
	}
	if got := a.Limits().Max; got != 3 {
		t.Fatalf("Max after hot-reload = %d, want 3", got)
	}
	rel()
}

func TestRegistryApplyRetiresRemovedServers(t *testing.T) {
	r := NewRegistry()
	r.Apply(Limits{}, map[string]Limits{"gone": {Max: 1, QueueSize: 4, QueueTimeout: time.Hour}})

	gone := r.Server("gone")
	rel, err := gone.Acquire(context.Background(), deadlineIn(time.Hour))
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		_, err := gone.Acquire(context.Background(), deadlineIn(time.Hour))
		errCh <- err
	}()
	waitFor(t, time.Second, func() bool { return gone.Stats().Queued == 1 })

	// Server removed from the config: its queued calls must fail promptly.
	r.Apply(Limits{}, map[string]Limits{})

	select {
	case err := <-errCh:
		if !errors.Is(err, ErrServerUnavailable) {
			t.Fatalf("queued call after removal: %v, want ErrServerUnavailable", err)
		}
	case <-time.After(time.Second):
		t.Fatal("removal did not promptly fail the queued call")
	}
	if r.Server("gone") != nil {
		t.Fatal("removed server still has a live limiter")
	}

	// The retired instance keeps draining the outstanding hold safely.
	rel()
	if got := gone.Stats().Running; got != 0 {
		t.Fatalf("retired instance Running = %d, want 0 after drain", got)
	}
}

func TestRetiredHoldsDoNotDoubleCountAgainstReAddedServer(t *testing.T) {
	r := NewRegistry()
	r.Apply(Limits{}, map[string]Limits{"s": {Max: 1, QueueSize: 1, QueueTimeout: time.Second}})

	old := r.Server("s")
	rel, err := old.Acquire(context.Background(), deadlineIn(time.Second))
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}

	r.RetireServer("s")
	r.Apply(Limits{}, map[string]Limits{"s": {Max: 1, QueueSize: 1, QueueTimeout: time.Second}})

	fresh := r.Server("s")
	if fresh == nil || fresh == old {
		t.Fatal("re-added server must get a fresh limiter instance")
	}
	if got := fresh.Stats().Running; got != 0 {
		t.Fatalf("fresh instance Running = %d, want 0 (old holds belong to the retired instance)", got)
	}

	// The fresh instance has its full capacity available immediately.
	rel2, err := fresh.Acquire(context.Background(), deadlineIn(time.Second))
	if err != nil {
		t.Fatalf("fresh acquire: %v", err)
	}
	rel2()

	// Releasing the old hold drains the retired instance only.
	rel()
	if got := old.Stats().Running; got != 0 {
		t.Fatalf("retired instance Running = %d, want 0", got)
	}
	if got := fresh.Stats().Running; got != 0 {
		t.Fatalf("fresh instance Running = %d after old release, want 0", got)
	}
}

// TestAdmissionAfterRetireIsRefused is the FR-009 admit-after-disable guard at
// the REGISTRY level. Deleting the entry on retirement made a later admission
// see "no limiter for this server" and pass straight through; the tombstone
// makes it fail with the server-unavailable semantics instead.
func TestAdmissionAfterRetireIsRefused(t *testing.T) {
	r := NewRegistry()
	r.Apply(Limits{Max: 8, QueueSize: 8, QueueTimeout: time.Second},
		map[string]Limits{"s": {Max: 2, QueueSize: 2, QueueTimeout: time.Second}})

	r.RetireServer("s")

	_, err := r.Acquire(context.Background(), "s")
	if !errors.Is(err, ErrServerUnavailable) {
		t.Fatalf("admission after retirement: %v, want ErrServerUnavailable", err)
	}
	if r.Server("s") != nil {
		t.Fatal("a retired server must not expose a live limiter")
	}
	if _, ok := r.ServerStats()["s"]; ok {
		t.Fatal("a retired server must not report occupancy")
	}
	// The global tier must not have taken a slot for the refused call.
	if got := r.Global().Stats().Running; got != 0 {
		t.Fatalf("global Running = %d, want 0", got)
	}
}

// TestUnconfiguredScopesStillCountOccupancy is the FR-021 shared-occupancy
// guard: a scope with no cap is still an occupancy tracker, so hot-enabling a
// cap admits nothing until the grandfathered calls drain.
func TestUnconfiguredScopesStillCountOccupancy(t *testing.T) {
	r := NewRegistry()
	r.Apply(Limits{}, map[string]Limits{"s": {}})

	rel1, err := r.Acquire(context.Background(), "s")
	if err != nil {
		t.Fatalf("unlimited acquire: %v", err)
	}
	rel2, err := r.Acquire(context.Background(), "s")
	if err != nil {
		t.Fatalf("unlimited acquire: %v", err)
	}
	if got := r.Server("s").Stats().Running; got != 2 {
		t.Fatalf("Running = %d, want 2 (an unlimited scope still counts)", got)
	}
	if got := r.Global().Stats().Running; got != 2 {
		t.Fatalf("global Running = %d, want 2", got)
	}

	// Cap enabled below the live occupancy.
	r.Apply(Limits{}, map[string]Limits{"s": {Max: 1, QueueSize: 0, QueueTimeout: time.Second}})
	if _, err := r.Acquire(context.Background(), "s"); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("admission over a freshly enabled cap: %v, want ErrQueueFull", err)
	}

	rel1()
	rel2()
	rel3, err := r.Acquire(context.Background(), "s")
	if err != nil {
		t.Fatalf("acquire after drain: %v", err)
	}
	rel3()
}

// TestAcquireObservesOneGeneration is the FR-021 atomic-publication guard. Every
// generation ties its cap to a queue timeout of cap × 10ms, so a rejection that
// reports limit N with a Retry-After other than N × 10ms proves the admission
// combined values from two different publications (or a cap from one generation
// with a queue deadline from another).
func TestAcquireObservesOneGeneration(t *testing.T) {
	r := NewRegistry()
	publish := func(n int) {
		limits := Limits{Max: n, QueueSize: 2, QueueTimeout: time.Duration(n) * 10 * time.Millisecond}
		r.Apply(limits, map[string]Limits{"a": limits})
	}
	publish(1)

	stop := make(chan struct{})
	var applyWG sync.WaitGroup
	applyWG.Add(1)
	go func() {
		defer applyWG.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			publish(1 + i%4)
		}
	}()

	// A published generation is internally consistent by construction: both
	// scopes' limits and the wait budget always come from the same publish.
	// This is the property Acquire relies on by loading the generation once.
	var readerWG sync.WaitGroup
	readerWG.Add(1)
	go func() {
		defer readerWG.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			gen := r.gen.Load()
			scope := gen.servers["a"]
			if scope.limits.Max != gen.globalLimits.Max ||
				scope.budget != time.Duration(scope.limits.Max)*10*time.Millisecond {
				t.Errorf("generation mixes publications: global=%+v server=%+v budget=%v",
					gen.globalLimits, scope.limits, scope.budget)
				return
			}
		}
	}()

	// Occupancy is shared across generations, so however the caps move, the
	// number of calls running at once can never exceed the largest cap ever
	// published. An admission that mixed a stale cap with live occupancy would
	// break this.
	var running, peak int64

	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rel, err := r.Acquire(context.Background(), "a")
			if err != nil {
				var le *LimitError
				if !errors.As(err, &le) {
					t.Errorf("unexpected error: %v", err)
					return
				}
				if want := time.Duration(le.Limit) * 10 * time.Millisecond; le.RetryAfter != want {
					t.Errorf("mixed generation: limit %d reported with Retry-After %v, want %v",
						le.Limit, le.RetryAfter, want)
				}
				return
			}
			cur := atomic.AddInt64(&running, 1)
			for {
				old := atomic.LoadInt64(&peak)
				if cur <= old || atomic.CompareAndSwapInt64(&peak, old, cur) {
					break
				}
			}
			time.Sleep(time.Millisecond)
			atomic.AddInt64(&running, -1)
			rel()
		}()
	}
	wg.Wait()
	if peak > 4 {
		t.Fatalf("peak concurrency = %d, want <= 4 (the largest cap ever published)", peak)
	}
	close(stop)
	applyWG.Wait()
	readerWG.Wait()

	if st := r.Server("a").Stats(); st.Running != 0 || st.Queued != 0 {
		t.Fatalf("server stats after drain = %+v", st)
	}
	if st := r.Global().Stats(); st.Running != 0 || st.Queued != 0 {
		t.Fatalf("global stats after drain = %+v", st)
	}
}

func TestRegistryAcquireAcquiresServerBeforeGlobal(t *testing.T) {
	r := NewRegistry()
	r.Apply(Limits{Max: 1, QueueSize: 0, QueueTimeout: time.Second},
		map[string]Limits{"a": {Max: 1, QueueSize: 4, QueueTimeout: time.Second}})

	rel, err := r.Acquire(context.Background(), "a")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if got := r.Global().Stats().Running; got != 1 {
		t.Fatalf("global Running = %d, want 1", got)
	}
	if got := r.Server("a").Stats().Running; got != 1 {
		t.Fatalf("server Running = %d, want 1", got)
	}
	rel()
	if got := r.Global().Stats().Running; got != 0 {
		t.Fatalf("global Running after release = %d, want 0", got)
	}
	if got := r.Server("a").Stats().Running; got != 0 {
		t.Fatalf("server Running after release = %d, want 0", got)
	}
}

func TestRegistryAcquireReleasesServerSlotWhenGlobalSheds(t *testing.T) {
	r := NewRegistry()
	r.Apply(Limits{Max: 1, QueueSize: 0, QueueTimeout: time.Second},
		map[string]Limits{"a": {Max: 4, QueueSize: 4, QueueTimeout: time.Second}, "b": {Max: 4, QueueSize: 4, QueueTimeout: time.Second}})

	relA, err := r.Acquire(context.Background(), "a")
	if err != nil {
		t.Fatalf("acquire a: %v", err)
	}
	defer relA()

	_, err = r.Acquire(context.Background(), "b")
	if !errors.Is(err, ErrQueueFull) {
		t.Fatalf("expected global shed, got %v", err)
	}
	var le *LimitError
	if !errors.As(err, &le) || le.Scope != ScopeGlobal {
		t.Fatalf("expected a global-scope LimitError, got %v", err)
	}
	if got := r.Server("b").Stats().Running; got != 0 {
		t.Fatalf("server b Running = %d, want 0 (the per-server slot must be released when global sheds)", got)
	}
}

func TestRegistryGlobalScopeMessageDoesNotBlameServer(t *testing.T) {
	err := &LimitError{Scope: ScopeGlobal, Reason: ReasonQueueFull, Limit: 4, RetryAfter: time.Second}
	msg := err.Error()
	if strings.Contains(msg, "server") {
		t.Fatalf("global-scope message must not blame a server: %q", msg)
	}
	if !strings.Contains(msg, "retry") {
		t.Fatalf("shed message must advise retrying: %q", msg)
	}

	srvErr := &LimitError{Scope: ScopeServer, Server: "github", Reason: ReasonQueueTimeout, Limit: 2, RetryAfter: 30 * time.Second}
	if !strings.Contains(srvErr.Error(), "github") {
		t.Fatalf("server-scope message must name the server: %q", srvErr.Error())
	}
}

func TestRegistryConcurrentApplyAndAcquire(t *testing.T) {
	r := NewRegistry()
	r.Apply(Limits{Max: 4, QueueSize: 16, QueueTimeout: 5 * time.Second},
		map[string]Limits{"a": {Max: 2, QueueSize: 16, QueueTimeout: 5 * time.Second}})

	stop := make(chan struct{})
	var applyWG sync.WaitGroup
	applyWG.Add(1)
	go func() {
		defer applyWG.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			maxN := 1 + i%4
			r.Apply(Limits{Max: maxN, QueueSize: 16, QueueTimeout: 5 * time.Second},
				map[string]Limits{"a": {Max: maxN, QueueSize: 16, QueueTimeout: 5 * time.Second}})
			time.Sleep(time.Millisecond)
		}
	}()

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rel, err := r.Acquire(context.Background(), "a")
			if err != nil {
				if !errors.Is(err, ErrQueueFull) && !errors.Is(err, ErrQueueTimeout) {
					t.Errorf("unexpected error: %v", err)
				}
				return
			}
			time.Sleep(time.Millisecond)
			rel()
		}()
	}
	wg.Wait()
	close(stop)
	applyWG.Wait()

	if st := r.Server("a").Stats(); st.Running != 0 || st.Queued != 0 {
		t.Fatalf("server stats after drain = %+v", st)
	}
	if st := r.Global().Stats(); st.Running != 0 || st.Queued != 0 {
		t.Fatalf("global stats after drain = %+v", st)
	}
}

func TestRegistryDisabledScopeKeepsInstanceForOccupancySharing(t *testing.T) {
	r := NewRegistry()
	r.Apply(Limits{}, map[string]Limits{"a": {Max: 2, QueueSize: 2, QueueTimeout: time.Second}})
	a := r.Server("a")
	rel, err := a.Acquire(context.Background(), deadlineIn(time.Second))
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}

	// Limit disabled at runtime: the instance stays so a later re-enable sees
	// the still-running call in its occupancy.
	r.Apply(Limits{}, map[string]Limits{"a": {Max: 0}})
	if r.Server("a") != a {
		t.Fatal("disabling a limit must not discard the instance while calls are running")
	}
	if got := a.Stats().Running; got != 1 {
		t.Fatalf("Running = %d, want 1", got)
	}
	// Disabled = pure passthrough.
	rel2, err := a.Acquire(context.Background(), deadlineIn(time.Second))
	if err != nil {
		t.Fatalf("acquire on disabled limiter: %v", err)
	}
	rel2()
	rel()
}

// TestAdmissionIsGovernedByTheGenerationItResolved is the FR-021 atomicity
// regression test, and it reproduces the exact hang the old code allowed.
//
// Limits used to live on the limiter instance and Apply mutated them BEFORE
// publishing the generation. An admission that had already resolved an
// UNCAPPED generation — budget 0, because nothing limits it — then made its
// decision against the freshly mutated cap, found the scope saturated, and
// parked in the queue with NO deadline. It could only be released by the
// caller's context, which for an agent call may never end.
func TestAdmissionIsGovernedByTheGenerationItResolved(t *testing.T) {
	r := NewRegistry()
	r.Apply(Limits{}, map[string]Limits{"s": {}})

	uncapped := r.gen.Load()
	if got := uncapped.servers["s"].budget; got != 0 {
		t.Fatalf("an uncapped generation must have no wait budget, got %v", got)
	}

	// Occupy the scope, so a CAPPED generation would have to queue the call below.
	busy, err := r.Acquire(context.Background(), "s")
	if err != nil {
		t.Fatalf("unlimited acquire: %v", err)
	}
	defer busy()

	// Reload to a cap of 1 while the call above still holds the scope.
	r.Apply(Limits{}, map[string]Limits{"s": {Max: 1, QueueSize: 4, QueueTimeout: 30 * time.Second}})

	done := make(chan error, 1)
	go func() {
		release, aerr := r.acquireIn(uncapped, context.Background(), "s")
		if aerr == nil {
			release()
		}
		done <- aerr
	}()

	select {
	case aerr := <-done:
		if aerr != nil {
			t.Fatalf("an admission resolved against an uncapped generation must be admitted: %v", aerr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("admission queued against a cap its generation never published (it would wait with no deadline)")
	}
}

// TestCappedGenerationAlwaysPublishesAWaitBudget closes the other half of the
// same hazard: whatever the caps came from, a scope that CAN queue always has a
// deadline to queue against, so no admission can wait indefinitely.
func TestCappedGenerationAlwaysPublishesAWaitBudget(t *testing.T) {
	r := NewRegistry()

	// Per-server cap with no queue_timeout published.
	r.Apply(Limits{}, map[string]Limits{"s": {Max: 1, QueueSize: 1}})
	if got := r.QueueBudget("s"); got != defaultQueueBudget {
		t.Fatalf("QueueBudget = %v, want the %v fallback", got, defaultQueueBudget)
	}

	// Global-only cap, including for a server with no scope of its own.
	r.Apply(Limits{Max: 1, QueueSize: 1}, map[string]Limits{"s": {}})
	if got := r.QueueBudget("s"); got != defaultQueueBudget {
		t.Fatalf("QueueBudget with a global cap = %v, want the %v fallback", got, defaultQueueBudget)
	}
	if got := r.QueueBudget("never-configured"); got != defaultQueueBudget {
		t.Fatalf("QueueBudget for an unconfigured server = %v, want the %v fallback", got, defaultQueueBudget)
	}

	// Nothing capped anywhere: there is nothing to wait for.
	r.Apply(Limits{}, map[string]Limits{"s": {}})
	if got := r.QueueBudget("s"); got != 0 {
		t.Fatalf("QueueBudget with no caps = %v, want 0", got)
	}
}

// TestSnapshotHolderCannotAdmitAfterRetirement is the FR-009 tombstone-lifetime
// test. Absence of a scope means "unlimited", so absence must never follow
// retirement: a caller that resolved its client (and its scope) before the
// server was disabled can reach admission arbitrarily later, and pruning the
// drained tombstone in the meantime turned its refusal into a free pass.
func TestSnapshotHolderCannotAdmitAfterRetirement(t *testing.T) {
	r := NewRegistry()
	r.Apply(Limits{}, map[string]Limits{"s": {Max: 2, QueueSize: 2, QueueTimeout: time.Second}})

	// The caller resolves its scope here, then stalls (in production: between
	// the managed client's IsConnected check and acquireAdmission).
	snapshot := r.gen.Load()

	r.RetireServer("s")
	// Reload cycles that used to prune the drained tombstone.
	for i := 0; i < 3; i++ {
		r.Apply(Limits{}, map[string]Limits{})
	}

	// The stalled caller finally admits — against the generation it resolved...
	if _, err := r.acquireIn(snapshot, context.Background(), "s"); !errors.Is(err, ErrServerUnavailable) {
		t.Fatalf("snapshot-holding admission after retirement: %v, want ErrServerUnavailable", err)
	}
	// ...and a fresh caller against the current one.
	if _, err := r.Acquire(context.Background(), "s"); !errors.Is(err, ErrServerUnavailable) {
		t.Fatalf("admission after retirement: %v, want ErrServerUnavailable", err)
	}
	if r.gen.Load().servers["s"] == nil {
		t.Fatal("the tombstone must survive every reload: absence would read as unlimited")
	}
	if _, ok := r.ServerStats()["s"]; ok {
		t.Fatal("a tombstone must not report occupancy")
	}
}
