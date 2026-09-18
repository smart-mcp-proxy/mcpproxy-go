package appctx

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.etcd.io/bbolt"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/cache"
)

// Spec 105 FR-002 (critique round 2, finding 7): CacheManagerAdapter.Set was
// the last production writer still going through the unstamped Store, so
// anything written through the appctx CacheManager interface landed as
// LEGACY provenance — refused for every read_cache caller and invalidated by
// the first probe of its key. The adapter writes on the proxy's own behalf
// and reads back through the ungated Get, which is the internal kind: kept,
// never redeemable through read_cache.
func TestCacheManagerAdapter_SetStampsInternal(t *testing.T) {
	db, err := bbolt.Open(filepath.Join(t.TempDir(), "cache.db"), 0644, &bbolt.Options{Timeout: time.Second})
	require.NoError(t, err)
	defer db.Close()
	base, err := cache.NewManager(db, zap.NewNop())
	require.NoError(t, err)
	defer base.Close()
	adapter := &CacheManagerAdapter{Manager: base}

	require.NoError(t, adapter.Set("generic:key", "SENTINEL_VALUE", time.Hour))

	rec, ok := base.Peek("generic:key")
	require.True(t, ok)
	require.NotNil(t, rec.Producer, "an unstamped entry is legacy provenance and would be invalidated on first gated read")
	assert.Equal(t, cache.CallerKindInternal, rec.Producer.CallerKind)
	assert.True(t, rec.HasCurrentProvenance())

	// The adapter's own reader still serves it ...
	got, ok := adapter.Get("generic:key")
	require.True(t, ok)
	assert.Equal(t, "SENTINEL_VALUE", got)
	// ... and the gated door refuses it for an administrator WITHOUT evicting.
	_, err = base.GetRecordsAs("generic:key", 0, 10, cache.Authorization{CallerKind: cache.CallerKindAdmin})
	assert.ErrorIs(t, err, cache.ErrInternalEntry)
	_, ok = base.Peek("generic:key")
	assert.True(t, ok, "refusing an internal entry must not evict it")
}
