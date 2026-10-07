package oauth

import (
	"errors"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (f *fakeClock) now() time.Time { f.mu.Lock(); defer f.mu.Unlock(); return f.t }
func (f *fakeClock) advance(d time.Duration) {
	f.mu.Lock()
	f.t = f.t.Add(d)
	f.mu.Unlock()
}

func TestDiscoveryCache_SuccessTTLAndFailureTTL(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	c := newDiscoveryCache(clk.now)
	key := makeDiscoveryKey("k", "https://s.example/mcp", discoveryOverrides{})

	var calls int32
	ok := func() (string, error) { atomic.AddInt32(&calls, 1); return "v", nil }
	for i := 0; i < 5; i++ {
		v, err := cachedDiscover(c, key, "https://s.example/mcp", ok)
		require.NoError(t, err)
		assert.Equal(t, "v", v)
	}
	assert.EqualValues(t, 1, calls, "5 lookups -> 1 fetch")
	clk.advance(59 * time.Minute)
	_, _ = cachedDiscover(c, key, "https://s.example/mcp", ok)
	assert.EqualValues(t, 1, calls)
	clk.advance(2 * time.Minute)
	_, _ = cachedDiscover(c, key, "https://s.example/mcp", ok)
	assert.EqualValues(t, 2, calls, "refetched after the 1h success TTL")

	// failures are cached briefly (30s), then retried
	fkey := makeDiscoveryKey("f", "https://s.example/mcp", discoveryOverrides{})
	var fcalls int32
	bad := func() (string, error) { atomic.AddInt32(&fcalls, 1); return "", errors.New("boom") }
	for i := 0; i < 3; i++ {
		_, err := cachedDiscover(c, fkey, "https://s.example/mcp", bad)
		require.Error(t, err)
	}
	assert.EqualValues(t, 1, fcalls)
	clk.advance(31 * time.Second)
	_, _ = cachedDiscover(c, fkey, "https://s.example/mcp", bad)
	assert.EqualValues(t, 2, fcalls)
}

func TestDiscoveryCache_KeyIncludesOverrides(t *testing.T) {
	a := makeDiscoveryKey("k", "https://s.example/mcp", discoveryOverrides{})
	b := makeDiscoveryKey("k", "https://s.example/mcp", discoveryOverrides{token: "https://idp.example/token"})
	c := makeDiscoveryKey("k", "https://s.example/mcp", discoveryOverrides{metadataURL: "https://idp.example/meta"})
	d := makeDiscoveryKey("k2", "https://s.example/mcp", discoveryOverrides{})
	e := makeDiscoveryKey("k", "https://other.example/mcp", discoveryOverrides{})
	seen := map[discoveryKey]bool{}
	for _, k := range []discoveryKey{a, b, c, d, e} {
		assert.False(t, seen[k], "keys must be distinct")
		seen[k] = true
	}
	assert.Equal(t, a, makeDiscoveryKey("k", "https://s.example/mcp", discoveryOverrides{}))
}

func TestDiscoveryCache_SingleFlight(t *testing.T) {
	c := newDiscoveryCache(time.Now)
	key := makeDiscoveryKey("k", "https://s.example/mcp", discoveryOverrides{})
	var calls int32
	release := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = cachedDiscover(c, key, "https://s.example/mcp", func() (int, error) {
				atomic.AddInt32(&calls, 1)
				<-release
				return 1, nil
			})
		}()
	}
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()
	assert.EqualValues(t, 1, calls)
}

func TestDiscoveryCache_LRUBound(t *testing.T) {
	c := newDiscoveryCache(time.Now)
	for i := 0; i < discoveryCacheMaxEntries+10; i++ {
		key := makeDiscoveryKey("k", "https://s.example/mcp", discoveryOverrides{token: string(rune('a' + i%26)), metadataURL: time.Duration(i).String()})
		_, _ = cachedDiscover(c, key, "https://s.example/mcp", func() (int, error) { return i, nil })
	}
	assert.LessOrEqual(t, c.len(), discoveryCacheMaxEntries)
}

func TestDiscoveryCache_InvalidateOnDifferentResourceMetadataURL(t *testing.T) {
	c := newDiscoveryCache(time.Now)
	srv := "https://s.example/mcp"
	key := makeDiscoveryKey("k", srv, discoveryOverrides{})
	var calls int32
	fn := func() (int, error) { atomic.AddInt32(&calls, 1); return 1, nil }
	_, _ = cachedDiscover(c, key, srv, fn)

	assert.False(t, c.noteResourceMetadataURL(srv, "https://s.example/.well-known/prm"), "first sighting just records")
	_, _ = cachedDiscover(c, key, srv, fn)
	assert.EqualValues(t, 1, calls)
	assert.False(t, c.noteResourceMetadataURL(srv, "https://s.example/.well-known/prm"), "same URL keeps the entry")
	_, _ = cachedDiscover(c, key, srv, fn)
	assert.EqualValues(t, 1, calls)

	assert.True(t, c.noteResourceMetadataURL(srv, "https://s.example/.well-known/prm2"), "different URL invalidates")
	_, _ = cachedDiscover(c, key, srv, fn)
	assert.EqualValues(t, 2, calls)

	// explicit invalidation drops entries for every override combo of the server
	k2 := makeDiscoveryKey("k", srv, discoveryOverrides{token: "https://idp.example/token"})
	_, _ = cachedDiscover(c, k2, srv, fn)
	c.invalidateServer(srv)
	assert.Equal(t, 0, c.len())
}

func TestDiscoveryCache_InFlightFetchDoesNotRepopulateAfterInvalidation(t *testing.T) {
	c := newDiscoveryCache(time.Now)
	srv := "https://s.example/mcp"
	key := makeDiscoveryKey("k", srv, discoveryOverrides{})
	started := make(chan struct{})
	release := make(chan struct{})
	var calls int32
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = cachedDiscover(c, key, srv, func() (string, error) {
			atomic.AddInt32(&calls, 1)
			close(started)
			<-release
			return "stale", nil
		})
	}()
	<-started
	c.invalidateServer(srv)
	// A caller arriving after the invalidation starts a fresh fetch, not joins the stale one.
	v, err := cachedDiscover(c, key, srv, func() (string, error) { atomic.AddInt32(&calls, 1); return "fresh", nil })
	require.NoError(t, err)
	assert.Equal(t, "fresh", v)
	close(release)
	<-done
	v, _ = cachedDiscover(c, key, srv, func() (string, error) { return "unexpected", nil })
	assert.Equal(t, "fresh", v, "the stale in-flight result must not overwrite the fresh entry")
	assert.EqualValues(t, 2, calls)
}

func TestSameOrigin_DefaultPorts(t *testing.T) {
	u := func(s string) *url.URL { x, _ := url.Parse(s); return x }
	assert.True(t, sameOrigin(u("https://idp.example/token"), u("https://idp.example:443/exchange")))
	assert.True(t, sameOrigin(u("http://127.0.0.1/token"), u("http://127.0.0.1:80/x")))
	assert.True(t, sameOrigin(u("https://IDP.example/token"), u("https://idp.example/x")))
	assert.False(t, sameOrigin(u("https://idp.example/token"), u("https://idp.example:8443/x")))
	assert.False(t, sameOrigin(u("https://idp.example/token"), u("http://idp.example/x")))
}

func TestDiscoveryCache_SeedsAreAtomicWithThePrimaryResult(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	c := newDiscoveryCache(clk.now)
	srv := "https://s.example/mcp"
	primary := makeDiscoveryKey("p", srv, discoveryOverrides{})
	seed := makeDiscoveryKey("s", srv, discoveryOverrides{})

	_, err := c.doWith(primary, srv, func() (any, time.Duration, []discoverySeed, error) {
		return 1, 0, []discoverySeed{{key: seed, val: "doc"}}, nil
	})
	require.NoError(t, err)
	assert.Equal(t, 2, c.len())

	// A cache hit never re-seeds or renews the seed's lifetime.
	clk.advance(59 * time.Minute)
	_, _ = c.doWith(primary, srv, func() (any, time.Duration, []discoverySeed, error) {
		t.Fatal("must be a cache hit")
		return nil, 0, nil, nil
	})
	clk.advance(2 * time.Minute)
	c.mu.Lock()
	_, ok, _ := c.lookupLocked(seed)
	c.mu.Unlock()
	assert.False(t, ok, "the seed expires with its primary")

	// A flight invalidated mid-fetch stores neither.
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		_, _ = c.doWith(primary, srv, func() (any, time.Duration, []discoverySeed, error) {
			close(started)
			<-release
			return 2, 0, []discoverySeed{{key: seed, val: "stale"}}, nil
		})
	}()
	<-started
	c.invalidateServer(srv)
	close(release)
	<-done
	assert.Equal(t, 0, c.len(), "an invalidated flight must not store its seeds")
}
