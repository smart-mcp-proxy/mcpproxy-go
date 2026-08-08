package limiter

import (
	"context"
	"errors"
	"strings"
	"sync"
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

	release, err := r.Acquire(context.Background(), "anything", deadlineIn(time.Millisecond))
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

func TestRegistryAcquireAcquiresServerBeforeGlobal(t *testing.T) {
	r := NewRegistry()
	r.Apply(Limits{Max: 1, QueueSize: 0, QueueTimeout: time.Second},
		map[string]Limits{"a": {Max: 1, QueueSize: 4, QueueTimeout: time.Second}})

	rel, err := r.Acquire(context.Background(), "a", deadlineIn(time.Second))
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

	relA, err := r.Acquire(context.Background(), "a", deadlineIn(time.Second))
	if err != nil {
		t.Fatalf("acquire a: %v", err)
	}
	defer relA()

	_, err = r.Acquire(context.Background(), "b", deadlineIn(time.Second))
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
			rel, err := r.Acquire(context.Background(), "a", deadlineIn(5*time.Second))
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
