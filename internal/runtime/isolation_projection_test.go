package runtime

import (
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func stdioServerConfig(iso *config.IsolationConfig) *config.ServerConfig {
	return &config.ServerConfig{
		Name: "python-mcp", Protocol: "stdio", Command: "uvx",
		Args: []string{"some-mcp"}, Isolation: iso,
	}
}

// TestBuildIsolationMaps_InheritReportsIsolated is THE regression test for
// GH #1142: a server whose isolation.enabled is nil ("inherit global") while
// global isolation is ON used to be projected as enabled:false, so every UI
// showed a containerised server as running unisolated.
func TestBuildIsolationMaps_InheritReportsIsolated(t *testing.T) {
	global := &config.DockerIsolationConfig{Enabled: true}
	sc := stdioServerConfig(&config.IsolationConfig{Image: "python:3.12"})

	iso, eff := buildIsolationMaps(global, sc)
	require.NotNil(t, iso, "an stdio server must carry an isolation block")

	assert.Equal(t, true, iso["enabled"],
		"enabled is the EFFECTIVE state: an inheriting server under global isolation IS isolated")
	assert.NotContains(t, iso, "enabled_override",
		"no per-server override was set, so the raw key must be absent (= inherit)")
	assert.Equal(t, "python:3.12", iso["image"])

	require.NotNil(t, eff)
	assert.Equal(t, "docker", eff["mode"])
	assert.Equal(t, true, eff["isolated"])
	assert.Equal(t, true, eff["inherited"])
	assert.Equal(t, "docker", eff["global_mode"])
	assert.Equal(t, config.IsolationSourceGlobal, eff["source"])
}

// TestBuildIsolationMaps_ExplicitOptOut pins the other half of the tri-state:
// an explicit enabled:false must be visible as a raw override AND must resolve
// to an unisolated effective state.
func TestBuildIsolationMaps_ExplicitOptOut(t *testing.T) {
	global := &config.DockerIsolationConfig{Enabled: true}
	sc := stdioServerConfig(&config.IsolationConfig{Enabled: config.BoolPtr(false)})

	iso, eff := buildIsolationMaps(global, sc)
	require.NotNil(t, iso)

	assert.Equal(t, false, iso["enabled"])
	require.Contains(t, iso, "enabled_override")
	assert.Equal(t, false, iso["enabled_override"])

	require.NotNil(t, eff)
	assert.Equal(t, "none", eff["mode"])
	assert.Equal(t, false, eff["isolated"])
	assert.Equal(t, false, eff["inherited"])
	assert.Equal(t, config.IsolationSourceServerOptOut, eff["source"])
}

// TestBuildIsolationMaps_ExplicitOptIn keeps the raw override readable so a UI
// can render "forced on" rather than guessing from the effective state.
func TestBuildIsolationMaps_ExplicitOptIn(t *testing.T) {
	global := &config.DockerIsolationConfig{Enabled: true}
	sc := stdioServerConfig(&config.IsolationConfig{Enabled: config.BoolPtr(true)})

	iso, eff := buildIsolationMaps(global, sc)
	require.NotNil(t, iso)

	assert.Equal(t, true, iso["enabled"])
	require.Contains(t, iso, "enabled_override")
	assert.Equal(t, true, iso["enabled_override"])
	assert.Equal(t, false, eff["inherited"])
}

// TestBuildIsolationMaps_NoIsolationBlockStillReportsEffective covers the other
// half of the reported bug: a server with no `isolation` key at all produced no
// block, so a client doing `isolation?.enabled ?? false` still read "off" for a
// containerised server.
func TestBuildIsolationMaps_NoIsolationBlockStillReportsEffective(t *testing.T) {
	global := &config.DockerIsolationConfig{Enabled: true}
	sc := stdioServerConfig(nil)

	iso, eff := buildIsolationMaps(global, sc)
	require.NotNil(t, iso, "an stdio server must carry an isolation block even with no override")
	assert.Equal(t, true, iso["enabled"])
	assert.NotContains(t, iso, "enabled_override")
	require.NotNil(t, eff)
	assert.Equal(t, true, eff["inherited"])
	assert.Equal(t, "docker", eff["mode"])
}

// TestBuildIsolationMaps_ModeOverride surfaces the per-server mode (MCP-34.2),
// which had no representation anywhere on the wire.
func TestBuildIsolationMaps_ModeOverride(t *testing.T) {
	sandbox := config.IsolationModeSandbox
	global := &config.DockerIsolationConfig{Enabled: true}
	sc := stdioServerConfig(&config.IsolationConfig{Mode: &sandbox})

	iso, eff := buildIsolationMaps(global, sc)
	require.NotNil(t, iso)
	assert.Equal(t, "sandbox", iso["mode_override"])
	assert.Equal(t, "sandbox", eff["mode"], "the mode is what the spawn path branches on")

	// Whether that mode actually CONFINES depends on the host: wrapWithSandbox
	// runs the server unconfined off Linux and on kernels without Landlock, so
	// the projection must not claim isolation the spawn path will not deliver
	// (GH #1142).
	if config.SandboxEnforceable() {
		assert.Equal(t, true, iso["enabled"], "a sandbox-isolated server is isolated")
		assert.Equal(t, config.IsolationSourceServerMode, eff["source"])
	} else {
		assert.Equal(t, false, iso["enabled"], "this host cannot enforce the sandbox")
		assert.Equal(t, config.IsolationSourceSandboxUnavailable, eff["source"])
	}
}

// TestBuildIsolationMaps_HTTPServerOmitted keeps HTTP servers free of a
// meaningless isolation block.
func TestBuildIsolationMaps_HTTPServerOmitted(t *testing.T) {
	global := &config.DockerIsolationConfig{Enabled: true}
	sc := &config.ServerConfig{Name: "remote", Protocol: "http", URL: "https://example.com/mcp"}

	iso, eff := buildIsolationMaps(global, sc)
	assert.Nil(t, iso)
	assert.Nil(t, eff)
}

// TestBuildIsolationMaps_GlobalOff proves the projection is not hardcoded to
// "isolated": with the global setting off, an inheriting server reports false.
func TestBuildIsolationMaps_GlobalOff(t *testing.T) {
	global := &config.DockerIsolationConfig{Enabled: false}
	sc := stdioServerConfig(nil)

	iso, eff := buildIsolationMaps(global, sc)
	require.NotNil(t, iso)
	assert.Equal(t, false, iso["enabled"])
	assert.Equal(t, "none", eff["mode"])
	assert.Equal(t, true, eff["inherited"])
}
