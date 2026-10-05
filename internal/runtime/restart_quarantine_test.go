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
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// A server written into mcp_config.json with no `quarantined` key is held by
// the config-load admission gate (#937). RestartServer re-reads the file from
// disk (#467); it must run that entry through the same gate instead of writing
// the raw, un-gated entry over the recorded quarantine in config.db.

func restartQuarantineEnv(t *testing.T, extra map[string]any, servers ...map[string]any) (*Runtime, string) {
	t.Helper()
	rt, _, cfgPath := gateEnvAt(t, servers, extra, zap.NewNop())
	require.NoError(t, rt.LoadConfiguredServers(nil))
	return rt, cfgPath
}

func silentServer(name string) map[string]any {
	return map[string]any{"name": name, "command": "true", "protocol": "stdio", "enabled": true}
}

// rewriteServersOnDisk replaces the mcpServers array of the config file.
func rewriteServersOnDisk(t *testing.T, cfgPath string, servers ...map[string]any) {
	t.Helper()
	raw, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	var doc map[string]any
	require.NoError(t, json.Unmarshal(raw, &doc))
	list := make([]any, 0, len(servers))
	for _, s := range servers {
		list = append(list, s)
	}
	doc["mcpServers"] = list
	out, err := json.Marshal(doc)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(cfgPath, out, 0600))
}

func TestRestartServer_KeepsImplicitQuarantine(t *testing.T) {
	rt, cfgPath := restartQuarantineEnv(t, nil, silentServer("victim"), silentServer("approved"))
	require.True(t, storedServer(t, rt, "victim").Quarantined, "precondition: gate quarantined the first-seen server")

	_ = rt.RestartServer("victim")

	assert.True(t, storedServer(t, rt, "victim").Quarantined,
		"RestartServer must not write the raw config file entry (quarantined=false) over the recorded quarantine")

	// Approving a DIFFERENT server must not change the victim.
	require.NoError(t, rt.QuarantineServer("approved", false))
	assert.True(t, storedServer(t, rt, "victim").Quarantined, "approving another server un-quarantined 'victim'")
	assert.False(t, storedServer(t, rt, "approved").Quarantined)

	for _, sc := range rt.Config().Servers {
		if sc.Name == "victim" {
			assert.True(t, sc.Quarantined, "published config must still hold victim")
		}
	}

	// The file written by SaveConfiguration must carry the quarantine.
	data, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	var raw struct {
		Servers []map[string]any `json:"mcpServers"`
	}
	require.NoError(t, json.Unmarshal(data, &raw))
	found := false
	for _, s := range raw.Servers {
		if s["name"] == "victim" {
			found = true
			assert.Equal(t, true, s["quarantined"], "config file must record victim as quarantined")
		}
	}
	assert.True(t, found)
}

func TestRestartServer_KeepsImplicitQuarantineAcrossReload(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "mcp_config.json")
	fileCfg := config.DefaultConfig()
	fileCfg.Listen = "127.0.0.1:0"
	fileCfg.DataDir = dir
	fileCfg.Servers = []*config.ServerConfig{
		{Name: "victim", Command: "true", Protocol: "stdio", Enabled: true},
		{Name: "approved", Command: "true", Protocol: "stdio", Enabled: true},
	}
	require.NoError(t, config.SaveConfig(fileCfg, cfgPath))

	boot := func() *Runtime {
		cfg, err := config.LoadFromFile(cfgPath)
		require.NoError(t, err)
		rt, err := New(cfg, cfgPath, zap.NewNop())
		require.NoError(t, err)
		require.NoError(t, rt.LoadConfiguredServers(nil))
		return rt
	}

	rt := boot()
	require.True(t, storedServer(t, rt, "victim").Quarantined)
	_ = rt.RestartServer("victim")
	require.NoError(t, rt.QuarantineServer("approved", false))
	require.NoError(t, rt.Close())

	// Simulate a reboot: fresh runtime over the same data dir and config file.
	rt2 := boot()
	t.Cleanup(func() { _ = rt2.Close() })

	assert.True(t, storedServer(t, rt2, "victim").Quarantined, "victim must stay quarantined after a reboot")
	approvals, err := rt2.ListToolApprovals("victim")
	require.NoError(t, err)
	for _, a := range approvals {
		assert.NotEqual(t, storage.ToolApprovalStatusApproved, a.Status, "victim tools must not be baseline-approved")
	}
}

func TestRestartServer_FirstSeenServerIsGated(t *testing.T) {
	rt, cfgPath := restartQuarantineEnv(t, nil, silentServer("existing"))

	// Hand-add a server to the file; the watcher has not fired, so config.db
	// has never seen it.
	rewriteServersOnDisk(t, cfgPath, silentServer("existing"), silentServer("fresh"))

	_ = rt.RestartServer("fresh")

	assert.True(t, storedServer(t, rt, "fresh").Quarantined, "first-seen server restarted from disk must be gated")
}

func TestRestartServer_ExplicitFalseOnDiskIsHonoured(t *testing.T) {
	rt, cfgPath := restartQuarantineEnv(t, nil, silentServer("victim"))
	require.True(t, storedServer(t, rt, "victim").Quarantined)

	// The operator states quarantined:false by hand.
	explicit := silentServer("victim")
	explicit["quarantined"] = false
	rewriteServersOnDisk(t, cfgPath, explicit)

	_ = rt.RestartServer("victim")

	assert.False(t, storedServer(t, rt, "victim").Quarantined, "an explicit operator false on disk is obeyed")
}

func TestRestartServer_TrustModeAutoNotGated(t *testing.T) {
	auto := func(name string) map[string]any {
		s := silentServer(name)
		s["trust_mode"] = "auto"
		return s
	}
	rt, cfgPath := restartQuarantineEnv(t, nil, auto("existing"))
	require.False(t, storedServer(t, rt, "existing").Quarantined, "precondition: auto trust mode does not gate")

	rewriteServersOnDisk(t, cfgPath, auto("existing"), auto("fresh"))

	_ = rt.RestartServer("fresh")

	assert.False(t, storedServer(t, rt, "fresh").Quarantined, "auto trust mode must not over-quarantine on restart")
}

func TestRestartAdmission_StorageUnreadableFailsClosed(t *testing.T) {
	rt, cfgPath := restartQuarantineEnv(t, nil, silentServer("victim"))
	require.True(t, storedServer(t, rt, "victim").Quarantined)

	diskCfg, err := config.LoadFromFile(cfgPath)
	require.NoError(t, err)
	disk := diskCfg.Servers[0]
	require.False(t, disk.Quarantined)

	// Storage unreadable: the published (gated) entry says quarantined.
	got, persist := rt.admitServerForRestart(diskCfg, disk, nil, false)
	assert.True(t, got.Quarantined, "fail closed: inherit the published quarantine")
	assert.False(t, persist, "never write to an unreadable storage")
	assert.False(t, disk.Quarantined, "the caller's struct must not be mutated")

	// Absent from the published config: fall back to the trust-mode default.
	other := &config.ServerConfig{Name: "unknown-to-runtime", Command: "true", Protocol: "stdio", Enabled: true}
	got, persist = rt.admitServerForRestart(diskCfg, other, nil, false)
	assert.True(t, got.Quarantined, "fail closed: manual trust mode default")
	assert.False(t, persist)

	// An explicit operator false is still obeyed.
	stated := &config.ServerConfig{Name: "victim", Command: "true", Protocol: "stdio", Enabled: true}
	stated.MarkQuarantineExplicitlySet(true)
	got, _ = rt.admitServerForRestart(diskCfg, stated, nil, false)
	assert.False(t, got.Quarantined, "explicit false is obeyed even when storage is unreadable")
}
