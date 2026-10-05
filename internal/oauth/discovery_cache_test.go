package oauth

import (
	"errors"
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
