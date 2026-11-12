package runtime

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.etcd.io/bbolt"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/telemetry"
)

// Spec 123 FR-016 x Spec 080 FR-010: the Spec 080 shutdown marker is armed
// before the OAuth boot sweep runs, so a crash or hang inside the sweep is
// reported as previous_shutdown=crash on the next start instead of carrying
// over the previous run's clean marker.
func TestRuntimeNew_ShutdownMarkerArmedBeforeOAuthBootSweep(t *testing.T) {
	spec123ClearEnv(t)
	dataDir := t.TempDir()

	seed, err := storage.NewManager(dataDir, zap.NewNop().Sugar(), storage.WithOAuthTokenKey(nil))
	require.NoError(t, err)
	require.NoError(t, seed.GetBoltDB().SaveOAuthToken(&storage.OAuthTokenRecord{
		ServerName:  oauth.GenerateServerKey("alpha", "https://alpha.example.invalid/mcp"),
		DisplayName: "alpha", AccessToken: "SPEC123-MARKER-ACCESS", ClientID: "client-alpha",
	}))
	// A previous run that shut down cleanly.
	store := telemetry.NewPreChurnStore()
	_, err = store.ArmShutdownMarker(seed.GetDB())
	require.NoError(t, err)
	require.NoError(t, store.ResolveCleanShutdown(seed.GetDB()))
	require.NoError(t, seed.Close())

	b64, _ := spec123Key(t)
	cfg := config.DefaultConfig()
	cfg.Listen = "127.0.0.1:0"
	cfg.DataDir = dataDir
	cfg.OAuthTokenEncryptionKey = b64

	markerAtSweep := ""
	reached := false
	runtimeNewStageHook = func(stage string, sm *storage.Manager) {
		if stage != runtimeStageOAuthBootSweep {
			return
		}
		reached = true
		require.NoError(t, sm.GetDB().View(func(tx *bbolt.Tx) error {
			if b := tx.Bucket([]byte(telemetry.PreChurnBucketName)); b != nil {
				markerAtSweep = string(b.Get([]byte("shutdown_marker")))
			}
			return nil
		}))
	}
	t.Cleanup(func() { runtimeNewStageHook = nil })

	rt, err := New(cfg, "", zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { _ = rt.Close() })

	require.True(t, reached, "the boot sweep stage was not reached")
	assert.Equal(t, "armed", markerAtSweep,
		"the shutdown marker must be armed before the OAuth boot sweep runs")
}
