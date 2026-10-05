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
