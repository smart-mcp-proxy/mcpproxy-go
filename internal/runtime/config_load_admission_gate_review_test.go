package runtime

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Cross-model review findings on the #937 admission gate.

// P1 — mcpproxy's own config save must not disarm the gate.
//
// `ServerConfig.Quarantined` had no omitempty and SaveConfig is a plain
// MarshalIndent, so any save (e.g. PUT /api/v1/config) stamped
// `"quarantined": false` onto every server. Reading that back set the presence
// bit, the gate skipped admission forever, and the save loop in
// LoadConfiguredServers then wrote false over the config.db quarantine — the
// poisoned server came back live one restart later.
func TestConfigLoadAdmissionGate_SurvivesMcpproxyOwnSave(t *testing.T) {
	rt, cfg, path := gateEnvAt(t, []map[string]any{
		{"name": "poison", "command": "./evil", "protocol": "stdio", "enabled": true},
	}, nil, zap.NewNop())

	// Run 1: the gate quarantines the first-seen server and it is persisted.
	require.NoError(t, rt.LoadConfiguredServers(cfg))
	require.True(t, storedServer(t, rt, "poison").Quarantined)

	// mcpproxy writes the config file itself — exactly what ApplyConfig does
	// BEFORE the gate ever runs on that apply.
	require.NoError(t, config.SaveConfig(rt.Config(), path))

	// Run 2: reload from that self-written file.
	reloaded, err := config.LoadFromFile(path)
	require.NoError(t, err)
	require.NoError(t, rt.LoadConfiguredServers(reloaded))

	assert.True(t, storedServer(t, rt, "poison").Quarantined,
		"a config file mcpproxy wrote itself must not disarm the admission gate")
}

// P1 variant — a server that is BOTH first-seen and only ever described by a
// config mcpproxy saved. Simulates PUT /api/v1/config adding a server whose
// apply required a restart, so the gate never ran in that process.
func TestConfigLoadAdmissionGate_GatesAfterSelfSaveOfNeverAdmittedServer(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp_config.json")
	raw, err := json.Marshal(map[string]any{
		"listen":   "127.0.0.1:0",
		"data_dir": dir,
		"api_key":  "k",
		"mcpServers": []map[string]any{
			{"name": "poison", "command": "./evil", "protocol": "stdio", "enabled": true},
		},
	})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, raw, 0600))

	cfg, err := config.LoadFromFile(path)
	require.NoError(t, err)

	// Disk gets the config before any gating (ApplyConfig saves first, and on
	// the RequiresRestart branch the gate never runs in that process at all).
	require.NoError(t, config.SaveConfig(cfg, path))

	reloaded, err := config.LoadFromFile(path)
	require.NoError(t, err)
	require.False(t, reloaded.Servers[0].QuarantineExplicitlySet(),
		"a save must not fabricate an operator statement")

	rt2, err := New(reloaded, path, zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { _ = rt2.Close() })

	require.NoError(t, rt2.LoadConfiguredServers(reloaded))
	assert.True(t, storedServer(t, rt2, "poison").Quarantined,
		"a never-admitted server must be gated even after mcpproxy rewrote the config file")
}

// P2 — a transient storage read failure must not mass-quarantine everything.
//
// ListUpstreamServers() errors were swallowed and `stored` became empty, so the
// gate saw every configured server as first-seen and walled off the user's
// entire server list — permanently, because the next boot inherits it.
func TestConfigLoadAdmissionGate_SkippedWhenStorageUnreadable(t *testing.T) {
	rt, cfg := gateEnv(t, []map[string]any{
		{"name": "a", "command": "./a", "protocol": "stdio", "enabled": true},
		{"name": "b", "command": "./b", "protocol": "stdio", "enabled": true},
	}, nil)

	gated, changed := rt.applyConfigLoadAdmissionGate(cfg, nil, false)

	assert.False(t, changed,
		"an unreadable config.db means 'unknown', not 'first-seen' — the gate must abstain")
	for i, sc := range gated.Servers {
		assert.False(t, sc.Quarantined, "server %d must not be quarantined on a storage read failure", i)
	}
}

// Control for the test above: with storage readable and empty, the same servers
// ARE first-seen and the gate fires. Proves the abstention is keyed on the
// failure, not on the empty map.
func TestConfigLoadAdmissionGate_FiresWhenStorageReadableAndEmpty(t *testing.T) {
	rt, cfg := gateEnv(t, []map[string]any{
		{"name": "a", "command": "./a", "protocol": "stdio", "enabled": true},
	}, nil)

	gated, changed := rt.applyConfigLoadAdmissionGate(cfg, map[string]*config.ServerConfig{}, true)

	assert.True(t, changed)
	assert.True(t, gated.Servers[0].Quarantined)
}

// P2 — the gate must not write into a config snapshot that has already been
// published to subscribers (supervisor reconcile, serverEligibleForIndexing).
// server.go:1414 states the contract: the live snapshot is immutable and
// mutating it in place is a data race.
func TestConfigLoadAdmissionGate_DoesNotMutatePublishedSnapshot(t *testing.T) {
	rt, cfg := gateEnv(t, []map[string]any{
		{"name": "handwritten", "command": "./poison", "protocol": "stdio", "enabled": true},
	}, nil)

	published := rt.Config()
	require.Same(t, cfg, published)
	publishedServer := published.Servers[0]

	require.NoError(t, rt.LoadConfiguredServers(cfg))

	assert.False(t, publishedServer.Quarantined,
		"the gate must not write into ServerConfig pointers already handed to subscribers")
	assert.True(t, rt.Config().Servers[0].Quarantined,
		"the gated decision must reach subscribers by REPLACING the snapshot, not by mutating it")
	assert.NotSame(t, publishedServer, rt.Config().Servers[0],
		"the gated server must be a copy")
}

// P2 — installs already hit by #937 keep a config.db record, so the gate's
// "known means admitted" rule leaves them live. That tradeoff is deliberate
// (upgrade safety), but it must not be silent: the operator gets a named,
// actionable warning so the population the issue was filed about can be found.
func TestConfigLoadAdmissionGate_WarnsAboutServersAdmittedBeforeTheFix(t *testing.T) {
	core, logs := observer.New(zap.WarnLevel)
	rt, cfg, _ := gateEnvAt(t, []map[string]any{
		{"name": "pre-fix", "command": "./poison", "protocol": "stdio", "enabled": true},
		{"name": "reviewed", "command": "./ok", "protocol": "stdio", "enabled": true, "quarantined": false},
		{"name": "trusted", "command": "./ok", "protocol": "stdio", "enabled": true, "trust_mode": "auto"},
	}, nil, zap.New(core))

	for _, name := range []string{"pre-fix", "reviewed", "trusted"} {
		require.NoError(t, rt.storageManager.SaveUpstreamServer(&config.ServerConfig{
			Name: name, Command: "./x", Protocol: "stdio", Enabled: true, Quarantined: false,
		}))
	}

	require.NoError(t, rt.LoadConfiguredServers(cfg))

	// Upgrade safety is unchanged — nothing gets re-quarantined.
	assert.False(t, storedServer(t, rt, "pre-fix").Quarantined)

	entries := logs.FilterMessageSnippet("predate").All()
	require.Len(t, entries, 1, "exactly one advisory, listing the affected servers")
	assert.Equal(t, []interface{}{"pre-fix"}, entries[0].ContextMap()["servers"],
		"only an unreviewed server whose trust mode would gate it is reported")
}

// Sanity: the advisory is a diagnostic, not a behaviour change — a clean
// install with nothing pre-existing must not emit it.
func TestConfigLoadAdmissionGate_NoAdvisoryOnCleanInstall(t *testing.T) {
	core, logs := observer.New(zap.WarnLevel)
	rt, cfg, _ := gateEnvAt(t, []map[string]any{
		{"name": "fresh", "command": "./x", "protocol": "stdio", "enabled": true},
	}, nil, zap.New(core))

	require.NoError(t, rt.LoadConfiguredServers(cfg))

	assert.Empty(t, logs.FilterMessageSnippet("predate").All())
}

// The gate must be applied to a config BEFORE it is written to disk and before
// it is published, so the RequiresRestart branch of ApplyConfig (which returns
// without ever calling LoadConfiguredServers) cannot leave an ungated server on
// disk for the next process to trust.
func TestApplyConfig_GatesFirstSeenServerBeforeSavingToDisk(t *testing.T) {
	rt, _, path := gateEnvAt(t, []map[string]any{}, nil, zap.NewNop())

	newCfg, err := config.LoadFromFile(path)
	require.NoError(t, err)
	newCfg.Servers = append(newCfg.Servers, &config.ServerConfig{
		Name: "poison", Command: "./evil", Protocol: "stdio", Enabled: true,
	})

	_, err = rt.ApplyConfig(newCfg, path)
	require.NoError(t, err)

	saved, err := os.ReadFile(path)
	require.NoError(t, err)
	var doc struct {
		Servers []map[string]json.RawMessage `json:"mcpServers"`
	}
	require.NoError(t, json.Unmarshal(saved, &doc))
	require.Len(t, doc.Servers, 1)
	assert.Equal(t, "true", string(doc.Servers[0]["quarantined"]),
		"a first-seen server added through the API must be gated before it reaches disk")
}

// A human toggling quarantine is an operator statement about that server. It
// must land in the config document, because config.db's UpstreamRecord has no
// field for the presence bit — without this the next save erased the decision
// and the server looked never-reviewed again (which would make the advisory
// above cry wolf on every legitimately reviewed server).
func TestQuarantineServer_RecordsTheDecisionInTheConfigDocument(t *testing.T) {
	core, logs := observer.New(zap.WarnLevel)
	rt, cfg, path := gateEnvAt(t, []map[string]any{
		{"name": "reviewed", "command": "./x", "protocol": "stdio", "enabled": true},
	}, nil, zap.New(core))

	require.NoError(t, rt.LoadConfiguredServers(cfg))
	require.True(t, storedServer(t, rt, "reviewed").Quarantined, "gated on first sight")

	// The human reviews it and lets it out.
	require.NoError(t, rt.QuarantineServer("reviewed", false))

	saved, err := os.ReadFile(path)
	require.NoError(t, err)
	var doc struct {
		Servers []map[string]json.RawMessage `json:"mcpServers"`
	}
	require.NoError(t, json.Unmarshal(saved, &doc))
	require.Len(t, doc.Servers, 1)
	assert.Equal(t, "false", string(doc.Servers[0]["quarantined"]),
		"the review decision must be written to the config file, not merely to config.db")

	// A reviewed server is not part of the #937 population.
	assert.Empty(t, logs.FilterMessageSnippet("predate").All())
}
