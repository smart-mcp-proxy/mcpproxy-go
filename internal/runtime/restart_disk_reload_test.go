package runtime

import (
	"os"
	"path/filepath"
	"testing"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRestartServer_PicksUpDiskEnvChanges reproduces issue #467: editing
// env in mcp_config.json and then calling `mcpproxy upstream restart` (which
// flows into Runtime.RestartServer) must propagate the new env to the
// running upstream. Pre-fix, RestartServer read from BoltDB only — and
// BoltDB never sees the file edit because there's no auto file-watcher —
// so the restart silently replayed the stale env.
//
// We verify the new behavior at the storage layer: after RestartServer,
// the BoltDB record for the server reflects the disk env. That guarantees
// the subsequent upstreamManager.AddServer call (which itself diffs by
// env / headers) sees the new value.
func TestRestartServer_PicksUpDiskEnvChanges(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "mcp_config.json")

	initialCfg := config.DefaultConfig()
	initialCfg.Listen = "127.0.0.1:0"
	initialCfg.DataDir = tmpDir
	initialCfg.Servers = []*config.ServerConfig{
		{
			Name:     "obsidian-pilot",
			Command:  "uvx",
			Args:     []string{"obsidianpilot"},
			Protocol: "stdio",
			Enabled:  true,
			Env:      map[string]string{"OBSIDIAN_VAULT_PATH": "/old/path"},
		},
	}
	require.NoError(t, config.SaveConfig(initialCfg, cfgPath))

	rt, err := New(initialCfg, cfgPath, zap.NewNop())
	require.NoError(t, err)
	defer func() { _ = rt.Close() }()

	// Seed BoltDB with the initial config so RestartServer's lookup succeeds.
	require.NoError(t, rt.storageManager.SaveUpstreamServer(initialCfg.Servers[0]))

	// Simulate the user's repro: edit the file on disk to change env, but do
	// NOT call ApplyConfig / ReloadConfiguration. BoltDB stays stale by
	// design — that's the bug condition.
	editedCfg := config.DefaultConfig()
	editedCfg.Listen = initialCfg.Listen
	editedCfg.DataDir = tmpDir
	editedCfg.Servers = []*config.ServerConfig{
		{
			Name:     "obsidian-pilot",
			Command:  "uvx",
			Args:     []string{"obsidianpilot"},
			Protocol: "stdio",
			Enabled:  true,
			Env:      map[string]string{"OBSIDIAN_VAULT_PATH": "/new/path"},
		},
	}
	require.NoError(t, config.SaveConfig(editedCfg, cfgPath))

	// Sanity: BoltDB still has old env at this point.
	beforeServers, err := rt.storageManager.ListUpstreamServers()
	require.NoError(t, err)
	require.Len(t, beforeServers, 1)
	assert.Equal(t, "/old/path", beforeServers[0].Env["OBSIDIAN_VAULT_PATH"],
		"precondition: BoltDB should still have stale env before restart")

	// Trigger restart. The upstream client doesn't actually exist (we never
	// connected), so RestartServer takes the "client doesn't exist, recreate"
	// branch; either way, the disk-read + storage persist must run first.
	_ = rt.RestartServer("obsidian-pilot")

	// Post-fix: BoltDB should now mirror the disk env.
	afterServers, err := rt.storageManager.ListUpstreamServers()
	require.NoError(t, err)
	require.Len(t, afterServers, 1)
	assert.Equal(t, "/new/path", afterServers[0].Env["OBSIDIAN_VAULT_PATH"],
		"RestartServer must re-read disk before consulting storage so env edits take effect (#467)")
}

// TestRestartServer_PicksUpDiskHeaderChanges is the http-transport sibling
// of the env test — same gap, same fix surface. Headers and env share the
// "edit-then-restart" UX, so both must behave identically.
func TestRestartServer_PicksUpDiskHeaderChanges(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "mcp_config.json")

	initialCfg := config.DefaultConfig()
	initialCfg.Listen = "127.0.0.1:0"
	initialCfg.DataDir = tmpDir
	initialCfg.Servers = []*config.ServerConfig{
		{
			Name:     "synapbus",
			URL:      "https://hub.synapbus.dev/mcp",
			Protocol: "streamable-http",
			Enabled:  true,
			Headers:  map[string]string{"Authorization": "Bearer old-token"},
		},
	}
	require.NoError(t, config.SaveConfig(initialCfg, cfgPath))

	rt, err := New(initialCfg, cfgPath, zap.NewNop())
	require.NoError(t, err)
	defer func() { _ = rt.Close() }()
	require.NoError(t, rt.storageManager.SaveUpstreamServer(initialCfg.Servers[0]))

	// Edit only on disk.
	editedCfg := config.DefaultConfig()
	editedCfg.Listen = initialCfg.Listen
	editedCfg.DataDir = tmpDir
	editedCfg.Servers = []*config.ServerConfig{
		{
			Name:     "synapbus",
			URL:      "https://hub.synapbus.dev/mcp",
			Protocol: "streamable-http",
			Enabled:  true,
			Headers:  map[string]string{"Authorization": "Bearer new-token"},
		},
	}
	require.NoError(t, config.SaveConfig(editedCfg, cfgPath))

	_ = rt.RestartServer("synapbus")

	after, err := rt.storageManager.ListUpstreamServers()
	require.NoError(t, err)
	require.Len(t, after, 1)
	assert.Equal(t, "Bearer new-token", after[0].Headers["Authorization"],
		"RestartServer must re-read disk so header edits take effect (#467)")
}

// TestRestartServer_FallsBackToStorageWhenDiskMissing covers the path where
// the disk file became unreadable between server creation and a restart
// (truncated, deleted, perms broken). The existing behavior — fall back to
// BoltDB — must still work so a transient disk problem doesn't make every
// restart fail.
func TestRestartServer_FallsBackToStorageWhenDiskMissing(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "mcp_config.json")

	initialCfg := config.DefaultConfig()
	initialCfg.Listen = "127.0.0.1:0"
	initialCfg.DataDir = tmpDir
	initialCfg.Servers = []*config.ServerConfig{
		{
			Name:     "obsidian-pilot",
			Command:  "uvx",
			Args:     []string{"obsidianpilot"},
			Protocol: "stdio",
			Enabled:  true,
			Env:      map[string]string{"OBSIDIAN_VAULT_PATH": "/storage/path"},
		},
	}
	require.NoError(t, config.SaveConfig(initialCfg, cfgPath))

	rt, err := New(initialCfg, cfgPath, zap.NewNop())
	require.NoError(t, err)
	defer func() { _ = rt.Close() }()
	require.NoError(t, rt.storageManager.SaveUpstreamServer(initialCfg.Servers[0]))

	// Wipe the file so the disk-read fails; storage must take over.
	require.NoError(t, writeFile(cfgPath, []byte("{not valid json")))

	// Should not error out — fallback path keeps the lookup working.
	err = rt.RestartServer("obsidian-pilot")
	// Server might or might not error depending on async ordering, but the
	// important guarantee is "no panic, no nil-deref, lookup succeeds".
	_ = err

	after, err := rt.storageManager.ListUpstreamServers()
	require.NoError(t, err)
	require.Len(t, after, 1)
	// Storage value preserved unchanged.
	assert.Equal(t, "/storage/path", after[0].Env["OBSIDIAN_VAULT_PATH"])
}

// writeFile overwrites a file in place — a tiny shim so tests don't need to
// pull os everywhere.
func writeFile(path string, content []byte) error {
	return os.WriteFile(path, content, 0o600)
}
