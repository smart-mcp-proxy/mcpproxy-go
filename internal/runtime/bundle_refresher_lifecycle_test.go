package runtime

import (
	"context"
	goruntime "runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/scanner"
)

// Spec 101 US3 tasks B19 / T360: the runtime owns the TPA bundle refresher's
// lifecycle. It starts it with the app context in StartBackgroundInitialization
// and Close stops and JOINS it before the BBolt handle starts closing, then
// clears the scanner slot so a later RefreshBundle falls back to ReloadBundle.

// lifecycleRefresher is a refresher with the telemetry.Service semantics: Start
// blocks until its context ends or Stop is called, Stop joins the loop, and a
// Start after Stop is a no-op.
type lifecycleRefresher struct {
	mu      sync.Mutex
	started chan context.Context
	stopped bool
	stops   int
	done    chan struct{}
}

func newLifecycleRefresher() *lifecycleRefresher {
	return &lifecycleRefresher{started: make(chan context.Context, 4)}
}

func (f *lifecycleRefresher) Start(ctx context.Context) {
	f.mu.Lock()
	if f.stopped || f.done != nil {
		f.mu.Unlock()
		return
	}
	f.done = make(chan struct{})
	done := f.done
	f.mu.Unlock()
	f.started <- ctx
	defer close(done)
	<-ctx.Done()
}

func (f *lifecycleRefresher) Stop() {
	f.mu.Lock()
	f.stopped = true
	f.stops++
	done := f.done
	f.mu.Unlock()
	if done != nil {
		<-done
	}
}

// newRefresherRuntime builds a runtime and returns it with a once-guarded Close
// (Runtime.Close is not idempotent: the cache manager closes a channel twice),
// which the test cleanup also calls.
func newRefresherRuntime(t *testing.T) (*Runtime, func() error) {
	t.Helper()
	cfg := &config.Config{DataDir: t.TempDir(), Listen: "127.0.0.1:0"}
	rt, err := New(cfg, "", zap.NewNop())
	require.NoError(t, err)
	var (
		once sync.Once
		cerr error
	)
	closeOnce := func() error {
		once.Do(func() { cerr = rt.Close() })
		return cerr
	}
	t.Cleanup(func() { _ = closeOnce() })
	return rt, closeOnce
}

func TestBundleRefresher_StartedWithTheAppContextInBackgroundInitialization(t *testing.T) {
	rt, _ := newRefresherRuntime(t)
	fake := newLifecycleRefresher()
	rt.bundleRefresher = fake

	rt.StartBackgroundInitialization()

	select {
	case ctx := <-fake.started:
		assert.Equal(t, rt.AppContext(), ctx, "the refresher runs under the runtime's app context so appCancel ends it")
	case <-time.After(5 * time.Second):
		t.Fatal("StartBackgroundInitialization never started the refresher")
	}
}

func TestBundleRefresher_CloseStopsAndJoinsBeforeStorageStopAsync(t *testing.T) {
	rt, closeRT := newRefresherRuntime(t)
	fake := newLifecycleRefresher()
	rt.bundleRefresher = fake
	var (
		mu    sync.Mutex
		steps []string
	)
	rt.closeOrderHook = func(step string) {
		mu.Lock()
		defer mu.Unlock()
		steps = append(steps, step)
		if step == "bundle_refresher_stopped" {
			fake.mu.Lock()
			assert.True(t, fake.stopped, "the hook fires only after Stop returned")
			fake.mu.Unlock()
		}
	}
	rt.StartBackgroundInitialization()
	select {
	case <-fake.started:
	case <-time.After(5 * time.Second):
		t.Fatal("StartBackgroundInitialization never started the refresher")
	}

	require.NoError(t, closeRT())

	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, []string{"bundle_refresher_stopped", "storage_stop_async"}, steps,
		"the refresher is joined before the storage manager starts to stop (it writes BBolt state)")
	assert.Equal(t, 1, fake.stops)
}

func TestBundleRefresher_CloseClearsTheScannerSlotOnlyForItsOwnInstance(t *testing.T) {
	t.Cleanup(func() { scanner.SetBundleRefresher(nil) })

	t.Run("own instance is cleared", func(t *testing.T) {
		rt, closeRT := newRefresherRuntime(t)
		own := scanner.NewBundleRefresher(scanner.BundleRefresherOptions{})
		rt.SetBundleRefresher(own)
		scanner.SetBundleRefresher(own)

		require.NoError(t, closeRT())

		assert.Nil(t, scanner.CurrentBundleRefresher(), "Close detaches its refresher so RefreshBundle falls back to ReloadBundle")
	})

	t.Run("a replacement installed by someone else is left alone", func(t *testing.T) {
		rt, closeRT := newRefresherRuntime(t)
		own := scanner.NewBundleRefresher(scanner.BundleRefresherOptions{})
		other := scanner.NewBundleRefresher(scanner.BundleRefresherOptions{})
		rt.SetBundleRefresher(own)
		scanner.SetBundleRefresher(other)

		require.NoError(t, closeRT())

		assert.Same(t, other, scanner.CurrentBundleRefresher())
	})
}

func TestBundleRefresher_SetBundleRefresherNilClearsIt(t *testing.T) {
	rt, _ := newRefresherRuntime(t)
	rt.SetBundleRefresher(scanner.NewBundleRefresher(scanner.BundleRefresherOptions{}))
	rt.SetBundleRefresher(nil)

	rt.mu.RLock()
	defer rt.mu.RUnlock()
	assert.Nil(t, rt.bundleRefresher, "a typed nil pointer must not survive as a non-nil interface")
}

// refresherGoroutines counts goroutines currently executing a BundleRefresher
// method. A whole-process goroutine count is useless here: every Runtime leaves
// unrelated goroutines behind, so the loop is identified by its stack frames.
func refresherGoroutines() int {
	buf := make([]byte, 1<<22)
	buf = buf[:goruntime.Stack(buf, true)]
	n := 0
	for _, g := range strings.Split(string(buf), "\n\n") {
		if strings.Contains(g, "scanner.(*BundleRefresher)") {
			n++
		}
	}
	return n
}

// A Close racing a late Start leaves no refresher goroutine behind: the real
// refresher treats a Start after Stop as a no-op, and Close joins a running one.
func TestBundleRefresher_CloseRacingALateStartLeavesNoGoroutine(t *testing.T) {
	t.Cleanup(func() { scanner.SetBundleRefresher(nil) })

	for i := 0; i < 10; i++ {
		rt, closeRT := newRefresherRuntime(t)
		refresher := scanner.NewBundleRefresher(scanner.BundleRefresherOptions{
			ConfigProvider: func() *config.SecurityConfig { return nil },
		})
		rt.SetBundleRefresher(refresher)

		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); rt.StartBackgroundInitialization() }()
		go func() { defer wg.Done(); _ = closeRT() }()
		wg.Wait()
		_ = closeRT()

		require.Eventually(t, func() bool { return !refresher.Running() && refresherGoroutines() == 0 },
			5*time.Second, 10*time.Millisecond, "iteration %d: the refresher loop must be gone after Close", i)
	}
}
