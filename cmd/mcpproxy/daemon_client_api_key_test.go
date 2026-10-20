package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

func TestResolveAPIKey_Precedence(t *testing.T) {
	keyFile := filepath.Join(t.TempDir(), "key")
	require.NoError(t, os.WriteFile(keyFile, []byte("file-key\n"), 0o600))
	cfg := &config.Config{APIKey: "config-key"}

	t.Run("config only", func(t *testing.T) {
		t.Setenv(config.EnvAPIKey, "")
		require.NoError(t, os.Unsetenv(config.EnvAPIKey))
		t.Setenv(config.EnvAPIKeyFile, "")
		assert.Equal(t, "config-key", resolveAPIKey(cfg))
	})
	t.Run("env beats config", func(t *testing.T) {
		t.Setenv(config.EnvAPIKey, "env-key")
		t.Setenv(config.EnvAPIKeyFile, "")
		assert.Equal(t, "env-key", resolveAPIKey(cfg))
	})
	t.Run("file beats env and config, as the daemon resolves it", func(t *testing.T) {
		t.Setenv(config.EnvAPIKey, "env-key")
		t.Setenv(config.EnvAPIKeyFile, keyFile)
		assert.Equal(t, "file-key", resolveAPIKey(cfg))
	})
	t.Run("an unusable file gives no key rather than a wrong one", func(t *testing.T) {
		t.Setenv(config.EnvAPIKey, "env-key")
		t.Setenv(config.EnvAPIKeyFile, filepath.Join(t.TempDir(), "missing"))
		assert.Empty(t, resolveAPIKey(cfg))
	})
}
