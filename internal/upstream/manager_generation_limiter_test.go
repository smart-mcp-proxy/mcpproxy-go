package upstream

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/limiter"
)

// Spec 116 PR3 (Sol round 2): a bound action's limiter publication belongs to
// the generation it validated. A stale action that passed its generation check
// and then lost the CPU between the manager unlock and its limiter publication
// must not retire, or republish obsolete limits over, the limiter a newer
// commit already published.

func limitedGenCfg(name, url string, enabled bool, maxConc int) *config.ServerConfig {
	c := genCfg(name, url)
	c.Enabled = enabled
	c.MaxConcurrentRequests = &maxConc
	return c
}

// pauseFirstLimiterPublish installs the test seam so the FIRST limiter
// publication for name parks until release is closed; every later one runs
// through. reached is closed when the first publication is parked.
func pauseFirstLimiterPublish(t *testing.T, m *Manager, name string) (reached, release chan struct{}) {
	t.Helper()
	reached = make(chan struct{})
	release = make(chan struct{})
	var once sync.Once
	hook := func(n string) {
		if n != name {
			return
		}
		fire := false
		once.Do(func() { fire = true })
		if !fire {
			return
		}
		close(reached)
		<-release
	}
	m.limiterPublishHook.Store(&hook)
	t.Cleanup(func() { m.limiterPublishHook.Store(nil) })
	return reached, release
}

// runNewerCommit runs fn (a newer commit and, where it has one, its worker)
// in a goroutine while the stale action is parked, and gives it a bounded
// chance to finish before returning. Without serialized publication it
// finishes, publishing its limiter, before the stale action resumes. With it,
// the newer commit's registry section waits for the parked publication
// (limiterPubMu is taken before mu is released), so the window elapses and
// the test releases the stale action first; the newer commit then publishes
// last. Either way the final state is asserted after both finished.
func runNewerCommit(t *testing.T, fn func()) (done chan struct{}) {
	t.Helper()
	done = make(chan struct{})
	go func() { defer close(done); fn() }()
	select {
	case <-done:
	case <-time.After(300 * time.Millisecond):
	}
	return done
}

func assertLiveLimiter(t *testing.T, m *Manager, name string, wantMax int) {
	t.Helper()
	lim := m.limiters.Server(name)
	require.NotNil(t, lim, "the newer commit's limiter must stay live")
	assert.Equal(t, wantMax, lim.Limits().Max, "the live limits must be the newer commit's")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	releaseHold, err := m.limiters.Acquire(ctx, name)
	require.NoError(t, err, "admission must succeed for the server the newer commit re-enabled")
	releaseHold()
}

// Disable worker at G parks after its unlock; a re-enable commit at G+1
// installs and publishes; the G worker must not retire G+1's limiter.
func TestManagerGenerations_StaleRemoveDoesNotRetireNewerLimiter(t *testing.T) {
	m := newGenTestManager(t)
	m.limiters = limiter.NewRegistry()
	const name = "s"
	url := "http://127.0.0.1:1/s"

	on1 := limitedGenCfg(name, url, true, 2)
	m.AdvanceGenerations([]GenerationChange{present(name, 1, on1)})
	require.NoError(t, m.AddServerConfigAt(name, on1, 1))
	require.NotNil(t, m.limiters.Server(name))

	off2 := limitedGenCfg(name, url, false, 2)
	m.AdvanceGenerations([]GenerationChange{{Name: name, Gen: 2, Present: true, Reconnect: true, Config: off2}})

	reached, release := pauseFirstLimiterPublish(t, m, name)
	staleDone := make(chan error, 1)
	go func() { staleDone <- m.RemoveServerAt(name, 2) }()
	<-reached

	on3 := limitedGenCfg(name, url, true, 3)
	done := runNewerCommit(t, func() {
		m.AdvanceGenerations([]GenerationChange{{Name: name, Gen: 3, Present: true, Reconnect: true, Config: on3}})
		_ = m.AddServerConfigAt(name, on3, 3)
	})
	close(release)
	require.NoError(t, <-staleDone)
	<-done

	registered(t, m, name)
	assertLiveLimiter(t, m, name, 3)
}

// A replacement at G parks after its unlock; a concurrency-limit commit at G+1
// restamps the new instance and publishes its limit; the G replacement must
// not republish G's limit afterwards.
func TestManagerGenerations_StaleReplaceDoesNotRepublishOldLimits(t *testing.T) {
	m := newGenTestManager(t)
	m.limiters = limiter.NewRegistry()
	const name = "s"
	url := "http://127.0.0.1:1/s"

	cfg1 := limitedGenCfg(name, url, true, 1)
	m.AdvanceGenerations([]GenerationChange{present(name, 1, cfg1)})
	require.NoError(t, m.AddServerConfigAt(name, cfg1, 1))
	inst := registered(t, m, name)

	reached, release := pauseFirstLimiterPublish(t, m, name)
	staleDone := make(chan error, 1)
	go func() {
		_, err := m.ReplaceInstanceAt(name, 1, inst, nil)
		staleDone <- err
	}()
	<-reached

	cfg2 := limitedGenCfg(name, url, true, 5)
	done := runNewerCommit(t, func() {
		m.AdvanceGenerations([]GenerationChange{present(name, 2, cfg2)})
	})
	close(release)
	require.NoError(t, <-staleDone)
	<-done

	assert.Equal(t, int64(2), registered(t, m, name).Generation())
	assertLiveLimiter(t, m, name, 5)
}

// A bound add at G parks after its unlock; a concurrency-limit commit at G+1
// restamps the instance and publishes its limit; the G add must not
// republish G's limit afterwards.
func TestManagerGenerations_StaleAddDoesNotRepublishOverNewerCommit(t *testing.T) {
	m := newGenTestManager(t)
	m.limiters = limiter.NewRegistry()
	const name = "s"
	url := "http://127.0.0.1:1/s"

	cfg1 := limitedGenCfg(name, url, true, 1)
	m.AdvanceGenerations([]GenerationChange{present(name, 1, cfg1)})

	reached, release := pauseFirstLimiterPublish(t, m, name)
	staleDone := make(chan error, 1)
	go func() { staleDone <- m.AddServerConfigAt(name, cfg1, 1) }()
	<-reached

	cfg2 := limitedGenCfg(name, url, true, 4)
	done := runNewerCommit(t, func() {
		m.AdvanceGenerations([]GenerationChange{present(name, 2, cfg2)})
	})
	close(release)
	require.NoError(t, <-staleDone)
	<-done

	assertLiveLimiter(t, m, name, 4)
}
