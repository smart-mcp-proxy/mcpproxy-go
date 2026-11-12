package runtime

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 123 PR-3 (FR-030): the runtime's accessor behind the doctor section
// reads rows, the compaction marker and the backup file at call time, and the
// key id and key-source metadata resolved at startup.
func TestRuntime_OAuthTokenStorageState(t *testing.T) {
	spec123ClearEnv(t)
	dataDir := t.TempDir()

	seed, err := storage.NewManager(dataDir, zap.NewNop().Sugar(), storage.WithOAuthTokenKey(nil))
	require.NoError(t, err)
	require.NoError(t, seed.GetBoltDB().SaveOAuthToken(&storage.OAuthTokenRecord{
		ServerName:  oauth.GenerateServerKey("alpha", "https://alpha.example.invalid/mcp"),
		AccessToken: "SPEC123-STATE-ACCESS",
	}))
	require.NoError(t, seed.Close())

	b64, kid := spec123Key(t)
	cfg := config.DefaultConfig()
	cfg.Listen = "127.0.0.1:0"
	cfg.DataDir = dataDir
	cfg.OAuthTokenEncryptionKey = b64

	rt, err := New(cfg, "", zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { _ = rt.Close() })

	st, err := rt.OAuthTokenStorageState()
	require.NoError(t, err)
	assert.Equal(t, kid, st.KeyID)
	assert.Equal(t, config.KeySourceLiteral, st.KeySource.Kind)
	require.Len(t, st.Rows, 1)
	assert.Equal(t, storage.OAuthRowSealed, st.Rows[0].Class)
	assert.True(t, st.CompactAdvised, "the boot sweep replaced a plaintext row")
	assert.False(t, st.BackupPresent)

	require.NoError(t, os.WriteFile(filepath.Join(dataDir, "config.db.bak"), []byte("x"), 0o600))
	st, err = rt.OAuthTokenStorageState()
	require.NoError(t, err)
	assert.True(t, st.BackupPresent)

	b, err := json.Marshal(st)
	require.NoError(t, err)
	assert.NotContains(t, string(b), b64, "the state never carries the key")
}

func TestRuntime_OAuthTokenStorageState_NoKey(t *testing.T) {
	spec123ClearEnv(t)
	cfg := config.DefaultConfig()
	cfg.Listen = "127.0.0.1:0"
	cfg.DataDir = t.TempDir()
	rt, err := New(cfg, "", zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { _ = rt.Close() })

	st, err := rt.OAuthTokenStorageState()
	require.NoError(t, err)
	assert.Equal(t, "", st.KeyID)
	assert.False(t, st.CompactAdvised)
}
