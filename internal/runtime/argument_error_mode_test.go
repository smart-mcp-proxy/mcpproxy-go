package runtime

import (
	"path/filepath"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// RUNTIME-MODE-01: the real two-store lifecycle must keep the stored policy
// while rebuilding JSON, reloading disk edits, and saving an unrelated server.
// Disabled loopback fixtures never connect to an external MCP service.
//
// Spec 116 makes the committed configuration the source of truth and storage
// its projection, so the policy is set through the same committed update path
// production uses: a direct storage write is deliberately re-projected by the
// next commit (STORE-MODE-01 covers the storage door itself).
func TestArgumentErrorModeSaveReload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp_config.json")
	cfg := config.DefaultConfig()
	cfg.DataDir = dir
	cfg.Tokenizer = &config.TokenizerConfig{Enabled: false}
	cfg.Servers = []*config.ServerConfig{
		{Name: "private-upstream", URL: "http://127.0.0.1:1/mcp", Protocol: "streamable-http", Enabled: false},
		{Name: "control", URL: "http://127.0.0.1:1/control", Protocol: "streamable-http", Enabled: false, Headers: map[string]string{"Authorization": "synthetic-control"}},
	}
	require.NoError(t, config.SaveConfig(cfg, path))
	rt, err := New(cfg, path, zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, rt.Close()) })
	require.NoError(t, rt.SaveConfiguration())

	update := func(name string, fn func(sc *config.ServerConfig)) {
		t.Helper()
		_, err := rt.Commit("update_server", func(tx *CommitTx) error {
			return tx.UpdateServer(name, func(cur *config.ServerConfig) error {
				fn(cur)
				return nil
			})
		})
		require.NoError(t, err)
	}

	verify := func(mode string) {
		t.Helper()
		disk, err := config.LoadFromFile(path)
		require.NoError(t, err)
		stored, err := rt.StorageManager().GetUpstreamServer("private-upstream")
		require.NoError(t, err)
		require.Equal(t, mode, stored.ToolArgumentErrorMode)
		for _, state := range []*config.Config{disk, rt.Config(), rt.ConfigSnapshot().Config} {
			require.Len(t, state.Servers, 2)
			found := false
			for _, sc := range state.Servers {
				if sc.Name == "private-upstream" {
					found = true
					require.Equal(t, mode, sc.ToolArgumentErrorMode)
				}
				if sc.Name == "control" {
					require.Empty(t, sc.ToolArgumentErrorMode)
					require.False(t, sc.Enabled)
					require.Equal(t, "synthetic-control", sc.Headers["Authorization"])
				}
			}
			require.True(t, found)
		}
	}

	verify("")
	update("private-upstream", func(cur *config.ServerConfig) { cur.ToolArgumentErrorMode = "redacted" })
	require.NoError(t, rt.SaveConfiguration())
	verify("redacted")
	for _, mode := range []string{"detailed", "redacted"} {
		disk, err := config.LoadFromFile(path)
		require.NoError(t, err)
		for _, sc := range disk.Servers {
			if sc.Name == "private-upstream" {
				sc.ToolArgumentErrorMode = mode
			}
		}
		require.NoError(t, config.SaveConfig(disk, path))
		require.NoError(t, rt.ReloadConfiguration())
		verify(mode)
	}
	update("control", func(cur *config.ServerConfig) { cur.URL = "http://127.0.0.1:1/updated-control" })
	require.NoError(t, rt.SaveConfiguration())
	verify("redacted")
	unchanged, err := rt.StorageManager().GetUpstreamServer("control")
	require.NoError(t, err)
	require.Equal(t, "http://127.0.0.1:1/updated-control", unchanged.URL)
	require.Equal(t, map[string]string{"Authorization": "synthetic-control"}, unchanged.Headers)
}
