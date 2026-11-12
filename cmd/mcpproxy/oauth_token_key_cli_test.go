package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/cli"
)

// Spec 123 T038 (US3.1, US3.4): with an unreadable ${file:} key reference,
// commands that never open config.db run normally, and every command that
// opens it exits 4 without touching the database.

type unreadableKeyFixture struct {
	cfgPath string
	dataDir string
	dbPath  string
	keyPath string
	before  []byte
}

func newUnreadableKeyFixture(t *testing.T) unreadableKeyFixture {
	t.Helper()
	t.Setenv(config.EnvOAuthTokenKey, "")
	t.Setenv(config.EnvOAuthTokenKeyFile, "")
	dataDir := t.TempDir()

	seed, err := storage.NewManager(dataDir, zap.NewNop().Sugar(), storage.WithOAuthTokenKey(nil))
	require.NoError(t, err)
	require.NoError(t, seed.GetBoltDB().SaveOAuthToken(&storage.OAuthTokenRecord{ServerName: "oauth-srv", AccessToken: "SPEC123-PLAIN"}))
	require.NoError(t, seed.Close())
	dbPath := filepath.Join(dataDir, "config.db")
	before, err := os.ReadFile(dbPath)
	require.NoError(t, err)

	keyPath := filepath.Join(t.TempDir(), "unmounted", "oauth-key")
	cfg := config.DefaultConfig()
	cfg.Listen = "127.0.0.1:0"
	cfg.DataDir = dataDir
	cfg.OAuthTokenEncryptionKey = "${file:" + keyPath + "}"
	cfg.Servers = []*config.ServerConfig{{
		Name: "oauth-srv", URL: "http://127.0.0.1:1/mcp", Protocol: "http", Enabled: true,
	}}
	cfgPath := filepath.Join(dataDir, "mcp_config.json")
	require.NoError(t, config.SaveConfig(cfg, cfgPath))
	return unreadableKeyFixture{cfgPath: cfgPath, dataDir: dataDir, dbPath: dbPath, keyPath: keyPath, before: before}
}

func (f unreadableKeyFixture) assertDBUnchanged(t *testing.T) {
	t.Helper()
	after, err := os.ReadFile(f.dbPath)
	require.NoError(t, err)
	assert.Equal(t, f.before, after, "config.db must be untouched")
}

func assertOAuthKeyExit4(t *testing.T, f unreadableKeyFixture, err error) {
	t.Helper()
	require.Error(t, err)
	assert.Equal(t, ExitCodeConfigError, classifyError(err), "got %v", err)
	assert.NotContains(t, err.Error(), f.keyPath)
	f.assertDBUnchanged(t)
}

func TestOAuthTokenKey_NonStorageCommandsIgnoreUnreadableKey(t *testing.T) {
	f := newUnreadableKeyFixture(t)

	// upstream list (daemonless path): config load + render, no storage.
	cfg, err := loadCLIConfig(f.cfgPath)
	require.NoError(t, err, "a config load must check only the reference syntax")
	prevFormat := globalOutputFormat
	globalOutputFormat = "table"
	t.Cleanup(func() { globalOutputFormat = prevFormat })
	stdout := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w
	runErr := runUpstreamListFromConfig(cfg)
	_ = w.Close()
	os.Stdout = stdout
	_, _ = io.Copy(io.Discard, r)
	require.NoError(t, runErr)
	assert.Equal(t, ExitCodeSuccess, classifyError(runErr))

	// doctor: its config load is the only local step before the socket call.
	prevDoctor := doctorConfigPath
	doctorConfigPath = f.cfgPath
	t.Cleanup(func() { doctorConfigPath = prevDoctor })
	_, err = loadDoctorConfig()
	require.NoError(t, err)

	f.assertDBUnchanged(t)
}

func TestOAuthTokenKey_StorageCommandsExit4OnUnreadableKey(t *testing.T) {
	f := newUnreadableKeyFixture(t)
	cfg, err := loadCLIConfig(f.cfgPath)
	require.NoError(t, err)

	t.Run("openStorageForCLI", func(t *testing.T) {
		mgr, err := openStorageForCLI(cfg, zap.NewNop().Sugar())
		if mgr != nil {
			_ = mgr.Close()
		}
		assertOAuthKeyExit4(t, f, err)
	})
	t.Run("call standalone", func(t *testing.T) {
		err := runCallToolVariantStandalone(context.Background(), "call_tool_read", map[string]interface{}{"name": "oauth-srv:x"}, cfg)
		assertOAuthKeyExit4(t, f, err)
	})
	t.Run("auth login client", func(t *testing.T) {
		c, err := cli.NewClient("oauth-srv", cfg, "error")
		if c != nil {
			_ = c.Close()
		}
		assertOAuthKeyExit4(t, f, err)
	})
}
