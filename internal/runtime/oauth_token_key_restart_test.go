package runtime

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/tokenseal"
)

// Spec 123 T032 (FR-020, FR-022, FR-023, US3.3, US3.5).

func spec123Key(t *testing.T) (b64, kid string) {
	t.Helper()
	raw := make([]byte, 32)
	_, err := rand.Read(raw)
	require.NoError(t, err)
	b64 = base64.StdEncoding.EncodeToString(raw)
	k, err := tokenseal.ParseKey(b64)
	require.NoError(t, err)
	return b64, k.ID()
}

func spec123ClearEnv(t *testing.T) {
	t.Setenv(config.EnvOAuthTokenKey, "")
	t.Setenv(config.EnvOAuthTokenKeyFile, "")
}

func newOAuthKeyRuntime(t *testing.T) (rt *Runtime, cfgPath, keyFile, kid string) {
	t.Helper()
	spec123ClearEnv(t)
	dataDir := t.TempDir()
	b64, kid := spec123Key(t)
	keyFile = filepath.Join(t.TempDir(), "oauth-key")
	require.NoError(t, os.WriteFile(keyFile, []byte(b64+"\n"), 0o600))

	cfgPath = filepath.Join(dataDir, "mcp_config.json")
	initial := config.DefaultConfig()
	initial.Listen = "127.0.0.1:8080"
	initial.DataDir = dataDir
	initial.OAuthTokenEncryptionKey = "${file:" + keyFile + "}"
	require.NoError(t, config.SaveConfig(initial, cfgPath))

	loaded, err := config.LoadFromFile(cfgPath)
	require.NoError(t, err)
	rt, err = New(loaded, cfgPath, zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { _ = rt.Close() })
	return rt, cfgPath, keyFile, kid
}

func TestOAuthTokenKey_RuntimeResolvesOnceAndKeepsSource(t *testing.T) {
	rt, _, _, kid := newOAuthKeyRuntime(t)
	assert.Equal(t, kid, rt.OAuthTokenKeyID())
	assert.Equal(t, config.KeySourceRefFile, rt.OAuthTokenKeySource().Kind)
	assert.False(t, rt.OAuthTokenKeySource().InDataDir)

	// The storage the runtime opened seals with that key.
	require.NoError(t, rt.storageManager.GetBoltDB().SaveOAuthToken(&storage.OAuthTokenRecord{
		ServerName: "srv", AccessToken: "SPEC123-ACCESS-MARKER", ExpiresAt: time.Now().Add(time.Hour),
	}))
	rows, err := rt.storageManager.ListOAuthTokenRows()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, storage.OAuthRowSealed, rows[0].Class)
	assert.Equal(t, kid, rows[0].RowKID)
}

func TestOAuthTokenKey_EditIsRestartGatedAndSaveKeepsReference(t *testing.T) {
	rt, cfgPath, keyFile, kid := newOAuthKeyRuntime(t)
	originalRef := "${file:" + keyFile + "}"
	// Absolute on every OS (LoadFromFile validates it with filepath.IsAbs,
	// and a POSIX-rooted literal is relative on Windows); never created.
	otherRef := "${file:" + filepath.Join(t.TempDir(), "other-key") + "}"

	onDisk, err := config.LoadFromFile(cfgPath)
	require.NoError(t, err)
	onDisk.OAuthTokenEncryptionKey = otherRef
	require.NoError(t, config.SaveConfig(onDisk, cfgPath))

	result := DetectConfigChanges(rt.Config(), onDisk)
	assert.True(t, result.RequiresRestart)
	assert.Contains(t, result.ChangedFields, "oauth_token_encryption_key")

	require.NoError(t, rt.ReloadConfiguration())
	// The running process keeps the key it started with.
	assert.Equal(t, originalRef, rt.Config().OAuthTokenEncryptionKey)
	assert.Equal(t, kid, rt.OAuthTokenKeyID())

	// A save writes the configured reference, never a resolved key.
	require.NoError(t, rt.SaveConfiguration())
	raw, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	// Decode instead of matching text: JSON escapes the backslashes of a
	// Windows path.
	var saved struct {
		Key string `json:"oauth_token_encryption_key"`
	}
	require.NoError(t, json.Unmarshal(raw, &saved))
	assert.Equal(t, otherRef, saved.Key)
	key, err := os.ReadFile(keyFile)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), string(key[:len(key)-1]))
}

func TestOAuthTokenKey_KeyFileRemovedAfterStartUnrelatedReloadApplies(t *testing.T) {
	rt, cfgPath, keyFile, kid := newOAuthKeyRuntime(t)
	require.NoError(t, os.Remove(keyFile))

	onDisk, err := config.LoadFromFile(cfgPath)
	require.NoError(t, err, "a load must not read the key file")
	onDisk.ToolsLimit = 37
	require.NoError(t, config.SaveConfig(onDisk, cfgPath))

	require.NoError(t, rt.ReloadConfiguration())
	assert.Equal(t, 37, rt.Config().ToolsLimit)
	assert.Equal(t, kid, rt.OAuthTokenKeyID())
}

// FR-022: an unresolvable reference stops runtime.New with exit 4 before any
// byte of config.db is written.
func TestOAuthTokenKey_UnresolvableStopsRuntimeBeforeOpeningStorage(t *testing.T) {
	spec123ClearEnv(t)
	dataDir := t.TempDir()
	seed, err := storage.NewManager(dataDir, zap.NewNop().Sugar(), storage.WithOAuthTokenKey(nil))
	require.NoError(t, err)
	require.NoError(t, seed.GetBoltDB().SaveOAuthToken(&storage.OAuthTokenRecord{ServerName: "srv", AccessToken: "plain"}))
	require.NoError(t, seed.Close())
	dbPath := filepath.Join(dataDir, "config.db")
	before, err := os.ReadFile(dbPath)
	require.NoError(t, err)

	missing := filepath.Join(t.TempDir(), "absent-key")
	cfg := config.DefaultConfig()
	cfg.DataDir = dataDir
	cfg.OAuthTokenEncryptionKey = "${file:" + missing + "}"

	rt, err := New(cfg, "", zap.NewNop())
	if rt != nil {
		_ = rt.Close()
	}
	require.Error(t, err)
	var se *config.StartupError
	require.True(t, errors.As(err, &se), "got %T: %v", err, err)
	assert.Equal(t, 4, se.ExitCode)
	assert.NotContains(t, err.Error(), missing)

	after, err := os.ReadFile(dbPath)
	require.NoError(t, err)
	assert.Equal(t, before, after, "config.db must be untouched")
}
