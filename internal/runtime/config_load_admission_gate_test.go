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
)

// Issue #937 — the trust-mode admission gate must apply on the CONFIG-LOAD path,
// not only on the add paths (`upstream_servers` tool, REST, registry add).
//
// Before the fix, a server written straight into mcp_config.json was admitted
// unquarantined with its tools auto-approved, serving a poisoned tool
// description verbatim — under the DEFAULT `manual` trust mode with
// quarantine_enabled:true. `mcpproxy upstream add` of the very same server was
// correctly quarantined.
//
// These tests drive the production path: file → LoadFromFile → runtime.New →
// LoadConfiguredServers, and assert on what lands in storage (which is what the
// supervisor, the REST /servers endpoint and the tool index all read).

// gateEnv writes a config file containing the given server entries and returns a
// runtime built from it plus the parsed config.
func gateEnv(t *testing.T, servers []map[string]any, extra map[string]any) (*Runtime, *config.Config) {
	t.Helper()

	dir := t.TempDir()
	p := filepath.Join(dir, "mcp_config.json")

	raw := map[string]any{
		"listen":     "127.0.0.1:0",
		"data_dir":   dir,
		"api_key":    "k",
		"mcpServers": servers,
	}
	for k, v := range extra {
		raw[k] = v
	}

	cfgJSON, err := json.Marshal(raw)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(p, cfgJSON, 0600))

	cfg, err := config.LoadFromFile(p)
	require.NoError(t, err)

	rt, err := New(cfg, p, zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { _ = rt.Close() })

	return rt, cfg
}

func storedServer(t *testing.T, rt *Runtime, name string) *config.ServerConfig {
	t.Helper()
	stored, err := rt.storageManager.ListUpstreamServers()
	require.NoError(t, err)
	for _, s := range stored {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("server %q not found in storage", name)
	return nil
}

// The headline case from the issue: a hand-written entry with NO `quarantined`
// key and NO `trust_mode`. EffectiveTrustMode() resolves to manual, which the
// docs publish as "Quarantined for human review".
func TestConfigLoadAdmissionGate_QuarantinesFirstSeenServer(t *testing.T) {
	rt, cfg := gateEnv(t, []map[string]any{
		{"name": "handwritten", "command": "./poison", "protocol": "stdio", "enabled": true},
	}, nil)

	require.NoError(t, rt.LoadConfiguredServers(cfg))

	assert.True(t, storedServer(t, rt, "handwritten").Quarantined,
		"a first-seen config-file server under the default manual trust mode must be quarantined (issue #937)")
	assert.True(t, cfg.Servers[0].Quarantined,
		"the in-memory config must agree, or the supervisor will still connect it unquarantined")
}

// trust_mode:auto is the documented opt-out. It must keep working, or the gate
// would quarantine servers the operator deliberately trusted.
func TestConfigLoadAdmissionGate_TrustModeAutoIsNotGated(t *testing.T) {
	rt, cfg := gateEnv(t, []map[string]any{
		{"name": "trusted", "command": "./ok", "protocol": "stdio", "enabled": true, "trust_mode": "auto"},
	}, nil)

	require.NoError(t, rt.LoadConfiguredServers(cfg))

	assert.False(t, storedServer(t, rt, "trusted").Quarantined,
		"trust_mode:auto is an explicit opt-out and must not be gated")
}

// Quarantine turned off globally disables the whole feature; the gate must not
// resurrect it.
func TestConfigLoadAdmissionGate_QuarantineDisabledGlobally(t *testing.T) {
	rt, cfg := gateEnv(t, []map[string]any{
		{"name": "anything", "command": "./ok", "protocol": "stdio", "enabled": true},
	}, map[string]any{"quarantine_enabled": false})

	require.NoError(t, rt.LoadConfiguredServers(cfg))

	assert.False(t, storedServer(t, rt, "anything").Quarantined,
		"quarantine_enabled:false must disable the gate entirely")
}

// An operator who wrote `"quarantined": false` said something. The gate keys off
// ABSENCE, not off the value, so an explicit opt-out is honoured.
func TestConfigLoadAdmissionGate_ExplicitFalseIsHonoured(t *testing.T) {
	rt, cfg := gateEnv(t, []map[string]any{
		{"name": "vetted", "command": "./ok", "protocol": "stdio", "enabled": true, "quarantined": false},
	}, nil)

	require.NoError(t, rt.LoadConfiguredServers(cfg))

	assert.False(t, storedServer(t, rt, "vetted").Quarantined,
		"an explicit quarantined:false is an operator statement and must be honoured")
}

// UPGRADE SAFETY — the property that makes this shippable.
//
// A server an existing user has been running for months has an entry in
// config.db. It must not be re-quarantined just because their hand-written
// config never spelled out `quarantined`. Storage presence is the "has already
// been through admission" signal.
func TestConfigLoadAdmissionGate_KnownServerIsNotRequarantined(t *testing.T) {
	rt, cfg := gateEnv(t, []map[string]any{
		{"name": "longstanding", "command": "./ok", "protocol": "stdio", "enabled": true},
	}, nil)

	// Simulate the pre-upgrade state: the server is already known to config.db,
	// unquarantined.
	require.NoError(t, rt.storageManager.SaveUpstreamServer(&config.ServerConfig{
		Name: "longstanding", Command: "./ok", Protocol: "stdio", Enabled: true, Quarantined: false,
	}))

	require.NoError(t, rt.LoadConfiguredServers(cfg))

	assert.False(t, storedServer(t, rt, "longstanding").Quarantined,
		"a server already known to config.db has been through admission and must not be re-quarantined on upgrade")
	assert.False(t, cfg.Servers[0].Quarantined)
}

// DURABILITY — the gate decision has to survive a restart.
//
// After the first gated load, config.db says quarantined while the config file
// still says nothing. On the next start the un-stated key must inherit
// mcpproxy's own state rather than silently reverting to false — otherwise the
// hole reopens one restart later.
func TestConfigLoadAdmissionGate_QuarantineSurvivesRestart(t *testing.T) {
	rt, cfg := gateEnv(t, []map[string]any{
		{"name": "handwritten", "command": "./poison", "protocol": "stdio", "enabled": true},
	}, nil)

	// Storage remembers the gate decision from a previous run.
	require.NoError(t, rt.storageManager.SaveUpstreamServer(&config.ServerConfig{
		Name: "handwritten", Command: "./poison", Protocol: "stdio", Enabled: true, Quarantined: true,
	}))

	require.NoError(t, rt.LoadConfiguredServers(cfg))

	assert.True(t, storedServer(t, rt, "handwritten").Quarantined,
		"a quarantine recorded in config.db must not be reverted by a config file that never mentions the key")
	assert.True(t, cfg.Servers[0].Quarantined)
}

// The issue's comparison table: the same server, same binary, same
// quarantine_enabled — reached by `upstream add` versus by a hand-written
// config — must land in the same place. The add paths call
// Config.QuarantineDefaultForServer; this asserts the config-load path now
// agrees with it for every trust mode, rather than re-deriving its own answer.
func TestConfigLoadAdmissionGate_MatchesAddPathDecision(t *testing.T) {
	for _, trustMode := range []string{"", "manual", "scan", "auto"} {
		t.Run("trust_mode="+trustMode, func(t *testing.T) {
			entry := map[string]any{
				"name": "same", "command": "./x", "protocol": "stdio", "enabled": true,
			}
			if trustMode != "" {
				entry["trust_mode"] = trustMode
			}

			rt, cfg := gateEnv(t, []map[string]any{entry}, nil)

			// What `upstream add` / the REST handler would have decided.
			addPath := cfg.QuarantineDefaultForServer(&config.ServerConfig{TrustMode: trustMode})

			require.NoError(t, rt.LoadConfiguredServers(cfg))

			assert.Equal(t, addPath, storedServer(t, rt, "same").Quarantined,
				"config-load admission must reach the same verdict as the add path (issue #937)")
		})
	}
}

// The gate only ever ADDS quarantine. A config that already asks for quarantine
// (in memory or on disk) must never be relaxed by the storage fallback — every
// admission path sets the flag before it reaches here, and un-quarantining is
// the user's decision alone.
func TestConfigLoadAdmissionGate_NeverUnquarantines(t *testing.T) {
	rt, cfg := gateEnv(t, []map[string]any{
		{"name": "held", "command": "./x", "protocol": "stdio", "enabled": true, "quarantined": true},
	}, nil)

	require.NoError(t, rt.storageManager.SaveUpstreamServer(&config.ServerConfig{
		Name: "held", Command: "./x", Protocol: "stdio", Enabled: true, Quarantined: false,
	}))

	require.NoError(t, rt.LoadConfiguredServers(cfg))

	assert.True(t, storedServer(t, rt, "held").Quarantined,
		"config asking for quarantine must win over a stale unquarantined storage record")
}
