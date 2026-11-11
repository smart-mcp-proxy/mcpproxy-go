package runtime

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 123 FR-016 (review): when the boot sweep fails at the transaction
// level, runtime.New closes storage and returns an error. No OAuth consumer
// (upstream manager, refresh manager) may be constructed over rows that are
// still plaintext.
func TestRuntimeNew_BootSweepFailureRefusesStartup(t *testing.T) {
	spec123ClearEnv(t)
	dataDir := t.TempDir()

	seed, err := storage.NewManager(dataDir, zap.NewNop().Sugar(), storage.WithOAuthTokenKey(nil))
	require.NoError(t, err)
	require.NoError(t, seed.GetBoltDB().SaveOAuthToken(&storage.OAuthTokenRecord{
		ServerName:  oauth.GenerateServerKey("alpha", "https://alpha.example.invalid/mcp"),
		DisplayName: "alpha", AccessToken: "SPEC123-FAIL-ACCESS", ClientID: "client-alpha",
	}))
	require.NoError(t, seed.Close())

	b64, _ := spec123Key(t)
	cfg := config.DefaultConfig()
	cfg.Listen = "127.0.0.1:0"
	cfg.DataDir = dataDir
	cfg.OAuthTokenEncryptionKey = b64

	var stages []string
	runtimeNewStageHook = func(stage string, sm *storage.Manager) {
		stages = append(stages, stage)
		if stage == runtimeStageOAuthBootSweep {
			// A transaction-level failure: the database goes away under the sweep.
			_ = sm.GetBoltDB().Close()
		}
	}
	t.Cleanup(func() { runtimeNewStageHook = nil })

	rt, err := New(cfg, "", zap.NewNop())
	if rt != nil {
		_ = rt.Close()
	}
	require.Error(t, err, "a failed boot sweep must refuse startup")
	assert.Nil(t, rt)
	assert.True(t, strings.Contains(err.Error(), "OAuth token"), "error names the sweep: %v", err)
	assert.Equal(t, []string{runtimeStageOAuthBootSweep}, stages,
		"no OAuth consumer may be constructed after a failed sweep")
}
