package runtime

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// TestConfigFileToolFilter_ReachesRuntime is a regression guard for the
// concern raised after spec 049: that config-file enabled_tools/disabled_tools
// on a stdio server might not reach IsToolConfigDenied at runtime.
//
// It exercises the full production path — file → LoadFromFile → runtime.New
// (ConfigService) → LoadConfiguredServers (config→storage sync) →
// SaveConfiguration (storage→ConfigService + file rewrite) — and asserts the
// filter is honored at every stage and survives the config-file rewrite.
func TestConfigFileToolFilter_ReachesRuntime(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "mcp_config.json")
	require.NoError(t, os.WriteFile(p, []byte(`{
	  "listen":"127.0.0.1:0","data_dir":"`+dir+`","api_key":"k",
	  "mcpServers":[{"name":"everything","command":"npx","args":["-y","x"],"protocol":"stdio","enabled":true,"disabled_tools":["echo"]}]
	}`), 0600))

	cfg, err := config.LoadFromFile(p)
	require.NoError(t, err)
	require.Equal(t, []string{"echo"}, cfg.Servers[0].DisabledTools, "parse boundary")

	rt, err := New(cfg, p, zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { _ = rt.Close() })

	assert.True(t, rt.IsToolConfigDenied("everything", "echo"), "after New")
	assert.False(t, rt.IsToolConfigDenied("everything", "get-tiny-image"), "non-listed tool allowed")

	// Sync config → storage (async upstream connect will fail for the fake
	// npx binary; the storage save is synchronous and is what we assert on).
	_ = rt.LoadConfiguredServers(cfg)
	time.Sleep(300 * time.Millisecond)

	stored, err := rt.storageManager.ListUpstreamServers()
	require.NoError(t, err)
	require.Len(t, stored, 1)
	assert.Equal(t, []string{"echo"}, stored[0].DisabledTools, "storage after LoadConfiguredServers")
	assert.True(t, rt.IsToolConfigDenied("everything", "echo"), "runtime after LoadConfiguredServers")

	// Round-trip through storage → ConfigService → config file.
	require.NoError(t, rt.SaveConfiguration())
	assert.True(t, rt.IsToolConfigDenied("everything", "echo"), "runtime after SaveConfiguration")

	rewritten, err := config.LoadFromFile(p)
	require.NoError(t, err)
	require.Len(t, rewritten.Servers, 1)
	assert.Equal(t, []string{"echo"}, rewritten.Servers[0].DisabledTools,
		"disabled_tools must survive the SaveConfiguration file rewrite")
}
