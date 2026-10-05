package oauth

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Spec 113 FR-014: the CLI in-memory store (no BBolt) serializes refresh
// through the same coordinator, keyed by server name.
func TestGlobalMemoryTokenStore_BoundRefreshIsSerialized(t *testing.T) {
	as := newRotatingAS(t, "rt-0")
	m := &TokenStoreManager{stores: map[string]client.TokenStore{}, completedOAuth: map[string]time.Time{}, logger: globalTokenStoreManager.logger}
	name := uniqueServerName(t)
	store := m.GetOrCreateTokenStore(name)
	require.NoError(t, store.SaveToken(context.Background(), &client.Token{
		AccessToken: "at-0", RefreshToken: "rt-0", TokenType: "Bearer", ExpiresAt: time.Now().Add(-time.Minute),
	}))
	require.True(t, BindRefresher(store, RefreshBinding{Refresh: asRefreshFunc(as, store)}))

	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make([]error, 20)
	toks := make([]*client.Token, 20)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			toks[i], errs[i] = store.GetToken(context.Background())
		}(i)
	}
	close(start)
	wg.Wait()

	assert.Equal(t, int32(1), as.requests.Load())
	for i := range errs {
		require.NoError(t, errs[i])
		assert.Equal(t, "at-1", toks[i].AccessToken)
		assert.False(t, toks[i].IsExpired())
	}
	assert.Same(t, store, m.GetOrCreateTokenStore(name), "store is shared per server")
}

// Review round 1: the CLI store must know when its token was saved, or a
// short-lived token (shorter than the 5 min grace period) sits inside the
// refresh margin on every request and is refreshed again and again.
func TestGlobalMemoryTokenStore_ShortTokenNotRefreshedEveryRequest(t *testing.T) {
	as := newRotatingAS(t, "rt-0")
	m := &TokenStoreManager{stores: map[string]client.TokenStore{}, completedOAuth: map[string]time.Time{}, logger: globalTokenStoreManager.logger}
	store := m.GetOrCreateTokenStore(uniqueServerName(t))
	require.NoError(t, store.SaveToken(context.Background(), &client.Token{
		AccessToken: "at-0", RefreshToken: "rt-0", TokenType: "Bearer", ExpiresAt: time.Now().Add(time.Minute),
	}))
	require.True(t, BindRefresher(store, RefreshBinding{Refresh: asRefreshFunc(as, store)}))

	for i := 0; i < 5; i++ {
		tok, err := store.GetToken(context.Background())
		require.NoError(t, err)
		assert.Equal(t, "at-0", tok.AccessToken)
	}
	assert.Equal(t, int32(0), as.requests.Load(), "a fresh 60 s token is not refreshed")
}
