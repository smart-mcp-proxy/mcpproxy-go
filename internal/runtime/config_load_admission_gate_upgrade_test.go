package runtime

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// RC-UPG-001 (v0.70.0-rc.2 NO-GO): v0.69's restart path re-read the config
// file and wrote its server entry to config.db ungated, so a keyless server
// the gate had quarantined ended up recorded as quarantined:false while v0.69
// itself still held it in memory. On upgrade the gate read that record as
// "already through admission", and auto-baseline then approved every tool.
//
// Being known to config.db is therefore not proof of admission on its own.
// A server that really ran under an older release always has an approval
// baseline (auto-baseline approves a trusted server's tools on first
// discovery); one that never got past quarantine has none.

// seedServer records name in config.db as unquarantined — the shape both a
// vetted server and the RC-UPG-001 stale record share.
func seedServer(t *testing.T, rt *Runtime, name string) {
	t.Helper()
	require.NoError(t, rt.storageManager.SaveUpstreamServer(&config.ServerConfig{
		Name: name, Command: "./x", Protocol: "stdio", Enabled: true, Quarantined: false,
	}))
}

// seedApproval records one tool approval for server.
func seedApproval(t *testing.T, rt *Runtime, server, tool, status, approvedHash string) {
	t.Helper()
	require.NoError(t, rt.storageManager.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: server, ToolName: tool, Status: status,
		ApprovedHash: approvedHash, CurrentHash: "h1",
	}))
}

func TestConfigLoadAdmissionGate_RequarantinesKnownServerWithoutApprovalBaseline(t *testing.T) {
	core, logs := observer.New(zap.InfoLevel)
	rt, cfg, _ := gateEnvAt(t, []map[string]any{
		{"name": "legacy-keyless", "command": "./x", "protocol": "stdio", "enabled": true},
	}, nil, zap.New(core))

	// What v0.69 left behind: known, recorded unquarantined, and only
	// pending tool records from discovery while it was held.
	seedServer(t, rt, "legacy-keyless")
	seedApproval(t, rt, "legacy-keyless", "create_entities", storage.ToolApprovalStatusPending, "")

	require.NoError(t, rt.LoadConfiguredServers(cfg))

	assert.True(t, storedServer(t, rt, "legacy-keyless").Quarantined,
		"a server with no approved tool was never admitted; a stale unquarantined record must not admit it")
	assert.True(t, rt.Config().Servers[0].Quarantined)
	assert.Empty(t, logs.FilterMessageSnippet("predate").All(),
		"a server the gate holds is not also reported as a live pre-fix admission")
	assert.NotEmpty(t, logs.FilterMessageSnippet("no approved tool baseline").All())
}

func TestConfigLoadAdmissionGate_RequarantinesKnownServerWithNoToolRecords(t *testing.T) {
	rt, cfg := gateEnv(t, []map[string]any{
		{"name": "legacy-keyless", "command": "./x", "protocol": "stdio", "enabled": true},
	}, nil)
	seedServer(t, rt, "legacy-keyless")

	require.NoError(t, rt.LoadConfiguredServers(cfg))

	assert.True(t, storedServer(t, rt, "legacy-keyless").Quarantined)
}

func TestConfigLoadAdmissionGate_ApprovalBaselineMeansVetted(t *testing.T) {
	for _, tc := range []struct {
		name, status, approvedHash string
	}{
		{"approved", storage.ToolApprovalStatusApproved, "h1"},
		// A rug-pulled tool is "changed" but was approved before.
		{"changed after approval", storage.ToolApprovalStatusChanged, "h0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt, cfg := gateEnv(t, []map[string]any{
				{"name": "vetted", "command": "./x", "protocol": "stdio", "enabled": true},
			}, nil)
			seedServer(t, rt, "vetted")
			seedApproval(t, rt, "vetted", "read_graph", tc.status, tc.approvedHash)

			require.NoError(t, rt.LoadConfiguredServers(cfg))

			assert.False(t, storedServer(t, rt, "vetted").Quarantined,
				"a server with an approval baseline was admitted before; upgrade safety leaves it live")
		})
	}
}

// The re-quarantine is still only for servers the trust mode would hold.
func TestConfigLoadAdmissionGate_NoBaselineButNotGatedStaysLive(t *testing.T) {
	rt, cfg := gateEnv(t, []map[string]any{
		{"name": "explicit", "command": "./x", "protocol": "stdio", "enabled": true, "quarantined": false},
		{"name": "trusted", "command": "./x", "protocol": "stdio", "enabled": true, "trust_mode": "auto"},
	}, nil)
	seedServer(t, rt, "explicit")
	seedServer(t, rt, "trusted")

	require.NoError(t, rt.LoadConfiguredServers(cfg))

	assert.False(t, storedServer(t, rt, "explicit").Quarantined, "an explicit quarantined:false is obeyed")
	assert.False(t, storedServer(t, rt, "trusted").Quarantined, "trust_mode auto never gates")
}

// The decision persists: the next load takes the "retain recorded
// quarantine" branch, so the server stays held until a user approves it.
func TestConfigLoadAdmissionGate_RequarantineSurvivesRestart(t *testing.T) {
	rt, cfg, path := gateEnvAt(t, []map[string]any{
		{"name": "legacy-keyless", "command": "./x", "protocol": "stdio", "enabled": true},
	}, nil, zap.NewNop())
	seedServer(t, rt, "legacy-keyless")
	require.NoError(t, rt.LoadConfiguredServers(cfg))
	require.True(t, storedServer(t, rt, "legacy-keyless").Quarantined)

	reloaded, err := config.LoadFromFile(path)
	require.NoError(t, err)
	require.NoError(t, rt.LoadConfiguredServers(reloaded))
	assert.True(t, storedServer(t, rt, "legacy-keyless").Quarantined)
}
